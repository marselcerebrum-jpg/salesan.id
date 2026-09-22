package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/salesan/omnichannel/backend/internal/models"
)

// The unified activity history.
//
// "Aktivitas" used to be its own screen, which was the wrong shape: what
// somebody wants is not a list of everything that happened, it is what this
// person did — read directly under their own figures, where it explains them.
// So this is a feed scoped like every other report and shown inside a person's
// detail rather than beside it.
//
// Five sources, unioned in the database rather than merged in Go:
//
//	messages           replying in a personal chat or a group
//	contact_label_events   attaching, removing and moving labels
//	follow_up_events   reaching back out to a quiet contact
//	admin_activity_logs    creating, scheduling, running, cancelling and
//	                       retrying a Broadcast or a Story
//
// Merging in Go would mean fetching a page from each and hoping the overlap
// works out; a database union pages correctly by construction.
//
// Every row carries the ids needed to open what it refers to, because an
// activity you cannot follow to its source is a claim rather than a record.

// ActivityCursor is the keyset position for the next page.
//
// A timestamp alone is not enough: several label changes land in the same
// millisecond, and paging on time only would either repeat them or skip them.
type ActivityCursor struct {
	Before time.Time
	// BeforeID breaks ties at the same instant.
	BeforeID string
}

// ActivityFeed returns one page of activity, newest first.
//
// `f.AdminID` narrows it to one person — which is how it is used from a
// performance detail. Left unset it is the whole visible scope, including the
// device activity nobody can be credited with.
func (r *Repo) ActivityFeed(
	ctx context.Context,
	sc Scope,
	f models.AnalyticsFilter,
	cursor ActivityCursor,
	limit int,
) ([]models.ActivityRow, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}

	q := &queryArgs{}
	ws := q.add(sc.WorkspaceID)

	// The scope restriction, written once and applied to every branch. Each
	// source names its application differently, so the column is a parameter.
	scoped := func(appCol, tsCol string) string {
		where := ""
		if !f.From.IsZero() {
			where += fmt.Sprintf(" and %s >= %s", tsCol, q.add(f.From))
		}
		if !f.To.IsZero() {
			where += fmt.Sprintf(" and %s < %s", tsCol, q.add(f.To))
		}
		if !sc.All {
			where += fmt.Sprintf(" and %s = any(%s)", appCol, q.add(sc.ApplicationIDs))
		}
		if f.ApplicationID != nil {
			where += fmt.Sprintf(" and %s = %s", appCol, q.add(*f.ApplicationID))
		}
		if f.PICUserID != nil {
			where += fmt.Sprintf(` and %s in (
				select application_id from public.pic_application_assignments
				 where pic_user_id = %s)`, appCol, q.add(*f.PICUserID))
		}
		return where
	}

	actorFilter := func(col string) string {
		if f.AdminID != nil {
			return fmt.Sprintf(" and %s = %s", col, q.add(*f.AdminID))
		}
		// A Freelance sees only their own activity, whatever they ask for.
		if !sc.All && sc.Role == models.RoleFreelance {
			return fmt.Sprintf(" and %s = %s", col, q.add(sc.UserID))
		}
		if !sc.All {
			return fmt.Sprintf(" and (%s = any(%s) or %s is null)",
				col, q.add(sc.AdminIDs), col)
		}
		return ""
	}

	// The label history follows labelActorWhere: one person's feed holds the
	// changes that person made. A change made on the phone names nobody, so it
	// appears only in the account-wide feed, never under a person.
	labelActor := actorFilter

	accountFilter := func(col string) string {
		if f.AccountID == nil {
			return ""
		}
		return fmt.Sprintf(" and %s = %s", col, q.add(*f.AccountID))
	}

	// Schedule compliance, applied only to sources that actually carry the flag.
	scheduleFilter := func(col string) string {
		if f.InSchedule == nil {
			return ""
		}
		return fmt.Sprintf(" and %s = %s", col, q.add(*f.InSchedule))
	}

	// --- messages -------------------------------------------------------------
	//
	// Manual replies only. Broadcast, story, bot and system traffic is activity
	// of a different kind and arrives through the campaign branch instead.
	msgWhere := fmt.Sprintf("m.workspace_id = %s", ws) +
		scoped("acc.application_id", "m.timestamp") +
		actorFilter("m.sent_by") + accountFilter("m.account_id") +
		scheduleFilter("m.in_schedule")

	// --- labels ---------------------------------------------------------------
	labelWhere := fmt.Sprintf("le.workspace_id = %s", ws) +
		scoped("le.application_id", "le.occurred_at") +
		labelActor("le.admin_id") + accountFilter("le.account_id") +
		scheduleFilter("le.in_schedule")

	// --- follow-ups -----------------------------------------------------------
	//
	// No schedule flag on this table, so a schedule filter excludes follow-ups
	// entirely rather than pretending they all qualify.
	fuWhere := fmt.Sprintf("fu.workspace_id = %s", ws) +
		scoped("fu.application_id", "fu.started_at") +
		actorFilter("fu.admin_id") + accountFilter("fu.account_id")
	if f.InSchedule != nil {
		fuWhere += " and false"
	}

	// --- campaigns ------------------------------------------------------------
	campWhere := fmt.Sprintf("al.workspace_id = %s", ws) +
		scoped("al.application_id", "al.occurred_at") +
		actorFilter("al.admin_id") + accountFilter("al.account_id") +
		scheduleFilter("al.in_schedule")

	var page string
	if !cursor.Before.IsZero() {
		page = fmt.Sprintf(" where (feed.occurred_at, feed.id) < (%s, %s)",
			q.add(cursor.Before), q.add(cursor.BeforeID))
	}

	sql := `
		with feed as (
			select
				'msg:' || m.id::text                       as id,
				case when c.type = 'group' then 'message_group'
				     else 'message_personal' end            as kind,
				coalesce(m.sender_source::text, 'web_admin') as type,
				m.timestamp                                 as occurred_at,
				m.sent_by                                   as actor_id,
				m.sender_role::text                         as actor_role,
				acc.application_id                          as application_id,
				m.account_id                                as account_id,
				null::text                                  as status,
				m.in_schedule                               as in_schedule,
				coalesce(nullif(btrim(c.name), ''), ct.phone_number,
				         split_part(c.chat_jid, '@', 1))    as subject,
				left(coalesce(nullif(m.body, ''), nullif(m.caption, ''),
				     '[' || m.type::text || ']'), 160)      as detail,
				c.id                                        as conversation_id,
				m.id                                        as message_id,
				null::uuid                                  as campaign_id,
				c.contact_id                                as contact_id,
				m.sender_source::text                       as source
			  from public.messages m
			  join public.conversations c on c.id = m.conversation_id
			  join public.whatsapp_accounts acc on acc.id = m.account_id
			  left join public.contacts ct on ct.id = c.contact_id
			 where ` + msgWhere + `
			   and m.from_me and m.hidden_at is null
			   and m.sender_source in ('web_admin', 'whatsapp_device')

			union all

			select
				'lbl:' || le.id::text,
				'label',
				le.event_type::text,
				le.occurred_at,
				le.admin_id,
				null::text,
				le.application_id,
				le.account_id,
				null::text,
				le.in_schedule,
				coalesce(nullif(btrim(ct.name), ''), nullif(btrim(ct.push_name), ''),
				         ct.phone_number, 'Kontak'),
				coalesce(fl.name, '') ||
				  case when fl.name is not null and tl.name is not null then ' → ' else '' end ||
				  coalesce(tl.name, ''),
				le.conversation_id,
				null::uuid,
				null::uuid,
				le.contact_id,
				le.source::text
			  from public.contact_label_events le
			  left join public.contacts ct on ct.id = le.contact_id
			  left join public.conversation_labels fl on fl.id = le.from_label_id
			  left join public.conversation_labels tl on tl.id = le.to_label_id
			 where ` + labelWhere + `

			union all

			select
				'fu:' || fu.id::text,
				'follow_up',
				case when fu.responded_at is null then 'pending' else 'answered' end,
				fu.started_at,
				fu.admin_id,
				null::text,
				fu.application_id,
				fu.account_id,
				case when fu.responded_at is null then 'menunggu' else 'dibalas' end,
				null::boolean,
				coalesce(nullif(btrim(c.name), ''), ct.phone_number,
				         split_part(c.chat_jid, '@', 1)),
				fu.message_count::text || ' pesan',
				fu.conversation_id,
				fu.trigger_message_id,
				null::uuid,
				fu.contact_id,
				fu.admin_source::text
			  from public.follow_up_events fu
			  join public.conversations c on c.id = fu.conversation_id
			  left join public.contacts ct on ct.id = fu.contact_id
			 where ` + fuWhere + `

			union all

			select
				'act:' || al.id::text,
				case when cc.campaign_type = 'story' then 'story' else 'broadcast' end,
				coalesce(al.activity_type::text, al.action, 'activity'),
				al.occurred_at,
				al.admin_id,
				al.admin_role::text,
				al.application_id,
				al.account_id,
				al.status,
				al.in_schedule,
				coalesce(al.entity_name, cc.name, 'Campaign'),
				al.failure_reason,
				null::uuid,
				null::uuid,
				al.entity_id,
				null::uuid,
				'web'
			  from public.admin_activity_logs al
			  left join public.content_campaigns cc on cc.id = al.entity_id
			 where ` + campWhere + `
			   and al.entity_type = 'campaign'
		)
		select feed.id, feed.kind, feed.type, feed.occurred_at,
		       feed.actor_id, coalesce(nullif(u.full_name, ''), u.email),
		       coalesce(feed.actor_role, ra.role::text),
		       feed.application_id, app.code, app.name, app.color,
		       feed.account_id, acc.name,
		       feed.status, feed.in_schedule, feed.subject, feed.detail,
		       feed.conversation_id, feed.message_id, feed.campaign_id, feed.contact_id,
		       feed.source
		  from feed
		  left join public.users u on u.id = feed.actor_id
		  left join public.role_assignments ra on ra.user_id = feed.actor_id and ra.is_active
		  left join public.applications app on app.id = feed.application_id
		  left join public.whatsapp_accounts acc on acc.id = feed.account_id` + page + `
		 order by feed.occurred_at desc, feed.id desc
		 limit ` + fmt.Sprint(limit)

	rows, err := r.pool.Query(ctx, sql, q.args...)
	if err != nil {
		return nil, fmt.Errorf("activity feed: %w", err)
	}
	defer rows.Close()

	out := []models.ActivityRow{}
	for rows.Next() {
		var a models.ActivityRow
		if err := rows.Scan(&a.ID, &a.Kind, &a.Type, &a.OccurredAt,
			&a.ActorID, &a.ActorName, &a.ActorRole,
			&a.ApplicationID, &a.ApplicationCode, &a.ApplicationName, &a.ApplicationColor,
			&a.AccountID, &a.AccountName,
			&a.Status, &a.InSchedule, &a.Subject, &a.Detail,
			&a.ConversationID, &a.MessageID, &a.CampaignID, &a.ContactID,
			&a.Source); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// TeamMemberIDs lists the people one PIC is responsible for, so a Leader can
// walk into a team without the caller sending the list back.
func (r *Repo) TeamMemberIDs(ctx context.Context, workspaceID, picID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := r.pool.Query(ctx,
		`select freelancer_user_id from public.freelancer_pic_assignments
		  where workspace_id = $1 and pic_user_id = $2`, workspaceID, picID)
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
