package httpapi

import (
	"net/http"
	"slices"
	"strings"

	"github.com/gorilla/websocket"
)

// handleWebSocket upgrades the connection and subscribes it to the caller's
// workspace feed.
//
// Browsers cannot set an Authorization header on a WebSocket handshake, so the
// access token arrives as the `token` query parameter. The origin is checked
// against ALLOWED_ORIGINS because a WebSocket handshake is not subject to CORS.
func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	token := bearerFromRequest(r)
	if token == "" {
		writeError(w, http.StatusUnauthorized, "unauthenticated", "Token tidak ditemukan")
		return
	}

	claims, err := s.verifier.Verify(r.Context(), token)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthenticated", "Token tidak valid")
		return
	}

	user, err := s.repo.EnsureUser(r.Context(), claims.UserID, claims.Email, "")
	if err != nil {
		writeAppError(w, err)
		return
	}

	upgrader := websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 4096,
		CheckOrigin: func(req *http.Request) bool {
			origin := req.Header.Get("Origin")
			if origin == "" {
				return true // non-browser client (curl, tests)
			}
			return slices.Contains(s.cfg.AllowedOrigins, origin)
		},
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		s.log.Debug("websocket upgrade failed", "err", err)
		return
	}

	// The name other browsers will see against "sedang dibuka oleh": the same
	// one the sidebar shows this person, so they recognise themselves in it.
	name := user.Email
	if user.FullName != nil && strings.TrimSpace(*user.FullName) != "" {
		name = strings.TrimSpace(*user.FullName)
	}
	s.hub.Register(conn, user.WorkspaceID, user.ID, name)
}
