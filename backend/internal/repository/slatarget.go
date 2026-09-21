package repository

import (
	"context"

	"github.com/google/uuid"

	"github.com/salesan/omnichannel/backend/internal/models"
)

// The response-time promise a personal chat is measured against.
//
// Three layers, most specific first: the application's own target, then the
// workspace default, then the process configuration. The last one exists so a
// workspace that has never opened this screen is still measured rather than
// silently unscored.
//
// Nothing here rescores history. The resolved target is copied onto each
// sla_cycles row when the cycle is created, so changing a setting today
// changes how tomorrow is judged and leaves last month exactly as it was.

// ListSLATargets returns the workspace default and every per-application
// override, with the application named for display.
func (r *Repo) ListSLATargets(
	ctx context.Context, workspaceID uuid.UUID,
) ([]models.SLATarget, error) {
	rows, err := r.pool.Query(ctx, `
		select t.id, t.application_id, `+appJoinColumns+`,
		       t.target_seconds, t.business_hours, t.updated_at
		  from public.sla_targets t
		  left join public.applications a on a.id = t.application_id
		 where t.workspace_id = $1
		 order by a.code nulls first`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []models.SLATarget{}
	for rows.Next() {
		var t models.SLATarget
		var appID *uuid.UUID
		if err := rows.Scan(&t.ID, &t.ApplicationID, &appID, &t.ApplicationCode,
			&t.ApplicationName, &t.ApplicationColor,
			&t.TargetSeconds, &t.BusinessHours, &t.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// UpsertSLATarget sets the default (nil application) or one override.
func (r *Repo) UpsertSLATarget(
	ctx context.Context, workspaceID, actor uuid.UUID, in models.SLATarget,
) (*models.SLATarget, error) {
	// Two conflict targets, matching the two partial indexes: Postgres treats
	// NULLs as distinct, so one constraint would allow two workspace defaults.
	conflict := `(workspace_id) where application_id is null`
	if in.ApplicationID != nil {
		conflict = `(workspace_id, application_id) where application_id is not null`
	}

	var id uuid.UUID
	err := r.pool.QueryRow(ctx, `
		insert into public.sla_targets
			(workspace_id, application_id, target_seconds, business_hours, updated_by)
		values ($1, $2, $3, $4, $5)
		on conflict `+conflict+` do update
		   set target_seconds = excluded.target_seconds,
		       business_hours = excluded.business_hours,
		       updated_by = excluded.updated_by
		returning id`,
		workspaceID, in.ApplicationID, in.TargetSeconds, in.BusinessHours, actor).Scan(&id)
	if err != nil {
		return nil, mapErr(err)
	}

	var out models.SLATarget
	var appID *uuid.UUID
	err = r.pool.QueryRow(ctx, `
		select t.id, t.application_id, `+appJoinColumns+`,
		       t.target_seconds, t.business_hours, t.updated_at
		  from public.sla_targets t
		  left join public.applications a on a.id = t.application_id
		 where t.id = $1 and t.workspace_id = $2`, id, workspaceID).
		Scan(&out.ID, &out.ApplicationID, &appID, &out.ApplicationCode,
			&out.ApplicationName, &out.ApplicationColor,
			&out.TargetSeconds, &out.BusinessHours, &out.UpdatedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	return &out, nil
}

// DeleteSLATarget removes one row.
//
// Removing an override makes that application fall back to the workspace
// default; removing the default falls back to the process configuration.
// Neither changes a cycle already scored.
func (r *Repo) DeleteSLATarget(ctx context.Context, workspaceID, id uuid.UUID) (*uuid.UUID, error) {
	var appID *uuid.UUID
	err := r.pool.QueryRow(ctx,
		`delete from public.sla_targets where id = $1 and workspace_id = $2
		 returning application_id`, id, workspaceID).Scan(&appID)
	if err != nil {
		return nil, mapErr(err)
	}
	return appID, nil
}

// SLATargetApplication reports which application a row belongs to, so a
// handler can check reach before changing it. Nil means the workspace default.
func (r *Repo) SLATargetApplication(
	ctx context.Context, workspaceID, id uuid.UUID,
) (*uuid.UUID, error) {
	var appID *uuid.UUID
	err := r.pool.QueryRow(ctx,
		`select application_id from public.sla_targets where id = $1 and workspace_id = $2`,
		id, workspaceID).Scan(&appID)
	if err != nil {
		return nil, mapErr(err)
	}
	return appID, nil
}

// ResolveSLATarget picks the target that applies to one application.
//
// `fallback` is the process configuration, used only when the workspace has
// set nothing at all. Returning it rather than zero matters: a target of zero
// would mark every cycle breached the moment it was created.
func (r *Repo) ResolveSLATarget(
	ctx context.Context,
	workspaceID uuid.UUID,
	applicationID *uuid.UUID,
	fallback MetricsConfig,
) MetricsConfig {
	out := fallback

	// One query, ordered so the application's own row wins over the default.
	// `application_id is not null` sorts first because false < true.
	var seconds int
	var business bool
	err := r.pool.QueryRow(ctx, `
		select target_seconds, business_hours
		  from public.sla_targets
		 where workspace_id = $1
		   and (application_id is null or application_id = $2)
		 order by (application_id is null)
		 limit 1`, workspaceID, applicationID).Scan(&seconds, &business)
	if err != nil {
		// No row, or the lookup failed. Either way the process default is the
		// honest answer: guessing a stricter or looser target would rescore
		// work against a promise nobody made.
		return out
	}

	out.TargetSeconds = seconds
	out.UseBusinessHours = business
	return out
}
