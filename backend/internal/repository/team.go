package repository

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/salesan/omnichannel/backend/internal/analytics"
	"github.com/salesan/omnichannel/backend/internal/models"
)

// Per-person performance.
//
// The hierarchy is answered here rather than in the interface, for the same
// reason the aggregates are: the browser must not be the thing that decides
// whose numbers somebody may read. Every query below restricts to the caller's
// scope before it groups, so a request for another PIC's team returns nothing
// rather than returning data the page then declines to draw.
//
// Each metric is grouped twice in one pass, over GROUPING SETS: once per admin,
// once per team. Summing a team from its members would be wrong for the
// distinct counts — two Freelance serving the same contact is one contact
// served by the team, not two — and running a second set of queries for the
// team totals would double the work for an answer the same scan already has.

// teamCTE maps every admin to the team they count towards.
//
// A Freelance counts towards their PIC. A PIC (or anyone without one) is their
// own team, which is what makes a PIC's team row include their personal work.
const teamCTE = `
	with team as (
		select u.id as admin_id,
		       coalesce(fp.pic_user_id, u.id) as team_id
		  from public.users u
		  left join public.freelancer_pic_assignments fp on fp.freelancer_user_id = u.id
		 where u.workspace_id = %s
	)`

// memberKey identifies one row of a grouped result.
type memberKey struct {
	id   uuid.UUID
	team bool
}

// metricSink collects grouped rows into per-admin and per-team buckets.
type metricSink struct {
	personal map[uuid.UUID]*models.MemberMetrics
	team     map[uuid.UUID]*models.MemberMetrics
	// unattributed holds rows whose admin is null: activity from a phone whose
	// operator cannot be verified.
	unattributed models.MemberMetrics
}

func newMetricSink() *metricSink {
	return &metricSink{
		personal: map[uuid.UUID]*models.MemberMetrics{},
		team:     map[uuid.UUID]*models.MemberMetrics{},
	}
}

func (s *metricSink) at(k memberKey) *models.MemberMetrics {
	bucket := s.personal
	if k.team {
		bucket = s.team
	}
	if bucket[k.id] == nil {
		bucket[k.id] = &models.MemberMetrics{}
	}
	return bucket[k.id]
}

// TeamPerformance returns one row per visible person for the period.
func (r *Repo) TeamPerformance(
	ctx context.Context, sc Scope, f models.AnalyticsFilter,
) (*models.TeamReport, error) {
	if f.ScheduleID != nil {
		narrowed, err := r.narrowToShift(ctx, sc, f)
		if err != nil {
			return nil, err
		}
		f = narrowed
	}

	members, err := r.visibleMembers(ctx, sc, f)
	if err != nil {
		return nil, err
	}

	sink := newMetricSink()
	for _, collect := range []func(context.Context, Scope, models.AnalyticsFilter, *metricSink) error{
		r.teamPersonal,
		r.teamGroup,
		r.teamSLA,
		r.teamFollowUp,
		r.teamLabels,
		r.teamCampaigns,
		r.teamLeads,
		r.teamScheduleCompliance,
	} {
		if err := collect(ctx, sc, f, sink); err != nil {
			return nil, err
		}
	}

	if err := r.teamSchedules(ctx, sc, f, members, sink); err != nil {
		return nil, err
	}

	// A member with no activity still gets a row: "did nothing today" is a
	// finding, and dropping them from the table hides it.
	onDuty, err := r.onDutyNow(ctx, sc.WorkspaceID)
	if err != nil {
		return nil, err
	}

	out := make([]models.MemberPerformance, 0, len(members))
	for _, m := range members {
		row := m
		row.OnDuty = onDuty[*m.UserID]
		if got := sink.personal[*m.UserID]; got != nil {
			row.Personal = *got
		}
		// Only somebody who actually leads a team gets a team figure; for
		// everyone else it would repeat their personal numbers under a heading
		// implying otherwise.
		if m.Role == models.RolePIC && m.FreelanceCount > 0 {
			team := models.MemberMetrics{}
			if got := sink.team[*m.UserID]; got != nil {
				team = *got
			}
			finishRates(&team)
			row.Team = &team
		}
		finishRates(&row.Personal)
		out = append(out, row)
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Role != out[j].Role {
			return rolePriority(out[i].Role) < rolePriority(out[j].Role)
		}
		return out[i].Name < out[j].Name
	})

	unattributed := sink.unattributed
	finishRates(&unattributed)

	return &models.TeamReport{
		From:         f.From.In(analytics.Jakarta).Format("2006-01-02"),
		To:           f.To.Add(-time.Second).In(analytics.Jakarta).Format("2006-01-02"),
		Role:         sc.Role,
		Members:      out,
		Unattributed: unattributed,
	}, nil
}

func rolePriority(role string) int {
	switch role {
	case models.RoleLeader:
		return 0
	case models.RolePIC:
		return 1
	case models.RoleFreelance:
		return 2
	default:
		return 3
	}
}

// finishRates turns counted work into per-hour figures.
func finishRates(m *models.MemberMetrics) {
	hours := float64(m.WorkSeconds) / 3600
	if hours <= 0 {
		return // no rota recorded is not the same as nothing per hour
	}
	messages := round2(float64(m.OutboundManual) / hours)
	contacts := round2(float64(m.ContactsServed) / hours)
	groups := round2(float64(m.GroupReplies) / hours)
	campaigns := round2(float64(m.BroadcastsCreated+m.StoriesCreated) / hours)
	followUps := round2(float64(m.FollowUps) / hours)

	m.MessagesPerHour = &messages
	m.ContactsPerHour = &contacts
	m.GroupRepliesPerHour = &groups
	m.CampaignsPerHour = &campaigns
	m.FollowUpsPerHour = &followUps
}

// visibleMembers lists the people this caller may read, already narrowed by the
// PIC and admin filters.
func (r *Repo) visibleMembers(
	ctx context.Context, sc Scope, f models.AnalyticsFilter,
) ([]models.MemberPerformance, error) {
	q := &queryArgs{}
	where := fmt.Sprintf(" where u.workspace_id = %s", q.add(sc.WorkspaceID))

	if !sc.All {
		// The same list ResolveScope built: a PIC sees themselves and their
		// Freelance, a Freelance sees only themselves.
		where += fmt.Sprintf(" and u.id = any(%s)", q.add(sc.AdminIDs))
	}
	if f.PICUserID != nil {
		pic := q.add(*f.PICUserID)
		where += fmt.Sprintf(" and (u.id = %s or fp.pic_user_id = %s)", pic, pic)
	}
	if f.AdminID != nil {
		where += fmt.Sprintf(" and u.id = %s", q.add(*f.AdminID))
	}

	rows, err := r.pool.Query(ctx, `
		select u.id, coalesce(nullif(u.full_name, ''), u.email), u.email,
		       coalesce(ra.role::text, ''), u.is_active,
		       fp.pic_user_id, coalesce(nullif(pic.full_name, ''), pic.email),
		       (select count(*) from public.freelancer_pic_assignments f2
		         where f2.pic_user_id = u.id)
		  from public.users u
		  join public.role_assignments ra
		    on ra.user_id = u.id and ra.workspace_id = u.workspace_id
		  left join public.freelancer_pic_assignments fp on fp.freelancer_user_id = u.id
		  left join public.users pic on pic.id = fp.pic_user_id`+where+`
		 order by coalesce(nullif(u.full_name, ''), u.email)`, q.args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []models.MemberPerformance{}
	index := map[uuid.UUID]int{}
	for rows.Next() {
		var m models.MemberPerformance
		var id uuid.UUID
		if err := rows.Scan(&id, &m.Name, &m.Email, &m.Role, &m.IsActive,
			&m.PICUserID, &m.PICName, &m.FreelanceCount); err != nil {
			return nil, err
		}
		m.UserID = &id
		m.Applications = []models.AppRef{}
		index[id] = len(out)
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return out, nil
	}

	appRows, err := r.pool.Query(ctx, `
		select x.user_id, a.id, a.code, a.name, a.color
		  from (
			select pic_user_id as user_id, application_id from public.pic_application_assignments
			 where workspace_id = $1
			union all
			select freelancer_user_id, application_id from public.freelancer_application_assignments
			 where workspace_id = $1
		  ) x
		  join public.applications a on a.id = x.application_id
		 order by a.sort_order, a.code`, sc.WorkspaceID)
	if err != nil {
		return nil, err
	}
	defer appRows.Close()
	for appRows.Next() {
		var userID uuid.UUID
		var ref models.AppRef
		if err := appRows.Scan(&userID, &ref.ID, &ref.Code, &ref.Name, &ref.Color); err != nil {
			return nil, err
		}
		if i, ok := index[userID]; ok {
			out[i].Applications = append(out[i].Applications, ref)
		}
	}
	return out, appRows.Err()
}

// onDutyNow reports who has a shift covering this moment.
func (r *Repo) onDutyNow(ctx context.Context, workspaceID uuid.UUID) (map[uuid.UUID]bool, error) {
	rows, err := r.pool.Query(ctx, `
		select distinct s.user_id
		  from public.work_schedules s
		 where s.workspace_id = $1 and s.is_active
		   and now() >= ((s.work_date + s.starts_at) at time zone s.timezone)
		   and now() <  ((s.work_date + s.ends_at)   at time zone s.timezone)`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[uuid.UUID]bool{}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// --- grouped collectors --------------------------------------------------------

// groupingKey reads the admin/team pair a GROUPING SETS row carries.
func groupingKey(adminID, teamID *uuid.UUID, isTeamRow bool) (memberKey, bool) {
	if isTeamRow {
		if teamID == nil {
			return memberKey{}, false
		}
		return memberKey{id: *teamID, team: true}, true
	}
	if adminID == nil {
		return memberKey{}, false
	}
	return memberKey{id: *adminID, team: false}, true
}

// teamScope narrows a grouped query the same way scopeWhere narrows an
// aggregate.
func teamScope(alias string, sc Scope, f models.AnalyticsFilter, tsCol string, q *queryArgs) string {
	where := fmt.Sprintf(" where %s.workspace_id = %s", alias, q.add(sc.WorkspaceID))
	if !f.From.IsZero() {
		where += fmt.Sprintf(" and %s.%s >= %s", alias, tsCol, q.add(f.From))
	}
	if !f.To.IsZero() {
		where += fmt.Sprintf(" and %s.%s < %s", alias, tsCol, q.add(f.To))
	}
	if !sc.All {
		where += fmt.Sprintf(" and %s.application_id = any(%s)", alias, q.add(sc.ApplicationIDs))
	}
	if f.ApplicationID != nil {
		where += fmt.Sprintf(" and %s.application_id = %s", alias, q.add(*f.ApplicationID))
	}
	if f.AccountID != nil {
		where += fmt.Sprintf(" and %s.account_id = %s", alias, q.add(*f.AccountID))
	}
	return where
}

// adminVisible restricts a grouped query to admins the caller may read.
//
// The null case is deliberate: unattributed device activity has no admin, and a
// Leader or the PIC who owns the application is allowed to see it. A Freelance
// is not — their view is their own work and nothing else.
func adminVisible(col string, sc Scope, q *queryArgs) string {
	if sc.All {
		return ""
	}
	if sc.Role == models.RoleFreelance {
		return fmt.Sprintf(" and %s = %s", col, q.add(sc.UserID))
	}
	return fmt.Sprintf(" and (%s = any(%s) or %s is null)", col, q.add(sc.AdminIDs), col)
}

// picNarrow limits a query to the applications one PIC holds.
func picNarrow(col string, f models.AnalyticsFilter, q *queryArgs) string {
	if f.PICUserID == nil {
		return ""
	}
	return fmt.Sprintf(` and %s in (
		select application_id from public.pic_application_assignments where pic_user_id = %s)`,
		col, q.add(*f.PICUserID))
}

// adminNarrow limits a query to one admin.
func adminNarrow(col string, f models.AnalyticsFilter, q *queryArgs) string {
	if f.AdminID == nil {
		return ""
	}
	return fmt.Sprintf(" and %s = %s", col, q.add(*f.AdminID))
}

// teamPersonal counts one-to-one bubbles sent and contacts served.
//
// Contacts are counted on the contact, not the conversation: one customer may
// reach the same team through two of our numbers, and that is one contact.
func (r *Repo) teamPersonal(
	ctx context.Context, sc Scope, f models.AnalyticsFilter, sink *metricSink,
) error {
	if f.ChatType == models.ConversationTypeGroup {
		return nil
	}
	q := &queryArgs{}
	cte := fmt.Sprintf(teamCTE, q.add(sc.WorkspaceID))
	where := messageWhere(sc, f, q)

	sql := cte + `
		select grouping(t.admin_id) = 0 as per_admin, t.admin_id, t.team_id,
		       count(*) as bubbles,
		       count(distinct coalesce(c.contact_id::text, c.chat_jid)) as contacts
		  from public.messages m
		  join public.conversations c on c.id = m.conversation_id
		  join public.whatsapp_accounts a on a.id = m.account_id
		  left join team t on t.admin_id = m.sent_by` + where + `
		   and c.type = 'personal'
		   and m.from_me and m.hidden_at is null
		   and m.sender_source in ('web_admin', 'whatsapp_device')` +
		adminVisible("m.sent_by", sc, q) +
		picNarrow("a.application_id", f, q) +
		adminNarrow("m.sent_by", f, q) + `
		 group by grouping sets ((t.admin_id), (t.team_id))`

	rows, err := r.pool.Query(ctx, sql, q.args...)
	if err != nil {
		return fmt.Errorf("team personal: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var perAdmin bool
		var adminID, teamID *uuid.UUID
		var bubbles, contacts int
		if err := rows.Scan(&perAdmin, &adminID, &teamID, &bubbles, &contacts); err != nil {
			return err
		}
		if perAdmin && adminID == nil {
			// Sent from a phone: real work, nobody to credit it to.
			sink.unattributed.OutboundManual += bubbles
			sink.unattributed.ContactsServed += contacts
			continue
		}
		key, ok := groupingKey(adminID, teamID, !perAdmin)
		if !ok {
			continue
		}
		m := sink.at(key)
		m.OutboundManual = bubbles
		m.ContactsServed = contacts
	}
	return rows.Err()
}

func (r *Repo) teamGroup(
	ctx context.Context, sc Scope, f models.AnalyticsFilter, sink *metricSink,
) error {
	if f.ChatType == models.ConversationTypePersonal {
		return nil
	}
	q := &queryArgs{}
	cte := fmt.Sprintf(teamCTE, q.add(sc.WorkspaceID))
	where := messageWhere(sc, f, q)

	sql := cte + `
		select grouping(t.admin_id) = 0 as per_admin, t.admin_id, t.team_id,
		       count(*) as replies,
		       count(distinct m.conversation_id) as groups
		  from public.messages m
		  join public.conversations c on c.id = m.conversation_id
		  join public.whatsapp_accounts a on a.id = m.account_id
		  left join team t on t.admin_id = m.sent_by` + where + `
		   and c.type = 'group'
		   and m.from_me and m.hidden_at is null
		   and m.sender_source in ('web_admin', 'whatsapp_device')` +
		adminVisible("m.sent_by", sc, q) +
		picNarrow("a.application_id", f, q) +
		adminNarrow("m.sent_by", f, q) + `
		 group by grouping sets ((t.admin_id), (t.team_id))`

	rows, err := r.pool.Query(ctx, sql, q.args...)
	if err != nil {
		return fmt.Errorf("team group: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var perAdmin bool
		var adminID, teamID *uuid.UUID
		var replies, groups int
		if err := rows.Scan(&perAdmin, &adminID, &teamID, &replies, &groups); err != nil {
			return err
		}
		if perAdmin && adminID == nil {
			sink.unattributed.GroupReplies += replies
			sink.unattributed.GroupsHandled += groups
			continue
		}
		key, ok := groupingKey(adminID, teamID, !perAdmin)
		if !ok {
			continue
		}
		m := sink.at(key)
		m.GroupReplies = replies
		m.GroupsHandled = groups
	}
	return rows.Err()
}

func (r *Repo) teamLabels(
	ctx context.Context, sc Scope, f models.AnalyticsFilter, sink *metricSink,
) error {
	q := &queryArgs{}
	cte := fmt.Sprintf(teamCTE, q.add(sc.WorkspaceID))
	where := teamScope("le", sc, f, "occurred_at", q)

	sql := cte + `
		select grouping(t.admin_id) = 0 as per_admin, t.admin_id, t.team_id,
		       count(*), count(distinct le.contact_id)
		  from public.contact_label_events le
		  left join team t on t.admin_id = le.admin_id` + where +
		adminVisible("le.admin_id", sc, q) +
		picNarrow("le.application_id", f, q) +
		adminNarrow("le.admin_id", f, q) + `
		 group by grouping sets ((t.admin_id), (t.team_id))`

	rows, err := r.pool.Query(ctx, sql, q.args...)
	if err != nil {
		return fmt.Errorf("team labels: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var perAdmin bool
		var adminID, teamID *uuid.UUID
		var changes, contacts int
		if err := rows.Scan(&perAdmin, &adminID, &teamID, &changes, &contacts); err != nil {
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
		target.LabelChanges = changes
		target.LabelContacts = contacts
	}
	return rows.Err()
}

func (r *Repo) teamCampaigns(
	ctx context.Context, sc Scope, f models.AnalyticsFilter, sink *metricSink,
) error {
	q := &queryArgs{}
	cte := fmt.Sprintf(teamCTE, q.add(sc.WorkspaceID))

	where := fmt.Sprintf(" where cc.workspace_id = %s", q.add(sc.WorkspaceID))
	if !f.From.IsZero() {
		where += fmt.Sprintf(" and cc.created_at >= %s", q.add(f.From))
	}
	if !f.To.IsZero() {
		where += fmt.Sprintf(" and cc.created_at < %s", q.add(f.To))
	}
	if !sc.All {
		where += fmt.Sprintf(" and cc.application_id = any(%s)", q.add(sc.ApplicationIDs))
	}
	if f.ApplicationID != nil {
		where += fmt.Sprintf(" and cc.application_id = %s", q.add(*f.ApplicationID))
	}

	sql := cte + `
		select grouping(t.admin_id) = 0 as per_admin, t.admin_id, t.team_id,
		       count(*) filter (where cc.campaign_type = 'broadcast'),
		       count(*) filter (where cc.campaign_type = 'broadcast' and cc.status = 'completed'),
		       count(*) filter (where cc.campaign_type = 'story'),
		       count(*) filter (where cc.campaign_type = 'story' and cc.status = 'completed'),
		       count(*) filter (where cc.status = 'failed')
		  from public.content_campaigns cc
		  left join team t on t.admin_id = cc.created_by` + where +
		adminVisible("cc.created_by", sc, q) +
		picNarrow("cc.application_id", f, q) +
		adminNarrow("cc.created_by", f, q) + `
		 group by grouping sets ((t.admin_id), (t.team_id))`

	rows, err := r.pool.Query(ctx, sql, q.args...)
	if err != nil {
		return fmt.Errorf("team campaigns: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var perAdmin bool
		var adminID, teamID *uuid.UUID
		var bCreated, bSent, sCreated, sPublished, failed int
		if err := rows.Scan(&perAdmin, &adminID, &teamID,
			&bCreated, &bSent, &sCreated, &sPublished, &failed); err != nil {
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
		target.BroadcastsCreated = bCreated
		target.BroadcastsSent = bSent
		target.StoriesCreated = sCreated
		target.StoriesPublished = sPublished
		target.CampaignsFailed = failed
	}
	return rows.Err()
}

// teamLeads credits a verified-new lead to whoever answered it first.
func (r *Repo) teamLeads(
	ctx context.Context, sc Scope, f models.AnalyticsFilter, sink *metricSink,
) error {
	q := &queryArgs{}
	cte := fmt.Sprintf(teamCTE, q.add(sc.WorkspaceID))

	where := fmt.Sprintf(
		" where lc.workspace_id = %s and lc.lead_status = 'verified_new'", q.add(sc.WorkspaceID))
	if !f.From.IsZero() {
		where += fmt.Sprintf(" and lc.qualified_date >= (%s %s", q.add(f.From), jakartaDate)
	}
	if !f.To.IsZero() {
		where += fmt.Sprintf(" and lc.qualified_date <= (%s %s",
			q.add(f.To.Add(-time.Second)), jakartaDate)
	}
	if !sc.All {
		where += fmt.Sprintf(" and lc.application_id = any(%s)", q.add(sc.ApplicationIDs))
	}
	if f.ApplicationID != nil {
		where += fmt.Sprintf(" and lc.application_id = %s", q.add(*f.ApplicationID))
	}
	if f.AccountID != nil {
		where += fmt.Sprintf(" and lc.account_id = %s", q.add(*f.AccountID))
	}

	sql := cte + `,
		credited as (
			select lc.contact_id,
			       (select m.sent_by
			          from public.messages m
			          join public.conversations c on c.id = m.conversation_id
			         where c.contact_id = lc.contact_id
			           and m.from_me and m.hidden_at is null
			           and m.sender_source = 'web_admin'
			         order by m.timestamp
			         limit 1) as admin_id
			  from public.lead_classifications lc` + where + `
		)
		select grouping(t.admin_id) = 0 as per_admin, t.admin_id, t.team_id, count(*)
		  from credited
		  left join team t on t.admin_id = credited.admin_id
		 where true` +
		adminVisible("credited.admin_id", sc, q) +
		adminNarrow("credited.admin_id", f, q) + `
		 group by grouping sets ((t.admin_id), (t.team_id))`

	rows, err := r.pool.Query(ctx, sql, q.args...)
	if err != nil {
		return fmt.Errorf("team leads: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var perAdmin bool
		var adminID, teamID *uuid.UUID
		var n int
		if err := rows.Scan(&perAdmin, &adminID, &teamID, &n); err != nil {
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
		target.VerifiedNewLeads = n
	}
	return rows.Err()
}

// teamScheduleCompliance counts work done inside and outside the rota.
//
// Read from the flag stamped when the message was written, never by comparing
// against the rota as it stands now: a schedule corrected next month must not
// rewrite last month's record.
func (r *Repo) teamScheduleCompliance(
	ctx context.Context, sc Scope, f models.AnalyticsFilter, sink *metricSink,
) error {
	q := &queryArgs{}
	cte := fmt.Sprintf(teamCTE, q.add(sc.WorkspaceID))
	where := messageWhere(sc, f, q)

	sql := cte + `
		select grouping(t.admin_id) = 0 as per_admin, t.admin_id, t.team_id,
		       count(*) filter (where m.in_schedule),
		       count(*) filter (where m.in_schedule = false)
		  from public.messages m
		  join public.whatsapp_accounts a on a.id = m.account_id
		  join team t on t.admin_id = m.sent_by` + where + `
		   and m.from_me and m.sent_by is not null and m.hidden_at is null` +
		adminVisible("m.sent_by", sc, q) +
		picNarrow("a.application_id", f, q) +
		adminNarrow("m.sent_by", f, q) + `
		 group by grouping sets ((t.admin_id), (t.team_id))`

	rows, err := r.pool.Query(ctx, sql, q.args...)
	if err != nil {
		return fmt.Errorf("team schedule compliance: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var perAdmin bool
		var adminID, teamID *uuid.UUID
		var inSched, outSched int
		if err := rows.Scan(&perAdmin, &adminID, &teamID, &inSched, &outSched); err != nil {
			return err
		}
		key, ok := groupingKey(adminID, teamID, !perAdmin)
		if !ok {
			continue
		}
		m := sink.at(key)
		m.ActivitiesInSchedule = inSched
		m.ActivitiesOutOfSchedule = outSched
	}
	return rows.Err()
}

// teamSchedules fills worked hours and active days per person, and rolls them
// up to the team.
//
// Computed in Go rather than SQL because overlapping blocks must be counted
// once — somebody covering two applications in the same hour worked one hour —
// and that is interval arithmetic, not aggregation.
func (r *Repo) teamSchedules(
	ctx context.Context,
	sc Scope,
	f models.AnalyticsFilter,
	members []models.MemberPerformance,
	sink *metricSink,
) error {
	windows, err := r.ScheduleWindows(ctx, sc.WorkspaceID, WindowQuery{
		From:          f.From,
		To:            f.To,
		ApplicationID: f.ApplicationID,
		AccountID:     f.AccountID,
	})
	if err != nil {
		return err
	}

	byUser := map[uuid.UUID][]analytics.Window{}
	for _, w := range windows {
		if !f.From.IsZero() && w.Start.Before(f.From) {
			w.Start = f.From
		}
		if !f.To.IsZero() && w.End.After(f.To) {
			w.End = f.To
		}
		if w.End.After(w.Start) {
			byUser[w.UserID] = append(byUser[w.UserID], w)
		}
	}

	teamOf := map[uuid.UUID]uuid.UUID{}
	for _, m := range members {
		key := *m.UserID
		if m.PICUserID != nil {
			key = *m.PICUserID
		}
		teamOf[*m.UserID] = key
	}

	teamWindows := map[uuid.UUID][]analytics.Window{}
	for _, m := range members {
		own := byUser[*m.UserID]
		metrics := sink.at(memberKey{id: *m.UserID})
		metrics.WorkSeconds = analytics.WorkedSeconds(own)
		metrics.ActiveDays = len(dailyWorkSeconds(own))

		if key, ok := teamOf[*m.UserID]; ok {
			teamWindows[key] = append(teamWindows[key], own...)
		}
	}
	for key, ws := range teamWindows {
		metrics := sink.at(memberKey{id: key, team: true})
		metrics.WorkSeconds = analytics.WorkedSeconds(ws)
		metrics.ActiveDays = len(dailyWorkSeconds(ws))
	}
	return nil
}
