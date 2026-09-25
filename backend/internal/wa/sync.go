package wa

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/appstate"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"

	"github.com/salesan/omnichannel/backend/internal/models"
	"github.com/salesan/omnichannel/backend/internal/realtime"
	"github.com/salesan/omnichannel/backend/internal/repository"
)

// DefaultSyncWindowDays bounds how far back a sync reaches. WhatsApp can push
// months of history on a fresh link; the inbox only claims to hold the last
// week, so anything older is dropped on ingest rather than silently kept.
const DefaultSyncWindowDays = 7

// SyncResult summarises what a "Sinkron" run touched.
type SyncResult struct {
	WindowDays int `json:"window_days"`
	Contacts   int `json:"contacts"`
	Groups     int `json:"groups"`
	// Linked counts threads that gained a contact link or a display name from
	// the freshly synced address book.
	Linked        int   `json:"linked"`
	Conversations int   `json:"conversations"`
	Messages      int   `json:"messages"`
	Skipped       int   `json:"skipped_out_of_window"`
	Labels        int   `json:"labels"`
	Pruned        int64 `json:"pruned"`
	// HistoryPending is true when the phone was asked for more history; those
	// messages arrive asynchronously as HistorySync events.
	HistoryPending bool `json:"history_pending"`
	// AppStateRecovering lists the app-state collections whose encrypted sync
	// failed and for which a plaintext copy was requested from the phone. This
	// is a normal, self-healing step — not a failure — but it completes
	// asynchronously, so the labels it carries land a few seconds later.
	AppStateRecovering []string `json:"app_state_recovering,omitempty"`
	// AppStateStale lists collections that could not be refreshed but kept
	// usable state: reads may lag, writes still work.
	AppStateStale []string `json:"app_state_stale,omitempty"`
	// AppStateError is set only when app-state could not be read AND the
	// recovery request could not even be sent. Labels and per-chat read state
	// live there, so a non-empty value means those two are genuinely stale.
	// Kept separate from AppStateRecovering so the UI never paints a red error
	// over what is really "wait a moment".
	AppStateError string `json:"app_state_error,omitempty"`
}

// SyncOptions configures one sync run.
type SyncOptions struct {
	// WindowDays is how many days of history to keep. Zero means the default.
	WindowDays int
	// Prune also deletes already-stored messages older than the window, so the
	// database matches the stated retention instead of keeping whatever an
	// earlier, unbounded sync happened to pull in.
	Prune bool
	// ForceHistory re-requests recent history even when the window already
	// looks covered.
	//
	// The ordinary check asks "does my oldest message reach back far enough",
	// which answers the wrong question after messages are lost from the middle
	// — a bug, an outage, a period where the socket was up but the handler was
	// dropping them. Forcing pages backwards from the *newest* message instead,
	// so the recent stretch is re-delivered and the gaps fill in. Inserts are
	// idempotent, so anything already stored is simply ignored.
	ForceHistory bool
}

// SyncAccount is the entry point behind the "Sinkron" button.
//
// It refreshes, in order: contacts and groups, then WhatsApp Business labels and
// per-chat read state (both live in app state), then chat history within the
// window. History arrives asynchronously, so the counts returned here cover only
// the synchronous passes.
func (m *Manager) SyncAccount(ctx context.Context, accountID uuid.UUID, opts SyncOptions) (*SyncResult, error) {
	s, ok := m.Session(accountID)
	if !ok {
		return nil, ErrSessionNotFound
	}
	if !s.IsConnected() {
		return nil, ErrNotConnected
	}

	days := opts.WindowDays
	if days <= 0 {
		days = DefaultSyncWindowDays
	}
	s.setSyncWindow(days)

	res := &SyncResult{WindowDays: days}

	// 1. Address book and groups.
	dir, err := s.syncDirectory(ctx)
	if err != nil {
		return nil, err
	}
	res.Contacts, res.Groups = dir.Contacts, dir.Groups
	res.Linked, res.Conversations = dir.Linked, dir.Conversations

	// 2. Labels and read state. Best effort: a stuck app state must not stop
	//    the rest of the sync, but it must not be hidden either.
	// Pressing Sinkron is an explicit request to re-read everything, so this is
	// the one path allowed to clear app-state versions and force a recovery.
	outcome := s.syncAppState(ctx, true)
	res.AppStateRecovering = outcome.Recovering
	res.AppStateStale = outcome.Stale
	if len(outcome.Errors) > 0 {
		err := errors.Join(outcome.Errors...)
		s.log.Warn("app state sync failed", "err", err)
		res.AppStateError = err.Error()
	}
	if len(outcome.Recovering) > 0 {
		s.log.Info("app state recovery in flight", "patches", outcome.Recovering)
	}
	if n, err := s.mgr.repo.CountWhatsAppLabels(ctx, accountID); err == nil {
		res.Labels = n
	}

	// 3. Trim anything already stored that falls outside the window.
	if opts.Prune {
		cutoff := s.windowStart()
		// Collect the object keys before the rows go, otherwise the files are
		// orphaned in the bucket with nothing left pointing at them.
		keys, err := s.mgr.repo.StoragePathsOlderThan(ctx, accountID, cutoff)
		if err != nil {
			s.log.Warn("collect expiring media", "err", err)
		}

		pruned, err := s.mgr.repo.PruneMessagesOlderThan(ctx, accountID, cutoff)
		if err != nil {
			s.log.Warn("prune old messages", "err", err)
		} else {
			res.Pruned = pruned
			// The message rows are gone, so whatever still references these
			// files is something outside the pruned window and keeps them.
			s.mgr.releaseAndRemove(ctx, keys, nil)
		}
	}

	// 4. Ask the phone for history only if the window is not already covered —
	//    unless the caller is repairing a gap, which the coverage check cannot
	//    see (see SyncOptions.ForceHistory).
	pending, err := s.requestHistoryIfNeeded(ctx, opts.ForceHistory)
	if err != nil {
		s.log.Warn("history sync request failed", "err", err)
	}
	res.HistoryPending = pending

	if err := s.mgr.repo.MarkAccountSynced(ctx, accountID, days); err != nil {
		s.log.Warn("mark account synced", "err", err)
	}
	m.broadcastAccount(s.WorkspaceID, accountID)

	return res, nil
}

// SyncDirectory is the lighter pass run automatically after connecting.
func (s *Session) SyncDirectory(ctx context.Context) error {
	_, err := s.syncDirectory(ctx)
	return err
}

// windowStart is the oldest timestamp this session will store.
func (s *Session) windowStart() time.Time {
	return time.Now().Add(-time.Duration(s.syncWindow()) * 24 * time.Hour)
}

// syncDirectory mirrors whatsmeow's contact store and joined groups into
// Postgres. Personal chats are deliberately NOT turned into conversations here:
// an address book entry is not an inbox thread. Those appear when a message
// arrives or when history sync delivers one.
func (s *Session) syncDirectory(ctx context.Context) (*SyncResult, error) {
	res := &SyncResult{}

	contacts, err := s.client.Store.Contacts.GetAllContacts(ctx)
	if err != nil {
		return nil, fmt.Errorf("read contact store: %w", err)
	}
	for jid, info := range contacts {
		if ctx.Err() != nil {
			return res, ctx.Err()
		}
		bare := jid.ToNonAD()
		phone := s.contactPhone(ctx, bare)

		// An address book entry with no resolvable number is skipped.
		//
		// WhatsApp lists the same saved contact twice while it migrates people
		// onto LIDs: once by number, once by LID. Importing both produced two
		// rows for one person that nothing could ever reconcile, because a LID
		// this account has never actually exchanged a message with is not in
		// whatsmeow's mapping and so has no number to unify on. That is what
		// "adtyptra" appearing twice was, once as 6282261412893 and once as the
		// digits of its own LID.
		//
		// "Saved contact" means a number was saved, so the numbered row is the
		// real one and always present. Somebody who genuinely exists only as a
		// LID is not in the address book at all: they turn up as a group member
		// or a message, and those paths create the contact with the address
		// they actually arrived under.
		if phone == "" && bare.Server == types.HiddenUserServer {
			continue
		}

		if _, err := s.mgr.repo.UpsertContact(ctx, repository.UpsertContactInput{
			WorkspaceID:  s.WorkspaceID,
			AccountID:    s.AccountID,
			JID:          bare.String(),
			PhoneNumber:  phone,
			Name:         sanitizeDisplayName(info.FullName),
			PushName:     sanitizeDisplayName(info.PushName),
			BusinessName: sanitizeDisplayName(info.BusinessName),
			IsBusiness:   info.BusinessName != "",
		}); err != nil {
			s.log.Warn("sync contact", "jid", bare.String(), "err", err)
			continue
		}
		res.Contacts++
	}

	// Threads created from a live message often predate the contact that names
	// them; wire them up now that the address book is fresh.
	if linked, err := s.mgr.repo.LinkConversationsToContacts(ctx, s.AccountID); err != nil {
		s.log.Warn("link conversations to contacts", "err", err)
	} else {
		res.Linked = int(linked)
	}

	groups, err := s.client.GetJoinedGroups(ctx)
	if err != nil {
		s.log.Warn("fetch joined groups", "err", err)
		return res, nil // contacts already synced; a group failure is not fatal
	}

	// Record the membership before walking the list. GetJoinedGroups is the only
	// authoritative answer to "which groups am I actually in", and history sync
	// has meanwhile created threads for groups we left months ago.
	joined := make([]string, 0, len(groups))
	for _, g := range groups {
		joined = append(joined, g.JID.String())
	}
	if _, err := s.mgr.repo.MarkJoinedGroups(ctx, s.AccountID, joined); err != nil {
		s.log.Warn("mark joined groups", "err", err)
	}

	for _, g := range groups {
		if ctx.Err() != nil {
			return res, ctx.Err()
		}
		s.groupNames.Store(g.JID.String(), g.Name)

		convID, err := s.mgr.repo.UpsertConversation(ctx, repository.UpsertConversationInput{
			WorkspaceID: s.WorkspaceID,
			AccountID:   s.AccountID,
			ChatJID:     g.JID.String(),
			Type:        models.ConversationTypeGroup,
			Name:        sanitizeDisplayName(g.Name),
		})
		if err != nil {
			s.log.Warn("sync group", "jid", g.JID.String(), "err", err)
			continue
		}
		res.Groups++
		res.Conversations++

		own := s.ownJIDs()
		selfAdmin := false
		members := make([]repository.GroupMember, 0, len(g.Participants))
		for _, p := range g.Participants {
			jid := p.JID.ToNonAD().String()
			isAdmin := p.IsAdmin || p.IsSuperAdmin
			members = append(members, repository.GroupMember{
				JID:         jid,
				PhoneNumber: participantPhone(p),
				DisplayName: sanitizeDisplayName(p.DisplayName),
				IsAdmin:     isAdmin,
			})
			if !isAdmin {
				continue
			}
			// Compared against both of our addresses, and against each of the
			// participant's, because WhatsApp may list either side by number or
			// by LID and the two do not have to agree.
			for _, o := range own {
				if jid == o || p.PhoneNumber.ToNonAD().String() == o || p.LID.ToNonAD().String() == o {
					selfAdmin = true
				}
			}
		}

		// Stored during the ordinary sync, not only when the group panel is
		// opened: the admin flag decides whether the management controls appear
		// at all, and having to press refresh on each group first would make
		// them look missing.
		if err := s.mgr.repo.SetGroupMeta(ctx, convID, repository.GroupMeta{
			Name:        sanitizeDisplayName(g.Name),
			Description: sanitizeDisplayName(g.Topic),
			TopicID:     g.TopicID,
			OwnerJID:    g.OwnerJID.ToNonAD().String(),
			SelfIsAdmin: selfAdmin,
			Announce:    g.IsAnnounce,
			Community:   g.IsParent,
		}); err != nil {
			s.log.Warn("store group meta", "jid", g.JID.String(), "err", err)
		}

		if err := s.mgr.repo.UpsertGroupMembers(ctx, convID, members); err != nil {
			s.log.Warn("sync group members", "jid", g.JID.String(), "err", err)
		}
	}

	s.mgr.hub.Broadcast(s.WorkspaceID, realtime.EventSyncProgress, map[string]any{
		"account_id": s.AccountID,
		"phase":      "directory",
		"result":     res,
	})
	return res, nil
}

// syncAppState pulls the app-state patches that carry WhatsApp Business labels,
// label-to-chat associations, and per-chat read/archive/pin state. whatsmeow
// turns each mutation into an event, which the handlers in events.go persist.
//
// Escalation order matters, and it is cheapest-first for a reason:
//
//  1. Incremental — reads only the patches added since the stored version. It
//     is fast and, crucially, leaves that version intact.
//  2. Full snapshot — clears the stored version and re-reads everything.
//  3. Plaintext recovery from the phone — last resort, takes tens of seconds
//     and blocks writes to the collection while it is in flight.
//
// Doing this the other way round is a trap worth spelling out: on an account
// whose server-side snapshot fails LTHash verification, starting with a full
// sync destroys a perfectly good local state, fails, and forces a recovery on
// *every* pass — permanently. Incremental-first keeps such an account working
// normally once a single recovery has repaired it.
//
// Note that whatsmeow discards every decoded mutation when verification fails,
// so a failing patch yields nothing at all rather than partial data. That is
// why the error is propagated to the caller instead of being logged and
// forgotten: "0 labels" and "labels could not be read" must not look the same.
func (s *Session) syncAppState(ctx context.Context, force bool) appStateOutcome {
	patches := []appstate.WAPatchName{
		appstate.WAPatchRegular,     // label definitions and associations
		appstate.WAPatchRegularHigh, // contact names
		appstate.WAPatchRegularLow,  // read state, archive, pin, mute
	}

	var out appStateOutcome
	for _, name := range patches {
		if ctx.Err() != nil {
			out.Errors = append(out.Errors, ctx.Err())
			return out
		}

		// 1. Incremental: cheap, and preserves the stored version.
		lastErr := s.client.FetchAppState(ctx, name, false, false)
		if lastErr == nil {
			continue
		}

		// 2. Full snapshot — only when the operator asked for it. It clears the
		// stored version, and on an account whose server snapshot fails
		// verification that turns a working (if stale) collection into an
		// unwritable one until the phone answers a recovery request. Automatic
		// passes must never inflict that.
		if force {
			if full := s.client.FetchAppState(ctx, name, true, false); full == nil {
				s.log.Debug("app state repaired by full sync", "patch", name, "incremental_err", lastErr)
				continue
			} else {
				lastErr = full
			}
		}

		// 3. Ask the phone for a plaintext copy, which skips verification
		// entirely. Declines on its own if usable state already exists and this
		// is not a forced pass.
		s.log.Warn("app state verification failed", "patch", name, "err", lastErr, "force", force)

		sent, recErr := s.requestAppStateRecovery(ctx, name, force)
		if recErr != nil {
			out.Errors = append(out.Errors,
				fmt.Errorf("%s: %w (permintaan pemulihan juga gagal: %v)", name, lastErr, recErr))
			continue
		}
		if sent {
			out.Recovering = append(out.Recovering, string(name))
			continue
		}
		// Recovery was declined or throttled: the collection keeps whatever
		// state it already had. Stale, but readable and writable.
		out.Stale = append(out.Stale, string(name))
	}
	return out
}

// appStateOutcome separates "asked the phone to fix it" from "genuinely broken".
type appStateOutcome struct {
	// Recovering names the collections whose plaintext copy was requested. The
	// data arrives asynchronously, so this is a "check back shortly", not an
	// error.
	Recovering []string
	// Stale names collections that could not be refreshed but kept usable
	// state. Reads may lag; writes still work. Deliberately not an error.
	Stale []string
	// Errors holds failures with no path forward.
	Errors []error
}

// requestAppStateRecovery asks the primary device to send an unencrypted copy
// of one app-state collection.
//
// This is the documented way out of an unverifiable snapshot: the phone answers
// with a peer message that whatsmeow decodes without MAC verification, then
// dispatches as ordinary LabelEdit / MarkChatAsRead / ... events, which land in
// the handlers in events.go. The reply is asynchronous, so this returns as soon
// as the request is on the wire.
//
// The stored version is cleared first because whatsmeow ignores a recovery
// snapshot that is not newer than what it already has.
//
// Note the deliberate choice of request: BuildFatalAppStateExceptionNotification
// would also reset the collection, but it logs out every linked device to do it.
// This one does not.
// recoveryGateTTL releases the write gate even if the phone never answers.
//
// A recovery request is not guaranteed a reply — WhatsApp rate-limits them, and
// a backgrounded phone may simply ignore one. Without an expiry the gate would
// block every label write for the rest of the session, which is a far worse
// failure than letting a write through and surfacing whatever error it hits.
const recoveryGateTTL = 90 * time.Second

// requestAppStateRecovery asks the phone for a plaintext copy of a collection.
//
// The stored version is cleared only when `force` is set, and that distinction
// is the whole point:
//
//   - whatsmeow ignores a recovery snapshot that is not newer than the version
//     it already holds. When the phone has moved on — which is exactly the case
//     after someone edits a label there — its snapshot IS newer, so it is
//     accepted with the local version left untouched.
//   - Clearing the version is therefore only needed to force a re-read of state
//     we already have, i.e. when the operator presses Sinkron. And it is not
//     free: a cleared version makes the collection unwritable until the phone
//     answers, because the server rejects patches built on a version it does
//     not recognise.
//
// Getting this backwards costs one of the two directions: clear it always and
// label writes break for a minute at a time; never clear it and a forced
// re-read silently does nothing.
//
// The bool reports whether a request was actually sent, so callers can log the
// truth instead of announcing a request the cooldown swallowed.
func (s *Session) requestAppStateRecovery(ctx context.Context, name appstate.WAPatchName, force bool) (bool, error) {
	// Two paths reach here for the same failure — the AppStateSyncError event
	// and the reconciliation pass — and a wedged collection fails on every
	// notification besides. The cooldown lives here, not in the callers, so
	// none of them can bypass it and make the phone dump the whole collection
	// twice over.
	if !s.claimRecoverySlot(name) {
		s.log.Debug("recovery already requested recently, skipping", "patch", name)
		return false, nil
	}

	if force {
		// Only a forced re-read pays the price: writes are gated until the
		// snapshot lands, because the cleared version makes the server reject
		// patches with `409 conflict`.
		s.markRecovering(name)

		if err := s.client.Store.AppState.DeleteAppStateVersion(ctx, string(name)); err != nil {
			s.markRecovered(name)
			return false, fmt.Errorf("reset stored version: %w", err)
		}
	}

	if _, err := s.client.SendPeerMessage(ctx, whatsmeow.BuildAppStateRecoveryRequest(name)); err != nil {
		if force {
			s.markRecovered(name)
		}
		return false, fmt.Errorf("send recovery request: %w", err)
	}

	s.log.Info("app state recovery requested", "patch", name, "forced", force)
	return true, nil
}

// markRecovering opens a gate that write paths wait on, with an expiry so a
// phone that never answers cannot block writes forever.
func (s *Session) markRecovering(name appstate.WAPatchName) {
	if _, loaded := s.recoveryGates.LoadOrStore(string(name), make(chan struct{})); loaded {
		return // already gated; the existing timer still owns the release
	}
	time.AfterFunc(recoveryGateTTL, func() {
		if _, still := s.recoveryGates.Load(string(name)); still {
			s.log.Warn("app state recovery timed out, releasing write gate", "patch", name)
			s.markRecovered(name)
		}
	})
}

// markRecovered releases anything waiting for this collection.
func (s *Session) markRecovered(name appstate.WAPatchName) {
	if raw, ok := s.recoveryGates.LoadAndDelete(string(name)); ok {
		if gate, _ := raw.(chan struct{}); gate != nil {
			close(gate)
		}
	}
}

// waitAppStateWritable blocks until a collection can accept patches again.
//
// Returns an error only when the wait times out or the caller is cancelled —
// a collection that was never recovering returns immediately. This is what
// turns "the write would have failed with a 409" into "the write happens a
// couple of seconds later", which is the behaviour the operator expects when
// they tag a chat right after connecting.
func (s *Session) waitAppStateWritable(ctx context.Context, name appstate.WAPatchName, timeout time.Duration) error {
	raw, ok := s.recoveryGates.Load(string(name))
	if !ok {
		return nil
	}
	gate, _ := raw.(chan struct{})
	if gate == nil {
		return nil
	}

	s.log.Debug("waiting for app state recovery before writing", "patch", name)
	select {
	case <-gate:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-s.done:
		return ErrNotConnected
	case <-time.After(timeout):
		return fmt.Errorf("app state %s masih dipulihkan dari HP", name)
	}
}

// claimRecoverySlot reports whether a recovery request for this collection is
// allowed right now, recording the attempt when it is.
func (s *Session) claimRecoverySlot(name appstate.WAPatchName) bool {
	key := string(name)
	now := time.Now()

	if last, ok := s.appStateRetryAt.Load(key); ok {
		if at, _ := last.(time.Time); now.Sub(at) < appStateRetryCooldown {
			return false
		}
	}
	s.appStateRetryAt.Store(key, now)
	return true
}

// requestHistoryIfNeeded asks the phone to resend history, but only when the
// window is not already covered.
//
// BuildHistorySyncRequest pages *backwards* from a known message, so asking
// while we already hold messages reaching past the window start would only
// fetch older messages that the ingest filter immediately discards.
func (s *Session) requestHistoryIfNeeded(ctx context.Context, force bool) (bool, error) {
	own := s.client.Store.ID
	if own == nil {
		return false, ErrNotConnected
	}

	// The anchor decides which way the phone pages.
	//
	// Normally we page back from the oldest message we hold, extending the
	// window further into the past. A forced repair pages back from the newest
	// instead, so the phone re-delivers the recent stretch — which is where a
	// gap left by lost messages actually is.
	anchor, err := s.mgr.repo.OldestMessage(ctx, s.AccountID)
	if err != nil {
		return false, err
	}

	if force {
		newest, err := s.mgr.repo.NewestMessage(ctx, s.AccountID)
		if err != nil {
			return false, err
		}
		anchor = newest
		s.log.Info("forcing history re-request to repair gaps")
	} else {
		start := s.windowStart()
		if anchor != nil && anchor.Timestamp.Before(start) {
			s.log.Debug("history already covers the window, skipping request",
				"oldest", anchor.Timestamp, "window_start", start)
			return false, nil
		}
	}

	// With no anchor at all, start from now.
	info := &types.MessageInfo{
		MessageSource: types.MessageSource{Chat: *own, Sender: *own, IsFromMe: true},
		ID:            s.client.GenerateMessageID(),
		Timestamp:     time.Now(),
	}
	if anchor != nil {
		chat, err := types.ParseJID(anchor.ChatJID)
		if err == nil {
			info.Chat = chat
			info.Sender = chat
			info.IsFromMe = anchor.FromMe
			info.ID = anchor.WAMessageID
			info.Timestamp = anchor.Timestamp
			if anchor.SenderJID != "" {
				if sender, err := types.ParseJID(anchor.SenderJID); err == nil {
					info.Sender = sender
				}
			}
		}
	}

	msg := s.client.BuildHistorySyncRequest(info, 200)
	if msg == nil {
		return false, errors.New("could not build history sync request")
	}

	// A history-sync request is a peer message: it targets our own devices.
	if _, err := s.client.SendMessage(ctx, *own, msg, whatsmeow.SendRequestExtra{Peer: true}); err != nil {
		return false, err
	}
	return true, nil
}

// handleHistorySync persists a blob of history pushed by the phone.
//
// Messages older than the sync window are dropped. Conversations are still
// created and stamped with WhatsApp's own activity time, so a chat whose newest
// message predates the window keeps its position in the inbox instead of
// sinking to the bottom.
func (s *Session) handleHistorySync(evt *events.HistorySync) {
	if evt.Data == nil {
		return
	}
	ctx, cancel := context.WithTimeout(s.mgr.rootCtx, 10*time.Minute)
	defer cancel()

	cutoff := s.windowStart()
	var stored, skipped, touched int

	// Every message written below is stamped with this batch. It is the
	// evidence the lead classifier reads: without it, a seven-day backfill of
	// old conversations is indistinguishable from a very good week of new ones,
	// and every one of those contacts would be counted as a fresh lead.
	batchID, err := s.mgr.repo.StartImportBatch(
		ctx, s.WorkspaceID, s.AccountID, s.syncWindow(), cutoff)
	if err != nil {
		s.log.Warn("open import batch", "err", err)
	}
	var batch *uuid.UUID
	if err == nil {
		batch = &batchID
	}
	// The recompute runs once per conversation after its whole batch has
	// landed, never per message: a week of history inserted one row at a time
	// would otherwise rebuild the same timeline a thousand times.
	touchedConvs := make([]uuid.UUID, 0, len(evt.Data.GetConversations()))

	for _, conv := range evt.Data.GetConversations() {
		if ctx.Err() != nil {
			break
		}

		chatJID, err := types.ParseJID(conv.GetID())
		if err != nil {
			continue
		}
		convType := conversationType(chatJID)

		name := sanitizeDisplayName(conv.GetName())
		if convType == models.ConversationTypeGroup && name != "" {
			s.groupNames.Store(chatJID.String(), name)
		}

		// For a 1:1 chat the contact is the chat itself. Group participants
		// already came from GetJoinedGroups, so they are not re-upserted here.
		var contactID *uuid.UUID
		if convType == models.ConversationTypePersonal {
			if id, err := s.mgr.repo.UpsertContact(ctx, repository.UpsertContactInput{
				WorkspaceID: s.WorkspaceID,
				AccountID:   s.AccountID,
				JID:         chatJID.String(),
				PhoneNumber: s.contactPhone(ctx, chatJID),
				Name:        name,
			}); err == nil {
				contactID = &id
			}
		}

		convID, err := s.mgr.repo.UpsertConversation(ctx, repository.UpsertConversationInput{
			WorkspaceID: s.WorkspaceID,
			AccountID:   s.AccountID,
			ChatJID:     chatJID.String(),
			PNJID:       s.phoneJID(ctx, chatJID),
			Type:        convType,
			Name:        name,
			ContactID:   contactID,
		})
		if err != nil {
			s.log.Warn("history conversation", "jid", chatJID.String(), "err", err)
			continue
		}
		touched++

		inputs := make([]repository.InsertMessageInput, 0, len(conv.GetMessages()))
		// Media found while walking the batch, keyed by WhatsApp message id.
		// The rows it belongs to do not exist yet, so it is attached after the
		// insert rather than during it.
		historyMedia := map[string]*mediaPayload{}
		historyPolls := map[string]*waE2E.PollCreationMessage{}
		for _, hm := range conv.GetMessages() {
			webMsg := hm.GetMessage()
			if webMsg == nil {
				continue
			}
			// Check the timestamp before the expensive decrypt/parse.
			if ts := int64(webMsg.GetMessageTimestamp()); ts > 0 &&
				time.Unix(ts, 0).Before(cutoff) {
				skipped++
				continue
			}

			parsed, err := s.client.ParseWebMessage(chatJID, webMsg)
			if err != nil {
				continue
			}
			if parsed.Info.Timestamp.Before(cutoff) {
				skipped++
				continue
			}

			input, ok := s.buildMessageInput(parsed, convID, "")
			if !ok {
				continue
			}
			if payload := extractMedia(parsed.Message); payload != nil {
				historyMedia[parsed.Info.ID] = payload
			}
			if poll := pollCreation(parsed.Message); poll != nil {
				historyPolls[parsed.Info.ID] = poll
			}
			input.ImportBatchID = batch
			inputs = append(inputs, input)
		}

		n, err := s.mgr.repo.InsertMessagesBackfill(ctx, inputs)
		if err != nil {
			s.log.Warn("history batch insert", "jid", chatJID.String(), "err", err)
		}
		stored += n

		// Historical media is recorded but not fetched. The thumbnail WhatsApp
		// embeds is enough to render the bubble; the full file is downloaded on
		// first open, so a seven-day backfill does not pull down every photo
		// the account has ever received.
		s.attachHistoryMedia(ctx, historyMedia)
		s.attachHistoryPolls(ctx, historyPolls)

		// The backfill path suppresses the per-row trigger, so the head is
		// recomputed once for the whole batch instead of once per message.
		if n > 0 {
			if err := s.mgr.repo.RefreshConversationHead(ctx, convID); err != nil {
				s.log.Warn("refresh conversation head", "jid", chatJID.String(), "err", err)
			}
		}

		// WhatsApp's own read/archive/pin state is authoritative and must be
		// applied after the inserts, which is what the backfill flag on the
		// trigger makes safe.
		state := repository.WAConversationState{
			AccountID:    s.AccountID,
			ChatJID:      chatJID.String(),
			UnreadCount:  int(conv.GetUnreadCount()),
			MarkedUnread: conv.GetMarkedAsUnread(),
			Archived:     conv.GetArchived(),
			Pinned:       conv.GetPinned() > 0,
		}
		if ts := conv.GetConversationTimestamp(); ts > 0 {
			state.ActivityAt = time.Unix(int64(ts), 0)
		} else if ts := conv.GetLastMsgTimestamp(); ts > 0 {
			state.ActivityAt = time.Unix(int64(ts), 0)
		}
		if err := s.mgr.repo.ApplyWAConversationState(ctx, state); err != nil {
			s.log.Warn("apply conversation state", "jid", chatJID.String(), "err", err)
		}

		if n > 0 {
			touchedConvs = append(touchedConvs, convID)
		}
	}

	if batch != nil {
		if err := s.mgr.repo.FinishImportBatch(ctx, *batch, touched, stored, skipped, ""); err != nil {
			s.log.Warn("close import batch", "err", err)
		}
	}

	// Reporting rows are rebuilt only after the batch is closed, so the
	// classifier sees the import stamp that has just been written rather than
	// deciding on a half-imported timeline.
	for _, id := range touchedConvs {
		if ctx.Err() != nil {
			break
		}
		s.mgr.RefreshConversationMetrics(ctx, s.WorkspaceID, id)
	}

	s.log.Info("history sync stored",
		"type", evt.Data.GetSyncType(),
		"conversations", touched,
		"messages", stored,
		"skipped_out_of_window", skipped,
		"window_days", s.syncWindow())

	s.mgr.hub.Broadcast(s.WorkspaceID, realtime.EventSyncProgress, map[string]any{
		"account_id": s.AccountID,
		"phase":      "history",
		"result": SyncResult{
			WindowDays:    s.syncWindow(),
			Conversations: touched,
			Messages:      stored,
			Skipped:       skipped,
		},
	})
}
