package repository

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/salesan/omnichannel/backend/internal/models"
)

// WA Story: publication rows and detected views.
//
// The unit of work is the device, not the recipient — a Story has no recipient
// list of ours; WhatsApp decides its audience from the account's own status
// privacy settings. So one campaign fans out into one row per selected device,
// each with its own status, its own retry, and its own view tally.
//
// The view figures need care, and the care is structural rather than a comment
// somewhere: whatsmeow exposes no API for who watched a status. The only real
// signal is a read receipt addressed to status@broadcast, which a viewer with
// read receipts switched off never sends. Every number here is therefore a lower
// bound, the tables are shaped so that a duplicate receipt cannot inflate it,
// and nothing anywhere estimates the gap.

// SeedStoryPublications creates one pending row per selected device.
func (r *Repo) SeedStoryPublications(
	ctx context.Context,
	workspaceID, campaignID uuid.UUID,
	accountIDs []uuid.UUID,
) error {
	for _, id := range accountIDs {
		if _, err := r.pool.Exec(ctx, `
			insert into public.story_publications
				(workspace_id, campaign_id, account_id, status, next_attempt_at)
			values ($1, $2, $3, 'pending', now())
			on conflict (campaign_id, account_id) do nothing`,
			workspaceID, campaignID, id); err != nil {
			return err
		}
	}
	return nil
}

// QueuedPublication is one Story publication a worker has claimed.
type QueuedPublication struct {
	ID          uuid.UUID
	WorkspaceID uuid.UUID
	CampaignID  uuid.UUID
	AccountID   uuid.UUID
	Attempt     int
	// WAMessageID is set when a previous attempt already sent with it. Reusing
	// it rather than minting a new one is what makes a retry safe: WhatsApp
	// deduplicates by message id, so a re-send of a Story that did land is
	// ignored instead of appearing on the poster's phone twice.
	WAMessageID *string
}

// ClaimStoryPublications takes the next batch of device publications.
func (r *Repo) ClaimStoryPublications(
	ctx context.Context,
	campaignID uuid.UUID,
	lease time.Duration,
	limit int,
) ([]QueuedPublication, error) {
	rows, err := r.pool.Query(ctx, `
		update public.story_publications p
		   set status = 'processing', claimed_at = now(),
		       lease_expires_at = now() + make_interval(secs => $2),
		       attempt = p.attempt + 1
		 where p.id in (
		   select id from public.story_publications
		    where campaign_id = $1
		      and status in ('pending', 'processing')
		      and coalesce(next_attempt_at, now()) <= now()
		      and (lease_expires_at is null or lease_expires_at < now())
		    order by created_at
		    limit $3
		    for update skip locked
		 )
		returning p.id, p.workspace_id, p.campaign_id, p.account_id, p.attempt,
		          p.wa_message_id`,
		campaignID, lease.Seconds(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []QueuedPublication{}
	for rows.Next() {
		var p QueuedPublication
		if err := rows.Scan(&p.ID, &p.WorkspaceID, &p.CampaignID, &p.AccountID, &p.Attempt,
			&p.WAMessageID); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// StartStoryPublication writes down the message id a publication is about to
// use, before it is used.
//
// The queue has to record what it is about to do before doing it. Without this
// the only evidence a Story was ever sent is the row written afterwards, and
// anything that interrupts the gap — a failed write, a cancelled context, a
// restart — loses the fact entirely. The lease then expires, the publication is
// claimed again, and the same Story is posted to the same number a second time.
// A duplicate status is something the operator's followers see; that is a worse
// failure than a report that lags.
func (r *Repo) StartStoryPublication(
	ctx context.Context, publicationID uuid.UUID, waMessageID string,
) error {
	_, err := r.pool.Exec(ctx, `
		update public.story_publications
		   set wa_message_id = $2
		 where id = $1`, publicationID, waMessageID)
	return err
}

// MarkStoryPublished records a successful publication.
//
// expires_at is stamped from the moment it actually went out, so "kedaluwarsa"
// is derived from what happened rather than from when somebody happens to open
// the report.
func (r *Repo) MarkStoryPublished(
	ctx context.Context,
	publicationID uuid.UUID,
	waMessageID string,
	at time.Time,
) error {
	// Both casts are load-bearing. A bare $3 is deduced as timestamptz by
	// `published_at = $3` and as timestamp by `$3 + interval`, because the
	// preferred operator for an untyped left operand is timestamp + interval.
	// Postgres then refuses the whole statement with 42P08, the publication is
	// left `processing` with no outcome, and the Story reads "Sedang
	// Dipublikasikan" on the web while it is already live on the phone.
	_, err := r.pool.Exec(ctx, `
		update public.story_publications
		   set status = 'published', wa_message_id = $2,
		       published_at = $3::timestamptz,
		       expires_at = $3::timestamptz + interval '24 hours',
		       lease_expires_at = null, next_attempt_at = null,
		       failure_reason = null, error_code = null
		 where id = $1`, publicationID, waMessageID, at)
	return err
}

// RevokeStoryPublication ends a publication early because whoever posted it
// deleted it from their phone.
//
// Not "expired", which means its 24 hours ran out, and not "failed", which means
// it never went out. It did go out, and then it was taken down — and the report
// should say which of those happened.
//
// expires_at is moved to the moment of deletion, which does two things at once:
// the interface stops showing a Story as live that nobody can watch, and
// RecordStoryView refuses any receipt stamped after it, so the viewer tally
// stops where the Story stopped.
//
// A no-op when the id belongs to something other than a Story publication, so
// the revoke handler can call it for every deletion without having to know.
func (r *Repo) RevokeStoryPublication(
	ctx context.Context, accountID uuid.UUID, waMessageID string, at time.Time,
) (bool, error) {
	tag, err := r.pool.Exec(ctx, `
		update public.story_publications
		   set status           = 'deleted',
		       expires_at       = $3::timestamptz,
		       lease_expires_at = null,
		       next_attempt_at  = null
		 where account_id = $1
		   and wa_message_id = $2
		   and status in ('published', 'processing')`, accountID, waMessageID, at)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// StoryPublicationTarget is what taking a Story down needs to know: which
// number posted it, and under which message id.
type StoryPublicationTarget struct {
	ID          uuid.UUID
	CampaignID  uuid.UUID
	AccountID   uuid.UUID
	WAMessageID string
	Status      string
}

// LiveStoryPublication loads one publication that can still be taken down.
//
// Scoped by workspace in the query rather than checked afterwards: this is the
// only thing standing between a publication id and somebody else's Story.
func (r *Repo) LiveStoryPublication(
	ctx context.Context, workspaceID, publicationID uuid.UUID,
) (*StoryPublicationTarget, error) {
	var t StoryPublicationTarget
	var waID *string
	err := r.pool.QueryRow(ctx, `
		select p.id, p.campaign_id, p.account_id, p.wa_message_id, p.status
		  from public.story_publications p
		 where p.id = $1 and p.workspace_id = $2`,
		publicationID, workspaceID,
	).Scan(&t.ID, &t.CampaignID, &t.AccountID, &waID, &t.Status)
	if err != nil {
		return nil, mapErr(err)
	}
	if waID == nil || *waID == "" {
		// Nothing was ever posted under this row, so there is nothing on
		// WhatsApp to take down.
		return nil, ErrNotFound
	}
	t.WAMessageID = *waID
	return &t, nil
}

// MarkStoryFailed records a failure and schedules a retry when one is left.
func (r *Repo) MarkStoryFailed(
	ctx context.Context,
	publicationID uuid.UUID,
	attempt, maxAttempts, retryGapSeconds int,
	errorCode, reason string,
) (retrying bool, err error) {
	retrying = attempt < maxAttempts
	status := models.DeviceStateFailed
	var next *time.Time
	if retrying {
		status = "pending"
		t := time.Now().UTC().Add(time.Duration(retryGapSeconds) * time.Second)
		next = &t
	}
	_, err = r.pool.Exec(ctx, `
		update public.story_publications
		   set status = $2, next_attempt_at = $3, lease_expires_at = null,
		       error_code = nullif($4, ''), failure_reason = nullif($5, '')
		 where id = $1`, publicationID, status, next, errorCode, reason)
	return retrying, err
}

// PendingPublicationCount reports how many devices are still to publish.
func (r *Repo) PendingPublicationCount(ctx context.Context, campaignID uuid.UUID) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx, `
		select count(*) from public.story_publications
		 where campaign_id = $1 and status in ('pending', 'processing')`,
		campaignID).Scan(&n)
	return n, err
}

// FinishStoryCampaign settles a Story campaign from its publications.
func (r *Repo) FinishStoryCampaign(ctx context.Context, campaignID uuid.UUID) (string, error) {
	var status string
	err := r.pool.QueryRow(ctx, `
		with tally as (
			select count(*) filter (where status = 'published') as ok,
			       count(*) filter (where status = 'failed') as bad,
			       count(*) filter (where status = 'cancelled') as cancelled,
			       count(*) as total
			  from public.story_publications where campaign_id = $1
		)
		update public.content_campaigns cc
		   set status = (case
		         when cc.cancel_requested then 'cancelled'
		         when tally.total = 0 then 'failed'
		         when tally.ok = 0 then 'failed'
		         when tally.bad > 0 or tally.cancelled > 0 then 'partial'
		         else 'completed'
		       end)::public.campaign_status,
		       success_count = tally.ok,
		       failed_count = tally.bad,
		       finished_at = now(),
		       executed_at = coalesce(cc.executed_at, now()),
		       lease_owner = null, lease_expires_at = null
		  from tally
		 where cc.id = $1
		returning cc.status::text`, campaignID).Scan(&status)
	if err != nil {
		return "", mapErr(err)
	}
	return status, nil
}

// --- detected views ------------------------------------------------------------

// RecordStoryView notes that one viewer's receipt arrived for one of our
// statuses.
//
// One fact, one row, one key: (account, status, viewer). The status is
// identified by its WhatsApp message id, which is what it actually is — not by
// the publication row and not by our copy of the message, because those are two
// names for the same status and counting through them separately is what made
// the Story report and the chat Status panel disagree.
//
// The two lookups below are permission checks, not parents. They answer one
// question — is this our status, and is it still inside its 24 hours — and
// whichever answers it first is enough.
//
// Idempotent by construction: the same person's receipt replayed on every
// reconnect updates a timestamp and a counter instead of adding to the view
// figure. That is the whole reason the number can be trusted as far as it goes.
//
// Bounded by the Story's own 24 hours, and bounded by the receipt's TIMESTAMP
// rather than by when it reached this server. The two are not the same: a
// viewer watches while the Story is live, but the receipt can arrive much later
// — on the next reconnect, for instance, if this backend was down at the time.
// Judging by arrival would throw away real views for being reported late and
// would accept receipts stamped after a Story nobody can watch any more.
//
// Returns true when this viewer had not been seen before, so the caller knows
// whether the figure actually moved and is worth pushing to an open screen.
func (r *Repo) RecordStoryView(
	ctx context.Context,
	accountID uuid.UUID,
	waMessageID, viewerJID, receiptType string,
	at time.Time,
) (fresh bool, err error) {
	var inserted bool
	err = r.pool.QueryRow(ctx, `
		with own as (
			-- Published through the Story scheduler. Bounded by the publication's
			-- own expires_at, which RevokeStoryPublication moves forward when the
			-- poster takes the Story down early.
			select p.workspace_id
			  from public.story_publications p
			 where p.account_id = $1 and p.wa_message_id = $2
			   and (p.expires_at is null or $5::timestamptz <= p.expires_at)
			union all
			-- Posted straight from the phone. No publication exists for it, but
			-- the message does, and the receipt is just as much evidence that
			-- somebody watched.
			select c.workspace_id
			  from public.messages m
			  join public.conversations c on c.id = m.conversation_id
			 where m.account_id = $1 and m.wa_message_id = $2
			   and m.from_me and c.type = 'status'
			   and m.timestamp > $5::timestamptz - interval '24 hours'
			   and (m.revoked_at is null or $5::timestamptz <= m.revoked_at)
			limit 1
		)
		insert into public.story_views
			(workspace_id, account_id, wa_message_id,
			 viewer_jid, receipt_type, first_seen_at, last_seen_at)
		select own.workspace_id, $1, $2, $3, $4, $5::timestamptz, $5::timestamptz
		  from own
		 -- Once the figure is frozen it is finished. Without this a receipt that
		 -- arrives after the freeze would write a row nothing counts, and the
		 -- table would grow for no reader.
		 where not exists (
		   select 1 from public.story_view_snapshots s
		    where s.is_final and s.account_id = $1 and s.wa_message_id = $2)
		on conflict (account_id, wa_message_id, viewer_jid) do update
		   set last_seen_at = greatest(story_views.last_seen_at, excluded.last_seen_at),
		       receipt_count = story_views.receipt_count + 1,
		       -- "played" is a stronger signal than "read": it means a video
		       -- status was actually watched. Never downgraded.
		       receipt_type = case when excluded.receipt_type = 'played'
		                           then 'played' else story_views.receipt_type end
		returning (xmax = 0) as inserted`,
		accountID, waMessageID, viewerJID, receiptType, at).Scan(&inserted)
	if errors.Is(err, pgx.ErrNoRows) {
		// Not ours, or past its 24 hours. Not an error: receipts for other
		// people's statuses reach this account too.
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return inserted, nil
}

// StoryPublications lists a campaign's per-device rows with their view tallies.
//
// The figure comes from status_view_counts, the same view the chat Status panel
// reads. While the Story is live it is the live count; once it is frozen it is
// the frozen number and the per-viewer rows are gone. Nothing here counts
// anything itself, which is the only way two screens can be made to agree.
func (r *Repo) StoryPublications(ctx context.Context, campaignID uuid.UUID) ([]models.StoryPublication, error) {
	rows, err := r.pool.Query(ctx, `
		select p.id, p.campaign_id, p.account_id, a.name, a.phone_number, p.status,
		       p.attempt, p.wa_message_id, p.published_at, p.expires_at,
		       p.failure_reason, p.error_code,
		       coalesce(vc.viewers, 0),
		       case when vc.frozen then vc.viewers end
		  from public.story_publications p
		  join public.whatsapp_accounts a on a.id = p.account_id
		  left join public.status_view_counts vc
		         on vc.account_id = p.account_id
		        and vc.wa_message_id = p.wa_message_id
		 where p.campaign_id = $1
		 order by a.name`, campaignID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []models.StoryPublication{}
	for rows.Next() {
		var p models.StoryPublication
		if err := rows.Scan(&p.ID, &p.CampaignID, &p.AccountID, &p.AccountName, &p.PhoneNumber,
			&p.Status, &p.Attempt, &p.WAMessageID, &p.PublishedAt, &p.ExpiresAt,
			&p.FailureReason, &p.ErrorCode, &p.DetectedViews, &p.FinalViews); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// StoryViewTotals returns the two campaign-level figures, which are deliberately
// different numbers with different names.
//
// perDevice sums the publications: one person who watched the same Story on two
// of our numbers watched two Stories, and both are real. unique deduplicates by
// viewer across the campaign. Showing only one of them, or showing either under
// a generic label like "views", is how the two get read as the same thing.
//
// uniqueKnown goes false once any of the campaign's figures has been frozen.
// Keeping only the number after 24 hours means the viewer identities are gone,
// and "how many different people" cannot be recovered from a set of per-device
// totals. Reporting a smaller number instead of saying so would be inventing
// one, so the interface says so.
func (r *Repo) StoryViewTotals(
	ctx context.Context, campaignID uuid.UUID,
) (perDevice, unique int, uniqueKnown bool, err error) {
	err = r.pool.QueryRow(ctx, `
		with pub as (
			select account_id, wa_message_id
			  from public.story_publications
			 where campaign_id = $1 and wa_message_id is not null
		)
		select
			coalesce((
				select sum(vc.viewers)
				  from pub
				  join public.status_view_counts vc
				    on vc.account_id = pub.account_id
				   and vc.wa_message_id = pub.wa_message_id), 0)::int,
			coalesce((
				select count(distinct v.viewer_jid)
				  from pub
				  join public.story_views v
				    on v.account_id = pub.account_id
				   and v.wa_message_id = pub.wa_message_id), 0)::int,
			not exists (
				select 1
				  from pub
				  join public.story_view_snapshots s
				    on s.account_id = pub.account_id
				   and s.wa_message_id = pub.wa_message_id
				 where s.is_final)`, campaignID).
		Scan(&perDevice, &unique, &uniqueKnown)
	return perDevice, unique, uniqueKnown, err
}

// ExpireStories closes out statuses past their 24 hours, freezes the figure each
// one reached, and then drops the viewer rows behind it.
//
// The status flips at the 24-hour mark, because that is when the Story is
// genuinely gone. The tally freezes an hour later, because it is not: a receipt
// for a view that happened inside the window can still be on its way, and
// freezing the instant the Story expired would drop it. An hour is long enough
// for a reconnect to replay what it held, and RecordStoryView refuses anything
// stamped outside the window, so the extra hour cannot admit a view that never
// happened.
//
// Only the number survives. The per-viewer rows are deleted once the number is
// safe, which is what "ambil angkanya" means and what keeps this table from
// growing without limit across every number in the workspace. The figure itself
// is kept for good: it is our own status, and the report has to be able to say
// what it reached long after WhatsApp has forgotten the status existed.
//
// Idempotent: the unique index on (account_id, wa_message_id) where is_final
// means a second sweep cannot write a second final figure.
func (r *Repo) ExpireStories(ctx context.Context) (int, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Published through the scheduler.
	if _, err := tx.Exec(ctx, `
		insert into public.story_view_snapshots
			(workspace_id, account_id, wa_message_id, viewers, is_final)
		select p.workspace_id, p.account_id, p.wa_message_id,
		       (select count(*) from public.story_views v
		         where v.account_id = p.account_id
		           and v.wa_message_id = p.wa_message_id),
		       true
		  from public.story_publications p
		 -- 'deleted' is included so a Story taken down early still freezes the
		 -- figure it reached, rather than leaving it live forever.
		 where p.status in ('published', 'expired', 'deleted')
		   and p.wa_message_id is not null
		   and p.expires_at is not null
		   and p.expires_at <= now() - interval '1 hour'
		on conflict do nothing`); err != nil {
		return 0, err
	}

	// Posted straight from the phone. No publication row exists, so the deadline
	// comes from the message itself — or from the moment it was taken down, when
	// that came first. Without this the figure for a status posted from the phone
	// would die with the message at the 24-hour mark, while the figure for a
	// scheduled one survived: the same fact with two different fates, which is
	// the thing this whole change exists to end.
	if _, err := tx.Exec(ctx, `
		insert into public.story_view_snapshots
			(workspace_id, account_id, wa_message_id, viewers, is_final)
		select c.workspace_id, m.account_id, m.wa_message_id,
		       (select count(*) from public.story_views v
		         where v.account_id = m.account_id
		           and v.wa_message_id = m.wa_message_id),
		       true
		  from public.messages m
		  join public.conversations c on c.id = m.conversation_id
		 where c.type = 'status' and m.from_me
		   and m.wa_message_id is not null
		   and least(m.timestamp + interval '24 hours',
		             coalesce(m.revoked_at, 'infinity'::timestamptz))
		       <= now() - interval '1 hour'
		   and not exists (
		     select 1 from public.story_publications p
		      where p.account_id = m.account_id
		        and p.wa_message_id = m.wa_message_id)
		on conflict do nothing`); err != nil {
		return 0, err
	}

	tag, err := tx.Exec(ctx, `
		update public.story_publications
		   set status = 'expired'
		 where status = 'published' and expires_at is not null and expires_at <= now()`)
	if err != nil {
		return 0, err
	}
	n := int(tag.RowsAffected())

	// A campaign whose every publication has expired is itself expired. It is
	// not a failure — it ran, and then its 24 hours passed.
	if _, err := tx.Exec(ctx, `
		update public.content_campaigns cc
		   set status = 'expired'
		 where cc.campaign_type = 'story'
		   and cc.status in ('completed', 'partial')
		   and not exists (
		     select 1 from public.story_publications p
		      where p.campaign_id = cc.id and p.status <> 'expired')
		   and exists (select 1 from public.story_publications p where p.campaign_id = cc.id)`); err != nil {
		return 0, err
	}

	// The viewer rows, once their number is safe. Deleted in the same transaction
	// as the snapshot that replaces them, so there is no moment where the figure
	// exists in neither place.
	if _, err := tx.Exec(ctx, `
		delete from public.story_views v
		 using public.story_view_snapshots s
		 where s.is_final
		   and s.account_id    = v.account_id
		   and s.wa_message_id = v.wa_message_id`); err != nil {
		return 0, err
	}
	return n, tx.Commit(ctx)
}

// StoryReportFor assembles the Story detail screen.
func (r *Repo) StoryReportFor(
	ctx context.Context,
	workspaceID, campaignID uuid.UUID,
) (*models.StoryReport, error) {
	campaign, err := r.GetCampaign(ctx, workspaceID, campaignID)
	if err != nil {
		return nil, err
	}
	pubs, err := r.StoryPublications(ctx, campaignID)
	if err != nil {
		return nil, err
	}
	perDevice, unique, uniqueKnown, err := r.StoryViewTotals(ctx, campaignID)
	if err != nil {
		return nil, err
	}
	labels, err := r.CampaignLabelsFor(ctx, campaignID)
	if err != nil {
		return nil, err
	}

	// "Available" means at least one Story is actually out there. Without that,
	// zero is not a measurement and the interface says so rather than drawing a
	// figure somebody would read as "nobody watched".
	available := false
	for _, p := range pubs {
		if p.PublishedAt != nil {
			available = true
			break
		}
	}

	return &models.StoryReport{
		Campaign:       *campaign,
		Publications:   pubs,
		ViewsPerDevice: perDevice,
		UniqueViewers:  unique,
		UniqueKnown:    uniqueKnown,
		ViewsAvailable: available,
		Labels:         labels,
	}, nil
}
