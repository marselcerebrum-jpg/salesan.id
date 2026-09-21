package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/salesan/omnichannel/backend/internal/analytics"
	"github.com/salesan/omnichannel/backend/internal/models"
)

// The Dashboard's own two questions.
//
// Everything else that screen shows is read from the endpoints Performa
// already uses — the application breakdown, the campaign tallies, the summary
// — because two screens computing one figure is how they come to disagree.
// What is here is only what nothing else answered: the state of the numbers
// right now, and message volume as a curve over time.

// DashboardStats is the state of the workspace at this moment.
//
// Not a period: these are counts of how things stand, which is why none of
// them takes a date range. "Berapa nomor tersambung" has no yesterday.
type DashboardStats struct {
	DevicesTotal     int `json:"devices_total"`
	DevicesConnected int `json:"devices_connected"`
	// Unanswered reads the same column the inbox badges read, so the number on
	// the Dashboard and the number in the sidebar cannot disagree.
	Unanswered int `json:"unanswered"`
	Contacts   int `json:"contacts"`
	Groups     int `json:"groups"`
	// Disconnected names the numbers that are down, because "3 dari 5
	// tersambung" tells somebody there is a problem without telling them which
	// phone to go and pick up.
	Disconnected []DisconnectedDevice `json:"disconnected"`
}

// DisconnectedDevice is one number that is not currently connected.
type DisconnectedDevice struct {
	ID              uuid.UUID  `json:"id"`
	Name            string     `json:"name"`
	PhoneNumber     *string    `json:"phone_number"`
	Status          string     `json:"status"`
	StatusDetail    *string    `json:"status_detail"`
	ApplicationCode *string    `json:"application_code"`
	LastConnectedAt *time.Time `json:"last_connected_at"`
}

// DashboardStatsFor counts the workspace as it stands, within the caller's own
// applications.
func (r *Repo) DashboardStatsFor(ctx context.Context, sc Scope) (*DashboardStats, error) {
	// One clause, written once, applied to every count below: a filter added to
	// four of the five is the kind of mistake nobody notices until a Freelance
	// sees another brand's contact total.
	reach := "a.workspace_id = $1 and ($2::boolean or a.application_id = any ($3::uuid[]))"
	args := []any{sc.WorkspaceID, sc.All, sc.ApplicationIDs}

	var out DashboardStats
	if err := r.pool.QueryRow(ctx, `
		select
		  (select count(*) from public.whatsapp_accounts a where `+reach+`),
		  (select count(*) from public.whatsapp_accounts a
		    where `+reach+` and a.status = 'connected'),
		  (select count(*) from public.conversations c
		     join public.whatsapp_accounts a on a.id = c.account_id
		    where `+reach+` and c.awaiting_reply and not c.is_archived),
		  (select count(*) from public.contacts ct
		     join public.whatsapp_accounts a on a.id = ct.account_id
		    where `+reach+`),
		  (select count(*) from public.conversations c
		     join public.whatsapp_accounts a on a.id = c.account_id
		    where `+reach+` and c.type = 'group' and not c.is_archived)`,
		args...).Scan(&out.DevicesTotal, &out.DevicesConnected,
		&out.Unanswered, &out.Contacts, &out.Groups); err != nil {
		return nil, fmt.Errorf("dashboard stats: %w", err)
	}

	rows, err := r.pool.Query(ctx, `
		select a.id, a.name, a.phone_number, a.status::text, a.status_detail,
		       app.code, a.last_connected_at
		  from public.whatsapp_accounts a
		  left join public.applications app on app.id = a.application_id
		 where `+reach+` and a.status <> 'connected'
		 order by a.last_connected_at desc nulls last, a.name`, args...)
	if err != nil {
		return nil, fmt.Errorf("disconnected devices: %w", err)
	}
	defer rows.Close()

	out.Disconnected = []DisconnectedDevice{}
	for rows.Next() {
		var d DisconnectedDevice
		if err := rows.Scan(&d.ID, &d.Name, &d.PhoneNumber, &d.Status,
			&d.StatusDetail, &d.ApplicationCode, &d.LastConnectedAt); err != nil {
			return nil, err
		}
		out.Disconnected = append(out.Disconnected, d)
	}
	return &out, rows.Err()
}

// TrafficPoint is one bucket of the message-volume curve.
//
// Inbound and outbound are kept apart, and personal and group are kept apart,
// because the four move for different reasons: a spike in group traffic is not
// the same event as a spike in customers writing in, and one line hiding both
// would be a chart that cannot be acted on.
type TrafficPoint struct {
	// Bucket is the start of the hour or day, in Jakarta.
	Bucket           string `json:"bucket"`
	InboundPersonal  int    `json:"inbound_personal"`
	OutboundPersonal int    `json:"outbound_personal"`
	GroupInbound     int    `json:"group_inbound"`
	GroupOutbound    int    `json:"group_outbound"`
}

// Traffic buckets message volume by hour or by day.
//
// Outbound counts what a human sent, from the web or from the phone. Broadcast
// and story traffic is excluded: it is not conversation, and letting it in
// would make the day a campaign ran look like a day of unusual engagement.
func (r *Repo) Traffic(
	ctx context.Context, sc Scope, f models.AnalyticsFilter, byHour bool,
) ([]TrafficPoint, bool, error) {
	unit, layout := "day", "2006-01-02"
	if byHour {
		unit, layout = "hour", "2006-01-02T15:04"
	}

	q := &queryArgs{}
	where := messageWhere(sc, f, q)

	/*
	 * Whose traffic this is.
	 *
	 * messageWhere narrows by workspace, period, application, number and PIC —
	 * everything except the person. That is right for the Dashboard, which asks
	 * how busy the operation was, and wrong everywhere this chart is drawn under
	 * somebody's name: a Freelance who answered nothing still got a tall chart,
	 * because their application was busy.
	 *
	 * The two halves are narrowed differently, and by the same rules the cards
	 * beside the chart already use. Outgoing messages carry the person who sent
	 * them. Incoming ones carry nobody — the customer wrote them — so they count
	 * for a person when they landed in a conversation that person actually
	 * worked in during the period. Anything else would either credit one person
	 * with every customer in the application, or show zero incoming for
	 * everybody.
	 */
	outboundBy := adminOutbound(f, q)
	inboundIn := adminTouchedConversation(f, q)

	/*
	 * The conversation type asked for in the filter row.
	 *
	 * The chart has its own Pribadi/Grup control, which is about reading it; this
	 * is the page's filter, which is about what the page is. A filter that
	 * silently stops at the edge of one card is worse than no filter.
	 */
	if f.ChatType == "personal" || f.ChatType == "group" {
		where += fmt.Sprintf(" and c.type = %s", q.add(f.ChatType))
	}

	/*
	 * Only the hours somebody was on shift, when asked for.
	 *
	 * The windows are the ones this filter already resolves for every other
	 * schedule figure on the page, so "jam kerja" means one thing across the
	 * whole report. Merged first, because two people covering the same hour is
	 * one open hour, not two — and overlapping ranges here would match the same
	 * message twice.
	 *
	 * Passed as two arrays and unnested rather than built into the SQL text: a
	 * month of shifts is a few dozen rows, the statement stays one shape
	 * whatever the rota looks like, and nothing about a schedule reaches the
	 * query as text.
	 */
	scheduled := true
	if f.WorkHoursOnly {
		windows, err := r.performanceWindows(ctx, sc, f)
		if err != nil {
			return nil, false, err
		}
		merged := analytics.MergeWindows(windows)
		scheduled = len(merged) > 0
		if !scheduled {
			// No rota covering this period. Nothing happened inside working
			// hours because there were none, and saying so is the honest
			// answer; inventing a fallback window would be inventing a fact.
			return []TrafficPoint{}, false, nil
		}

		starts := make([]time.Time, 0, len(merged))
		ends := make([]time.Time, 0, len(merged))
		for _, w := range merged {
			starts = append(starts, w.Start)
			ends = append(ends, w.End)
		}
		where += fmt.Sprintf(` and exists (
			select 1 from unnest(%s::timestamptz[], %s::timestamptz[]) as w(s, e)
			 where m.timestamp >= w.s and m.timestamp < w.e)`,
			q.add(starts), q.add(ends))
	}

	sql := `
		select date_trunc('` + unit + `', m.timestamp at time zone 'Asia/Jakarta') as b,
		       count(*) filter (where not m.from_me and c.type = 'personal'` + inboundIn + `),
		       count(*) filter (where m.from_me and c.type = 'personal'
		                          and m.sender_source in ('web_admin', 'whatsapp_device')` + outboundBy + `),
		       count(*) filter (where not m.from_me and c.type = 'group'` + inboundIn + `),
		       count(*) filter (where m.from_me and c.type = 'group'
		                          and m.sender_source in ('web_admin', 'whatsapp_device')` + outboundBy + `)
		  from public.messages m
		  join public.conversations c on c.id = m.conversation_id
		  join public.whatsapp_accounts a on a.id = m.account_id` + where + `
		   and m.hidden_at is null
		   and c.type in ('personal', 'group')
		 group by b
		 order by b`

	rows, err := r.pool.Query(ctx, sql, q.args...)
	if err != nil {
		return nil, false, fmt.Errorf("traffic: %w", err)
	}
	defer rows.Close()

	out := []TrafficPoint{}
	for rows.Next() {
		var at time.Time
		var p TrafficPoint
		if err := rows.Scan(&at, &p.InboundPersonal, &p.OutboundPersonal,
			&p.GroupInbound, &p.GroupOutbound); err != nil {
			return nil, false, err
		}
		p.Bucket = at.Format(layout)
		out = append(out, p)
	}
	return out, scheduled, rows.Err()
}
