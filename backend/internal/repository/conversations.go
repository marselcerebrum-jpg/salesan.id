package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/salesan/omnichannel/backend/internal/models"
)

const conversationColumns = `
	c.id, c.workspace_id, c.account_id, c.contact_id, c.chat_jid, c.type::text,
	c.name, c.avatar_url, c.status::text, c.unread_count, c.mention_count, c.marked_unread,
	c.last_message_id, c.last_message_at, c.last_message_text, c.last_message_direction,
	c.wa_conversation_at, c.is_archived, c.is_pinned, ct.phone_number, c.updated_at,
	c.pn_jid, c.group_description, c.group_owner_jid, c.self_is_admin`

// conversationOrder mirrors the WhatsApp inbox: pinned chats first, then the
// most recent evidence of activity.
//
// `greatest` rather than a plain last_message_at: a chat whose newest message
// falls outside the synced window has no last_message_at to sort on, and would
// otherwise sink to the bottom despite sitting near the top on the phone.
// updated_at is deliberately NOT part of this — it moves whenever a label or
// status changes, which would reorder the inbox for reasons that have nothing
// to do with conversation activity.
const conversationOrder = `
	order by c.is_pinned desc,
	         greatest(
	           coalesce(c.last_message_at, '-infinity'::timestamptz),
	           coalesce(c.wa_conversation_at, '-infinity'::timestamptz)
	         ) desc,
	         c.created_at desc`

func scanConversation(row interface {
	Scan(dest ...any) error
}) (*models.Conversation, error) {
	var c models.Conversation
	err := row.Scan(
		&c.ID, &c.WorkspaceID, &c.AccountID, &c.ContactID, &c.ChatJID, &c.Type,
		&c.Name, &c.AvatarURL, &c.Status, &c.UnreadCount, &c.MentionCount, &c.MarkedUnread,
		&c.LastMessageID, &c.LastMessageAt, &c.LastMessageText, &c.LastMessageDirection,
		&c.WAConversationAt, &c.IsArchived, &c.IsPinned, &c.PhoneNumber, &c.UpdatedAt,
		&c.PNJID, &c.GroupDescription, &c.GroupOwnerJID, &c.SelfIsAdmin,
	)
	if err != nil {
		return nil, err
	}
	c.Labels = []models.Label{}
	return &c, nil
}

// ListConversations returns the inbox sidebar (reference screen 6).
func (r *Repo) ListConversations(ctx context.Context, workspaceID uuid.UUID, f models.ConversationFilter) ([]models.Conversation, error) {
	args := []any{workspaceID, f.AccountID}
	where := []string{
		"c.workspace_id = $1", "c.account_id = $2", "c.is_archived = false",
		// The Status thread is never an inbox row. It has its own screen, and
		// left in here it sat among real customers carrying everybody's Status
		// as one unread count.
		"c.type <> 'status'",
	}

	if f.Type != "" {
		args = append(args, f.Type)
		where = append(where, fmt.Sprintf("c.type = $%d::public.conversation_type", len(args)))
	}
	if f.Status != "" {
		args = append(args, f.Status)
		where = append(where, fmt.Sprintf("c.status = $%d::public.conversation_status", len(args)))
	}
	if f.UnreadOnly {
		// "Belum dibaca" covers both real unread messages and chats the
		// operator flagged as unread by hand, matching WhatsApp.
		where = append(where, "(c.unread_count > 0 or c.marked_unread)")
	}
	if f.MentionsOnly {
		// Deliberately not folded into the unread filter: a mention in a group
		// that has otherwise been read through is exactly the case this exists
		// for, and it would disappear if the two were combined.
		where = append(where, "c.mention_count > 0")
	}
	if f.Search != "" {
		args = append(args, "%"+f.Search+"%")
		where = append(where, fmt.Sprintf(
			"(coalesce(c.name, '') ilike $%d or c.chat_jid ilike $%d or coalesce(c.last_message_preview, '') ilike $%d)",
			len(args), len(args), len(args)))
	}
	if f.LabelID != nil {
		args = append(args, *f.LabelID)
		where = append(where, fmt.Sprintf(
			"exists (select 1 from public.conversation_label_assignments la where la.conversation_id = c.id and la.label_id = $%d)",
			len(args)))
	}

	limit := f.Limit
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	args = append(args, limit)
	limitClause := fmt.Sprintf(" limit $%d", len(args))
	args = append(args, f.Offset)
	limitClause += fmt.Sprintf(" offset $%d", len(args))

	q := "select " + conversationColumns + `
		  from public.conversations c
		  left join public.contacts ct on ct.id = c.contact_id
		 where ` + strings.Join(where, " and ") +
		conversationOrder + limitClause

	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []models.Conversation{}
	ids := []uuid.UUID{}
	for rows.Next() {
		c, err := scanConversation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
		ids = append(ids, c.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if err := r.attachLabels(ctx, out, ids); err != nil {
		return nil, err
	}
	return out, nil
}

// attachLabels fills Conversation.Labels in one extra round trip.
func (r *Repo) attachLabels(ctx context.Context, convs []models.Conversation, ids []uuid.UUID) error {
	if len(ids) == 0 {
		return nil
	}
	// Selects the full label shape: the inbox needs account_id and wa_label_id
	// to know which account a tag belongs to, not just its name and colour.
	rows, err := r.pool.Query(ctx, `
		select la.conversation_id, l.id, l.workspace_id, l.account_id, l.wa_label_id,
		       l.name, l.color, l.color_index, l.sort_order, l.source, l.wa_updated_at
		  from public.conversation_label_assignments la
		  join public.conversation_labels l on l.id = la.label_id
		 where la.conversation_id = any($1)
		   and l.deleted_at is null
		 order by l.sort_order asc`, ids)
	if err != nil {
		return err
	}
	defer rows.Close()

	byConv := map[uuid.UUID][]models.Label{}
	for rows.Next() {
		var convID uuid.UUID
		var l models.Label
		if err := rows.Scan(
			&convID, &l.ID, &l.WorkspaceID, &l.AccountID, &l.WALabelID,
			&l.Name, &l.Color, &l.ColorIndex, &l.SortOrder, &l.Source, &l.WAUpdatedAt,
		); err != nil {
			return err
		}
		byConv[convID] = append(byConv[convID], l)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for i := range convs {
		if labels, ok := byConv[convs[i].ID]; ok {
			convs[i].Labels = labels
		}
	}
	return nil
}

// GetConversation loads a single thread with its labels.
func (r *Repo) GetConversation(ctx context.Context, workspaceID, id uuid.UUID) (*models.Conversation, error) {
	q := "select " + conversationColumns + `
		  from public.conversations c
		  left join public.contacts ct on ct.id = c.contact_id
		 where c.workspace_id = $1 and c.id = $2`
	c, err := scanConversation(r.pool.QueryRow(ctx, q, workspaceID, id))
	if err != nil {
		return nil, mapErr(err)
	}
	list := []models.Conversation{*c}
	if err := r.attachLabels(ctx, list, []uuid.UUID{c.ID}); err != nil {
		return nil, err
	}
	return &list[0], nil
}

// GetConversationByID is the unscoped variant used by the event pipeline, which
// already knows the conversation belongs to the account it is handling.
func (r *Repo) GetConversationByID(ctx context.Context, id uuid.UUID) (*models.Conversation, error) {
	q := "select " + conversationColumns + `
		  from public.conversations c
		  left join public.contacts ct on ct.id = c.contact_id
		 where c.id = $1`
	c, err := scanConversation(r.pool.QueryRow(ctx, q, id))
	if err != nil {
		return nil, mapErr(err)
	}
	list := []models.Conversation{*c}
	if err := r.attachLabels(ctx, list, []uuid.UUID{c.ID}); err != nil {
		return nil, err
	}
	return &list[0], nil
}

// LinkConversationsToContacts attaches personal chats to the matching address
// book entry and fills in any thread that still has no display name.
//
// Threads are created the moment a message arrives, which is often before the
// contact sync has run — and a message we sent ourselves carries no push name
// at all. Without this pass the inbox shows bare phone numbers for chats whose
// owner is perfectly well known.
//
// Names already set are never overwritten: a name typed here outranks whatever
// the phone's address book says.
func (r *Repo) LinkConversationsToContacts(ctx context.Context, accountID uuid.UUID) (int64, error) {
	tag, err := r.pool.Exec(ctx, `
		with matched as (
			select c.id as conversation_id,
			       ct.id as contact_id,
			       coalesce(nullif(btrim(ct.name), ''), nullif(btrim(ct.push_name), '')) as display_name
			  from public.conversations c
			  join public.contacts ct
			    on ct.account_id = c.account_id
			   and ct.jid = c.chat_jid
			 where c.account_id = $1
			   and c.type = 'personal'
		)
		update public.conversations c
		   set contact_id = m.contact_id,
		       name       = coalesce(nullif(btrim(c.name), ''), m.display_name),
		       updated_at = now()
		  from matched m
		 where c.id = m.conversation_id
		   and (c.contact_id is distinct from m.contact_id
		        or (nullif(btrim(coalesce(c.name, '')), '') is null
		            and m.display_name is not null))`, accountID)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// GetConversationByAccountChat resolves a thread by its WhatsApp identifiers.
// Used by the event pipeline, which knows the account but not our row ids.
func (r *Repo) GetConversationByAccountChat(ctx context.Context, accountID uuid.UUID, chatJID string) (*models.Conversation, error) {
	q := "select " + conversationColumns + `
		  from public.conversations c
		  left join public.contacts ct on ct.id = c.contact_id
		 where c.account_id = $1 and (c.chat_jid = $2 or c.pn_jid = $2)
		 order by (c.chat_jid = $2) desc
		 limit 1`
	c, err := scanConversation(r.pool.QueryRow(ctx, q, accountID, chatJID))
	if err != nil {
		return nil, mapErr(err)
	}
	list := []models.Conversation{*c}
	if err := r.attachLabels(ctx, list, []uuid.UUID{c.ID}); err != nil {
		return nil, err
	}
	return &list[0], nil
}

// conversationFlags whitelists the boolean columns SetConversationFlag may
// touch, so the column name can never come straight from an event.
var conversationFlags = map[string]string{
	"is_archived": "is_archived",
	"is_pinned":   "is_pinned",
}

// SetConversationFlag toggles archive or pin state.
func (r *Repo) SetConversationFlag(ctx context.Context, accountID uuid.UUID, chatJID, column string, value bool) error {
	col, ok := conversationFlags[column]
	if !ok {
		return fmt.Errorf("repository: %q is not a settable conversation flag", column)
	}
	_, err := r.pool.Exec(ctx, fmt.Sprintf(`
		update public.conversations
		   set %s = $3, updated_at = now()
		 where account_id = $1 and (chat_jid = $2 or pn_jid = $2)`, col), accountID, chatJID, value)
	return err
}

// UpsertConversationInput is what the WhatsApp event pipeline knows about a chat.
type UpsertConversationInput struct {
	WorkspaceID uuid.UUID
	AccountID   uuid.UUID
	ChatJID     string
	// PNJID is the phone-number form of the same chat, empty for groups.
	//
	// WhatsApp addresses one person two ways — by number and by LID — and which
	// one arrives varies per message. Carrying both means the second form finds
	// the thread the first one created instead of starting its own.
	PNJID     string
	Type      string
	Name      string
	ContactID *uuid.UUID
}

// UpsertConversation creates the thread if it is new and returns its ID.
// Existing names are never blanked out by an empty incoming name.
func (r *Repo) UpsertConversation(ctx context.Context, in UpsertConversationInput) (uuid.UUID, error) {
	// Two passes at most. The lookup can miss when a concurrent insert lands
	// between it and ours, in which case the unique index rejects the second
	// write and the retry finds the row the winner created.
	for attempt := 0; attempt < 2; attempt++ {
		id, found, err := r.findConversation(ctx, in)
		if err != nil {
			return uuid.Nil, err
		}
		if found {
			return id, r.enrichConversation(ctx, id, in)
		}

		id, err = r.insertConversation(ctx, in)
		if err == nil {
			return id, nil
		}
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			continue // someone else created it; look it up again
		}
		return uuid.Nil, err
	}
	return uuid.Nil, fmt.Errorf("repository: could not resolve conversation %s", in.ChatJID)
}

// findConversation resolves a thread by either of its addresses.
//
// Three ways to match, not two. A person has a number and a LID, and a thread
// may be keyed by whichever form WhatsApp used when it was first created: an
// older one-to-one chat is usually keyed by the number, a thread that arrived
// through a group is usually keyed by the LID.
//
// The third clause, `chat_jid = pn_jid`, is what was missing. Holding a LID and
// the number behind it, the lookup found neither a row keyed by that LID nor a
// row whose pn_jid was filled in, so it opened a second thread for somebody who
// already had one. The duplicate then collects its own messages and neither
// half shows the whole conversation.
func (r *Repo) findConversation(ctx context.Context, in UpsertConversationInput) (uuid.UUID, bool, error) {
	var id uuid.UUID
	err := r.pool.QueryRow(ctx, `
		select id from public.conversations
		 where account_id = $1
		   and (chat_jid = $2 or ($3 <> '' and (pn_jid = $3 or chat_jid = $3)))
		 order by (chat_jid = $2) desc, (pn_jid = $3) desc
		 limit 1`, in.AccountID, in.ChatJID, in.PNJID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, err
	}
	return id, true, nil
}

// enrichConversation fills in what the row is missing without overwriting what
// it already has. A later event that knows less must not erase what an earlier
// one knew.
func (r *Repo) enrichConversation(ctx context.Context, id uuid.UUID, in UpsertConversationInput) error {
	_, err := r.pool.Exec(ctx, `
		update public.conversations
		   set name       = coalesce(nullif($2, ''), name),
		       contact_id = coalesce($3, contact_id),
		       pn_jid     = coalesce(pn_jid, nullif($4, ''))
		 where id = $1`, id, in.Name, in.ContactID, in.PNJID)
	return err
}

func (r *Repo) insertConversation(ctx context.Context, in UpsertConversationInput) (uuid.UUID, error) {
	var id uuid.UUID
	err := r.pool.QueryRow(ctx, `
		insert into public.conversations
			(workspace_id, account_id, contact_id, chat_jid, pn_jid, type, name)
		values ($1, $2, $3, $4, nullif($5, ''), $6::public.conversation_type, nullif($7, ''))
		returning id`,
		in.WorkspaceID, in.AccountID, in.ContactID, in.ChatJID, in.PNJID, in.Type, in.Name,
	).Scan(&id)
	return id, err
}

// DeleteConversation removes a thread and everything hanging off it.
//
// Local only: WhatsApp keeps its own copy, and deleting here is the operator
// clearing their inbox, not reaching into the customer's phone. A later message
// in the same chat recreates the thread — which is exactly what the phone does
// too.
//
// Returns the storage keys of the attachments that went with it, so the caller
// can delete the files rather than orphan them in the bucket.
func (r *Repo) DeleteConversation(ctx context.Context, workspaceID, id uuid.UUID) ([]string, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Scoped to the workspace: a thread belonging to another tenant is simply
	// not found, and nothing is deleted.
	var found uuid.UUID
	err = tx.QueryRow(ctx,
		`select id from public.conversations where id = $1 and workspace_id = $2`,
		id, workspaceID).Scan(&found)
	if err != nil {
		return nil, mapErr(err)
	}

	rows, err := tx.Query(ctx, `
		select a.storage_path
		  from public.message_attachments a
		  join public.messages m on m.id = a.message_id
		 where m.conversation_id = $1 and a.storage_path is not null`, id)
	if err != nil {
		return nil, err
	}
	keys := []string{}
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			rows.Close()
			return nil, err
		}
		keys = append(keys, k)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// The head points at a message that is about to go; clear it first or the
	// foreign key holds the delete.
	if _, err := tx.Exec(ctx,
		`update public.conversations set last_message_id = null where id = $1`, id); err != nil {
		return nil, err
	}
	// Messages, attachments, labels and members all cascade from here.
	if _, err := tx.Exec(ctx, `delete from public.conversations where id = $1`, id); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return keys, nil
}

// ClearConversationMessages empties a thread but keeps it in the list.
//
// The WhatsApp equivalent of "clear chat": the conversation stays where it is,
// with no history behind it.
func (r *Repo) ClearConversationMessages(ctx context.Context, workspaceID, id uuid.UUID) ([]string, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var found uuid.UUID
	if err := tx.QueryRow(ctx,
		`select id from public.conversations where id = $1 and workspace_id = $2`,
		id, workspaceID).Scan(&found); err != nil {
		return nil, mapErr(err)
	}

	rows, err := tx.Query(ctx, `
		select a.storage_path
		  from public.message_attachments a
		  join public.messages m on m.id = a.message_id
		 where m.conversation_id = $1 and a.storage_path is not null`, id)
	if err != nil {
		return nil, err
	}
	keys := []string{}
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			rows.Close()
			return nil, err
		}
		keys = append(keys, k)
	}
	rows.Close()

	if _, err := tx.Exec(ctx, `
		update public.conversations
		   set last_message_id = null, last_message_text = null, last_message_at = null,
		       last_message_direction = null, unread_count = 0, marked_unread = false,
		       updated_at = now()
		 where id = $1`, id); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `delete from public.messages where conversation_id = $1`, id); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return keys, nil
}

// WAConversationState is the chat-level state WhatsApp reports for a thread.
// Every field is authoritative: it comes from the phone, not from counting rows.
type WAConversationState struct {
	AccountID    uuid.UUID
	ChatJID      string
	UnreadCount  int
	MarkedUnread bool
	Archived     bool
	Pinned       bool
	// ActivityAt is WhatsApp's conversation timestamp; zero means "unknown",
	// in which case the stored value is left alone.
	ActivityAt time.Time
}

// ApplyWAConversationState overwrites the local read/archive/pin state with
// WhatsApp's version.
//
// This runs *after* the messages of a sync batch are inserted, so it corrects
// any counting the insert trigger did. The unread count from the phone is the
// only trustworthy one — recomputing it from stored rows would be wrong the
// moment the synced window does not cover every unread message.
func (r *Repo) ApplyWAConversationState(ctx context.Context, s WAConversationState) error {
	var activity *time.Time
	if !s.ActivityAt.IsZero() {
		activity = &s.ActivityAt
	}

	_, err := r.pool.Exec(ctx, `
		update public.conversations
		   set unread_count       = $3,
		       marked_unread      = $4,
		       is_archived        = $5,
		       is_pinned          = $6,
		       wa_conversation_at = greatest(coalesce(wa_conversation_at, $7::timestamptz), $7::timestamptz),
		       updated_at         = now()
		 where account_id = $1 and (chat_jid = $2 or pn_jid = $2)`,
		s.AccountID, s.ChatJID, s.UnreadCount, s.MarkedUnread, s.Archived, s.Pinned, activity)
	return err
}

// SetConversationReadState handles a chat being read or un-read elsewhere —
// MarkChatAsRead from another device, or a read-self receipt.
//
// Marking read also stamps read_at on the inbound messages that were still
// unread, so the state survives a restart instead of living only in a counter.
// Both statements run in one transaction: a counter cleared without the
// messages following would report zero unread over messages that still look
// unread individually.
func (r *Repo) SetConversationReadState(ctx context.Context, accountID uuid.UUID, chatJID string, read bool) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var conversationID uuid.UUID
	err = tx.QueryRow(ctx, `
		update public.conversations
		   set unread_count  = case when $3 then 0 else unread_count end,
		       marked_unread = not $3,
		       updated_at    = now()
		 where account_id = $1 and (chat_jid = $2 or pn_jid = $2)
		returning id`, accountID, chatJID, read).Scan(&conversationID)
	if err != nil {
		return mapErr(err)
	}

	if read {
		if _, err := tx.Exec(ctx, `
			update public.messages
			   set read_at = coalesce(read_at, now())
			 where conversation_id = $1 and from_me = false and read_at is null`,
			conversationID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// ChatAnchor identifies the newest message in a chat.
//
// WhatsApp's mark-as-read mutation carries a "message range" — the chat plus
// the last message it applies up to — so the phone knows how far the flag
// reaches. Without it the mutation is rejected or silently ignored.
type ChatAnchor struct {
	ChatJID     string
	WAMessageID string
	SenderJID   string
	FromMe      bool
	Timestamp   time.Time
}

// ChatAnchorFor returns the newest message of a conversation, for building the
// message range a read-state mutation needs.
//
// A thread with no stored messages still returns its JID with a zero anchor:
// WhatsApp accepts the mutation with an empty range, which is the right
// behaviour for a chat whose history sits outside the synced window.
func (r *Repo) ChatAnchorFor(ctx context.Context, workspaceID, conversationID uuid.UUID) (*ChatAnchor, error) {
	var a ChatAnchor
	var waID, senderJID *string
	var fromMe *bool
	var ts *time.Time

	err := r.pool.QueryRow(ctx, `
		select c.chat_jid, m.wa_message_id, m.sender_jid, m.from_me, m.timestamp
		  from public.conversations c
		  left join public.messages m on m.id = c.last_message_id
		 where c.workspace_id = $1 and c.id = $2`,
		workspaceID, conversationID,
	).Scan(&a.ChatJID, &waID, &senderJID, &fromMe, &ts)
	if err != nil {
		return nil, mapErr(err)
	}

	if waID != nil {
		a.WAMessageID = *waID
	}
	if senderJID != nil {
		a.SenderJID = *senderJID
	}
	if fromMe != nil {
		a.FromMe = *fromMe
	}
	if ts != nil {
		a.Timestamp = *ts
	}
	return &a, nil
}

// SetMarkedUnread raises or clears the manual "unread" flag.
//
// This is WhatsApp's own distinction: a chat can be flagged unread with nothing
// actually unread in it, which is why the flag lives apart from unread_count.
// Raising it never invents a count; clearing it never touches one either —
// opening the thread is what zeroes the counter.
func (r *Repo) SetMarkedUnread(ctx context.Context, workspaceID, id uuid.UUID, marked bool) (*models.Conversation, error) {
	tag, err := r.pool.Exec(ctx, `
		update public.conversations
		   set marked_unread = $3, updated_at = now()
		 where workspace_id = $1 and id = $2`, workspaceID, id, marked)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrNotFound
	}
	return r.GetConversation(ctx, workspaceID, id)
}

// RefreshConversationHead recomputes a conversation's preview and ordering
// stamp from its newest message.
//
// The message trigger does this automatically, except during a history
// backfill where doing it per row would mean thousands of redundant
// recomputations. Callers of the bulk insert path invoke it once instead.
func (r *Repo) RefreshConversationHead(ctx context.Context, conversationID uuid.UUID) error {
	_, err := r.pool.Exec(ctx,
		`select public.refresh_conversation_head($1)`, conversationID)
	return err
}

// SetConversationStatus moves a thread between Baru / Diproses / Selesai.
func (r *Repo) SetConversationStatus(ctx context.Context, workspaceID, id uuid.UUID, status string) (*models.Conversation, error) {
	tag, err := r.pool.Exec(ctx, `
		update public.conversations
		   set status = $3::public.conversation_status
		 where workspace_id = $1 and id = $2`, workspaceID, id, status)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrNotFound
	}
	return r.GetConversation(ctx, workspaceID, id)
}

// MarkConversationRead zeroes the unread counter and stamps read_at on the
// inbound messages that were still unread.
//
// Both happen in one transaction because the reconciliation that runs after a
// reconnect rebuilds counters by counting messages with no read_at: clearing
// the counter without stamping the messages would make that pass resurrect the
// badge the operator just dismissed.
func (r *Repo) MarkConversationRead(ctx context.Context, workspaceID, id uuid.UUID) (*models.Conversation, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx, `
		update public.conversations
		   set unread_count = 0, marked_unread = false, updated_at = now()
		 where workspace_id = $1 and id = $2`, workspaceID, id)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrNotFound
	}

	if _, err := tx.Exec(ctx, `
		update public.messages
		   set read_at = coalesce(read_at, now())
		 where conversation_id = $1 and from_me = false and read_at is null`, id); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return r.GetConversation(ctx, workspaceID, id)
}

// ConversationCounts backs the filter chips ("Belum dibaca (44)", status tabs).
type ConversationCounts struct {
	All        int `json:"all"`
	Personal   int `json:"personal"`
	Group      int `json:"group"`
	Unread     int `json:"unread"`
	New        int `json:"new"`
	InProgress int `json:"in_progress"`
	Done       int `json:"done"`
}

// CountConversations computes every chip count in a single pass.
func (r *Repo) CountConversations(ctx context.Context, workspaceID, accountID uuid.UUID) (*ConversationCounts, error) {
	var c ConversationCounts
	err := r.pool.QueryRow(ctx, `
		select count(*),
		       count(*) filter (where type = 'personal'),
		       count(*) filter (where type = 'group'),
		       count(*) filter (where unread_count > 0 or marked_unread),
		       count(*) filter (where status = 'new'),
		       count(*) filter (where status = 'in_progress'),
		       count(*) filter (where status = 'done')
		  from public.conversations
		 where workspace_id = $1 and account_id = $2 and is_archived = false
		   -- Same exclusion as the list these chips sit above: a count that
		   -- includes a row the list will not show is a count nobody can check.
		   and type <> 'status'`,
		workspaceID, accountID,
	).Scan(&c.All, &c.Personal, &c.Group, &c.Unread, &c.New, &c.InProgress, &c.Done)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// UpsertGroupMembers replaces the participant list of a group conversation.
func (r *Repo) UpsertGroupMembers(ctx context.Context, conversationID uuid.UUID, members []GroupMember) error {
	if len(members) == 0 {
		return nil
	}
	batchJIDs := make([]string, 0, len(members))
	for _, m := range members {
		batchJIDs = append(batchJIDs, m.JID)
		if _, err := r.pool.Exec(ctx, `
			insert into public.conversation_members
			       (conversation_id, jid, phone_number, display_name, is_admin)
			values ($1, $2, nullif($3, ''), nullif($4, ''), $5)
			on conflict (conversation_id, jid) do update
			   set phone_number = coalesce(nullif(excluded.phone_number, ''), public.conversation_members.phone_number),
			       display_name = coalesce(nullif(excluded.display_name, ''), public.conversation_members.display_name),
			       is_admin     = excluded.is_admin`,
			conversationID, m.JID, m.PhoneNumber, m.DisplayName, m.IsAdmin); err != nil {
			return err
		}
	}
	_, err := r.pool.Exec(ctx,
		`delete from public.conversation_members where conversation_id = $1 and jid <> all($2)`,
		conversationID, batchJIDs)
	return err
}

// MarkJoinedGroups records which groups this account is actually a member of.
//
// `joined` is the complete list from GetJoinedGroups, so everything absent from
// it is a group we are not in — one we left, or were removed from, whose thread
// history sync imported anyway. Call it only when that call succeeded: an empty
// list from a failed fetch would mark every group as gone.
//
// Returns how many rows changed, which is what the caller reports as "N grup
// yang tidak Anda ikuti disembunyikan".
func (r *Repo) MarkJoinedGroups(
	ctx context.Context, accountID uuid.UUID, joined []string,
) (int64, error) {
	if joined == nil {
		joined = []string{}
	}
	tag, err := r.pool.Exec(ctx, `
		update public.conversations
		   set group_is_member = (chat_jid = any($2)),
		       updated_at      = now()
		 where account_id = $1
		   and type = 'group'
		   and group_is_member is distinct from (chat_jid = any($2))`,
		accountID, joined)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// GroupMember is one participant of a WhatsApp group.
type GroupMember struct {
	JID string
	// PhoneNumber is what WhatsApp says the participant's number is, bare and
	// without a server suffix. Empty when WhatsApp will not say, which happens
	// for anonymous participants in announcement groups.
	//
	// Kept because JID is now a LID for almost everyone, and a LID is not a
	// number: it cannot be dialled, messaged from another device, or exported.
	PhoneNumber string
	DisplayName string
	IsAdmin     bool
}
