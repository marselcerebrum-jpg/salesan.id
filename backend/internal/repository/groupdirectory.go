package repository

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/salesan/omnichannel/backend/internal/models"
)

// The group directory: one row per WhatsApp group, not one per thread.
//
// The difference is the whole point of the screen. Three of our numbers in the
// same group are three conversation rows here, and reading them as three groups
// would triple the count and make the list unusable. A group is identified by
// its chat JID, which is the same on every number that joined it, so that is
// what the rows are grouped by. Which of our numbers are inside it becomes a
// column rather than a duplicate.
//
// The chip counts are de-duplicated the same way, for the same reason. A brand
// with two numbers inside one group reaches one group, and counting it twice
// put a number on the chips that did not agree with the number in the header,
// with nothing on screen to say which one to believe.

// groupVisible keeps non-group threads, and groups we are not in, out of every
// query here.
//
// History sync imports every chat the phone remembers, so a group somebody
// added us to in July and removed us from in August arrives looking exactly like
// one we are still in: it has a name, a JID and a thread. It is not ours to
// list. `is not false` rather than `is true` so a group we have not checked yet
// still shows — unknown is not the same as no.
const groupVisible = `c.type = 'group' and c.group_is_member is not false`

// groupWhere builds the filter shared by the list, the counts and the export.
func groupWhere(workspaceID uuid.UUID, f models.GroupFilter, q *queryArgs) string {
	where := fmt.Sprintf(" where c.workspace_id = %s and %s", q.add(workspaceID), groupVisible)
	if f.AccountID != nil {
		where += fmt.Sprintf(" and c.account_id = %s", q.add(*f.AccountID))
	}
	if f.ApplicationID != nil {
		where += fmt.Sprintf(" and a.application_id = %s", q.add(*f.ApplicationID))
	}
	if jid := strings.TrimSpace(f.ChatJID); jid != "" {
		where += fmt.Sprintf(" and c.chat_jid = %s", q.add(jid))
	}
	if s := strings.TrimSpace(f.Search); s != "" {
		p := q.add("%" + s + "%")
		where += fmt.Sprintf(" and (c.name ilike %s or c.chat_jid ilike %s)", p, p)
	}
	return where
}

const groupFrom = `
	from public.conversations c
	join public.whatsapp_accounts a on a.id = c.account_id
	left join public.applications app on app.id = a.application_id`

// ListGroups returns one page of the directory, one row per group.
//
// Ordered by member count because that is what the screen is for: the biggest
// audiences are the ones anybody is looking for, and a directory of nine
// hundred groups sorted by name is a directory nobody scrolls.
func (r *Repo) ListGroups(
	ctx context.Context, workspaceID uuid.UUID, f models.GroupFilter,
) ([]models.GroupRow, error) {
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 100
	}
	if f.Offset < 0 {
		f.Offset = 0
	}

	q := &queryArgs{}
	where := groupWhere(workspaceID, f, q)

	// json_agg over the accounts rather than a second query per row: a hundred
	// rows would otherwise be a hundred round trips, and the list is the one
	// thing on this screen that must stay fast.
	sql := `
		select c.chat_jid,
		       max(coalesce(nullif(btrim(c.name), ''), '')) as name,
		       count(distinct c.account_id) as account_count,
		       coalesce(max(mem.n), 0) as member_count,
		       bool_or(mem.n is not null) as fetched,
		       json_agg(json_build_object(
		         'conversation_id', c.id,
		         'account_id',      c.account_id,
		         'account_name',    a.name,
		         'account_phone',   coalesce(a.phone_number, ''),
		         'application_code', coalesce(app.code, ''),
		         'application_color', coalesce(app.color, '')
		       ) order by a.name) as accounts` + groupFrom + `
		  left join lateral (
		    select count(*) as n from public.conversation_members m
		     where m.conversation_id = c.id
		  ) mem on mem.n > 0` + where + `
		 group by c.chat_jid
		 order by member_count desc, name asc, c.chat_jid asc` +
		fmt.Sprintf(" limit %s offset %s", q.add(f.Limit), q.add(f.Offset))

	rows, err := r.pool.Query(ctx, sql, q.args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []models.GroupRow{}
	for rows.Next() {
		var g models.GroupRow
		if err := rows.Scan(&g.ChatJID, &g.Name, &g.AccountCount, &g.MemberCount,
			&g.Fetched, &g.Accounts); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// CountGroups is how many distinct groups the filter matches, and how many of
// them have had their member list pulled.
//
// Both in one pass, because the header states them in one sentence: "246 of 907
// fetched". Two queries could disagree by a group somebody joined in between.
func (r *Repo) CountGroups(
	ctx context.Context, workspaceID uuid.UUID, f models.GroupFilter,
) (total, fetched int, err error) {
	q := &queryArgs{}
	where := groupWhere(workspaceID, f, q)

	err = r.pool.QueryRow(ctx, `
		select count(*), count(*) filter (where g.fetched)
		  from (
		    select c.chat_jid,
		           bool_or(exists (select 1 from public.conversation_members m
		                            where m.conversation_id = c.id)) as fetched`+
		groupFrom+where+`
		     group by c.chat_jid
		  ) g`, q.args...).Scan(&total, &fetched)
	return total, fetched, err
}

// GroupFacets counts the directory per application and per WhatsApp number.
//
// Same shape as the address book's: applications are the primary choice and are
// counted across the workspace, numbers are the step inside that choice and are
// offered and counted within it.
//
// Every count is of DISTINCT groups, not of memberships. A brand with two
// numbers inside one group reaches one group, and counting it twice made the
// chips add up to more than the header said — two numbers on the same screen
// that disagree, with nothing to tell the reader which one to believe.
func (r *Repo) GroupFacets(
	ctx context.Context, workspaceID uuid.UUID, applicationID *uuid.UUID,
) (*models.ContactFacets, error) {
	out := &models.ContactFacets{
		Applications: []models.ContactFacet{},
		Accounts:     []models.ContactFacet{},
	}

	appRows, err := r.pool.Query(ctx, `
		select app.id, app.code, app.name, app.color,
		       count(distinct c.chat_jid) filter (where c.type = 'group')
		  from public.applications app
		  left join public.whatsapp_accounts a on a.application_id = app.id
		  left join public.conversations c on c.account_id = a.id
		 where app.workspace_id = $1
		 group by app.id, app.code, app.name, app.color, app.sort_order
		 order by app.sort_order, app.code`, workspaceID)
	if err != nil {
		return nil, err
	}
	for appRows.Next() {
		var f models.ContactFacet
		var id uuid.UUID
		var name string
		if err := appRows.Scan(&id, &f.Label, &name, &f.Color, &f.Count); err != nil {
			appRows.Close()
			return nil, err
		}
		f.ID = &id
		f.Hint = name
		out.Applications = append(out.Applications, f)
	}
	appRows.Close()
	if err := appRows.Err(); err != nil {
		return nil, err
	}

	accRows, err := r.pool.Query(ctx, `
		select a.id, a.name, coalesce(a.phone_number, ''),
		       count(distinct c.chat_jid) filter (where c.type = 'group')
		  from public.whatsapp_accounts a
		  left join public.conversations c on c.account_id = a.id
		 where a.workspace_id = $1
		   and ($2::uuid is null or a.application_id = $2::uuid)
		 group by a.id, a.name, a.phone_number
		 order by a.name`, workspaceID, applicationID)
	if err != nil {
		return nil, err
	}
	defer accRows.Close()
	for accRows.Next() {
		var f models.ContactFacet
		var id uuid.UUID
		if err := accRows.Scan(&id, &f.Label, &f.Hint, &f.Count); err != nil {
			return nil, err
		}
		f.ID = &id
		out.Accounts = append(out.Accounts, f)
	}
	if err := accRows.Err(); err != nil {
		return nil, err
	}

	if err := r.pool.QueryRow(ctx, `
		select count(distinct c.chat_jid),
		       count(distinct c.chat_jid) filter (where $2::uuid is null or a.application_id = $2::uuid)
		  from public.conversations c
		  join public.whatsapp_accounts a on a.id = c.account_id
		 where c.workspace_id = $1 and c.type = 'group'`,
		workspaceID, applicationID).Scan(&out.Total, &out.ScopedTotal); err != nil {
		return nil, err
	}
	return out, nil
}

// GroupMembersByJID lists a group's participants across every one of our
// numbers that is inside it.
//
// Merged rather than shown per number: a group has one membership list, and
// which of our phones happened to read it is not a fact about the members. The
// admin flag is true if any copy saw them as an admin, because admin is a
// property of the group and the other copy is simply stale.
func (r *Repo) GroupMembersByJID(
	ctx context.Context, workspaceID uuid.UUID, chatJID string,
) ([]GroupMemberProfile, error) {
	rows, err := r.pool.Query(ctx, `
		select jid, phone_number,
		       coalesce(name, phone_number, '') as display_name,
		       is_admin, seen_at
		  from (
		    select mem.jid                       as jid,
		           max(`+memberPhoneExpr+`)      as phone_number,
		           max(`+memberNameExpr+`)       as name,
		           bool_or(mem.is_admin)         as is_admin,
		           max(mem.joined_at)            as seen_at
		      from public.conversation_members mem
		      join public.conversations c on c.id = mem.conversation_id`+
		memberContactJoin+`
		     where c.workspace_id = $1 and c.chat_jid = $2
		     group by mem.jid
		  ) s
		 -- Admins first, then the people we can name, then the rest. Somebody
		 -- scrolling a group of nine hundred is looking for one of the first two.
		 order by is_admin desc, (name is null), lower(coalesce(name, phone_number, jid))`,
		workspaceID, chatJID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []GroupMemberProfile{}
	for rows.Next() {
		var m GroupMemberProfile
		if err := rows.Scan(&m.JID, &m.PhoneNumber, &m.DisplayName,
			&m.IsAdmin, &m.LastSeenAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// GroupsNeedingFetch returns conversation ids whose member list has never been
// pulled, one per group so the same group is not read twice through two of our
// numbers.
//
// Bounded by the caller: reading a group's metadata is a round trip to WhatsApp
// per group, and nine hundred of them in one request would time out long before
// it finished. The screen asks repeatedly instead, reporting progress.
func (r *Repo) GroupsNeedingFetch(
	ctx context.Context, workspaceID uuid.UUID, f models.GroupFilter, limit int,
) ([]uuid.UUID, error) {
	if limit <= 0 || limit > 200 {
		limit = 25
	}
	q := &queryArgs{}
	where := groupWhere(workspaceID, f, q)

	rows, err := r.pool.Query(ctx, `
		select (array_agg(c.id order by c.created_at))[1]`+groupFrom+where+`
		   and not exists (select 1 from public.conversation_members m
		                    where m.conversation_id = c.id)
		 group by c.chat_jid
		 limit `+q.add(limit), q.args...)
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

// GetGroup loads one group for its own page.
//
// Built on ListGroups rather than on a query of its own, so the page and the
// row it was opened from can never describe the same group differently.
func (r *Repo) GetGroup(
	ctx context.Context, workspaceID uuid.UUID, chatJID string,
) (*models.GroupRow, error) {
	rows, err := r.ListGroups(ctx, workspaceID, models.GroupFilter{ChatJID: chatJID, Limit: 1})
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, ErrNotFound
	}
	return &rows[0], nil
}

// GroupConversationIDs lists our threads for one group, one per number inside
// it, so a refresh can read the group through whichever of them still works.
func (r *Repo) GroupConversationIDs(
	ctx context.Context, workspaceID uuid.UUID, chatJID string,
) ([]uuid.UUID, error) {
	rows, err := r.pool.Query(ctx, `
		select id from public.conversations
		 where workspace_id = $1 and chat_jid = $2 and type = 'group'
		 order by created_at`, workspaceID, chatJID)
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

// ExportGroups reads the whole filtered directory for a download.
func (r *Repo) ExportGroups(
	ctx context.Context, workspaceID uuid.UUID, f models.GroupFilter, only []string,
) ([]models.GroupRow, error) {
	f.Limit = 0
	f.Offset = 0
	all, err := r.ListGroups(ctx, workspaceID, models.GroupFilter{
		AccountID:     f.AccountID,
		ApplicationID: f.ApplicationID,
		Search:        f.Search,
		Limit:         500,
	})
	if err != nil {
		return nil, err
	}
	if len(only) == 0 {
		return all, nil
	}

	// A selection is a list of chat JIDs the operator ticked. Filtered here
	// rather than in SQL so the export and the list cannot disagree about what
	// a row is: both come from ListGroups.
	wanted := make(map[string]struct{}, len(only))
	for _, jid := range only {
		wanted[jid] = struct{}{}
	}
	out := make([]models.GroupRow, 0, len(only))
	for _, g := range all {
		if _, ok := wanted[g.ChatJID]; ok {
			out = append(out, g)
		}
	}
	return out, nil
}
