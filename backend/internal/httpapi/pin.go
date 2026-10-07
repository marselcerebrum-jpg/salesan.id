package httpapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// pinRequest says which way the pin is going.
//
// A body rather than two routes, because the two are the same operation to
// WhatsApp — one message type carrying PIN_FOR_ALL or UNPIN_FOR_ALL — and
// splitting them here would invent a distinction the protocol does not make.
type pinRequest struct {
	Pinned bool `json:"pinned"`
}

// handlePinMessage pins a message in its chat, or takes the pin off.
//
//	POST /messages/{id}/pin  {"pinned": true}
//
// Worth stating plainly, because the word "pin" suggests otherwise: this is not
// a private bookmark. WhatsApp offers only pin-for-everyone, so the customer
// sees it too, and in a group everyone sees who did it.
func (s *Server) handlePinMessage(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	messageID, ok := parseUUIDParam(w, chi.URLParam(r, "id"), "message_id")
	if !ok {
		return
	}

	var req pinRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	msg, err := s.manager.PinMessage(r.Context(), user.WorkspaceID, messageID, req.Pinned)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"message": msg})
}
