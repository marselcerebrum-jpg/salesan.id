package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/salesan/omnichannel/backend/internal/models"
)

// The Broadcast and Story queue.
//
// Everything a worker needs is a row, and every transition is a conditional
// UPDATE. That is not ceremony: a queue that lives in a process disappears with
// the process, and a campaign half-sent when a backend restarted must be
// finishable without anybody having to work out by hand which of four thousand
// people already got the message.
//
// Two guarantees hold the whole thing together:
//
//   - Claiming uses `for update skip locked` behind an UPDATE, so two workers
//     racing for the same row produce one winner and one worker that simply
//     moves on. No advisory locks, no leader election, no coordination.
//   - A lease expires. A worker that dies mid-send leaves rows marked
//     `processing` with a lease in the past; those are reconciled — checked
//     against the message actually recorded — before anything is re-sent.

// SaveBroadcastInput is a campaign to create or replace the draft of.
type SaveBroadcastInput struct {
	CampaignType   string
	Name           string
	Template       string
	ComposeMode    string
	ApplicationID  *uuid.UUID
	AccountIDs     []uuid.UUID
	MediaURL       *string
	MediaKind      *string
	MediaMime      *string
	MediaSizeBytes *int64
	MediaSHA256    *string
	// MediaStoragePath and MediaFileName describe an uploaded document living in
	// the private bucket. The bytes are never in the database, and the runner
	// deletes the object once the campaign is over.
	MediaStoragePath *string
	MediaFileName    *string
	Caption          *string
	DelayProfile     string
	DelayMinSeconds  *int
	DelayMaxSeconds  *int
	// AutoRetryOnDisconnect keeps a disconnected number's share waiting for it.
	AutoRetryOnDisconnect bool
	// Recurrence is "daily", "weekly", "monthly", or empty for a single run.
	Recurrence      string
	RecurrenceUntil *time.Time
	// RecurrenceTime is a WIB wall clock, "HH:MM".
	RecurrenceTime    string
	RecurrenceWeekday *int
	RecurrenceDay     *int
	TargetSource      string
	ScheduledAt       *time.Time
	MaxAttempts       int
	RetryGapSeconds   int
	Targets           []models.ResolvedTarget
	LabelIDs          []uuid.UUID
}

// SaveBroadcast writes a campaign together with its devices and recipients.
//
// One transaction, because the three are one thing: a campaign row whose target
// list half-landed would report a total nobody can reconcile against the rows
// behind it, and a device list that half-landed would send from numbers the
// review screen never showed.
func (r *Repo) SaveBroadcast(
	ctx context.Context,
	workspaceID, createdBy uuid.UUID,
	in SaveBroadcastInput,
) (*models.Campaign, error) {
	status := models.CampaignDraft
	if in.ScheduledAt != nil {
		status = models.CampaignScheduled
	}
	if in.MaxAttempts <= 0 {
		in.MaxAttempts = 3
	}
	if in.RetryGapSeconds <= 0 {
		in.RetryGapSeconds = 180
	}
	if in.DelayProfile == "" {
		in.DelayProfile = models.DelayNormal
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// The creator's role and PIC are frozen onto the campaign, so a promotion
	// next month does not change what last month's report says about who ran it.
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

	// content_campaigns.account_id predates multi-device and is still what the
	// Dashboard filters on. It carries the first sender so those filters keep
	// working; broadcast_sender_devices is the real list.
	var primary *uuid.UUID
	if len(in.AccountIDs) > 0 {
		primary = &in.AccountIDs[0]
	}

	var id uuid.UUID
	if err := tx.QueryRow(ctx, `
		insert into public.content_campaigns
			(workspace_id, application_id, account_id, campaign_type, name, body,
			 status, scheduled_at, target_count, created_by, creator_role, creator_pic_id,
			 delay_profile, compose_mode, message_template, media_url, media_kind,
			 media_mime, media_size_bytes, media_sha256, caption, target_source,
			 max_attempts, retry_gap_seconds,
			 delay_min_seconds, delay_max_seconds, auto_retry_on_disconnect,
			 recurrence, recurrence_until,
			 recurrence_time, recurrence_weekday, recurrence_day,
			 media_storage_path, media_file_name)
		values ($1, $2, $3, $4::public.campaign_type, $5, $6,
		        $7::public.campaign_status, $8, $9, $10, $11::public.operational_role, $12,
		        $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24,
		        $25, $26, $27, $28, $29,
		        -- Stored as a bare wall clock; the scheduler reads it in Jakarta.
		        nullif($30, '')::time, $31, $32, $33, $34)
		returning id`,
		workspaceID, in.ApplicationID, primary, in.CampaignType, in.Name, in.Template,
		status, in.ScheduledAt, len(in.Targets), createdBy, role, picUser,
		in.DelayProfile, in.ComposeMode, in.Template, in.MediaURL, in.MediaKind,
		in.MediaMime, in.MediaSizeBytes, in.MediaSHA256, in.Caption, nullIfEmpty(in.TargetSource),
		in.MaxAttempts, in.RetryGapSeconds,
		in.DelayMinSeconds, in.DelayMaxSeconds, in.AutoRetryOnDisconnect,
		nullIfEmpty(in.Recurrence), in.RecurrenceUntil,
		in.RecurrenceTime, in.RecurrenceWeekday, in.RecurrenceDay,
		in.MediaStoragePath, in.MediaFileName,
	).Scan(&id); err != nil {
		return nil, err
	}

	for i, accountID := range in.AccountIDs {
		if _, err := tx.Exec(ctx, `
			insert into public.broadcast_sender_devices
				(workspace_id, campaign_id, account_id, position)
			values ($1, $2, $3, $4)
			on conflict (campaign_id, account_id) do nothing`,
			workspaceID, id, accountID, i); err != nil {
			return nil, err
		}
	}

	if err := insertTargets(ctx, tx, workspaceID, id, in.Targets); err != nil {
		return nil, err
	}

	for _, labelID := range in.LabelIDs {
		if _, err := tx.Exec(ctx, `
			insert into public.campaign_label_assignments (campaign_id, label_id, assigned_by)
			values ($1, $2, $3) on conflict do nothing`, id, labelID, createdBy); err != nil {
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

	if err := syncDeviceCounts(ctx, tx, id); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return r.GetCampaign(ctx, workspaceID, id)
}

// ErrCampaignStarted reports an edit aimed at a campaign that has already gone
// out, wholly or in part.
//
// Its own error rather than a plain refusal, because the reason matters to the
// person reading it: nothing is wrong with the request, the campaign has simply
// passed the point where rewriting it would mean anything. What it said is what
// people received, and the answer is to duplicate it, not to edit it.
var ErrCampaignStarted = errors.New("repository: campaign sudah berjalan")

// UpdateBroadcast rewrites a campaign that has not gone out yet.
//
// The same row, edited. Not a copy: an operator fixing a typo before a broadcast
// leaves is not creating a second broadcast, and leaving the original behind
// would fill the list with near-identical entries nobody can tell apart.
//
// Everything hanging off the campaign is replaced rather than merged, for the
// same reason the composer sends the whole form rather than a patch: a recipient
// list that is the union of the old choice and the new one is a list nobody
// chose, and it is exactly how somebody receives a message that was removed.
//
// The schedule is deliberately NOT carried over by the caller. An edited
// campaign comes back to the composer with its departure time blank and has to
// be given one again, so a run cannot fire out of a half-finished edit.
func (r *Repo) UpdateBroadcast(
	ctx context.Context,
	workspaceID, campaignID, editedBy uuid.UUID,
	in SaveBroadcastInput,
) (*models.Campaign, error) {
	status := models.CampaignDraft
	if in.ScheduledAt != nil {
		status = models.CampaignScheduled
	}
	if in.DelayProfile == "" {
		in.DelayProfile = models.DelayNormal
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Locked and inspected first, so "not yours", "does not exist" and "already
	// running" are three different answers rather than one silent no-op. The lock
	// is what stops the scheduler from claiming the campaign between this check
	// and the rewrite below.
	var current string
	var started, executed *time.Time
	if err := tx.QueryRow(ctx, `
		select status::text, started_at, executed_at
		  from public.content_campaigns
		 where id = $1 and workspace_id = $2
		 for update`, campaignID, workspaceID).Scan(&current, &started, &executed); err != nil {
		return nil, mapErr(err)
	}
	if started != nil || executed != nil ||
		(current != models.CampaignDraft && current != models.CampaignScheduled) {
		return nil, ErrCampaignStarted
	}

	var primary *uuid.UUID
	if len(in.AccountIDs) > 0 {
		primary = &in.AccountIDs[0]
	}

	// creator_role, creator_pic_id and created_by are untouched: they record who
	// made this campaign, and an edit does not change that.
	if _, err := tx.Exec(ctx, `
		update public.content_campaigns
		   set application_id = $3, account_id = $4, name = $5, body = $6,
		       status = $7::public.campaign_status, scheduled_at = $8,
		       delay_profile = $9, compose_mode = $10, message_template = $6,
		       media_url = $11, media_kind = $12, media_mime = $13,
		       media_size_bytes = $14, media_sha256 = $15, caption = $16,
		       target_source = nullif($17, ''),
		       delay_min_seconds = $18, delay_max_seconds = $19,
		       auto_retry_on_disconnect = $20,
		       recurrence = nullif($21, ''), recurrence_until = $22,
		       recurrence_time = nullif($23, '')::time,
		       recurrence_weekday = $24, recurrence_day = $25,
		       media_storage_path = $26, media_file_name = $27,
		       -- An edit clears what the previous attempt left behind. Without
		       -- this a campaign that failed, was edited and fixed would still
		       -- carry the old failure_reason into its next report.
		       failure_reason = null, cancel_requested = false, cancelled_at = null,
		       success_count = 0, failed_count = 0,
		       recurrence_spawned_at = null,
		       lease_owner = null, lease_expires_at = null,
		       updated_at = now()
		 where id = $1 and workspace_id = $2`,
		campaignID, workspaceID, in.ApplicationID, primary, in.Name, in.Template,
		status, in.ScheduledAt,
		in.DelayProfile, in.ComposeMode,
		in.MediaURL, in.MediaKind, in.MediaMime,
		in.MediaSizeBytes, in.MediaSHA256, in.Caption,
		in.TargetSource,
		in.DelayMinSeconds, in.DelayMaxSeconds, in.AutoRetryOnDisconnect,
		in.Recurrence, in.RecurrenceUntil, in.RecurrenceTime,
		in.RecurrenceWeekday, in.RecurrenceDay,
		in.MediaStoragePath, in.MediaFileName); err != nil {
		return nil, err
	}

	// Replaced wholesale. Deleting first is safe here and only here: none of
	// these rows can have an outcome yet, because the campaign has not run.
	for _, q := range []string{
		`delete from public.broadcast_sender_devices where campaign_id = $1`,
		`delete from public.campaign_targets where campaign_id = $1`,
		`delete from public.campaign_label_assignments where campaign_id = $1`,
		`delete from public.story_publications where campaign_id = $1`,
	} {
		if _, err := tx.Exec(ctx, q, campaignID); err != nil {
			return nil, err
		}
	}

	for i, accountID := range in.AccountIDs {
		if _, err := tx.Exec(ctx, `
			insert into public.broadcast_sender_devices
				(workspace_id, campaign_id, account_id, position)
			values ($1, $2, $3, $4)
			on conflict (campaign_id, account_id) do nothing`,
			workspaceID, campaignID, accountID, i); err != nil {
			return nil, err
		}
	}

	if err := insertTargets(ctx, tx, workspaceID, campaignID, in.Targets); err != nil {
		return nil, err
	}

	for _, labelID := range in.LabelIDs {
		if _, err := tx.Exec(ctx, `
			insert into public.campaign_label_assignments (campaign_id, label_id, assigned_by)
			values ($1, $2, $3) on conflict do nothing`,
			campaignID, labelID, editedBy); err != nil {
			return nil, err
		}
	}

	// The old departure time is retired whether or not a new one replaces it, so
	// an edit that leaves the campaign unscheduled cannot be fired by a schedule
	// row nobody remembered was still there.
	if _, err := tx.Exec(ctx, `
		update public.scheduled_publications
		   set superseded_at = now()
		 where campaign_id = $1 and superseded_at is null and status = 'scheduled'`,
		campaignID); err != nil {
		return nil, err
	}
	if in.ScheduledAt != nil {
		if _, err := tx.Exec(ctx, `
			insert into public.scheduled_publications
				(workspace_id, campaign_id, scheduled_at, created_by, attempt)
			values ($1, $2, $3, $4,
			        (select coalesce(max(attempt), 0) + 1
			           from public.scheduled_publications where campaign_id = $2))`,
			workspaceID, campaignID, *in.ScheduledAt, editedBy); err != nil {
			return nil, err
		}
	}

	if err := syncDeviceCounts(ctx, tx, campaignID); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return r.GetCampaign(ctx, workspaceID, campaignID)
}

// insertTargets writes the recipient rows.
//
// The unique key (campaign_id, chat_jid) from migration 0024 is what enforces
// "one recipient receives this campaign once", whatever route they arrived by —
// pasted, imported, or picked from a group. Deduplication is therefore a
// property of the schema rather than of whichever code path built the list.
func insertTargets(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID, campaignID uuid.UUID,
	targets []models.ResolvedTarget,
) error {
	for _, t := range targets {
		vars := []byte("{}")
		if len(t.Variables) > 0 {
			encoded, err := json.Marshal(t.Variables)
			if err != nil {
				return err
			}
			vars = encoded
		}
		if _, err := tx.Exec(ctx, `
			insert into public.campaign_targets
				(workspace_id, campaign_id, contact_id, conversation_id, chat_jid,
				 account_id, target_type, phone_number, display_name, variables,
				 rendered_body, status)
			values ($1, $2, $3, $4, $5, $6, $7, nullif($8, ''), nullif($9, ''),
			        $10::jsonb, nullif($11, ''), 'pending')
			on conflict (campaign_id, chat_jid) do nothing`,
			workspaceID, campaignID, t.ContactID, t.ConversationID, t.ChatJID,
			nullUUID(t.AccountID), t.TargetType, t.PhoneNumber, t.DisplayName, vars,
			t.RenderedBody); err != nil {
			return err
		}
	}
	return nil
}

// syncDeviceCounts refreshes each device's assigned tally from the target rows,
// so the review screen and the report never disagree with the queue.
func syncDeviceCounts(ctx context.Context, tx pgx.Tx, campaignID uuid.UUID) error {
	_, err := tx.Exec(ctx, `
		update public.broadcast_sender_devices d
		   set assigned_count = coalesce(x.n, 0)
		  from (select account_id, count(*) as n
		          from public.campaign_targets
		         where campaign_id = $1 and account_id is not null
		         group by account_id) x
		 where d.campaign_id = $1 and d.account_id = x.account_id`, campaignID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx,
		`update public.content_campaigns
		    set target_count = (select count(*) from public.campaign_targets where campaign_id = $1)
		  where id = $1`, campaignID)
	return err
}

// --- the queue ---------------------------------------------------------------

// CampaignJob is a campaign a worker has taken responsibility for.
type CampaignJob struct {
	ID            uuid.UUID
	WorkspaceID   uuid.UUID
	ApplicationID *uuid.UUID
	CampaignType  string
	Name          string
	Template      string
	ComposeMode   string
	MediaURL      *string
	MediaKind     *string
	// MediaStoragePath names an uploaded document in the private bucket, for a
	// campaign that attached a file rather than a link.
	MediaStoragePath *string
	MediaFileName    *string
	Caption          *string
	DelayProfile     string
	// DelayMinSeconds and DelayMaxSeconds override the profile when the operator
	// set the range by hand. Both nil means the profile decides.
	DelayMinSeconds *int
	DelayMaxSeconds *int
	// AutoRetryOnDisconnect keeps a number's remaining share waiting for it to
	// come back. False abandons the share instead, which is what a time-bound
	// promotion needs: finishing it six hours late is worse than not finishing.
	AutoRetryOnDisconnect bool
	MaxAttempts           int
	RetryGapSeconds       int
	CreatedBy             *uuid.UUID
	AccountIDs            []uuid.UUID
}

// DelayRange is the interval this job's pauses are drawn from: the operator's
// own numbers when they set them, otherwise the named profile's.
func (j CampaignJob) DelayRange() models.DelayRange {
	if j.DelayMinSeconds != nil && j.DelayMaxSeconds != nil {
		return models.DelayRange{
			Min: time.Duration(*j.DelayMinSeconds) * time.Second,
			Max: time.Duration(*j.DelayMaxSeconds) * time.Second,
		}
	}
	return models.DelayProfile(j.DelayProfile)
}

// ClaimDueCampaigns takes ownership of campaigns whose time has come.
//
// `owner` identifies this process instance. It is recorded rather than used for
// exclusion — exclusion comes from the lease and from SKIP LOCKED — but it makes
// a stuck campaign traceable to the worker that stalled on it.
func (r *Repo) ClaimDueCampaigns(
	ctx context.Context,
	owner uuid.UUID,
	lease time.Duration,
	limit int,
) ([]CampaignJob, error) {
	rows, err := r.pool.Query(ctx, `
		update public.content_campaigns cc
		   set status = 'running',
		       lease_owner = $1,
		       lease_expires_at = now() + make_interval(secs => $2),
		       started_at = coalesce(cc.started_at, now())
		 where cc.id in (
		   select id from public.content_campaigns
		    where status in ('scheduled', 'running')
		      and archived_at is null
		      -- A cancel pressed while a worker held the campaign cannot close
		      -- it: the row stays running with cancel_requested set, and the
		      -- worker is meant to notice and settle it. If that worker dies
		      -- first, from a restart or a send that hung, nobody else would
		      -- ever look at the row again and the campaign would read
		      -- "berjalan" for good with recipients waiting. Claiming it is
		      -- what lets the next worker finish the cancellation.
		      and (cancel_requested = false or status = 'running')
		      and coalesce(scheduled_at, now()) <= now()
		      and (lease_owner is null or lease_expires_at is null or lease_expires_at < now())
		    order by coalesce(scheduled_at, created_at)
		    limit $3
		    for update skip locked
		 )
		returning cc.id, cc.workspace_id, cc.application_id, cc.campaign_type::text,
		          cc.name, coalesce(cc.message_template, cc.body, ''), cc.compose_mode,
		          cc.media_url, cc.media_kind,
		          cc.media_storage_path, cc.media_file_name,
		          cc.caption, cc.delay_profile,
		          cc.delay_min_seconds, cc.delay_max_seconds,
		          cc.auto_retry_on_disconnect,
		          cc.max_attempts, cc.retry_gap_seconds, cc.created_by`,
		owner, lease.Seconds(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []CampaignJob
	for rows.Next() {
		var j CampaignJob
		if err := rows.Scan(&j.ID, &j.WorkspaceID, &j.ApplicationID, &j.CampaignType,
			&j.Name, &j.Template, &j.ComposeMode, &j.MediaURL, &j.MediaKind,
			&j.MediaStoragePath, &j.MediaFileName, &j.Caption,
			&j.DelayProfile, &j.DelayMinSeconds, &j.DelayMaxSeconds,
			&j.AutoRetryOnDisconnect,
			&j.MaxAttempts, &j.RetryGapSeconds, &j.CreatedBy); err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for i := range out {
		ids, err := r.CampaignDeviceIDs(ctx, out[i].ID)
		if err != nil {
			return nil, err
		}
		out[i].AccountIDs = ids
	}
	return out, nil
}

// CampaignDeviceIDs lists a campaign's sending accounts in order.
func (r *Repo) CampaignDeviceIDs(ctx context.Context, campaignID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := r.pool.Query(ctx,
		`select account_id from public.broadcast_sender_devices
		  where campaign_id = $1 order by position, created_at`, campaignID)
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

// RenewCampaignLease extends the hold while a long campaign is still running.
func (r *Repo) RenewCampaignLease(ctx context.Context, campaignID, owner uuid.UUID, lease time.Duration) error {
	_, err := r.pool.Exec(ctx,
		`update public.content_campaigns
		    set lease_expires_at = now() + make_interval(secs => $3)
		  where id = $1 and lease_owner = $2`, campaignID, owner, lease.Seconds())
	return err
}

// ReleaseCampaign drops the lease without deciding the outcome, for a worker
// shutting down mid-run. The campaign stays `running` and the next claim picks
// it up where it left off.
func (r *Repo) ReleaseCampaign(ctx context.Context, campaignID, owner uuid.UUID) error {
	_, err := r.pool.Exec(ctx,
		`update public.content_campaigns
		    set lease_owner = null, lease_expires_at = null
		  where id = $1 and lease_owner = $2`, campaignID, owner)
	return err
}

// CancelRequested reports whether somebody pressed cancel while this campaign
// was running. Read between targets, which is the only safe place to stop:
// interrupting a send in flight would leave a recipient whose state nobody knows.
func (r *Repo) CancelRequested(ctx context.Context, campaignID uuid.UUID) (bool, error) {
	var requested bool
	err := r.pool.QueryRow(ctx,
		`select cancel_requested from public.content_campaigns where id = $1`,
		campaignID).Scan(&requested)
	if err != nil {
		return false, mapErr(err)
	}
	return requested, nil
}

// QueuedTarget is one recipient a worker has claimed.
type QueuedTarget struct {
	ID             uuid.UUID
	CampaignID     uuid.UUID
	WorkspaceID    uuid.UUID
	AccountID      uuid.UUID
	ChatJID        string
	ContactID      *uuid.UUID
	ConversationID *uuid.UUID
	PhoneNumber    *string
	DisplayName    *string
	TargetType     string
	Attempt        int
	RenderedBody   *string
	Variables      map[string]string
}

// ClaimTargets takes the next batch of recipients for one device.
func (r *Repo) ClaimTargets(
	ctx context.Context,
	campaignID, accountID uuid.UUID,
	lease time.Duration,
	limit int,
) ([]QueuedTarget, error) {
	rows, err := r.pool.Query(ctx, `
		update public.campaign_targets t
		   set status = 'processing',
		       claimed_at = now(),
		       lease_expires_at = now() + make_interval(secs => $3),
		       attempt = t.attempt + 1
		 where t.id in (
		   select id from public.campaign_targets
		    where campaign_id = $1 and account_id = $2
		      and status in ('pending', 'retry_wait')
		      and coalesce(next_attempt_at, now()) <= now()
		    order by created_at
		    limit $4
		    for update skip locked
		 )
		returning t.id, t.campaign_id, t.workspace_id, t.account_id, t.chat_jid,
		          t.contact_id, t.conversation_id, t.phone_number, t.display_name,
		          t.target_type, t.attempt, t.rendered_body, t.variables`,
		campaignID, accountID, lease.Seconds(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []QueuedTarget{}
	for rows.Next() {
		var t QueuedTarget
		var raw []byte
		if err := rows.Scan(&t.ID, &t.CampaignID, &t.WorkspaceID, &t.AccountID, &t.ChatJID,
			&t.ContactID, &t.ConversationID, &t.PhoneNumber, &t.DisplayName,
			&t.TargetType, &t.Attempt, &t.RenderedBody, &raw); err != nil {
			return nil, err
		}
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &t.Variables)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ErrAlreadySent reports that this (campaign, device, target) already has a
// successful attempt. Raised by the unique index, not by a check in Go — which
// is the point: it holds even when two workers reach the same row at once.
var ErrAlreadySent = fmt.Errorf("repository: target sudah pernah terkirim")

// BeginAttempt records that a send is about to happen.
//
// Written BEFORE the network call, so a crash between here and the response
// leaves evidence that an attempt was in flight. Reconciliation later uses that
// row to check whether the message actually went, instead of assuming it did not
// and sending a second copy.
func (r *Repo) BeginAttempt(
	ctx context.Context,
	t QueuedTarget,
	waMessageID string,
) (uuid.UUID, error) {
	var id uuid.UUID
	err := r.pool.QueryRow(ctx, `
		insert into public.broadcast_target_attempts
			(workspace_id, campaign_id, target_id, account_id, attempt, status, wa_message_id)
		values ($1, $2, $3, $4, $5, 'sending', $6)
		returning id`,
		t.WorkspaceID, t.CampaignID, t.ID, t.AccountID, t.Attempt, waMessageID).Scan(&id)
	if err != nil {
		var pgErr *pgconn.PgError
		if asPgError(err, &pgErr) && pgErr.Code == "23505" {
			return uuid.Nil, ErrAlreadySent
		}
		return uuid.Nil, err
	}
	return id, nil
}

// FinishAttemptSent records a delivery and moves the target on.
func (r *Repo) FinishAttemptSent(
	ctx context.Context,
	attemptID uuid.UUID,
	t QueuedTarget,
	waMessageID string,
	messageID *uuid.UUID,
	at time.Time,
) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `
		update public.broadcast_target_attempts
		   set status = 'sent', finished_at = now(), wa_message_id = $2, message_id = $3
		 where id = $1`, attemptID, waMessageID, messageID); err != nil {
		var pgErr *pgconn.PgError
		if asPgError(err, &pgErr) && pgErr.Code == "23505" {
			return ErrAlreadySent
		}
		return err
	}
	if _, err := tx.Exec(ctx, `
		update public.campaign_targets
		   set status = 'sent', sent_at = $2, wa_message_id = $3, message_id = $4,
		       lease_expires_at = null, failure_reason = null, error_code = null,
		       next_attempt_at = null
		 where id = $1`, t.ID, at, waMessageID, messageID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		update public.broadcast_sender_devices
		   set sent_count = sent_count + 1
		 where campaign_id = $1 and account_id = $2`, t.CampaignID, t.AccountID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`update public.content_campaigns set success_count = success_count + 1 where id = $1`,
		t.CampaignID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// FinishAttemptFailed records a failure and either schedules a retry or gives up.
//
// Retries are spaced by the campaign's own gap and bounded by its own attempt
// limit, both frozen on the campaign row — so editing the defaults later does
// not change how a campaign already in flight behaves.
func (r *Repo) FinishAttemptFailed(
	ctx context.Context,
	attemptID uuid.UUID,
	t QueuedTarget,
	maxAttempts, retryGapSeconds int,
	errorCode, reason string,
) (retrying bool, err error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if attemptID != uuid.Nil {
		if _, err := tx.Exec(ctx, `
			update public.broadcast_target_attempts
			   set status = 'failed', finished_at = now(),
			       error_code = nullif($2, ''), failure_reason = nullif($3, '')
			 where id = $1`, attemptID, errorCode, reason); err != nil {
			return false, err
		}
	}

	retrying = t.Attempt < maxAttempts
	status := models.TargetFailed
	var next *time.Time
	if retrying {
		status = models.TargetRetryWait
		at := time.Now().UTC().Add(time.Duration(retryGapSeconds) * time.Second)
		next = &at
	}

	if _, err := tx.Exec(ctx, `
		update public.campaign_targets
		   set status = $2, next_attempt_at = $3, lease_expires_at = null,
		       error_code = nullif($4, ''), failure_reason = nullif($5, '')
		 where id = $1`, t.ID, status, next, errorCode, reason); err != nil {
		return false, err
	}

	if !retrying {
		if _, err := tx.Exec(ctx, `
			update public.broadcast_sender_devices
			   set failed_count = failed_count + 1
			 where campaign_id = $1 and account_id = $2`, t.CampaignID, t.AccountID); err != nil {
			return false, err
		}
		if _, err := tx.Exec(ctx,
			`update public.content_campaigns set failed_count = failed_count + 1 where id = $1`,
			t.CampaignID); err != nil {
			return false, err
		}
	}
	return retrying, tx.Commit(ctx)
}

// ErrCodeUnknownOutcome marks a target whose send neither succeeded nor failed
// as far as this server can tell.
const ErrCodeUnknownOutcome = "unknown_outcome"

// ReconcileStuckTargets settles recipients a dead worker left in flight.
//
// This is the rule the specification asks for in as many words: when the outcome
// of a send is unknown, find out before sending again — and if it cannot be
// found out, do not send again on your own.
//
// The evidence is the message row's status. Every campaign send writes a
// `pending` message row before the network call and moves it to `sent` after,
// and WhatsApp's own delivered/read receipts move it further still. So:
//
//	status past 'pending'  → the message demonstrably left. Marked sent.
//	status still 'pending' → nobody knows. Marked failed with
//	                         ErrCodeUnknownOutcome and NOT retried automatically.
//
// The second case is the important one. Retrying it might send a second copy of
// the same message to a customer who already has it; leaving it for a person to
// decide costs a click and cannot. The report names those targets explicitly so
// the decision is an informed one rather than a guess.
//
// Returns how many were settled as sent and how many were left for a human.
func (r *Repo) ReconcileStuckTargets(ctx context.Context, campaignID uuid.UUID) (sent, unknown int, err error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Evidence found: promote both the attempt and the target.
	tag, err := tx.Exec(ctx, `
		with landed as (
			select a.id as attempt_id, t.id as target_id, a.wa_message_id, m.id as message_id,
			       m.timestamp as landed_at
			  from public.broadcast_target_attempts a
			  join public.campaign_targets t on t.id = a.target_id
			  join public.messages m
			    on m.account_id = a.account_id and m.wa_message_id = a.wa_message_id
			 where a.campaign_id = $1
			   and a.status = 'sending'
			   and t.status = 'processing'
			   and t.lease_expires_at is not null and t.lease_expires_at < now()
			   and m.status <> 'pending' and m.status <> 'failed'
		),
		fix_attempt as (
			update public.broadcast_target_attempts a
			   set status = 'sent', finished_at = now(), message_id = landed.message_id
			  from landed where a.id = landed.attempt_id
		)
		update public.campaign_targets t
		   set status = 'sent', sent_at = landed.landed_at, wa_message_id = landed.wa_message_id,
		       message_id = landed.message_id, lease_expires_at = null, next_attempt_at = null
		  from landed where t.id = landed.target_id`, campaignID)
	if err != nil {
		return 0, 0, err
	}
	sent = int(tag.RowsAffected())

	if _, err := tx.Exec(ctx, `
		update public.broadcast_target_attempts a
		   set status = 'failed', finished_at = now(),
		       error_code = $2,
		       failure_reason = 'Worker berhenti sebelum hasil pengiriman diketahui.'
		  from public.campaign_targets t
		 where a.target_id = t.id and a.campaign_id = $1 and a.status = 'sending'
		   and t.status = 'processing'
		   and t.lease_expires_at is not null and t.lease_expires_at < now()`,
		campaignID, ErrCodeUnknownOutcome); err != nil {
		return 0, 0, err
	}

	tag, err = tx.Exec(ctx, `
		update public.campaign_targets
		   set status = 'failed', lease_expires_at = null, next_attempt_at = null,
		       error_code = $2,
		       failure_reason = 'Hasil pengiriman tidak diketahui. Periksa percakapan sebelum mencoba ulang.'
		 where campaign_id = $1 and status = 'processing'
		   and lease_expires_at is not null and lease_expires_at < now()`,
		campaignID, ErrCodeUnknownOutcome)
	if err != nil {
		return 0, 0, err
	}
	unknown = int(tag.RowsAffected())

	if err := tx.Commit(ctx); err != nil {
		return 0, 0, err
	}
	return sent, unknown, nil
}

// PendingTargetCount reports how much of a campaign is still to do.
func (r *Repo) PendingTargetCount(ctx context.Context, campaignID uuid.UUID) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx, `
		select count(*) from public.campaign_targets
		 where campaign_id = $1 and status in ('pending', 'retry_wait', 'processing')`,
		campaignID).Scan(&n)
	return n, err
}

// FinishCampaign settles the campaign row from its recipients' outcomes.
//
// The status is derived, never asserted: some sent and some failed is `partial`,
// and calling that "completed" would hide the failures while calling it "failed"
// would invite somebody to run the whole thing again.
func (r *Repo) FinishCampaign(ctx context.Context, campaignID uuid.UUID) (string, error) {
	var status string
	err := r.pool.QueryRow(ctx, `
		with tally as (
			select count(*) filter (where status in ('sent', 'delivered', 'read')) as ok,
			       count(*) filter (where status in ('failed', 'invalid')) as bad,
			       count(*) filter (where status = 'cancelled') as cancelled,
			       count(*) as total
			  from public.campaign_targets where campaign_id = $1
		)
		update public.content_campaigns cc
		   set status = (case
		         when cc.cancel_requested then 'cancelled'
		         when tally.total = 0 then 'failed'
		         when tally.ok = 0 and tally.bad > 0 then 'failed'
		         when tally.bad > 0 or tally.cancelled > 0 then 'partial'
		         else 'completed'
		       end)::public.campaign_status,
		       success_count = tally.ok,
		       failed_count = tally.bad,
		       -- A campaign with no recipients at all says so, rather than
		       -- failing with the reason column blank.
		       failure_reason = case
		         when tally.total = 0
		           then 'Tidak ada penerima yang bisa dikirimi: semua grup/kontak yang dipilih tidak terjangkau perangkat pengirim.'
		         else cc.failure_reason end,
		       finished_at = now(),
		       executed_at = coalesce(cc.executed_at, now()),
		       lease_owner = null,
		       lease_expires_at = null,
		       cancelled_at = case when cc.cancel_requested then now() else cc.cancelled_at end
		  from tally
		 where cc.id = $1
		returning cc.status::text`, campaignID).Scan(&status)
	if err != nil {
		return "", mapErr(err)
	}

	// Devices are settled from their own tallies for the same reason.
	if _, err := r.pool.Exec(ctx, `
		update public.broadcast_sender_devices d
		   set status = (case
		         when x.pending > 0 then 'running'
		         when x.ok = 0 and x.bad > 0 then 'failed'
		         else 'done'
		       end),
		       sent_count = x.ok,
		       failed_count = x.bad,
		       finished_at = case when x.pending = 0 then now() else null end
		  from (select account_id,
		               count(*) filter (where status in ('sent','delivered','read')) as ok,
		               count(*) filter (where status in ('failed','invalid')) as bad,
		               count(*) filter (where status in ('pending','retry_wait','processing')) as pending
		          from public.campaign_targets
		         where campaign_id = $1 and account_id is not null
		         group by account_id) x
		 where d.campaign_id = $1 and d.account_id = x.account_id`, campaignID); err != nil {
		return status, err
	}
	return status, nil
}

// FailCampaign records a failure that stopped the whole run before any
// recipient could be attempted — no devices selected, media unreachable.
func (r *Repo) FailCampaign(ctx context.Context, campaignID uuid.UUID, reason string) error {
	_, err := r.pool.Exec(ctx, `
		update public.content_campaigns
		   set status = 'failed', failure_reason = $2, finished_at = now(),
		       lease_owner = null, lease_expires_at = null
		 where id = $1`, campaignID, reason)
	return err
}

// MarkDeviceRunning stamps the moment a device started sending.
func (r *Repo) MarkDeviceRunning(ctx context.Context, campaignID, accountID uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `
		update public.broadcast_sender_devices
		   set status = 'running', started_at = coalesce(started_at, now())
		 where campaign_id = $1 and account_id = $2 and status = 'pending'`,
		campaignID, accountID)
	return err
}

// MarkDeviceFailed records that a whole device could not be used — offline, or
// logged out — without touching the recipients, which may still be reassigned.
func (r *Repo) MarkDeviceFailed(ctx context.Context, campaignID, accountID uuid.UUID, reason string) error {
	_, err := r.pool.Exec(ctx, `
		update public.broadcast_sender_devices
		   set status = 'failed', finished_at = now(), failure_reason = $3
		 where campaign_id = $1 and account_id = $2`, campaignID, accountID, reason)
	return err
}

// AbandonDeviceShare releases the recipients still waiting on one number, for a
// campaign whose operator turned auto-retry off.
//
// Marked failed with a stated reason rather than deleted or left pending:
// deleting them would make the report claim a campaign reached fewer people than
// it was aimed at, and leaving them pending would make it look like it is still
// running when nothing is going to happen. "Failed with a reason" is the only
// one of the three that is true.
//
// Already-sent recipients are untouched. Returns how many were released.
func (r *Repo) AbandonDeviceShare(
	ctx context.Context, campaignID, accountID uuid.UUID, reason string,
) (int64, error) {
	tag, err := r.pool.Exec(ctx, `
		update public.campaign_targets
		   set status = 'failed',
		       failure_reason = $3,
		       lease_expires_at = null,
		       next_attempt_at = null
		 where campaign_id = $1 and account_id = $2
		   and status in ('pending', 'retry_wait', 'processing')`,
		campaignID, accountID, reason)
	if err != nil {
		return 0, err
	}
	if err := r.MarkDeviceFailed(ctx, campaignID, accountID, reason); err != nil {
		return tag.RowsAffected(), err
	}
	return tag.RowsAffected(), nil
}

// ResetTargetsForResend puts every recipient back in the queue.
//
// Distinct from retry, which only picks up the ones that failed. This is the
// operator saying "send the whole thing again", and it is the more dangerous of
// the two: everybody who already received the message receives it a second time.
// The interface asks before calling it; this function only does what it says.
//
// The previous attempt's outcome is not preserved on the row — status, failure
// reason and timestamps are cleared — because the row is the current state of
// one recipient, and the history of the run lives in the campaign's own record.
func (r *Repo) ResetTargetsForResend(ctx context.Context, campaignID uuid.UUID) (int64, error) {
	tag, err := r.pool.Exec(ctx, `
		update public.campaign_targets
		   set status = 'pending',
		       attempt = 0,
		       failure_reason = null,
		       error_code = null,
		       sent_at = null,
		       delivered_at = null,
		       read_at = null,
		       cancelled_at = null,
		       wa_message_id = null,
		       rendered_body = null,
		       next_attempt_at = null,
		       claimed_at = null,
		       lease_expires_at = null
		 where campaign_id = $1`, campaignID)
	if err != nil {
		return 0, err
	}

	if _, err := r.pool.Exec(ctx, `
		update public.broadcast_sender_devices
		   set status = 'pending', started_at = null, finished_at = null,
		       failure_reason = null, sent_count = 0, failed_count = 0
		 where campaign_id = $1`, campaignID); err != nil {
		return tag.RowsAffected(), err
	}

	_, err = r.pool.Exec(ctx, `
		update public.content_campaigns
		   set status = 'scheduled', scheduled_at = now(),
		       success_count = 0, failed_count = 0,
		       started_at = null, finished_at = null, cancelled_at = null,
		       cancel_requested = false, failure_reason = null,
		       lease_owner = null, lease_expires_at = null
		 where id = $1`, campaignID)
	return tag.RowsAffected(), err
}

// MarkDocumentPurged records that a campaign's uploaded document is gone.
//
// The path and the filename stay on the row: the report has to be able to say
// what was sent, and a name is not a file.
func (r *Repo) MarkDocumentPurged(ctx context.Context, campaignID uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `
		update public.content_campaigns
		   set media_purged_at = now()
		 where id = $1 and media_purged_at is null`, campaignID)
	return err
}

// RecurringSeries describes the schedule a campaign belongs to, or nil when it
// is a one-off.
type RecurringSeries struct {
	Frequency string
	Hour      int
	Minute    int
	Weekday   int
	Day       int
	Until     *time.Time
}

// RecurringSeries reads the recurrence rule of a campaign, if it has one.
//
// Returns nil rather than an error for a one-off campaign: most campaigns are
// one-offs, and the caller asks about every single one as it finishes.
func (r *Repo) RecurringSeries(ctx context.Context, campaignID uuid.UUID) (*RecurringSeries, error) {
	var (
		freq    *string
		at      *time.Time
		weekday *int16
		day     *int16
		until   *time.Time
	)
	err := r.pool.QueryRow(ctx, `
		select recurrence,
		       -- Read back as a timestamp on an arbitrary date: pgx has no time
		       -- type of its own, and only the hour and minute are wanted.
		       (date '2000-01-01' + recurrence_time)::timestamp,
		       recurrence_weekday, recurrence_day, recurrence_until
		  from public.content_campaigns
		 where id = $1`, campaignID).Scan(&freq, &at, &weekday, &day, &until)
	if err != nil {
		return nil, mapErr(err)
	}
	if freq == nil || *freq == "" || at == nil {
		return nil, nil
	}

	out := &RecurringSeries{Frequency: *freq, Hour: at.Hour(), Minute: at.Minute(), Until: until}
	if weekday != nil {
		out.Weekday = int(*weekday)
	}
	if day != nil {
		out.Day = int(*day)
	}
	return out, nil
}

// CloneCampaignForNextRun copies a finished campaign into its next occurrence.
//
// The copy carries the message, the media, the sending numbers, the delay
// settings and the recipient list, with every per-run fact reset: no counts, no
// lease, no timestamps, and every recipient back to pending. The original keeps
// its report untouched, which is the whole reason this is a copy.
//
// recurrence_spawned_at on the source is set inside the same transaction and
// checked before copying, so two workers finishing the same campaign cannot
// produce two occurrences of it.
func (r *Repo) CloneCampaignForNextRun(
	ctx context.Context, sourceID uuid.UUID, at time.Time,
) (uuid.UUID, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Claim the right to spawn. Nothing happens if somebody already did.
	var claimed bool
	if err := tx.QueryRow(ctx, `
		update public.content_campaigns
		   set recurrence_spawned_at = now()
		 where id = $1 and recurrence is not null and recurrence_spawned_at is null
		returning true`, sourceID).Scan(&claimed); err != nil {
		if isNoRows(err) {
			return uuid.Nil, nil
		}
		return uuid.Nil, err
	}

	var newID uuid.UUID
	if err := tx.QueryRow(ctx, `
		insert into public.content_campaigns
			(workspace_id, application_id, account_id, campaign_type, name, body,
			 status, scheduled_at, target_count, created_by, creator_role, creator_pic_id,
			 delay_profile, compose_mode, message_template, media_url, media_kind,
			 media_mime, media_size_bytes, media_sha256, caption, target_source,
			 max_attempts, retry_gap_seconds,
			 delay_min_seconds, delay_max_seconds, auto_retry_on_disconnect,
			 recurrence, recurrence_until, recurrence_time, recurrence_weekday,
			 recurrence_day, recurrence_parent_id,
			 media_storage_path, media_file_name)
		select workspace_id, application_id, account_id, campaign_type, name, body,
		       'scheduled'::public.campaign_status, $2, target_count, created_by,
		       creator_role, creator_pic_id,
		       delay_profile, compose_mode, message_template, media_url, media_kind,
		       media_mime, media_size_bytes, media_sha256, caption, target_source,
		       max_attempts, retry_gap_seconds,
		       delay_min_seconds, delay_max_seconds, auto_retry_on_disconnect,
		       recurrence, recurrence_until, recurrence_time, recurrence_weekday,
		       recurrence_day, coalesce(recurrence_parent_id, id),
		       media_storage_path, media_file_name
		  from public.content_campaigns
		 where id = $1
		returning id`, sourceID, at).Scan(&newID); err != nil {
		return uuid.Nil, err
	}

	if _, err := tx.Exec(ctx, `
		insert into public.broadcast_sender_devices
			(workspace_id, campaign_id, account_id, position)
		select workspace_id, $2, account_id, position
		  from public.broadcast_sender_devices
		 where campaign_id = $1`, sourceID, newID); err != nil {
		return uuid.Nil, err
	}

	// Recipients are copied as they were resolved, not re-resolved. A series
	// whose audience silently changed between runs would make "who got this"
	// unanswerable, and re-resolving is a decision for the operator to make by
	// editing the campaign rather than one to take on their behalf overnight.
	if _, err := tx.Exec(ctx, `
		insert into public.campaign_targets
			(workspace_id, campaign_id, account_id, contact_id, conversation_id,
			 chat_jid, phone_number, display_name, target_type, variables,
			 rendered_body, status, attempt)
		select workspace_id, $2, account_id, contact_id, conversation_id,
		       chat_jid, phone_number, display_name, target_type, variables,
		       -- Cleared so spintax is spun again for the new run: a weekly
		       -- broadcast that sends a byte-identical message every week is the
		       -- pattern spintax exists to avoid.
		       null, 'pending', 0
		  from public.campaign_targets
		 where campaign_id = $1`, sourceID, newID); err != nil {
		return uuid.Nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, err
	}
	return newID, nil
}

// --- operator actions --------------------------------------------------------

// RequestCancel asks a running campaign to stop.
//
// Cooperative on purpose. Recipients already sent stay sent — there is no
// unsending a WhatsApp message, and pretending otherwise in the interface would
// be a lie — while everything untouched is marked cancelled straight away.
func (r *Repo) RequestCancel(
	ctx context.Context, workspaceID, campaignID, actor uuid.UUID,
) (*models.Campaign, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// A running campaign is finished here unless a worker is actually holding it.
	//
	// Cancellation is cooperative: the flag goes up and the worker reads it
	// between two recipients. That only works while a worker has the campaign.
	// With no live lease nobody was going to read the flag — and ClaimDueCampaigns
	// refuses to pick the campaign up precisely because the flag is now raised,
	// so nothing ever would. The campaign sat at "Sedang Dikirim" for good: its
	// recipients cancelled, its work over, its status permanently wrong, and
	// undeletable because only drafts may be deleted.
	tag, err := tx.Exec(ctx, `
		update public.content_campaigns
		   set cancel_requested = true,
		       status = case
		                  when status in ('draft', 'scheduled')
		                    then 'cancelled'::public.campaign_status
		                  when lease_owner is null or lease_expires_at is null
		                       or lease_expires_at < now()
		                    then 'cancelled'::public.campaign_status
		                  else status
		                end,
		       finished_at = case
		                       when lease_owner is null or lease_expires_at is null
		                            or lease_expires_at < now()
		                         then coalesce(finished_at, now())
		                       else finished_at
		                     end,
		       cancelled_at = coalesce(cancelled_at, now())
		 where id = $1 and workspace_id = $2
		   and status in ('draft', 'scheduled', 'running')`, campaignID, workspaceID)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrNotFound
	}

	if _, err := tx.Exec(ctx, `
		update public.campaign_targets
		   set status = 'cancelled', cancelled_at = now(), next_attempt_at = null
		 where campaign_id = $1 and status in ('pending', 'retry_wait')`, campaignID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
		update public.scheduled_publications
		   set status = 'cancelled', superseded_at = now()
		 where campaign_id = $1 and superseded_at is null`, campaignID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
		update public.story_publications
		   set status = 'cancelled', next_attempt_at = null
		 where campaign_id = $1 and status in ('pending', 'processing')`, campaignID); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return r.GetCampaign(ctx, workspaceID, campaignID)
}

// CancelRemainingWork closes off whatever a cancelled campaign never sent.
//
// Separate from RequestCancel because the two answer different moments: that
// one runs when the operator presses cancel, this one when a worker picks the
// campaign up afterwards and has to leave it in a state the report can explain.
// Recipients still `processing` are included, because by the time this is
// called no device is working and a processing row is the remains of a worker
// that stopped.
func (r *Repo) CancelRemainingWork(ctx context.Context, campaignID uuid.UUID) (int, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx, `
		update public.campaign_targets
		   set status = 'cancelled', cancelled_at = now(),
		       next_attempt_at = null, lease_expires_at = null
		 where campaign_id = $1
		   and status in ('pending', 'retry_wait', 'processing')`, campaignID)
	if err != nil {
		return 0, err
	}
	n := int(tag.RowsAffected())

	if _, err := tx.Exec(ctx, `
		update public.story_publications
		   set status = 'cancelled', next_attempt_at = null, lease_expires_at = null
		 where campaign_id = $1 and status in ('pending', 'processing')`,
		campaignID); err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return n, nil
}

// RetryFailed requeues the recipients that did not make it.
//
// Only those. A target already sent is untouched, and its attempt row stays
// where it is — which is what stops "retry" from meaning "send the whole thing
// again to everybody".
func (r *Repo) RetryFailed(ctx context.Context, workspaceID, campaignID uuid.UUID) (int, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var owns bool
	if err := tx.QueryRow(ctx,
		`select true from public.content_campaigns where id = $1 and workspace_id = $2`,
		campaignID, workspaceID).Scan(&owns); err != nil {
		return 0, mapErr(err)
	}

	tag, err := tx.Exec(ctx, `
		update public.campaign_targets
		   set status = 'retry_wait', next_attempt_at = now(), attempt = 0,
		       failure_reason = null, error_code = null, lease_expires_at = null
		 where campaign_id = $1 and status in ('failed', 'cancelled')`, campaignID)
	if err != nil {
		return 0, err
	}
	n := int(tag.RowsAffected())
	if n == 0 {
		return 0, tx.Commit(ctx)
	}

	// The campaign goes back to running with its cancel flag cleared, and the
	// failed tally is reset for the rows about to be tried again.
	if _, err := tx.Exec(ctx, `
		update public.content_campaigns
		   set status = 'running', cancel_requested = false, finished_at = null,
		       failed_count = greatest(failed_count - $2, 0),
		       lease_owner = null, lease_expires_at = null,
		       scheduled_at = case when scheduled_at is null then null else now() end
		 where id = $1`, campaignID, n); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `
		update public.broadcast_sender_devices
		   set status = 'pending', finished_at = null
		 where campaign_id = $1 and status in ('done', 'failed', 'cancelled')`,
		campaignID); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `
		update public.story_publications
		   set status = 'pending', next_attempt_at = now(), attempt = 0,
		       failure_reason = null, error_code = null, lease_expires_at = null
		 where campaign_id = $1 and status in ('failed', 'cancelled')`,
		campaignID); err != nil {
		return 0, err
	}
	return n, tx.Commit(ctx)
}

// RunNow schedules a draft to start immediately.
func (r *Repo) RunNow(ctx context.Context, workspaceID, campaignID uuid.UUID) (*models.Campaign, error) {
	tag, err := r.pool.Exec(ctx, `
		update public.content_campaigns
		   set status = 'scheduled', scheduled_at = now(), cancel_requested = false,
		       cancelled_at = null, failure_reason = null,
		       lease_owner = null, lease_expires_at = null
		 where id = $1 and workspace_id = $2 and status in ('draft', 'cancelled', 'failed', 'partial')`,
		campaignID, workspaceID)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrNotFound
	}
	return r.GetCampaign(ctx, workspaceID, campaignID)
}

// --- reporting ---------------------------------------------------------------

// CampaignDevices lists the sending numbers with their tallies.
func (r *Repo) CampaignDevices(ctx context.Context, campaignID uuid.UUID) ([]models.BroadcastDevice, error) {
	rows, err := r.pool.Query(ctx, `
		select d.id, d.campaign_id, d.account_id, a.name, a.phone_number, d.position,
		       d.status, d.assigned_count, d.sent_count, d.failed_count,
		       d.started_at, d.finished_at, d.failure_reason
		  from public.broadcast_sender_devices d
		  join public.whatsapp_accounts a on a.id = d.account_id
		 where d.campaign_id = $1
		 order by d.position, a.name`, campaignID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []models.BroadcastDevice{}
	for rows.Next() {
		var d models.BroadcastDevice
		if err := rows.Scan(&d.ID, &d.CampaignID, &d.AccountID, &d.AccountName, &d.PhoneNumber,
			&d.Position, &d.Status, &d.AssignedCount, &d.SentCount, &d.FailedCount,
			&d.StartedAt, &d.FinishedAt, &d.FailureReason); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// TargetTotals counts a campaign's recipients by state.
func (r *Repo) TargetTotals(ctx context.Context, campaignID uuid.UUID) (models.TargetTotals, error) {
	var t models.TargetTotals
	err := r.pool.QueryRow(ctx, `
		select count(*),
		       count(*) filter (where status <> 'invalid'),
		       count(*) filter (where status = 'invalid'),
		       count(*) filter (where status = 'pending'),
		       count(*) filter (where status = 'processing'),
		       count(*) filter (where status in ('sent', 'delivered', 'read')),
		       count(*) filter (where status in ('delivered', 'read')),
		       count(*) filter (where status = 'read'),
		       count(*) filter (where status = 'failed'),
		       count(*) filter (where status = 'cancelled'),
		       count(*) filter (where status = 'retry_wait'),
		       coalesce((select count(*) from public.broadcast_target_attempts a
		                  where a.campaign_id = $1 and a.attempt > 1), 0)
		  from public.campaign_targets where campaign_id = $1`, campaignID).
		Scan(&t.Total, &t.Valid, &t.Invalid, &t.Pending, &t.Processing, &t.Sent,
			&t.Delivered, &t.Read, &t.Failed, &t.Cancelled, &t.RetryWait, &t.Retries)
	return t, err
}

// CampaignReplies counts inbound messages from targeted contacts that arrived
// after their campaign message.
//
// Those messages are ordinary inbound messages everywhere else — they count as
// Pesan Masuk, they open an SLA cycle, they are not campaign traffic. This
// figure only answers "how many wrote back", which is a question about the
// campaign rather than a redefinition of the chat metrics.
func (r *Repo) CampaignReplies(ctx context.Context, campaignID uuid.UUID) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx, `
		select count(distinct t.id)
		  from public.campaign_targets t
		  join public.messages m on m.conversation_id = t.conversation_id
		 where t.campaign_id = $1
		   and t.conversation_id is not null
		   and t.sent_at is not null
		   and not m.from_me
		   and m.hidden_at is null
		   and m.timestamp > t.sent_at`, campaignID).Scan(&n)
	return n, err
}

// ListTargets pages through a campaign's recipients.
func (r *Repo) ListTargets(
	ctx context.Context,
	campaignID uuid.UUID,
	status string,
	limit, offset int,
) ([]models.CampaignTarget, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := &queryArgs{}
	where := fmt.Sprintf(" where t.campaign_id = %s", q.add(campaignID))
	switch status {
	case "":
	case "sent":
		where += " and t.status in ('sent', 'delivered', 'read')"
	default:
		where += fmt.Sprintf(" and t.status = %s", q.add(status))
	}

	rows, err := r.pool.Query(ctx, `
		select t.id, t.campaign_id, t.contact_id, t.conversation_id, t.account_id, a.name,
		       t.chat_jid, t.target_type, t.phone_number, t.display_name, t.status,
		       t.attempt, t.rendered_body, t.wa_message_id, t.sent_at, t.delivered_at,
		       t.read_at, t.next_attempt_at, t.failure_reason, t.error_code, t.invalid_reason,
		       exists (select 1 from public.messages m
		                where m.conversation_id = t.conversation_id
		                  and not m.from_me and m.hidden_at is null
		                  and t.sent_at is not null and m.timestamp > t.sent_at)
		  from public.campaign_targets t
		  left join public.whatsapp_accounts a on a.id = t.account_id`+where+
		fmt.Sprintf(" order by t.created_at limit %d offset %d", limit, offset), q.args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []models.CampaignTarget{}
	for rows.Next() {
		var t models.CampaignTarget
		if err := rows.Scan(&t.ID, &t.CampaignID, &t.ContactID, &t.ConversationID, &t.AccountID,
			&t.AccountName, &t.ChatJID, &t.TargetType, &t.PhoneNumber, &t.DisplayName, &t.Status,
			&t.Attempt, &t.RenderedBody, &t.WAMessageID, &t.SentAt, &t.DeliveredAt,
			&t.ReadAt, &t.NextAttemptAt, &t.FailureReason, &t.ErrorCode, &t.InvalidReason,
			&t.Replied); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// CampaignDuration is wall-clock from the first send to the last, in seconds.
func (r *Repo) CampaignDuration(ctx context.Context, campaignID uuid.UUID) (*int, error) {
	var secs *float64
	err := r.pool.QueryRow(ctx, `
		select extract(epoch from (max(sent_at) - min(sent_at)))
		  from public.campaign_targets where campaign_id = $1 and sent_at is not null`,
		campaignID).Scan(&secs)
	if err != nil || secs == nil {
		return nil, err
	}
	n := int(*secs + 0.5)
	return &n, nil
}

// AdvanceCampaignTargets applies a WhatsApp receipt to the campaign rows.
//
// Called from the same receipt handler that updates the chat, so delivered and
// read on a campaign report mean exactly what they mean on a message bubble.
func (r *Repo) AdvanceCampaignTargets(
	ctx context.Context,
	accountID uuid.UUID,
	waMessageIDs []string,
	status string,
	at time.Time,
) error {
	if len(waMessageIDs) == 0 {
		return nil
	}
	col := "delivered_at"
	if status == models.MessageStatusRead {
		col = "read_at"
	}
	// Never moves backwards: a delivered receipt arriving after a read one must
	// not undo the read.
	_, err := r.pool.Exec(ctx, fmt.Sprintf(`
		update public.campaign_targets
		   set %s = coalesce(%s, $3),
		       status = case
		         when $4 = 'read' then 'read'
		         when status = 'read' then 'read'
		         else 'delivered' end
		 where account_id = $1 and wa_message_id = any($2)
		   and status in ('sent', 'delivered', 'read')`, col, col),
		accountID, waMessageIDs, at, status)
	return err
}

// --- helpers -----------------------------------------------------------------

func nullIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func nullUUID(id uuid.UUID) *uuid.UUID {
	if id == uuid.Nil {
		return nil
	}
	return &id
}
