package httpapi

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/salesan/omnichannel/backend/internal/realtime"
	"github.com/salesan/omnichannel/backend/internal/repository"
	"github.com/salesan/omnichannel/backend/internal/wa"
)

// handleListLabels returns the tags available to the caller.
//
//	GET /labels?account_id=...
//
// Labels belong to one WhatsApp account, because WhatsApp identifies them by a
// per-device numeric id. Without account_id the whole workspace is returned,
// which is only useful for administration — the inbox always scopes by account.
func (s *Server) handleListLabels(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())

	accountID := queryUUID(r, "account_id")
	if accountID != nil {
		if _, err := s.repo.GetAccount(r.Context(), user.WorkspaceID, *accountID); err != nil {
			writeAppError(w, err)
			return
		}
	}

	labels, err := s.repo.ListLabels(r.Context(), user.WorkspaceID, accountID)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"labels": labels})
}

type createLabelRequest struct {
	Name      string  `json:"name"`
	Color     string  `json:"color"`
	AccountID *string `json:"account_id"`
}

// handleCreateLabel creates a tag and, when it belongs to an account, defines
// it on WhatsApp so the phone shows the same label.
func (s *Server) handleCreateLabel(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())

	var req createLabelRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "invalid_body", "Nama label wajib diisi")
		return
	}

	var accountID *uuid.UUID
	if req.AccountID != nil && strings.TrimSpace(*req.AccountID) != "" {
		parsed, err := uuid.Parse(*req.AccountID)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_id", "account_id tidak valid")
			return
		}
		if _, err := s.repo.GetAccount(r.Context(), user.WorkspaceID, parsed); err != nil {
			writeAppError(w, err)
			return
		}
		accountID = &parsed
	}

	label, err := s.repo.CreateLabel(r.Context(), user.WorkspaceID, repository.CreateLabelInput{
		AccountID: accountID,
		Name:      req.Name,
		Color:     req.Color,
	})
	if err != nil {
		if strings.Contains(err.Error(), "uq_conversation_labels_manual_name") {
			writeError(w, http.StatusConflict, "duplicate_label", "Label dengan nama itu sudah ada")
			return
		}
		writeAppError(w, err)
		return
	}

	push := s.pushDefinition(r, label.ID, accountID, false)

	// Re-read: a successful push assigns the WhatsApp id, and returning the
	// pre-push row would tell the caller the label has no identity when it
	// just acquired one.
	if fresh, err := s.repo.GetLabel(r.Context(), user.WorkspaceID, label.ID); err == nil {
		label = fresh
	}
	// Recorded after the push, because a label only joins an account's history
	// once the phone has given it an identity there.
	s.recordLabelDefinition(r, label.ID, repository.LabelEventCreated, user.ID)
	s.broadcastLabels(r, accountID)

	writeJSON(w, http.StatusCreated, map[string]any{
		"label":           label,
		"pushed_to_phone": push.PushedToPhone,
		"reason":          push.Reason,
	})
}

type updateLabelRequest struct {
	Name  *string `json:"name"`
	Color *string `json:"color"`
}

// handleUpdateLabel renames or recolours a tag on both sides.
func (s *Server) handleUpdateLabel(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	id, ok := parseUUIDParam(w, chi.URLParam(r, "id"), "label_id")
	if !ok {
		return
	}

	var req updateLabelRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Name != nil {
		trimmed := strings.TrimSpace(*req.Name)
		if trimmed == "" {
			writeError(w, http.StatusBadRequest, "invalid_body", "Nama label tidak boleh kosong")
			return
		}
		req.Name = &trimmed
	}

	label, err := s.repo.UpdateLabel(r.Context(), user.WorkspaceID, id, repository.UpdateLabelInput{
		Name:  req.Name,
		Color: req.Color,
	})
	if err != nil {
		writeAppError(w, err)
		return
	}

	push := s.pushDefinition(r, label.ID, label.AccountID, false)
	s.recordLabelDefinition(r, label.ID, repository.LabelEventUpdated, user.ID)
	s.broadcastLabels(r, label.AccountID)

	writeJSON(w, http.StatusOK, map[string]any{
		"label":           label,
		"pushed_to_phone": push.PushedToPhone,
		"reason":          push.Reason,
	})
}

// handleDeleteLabel removes a tag here and on WhatsApp.
func (s *Server) handleDeleteLabel(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	id, ok := parseUUIDParam(w, chi.URLParam(r, "id"), "label_id")
	if !ok {
		return
	}

	// Read it before deleting: the push needs its account and WhatsApp id.
	label, err := s.repo.GetLabel(r.Context(), user.WorkspaceID, id)
	if err != nil {
		writeAppError(w, err)
		return
	}

	push := s.pushDefinition(r, label.ID, label.AccountID, true)

	// Recorded before the delete, while the label's name can still be read for
	// the snapshot the history keeps.
	s.recordLabelDefinition(r, label.ID, repository.LabelEventDeleted, user.ID)

	if err := s.repo.DeleteLabel(r.Context(), user.WorkspaceID, id); err != nil {
		writeAppError(w, err)
		return
	}
	s.broadcastLabels(r, label.AccountID)

	writeJSON(w, http.StatusOK, map[string]any{
		"deleted":         true,
		"pushed_to_phone": push.PushedToPhone,
		"reason":          push.Reason,
	})
}

// pushDefinition mirrors a label definition to WhatsApp, tolerating an account
// that is offline or a label that belongs to no account at all.
func (s *Server) pushDefinition(r *http.Request, labelID uuid.UUID, accountID *uuid.UUID, deleted bool) *wa.LabelPush {
	if accountID == nil {
		return &wa.LabelPush{
			Reason: "Label belum terikat ke akun WhatsApp — akan dikirim saat pertama dipasang ke chat.",
		}
	}
	user := userFrom(r.Context())

	push, err := s.manager.PushLabelDefinition(r.Context(), user.WorkspaceID, labelID, *accountID, deleted)
	if err != nil {
		s.log.Warn("push label definition", "label_id", labelID, "err", err)
		return &wa.LabelPush{Reason: err.Error()}
	}
	return push
}

// recordLabelDefinition appends a definition change to the label history.
//
// Best effort, and deliberately so: the label change itself has already
// happened on both sides, and refusing the request now would leave the caller
// believing it failed while the phone shows that it did not.
func (s *Server) recordLabelDefinition(r *http.Request, labelID uuid.UUID, eventType string, adminID uuid.UUID) {
	if err := s.repo.RecordLabelDefinitionEvent(
		r.Context(), labelID, eventType, repository.ChangeSourceWeb, &adminID,
	); err != nil {
		s.log.Warn("record label definition event", "label_id", labelID, "type", eventType, "err", err)
	}
}

// broadcastLabels pushes the refreshed label set so every open tab agrees.
func (s *Server) broadcastLabels(r *http.Request, accountID *uuid.UUID) {
	user := userFrom(r.Context())

	labels, err := s.repo.ListLabels(r.Context(), user.WorkspaceID, accountID)
	if err != nil {
		return
	}
	payload := map[string]any{"reason": "web", "labels": labels}
	if accountID != nil {
		payload["account_id"] = *accountID
	}
	s.hub.Broadcast(user.WorkspaceID, realtime.EventLabelsUpdated, payload)
}
