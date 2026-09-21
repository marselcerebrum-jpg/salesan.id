package repository

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/salesan/omnichannel/backend/internal/analytics"
	"github.com/salesan/omnichannel/backend/internal/models"
)

// maxTimelineRows bounds how much of a conversation is reloaded for a
// recompute. Messages are pruned to the account's sync window anyway, so this
// is a guard against a pathological thread rather than a real limit.
const maxTimelineRows = 5000

// ConversationScope is the identity a conversation's derived rows are stamped
// with, loaded once so each insert does not have to re-join for it.
type ConversationScope struct {
	ID            uuid.UUID
	WorkspaceID   uuid.UUID
	AccountID     uuid.UUID
	ApplicationID *uuid.UUID
	ContactID     *uuid.UUID
	Type          string
}

// ConversationScopeByID loads the identity of one conversation.
func (r *Repo) ConversationScopeByID(ctx context.Context, id uuid.UUID) (*ConversationScope, error) {
	var s ConversationScope
	err := r.pool.QueryRow(ctx, `
		select c.id, c.workspace_id, c.account_id, a.application_id, c.contact_id, c.type::text
		  from public.conversations c
		  join public.whatsapp_accounts a on a.id = c.account_id
		 where c.id = $1`, id,
	).Scan(&s.ID, &s.WorkspaceID, &s.AccountID, &s.ApplicationID, &s.ContactID, &s.Type)
	if err != nil {
		return nil, mapErr(err)
	}
	return &s, nil
}

// ConversationTimeline loads the fields the analytics rules read.
//
// Deliberately not the full message rows: these rules consult ten fields, and
// loading attachments, quotes and reactions for a recompute would make an
// operation that runs on every incoming message far more expensive than the
// work it does.
func (r *Repo) ConversationTimeline(ctx context.Context, conversationID uuid.UUID) ([]analytics.Message, error) {
	rows, err := r.pool.Query(ctx, `
		select m.id, m.timestamp, m.from_me, coalesce(m.sender_source::text, ''),
		       m.sent_by, m.type::text, m.mentions_me,
		       coalesce(m.participant_jid, ''), coalesce(m.sender_phone, ''),
		       m.hidden_at is not null, m.revoked_at is not null
		  from public.messages m
		 where m.conversation_id = $1
		 order by m.timestamp
		 limit $2`, conversationID, maxTimelineRows)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []analytics.Message{}
	for rows.Next() {
		var m analytics.Message
		if err := rows.Scan(&m.ID, &m.Timestamp, &m.FromMe, &m.Source, &m.SentBy, &m.Type,
			&m.MentionsMe, &m.ParticipantJID, &m.SenderPhone, &m.Hidden, &m.Revoked); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// MetricsConfig is how a recompute should score what it finds.
type MetricsConfig struct {
	TargetSeconds    int
	UseBusinessHours bool
	FollowUpGap      time.Duration
}

// RecomputeConversation rebuilds every derived row for one conversation.
//
// Rebuilding rather than patching is what makes this safe to call from
// anywhere: an incoming message, a history sync that inserted a week at once,
// an edit, a delete, or the reconciliation sweep. WhatsApp delivers events out
// of order and repeats them after a reconnect, so an incremental update would
// have to be correct under every interleaving; a recompute only has to be
// correct once, and every call converges on the same answer.
//
// Everything happens in one transaction. A half-written set of SLA cycles would
// show a Leader a number that never existed.
func (r *Repo) RecomputeConversation(ctx context.Context, conversationID uuid.UUID, cfg MetricsConfig) error {
	scope, err := r.ConversationScopeByID(ctx, conversationID)
	if err != nil {
		return err
	}
	timeline, err := r.ConversationTimeline(ctx, conversationID)
	if err != nil {
		return err
	}

	windows, err := r.conversationWindows(ctx, scope, timeline)
	if err != nil {
		return err
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if scope.Type == models.ConversationTypePersonal {
		// The promise this conversation is measured against, resolved from the
		// workspace's own settings: the application's target if it has one,
		// otherwise the workspace default, otherwise `cfg` as it arrived from
		// the process configuration.
		//
		// Resolved here rather than passed in by the caller because only this
		// point knows which application the conversation belongs to. The
		// result is snapshot onto each cycle by writeSLACycles, which is what
		// keeps a settings change from rescoring last month.
		cfg = r.ResolveSLATarget(ctx, scope.WorkspaceID, scope.ApplicationID, cfg)

		cycles := analytics.ComputeSLACycles(timeline, analytics.SLAConfig{
			TargetSeconds:    cfg.TargetSeconds,
			UseBusinessHours: cfg.UseBusinessHours,
			Windows:          windows,
		})
		if err := writeSLACycles(ctx, tx, scope, cycles); err != nil {
			return err
		}

		followUps := analytics.ComputeFollowUps(timeline, analytics.FollowUpConfig{Gap: cfg.FollowUpGap})
		if err := writeFollowUps(ctx, tx, scope, followUps); err != nil {
			return err
		}
	} else {
		mentions := analytics.ComputeMentions(timeline)
		if err := writeMentions(ctx, tx, scope, mentions); err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}

// conversationWindows fetches the shifts covering the period the timeline
// spans, for the account and application this conversation belongs to.
func (r *Repo) conversationWindows(
	ctx context.Context,
	scope *ConversationScope,
	timeline []analytics.Message,
) ([]analytics.Window, error) {
	if len(timeline) == 0 {
		return nil, nil
	}
	sorted := analytics.SortTimeline(timeline)
	return r.ScheduleWindows(ctx, scope.WorkspaceID, WindowQuery{
		From:          sorted[0].Timestamp,
		To:            sorted[len(sorted)-1].Timestamp,
		AccountID:     &scope.AccountID,
		ApplicationID: scope.ApplicationID,
	})
}

func writeSLACycles(ctx context.Context, tx pgx.Tx, scope *ConversationScope, cycles []analytics.Cycle) error {
	keep := make([]uuid.UUID, 0, len(cycles))
	for _, c := range cycles {
		keep = append(keep, c.InboundMessageID)

		if _, err := tx.Exec(ctx, `
			insert into public.sla_cycles
				(workspace_id, account_id, application_id, conversation_id, contact_id,
				 inbound_message_id, started_at, inbound_message_count,
				 first_response_message_id, responded_at,
				 raw_duration_seconds, business_duration_seconds, target_seconds,
				 status, exclusion_reason, responder_admin_id, responder_source, schedule_id)
			values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13,
			        $14::public.sla_status, nullif($15, ''), $16,
			        nullif($17, '')::public.sender_source, $18)
			on conflict (inbound_message_id) do update set
				inbound_message_count     = excluded.inbound_message_count,
				first_response_message_id = excluded.first_response_message_id,
				responded_at              = excluded.responded_at,
				raw_duration_seconds      = excluded.raw_duration_seconds,
				business_duration_seconds = excluded.business_duration_seconds,
				target_seconds            = excluded.target_seconds,
				status                    = excluded.status,
				exclusion_reason          = excluded.exclusion_reason,
				responder_admin_id        = excluded.responder_admin_id,
				responder_source          = excluded.responder_source,
				schedule_id               = excluded.schedule_id`,
			scope.WorkspaceID, scope.AccountID, scope.ApplicationID, scope.ID, scope.ContactID,
			c.InboundMessageID, c.StartedAt, c.InboundCount,
			c.FirstResponseMessageID, c.RespondedAt,
			c.RawDurationSeconds, c.BusinessDurationSeconds, c.TargetSeconds,
			c.Status, c.ExclusionReason, c.ResponderAdminID, c.ResponderSource, c.ScheduleID,
		); err != nil {
			return err
		}
	}

	// Anything left over described a cycle the timeline no longer contains:
	// a message was revoked, hidden, or deleted. Leaving it would report a wait
	// that, as far as the conversation is now concerned, never happened.
	_, err := tx.Exec(ctx,
		`delete from public.sla_cycles
		  where conversation_id = $1 and not (inbound_message_id = any($2))`,
		scope.ID, keep)
	return err
}

func writeFollowUps(ctx context.Context, tx pgx.Tx, scope *ConversationScope, followUps []analytics.FollowUp) error {
	keep := make([]time.Time, 0, len(followUps))
	for _, f := range followUps {
		keep = append(keep, f.LocalDate)

		if _, err := tx.Exec(ctx, `
			insert into public.follow_up_events
				(workspace_id, account_id, application_id, conversation_id, contact_id,
				 local_date, trigger_message_id, started_at, message_count,
				 gap_seconds, last_inbound_at, admin_id, admin_source,
				 responded_at, response_message_id)
			values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12,
			        nullif($13, '')::public.sender_source, $14, $15)
			on conflict (conversation_id, local_date) do update set
				trigger_message_id  = excluded.trigger_message_id,
				started_at          = excluded.started_at,
				message_count       = excluded.message_count,
				gap_seconds         = excluded.gap_seconds,
				last_inbound_at     = excluded.last_inbound_at,
				admin_id            = excluded.admin_id,
				admin_source        = excluded.admin_source,
				responded_at        = excluded.responded_at,
				response_message_id = excluded.response_message_id`,
			scope.WorkspaceID, scope.AccountID, scope.ApplicationID, scope.ID, scope.ContactID,
			f.LocalDate, f.TriggerMessageID, f.StartedAt, f.MessageCount,
			f.GapSeconds, f.LastInboundAt, f.AdminID, f.AdminSource,
			f.RespondedAt, f.ResponseMessageID,
		); err != nil {
			return err
		}
	}

	_, err := tx.Exec(ctx,
		`delete from public.follow_up_events
		  where conversation_id = $1 and not (local_date = any($2))`,
		scope.ID, keep)
	return err
}

func writeMentions(ctx context.Context, tx pgx.Tx, scope *ConversationScope, mentions []analytics.Mention) error {
	keep := make([]uuid.UUID, 0, len(mentions))
	for _, m := range mentions {
		keep = append(keep, m.MessageID)

		if _, err := tx.Exec(ctx, `
			insert into public.group_mentions
				(workspace_id, account_id, application_id, conversation_id, message_id,
				 participant_jid, sender_phone, mentioned_at,
				 responded_at, response_message_id, responder_admin_id, responder_source)
			values ($1, $2, $3, $4, $5, nullif($6, ''), nullif($7, ''), $8, $9, $10, $11,
			        nullif($12, '')::public.sender_source)
			on conflict (message_id) do update set
				responded_at        = excluded.responded_at,
				response_message_id = excluded.response_message_id,
				responder_admin_id  = excluded.responder_admin_id,
				responder_source    = excluded.responder_source`,
			scope.WorkspaceID, scope.AccountID, scope.ApplicationID, scope.ID, m.MessageID,
			m.ParticipantJID, m.SenderPhone, m.MentionedAt,
			m.RespondedAt, m.ResponseMessageID, m.ResponderAdminID, m.ResponderSource,
		); err != nil {
			return err
		}
	}

	_, err := tx.Exec(ctx,
		`delete from public.group_mentions
		  where conversation_id = $1 and not (message_id = any($2))`,
		scope.ID, keep)
	return err
}

// --- leads --------------------------------------------------------------------

// RefreshLead records how a contact reached us and classifies it.
//
// The facts and the verdict are written together but stored apart: if the rule
// is ever corrected, the evidence it was applied to is still there to apply the
// new one to.
func (r *Repo) RefreshLead(ctx context.Context, contactID uuid.UUID) error {
	var (
		workspaceID, accountID uuid.UUID
		applicationID          *uuid.UUID
		conversationID         *uuid.UUID
		contactCreated         time.Time
		trackingStart          *time.Time
		firstInboundAt         *time.Time
		firstMessageID         *uuid.UUID
		importBatchID          *uuid.UUID
		importedAt             *time.Time
		historyBefore          bool
		hadLabelBefore         bool
	)

	err := r.pool.QueryRow(ctx, `
		with c as (
			select ct.id, ct.workspace_id, ct.account_id, ct.created_at, a.application_id,
			       a.tracking_started_at
			  from public.contacts ct
			  join public.whatsapp_accounts a on a.id = ct.account_id
			 where ct.id = $1
		),
		conv as (
			select cv.id from public.conversations cv, c
			 where cv.contact_id = c.id and cv.account_id = c.account_id
			 order by cv.created_at limit 1
		),
		first_in as (
			select m.id, m.timestamp, m.import_batch_id
			  from public.messages m
			  join conv on conv.id = m.conversation_id
			 where not m.from_me and m.hidden_at is null
			 order by m.timestamp limit 1
		)
		select c.workspace_id, c.account_id, c.application_id, c.created_at, c.tracking_started_at,
		       (select id from conv),
		       (select timestamp from first_in),
		       (select id from first_in),
		       (select import_batch_id from first_in),
		       (select min(b.started_at)
		          from public.import_batches b
		         where b.account_id = c.account_id),
		       exists (
		         select 1 from public.messages m
		           join conv on conv.id = m.conversation_id
		          where c.tracking_started_at is not null
		            and m.timestamp < c.tracking_started_at
		       ),
		       coalesce((
		         select cls.label_ids <> '{}'
		                and (cls.first_labeled_at is null
		                     or (c.tracking_started_at is not null
		                         and cls.first_labeled_at < c.tracking_started_at))
		           from public.contact_label_state cls where cls.contact_id = c.id
		       ), false)
		  from c`, contactID,
	).Scan(&workspaceID, &accountID, &applicationID, &contactCreated, &trackingStart,
		&conversationID, &firstInboundAt, &firstMessageID, &importBatchID, &importedAt,
		&historyBefore, &hadLabelBefore)
	if err != nil {
		return mapErr(err)
	}

	// A batch that ran after this contact's first message says nothing about
	// it. Only a batch recorded on the message itself is evidence of import.
	if importBatchID == nil {
		importedAt = nil
	}

	evidence := analytics.LeadEvidence{
		ContactCreatedAt:         contactCreated,
		FirstInboundAt:           firstInboundAt,
		TrackingStartedAt:        trackingStart,
		ImportBatchID:            importBatchID,
		ImportedAt:               importedAt,
		HasHistoryBeforeTracking: historyBefore,
		HadLabelBefore:           hadLabelBefore,
	}
	status, reason := analytics.ClassifyLead(evidence)

	var qualified *time.Time
	if d := analytics.QualifiedDate(status, evidence); !d.IsZero() {
		qualified = &d
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `
		insert into public.contact_first_seen
			(contact_id, workspace_id, account_id, application_id, conversation_id,
			 first_seen_at, first_inbound_at, first_message_id,
			 system_tracking_started_at, imported_at, import_batch_id, had_label_before)
		values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		on conflict (contact_id) do update set
			application_id             = excluded.application_id,
			conversation_id            = excluded.conversation_id,
			first_inbound_at           = excluded.first_inbound_at,
			first_message_id           = excluded.first_message_id,
			system_tracking_started_at = excluded.system_tracking_started_at,
			imported_at                = excluded.imported_at,
			import_batch_id            = excluded.import_batch_id,
			had_label_before           = excluded.had_label_before`,
		contactID, workspaceID, accountID, applicationID, conversationID,
		contactCreated, firstInboundAt, firstMessageID,
		trackingStart, importedAt, importBatchID, hadLabelBefore,
	); err != nil {
		return err
	}

	if _, err := tx.Exec(ctx, `
		insert into public.lead_classifications
			(contact_id, workspace_id, account_id, application_id,
			 lead_status, status_reason, qualified_date)
		values ($1, $2, $3, $4, $5::public.lead_status, $6, $7)
		on conflict (contact_id) do update set
			application_id = excluded.application_id,
			lead_status    = excluded.lead_status,
			status_reason  = excluded.status_reason,
			qualified_date = excluded.qualified_date`,
		contactID, workspaceID, accountID, applicationID, status, reason, qualified,
	); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// StaleMetricConversations lists conversations whose derived rows are older
// than their newest message.
//
// This is what the reconciliation sweep walks. Events do get lost: a dropped
// WebSocket, a process restart mid-batch, a WhatsApp reconnect that replays
// half a day. A dashboard that silently under-reports is worse than one that is
// briefly behind.
func (r *Repo) StaleMetricConversations(ctx context.Context, since time.Time, limit int) ([]uuid.UUID, error) {
	rows, err := r.pool.Query(ctx, `
		select c.id
		  from public.conversations c
		 where c.last_message_at >= $1
		   and (
		     (c.type = 'personal' and exists (
		        select 1 from public.messages m
		         where m.conversation_id = c.id
		           and m.timestamp >= $1
		           and m.hidden_at is null
		           and not exists (
		             select 1 from public.sla_cycles s
		              where s.conversation_id = c.id and s.updated_at >= m.created_at)
		           and not exists (
		             select 1 from public.follow_up_events f
		              where f.conversation_id = c.id and f.updated_at >= m.created_at)
		      ))
		     or
		     (c.type = 'group' and exists (
		        select 1 from public.messages m
		         where m.conversation_id = c.id
		           and m.mentions_me and m.timestamp >= $1 and m.hidden_at is null
		           and not exists (
		             select 1 from public.group_mentions g where g.message_id = m.id)
		      ))
		   )
		 order by c.last_message_at desc
		 limit $2`, since, limit)
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

// UnclassifiedContacts lists contacts whose lead verdict needs (re)computing.
//
// Two kinds: contacts never classified, and the oldest verdicts. The second is
// what lets a corrected rule reach contacts that were already decided — the
// classification is a conclusion drawn from stored evidence, so re-drawing it
// costs nothing and a rule that only ever applied to new contacts would leave
// the report permanently split between two definitions.
func (r *Repo) UnclassifiedContacts(ctx context.Context, limit int) ([]uuid.UUID, error) {
	rows, err := r.pool.Query(ctx, `
		(
		  select c.id, 0 as priority, c.created_at as ordering
		    from public.contacts c
		   where not exists (
		     select 1 from public.lead_classifications l where l.contact_id = c.id)
		)
		union all
		(
		  select l.contact_id, 1, l.classified_at
		    from public.lead_classifications l
		   order by l.classified_at
		   limit $1
		)
		order by priority, ordering desc
		limit $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []uuid.UUID{}
	for rows.Next() {
		var id uuid.UUID
		var priority int
		var ordering time.Time
		if err := rows.Scan(&id, &priority, &ordering); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// StartImportBatch opens a history-sync batch and returns its id.
//
// Every message written while it is open carries the id, which is the evidence
// the lead classifier reads: without it, a seven-day backfill is
// indistinguishable from a very good week.
func (r *Repo) StartImportBatch(
	ctx context.Context,
	workspaceID, accountID uuid.UUID,
	windowDays int,
	windowStart time.Time,
) (uuid.UUID, error) {
	var id uuid.UUID
	err := r.pool.QueryRow(ctx, `
		insert into public.import_batches
			(workspace_id, account_id, window_days, window_start)
		values ($1, $2, $3, $4)
		returning id`, workspaceID, accountID, windowDays, windowStart).Scan(&id)
	return id, err
}

// FinishImportBatch closes a batch with its tallies.
func (r *Repo) FinishImportBatch(
	ctx context.Context,
	id uuid.UUID,
	conversations, messages, skipped int,
	failure string,
) error {
	status := "completed"
	var reason *string
	if failure != "" {
		status = "failed"
		reason = &failure
	}
	_, err := r.pool.Exec(ctx, `
		update public.import_batches
		   set finished_at = now(), conversations = $2, messages = $3,
		       skipped = $4, status = $5, error_message = $6
		 where id = $1`, id, conversations, messages, skipped, status, reason)
	return err
}
