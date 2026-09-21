package httpapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// Scope guards for routes addressed by id.
//
// Every listing endpoint narrows to the caller's applications, but a listing is
// not the only door: /conversations/{id} reaches a thread without passing
// through any list, and /accounts/{id} reaches a number the same way. These
// middlewares stand in front of those doors.
//
// Written as middleware rather than as a line in each handler for one reason:
// the conversation group has fourteen routes under it, and a check repeated
// fourteen times is a check that will eventually be forgotten on the fifteenth.
//
// The refusal is a 403 with the ids never leaving the server, and it happens
// before any handler runs — so nothing is read, and nothing is sent to a
// browser that then has to pretend it did not receive it.

// requireConversationScope refuses a thread outside the caller's applications.
func (s *Server) requireConversationScope(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sc, ok := s.scopeFor(w, r)
		if !ok {
			return
		}
		id, ok := parseUUIDParam(w, chi.URLParam(r, "id"), "conversation_id")
		if !ok {
			return
		}

		allowed, err := s.repo.ConversationInScope(r.Context(), sc, id)
		if err != nil {
			writeAppError(w, err)
			return
		}
		if !allowed {
			s.log.Warn("blocked out-of-scope conversation access",
				"user_id", sc.UserID, "role", sc.Role, "conversation_id", id, "path", r.URL.Path)
			writeError(w, http.StatusForbidden, "forbidden",
				"Percakapan ini berada di luar aplikasi yang ditugaskan kepada akun Anda")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// requireAccountScope refuses a WhatsApp number outside the caller's
// applications.
//
// A number with no application at all is visible only to a Leader: it sits
// outside the hierarchy, and the person who resolves that is the one who hands
// out applications.
func (s *Server) requireAccountScope(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sc, ok := s.scopeFor(w, r)
		if !ok {
			return
		}
		id, ok := parseUUIDParam(w, chi.URLParam(r, "id"), "account_id")
		if !ok {
			return
		}

		allowed, err := s.repo.AccountInScope(r.Context(), sc, id)
		if err != nil {
			writeAppError(w, err)
			return
		}
		if !allowed {
			s.log.Warn("blocked out-of-scope account access",
				"user_id", sc.UserID, "role", sc.Role, "account_id", id, "path", r.URL.Path)
			writeError(w, http.StatusForbidden, "forbidden",
				"Nomor ini berada di luar aplikasi yang ditugaskan kepada akun Anda")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// requireApplicationScope refuses an application the caller is not assigned to.
func (s *Server) requireApplicationScope(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sc, ok := s.scopeFor(w, r)
		if !ok {
			return
		}
		id, ok := parseUUIDParam(w, chi.URLParam(r, "id"), "application_id")
		if !ok {
			return
		}
		if !sc.CanSeeApplication(id) {
			s.log.Warn("blocked out-of-scope application access",
				"user_id", sc.UserID, "role", sc.Role, "application_id", id, "path", r.URL.Path)
			writeError(w, http.StatusForbidden, "forbidden",
				"Aplikasi ini di luar wewenang akun Anda")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// requireMessageScope refuses a message whose conversation is out of scope.
//
// Resolved through the message rather than assumed from the id, because
// /messages/{id} carries no conversation in its path — and forwarding, editing
// or deleting somebody else's message is exactly the kind of thing an id in a
// URL should not be able to do.
func (s *Server) requireMessageScope(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sc, ok := s.scopeFor(w, r)
		if !ok {
			return
		}
		id, ok := parseUUIDParam(w, chi.URLParam(r, "id"), "message_id")
		if !ok {
			return
		}

		msg, err := s.repo.GetMessageByID(r.Context(), sc.WorkspaceID, id)
		if err != nil {
			writeAppError(w, err)
			return
		}
		allowed, err := s.repo.ConversationInScope(r.Context(), sc, msg.ConversationID)
		if err != nil {
			writeAppError(w, err)
			return
		}
		if !allowed {
			s.log.Warn("blocked out-of-scope message access",
				"user_id", sc.UserID, "role", sc.Role, "message_id", id, "path", r.URL.Path)
			writeError(w, http.StatusForbidden, "forbidden",
				"Pesan ini berada di luar aplikasi yang ditugaskan kepada akun Anda")
			return
		}
		next.ServeHTTP(w, r)
	})
}
