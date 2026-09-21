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

// The Dashboard and Performa aggregates.
//
// Three rules run through every query here, and each of them is a decision that
// changes what the numbers mean:
//
//   - Messages are counted as BUBBLES. Three messages in a row from a customer
//     are three, not one conversation.
//   - Contacts are counted DISTINCT over the whole period, recomputed rather
//     than summed from the daily rows. The same person writing on two days is
//     one contact for the month; adding the daily figures would report two.
//   - "Manual" means sent from the web by a signed-in person. A message typed
//     on the phone is real work, but WhatsApp does not say who did it, so it is
//     reported beside the attributed figure instead of inside it. Broadcast,
//     story, bot and system messages are excluded outright.

// jakartaDate is the expression every daily bucket is built from.
//
// Written out once and reused, because a single query that buckets in UTC while
// its neighbours bucket in WIB produces a report whose columns disagree by a
// day for everything that happened after 17:00 — which is most of an evening
// shift.
const jakartaDate = `at time zone 'Asia/Jakarta')::date`

// manualWeb is the source filter for "a person did this from the web".
const manualWeb = `m.sender_source = 'web_admin'`

// queryArgs accumulates positional parameters while clauses are assembled.
type queryArgs struct{ args []any }

func (q *queryArgs) add(v any) string {
	q.args = append(q.args, v)
	return fmt.Sprintf("$%d", len(q.args))
}

// scopeWhere builds the workspace, scope and filter restrictions shared by
// every derived table.
//
// The RLS policies express the same rules for the browser's direct Supabase
// path. This is the API path's copy: the backend connects as the service role,
// for which RLS does not run, so leaving it out here would make the whole
// dashboard readable by anyone with a token.
func scopeWhere(alias string, sc Scope, f models.AnalyticsFilter, tsCol string, q *queryArgs) string {
	where := fmt.Sprintf(" where %s.workspace_id = %s", alias, q.add(sc.WorkspaceID))

	if !f.From.IsZero() {
		where += fmt.Sprintf(" and %s.%s >= %s", alias, tsCol, q.add(f.From))
	}
	if !f.To.IsZero() {
		where += fmt.Sprintf(" and %s.%s < %s", alias, tsCol, q.add(f.To))
	}

	if !sc.All {
		// A caller with a role but no applications sees nothing, which is what
		// an empty array produces here. That is the correct answer, not a bug:
		// somebody assigned nothing has nothing to look at.
		where += fmt.Sprintf(" and %s.application_id = any(%s)", alias, q.add(sc.ApplicationIDs))
	}
	if f.ApplicationID != nil {
		where += fmt.Sprintf(" and %s.application_id = %s", alias, q.add(*f.ApplicationID))
	}
	if f.AccountID != nil {
		where += fmt.Sprintf(" and %s.account_id = %s", alias, q.add(*f.AccountID))
	}
	if f.PICUserID != nil {
		// A PIC's scope is the set of applications they hold. Filtering by them
		// means filtering by that set, not by who happened to type the message.
		where += fmt.Sprintf(` and %s.application_id in (
			select application_id from public.pic_application_assignments where pic_user_id = %s)`,
			alias, q.add(*f.PICUserID))
	}
	return where
}

// labelActorWhere narrows the label history to one person, by the rule the
// operator set: a label change counts as long as it happened on their account,
// from the phone or from the web alike.
//
// A label change carries a person only when it was made on the web, where
// somebody was signed in. One made on the phone carries none — WhatsApp does
// not say who was holding it, and naming a guess would file one person's work
// under another's name. Those used to be dropped from a person's figures
// entirely, which meant a workspace that labels chats on the phone read "belum
// ada perubahan label" on days when labels had been changed all along.
//
// So they are counted for the account instead of discarded. scopeWhere has
// already restricted the rows to the accounts this reader may see, which is
// what makes "made by this person, or by nobody we can name" mean "on this
// person's numbers".
//
// The cost, stated rather than hidden: two people who share one number both see
// the same phone change in their own figures. That is the honest shape of what
// WhatsApp gives us — the change belongs to the number, not to a person — and
// the label card says so under the figures.
func labelActorWhere(alias string, f models.AnalyticsFilter, q *queryArgs) string {
	if f.AdminID == nil {
		return ""
	}
	return fmt.Sprintf(" and (%s.admin_id = %s or %s.admin_id is null)",
		alias, q.add(*f.AdminID), alias)
}

// messageWhere is the same restriction for a message query, where the columns
// live on three tables: the timestamp on the message, the application on the
// account, and the account's own id under a different name.
func messageWhere(sc Scope, f models.AnalyticsFilter, q *queryArgs) string {
	where := fmt.Sprintf(" where m.workspace_id = %s", q.add(sc.WorkspaceID))
	if !f.From.IsZero() {
		where += fmt.Sprintf(" and m.timestamp >= %s", q.add(f.From))
	}
	if !f.To.IsZero() {
		where += fmt.Sprintf(" and m.timestamp < %s", q.add(f.To))
	}
	if !sc.All {
		where += fmt.Sprintf(" and a.application_id = any(%s)", q.add(sc.ApplicationIDs))
	}
	if f.ApplicationID != nil {
		where += fmt.Sprintf(" and a.application_id = %s", q.add(*f.ApplicationID))
	}
	if f.AccountID != nil {
		where += fmt.Sprintf(" and m.account_id = %s", q.add(*f.AccountID))
	}
	if f.PICUserID != nil {
		where += fmt.Sprintf(` and a.application_id in (
			select application_id from public.pic_application_assignments where pic_user_id = %s)`,
			q.add(*f.PICUserID))
	}
	return where
}

// adminScope narrows a message query to one person's outgoing work.
//
// Inbound messages have no admin — nobody on our side wrote them — so when a
// report is about one person, the inbound side is restricted to conversations
// that person actually worked in. That is stated here rather than left implicit
// in a column heading.
func adminOutbound(f models.AnalyticsFilter, q *queryArgs) string {
	if f.AdminID == nil {
		return ""
	}
	return fmt.Sprintf(" and m.sent_by = %s", q.add(*f.AdminID))
}

func adminTouchedConversation(f models.AnalyticsFilter, q *queryArgs) string {
	if f.AdminID == nil {
		return ""
	}
	admin := q.add(*f.AdminID)
	from := q.add(f.From)
	to := q.add(f.To)
	return fmt.Sprintf(` and exists (
		select 1 from public.messages am
		 where am.conversation_id = m.conversation_id
		   and am.sent_by = %s and am.timestamp >= %s and am.timestamp < %s)`, admin, from, to)
}

// Dashboard builds one day's (or one range's) operational summary.
func (r *Repo) Dashboard(ctx context.Context, sc Scope, f models.AnalyticsFilter) (*models.DashboardSummary, error) {
	report, err := r.Performance(ctx, sc, f)
	if err != nil {
		return nil, err
	}
	return &report.Summary, nil
}

// Performance builds a range summary together with its per-day breakdown.
//
// The daily rows are grouped; the summary is the same data ungrouped. The
// distinct-contact figures are deliberately NOT summed from the days, because
// they do not add up.
func (r *Repo) Performance(ctx context.Context, sc Scope, f models.AnalyticsFilter) (*models.PerformanceReport, error) {
	if f.ScheduleID != nil {
		narrowed, err := r.narrowToShift(ctx, sc, f)
		if err != nil {
			return nil, err
		}
		f = narrowed
	}

	days := map[string]*models.DashboardSummary{}
	day := func(d string) *models.DashboardSummary {
		if days[d] == nil {
			days[d] = &models.DashboardSummary{Date: d}
		}
		return days[d]
	}

	if f.ChatType != models.ConversationTypeGroup {
		if err := r.collectPersonal(ctx, sc, f, day); err != nil {
			return nil, err
		}
		if err := r.collectLeads(ctx, sc, f, day); err != nil {
			return nil, err
		}
		// Response time, SLA and follow-up are personal-chat measures. A group
		// has no single customer waiting for an answer and a Broadcast is not a
		// question, so neither is scored — and skipping them when the filter is
		// set to groups keeps the page from showing a figure that would be read
		// as being about groups.
		if err := r.collectSLA(ctx, sc, f, day); err != nil {
			return nil, err
		}
		if err := r.collectFollowUp(ctx, sc, f, day); err != nil {
			return nil, err
		}
	}
	if f.ChatType != models.ConversationTypePersonal {
		if err := r.collectGroup(ctx, sc, f, day); err != nil {
			return nil, err
		}
	}
	if err := r.collectLabels(ctx, sc, f, day); err != nil {
		return nil, err
	}
	if err := r.collectCampaigns(ctx, sc, f, day); err != nil {
		return nil, err
	}
	if err := r.collectSchedule(ctx, sc, f, day); err != nil {
		return nil, err
	}

	windows, err := r.performanceWindows(ctx, sc, f)
	if err != nil {
		return nil, err
	}
	for d, secs := range dailyWorkSeconds(windows) {
		day(d).WorkSeconds = secs
	}

	keys := make([]string, 0, len(days))
	for k := range days {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	ordered := make([]models.PerformanceDay, 0, len(days))
	summary := models.DashboardSummary{}
	for _, k := range keys {
		d := days[k]
		ordered = append(ordered, models.PerformanceDay{Date: k, DashboardSummary: *d})
		addDay(&summary, d)
	}

	// Distinct counts are recomputed over the whole range. Summing the daily
	// figures would count the same contact once per day they wrote.
	if err := r.fillRangeDistincts(ctx, sc, f, &summary); err != nil {
		return nil, err
	}
	if f.ChatType != models.ConversationTypeGroup {
		if err := r.fillContactSchedule(ctx, sc, f, windows, &summary); err != nil {
			return nil, err
		}
	}
	if f.ChatType != models.ConversationTypeGroup {
		if err := r.fillRangeResponse(ctx, sc, f, &summary); err != nil {
			return nil, err
		}
	}
	summary.ActiveDays = countActiveDays(ordered)
	summary.ElapsedDays = elapsedDays(f.From, f.To)
	summary.WorkSeconds = analytics.WorkedSeconds(windows)
	summary.Date = f.From.In(analytics.Jakarta).Format("2006-01-02")
	// Whether any cycle in range was actually scored against a target. Read from
	// the cycles rather than from configuration, because the target is snapshot
	// onto each cycle when it is created — which is what stops a settings change
	// from rescoring history. With no scored cycle the interface says "Belum
	// dikonfigurasi" rather than drawing a zero that reads as a measurement.
	if err := r.pool.QueryRow(ctx,
		`select exists (
		   select 1 from public.sla_cycles
		    where workspace_id = $1 and target_seconds > 0
		      and ($2::timestamptz is null or started_at >= $2)
		      and ($3::timestamptz is null or started_at < $3))`,
		sc.WorkspaceID, nullTime(f.From), nullTime(f.To)).Scan(&summary.SLAConfigured); err != nil {
		return nil, fmt.Errorf("sla configured: %w", err)
	}

	return &models.PerformanceReport{
		From:    f.From.In(analytics.Jakarta).Format("2006-01-02"),
		To:      f.To.Add(-time.Second).In(analytics.Jakarta).Format("2006-01-02"),
		Summary: summary,
		Days:    ordered,
		PerHour: perHour(summary),
	}, nil
}

// narrowToShift turns a shift filter into the time window and person it means.
func (r *Repo) narrowToShift(ctx context.Context, sc Scope, f models.AnalyticsFilter) (models.AnalyticsFilter, error) {
	windows, err := r.ScheduleWindows(ctx, sc.WorkspaceID, WindowQuery{ScheduleID: f.ScheduleID})
	if err != nil {
		return f, err
	}
	if len(windows) == 0 {
		return f, ErrNotFound
	}
	w := windows[0]
	if f.From.IsZero() || w.Start.After(f.From) {
		f.From = w.Start
	}
	if f.To.IsZero() || w.End.Before(f.To) {
		f.To = w.End
	}
	if f.AdminID == nil {
		f.AdminID = &w.UserID
	}
	return f, nil
}

// --- collectors ---------------------------------------------------------------

// collectPersonal fills the one-to-one chat figures for each day.
func (r *Repo) collectPersonal(
	ctx context.Context, sc Scope, f models.AnalyticsFilter,
	day func(string) *models.DashboardSummary,
) error {
	q := &queryArgs{}
	where := messageWhere(sc, f, q)

	// Inbound is restricted to conversations the filtered admin worked in;
	// outbound is restricted to messages they actually sent.
	inboundNarrow := adminTouchedConversation(f, q)
	var outboundNarrow string
	if f.AdminID != nil {
		outboundNarrow = fmt.Sprintf(" and m.sent_by = %s", q.add(*f.AdminID))
	}

	sql := `
		select (m.timestamp ` + jakartaDate + ` as d,
		       count(*) filter (where not m.from_me` + inboundNarrow + `) as inbound,
		       count(*) filter (where m.from_me and ` + manualWeb + outboundNarrow + `) as outbound_web,
		       count(*) filter (where m.from_me and m.sender_source = 'whatsapp_device') as outbound_device,
		       count(distinct m.conversation_id) filter (where not m.from_me` + inboundNarrow + `) as convs_in,
		       count(distinct m.conversation_id) filter (
		         where m.from_me and ` + manualWeb + outboundNarrow + `) as convs_served
		  from public.messages m
		  join public.conversations c on c.id = m.conversation_id
		  join public.whatsapp_accounts a on a.id = m.account_id` + where + `
		   and c.type = 'personal'
		   and m.hidden_at is null
		   and (m.sender_source is null or m.sender_source not in ('broadcast', 'story', 'bot', 'system'))
		 group by d`

	rows, err := r.pool.Query(ctx, sql, q.args...)
	if err != nil {
		return fmt.Errorf("personal messages: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var d time.Time
		var inbound, outWeb, outDevice, convsIn, convsServed int
		if err := rows.Scan(&d, &inbound, &outWeb, &outDevice, &convsIn, &convsServed); err != nil {
			return err
		}
		s := day(d.Format("2006-01-02"))
		s.InboundPersonal = inbound
		s.OutboundManualPersonal = outWeb
		s.OutboundDevicePersonal = outDevice
		// Daily contact figures are per-day distinct; the range totals are
		// recomputed separately because these cannot be added together.
		s.ContactsInbound = convsIn
		s.ContactsServed = convsServed
		s.ContactsUnserved = convsIn - convsServed
		if s.ContactsUnserved < 0 {
			s.ContactsUnserved = 0
		}
	}
	return rows.Err()
}

// collectGroup fills the group figures, kept entirely apart from personal ones.
func (r *Repo) collectGroup(
	ctx context.Context, sc Scope, f models.AnalyticsFilter,
	day func(string) *models.DashboardSummary,
) error {
	q := &queryArgs{}
	where := messageWhere(sc, f, q)

	inboundNarrow := adminTouchedConversation(f, q)
	var outboundNarrow string
	if f.AdminID != nil {
		outboundNarrow = fmt.Sprintf(" and m.sent_by = %s", q.add(*f.AdminID))
	}

	// A broadcast into a group is a campaign, never a reply — excluded by the
	// sender_source filter below rather than by hoping nobody sends one.
	sql := `
		select (m.timestamp ` + jakartaDate + ` as d,
		       count(*) filter (where not m.from_me` + inboundNarrow + `) as inbound,
		       count(*) filter (where m.from_me and ` + manualWeb + outboundNarrow + `) as replies,
		       count(distinct m.conversation_id) filter (where not m.from_me` + inboundNarrow + `) as active,
		       count(distinct m.conversation_id) filter (
		         where m.from_me and ` + manualWeb + outboundNarrow + `) as handled
		  from public.messages m
		  join public.conversations c on c.id = m.conversation_id
		  join public.whatsapp_accounts a on a.id = m.account_id` + where + `
		   and c.type = 'group'
		   and m.hidden_at is null
		   and (m.sender_source is null or m.sender_source not in ('broadcast', 'story', 'bot', 'system'))
		 group by d`

	rows, err := r.pool.Query(ctx, sql, q.args...)
	if err != nil {
		return fmt.Errorf("group messages: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var d time.Time
		var inbound, replies, active, handled int
		if err := rows.Scan(&d, &inbound, &replies, &active, &handled); err != nil {
			return err
		}
		s := day(d.Format("2006-01-02"))
		s.GroupInbound = inbound
		s.GroupReplies = replies
		s.GroupsActive = active
		s.GroupsHandled = handled
	}
	return rows.Err()
}

func (r *Repo) collectLeads(
	ctx context.Context, sc Scope, f models.AnalyticsFilter,
	day func(string) *models.DashboardSummary,
) error {
	q := &queryArgs{}
	where := fmt.Sprintf(" where lc.workspace_id = %s and lc.lead_status = 'verified_new'",
		q.add(sc.WorkspaceID))
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
	if f.PICUserID != nil {
		where += fmt.Sprintf(` and lc.application_id in (
			select application_id from public.pic_application_assignments where pic_user_id = %s)`,
			q.add(*f.PICUserID))
	}
	if f.AdminID != nil {
		where += leadCreditedTo(q.add(*f.AdminID))
	}

	rows, err := r.pool.Query(ctx,
		`select lc.qualified_date, count(*)
		   from public.lead_classifications lc`+where+`
		  group by lc.qualified_date`, q.args...)
	if err != nil {
		return fmt.Errorf("leads: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var d time.Time
		var n int
		if err := rows.Scan(&d, &n); err != nil {
			return err
		}
		day(d.Format("2006-01-02")).VerifiedNewLeads = n
	}
	return rows.Err()
}

// leadCreditedTo restricts leads to those a given admin answered first.
//
// A lead belongs to nobody in the data — it is a contact. Answering it first is
// the closest thing to having brought it in, and stating the rule in one place
// keeps the aggregate and the drill-down from disagreeing.
func leadCreditedTo(adminParam string) string {
	return fmt.Sprintf(` and exists (
		select 1 from public.messages m
		  join public.conversations c on c.id = m.conversation_id
		 where c.contact_id = lc.contact_id
		   and m.from_me and m.hidden_at is null and m.sender_source = 'web_admin'
		   and m.sent_by = %s
		   and m.timestamp = (
		     select min(m2.timestamp) from public.messages m2
		      where m2.conversation_id = m.conversation_id
		        and m2.from_me and m2.hidden_at is null
		        and m2.sender_source = 'web_admin'))`, adminParam)
}

func (r *Repo) collectLabels(
	ctx context.Context, sc Scope, f models.AnalyticsFilter,
	day func(string) *models.DashboardSummary,
) error {
	q := &queryArgs{}
	where := scopeWhere("le", sc, f, "occurred_at", q) + labelActorWhere("le", f, q)
	windowArg := q.add(fmt.Sprintf("%d seconds", int(TransitionWindow.Seconds())))

	// A "move" is not stored as its own event: neither the web nor the phone
	// says that one label replaced another — both send a removal and an
	// addition. Inventing a third event would count one action twice, so the
	// pair is recognised here instead.
	sql := `
		select (le.occurred_at ` + jakartaDate + ` as d,
		       count(*),
		       count(*) filter (where le.event_type = 'label_assigned'),
		       count(*) filter (where le.event_type = 'label_removed'),
		       count(distinct le.contact_id),
		       count(*) filter (
		         where le.event_type = 'label_assigned'
		           and exists (
		             select 1 from public.contact_label_events prev
		              where prev.contact_id = le.contact_id
		                and prev.event_type = 'label_removed'
		                and prev.occurred_at <= le.occurred_at
		                and prev.occurred_at >= le.occurred_at - ` + windowArg + `::interval))
		  from public.contact_label_events le` + where + `
		 group by d`

	rows, err := r.pool.Query(ctx, sql, q.args...)
	if err != nil {
		return fmt.Errorf("label events: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var d time.Time
		var total, assigned, removed, contacts, moved int
		if err := rows.Scan(&d, &total, &assigned, &removed, &contacts, &moved); err != nil {
			return err
		}
		s := day(d.Format("2006-01-02"))
		s.LabelChangesTotal = total
		s.LabelsAssigned = assigned
		s.LabelsRemoved = removed
		s.LabelContactsChanged = contacts
		s.LabelsMoved = moved
	}
	if err := rows.Err(); err != nil {
		return err
	}

	// Contacts that went from no label at all to having one. Answered from
	// contact_label_state, because the current assignment table cannot say when
	// a contact first acquired a tag.
	q2 := &queryArgs{}
	w2 := scopeWhere("cls", sc, f, "first_labeled_at", q2)
	if f.AdminID != nil {
		w2 += fmt.Sprintf(` and exists (
			select 1 from public.contact_label_events le
			 where le.contact_id = cls.contact_id
			   and (le.admin_id = %s or le.admin_id is null))`, q2.add(*f.AdminID))
	}
	firstRows, err := r.pool.Query(ctx,
		`select (cls.first_labeled_at `+jakartaDate+` as d, count(*)
		   from public.contact_label_state cls`+w2+`
		    and cls.first_labeled_at is not null
		  group by d`, q2.args...)
	if err != nil {
		return fmt.Errorf("first labelled: %w", err)
	}
	defer firstRows.Close()
	for firstRows.Next() {
		var d time.Time
		var n int
		if err := firstRows.Scan(&d, &n); err != nil {
			return err
		}
		day(d.Format("2006-01-02")).ContactsFirstLabeled = n
	}
	return firstRows.Err()
}

func (r *Repo) collectCampaigns(
	ctx context.Context, sc Scope, f models.AnalyticsFilter,
	day func(string) *models.DashboardSummary,
) error {
	// Each pass builds its own argument list.
	//
	// Sharing one list across the three passes does not work: a query that is
	// handed a placeholder it never mentions fails with "could not determine
	// data type of parameter". So the shared predicates are a function of a
	// fresh `queryArgs`, called once per pass.
	scope := func(q *queryArgs) string {
		where := fmt.Sprintf(" where cc.workspace_id = %s", q.add(sc.WorkspaceID))
		if !sc.All {
			where += fmt.Sprintf(" and cc.application_id = any(%s)", q.add(sc.ApplicationIDs))
		}
		if f.ApplicationID != nil {
			where += fmt.Sprintf(" and cc.application_id = %s", q.add(*f.ApplicationID))
		}
		if f.AccountID != nil {
			where += fmt.Sprintf(" and cc.account_id = %s", q.add(*f.AccountID))
		}

		// Ownership, and only ownership.
		//
		// A campaign belongs to the person who created it, for its whole life.
		// The worker that runs a schedule at 09:00 does not transfer it to
		// whoever happens to be on shift then, and neither does an edit or a
		// cancellation by somebody else: those are recorded in scheduled_by,
		// updated_by and cancelled_by, and are never read here.
		if f.AdminID != nil {
			where += fmt.Sprintf(" and cc.created_by = %s", q.add(*f.AdminID))
		}

		// A PIC's combined figures are their own campaigns plus their team's,
		// not every campaign that happens to sit on an application they hold.
		//
		// This was a real discrepancy: filtering by application counted a
		// Leader's campaign on a shared application as the PIC's work.
		// `creator_pic_id` is a snapshot taken when the campaign was created,
		// so a later reshuffle of the team does not rewrite an old report.
		if f.PICUserID != nil {
			pic := q.add(*f.PICUserID)
			where += fmt.Sprintf(" and (cc.creator_pic_id = %s or cc.created_by = %s)", pic, pic)
		}
		return where
	}

	// Every status the campaign lifecycle can end in, so the card reports what
	// actually happened rather than collapsing three outcomes into two.
	// `partial` in particular has to stand on its own: calling it "selesai"
	// hides failures, calling it "gagal" invites somebody to re-send to people
	// who already received it.
	const bc = "cc.campaign_type = 'broadcast'"
	const st = "cc.campaign_type = 'story'"

	// Three date bases, because three different things are being counted.
	//
	// Booking everything on created_at was wrong in a way that only shows up
	// across a midnight: a broadcast written on the 10th and delivered on the
	// 11th put its "selesai" on the 10th, a day on which nothing was sent.
	//
	//   dibuat       -> created_at
	//   dijadwalkan  -> scheduled_at
	//   hasil        -> executed_at, else started_at, else cancelled_at, else created_at
	//
	// The chain on the last one walks from the most specific evidence to the
	// least. `executed_at` is stamped when a campaign settles, so it is the
	// right date for every finished outcome. `started_at` is stamped when the
	// worker picks the campaign up, and it is what makes "Sedang Dikirim"
	// land on the day the sending is happening: without it a campaign written
	// on the 8th and still running on the 11th was booked on the 8th, and
	// vanished entirely from a one-day filter. `cancelled_at` covers one
	// cancelled before it ever ran. Only a campaign that never left draft
	// falls through to created_at, and none of its outcome counters fire.
	const outcomeAt =
		"coalesce(cc.executed_at, cc.started_at, cc.cancelled_at, cc.created_at)"

	period := func(q *queryArgs, col string) string {
		out := ""
		if !f.From.IsZero() {
			out += fmt.Sprintf(" and %s >= %s", col, q.add(f.From))
		}
		if !f.To.IsZero() {
			out += fmt.Sprintf(" and %s < %s", col, q.add(f.To))
		}
		return out
	}

	// Pass 1: created.
	qc := &queryArgs{}
	createdSQL := `
		select (cc.created_at ` + jakartaDate + ` as d,
		       count(*) filter (where ` + bc + `),
		       count(*) filter (where ` + st + `)
		  from public.content_campaigns cc` + scope(qc) + period(qc, "cc.created_at") + `
		 group by d`
	if err := r.scanCampaignPair(ctx, createdSQL, qc.args, day,
		func(s *models.DashboardSummary, b, t int) {
			s.BroadcastsCreated, s.StoriesCreated = b, t
		}); err != nil {
		return err
	}

	// Pass 2: scheduled, on the day the schedule is set for.
	qs := &queryArgs{}
	scheduledSQL := `
		select (cc.scheduled_at ` + jakartaDate + ` as d,
		       count(*) filter (where ` + bc + `),
		       count(*) filter (where ` + st + `)
		  from public.content_campaigns cc` + scope(qs) +
		" and cc.scheduled_at is not null" + period(qs, "cc.scheduled_at") + `
		 group by d`
	if err := r.scanCampaignPair(ctx, scheduledSQL, qs.args, day,
		func(s *models.DashboardSummary, b, t int) {
			s.BroadcastsScheduled, s.StoriesScheduled = b, t
		}); err != nil {
		return err
	}

	// Pass 3: outcomes, on the day they happened.
	q := &queryArgs{}
	where := scope(q)
	sql := `
		select (` + outcomeAt + ` ` + jakartaDate + ` as d,
		       count(*) filter (where ` + bc + ` and cc.status = 'running'),
		       count(*) filter (where ` + bc + ` and cc.status = 'completed'),
		       count(*) filter (where ` + bc + ` and cc.status = 'partial'),
		       count(*) filter (where ` + bc + ` and cc.status = 'failed'),
		       count(*) filter (where ` + bc + ` and cc.status = 'cancelled'),
		       coalesce(sum(cc.success_count) filter (where ` + bc + `), 0),
		       count(*) filter (where ` + st + ` and cc.status = 'running'),
		       count(*) filter (where ` + st + ` and cc.status = 'completed'),
		       count(*) filter (where ` + st + ` and cc.status = 'partial'),
		       count(*) filter (where ` + st + ` and cc.status = 'failed'),
		       count(*) filter (where ` + st + ` and cc.status = 'expired'),
		       -- Views come from status_view_counts, the same place the Story
		       -- report and the chat Status panel read. A lower bound, never an
		       -- estimate. Reading story_views directly would have made this
		       -- number collapse to zero the moment a figure was frozen and its
		       -- viewer rows were dropped.
		       coalesce((
		         select sum(vc.viewers)
		           from public.story_publications p
		           join public.status_view_counts vc
		             on vc.account_id = p.account_id
		            and vc.wa_message_id = p.wa_message_id
		          where p.campaign_id = any(array_agg(cc.id) filter (where ` + st + `))
		       ), 0)
		  from public.content_campaigns cc` + where + period(q, outcomeAt) + `
		 group by d`

	rows, err := r.pool.Query(ctx, sql, q.args...)
	if err != nil {
		return fmt.Errorf("campaigns: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var d time.Time
		var s models.DashboardSummary
		if err := rows.Scan(&d,
			&s.BroadcastsRunning,
			&s.BroadcastsSent, &s.BroadcastsPartial, &s.BroadcastsFailed,
			&s.BroadcastsCancelled, &s.BroadcastTargetsSent,
			&s.StoriesRunning,
			&s.StoriesPublished, &s.StoriesPartial, &s.StoriesFailed,
			&s.StoriesExpired, &s.StoryViewsDetected); err != nil {
			return err
		}
		dst := day(d.Format("2006-01-02"))
		dst.BroadcastsRunning = s.BroadcastsRunning
		dst.BroadcastsSent = s.BroadcastsSent
		dst.BroadcastsPartial = s.BroadcastsPartial
		dst.BroadcastsFailed = s.BroadcastsFailed
		dst.BroadcastsCancelled = s.BroadcastsCancelled
		dst.BroadcastTargetsSent = s.BroadcastTargetsSent
		dst.StoriesRunning = s.StoriesRunning
		dst.StoriesPublished = s.StoriesPublished
		dst.StoriesPartial = s.StoriesPartial
		dst.StoriesFailed = s.StoriesFailed
		dst.StoriesExpired = s.StoriesExpired
		dst.StoryViewsDetected = s.StoryViewsDetected
	}
	return rows.Err()
}

// scanCampaignPair runs one of the simple two-column passes above.
//
// Both "created" and "scheduled" have the same shape, a date and a count per
// campaign type, and differ only in which date column they group on and which
// pair of fields they land in. Assigning rather than adding is deliberate:
// each pass owns its fields outright, so running them in any order gives the
// same answer.
func (r *Repo) scanCampaignPair(
	ctx context.Context,
	sql string,
	args []any,
	day func(string) *models.DashboardSummary,
	assign func(s *models.DashboardSummary, broadcasts, stories int),
) error {
	rows, err := r.pool.Query(ctx, sql, args...)
	if err != nil {
		return fmt.Errorf("campaigns: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var d time.Time
		var broadcasts, stories int
		if err := rows.Scan(&d, &broadcasts, &stories); err != nil {
			return err
		}
		assign(day(d.Format("2006-01-02")), broadcasts, stories)
	}
	return rows.Err()
}

// collectSchedule counts activity inside and outside the rota.
//
// Both figures come from the flag stamped on the row when it was written, never
// from re-reading the schedule now: a rota edited next month must not rewrite
// last month's compliance.
func (r *Repo) collectSchedule(
	ctx context.Context, sc Scope, f models.AnalyticsFilter,
	day func(string) *models.DashboardSummary,
) error {
	q := &queryArgs{}
	where := messageWhere(sc, f, q) + adminOutbound(f, q)

	sql := `
		select (m.timestamp ` + jakartaDate + ` as d,
		       count(*) filter (where m.in_schedule),
		       count(*) filter (where m.in_schedule = false)
		  from public.messages m
		  join public.whatsapp_accounts a on a.id = m.account_id` + where + `
		   and m.from_me and m.sent_by is not null and m.hidden_at is null
		 group by d`

	rows, err := r.pool.Query(ctx, sql, q.args...)
	if err != nil {
		return fmt.Errorf("schedule compliance: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var d time.Time
		var inSched, outSched int
		if err := rows.Scan(&d, &inSched, &outSched); err != nil {
			return err
		}
		s := day(d.Format("2006-01-02"))
		s.ActivitiesInSchedule = inSched
		s.ActivitiesOutOfSchedule = outSched
	}
	return rows.Err()
}

// fillRangeDistincts recomputes the contact and group figures over the whole
// period, because distinct counts cannot be summed from the daily rows.
// fillContactSchedule splits the inbound contacts by the rota.
//
// A contact belongs to the side their FIRST message of the period landed on.
// Anything else double counts: somebody who writes at 2am and again at 10am
// is one contact who arrived out of hours, not one of each.
//
// The windows are already resolved to absolute times by the caller, so they
// are handed to Postgres as two parallel arrays and the whole thing stays one
// query. Doing it in Go would mean pulling back one row per contact.
//
// With no window at all the split is not a measurement, and saying so is the
// point of ScheduleConfigured: a card that led on "0 kontak dalam jam kerja"
// because nobody filled in a rota would read as a catastrophe instead of a
// blank setting.
func (r *Repo) fillContactSchedule(
	ctx context.Context,
	sc Scope,
	f models.AnalyticsFilter,
	windows []analytics.Window,
	out *models.DashboardSummary,
) error {
	if len(windows) == 0 {
		out.ScheduleConfigured = false
		out.ContactsInboundInSchedule = out.ContactsInbound
		out.ContactsInboundOutOfSchedule = 0
		return nil
	}
	out.ScheduleConfigured = true

	starts := make([]time.Time, 0, len(windows))
	ends := make([]time.Time, 0, len(windows))
	for _, w := range windows {
		starts = append(starts, w.Start)
		ends = append(ends, w.End)
	}

	q := &queryArgs{}
	where := messageWhere(sc, f, q)
	inboundNarrow := adminTouchedConversation(f, q)
	from := q.add(starts)
	to := q.add(ends)

	sql := `
		with first_message as (
		  select coalesce(c.contact_id::text, c.chat_jid) as who,
		         min(m.timestamp) as at
		    from public.messages m
		    join public.conversations c on c.id = m.conversation_id
		    join public.whatsapp_accounts a on a.id = m.account_id` + where + `
		     and c.type = 'personal' and m.hidden_at is null
		     and not m.from_me` + inboundNarrow + `
		     and (m.sender_source is null
		          or m.sender_source not in ('broadcast', 'story', 'bot', 'system'))
		   group by 1
		), classified as (
		  select fm.who,
		         exists (
		           select 1
		             from unnest(` + from + `::timestamptz[], ` + to + `::timestamptz[]) as w(s, e)
		            where fm.at >= w.s and fm.at < w.e
		         ) as inside
		    from first_message fm
		)
		select count(*) filter (where inside), count(*) filter (where not inside)
		  from classified`

	if err := r.pool.QueryRow(ctx, sql, q.args...).
		Scan(&out.ContactsInboundInSchedule, &out.ContactsInboundOutOfSchedule); err != nil {
		return fmt.Errorf("contacts by schedule: %w", err)
	}
	return nil
}

func (r *Repo) fillRangeDistincts(
	ctx context.Context, sc Scope, f models.AnalyticsFilter, out *models.DashboardSummary,
) error {
	if f.ChatType != models.ConversationTypeGroup {
		q := &queryArgs{}
		where := messageWhere(sc, f, q)
		inboundNarrow := adminTouchedConversation(f, q)
		var outboundNarrow string
		if f.AdminID != nil {
			outboundNarrow = fmt.Sprintf(" and m.sent_by = %s", q.add(*f.AdminID))
		}

		// Counted on the contact, not the conversation: one person may reach the
		// same number through two of our devices, and that is one contact.
		sql := `
			select count(distinct coalesce(c.contact_id::text, c.chat_jid))
			         filter (where not m.from_me` + inboundNarrow + `),
			       count(distinct coalesce(c.contact_id::text, c.chat_jid))
			         filter (where m.from_me and ` + manualWeb + outboundNarrow + `)
			  from public.messages m
			  join public.conversations c on c.id = m.conversation_id
			  join public.whatsapp_accounts a on a.id = m.account_id` + where + `
			   and c.type = 'personal' and m.hidden_at is null
			   and (m.sender_source is null or m.sender_source not in ('broadcast', 'story', 'bot', 'system'))`

		if err := r.pool.QueryRow(ctx, sql, q.args...).
			Scan(&out.ContactsInbound, &out.ContactsServed); err != nil {
			return fmt.Errorf("distinct contacts: %w", err)
		}
		out.ContactsUnserved = out.ContactsInbound - out.ContactsServed
		if out.ContactsUnserved < 0 {
			out.ContactsUnserved = 0
		}
	}

	if f.ChatType != models.ConversationTypePersonal {
		q := &queryArgs{}
		where := messageWhere(sc, f, q)
		inboundNarrow := adminTouchedConversation(f, q)
		var outboundNarrow string
		if f.AdminID != nil {
			outboundNarrow = fmt.Sprintf(" and m.sent_by = %s", q.add(*f.AdminID))
		}

		sql := `
			select count(distinct m.conversation_id) filter (where not m.from_me` + inboundNarrow + `),
			       count(distinct m.conversation_id) filter (
			         where m.from_me and ` + manualWeb + outboundNarrow + `)
			  from public.messages m
			  join public.conversations c on c.id = m.conversation_id
			  join public.whatsapp_accounts a on a.id = m.account_id` + where + `
			   and c.type = 'group' and m.hidden_at is null
			   and (m.sender_source is null or m.sender_source not in ('broadcast', 'story', 'bot', 'system'))`

		if err := r.pool.QueryRow(ctx, sql, q.args...).
			Scan(&out.GroupsActive, &out.GroupsHandled); err != nil {
			return fmt.Errorf("distinct groups: %w", err)
		}
	}

	q := &queryArgs{}
	w := scopeWhere("le", sc, f, "occurred_at", q) + labelActorWhere("le", f, q)
	return r.pool.QueryRow(ctx,
		`select count(distinct le.contact_id) from public.contact_label_events le`+w,
		q.args...).Scan(&out.LabelContactsChanged)
}

// performanceWindows loads the shifts that back the per-hour figures.
func (r *Repo) performanceWindows(
	ctx context.Context, sc Scope, f models.AnalyticsFilter,
) ([]analytics.Window, error) {
	windows, err := r.ScheduleWindows(ctx, sc.WorkspaceID, WindowQuery{
		From:          f.From,
		To:            f.To,
		UserID:        f.AdminID,
		ApplicationID: f.ApplicationID,
		AccountID:     f.AccountID,
		PICUserID:     f.PICUserID,
	})
	if err != nil {
		return nil, err
	}

	// A shift that only partly overlaps the period counts only for the part
	// that does; otherwise a one-day range would import a whole week of
	// scheduled hours through its edges.
	clipped := make([]analytics.Window, 0, len(windows))
	for _, w := range windows {
		if !f.From.IsZero() && w.Start.Before(f.From) {
			w.Start = f.From
		}
		if !f.To.IsZero() && w.End.After(f.To) {
			w.End = f.To
		}
		if w.End.After(w.Start) {
			clipped = append(clipped, w)
		}
	}
	if !sc.All && f.AdminID == nil {
		allowed := make([]analytics.Window, 0, len(clipped))
		for _, w := range clipped {
			if sc.CanSeeAdmin(w.UserID) {
				allowed = append(allowed, w)
			}
		}
		clipped = allowed
	}
	return clipped, nil
}

// dailyWorkSeconds splits merged windows across the WIB days they cover.
func dailyWorkSeconds(windows []analytics.Window) map[string]int {
	out := map[string]int{}
	for _, w := range analytics.MergeWindows(windows) {
		cursor := w.Start
		for cursor.Before(w.End) {
			local := cursor.In(analytics.Jakarta)
			midnight := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, analytics.Jakarta).
				AddDate(0, 0, 1)
			end := w.End
			if midnight.Before(end) {
				end = midnight
			}
			out[local.Format("2006-01-02")] += int(end.Sub(cursor).Seconds())
			cursor = end
		}
	}
	return out
}

// addDay folds one day's figures into the range total. Distinct counts are
// deliberately left out; fillRangeDistincts computes those.
func addDay(sum *models.DashboardSummary, d *models.DashboardSummary) {
	sum.InboundPersonal += d.InboundPersonal
	sum.OutboundManualPersonal += d.OutboundManualPersonal
	sum.OutboundDevicePersonal += d.OutboundDevicePersonal
	sum.VerifiedNewLeads += d.VerifiedNewLeads

	sum.SLAAchieved += d.SLAAchieved
	sum.SLABreached += d.SLABreached
	sum.SLAWaiting += d.SLAWaiting
	// Counts add; the queue's average and slowest are recomputed over the whole
	// range by fillRangeResponse, for the same reason the SLA durations are.
	sum.QueuedTotal += d.QueuedTotal
	sum.QueuedAnswered += d.QueuedAnswered
	sum.QueuedWaiting += d.QueuedWaiting
	sum.FollowUps += d.FollowUps
	sum.FollowUpsAnswered += d.FollowUpsAnswered
	sum.FollowUpsUnanswered += d.FollowUpsUnanswered

	sum.GroupInbound += d.GroupInbound
	sum.GroupReplies += d.GroupReplies

	sum.ContactsFirstLabeled += d.ContactsFirstLabeled
	sum.LabelChangesTotal += d.LabelChangesTotal
	sum.LabelsAssigned += d.LabelsAssigned
	sum.LabelsRemoved += d.LabelsRemoved
	sum.LabelsMoved += d.LabelsMoved

	sum.BroadcastsCreated += d.BroadcastsCreated
	sum.BroadcastsScheduled += d.BroadcastsScheduled
	sum.BroadcastsRunning += d.BroadcastsRunning
	sum.BroadcastsSent += d.BroadcastsSent
	sum.BroadcastsPartial += d.BroadcastsPartial
	sum.BroadcastsFailed += d.BroadcastsFailed
	sum.BroadcastsCancelled += d.BroadcastsCancelled
	sum.BroadcastTargetsSent += d.BroadcastTargetsSent
	sum.StoriesCreated += d.StoriesCreated
	sum.StoriesScheduled += d.StoriesScheduled
	sum.StoriesRunning += d.StoriesRunning
	sum.StoriesPublished += d.StoriesPublished
	sum.StoriesPartial += d.StoriesPartial
	sum.StoriesFailed += d.StoriesFailed
	sum.StoriesExpired += d.StoriesExpired
	sum.StoryViewsDetected += d.StoryViewsDetected

	// SLACompleted is deliberately not summed here: fillRangeResponse
	// recomputes it over the whole range alongside the average, the median and
	// the extremes, so all five describe exactly the same set of cycles.

	sum.ActivitiesInSchedule += d.ActivitiesInSchedule
	sum.ActivitiesOutOfSchedule += d.ActivitiesOutOfSchedule
}

// elapsedDays is how many WIB calendar days of the period have happened.
//
// The divisor for every per-day average, and the reason it exists is the
// current month: on the third of the month a period runs to the thirty-first,
// and dividing by thirty-one would report a third of the real rate. Future days
// are not days somebody failed to work.
//
// A day that has started counts in full — somebody looking at 09:00 wants
// today included, not today weighted at three eighths.
func elapsedDays(from, to time.Time) int {
	if from.IsZero() || to.IsZero() {
		return 0
	}
	now := time.Now().In(analytics.Jakarta)

	// `to` is exclusive everywhere in this package — a one-day range is
	// [00:00 today, 00:00 tomorrow). So the last day of a period that has
	// already finished is the day before `to`, and treating the boundary as
	// inclusive counts one day too many. A period still running stops at today
	// instead, because tomorrow is not a day anybody failed to work.
	end := to
	if end.After(now) {
		end = now
	} else {
		end = end.Add(-time.Nanosecond)
	}
	if !end.After(from) {
		return 1 // a period that has only just begun is still one day of work
	}

	start := from.In(analytics.Jakarta)
	startDay := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, analytics.Jakarta)
	last := end.In(analytics.Jakarta)
	lastDay := time.Date(last.Year(), last.Month(), last.Day(), 0, 0, 0, 0, analytics.Jakarta)

	days := int(lastDay.Sub(startDay).Hours()/24) + 1
	if days < 1 {
		return 1
	}
	return days
}

// countActiveDays counts days on which anything at all happened.
func countActiveDays(days []models.PerformanceDay) int {
	n := 0
	for _, d := range days {
		if d.InboundPersonal+d.OutboundManualPersonal+d.GroupInbound+d.GroupReplies+
			d.LabelChangesTotal+d.BroadcastsCreated+d.StoriesCreated > 0 {
			n++
		}
	}
	return n
}

// perHour expresses the effort against scheduled time.
//
// Null rather than zero when nothing was scheduled: "no rota recorded" and
// "worked all day and achieved nothing" are different statements, and a report
// that renders both as 0.0 makes the wrong one look true.
func perHour(s models.DashboardSummary) models.PerHourMetrics {
	hours := float64(s.WorkSeconds) / 3600
	out := models.PerHourMetrics{WorkHours: round2(hours)}
	if hours <= 0 {
		return out
	}
	out.MessagesPerHour = f64(round2(float64(s.OutboundManualPersonal) / hours))
	out.ChatsPerHour = f64(round2(float64(s.ContactsServed) / hours))
	out.GroupRepliesPerHour = f64(round2(float64(s.GroupReplies) / hours))
	out.FollowUpsPerHour = f64(round2(float64(s.FollowUps) / hours))
	return out
}

func round2(v float64) float64 { return float64(int(v*100+0.5)) / 100 }
func f64(v float64) *float64   { return &v }

// nullTime turns a zero time into a SQL null, so an unbounded range is written
// once in the query rather than assembled by string surgery.
func nullTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

func roundSeconds(v *float64) *int {
	if v == nil {
		return nil
	}
	n := int(*v + 0.5)
	return &n
}

// FilterOptions lists everything the filter row may offer this caller.
//
// `pic` narrows the result to one PIC's world: their applications, the accounts
// on them, and the Freelance reporting to them. That is what makes the filter
// row behave as a hierarchy rather than as four independent dropdowns.
func (r *Repo) FilterOptions(
	ctx context.Context, sc Scope, pic *uuid.UUID,
) (*models.FilterOptions, error) {
	out := &models.FilterOptions{
		Role:         sc.Role,
		Applications: []models.AppRef{},
		Accounts:     []models.AccountRef{},
		PICs:         []models.PersonRef{},
		Freelancers:  []models.PersonRef{},
		Shifts:       []models.ShiftRef{},
	}

	picApps := func(col string, q *queryArgs) string {
		if pic == nil {
			return ""
		}
		return fmt.Sprintf(` and %s in (
			select application_id from public.pic_application_assignments where pic_user_id = %s)`,
			col, q.add(*pic))
	}

	appB := &queryArgs{}
	appQ := fmt.Sprintf(
		`select id, code, name, color from public.applications where workspace_id = %s`,
		appB.add(sc.WorkspaceID))
	if !sc.All {
		appQ += fmt.Sprintf(" and id = any(%s)", appB.add(sc.ApplicationIDs))
	}
	appQ += picApps("id", appB) + " order by sort_order, code"

	rows, err := r.pool.Query(ctx, appQ, appB.args...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var a models.AppRef
		if err := rows.Scan(&a.ID, &a.Code, &a.Name, &a.Color); err != nil {
			rows.Close()
			return nil, err
		}
		out.Applications = append(out.Applications, a)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	accB := &queryArgs{}
	accQ := fmt.Sprintf(
		`select id, name, phone_number, application_id from public.whatsapp_accounts
		  where workspace_id = %s`, accB.add(sc.WorkspaceID))
	if !sc.All {
		accQ += fmt.Sprintf(" and application_id = any(%s)", accB.add(sc.ApplicationIDs))
	}
	accQ += picApps("application_id", accB) + " order by name"

	accRows, err := r.pool.Query(ctx, accQ, accB.args...)
	if err != nil {
		return nil, err
	}
	for accRows.Next() {
		var a models.AccountRef
		if err := accRows.Scan(&a.ID, &a.Name, &a.PhoneNumber, &a.ApplicationID); err != nil {
			accRows.Close()
			return nil, err
		}
		out.Accounts = append(out.Accounts, a)
	}
	accRows.Close()
	if err := accRows.Err(); err != nil {
		return nil, err
	}

	// People. A Freelance sees only themselves in these lists, matching what
	// the reports allow — a filter row must not advertise a colleague whose
	// numbers cannot be opened.
	peopleB := &queryArgs{}
	peopleQ := fmt.Sprintf(`
		select u.id, coalesce(nullif(u.full_name, ''), u.email), ra.role::text
		  from public.users u
		  join public.role_assignments ra on ra.user_id = u.id and ra.is_active
		  left join public.freelancer_pic_assignments fp on fp.freelancer_user_id = u.id
		 where u.workspace_id = %s and u.is_active`, peopleB.add(sc.WorkspaceID))
	if !sc.All {
		peopleQ += fmt.Sprintf(" and u.id = any(%s)", peopleB.add(sc.AdminIDs))
	}
	if pic != nil {
		p := peopleB.add(*pic)
		peopleQ += fmt.Sprintf(" and (ra.role <> 'freelance' or fp.pic_user_id = %s)", p)
	}
	peopleQ += " order by 2"

	pRows, err := r.pool.Query(ctx, peopleQ, peopleB.args...)
	if err != nil {
		return nil, err
	}
	for pRows.Next() {
		var p models.PersonRef
		if err := pRows.Scan(&p.ID, &p.Name, &p.Role); err != nil {
			pRows.Close()
			return nil, err
		}
		switch p.Role {
		case models.RolePIC:
			out.PICs = append(out.PICs, p)
		case models.RoleFreelance:
			out.Freelancers = append(out.Freelancers, p)
		}
	}
	pRows.Close()
	if err := pRows.Err(); err != nil {
		return nil, err
	}

	// Shifts for the last month; older ones are history, not a filter anyone
	// reaches for.
	shiftB := &queryArgs{}
	shiftQ := fmt.Sprintf(`
		select s.id, coalesce(nullif(u.full_name, ''), u.email),
		       to_char(s.work_date, 'YYYY-MM-DD'),
		       to_char(s.starts_at, 'HH24:MI') || '-' || to_char(s.ends_at, 'HH24:MI'),
		       s.user_id
		  from public.work_schedules s
		  join public.users u on u.id = s.user_id
		 where s.workspace_id = %s and s.is_active
		   and s.work_date >= (now() at time zone 'Asia/Jakarta')::date - 31`,
		shiftB.add(sc.WorkspaceID))
	if !sc.All {
		shiftQ += fmt.Sprintf(" and s.user_id = any(%s)", shiftB.add(sc.AdminIDs))
	}
	if pic != nil {
		p := shiftB.add(*pic)
		shiftQ += fmt.Sprintf(" and (s.pic_user_id = %s or s.user_id = %s)", p, p)
	}
	shiftQ += " order by s.work_date desc, s.starts_at limit 200"

	sRows, err := r.pool.Query(ctx, shiftQ, shiftB.args...)
	if err != nil {
		return nil, err
	}
	defer sRows.Close()
	for sRows.Next() {
		var s models.ShiftRef
		var window string
		if err := sRows.Scan(&s.ID, &s.UserName, &s.WorkDate, &window, &s.UserID); err != nil {
			return nil, err
		}
		s.Label = fmt.Sprintf("%s · %s · %s", s.UserName, s.WorkDate, window)
		out.Shifts = append(out.Shifts, s)
	}
	return out, sRows.Err()
}

// UpcomingCampaigns lists the next scheduled Story/Broadcast runs.
func (r *Repo) UpcomingCampaigns(ctx context.Context, sc Scope, limit int) ([]models.Campaign, error) {
	q := `
		select ` + campaignColumns + `
		  from public.content_campaigns cc
		  left join public.applications app on app.id = cc.application_id
		  left join public.whatsapp_accounts acc on acc.id = cc.account_id
		  left join public.users u on u.id = cc.created_by
		  left join public.role_assignments ra on ra.user_id = cc.created_by and ra.is_active
		 where cc.workspace_id = $1 and cc.status = 'scheduled' and cc.scheduled_at >= now()`
	args := []any{sc.WorkspaceID}
	if !sc.All {
		q += " and (cc.application_id = any($2) or cc.created_by = $3)"
		args = append(args, sc.ApplicationIDs, sc.UserID)
	}
	q += fmt.Sprintf(" order by cc.scheduled_at limit %d", limit)

	rows, err := r.pool.Query(ctx, q, args...)
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
