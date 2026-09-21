package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Label event types, mirroring public.label_event_type.
const (
	LabelEventCreated  = "label_created"
	LabelEventUpdated  = "label_updated"
	LabelEventDeleted  = "label_deleted"
	LabelEventAssigned = "label_assigned"
	LabelEventRemoved  = "label_removed"
	LabelEventMoved    = "label_moved"
)

// Change sources, mirroring public.change_source.
const (
	ChangeSourceWeb      = "web"
	ChangeSourceWhatsApp = "whatsapp"
	ChangeSourceSystem   = "system"
)

// TransitionWindow is how close a removal and an assignment on the same contact
// have to be to read as one move rather than two separate decisions.
//
// A move is not recorded as its own event, because neither the web path nor the
// phone's app-state tells us "this replaced that" — both send a removal and an
// addition. Inventing a third event from them would mean the same action was
// counted twice in "total perubahan label". Instead the pair is recognised when
// the report is built, from the two real events. Two minutes is generous enough
// for a batch of app-state mutations and far too short for two deliberate,
// unrelated edits.
const TransitionWindow = 2 * time.Minute

// LabelEventInput is one change to record.
type LabelEventInput struct {
	AccountID      uuid.UUID
	ConversationID *uuid.UUID
	EventType      string
	FromLabelID    *uuid.UUID
	ToLabelID      *uuid.UUID
	Source         string
	// AdminID is null for anything coming from the phone: WhatsApp does not say
	// who was holding it, and a guess here would attribute somebody else's work.
	AdminID    *uuid.UUID
	OccurredAt time.Time
	// EventKey makes the write idempotent. A replayed app-state mutation, a
	// retried request, or a reconnect that re-delivers the same change must
	// leave one row, not several.
	EventKey string
}

// RecordLabelEvent appends one entry to the label history and refreshes the
// contact's current label state.
//
// Returns false when the event was already recorded, which callers use to avoid
// broadcasting a change that nothing actually changed.
func (r *Repo) RecordLabelEvent(ctx context.Context, in LabelEventInput) (bool, error) {
	if in.OccurredAt.IsZero() {
		in.OccurredAt = time.Now().UTC()
	}
	if in.EventKey == "" {
		return false, fmt.Errorf("record label event: event key is required for idempotency")
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var (
		workspaceID   uuid.UUID
		applicationID *uuid.UUID
		contactID     *uuid.UUID
	)
	if err := tx.QueryRow(ctx,
		`select a.workspace_id, a.application_id from public.whatsapp_accounts a where a.id = $1`,
		in.AccountID).Scan(&workspaceID, &applicationID); err != nil {
		return false, mapErr(err)
	}
	if in.ConversationID != nil {
		if err := tx.QueryRow(ctx,
			`select contact_id from public.conversations where id = $1`,
			*in.ConversationID).Scan(&contactID); err != nil && !isNoRows(err) {
			return false, err
		}
	}

	// Names are frozen at the moment of the change. Renaming a label on the
	// phone tomorrow must not rewrite what happened today.
	fromName, err := labelNameSnapshot(ctx, tx, in.FromLabelID)
	if err != nil {
		return false, err
	}
	toName, err := labelNameSnapshot(ctx, tx, in.ToLabelID)
	if err != nil {
		return false, err
	}

	var adminRole *string
	if in.AdminID != nil {
		if err := tx.QueryRow(ctx,
			`select role::text from public.role_assignments where user_id = $1 and is_active`,
			*in.AdminID).Scan(&adminRole); err != nil && !isNoRows(err) {
			return false, err
		}
	}

	tag, err := tx.Exec(ctx, `
		insert into public.contact_label_events
			(workspace_id, account_id, application_id, contact_id, conversation_id,
			 event_type, from_label_id, to_label_id, from_label_name, to_label_name,
			 source, admin_id, admin_role, occurred_at, event_key)
		values ($1, $2, $3, $4, $5, $6::public.label_event_type, $7, $8, $9, $10,
		        $11::public.change_source, $12, $13::public.operational_role, $14, $15)
		on conflict (account_id, event_key) do nothing`,
		workspaceID, in.AccountID, applicationID, contactID, in.ConversationID,
		in.EventType, in.FromLabelID, in.ToLabelID, fromName, toName,
		in.Source, in.AdminID, adminRole, in.OccurredAt, in.EventKey)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 0 {
		return false, tx.Commit(ctx)
	}

	if contactID != nil {
		if err := refreshLabelState(ctx, tx, *contactID, workspaceID, in.AccountID, applicationID, in.OccurredAt); err != nil {
			return false, err
		}
	}

	return true, tx.Commit(ctx)
}

// RecordLabelDefinitionEvent appends a created/renamed/deleted entry for the
// label itself, as opposed to its use on a contact.
//
// A label that has not been pushed to WhatsApp yet has no account, and the
// history is keyed per account: those are skipped and recorded once the phone
// adopts the label and gives it an id. Recording them against an arbitrary
// account would put one number's history into another's report.
func (r *Repo) RecordLabelDefinitionEvent(
	ctx context.Context,
	labelID uuid.UUID,
	eventType, source string,
	adminID *uuid.UUID,
) error {
	var accountID *uuid.UUID
	if err := r.pool.QueryRow(ctx,
		`select account_id from public.conversation_labels where id = $1`,
		labelID).Scan(&accountID); err != nil {
		return mapErr(err)
	}
	if accountID == nil {
		return nil
	}

	at := time.Now().UTC()
	_, err := r.RecordLabelEvent(ctx, LabelEventInput{
		AccountID:  *accountID,
		EventType:  eventType,
		ToLabelID:  &labelID,
		Source:     source,
		AdminID:    adminID,
		OccurredAt: at,
		EventKey:   fmt.Sprintf("%s:%s:%d", eventType, labelID, at.UnixMilli()),
	})
	return err
}

func labelNameSnapshot(ctx context.Context, tx pgx.Tx, id *uuid.UUID) (*string, error) {
	if id == nil {
		return nil, nil
	}
	var name string
	if err := tx.QueryRow(ctx,
		`select name from public.conversation_labels where id = $1`, *id).Scan(&name); err != nil {
		if isNoRows(err) {
			return nil, nil
		}
		return nil, err
	}
	return &name, nil
}

// refreshLabelState recomputes what labels a contact currently carries.
//
// first_labeled_at is set once and never cleared: "this contact went from
// having no label to having one" is a thing that happened, and removing the
// label later does not unhappen it. That column is what answers the "kontak
// yang awalnya belum memiliki label lalu diberi label" card.
func refreshLabelState(
	ctx context.Context,
	tx pgx.Tx,
	contactID, workspaceID, accountID uuid.UUID,
	applicationID *uuid.UUID,
	at time.Time,
) error {
	_, err := tx.Exec(ctx, `
		with current as (
			select coalesce(array_agg(l.id order by l.sort_order, l.name)
			                filter (where l.id is not null), '{}') as ids,
			       coalesce(array_agg(l.name order by l.sort_order, l.name)
			                filter (where l.id is not null), '{}') as names
			  from public.conversations c
			  left join public.conversation_label_assignments a on a.conversation_id = c.id
			  left join public.conversation_labels l
			         on l.id = a.label_id and l.deleted_at is null
			 where c.contact_id = $1 and c.account_id = $3
		)
		insert into public.contact_label_state
			(contact_id, workspace_id, account_id, application_id,
			 label_ids, label_names, first_labeled_at, last_changed_at, change_count)
		select $1, $2, $3, $4, current.ids, current.names,
		       -- The cast is load-bearing. A bare $5 inside a CASE whose only
		       -- other branch is NULL gives Postgres nothing to infer from, so
		       -- the parameter is typed as text and the whole statement is
		       -- rejected with 42804 - taking the event row in the same
		       -- transaction down with it. That is what left the label history
		       -- empty while label changes were happening all day.
		       case when array_length(current.ids, 1) > 0 then $5::timestamptz end,
		       $5::timestamptz, 1
		  from current
		on conflict (contact_id) do update set
			application_id  = excluded.application_id,
			label_ids       = excluded.label_ids,
			label_names     = excluded.label_names,
			first_labeled_at = coalesce(public.contact_label_state.first_labeled_at,
			                            excluded.first_labeled_at),
			last_changed_at = excluded.last_changed_at,
			change_count    = public.contact_label_state.change_count + 1`,
		contactID, workspaceID, accountID, applicationID, at)
	return err
}
