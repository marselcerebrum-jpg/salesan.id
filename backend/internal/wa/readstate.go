package wa

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"go.mau.fi/whatsmeow/appstate"
	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"

	"github.com/salesan/omnichannel/backend/internal/repository"
)

// ReadStatePush reports whether a read/unread change reached WhatsApp.
type ReadStatePush struct {
	PushedToPhone bool   `json:"pushed_to_phone"`
	Reason        string `json:"reason,omitempty"`
}

// SetChatReadState marks a chat read or unread on WhatsApp.
//
// "Mark as unread" is a flag WhatsApp keeps per chat, separate from whether any
// message in it is actually unread — which is why it needs its own mutation
// rather than a read receipt. It lives in the regular_low app-state
// collection, the one that syncs normally on every account, so unlike labels
// this path does not depend on the fragile `regular` collection.
//
// WhatsApp is written before the local row: a flag stored here that the phone
// rejected would be exactly the silent divergence this feature exists to avoid.
func (m *Manager) SetChatReadState(
	ctx context.Context,
	workspaceID, conversationID uuid.UUID,
	read bool,
) (*ReadStatePush, error) {
	conv, err := m.repo.GetConversation(ctx, workspaceID, conversationID)
	if err != nil {
		return nil, err
	}

	s, ok := m.Session(conv.AccountID)
	if !ok || !s.IsConnected() {
		return &ReadStatePush{
			Reason: "Akun WhatsApp tidak terhubung — tanda ini baru tersimpan di sini, belum dikirim ke HP.",
		}, nil
	}

	anchor, err := m.repo.ChatAnchorFor(ctx, workspaceID, conversationID)
	if err != nil {
		return nil, err
	}
	chatJID, err := types.ParseJID(anchor.ChatJID)
	if err != nil {
		return nil, fmt.Errorf("invalid chat jid %q: %w", anchor.ChatJID, err)
	}

	patch := appstate.BuildMarkChatAsRead(chatJID, read, anchor.Timestamp, messageKey(anchor, chatJID))
	if err := s.client.SendAppState(ctx, patch); err != nil {
		s.log.Warn("push read state to phone", "chat", anchor.ChatJID, "read", read, "err", err)
		return &ReadStatePush{Reason: "WhatsApp menolak perubahan: " + err.Error()}, nil
	}

	s.log.Info("read state pushed to phone",
		"chat", anchor.ChatJID, "read", read, "anchor", anchor.WAMessageID)
	return &ReadStatePush{PushedToPhone: true}, nil
}

// messageKey builds the message reference WhatsApp expects alongside a
// read-state mutation, or nil when the thread has no stored message to point at.
func messageKey(anchor *repository.ChatAnchor, chatJID types.JID) *waCommon.MessageKey {
	if anchor.WAMessageID == "" {
		return nil
	}

	key := &waCommon.MessageKey{
		RemoteJID: proto.String(chatJID.String()),
		FromMe:    proto.Bool(anchor.FromMe),
		ID:        proto.String(anchor.WAMessageID),
	}
	// Group messages need the participant, so the phone can resolve which
	// member's message the range ends at.
	if chatJID.Server == types.GroupServer && anchor.SenderJID != "" {
		key.Participant = proto.String(anchor.SenderJID)
	}
	return key
}
