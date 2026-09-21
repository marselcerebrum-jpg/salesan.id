package repository

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"

	"github.com/salesan/omnichannel/backend/internal/models"
)

const userSelect = `
	select u.id, u.workspace_id, u.email, u.full_name, u.avatar_url, u.role::text
	  from public.users u
	 where u.id = $1`

// GetUser loads the caller's profile.
func (r *Repo) GetUser(ctx context.Context, userID uuid.UUID) (*models.User, error) {
	var u models.User
	err := r.pool.QueryRow(ctx, userSelect, userID).
		Scan(&u.ID, &u.WorkspaceID, &u.Email, &u.FullName, &u.AvatarURL, &u.Role)
	if err != nil {
		return nil, mapErr(err)
	}
	return &u, nil
}

var slugUnsafe = regexp.MustCompile(`[^a-z0-9]+`)

// EnsureUser returns the caller's profile, bootstrapping a workspace for them
// if none exists yet. The migration's auth trigger normally does this at signup;
// this covers users that already existed when the trigger was installed.
func (r *Repo) EnsureUser(ctx context.Context, userID uuid.UUID, email, fullName string) (*models.User, error) {
	u, err := r.GetUser(ctx, userID)
	if err == nil {
		return u, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, err
	}

	if fullName == "" {
		fullName = strings.Split(email, "@")[0]
	}
	slug := slugUnsafe.ReplaceAllString(strings.ToLower(strings.Split(email, "@")[0]), "-")
	slug = fmt.Sprintf("%s-%s", strings.Trim(slug, "-"), strings.ReplaceAll(userID.String(), "-", "")[:8])

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var workspaceID uuid.UUID
	if err := tx.QueryRow(ctx,
		`insert into public.workspaces (name, slug) values ($1, $2) returning id`,
		fullName+"'s Workspace", slug,
	).Scan(&workspaceID); err != nil {
		return nil, fmt.Errorf("create workspace: %w", err)
	}

	if _, err := tx.Exec(ctx,
		`insert into public.users (id, workspace_id, email, full_name, role)
		 values ($1, $2, $3, $4, 'owner')`,
		userID, workspaceID, email, fullName,
	); err != nil {
		return nil, fmt.Errorf("create user profile: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		insert into public.applications (workspace_id, code, name, color, sort_order)
		select $1, code, code, color, ord
		  from (values
			('JADIASN','#1B7F5A',1), ('JADIBEASISWA','#C2853A',2), ('JADIBUMN','#166534',3),
			('JADIOJK','#2563EB',4), ('JADIPCPM','#7C3AED',5),     ('JADIPOLISI','#DC2626',6),
			('JADISEKDIN','#D97706',7), ('TOEFLACADEMY','#0891B2',8)
		  ) as seed(code, color, ord)
		on conflict (workspace_id, code) do nothing`, workspaceID); err != nil {
		return nil, fmt.Errorf("seed applications: %w", err)
	}

	// Labels are deliberately not seeded: they must mirror what actually exists
	// in WhatsApp Business, or be created by hand in the app. Inventing tags
	// here would fill the inbox filter row with names nobody uses and that have
	// no counterpart on the phone.

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return r.GetUser(ctx, userID)
}

// GetWorkspace loads a workspace by ID.
func (r *Repo) GetWorkspace(ctx context.Context, id uuid.UUID) (*models.Workspace, error) {
	var w models.Workspace
	err := r.pool.QueryRow(ctx,
		`select id, name, slug from public.workspaces where id = $1`, id,
	).Scan(&w.ID, &w.Name, &w.Slug)
	if err != nil {
		return nil, mapErr(err)
	}
	return &w, nil
}
