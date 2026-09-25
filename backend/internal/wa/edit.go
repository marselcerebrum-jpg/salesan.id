package wa

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.mau.fi/whatsmeow/appstate"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waSyncAction"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"github.com/salesan/omnichannel/backend/internal/media"
	"github.com/salesan/omnichannel/backend/internal/models"
	"github.com/salesan/omnichannel/backend/internal/realtime"
	"github.com/salesan/omnichannel/backend/internal/repository"
)

// Errors the HTTP layer maps onto status codes.
var (
	// ErrNotEditable covers everything WhatsApp will not let us change: a
	// message someone else sent, one already deleted, or one of a kind that
	// carries no text.
	ErrNotEditable = errors.New("wa: this message cannot be edited")
	// ErrNotRevocable means WhatsApp will not accept a delete-for-everyone.
	ErrNotRevocable = errors.New("wa: this message cannot be deleted for everyone")
)

// EditWindow is how long WhatsApp allows a message to be edited after sending.
// Past it the server rejects the edit, so it is refused here with a message
// that explains why rather than a generic failure from the wire.
const EditWindow = 15 * time.Minute

// EditMessage changes the text of a message already sent.
//
// For a plain message that is the body; for a photo or a document it is the
// caption. WhatsApp models both as "replace the content", so one call covers
// the two cases the operator thinks of as different.
func (m *Manager) EditMessage(
	ctx context.Context,
	workspaceID, messageID uuid.UUID,
	text string,
) (*models.Message, error) {
	target, err := m.repo.MessageTargetByID(ctx, workspaceID, messageID)
	if err != nil {
		return nil, err
	}
	if err := checkEditable(target); err != nil {
		return nil, err
	}

	text = media.TrimCaption(text)
	if text == "" && !target.HasAttachment {
		// Emptying a text message would leave a blank bubble. Deleting is the
		// action they want, and it is one menu item away.
		return nil, fmt.Errorf("%w: teks kosong — pakai hapus pesan", ErrNotEditable)
	}

	s, ok := m.Session(target.AccountID)
	if !ok {
		return nil, ErrSessionNotFound
	}
	if !s.IsConnected() {
		return nil, ErrNotConnected
	}
	chatJID, err := types.ParseJID(target.ChatJID)
	if err != nil {
		return nil, err
	}

	// The replacement has to be the same kind of message as the original, or
	// WhatsApp shows an empty bubble on the other side.
	replacement, err := editedContent(target, text)
	if err != nil {
		return nil, err
	}

	if _, err := s.client.SendMessage(ctx, chatJID,
		s.client.BuildEdit(chatJID, types.MessageID(target.WAMessageID), replacement)); err != nil {
		return nil, fmt.Errorf("kirim perubahan: %w", err)
	}

	body, caption := storedContent(target, text)
	updated, err := m.repo.EditMessageContent(ctx, target.AccountID, target.WAMessageID, body, caption, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	if updated == nil {
		return nil, repository.ErrNotFound
	}

	m.broadcastMessageStatus(workspaceID, target.AccountID, target.ConversationID, updated)
	m.refreshConversation(ctx, workspaceID, target.ConversationID)
	return updated, nil
}

// checkEditable reports why a message cannot be edited, or nil.
func checkEditable(t *repository.MessageTarget) error {
	switch {
	case !t.FromMe:
		return fmt.Errorf("%w: hanya pesan sendiri yang bisa diedit", ErrNotEditable)
	case t.RevokedAt != nil:
		return fmt.Errorf("%w: pesan sudah dihapus", ErrNotEditable)
	case time.Since(t.Timestamp) > EditWindow:
		return fmt.Errorf("%w: batas edit %d menit sudah lewat",
			ErrNotEditable, int(EditWindow.Minutes()))
	}

	switch t.Type {
	case "text", "image", "video", "document":
		return nil
	default:
		return fmt.Errorf("%w: jenis pesan ini tidak punya teks", ErrNotEditable)
	}
}

// editedContent builds the replacement WhatsApp expects.
//
// Media keeps its original payload and only its caption changes — which is why
// the message is rebuilt from what we stored rather than sent as bare text. A
// bare text replacement for a photo would blank the picture.
func editedContent(t *repository.MessageTarget, text string) (*waE2E.Message, error) {
	if !t.HasAttachment {
		return &waE2E.Message{Conversation: proto.String(text)}, nil
	}

	// The media itself is already on WhatsApp's servers and is not being
	// replaced, so the edit carries the caption alone. WhatsApp treats an edit
	// of a media message as a caption change.
	switch t.Type {
	case "image":
		return &waE2E.Message{ImageMessage: &waE2E.ImageMessage{Caption: proto.String(text)}}, nil
	case "video":
		return &waE2E.Message{VideoMessage: &waE2E.VideoMessage{Caption: proto.String(text)}}, nil
	case "document":
		return &waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{Caption: proto.String(text)}}, nil
	default:
		return nil, fmt.Errorf("%w: %s", ErrNotEditable, t.Type)
	}
}

// storedContent decides which column the new text belongs in.
func storedContent(t *repository.MessageTarget, text string) (body, caption *string) {
	if t.HasAttachment {
		// A document keeps its filename in the body; only the caption moves.
		return t.Body, strPtr(text)
	}
	return strPtr(text), nil
}

// --- delete for everyone -------------------------------------------------------

// RevokeMessage deletes a message for everyone in the chat.
//
// Own messages anywhere; anyone's message in a group where this account is an
// admin — which is exactly what WhatsApp allows. The stored admin flag only
// decides whether to try: the server is what actually authorises it, so a stale
// flag ends in a refusal the operator can read rather than a silent failure.
func (m *Manager) RevokeMessage(ctx context.Context, workspaceID, messageID uuid.UUID) (*models.Message, error) {
	target, err := m.repo.MessageTargetByID(ctx, workspaceID, messageID)
	if err != nil {
		return nil, err
	}
	if target.RevokedAt != nil {
		return nil, fmt.Errorf("%w: pesan sudah dihapus", ErrNotRevocable)
	}

	if !target.FromMe {
		conv, err := m.repo.GetConversationByID(ctx, target.ConversationID)
		if err != nil {
			return nil, err
		}
		if conv.Type != models.ConversationTypeGroup || !conv.SelfIsAdmin {
			return nil, fmt.Errorf(
				"%w: pesan orang lain hanya bisa dihapus oleh admin grup", ErrNotRevocable)
		}
	}

	s, ok := m.Session(target.AccountID)
	if !ok {
		return nil, ErrSessionNotFound
	}
	if !s.IsConnected() {
		return nil, ErrNotConnected
	}
	chatJID, err := types.ParseJID(target.ChatJID)
	if err != nil {
		return nil, err
	}

	sender := types.EmptyJID
	if target.SenderJID != "" {
		if parsed, err := types.ParseJID(target.SenderJID); err == nil {
			sender = parsed
		}
	}

	// WhatsApp only lets you revoke someone else's message as a group admin,
	// and rejects it otherwise. The rejection is surfaced rather than swallowed
	// so the operator is not left thinking it worked.
	if _, err := s.client.SendMessage(ctx, chatJID,
		s.client.BuildRevoke(chatJID, sender, types.MessageID(target.WAMessageID))); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNotRevocable, err)
	}

	updated, keys, err := m.repo.RevokeMessage(ctx, target.AccountID, target.WAMessageID, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	m.releaseAndRemove(ctx, keys, nil)
	if updated == nil {
		return nil, repository.ErrNotFound
	}

	m.broadcastMessageStatus(workspaceID, target.AccountID, target.ConversationID, updated)
	m.refreshConversation(ctx, workspaceID, target.ConversationID)
	return updated, nil
}

// --- delete for me -------------------------------------------------------------

// HideMessage removes a message from this inbox only.
//
// The phone is told through an app-state mutation, which is what makes the
// message disappear from WhatsApp on the operator's own devices too. That part
// is best effort: if it fails, the message is still gone from here, and saying
// so is more useful than refusing the whole action.
func (m *Manager) HideMessage(ctx context.Context, workspaceID, messageID uuid.UUID) (*models.Message, bool, error) {
	target, err := m.repo.MessageTargetByID(ctx, workspaceID, messageID)
	if err != nil {
		return nil, false, err
	}

	pushed := m.pushDeleteForMe(ctx, target)

	conversationID, keys, err := m.repo.HideMessage(ctx, workspaceID, messageID)
	if err != nil {
		return nil, pushed, err
	}
	m.releaseAndRemove(ctx, keys, nil)

	if err := m.repo.RefreshConversationHead(ctx, conversationID); err != nil {
		m.log.Warn("refresh head after hide", "conversation_id", conversationID, "err", err)
	}

	m.hub.Broadcast(workspaceID, realtime.EventMessageHidden, map[string]any{
		"account_id":      target.AccountID,
		"conversation_id": conversationID,
		"message_id":      messageID,
	})
	m.refreshConversation(ctx, workspaceID, conversationID)
	return nil, pushed, nil
}

// pushDeleteForMe sends WhatsApp's own "delete for me" mutation.
//
// whatsmeow has no builder for this one, but the app-state index and the action
// are both defined, so the patch is assembled here. Reported as a boolean
// rather than an error: the local deletion has already happened and is what the
// operator asked for.
func (m *Manager) pushDeleteForMe(ctx context.Context, t *repository.MessageTarget) bool {
	s, ok := m.Session(t.AccountID)
	if !ok || !s.IsConnected() {
		return false
	}
	chatJID, err := types.ParseJID(t.ChatJID)
	if err != nil {
		return false
	}

	fromMe := "0"
	if t.FromMe {
		fromMe = "1"
	}
	// The sender slot is "0" for a one-to-one chat, matching how WhatsApp
	// indexes these mutations; only a group names the participant.
	sender := "0"
	if chatJID.Server == types.GroupServer && t.SenderJID != "" {
		sender = t.SenderJID
	}

	patch := appstate.PatchInfo{
		Type: appstate.WAPatchRegularHigh,
		Mutations: []appstate.MutationInfo{{
			Index:   []string{appstate.IndexDeleteMessageForMe, chatJID.String(), t.WAMessageID, fromMe, sender},
			Version: 3,
			Value: &waSyncAction.SyncActionValue{
				DeleteMessageForMeAction: &waSyncAction.DeleteMessageForMeAction{
					DeleteMedia:      proto.Bool(true),
					MessageTimestamp: proto.Int64(t.Timestamp.UnixMilli()),
				},
			},
		}},
	}

	if err := s.client.SendAppState(ctx, patch); err != nil {
		s.log.Warn("push delete-for-me to phone", "wa_id", t.WAMessageID, "err", err)
		return false
	}
	return true
}

// refreshConversation pushes the thread's new head to the browser.
func (m *Manager) refreshConversation(ctx context.Context, workspaceID, conversationID uuid.UUID) {
	if conv, err := m.repo.GetConversationByID(ctx, conversationID); err == nil {
		m.hub.Broadcast(workspaceID, realtime.EventConversationUpdate, conv)
	}
	// Editing, revoking or hiding a message changes the timeline the reports
	// are derived from. A revoked message must stop counting as the reply that
	// closed somebody's SLA cycle, which only a recompute can undo.
	m.RefreshConversationMetrics(ctx, workspaceID, conversationID)
}

// --- incoming ------------------------------------------------------------------

// handleMessageEdit applies an edit made on the phone or by the other party.
func (s *Session) handleMessageEdit(ctx context.Context, evt *events.Message) {
	protocol := evt.Message.GetProtocolMessage()
	targetID := protocol.GetKey().GetID()
	if targetID == "" {
		return
	}

	content := extractContent(protocol.GetEditedMessage())
	if content.Skip {
		return
	}

	at := evt.Info.Timestamp
	if ms := protocol.GetTimestampMS(); ms > 0 {
		at = time.UnixMilli(ms)
	}

	updated, err := s.mgr.repo.EditMessageContent(
		ctx, s.AccountID, targetID, content.Body, content.Caption, at)
	if err != nil {
		s.log.Warn("apply incoming edit", "wa_id", targetID, "err", err)
		return
	}
	if updated == nil {
		return // the edited message is outside our window
	}

	s.log.Info("message edited", "wa_id", targetID, "from_me", evt.Info.IsFromMe)
	s.mgr.broadcastMessageStatus(s.WorkspaceID, s.AccountID, updated.ConversationID, updated)
	if err := s.mgr.repo.RefreshConversationHead(ctx, updated.ConversationID); err == nil {
		s.mgr.refreshConversation(ctx, s.WorkspaceID, updated.ConversationID)
	}
}

// handleMessageRevoke applies a delete-for-everyone from either side.
func (s *Session) handleMessageRevoke(ctx context.Context, evt *events.Message) {
	protocol := evt.Message.GetProtocolMessage()
	targetID := protocol.GetKey().GetID()
	if targetID == "" {
		return
	}

	updated, keys, err := s.mgr.repo.RevokeMessage(ctx, s.AccountID, targetID, evt.Info.Timestamp)
	if err != nil {
		s.log.Warn("apply incoming revoke", "wa_id", targetID, "err", err)
		return
	}
	s.mgr.releaseAndRemove(ctx, keys, nil)

	// A deleted Status may be one the Story scheduler published. Ending its
	// publication here is what keeps the Story report from going on claiming it
	// is live, and stops its viewer tally at the moment it came down.
	//
	// Attempted before the `updated == nil` guard: a Story older than the sync
	// window has no message row left to revoke, but its publication is still
	// sitting there saying "Tayang".
	if ended, err := s.mgr.repo.RevokeStoryPublication(
		ctx, s.AccountID, targetID, evt.Info.Timestamp); err != nil {
		s.log.Warn("end story publication after revoke", "wa_id", targetID, "err", err)
	} else if ended {
		s.log.Info("story deleted by its poster", "wa_id", targetID)
	}

	if updated == nil {
		return // outside our window
	}

	s.log.Info("message revoked", "wa_id", targetID, "from_me", evt.Info.IsFromMe)
	s.mgr.broadcastMessageStatus(s.WorkspaceID, s.AccountID, updated.ConversationID, updated)
	if err := s.mgr.repo.RefreshConversationHead(ctx, updated.ConversationID); err == nil {
		s.mgr.refreshConversation(ctx, s.WorkspaceID, updated.ConversationID)
	}
}
