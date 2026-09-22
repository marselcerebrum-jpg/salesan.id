package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/salesan/omnichannel/backend/internal/analytics"
	"github.com/salesan/omnichannel/backend/internal/models"
)

// Drill-down listings.
//
// Every card on the Dashboard has to be openable: a Leader who sees "12 SLA
// terlewati" needs to reach the twelve conversations behind it, otherwise the
// number is a claim rather than a finding. These queries are the same
// restrictions as the aggregates, returning rows instead of counts.

const drillLimit = 200

func limitOf(n int) int {
	if n <= 0 || n > drillLimit {
		return drillLimit
	}
	return n
}

// MessageActivity lists the messages behind a chat figure.
//
// This is what "tekan angka Pesan Terkirim lalu lihat daftar pesan sumbernya"
// asks for. Every restriction the aggregate applies is applied here too, in the
// same order and with the same source filter — a drill-down that lists
// different rows from the number that opened it is worse than none at all.
//
// direction: "in" for customer messages, "out" for manual messages a person
// sent from the web, "device" for messages typed on the phone.
func (r *Repo) MessageActivity(
	ctx context.Context,
	sc Scope,
	f models.AnalyticsFilter,
	direction, chatType string,
	limit int,
) ([]models.MessageActivityRow, error) {
	q := &queryArgs{}
	where := messageWhere(sc, f, q)

	switch direction {
	case "out":
		where += " and m.from_me and m.sender_source = 'web_admin'"
		if f.AdminID != nil {
			where += fmt.Sprintf(" and m.sent_by = %s", q.add(*f.AdminID))
		}
	case "device":
		where += " and m.from_me and m.sender_source = 'whatsapp_device'"
	default:
		where += " and not m.from_me"
		where += adminTouchedConversation(f, q)
	}

	switch chatType {
	case models.ConversationTypePersonal, models.ConversationTypeGroup:
		where += fmt.Sprintf(" and c.type = %s::public.conversation_type", q.add(chatType))
	}

	rows, err := r.pool.Query(ctx, `
		select m.id, m.conversation_id, c.type::text, c.name, ct.phone_number,
		       app.code, acc.name, m.timestamp, m.type::text,
		       left(coalesce(nullif(m.body, ''), nullif(m.caption, ''), ''), 180),
		       m.sender_source::text, m.sent_by,
		       coalesce(nullif(u.full_name, ''), u.email), m.in_schedule
		  from public.messages m
		  join public.conversations c on c.id = m.conversation_id
		  join public.whatsapp_accounts a on a.id = m.account_id
		  left join public.contacts ct on ct.id = c.contact_id
		  left join public.applications app on app.id = a.application_id
		  left join public.whatsapp_accounts acc on acc.id = m.account_id
		  left join public.users u on u.id = m.sent_by`+where+`
		   and m.hidden_at is null
		 order by m.timestamp desc
		 limit `+fmt.Sprint(limitOf(limit)), q.args...)
	if err != nil {
		return nil, fmt.Errorf("message activity: %w", err)
	}
	defer rows.Close()

	out := []models.MessageActivityRow{}
	for rows.Next() {
		var m models.MessageActivityRow
		if err := rows.Scan(&m.ID, &m.ConversationID, &m.ChatType, &m.ConversationName,
			&m.PhoneNumber, &m.ApplicationCode, &m.AccountName, &m.Timestamp, &m.MessageType,
			&m.Preview, &m.Source, &m.ActorID, &m.ActorName, &m.InSchedule); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// SLACycles lists the cycles behind the SLA cards.
func (r *Repo) SLACycles(
	ctx context.Context, sc Scope, f models.AnalyticsFilter, status string, limit int,
) ([]models.SLACycleRow, error) {
	q := &queryArgs{}
	where := scopeWhere("s", sc, f, "started_at", q)
	if status != "" {
		where += fmt.Sprintf(" and s.status = %s::public.sla_status", q.add(status))
	}
	if f.AdminID != nil {
		where += fmt.Sprintf(" and s.responder_admin_id = %s", q.add(*f.AdminID))
	}

	// Waiting cycles are read longest-first. That is the order somebody acts on:
	// the customer who has been waiting two hours matters more than the one who
	// wrote a minute ago, and a newest-first list buries exactly the row the
	// card was pressed to find.
	order := " order by s.started_at desc"
	if status == "waiting" {
		order = " order by s.started_at asc"
	}

	rows, err := r.pool.Query(ctx, `
		select s.id, s.conversation_id, c.name, ct.phone_number, app.code, acc.name,
		       s.inbound_message_id, s.started_at, s.inbound_message_count,
		       s.first_response_message_id, s.responded_at,
		       s.raw_duration_seconds, s.business_duration_seconds, s.target_seconds,
		       s.status::text, s.exclusion_reason,
		       s.responder_admin_id, coalesce(nullif(u.full_name, ''), u.email),
		       s.responder_source::text,
		       -- The three ids a deep link needs. A link built from the phone
		       -- number alone opens the wrong room whenever the same customer
		       -- has written to two of our numbers.
		       s.application_id, s.account_id, app.color,
		       -- How long this one has been waiting, measured now rather than
		       -- in the browser, so every reader sees the same figure.
		       case when s.responded_at is null
		            then extract(epoch from (now() - s.started_at))::int
		            else null end
		  from public.sla_cycles s
		  join public.conversations c on c.id = s.conversation_id
		  -- The number lives on the contact, not on the thread; the inbox
		  -- listing resolves it the same way.
		  left join public.contacts ct on ct.id = c.contact_id
		  left join public.applications app on app.id = s.application_id
		  left join public.whatsapp_accounts acc on acc.id = s.account_id
		  left join public.users u on u.id = s.responder_admin_id`+where+order+
		fmt.Sprintf(" limit %d", limitOf(limit)), q.args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []models.SLACycleRow{}
	for rows.Next() {
		var s models.SLACycleRow
		if err := rows.Scan(&s.ID, &s.ConversationID, &s.ConversationName, &s.PhoneNumber,
			&s.ApplicationCode, &s.AccountName, &s.InboundMessageID, &s.StartedAt,
			&s.InboundMessageCount, &s.FirstResponseMessageID, &s.RespondedAt,
			&s.RawDurationSeconds, &s.BusinessDurationSeconds, &s.TargetSeconds,
			&s.Status, &s.ExclusionReason, &s.ResponderAdminID, &s.ResponderName,
			&s.ResponderSource,
			&s.ApplicationID, &s.AccountID, &s.ApplicationColor, &s.WaitingSeconds); err != nil {
			return nil, err
		}
		// Whether it has already blown the target it was measured against. Read
		// from the snapshot on the cycle, not from today's setting.
		if s.WaitingSeconds != nil && s.TargetSeconds > 0 {
			breached := *s.WaitingSeconds > s.TargetSeconds
			s.Breached = &breached
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// FollowUps lists the activities behind the follow-up cards.
func (r *Repo) FollowUps(
	ctx context.Context, sc Scope, f models.AnalyticsFilter, replied string, limit int,
) ([]models.FollowUpRow, error) {
	q := &queryArgs{}
	where := scopeWhere("fu", sc, f, "started_at", q)
	if f.AdminID != nil {
		where += fmt.Sprintf(" and fu.admin_id = %s", q.add(*f.AdminID))
	}
	switch replied {
	case "yes":
		where += " and fu.responded_at is not null"
	case "no":
		where += " and fu.responded_at is null"
	}

	rows, err := r.pool.Query(ctx, `
		select fu.id, fu.conversation_id, c.name, ct.phone_number, app.code,
		       to_char(fu.local_date, 'YYYY-MM-DD'), fu.started_at, fu.message_count,
		       fu.gap_seconds, fu.last_inbound_at,
		       fu.admin_id, coalesce(nullif(u.full_name, ''), u.email), fu.admin_source::text,
		       fu.responded_at, fu.trigger_message_id
		  from public.follow_up_events fu
		  join public.conversations c on c.id = fu.conversation_id
		  left join public.contacts ct on ct.id = c.contact_id
		  left join public.applications app on app.id = fu.application_id
		  left join public.users u on u.id = fu.admin_id`+where+
		fmt.Sprintf(" order by fu.started_at desc limit %d", limitOf(limit)), q.args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []models.FollowUpRow{}
	for rows.Next() {
		var f models.FollowUpRow
		if err := rows.Scan(&f.ID, &f.ConversationID, &f.ConversationName, &f.PhoneNumber,
			&f.ApplicationCode, &f.LocalDate, &f.StartedAt, &f.MessageCount,
			&f.GapSeconds, &f.LastInboundAt, &f.AdminID, &f.AdminName, &f.AdminSource,
			&f.RespondedAt, &f.TriggerMessageID); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// GroupMentions lists the mentions behind the group cards.
//
// The sender's name is resolved at read time from contacts, the same ladder the
// chat bubbles use — saved contact name, then push name, then the number — so
// renaming somebody on the phone updates old rows here too.
func (r *Repo) GroupMentions(
	ctx context.Context, sc Scope, f models.AnalyticsFilter, answered string, limit int,
) ([]models.GroupMentionRow, error) {
	q := &queryArgs{}
	where := scopeWhere("gm", sc, f, "mentioned_at", q)
	if f.AdminID != nil {
		where += fmt.Sprintf(" and gm.responder_admin_id = %s", q.add(*f.AdminID))
	}
	switch answered {
	case "yes":
		where += " and gm.responded_at is not null"
	case "no":
		where += " and gm.responded_at is null"
	}

	rows, err := r.pool.Query(ctx, `
		select gm.id, gm.conversation_id, c.name, app.code, acc.name, gm.message_id,
		       gm.participant_jid, gm.sender_phone,
		       coalesce(nullif(ct.name, ''), nullif(ct.push_name, ''),
		                nullif(m.sender_name, ''), gm.sender_phone),
		       coalesce(nullif(m.body, ''), nullif(m.caption, '')),
		       gm.mentioned_at, gm.responded_at,
		       gm.responder_admin_id, coalesce(nullif(u.full_name, ''), u.email),
		       gm.responder_source::text
		  from public.group_mentions gm
		  join public.conversations c on c.id = gm.conversation_id
		  join public.messages m on m.id = gm.message_id
		  left join public.contacts ct on ct.account_id = gm.account_id and ct.jid = gm.participant_jid
		  left join public.applications app on app.id = gm.application_id
		  left join public.whatsapp_accounts acc on acc.id = gm.account_id
		  left join public.users u on u.id = gm.responder_admin_id`+where+
		fmt.Sprintf(" order by gm.mentioned_at desc limit %d", limitOf(limit)), q.args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []models.GroupMentionRow{}
	for rows.Next() {
		var g models.GroupMentionRow
		if err := rows.Scan(&g.ID, &g.ConversationID, &g.GroupName, &g.ApplicationCode,
			&g.AccountName, &g.MessageID, &g.ParticipantJID, &g.SenderPhone, &g.SenderName,
			&g.Body, &g.MentionedAt, &g.RespondedAt, &g.ResponderAdminID,
			&g.ResponderName, &g.ResponderSource); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// LabelEvents lists the append-only label history.
func (r *Repo) LabelEvents(
	ctx context.Context, sc Scope, f models.AnalyticsFilter, eventType string, limit int,
) ([]models.LabelEventRow, error) {
	q := &queryArgs{}
	where := scopeWhere("le", sc, f, "occurred_at", q) + labelActorWhere("le", f, q)
	if eventType != "" {
		where += fmt.Sprintf(" and le.event_type = %s::public.label_event_type", q.add(eventType))
	}

	rows, err := r.pool.Query(ctx, `
		select le.id, le.event_type::text, le.contact_id,
		       coalesce(nullif(ct.name, ''), nullif(ct.push_name, ''), ct.phone_number),
		       ct.phone_number, le.conversation_id,
		       le.from_label_name, le.to_label_name, le.source::text,
		       le.admin_id, coalesce(nullif(u.full_name, ''), u.email),
		       app.code, acc.name, le.occurred_at
		  from public.contact_label_events le
		  left join public.contacts ct on ct.id = le.contact_id
		  left join public.applications app on app.id = le.application_id
		  left join public.whatsapp_accounts acc on acc.id = le.account_id
		  left join public.users u on u.id = le.admin_id`+where+
		fmt.Sprintf(" order by le.occurred_at desc limit %d", limitOf(limit)), q.args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []models.LabelEventRow{}
	for rows.Next() {
		var e models.LabelEventRow
		if err := rows.Scan(&e.ID, &e.EventType, &e.ContactID, &e.ContactName, &e.PhoneNumber,
			&e.ConversationID, &e.FromLabelName, &e.ToLabelName, &e.Source,
			&e.AdminID, &e.AdminName, &e.ApplicationCode, &e.AccountName,
			&e.OccurredAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// LabelTransitions lists the Cold -> Warm style moves in a period.
//
// Derived by pairing a removal with an addition on the same contact inside
// TransitionWindow, because neither the web nor the phone reports a move as
// such. Counting the pair rather than storing a third event is what keeps
// "total perubahan label" from double-counting one action.
// LabelUsage breaks the label figures down per label.
//
// Built from the same append-only event table the summary counts, so the rows
// and the total can never disagree. The label's name is read from the event
// rather than joined from the label table: a label renamed last week should
// still show the name it had when somebody attached it, and one deleted since
// should still appear rather than vanishing from its own history.
func (r *Repo) LabelUsage(
	ctx context.Context, sc Scope, f models.AnalyticsFilter, limit int,
) ([]models.LabelUsage, error) {
	q := &queryArgs{}
	where := scopeWhere("le", sc, f, "occurred_at", q) + labelActorWhere("le", f, q)

	rows, err := r.pool.Query(ctx, `
		with touched as (
			select coalesce(le.to_label_id, le.from_label_id) as label_id,
			       coalesce(le.to_label_name, le.from_label_name) as name,
			       le.event_type::text as event_type,
			       le.contact_id
			  from public.contact_label_events le`+where+`
			   and le.event_type in ('label_assigned', 'label_removed')
		)
		select t.label_id,
		       coalesce(max(t.name), 'Tanpa nama') as name,
		       coalesce(max(cl.color), '#64748B') as color,
		       count(*) filter (where t.event_type = 'label_assigned') as assigned,
		       count(*) filter (where t.event_type = 'label_removed') as removed,
		       count(distinct t.contact_id) as contacts,
		       coalesce(max(active.n), 0) as active_contacts
		  from touched t
		  left join public.conversation_labels cl on cl.id = t.label_id
		  left join lateral (
		       select count(distinct c.contact_id) as n
		         from public.conversation_label_assignments cla
		         join public.conversations c on c.id = cla.conversation_id
		        where cla.label_id = t.label_id and c.contact_id is not null
		  ) active on true
		 group by t.label_id
		 -- The expression is repeated rather than aliased: Postgres accepts a
		 -- bare output alias in ORDER BY but not one inside an expression.
		 order by count(*) desc`+fmt.Sprintf(" limit %d", limitOf(limit)), q.args...)
	if err != nil {
		return nil, fmt.Errorf("label usage: %w", err)
	}
	defer rows.Close()

	out := []models.LabelUsage{}
	for rows.Next() {
		var u models.LabelUsage
		if err := rows.Scan(&u.LabelID, &u.Name, &u.Color,
			&u.Assigned, &u.Removed, &u.Contacts, &u.ActiveContacts); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// UnattributedLabelEvents counts how many of the label changes in this period
// came from a phone, where WhatsApp names nobody.
//
// Those are left out of a person's figures — see labelActorWhere — so this
// number exists to say what the personal view is not showing, rather than let
// an empty card imply nothing happened on the number.
//
// Zero when no person is selected, because then the question does not arise.
func (r *Repo) UnattributedLabelEvents(
	ctx context.Context, sc Scope, f models.AnalyticsFilter,
) (int, error) {
	if f.AdminID == nil {
		return 0, nil
	}
	q := &queryArgs{}
	where := scopeWhere("le", sc, f, "occurred_at", q)
	where += " and le.admin_id is null"

	var n int
	err := r.pool.QueryRow(ctx,
		`select count(*) from public.contact_label_events le`+where, q.args...).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("unattributed label events: %w", err)
	}
	return n, nil
}

func (r *Repo) LabelTransitions(
	ctx context.Context, sc Scope, f models.AnalyticsFilter, limit int,
) ([]models.LabelTransition, error) {
	q := &queryArgs{}
	where := scopeWhere("le", sc, f, "occurred_at", q) + labelActorWhere("le", f, q)
	windowArg := q.add(fmt.Sprintf("%d seconds", int(TransitionWindow.Seconds())))

	rows, err := r.pool.Query(ctx, `
		select prev.from_label_name as from_label,
		       le.to_label_name as to_label,
		       count(*) as moves,
		       count(distinct le.contact_id) as contacts
		  from public.contact_label_events le
		  join lateral (
		    select p.from_label_name
		      from public.contact_label_events p
		     where p.contact_id = le.contact_id
		       and p.event_type = 'label_removed'
		       and p.occurred_at <= le.occurred_at
		       and p.occurred_at >= le.occurred_at - `+windowArg+`::interval
		     order by p.occurred_at desc
		     limit 1
		  ) prev on true`+where+`
		   and le.event_type = 'label_assigned'
		   and le.to_label_name is not null
		   and prev.from_label_name is not null
		 group by from_label, to_label
		 order by moves desc`+fmt.Sprintf(" limit %d", limitOf(limit)), q.args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []models.LabelTransition{}
	for rows.Next() {
		var t models.LabelTransition
		if err := rows.Scan(&t.FromLabel, &t.ToLabel, &t.Count, &t.Contacts); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Leads lists classified contacts.
//
// The status filter defaults to verified_new because that is what the Dashboard
// counts; historical and unknown are reachable so a Leader can check what was
// excluded and why, which is the whole point of storing the reason.
func (r *Repo) Leads(
	ctx context.Context, sc Scope, f models.AnalyticsFilter, status string, limit int,
) ([]models.LeadRow, error) {
	q := &queryArgs{}
	where := fmt.Sprintf(" where lc.workspace_id = %s", q.add(sc.WorkspaceID))
	if status == "" {
		status = analytics.LeadVerifiedNew
	}
	where += fmt.Sprintf(" and lc.lead_status = %s::public.lead_status", q.add(status))

	// Only a verified lead has a qualified date, so only that listing is
	// bounded by one. Historical and unknown contacts are shown in full: they
	// are consulted to check what the count left out, and hiding most of them
	// behind a date range defeats that.
	if status == analytics.LeadVerifiedNew {
		if !f.From.IsZero() {
			where += fmt.Sprintf(" and lc.qualified_date >= (%s %s", q.add(f.From), jakartaDate)
		}
		if !f.To.IsZero() {
			where += fmt.Sprintf(" and lc.qualified_date <= (%s %s",
				q.add(f.To.Add(-time.Second)), jakartaDate)
		}
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
	// Credited to whoever answered first, matching the aggregate exactly: a
	// drill-down that lists different rows from the number it opened is worse
	// than no drill-down.
	if f.AdminID != nil {
		where += fmt.Sprintf(` and exists (
			select 1 from public.messages m
			  join public.conversations c2 on c2.id = m.conversation_id
			 where c2.contact_id = lc.contact_id
			   and m.from_me and m.hidden_at is null
			   and m.sender_source in ('web_admin', 'whatsapp_device')
			   and m.sent_by = %s
			   and m.timestamp = (
			     select min(m2.timestamp) from public.messages m2
			      where m2.conversation_id = m.conversation_id
			        and m2.from_me and m2.hidden_at is null
			        and m2.sender_source in ('web_admin', 'whatsapp_device')))`,
			q.add(*f.AdminID))
	}

	rows, err := r.pool.Query(ctx, `
		select lc.contact_id,
		       coalesce(nullif(ct.name, ''), nullif(ct.push_name, ''), ct.phone_number),
		       ct.phone_number, app.code, acc.name,
		       lc.lead_status::text, lc.status_reason,
		       to_char(lc.qualified_date, 'YYYY-MM-DD'),
		       cf.first_inbound_at, cf.conversation_id
		  from public.lead_classifications lc
		  join public.contacts ct on ct.id = lc.contact_id
		  left join public.contact_first_seen cf on cf.contact_id = lc.contact_id
		  left join public.applications app on app.id = lc.application_id
		  left join public.whatsapp_accounts acc on acc.id = lc.account_id`+where+
		fmt.Sprintf(" order by cf.first_inbound_at desc nulls last limit %d", limitOf(limit)),
		q.args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []models.LeadRow{}
	for rows.Next() {
		var l models.LeadRow
		if err := rows.Scan(&l.ContactID, &l.Name, &l.PhoneNumber, &l.ApplicationCode,
			&l.AccountName, &l.LeadStatus, &l.StatusReason, &l.QualifiedDate,
			&l.FirstInboundAt, &l.ConversationID); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}
