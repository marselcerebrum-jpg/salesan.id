package httpapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/salesan/omnichannel/backend/internal/models"
)

// Response-time targets.
//
// Reading is open to everyone in the workspace: a Freelance is measured
// against this number, and a rule somebody is judged by that they are not
// allowed to see is not a rule, it is a trap.
//
// Writing is narrower. The workspace default is a Leader's decision because
// it reaches every application; an override belongs to whoever holds that
// application. Both are checked here and again by RLS.

func (s *Server) handleListSLATargets(w http.ResponseWriter, r *http.Request) {
	sc, ok := s.scopeFor(w, r)
	if !ok {
		return
	}
	targets, err := s.repo.ListSLATargets(r.Context(), sc.WorkspaceID)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"targets": targets,
		// What applies when the workspace has set nothing at all, so the
		// screen can show the figure in force rather than an empty box that
		// implies nothing is being measured.
		"fallback_seconds": s.cfg.SLATargetSeconds,
		"fallback_business_hours": s.cfg.SLABusinessHours,
		"can_edit_default":        sc.All,
	})
}

func (s *Server) handleUpsertSLATarget(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	sc, ok := s.scopeFor(w, r)
	if !ok {
		return
	}
	if !canDefineVocabulary(sc) {
		writeError(w, http.StatusForbidden, "forbidden",
			"Hanya Leader dan PIC yang dapat mengatur target SLA")
		return
	}

	var req struct {
		ApplicationID *string `json:"application_id"`
		TargetSeconds int     `json:"target_seconds"`
		BusinessHours *bool   `json:"business_hours"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}

	// scopedApplication already refuses a workspace-wide write from a PIC and
	// an application out of their reach, which is exactly the rule here.
	appID, ok := scopedApplication(w, sc, req.ApplicationID)
	if !ok {
		return
	}

	// The same bounds the database enforces, checked here so the message says
	// what is wrong instead of surfacing a constraint name.
	if req.TargetSeconds < 60 || req.TargetSeconds > 86400 {
		writeError(w, http.StatusBadRequest, "invalid_target",
			"Target SLA harus antara 1 menit dan 24 jam")
		return
	}

	business := true
	if req.BusinessHours != nil {
		business = *req.BusinessHours
	}

	target, err := s.repo.UpsertSLATarget(r.Context(), sc.WorkspaceID, user.ID, models.SLATarget{
		ApplicationID: appID,
		TargetSeconds: req.TargetSeconds,
		BusinessHours: business,
	})
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"target": target})
}

func (s *Server) handleDeleteSLATarget(w http.ResponseWriter, r *http.Request) {
	sc, ok := s.scopeFor(w, r)
	if !ok {
		return
	}
	if !canDefineVocabulary(sc) {
		writeError(w, http.StatusForbidden, "forbidden",
			"Hanya Leader dan PIC yang dapat mengatur target SLA")
		return
	}
	id, ok := parseUUIDParam(w, chi.URLParam(r, "id"), "target_id")
	if !ok {
		return
	}

	// Read the owner before deleting, so a PIC reaching past their remit gets
	// a refusal that says so rather than a "not found" that tells them
	// nothing. The service-role path applies no RLS, so this is the check.
	appID, err := s.repo.SLATargetApplication(r.Context(), sc.WorkspaceID, id)
	if err != nil {
		writeAppError(w, err)
		return
	}
	if appID == nil {
		if !sc.All {
			writeError(w, http.StatusForbidden, "forbidden",
				"Target bawaan workspace hanya dapat diubah Leader")
			return
		}
	} else if !sc.CanSeeApplication(*appID) {
		writeError(w, http.StatusForbidden, "forbidden",
			"Aplikasi ini di luar tanggung jawab Anda")
		return
	}

	if _, err := s.repo.DeleteSLATarget(r.Context(), sc.WorkspaceID, id); err != nil {
		writeAppError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
