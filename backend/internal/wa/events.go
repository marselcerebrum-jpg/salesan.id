package wa

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.mau.fi/whatsmeow/appstate"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"

	"github.com/salesan/omnichannel/backend/internal/models"
	"github.com/salesan/omnichannel/backend/internal/realtime"
	"github.com/salesan/omnichannel/backend/internal/repository"
)

// eventTimeout bounds the database work done inside a single event.
const eventTimeout = 20 * time.Second

// handleEvent is the single whatsmeow event handler for this session.
//
// whatsmeow invokes handlers synchronously and in order, which is exactly what
// we want: messages land in the database in the order WhatsApp delivered them.
// Every branch is therefore expected to finish quickly; anything slower (like a
// full contact sync) is dispatched to its own goroutine.
func (s *Session) handleEvent(rawEvt any) {
	switch evt := rawEvt.(type) {
	case *events.Connected:
		s.onConnected()

	case *events.PairSuccess:
		s.onPairSuccess(evt)

	case *events.LoggedOut:
		s.onLoggedOut(evt)

	case *events.StreamReplaced:
		s.log.Warn("stream replaced by another client")
		s.mgr.setStatus(s.AccountID, s.WorkspaceID, models.AccountStatusDisconnected,
			"Sesi diambil alih oleh perangkat lain")

	case *events.Disconnected:
		s.onDisconnected()

	case *events.ConnectFailure:
		s.log.Error("connect failure", "reason", evt.Reason, "message", evt.Message)
		s.mgr.setStatus(s.AccountID, s.WorkspaceID, models.AccountStatusError, evt.Message)

	case *events.ClientOutdated:
		s.mgr.setStatus(s.AccountID, s.WorkspaceID, models.AccountStatusError,
			"Versi klien WhatsApp sudah usang, perbarui whatsmeow")

	case *events.TemporaryBan:
		s.log.Error("temporary ban", "code", evt.Code, "expires_in", evt.Expire)
		s.mgr.setStatus(s.AccountID, s.WorkspaceID, models.AccountStatusError, evt.String())

	case *events.Message:
		s.handleMessage(evt)

	case *events.Receipt:
		s.handleReceipt(evt)

	case *events.HistorySync:
		go s.handleHistorySync(evt)

	case *events.PushName:
		s.handlePushName(evt)

	case *events.LabelEdit:
		s.handleLabelEdit(evt)

	case *events.LabelAssociationChat:
		s.handleLabelAssociation(evt)

	case *events.MarkChatAsRead:
		s.handleMarkChatAsRead(evt)

	case *events.Archive:
		s.handleChatFlag(evt.JID, "is_archived", evt.Action.GetArchived())

	case *events.Pin:
		s.handleChatFlag(evt.JID, "is_pinned", evt.Action.GetPinned())

	case *events.AppStateSyncComplete:
		s.handleAppStateSyncComplete(evt)

	case *events.AppStateSyncError:
		go s.handleAppStateSyncError(evt)

	case *events.OfflineSyncCompleted:
		s.log.Info("offline sync completed", "count", evt.Count)
	}
}

// handleAppStateSyncComplete fires once a collection finishes syncing, whether
// through the normal encrypted route or the plaintext recovery.
//
// Recovery is asynchronous: the Sinkron request has already returned by the
// time the phone answers. Without this push the operator would sit looking at a
// stale label list until they pressed Sinkron a second time.
func (s *Session) handleAppStateSyncComplete(evt *events.AppStateSyncComplete) {
	ctx, cancel := context.WithTimeout(context.Background(), eventTimeout)
	defer cancel()

	labels, err := s.mgr.repo.CountWhatsAppLabels(ctx, s.AccountID)
	if err != nil {
		s.log.Warn("count labels after app state sync", "err", err)
	}

	s.log.Info("app state sync complete",
		"patch", evt.Name, "version", evt.Version, "recovery", evt.Recovery, "labels", labels)

	s.mgr.hub.Broadcast(s.WorkspaceID, realtime.EventSyncProgress, map[string]any{
		"account_id": s.AccountID,
		"phase":      "app_state",
		"patch":      string(evt.Name),
		"result":     SyncResult{Labels: labels},
	})

	// The collection is writable again now that its version is restored.
	s.markRecovered(evt.Name)

	// `regular` is the collection labels live in; once it lands the two sides
	// agree, so flip the badge and push the finished set.
	if evt.Name == appstate.WAPatchRegular {
		// The phone answered, so nothing is outstanding any more.
		s.labelRecoverySince.Store(0)
		_ = s.mgr.repo.SetLabelSyncState(ctx, s.AccountID, LabelSyncSynced, "")
		s.broadcastSyncState(ctx, LabelSyncSynced, "")
		s.broadcastLabels(ctx, "app_state")
	}
}

// --- read state and chat flags ----------------------------------------------

// handleMarkChatAsRead applies a read/unread toggle made on another device.
func (s *Session) handleMarkChatAsRead(evt *events.MarkChatAsRead) {
	ctx, cancel := context.WithTimeout(context.Background(), eventTimeout)
	defer cancel()

	chat := evt.JID.ToNonAD().String()
	read := evt.Action.GetRead()

	s.log.Info("chat read state from phone",
		"chat", chat, "read", read, "from_full_sync", evt.FromFullSync)

	if err := s.mgr.repo.SetConversationReadState(ctx, s.AccountID, chat, read); err != nil {
		s.log.Warn("apply read state", "chat", chat, "err", err)
		return
	}
	s.broadcastConversation(ctx, chat)
}

// handleChatFlag applies an archive or pin toggle from another device.
func (s *Session) handleChatFlag(jid types.JID, column string, value bool) {
	ctx, cancel := context.WithTimeout(context.Background(), eventTimeout)
	defer cancel()

	chat := jid.ToNonAD().String()
	if err := s.mgr.repo.SetConversationFlag(ctx, s.AccountID, chat, column, value); err != nil {
		s.log.Warn("apply chat flag", "chat", chat, "column", column, "err", err)
		return
	}
	s.broadcastConversation(ctx, chat)
}

// broadcastConversation pushes the current state of one thread to the browser.
func (s *Session) broadcastConversation(ctx context.Context, chatJID string) {
	conv, err := s.mgr.repo.GetConversationByAccountChat(ctx, s.AccountID, chatJID)
	if err != nil {
		return
	}
	s.mgr.hub.Broadcast(s.WorkspaceID, realtime.EventConversationUpdate, conv)
}

// --- connection lifecycle ----------------------------------------------------

func (s *Session) onConnected() {
	s.log.Info("whatsapp connected")

	// Presence is per connection: a reconnect starts out unannounced, so the
	// next send has to say "available" again before its typing indicator will
	// be delivered.
	s.presenceSet.Store(false)

	ctx, cancel := context.WithTimeout(context.Background(), eventTimeout)
	defer cancel()

	if id := s.client.Store.ID; id != nil {
		_ = s.mgr.repo.SetAccountIdentity(ctx, s.AccountID, id.String(), phoneFromJID(*id))
		_ = s.mgr.repo.UpsertSession(ctx, s.AccountID, id.String(),
			s.client.Store.PushName, s.client.Store.Platform, s.client.Store.BusinessName)
	}
	// The account's second address. WhatsApp uses it instead of the phone
	// number on a good half of the threads here, so anything asking "was this
	// me?" — a poll vote, most immediately — needs it alongside the JID.
	if lid := s.client.Store.GetLID(); !lid.IsEmpty() {
		if err := s.mgr.repo.SetAccountLID(ctx, s.AccountID, lid.ToNonAD().String()); err != nil {
			s.log.Warn("store account lid", "err", err)
		}
	}

	s.mgr.setStatus(s.AccountID, s.WorkspaceID, models.AccountStatusConnected, "")
	s.mgr.broadcastAccount(s.WorkspaceID, s.AccountID)

	// Pull contacts, groups and the label set in the background so the inbox is
	// populated and the two sides agree before the first message arrives.
	//
	// This runs on every connect, not just the first: while the socket was down
	// the phone may have added, renamed or removed labels, and nothing else
	// would tell us about those.
	go func() {
		syncCtx, syncCancel := context.WithTimeout(s.mgr.rootCtx, 5*time.Minute)
		defer syncCancel()

		if err := s.SyncDirectory(syncCtx); err != nil {
			s.log.Warn("initial directory sync failed", "err", err)
		}
		s.reconcileLabels(syncCtx, "connect")
		s.reconcileReadState(syncCtx, "connect")
	}()
}

func (s *Session) onPairSuccess(evt *events.PairSuccess) {
	s.log.Info("pair success", "jid", evt.ID.String(), "platform", evt.Platform)

	ctx, cancel := context.WithTimeout(context.Background(), eventTimeout)
	defer cancel()

	if err := s.mgr.repo.SetAccountIdentity(ctx, s.AccountID, evt.ID.String(), phoneFromJID(evt.ID)); err != nil {
		s.log.Error("persist paired identity", "err", err)
	}
	if err := s.mgr.repo.UpsertSession(ctx, s.AccountID, evt.ID.String(),
		s.client.Store.PushName, evt.Platform, evt.BusinessName); err != nil {
		s.log.Error("persist session", "err", err)
	}

	s.clearQR()
	s.mgr.setStatus(s.AccountID, s.WorkspaceID, models.AccountStatusConnecting, "")
	s.mgr.broadcastAccount(s.WorkspaceID, s.AccountID)
}

func (s *Session) onLoggedOut(evt *events.LoggedOut) {
	s.log.Warn("logged out by phone", "on_connect", evt.OnConnect, "reason", evt.Reason)

	ctx, cancel := context.WithTimeout(context.Background(), eventTimeout)
	defer cancel()

	_ = s.mgr.repo.CloseSessions(ctx, s.AccountID)
	_ = s.mgr.repo.ClearAccountIdentity(ctx, s.AccountID)

	s.mgr.hub.Broadcast(s.WorkspaceID, realtime.EventAccountStatus, map[string]any{
		"account_id":    s.AccountID,
		"status":        models.AccountStatusLoggedOut,
		"status_detail": "Perangkat ditautkan-lepas dari HP",
	})
	s.mgr.broadcastAccount(s.WorkspaceID, s.AccountID)

	// Drop the session: re-pairing needs a brand new device.
	go func() {
		s.stop(false)
		s.mgr.dropSession(s.AccountID)
	}()
}

func (s *Session) onDisconnected() {
	// whatsmeow retries on its own while EnableAutoReconnect is set; surface
	// the interim state rather than declaring the account dead.
	s.mu.Lock()
	pairing := s.pairing
	s.mu.Unlock()
	if pairing {
		return
	}

	select {
	case <-s.done:
		return // an intentional teardown already reported the status
	default:
	}

	s.log.Warn("whatsapp disconnected, auto-reconnect will retry")
	s.mgr.setStatus(s.AccountID, s.WorkspaceID, models.AccountStatusConnecting,
		"Terputus, mencoba menyambung ulang")
}

// --- messages ----------------------------------------------------------------

// messageRoute is what a message event turns out to be.
type messageRoute string

const (
	// routeStore is an ordinary message that becomes a row in the thread.
	routeStore messageRoute = "store"
	// routePollUpdate is a vote on a poll already in the thread.
	routePollUpdate messageRoute = "poll-update"
	// routeEdit and routeRevoke change a message already in the thread.
	routeEdit   messageRoute = "edit"
	routeRevoke messageRoute = "revoke"
	// routeReaction attaches an emoji to a message already in the thread.
	routeReaction messageRoute = "reaction"
)

// routeMessage decides what kind of event this is.
//
// A pure function with no session behind it, so the decision can be tested
// directly. That is not incidental: this classification silently broke every
// incoming message once, and a branch that fails by dropping messages without
// an error deserves to be pinned by a test rather than trusted.
func routeMessage(msg *waE2E.Message, isEdit bool) messageRoute {
	// A vote is an update to a poll, not a new message.
	if msg.GetPollUpdateMessage() != nil {
		return routePollUpdate
	}
	// A reaction attaches to a message rather than joining the thread. Storing
	// it as a row turns every thumbs-up in a busy group into its own bubble.
	if msg.GetReactionMessage() != nil {
		return routeReaction
	}
	// Edits and deletions change a message already in the thread. They arrive
	// as protocol messages, which extractContent skips, so they have to be
	// picked off here or a change made on the phone never reaches the web.
	if isEdit {
		return routeEdit
	}
	// The nil check is not optional. ProtocolMessage_REVOKE is 0, the zero
	// value of the enum, so `GetProtocolMessage().GetType()` on an ordinary
	// message — which has no protocol message at all — reads as REVOKE, and
	// every incoming message goes down the deletion path and vanishes with no
	// error to notice.
	if p := msg.GetProtocolMessage(); p != nil && p.GetType() == waE2E.ProtocolMessage_REVOKE {
		return routeRevoke
	}
	return routeStore
}

func (s *Session) handleMessage(evt *events.Message) {
	ctx, cancel := context.WithTimeout(context.Background(), eventTimeout)
	defer cancel()

	switch routeMessage(evt.Message, evt.IsEdit) {
	case routePollUpdate:
		s.handlePollUpdate(ctx, evt)
	case routeEdit:
		s.handleMessageEdit(ctx, evt)
	case routeRevoke:
		s.handleMessageRevoke(ctx, evt)
	case routeReaction:
		s.handleReaction(ctx, evt)
	default:
		if _, err := s.persistMessage(ctx, evt, ""); err != nil {
			s.log.Error("persist incoming message", "wa_id", evt.Info.ID, "err", err)
		}
	}
}

// persistMessage stores one message and, when it is genuinely new, pushes it to
// the browser. `forcedStatus` overrides the default status (used by history
// sync, where delivery state is already settled).
func (s *Session) persistMessage(ctx context.Context, evt *events.Message, forcedStatus string) (bool, error) {
	// Cheap gate first: protocol traffic must not create contacts or threads.
	if extractContent(evt.Message).Skip {
		return false, nil
	}

	chat := evt.Info.Chat.ToNonAD()
	sender := evt.Info.Sender.ToNonAD()
	convType := conversationType(chat)

	// Resolve who this thread belongs to. For a 1:1 chat the contact is the
	// chat itself; in a group the contact is the individual sender.
	var contactID *uuid.UUID
	contactJID := sender
	if convType == models.ConversationTypePersonal {
		contactJID = chat
	}
	if !evt.Info.IsFromMe || convType == models.ConversationTypeGroup {
		if id, err := s.mgr.repo.UpsertContact(ctx, repository.UpsertContactInput{
			WorkspaceID: s.WorkspaceID,
			AccountID:   s.AccountID,
			JID:         contactJID.String(),
			PhoneNumber: s.contactPhone(ctx, contactJID),
			PushName:    sanitizeDisplayName(evt.Info.PushName),
		}); err == nil {
			if convType == models.ConversationTypePersonal {
				contactID = &id
			}
		} else {
			s.log.Warn("upsert contact", "jid", contactJID.String(), "err", err)
		}
	}

	convName := ""
	if convType == models.ConversationTypeGroup {
		convName = s.groupName(ctx, chat)
	} else if !evt.Info.IsFromMe {
		convName = sanitizeDisplayName(evt.Info.PushName)
	}

	convID, err := s.mgr.repo.UpsertConversation(ctx, repository.UpsertConversationInput{
		WorkspaceID: s.WorkspaceID,
		AccountID:   s.AccountID,
		ChatJID:     chat.String(),
		PNJID:       s.phoneJID(ctx, chat),
		Type:        convType,
		Name:        convName,
		ContactID:   contactID,
	})
	if err != nil {
		return false, err
	}

	input, ok := s.buildMessageInput(evt, convID, forcedStatus)
	if !ok {
		return false, nil
	}

	msg, inserted, err := s.mgr.repo.InsertMessage(ctx, input)
	if err != nil {
		return false, err
	}
	if !inserted {
		return false, nil // duplicate: already delivered to the UI
	}

	// Attach any media before the broadcast so the bubble arrives with its
	// thumbnail and dimensions already in place, rather than resizing a moment
	// later. `forcedStatus` is only set by the history path, which downloads
	// lazily instead of eagerly.
	s.captureIncomingMedia(ctx, evt, msg, forcedStatus == "")
	s.capturePollCreation(ctx, evt, msg)

	// The badge is recomputed rather than incremented — see RefreshMentionCount.
	if msg.MentionsMe {
		if err := s.mgr.repo.RefreshMentionCount(ctx, convID); err != nil {
			s.log.Warn("refresh mention count", "conversation_id", convID, "err", err)
		} else {
			s.log.Info("mentioned in group", "chat", chat.String(), "wa_id", evt.Info.ID)
		}
	}

	if full, err := s.mgr.repo.GetMessageByID(ctx, s.WorkspaceID, msg.ID); err == nil {
		msg = full
	}

	s.mgr.hub.Broadcast(s.WorkspaceID, realtime.EventMessageNew, map[string]any{
		"account_id": s.AccountID,
		"message":    msg,
	})
	if conv, err := s.mgr.repo.GetConversationByID(ctx, convID); err == nil {
		s.mgr.hub.Broadcast(s.WorkspaceID, realtime.EventConversationUpdate, conv)
	}

	// After the broadcast, so the chat bubble is never waiting on a report.
	// This is where an SLA cycle opens or closes, a follow-up is recognised, a
	// mention is paired with its answer, and a contact is classified.
	s.refreshAfterMessage(ctx, convID, contactID)
	return true, nil
}

// buildMessageInput turns a whatsmeow message event into a storable row.
//
// Shared by the live path and the history-sync path so both agree on how a
// payload maps onto our schema. Returns false for payloads that carry no
// user-visible content.
func (s *Session) buildMessageInput(
	evt *events.Message,
	conversationID uuid.UUID,
	forcedStatus string,
) (repository.InsertMessageInput, bool) {
	body := extractContent(evt.Message)
	if body.Skip {
		return repository.InsertMessageInput{}, false
	}

	status := forcedStatus
	if status == "" {
		if evt.Info.IsFromMe {
			status = models.MessageStatusSent
		} else {
			status = models.MessageStatusDelivered
		}
	}

	sender := evt.Info.Sender.ToNonAD()
	senderJID := sender.String()

	// Who wrote it, kept separately from the chat it landed in. In a group
	// these differ; in a one-to-one chat the participant is the chat, and the
	// column stays empty so "group member" keeps one meaning.
	var participant, senderPhone *string
	if evt.Info.Chat.Server == types.GroupServer {
		participant = &senderJID
		if phone := s.participantPhone(context.Background(), sender); phone != "" {
			senderPhone = &phone
		}
	}

	return repository.InsertMessageInput{
		WorkspaceID:     s.WorkspaceID,
		AccountID:       s.AccountID,
		ConversationID:  conversationID,
		WAMessageID:     evt.Info.ID,
		SenderJID:       &senderJID,
		SenderName:      strPtr(sanitizeDisplayName(evt.Info.PushName)),
		FromMe:          evt.Info.IsFromMe,
		Type:            body.Type,
		Body:            body.Body,
		Caption:         body.Caption,
		MediaMime:       body.MediaMime,
		QuotedMessageID: body.QuotedID,
		Status:          status,
		Timestamp:       evt.Info.Timestamp,

		ParticipantJID: participant,
		SenderPhone:    senderPhone,
		MentionedJIDs:  body.MentionedJIDs,
		// Resolved here, against this account's own addresses, because the same
		// message reaching two of our numbers in one group mentions one of them
		// and not the other.
		MentionsMe: !evt.Info.IsFromMe && s.mentionsUs(body.MentionedJIDs),
	}, true
}

// mentionsUs reports whether any of the named addresses is this account.
//
// Compared against both forms the account answers to — its phone-number JID
// and its LID — with device suffixes already stripped on both sides. Matching
// only one form is how a mention in a LID-addressed group goes unnoticed, which
// is most of the groups on this workspace.
func (s *Session) mentionsUs(mentioned []string) bool {
	if len(mentioned) == 0 {
		return false
	}
	for _, jid := range mentioned {
		for _, own := range s.ownJIDs() {
			if jid == own {
				return true
			}
		}
	}
	return false
}

// ownJIDs is every address this account answers to, normalised.
func (s *Session) ownJIDs() []string {
	out := make([]string, 0, 2)
	if id := s.client.Store.ID; id != nil {
		out = append(out, id.ToNonAD().String())
	}
	if lid := s.client.Store.GetLID(); !lid.IsEmpty() {
		out = append(out, lid.ToNonAD().String())
	}
	return out
}

// participantPhone returns a group member's phone number, resolving a LID
// through whatsmeow's mapping when that is how they are addressed.
func (s *Session) participantPhone(ctx context.Context, jid types.JID) string {
	pn := s.phoneJID(ctx, jid)
	if pn == "" {
		return ""
	}
	parsed, err := types.ParseJID(pn)
	if err != nil {
		return ""
	}
	return phoneFromJID(parsed)
}

// groupName returns a group's subject, caching it per session so a busy group
// does not trigger a metadata fetch on every message.
func (s *Session) groupName(ctx context.Context, chat types.JID) string {
	if cached, ok := s.groupNames.Load(chat.String()); ok {
		if name, _ := cached.(string); name != "" {
			return name
		}
	}
	info, err := s.client.GetGroupInfo(ctx, chat)
	if err != nil || info == nil {
		return ""
	}
	name := sanitizeDisplayName(info.Name)
	s.groupNames.Store(chat.String(), name)
	return name
}

// --- receipts ----------------------------------------------------------------

func (s *Session) handleReceipt(evt *events.Receipt) {
	if len(evt.MessageIDs) == 0 {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), eventTimeout)
	defer cancel()

	chat := evt.Chat.ToNonAD().String()
	at := evt.Timestamp
	if at.IsZero() {
		at = time.Now().UTC()
	}

	// Logged unconditionally at info: read state is the one area where "nothing
	// happened" and "the event never arrived" look identical from the outside,
	// and telling them apart is impossible without seeing the raw receipts.
	receiptType := string(evt.Type)
	if receiptType == "" {
		receiptType = "delivered" // whatsmeow spells delivery receipts as ""
	}
	s.log.Info("receipt from whatsapp",
		"type", receiptType,
		"chat", chat,
		"sender", evt.Sender.ToNonAD().String(),
		"is_from_me", evt.IsFromMe,
		"is_group", evt.IsGroup,
		"messages", len(evt.MessageIDs))

	// A receipt on status@broadcast is somebody watching one of our Stories.
	// Handled before the generic idempotency claim below, because that key folds
	// in the timestamp and the message ids but NOT the sender — two viewers
	// opening the same Story in the same millisecond would collide and one would
	// be dropped. The story_views table has its own per-viewer unique key, which
	// is both stricter and the right shape.
	if evt.Chat.ToNonAD() == types.StatusBroadcastJID {
		s.recordStoryViews(ctx, evt, at)
		return
	}

	// Idempotency. WhatsApp resends receipts freely: on reconnect, during
	// offline sync, and once per linked device. The key folds in the timestamp
	// and the message ids, so a genuinely later receipt still gets through
	// while a replay of the same one is dropped.
	key := fmt.Sprintf("receipt:%s:%s:%d:%s",
		receiptType, chat, at.UnixMilli(), strings.Join(evt.MessageIDs, ","))
	if fresh, err := s.mgr.repo.ClaimReceiptEvent(ctx, s.AccountID, key); err != nil {
		s.log.Warn("claim receipt event", "err", err)
	} else if !fresh {
		s.log.Debug("receipt already applied, skipping", "type", receiptType, "chat", chat)
		return
	}

	// ReadSelf is not the same thing as Read. Read means the *recipient* opened
	// a message we sent; ReadSelf means *we* opened an incoming message on
	// another device â€” the operator reading the chat on their phone. Treating
	// them alike would mark our outbound messages read whenever the operator
	// glanced at their phone, and would never clear the unread badge here.
	// Who read it is decided by IsFromMe, NOT by the receipt type.
	//
	// The obvious reading — that "read-self" marks the operator reading on
	// their own phone — does not hold: WhatsApp sends a plain "read" receipt
	// with IsFromMe=true for that case. Sender is whoever sent the receipt, so
	// IsFromMe true means we read the message on another device; false means
	// the customer read something we sent. Branching on the type instead routed
	// the operator's own reads into the outbound-status path, where they marked
	// our sent messages read and never touched the unread badge.
	isRead := evt.Type == types.ReceiptTypeRead || evt.Type == types.ReceiptTypeReadSelf
	if isRead && evt.IsFromMe {
		s.applyReadSelf(ctx, evt.MessageIDs, chat, at)
		return
	}

	// From here on the receipt is about a message we sent, reported by whoever
	// received it.
	var status string
	switch evt.Type {
	case types.ReceiptTypeDelivered:
		status = models.MessageStatusDelivered
	case types.ReceiptTypeRead, types.ReceiptTypeReadSelf:
		status = models.MessageStatusRead
	default:
		return // played / retry / server-error carry no inbox meaning here
	}

	// A Broadcast recipient's phone reports delivery and reading exactly as any
	// other recipient's does, so the campaign report is driven by the same
	// receipt as the message bubble rather than by a second, parallel notion of
	// "delivered" that could disagree with it.
	if err := s.mgr.repo.AdvanceCampaignTargets(ctx, s.AccountID, evt.MessageIDs, status, at); err != nil {
		s.log.Warn("apply receipt to campaign targets", "err", err)
	}

	changes, err := s.mgr.repo.AdvanceMessageStatus(ctx, s.AccountID, evt.MessageIDs, status, at)
	if err != nil {
		s.log.Error("apply receipt", "status", status, "err", err)
		return
	}
	if len(changes) == 0 {
		return
	}
	s.mgr.hub.Broadcast(s.WorkspaceID, realtime.EventMessageStatus, map[string]any{
		"account_id": s.AccountID,
		"changes":    changes,
	})
}

// recordStoryViews turns a status@broadcast receipt into detected views.
//
// This is the ONLY source of Story view data, and it is worth being blunt about
// what that means. whatsmeow exposes no API for reading who viewed a status —
// there is no equivalent of the viewer list the phone app shows. What arrives
// here is a read (or played) receipt from a viewer whose client chose to send
// one, while this backend happened to be connected.
//
// So the figure this produces is a LOWER BOUND, never a view count:
//
//   - A viewer with read receipts switched off never generates one of these.
//   - A receipt that arrives while the backend is down is not replayed.
//   - Only viewers WhatsApp routes to us at all are visible.
//
// The interface says so beside every number (models.StoryViewNotice), and
// nothing anywhere estimates the difference. Inventing the missing viewers would
// turn an honest partial measurement into a fabricated total.
func (s *Session) recordStoryViews(ctx context.Context, evt *events.Receipt, at time.Time) {
	// Our own receipts are not views of our own Story.
	if evt.IsFromMe {
		return
	}
	var kind string
	switch evt.Type {
	case types.ReceiptTypeRead, types.ReceiptTypeReadSelf:
		kind = "read"
	case types.ReceiptTypePlayed:
		kind = "played"
	default:
		// delivered / retry / sender carry no evidence that anybody watched.
		return
	}

	viewer := evt.Sender.ToNonAD().String()
	if viewer == "" {
		return
	}

	fresh := 0
	for _, waID := range evt.MessageIDs {
		added, err := s.mgr.repo.RecordStoryView(ctx, s.AccountID, string(waID), viewer, kind, at)
		if err != nil {
			s.log.Warn("record story view", "wa_message_id", waID, "err", err)
			continue
		}
		if added {
			fresh++
		}
	}
	if fresh == 0 {
		return // a replayed receipt from somebody already counted
	}

	s.log.Info("story view detected", "viewer", viewer, "type", kind, "new", fresh)
	s.mgr.hub.Broadcast(s.WorkspaceID, realtime.EventCampaignUpdated, map[string]any{
		"account_id": s.AccountID,
		"story_view": true,
	})
}

// applyReadSelf records that the operator read messages on another device.
//
// Conversations are resolved from the message ids rather than the receipt's
// chat JID. WhatsApp addresses the same chat as a phone number in one context
// and as a LID in another — most of this workspace's threads are stored under
// `@lid` — so matching on the JID silently finds nothing and the badge never
// clears. Message ids are unambiguous, and they work identically for groups.
//
// The chat JID is still used, but only as a fallback for the case where the
// receipt names messages we never stored (outside the sync window).
func (s *Session) applyReadSelf(ctx context.Context, waIDs []string, chatJID string, at time.Time) {
	changes, convIDs, err := s.mgr.repo.MarkMessagesRead(ctx, s.AccountID, waIDs, at)
	if err != nil {
		s.log.Warn("stamp read-self messages", "chat", chatJID, "err", err)
		return
	}

	if len(convIDs) == 0 {
		// None of those ids are stored. Fall back to the chat JID so a receipt
		// for messages outside the window still clears the badge.
		if err := s.mgr.repo.SetConversationReadState(ctx, s.AccountID, chatJID, true); err != nil {
			s.log.Debug("read-self receipt matched no stored message and no chat",
				"chat", chatJID, "err", err)
			return
		}
		s.log.Info("chat read on phone (matched by chat jid)", "chat", chatJID)
		s.broadcastConversation(ctx, chatJID)
		return
	}

	// Recompute rather than zero: a receipt covers specific messages, and
	// anything newer that it did not name must stay unread.
	updated, err := s.mgr.repo.RecomputeUnread(ctx, convIDs)
	if err != nil {
		s.log.Warn("recompute unread after read-self", "err", err)
	}

	s.log.Info("chat read on phone",
		"messages_marked", len(changes),
		"conversations", len(convIDs),
		"counters_changed", len(updated))

	if len(changes) > 0 {
		s.mgr.hub.Broadcast(s.WorkspaceID, realtime.EventMessageStatus, map[string]any{
			"account_id": s.AccountID,
			"changes":    changes,
		})
	}
	for _, id := range convIDs {
		s.broadcastConversationByID(ctx, id)
	}
}

// broadcastConversationByID pushes one thread's current state to the browser.
func (s *Session) broadcastConversationByID(ctx context.Context, conversationID uuid.UUID) {
	conv, err := s.mgr.repo.GetConversationByID(ctx, conversationID)
	if err != nil {
		return
	}
	s.mgr.hub.Broadcast(s.WorkspaceID, realtime.EventConversationUpdate, conv)
}

// reconcileReadState rebuilds unread counters from the stored messages.
//
// Receipts that arrive while the socket is down are simply lost — WhatsApp does
// not replay every one on reconnect. A counter can therefore be left standing
// over messages that were read on the phone long ago. Counting inbound messages
// with no read_at is the only version of the truth that survives a gap in the
// event stream, so that is what the counters are rebuilt from.
func (s *Session) reconcileReadState(ctx context.Context, reason string) {
	// Mention badges are rebuilt from the stored messages for the same reason
	// unread counts are: events that arrived while the socket was down cannot
	// be replayed, so the counters are recomputed rather than trusted.
	if n, err := s.mgr.repo.ReconcileMentionCounts(ctx, s.AccountID); err != nil {
		s.log.Warn("reconcile mention counts", "reason", reason, "err", err)
	} else if n > 0 {
		s.log.Info("mention counts reconciled", "reason", reason, "conversations", n)
	}

	changed, err := s.mgr.repo.ReconcileUnreadCounts(ctx, s.AccountID)
	if err != nil {
		s.log.Warn("reconcile unread counts", "reason", reason, "err", err)
		return
	}
	if changed == 0 {
		return
	}

	s.log.Info("unread counts reconciled", "reason", reason, "conversations", changed)
	s.mgr.hub.Broadcast(s.WorkspaceID, realtime.EventSyncProgress, map[string]any{
		"account_id": s.AccountID,
		"phase":      "read_state",
		"result":     map[string]any{"conversations": changed},
	})
}

// --- contacts ----------------------------------------------------------------

func (s *Session) handlePushName(evt *events.PushName) {
	pushName := sanitizeDisplayName(evt.NewPushName)
	if pushName == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), eventTimeout)
	defer cancel()

	jid := evt.JID.ToNonAD()
	if _, err := s.mgr.repo.UpsertContact(ctx, repository.UpsertContactInput{
		WorkspaceID: s.WorkspaceID,
		AccountID:   s.AccountID,
		JID:         jid.String(),
		PhoneNumber: s.contactPhone(ctx, jid),
		PushName:    pushName,
	}); err != nil {
		s.log.Warn("upsert push name", "err", err)
	}
}
