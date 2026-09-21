package repository

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/salesan/omnichannel/backend/internal/models"
)

// The per-application split of whatever is currently on screen.
//
// A PIC holds more than one application. "Gabungan" answers "how are we doing"
// without saying which brand the answer is about, and the application filter
// answers it for one brand at a time: comparing three of them means running the
// filter three times and remembering the numbers in between. This produces all
// of them at once.
//
// It runs the ordinary performance aggregate once per application rather than
// adding a second set of queries grouped by application_id. That costs more
// round trips, and it is the right trade: a parallel set of SQL would drift
// from the cards above it, and where that drift shows is one screen quoting two
// different numbers for the same day.

// maxApplicationFanout bounds how many aggregates run at once.
//
// Each one is a dozen indexed queries. Letting a workspace with twenty
// applications fire all of them together would take the connection pool from
// every other request on the server for as long as it ran.
const maxApplicationFanout = 4

// PerformanceByApplication returns one summary per application the caller may
// read, already narrowed by whatever else the filter row is holding.
//
// The scope ceiling is applied to the application list itself, so a PIC gets
// their own applications and nothing else, and a caller with no applications
// gets an empty list rather than the whole workspace.
func (r *Repo) PerformanceByApplication(
	ctx context.Context, sc Scope, f models.AnalyticsFilter,
) ([]models.ApplicationPerformance, error) {
	apps, err := r.applicationsInScope(ctx, sc, f)
	if err != nil {
		return nil, err
	}
	if len(apps) == 0 {
		return []models.ApplicationPerformance{}, nil
	}

	// Indexed rather than appended, so the result keeps the application order
	// the query chose no matter which goroutine finishes first.
	out := make([]models.ApplicationPerformance, len(apps))

	if err := fanOut(len(apps), maxApplicationFanout, func(i int) error {
		app := apps[i]

		scoped := f
		id := app.ID
		scoped.ApplicationID = &id

		report, err := r.Performance(ctx, sc, scoped)
		if err != nil {
			return fmt.Errorf("application %s: %w", app.Code, err)
		}
		out[i] = models.ApplicationPerformance{Application: app, Summary: report.Summary}
		return nil
	}); err != nil {
		return nil, err
	}

	// One query for every application, not one per application: this is a plain
	// count over an indexed join, and running it inside the fan-out above would
	// multiply it by the number of brands for no gain.
	ids := make([]uuid.UUID, len(apps))
	for i, a := range apps {
		ids[i] = a.ID
	}
	counts, err := r.contactsByApplication(ctx, sc, ids)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].ContactsTotal = counts[out[i].Application.ID]
	}

	return out, nil
}

// contactsByApplication counts the address book each application holds now.
//
// Scoped by workspace as well as by the id list, so a caller who somehow held
// an application id from elsewhere still reads nothing: the ceiling is applied
// in SQL, never by trusting the ids that got this far.
func (r *Repo) contactsByApplication(
	ctx context.Context, sc Scope, ids []uuid.UUID,
) (map[uuid.UUID]int, error) {
	out := map[uuid.UUID]int{}
	if len(ids) == 0 {
		return out, nil
	}

	rows, err := r.pool.Query(ctx, `
		select a.application_id, count(*)
		  from public.contacts ct
		  join public.whatsapp_accounts a on a.id = ct.account_id
		 where a.workspace_id = $1
		   and a.application_id = any ($2::uuid[])
		 group by a.application_id`, sc.WorkspaceID, ids)
	if err != nil {
		return nil, fmt.Errorf("contacts by application: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var id uuid.UUID
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}

// applicationsInScope lists the applications this view covers.
func (r *Repo) applicationsInScope(
	ctx context.Context, sc Scope, f models.AnalyticsFilter,
) ([]models.AppRef, error) {
	q := &queryArgs{}
	sql := fmt.Sprintf(
		`select id, code, name, color from public.applications where workspace_id = %s`,
		q.add(sc.WorkspaceID))

	if !sc.All {
		sql += fmt.Sprintf(" and id = any(%s)", q.add(sc.ApplicationIDs))
	}
	// When the report is about one person, the split is about the applications
	// that person actually holds. The fallback matters as much as the rule: a
	// Leader has no assignments of their own, so narrowing by them would return
	// nothing for exactly the reader who can see everything. The clause below
	// therefore narrows only when the person has assignments at all.
	if f.AdminID != nil {
		admin := q.add(*f.AdminID)
		sql += fmt.Sprintf(` and (
			id in (
				select application_id from public.pic_application_assignments
				 where pic_user_id = %[1]s
				union
				select application_id from public.freelancer_application_assignments
				 where freelancer_user_id = %[1]s
			)
			or not exists (
				select 1 from public.pic_application_assignments where pic_user_id = %[1]s
				union all
				select 1 from public.freelancer_application_assignments
				 where freelancer_user_id = %[1]s
			))`, admin)
	}
	// An application already chosen in the filter row leaves one row, which is
	// the honest answer: the split of a single application is that application.
	if f.ApplicationID != nil {
		sql += fmt.Sprintf(" and id = %s", q.add(*f.ApplicationID))
	}
	if f.PICUserID != nil {
		sql += fmt.Sprintf(` and id in (
			select application_id from public.pic_application_assignments where pic_user_id = %s)`,
			q.add(*f.PICUserID))
	}
	if f.AccountID != nil {
		sql += fmt.Sprintf(` and id = (
			select application_id from public.whatsapp_accounts
			 where id = %s and workspace_id = %s)`,
			q.add(*f.AccountID), q.add(sc.WorkspaceID))
	}
	sql += " order by sort_order, code"

	rows, err := r.pool.Query(ctx, sql, q.args...)
	if err != nil {
		return nil, fmt.Errorf("applications in scope: %w", err)
	}
	defer rows.Close()

	out := []models.AppRef{}
	for rows.Next() {
		var a models.AppRef
		if err := rows.Scan(&a.ID, &a.Code, &a.Name, &a.Color); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
