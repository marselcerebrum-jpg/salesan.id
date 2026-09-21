package repository

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/salesan/omnichannel/backend/internal/models"
)

// EditMessageContent replaces the text of a message that has already been sent.
//
// Keyed on the WhatsApp id rather than ours, because the same function serves
// an edit made here and one arriving from the phone. A revoked message is never
// edited: WhatsApp lets a deletion stand.
//
// Returns nil when nothing matched, which is the normal outcome for an edit of
// a message that fell outside the sync window.
func (r *Repo) EditMessageContent(
	ctx context.Context,
	accountID uuid.UUID,
	waMessageID string,
	body, caption *string,
	at time.Time,
) (*models.Message, error) {
	if at.IsZero() {
		at = time.Now().UTC()
	}

	q := `with m as (
		update public.messages
		   set body      = $3,
		       caption   = $4,
		       edited_at = $5
		 where account_id = $1
		   and wa_message_id = $2
		   and revoked_at is null
		returning *
	) select ` + messageColumns + " from m"

	msg, err := scanMessage(r.pool.QueryRow(ctx, q, accountID, waMessageID, body, caption, at))
	if err != nil {
		if isNoRows(err) {
			return nil, nil
		}
		return nil, err
	}
	if list, err := r.AttachmentsForMessage(ctx, msg.ID); err == nil {
		msg.Attachments = list
	}
	return msg, nil
}

// RevokeMessage marks a message deleted for everyone.
//
// The row survives on purpose. WhatsApp leaves a placeholder in the thread
// rather than closing the gap, and so does this — a message that silently
// vanishes leaves the reader wondering what they missed. What does go is the
// content: body, caption and any attachment file.
//
// Returns the message plus the storage keys of its attachments, so the caller
// can delete the files too.
func (r *Repo) RevokeMessage(
	ctx context.Context,
	accountID uuid.UUID,
	waMessageID string,
	at time.Time,
) (*models.Message, []string, error) {
	if at.IsZero() {
		at = time.Now().UTC()
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var messageID uuid.UUID
	err = tx.QueryRow(ctx, `
		select id from public.messages
		 where account_id = $1 and wa_message_id = $2`, accountID, waMessageID).Scan(&messageID)
	if err != nil {
		if isNoRows(err) {
			return nil, nil, nil
		}
		return nil, nil, err
	}

	rows, err := tx.Query(ctx, `
		select storage_path from public.message_attachments
		 where message_id = $1 and storage_path is not null`, messageID)
	if err != nil {
		return nil, nil, err
	}
	keys := []string{}
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			rows.Close()
			return nil, nil, err
		}
		keys = append(keys, k)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	// The attachment rows go entirely: there is nothing left to show, and
	// keeping them would leave a bubble asking for a file that was deleted.
	if _, err := tx.Exec(ctx,
		`delete from public.message_attachments where message_id = $1`, messageID); err != nil {
		return nil, nil, err
	}

	q := `with m as (
		update public.messages
		   set revoked_at = coalesce(revoked_at, $2::timestamptz),
		       body       = null,
		       caption    = null,
		       media_url  = null
		 where id = $1
		returning *
	) select ` + messageColumns + " from m"

	msg, err := scanMessage(tx.QueryRow(ctx, q, messageID, at))
	if err != nil {
		return nil, nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, nil, err
	}
	return msg, keys, nil
}

// HideMessage removes one message from this inbox only.
//
// Scoped to the workspace, which is the access check. Nothing is sent to
// WhatsApp by this call — the caller decides separately whether to tell the
// phone, and the message stays intact there and for the other party either way.
//
// Returns the conversation the message belonged to plus its attachment keys.
func (r *Repo) HideMessage(ctx context.Context, workspaceID, messageID uuid.UUID) (uuid.UUID, []string, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var conversationID uuid.UUID
	err = tx.QueryRow(ctx, `
		update public.messages
		   set hidden_at = coalesce(hidden_at, now())
		 where id = $1 and workspace_id = $2
		returning conversation_id`, messageID, workspaceID).Scan(&conversationID)
	if err != nil {
		return uuid.Nil, nil, mapErr(err)
	}

	rows, err := tx.Query(ctx, `
		select storage_path from public.message_attachments
		 where message_id = $1 and storage_path is not null`, messageID)
	if err != nil {
		return uuid.Nil, nil, err
	}
	keys := []string{}
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			rows.Close()
			return uuid.Nil, nil, err
		}
		keys = append(keys, k)
	}
	rows.Close()

	if _, err := tx.Exec(ctx,
		`delete from public.message_attachments where message_id = $1`, messageID); err != nil {
		return uuid.Nil, nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, nil, err
	}
	return conversationID, keys, nil
}

// MessageTarget is what the WhatsApp layer needs to address a message on the
// wire: which chat it is in and who sent it.
type MessageTarget struct {
	MessageID      uuid.UUID
	ConversationID uuid.UUID
	AccountID      uuid.UUID
	WAMessageID    string
	ChatJID        string
	SenderJID      string
	FromMe         bool
	Type           string
	Body           *string
	Caption        *string
	RevokedAt      *time.Time
	Timestamp      time.Time
	HasAttachment  bool
}

// MessageTargetByID loads the addressing details for one message, scoped to a
// workspace.
func (r *Repo) MessageTargetByID(ctx context.Context, workspaceID, messageID uuid.UUID) (*MessageTarget, error) {
	var t MessageTarget
	err := r.pool.QueryRow(ctx, `
		select m.id, m.conversation_id, m.account_id, m.wa_message_id, c.chat_jid,
		       coalesce(m.sender_jid, ''), m.from_me, m.type::text, m.body, m.caption,
		       m.revoked_at, m.timestamp,
		       exists (select 1 from public.message_attachments a where a.message_id = m.id)
		  from public.messages m
		  join public.conversations c on c.id = m.conversation_id
		 where m.id = $1 and m.workspace_id = $2`, messageID, workspaceID,
	).Scan(&t.MessageID, &t.ConversationID, &t.AccountID, &t.WAMessageID, &t.ChatJID,
		&t.SenderJID, &t.FromMe, &t.Type, &t.Body, &t.Caption, &t.RevokedAt, &t.Timestamp,
		&t.HasAttachment)
	if err != nil {
		return nil, mapErr(err)
	}
	return &t, nil
}

// ConversationIDForWAMessage resolves which thread a WhatsApp message id
// belongs to, for edits and revokes arriving from the phone.
func (r *Repo) ConversationIDForWAMessage(ctx context.Context, accountID uuid.UUID, waMessageID string) (uuid.UUID, error) {
	var id uuid.UUID
	err := r.pool.QueryRow(ctx, `
		select conversation_id from public.messages
		 where account_id = $1 and wa_message_id = $2`, accountID, waMessageID).Scan(&id)
	if err != nil {
		return uuid.Nil, mapErr(err)
	}
	return id, nil
}
