package wa

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"

	"github.com/salesan/omnichannel/backend/internal/models"
	"github.com/salesan/omnichannel/backend/internal/realtime"
	"github.com/salesan/omnichannel/backend/internal/repository"
)

// Group errors surfaced to the HTTP layer.
var (
	// ErrNotAGroup is a group action asked of a one-to-one chat.
	ErrNotAGroup = errors.New("wa: this conversation is not a group")
	// ErrNotGroupAdmin means WhatsApp will refuse the change — only admins may
	// promote, demote, remove members or edit the description.
	ErrNotGroupAdmin = errors.New("wa: only a group admin can do this")
)

// MaxGroupDescription is WhatsApp's own limit.
const MaxGroupDescription = 2048

// groupContext resolves a conversation to the group it addresses, refusing
// anything that is not one.
func (m *Manager) groupContext(
	ctx context.Context,
	workspaceID, conversationID uuid.UUID,
) (*models.Conversation, *Session, types.JID, error) {
	conv, err := m.repo.GetConversation(ctx, workspaceID, conversationID)
	if err != nil {
		return nil, nil, types.EmptyJID, err
	}
	if conv.Type != models.ConversationTypeGroup {
		return nil, nil, types.EmptyJID, ErrNotAGroup
	}

	s, ok := m.Session(conv.AccountID)
	if !ok {
		return nil, nil, types.EmptyJID, ErrSessionNotFound
	}
	if !s.IsConnected() {
		return nil, nil, types.EmptyJID, ErrNotConnected
	}

	jid, err := types.ParseJID(conv.ChatJID)
	if err != nil {
		return nil, nil, types.EmptyJID, fmt.Errorf("invalid group jid %q: %w", conv.ChatJID, err)
	}
	return conv, s, jid, nil
}

// GroupMemberAction is what to do to a participant.
type GroupMemberAction string

const (
	GroupPromote GroupMemberAction = "promote"
	GroupDemote  GroupMemberAction = "demote"
	GroupRemove  GroupMemberAction = "remove"
)

// UpdateGroupMember promotes, demotes or removes a participant.
//
// The stored admin flag decides which buttons the operator sees, but it is not
// what authorises the change — WhatsApp is. A stale flag therefore produces a
// refusal from the server rather than a silent no-op, and that refusal is
// passed through unchanged so the operator learns the real reason.
func (m *Manager) UpdateGroupMember(
	ctx context.Context,
	workspaceID, conversationID uuid.UUID,
	memberJID string,
	action GroupMemberAction,
) ([]repository.GroupMemberProfile, error) {
	conv, s, groupJID, err := m.groupContext(ctx, workspaceID, conversationID)
	if err != nil {
		return nil, err
	}

	target, err := types.ParseJID(memberJID)
	if err != nil {
		return nil, fmt.Errorf("invalid member jid %q: %w", memberJID, err)
	}
	// Removing ourselves is leaving the group, which is a different action with
	// different consequences; it is not offered here by accident.
	for _, own := range s.ownJIDs() {
		if target.ToNonAD().String() == own {
			return nil, fmt.Errorf("%w: gunakan keluar grup untuk diri sendiri", ErrNotGroupAdmin)
		}
	}

	var change whatsmeow.ParticipantChange
	switch action {
	case GroupPromote:
		change = whatsmeow.ParticipantChangePromote
	case GroupDemote:
		change = whatsmeow.ParticipantChangeDemote
	case GroupRemove:
		change = whatsmeow.ParticipantChangeRemove
	default:
		return nil, fmt.Errorf("%w: aksi %q tidak dikenal", ErrNotGroupAdmin, action)
	}

	if _, err := s.client.UpdateGroupParticipants(ctx, groupJID, []types.JID{target}, change); err != nil {
		return nil, wrapGroupError(err)
	}

	// Re-read from WhatsApp rather than patching our copy: a promotion can
	// change more than the one row, and the server's version is the truth.
	if err := s.refreshGroupInfo(ctx, groupJID, conv.ID); err != nil {
		s.log.Warn("refresh group after member change", "group", conv.ChatJID, "err", err)
	}

	s.log.Info("group membership changed",
		"group", conv.ChatJID, "member", target.String(), "action", action)
	m.broadcastGroup(ctx, workspaceID, conv.ID)

	return m.repo.GroupMembers(ctx, workspaceID, conv.ID)
}

// SetGroupDescription changes the group's description ("topic" on the wire).
func (m *Manager) SetGroupDescription(
	ctx context.Context,
	workspaceID, conversationID uuid.UUID,
	description string,
) (*models.Conversation, error) {
	conv, s, groupJID, err := m.groupContext(ctx, workspaceID, conversationID)
	if err != nil {
		return nil, err
	}

	description = strings.TrimSpace(description)
	if len([]rune(description)) > MaxGroupDescription {
		return nil, fmt.Errorf("%w: deskripsi maksimal %d karakter",
			ErrNotGroupAdmin, MaxGroupDescription)
	}

	// An empty previousID makes whatsmeow fetch the current one itself, which
	// is what WhatsApp needs to order the change against what it replaces.
	if err := s.client.SetGroupTopic(ctx, groupJID, "", "", description); err != nil {
		return nil, wrapGroupError(err)
	}

	if err := s.refreshGroupInfo(ctx, groupJID, conv.ID); err != nil {
		s.log.Warn("refresh group after description change", "group", conv.ChatJID, "err", err)
	}
	s.log.Info("group description changed", "group", conv.ChatJID)

	updated, err := m.repo.GetConversationByID(ctx, conv.ID)
	if err != nil {
		return nil, err
	}
	m.hub.Broadcast(workspaceID, realtime.EventConversationUpdate, updated)
	return updated, nil
}

// SetGroupName changes the group's subject.
func (m *Manager) SetGroupName(
	ctx context.Context,
	workspaceID, conversationID uuid.UUID,
	name string,
) (*models.Conversation, error) {
	conv, s, groupJID, err := m.groupContext(ctx, workspaceID, conversationID)
	if err != nil {
		return nil, err
	}

	name = sanitizeDisplayName(name)
	if name == "" {
		return nil, fmt.Errorf("%w: nama grup tidak boleh kosong", ErrNotGroupAdmin)
	}

	if err := s.client.SetGroupName(ctx, groupJID, name); err != nil {
		return nil, wrapGroupError(err)
	}
	if err := s.refreshGroupInfo(ctx, groupJID, conv.ID); err != nil {
		s.log.Warn("refresh group after rename", "group", conv.ChatJID, "err", err)
	}

	updated, err := m.repo.GetConversationByID(ctx, conv.ID)
	if err != nil {
		return nil, err
	}
	m.hub.Broadcast(workspaceID, realtime.EventConversationUpdate, updated)
	return updated, nil
}

// RefreshGroup re-reads a group's metadata from WhatsApp on demand, for when
// the operator opens the member panel and wants what is true now.
func (m *Manager) RefreshGroup(ctx context.Context, workspaceID, conversationID uuid.UUID) error {
	conv, s, groupJID, err := m.groupContext(ctx, workspaceID, conversationID)
	if err != nil {
		return err
	}
	if err := s.refreshGroupInfo(ctx, groupJID, conv.ID); err != nil {
		return err
	}
	m.broadcastGroup(ctx, workspaceID, conv.ID)
	return nil
}

// SyncGroupMembership re-checks which groups each of the workspace's connected
// numbers is actually in, and returns how many rows changed.
//
// Separate from the full account sync because it is cheap — one call per number
// — and because the group directory is where the mistake shows. Pressing
// "Fetch / Refresh" there should be able to correct a list that has a group
// nobody joined in it, without waiting for the next full sync.
//
// A number that is offline or fails is skipped rather than treated as having no
// groups: marking every group as left because one phone was disconnected would
// empty the screen on the strength of nothing.
func (m *Manager) SyncGroupMembership(ctx context.Context, workspaceID uuid.UUID) (int64, error) {
	m.mu.RLock()
	sessions := make([]*Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		if s.WorkspaceID == workspaceID {
			sessions = append(sessions, s)
		}
	}
	m.mu.RUnlock()

	var changed int64
	var lastErr error
	for _, s := range sessions {
		if ctx.Err() != nil {
			break
		}
		if s.client == nil || !s.client.IsConnected() {
			continue
		}
		groups, err := s.client.GetJoinedGroups(ctx)
		if err != nil {
			lastErr = err
			s.log.Warn("membership sweep: fetch joined groups", "err", err)
			continue
		}
		joined := make([]string, 0, len(groups))
		for _, g := range groups {
			joined = append(joined, g.JID.String())
		}
		n, err := m.repo.MarkJoinedGroups(ctx, s.AccountID, joined)
		if err != nil {
			lastErr = err
			continue
		}
		changed += n
	}
	if changed == 0 && lastErr != nil {
		return 0, lastErr
	}
	return changed, nil
}

// participantPhone is the participant's real number, bare, or "".
//
// WhatsApp now addresses group participants by LID, so p.JID is usually
// "<lid>@lid" — an identifier, not a number. It hands the number over
// separately in PhoneNumber, and that is the only place most participants have
// one: they have never been a contact and never sent us a message, so nothing
// else in the database knows who they are.
//
// Empty for an anonymous participant in an announcement group, where WhatsApp
// deliberately withholds the number. p.DisplayName carries an obfuscated form
// in that case ("+62∙∙∙∙∙∙∙∙∙93"), which is not used here: a number with its
// middle hidden cannot be dialled or exported, and printing it in a column of
// numbers would suggest otherwise.
func participantPhone(p types.GroupParticipant) string {
	if !p.PhoneNumber.IsEmpty() && p.PhoneNumber.User != "" {
		return p.PhoneNumber.User
	}
	if p.JID.Server == types.DefaultUserServer || p.JID.Server == types.LegacyUserServer {
		return p.JID.User
	}
	return ""
}

// refreshGroupInfo pulls the group's metadata and stores it.
//
// Also records whether *we* are an admin, which is what the interface uses to
// decide which controls to show at all.
func (s *Session) refreshGroupInfo(ctx context.Context, groupJID types.JID, conversationID uuid.UUID) error {
	info, err := s.client.GetGroupInfo(ctx, groupJID)
	if err != nil {
		return fmt.Errorf("read group info: %w", err)
	}

	own := s.ownJIDs()
	selfAdmin := false
	members := make([]repository.GroupMember, 0, len(info.Participants))
	for _, p := range info.Participants {
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
		// A participant is us under either of our two addresses, and WhatsApp
		// may list them under either — so both are compared.
		for _, o := range own {
			if jid == o || p.PhoneNumber.ToNonAD().String() == o || p.LID.ToNonAD().String() == o {
				selfAdmin = true
			}
		}
	}

	if err := s.mgr.repo.UpsertGroupMembers(ctx, conversationID, members); err != nil {
		return err
	}
	return s.mgr.repo.SetGroupMeta(ctx, conversationID, repository.GroupMeta{
		Name:        sanitizeDisplayName(info.Name),
		Description: sanitizeDisplayName(info.Topic),
		TopicID:     info.TopicID,
		OwnerJID:    info.OwnerJID.ToNonAD().String(),
		SelfIsAdmin: selfAdmin,
		Announce:    info.IsAnnounce,
	})
}

// broadcastGroup pushes the refreshed conversation so open tabs pick up the
// new member list and admin state without a reload.
func (m *Manager) broadcastGroup(ctx context.Context, workspaceID, conversationID uuid.UUID) {
	if conv, err := m.repo.GetConversationByID(ctx, conversationID); err == nil {
		m.hub.Broadcast(workspaceID, realtime.EventConversationUpdate, conv)
	}
}

// wrapGroupError turns WhatsApp's refusal into something the operator can act
// on. Its own message is kept, because "not-authorized" from the server is more
// informative than a sentence we invented.
func wrapGroupError(err error) error {
	text := err.Error()
	if strings.Contains(text, "403") ||
		strings.Contains(strings.ToLower(text), "not-authorized") ||
		strings.Contains(strings.ToLower(text), "forbidden") {
		return fmt.Errorf("%w: WhatsApp menolak — %s", ErrNotGroupAdmin, text)
	}
	return err
}
