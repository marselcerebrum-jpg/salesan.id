package httpapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

type reactRequest struct {
	// Emoji to place on the message. An empty string takes the reaction back,
	// which is how WhatsApp expresses it too — one call, both meanings.
	Emoji string `json:"emoji"`
}

func (s *Server) handleReactToMessage(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	messageID, ok := parseUUIDParam(w, chi.URLParam(r, "id"), "message_id")
	if !ok {
		return
	}

	var req reactRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	msg, err := s.manager.SendReaction(r.Context(), user.WorkspaceID, messageID, req.Emoji)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"message": msg})
}
