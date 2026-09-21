package httpapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

type createPollRequest struct {
	Name    string   `json:"name"`
	Options []string `json:"options"`
	// AllowMultiple lets a voter pick more than one option. WhatsApp expresses
	// this as a selectable count; the API takes the boolean because that is the
	// only choice the UI actually offers.
	AllowMultiple bool `json:"allow_multiple"`
}

func (s *Server) handleCreatePoll(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	conversationID, ok := parseUUIDParam(w, chi.URLParam(r, "id"), "conversation_id")
	if !ok {
		return
	}

	var req createPollRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	selectable := 1
	if req.AllowMultiple {
		selectable = 0 // 0 means "as many as you like"
	}

	msg, err := s.manager.SendPoll(
		r.Context(), user.WorkspaceID, conversationID,
		req.Name, req.Options, selectable, user.ID,
	)
	if err != nil {
		// A poll that reached the database but not WhatsApp still has a row, so
		// the bubble can show the failure instead of disappearing.
		if msg != nil {
			writeJSON(w, http.StatusOK, map[string]any{
				"message": msg,
				"error":   "send_failed",
				"detail":  err.Error(),
			})
			return
		}
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"message": msg})
}

type votePollRequest struct {
	// Options is the voter's complete selection, not a change to it. An empty
	// array clears the vote, which is what WhatsApp does when someone taps
	// their own answer off.
	Options []int `json:"options"`
}

func (s *Server) handleVotePoll(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	messageID, ok := parseUUIDParam(w, chi.URLParam(r, "id"), "message_id")
	if !ok {
		return
	}

	var req votePollRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	msg, err := s.manager.VotePoll(r.Context(), user.WorkspaceID, messageID, req.Options)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"message": msg})
}
