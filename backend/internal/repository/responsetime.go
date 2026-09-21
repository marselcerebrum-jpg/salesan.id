package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/salesan/omnichannel/backend/internal/models"
)

// First response time, SLA, and follow-up.
//
// All three are derived tables, written by the analytics package as messages
// arrive and repaired by the reconciler; nothing here recomputes them from raw
// messages. That separation is what makes the drill-down honest — the list of
// cycles behind "SLA terlewati" is the same set of rows the number was counted
// from, not a second query that might disagree at the edges.
//
// Three rules apply throughout, and each is a decision rather than an accident:
//
//   - Personal chats only. A group has no single customer waiting, and a
//     Broadcast is not a question. Both are excluded at the source.
//   - Reading a message does not stop the clock. Only a manual reply does.
//   - A cycle closed from the phone counts for the application and the device
//     but for nobody's personal figures, because WhatsApp does not say who
//     typed it and guessing from the rota is expressly forbidden.

// slaDurationExpr is the duration a cycle is judged on.
//
// Business-hours duration when it was computed (the cycle was scored against
// time inside the responder's shift), wall-clock otherwise. Chosen per row from
// what was stored at the time, so changing the setting later does not silently
// rescore last month.
const slaDurationExpr = `coalesce(sc.business_duration_seconds, sc.raw_duration_seconds)`

// collectSLA fills the response-time figures for each day.
func (r *Repo) collectSLA(
	ctx context.Context, sc Scope, f models.AnalyticsFilter,
	day func(string) *models.DashboardSummary,
) error {
	q := &queryArgs{}
	where := scopeWhere("sc", sc, f, "started_at", q)
	where += slaAdminNarrow(f, q)

	// "Completed" means answered AND judged as an SLA cycle. A message that
	// arrived out of hours is answered too, but it is a queue item: letting it
	// into the average would mix "how fast we answer while we are open" with
	// "how long the overnight pile took to clear", and neither number would
	// mean anything afterwards.
	const completed = "sc.responded_at is not null and sc.status <> 'queued'"
	const queued = "sc.status = 'queued'"

	sql := `
		select (sc.started_at ` + jakartaDate + ` as d,
		       count(*) filter (where sc.status = 'achieved'),
		       count(*) filter (where sc.status = 'breached'),
		       count(*) filter (where sc.status = 'waiting'),
		       count(*) filter (where ` + completed + `),
		       avg(` + slaDurationExpr + `) filter (where ` + completed + `),
		       percentile_cont(0.5) within group (order by ` + slaDurationExpr + `)
		         filter (where ` + completed + `),
		       min(` + slaDurationExpr + `) filter (where ` + completed + `),
		       max(` + slaDurationExpr + `) filter (where ` + completed + `),
		       -- The queue, counted apart. Its duration is measured from the
		       -- shift opening, which is what the business duration already
		       -- means on these rows: none of the time before the shift counts.
		       count(*) filter (where ` + queued + `),
		       count(*) filter (where ` + queued + ` and sc.responded_at is not null),
		       count(*) filter (where ` + queued + ` and sc.responded_at is null),
		       avg(sc.business_duration_seconds)
		         filter (where ` + queued + ` and sc.responded_at is not null),
		       max(sc.business_duration_seconds)
		         filter (where ` + queued + ` and sc.responded_at is not null)
		  from public.sla_cycles sc` + where + `
		 group by d`

	rows, err := r.pool.Query(ctx, sql, q.args...)
	if err != nil {
		return fmt.Errorf("sla cycles: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var d time.Time
		var achieved, breached, waiting, done int
		var avg, median *float64
		var fastest, slowest *int
		var qTotal, qAnswered, qWaiting int
		var qAvg *float64
		var qSlowest *int
		if err := rows.Scan(&d, &achieved, &breached, &waiting, &done,
			&avg, &median, &fastest, &slowest,
			&qTotal, &qAnswered, &qWaiting, &qAvg, &qSlowest); err != nil {
			return err
		}
		s := day(d.Format("2006-01-02"))
		s.SLAAchieved = achieved
		s.SLABreached = breached
		s.SLAWaiting = waiting
		s.SLACompleted = done
		s.AvgFirstResponseSeconds = roundSeconds(avg)
		s.MedianFirstResponseSeconds = roundSeconds(median)
		s.FastestResponseSeconds = fastest
		s.SlowestResponseSeconds = slowest
		s.QueuedTotal = qTotal
		s.QueuedAnswered = qAnswered
		s.QueuedWaiting = qWaiting
		s.QueuedAvgSeconds = roundSeconds(qAvg)
		s.QueuedSlowestSeconds = qSlowest
	}
	return rows.Err()
}

// slaAdminNarrow restricts cycles to one person's work.
//
// Only cycles they answered. A cycle still waiting has no responder at all, so
// narrowing by admin necessarily excludes it — which is correct: "menunggu
// balasan" is a fact about a conversation, not about a person, and attributing
// it to whoever happens to be on shift is the guess this system refuses to make.
func slaAdminNarrow(f models.AnalyticsFilter, q *queryArgs) string {
	if f.AdminID == nil {
		return ""
	}
	return fmt.Sprintf(" and sc.responder_admin_id = %s", q.add(*f.AdminID))
}

// collectFollowUp fills the follow-up figures for each day.
func (r *Repo) collectFollowUp(
	ctx context.Context, sc Scope, f models.AnalyticsFilter,
	day func(string) *models.DashboardSummary,
) error {
	q := &queryArgs{}
	where := scopeWhere("fu", sc, f, "started_at", q)
	if f.AdminID != nil {
		where += fmt.Sprintf(" and fu.admin_id = %s", q.add(*f.AdminID))
	}

	sql := `
		select fu.local_date,
		       count(*),
		       count(distinct coalesce(fu.contact_id::text, fu.conversation_id::text)),
		       count(*) filter (where fu.responded_at is not null),
		       count(*) filter (where fu.responded_at is null)
		  from public.follow_up_events fu` + where + `
		 group by fu.local_date`

	rows, err := r.pool.Query(ctx, sql, q.args...)
	if err != nil {
		return fmt.Errorf("follow ups: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var d time.Time
		var total, contacts, answered, unanswered int
		if err := rows.Scan(&d, &total, &contacts, &answered, &unanswered); err != nil {
			return err
		}
		s := day(d.Format("2006-01-02"))
		s.FollowUps = total
		s.FollowUpContacts = contacts
		s.FollowUpsAnswered = answered
		s.FollowUpsUnanswered = unanswered
	}
	return rows.Err()
}

// fillRangeResponse recomputes the response figures over the whole period.
//
// An average of daily averages is not the average, and a median of medians is
// not the median — both have to be computed once over every cycle in the range.
// Distinct follow-up contacts have the same problem as every other distinct
// count: the same person followed up on two days is one contact.
func (r *Repo) fillRangeResponse(
	ctx context.Context, sc Scope, f models.AnalyticsFilter, out *models.DashboardSummary,
) error {
	q := &queryArgs{}
	where := scopeWhere("sc", sc, f, "started_at", q) + slaAdminNarrow(f, q)

	// Recomputed over the whole range rather than folded from the daily rows:
	// an average of averages is not the average, a median of medians is not the
	// median, and the slowest day's slowest cycle is not necessarily the
	// period's — it is, but only by luck of how min and max happen to compose.
	// Computing all four once removes the need to reason about which.
	// The same exclusion as the daily pass: a queue item is answered, but it is
	// not an SLA cycle, and mixing it into the average is what made a busy night
	// read as a slow day.
	const answered = "sc.responded_at is not null and sc.status <> 'queued'"
	const queuedDone = "sc.status = 'queued' and sc.responded_at is not null"

	var avg, median *float64
	var fastest, slowest *int
	var qAvg *float64
	if err := r.pool.QueryRow(ctx, `
		select avg(`+slaDurationExpr+`) filter (where `+answered+`),
		       percentile_cont(0.5) within group (order by `+slaDurationExpr+`)
		         filter (where `+answered+`),
		       min(`+slaDurationExpr+`) filter (where `+answered+`),
		       max(`+slaDurationExpr+`) filter (where `+answered+`),
		       count(*) filter (where `+answered+`),
		       avg(sc.business_duration_seconds) filter (where `+queuedDone+`),
		       max(sc.business_duration_seconds) filter (where `+queuedDone+`)
		  from public.sla_cycles sc`+where, q.args...).
		Scan(&avg, &median, &fastest, &slowest, &out.SLACompleted,
			&qAvg, &out.QueuedSlowestSeconds); err != nil {
		return fmt.Errorf("range response time: %w", err)
	}
	out.AvgFirstResponseSeconds = roundSeconds(avg)
	out.MedianFirstResponseSeconds = roundSeconds(median)
	out.FastestResponseSeconds = fastest
	out.SlowestResponseSeconds = slowest
	out.QueuedAvgSeconds = roundSeconds(qAvg)

	q2 := &queryArgs{}
	w2 := scopeWhere("fu", sc, f, "started_at", q2)
	if f.AdminID != nil {
		w2 += fmt.Sprintf(" and fu.admin_id = %s", q2.add(*f.AdminID))
	}
	return r.pool.QueryRow(ctx,
		`select count(distinct coalesce(fu.contact_id::text, fu.conversation_id::text))
		   from public.follow_up_events fu`+w2, q2.args...).Scan(&out.FollowUpContacts)
}

// --- per person ----------------------------------------------------------------

// teamSLA groups response times per admin and per team in one pass.
func (r *Repo) teamSLA(
	ctx context.Context, sc Scope, f models.AnalyticsFilter, sink *metricSink,
) error {
	if f.ChatType == models.ConversationTypeGroup {
		return nil // SLA is a personal-chat measure only
	}
	q := &queryArgs{}
	cte := fmt.Sprintf(teamCTE, q.add(sc.WorkspaceID))
	where := teamScope("sc", sc, f, "started_at", q)

	sql := cte + `
		select grouping(t.admin_id) = 0 as per_admin, t.admin_id, t.team_id,
		       count(*) filter (where sc.status = 'achieved'),
		       count(*) filter (where sc.status = 'breached'),
		       avg(` + slaDurationExpr + `) filter (where sc.responded_at is not null),
		       percentile_cont(0.5) within group (order by ` + slaDurationExpr + `)
		         filter (where sc.responded_at is not null)
		  from public.sla_cycles sc
		  left join team t on t.admin_id = sc.responder_admin_id` + where + `
		   and sc.responded_at is not null` +
		adminVisible("sc.responder_admin_id", sc, q) +
		picNarrow("sc.application_id", f, q) +
		adminNarrow("sc.responder_admin_id", f, q) + `
		 group by grouping sets ((t.admin_id), (t.team_id))`

	rows, err := r.pool.Query(ctx, sql, q.args...)
	if err != nil {
		return fmt.Errorf("team sla: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var perAdmin bool
		var adminID, teamID *uuid.UUID
		var achieved, breached int
		var avg, median *float64
		if err := rows.Scan(&perAdmin, &adminID, &teamID, &achieved, &breached, &avg, &median); err != nil {
			return err
		}
		target := &sink.unattributed
		if !perAdmin || adminID != nil {
			key, ok := groupingKey(adminID, teamID, !perAdmin)
			if !ok {
				continue
			}
			target = sink.at(key)
		}
		target.SLAAchieved = achieved
		target.SLABreached = breached
		target.AvgFirstResponseSeconds = roundSeconds(avg)
		target.MedianFirstResponseSeconds = roundSeconds(median)
	}
	return rows.Err()
}

// teamFollowUp groups follow-up activity per admin and per team.
func (r *Repo) teamFollowUp(
	ctx context.Context, sc Scope, f models.AnalyticsFilter, sink *metricSink,
) error {
	if f.ChatType == models.ConversationTypeGroup {
		return nil // a follow-up is a personal chat by definition
	}
	q := &queryArgs{}
	cte := fmt.Sprintf(teamCTE, q.add(sc.WorkspaceID))
	where := teamScope("fu", sc, f, "started_at", q)

	sql := cte + `
		select grouping(t.admin_id) = 0 as per_admin, t.admin_id, t.team_id,
		       count(*),
		       count(distinct coalesce(fu.contact_id::text, fu.conversation_id::text)),
		       count(*) filter (where fu.responded_at is not null),
		       count(*) filter (where fu.responded_at is null)
		  from public.follow_up_events fu
		  left join team t on t.admin_id = fu.admin_id` + where +
		adminVisible("fu.admin_id", sc, q) +
		picNarrow("fu.application_id", f, q) +
		adminNarrow("fu.admin_id", f, q) + `
		 group by grouping sets ((t.admin_id), (t.team_id))`

	rows, err := r.pool.Query(ctx, sql, q.args...)
	if err != nil {
		return fmt.Errorf("team follow ups: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var perAdmin bool
		var adminID, teamID *uuid.UUID
		var total, contacts, answered, unanswered int
		if err := rows.Scan(&perAdmin, &adminID, &teamID,
			&total, &contacts, &answered, &unanswered); err != nil {
			return err
		}
		target := &sink.unattributed
		if !perAdmin || adminID != nil {
			key, ok := groupingKey(adminID, teamID, !perAdmin)
			if !ok {
				continue
			}
			target = sink.at(key)
		}
		target.FollowUps = total
		target.FollowUpContacts = contacts
		target.FollowUpsAnswered = answered
		target.FollowUpsUnanswered = unanswered
	}
	return rows.Err()
}
