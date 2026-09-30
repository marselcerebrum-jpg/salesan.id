package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Keeping track of who comes and goes in a group.
//
// The member list itself has only ever held the present tense: who is in the
// group right now. A refetch deletes whoever left, so "did this group grow this
// week" had no answer anywhere in the system.
//
// What is recorded here are WhatsApp's own notifications, not a nightly head
// count. The same group watched by five of our numbers produces five copies of
// every notification, so the write path leans on a unique index to collapse
// them: one person joining one group is one row however many of our numbers
// were listening.

// GroupMemberChange is one person arriving at or leaving a group.
type GroupMemberChange struct {
	ParticipantJID string
	// Leaving rather than Direction: a bool at the call site reads as
	// `Leaving: true`, which is harder to get backwards than a string.
	Leaving bool
}

// RecordGroupMemberChanges stores a batch of arrivals and departures for one
// group and folds them into that day's tally.
//
// Returns how many of each were genuinely new, which is what the caller logs.
// A second number hearing the same notification adds nothing and reports zero,
// so the log says what happened rather than how many of our phones saw it.
func (r *Repo) RecordGroupMemberChanges(
	ctx context.Context,
	workspaceID uuid.UUID,
	chatJID string,
	heardBy uuid.UUID,
	actorJID string,
	at time.Time,
	changes []GroupMemberChange,
) (joined, left int, err error) {
	if len(changes) == 0 {
		return 0, 0, nil
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// One statement for the whole batch, not one per person. whatsmeow runs
	// event handlers one after another on a single goroutine, so a round trip
	// per participant would hold up every other event on this number — and the
	// batches that matter are exactly the large ones, an admin adding three
	// hundred people at once.
	jids := make([]string, 0, len(changes))
	directions := make([]string, 0, len(changes))
	for _, c := range changes {
		jids = append(jids, c.ParticipantJID)
		if c.Leaving {
			directions = append(directions, "leave")
		} else {
			directions = append(directions, "join")
		}
	}

	rows, err := tx.Query(ctx, `
		insert into public.group_member_events
		       (workspace_id, chat_jid, participant_jid, direction, occurred_at, actor_jid, heard_by)
		select $1, $2, p.jid, p.direction, $3, nullif($4, ''), $5
		  from unnest($6::text[], $7::text[]) as p(jid, direction)
		on conflict do nothing
		returning direction`,
		workspaceID, chatJID, at, actorJID, heardBy, jids, directions)
	if err != nil {
		return 0, 0, err
	}
	for rows.Next() {
		// Only the rows that were actually written come back, so whatever
		// another of our numbers already reported is simply absent.
		var direction string
		if err := rows.Scan(&direction); err != nil {
			rows.Close()
			return 0, 0, err
		}
		if direction == "leave" {
			left++
		} else {
			joined++
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, 0, err
	}

	if joined == 0 && left == 0 {
		return 0, 0, tx.Commit(ctx)
	}

	// The day's row carries the count as it stands after this batch, so the
	// anchor and the tally are written from the same moment and cannot disagree.
	//
	// Dated by the notification, not by now(): an event that arrives late
	// because this process was restarting belongs to the day it happened on.
	if _, err := tx.Exec(ctx, `
		insert into public.group_member_daily
		       (workspace_id, chat_jid, day, member_count, joined, left_count)
		select $1, $2, ($3 at time zone 'Asia/Jakarta')::date,
		       coalesce(max(mem.n), 0), $4, $5
		  from public.conversations c
		  left join lateral (
		    select count(*) as n from public.conversation_members m
		     where m.conversation_id = c.id
		  ) mem on true
		 where c.workspace_id = $1 and c.chat_jid = $2
		-- A group nobody has fetched yet has an empty member list, and anchoring
		-- its history at zero would draw a chart claiming it lost every member
		-- it has. The arrivals are still recorded; only the head count waits
		-- until there is a real one to record.
		having coalesce(max(mem.n), 0) > 0
		on conflict (workspace_id, chat_jid, day) do update
		   set member_count = excluded.member_count,
		       joined       = public.group_member_daily.joined + excluded.joined,
		       left_count   = public.group_member_daily.left_count + excluded.left_count,
		       updated_at   = now()`,
		workspaceID, chatJID, at, joined, left); err != nil {
		return 0, 0, err
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, 0, err
	}
	return joined, left, nil
}

// GroupMemberDeltaToday is the net change for each of these groups since
// midnight in Jakarta, keyed by chat JID.
//
// Groups with nothing to report are absent rather than zero, so the caller can
// tell "no change" from "not asked about" without a second lookup.
func (r *Repo) GroupMemberDeltaToday(
	ctx context.Context, workspaceID uuid.UUID, chatJIDs []string,
) (map[string]int, error) {
	out := map[string]int{}
	if len(chatJIDs) == 0 {
		return out, nil
	}

	// Read from the events rather than from the day's row: the row is only
	// written when something happens, and this has to be right for the page
	// that is open when the first change of the day arrives.
	rows, err := r.pool.Query(ctx, `
		select chat_jid,
		       count(*) filter (where direction = 'join')
		         - count(*) filter (where direction = 'leave')
		  from public.group_member_events
		 where workspace_id = $1
		   and chat_jid = any($2)
		   and (occurred_at at time zone 'Asia/Jakarta')::date
		       = (now() at time zone 'Asia/Jakarta')::date
		 group by chat_jid`, workspaceID, chatJIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var jid string
		var delta int
		if err := rows.Scan(&jid, &delta); err != nil {
			return nil, err
		}
		out[jid] = delta
	}
	return out, rows.Err()
}

// GroupMemberDay is one day in a group's history.
type GroupMemberDay struct {
	Day         string `json:"day"`
	MemberCount int    `json:"member_count"`
	Joined      int    `json:"joined"`
	Left        int    `json:"left"`
}

// GroupMemberHistory is one calendar month for one group, oldest day first,
// plus the earliest day ever recorded for it.
//
// A month rather than a rolling window because that is what the screen asks
// for, and because a rolling window makes two people looking at the same group
// on different days see different numbers with no way to say which period they
// meant.
//
// firstDay is empty until something has been recorded. The screen uses it to
// avoid offering months that cannot contain anything: this began being kept
// the day it shipped, and a filter that cheerfully offers last year would be
// promising a past that does not exist.
//
// Days on which nothing happened are absent. Filling them in belongs to the
// screen, which knows how wide its chart is; inventing rows here would hide the
// difference between "no change" and "we were not watching yet".
func (r *Repo) GroupMemberHistory(
	ctx context.Context, workspaceID uuid.UUID, chatJID string, year, month int,
) (days []GroupMemberDay, firstDay string, err error) {
	if month < 1 || month > 12 {
		return nil, "", fmt.Errorf("bulan %d di luar 1..12", month)
	}
	if year < 2000 || year > 3000 {
		return nil, "", fmt.Errorf("tahun %d di luar jangkauan", year)
	}

	rows, err := r.pool.Query(ctx, `
		with bulan as (
		  select make_date($3::int, $4::int, 1) as awal
		),
		recorded as (
		  select d.day, d.member_count, d.joined, d.left_count
		    from public.group_member_daily d, bulan b
		   where d.workspace_id = $1 and d.chat_jid = $2
		     and d.day >= b.awal and d.day < b.awal + interval '1 month'
		),
		-- Today's head count stands in when the month being looked at is the
		-- one we are living in and nothing has been written for today yet.
		-- Without it a group plainly full of people opens on an empty screen,
		-- which reads as broken rather than as new. Never applied to a past
		-- month: today's count is not a fact about August.
		baseline as (
		  select (now() at time zone 'Asia/Jakarta')::date as day,
		         coalesce(max(mem.n), 0) as member_count, 0 as joined, 0 as left_count
		    from public.conversations c, bulan b
		    left join lateral (
		      select count(*) as n from public.conversation_members m
		       where m.conversation_id = c.id
		    ) mem on true
		   where c.workspace_id = $1 and c.chat_jid = $2
		     and date_trunc('month', (now() at time zone 'Asia/Jakarta')::date) = b.awal
		  having coalesce(max(mem.n), 0) > 0
		)
		select to_char(day, 'YYYY-MM-DD'), member_count, joined, left_count from recorded
		union all
		select to_char(b.day, 'YYYY-MM-DD'), b.member_count, b.joined, b.left_count
		  from baseline b
		 where not exists (select 1 from recorded r where r.day = b.day)
		 order by 1 asc`, workspaceID, chatJID, year, month)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()

	days = []GroupMemberDay{}
	for rows.Next() {
		var d GroupMemberDay
		if err := rows.Scan(&d.Day, &d.MemberCount, &d.Joined, &d.Left); err != nil {
			return nil, "", err
		}
		days = append(days, d)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}

	var earliest *string
	if err := r.pool.QueryRow(ctx, `
		select to_char(min(day), 'YYYY-MM-DD')
		  from public.group_member_daily
		 where workspace_id = $1 and chat_jid = $2`, workspaceID, chatJID,
	).Scan(&earliest); err != nil {
		return nil, "", err
	}
	if earliest != nil {
		firstDay = *earliest
	}
	return days, firstDay, nil
}

// SnapshotGroupMemberCounts writes today's head count for every group that has
// one, without touching the day's arrival and departure tallies.
//
// This is the anchor. Notifications go missing — a socket drops, this process
// restarts, WhatsApp does not replay what happened while nobody was listening —
// and a history derived only from arrivals and departures would carry every one
// of those gaps forward forever. A head count taken once a day means a missed
// notification spoils one day's tally and nothing else.
func (r *Repo) SnapshotGroupMemberCounts(ctx context.Context) (int, error) {
	tag, err := r.pool.Exec(ctx, `
		insert into public.group_member_daily
		       (workspace_id, chat_jid, day, member_count)
		select c.workspace_id, c.chat_jid,
		       (now() at time zone 'Asia/Jakarta')::date,
		       max(mem.n)
		  from public.conversations c
		  join lateral (
		    select count(*) as n from public.conversation_members m
		     where m.conversation_id = c.id
		  ) mem on mem.n > 0
		 where c.type = 'group' and c.group_is_member is not false
		 group by c.workspace_id, c.chat_jid
		on conflict (workspace_id, chat_jid, day) do update
		   set member_count = excluded.member_count,
		       updated_at   = now()`)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

// AddGroupMembers records people who have just joined, without disturbing the
// rest of the list.
//
// Does nothing for a group whose member list has never been fetched. Inserting
// one arrival into an empty list would turn "nobody has looked yet" into "this
// group has one member", which is the one thing the directory is careful to
// keep apart.
func (r *Repo) AddGroupMembers(ctx context.Context, conversationID uuid.UUID, jids []string) error {
	if len(jids) == 0 {
		return nil
	}
	_, err := r.pool.Exec(ctx, `
		insert into public.conversation_members (conversation_id, jid)
		select $1, j from unnest($2::text[]) as j
		 where exists (
		   select 1 from public.conversation_members m where m.conversation_id = $1)
		on conflict (conversation_id, jid) do nothing`, conversationID, jids)
	return err
}

// RemoveGroupMembers drops people who have left.
func (r *Repo) RemoveGroupMembers(ctx context.Context, conversationID uuid.UUID, jids []string) error {
	if len(jids) == 0 {
		return nil
	}
	_, err := r.pool.Exec(ctx,
		`delete from public.conversation_members where conversation_id = $1 and jid = any($2)`,
		conversationID, jids)
	return err
}
