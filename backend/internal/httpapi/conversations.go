package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/salesan/omnichannel/backend/internal/models"
	"github.com/salesan/omnichannel/backend/internal/realtime"
	"github.com/salesan/omnichannel/backend/internal/repository"
	"github.com/salesan/omnichannel/backend/internal/wa"
)

func (s *Server) handleListConversations(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	accountID, ok := parseUUIDParam(w, chi.URLParam(r, "id"), "account_id")
	if !ok {
		return
	}
	if _, err := s.repo.GetAccount(r.Context(), user.WorkspaceID, accountID); err != nil {
		writeAppError(w, err)
		return
	}

	filter := models.ConversationFilter{
		AccountID:    accountID,
		Search:       strings.TrimSpace(r.URL.Query().Get("search")),
		Type:         oneOf(r.URL.Query().Get("type"), models.ConversationTypePersonal, models.ConversationTypeGroup),
		Status:       oneOf(r.URL.Query().Get("status"), models.ConversationStatusNew, models.ConversationStatusInProgress, models.ConversationStatusDone),
		UnreadOnly:   queryBool(r, "unread"),
		MentionsOnly: queryBool(r, "mentions"),
		LabelID:      queryUUID(r, "label_id"),
		Limit:        queryInt(r, "limit", 100),
		Offset:       queryInt(r, "offset", 0),
	}

	conversations, err := s.repo.ListConversations(r.Context(), user.WorkspaceID, filter)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"conversations": conversations})
}

func (s *Server) handleConversationCounts(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	accountID, ok := parseUUIDParam(w, chi.URLParam(r, "id"), "account_id")
	if !ok {
		return
	}
	counts, err := s.repo.CountConversations(r.Context(), user.WorkspaceID, accountID)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, counts)
}

// handleGroupMembers lists a group's participants with their resolved names.
func (s *Server) handleGroupMembers(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	id, ok := parseUUIDParam(w, chi.URLParam(r, "id"), "conversation_id")
	if !ok {
		return
	}
	if _, err := s.repo.GetConversation(r.Context(), user.WorkspaceID, id); err != nil {
		writeAppError(w, err)
		return
	}

	members, err := s.repo.GroupMembers(r.Context(), user.WorkspaceID, id)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"members": members})
}

// handleFirstMention points the browser at the earliest mention it has not
// looked at, so opening a chat from the mention filter lands on the message
// rather than at the bottom of a busy group.
func (s *Server) handleFirstMention(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	id, ok := parseUUIDParam(w, chi.URLParam(r, "id"), "conversation_id")
	if !ok {
		return
	}

	messageID, err := s.repo.FirstUnseenMention(r.Context(), user.WorkspaceID, id)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"message_id": messageID})
}

func (s *Server) handleGetConversation(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	id, ok := parseUUIDParam(w, chi.URLParam(r, "id"), "conversation_id")
	if !ok {
		return
	}
	conv, err := s.repo.GetConversation(r.Context(), user.WorkspaceID, id)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, conv)
}

// handleDeleteConversation removes a thread from this inbox.
//
// Local only. WhatsApp keeps its own copy on the phone, so this is the operator
// tidying their workspace rather than deleting anyone's messages — and a new
// message in the same chat brings the thread straight back.
//
// `?clear=true` empties the history but keeps the thread in the list, matching
// WhatsApp's "clear chat" as opposed to "delete chat".
func (s *Server) handleDeleteConversation(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	id, ok := parseUUIDParam(w, chi.URLParam(r, "id"), "conversation_id")
	if !ok {
		return
	}
	clearOnly := queryBool(r, "clear")

	var (
		keys []string
		err  error
	)
	if clearOnly {
		keys, err = s.repo.ClearConversationMessages(r.Context(), user.WorkspaceID, id)
	} else {
		keys, err = s.repo.DeleteConversation(r.Context(), user.WorkspaceID, id)
	}
	if err != nil {
		writeAppError(w, err)
		return
	}

	// The rows are gone; their files must go too, or the bucket keeps paying
	// for media nothing points at any more.
	s.manager.RemoveStoredMedia(r.Context(), keys)

	s.hub.Broadcast(user.WorkspaceID, realtime.EventConversationDeleted, map[string]any{
		"conversation_id": id,
		"cleared":         clearOnly,
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"deleted":       !clearOnly,
		"cleared":       clearOnly,
		"files_removed": len(keys),
	})
}

type updateConversationRequest struct {
	Status *string `json:"status"`
}

func (s *Server) handleUpdateConversation(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	id, ok := parseUUIDParam(w, chi.URLParam(r, "id"), "conversation_id")
	if !ok {
		return
	}

	var req updateConversationRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Status == nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "Tidak ada perubahan")
		return
	}
	status := oneOf(*req.Status,
		models.ConversationStatusNew, models.ConversationStatusInProgress, models.ConversationStatusDone)
	if status == "" {
		writeError(w, http.StatusBadRequest, "invalid_status", "Status tidak dikenal")
		return
	}

	conv, err := s.repo.SetConversationStatus(r.Context(), user.WorkspaceID, id, status)
	if err != nil {
		writeAppError(w, err)
		return
	}
	s.hub.Broadcast(user.WorkspaceID, realtime.EventConversationUpdate, conv)
	writeJSON(w, http.StatusOK, conv)
}

func (s *Server) handleMarkConversationRead(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	id, ok := parseUUIDParam(w, chi.URLParam(r, "id"), "conversation_id")
	if !ok {
		return
	}

	conv, err := s.repo.MarkConversationRead(r.Context(), user.WorkspaceID, id)
	if err != nil {
		writeAppError(w, err)
		return
	}

	// Opening the thread is what counts as having seen the mentions in it —
	// seeing the badge in the list is not the same as reading what was said.
	if n, err := s.repo.MarkMentionsSeen(r.Context(), user.WorkspaceID, id); err != nil {
		s.log.Warn("mark mentions seen", "conversation_id", id, "err", err)
	} else if n > 0 {
		if refreshed, err := s.repo.GetConversation(r.Context(), user.WorkspaceID, id); err == nil {
			conv = refreshed
		}
	}

	// Two different things reach the phone here, and both are needed:
	//
	//   - the read receipt tells the sender their message was seen;
	//   - the app-state mutation clears WhatsApp's own "marked unread" flag,
	//     which a receipt alone leaves standing.
	//
	// Both are best-effort: the local counter is already cleared, and an
	// offline account must not stop an operator from reading a thread.
	if s.cfg.AutoMarkRead {
		if err := s.manager.SendReadReceipt(r.Context(), user.WorkspaceID, id); err != nil &&
			!errors.Is(err, wa.ErrNotConnected) && !errors.Is(err, wa.ErrSessionNotFound) {
			s.log.Warn("send read receipt", "conversation_id", id, "err", err)
		}
	}
	if _, err := s.manager.SetChatReadState(r.Context(), user.WorkspaceID, id, true); err != nil {
		s.log.Warn("push read state", "conversation_id", id, "err", err)
	}

	s.hub.Broadcast(user.WorkspaceID, realtime.EventConversationUpdate, conv)
	writeJSON(w, http.StatusOK, conv)
}

// handleMarkConversationUnread raises WhatsApp's manual "unread" flag.
//
// Distinct from having unread messages: the operator is deliberately flagging a
// thread to come back to, exactly as long-pressing a chat on the phone does.
// The phone is written first so a rejected change leaves nothing stored here.
func (s *Server) handleMarkConversationUnread(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	id, ok := parseUUIDParam(w, chi.URLParam(r, "id"), "conversation_id")
	if !ok {
		return
	}

	push, err := s.manager.SetChatReadState(r.Context(), user.WorkspaceID, id, false)
	if err != nil {
		writeAppError(w, err)
		return
	}
	if !push.PushedToPhone {
		writeError(w, http.StatusConflict, "not_synced", push.Reason)
		return
	}

	conv, err := s.repo.SetMarkedUnread(r.Context(), user.WorkspaceID, id, true)
	if err != nil {
		writeAppError(w, err)
		return
	}

	s.hub.Broadcast(user.WorkspaceID, realtime.EventConversationUpdate, conv)
	writeJSON(w, http.StatusOK, map[string]any{
		"conversation":    conv,
		"pushed_to_phone": true,
	})
}

func (s *Server) handleListMessages(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	id, ok := parseUUIDParam(w, chi.URLParam(r, "id"), "conversation_id")
	if !ok {
		return
	}
	if _, err := s.repo.GetConversation(r.Context(), user.WorkspaceID, id); err != nil {
		writeAppError(w, err)
		return
	}

	messages, err := s.repo.ListMessages(r.Context(), user.WorkspaceID, repository.ListMessagesInput{
		ConversationID: id,
		Limit:          queryInt(r, "limit", 50),
		Before:         queryTime(r, "before"),
	})
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"messages": messages})
}

type sendMessageRequest struct {
	Body string `json:"body"`
	// ReplyTo is the id of a message in this same conversation to quote.
	ReplyTo *uuid.UUID `json:"reply_to"`
}

type forwardRequest struct {
	// Conversations receiving a copy. Each is independent: one failure does
	// not stop the others, and the response says which did not go.
	Conversations []uuid.UUID `json:"conversations"`
}

// handleForwardMessage re-sends a message into other conversations.
func (s *Server) handleForwardMessage(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	messageID, ok := parseUUIDParam(w, chi.URLParam(r, "id"), "message_id")
	if !ok {
		return
	}

	var req forwardRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if len(req.Conversations) == 0 {
		writeError(w, http.StatusBadRequest, "no_target", "Pilih minimal satu percakapan tujuan")
		return
	}
	const maxTargets = 20
	if len(req.Conversations) > maxTargets {
		writeError(w, http.StatusBadRequest, "too_many_targets",
			"Maksimal 20 percakapan sekaligus")
		return
	}

	sent, failures, err := s.manager.ForwardMessage(
		r.Context(), user.WorkspaceID, messageID, req.Conversations, user.ID)
	if err != nil {
		writeAppError(w, err)
		return
	}

	failed := make([]map[string]string, 0, len(failures))
	for id, reason := range failures {
		failed = append(failed, map[string]string{"conversation_id": id.String(), "reason": reason})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"sent":   len(sent),
		"failed": failed,
	})
}

type privateReplyRequest struct {
	Body string `json:"body"`
}

// handlePrivateReplyTarget answers where a private reply would go.
//
// Called before the composer opens, so the operator can see whose chat they are
// about to land in and back out if it is the wrong person. It also creates the
// one-to-one thread when there is not one yet, which is the ordinary case: this
// exists precisely for somebody who has only ever written in the group.
func (s *Server) handlePrivateReplyTarget(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	messageID, ok := parseUUIDParam(w, chi.URLParam(r, "id"), "message_id")
	if !ok {
		return
	}

	target, _, err := s.manager.ResolvePrivateReply(r.Context(), user.WorkspaceID, messageID)
	if err != nil {
		writePrivateReplyError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, target)
}

// handlePrivateReply sends a one-to-one answer quoting a group message.
func (s *Server) handlePrivateReply(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	messageID, ok := parseUUIDParam(w, chi.URLParam(r, "id"), "message_id")
	if !ok {
		return
	}

	var req privateReplyRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Body) == "" {
		writeError(w, http.StatusBadRequest, "empty_message", "Pesan tidak boleh kosong")
		return
	}

	msg, target, err := s.manager.SendPrivateReply(
		r.Context(), user.WorkspaceID, messageID, req.Body, user.ID)
	if err != nil {
		// A persisted-but-failed message is still worth returning, so the thread
		// shows the failed bubble with a retry rather than losing the attempt.
		if msg != nil {
			writeJSON(w, http.StatusBadGateway, map[string]any{
				"error":   "send_failed",
				"message": err.Error(),
				"data":    msg,
				"target":  target,
			})
			return
		}
		writePrivateReplyError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"message": msg, "target": target})
}

// writePrivateReplyError turns the two refusals particular to this feature into
// something the operator can act on, and leaves everything else to the shared
// mapping.
func writePrivateReplyError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, wa.ErrNotAGroupMessage):
		writeError(w, http.StatusBadRequest, "not_a_group_message",
			"Balas pribadi hanya berlaku untuk pesan dari orang lain di dalam grup")
	case errors.Is(err, wa.ErrNoSender):
		writeError(w, http.StatusBadRequest, "no_sender",
			"WhatsApp tidak menyebutkan siapa pengirim pesan ini, jadi tidak ada yang bisa dibalas pribadi")
	default:
		writeAppError(w, err)
	}
}

func (s *Server) handleSendMessage(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	id, ok := parseUUIDParam(w, chi.URLParam(r, "id"), "conversation_id")
	if !ok {
		return
	}

	var req sendMessageRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Body = strings.TrimSpace(req.Body)
	if req.Body == "" {
		writeError(w, http.StatusBadRequest, "empty_message", "Pesan tidak boleh kosong")
		return
	}

	// A reply quotes a message from this same thread; quoting one from another
	// conversation would produce a quote the recipient cannot resolve.
	var replyTo *repository.MessageTarget
	if req.ReplyTo != nil {
		target, err := s.manager.ReplyTarget(r.Context(), user.WorkspaceID, id, *req.ReplyTo)
		if err != nil {
			writeAppError(w, err)
			return
		}
		replyTo = target
	}

	msg, err := s.manager.SendText(r.Context(), user.WorkspaceID, id, req.Body, user.ID, replyTo)
	if err != nil {
		// A persisted-but-failed message is still worth returning so the UI can
		// show it with a retry affordance.
		if msg != nil {
			writeJSON(w, http.StatusBadGateway, map[string]any{
				"error":   "send_failed",
				"message": err.Error(),
				"data":    msg,
			})
			return
		}
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, msg)
}

type assignLabelRequest struct {
	LabelID string `json:"label_id"`
}

func (s *Server) handleAssignLabel(w http.ResponseWriter, r *http.Request) {
	convID, ok := parseUUIDParam(w, chi.URLParam(r, "id"), "conversation_id")
	if !ok {
		return
	}

	var req assignLabelRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	labelID, ok := parseUUIDParam(w, req.LabelID, "label_id")
	if !ok {
		return
	}

	s.applyChatLabel(w, r, convID, labelID, true)
}

// applyChatLabel attaches or detaches a label on both sides.
//
// WhatsApp is written FIRST, and the local row only follows once the phone has
// accepted the change. Doing it the other way round would leave the database
// holding a tag the phone never got — exactly the silent divergence this
// feature exists to prevent. A rejected change therefore returns an error and
// changes nothing, which is what lets the browser roll its optimistic update
// back cleanly.
func (s *Server) applyChatLabel(
	w http.ResponseWriter,
	r *http.Request,
	convID, labelID uuid.UUID,
	labeled bool,
) {
	user := userFrom(r.Context())

	change, err := s.manager.PushChatLabel(r.Context(), user.WorkspaceID, convID, labelID, labeled)
	if err != nil {
		writeAppError(w, err)
		return
	}
	if !change.PushedToPhone {
		writeError(w, http.StatusConflict, "not_synced", change.Reason)
		return
	}

	if labeled {
		err = s.repo.AssignLabel(r.Context(), user.WorkspaceID, convID, labelID, user.ID)
	} else {
		err = s.repo.UnassignLabel(r.Context(), user.WorkspaceID, convID, labelID, user.ID)
	}
	if err != nil {
		writeAppError(w, err)
		return
	}

	conv, err := s.repo.GetConversation(r.Context(), user.WorkspaceID, convID)
	if err != nil {
		writeAppError(w, err)
		return
	}
	s.hub.Broadcast(user.WorkspaceID, realtime.EventConversationUpdate, conv)

	writeJSON(w, http.StatusOK, map[string]any{
		"conversation":    conv,
		"pushed_to_phone": true,
	})
}

func (s *Server) handleUnassignLabel(w http.ResponseWriter, r *http.Request) {
	convID, ok := parseUUIDParam(w, chi.URLParam(r, "id"), "conversation_id")
	if !ok {
		return
	}
	labelID, ok := parseUUIDParam(w, chi.URLParam(r, "labelID"), "label_id")
	if !ok {
		return
	}

	s.applyChatLabel(w, r, convID, labelID, false)
}
