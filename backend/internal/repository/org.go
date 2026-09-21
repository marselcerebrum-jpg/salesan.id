package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/salesan/omnichannel/backend/internal/models"
)

// ErrForbidden reports a request the caller's role does not permit.
var ErrForbidden = errors.New("repository: not allowed for this role")

// Scope is what one caller is allowed to see, resolved once per request.
//
// The RLS policies in migration 0019 express the same rules for the direct
// Supabase path. This is the API path's copy of them, and the two must agree —
// which is why both are derived from the same three assignment tables rather
// than from a list maintained by hand in either place.
type Scope struct {
	UserID      uuid.UUID
	WorkspaceID uuid.UUID
	// Role is the operational role: leader, pic, freelance, or empty when the
	// user has none.
	Role string
	// All is true when the caller sees the whole workspace. A workspace owner
	// with no operational role assigned yet counts as a Leader, so a fresh
	// workspace is never locked out of its own dashboard.
	All bool
	// ApplicationIDs is the visible set; empty when All is true.
	ApplicationIDs []uuid.UUID
	// AdminIDs is whose performance may be read; always contains UserID.
	AdminIDs []uuid.UUID
}

// CanSeeAdmin reports whether this scope may read one person's activity.
func (s Scope) CanSeeAdmin(id uuid.UUID) bool {
	if s.All {
		return true
	}
	for _, a := range s.AdminIDs {
		if a == id {
			return true
		}
	}
	return false
}

// CanSeeApplication reports whether this scope covers one application.
func (s Scope) CanSeeApplication(id uuid.UUID) bool {
	if s.All {
		return true
	}
	for _, a := range s.ApplicationIDs {
		if a == id {
			return true
		}
	}
	return false
}

// IsLeader reports whether the caller administers the whole workspace.
func (s Scope) IsLeader() bool { return s.All }

// CanManageSchedules reports whether the caller may create or change shifts.
// A Freelance does not write their own rota.
func (s Scope) CanManageSchedules() bool {
	return s.All || s.Role == models.RolePIC
}

// CanCreateRole reports whether the caller may create an account with this role.
//
// A Leader hires anybody. A PIC hires only into their own team, which is what
// makes the hierarchy something they can actually run rather than a shape they
// have to ask somebody else to change for them. Nobody else creates accounts:
// an account is access, and handing out access is a responsibility, not a task.
func (s Scope) CanCreateRole(role string) bool {
	switch {
	case s.All:
		return true
	case s.Role == models.RolePIC:
		return role == models.RoleFreelance
	default:
		return false
	}
}

// CreatableRoles lists the roles this caller may hand out, for an interface
// that would rather not offer a choice that will be refused.
func (s Scope) CreatableRoles() []string {
	switch {
	case s.All:
		return []string{models.RoleLeader, models.RolePIC, models.RoleFreelance}
	case s.Role == models.RolePIC:
		return []string{models.RoleFreelance}
	default:
		return []string{}
	}
}

// CanManageMember reports whether the caller may change one member's
// assignments or switch their access off.
//
// A PIC manages the people under them and nobody else — not another PIC's
// Freelance, and not themselves: changing your own scope is how a mistake
// becomes unrecoverable without help.
func (s Scope) CanManageMember(memberID uuid.UUID, memberRole string, picID *uuid.UUID) bool {
	if s.All {
		return true
	}
	if s.Role != models.RolePIC || memberRole != models.RoleFreelance {
		return false
	}
	return picID != nil && *picID == s.UserID && memberID != s.UserID
}

// ResolveScope works out what a caller may see.
func (r *Repo) ResolveScope(ctx context.Context, user *models.User) (Scope, error) {
	sc := Scope{UserID: user.ID, WorkspaceID: user.WorkspaceID, AdminIDs: []uuid.UUID{user.ID}}

	// A deactivated member keeps their profile — history has to stay
	// attributable — but nothing they can see. Checked here rather than only in
	// the role table, so deactivating a workspace owner takes effect too.
	var active bool
	if err := r.pool.QueryRow(ctx,
		`select is_active from public.users where id = $1`, user.ID).Scan(&active); err != nil {
		return sc, mapErr(err)
	}
	if !active {
		return sc, nil
	}

	var role *string
	err := r.pool.QueryRow(ctx,
		`select ra.role::text from public.role_assignments ra
		  where ra.user_id = $1 and ra.workspace_id = $2 and ra.is_active`,
		user.ID, user.WorkspaceID,
	).Scan(&role)
	if err != nil && !isNoRows(err) {
		return sc, err
	}
	if role != nil {
		sc.Role = *role
	}

	switch {
	case sc.Role == models.RoleLeader:
		sc.All = true
	case sc.Role == "" && (user.Role == "owner" || user.Role == "admin"):
		// No operational role has been set up yet. The workspace owner is the
		// person who would set it up, so locking them out first would be a
		// deadlock rather than a safeguard.
		sc.All = true
		sc.Role = models.RoleLeader
	}

	if sc.All {
		return sc, nil
	}

	var q string
	switch sc.Role {
	case models.RolePIC:
		q = `select application_id from public.pic_application_assignments
		      where pic_user_id = $1 and workspace_id = $2`
	case models.RoleFreelance:
		q = `select application_id from public.freelancer_application_assignments
		      where freelancer_user_id = $1 and workspace_id = $2`
	default:
		// A member with no role and no ownership sees nothing until somebody
		// assigns them one. Returning an empty scope is the safe default; the
		// alternative — falling through to "everything" — is how these systems
		// leak.
		return sc, nil
	}

	rows, err := r.pool.Query(ctx, q, user.ID, user.WorkspaceID)
	if err != nil {
		return sc, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return sc, err
		}
		sc.ApplicationIDs = append(sc.ApplicationIDs, id)
	}
	if err := rows.Err(); err != nil {
		return sc, err
	}

	if sc.Role == models.RolePIC {
		team, err := r.pool.Query(ctx,
			`select freelancer_user_id from public.freelancer_pic_assignments
			  where pic_user_id = $1 and workspace_id = $2`,
			user.ID, user.WorkspaceID)
		if err != nil {
			return sc, err
		}
		defer team.Close()
		for team.Next() {
			var id uuid.UUID
			if err := team.Scan(&id); err != nil {
				return sc, err
			}
			sc.AdminIDs = append(sc.AdminIDs, id)
		}
		if err := team.Err(); err != nil {
			return sc, err
		}
	}

	return sc, nil
}

// --- org directory -----------------------------------------------------------

const orgMemberQuery = `
	select u.id, u.email, u.full_name, u.avatar_url, u.role::text,
	       coalesce(ra.role::text, ''), u.is_active,
	       fp.pic_user_id, pic.full_name,
	       coalesce(ra.created_at, u.created_at),
	       (select count(*) from public.freelancer_pic_assignments f2
	         where f2.pic_user_id = u.id)
	  from public.users u
	  left join public.role_assignments ra
	         on ra.user_id = u.id and ra.workspace_id = u.workspace_id
	  left join public.freelancer_pic_assignments fp on fp.freelancer_user_id = u.id
	  left join public.users pic on pic.id = fp.pic_user_id
	 where u.workspace_id = $1
	 order by case coalesce(ra.role::text, 'zzz')
	            when 'leader' then 0 when 'pic' then 1 when 'freelance' then 2 else 3 end,
	          coalesce(u.full_name, u.email)`

// ListOrgMembers returns everyone in the workspace with their operational role
// and assignments.
//
// Every member of the workspace can read this list — a Freelance needs to know
// who their PIC is. What is restricted is changing it.
func (r *Repo) ListOrgMembers(ctx context.Context, workspaceID uuid.UUID) ([]models.OrgMember, error) {
	rows, err := r.pool.Query(ctx, orgMemberQuery, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []models.OrgMember{}
	index := map[uuid.UUID]int{}
	for rows.Next() {
		var m models.OrgMember
		if err := rows.Scan(&m.UserID, &m.Email, &m.FullName, &m.AvatarURL, &m.WorkspaceRole,
			&m.Role, &m.IsActive, &m.PICUserID, &m.PICName, &m.CreatedAt, &m.FreelanceCount); err != nil {
			return nil, err
		}
		m.Applications = []models.AppRef{}
		index[m.UserID] = len(out)
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return out, nil
	}

	// Applications come from two tables depending on the role, unioned here so
	// the caller sees one list per person rather than having to know which
	// table a given role's assignments live in.
	appRows, err := r.pool.Query(ctx, `
		select x.user_id, a.id, a.code, a.name, a.color
		  from (
			select pic_user_id as user_id, application_id from public.pic_application_assignments
			 where workspace_id = $1
			union all
			select freelancer_user_id, application_id from public.freelancer_application_assignments
			 where workspace_id = $1
		  ) x
		  join public.applications a on a.id = x.application_id
		 order by a.sort_order, a.code`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer appRows.Close()
	for appRows.Next() {
		var userID uuid.UUID
		var ref models.AppRef
		if err := appRows.Scan(&userID, &ref.ID, &ref.Code, &ref.Name, &ref.Color); err != nil {
			return nil, err
		}
		if i, ok := index[userID]; ok {
			out[i].Applications = append(out[i].Applications, ref)
		}
	}
	return out, appRows.Err()
}

// SetOperationalRole assigns or changes someone's operational role.
//
// Changing a role deliberately clears the assignments that no longer apply:
// a PIC demoted to Freelance must not keep holding applications as a PIC, and
// leaving those rows behind is how a demoted account keeps its old access.
func (r *Repo) SetOperationalRole(
	ctx context.Context,
	workspaceID, userID, assignedBy uuid.UUID,
	role string,
) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var name *string
	if err := tx.QueryRow(ctx,
		`select full_name from public.users where id = $1 and workspace_id = $2`,
		userID, workspaceID).Scan(&name); err != nil {
		return mapErr(err)
	}

	if _, err := tx.Exec(ctx, `
		insert into public.role_assignments (workspace_id, user_id, role, display_name, assigned_by)
		values ($1, $2, $3::public.operational_role, $4, $5)
		on conflict (workspace_id, user_id) do update
		   set role = excluded.role,
		       display_name = excluded.display_name,
		       assigned_by = excluded.assigned_by,
		       is_active = true`,
		workspaceID, userID, role, name, assignedBy); err != nil {
		return err
	}

	if role != models.RolePIC {
		if _, err := tx.Exec(ctx,
			`delete from public.pic_application_assignments where pic_user_id = $1`, userID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`delete from public.freelancer_pic_assignments where pic_user_id = $1`, userID); err != nil {
			return err
		}
	}
	if role != models.RoleFreelance {
		if _, err := tx.Exec(ctx,
			`delete from public.freelancer_application_assignments where freelancer_user_id = $1`, userID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`delete from public.freelancer_pic_assignments where freelancer_user_id = $1`, userID); err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}

// AssignmentInput carries one org edit.
type AssignmentInput struct {
	UserID         uuid.UUID
	ApplicationIDs []uuid.UUID
	PICUserID      *uuid.UUID
}

// SetMemberAssignments replaces one member's application list and, for a
// Freelance, their PIC.
//
// Replacement rather than incremental add/remove: the caller sends the state it
// wants, and one transaction makes the database match. Two half-applied edits
// cannot leave someone holding an application nobody meant them to have.
func (r *Repo) SetMemberAssignments(
	ctx context.Context,
	workspaceID, assignedBy uuid.UUID,
	in AssignmentInput,
) error {
	var role string
	if err := r.pool.QueryRow(ctx,
		`select role::text from public.role_assignments
		  where user_id = $1 and workspace_id = $2 and is_active`,
		in.UserID, workspaceID).Scan(&role); err != nil {
		if isNoRows(err) {
			return fmt.Errorf("%w: peran operasional belum ditetapkan", ErrForbidden)
		}
		return err
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	switch role {
	case models.RolePIC:
		/*
		 * One application, one PIC.
		 *
		 * Checked here as well as by the unique index behind it, for the reason
		 * every rule worth keeping is checked twice: the index protects the
		 * data, and this protects the person doing the assigning. A constraint
		 * violation surfaces as "terjadi kesalahan" and leaves them guessing;
		 * this names the PIC who already holds it, which is the one fact they
		 * need to fix it.
		 *
		 * Two PICs on one application would not fail loudly anywhere else: both
		 * of their figures would simply include it, and the sum of the PICs
		 * would quietly exceed the operation.
		 */
		for _, appID := range in.ApplicationIDs {
			var holder string
			err := tx.QueryRow(ctx, `
				select coalesce(nullif(u.full_name, ''), u.email)
				  from public.pic_application_assignments p
				  join public.users u on u.id = p.pic_user_id
				 where p.application_id = $1 and p.workspace_id = $2 and p.pic_user_id <> $3
				 limit 1`, appID, workspaceID, in.UserID).Scan(&holder)
			if err != nil && !isNoRows(err) {
				return err
			}
			if err == nil {
				return fmt.Errorf(
					"%w: aplikasi ini sudah dipegang %s. Lepaskan dari PIC itu dulu sebelum memindahkannya",
					ErrForbidden, holder)
			}
		}

		if _, err := tx.Exec(ctx,
			`delete from public.pic_application_assignments where pic_user_id = $1`, in.UserID); err != nil {
			return err
		}
		for _, appID := range in.ApplicationIDs {
			if _, err := tx.Exec(ctx, `
				insert into public.pic_application_assignments
					(workspace_id, pic_user_id, application_id, assigned_by)
				values ($1, $2, $3, $4)
				on conflict (pic_user_id, application_id) do nothing`,
				workspaceID, in.UserID, appID, assignedBy); err != nil {
				return err
			}
		}

	case models.RoleFreelance:
		// A Freelance works inside their PIC's remit, never beside it.
		//
		// Enforced here rather than only in the form, for the usual reason: the
		// form is a convenience and this is the rule. A Freelance holding an
		// application their PIC does not hold would see conversations the
		// person accountable for them cannot, and every per-team figure that
		// rolls a Freelance up into their PIC would quietly stop adding up.
		if in.PICUserID != nil && len(in.ApplicationIDs) > 0 {
			rows, err := tx.Query(ctx, `
				select application_id
				  from public.pic_application_assignments
				 where pic_user_id = $1 and workspace_id = $2`,
				*in.PICUserID, workspaceID)
			if err != nil {
				return err
			}
			allowed := map[uuid.UUID]bool{}
			for rows.Next() {
				var id uuid.UUID
				if err := rows.Scan(&id); err != nil {
					rows.Close()
					return err
				}
				allowed[id] = true
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				return err
			}
			for _, appID := range in.ApplicationIDs {
				if !allowed[appID] {
					return fmt.Errorf(
						"%w: Freelance hanya dapat ditugaskan ke aplikasi yang dipegang PIC-nya",
						ErrForbidden)
				}
			}
		}

		if _, err := tx.Exec(ctx,
			`delete from public.freelancer_application_assignments where freelancer_user_id = $1`,
			in.UserID); err != nil {
			return err
		}
		for _, appID := range in.ApplicationIDs {
			if _, err := tx.Exec(ctx, `
				insert into public.freelancer_application_assignments
					(workspace_id, freelancer_user_id, application_id, assigned_by)
				values ($1, $2, $3, $4)
				on conflict (freelancer_user_id, application_id) do nothing`,
				workspaceID, in.UserID, appID, assignedBy); err != nil {
				return err
			}
		}

		if in.PICUserID == nil {
			if _, err := tx.Exec(ctx,
				`delete from public.freelancer_pic_assignments where freelancer_user_id = $1`,
				in.UserID); err != nil {
				return err
			}
		} else if _, err := tx.Exec(ctx, `
			insert into public.freelancer_pic_assignments
				(workspace_id, freelancer_user_id, pic_user_id, assigned_by)
			values ($1, $2, $3, $4)
			on conflict (freelancer_user_id) do update
			   set pic_user_id = excluded.pic_user_id, assigned_by = excluded.assigned_by`,
			workspaceID, in.UserID, *in.PICUserID, assignedBy); err != nil {
			return err
		}

	default:
		// A Leader already sees everything; giving them an application list
		// would suggest a narrowing that does not exist.
		return fmt.Errorf("%w: leader tidak memerlukan penugasan aplikasi", ErrForbidden)
	}

	return tx.Commit(ctx)
}

// AttachMember records the profile row for a freshly created Supabase account
// and gives it its operational role, in one transaction.
//
// The signup trigger in migration 0025 has normally already written the profile
// by the time the Admin API returns, since it fires inside the same statement.
// The insert here is a fallback for the case where it has not — a trigger that
// was dropped, or a project where the auth schema is managed differently — so
// that a created account is never left visible in Supabase and invisible here.
// AttachMemberInput carries the placement of a newly created account.
type AttachMemberInput struct {
	UserID   uuid.UUID
	Email    string
	FullName string
	// WorkspaceRole is the ownership role; OperationalRole is the responsibility.
	WorkspaceRole   string
	OperationalRole string
	// PICUserID is set when a Freelance is created under somebody. Filled in
	// automatically when a PIC creates the account, so a new team member is
	// never left dangling with a role and no one to report to.
	PICUserID *uuid.UUID
	// ApplicationIDs the new member starts on. A Freelance with none sees an
	// empty product, so a PIC's own applications are the useful default.
	ApplicationIDs []uuid.UUID
}

func (r *Repo) AttachMember(
	ctx context.Context,
	workspaceID, userID, assignedBy uuid.UUID,
	email, fullName, workspaceRole, operationalRole string,
) error {
	return r.AttachMemberFull(ctx, workspaceID, assignedBy, AttachMemberInput{
		UserID:          userID,
		Email:           email,
		FullName:        fullName,
		WorkspaceRole:   workspaceRole,
		OperationalRole: operationalRole,
	})
}

// AttachMemberFull records the profile, the role, and the placement in one
// transaction.
//
// One transaction because they are one act: an account that exists with no role,
// or a Freelance with a role but no PIC, is a half-created person somebody has
// to notice and finish by hand.
func (r *Repo) AttachMemberFull(
	ctx context.Context,
	workspaceID, assignedBy uuid.UUID,
	in AttachMemberInput,
) error {
	userID := in.UserID
	email, fullName := in.Email, in.FullName
	workspaceRole, operationalRole := in.WorkspaceRole, in.OperationalRole
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `
		insert into public.users (id, workspace_id, email, full_name, role)
		values ($1, $2, $3, $4, $5::public.workspace_role)
		on conflict (id) do update set
			workspace_id = excluded.workspace_id,
			full_name    = coalesce(nullif(excluded.full_name, ''), public.users.full_name),
			is_active    = true,
			deactivated_at = null`,
		userID, workspaceID, email, fullName, workspaceRole); err != nil {
		return fmt.Errorf("attach profile: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		insert into public.role_assignments (workspace_id, user_id, role, display_name, assigned_by)
		values ($1, $2, $3::public.operational_role, $4, $5)
		on conflict (workspace_id, user_id) do update set
			role = excluded.role, display_name = excluded.display_name, is_active = true`,
		workspaceID, userID, operationalRole, fullName, assignedBy); err != nil {
		return fmt.Errorf("assign role: %w", err)
	}

	if operationalRole == models.RoleFreelance && in.PICUserID != nil {
		if _, err := tx.Exec(ctx, `
			insert into public.freelancer_pic_assignments
				(workspace_id, freelancer_user_id, pic_user_id, assigned_by)
			values ($1, $2, $3, $4)
			on conflict (freelancer_user_id) do update
			   set pic_user_id = excluded.pic_user_id, assigned_by = excluded.assigned_by`,
			workspaceID, userID, *in.PICUserID, assignedBy); err != nil {
			return fmt.Errorf("assign pic: %w", err)
		}
	}

	table := "freelancer_application_assignments"
	column := "freelancer_user_id"
	if operationalRole == models.RolePIC {
		table = "pic_application_assignments"
		column = "pic_user_id"
	}
	if operationalRole != models.RoleLeader {
		for _, appID := range in.ApplicationIDs {
			if _, err := tx.Exec(ctx, fmt.Sprintf(`
				insert into public.%s (workspace_id, %s, application_id, assigned_by)
				values ($1, $2, $3, $4)
				on conflict (%s, application_id) do nothing`, table, column, column),
				workspaceID, userID, appID, assignedBy); err != nil {
				return fmt.Errorf("assign applications: %w", err)
			}
		}
	}

	return tx.Commit(ctx)
}

// PICApplications lists the applications one PIC holds.
//
// Used as the default for a Freelance they create: a PIC cannot grant reach
// they do not have themselves, and starting the new person on the PIC's own
// applications is both the safe bound and the useful default.
func (r *Repo) PICApplications(ctx context.Context, picUserID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := r.pool.Query(ctx,
		`select application_id from public.pic_application_assignments where pic_user_id = $1`,
		picUserID)
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

// MemberPlacement is the minimum needed to decide whether the caller may manage
// somebody: their role and who they report to.
type MemberPlacement struct {
	Role      string
	PICUserID *uuid.UUID
}

// PlacementOf loads one member's role and PIC.
func (r *Repo) PlacementOf(ctx context.Context, workspaceID, userID uuid.UUID) (*MemberPlacement, error) {
	var p MemberPlacement
	err := r.pool.QueryRow(ctx, `
		select coalesce(ra.role::text, ''), fp.pic_user_id
		  from public.users u
		  left join public.role_assignments ra
		         on ra.user_id = u.id and ra.workspace_id = u.workspace_id
		  left join public.freelancer_pic_assignments fp on fp.freelancer_user_id = u.id
		 where u.id = $1 and u.workspace_id = $2`, userID, workspaceID).Scan(&p.Role, &p.PICUserID)
	if err != nil {
		return nil, mapErr(err)
	}
	return &p, nil
}

// SetMemberActive turns a member's access on or off.
//
// Deactivating clears the assignments that grant visibility but keeps the
// profile: messages they sent still point at it through sent_by, and last
// month's report has to keep being able to name them. A deleted person would
// leave that history attributed to nobody.
func (r *Repo) SetMemberActive(ctx context.Context, workspaceID, userID uuid.UUID, active bool) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx, `
		update public.users
		   set is_active = $3,
		       deactivated_at = case when $3 then null else now() end
		 where id = $1 and workspace_id = $2`, userID, workspaceID, active)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}

	if _, err := tx.Exec(ctx,
		`update public.role_assignments set is_active = $3
		  where user_id = $1 and workspace_id = $2`, userID, workspaceID, active); err != nil {
		return err
	}

	if !active {
		// Application and PIC links go with the access they granted. Leaving
		// them behind is how a deactivated account keeps appearing in the
		// filter rows and the rota long after the person has left.
		for _, q := range []string{
			`delete from public.pic_application_assignments where pic_user_id = $1`,
			`delete from public.freelancer_application_assignments where freelancer_user_id = $1`,
			`delete from public.freelancer_pic_assignments where freelancer_user_id = $1 or pic_user_id = $1`,
		} {
			if _, err := tx.Exec(ctx, q, userID); err != nil {
				return err
			}
		}
	}

	return tx.Commit(ctx)
}

// AdminNames resolves a set of user IDs to display names in one round trip.
func (r *Repo) AdminNames(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]string, error) {
	out := map[uuid.UUID]string{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := r.pool.Query(ctx,
		`select id, coalesce(nullif(full_name, ''), email) from public.users where id = any($1)`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		out[id] = name
	}
	return out, rows.Err()
}
