package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/salesan/omnichannel/backend/internal/models"
)

const messageColumns = `
	m.id, m.conversation_id, m.account_id, m.wa_message_id, m.sender_jid, m.sender_name,
	m.from_me, m.type::text, m.body, m.caption, m.media_url, m.media_mime,
	m.quoted_message_id, m.status::text, m.error_message, m.delivered_at, m.read_at,
	m.edited_at, m.revoked_at, m.timestamp, m.created_at, m.sent_by,
	m.participant_jid, m.sender_phone, coalesce(m.mentioned_jids, '{}'), m.mentions_me,
	m.mention_seen_at`

// senderIdentity resolves who wrote a message, joined at read time rather than
// frozen into the row.
//
// The order is the one a person expects: the name they saved for this contact
// first, then whatever the contact currently calls themselves, then the name
// that came with the message, and only then the bare number. Resolving it here
// rather than at write time is what makes a rename on the phone show up on
// messages that arrived last week — freezing the name would leave the old one
// there for ever.
//
// `display_name` is the finished answer; `sender_phone` is kept alongside it so
// the interface can show the number as well when it has one.
const senderIdentity = `
	coalesce(
	  nullif(btrim(ct.name), ''),
	  nullif(btrim(ct.push_name), ''),
	  nullif(btrim(m.sender_name), ''),
	  nullif(m.sender_phone, ''),
	  nullif(split_part(coalesce(m.participant_jid, m.sender_jid, ''), '@', 1), '')
	) as display_name`

// senderJoin attaches the contact behind a message, matched on either of the
// two addresses WhatsApp may have used for them.
const senderJoin = `
	left join public.contacts ct
	       on ct.account_id = m.account_id
	      and ct.jid = coalesce(m.participant_jid, m.sender_jid)`

// messageColumnsWithSender adds the resolved display name; pair it with
// senderJoin.
const messageColumnsWithSender = messageColumns + ",\n" + senderIdentity

type scannable interface{ Scan(dest ...any) error }

// messageDest lists the destinations for messageColumns, in order. Kept in one
// place so the column list and the scan cannot drift apart.
func messageDest(m *models.Message) []any {
	return []any{
		&m.ID, &m.ConversationID, &m.AccountID, &m.WAMessageID, &m.SenderJID, &m.SenderName,
		&m.FromMe, &m.Type, &m.Body, &m.Caption, &m.MediaURL, &m.MediaMime,
		&m.QuotedMessageID, &m.Status, &m.ErrorMessage, &m.DeliveredAt, &m.ReadAt,
		&m.EditedAt, &m.RevokedAt, &m.Timestamp, &m.CreatedAt, &m.SentBy,
		&m.ParticipantJID, &m.SenderPhone, &m.MentionedJIDs, &m.MentionsMe,
		&m.MentionSeenAt,
	}
}

func scanMessage(row scannable) (*models.Message, error) {
	var m models.Message
	if err := row.Scan(messageDest(&m)...); err != nil {
		return nil, err
	}
	// Never null on the wire: the frontend maps over these unconditionally.
	m.Attachments = []models.Attachment{}
	m.Reactions = []models.Reaction{}
	if m.MentionedJIDs == nil {
		m.MentionedJIDs = []string{}
	}
	return &m, nil
}

// scanMessageWithSender reads a row selected with messageColumnsWithSender.
func scanMessageWithSender(row scannable) (*models.Message, error) {
	var m models.Message
	dest := append(messageDest(&m), &m.DisplayName)
	if err := row.Scan(dest...); err != nil {
		return nil, err
	}
	m.Attachments = []models.Attachment{}
	if m.MentionedJIDs == nil {
		m.MentionedJIDs = []string{}
	}
	return &m, nil
}

// InsertMessageInput describes one message to persist.
type InsertMessageInput struct {
	WorkspaceID     uuid.UUID
	AccountID       uuid.UUID
	ConversationID  uuid.UUID
	WAMessageID     string
	SenderJID       *string
	SenderName      *string
	FromMe          bool
	Type            string
	Body            *string
	Caption         *string
	MediaURL        *string
	MediaMime       *string
	QuotedMessageID *string
	Status          string
	Timestamp       time.Time
	SentBy          *uuid.UUID
	// ParticipantJID and SenderPhone identify the group member who wrote this.
	// Empty for a one-to-one chat, where the sender is the chat itself.
	ParticipantJID *string
	SenderPhone    *string
	// MentionedJIDs is WhatsApp's own list of who the message names.
	MentionedJIDs []string
	// MentionsMe is that list resolved against the receiving account. Decided
	// once, on the way in, because the answer differs per account and a group
	// may hold several of ours.
	MentionsMe bool
	// ClientToken makes an outbound send idempotent across retries. Nil for
	// anything arriving from WhatsApp, which is already keyed by wa_message_id.
	ClientToken *string
	// ImportBatchID marks a message as having arrived through a history sync
	// rather than through a live conversation. It is the evidence that keeps a
	// seven-day backfill from being counted as a week of new leads.
	ImportBatchID *uuid.UUID
	// SenderSource overrides what the attribution trigger would infer. Left
	// empty for ordinary chat, where the trigger's rule is the right one; set to
	// "broadcast" or "story" by the campaign executor, which is what keeps a
	// campaign's outgoing messages out of Pesan Terkirim, Kontak Terlayani, SLA
	// and follow-up. The trigger only fills the column when it is null, so an
	// explicit value here wins.
	SenderSource string
	// CampaignID links the message to the Story or Broadcast that produced it,
	// or — on an inbound message — to the campaign the customer is answering.
	CampaignID *uuid.UUID
}

// ErrDuplicateClientToken reports that a send with this token was already
// accepted. The caller answers with the message that already exists instead of
// sending a second copy.
var ErrDuplicateClientToken = errors.New("repository: duplicate client token")

// InsertMessage writes a message idempotently.
//
// (account_id, wa_message_id) is the idempotency key, so a message replayed by
// a reconnect or a history sync is silently ignored. The second return value
// reports whether a new row was actually created — callers use it to decide
// whether to broadcast a realtime event.
func (r *Repo) InsertMessage(ctx context.Context, in InsertMessageInput) (*models.Message, bool, error) {
	if in.Status == "" {
		in.Status = models.MessageStatusSent
	}
	if in.Type == "" {
		in.Type = "text"
	}
	if in.Timestamp.IsZero() {
		in.Timestamp = time.Now().UTC()
	}

	// Wrapped in a CTE aliased `m` so it can reuse messageColumns, which is
	// written in terms of `m.`.
	const q = `with m as (
		insert into public.messages
			(workspace_id, account_id, conversation_id, wa_message_id, sender_jid, sender_name,
			 from_me, type, body, caption, media_url, media_mime, quoted_message_id,
			 status, timestamp, sent_by, client_token,
			 participant_jid, sender_phone, mentioned_jids, mentions_me,
			 sender_source, campaign_id, import_batch_id)
		values ($1, $2, $3, $4, $5, $6, $7, $8::public.message_type, $9, $10, $11, $12, $13,
		        $14::public.message_status, $15, $16, $17, $18, $19, $20, $21,
		        nullif($22, '')::public.sender_source, $23, $24)
		on conflict (account_id, wa_message_id) do nothing
		returning *
	) select ` + messageColumns + " from m"

	msg, err := scanMessage(r.pool.QueryRow(ctx, q,
		in.WorkspaceID, in.AccountID, in.ConversationID, in.WAMessageID, in.SenderJID, in.SenderName,
		in.FromMe, in.Type, in.Body, in.Caption, in.MediaURL, in.MediaMime, in.QuotedMessageID,
		in.Status, in.Timestamp, in.SentBy, in.ClientToken,
		in.ParticipantJID, in.SenderPhone, in.MentionedJIDs, in.MentionsMe,
		// Written here as well as in the backfill path. Without it a message that
		// arrived through a history import but was inserted one at a time lost
		// the only evidence that it was imported, and its contact was classified
		// as a new lead.
		in.SenderSource, in.CampaignID, in.ImportBatchID))
	if err == nil {
		return msg, true, nil
	}

	// The client-token index fires before the row exists, which is the point:
	// the duplicate is stopped here, ahead of the network call.
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" &&
		pgErr.ConstraintName == "uq_messages_client_token" {
		return nil, false, ErrDuplicateClientToken
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, false, err
	}

	// Conflict: the message already exists. Return the stored row.
	existing, err := r.GetMessageByWAID(ctx, in.AccountID, in.WAMessageID)
	if err != nil {
		return nil, false, err
	}
	return existing, false, nil
}

// InsertMessagesBackfill writes a batch of historical messages in one
// transaction and reports how many rows were genuinely new.
//
// Two things make this different from calling InsertMessage in a loop:
//
//   - `salesan.backfill` is set for the transaction, which tells the conversation
//     trigger not to increment unread_count. During a history sync the correct
//     unread figure comes from WhatsApp itself and is applied afterwards by
//     ApplyWAConversationState; counting inserted rows would inflate it on every
//     re-sync.
//   - One round trip per batch instead of per message, which matters when the
//     phone dumps a thousand messages at once.
func (r *Repo) InsertMessagesBackfill(ctx context.Context, batch []InsertMessageInput) (int, error) {
	if len(batch) == 0 {
		return 0, nil
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `select set_config('salesan.backfill', 'true', true)`); err != nil {
		return 0, fmt.Errorf("set backfill flag: %w", err)
	}

	// Inbound history is stamped as read at its own timestamp. History carries
	// no receipts, and the authoritative unread figure arrives separately from
	// WhatsApp right after this batch — so leaving these unstamped would make
	// the reconciliation pass treat months of settled conversation as unread.
	// Historical mentions are stamped as already seen. The unread figure comes
	// from WhatsApp itself right after this batch, and a mention from four days
	// ago that the operator has long since read on their phone must not raise a
	// badge here — the same reason inbound history is stamped read.
	const q = `
		insert into public.messages
			(workspace_id, account_id, conversation_id, wa_message_id, sender_jid, sender_name,
			 from_me, type, body, caption, media_url, media_mime, quoted_message_id,
			 status, timestamp, sent_by, read_at,
			 participant_jid, sender_phone, mentioned_jids, mentions_me, mention_seen_at,
			 import_batch_id)
		values ($1, $2, $3, $4, $5, $6, $7, $8::public.message_type, $9, $10, $11, $12, $13,
		        $14::public.message_status, $15::timestamptz, $16,
		        -- Cast in the CASE branches too. Their only other branch is NULL,
		        -- so a bare $15 is deduced as text there and as timestamptz in the
		        -- timestamp column, and Postgres rejects the whole statement with
		        -- 42P08 — which silently broke every history-sync batch.
		        case when $7 then null else $15::timestamptz end,
		        $17, $18, $19, $20,
		        case when $20 then $15::timestamptz else null end,
		        $21)
		on conflict (account_id, wa_message_id) do nothing`

	inserted := 0
	for _, in := range batch {
		if in.Status == "" {
			in.Status = models.MessageStatusDelivered
		}
		if in.Type == "" {
			in.Type = "text"
		}
		if in.Timestamp.IsZero() {
			in.Timestamp = time.Now().UTC()
		}

		tag, err := tx.Exec(ctx, q,
			in.WorkspaceID, in.AccountID, in.ConversationID, in.WAMessageID, in.SenderJID, in.SenderName,
			in.FromMe, in.Type, in.Body, in.Caption, in.MediaURL, in.MediaMime, in.QuotedMessageID,
			in.Status, in.Timestamp, in.SentBy,
			in.ParticipantJID, in.SenderPhone, in.MentionedJIDs, in.MentionsMe,
			in.ImportBatchID)
		if err != nil {
			return inserted, fmt.Errorf("insert %s: %w", in.WAMessageID, err)
		}
		inserted += int(tag.RowsAffected())
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return inserted, nil
}

// MarkMessagesRead stamps read_at on inbound messages named by a read receipt.
//
// Resolution is by (account_id, wa_message_id), deliberately not by chat JID.
// WhatsApp addresses the same chat as a phone number in one place and as a LID
// in another, so a receipt's Chat field routinely fails to match the JID a
// conversation was stored under. Message ids carry no such ambiguity, and they
// identify the conversation just as precisely — including in groups, where the
// participant who read the message is irrelevant to which thread it belongs to.
//
// Returns the rows that changed plus the conversations they belong to.
func (r *Repo) MarkMessagesRead(
	ctx context.Context,
	accountID uuid.UUID,
	waIDs []string,
	at time.Time,
) ([]MessageStatusChange, []uuid.UUID, error) {
	if len(waIDs) == 0 {
		return nil, nil, nil
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}

	rows, err := r.pool.Query(ctx, `
		update public.messages m
		   set read_at      = coalesce(m.read_at, $3::timestamptz),
		       delivered_at = coalesce(m.delivered_at, $3::timestamptz)
		 where m.account_id = $1
		   and m.wa_message_id = any($2)
		   and m.from_me = false
		   and m.read_at is null
		returning m.id, m.conversation_id, m.wa_message_id, m.status::text`,
		accountID, waIDs, at)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	changes := []MessageStatusChange{}
	seen := map[uuid.UUID]struct{}{}
	convIDs := []uuid.UUID{}
	for rows.Next() {
		var c MessageStatusChange
		if err := rows.Scan(&c.ID, &c.ConversationID, &c.WAMessageID, &c.Status); err != nil {
			return nil, nil, err
		}
		changes = append(changes, c)
		if _, ok := seen[c.ConversationID]; !ok {
			seen[c.ConversationID] = struct{}{}
			convIDs = append(convIDs, c.ConversationID)
		}
	}
	return changes, convIDs, rows.Err()
}

// MessageIDsByWAIDs maps WhatsApp message ids onto our row ids in one query.
//
// Used after a backfill batch, which reports only how many rows it inserted:
// attaching media to those rows needs their ids, and asking for them one at a
// time would undo the point of batching.
func (r *Repo) MessageIDsByWAIDs(ctx context.Context, accountID uuid.UUID, waIDs []string) (map[string]uuid.UUID, error) {
	out := map[string]uuid.UUID{}
	if len(waIDs) == 0 {
		return out, nil
	}
	rows, err := r.pool.Query(ctx, `
		select wa_message_id, id from public.messages
		 where account_id = $1 and wa_message_id = any($2)`, accountID, waIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var waID string
		var id uuid.UUID
		if err := rows.Scan(&waID, &id); err != nil {
			return nil, err
		}
		out[waID] = id
	}
	return out, rows.Err()
}

// ConversationIDsForWAMessages resolves which conversations a set of WhatsApp
// message ids belongs to, without going through the chat JID.
func (r *Repo) ConversationIDsForWAMessages(ctx context.Context, accountID uuid.UUID, waIDs []string) ([]uuid.UUID, error) {
	if len(waIDs) == 0 {
		return nil, nil
	}
	rows, err := r.pool.Query(ctx, `
		select distinct conversation_id
		  from public.messages
		 where account_id = $1 and wa_message_id = any($2)`, accountID, waIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []uuid.UUID{}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// RecomputeUnread rebuilds the unread counter for specific conversations from
// their messages.
//
// Used after a read receipt: counting inbound messages that still have no
// read_at is exact, where simply zeroing the counter would also clear messages
// the receipt did not cover. Backfilled history is stamped read on insert, so
// only genuinely live-received messages can be counted here.
func (r *Repo) RecomputeUnread(ctx context.Context, conversationIDs []uuid.UUID) ([]uuid.UUID, error) {
	if len(conversationIDs) == 0 {
		return nil, nil
	}
	rows, err := r.pool.Query(ctx, `
		update public.conversations c
		   set unread_count = sub.n,
		       updated_at   = now()
		  from (
		    select c2.id,
		           count(m.id) filter (where m.from_me = false and m.read_at is null) as n
		      from public.conversations c2
		      left join public.messages m on m.conversation_id = c2.id
		     where c2.id = any($1)
		     group by c2.id
		  ) sub
		 where c.id = sub.id
		   and c.unread_count is distinct from sub.n
		returning c.id`, conversationIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	changed := []uuid.UUID{}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		changed = append(changed, id)
	}
	return changed, rows.Err()
}

// ClaimReceiptEvent records that a receipt has been applied and reports whether
// this call is the one that claimed it.
//
// WhatsApp resends receipts freely — on reconnect, during offline sync, and
// once per linked device. Claiming each by key keeps a repeat from being
// processed twice.
func (r *Repo) ClaimReceiptEvent(ctx context.Context, accountID uuid.UUID, key string) (bool, error) {
	tag, err := r.pool.Exec(ctx, `
		insert into public.whatsapp_receipt_events (account_id, event_key)
		values ($1, $2) on conflict do nothing`, accountID, key)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// PruneReceiptEvents drops idempotency records older than the retention window.
func (r *Repo) PruneReceiptEvents(ctx context.Context, olderThan time.Duration) error {
	_, err := r.pool.Exec(ctx,
		`delete from public.whatsapp_receipt_events where applied_at < now() - $1::interval`,
		olderThan.String())
	return err
}

// ReconcileUnreadCounts lowers unread counters to match what the stored
// messages can actually justify.
//
// Run after a reconnect: receipts that arrived while the socket was down are
// lost, so a counter can be left standing over messages that were read on the
// phone long ago.
//
// It only ever lowers. Raising would be wrong: WhatsApp's own unread figure —
// applied from history sync — is authoritative, while our read_at stamps only
// cover messages we personally saw a receipt for. Backfilled history carries no
// receipts at all, so counting un-stamped messages as unread would invent a
// badge over conversations the operator read on their phone months ago.
func (r *Repo) ReconcileUnreadCounts(ctx context.Context, accountID uuid.UUID) (int64, error) {
	tag, err := r.pool.Exec(ctx, `
		update public.conversations c
		   set unread_count = sub.n,
		       updated_at   = now()
		  from (
		    select c2.id, count(m.id) filter (where m.from_me = false and m.read_at is null) as n
		      from public.conversations c2
		      left join public.messages m on m.conversation_id = c2.id
		     where c2.account_id = $1
		     group by c2.id
		  ) sub
		 where c.id = sub.id
		   and c.marked_unread = false
		   and sub.n < c.unread_count`, accountID)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// PruneMessagesOlderThan deletes stored messages that fall outside the sync
// window. Conversations are kept: their ordering stamp comes from WhatsApp, so
// a chat with no messages left in the window still appears in the right place.
func (r *Repo) PruneMessagesOlderThan(ctx context.Context, accountID uuid.UUID, cutoff time.Time) (int64, error) {
	tag, err := r.pool.Exec(ctx,
		`delete from public.messages where account_id = $1 and timestamp < $2`, accountID, cutoff)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// GetMessageByClientToken returns the message a previous send with this token
// produced, with its attachments loaded. Used to answer a retry.
func (r *Repo) GetMessageByClientToken(ctx context.Context, accountID uuid.UUID, token string) (*models.Message, error) {
	q := "select " + messageColumns + `
		  from public.messages m
		 where m.account_id = $1 and m.client_token = $2`
	m, err := scanMessage(r.pool.QueryRow(ctx, q, accountID, token))
	if err != nil {
		return nil, mapErr(err)
	}
	if list, err := r.AttachmentsForMessage(ctx, m.ID); err == nil {
		m.Attachments = list
	}
	page := []models.Message{*m}
	if err := r.attachPolls(ctx, page); err == nil {
		m.Poll = page[0].Poll
	}
	if err := r.attachQuotes(ctx, page); err == nil {
		m.Quoted = page[0].Quoted
	}
	if err := r.attachReactions(ctx, page); err == nil {
		m.Reactions = page[0].Reactions
	}
	return m, nil
}

// GetMessageByID loads one message, scoped to a workspace, with attachments.
func (r *Repo) GetMessageByID(ctx context.Context, workspaceID, id uuid.UUID) (*models.Message, error) {
	q := "select " + messageColumnsWithSender + `
		  from public.messages m` + senderJoin + `
		 where m.id = $1 and m.workspace_id = $2`
	m, err := scanMessageWithSender(r.pool.QueryRow(ctx, q, id, workspaceID))
	if err != nil {
		return nil, mapErr(err)
	}
	if list, err := r.AttachmentsForMessage(ctx, m.ID); err == nil {
		m.Attachments = list
	}
	page := []models.Message{*m}
	if err := r.attachPolls(ctx, page); err == nil {
		m.Poll = page[0].Poll
	}
	if err := r.attachQuotes(ctx, page); err == nil {
		m.Quoted = page[0].Quoted
	}
	if err := r.attachReactions(ctx, page); err == nil {
		m.Reactions = page[0].Reactions
	}
	return m, nil
}

// GetMessageByWAID looks a message up by its WhatsApp ID.
func (r *Repo) GetMessageByWAID(ctx context.Context, accountID uuid.UUID, waID string) (*models.Message, error) {
	q := "select " + messageColumns + `
		  from public.messages m
		 where m.account_id = $1 and m.wa_message_id = $2`
	m, err := scanMessage(r.pool.QueryRow(ctx, q, accountID, waID))
	if err != nil {
		return nil, mapErr(err)
	}
	return m, nil
}

// ListMessagesInput pages a thread backwards from `Before`.
type ListMessagesInput struct {
	ConversationID uuid.UUID
	Limit          int
	Before         *time.Time
}

// ListMessages returns a page of messages in ascending chronological order,
// which is the order the chat panel renders them in.
func (r *Repo) ListMessages(ctx context.Context, workspaceID uuid.UUID, in ListMessagesInput) ([]models.Message, error) {
	limit := in.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}

	// hidden_at filters out messages deleted for this side only. They are still
	// on the phone; they are simply not part of this inbox any more.
	q := `select * from (select ` + messageColumnsWithSender + `
		  from public.messages m` + senderJoin + `
		 where m.workspace_id = $1
		   and m.conversation_id = $2
		   and m.hidden_at is null
		   and ($3::timestamptz is null or m.timestamp < $3::timestamptz)
		 order by m.timestamp desc, m.created_at desc
		 limit $4) page order by page.timestamp asc, page.created_at asc`

	rows, err := r.pool.Query(ctx, q, workspaceID, in.ConversationID, in.Before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []models.Message{}
	ids := []uuid.UUID{}
	for rows.Next() {
		m, err := scanMessageWithSender(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *m)
		ids = append(ids, m.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// One extra query for the whole page rather than one per message.
	byMessage, err := r.AttachmentsForMessages(ctx, ids)
	if err != nil {
		return nil, err
	}
	for i := range out {
		if list := byMessage[out[i].ID]; len(list) > 0 {
			out[i].Attachments = list
		}
	}

	if err := r.attachPolls(ctx, out); err != nil {
		return nil, err
	}
	if err := r.attachQuotes(ctx, out); err != nil {
		return nil, err
	}
	if err := r.attachReactions(ctx, out); err != nil {
		return nil, err
	}
	return out, nil
}

// attachReactions fills in the reactions on a page of messages.
func (r *Repo) attachReactions(ctx context.Context, page []models.Message) error {
	if len(page) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, 0, len(page))
	for _, m := range page {
		ids = append(ids, m.ID)
	}

	byMessage, err := r.ReactionsForMessages(ctx, ids)
	if err != nil {
		return err
	}
	for i := range page {
		if list := byMessage[page[i].ID]; len(list) > 0 {
			page[i].Reactions = list
		}
	}
	return nil
}

// attachQuotes fills in the preview of whatever each reply is answering.
//
// One query for the page, not one per reply. A quote whose original has aged
// out of the window simply stays unresolved — the reply still renders, with the
// quote shown as unavailable rather than the whole message failing to load.
func (r *Repo) attachQuotes(ctx context.Context, page []models.Message) error {
	waIDs := []string{}
	var accountID uuid.UUID
	for _, m := range page {
		if m.QuotedMessageID != nil && *m.QuotedMessageID != "" {
			waIDs = append(waIDs, *m.QuotedMessageID)
			accountID = m.AccountID
		}
	}
	if len(waIDs) == 0 {
		return nil
	}

	// Found by WhatsApp's own message id, across every number in this
	// workspace, rather than only under the number being read.
	//
	// One group is followed by several of our numbers and its messages are
	// stored once per number. A reply read through number A whose quoted
	// message happens to be stored under number B was declared missing, and the
	// bubble above it read as an unsupported message. Counted on production
	// data: 916 quotes failed to resolve in seven days, 144 of which were in
	// the database the whole time under another number.
	//
	// `distinct on` keeps the reader's own copy when there is one, so a quote
	// that can be opened still points at the row in the thread they are looking
	// at. The workspace join is the boundary: a message id is unique to
	// WhatsApp, but nothing outside this workspace may be reached through it.
	rows, err := r.pool.Query(ctx, `
		select distinct on (m.wa_message_id)
		       m.wa_message_id, m.id, m.account_id, m.sender_name, m.from_me, m.type::text,
		       coalesce(nullif(m.body,''), nullif(m.caption,''), ''),
		       m.revoked_at,
		       (select a.thumbnail_b64 from public.message_attachments a
		         where a.message_id = m.id order by a.idx limit 1)
		  from public.messages m
		  join public.whatsapp_accounts acc on acc.id = m.account_id
		 where m.wa_message_id = any($2)
		   and acc.workspace_id = (
		     select workspace_id from public.whatsapp_accounts where id = $1)
		 order by m.wa_message_id, (m.account_id = $1) desc, m.timestamp`,
		accountID, waIDs)
	if err != nil {
		return err
	}
	defer rows.Close()

	found := map[string]models.QuotedMessage{}
	for rows.Next() {
		var q models.QuotedMessage
		var id, owner uuid.UUID
		var revokedAt *time.Time
		if err := rows.Scan(&q.WAMessageID, &id, &owner, &q.SenderName, &q.FromMe, &q.Type,
			&q.Text, &revokedAt, &q.Thumbnail); err != nil {
			return err
		}
		if revokedAt != nil {
			q.Text = "Pesan ini dihapus"
			q.Thumbnail = nil
		}
		// The id is an offer to jump to the original, so it is only given when
		// the original is in this number's own thread. Borrowing another
		// number's copy would send the reader to a conversation they were not
		// reading, which is worse than not offering the jump at all.
		if owner == accountID {
			q.ID = &id
		}
		found[q.WAMessageID] = q
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for i := range page {
		ref := page[i].QuotedMessageID
		if ref == nil || *ref == "" {
			continue
		}
		if q, ok := found[*ref]; ok {
			quoted := q
			page[i].Quoted = &quoted
		} else {
			// Known to be a reply, but the original is out of reach.
			page[i].Quoted = &models.QuotedMessage{
				WAMessageID: *ref,
				Type:        "unsupported",
				Text:        "Pesan tidak tersedia",
			}
		}
	}
	return nil
}

// attachPolls fills in the poll on every poll message in a page.
//
// Skipped entirely when the page holds no polls, which is the common case —
// the extra queries only happen where they can produce something.
func (r *Repo) attachPolls(ctx context.Context, page []models.Message) error {
	pollIDs := []uuid.UUID{}
	var accountID uuid.UUID
	for _, m := range page {
		if m.Type == "poll" {
			pollIDs = append(pollIDs, m.ID)
			accountID = m.AccountID
		}
	}
	if len(pollIDs) == 0 {
		return nil
	}

	ownJIDs, err := r.AccountIdentities(ctx, accountID)
	if err != nil {
		return err
	}

	polls, err := r.PollsForMessages(ctx, pollIDs, ownJIDs)
	if err != nil {
		return err
	}
	for i := range page {
		if poll := polls[page[i].ID]; poll != nil {
			page[i].Poll = poll
		}
	}
	return nil
}

// AccountIdentities returns every address an account answers to, normalised
// without the device number.
//
// There are two, because WhatsApp addresses the same account by phone number in
// some chats and by LID in others. Anything asking "was this me?" has to check
// both — a single address gets the answer wrong for roughly half of the threads
// on this workspace.
func (r *Repo) AccountIdentities(ctx context.Context, accountID uuid.UUID) ([]string, error) {
	var jid, lid *string
	err := r.pool.QueryRow(ctx,
		`select jid, lid from public.whatsapp_accounts where id = $1`, accountID).Scan(&jid, &lid)
	if err != nil {
		return nil, mapErr(err)
	}

	out := []string{}
	for _, v := range []*string{jid, lid} {
		if v == nil || *v == "" {
			continue
		}
		if n := NormalizeJID(*v); n != "" {
			out = append(out, n)
		}
	}
	return out, nil
}

// SetAccountLID records the account's LID, learned from whatsmeow on connect.
func (r *Repo) SetAccountLID(ctx context.Context, accountID uuid.UUID, lid string) error {
	_, err := r.pool.Exec(ctx, `
		update public.whatsapp_accounts
		   set lid = $2, updated_at = now()
		 where id = $1 and coalesce(lid, '') is distinct from $2`, accountID, lid)
	return err
}

// MessageStatusChange reports one row touched by a receipt.
type MessageStatusChange struct {
	ID             uuid.UUID `json:"id"`
	ConversationID uuid.UUID `json:"conversation_id"`
	WAMessageID    string    `json:"wa_message_id"`
	Status         string    `json:"status"`
}

// AdvanceMessageStatus applies a delivery/read receipt to a batch of messages,
// stamping the moment WhatsApp reported it.
func (r *Repo) AdvanceMessageStatus(
	ctx context.Context,
	accountID uuid.UUID,
	waIDs []string,
	status string,
	at time.Time,
) ([]MessageStatusChange, error) {
	if len(waIDs) == 0 {
		return nil, nil
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}

	// Stamps delivered_at / read_at alongside the status so the milestone
	// survives a restart, and never moves a message backwards: a delivery
	// receipt arriving after a read receipt must not un-read the message.
	const q = `
		update public.messages m
		   set status       = $3::public.message_status,
		       delivered_at = case
		         when $3::text in ('delivered', 'read') then coalesce(m.delivered_at, $4::timestamptz)
		         else m.delivered_at end,
		       read_at      = case
		         when $3::text = 'read' then coalesce(m.read_at, $4::timestamptz)
		         else m.read_at end
		 where m.account_id = $1
		   and m.wa_message_id = any($2)
		   and (case m.status::text
		          when 'pending' then 0 when 'failed' then 0 when 'sent' then 1
		          when 'delivered' then 2 when 'read' then 3 else 0 end)
		     < (case $3::text
		          when 'pending' then 0 when 'failed' then 0 when 'sent' then 1
		          when 'delivered' then 2 when 'read' then 3 else 0 end)
		returning m.id, m.conversation_id, m.wa_message_id, m.status::text`

	rows, err := r.pool.Query(ctx, q, accountID, waIDs, status, at)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []MessageStatusChange{}
	for rows.Next() {
		var c MessageStatusChange
		if err := rows.Scan(&c.ID, &c.ConversationID, &c.WAMessageID, &c.Status); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// SetMessageContent updates the text of a message that has not gone out yet.
//
// Used when a failed send is retried with a caption the operator changed in the
// meantime: WhatsApp is about to receive the new text, so the stored row has to
// carry it too.
func (r *Repo) SetMessageContent(ctx context.Context, id uuid.UUID, body, caption *string) (*models.Message, error) {
	q := `with m as (
		update public.messages
		   set body = $2, caption = $3
		 where id = $1
		returning *
	) select ` + messageColumns + " from m"
	msg, err := scanMessage(r.pool.QueryRow(ctx, q, id, body, caption))
	if err != nil {
		return nil, mapErr(err)
	}
	if list, err := r.AttachmentsForMessage(ctx, msg.ID); err == nil {
		msg.Attachments = list
	}
	return msg, nil
}

// SetMessageOutcome records the result of an outbound send attempt.
func (r *Repo) SetMessageOutcome(ctx context.Context, id uuid.UUID, status string, ts *time.Time, errMsg *string) (*models.Message, error) {
	q := `with m as (
		update public.messages
		   set status        = $2::public.message_status,
		       timestamp     = coalesce($3::timestamptz, timestamp),
		       error_message = $4
		 where id = $1
		returning *
	) select ` + messageColumns + " from m"
	msg, err := scanMessage(r.pool.QueryRow(ctx, q, id, status, ts, errMsg))
	if err != nil {
		return nil, mapErr(err)
	}
	return msg, nil
}

// RecentIncomingWAIDs returns the newest inbound WhatsApp message IDs in a
// thread plus the JID of their most recent sender, which is what MarkRead needs.
func (r *Repo) RecentIncomingWAIDs(ctx context.Context, conversationID uuid.UUID, limit int) ([]string, string, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := r.pool.Query(ctx, `
		select wa_message_id, coalesce(sender_jid, '')
		  from public.messages
		 where conversation_id = $1 and from_me = false
		 order by timestamp desc
		 limit $2`, conversationID, limit)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()

	var ids []string
	var sender string
	for rows.Next() {
		var id, s string
		if err := rows.Scan(&id, &s); err != nil {
			return nil, "", err
		}
		if sender == "" {
			sender = s
		}
		ids = append(ids, id)
	}
	return ids, sender, rows.Err()
}

// MessageRef is the minimum a history-sync request needs to page backwards
// from a message we already hold.
type MessageRef struct {
	WAMessageID string
	ChatJID     string
	SenderJID   string
	FromMe      bool
	Timestamp   time.Time
}

// OldestMessage returns the earliest stored message for an account, or nil when
// there are none. Used to decide whether the sync window is already covered.
func (r *Repo) OldestMessage(ctx context.Context, accountID uuid.UUID) (*MessageRef, error) {
	var ref MessageRef
	err := r.pool.QueryRow(ctx, `
		select m.wa_message_id, c.chat_jid, coalesce(m.sender_jid, ''), m.from_me, m.timestamp
		  from public.messages m
		  join public.conversations c on c.id = m.conversation_id
		 where m.account_id = $1
		 order by m.timestamp asc
		 limit 1`, accountID,
	).Scan(&ref.WAMessageID, &ref.ChatJID, &ref.SenderJID, &ref.FromMe, &ref.Timestamp)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &ref, nil
}

// NewestMessage returns the most recent stored message for an account, or nil
// when there are none.
//
// The anchor for a forced history repair: paging back from the newest message
// makes the phone re-deliver the recent stretch, which is where a gap left by
// lost messages actually sits.
func (r *Repo) NewestMessage(ctx context.Context, accountID uuid.UUID) (*MessageRef, error) {
	var ref MessageRef
	err := r.pool.QueryRow(ctx, `
		select m.wa_message_id, c.chat_jid, coalesce(m.sender_jid, ''), m.from_me, m.timestamp
		  from public.messages m
		  join public.conversations c on c.id = m.conversation_id
		 where m.account_id = $1
		 order by m.timestamp desc
		 limit 1`, accountID,
	).Scan(&ref.WAMessageID, &ref.ChatJID, &ref.SenderJID, &ref.FromMe, &ref.Timestamp)
	if err != nil {
		if isNoRows(err) {
			return nil, nil
		}
		return nil, err
	}
	return &ref, nil
}

// LatestMessageTime returns the newest message timestamp for an account, used
// to decide how far back a history sync needs to go.
func (r *Repo) LatestMessageTime(ctx context.Context, accountID uuid.UUID) (*time.Time, error) {
	var ts *time.Time
	err := r.pool.QueryRow(ctx,
		`select max(timestamp) from public.messages where account_id = $1`, accountID).Scan(&ts)
	if err != nil {
		return nil, err
	}
	return ts, nil
}
