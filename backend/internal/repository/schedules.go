package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/salesan/omnichannel/backend/internal/analytics"
	"github.com/salesan/omnichannel/backend/internal/models"
)

const scheduleColumns = `
	s.id, s.workspace_id, s.user_id, coalesce(nullif(u.full_name, ''), u.email),
	s.pic_user_id, pic.full_name,
	s.application_id, app.code,
	s.account_id, acc.name,
	to_char(s.work_date, 'YYYY-MM-DD'), s.weekday,
	to_char(s.starts_at, 'HH24:MI'), to_char(s.ends_at, 'HH24:MI'),
	s.timezone, s.is_active, s.note, s.created_at`

const scheduleJoins = `
	from public.work_schedules s
	join public.users u on u.id = s.user_id
	left join public.users pic on pic.id = s.pic_user_id
	left join public.applications app on app.id = s.application_id
	left join public.whatsapp_accounts acc on acc.id = s.account_id`

func scanSchedule(row scannable) (*models.WorkSchedule, error) {
	var s models.WorkSchedule
	err := row.Scan(&s.ID, &s.WorkspaceID, &s.UserID, &s.UserName,
		&s.PICUserID, &s.PICName,
		&s.ApplicationID, &s.ApplicationCode,
		&s.AccountID, &s.AccountName,
		&s.WorkDate, &s.Weekday, &s.StartsAt, &s.EndsAt,
		&s.Timezone, &s.IsActive, &s.Note, &s.CreatedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	return &s, nil
}

// Which of the two shapes a listing wants. Empty means both.
const (
	ScheduleKindWeekly = "weekly"
	ScheduleKindDated  = "dated"
)

// ScheduleFilter narrows a schedule listing.
type ScheduleFilter struct {
	From          time.Time // inclusive calendar date
	To            time.Time // inclusive calendar date
	UserID        *uuid.UUID
	ApplicationID *uuid.UUID
	AccountID     *uuid.UUID
	PICUserID     *uuid.UUID
	// Kind is "weekly", "dated", or empty for both.
	Kind string
}

// ListSchedules returns the shifts a caller may see in a date range.
func (r *Repo) ListSchedules(ctx context.Context, sc Scope, f ScheduleFilter) ([]models.WorkSchedule, error) {
	q := `select ` + scheduleColumns + scheduleJoins + ` where s.workspace_id = $1`
	args := []any{sc.WorkspaceID}

	// A date range asks about dated shifts. The weekly pattern has no date to
	// compare against — it is the setting behind those days, not one of them —
	// so it is listed only when no range was given, which is how the settings
	// screen asks for it.
	if !f.From.IsZero() {
		args = append(args, f.From)
		q += fmt.Sprintf(" and s.work_date >= $%d", len(args))
	}
	if !f.To.IsZero() {
		args = append(args, f.To)
		q += fmt.Sprintf(" and s.work_date <= $%d", len(args))
	}
	switch f.Kind {
	case ScheduleKindWeekly:
		q += " and s.weekday is not null"
	case ScheduleKindDated:
		q += " and s.work_date is not null"
	}
	if f.UserID != nil {
		args = append(args, *f.UserID)
		q += fmt.Sprintf(" and s.user_id = $%d", len(args))
	}
	if f.ApplicationID != nil {
		args = append(args, *f.ApplicationID)
		q += fmt.Sprintf(" and s.application_id = $%d", len(args))
	}
	if f.AccountID != nil {
		args = append(args, *f.AccountID)
		q += fmt.Sprintf(" and s.account_id = $%d", len(args))
	}
	if f.PICUserID != nil {
		args = append(args, *f.PICUserID)
		q += fmt.Sprintf(" and s.pic_user_id = $%d", len(args))
	}

	// The same restriction the RLS policy applies, repeated here because the
	// backend connects as the service role and RLS does not run for it.
	if !sc.All {
		args = append(args, sc.AdminIDs)
		q += fmt.Sprintf(" and (s.user_id = any($%d)", len(args))
		args = append(args, sc.ApplicationIDs)
		q += fmt.Sprintf(" or s.application_id = any($%d))", len(args))
	}

	q += " order by s.weekday nulls first, s.work_date desc, s.starts_at"

	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []models.WorkSchedule{}
	for rows.Next() {
		s, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *s)
	}
	return out, rows.Err()
}

// ScheduleInput is one shift to create or update.
type ScheduleInput struct {
	UserID        uuid.UUID
	PICUserID     *uuid.UUID
	ApplicationID *uuid.UUID
	AccountID     *uuid.UUID
	// Exactly one of WorkDate and Weekday carries the answer to "when". A zero
	// WorkDate with a Weekday set writes a weekly pattern row; a WorkDate with
	// no Weekday writes a single dated shift.
	WorkDate time.Time
	Weekday  *int
	StartsAt string // HH:MM
	EndsAt   string // HH:MM
	Timezone string
	IsActive bool
	Note     *string
}

// UpsertSchedule writes a shift, replacing an identical slot rather than
// duplicating it.
//
// The unique index in migration 0020 covers the whole assignment tuple, so
// saving the same shift twice — a double-click, a retried request — updates it
// instead of creating a second block that would double the person's counted
// working hours.
func (r *Repo) UpsertSchedule(
	ctx context.Context,
	workspaceID, createdBy uuid.UUID,
	in ScheduleInput,
) (*models.WorkSchedule, error) {
	if in.Timezone == "" {
		in.Timezone = "Asia/Jakarta"
	}

	// A Freelance's PIC is not asked for on the form: it is looked up, so the
	// shift always records the atasan that was actually in force that day.
	if in.PICUserID == nil {
		var pic *uuid.UUID
		if err := r.pool.QueryRow(ctx,
			`select pic_user_id from public.freelancer_pic_assignments where freelancer_user_id = $1`,
			in.UserID).Scan(&pic); err == nil {
			in.PICUserID = pic
		} else if !isNoRows(err) {
			return nil, err
		}
	}

	// Two shapes, two unique indexes, so the ON CONFLICT target has to match the
	// one this row belongs to. Writing both into one statement would need a
	// conflict target that covers a column that is null on half the rows, and
	// Postgres never treats two nulls as a collision — every save would insert
	// a new row and the person's counted hours would grow with each click.
	target := `(user_id, work_date,
	            coalesce(application_id, '00000000-0000-0000-0000-000000000000'::uuid),
	            coalesce(account_id,     '00000000-0000-0000-0000-000000000000'::uuid),
	            starts_at) where work_date is not null`
	var workDate any = in.WorkDate
	if in.Weekday != nil {
		workDate = nil
		target = `(user_id, weekday,
		           coalesce(application_id, '00000000-0000-0000-0000-000000000000'::uuid),
		           coalesce(account_id,     '00000000-0000-0000-0000-000000000000'::uuid),
		           starts_at) where weekday is not null`
	}

	q := `
		insert into public.work_schedules
			(workspace_id, user_id, pic_user_id, application_id, account_id,
			 work_date, weekday, starts_at, ends_at, timezone, is_active, note, created_by)
		values ($1, $2, $3, $4, $5, $6, $7, $8::time, $9::time, $10, $11, $12, $13)
		on conflict ` + target + `
		do update set ends_at    = excluded.ends_at,
		              pic_user_id = excluded.pic_user_id,
		              timezone   = excluded.timezone,
		              is_active  = excluded.is_active,
		              note       = excluded.note
		returning id`

	var id uuid.UUID
	if err := r.pool.QueryRow(ctx, q,
		workspaceID, in.UserID, in.PICUserID, in.ApplicationID, in.AccountID,
		workDate, in.Weekday, in.StartsAt, in.EndsAt, in.Timezone, in.IsActive, in.Note, createdBy,
	).Scan(&id); err != nil {
		return nil, err
	}
	return r.GetSchedule(ctx, workspaceID, id)
}

// GetSchedule loads one shift.
func (r *Repo) GetSchedule(ctx context.Context, workspaceID, id uuid.UUID) (*models.WorkSchedule, error) {
	return scanSchedule(r.pool.QueryRow(ctx,
		`select `+scheduleColumns+scheduleJoins+` where s.id = $1 and s.workspace_id = $2`,
		id, workspaceID))
}

// DeleteSchedule removes a shift.
func (r *Repo) DeleteSchedule(ctx context.Context, workspaceID, id uuid.UUID) error {
	tag, err := r.pool.Exec(ctx,
		`delete from public.work_schedules where id = $1 and workspace_id = $2`, id, workspaceID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// WindowQuery selects which shifts become working windows.
type WindowQuery struct {
	From          time.Time
	To            time.Time
	UserID        *uuid.UUID
	ApplicationID *uuid.UUID
	AccountID     *uuid.UUID
	PICUserID     *uuid.UUID
	ScheduleID    *uuid.UUID
}

// ScheduleWindows turns stored shifts into absolute UTC intervals.
//
// The conversion happens in SQL, using each row's own timezone, because
// Postgres knows the tzdata and a Go binary on Windows may not. A shift stored
// as 09:00–17:00 Asia/Jakarta on 2026-09-09 becomes 02:00–10:00 UTC that day —
// and would become something else entirely if it were interpreted in the
// server's local zone.
func (r *Repo) ScheduleWindows(ctx context.Context, workspaceID uuid.UUID, q WindowQuery) ([]analytics.Window, error) {
	args := []any{workspaceID}
	// Shared by both halves of the union below, so a filter cannot be applied to
	// dated shifts and forgotten on the weekly pattern.
	common := ""
	if q.UserID != nil {
		args = append(args, *q.UserID)
		common += fmt.Sprintf(" and s.user_id = $%d", len(args))
	}
	if q.ApplicationID != nil {
		args = append(args, *q.ApplicationID)
		common += fmt.Sprintf(" and (s.application_id = $%d or s.application_id is null)", len(args))
	}
	if q.AccountID != nil {
		args = append(args, *q.AccountID)
		common += fmt.Sprintf(" and (s.account_id = $%d or s.account_id is null)", len(args))
	}
	if q.PICUserID != nil {
		args = append(args, *q.PICUserID)
		// A weekly pattern carries no PIC of its own — it says when a person
		// works, not who they answered to that day — so it is matched through
		// the assignment instead. A dated shift keeps its frozen pic_user_id,
		// which is what makes last month's report survive a change of manager.
		common += fmt.Sprintf(` and (s.pic_user_id = $%[1]d or (s.pic_user_id is null and exists (
			select 1 from public.freelancer_pic_assignments fp
			 where fp.freelancer_user_id = s.user_id and fp.pic_user_id = $%[1]d)))`, len(args))
	}
	if q.ScheduleID != nil {
		args = append(args, *q.ScheduleID)
		common += fmt.Sprintf(" and s.id = $%d", len(args))
	}

	// One day of slack on each side: a shift may start the evening before the
	// range in local time and still overlap it.
	//
	// A weekly pattern has to be given bounds whatever the caller asked for —
	// "every Monday" has no end — so an open-ended request falls back to the
	// last month. Dated rows keep the old behaviour of having no bound at all
	// when none was given, because a caller asking for one shift by id means
	// that shift wherever it sits in the calendar.
	dated := ""
	if !q.From.IsZero() {
		args = append(args, q.From.AddDate(0, 0, -1))
		dated += fmt.Sprintf(" and s.work_date >= $%d::date", len(args))
	}
	if !q.To.IsZero() {
		args = append(args, q.To.AddDate(0, 0, 1))
		dated += fmt.Sprintf(" and s.work_date <= $%d::date", len(args))
	}

	from, to := q.From, q.To
	if from.IsZero() {
		from = time.Now().AddDate(0, 0, -31)
	}
	if to.IsZero() {
		to = time.Now().AddDate(0, 0, 1)
	}
	args = append(args, from.AddDate(0, 0, -1))
	fromArg := fmt.Sprintf("$%d", len(args))
	args = append(args, to.AddDate(0, 0, 1))
	toArg := fmt.Sprintf("$%d", len(args))

	// Two ways of saying when somebody works, resolved into one set of
	// intervals. The dated rows are taken as they are; the weekly rows are
	// spread across the days of the range that fall on their weekday.
	//
	// A dated row for a person WINS over their pattern for that whole day. That
	// is what makes a public holiday or a swapped shift expressible without
	// touching the pattern: write the one day, and the pattern steps aside for
	// that person on that date.
	sql := `
		select s.id, s.user_id,
		       ((s.work_date + s.starts_at) at time zone s.timezone) at time zone 'UTC',
		       ((s.work_date + s.ends_at)   at time zone s.timezone) at time zone 'UTC'
		  from public.work_schedules s
		 where s.workspace_id = $1 and s.is_active
		   and s.work_date is not null` + dated + common + `

		union all

		select s.id, s.user_id,
		       ((d::date + s.starts_at) at time zone s.timezone) at time zone 'UTC',
		       ((d::date + s.ends_at)   at time zone s.timezone) at time zone 'UTC'
		  from public.work_schedules s
		  cross join lateral generate_series(
		         ` + fromArg + `::date, ` + toArg + `::date, interval '1 day') as d
		 where s.workspace_id = $1 and s.is_active
		   and s.weekday is not null
		   and extract(dow from d)::int = s.weekday
		   and not exists (
		     select 1 from public.work_schedules o
		      where o.workspace_id = s.workspace_id
		        and o.user_id = s.user_id
		        and o.work_date = d::date)` + common

	rows, err := r.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []analytics.Window{}
	for rows.Next() {
		var w analytics.Window
		if err := rows.Scan(&w.ScheduleID, &w.UserID, &w.Start, &w.End); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}
