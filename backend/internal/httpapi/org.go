package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/salesan/omnichannel/backend/internal/analytics"
	"github.com/salesan/omnichannel/backend/internal/auth"
	"github.com/salesan/omnichannel/backend/internal/models"
	"github.com/salesan/omnichannel/backend/internal/realtime"
	"github.com/salesan/omnichannel/backend/internal/repository"
)

// handleListOrgMembers returns the workspace's operational hierarchy.
//
// Readable by every member: a Freelance needs to know who their PIC is, and a
// PIC needs to see their own team. What is restricted is changing it.
func (s *Server) handleListOrgMembers(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	sc, ok := s.scopeFor(w, r)
	if !ok {
		return
	}
	members, err := s.repo.ListOrgMembers(r.Context(), user.WorkspaceID)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"members": members,
		"scope":   scopePayload(sc),
		// Told to the interface rather than discovered by it failing: without a
		// service-role key the form cannot work, and a button that always
		// errors is worse than one that explains why it is not there.
		"can_create_accounts": s.admin.Enabled() && len(sc.CreatableRoles()) > 0,
		// Which roles this caller may hand out, so the form offers exactly
		// those. A PIC sees only Freelance.
		"creatable_roles": sc.CreatableRoles(),
		"admin_available": s.admin.Enabled(),
	})
}

type createMemberRequest struct {
	Email    string `json:"email"`
	FullName string `json:"full_name"`
	Password string `json:"password"`
	Role     string `json:"role"`
}

// handleCreateMember creates a Supabase account and puts it in this workspace.
//
// Leader only, and only when a service-role key is configured: creating auth
// users is an administrative operation, and the key that permits it bypasses
// RLS entirely. It never leaves this process.
//
// The account is created confirmed, with the password the Leader chose, because
// the person receiving it is a colleague being set up in the same room — not a
// stranger who can be asked to check their inbox.
func (s *Server) handleCreateMember(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	sc, ok := s.scopeFor(w, r)
	if !ok {
		return
	}
	if len(sc.CreatableRoles()) == 0 {
		writeError(w, http.StatusForbidden, "forbidden",
			"Hanya Leader dan PIC yang dapat membuat akun")
		return
	}
	if !s.admin.Enabled() {
		writeError(w, http.StatusServiceUnavailable, "admin_unavailable",
			"Pembuatan akun butuh SUPABASE_SERVICE_ROLE_KEY di backend/.env. "+
				"Isi kunci itu lalu jalankan ulang server; peran anggota yang sudah ada tetap bisa diatur tanpa kunci ini.")
		return
	}

	var req createMemberRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	req.FullName = strings.TrimSpace(req.FullName)

	if !strings.Contains(req.Email, "@") || len(req.Email) < 5 {
		writeError(w, http.StatusBadRequest, "invalid_email", "Alamat email tidak valid")
		return
	}
	if req.FullName == "" {
		writeError(w, http.StatusBadRequest, "invalid_body", "Nama lengkap wajib diisi")
		return
	}
	// Supabase's own minimum is six; eight is the shortest that is not an
	// insult to the account it protects.
	if len([]rune(req.Password)) < 8 {
		writeError(w, http.StatusBadRequest, "weak_password", "Kata sandi minimal 8 karakter")
		return
	}
	switch req.Role {
	case models.RoleLeader, models.RolePIC, models.RoleFreelance:
	default:
		writeError(w, http.StatusBadRequest, "invalid_role",
			"Peran harus salah satu dari leader, pic, atau freelance")
		return
	}
	if !sc.CanCreateRole(req.Role) {
		writeError(w, http.StatusForbidden, "forbidden",
			"Sebagai PIC, Anda hanya dapat membuat akun Freelance di bawah Anda")
		return
	}

	// A Leader operates the whole workspace, so they get the workspace-level
	// admin role too. PIC and Freelance stay agents: their reach is decided by
	// the operational role and the assignments, not by workspace ownership.
	workspaceRole := "agent"
	if req.Role == models.RoleLeader {
		workspaceRole = "admin"
	}

	// Placement, decided here rather than left to a second request. A PIC's new
	// Freelance reports to them and starts on their applications: a PIC cannot
	// grant reach they do not have, and a Freelance with no applications opens
	// the product to an empty screen.
	placement := repository.AttachMemberInput{
		WorkspaceRole:   workspaceRole,
		OperationalRole: req.Role,
	}
	if sc.Role == models.RolePIC && req.Role == models.RoleFreelance {
		pic := user.ID
		placement.PICUserID = &pic
		apps, err := s.repo.PICApplications(r.Context(), user.ID)
		if err != nil {
			writeAppError(w, err)
			return
		}
		placement.ApplicationIDs = apps
	}

	userID, err := s.admin.CreateUser(r.Context(), auth.CreateUserInput{
		Email:         req.Email,
		Password:      req.Password,
		FullName:      req.FullName,
		WorkspaceID:   user.WorkspaceID,
		WorkspaceRole: workspaceRole,
	})
	if err != nil {
		switch {
		case errors.Is(err, auth.ErrEmailTaken):
			writeError(w, http.StatusConflict, "email_taken",
				"Email itu sudah terdaftar. Gunakan alamat lain, atau atur perannya dari daftar anggota jika orangnya sudah ada di workspace ini.")
		case errors.Is(err, auth.ErrWeakPassword):
			writeError(w, http.StatusBadRequest, "weak_password", err.Error())
		case errors.Is(err, auth.ErrAdminUnavailable):
			writeError(w, http.StatusServiceUnavailable, "admin_unavailable", err.Error())
		default:
			s.log.Error("create member", "email", req.Email, "err", err)
			writeError(w, http.StatusBadGateway, "create_failed", "Akun gagal dibuat: "+err.Error())
		}
		return
	}

	// The signup trigger has normally written the profile already; this makes
	// the operational role and the placement stick, and repairs the profile if
	// the trigger has not run.
	placement.UserID = userID
	placement.Email = req.Email
	placement.FullName = req.FullName
	if err := s.repo.AttachMemberFull(
		r.Context(), user.WorkspaceID, user.ID, placement,
	); err != nil {
		s.log.Error("attach member", "user_id", userID, "err", err)
		writeError(w, http.StatusInternalServerError, "attach_failed",
			"Akun dibuat di Supabase tetapi gagal ditautkan ke workspace: "+err.Error())
		return
	}

	s.recordOrgActivity(r, "member", userID, "member_created", map[string]any{
		"email": req.Email, "role": req.Role,
	})

	members, err := s.repo.ListOrgMembers(r.Context(), user.WorkspaceID)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"user_id": userID,
		"members": members,
	})
}

type activeRequest struct {
	IsActive bool `json:"is_active"`
}

// handleSetMemberActive turns a member's access on or off.
//
// Not a delete. The profile stays because messages point at it and last
// month's report has to keep being able to name them; what goes away is the
// access and the assignments that granted it.
func (s *Server) handleSetMemberActive(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	sc, ok := s.scopeFor(w, r)
	if !ok {
		return
	}
	targetID, ok := parseUUIDParam(w, chi.URLParam(r, "id"), "user_id")
	if !ok {
		return
	}
	if !s.mayManage(w, r, sc, targetID) {
		return
	}

	var req activeRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	// Locking yourself out is not a decision anybody means to make, and it
	// would leave a workspace with no one able to undo it.
	if targetID == user.ID && !req.IsActive {
		writeError(w, http.StatusBadRequest, "self_deactivate",
			"Anda tidak dapat menonaktifkan akun Anda sendiri")
		return
	}

	if err := s.repo.SetMemberActive(r.Context(), user.WorkspaceID, targetID, req.IsActive); err != nil {
		writeAppError(w, err)
		return
	}

	action := "member_deactivated"
	if req.IsActive {
		action = "member_reactivated"
	}
	s.recordOrgActivity(r, "member", targetID, action, nil)

	members, err := s.repo.ListOrgMembers(r.Context(), user.WorkspaceID)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"members": members})
}

// mayManage checks that the caller is allowed to change this member.
//
// A Leader manages anybody. A PIC manages the Freelance reporting to them, and
// nobody else — not another PIC's team, and not themselves: narrowing your own
// scope is the one mistake you cannot undo without asking somebody for help.
func (s *Server) mayManage(
	w http.ResponseWriter, r *http.Request, sc repository.Scope, targetID uuid.UUID,
) bool {
	user := userFrom(r.Context())

	placement, err := s.repo.PlacementOf(r.Context(), user.WorkspaceID, targetID)
	if err != nil {
		writeAppError(w, err)
		return false
	}
	if !sc.CanManageMember(targetID, placement.Role, placement.PICUserID) {
		writeError(w, http.StatusForbidden, "forbidden",
			"Anda hanya dapat mengelola Freelance yang berada di bawah Anda")
		return false
	}
	return true
}

type passwordRequest struct {
	Password string `json:"password"`
}

// handleSetMemberPassword resets a member's password.
//
// The same people who may manage the member may reset it: a Leader for anybody,
// a PIC for the Freelance under them — checked by mayManage, which also confirms
// the member belongs to this workspace. The rule mirrors creation: whoever may
// hand somebody an account may hand them a new key to it.
//
// The new password is never logged or returned, and the audit entry records
// only that it changed.
func (s *Server) handleSetMemberPassword(w http.ResponseWriter, r *http.Request) {
	sc, ok := s.scopeFor(w, r)
	if !ok {
		return
	}
	targetID, ok := parseUUIDParam(w, chi.URLParam(r, "id"), "user_id")
	if !ok {
		return
	}
	if !s.mayManage(w, r, sc, targetID) {
		return
	}
	if !s.admin.Enabled() {
		writeError(w, http.StatusServiceUnavailable, "admin_unavailable",
			"Mengganti kata sandi butuh SUPABASE_SERVICE_ROLE_KEY di backend/.env.")
		return
	}

	var req passwordRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	// The same floor as creation.
	if len([]rune(req.Password)) < 8 {
		writeError(w, http.StatusBadRequest, "weak_password", "Kata sandi minimal 8 karakter")
		return
	}

	if err := s.admin.SetPassword(r.Context(), targetID, req.Password); err != nil {
		switch {
		case errors.Is(err, auth.ErrWeakPassword):
			writeError(w, http.StatusBadRequest, "weak_password", err.Error())
		case errors.Is(err, auth.ErrAdminUnavailable):
			writeError(w, http.StatusServiceUnavailable, "admin_unavailable", err.Error())
		default:
			s.log.Error("set member password", "user_id", targetID, "err", err)
			writeError(w, http.StatusBadGateway, "update_failed", "Kata sandi gagal diganti: "+err.Error())
		}
		return
	}

	s.recordOrgActivity(r, "member", targetID, "password_reset", nil)
	writeJSON(w, http.StatusOK, map[string]any{"updated": true})
}

type roleRequest struct {
	Role string `json:"role"`
}

// handleSetOperationalRole assigns Leader / PIC / Freelance.
//
// Leader only. Letting a PIC promote themselves would make the whole hierarchy
// decorative.
func (s *Server) handleSetOperationalRole(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	sc, ok := s.scopeFor(w, r)
	if !ok {
		return
	}
	if !sc.IsLeader() {
		writeError(w, http.StatusForbidden, "forbidden", "Hanya Leader yang dapat mengubah peran")
		return
	}

	targetID, ok := parseUUIDParam(w, chi.URLParam(r, "id"), "user_id")
	if !ok {
		return
	}

	var req roleRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	switch req.Role {
	case models.RoleLeader, models.RolePIC, models.RoleFreelance:
	default:
		writeError(w, http.StatusBadRequest, "invalid_role",
			"Peran harus salah satu dari leader, pic, atau freelance")
		return
	}

	if err := s.repo.SetOperationalRole(
		r.Context(), user.WorkspaceID, targetID, user.ID, req.Role,
	); err != nil {
		writeAppError(w, err)
		return
	}

	s.recordOrgActivity(r, "role", targetID, "role_assigned", map[string]any{
		"role": req.Role, "user_id": targetID.String(),
	})

	members, err := s.repo.ListOrgMembers(r.Context(), user.WorkspaceID)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"members": members})
}

type assignmentRequest struct {
	ApplicationIDs []string `json:"application_ids"`
	PICUserID      *string  `json:"pic_user_id"`
}

// handleSetMemberAssignments replaces one member's applications and PIC.
func (s *Server) handleSetMemberAssignments(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	sc, ok := s.scopeFor(w, r)
	if !ok {
		return
	}
	targetID, ok := parseUUIDParam(w, chi.URLParam(r, "id"), "user_id")
	if !ok {
		return
	}
	if !s.mayManage(w, r, sc, targetID) {
		return
	}

	var req assignmentRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	in := repository.AssignmentInput{UserID: targetID}
	for _, raw := range req.ApplicationIDs {
		id, err := uuid.Parse(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_uuid", "ID aplikasi tidak valid")
			return
		}
		in.ApplicationIDs = append(in.ApplicationIDs, id)
	}
	if req.PICUserID != nil && *req.PICUserID != "" {
		id, err := uuid.Parse(*req.PICUserID)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_uuid", "ID PIC tidak valid")
			return
		}
		in.PICUserID = &id
	}

	// A PIC cannot grant reach they do not have. Checked here rather than left
	// to the RLS policy alone, because the API path runs as the service role
	// and would otherwise apply no check at all.
	if !sc.All {
		for _, appID := range in.ApplicationIDs {
			if !sc.CanSeeApplication(appID) {
				writeError(w, http.StatusForbidden, "forbidden",
					"Anda hanya dapat menugaskan aplikasi yang menjadi tanggung jawab Anda")
				return
			}
		}
		if in.PICUserID != nil && *in.PICUserID != user.ID {
			writeError(w, http.StatusForbidden, "forbidden",
				"Sebagai PIC, Anda hanya dapat menempatkan Freelance di bawah Anda sendiri")
			return
		}
	}

	if err := s.repo.SetMemberAssignments(r.Context(), user.WorkspaceID, user.ID, in); err != nil {
		if errors.Is(err, repository.ErrForbidden) {
			writeError(w, http.StatusBadRequest, "invalid_assignment", err.Error())
			return
		}
		writeAppError(w, err)
		return
	}

	s.recordOrgActivity(r, "role", targetID, "assignments_changed", map[string]any{
		"application_count": len(in.ApplicationIDs),
	})

	members, err := s.repo.ListOrgMembers(r.Context(), user.WorkspaceID)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"members": members})
}

// --- schedules ----------------------------------------------------------------

// handleListSchedules returns the shifts in a date range.
func (s *Server) handleListSchedules(w http.ResponseWriter, r *http.Request) {
	sc, ok := s.scopeFor(w, r)
	if !ok {
		return
	}

	f := repository.ScheduleFilter{}
	q := r.URL.Query()
	if raw := q.Get("from"); raw != "" {
		d, err := time.ParseInLocation("2006-01-02", raw, analytics.Jakarta)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_date", "Format tanggal harus YYYY-MM-DD")
			return
		}
		f.From = d
	}
	if raw := q.Get("to"); raw != "" {
		d, err := time.ParseInLocation("2006-01-02", raw, analytics.Jakarta)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_date", "Format tanggal harus YYYY-MM-DD")
			return
		}
		f.To = d
	}
	for key, dest := range map[string]**uuid.UUID{
		"user_id":        &f.UserID,
		"application_id": &f.ApplicationID,
		"account_id":     &f.AccountID,
		"pic_id":         &f.PICUserID,
	} {
		if raw := q.Get(key); raw != "" {
			id, err := uuid.Parse(raw)
			if err != nil {
				writeError(w, http.StatusBadRequest, "invalid_uuid", "Nilai "+key+" tidak valid")
				return
			}
			*dest = &id
		}
	}

	switch kind := q.Get("kind"); kind {
	case "", repository.ScheduleKindWeekly, repository.ScheduleKindDated:
		f.Kind = kind
	default:
		writeError(w, http.StatusBadRequest, "invalid_kind",
			"Jenis jadwal harus weekly atau dated")
		return
	}

	schedules, err := s.repo.ListSchedules(r.Context(), sc, f)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"schedules": schedules,
		"scope":     scopePayload(sc),
	})
}

type scheduleRequest struct {
	UserID        string  `json:"user_id"`
	ApplicationID *string `json:"application_id"`
	AccountID     *string `json:"account_id"`
	PICUserID     *string `json:"pic_user_id"`
	// Exactly one of these says when. WorkDate is a single day; Weekday (0 =
	// Sunday) is a weekly pattern that holds until it is changed.
	WorkDate      string  `json:"work_date"`
	Weekday       *int    `json:"weekday"`
	StartsAt      string  `json:"starts_at"`
	EndsAt        string  `json:"ends_at"`
	Timezone      string  `json:"timezone"`
	IsActive      *bool   `json:"is_active"`
	Note          *string `json:"note"`
}

// handleUpsertSchedule creates or updates a shift.
//
// Leader and PIC only: a Freelance does not write their own rota, and the
// per-hour figures on the Performa page are divided by exactly this number.
func (s *Server) handleUpsertSchedule(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	sc, ok := s.scopeFor(w, r)
	if !ok {
		return
	}
	if !sc.CanManageSchedules() {
		writeError(w, http.StatusForbidden, "forbidden", "Hanya Leader dan PIC yang dapat mengatur jadwal")
		return
	}

	var req scheduleRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	userID, err := uuid.Parse(req.UserID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_uuid", "ID admin tidak valid")
		return
	}
	if !sc.CanSeeAdmin(userID) {
		writeError(w, http.StatusForbidden, "forbidden", "Admin ini di luar wewenang akun Anda")
		return
	}

	// A row says either "this date" or "every one of these days". Both would be
	// two answers to one question, and neither leaves the shift unplaceable in
	// time — the database refuses both, and this is the readable refusal.
	var workDate time.Time
	switch {
	case req.Weekday != nil && req.WorkDate != "":
		writeError(w, http.StatusBadRequest, "invalid_body",
			"Isi tanggal atau hari dalam pekan, bukan keduanya")
		return
	case req.Weekday != nil:
		if *req.Weekday < 0 || *req.Weekday > 6 {
			writeError(w, http.StatusBadRequest, "invalid_body",
				"Hari harus 0 (Minggu) sampai 6 (Sabtu)")
			return
		}
		// Only a Leader sets the standing hours. A PIC may still write a single
		// dated shift for somebody in their team — covering a day, swapping a
		// shift — but the pattern those days sit against is the Leader's.
		if !sc.IsLeader() {
			writeError(w, http.StatusForbidden, "forbidden",
				"Hanya Leader yang dapat mengatur pola jam kerja mingguan")
			return
		}
	default:
		d, err := time.ParseInLocation("2006-01-02", req.WorkDate, analytics.Jakarta)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_date", "Format tanggal harus YYYY-MM-DD")
			return
		}
		workDate = d
	}
	if !validClock(req.StartsAt) || !validClock(req.EndsAt) {
		writeError(w, http.StatusBadRequest, "invalid_time", "Format jam harus HH:MM")
		return
	}
	if req.EndsAt <= req.StartsAt {
		// A shift ending before it starts is either a typo or an overnight
		// shift; the second needs two rows, and guessing which was meant would
		// silently record hours nobody worked.
		writeError(w, http.StatusBadRequest, "invalid_range",
			"Jam selesai harus setelah jam mulai. Untuk shift melewati tengah malam, buat dua jadwal.")
		return
	}

	in := repository.ScheduleInput{
		UserID:   userID,
		WorkDate: workDate,
		Weekday:  req.Weekday,
		StartsAt: req.StartsAt,
		EndsAt:   req.EndsAt,
		Timezone: req.Timezone,
		IsActive: true,
		Note:     req.Note,
	}
	if req.IsActive != nil {
		in.IsActive = *req.IsActive
	}
	for raw, dest := range map[*string]**uuid.UUID{
		req.ApplicationID: &in.ApplicationID,
		req.AccountID:     &in.AccountID,
		req.PICUserID:     &in.PICUserID,
	} {
		if raw == nil || *raw == "" {
			continue
		}
		id, err := uuid.Parse(*raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_uuid", "ID pada jadwal tidak valid")
			return
		}
		*dest = &id
	}
	if in.ApplicationID != nil && !sc.CanSeeApplication(*in.ApplicationID) {
		writeError(w, http.StatusForbidden, "forbidden", "Aplikasi ini di luar wewenang akun Anda")
		return
	}

	schedule, err := s.repo.UpsertSchedule(r.Context(), user.WorkspaceID, user.ID, in)
	if err != nil {
		writeAppError(w, err)
		return
	}

	s.recordOrgActivity(r, "schedule", schedule.ID, "schedule_saved", map[string]any{
		"work_date": schedule.WorkDate,
		"window":    schedule.StartsAt + "-" + schedule.EndsAt,
		"user_id":   schedule.UserID.String(),
	})
	s.hub.Broadcast(user.WorkspaceID, realtime.EventScheduleUpdated, schedule)

	writeJSON(w, http.StatusOK, map[string]any{"schedule": schedule})
}

// handleDeleteSchedule removes a shift.
func (s *Server) handleDeleteSchedule(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	sc, ok := s.scopeFor(w, r)
	if !ok {
		return
	}
	if !sc.CanManageSchedules() {
		writeError(w, http.StatusForbidden, "forbidden", "Hanya Leader dan PIC yang dapat mengatur jadwal")
		return
	}
	id, ok := parseUUIDParam(w, chi.URLParam(r, "id"), "schedule_id")
	if !ok {
		return
	}
	if err := s.repo.DeleteSchedule(r.Context(), user.WorkspaceID, id); err != nil {
		writeAppError(w, err)
		return
	}

	s.recordOrgActivity(r, "schedule", id, "schedule_deleted", nil)
	s.hub.Broadcast(user.WorkspaceID, realtime.EventScheduleUpdated, map[string]any{
		"id": id, "deleted": true,
	})
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

// validClock accepts HH:MM in 24-hour form.
func validClock(v string) bool {
	_, err := time.Parse("15:04", v)
	return err == nil
}

// recordOrgActivity appends an organisational change to the audit log.
//
// Best effort: the change itself has already been committed, and refusing the
// response now would tell the caller it failed when it did not. A lost line is
// repaired by nothing, which is why the key includes the exact moment — a
// retried request writes one row, not two.
func (s *Server) recordOrgActivity(
	r *http.Request, entityType string, entityID uuid.UUID, action string, detail map[string]any,
) {
	user := userFrom(r.Context())
	at := time.Now().UTC()

	if err := s.repo.RecordActivity(r.Context(), repository.ActivityInput{
		WorkspaceID: user.WorkspaceID,
		AdminID:     &user.ID,
		EntityType:  entityType,
		EntityID:    &entityID,
		Action:      action,
		Detail:      detail,
		OccurredAt:  at,
		EventKey:    fmt.Sprintf("%s:%s:%s:%d", entityType, entityID, action, at.UnixMilli()),
	}); err != nil {
		s.log.Warn("record org activity", "entity", entityType, "action", action, "err", err)
	}
}
