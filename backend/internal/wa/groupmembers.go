package wa

import (
	"context"
	"time"

	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"

	"github.com/salesan/omnichannel/backend/internal/repository"
)

// Watching groups fill up and empty out.
//
// WhatsApp tells us every time somebody joins or leaves a group one of our
// numbers is in. Until now that notification was dropped on the floor and the
// member list only moved when an operator pressed Fetch, so a group could gain
// four hundred people between one fetch and the next with nothing on screen to
// say so.
//
// Listening costs nothing and asking costs a great deal: there are nine hundred
// groups, and polling them for a daily head count would be nine hundred requests
// to WhatsApp every day for information it was already volunteering.

// handleGroupInfo applies a membership change and records it.
//
// Only the arrivals and departures are acted on here. Name, topic and the other
// fields of this event belong to the group's own metadata, which is refreshed on
// its own path.
func (s *Session) handleGroupInfo(evt *events.GroupInfo) {
	if len(evt.Join) == 0 && len(evt.Leave) == 0 {
		return
	}

	ctx, cancel := context.WithTimeout(s.mgr.rootCtx, 20*time.Second)
	defer cancel()

	chatJID := evt.JID.String()
	conv, err := s.mgr.repo.GetConversationByAccountChat(ctx, s.AccountID, chatJID)
	if err != nil {
		// A group with no thread on this number is not in the directory either,
		// so there is nothing for this to be a change to.
		s.log.Debug("group membership change for an unknown thread",
			"chat", chatJID, "err", err)
		return
	}

	joinJIDs := jidStrings(evt.Join)
	leaveJIDs := jidStrings(evt.Leave)

	if err := s.mgr.repo.AddGroupMembers(ctx, conv.ID, joinJIDs); err != nil {
		s.log.Warn("add group members", "chat", chatJID, "err", err)
	}
	if err := s.mgr.repo.RemoveGroupMembers(ctx, conv.ID, leaveJIDs); err != nil {
		s.log.Warn("remove group members", "chat", chatJID, "err", err)
	}

	changes := make([]repository.GroupMemberChange, 0, len(joinJIDs)+len(leaveJIDs))
	for _, jid := range joinJIDs {
		changes = append(changes, repository.GroupMemberChange{ParticipantJID: jid})
	}
	for _, jid := range leaveJIDs {
		changes = append(changes, repository.GroupMemberChange{ParticipantJID: jid, Leaving: true})
	}

	actor := ""
	if evt.Sender != nil {
		actor = evt.Sender.String()
	}

	// WhatsApp's own timestamp, so a notification delivered late by a reconnect
	// still counts on the day it happened rather than the day we heard it.
	at := evt.Timestamp
	if at.IsZero() {
		at = time.Now().UTC()
	}

	joined, left, err := s.mgr.repo.RecordGroupMemberChanges(
		ctx, s.WorkspaceID, chatJID, s.AccountID, actor, at, changes)
	if err != nil {
		s.log.Warn("record group membership change", "chat", chatJID, "err", err)
		return
	}
	if joined == 0 && left == 0 {
		// Another of our numbers in the same group reported this already.
		return
	}
	s.log.Info("group membership changed",
		"chat", chatJID, "joined", joined, "left", left)
}

// jidStrings flattens a participant list, dropping anything empty.
func jidStrings(list []types.JID) []string {
	out := make([]string, 0, len(list))
	for _, j := range list {
		if j.IsEmpty() {
			continue
		}
		out = append(out, j.String())
	}
	return out
}

const (
	// groupSnapshotInterval is how often the head count is written down.
	//
	// Hourly rather than once at midnight. A single nightly run is a single
	// point of failure — miss it and that day has no anchor at all — and this
	// costs one aggregate query, so running it twenty-four times is cheaper
	// than the machinery needed to run it reliably once.
	groupSnapshotInterval = time.Hour
	// groupSnapshotFirstDelay lets the accounts finish connecting first, so the
	// first count of the day is not taken while the member lists are still
	// settling after a restart.
	groupSnapshotFirstDelay = 5 * time.Minute
)

// startGroupSnapshot keeps a daily head count for every group.
//
// The arrival and departure notifications are the detail, but they are not
// complete: nothing is delivered for what happened while this process was down,
// and WhatsApp does not replay it afterwards. A history built only from them
// would carry every one of those gaps forward forever. Writing the real count
// down each day means a missed notification spoils that day's tally and leaves
// every other day intact.
func (m *Manager) startGroupSnapshot() {
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()

		timer := time.NewTimer(groupSnapshotFirstDelay)
		defer timer.Stop()

		for {
			select {
			case <-m.rootCtx.Done():
				return
			case <-timer.C:
			}

			ctx, cancel := context.WithTimeout(m.rootCtx, 2*time.Minute)
			n, err := m.repo.SnapshotGroupMemberCounts(ctx)
			cancel()
			if err != nil {
				m.log.Warn("snapshot group member counts", "err", err)
			} else if n > 0 {
				m.log.Info("group member counts recorded", "groups", n)
			}

			timer.Reset(groupSnapshotInterval)
		}
	}()
}
