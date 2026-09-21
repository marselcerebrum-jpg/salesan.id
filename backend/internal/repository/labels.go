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

const labelColumns = `
	id, workspace_id, account_id, wa_label_id, name, color, color_index,
	sort_order, source, wa_updated_at`

func scanLabel(row interface {
	Scan(dest ...any) error
}) (*models.Label, error) {
	var l models.Label
	err := row.Scan(
		&l.ID, &l.WorkspaceID, &l.AccountID, &l.WALabelID, &l.Name, &l.Color,
		&l.ColorIndex, &l.SortOrder, &l.Source, &l.WAUpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &l, nil
}

// ListLabels returns the workspace's tags.
//
// When accountID is set, the result is that account's WhatsApp labels plus any
// manual tags not tied to an account. Labels belong to one account precisely so
// that two linked numbers with a same-named label stay independent.
func (r *Repo) ListLabels(ctx context.Context, workspaceID uuid.UUID, accountID *uuid.UUID) ([]models.Label, error) {
	rows, err := r.pool.Query(ctx, `
		select `+labelColumns+`
		  from public.conversation_labels
		 where workspace_id = $1
		   and deleted_at is null
		   and ($2::uuid is null or account_id = $2::uuid or account_id is null)
		 order by account_id nulls first, sort_order asc, name asc`,
		workspaceID, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []models.Label{}
	for rows.Next() {
		l, err := scanLabel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *l)
	}
	return out, rows.Err()
}

// GetLabel loads one label scoped to the workspace.
func (r *Repo) GetLabel(ctx context.Context, workspaceID, id uuid.UUID) (*models.Label, error) {
	l, err := scanLabel(r.pool.QueryRow(ctx,
		`select `+labelColumns+`
		   from public.conversation_labels
		  where workspace_id = $1 and id = $2 and deleted_at is null`, workspaceID, id))
	if err != nil {
		return nil, mapErr(err)
	}
	return l, nil
}

// GetLabelByWAID resolves a label by its WhatsApp identity.
func (r *Repo) GetLabelByWAID(ctx context.Context, accountID uuid.UUID, waLabelID string) (*models.Label, error) {
	l, err := scanLabel(r.pool.QueryRow(ctx,
		`select `+labelColumns+`
		   from public.conversation_labels
		  where account_id = $1 and wa_label_id = $2`, accountID, waLabelID))
	if err != nil {
		return nil, mapErr(err)
	}
	return l, nil
}

// CreateLabelInput describes a tag created in this app.
type CreateLabelInput struct {
	AccountID  *uuid.UUID
	WALabelID  *string
	Name       string
	Color      string
	ColorIndex *int
}

// CreateLabel adds a tag.
func (r *Repo) CreateLabel(ctx context.Context, workspaceID uuid.UUID, in CreateLabelInput) (*models.Label, error) {
	source := "manual"
	if in.WALabelID != nil {
		source = "whatsapp"
	}
	if in.Color == "" {
		in.Color = "#B45309"
	}

	l, err := scanLabel(r.pool.QueryRow(ctx, `
		insert into public.conversation_labels
			(workspace_id, account_id, wa_label_id, name, color, color_index, sort_order, source)
		values ($1, $2, $3, $4, $5, $6,
		        (select coalesce(max(sort_order), 0) + 1
		           from public.conversation_labels where workspace_id = $1),
		        $7)
		returning `+labelColumns,
		workspaceID, in.AccountID, in.WALabelID, in.Name, in.Color, in.ColorIndex, source))
	if err != nil {
		return nil, mapErr(err)
	}
	return l, nil
}

// UpdateLabelInput carries partial edits.
type UpdateLabelInput struct {
	Name       *string
	Color      *string
	ColorIndex *int
}

// UpdateLabel renames or recolours a tag.
func (r *Repo) UpdateLabel(ctx context.Context, workspaceID, id uuid.UUID, in UpdateLabelInput) (*models.Label, error) {
	l, err := scanLabel(r.pool.QueryRow(ctx, `
		update public.conversation_labels
		   set name        = coalesce($3, name),
		       color       = coalesce($4, color),
		       color_index = coalesce($5, color_index),
		       updated_at  = now()
		 where workspace_id = $1 and id = $2 and deleted_at is null
		returning `+labelColumns,
		workspaceID, id, in.Name, in.Color, in.ColorIndex))
	if err != nil {
		return nil, mapErr(err)
	}
	return l, nil
}

// DeleteLabel tombstones a tag and drops its assignments.
//
// The row is kept rather than removed so a late-arriving app-state mutation
// carrying the old definition cannot resurrect it.
func (r *Repo) DeleteLabel(ctx context.Context, workspaceID, id uuid.UUID) error {
	tag, err := r.pool.Exec(ctx, `
		update public.conversation_labels
		   set deleted_at = now(), updated_at = now()
		 where workspace_id = $1 and id = $2 and deleted_at is null`, workspaceID, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	_, err = r.pool.Exec(ctx,
		`delete from public.conversation_label_assignments where label_id = $1`, id)
	return err
}

// --- mirroring from WhatsApp -------------------------------------------------

// UpsertWALabelInput is one label definition as WhatsApp describes it.
type UpsertWALabelInput struct {
	WorkspaceID uuid.UUID
	AccountID   uuid.UUID
	WALabelID   string
	Name        string
	Color       string
	ColorIndex  int
	Deleted     bool
	// Timestamp of the app-state mutation. Older mutations are ignored, which
	// is what makes replayed history and out-of-order snapshots harmless.
	Timestamp time.Time
}

// UpsertWALabel applies a label definition coming from the phone.
//
// Identity is (account_id, wa_label_id) — never the name, because renaming a
// label on the phone keeps its id and would otherwise look like a new tag.
// A mutation older than the one already stored is discarded, so the newest
// change from WhatsApp always wins.
func (r *Repo) UpsertWALabel(ctx context.Context, in UpsertWALabelInput) (*models.Label, bool, error) {
	if in.Deleted {
		tag, err := r.pool.Exec(ctx, `
			update public.conversation_labels
			   set deleted_at    = now(),
			       wa_updated_at = greatest(coalesce(wa_updated_at, $3), $3),
			       updated_at    = now()
			 where account_id = $1 and wa_label_id = $2
			   and (wa_updated_at is null or wa_updated_at <= $3)`,
			in.AccountID, in.WALabelID, in.Timestamp)
		if err != nil {
			return nil, false, err
		}
		if tag.RowsAffected() > 0 {
			_, _ = r.pool.Exec(ctx, `
				delete from public.conversation_label_assignments a
				 using public.conversation_labels l
				 where a.label_id = l.id
				   and l.account_id = $1 and l.wa_label_id = $2`, in.AccountID, in.WALabelID)
		}
		return nil, tag.RowsAffected() > 0, nil
	}

	l, err := scanLabel(r.pool.QueryRow(ctx, `
		insert into public.conversation_labels
			(workspace_id, account_id, wa_label_id, name, color, color_index,
			 sort_order, source, wa_updated_at)
		values ($1, $2, $3, $4, $5, $6,
		        (select coalesce(max(sort_order), 0) + 1
		           from public.conversation_labels where workspace_id = $1),
		        'whatsapp', $7)
		on conflict (account_id, wa_label_id) where wa_label_id is not null
		do update set
			name          = excluded.name,
			color         = excluded.color,
			color_index   = excluded.color_index,
			deleted_at    = null,
			wa_updated_at = excluded.wa_updated_at,
			updated_at    = now()
		  where public.conversation_labels.wa_updated_at is null
		     or public.conversation_labels.wa_updated_at <= excluded.wa_updated_at
		returning `+labelColumns,
		in.WorkspaceID, in.AccountID, in.WALabelID, in.Name, in.Color, in.ColorIndex, in.Timestamp))

	if err != nil {
		// No row returned means the stored mutation is newer; that is a
		// successful no-op, not a failure.
		if errors.Is(err, pgx.ErrNoRows) {
			existing, getErr := r.GetLabelByWAID(ctx, in.AccountID, in.WALabelID)
			if getErr != nil {
				return nil, false, getErr
			}
			return existing, false, nil
		}
		return nil, false, err
	}
	return l, true, nil
}

// AdoptWALabelID stamps a WhatsApp identity onto a label created in this app,
// after it has been pushed to the phone for the first time.
func (r *Repo) AdoptWALabelID(ctx context.Context, labelID, accountID uuid.UUID, waLabelID string, colorIndex int) error {
	_, err := r.pool.Exec(ctx, `
		update public.conversation_labels
		   set account_id  = $2,
		       wa_label_id = $3,
		       color_index = $4,
		       source      = 'whatsapp',
		       updated_at  = now()
		 where id = $1`, labelID, accountID, waLabelID, colorIndex)
	return err
}

// NextWALabelID picks an id no label on this account is using.
//
// WhatsApp identifies labels by small integers and ships around twenty built-in
// ones, so allocation starts above that range to avoid colliding with a
// built-in label the phone has not told us about yet.
func (r *Repo) NextWALabelID(ctx context.Context, accountID uuid.UUID) (string, error) {
	var next int
	err := r.pool.QueryRow(ctx, `
		select greatest(coalesce(max(wa_label_id::int), 0) + 1, 100)
		  from public.conversation_labels
		 where account_id = $1 and wa_label_id ~ '^[0-9]+$'`, accountID).Scan(&next)
	if err != nil {
		return "", err
	}
	return itoa(next), nil
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// --- assignments -------------------------------------------------------------

// AssignLabel tags a conversation, verifying both sides belong to the workspace.
func (r *Repo) AssignLabel(ctx context.Context, workspaceID, conversationID, labelID, userID uuid.UUID) error {
	tag, err := r.pool.Exec(ctx, `
		insert into public.conversation_label_assignments (conversation_id, label_id, assigned_by)
		select $2, $3, $4
		 where exists (select 1 from public.conversations where id = $2 and workspace_id = $1)
		   and exists (select 1 from public.conversation_labels
		                where id = $3 and workspace_id = $1 and deleted_at is null)
		on conflict do nothing`, workspaceID, conversationID, labelID, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		var ok bool
		if err := r.pool.QueryRow(ctx, `
			select exists (select 1 from public.conversations where id = $2 and workspace_id = $1)
			   and exists (select 1 from public.conversation_labels
			                where id = $3 and workspace_id = $1 and deleted_at is null)`,
			workspaceID, conversationID, labelID).Scan(&ok); err != nil {
			return err
		}
		if !ok {
			return ErrNotFound
		}
		// Already tagged. Nothing changed, so nothing is recorded: an audit log
		// that fills with no-ops stops being readable.
		return nil
	}

	return r.recordAssignmentChange(ctx, conversationID, labelID,
		LabelEventAssigned, ChangeSourceWeb, &userID)
}

// UnassignLabel removes a tag from a conversation.
func (r *Repo) UnassignLabel(ctx context.Context, workspaceID, conversationID, labelID, userID uuid.UUID) error {
	tag, err := r.pool.Exec(ctx, `
		delete from public.conversation_label_assignments a
		 where a.conversation_id = $2
		   and a.label_id = $3
		   and exists (select 1 from public.conversations c
		                where c.id = a.conversation_id and c.workspace_id = $1)`,
		workspaceID, conversationID, labelID)
	if err != nil || tag.RowsAffected() == 0 {
		return err
	}
	return r.recordAssignmentChange(ctx, conversationID, labelID,
		LabelEventRemoved, ChangeSourceWeb, &userID)
}

// recordAssignmentChange appends one assign/remove entry to the label history.
//
// Called only after the assignment table actually changed, so the history holds
// changes rather than attempts. A failure to record is reported to the caller
// but does not undo the tag itself: losing an audit line is bad, silently
// refusing to label a chat because of it is worse.
func (r *Repo) recordAssignmentChange(
	ctx context.Context,
	conversationID, labelID uuid.UUID,
	eventType, source string,
	adminID *uuid.UUID,
) error {
	var accountID uuid.UUID
	if err := r.pool.QueryRow(ctx,
		`select account_id from public.conversations where id = $1`,
		conversationID).Scan(&accountID); err != nil {
		return mapErr(err)
	}

	at := time.Now().UTC()
	in := LabelEventInput{
		AccountID:      accountID,
		ConversationID: &conversationID,
		EventType:      eventType,
		Source:         source,
		AdminID:        adminID,
		OccurredAt:     at,
		EventKey: fmt.Sprintf("%s:%s:%s:%d",
			eventType, conversationID, labelID, at.UnixMilli()),
	}
	if eventType == LabelEventRemoved {
		in.FromLabelID = &labelID
	} else {
		in.ToLabelID = &labelID
	}

	_, err := r.RecordLabelEvent(ctx, in)
	return err
}

// SetChatLabelByWAID applies an association coming from the phone, resolving
// both sides through their WhatsApp identifiers.
//
// Returns false when the chat or the label is not known yet — associations and
// definitions arrive in whatever order the snapshot holds them.
func (r *Repo) SetChatLabelByWAID(
	ctx context.Context,
	accountID uuid.UUID,
	chatJID, waLabelID string,
	labeled bool,
) (bool, error) {
	var conversationID, labelID uuid.UUID
	err := r.pool.QueryRow(ctx, `
		select c.id, l.id
		  from public.conversations c
		  join public.conversation_labels l
		    on l.account_id = c.account_id
		   and l.wa_label_id = $3
		   and l.deleted_at is null
		 where c.account_id = $1 and (c.chat_jid = $2 or c.pn_jid = $2)`,
		accountID, chatJID, waLabelID).Scan(&conversationID, &labelID)
	if err != nil {
		if mapErr(err) == ErrNotFound {
			return false, nil
		}
		return false, err
	}

	var tag pgconn.CommandTag
	eventType := LabelEventRemoved
	if labeled {
		eventType = LabelEventAssigned
		tag, err = r.pool.Exec(ctx, `
			insert into public.conversation_label_assignments (conversation_id, label_id)
			values ($1, $2) on conflict do nothing`, conversationID, labelID)
	} else {
		tag, err = r.pool.Exec(ctx, `
			delete from public.conversation_label_assignments
			 where conversation_id = $1 and label_id = $2`, conversationID, labelID)
	}
	if err != nil {
		return false, err
	}

	// Only a real change is written to the history. The phone re-sends its whole
	// label state on every reconnect, so recording attempts rather than changes
	// would fill the audit log with the same day repeated.
	//
	// admin_id stays null: this came from the phone, and WhatsApp does not say
	// who was holding it. Attributing it to whoever happens to be on shift is
	// exactly the guess the attribution rules forbid.
	if tag.RowsAffected() > 0 {
		if err := r.recordAssignmentChange(ctx, conversationID, labelID,
			eventType, ChangeSourceWhatsApp, nil); err != nil {
			return true, err
		}
	}
	return true, nil
}

// CountWhatsAppLabels reports how many live labels an account mirrors.
func (r *Repo) CountWhatsAppLabels(ctx context.Context, accountID uuid.UUID) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx, `
		select count(*) from public.conversation_labels
		 where account_id = $1 and deleted_at is null`, accountID).Scan(&n)
	return n, err
}

// --- idempotency -------------------------------------------------------------

// ClaimLabelEvent records that a mutation has been applied and reports whether
// this call is the one that claimed it.
//
// App-state mutations are replayed freely — on reconnect, on snapshot recovery,
// and as an echo of changes this app itself pushed. Claiming each one by key
// keeps those replays from being processed twice and stops a change from
// bouncing between the two sides.
func (r *Repo) ClaimLabelEvent(ctx context.Context, accountID uuid.UUID, key string) (bool, error) {
	tag, err := r.pool.Exec(ctx, `
		insert into public.whatsapp_label_events (account_id, event_key)
		values ($1, $2) on conflict do nothing`, accountID, key)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// PruneLabelEvents drops idempotency records older than the retention window.
func (r *Repo) PruneLabelEvents(ctx context.Context, olderThan time.Duration) error {
	_, err := r.pool.Exec(ctx,
		`delete from public.whatsapp_label_events where applied_at < now() - $1::interval`,
		olderThan.String())
	return err
}

// --- per-account sync state --------------------------------------------------

// SetLabelSyncState records the label sync status shown in the UI.
func (r *Repo) SetLabelSyncState(ctx context.Context, accountID uuid.UUID, state, detail string) error {
	var detailPtr *string
	if detail != "" {
		detailPtr = &detail
	}
	_, err := r.pool.Exec(ctx, `
		update public.whatsapp_accounts
		   set label_sync_state = $2,
		       label_sync_error = $3,
		       labels_synced_at = case when $2 = 'synced' then now() else labels_synced_at end
		 where id = $1`, accountID, state, detailPtr)
	return err
}
