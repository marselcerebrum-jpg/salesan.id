package httpapi

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/salesan/omnichannel/backend/internal/models"
	"github.com/salesan/omnichannel/backend/internal/wa"
)

type groupMemberRequest struct {
	// JID of the participant to act on.
	MemberJID string `json:"member_jid"`
	// Action is promote, demote or remove. Spelled out rather than inferred
	// from the HTTP verb: removing someone from a group and demoting them are
	// different enough that the caller should have to say which.
	Action string `json:"action"`
}

func (s *Server) handleUpdateGroupMember(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	id, ok := parseUUIDParam(w, chi.URLParam(r, "id"), "conversation_id")
	if !ok {
		return
	}

	var req groupMemberRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	req.MemberJID = strings.TrimSpace(req.MemberJID)
	if req.MemberJID == "" {
		writeError(w, http.StatusBadRequest, "missing_member", "member_jid wajib diisi")
		return
	}

	var action wa.GroupMemberAction
	switch req.Action {
	case "promote":
		action = wa.GroupPromote
	case "demote":
		action = wa.GroupDemote
	case "remove":
		action = wa.GroupRemove
	default:
		writeError(w, http.StatusBadRequest, "invalid_action",
			`action harus "promote", "demote" atau "remove"`)
		return
	}

	members, err := s.manager.UpdateGroupMember(r.Context(), user.WorkspaceID, id, req.MemberJID, action)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"members": members})
}

type updateGroupRequest struct {
	// Both optional; whichever is present is changed.
	Name        *string `json:"name"`
	Description *string `json:"description"`
}

func (s *Server) handleUpdateGroup(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	id, ok := parseUUIDParam(w, chi.URLParam(r, "id"), "conversation_id")
	if !ok {
		return
	}

	var req updateGroupRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Name == nil && req.Description == nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "Tidak ada perubahan")
		return
	}

	// Applied in turn, each returning the refreshed conversation; the last one
	// wins, so the response always reflects both changes.
	var conv *models.Conversation
	if req.Name != nil {
		updated, err := s.manager.SetGroupName(r.Context(), user.WorkspaceID, id, *req.Name)
		if err != nil {
			writeAppError(w, err)
			return
		}
		conv = updated
	}
	if req.Description != nil {
		updated, err := s.manager.SetGroupDescription(r.Context(), user.WorkspaceID, id, *req.Description)
		if err != nil {
			writeAppError(w, err)
			return
		}
		conv = updated
	}

	writeJSON(w, http.StatusOK, conv)
}

// handleRefreshGroup re-reads a group's metadata and member list from WhatsApp.
//
// Its own endpoint because the member panel wants what is true now, not what
// the last background sync happened to record.
func (s *Server) handleRefreshGroup(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	id, ok := parseUUIDParam(w, chi.URLParam(r, "id"), "conversation_id")
	if !ok {
		return
	}

	if err := s.manager.RefreshGroup(r.Context(), user.WorkspaceID, id); err != nil {
		writeAppError(w, err)
		return
	}
	members, err := s.repo.GroupMembers(r.Context(), user.WorkspaceID, id)
	if err != nil {
		writeAppError(w, err)
		return
	}
	conv, err := s.repo.GetConversation(r.Context(), user.WorkspaceID, id)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"conversation": conv, "members": members})
}
