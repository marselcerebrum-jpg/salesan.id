package wa

import (
	"context"
	"errors"
	"fmt"
	"time"
	"unicode"

	"github.com/google/uuid"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"

	"github.com/salesan/omnichannel/backend/internal/models"
	"github.com/salesan/omnichannel/backend/internal/repository"
)

// ErrInvalidReaction reports a reaction WhatsApp would not accept.
var ErrInvalidReaction = errors.New("wa: reaction is not valid")

// SendReaction reacts to a message, or takes the reaction back.
//
// An empty emoji removes it, which is how WhatsApp itself expresses the
// difference — the same call serves both, and the caller does not have to
// decide which operation this is.
func (m *Manager) SendReaction(
	ctx context.Context,
	workspaceID, messageID uuid.UUID,
	emoji string,
) (*models.Message, error) {
	emoji, err := normalizeReaction(emoji)
	if err != nil {
		return nil, err
	}

	target, err := m.repo.MessageTargetByID(ctx, workspaceID, messageID)
	if err != nil {
		return nil, err
	}
	if target.RevokedAt != nil {
		return nil, fmt.Errorf("%w: pesan sudah dihapus", ErrInvalidReaction)
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
	sender := types.EmptyJID
	if target.SenderJID != "" {
		if parsed, err := types.ParseJID(target.SenderJID); err == nil {
			sender = parsed
		}
	}

	// Reactions are not paced like messages: they carry no text, produce no
	// typing indicator, and a person taps them instantly. Running them through
	// the humanizer would add a delay that makes the interface feel broken
	// without making the account look any more human.
	reaction := s.client.BuildReaction(chatJID, sender, types.MessageID(target.WAMessageID), emoji)
	if _, err := s.client.SendMessage(ctx, chatJID, reaction); err != nil {
		return nil, fmt.Errorf("kirim reaksi: %w", err)
	}

	// Recorded locally straight away. WhatsApp does not echo a reaction back to
	// the device that sent it, so waiting for confirmation would leave the
	// chip missing until somebody else reacted.
	if _, err := m.repo.ApplyReaction(ctx, messageID, s.ownReactionJID(), emoji, true, time.Now().UTC()); err != nil {
		return nil, fmt.Errorf("record own reaction: %w", err)
	}

	updated, err := m.repo.GetMessageByID(ctx, workspaceID, messageID)
	if err != nil {
		return nil, err
	}
	m.broadcastMessageStatus(workspaceID, conv.AccountID, conv.ID, updated)
	return updated, nil
}

// ownReactionJID is the address this account files its own reactions under.
//
// The phone-number form, consistently — a reaction is only ever compared
// against itself, so the important thing is that the same address is used every
// time rather than which of the two it happens to be.
func (s *Session) ownReactionJID() string {
	if id := s.client.Store.ID; id != nil {
		return id.ToNonAD().String()
	}
	if lid := s.client.Store.GetLID(); !lid.IsEmpty() {
		return lid.ToNonAD().String()
	}
	return ""
}

// normalizeReaction checks that the text is something WhatsApp will take.
//
// One emoji, or nothing. WhatsApp accepts a single grapheme; a sentence sent as
// a reaction is rejected by the server, so it is rejected here with an
// explanation instead.
func normalizeReaction(emoji string) (string, error) {
	if emoji == "" {
		return "", nil // removing
	}

	runes := []rune(emoji)
	// A single emoji can be several code points — a skin tone, a zero-width
	// joiner sequence, a variation selector — so the limit is generous while
	// still refusing prose.
	if len(runes) > 12 {
		return "", fmt.Errorf("%w: reaksi harus satu emoji", ErrInvalidReaction)
	}
	for _, r := range runes {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsSpace(r) {
			return "", fmt.Errorf("%w: reaksi harus emoji, bukan teks", ErrInvalidReaction)
		}
	}
	return emoji, nil
}

// --- incoming ------------------------------------------------------------------

// handleReaction applies a reaction that arrived from WhatsApp.
//
// Reactions are not messages: they attach to one. Storing them as rows in the
// thread — which is what happened before — turns every thumbs-up in a busy
// group into its own bubble.
func (s *Session) handleReaction(ctx context.Context, evt *events.Message) {
	reaction := evt.Message.GetReactionMessage()
	targetWAID := reaction.GetKey().GetID()
	if targetWAID == "" {
		return
	}

	messageID, conversationID, err := s.mgr.repo.ReactionTargetByWAID(ctx, s.AccountID, targetWAID)
	if err != nil {
		if !errors.Is(err, repository.ErrNotFound) {
			s.log.Warn("resolve reaction target", "wa_id", targetWAID, "err", err)
		}
		return // reacting to a message outside our window
	}

	at := evt.Info.Timestamp
	if ms := reaction.GetSenderTimestampMS(); ms > 0 {
		at = time.UnixMilli(ms)
	}

	// Our own reactions are filed under one fixed address. The phone reaches us
	// as either the number or the LID depending on the chat, and two addresses
	// for one person would show the same thumbs-up twice.
	reactor := evt.Info.Sender.ToNonAD().String()
	if evt.Info.IsFromMe {
		if own := s.ownReactionJID(); own != "" {
			reactor = own
		}
	}

	changed, err := s.mgr.repo.ApplyReaction(
		ctx, messageID, reactor,
		reaction.GetText(), evt.Info.IsFromMe, at)
	if err != nil {
		s.log.Warn("apply reaction", "wa_id", targetWAID, "err", err)
		return
	}
	if !changed {
		return // a repeat, or older than what is already stored
	}

	msg, err := s.mgr.repo.GetMessageByID(ctx, s.WorkspaceID, messageID)
	if err != nil {
		return
	}
	s.mgr.broadcastMessageStatus(s.WorkspaceID, s.AccountID, conversationID, msg)
}
