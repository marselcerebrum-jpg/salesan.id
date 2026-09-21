package wa

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"

	"github.com/salesan/omnichannel/backend/internal/models"
	"github.com/salesan/omnichannel/backend/internal/realtime"
	"github.com/salesan/omnichannel/backend/internal/repository"
)

// Poll limits, matching what WhatsApp itself accepts.
const (
	MinPollOptions = 2
	MaxPollOptions = 12
	MaxPollName    = 255
	MaxPollOption  = 100
)

// ErrInvalidPoll reports a poll the app refuses to send.
var ErrInvalidPoll = errors.New("wa: poll is not valid")

// SendPoll creates a poll in a conversation.
//
// Same shape as SendText: the row is written as `pending` before the network
// call, using an id generated up front, so the optimistic row and WhatsApp's
// echo share one idempotency key.
func (m *Manager) SendPoll(
	ctx context.Context,
	workspaceID, conversationID uuid.UUID,
	name string,
	options []string,
	selectableCount int,
	sentBy uuid.UUID,
) (*models.Message, error) {
	name, options, err := normalizePoll(name, options)
	if err != nil {
		return nil, err
	}
	// 1 means single choice; anything else is treated as "any number", which
	// WhatsApp expresses as 0.
	if selectableCount != 1 {
		selectableCount = 0
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

	// The question doubles as the message body, so search and the conversation
	// preview have something meaningful to show.
	msg, _, err := m.repo.InsertMessage(ctx, repository.InsertMessageInput{
		WorkspaceID:    workspaceID,
		AccountID:      conv.AccountID,
		ConversationID: conv.ID,
		WAMessageID:    msgID,
		SenderJID:      strPtr(senderJID),
		FromMe:         true,
		Type:           "poll",
		Body:           &name,
		Status:         models.MessageStatusPending,
		Timestamp:      time.Now().UTC(),
		SentBy:         &sentBy,
	})
	if err != nil {
		return nil, fmt.Errorf("persist poll message: %w", err)
	}

	if err := m.repo.InsertPoll(ctx, repository.InsertPollInput{
		WorkspaceID:     workspaceID,
		AccountID:       conv.AccountID,
		MessageID:       msg.ID,
		Name:            name,
		SelectableCount: selectableCount,
		Options:         options,
	}); err != nil {
		return nil, fmt.Errorf("persist poll: %w", err)
	}

	if full, err := m.repo.GetMessageByID(ctx, workspaceID, msg.ID); err == nil {
		msg = full
	}
	m.hub.Broadcast(workspaceID, realtime.EventMessageNew, map[string]any{
		"account_id": conv.AccountID,
		"message":    msg,
	})

	m.readBeforeReplying(ctx, workspaceID, conv)
	release := s.beginSend(ctx, chatJID, name, types.ChatPresenceMediaText)
	defer release()

	// whatsmeow generates the poll's message secret and stores it on send, which
	// is what later lets incoming votes be decrypted.
	poll := s.client.BuildPollCreation(name, options, selectableCount)
	resp, sendErr := s.client.SendMessage(ctx, chatJID, poll,
		whatsmeow.SendRequestExtra{ID: msgID})
	if sendErr != nil {
		detail := truncate(sendErr.Error(), 400)
		if failed, err := m.repo.SetMessageOutcome(ctx, msg.ID, models.MessageStatusFailed, nil, &detail); err == nil {
			msg = failed
		}
		m.broadcastMessageStatus(workspaceID, conv.AccountID, conv.ID, msg)
		return msg, fmt.Errorf("send poll: %w", sendErr)
	}

	sentAt := resp.Timestamp
	updated, err := m.repo.SetMessageOutcome(ctx, msg.ID, models.MessageStatusSent, &sentAt, nil)
	if err != nil {
		return msg, nil // it went out; the receipt handler will correct the row
	}
	if full, err := m.repo.GetMessageByID(ctx, workspaceID, updated.ID); err == nil {
		updated = full
	}

	m.broadcastMessageStatus(workspaceID, conv.AccountID, conv.ID, updated)
	if refreshed, err := m.repo.GetConversationByID(ctx, conv.ID); err == nil {
		m.hub.Broadcast(workspaceID, realtime.EventConversationUpdate, refreshed)
	}
	return updated, nil
}

// VotePoll casts this account's vote on a poll.
//
// The selection is absolute, not a delta: WhatsApp expects the voter's complete
// current choice on every update, and an empty list clears the vote.
func (m *Manager) VotePoll(
	ctx context.Context,
	workspaceID, messageID uuid.UUID,
	optionIdx []int,
) (*models.Message, error) {
	ref, err := m.repo.PollByMessage(ctx, workspaceID, messageID)
	if err != nil {
		return nil, err
	}

	msg, err := m.repo.GetMessageByID(ctx, workspaceID, messageID)
	if err != nil {
		return nil, err
	}
	conv, err := m.repo.GetConversationByID(ctx, msg.ConversationID)
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

	chosen := make([]string, 0, len(optionIdx))
	for _, i := range optionIdx {
		if i < 0 || i >= len(ref.Options) {
			return nil, fmt.Errorf("%w: pilihan ke-%d tidak ada", ErrInvalidPoll, i)
		}
		chosen = append(chosen, ref.Options[i])
	}

	chatJID, err := types.ParseJID(conv.ChatJID)
	if err != nil {
		return nil, err
	}
	senderJID := chatJID
	if ref.SenderJID != "" {
		if parsed, err := types.ParseJID(ref.SenderJID); err == nil {
			senderJID = parsed
		}
	}

	// Rebuilt from our own row rather than kept in memory: the poll may have
	// been created days ago, or by the phone rather than by this process.
	info := &types.MessageInfo{
		ID: ref.WAMessageID,
		MessageSource: types.MessageSource{
			Chat:     chatJID,
			Sender:   senderJID,
			IsFromMe: ref.FromMe,
			IsGroup:  chatJID.Server == types.GroupServer,
		},
	}

	vote, err := s.client.BuildPollVote(ctx, info, chosen)
	if err != nil {
		return nil, fmt.Errorf("build poll vote: %w", err)
	}
	if _, err := s.client.SendMessage(ctx, chatJID, vote); err != nil {
		return nil, fmt.Errorf("send poll vote: %w", err)
	}

	// Record our own vote locally. WhatsApp does not echo a vote back to the
	// device that cast it, so waiting for confirmation would leave the tally
	// looking broken until someone else voted.
	//
	// Filed under our canonical address, with the other one as an alias, so a
	// later vote from the phone — which may arrive under either — replaces this
	// one instead of standing beside it.
	ownJID, ownAlias := s.ownVoter()
	hashes := make([]string, 0, len(chosen))
	for _, name := range chosen {
		hashes = append(hashes, repository.PollOptionHash(name))
	}
	if _, err := m.repo.ApplyPollVote(ctx, messageID, ownJID, hashes, time.Now().UTC(), ownAlias); err != nil {
		return nil, fmt.Errorf("record own vote: %w", err)
	}

	updated, err := m.repo.GetMessageByID(ctx, workspaceID, messageID)
	if err != nil {
		return nil, err
	}
	m.broadcastMessageStatus(workspaceID, conv.AccountID, conv.ID, updated)
	return updated, nil
}

// ownVoter returns the address this account's votes are filed under, and the
// other address the same account can appear as.
//
// The phone number is canonical because it is the one that never changes for a
// number; the LID is the alias. Either may be empty on a session that has not
// learnt it yet.
func (s *Session) ownVoter() (canonical, alias string) {
	if own := s.client.Store.ID; own != nil {
		canonical = own.ToNonAD().String()
	}
	if lid := s.client.Store.GetLID(); !lid.IsEmpty() {
		alias = lid.ToNonAD().String()
	}
	if canonical == "" {
		return alias, ""
	}
	return canonical, alias
}

// normalizePoll trims and validates a poll before it is stored or sent.
func normalizePoll(name string, options []string) (string, []string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", nil, fmt.Errorf("%w: pertanyaan tidak boleh kosong", ErrInvalidPoll)
	}
	if len([]rune(name)) > MaxPollName {
		return "", nil, fmt.Errorf("%w: pertanyaan terlalu panjang", ErrInvalidPoll)
	}

	cleaned := make([]string, 0, len(options))
	seen := map[string]bool{}
	for _, opt := range options {
		opt = strings.TrimSpace(opt)
		if opt == "" {
			continue
		}
		if len([]rune(opt)) > MaxPollOption {
			return "", nil, fmt.Errorf("%w: pilihan terlalu panjang", ErrInvalidPoll)
		}
		// Duplicates are refused rather than silently merged: options are
		// identified by the hash of their text, so two identical options would
		// be indistinguishable in every vote that followed.
		if seen[opt] {
			return "", nil, fmt.Errorf("%w: pilihan %q ditulis dua kali", ErrInvalidPoll, opt)
		}
		seen[opt] = true
		cleaned = append(cleaned, opt)
	}

	if len(cleaned) < MinPollOptions {
		return "", nil, fmt.Errorf("%w: minimal %d pilihan", ErrInvalidPoll, MinPollOptions)
	}
	if len(cleaned) > MaxPollOptions {
		return "", nil, fmt.Errorf("%w: maksimal %d pilihan", ErrInvalidPoll, MaxPollOptions)
	}
	return name, cleaned, nil
}

// --- incoming ----------------------------------------------------------------

// capturePollCreation stores the poll carried by an inbound message.
func (s *Session) capturePollCreation(ctx context.Context, evt *events.Message, msg *models.Message) {
	poll := pollCreation(evt.Message)
	if poll == nil {
		return
	}

	options := make([]string, 0, len(poll.GetOptions()))
	for _, opt := range poll.GetOptions() {
		options = append(options, sanitizeDisplayName(opt.GetOptionName()))
	}

	if err := s.mgr.repo.InsertPoll(ctx, repository.InsertPollInput{
		WorkspaceID:     s.WorkspaceID,
		AccountID:       s.AccountID,
		MessageID:       msg.ID,
		Name:            sanitizeDisplayName(poll.GetName()),
		SelectableCount: int(poll.GetSelectableOptionsCount()),
		Options:         options,
	}); err != nil {
		s.log.Warn("store incoming poll", "wa_id", evt.Info.ID, "err", err)
	}
}

// attachHistoryPolls stores the polls found in a completed backfill batch.
func (s *Session) attachHistoryPolls(ctx context.Context, byWAID map[string]*waE2E.PollCreationMessage) {
	if len(byWAID) == 0 {
		return
	}
	waIDs := make([]string, 0, len(byWAID))
	for id := range byWAID {
		waIDs = append(waIDs, id)
	}

	ids, err := s.mgr.repo.MessageIDsByWAIDs(ctx, s.AccountID, waIDs)
	if err != nil {
		s.log.Warn("resolve history poll ids", "err", err)
		return
	}
	for waID, poll := range byWAID {
		messageID, ok := ids[waID]
		if !ok {
			continue // fell outside the window
		}
		options := make([]string, 0, len(poll.GetOptions()))
		for _, opt := range poll.GetOptions() {
			options = append(options, sanitizeDisplayName(opt.GetOptionName()))
		}
		if err := s.mgr.repo.InsertPoll(ctx, repository.InsertPollInput{
			WorkspaceID:     s.WorkspaceID,
			AccountID:       s.AccountID,
			MessageID:       messageID,
			Name:            sanitizeDisplayName(poll.GetName()),
			SelectableCount: int(poll.GetSelectableOptionsCount()),
			Options:         options,
		}); err != nil {
			s.log.Warn("store history poll", "wa_id", waID, "err", err)
		}
	}
}

// pollCreation returns the poll payload regardless of which version of the
// message WhatsApp used. Older clients still send V1/V2.
func pollCreation(msg *waE2E.Message) *waE2E.PollCreationMessage {
	if msg == nil {
		return nil
	}
	if p := msg.GetPollCreationMessageV3(); p != nil {
		return p
	}
	if p := msg.GetPollCreationMessageV2(); p != nil {
		return p
	}
	return msg.GetPollCreationMessage()
}

// handlePollUpdate applies an incoming vote.
//
// The vote itself is encrypted with a secret derived from the poll message,
// which whatsmeow stored when the poll was sent or received — so a poll this
// process never saw created cannot have its votes read, and that is reported
// rather than guessed at.
func (s *Session) handlePollUpdate(ctx context.Context, evt *events.Message) {
	update := evt.Message.GetPollUpdateMessage()
	if update == nil {
		return
	}
	pollWAID := update.GetPollCreationMessageKey().GetID()
	if pollWAID == "" {
		return
	}

	messageID, err := s.mgr.repo.PollMessageIDForWAID(ctx, s.AccountID, pollWAID)
	if err != nil {
		if !errors.Is(err, repository.ErrNotFound) {
			s.log.Warn("resolve poll for vote", "poll_wa_id", pollWAID, "err", err)
		}
		return // a poll outside our sync window
	}

	vote, err := s.client.DecryptPollVote(ctx, evt)
	if err != nil {
		s.log.Warn("decrypt poll vote", "poll_wa_id", pollWAID, "err", err)
		return
	}

	hashes := make([]string, 0, len(vote.GetSelectedOptions()))
	for _, h := range vote.GetSelectedOptions() {
		hashes = append(hashes, hex.EncodeToString(h))
	}

	voter := evt.Info.Sender.ToNonAD().String()
	var aliases []string
	// Our own vote, cast on the phone. It can arrive under the phone number or
	// the LID; both are folded into the one address the web uses, which is what
	// keeps a phone vote and a web vote from counting this account twice.
	if evt.Info.IsFromMe {
		own, alias := s.ownVoter()
		if own != "" {
			aliases = []string{alias, voter}
			voter = own
		}
	}
	at := evt.Info.Timestamp
	if ts := update.GetSenderTimestampMS(); ts > 0 {
		at = time.UnixMilli(ts)
	}

	applied, err := s.mgr.repo.ApplyPollVote(ctx, messageID, voter, hashes, at, aliases...)
	if err != nil {
		s.log.Warn("apply poll vote", "poll_wa_id", pollWAID, "err", err)
		return
	}
	if !applied {
		return // an older update than the one already stored
	}

	s.log.Info("poll vote applied",
		"poll_wa_id", pollWAID, "voter", voter, "options", len(hashes))

	msg, err := s.mgr.repo.GetMessageByID(ctx, s.WorkspaceID, messageID)
	if err != nil {
		return
	}
	s.mgr.broadcastMessageStatus(s.WorkspaceID, s.AccountID, msg.ConversationID, msg)
}
