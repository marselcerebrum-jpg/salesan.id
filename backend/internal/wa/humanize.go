package wa

import (
	"context"
	"math/rand"
	"time"

	"github.com/google/uuid"
	"go.mau.fi/whatsmeow/types"

	"github.com/salesan/omnichannel/backend/internal/models"
)

// Sending like a person, not like a script.
//
// WhatsApp does not publish what it looks for, but the shape of it is no
// mystery: an account that replies in 80 milliseconds, never shows a typing
// indicator, leaves every incoming message unread, and sends twenty messages in
// two seconds is not behaving like someone holding a phone. Accounts that do
// that get rate-limited and then banned — and the ban falls on a real business
// number, which is not something this software can hand back.
//
// So every outgoing message goes through the same sequence a person produces
// without thinking about it:
//
//	read the chat  →  come online  →  type for a while  →  pause  →  send
//
// plus a floor on how close together two messages may go out. None of it is
// cosmetic: the typing indicator and the read receipt are real signals the
// other side sees, and the gap is what turns a bulk forward into a series of
// messages rather than a burst.

// jitter returns d scaled by a random factor in [1-spread, 1+spread].
//
// Fixed delays are their own tell — a reply that is always exactly 2.0 seconds
// late is as machine-like as one that is never late at all.
func jitter(d time.Duration, spread float64) time.Duration {
	if d <= 0 {
		return 0
	}
	factor := 1 + (rand.Float64()*2-1)*spread
	return time.Duration(float64(d) * factor)
}

// typingDuration is how long to hold the typing indicator for a message.
//
// Proportional to the length, because a long reply visibly takes longer to
// write than a short one, and clamped at both ends: a one-word answer should
// not appear instantly, and nobody waits half a minute for a paragraph.
func (s *Session) typingDuration(text string) time.Duration {
	cfg := s.mgr.cfg
	cps := cfg.TypingCPS
	if cps <= 0 {
		cps = 25
	}

	typed := time.Duration(float64(len([]rune(text))) / float64(cps) * float64(time.Second))
	total := cfg.TypingMin + typed

	if total > cfg.TypingMax {
		total = cfg.TypingMax
	}
	if total < cfg.TypingMin {
		total = cfg.TypingMin
	}
	return jitter(total, 0.2)
}

// sleepFor waits, but gives up the moment the request is cancelled — a browser
// that closed the tab should not leave a goroutine holding the send gate.
func sleepFor(ctx context.Context, d time.Duration) {
	if d <= 0 {
		return
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

// ensureOnline marks the account available so WhatsApp will deliver its typing
// indicator.
//
// Done once per connection rather than per message: presence is connection
// state, and repeating it on every send would be noise on the wire.
func (s *Session) ensureOnline(ctx context.Context) {
	if !s.mgr.cfg.PresenceOnline || s.presenceSet.Load() {
		return
	}
	if err := s.client.SendPresence(ctx, types.PresenceAvailable); err != nil {
		// Most often ErrNoPushName, on an account that has never set a display
		// name. Typing indicators will not show, which is worth one line in the
		// log and no more — the message itself still goes.
		s.log.Debug("could not go online for typing indicator", "err", err)
		return
	}
	s.presenceSet.Store(true)
}

// beginSend prepares the wire for one outgoing message and returns the release
// to call once it has gone.
//
// The gate is held across the send itself, so the gap between messages is
// measured send-to-send. That is what makes it a rate limit rather than a
// decoration: two operators replying at once queue behind each other instead of
// producing the burst the limit exists to prevent.
func (s *Session) beginSend(ctx context.Context, chat types.JID, text string, media types.ChatPresenceMedia) func() {
	s.sendMu.Lock()

	release := func() {
		s.lastSendAt = time.Now()
		s.sendMu.Unlock()
	}

	cfg := s.mgr.cfg
	if !cfg.HumanizeSending {
		return release
	}

	// Space this message from the previous one.
	if !s.lastSendAt.IsZero() {
		if gap := cfg.SendMinGap - time.Since(s.lastSendAt); gap > 0 {
			sleepFor(ctx, jitter(gap, 0.3))
		}
	}
	if ctx.Err() != nil {
		return release
	}

	s.ensureOnline(ctx)

	// The typing indicator, held for as long as the message would take to
	// write, then explicitly cleared. Leaving it composing would show the
	// recipient a typing bubble that never resolves.
	if err := s.client.SendChatPresence(ctx, chat, types.ChatPresenceComposing, media); err != nil {
		s.log.Debug("send typing indicator", "err", err)
		return release
	}
	sleepFor(ctx, s.typingDuration(text))
	if err := s.client.SendChatPresence(ctx, chat, types.ChatPresencePaused, media); err != nil {
		s.log.Debug("clear typing indicator", "err", err)
	}
	return release
}

// readBeforeReplying marks a thread read on the way to answering it.
//
// A reply to a message that is still showing as unread is a small
// inconsistency, and it is the sort of thing an automated sender produces and a
// person never does. Best effort throughout: failing to send a receipt must
// never stop the reply.
func (m *Manager) readBeforeReplying(ctx context.Context, workspaceID uuid.UUID, conv *models.Conversation) {
	if !m.cfg.HumanizeSending || !m.cfg.AutoMarkRead {
		return
	}
	if conv.UnreadCount == 0 && !conv.MarkedUnread {
		return
	}

	if err := m.SendReadReceipt(ctx, workspaceID, conv.ID); err != nil {
		m.log.Debug("read receipt before replying", "conversation_id", conv.ID, "err", err)
		return
	}
	if _, err := m.repo.MarkConversationRead(ctx, workspaceID, conv.ID); err != nil {
		m.log.Debug("clear unread before replying", "conversation_id", conv.ID, "err", err)
	}
}
