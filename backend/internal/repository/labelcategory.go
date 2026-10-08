package repository

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/salesan/omnichannel/backend/internal/models"
)

// Cold, Warm and Hot on top of WhatsApp's own labels.
//
// A customer holds labels, not a status: a quarter of the labelled contacts
// carry more than one, and the names are free text typed on somebody's phone —
// "Cold", "fu cold", "DB COLD", "Pindahan COLD" are four labels meaning one
// thing. The dashboard asks for three numbers, so something has to turn the one
// into the other, and this is where that happens.
//
// The rule is the most recent one wins. A contact tagged "Cold" in August and
// "FU HOT" last week is Hot: that is what "kondisi terakhir" means, and it is
// the only rule that gives every contact exactly one place to be counted.
// Priority by temperature was the alternative and it is worse — it would report
// a customer who has gone cold as Hot forever, because the Hot label is still
// hanging on them.

// currentCategoryCTE is the definition every figure on this screen rests on.
//
// Written once and shared, because five queries each carrying their own copy of
// "which label counts" is five chances for the summary and the drill-down to
// disagree about the same customer.
const currentCategoryCTE = `
	kategori as (
	  select distinct on (c.contact_id)
	         c.contact_id,
	         c.account_id,
	         acc.application_id,
	         vc.category,
	         vc.name  as label_name,
	         a.assigned_at
	    from public.conversation_label_assignments a
	    join public.v_label_category vc on vc.label_id = a.label_id
	    join public.conversations c      on c.id = a.conversation_id
	    join public.whatsapp_accounts acc on acc.id = c.account_id
	   where c.contact_id is not null %s
	   -- The newest assignment decides. assigned_at is when we recorded it,
	   -- which for a label set on the phone is when the sync brought it over;
	   -- WhatsApp does not tell us when the operator actually tapped it.
	   order by c.contact_id, a.assigned_at desc
	)`

// categoryScope builds the filters shared by every query here.
func categoryScope(sc Scope, f models.AnalyticsFilter, q *queryArgs) string {
	where := fmt.Sprintf(" and c.workspace_id = %s", q.add(sc.WorkspaceID))
	if !sc.All {
		where += fmt.Sprintf(" and acc.application_id = any(%s)", q.add(sc.ApplicationIDs))
	}
	if f.ApplicationID != nil {
		where += fmt.Sprintf(" and acc.application_id = %s", q.add(*f.ApplicationID))
	}
	if f.AccountID != nil {
		where += fmt.Sprintf(" and c.account_id = %s", q.add(*f.AccountID))
	}
	return where
}

// LabelCategorySummary is how many customers stand in each of the three
// categories right now.
//
// Deliberately not filtered by period. "How many customers are Hot" is a
// question about today; applying a date range to it would produce a number that
// looks like an answer and is not one — a customer tagged Hot in June is still
// Hot in September, and a September filter would drop them. The period belongs
// to the movement figures, which are about what happened.
func (r *Repo) LabelCategorySummary(
	ctx context.Context, sc Scope, f models.AnalyticsFilter,
) (models.LabelCategorySummary, error) {
	var out models.LabelCategorySummary
	q := &queryArgs{}
	sql := "with" + fmt.Sprintf(currentCategoryCTE, categoryScope(sc, f, q)) + `
		select count(*) filter (where category = 'cold'),
		       count(*) filter (where category = 'warm'),
		       count(*) filter (where category = 'hot')
		  from kategori`

	if err := r.pool.QueryRow(ctx, sql, q.args...).
		Scan(&out.Cold, &out.Warm, &out.Hot); err != nil {
		return out, fmt.Errorf("label category summary: %w", err)
	}
	return out, nil
}

// LabelCategoryByApplication splits one category across the brands.
//
// The percentage is left to the caller rather than computed here: it is a share
// of the total this same call returns, and a rounded percentage stored beside
// its own numerator invites the two to disagree on screen.
func (r *Repo) LabelCategoryByApplication(
	ctx context.Context, sc Scope, f models.AnalyticsFilter, category string,
) ([]models.LabelCategoryApplication, error) {
	if !validLabelCategory(category) {
		return nil, fmt.Errorf("kategori %q tidak dikenal", category)
	}

	q := &queryArgs{}
	scope := categoryScope(sc, f, q)
	cat := q.add(category)

	sql := "with" + fmt.Sprintf(currentCategoryCTE, scope) + `
		select k.application_id, coalesce(app.code, '-'), coalesce(app.name, 'Tanpa aplikasi'),
		       coalesce(app.color, '#64748B'), count(*)
		  from kategori k
		  left join public.applications app on app.id = k.application_id
		 where k.category = ` + cat + `
		 group by k.application_id, app.code, app.name, app.color
		 order by count(*) desc`

	rows, err := r.pool.Query(ctx, sql, q.args...)
	if err != nil {
		return nil, fmt.Errorf("label category by application: %w", err)
	}
	defer rows.Close()

	out := []models.LabelCategoryApplication{}
	for rows.Next() {
		var a models.LabelCategoryApplication
		if err := rows.Scan(&a.ApplicationID, &a.Code, &a.Name, &a.Color, &a.Contacts); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// LabelCategoryContacts lists the customers in one category, newest change
// first, with the number of label changes each has been through.
func (r *Repo) LabelCategoryContacts(
	ctx context.Context, sc Scope, f models.AnalyticsFilter,
	category, search string, limit, offset int,
) ([]models.LabelCategoryContact, int, error) {
	if !validLabelCategory(category) {
		return nil, 0, fmt.Errorf("kategori %q tidak dikenal", category)
	}
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	q := &queryArgs{}
	scope := categoryScope(sc, f, q)
	cat := q.add(category)

	filter := " where k.category = " + cat
	if s := strings.TrimSpace(search); s != "" {
		// Digits on both sides for the number, plain text for the name: an
		// operator types a phone number the way it is written down, not the way
		// WhatsApp happens to store it.
		p := q.add("%" + s + "%")
		digits := q.add("%" + onlyDigits(s) + "%")
		filter += fmt.Sprintf(
			" and (ct.name ilike %s or ct.push_name ilike %s or ct.phone_number like %s)",
			p, p, digits)
	}

	base := "with" + fmt.Sprintf(currentCategoryCTE, scope) + `
		select ct.id, coalesce(nullif(ct.name, ''), nullif(ct.push_name, ''), ''),
		       coalesce(ct.phone_number, ''), k.label_name, k.category,
		       coalesce(st.change_count, 0), k.assigned_at,
		       count(*) over () as total
		  from kategori k
		  join public.contacts ct on ct.id = k.contact_id
		  left join public.contact_label_state st on st.contact_id = k.contact_id` +
		filter + `
		 order by k.assigned_at desc
		 limit ` + q.add(limit) + ` offset ` + q.add(offset)

	rows, err := r.pool.Query(ctx, base, q.args...)
	if err != nil {
		return nil, 0, fmt.Errorf("label category contacts: %w", err)
	}
	defer rows.Close()

	out := []models.LabelCategoryContact{}
	total := 0
	for rows.Next() {
		var c models.LabelCategoryContact
		if err := rows.Scan(&c.ContactID, &c.Name, &c.Phone, &c.LabelName,
			&c.Category, &c.ChangeCount, &c.ChangedAt, &total); err != nil {
			return nil, 0, err
		}
		out = append(out, c)
	}
	return out, total, rows.Err()
}

// ContactLabelHistory is one customer's label journey, oldest first.
//
// Only assignments and removals: renaming a label is a change to the label, not
// to this customer, and putting it in their timeline would read as though they
// had moved when they had not.
func (r *Repo) ContactLabelHistory(
	ctx context.Context, sc Scope, contactID uuid.UUID,
) ([]models.ContactLabelHistoryRow, error) {
	rows, err := r.pool.Query(ctx, `
		select le.occurred_at,
		       le.event_type::text,
		       coalesce(le.to_label_name, le.from_label_name, ''),
		       public.label_category_of(coalesce(le.to_label_name, le.from_label_name)),
		       le.source::text,
		       coalesce(u.full_name, '')
		  from public.contact_label_events le
		  left join public.users u on u.id = le.admin_id
		 where le.contact_id = $1
		   and le.workspace_id = $2
		   and le.event_type in ('label_assigned', 'label_removed')
		 order by le.occurred_at asc`, contactID, sc.WorkspaceID)
	if err != nil {
		return nil, fmt.Errorf("contact label history: %w", err)
	}
	defer rows.Close()

	out := []models.ContactLabelHistoryRow{}
	for rows.Next() {
		var h models.ContactLabelHistoryRow
		var category *string
		if err := rows.Scan(&h.At, &h.EventType, &h.LabelName,
			&category, &h.Source, &h.ChangedBy); err != nil {
			return nil, err
		}
		if category != nil {
			h.Category = *category
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// LabelCategoryTransitions counts movements between categories, day by day.
//
// A move is not something either side sends. The phone and the web both report
// a removal followed by an assignment, so the pair is recognised here inside a
// window — the same rule the existing label drill-down uses, kept identical on
// purpose so two screens cannot report different numbers of the same event.
//
// Movements inside one category are dropped: "Cold" to "FU COLD" is a
// housekeeping change on the phone, not a customer warming up.
func (r *Repo) LabelCategoryTransitions(
	ctx context.Context, sc Scope, f models.AnalyticsFilter,
) ([]models.LabelCategoryTransition, error) {
	q := &queryArgs{}
	where := scopeWhere("le", sc, f, "occurred_at", q)
	windowArg := q.add(fmt.Sprintf("%d seconds", int(TransitionWindow.Seconds())))

	sql := `
		-- Rendered as text here rather than scanned as a date: the row is on its
		-- way to a JSON field and a spreadsheet cell, both of which want the day
		-- the Jakarta calendar calls it, not a timestamp a driver has to guess
		-- a zone for.
		select to_char((le.occurred_at ` + jakartaDate + `, 'YYYY-MM-DD') as hari,
		       public.label_category_of(prev.from_label_name) as dari,
		       public.label_category_of(le.to_label_name) as ke,
		       count(*) as jumlah,
		       count(distinct le.contact_id) as kontak
		  from public.contact_label_events le
		  join lateral (
		    select p.from_label_name
		      from public.contact_label_events p
		     where p.contact_id = le.contact_id
		       and p.event_type = 'label_removed'
		       and p.occurred_at <= le.occurred_at
		       and p.occurred_at >= le.occurred_at - ` + windowArg + `::interval
		     order by p.occurred_at desc
		     limit 1
		  ) prev on true` + where + `
		   and le.event_type = 'label_assigned'
		   and public.label_category_of(le.to_label_name) is not null
		   and public.label_category_of(prev.from_label_name) is not null
		   and public.label_category_of(prev.from_label_name)
		       is distinct from public.label_category_of(le.to_label_name)
		 group by hari, dari, ke
		 order by hari asc, jumlah desc`

	rows, err := r.pool.Query(ctx, sql, q.args...)
	if err != nil {
		return nil, fmt.Errorf("label category transitions: %w", err)
	}
	defer rows.Close()

	out := []models.LabelCategoryTransition{}
	for rows.Next() {
		var t models.LabelCategoryTransition
		if err := rows.Scan(&t.Day, &t.From, &t.To, &t.Count, &t.Contacts); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// LabelCategoryDaily counts, for each day, how many customers were given a
// label of each category.
//
// A flow, not a stock. The three figures on the card answer "how many customers
// are Hot"; this answers "how many became Hot on Tuesday", and the two are
// different questions with different uses — one is the size of the book, the
// other is the work that moved it.
//
// Distinct contacts rather than events: somebody tagged "FU HOT" and then
// "Pindahan Hot" in one afternoon moved once, and counting them twice would
// make a busy day of housekeeping look like a busy day of selling.
func (r *Repo) LabelCategoryDaily(
	ctx context.Context, sc Scope, f models.AnalyticsFilter,
) ([]models.LabelCategoryDay, error) {
	q := &queryArgs{}
	where := scopeWhere("le", sc, f, "occurred_at", q)

	sql := `
		select to_char((le.occurred_at ` + jakartaDate + `, 'YYYY-MM-DD') as hari,
		       count(distinct le.contact_id) filter (
		         where public.label_category_of(le.to_label_name) = 'cold') as cold,
		       count(distinct le.contact_id) filter (
		         where public.label_category_of(le.to_label_name) = 'warm') as warm,
		       count(distinct le.contact_id) filter (
		         where public.label_category_of(le.to_label_name) = 'hot')  as hot
		  from public.contact_label_events le` + where + `
		   and le.event_type = 'label_assigned'
		   and le.contact_id is not null
		   and public.label_category_of(le.to_label_name) is not null
		 group by hari
		 order by hari asc`

	rows, err := r.pool.Query(ctx, sql, q.args...)
	if err != nil {
		return nil, fmt.Errorf("label category daily: %w", err)
	}
	defer rows.Close()

	out := []models.LabelCategoryDay{}
	for rows.Next() {
		var d models.LabelCategoryDay
		if err := rows.Scan(&d.Day, &d.Cold, &d.Warm, &d.Hot); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func validLabelCategory(c string) bool {
	switch c {
	case "cold", "warm", "hot":
		return true
	}
	return false
}

func onlyDigits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// LabelSpreadByApplication gives each application its own mix of the three
// categories.
//
// The percentages are left to the caller, as in LabelCategoryByApplication, and
// for the same reason: a rounded share stored beside its own numerator is two
// numbers that can disagree on screen.
func (r *Repo) LabelSpreadByApplication(
	ctx context.Context, sc Scope, f models.AnalyticsFilter,
) ([]models.LabelSpread, error) {
	q := &queryArgs{}
	scope := categoryScope(sc, f, q)

	sql := "with" + fmt.Sprintf(currentCategoryCTE, scope) + `
		select k.application_id, coalesce(app.code, '-'), coalesce(app.name, 'Tanpa aplikasi'),
		       coalesce(app.color, '#64748B'),
		       count(*) filter (where k.category = 'cold'),
		       count(*) filter (where k.category = 'warm'),
		       count(*) filter (where k.category = 'hot'),
		       count(*)
		  from kategori k
		  left join public.applications app on app.id = k.application_id
		 group by k.application_id, app.code, app.name, app.color
		 order by count(*) desc`

	rows, err := r.pool.Query(ctx, sql, q.args...)
	if err != nil {
		return nil, fmt.Errorf("label spread by application: %w", err)
	}
	defer rows.Close()

	out := []models.LabelSpread{}
	for rows.Next() {
		var a models.LabelSpread
		if err := rows.Scan(&a.ApplicationID, &a.Code, &a.Name, &a.Color,
			&a.Cold, &a.Warm, &a.Hot, &a.Total); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
