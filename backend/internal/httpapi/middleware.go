package httpapi

import (
	"context"
	"net/http"
	"strings"

	"github.com/salesan/omnichannel/backend/internal/models"
)

type ctxKey string

const userCtxKey ctxKey = "salesan.user"

// withUser stores the resolved profile on the request context.
func withUser(ctx context.Context, u *models.User) context.Context {
	return context.WithValue(ctx, userCtxKey, u)
}

// userFrom pulls the authenticated profile back out. It is only ever called
// from handlers mounted behind requireAuth, so the assertion cannot fail.
func userFrom(ctx context.Context) *models.User {
	u, _ := ctx.Value(userCtxKey).(*models.User)
	return u
}

// requireAuth verifies the Supabase access token and resolves the caller's
// workspace. Every downstream query is scoped by that workspace ID.
func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := bearerFromRequest(r)
		if token == "" {
			writeError(w, http.StatusUnauthorized, "unauthenticated", "Token tidak ditemukan")
			return
		}

		claims, err := s.verifier.Verify(r.Context(), token)
		if err != nil {
			s.log.Debug("token rejected", "err", err)
			writeError(w, http.StatusUnauthorized, "unauthenticated", "Token tidak valid atau kedaluwarsa")
			return
		}

		user, err := s.repo.EnsureUser(r.Context(), claims.UserID, claims.Email, "")
		if err != nil {
			writeAppError(w, err)
			return
		}

		next.ServeHTTP(w, r.WithContext(withUser(r.Context(), user)))
	})
}

// bearerFromRequest reads the token from the Authorization header, falling back
// to the `token` query parameter, which is the only option browsers have when
// opening a WebSocket.
func bearerFromRequest(r *http.Request) string {
	if h := r.Header.Get("Authorization"); h != "" {
		const prefix = "Bearer "
		if len(h) > len(prefix) && strings.EqualFold(h[:len(prefix)], prefix) {
			return strings.TrimSpace(h[len(prefix):])
		}
	}
	return strings.TrimSpace(r.URL.Query().Get("token"))
}
