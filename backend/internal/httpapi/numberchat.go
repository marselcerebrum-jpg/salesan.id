package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/salesan/omnichannel/backend/internal/wa"
)

// handleResolveNumberChat opens the one-to-one thread for a number.
//
//	POST /accounts/{id}/chats/resolve   {"phone": "0812-3456-7890"}
//
// Pressed from a number the operator read inside somebody else's message. The
// browser hands over the digits exactly as they were written and nothing else:
// which thread that is, and whether it already existed, are answers only the
// server can give, and a browser that guessed either would be guessing which
// customer to write to.
//
// The account comes from the path and is already checked by requireAccountScope
// the way every other `/accounts/{id}` route is, so a number outside the
// caller's applications cannot be used to start a conversation from.
func (s *Server) handleResolveNumberChat(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	accountID, ok := parseUUIDParam(w, chi.URLParam(r, "id"), "account_id")
	if !ok {
		return
	}

	var req struct {
		Phone string `json:"phone"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	// Bounded before it reaches the normaliser: the field arrives from a page,
	// and a megabyte of digits is not a phone number.
	req.Phone = strings.TrimSpace(req.Phone)
	if req.Phone == "" || len(req.Phone) > 40 {
		writeError(w, http.StatusBadRequest, "invalid_request", "Nomor tidak dapat dibaca")
		return
	}

	target, err := s.manager.ResolveNumberChat(r.Context(), user.WorkspaceID, accountID, req.Phone)
	if err != nil {
		switch {
		case errors.Is(err, wa.ErrNumberUnreadable):
			writeError(w, http.StatusBadRequest, "invalid_phone",
				"Nomor ini tidak dapat dibaca sebagai nomor telepon")
		case errors.Is(err, wa.ErrNumberNotOnWhatsApp):
			writeError(w, http.StatusNotFound, "not_on_whatsapp",
				"Nomor ini tidak terdaftar di WhatsApp, jadi tidak ada percakapan yang bisa dibuka")
		default:
			writeAppError(w, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"target": target})
}
