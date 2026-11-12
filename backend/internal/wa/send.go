package wa

import (
	"context"
	"errors"
	"fmt"
	"strings"
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

	resp, sendErr := s.client.SendMessage(ctx, chatJID, proto.Clone(payload).(*waE2E.Message),
		whatsmeow.SendRequestExtra{ID: msgID})

	if sendErr != nil {
		if transientSendError(sendErr) {
			// Left pending and sent again in the background; see retryText.
			s.log.Warn("send hit a passing fault; retrying in the background",
				"message_id", msg.ID, "err", sendErr)
			go m.retryText(workspaceID, conv, msg.ID, chatJID, payload, msgID, sendErr)
			return msg, nil
		}
		return m.finishFailed(ctx, workspaceID, conv, msg, sendErr), fmt.Errorf("send message: %w", sendErr)
	}
	return m.finishSent(ctx, workspaceID, conv, msg, resp.Timestamp), nil
}

// sendRetryWaits is the pause before each background retry of a text message
// that failed on a passing fault: about eight minutes in all, which outlasts
// the three-to-four minute drops seen on 12 Nov and stops well short of a reply
// arriving so late it reads as out of place.
var sendRetryWaits = []time.Duration{
	15 * time.Second, 30 * time.Second, time.Minute, 2 * time.Minute, 4 * time.Minute,
}

// transientSendError reports whether a failed send is worth trying again
// unchanged.
//
// Timeouts and dropped connections are: on 12 Nov four numbers lost WhatsApp
// for three minutes and a reply sent in that window failed with "info query
// timed out", although the same send a minute later would have gone through.
// A refusal is not — a blocked recipient, an unknown number, a group this
// number has left — and retrying those only delays the error the admin needs.
func transientSendError(err error) bool {
	var disconnected *whatsmeow.DisconnectedError
	if errors.Is(err, whatsmeow.ErrIQTimedOut) ||
		errors.Is(err, whatsmeow.ErrNotConnected) ||
		errors.As(err, &disconnected) ||
		errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, context.Canceled) {
		return true
	}
	// Some of these arrive flattened into a string by whatsmeow or the
	// database driver, so the sentinels above cannot see them.
	text := err.Error()
	return strings.Contains(text, "deadline exceeded") ||
		strings.Contains(text, "timed out") ||
		strings.Contains(text, "bad connection")
}

// retryText sends a text message again after a passing fault.
//
// The same WhatsApp message ID every time, so a first attempt that did reach
// WhatsApp before its answer was lost is not delivered as a second message.
// The row stays pending throughout; the admin sees a clock rather than an
// error, and only a fault that outlasts every retry, or one that turns out to
// be a refusal, is recorded as a failure.
func (m *Manager) retryText(
	workspaceID uuid.UUID,
	conv *models.Conversation,
	messageID uuid.UUID,
	chatJID types.JID,
	payload *waE2E.Message,
	msgID types.MessageID,
	lastErr error,
) {
	for _, wait := range sendRetryWaits {
		select {
		case <-m.rootCtx.Done():
			// Shutting down: a pending row nobody will finish would show a
			// clock forever, so it is closed as failed instead.
			m.failRetry(workspaceID, conv, messageID, lastErr)
			return
		case <-time.After(wait):
		}

		s, ok := m.Session(conv.AccountID)
		if !ok || !s.IsConnected() {
			lastErr = ErrNotConnected
			continue
		}

		ctx, cancel := context.WithTimeout(m.rootCtx, time.Minute)
		s.sendMu.Lock()
		resp, err := s.client.SendMessage(ctx, chatJID, proto.Clone(payload).(*waE2E.Message),
			whatsmeow.SendRequestExtra{ID: msgID})
		s.lastSendAt = time.Now()
		s.sendMu.Unlock()

		if err == nil {
			if msg, getErr := m.repo.GetMessageByID(ctx, workspaceID, messageID); getErr == nil {
				m.finishSent(ctx, workspaceID, conv, msg, resp.Timestamp)
			}
			cancel()
			s.log.Info("message sent on retry", "message_id", messageID)
			return
		}
		cancel()
		lastErr = err
		s.log.Warn("send retry failed", "message_id", messageID, "err", err)
		if !transientSendError(err) {
			break
		}
	}
	m.failRetry(workspaceID, conv, messageID, lastErr)
}

// failRetry records a background retry's final failure.
func (m *Manager) failRetry(workspaceID uuid.UUID, conv *models.Conversation, messageID uuid.UUID, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	msg, getErr := m.repo.GetMessageByID(ctx, workspaceID, messageID)
	if getErr != nil {
		m.log.Error("record send failure after retries", "message_id", messageID, "err", getErr)
		return
	}
	m.finishFailed(ctx, workspaceID, conv, msg, err)
}

// finishFailed moves an outgoing message to failed and tells the browser.
func (m *Manager) finishFailed(
	ctx context.Context,
	workspaceID uuid.UUID,
	conv *models.Conversation,
	msg *models.Message,
	sendErr error,
) *models.Message {
	detail := sendErr.Error()
	failed, err := m.repo.SetMessageOutcome(ctx, msg.ID, models.MessageStatusFailed, nil, &detail)
	if err != nil {
		m.log.Error("record send failure", "account_id", conv.AccountID, "err", err)
	} else {
		msg = failed
	}
	m.broadcastMessageStatus(workspaceID, conv.AccountID, conv.ID, msg)
	return msg
}

// finishSent moves an outgoing message to sent and brings everything that
// depends on it up to date.
func (m *Manager) finishSent(
	ctx context.Context,
	workspaceID uuid.UUID,
	conv *models.Conversation,
	msg *models.Message,
	sentAt time.Time,
) *models.Message {
	updated, err := m.repo.SetMessageOutcome(ctx, msg.ID, models.MessageStatusSent, &sentAt, nil)
	if err != nil {
		return msg // it went out; the receipt handler will correct the row
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
	return updated
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
