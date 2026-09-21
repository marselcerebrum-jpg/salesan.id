package wa

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.mau.fi/whatsmeow/appstate"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"

	"github.com/salesan/omnichannel/backend/internal/realtime"
	"github.com/salesan/omnichannel/backend/internal/repository"
)

// Label sync states surfaced to the UI.
const (
	LabelSyncIdle    = "idle"
	LabelSyncSyncing = "syncing"
	LabelSyncSynced  = "synced"
	LabelSyncFailed  = "failed"
)

// waLabelPalette approximates the fixed colour wheel WhatsApp Business offers
// for labels. The wire format is a palette index, not a colour value, so the
// index is what round-trips; these hexes are only for rendering.
var waLabelPalette = []string{
	"#FF9485", "#64C4FF", "#FFD429", "#DB6FDF", "#7CCFAD",
	"#5AB6F5", "#FF8B8B", "#C4A2FF", "#93CE7A", "#FFB347",
	"#8FD7E8", "#F5A3C7", "#B4B4FF", "#FFCC80", "#A5D6A7",
	"#EF9A9A", "#CE93D8", "#80DEEA", "#D4C36A", "#BCAAA4",
}

func waLabelColor(index int32) string {
	if index < 0 {
		index = 0
	}
	return waLabelPalette[int(index)%len(waLabelPalette)]
}

// waLabelColorIndex maps a hex colour back onto WhatsApp's palette. An unknown
// colour falls back to the first slot: the exact shade matters far less than
// the label existing on both sides.
func waLabelColorIndex(hex string) int32 {
	for i, c := range waLabelPalette {
		if c == hex {
			return int32(i)
		}
	}
	return 0
}

// --- inbound: phone -> web ---------------------------------------------------

// handleLabelEdit mirrors a label definition created, renamed, recoloured or
// deleted on the phone.
func (s *Session) handleLabelEdit(evt *events.LabelEdit) {
	ctx, cancel := context.WithTimeout(context.Background(), eventTimeout)
	defer cancel()

	name := sanitizeDisplayName(evt.Action.GetName())
	deleted := evt.Action.GetDeleted()

	// Idempotency: the same mutation is replayed on reconnect, on snapshot
	// recovery, and as an echo of a change this app pushed. The key folds in
	// the timestamp so a genuinely new edit is never mistaken for a replay.
	key := fmt.Sprintf("edit:%s:%d:%s:%t", evt.LabelID, evt.Timestamp.UnixMilli(), name, deleted)
	fresh, err := s.mgr.repo.ClaimLabelEvent(ctx, s.AccountID, key)
	if err != nil {
		s.log.Warn("claim label event", "err", err)
	} else if !fresh {
		s.log.Debug("label edit already applied, skipping", "label_id", evt.LabelID)
		return
	}

	if name == "" && !deleted {
		// WhatsApp ships unnamed built-ins; a placeholder keeps chats carrying
		// that label from losing their tag.
		name = "Label " + evt.LabelID
	}

	colorIndex := int(evt.Action.GetColor())
	label, changed, err := s.mgr.repo.UpsertWALabel(ctx, repository.UpsertWALabelInput{
		WorkspaceID: s.WorkspaceID,
		AccountID:   s.AccountID,
		WALabelID:   evt.LabelID,
		Name:        name,
		Color:       waLabelColor(evt.Action.GetColor()),
		ColorIndex:  colorIndex,
		Deleted:     deleted,
		Timestamp:   evt.Timestamp,
	})
	if err != nil {
		s.log.Warn("apply label definition", "label_id", evt.LabelID, "err", err)
		return
	}
	if !changed {
		// A newer mutation is already stored; WhatsApp's latest wins.
		s.log.Debug("label edit is older than stored state, ignored", "label_id", evt.LabelID)
		return
	}

	s.log.Info("label from phone applied",
		"label_id", evt.LabelID, "name", name, "deleted", deleted)

	// The history records only genuine changes: `changed` is false for a replay
	// or an older mutation, and both returned above. admin_id stays null —
	// this came from the phone, and WhatsApp does not say who was holding it.
	if label != nil || deleted {
		eventType := repository.LabelEventUpdated
		if deleted {
			eventType = repository.LabelEventDeleted
		}
		if err := s.recordPhoneLabelDefinition(ctx, evt.LabelID, eventType, evt.Timestamp); err != nil {
			s.log.Warn("record label definition history", "label_id", evt.LabelID, "err", err)
		}
	}

	if !deleted && label != nil {
		s.drainPendingLabels(ctx, evt.LabelID)
	}
	s.broadcastLabels(ctx, "label.changed")
}

// recordPhoneLabelDefinition appends a definition change made on the phone.
//
// The label is looked up by its WhatsApp id rather than passed in, because a
// deletion returns no row: the tombstone is still there and still carries the
// name the history needs to freeze.
func (s *Session) recordPhoneLabelDefinition(
	ctx context.Context,
	waLabelID, eventType string,
	at time.Time,
) error {
	label, err := s.mgr.repo.GetLabelByWAID(ctx, s.AccountID, waLabelID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil
		}
		return err
	}
	_, err = s.mgr.repo.RecordLabelEvent(ctx, repository.LabelEventInput{
		AccountID:  s.AccountID,
		EventType:  eventType,
		ToLabelID:  &label.ID,
		Source:     repository.ChangeSourceWhatsApp,
		OccurredAt: at,
		EventKey:   fmt.Sprintf("def:%s:%s:%d", eventType, waLabelID, at.UnixMilli()),
	})
	return err
}

// handleLabelAssociation attaches or detaches a label on a chat, as done on the
// phone.
func (s *Session) handleLabelAssociation(evt *events.LabelAssociationChat) {
	ctx, cancel := context.WithTimeout(context.Background(), eventTimeout)
	defer cancel()

	chat := evt.JID.ToNonAD().String()
	labeled := evt.Action.GetLabeled()

	key := fmt.Sprintf("assoc:%s:%s:%d:%t", evt.LabelID, chat, evt.Timestamp.UnixMilli(), labeled)
	fresh, err := s.mgr.repo.ClaimLabelEvent(ctx, s.AccountID, key)
	if err != nil {
		s.log.Warn("claim label association event", "err", err)
	} else if !fresh {
		return
	}

	applied, err := s.mgr.repo.SetChatLabelByWAID(ctx, s.AccountID, chat, evt.LabelID, labeled)
	if err != nil {
		s.log.Warn("apply label association", "label_id", evt.LabelID, "err", err)
		return
	}
	if !applied {
		// The definition or the chat has not arrived yet; park it for replay.
		if labeled {
			s.parkPendingLabel(evt.LabelID, chat)
		}
		s.log.Debug("label association parked", "label_id", evt.LabelID, "chat", chat)
		return
	}
	s.broadcastConversation(ctx, chat)
}

// parkPendingLabel remembers a chat awaiting its label definition.
func (s *Session) parkPendingLabel(waLabelID, chatJID string) {
	existing, _ := s.pendingLabels.LoadOrStore(waLabelID, []string{})
	queued, _ := existing.([]string)
	for _, jid := range queued {
		if jid == chatJID {
			return
		}
	}
	s.pendingLabels.Store(waLabelID, append(queued, chatJID))
}

// drainPendingLabels applies every association that was waiting on this label.
func (s *Session) drainPendingLabels(ctx context.Context, waLabelID string) {
	raw, ok := s.pendingLabels.LoadAndDelete(waLabelID)
	if !ok {
		return
	}
	queued, _ := raw.([]string)

	applied := 0
	for _, chat := range queued {
		ok, err := s.mgr.repo.SetChatLabelByWAID(ctx, s.AccountID, chat, waLabelID, true)
		if err != nil {
			s.log.Warn("replay label association", "label_id", waLabelID, "chat", chat, "err", err)
			continue
		}
		if ok {
			applied++
			s.broadcastConversation(ctx, chat)
		}
	}
	if applied > 0 {
		s.log.Info("replayed parked label associations",
			"label_id", waLabelID, "applied", applied, "queued", len(queued))
	}
}

// broadcastLabels pushes the account's current label set to the browser.
func (s *Session) broadcastLabels(ctx context.Context, reason string) {
	labels, err := s.mgr.repo.ListLabels(ctx, s.WorkspaceID, &s.AccountID)
	if err != nil {
		s.log.Warn("list labels for broadcast", "err", err)
		return
	}
	s.mgr.hub.Broadcast(s.WorkspaceID, realtime.EventLabelsUpdated, map[string]any{
		"account_id": s.AccountID,
		"reason":     reason,
		"labels":     labels,
	})
}

// --- outbound: web -> phone --------------------------------------------------

// LabelPush reports whether a change made in this app reached WhatsApp.
type LabelPush struct {
	// PushedToPhone is false when the change is stored locally but WhatsApp
	// does not know about it — the account is offline, or the patch failed.
	PushedToPhone bool   `json:"pushed_to_phone"`
	Reason        string `json:"reason,omitempty"`
}

func offline(reason string) *LabelPush { return &LabelPush{Reason: reason} }

// labelWriteWait bounds how long a write waits for a collection being
// recovered. Recovery normally completes in two to three seconds; beyond this
// the operator is better served by a clear failure than a hanging request.
const labelWriteWait = 20 * time.Second

// awaitWritable holds a label write until WhatsApp will accept patches for the
// `regular` collection.
//
// Recovery clears the local version, and the server rejects any patch built on
// a version it does not recognise with `409 conflict`. Without this wait, a
// label change made in the seconds after connecting fails for a reason that has
// nothing to do with the change itself.
func (s *Session) awaitWritable(ctx context.Context) error {
	return s.waitAppStateWritable(ctx, appstate.WAPatchRegular, labelWriteWait)
}

// session returns the connected session for an account, or a reason it is not
// usable.
func (m *Manager) connectedSession(accountID uuid.UUID) (*Session, string) {
	s, ok := m.Session(accountID)
	if !ok || !s.IsConnected() {
		return nil, "Akun WhatsApp tidak terhubung — perubahan tersimpan di sini, belum dikirim ke HP."
	}
	return s, ""
}

// PushLabelDefinition creates, renames or deletes a label on WhatsApp.
//
// A label created in this app has no WhatsApp id until this runs; the id it is
// given here becomes its shared identity from then on.
func (m *Manager) PushLabelDefinition(
	ctx context.Context,
	workspaceID, labelID uuid.UUID,
	accountID uuid.UUID,
	deleted bool,
) (*LabelPush, error) {
	label, err := m.repo.GetLabel(ctx, workspaceID, labelID)
	if err != nil && !deleted {
		return nil, err
	}

	s, reason := m.connectedSession(accountID)
	if s == nil {
		return offline(reason), nil
	}
	if err := s.awaitWritable(ctx); err != nil {
		return offline("Label sedang disinkronkan dari HP, coba lagi sebentar."), nil
	}

	waID := ""
	name, colorIndex := "", int32(0)
	if label != nil {
		name = label.Name
		if label.ColorIndex != nil {
			colorIndex = int32(*label.ColorIndex)
		} else {
			colorIndex = waLabelColorIndex(label.Color)
		}
		if label.WALabelID != nil {
			waID = *label.WALabelID
		}
	}

	if waID == "" {
		if deleted {
			// Never existed on the phone; nothing to remove there.
			return &LabelPush{PushedToPhone: true}, nil
		}
		allocated, err := m.repo.NextWALabelID(ctx, accountID)
		if err != nil {
			return offline("Gagal mengalokasikan ID label: " + err.Error()), nil
		}
		waID = allocated
	}

	if err := s.client.SendAppState(ctx,
		appstate.BuildLabelEdit(waID, name, colorIndex, deleted)); err != nil {
		s.log.Warn("push label definition", "label", name, "err", err)
		return offline("WhatsApp menolak perubahan: " + err.Error()), nil
	}

	if !deleted {
		if err := m.repo.AdoptWALabelID(ctx, labelID, accountID, waID, int(colorIndex)); err != nil {
			s.log.Warn("record adopted label id", "err", err)
		}
	}

	s.log.Info("label definition pushed to phone",
		"wa_label_id", waID, "name", name, "deleted", deleted)
	return &LabelPush{PushedToPhone: true}, nil
}

// PushChatLabel attaches or detaches a label on a chat, on WhatsApp.
func (m *Manager) PushChatLabel(
	ctx context.Context,
	workspaceID, conversationID, labelID uuid.UUID,
	labeled bool,
) (*LabelPush, error) {
	conv, err := m.repo.GetConversation(ctx, workspaceID, conversationID)
	if err != nil {
		return nil, err
	}
	label, err := m.repo.GetLabel(ctx, workspaceID, labelID)
	if err != nil {
		return nil, err
	}

	s, reason := m.connectedSession(conv.AccountID)
	if s == nil {
		return offline(reason), nil
	}
	if err := s.awaitWritable(ctx); err != nil {
		return offline("Label sedang disinkronkan dari HP, coba lagi sebentar."), nil
	}

	chatJID, err := types.ParseJID(conv.ChatJID)
	if err != nil {
		return nil, fmt.Errorf("invalid chat jid %q: %w", conv.ChatJID, err)
	}

	// A label belonging to another account cannot be pushed here: label ids are
	// per-account, and reusing one would tag the wrong chat on the wrong phone.
	if label.AccountID != nil && *label.AccountID != conv.AccountID {
		return offline("Label ini milik akun WhatsApp lain."), nil
	}

	waID := ""
	if label.WALabelID != nil {
		waID = *label.WALabelID
	}
	if waID == "" {
		// First use of a locally created label: define it on the phone first.
		push, err := m.PushLabelDefinition(ctx, workspaceID, labelID, conv.AccountID, false)
		if err != nil {
			return nil, err
		}
		if !push.PushedToPhone {
			return push, nil
		}
		refreshed, err := m.repo.GetLabel(ctx, workspaceID, labelID)
		if err != nil {
			return nil, err
		}
		if refreshed.WALabelID == nil {
			return offline("Label belum memperoleh ID dari WhatsApp."), nil
		}
		waID = *refreshed.WALabelID
	}

	// Pushed under both of the chat's addresses, when it has two.
	//
	// A person is addressed by phone number and by LID, and a conversation here
	// is keyed by whichever form happened to arrive first. The phone indexes its
	// label associations by one of them, and from this side there is no way to
	// know which: the inbound handler already matches on `chat_jid or pn_jid`
	// precisely because either is what turns up.
	//
	// So sending only the form our row carries is a coin flip, and a lost flip
	// is silent in the worst way: WhatsApp accepts the patch and stores it under
	// a key the phone never reads, so the web shows the label, the phone shows
	// nothing, and no error is raised anywhere. One extra mutation removes the
	// guess. The unused key is an orphan entry in app state and nothing more.
	for _, jid := range s.labelChatAddresses(ctx, chatJID) {
		if err := s.client.SendAppState(ctx, appstate.BuildLabelChat(jid, waID, labeled)); err != nil {
			s.log.Warn("push chat label", "wa_label_id", waID, "chat", jid.String(), "err", err)
			return offline("WhatsApp menolak perubahan: " + err.Error()), nil
		}
		s.log.Info("chat label pushed to phone",
			"wa_label_id", waID, "chat", jid.String(), "labeled", labeled)
	}
	return &LabelPush{PushedToPhone: true}, nil
}

// labelChatAddresses lists every address this chat answers to.
//
// One entry for a group, which has only ever had one address. Up to two for a
// person: the form the conversation is stored under, and the other form
// whatsmeow's own mapping knows about.
func (s *Session) labelChatAddresses(ctx context.Context, chat types.JID) []types.JID {
	out := []types.JID{chat.ToNonAD()}
	add := func(other types.JID) {
		if other.IsEmpty() || other.ToNonAD() == out[0] {
			return
		}
		out = append(out, other.ToNonAD())
	}

	switch chat.Server {
	case types.DefaultUserServer, types.LegacyUserServer:
		if lid, err := s.client.Store.LIDs.GetLIDForPN(ctx, chat.ToNonAD()); err == nil {
			add(lid)
		}
	case types.HiddenUserServer:
		if pn, err := s.client.Store.LIDs.GetPNForLID(ctx, chat.ToNonAD()); err == nil {
			add(pn)
		}
	}
	return out
}

// --- keeping the phone's side live ------------------------------------------

// appStateRetryCooldown throttles automatic recovery requests. A wedged
// collection fails on every notification, and each recovery costs the phone a
// full plaintext dump of the collection.
const appStateRetryCooldown = 20 * time.Second

// handleAppStateSyncError reacts to a live app-state notification that failed
// to decode.
//
// On accounts whose LTHash is permanently broken this fires on every label
// change made from the phone, and whatsmeow discards the mutations — so without
// this, a label added on the phone would stay invisible until someone pressed
// Sinkron. Requesting the plaintext copy turns that into a couple of seconds.
func (s *Session) handleAppStateSyncError(evt *events.AppStateSyncError) {
	s.log.Warn("app state sync failed", "patch", evt.Name, "full_sync", evt.FullSync, "err", evt.Error)

	ctx, cancel := context.WithTimeout(s.mgr.rootCtx, eventTimeout)
	defer cancel()

	_ = s.mgr.repo.SetLabelSyncState(ctx, s.AccountID, LabelSyncSyncing, "")
	s.broadcastSyncState(ctx, LabelSyncSyncing, "")

	// Automatic repair: declines if usable state already exists, so a failed
	// notification never costs the account its ability to write labels.
	sent, err := s.requestAppStateRecovery(ctx, evt.Name, false)
	if err != nil {
		s.log.Warn("automatic app state recovery failed", "patch", evt.Name, "err", err)
		_ = s.mgr.repo.SetLabelSyncState(ctx, s.AccountID, LabelSyncFailed, err.Error())
		s.broadcastSyncState(ctx, LabelSyncFailed, err.Error())
		return
	}
	if sent {
		s.log.Info("automatic app state recovery requested", "patch", evt.Name)
	}
}

// --- reconciliation ----------------------------------------------------------

// labelReconcileInterval is how often each connected account re-reads its label
// set from WhatsApp.
//
// Live mutations are the fast path and normally arrive within a second or two;
// this is the safety net for the cases they cannot cover — a mutation dropped
// while the socket was down, an app-state notification that failed silently, or
// a phone that never announced a change at all.
const labelReconcileInterval = 10 * time.Minute

// reconcileLabels re-reads the account's app state and republishes the result.
//
// WhatsApp is the source of truth: whatever it reports replaces what we hold,
// with the newest mutation timestamp winning any conflict. Running it is
// idempotent, so a scheduled pass and a manual Sinkron can overlap harmlessly.
func (s *Session) reconcileLabels(ctx context.Context, reason string) {
	if !s.IsConnected() {
		return
	}

	_ = s.mgr.repo.SetLabelSyncState(ctx, s.AccountID, LabelSyncSyncing, "")
	s.broadcastSyncState(ctx, LabelSyncSyncing, "")

	// Never forced: an automatic pass must not clear app-state versions, which
	// would leave the collection unwritable while it waits on the phone.
	outcome := s.syncAppState(ctx, false)

	switch {
	case len(outcome.Errors) > 0:
		detail := errorsJoin(outcome.Errors)
		s.log.Warn("label reconciliation failed", "reason", reason, "err", detail)
		_ = s.mgr.repo.SetLabelSyncState(ctx, s.AccountID, LabelSyncFailed, detail)
		s.broadcastSyncState(ctx, LabelSyncFailed, detail)

	case len(outcome.Recovering) > 0:
		// Recovery is in flight; AppStateSyncComplete flips the state to synced
		// once the phone answers.
		//
		// Except it may never answer. Nothing was written here at all, which
		// left the badge reading "Menyinkronkan label" with no error and no end:
		// six minutes, an hour, until the process restarted. That is the worst
		// thing an indicator can do, because the labels on screen were correct
		// and usable the whole time and it said otherwise.
		//
		// So the wait is bounded. Past the grace period the badge settles to
		// synced, which is the honest reading: what is on screen is the set
		// WhatsApp last gave us, and a refresh is still outstanding. The
		// timestamp is deliberately NOT cleared here, so every later pass that
		// still finds recovery outstanding settles immediately instead of
		// flapping between the two states every ten minutes.
		s.log.Info("label reconciliation awaiting phone recovery",
			"reason", reason, "patches", outcome.Recovering)

		since := s.labelRecoverySince.Load()
		if since == 0 {
			s.labelRecoverySince.Store(time.Now().Unix())
			break
		}
		if waited := time.Since(time.Unix(since, 0)); waited >= labelRecoveryGrace {
			s.log.Warn("phone has not answered the label recovery; settling the indicator",
				"reason", reason, "waited", waited.Round(time.Second))
			_ = s.mgr.repo.SetLabelSyncState(ctx, s.AccountID, LabelSyncSynced, "")
			s.broadcastSyncState(ctx, LabelSyncSynced, "")
		}

	case len(outcome.Stale) > 0:
		// Could not refresh, but the stored state is intact and writable. Say
		// synced rather than failed: the operator can still work, and claiming
		// failure here would cry wolf on every pass for a wedged account.
		s.log.Info("label reconciliation kept existing state",
			"reason", reason, "patches", outcome.Stale)
		s.labelRecoverySince.Store(0)
		_ = s.mgr.repo.SetLabelSyncState(ctx, s.AccountID, LabelSyncSynced, "")
		s.broadcastSyncState(ctx, LabelSyncSynced, "")

	default:
		s.labelRecoverySince.Store(0)
		_ = s.mgr.repo.SetLabelSyncState(ctx, s.AccountID, LabelSyncSynced, "")
		s.broadcastSyncState(ctx, LabelSyncSynced, "")
		s.broadcastLabels(ctx, "reconcile:"+reason)
	}
}

// labelRecoveryGrace is how long the indicator may claim to be syncing while
// waiting for the phone to resend its app state.
//
// Short, because the phone normally answers within seconds. Passing it does not
// mean anything is broken; it means the wait has stopped being news, and the
// reconciler settles the badge on its next pass.
const labelRecoveryGrace = 2 * time.Minute

func errorsJoin(errs []error) string {
	parts := make([]string, 0, len(errs))
	for _, e := range errs {
		parts = append(parts, e.Error())
	}
	return strings.Join(parts, "; ")
}

// startLabelReconciler runs periodic reconciliation for the lifetime of the
// session.
func (s *Session) startLabelReconciler() {
	s.mgr.wg.Add(1)
	go func() {
		defer s.mgr.wg.Done()

		ticker := time.NewTicker(labelReconcileInterval)
		defer ticker.Stop()

		for {
			select {
			case <-s.done:
				return
			case <-s.mgr.rootCtx.Done():
				return
			case <-ticker.C:
				ctx, cancel := context.WithTimeout(s.mgr.rootCtx, 3*time.Minute)
				s.reconcileLabels(ctx, "periodic")
				cancel()
			}
		}
	}()
}

// broadcastSyncState pushes the label sync indicator to the browser.
func (s *Session) broadcastSyncState(_ context.Context, state, detail string) {
	s.mgr.hub.Broadcast(s.WorkspaceID, realtime.EventLabelSyncState, map[string]any{
		"account_id": s.AccountID,
		"state":      state,
		"detail":     detail,
	})
}
