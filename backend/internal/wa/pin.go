package wa

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"

	"github.com/salesan/omnichannel/backend/internal/models"
)

// ErrInvalidPin reports a message WhatsApp would not let us pin.
var ErrInvalidPin = errors.New("wa: pesan ini tidak bisa disematkan")

// pinDuration is how long a pin lasts.
//
// WhatsApp pins for a chosen span rather than forever — twenty-four hours,
// seven days or thirty days — and the chat quietly unpins itself when the span
// runs out. Thirty days is the longest it offers, and the one that matches what
// a pin is for here: an operator pinning a price list or a running instruction
// wants it to outlive the week, and nobody is going to remember to renew it.
const pinDuration = 30 * 24 * time.Hour

// PinMessage pins a message in its chat, or takes the pin off.
//
// One call for both directions, as WhatsApp itself expresses it: the same
// message type carries PIN_FOR_ALL and UNPIN_FOR_ALL, so the caller says what
// it wants rather than choosing an operation.
//
// "For all" is the only kind WhatsApp offers here, and it is worth saying
// plainly: this is not a private bookmark. Everyone in the chat sees the pin,
// including the customer, and in a group everyone sees who pinned it.
func (m *Manager) PinMessage(
	ctx context.Context,
	workspaceID, messageID uuid.UUID,
	pin bool,
) (*models.Message, error) {
	target, err := m.repo.MessageTargetByID(ctx, workspaceID, messageID)
	if err != nil {
		return nil, err
	}
	if target.RevokedAt != nil {
		return nil, fmt.Errorf("%w: pesan sudah dihapus", ErrInvalidPin)
	}

	conv, err := m.repo.GetConversationByID(ctx, target.ConversationID)
	if err != nil {
		return nil, err
	}
	s, ok := m.Session(conv.AccountID)
	if !ok {
		return nil, ErrSessionNotFound
	}
	if !s.IsConnected() {
		return nil, ErrNotConnected
	}

	chatJID, err := types.ParseJID(conv.ChatJID)
	if err != nil {
		return nil, err
	}

	kind := waE2E.PinInChatMessage_UNPIN_FOR_ALL
	if pin {
		kind = waE2E.PinInChatMessage_PIN_FOR_ALL
	}

	now := time.Now().UTC()
	msg := &waE2E.Message{PinInChatMessage: &waE2E.PinInChatMessage{
		Key: &waCommon.MessageKey{
			RemoteJID: proto.String(chatJID.String()),
			FromMe:    proto.Bool(target.FromMe),
			ID:        proto.String(target.WAMessageID),
		},
		Type:              kind.Enum(),
		SenderTimestampMS: proto.Int64(now.UnixMilli()),
	}}

	if _, err := s.client.SendMessage(ctx, chatJID, msg); err != nil {
		return nil, fmt.Errorf("kirim sematan: %w", err)
	}

	// Written straight away rather than waiting for WhatsApp to echo it back.
	// It does not echo a pin to the device that sent one, so waiting would
	// leave the banner missing until somebody else pinned something.
	var until *time.Time
	var at *time.Time
	if pin {
		expiry := now.Add(pinDuration)
		until, at = &expiry, &now
	}
	if err := m.repo.SetMessagePinned(ctx, messageID, at, until); err != nil {
		return nil, fmt.Errorf("catat sematan: %w", err)
	}

	updated, err := m.repo.GetMessageByID(ctx, workspaceID, messageID)
	if err != nil {
		return nil, err
	}
	m.broadcastMessageStatus(workspaceID, conv.AccountID, conv.ID, updated)
	return updated, nil
}

// applyPinFromPhone records a pin made on another device.
//
// The pin belongs to the chat, not to the device that set it, so one made on
// the operator's phone has to reach the banner here — otherwise the two screens
// disagree about which message the chat is pointing at, and the one on the
// phone is right.
func (s *Session) applyPinFromPhone(ctx context.Context, pin *waE2E.PinInChatMessage, chatJID string) {
	key := pin.GetKey()
	waID := key.GetID()
	if waID == "" {
		return
	}

	target, err := s.mgr.repo.GetMessageByWAID(ctx, s.AccountID, waID)
	if err != nil {
		// Routine rather than exceptional: a pin can name a message older than
		// the window we keep, and there is nothing to point at then.
		s.log.Debug("pin names a message we do not hold", "wa_id", waID)
		return
	}

	now := time.Now().UTC()
	var at, until *time.Time
	if pin.GetType() == waE2E.PinInChatMessage_PIN_FOR_ALL {
		expiry := now.Add(pinDuration)
		at, until = &now, &expiry
	}

	if err := s.mgr.repo.SetMessagePinned(ctx, target.ID, at, until); err != nil {
		s.log.Warn("apply pin from phone", "err", err)
		return
	}
	s.log.Info("pin changed on phone",
		"chat", chatJID, "wa_id", waID, "pinned", at != nil)

	if updated, err := s.mgr.repo.GetMessageByID(ctx, s.WorkspaceID, target.ID); err == nil {
		s.mgr.broadcastMessageStatus(s.WorkspaceID, s.AccountID, target.ConversationID, updated)
	}
}
