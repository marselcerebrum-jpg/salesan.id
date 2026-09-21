package httpapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

type editMessageRequest struct {
	// Text replaces the body of a plain message, or the caption of a photo,
	// video or document — WhatsApp treats both as the message's content.
	Text string `json:"text"`
}

func (s *Server) handleEditMessage(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	messageID, ok := parseUUIDParam(w, chi.URLParam(r, "id"), "message_id")
	if !ok {
		return
	}

	var req editMessageRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	msg, err := s.manager.EditMessage(r.Context(), user.WorkspaceID, messageID, req.Text)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"message": msg})
}

// handleDeleteMessage removes a message, for everyone or for this side only.
//
// The scope is explicit in the query string rather than inferred, because the
// two are not variations of one action: one reaches into the other party's
// phone, the other does not.
func (s *Server) handleDeleteMessage(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	messageID, ok := parseUUIDParam(w, chi.URLParam(r, "id"), "message_id")
	if !ok {
		return
	}

	switch r.URL.Query().Get("scope") {
	case "everyone":
		msg, err := s.manager.RevokeMessage(r.Context(), user.WorkspaceID, messageID)
		if err != nil {
			writeAppError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"scope": "everyone", "message": msg})

	case "me", "":
		_, pushed, err := s.manager.HideMessage(r.Context(), user.WorkspaceID, messageID)
		if err != nil {
			writeAppError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"scope":   "me",
			"deleted": true,
			// False when the phone could not be told — the message is gone from
			// here either way, and the UI says so rather than implying more.
			"pushed_to_phone": pushed,
		})

	default:
		writeError(w, http.StatusBadRequest, "invalid_scope",
			`scope harus "everyone" atau "me"`)
	}
}
