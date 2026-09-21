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

// Replying privately to someone who wrote in a group.
//
// WhatsApp calls this "reply privately", and it is not the same thing as
// opening the person's chat and typing. The message goes to the one-to-one
// thread but carries a quote pointing back into the group, so the recipient
// sees which of forty messages is being answered. Without that context the
// reply arrives as a message from a number the customer may not recognise,
// about something they said an hour ago, with nothing to anchor it.
//
// The difference is entirely in the ContextInfo:
//
//	StanzaID      the group message being answered
//	Participant   who wrote it, which is also who we are writing to
//	RemoteJID     the group it was written in
//	QuotedMessage enough of the original for the quote bubble to render
//
// RemoteJID is what makes it a private reply rather than an ordinary quote.
// Left out, the recipient's client looks for the quoted id in the one-to-one
// thread, does not find it, and draws an empty box.

var (
	// ErrNotAGroupMessage covers a private reply asked for from a chat that is
	// not a group, where it would mean nothing.
	ErrNotAGroupMessage = errors.New("wa: private reply only applies to group messages")
	// ErrNoSender covers a group message with no identifiable author. Messages
	// sent from our own linked phone are the usual case: WhatsApp does not say
	// who held it.
	ErrNoSender = errors.New("wa: this message has no identifiable sender")
)

// PrivateReplyTarget describes where a private reply would go, without sending.
//
// Returned to the browser before it opens the composer so the operator can see
// whose chat they are about to land in. Guessing it client-side from the group
// member list would be a second answer to a question only the server can settle.
type PrivateReplyTarget struct {
	ConversationID uuid.UUID  `json:"conversation_id"`
	AccountID      uuid.UUID  `json:"account_id"`
	// ApplicationID completes the chat route, which is
	// /chat/{applicationId}/{accountId}. Resolved here rather than guessed by
	// the browser, because it is an authorization answer as much as a route.
	ApplicationID *uuid.UUID `json:"application_id"`
	Name          string     `json:"name"`
	PhoneNumber   string     `json:"phone_number"`
	// GroupName is where the quoted message was written.
	GroupName string `json:"group_name"`
}

// ResolvePrivateReply finds (or opens) the one-to-one thread with whoever wrote
// a group message.
//
// The thread is created if it does not exist yet, which is the whole point: the
// operator is answering somebody they have never had a private chat with. It is
// created empty and nothing is sent, so resolving is safe to call from a menu
// that the operator may then dismiss.
func (m *Manager) ResolvePrivateReply(
	ctx context.Context,
	workspaceID, messageID uuid.UUID,
) (*PrivateReplyTarget, *repository.MessageTarget, error) {
	target, err := m.repo.MessageTargetByID(ctx, workspaceID, messageID)
	if err != nil {
		return nil, nil, err
	}

	groupJID, err := types.ParseJID(target.ChatJID)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid chat jid %q: %w", target.ChatJID, err)
	}
	if groupJID.Server != types.GroupServer {
		return nil, nil, ErrNotAGroupMessage
	}
	// Answering ourselves privately is a chat with ourselves. Refused rather
	// than quietly opening the Message Yourself thread, which is never what the
	// operator meant by pressing this.
	if target.FromMe {
		return nil, nil, fmt.Errorf("%w: pesan ini dikirim oleh akun sendiri", ErrNotAGroupMessage)
	}
	if target.SenderJID == "" {
		return nil, nil, ErrNoSender
	}
	senderJID, err := types.ParseJID(target.SenderJID)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid sender jid %q: %w", target.SenderJID, err)
	}

	conv, err := m.repo.GetConversationByID(ctx, target.ConversationID)
	if err != nil {
		return nil, nil, err
	}

	// Both of the sender's addresses, because a person has two and a thread may
	// be keyed by either. Carrying only the one the group happened to use is
	// what opens a second Thaariq beside the Thaariq that already exists.
	//
	// The phone form comes from whatsmeow's own LID mapping rather than from our
	// contact rows: the mapping is the authority, and a group member we have
	// never had a private chat with has no contact row to read.
	pnJID := ""
	if s, ok := m.Session(conv.AccountID); ok {
		pnJID = s.phoneJID(ctx, senderJID)
	}
	name := m.senderName(ctx, workspaceID, conv.ID, senderJID)

	privateID, err := m.repo.UpsertConversation(ctx, repository.UpsertConversationInput{
		WorkspaceID: workspaceID,
		AccountID:   conv.AccountID,
		ChatJID:     senderJID.ToNonAD().String(),
		PNJID:       pnJID,
		Type:        models.ConversationTypePersonal,
		Name:        name,
	})
	if err != nil {
		return nil, nil, err
	}

	private, err := m.repo.GetConversationByID(ctx, privateID)
	if err != nil {
		return nil, nil, err
	}

	appID, err := m.repo.ApplicationOfAccount(ctx, workspaceID, private.AccountID)
	if err != nil {
		return nil, nil, err
	}

	out := &PrivateReplyTarget{
		ConversationID: private.ID,
		AccountID:      private.AccountID,
		ApplicationID:  appID,
		Name:           textOr(private.Name, name),
		PhoneNumber:    senderJID.User,
		GroupName:      textOr(conv.Name, ""),
	}
	if out.Name == "" {
		out.Name = senderJID.User
	}
	return out, target, nil
}

// textOr reads an optional string, falling back when it is absent or blank.
func textOr(value *string, fallback string) string {
	if value != nil && *value != "" {
		return *value
	}
	return fallback
}

// senderName resolves a group member's display name from the member list.
//
// Best effort by design: the list may be stale or missing, and a private reply
// labelled with the number is better than one refused for want of a nickname.
func (m *Manager) senderName(
	ctx context.Context,
	workspaceID, groupConversationID uuid.UUID,
	senderJID types.JID,
) string {
	members, err := m.repo.GroupMembers(ctx, workspaceID, groupConversationID)
	if err != nil {
		return ""
	}
	want := senderJID.ToNonAD().String()
	for _, member := range members {
		if member.JID == want {
			return member.DisplayName
		}
	}
	return ""
}

// SendPrivateReply sends a text into the sender's one-to-one thread, quoting
// the group message it answers.
func (m *Manager) SendPrivateReply(
	ctx context.Context,
	workspaceID, messageID uuid.UUID,
	text string,
	sentBy uuid.UUID,
) (*models.Message, *PrivateReplyTarget, error) {
	if text == "" {
		return nil, nil, ErrEmptyMessage
	}

	where, quoted, err := m.ResolvePrivateReply(ctx, workspaceID, messageID)
	if err != nil {
		return nil, nil, err
	}

	conv, err := m.repo.GetConversation(ctx, workspaceID, where.ConversationID)
	if err != nil {
		return nil, nil, err
	}
	s, ok := m.Session(conv.AccountID)
	if !ok {
		return nil, nil, ErrSessionNotFound
	}
	if !s.IsConnected() {
		return nil, nil, ErrNotConnected
	}
	chatJID, err := types.ParseJID(conv.ChatJID)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid chat jid %q: %w", conv.ChatJID, err)
	}

	waID := s.client.GenerateMessageID()
	senderJID := ""
	if own := s.client.Store.ID; own != nil {
		senderJID = own.ToNonAD().String()
	}

	msg, _, err := m.repo.InsertMessage(ctx, repository.InsertMessageInput{
		WorkspaceID:    workspaceID,
		AccountID:      conv.AccountID,
		ConversationID: conv.ID,
		WAMessageID:    waID,
		SenderJID:      strPtr(senderJID),
		FromMe:         true,
		Type:           "text",
		Body:           &text,
		// Stored, so our thread shows the same quote the recipient sees.
		//
		// This was left out at first on the assumption that a quote pointing
		// into a group would not resolve from a one-to-one thread and would
		// draw an empty box. That assumption was wrong: quoted messages are
		// looked up by account, not by conversation, so the group message
		// resolves from here perfectly well. Leaving it out only meant the
		// customer could see what was being answered and the operator could
		// not.
		QuotedMessageID: &quoted.WAMessageID,
		Status:          models.MessageStatusPending,
		Timestamp:       time.Now().UTC(),
		SentBy:          &sentBy,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("persist outgoing message: %w", err)
	}
	if full, err := m.repo.GetMessageByID(ctx, workspaceID, msg.ID); err == nil {
		msg = full
	}

	m.hub.Broadcast(workspaceID, realtime.EventMessageNew, map[string]any{
		"account_id": conv.AccountID,
		"message":    msg,
	})

	m.readBeforeReplying(ctx, workspaceID, conv)
	release := s.beginSend(ctx, chatJID, text, types.ChatPresenceMediaText)
	defer release()

	ctxInfo := quoteContext(s, quoted)
	// The group the quoted message was written in. This is the field that makes
	// it a private reply rather than a quote of something the recipient's
	// one-to-one thread has never seen.
	ctxInfo.RemoteJID = proto.String(quoted.ChatJID)

	payload := &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{
		Text:        proto.String(text),
		ContextInfo: ctxInfo,
	}}

	resp, sendErr := s.client.SendMessage(ctx, chatJID, payload,
		whatsmeow.SendRequestExtra{ID: types.MessageID(waID)})
	if sendErr != nil {
		detail := truncate(sendErr.Error(), 400)
		if failed, err := m.repo.SetMessageOutcome(
			ctx, msg.ID, models.MessageStatusFailed, nil, &detail); err == nil {
			msg = failed
		}
		m.broadcastMessageStatus(workspaceID, conv.AccountID, conv.ID, msg)
		return msg, where, fmt.Errorf("kirim balasan pribadi: %w", sendErr)
	}

	sentAt := resp.Timestamp
	updated, err := m.repo.SetMessageOutcome(ctx, msg.ID, models.MessageStatusSent, &sentAt, nil)
	if err != nil {
		return msg, where, nil // it went out; the receipt handler corrects the row
	}

	m.broadcastMessageStatus(workspaceID, conv.AccountID, conv.ID, updated)
	if refreshed, err := m.repo.GetConversationByID(ctx, conv.ID); err == nil {
		m.hub.Broadcast(workspaceID, realtime.EventConversationUpdate, refreshed)
	}
	// A private reply answers a customer exactly as any other message does, so
	// it closes an SLA cycle the same way.
	m.RefreshConversationMetrics(ctx, workspaceID, conv.ID)
	return updated, where, nil
}
