package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/salesan/omnichannel/backend/internal/models"
)

const campaignColumns = `
	cc.id, cc.workspace_id, cc.application_id, app.code, cc.account_id, acc.name,
	cc.campaign_type::text, cc.name, cc.body, cc.attachment_ids, cc.status::text,
	cc.scheduled_at, cc.executed_at, cc.target_count, cc.success_count, cc.failed_count,
	cc.failure_reason, cc.created_by, coalesce(nullif(u.full_name, ''), u.email),
	ra.role::text, cc.created_at, cc.updated_at,
	cc.delay_profile, cc.compose_mode, cc.message_template,
	cc.media_url, cc.media_kind, cc.media_mime, cc.media_size_bytes,
	-- The document's display name, so a report can say what was attached after
	-- the file itself has been deleted from the bucket.
	cc.media_file_name, cc.caption,
	cc.target_source, cc.started_at, cc.finished_at, cc.cancelled_at,
	cc.cancel_requested, cc.max_attempts, cc.retry_gap_seconds, cc.archived_at,
	(select count(*) from public.broadcast_sender_devices d where d.campaign_id = cc.id),
	-- The numbers by name, in the order they were chosen. The Story list shows
	-- them on the card, where a bare count would not say which phone posted.
	(select coalesce(array_agg(a2.name order by d2.position), '{}')
	   from public.broadcast_sender_devices d2
	   join public.whatsapp_accounts a2 on a2.id = d2.account_id
	  where d2.campaign_id = cc.id)`

const campaignJoins = `
	from public.content_campaigns cc
	left join public.applications app on app.id = cc.application_id
	left join public.whatsapp_accounts acc on acc.id = cc.account_id
	left join public.users u on u.id = cc.created_by
	left join public.role_assignments ra on ra.user_id = cc.created_by and ra.is_active`

func scanCampaign(row scannable) (*models.Campaign, error) {
	var c models.Campaign
	err := row.Scan(&c.ID, &c.WorkspaceID, &c.ApplicationID, &c.ApplicationCode,
		&c.AccountID, &c.AccountName, &c.CampaignType, &c.Name, &c.Body, &c.AttachmentIDs,
		&c.Status, &c.ScheduledAt, &c.ExecutedAt, &c.TargetCount, &c.SuccessCount,
		&c.FailedCount, &c.FailureReason, &c.CreatedBy, &c.CreatorName, &c.CreatorRole,
		&c.CreatedAt, &c.UpdatedAt,
		&c.DelayProfile, &c.ComposeMode, &c.MessageTemplate,
		&c.MediaURL, &c.MediaKind, &c.MediaMime, &c.MediaSizeBytes,
		&c.MediaFileName, &c.Caption,
		&c.TargetSource, &c.StartedAt, &c.FinishedAt, &c.CancelledAt,
		&c.CancelRequested, &c.MaxAttempts, &c.RetryGapSeconds, &c.ArchivedAt,
		&c.DeviceCount, &c.DeviceNames)
	if err != nil {
		return nil, mapErr(err)
	}
	// Never nil. A nil Go slice marshals to `null`, and the browser reads these
	// with .map and .join — which is how a campaign with no devices took a whole
	// page down once.
	if c.AttachmentIDs == nil {
		c.AttachmentIDs = []uuid.UUID{}
	}
	if c.DeviceNames == nil {
		c.DeviceNames = []string{}
	}
	return &c, nil
}

// CampaignFilter narrows a campaign listing.
type CampaignFilter struct {
	CampaignType  string
	Status        string
	ApplicationID *uuid.UUID
	// AccountID narrows to campaigns that send from one number.
	AccountID *uuid.UUID
	CreatedBy *uuid.UUID
	// Search matches the campaign name or one of its internal labels.
	Search string
	From   time.Time
	To     time.Time
	Limit  int
	// IncludeArchived brings back campaigns the operator has put away. Off by
	// default: archiving is how a finished campaign leaves the list.
	IncludeArchived bool
	// Recurring separates the two tabs. True lists only campaigns that belong to
	// a repeating series; false lists only one-off sends; nil lists both.
	//
	// A series member is any campaign that either carries a recurrence rule or
	// was born from one, so an occurrence that has already run stays in the tab
	// its operator expects to find it in rather than moving to the other one the
	// moment it fires.
	Recurring *bool
}

// ListCampaigns returns the campaigns a caller may see.
func (r *Repo) ListCampaigns(ctx context.Context, sc Scope, f CampaignFilter) ([]models.Campaign, error) {
	q := &queryArgs{}
	where := campaignWhere(sc, f, q)

	limit := f.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}

	rows, err := r.pool.Query(ctx,
		`select `+campaignColumns+campaignJoins+where+
			fmt.Sprintf(" order by coalesce(cc.scheduled_at, cc.created_at) desc limit %d", limit),
		q.args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []models.Campaign{}
	for rows.Next() {
		c, err := scanCampaign(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// campaignWhere is the scoping and filtering both the list and its facet counts
// use, so a chip can never claim a number the table below it does not show.
func campaignWhere(sc Scope, f CampaignFilter, q *queryArgs) string {
	where := fmt.Sprintf(" where cc.workspace_id = %s", q.add(sc.WorkspaceID))

	if !sc.All {
		// Somebody may always see what they created themselves, even for an
		// application they are no longer assigned to: their own audit trail is
		// theirs.
		where += fmt.Sprintf(" and (cc.application_id = any(%s) or cc.created_by = %s)",
			q.add(sc.ApplicationIDs), q.add(sc.UserID))
	}
	if f.CampaignType != "" {
		where += fmt.Sprintf(" and cc.campaign_type = %s::public.campaign_type", q.add(f.CampaignType))
	}
	if f.Status != "" {
		where += fmt.Sprintf(" and cc.status = %s::public.campaign_status", q.add(f.Status))
	}
	if f.ApplicationID != nil {
		where += fmt.Sprintf(" and cc.application_id = %s", q.add(*f.ApplicationID))
	}
	if f.CreatedBy != nil {
		where += fmt.Sprintf(" and cc.created_by = %s", q.add(*f.CreatedBy))
	}
	if f.AccountID != nil {
		// Either the campaign's primary number or one of its sender devices —
		// a multi-device campaign is "from" every number it uses.
		id := q.add(*f.AccountID)
		where += fmt.Sprintf(` and (cc.account_id = %s or exists (
			select 1 from public.broadcast_sender_devices d
			 where d.campaign_id = cc.id and d.account_id = %s))`, id, id)
	}
	if f.Search != "" {
		// The name, or one of the internal labels attached to it: both are how
		// somebody refers to a campaign a week later.
		term := q.add("%" + strings.ToLower(f.Search) + "%")
		where += fmt.Sprintf(` and (lower(cc.name) like %s or exists (
			select 1 from public.campaign_label_assignments cla
			  join public.campaign_labels cl on cl.id = cla.label_id
			 where cla.campaign_id = cc.id and lower(cl.name) like %s))`, term, term)
	}
	if !f.From.IsZero() {
		where += fmt.Sprintf(" and cc.created_at >= %s", q.add(f.From))
	}
	if !f.To.IsZero() {
		where += fmt.Sprintf(" and cc.created_at < %s", q.add(f.To))
	}
	// Archived campaigns are hidden unless asked for. Archiving is the answer to
	// "get this off my screen" for anything that is not a draft, so a list that
	// still showed them would leave the operator with nothing that works.
	if !f.IncludeArchived {
		where += " and cc.archived_at is null"
	}
	if f.Recurring != nil {
		if *f.Recurring {
			where += " and (cc.recurrence is not null or cc.recurrence_parent_id is not null)"
		} else {
			where += " and cc.recurrence is null and cc.recurrence_parent_id is null"
		}
	}
	return where
}

// CampaignFacet is one application chip: how many campaigns it holds.
type CampaignFacet struct {
	ID    *uuid.UUID `json:"id"`
	Code  string     `json:"code"`
	Name  string     `json:"name"`
	Color *string    `json:"color"`
	Count int        `json:"count"`
}

// CampaignFacets counts campaigns per application, under the same filters as the
// list except the application filter itself.
//
// The application filter is deliberately dropped: chips that narrowed as soon as
// one was pressed would show every other brand at zero, which is not what the
// reader is asking them.
func (r *Repo) CampaignFacets(
	ctx context.Context, sc Scope, f CampaignFilter,
) (total int, facets []CampaignFacet, statuses map[string]int, err error) {
	/*
	 * Each dimension is counted without its own filter.
	 *
	 * The application chips count every application under the status that is
	 * chosen; the status chips count every status under the application that is
	 * chosen. Counting a dimension with its own filter still applied is how a
	 * chip row ends up reading "JADIASN 5" beside eleven zeroes the moment
	 * somebody presses JADIASN.
	 */
	byStatus := f
	byStatus.Status = ""
	f.ApplicationID = nil

	q := &queryArgs{}
	where := campaignWhere(sc, f, q)

	rows, err := r.pool.Query(ctx, `
		select cc.application_id, coalesce(app.code, ''), coalesce(app.name, 'Tanpa aplikasi'),
		       app.color, count(*)
		  from public.content_campaigns cc
		  left join public.applications app on app.id = cc.application_id`+where+`
		 group by cc.application_id, app.code, app.name, app.color
		 order by count(*) desc, app.code`, q.args...)
	if err != nil {
		return 0, nil, nil, err
	}
	defer rows.Close()

	facets = []CampaignFacet{}
	for rows.Next() {
		var fc CampaignFacet
		if err := rows.Scan(&fc.ID, &fc.Code, &fc.Name, &fc.Color, &fc.Count); err != nil {
			return 0, nil, nil, err
		}
		total += fc.Count
		facets = append(facets, fc)
	}
	if err := rows.Err(); err != nil {
		return 0, nil, nil, err
	}

	// The same count, asked of the other dimension. Its own query rather than a
	// second pass over the rows above, because those are already narrowed to
	// one status and could only ever report that one.
	q2 := &queryArgs{}
	where2 := campaignWhere(sc, byStatus, q2)
	sRows, err := r.pool.Query(ctx, `
		select cc.status, count(*)
		  from public.content_campaigns cc`+where2+`
		 group by cc.status`, q2.args...)
	if err != nil {
		return 0, nil, nil, err
	}
	defer sRows.Close()

	statuses = map[string]int{}
	for sRows.Next() {
		var status string
		var n int
		if err := sRows.Scan(&status, &n); err != nil {
			return 0, nil, nil, err
		}
		statuses[status] = n
	}
	return total, facets, statuses, sRows.Err()
}

// GetCampaign loads one campaign.
func (r *Repo) GetCampaign(ctx context.Context, workspaceID, id uuid.UUID) (*models.Campaign, error) {
	return scanCampaign(r.pool.QueryRow(ctx,
		`select `+campaignColumns+campaignJoins+` where cc.id = $1 and cc.workspace_id = $2`,
		id, workspaceID))
}

// CampaignInput carries a campaign to create or edit.
type CampaignInput struct {
	CampaignType  string
	Name          string
	Body          *string
	ApplicationID *uuid.UUID
	AccountID     *uuid.UUID
	AttachmentIDs []uuid.UUID
	ScheduledAt   *time.Time
	Targets       []CampaignTargetInput
}

// CampaignTargetInput is one recipient.
type CampaignTargetInput struct {
	ChatJID        string
	ContactID      *uuid.UUID
	ConversationID *uuid.UUID
}

// CreateCampaign writes a draft (or a scheduled campaign) and its recipients.
//
// One transaction, because the recipient list is part of what the campaign is:
// a campaign row whose targets half-landed would report a target count nobody
// can reconcile against the rows behind it.
func (r *Repo) CreateCampaign(
	ctx context.Context,
	workspaceID, createdBy uuid.UUID,
	in CampaignInput,
) (*models.Campaign, error) {
	status := models.CampaignDraft
	if in.ScheduledAt != nil {
		status = models.CampaignScheduled
	}
	if in.AttachmentIDs == nil {
		in.AttachmentIDs = []uuid.UUID{}
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// The creator's role and PIC are frozen onto the campaign. A Freelance
	// promoted next month must not change what last month's report says about
	// who ran this.
	var role *string
	if err := tx.QueryRow(ctx,
		`select role::text from public.role_assignments where user_id = $1 and is_active`,
		createdBy).Scan(&role); err != nil && !isNoRows(err) {
		return nil, err
	}

	var picUser *uuid.UUID
	if err := tx.QueryRow(ctx,
		`select pic_user_id from public.freelancer_pic_assignments where freelancer_user_id = $1`,
		createdBy).Scan(&picUser); err != nil && !isNoRows(err) {
		return nil, err
	}

	var id uuid.UUID
	if err := tx.QueryRow(ctx, `
		insert into public.content_campaigns
			(workspace_id, application_id, account_id, campaign_type, name, body,
			 attachment_ids, status, scheduled_at, target_count,
			 created_by, creator_role, creator_pic_id)
		values ($1, $2, $3, $4::public.campaign_type, $5, $6, $7,
		        $8::public.campaign_status, $9, $10, $11,
		        $12::public.operational_role, $13)
		returning id`,
		workspaceID, in.ApplicationID, in.AccountID, in.CampaignType, in.Name, in.Body,
		in.AttachmentIDs, status, in.ScheduledAt, len(in.Targets),
		createdBy, role, picUser,
	).Scan(&id); err != nil {
		return nil, err
	}

	for _, t := range in.Targets {
		if _, err := tx.Exec(ctx, `
			insert into public.campaign_targets
				(workspace_id, campaign_id, contact_id, conversation_id, chat_jid)
			values ($1, $2, $3, $4, $5)
			on conflict (campaign_id, chat_jid) do nothing`,
			workspaceID, id, t.ContactID, t.ConversationID, t.ChatJID); err != nil {
			return nil, err
		}
	}

	if in.ScheduledAt != nil {
		if _, err := tx.Exec(ctx, `
			insert into public.scheduled_publications
				(workspace_id, campaign_id, scheduled_at, created_by)
			values ($1, $2, $3, $4)`, workspaceID, id, *in.ScheduledAt, createdBy); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return r.GetCampaign(ctx, workspaceID, id)
}

// ScheduleCampaign sets or moves a campaign's run time.
//
// Rescheduling supersedes the previous publication row rather than editing it,
// and never creates a second campaign. That is what makes "Riwayat perubahan"
// on the detail screen real history instead of a single mutable field.
func (r *Repo) ScheduleCampaign(
	ctx context.Context,
	workspaceID, campaignID, actor uuid.UUID,
	at time.Time,
) (*models.Campaign, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// `scheduled_by` and `updated_by` are audit only. They record who touched
	// the campaign; they never move who it belongs to, which stays created_by
	// for the whole of its life. Written here rather than by a database
	// trigger because every write in this application goes through the
	// service role, where auth.uid() is null and a trigger would only ever
	// store nothing.
	tag, err := tx.Exec(ctx, `
		update public.content_campaigns
		   set scheduled_at = $3, status = 'scheduled', failure_reason = null,
		       scheduled_by = $4, updated_by = $4
		 where id = $1 and workspace_id = $2
		   and status in ('draft', 'scheduled', 'failed', 'cancelled')`,
		campaignID, workspaceID, at, actor)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrNotFound
	}

	if _, err := tx.Exec(ctx, `
		update public.scheduled_publications
		   set superseded_at = now()
		 where campaign_id = $1 and superseded_at is null and status = 'scheduled'`,
		campaignID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
		insert into public.scheduled_publications
			(workspace_id, campaign_id, scheduled_at, created_by, attempt)
		values ($1, $2, $3, $4,
		        (select coalesce(max(attempt), 0) + 1
		           from public.scheduled_publications where campaign_id = $2))`,
		workspaceID, campaignID, at, actor); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return r.GetCampaign(ctx, workspaceID, campaignID)
}

// CancelCampaign stops a scheduled run without deleting its history.
// CancelCampaign stops one that has not started yet.
//
// `actor` is recorded as the canceller and nothing more. A campaign cancelled
// by somebody else still counts as its creator's activity: the work of
// writing and scheduling it happened, and moving the figure to whoever
// pressed stop would credit them with a campaign they never wrote.
func (r *Repo) CancelCampaign(
	ctx context.Context, workspaceID, campaignID, actor uuid.UUID,
) (*models.Campaign, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx, `
		update public.content_campaigns
		   set status = 'cancelled', cancelled_at = coalesce(cancelled_at, now()),
		       cancelled_by = $3, updated_by = $3
		 where id = $1 and workspace_id = $2 and status in ('draft', 'scheduled')`,
		campaignID, workspaceID, actor)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrNotFound
	}
	if _, err := tx.Exec(ctx, `
		update public.scheduled_publications
		   set status = 'cancelled', superseded_at = now()
		 where campaign_id = $1 and superseded_at is null`, campaignID); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return r.GetCampaign(ctx, workspaceID, campaignID)
}

// DeleteCampaign removes a draft that was never scheduled.
func (r *Repo) DeleteCampaign(ctx context.Context, workspaceID, campaignID uuid.UUID) error {
	// A Story may be deleted whatever its state, at the operator's explicit
	// instruction: it lives 24 hours and then stops existing on WhatsApp too, so
	// there is no long-lived record to protect the way a broadcast's recipient
	// list is protected. Everything hanging off it goes with it through the
	// foreign keys — publications, detected viewers, the schedule, the labels.
	//
	// A broadcast still only deletes as a draft. A record of what reached two
	// hundred people is not somebody's to destroy for a shorter list; Arsipkan
	// remains the answer there.
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Viewers and frozen figures hang off the status itself, not off the
	// campaign, so no foreign key carries them away. Removed explicitly here,
	// because "hilangkan semuanya jika dihapus" means the figure goes too.
	for _, q := range []string{
		`delete from public.story_views v
		  using public.story_publications p, public.content_campaigns cc
		  where p.campaign_id = cc.id and cc.id = $1 and cc.workspace_id = $2
		    and (cc.status = 'draft' or cc.campaign_type = 'story')
		    and v.account_id = p.account_id and v.wa_message_id = p.wa_message_id`,
		`delete from public.story_view_snapshots s
		  using public.story_publications p, public.content_campaigns cc
		  where p.campaign_id = cc.id and cc.id = $1 and cc.workspace_id = $2
		    and (cc.status = 'draft' or cc.campaign_type = 'story')
		    and s.account_id = p.account_id and s.wa_message_id = p.wa_message_id`,
	} {
		if _, err := tx.Exec(ctx, q, campaignID, workspaceID); err != nil {
			return err
		}
	}

	tag, err := tx.Exec(ctx,
		`delete from public.content_campaigns
		  where id = $1 and workspace_id = $2
		    and (status = 'draft' or campaign_type = 'story')`,
		campaignID, workspaceID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return tx.Commit(ctx)
}

// ArchiveCampaign takes a finished campaign out of the list without destroying
// its report.
//
// The answer to "get this off my screen" for everything that is not a draft.
// Deleting it would take the send history with it, and a campaign that reached
// two hundred people is a record of something that happened — the operator's
// wish to stop looking at it is not a reason to lose it. The row stays, the
// scheduler already skips archived campaigns, and the list hides them.
//
// Reversible: pass archived = false to bring it back.
func (r *Repo) ArchiveCampaign(
	ctx context.Context, workspaceID, campaignID uuid.UUID, archived bool,
) error {
	var at *time.Time
	if archived {
		now := time.Now().UTC()
		at = &now
	}
	tag, err := r.pool.Exec(ctx, `
		update public.content_campaigns
		   set archived_at = $3
		 where id = $1 and workspace_id = $2`, campaignID, workspaceID, at)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// CampaignSchedules returns a campaign's scheduling history, newest first.
func (r *Repo) CampaignSchedules(ctx context.Context, campaignID uuid.UUID) ([]models.CampaignSchedule, error) {
	rows, err := r.pool.Query(ctx, `
		select sp.id, sp.scheduled_at, sp.timezone, sp.status::text, sp.attempt,
		       sp.started_at, sp.finished_at, sp.failure_reason, sp.superseded_at,
		       sp.created_by, coalesce(nullif(u.full_name, ''), u.email), sp.created_at
		  from public.scheduled_publications sp
		  left join public.users u on u.id = sp.created_by
		 where sp.campaign_id = $1
		 order by sp.created_at desc`, campaignID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []models.CampaignSchedule{}
	for rows.Next() {
		var s models.CampaignSchedule
		if err := rows.Scan(&s.ID, &s.ScheduledAt, &s.Timezone, &s.Status, &s.Attempt,
			&s.StartedAt, &s.FinishedAt, &s.FailureReason, &s.SupersededAt,
			&s.CreatedBy, &s.CreatorName, &s.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// --- activity log --------------------------------------------------------------

// ActivityInput is one admin action to record.
type ActivityInput struct {
	WorkspaceID   uuid.UUID
	ApplicationID *uuid.UUID
	AccountID     *uuid.UUID
	AdminID       *uuid.UUID
	EntityType    string
	EntityID      *uuid.UUID
	EntityName    string
	ActivityType  string
	Action        string
	Status        string
	FailureReason string
	Detail        map[string]any
	OccurredAt    time.Time
	// EventKey is the idempotency key. A retried publish must not appear twice
	// in somebody's activity count.
	EventKey string
}

// RecordActivity appends one entry to the admin activity log.
func (r *Repo) RecordActivity(ctx context.Context, in ActivityInput) error {
	if in.OccurredAt.IsZero() {
		in.OccurredAt = time.Now().UTC()
	}
	if in.EventKey == "" {
		return fmt.Errorf("record activity: event key is required for idempotency")
	}

	detail := []byte("{}")
	if len(in.Detail) > 0 {
		encoded, err := json.Marshal(in.Detail)
		if err != nil {
			return err
		}
		detail = encoded
	}

	var role *string
	var pic *uuid.UUID
	if in.AdminID != nil {
		if err := r.pool.QueryRow(ctx,
			`select role::text from public.role_assignments where user_id = $1 and is_active`,
			*in.AdminID).Scan(&role); err != nil && !isNoRows(err) {
			return err
		}
		if err := r.pool.QueryRow(ctx,
			`select pic_user_id from public.freelancer_pic_assignments where freelancer_user_id = $1`,
			*in.AdminID).Scan(&pic); err != nil && !isNoRows(err) {
			return err
		}
	}

	_, err := r.pool.Exec(ctx, `
		insert into public.admin_activity_logs
			(workspace_id, application_id, account_id, admin_id, admin_role, pic_id,
			 entity_type, entity_id, activity_type, action, entity_name,
			 detail, status, failure_reason, occurred_at, event_key)
		values ($1, $2, $3, $4, $5::public.operational_role, $6, $7, $8,
		        nullif($9, '')::public.campaign_activity_type, nullif($10, ''),
		        nullif($11, ''), $12::jsonb, nullif($13, ''), nullif($14, ''), $15, $16)
		on conflict (workspace_id, event_key) do nothing`,
		in.WorkspaceID, in.ApplicationID, in.AccountID, in.AdminID, role, pic,
		in.EntityType, in.EntityID, in.ActivityType, in.Action, in.EntityName,
		detail, in.Status, in.FailureReason, in.OccurredAt, in.EventKey)
	return err
}

// EntityActivity returns the audit trail for one entity, newest first.
func (r *Repo) EntityActivity(
	ctx context.Context,
	workspaceID uuid.UUID,
	entityType string,
	entityID uuid.UUID,
) ([]models.CampaignActivity, error) {
	rows, err := r.pool.Query(ctx, `
		select al.id, al.activity_type::text, al.action, al.entity_name,
		       al.admin_id, coalesce(nullif(u.full_name, ''), u.email), al.admin_role::text,
		       al.status, al.failure_reason, al.detail, al.occurred_at
		  from public.admin_activity_logs al
		  left join public.users u on u.id = al.admin_id
		 where al.workspace_id = $1 and al.entity_type = $2 and al.entity_id = $3
		 order by al.occurred_at desc`, workspaceID, entityType, entityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []models.CampaignActivity{}
	for rows.Next() {
		var a models.CampaignActivity
		if err := rows.Scan(&a.ID, &a.ActivityType, &a.Action, &a.EntityName,
			&a.AdminID, &a.AdminName, &a.AdminRole, &a.Status, &a.FailureReason,
			&a.Detail, &a.OccurredAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
