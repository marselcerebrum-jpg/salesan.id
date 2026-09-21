package wa

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"

	"github.com/salesan/omnichannel/backend/internal/models"
	"github.com/salesan/omnichannel/backend/internal/realtime"
	"github.com/salesan/omnichannel/backend/internal/repository"
)

// ErrEmptyMessage guards against sending blank text.
var ErrEmptyMessage = errors.New("wa: message body is empty")

// SendText sends a text message and records its lifecycle.
//
// The row is written as `pending` *before* the network call using a message ID
// generated up front, so the same (account_id, wa_message_id) idempotency key
// covers both the optimistic row and the echo WhatsApp sends back to us. The
// outcome then moves the row to `sent` or `failed`.
// replyTo is the message a new one is answering, or nil.
func (m *Manager) SendText(
	ctx context.Context,
	workspaceID, conversationID uuid.UUID,
	text string,
	sentBy uuid.UUID,
	replyTo *repository.MessageTarget,
) (*models.Message, error) {
	if text == "" {
		return nil, ErrEmptyMessage
	}

	conv, err := m.repo.GetConversation(ctx, workspaceID, conversationID)
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
		return nil, fmt.Errorf("invalid chat jid %q: %w", conv.ChatJID, err)
	}

	msgID := s.client.GenerateMessageID()
	senderJID := ""
	if own := s.client.Store.ID; own != nil {
		senderJID = own.ToNonAD().String()
	}

	var quotedID *string
	if replyTo != nil {
		quotedID = &replyTo.WAMessageID
	}

	msg, _, err := m.repo.InsertMessage(ctx, repository.InsertMessageInput{
		WorkspaceID:     workspaceID,
		AccountID:       conv.AccountID,
		ConversationID:  conv.ID,
		WAMessageID:     msgID,
		SenderJID:       strPtr(senderJID),
		FromMe:          true,
		Type:            "text",
		Body:            &text,
		QuotedMessageID: quotedID,
		Status:          models.MessageStatusPending,
		Timestamp:       time.Now().UTC(),
		SentBy:          &sentBy,
	})
	if err != nil {
		return nil, fmt.Errorf("persist outgoing message: %w", err)
	}
	if full, err := m.repo.GetMessageByID(ctx, workspaceID, msg.ID); err == nil {
		msg = full
	}

	m.hub.Broadcast(workspaceID, realtime.EventMessageNew, map[string]any{
		"account_id": conv.AccountID,
		"message":    msg,
	})

	// Read the thread on the way to answering it, then type. Both are visible
	// to the recipient and both are what a person at a phone produces — see
	// humanize.go for why that matters.
	m.readBeforeReplying(ctx, workspaceID, conv)
	release := s.beginSend(ctx, chatJID, text, types.ChatPresenceMediaText)
	defer release()

	// A reply carries the quote in an ExtendedTextMessage; a plain message is
	// a bare Conversation string, which is what WhatsApp itself sends and what
	// keeps the common case on the cheaper wire format.
	payload := &waE2E.Message{Conversation: proto.String(text)}
	if replyTo != nil {
		payload = &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{
			Text:        proto.String(text),
			ContextInfo: quoteContext(s, replyTo),
		}}
	}

	resp, sendErr := s.client.SendMessage(ctx, chatJID, payload,
		whatsmeow.SendRequestExtra{ID: msgID})

	if sendErr != nil {
		detail := sendErr.Error()
		failed, err := m.repo.SetMessageOutcome(ctx, msg.ID, models.MessageStatusFailed, nil, &detail)
		if err != nil {
			s.log.Error("record send failure", "err", err)
		} else {
			msg = failed
		}
		m.broadcastMessageStatus(workspaceID, conv.AccountID, conv.ID, msg)
		return msg, fmt.Errorf("send message: %w", sendErr)
	}

	sentAt := resp.Timestamp
	updated, err := m.repo.SetMessageOutcome(ctx, msg.ID, models.MessageStatusSent, &sentAt, nil)
	if err != nil {
		return msg, nil // it went out; the receipt handler will correct the row
	}
	// SetMessageOutcome returns the bare row. Re-reading it brings back the
	// quote preview, which the browser needs to draw the reply properly the
	// first time rather than only after a reload.
	if full, err := m.repo.GetMessageByID(ctx, workspaceID, updated.ID); err == nil {
		updated = full
	}

	m.broadcastMessageStatus(workspaceID, conv.AccountID, conv.ID, updated)
	if refreshed, err := m.repo.GetConversationByID(ctx, conv.ID); err == nil {
		m.hub.Broadcast(workspaceID, realtime.EventConversationUpdate, refreshed)
	}
	// A manual reply is what closes an SLA cycle and what a follow-up is made
	// of, so the report has to be rebuilt here as well as on the way in.
	m.RefreshConversationMetrics(ctx, workspaceID, conv.ID)
	return updated, nil
}

func (m *Manager) broadcastMessageStatus(workspaceID, accountID, conversationID uuid.UUID, msg *models.Message) {
	m.hub.Broadcast(workspaceID, realtime.EventMessageStatus, map[string]any{
		"account_id": accountID,
		"changes": []repository.MessageStatusChange{{
			ID:             msg.ID,
			ConversationID: conversationID,
			WAMessageID:    msg.WAMessageID,
			Status:         msg.Status,
		}},
		"message": msg,
	})
}

// SendReadReceipt tells WhatsApp that the operator has read a thread.
//
// Best-effort: a failure here never blocks marking the conversation read in our
// own database.
func (m *Manager) SendReadReceipt(ctx context.Context, workspaceID, conversationID uuid.UUID) error {
	conv, err := m.repo.GetConversation(ctx, workspaceID, conversationID)
	if err != nil {
		return err
	}
	s, ok := m.Session(conv.AccountID)
	if !ok || !s.IsConnected() {
		return ErrNotConnected
	}

	ids, senderJID, err := m.repo.RecentIncomingWAIDs(ctx, conversationID, 50)
	if err != nil || len(ids) == 0 {
		return err
	}

	chatJID, err := types.ParseJID(conv.ChatJID)
	if err != nil {
		return err
	}
	sender := types.EmptyJID
	if senderJID != "" {
		if parsed, err := types.ParseJID(senderJID); err == nil {
			sender = parsed
		}
	}

	msgIDs := make([]types.MessageID, 0, len(ids))
	for _, id := range ids {
		msgIDs = append(msgIDs, types.MessageID(id))
	}
	return s.client.MarkRead(ctx, msgIDs, time.Now(), chatJID, sender)
}
