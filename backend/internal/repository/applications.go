package repository

import (
	"context"

	"github.com/google/uuid"

	"github.com/salesan/omnichannel/backend/internal/models"
)

// ListApplications returns every application in the workspace with the
// aggregates the app picker (reference screen 4) renders.
// ListApplications returns the applications this caller is allowed to see.
//
// Narrowed in SQL rather than filtered afterwards: an application a Freelance
// is not assigned to must never reach the browser at all, and a list trimmed in
// JavaScript has already been sent by the time it is trimmed.
func (r *Repo) ListApplications(ctx context.Context, sc Scope) ([]models.Application, error) {
	q := `
		select a.id, a.workspace_id, a.code, a.name, a.color, a.icon_url,
		       a.sort_order, a.is_active,
		       coalesce(acc.n, 0), coalesce(conv.n, 0), coalesce(conv.unread, 0),
		       coalesce(conv.awaiting, 0)
		  from public.applications a
		  left join lateral (
		       select count(*) as n
		         from public.whatsapp_accounts w
		        where w.application_id = a.id
		  ) acc on true
		  left join lateral (
		       select count(*) as n,
		              coalesce(sum(c.unread_count), 0) as unread,
		              -- The same figure the account rows and the sidebar show,
		              -- read from the same column, so the three badges cannot
		              -- disagree about one question.
		              count(*) filter (where c.awaiting_reply) as awaiting
		         from public.conversations c
		         join public.whatsapp_accounts w on w.id = c.account_id
		        where w.application_id = a.id
		          and c.is_archived = false
		  ) conv on true
		 where a.workspace_id = $1`

	args := []any{sc.WorkspaceID}
	if !sc.All {
		// A caller with a role but no applications sees nothing, which is the
		// correct answer rather than a bug: somebody assigned nothing has
		// nothing to look at.
		args = append(args, sc.ApplicationIDs)
		q += " and a.id = any($2)"
	}
	q += " order by a.sort_order asc, a.name asc"

	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []models.Application{}
	for rows.Next() {
		var a models.Application
		if err := rows.Scan(
			&a.ID, &a.WorkspaceID, &a.Code, &a.Name, &a.Color, &a.IconURL,
			&a.SortOrder, &a.IsActive,
			&a.AccountCount, &a.ConversationCount, &a.UnreadCount, &a.UnansweredCount,
		); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// GetApplication loads one application scoped to the workspace.
func (r *Repo) GetApplication(ctx context.Context, workspaceID, id uuid.UUID) (*models.Application, error) {
	var a models.Application
	err := r.pool.QueryRow(ctx, `
		select id, workspace_id, code, name, color, icon_url, sort_order, is_active
		  from public.applications
		 where workspace_id = $1 and id = $2`, workspaceID, id,
	).Scan(&a.ID, &a.WorkspaceID, &a.Code, &a.Name, &a.Color, &a.IconURL, &a.SortOrder, &a.IsActive)
	if err != nil {
		return nil, mapErr(err)
	}
	return &a, nil
}

// CreateApplicationInput carries the fields the "Kelola Aplikasi" dialog sends.
type CreateApplicationInput struct {
	Code      string
	Name      string
	Color     string
	SortOrder int
}

// CreateApplication inserts a new application.
func (r *Repo) CreateApplication(ctx context.Context, workspaceID uuid.UUID, in CreateApplicationInput) (*models.Application, error) {
	var a models.Application
	err := r.pool.QueryRow(ctx, `
		insert into public.applications (workspace_id, code, name, color, sort_order)
		values ($1, $2, $3, $4, coalesce(nullif($5, 0),
		        (select coalesce(max(sort_order), 0) + 1 from public.applications where workspace_id = $1)))
		returning id, workspace_id, code, name, color, icon_url, sort_order, is_active`,
		workspaceID, in.Code, in.Name, in.Color, in.SortOrder,
	).Scan(&a.ID, &a.WorkspaceID, &a.Code, &a.Name, &a.Color, &a.IconURL, &a.SortOrder, &a.IsActive)
	if err != nil {
		return nil, mapErr(err)
	}
	return &a, nil
}

// UpdateApplicationInput carries partial updates; nil fields are left alone.
type UpdateApplicationInput struct {
	Name     *string
	Color    *string
	IsActive *bool
}

// UpdateApplication applies a partial update.
func (r *Repo) UpdateApplication(ctx context.Context, workspaceID, id uuid.UUID, in UpdateApplicationInput) (*models.Application, error) {
	var a models.Application
	err := r.pool.QueryRow(ctx, `
		update public.applications
		   set name      = coalesce($3, name),
		       color     = coalesce($4, color),
		       is_active = coalesce($5, is_active)
		 where workspace_id = $1 and id = $2
		returning id, workspace_id, code, name, color, icon_url, sort_order, is_active`,
		workspaceID, id, in.Name, in.Color, in.IsActive,
	).Scan(&a.ID, &a.WorkspaceID, &a.Code, &a.Name, &a.Color, &a.IconURL, &a.SortOrder, &a.IsActive)
	if err != nil {
		return nil, mapErr(err)
	}
	return &a, nil
}

// SetApplicationIcon points an application at a new logo, or at none, and
// returns the storage key it held before so the caller can delete that file.
//
// icon_url holds a storage key, not a link: the logo lives in the private
// bucket, and the API mints a short-lived signed URL for it on the way out.
func (r *Repo) SetApplicationIcon(
	ctx context.Context, workspaceID, id uuid.UUID, key *string,
) (previous *string, err error) {
	err = r.pool.QueryRow(ctx, `
		with old as (
			select icon_url from public.applications where workspace_id = $1 and id = $2
		)
		update public.applications a
		   set icon_url = $3
		  from old
		 where a.workspace_id = $1 and a.id = $2
		returning old.icon_url`,
		workspaceID, id, key,
	).Scan(&previous)
	if err != nil {
		return nil, mapErr(err)
	}
	return previous, nil
}

// DeleteApplication removes an application; accounts referencing it are simply
// unassigned (the FK is ON DELETE SET NULL).
func (r *Repo) DeleteApplication(ctx context.Context, workspaceID, id uuid.UUID) error {
	tag, err := r.pool.Exec(ctx,
		`delete from public.applications where workspace_id = $1 and id = $2`, workspaceID, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
