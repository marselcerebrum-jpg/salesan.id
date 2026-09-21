package wa

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"

	"github.com/salesan/omnichannel/backend/internal/repository"
)

// channelInvite matches the link people actually paste, in the shapes WhatsApp
// hands out: with or without the scheme, with or without a query string.
var channelInvite = regexp.MustCompile(`(?i)whatsapp\.com/channel/([A-Za-z0-9_-]+)`)

// inviteKey pulls the key out of a channel invite link, or returns "" when the
// text is not one.
func inviteKey(ref string) string {
	if m := channelInvite.FindStringSubmatch(strings.TrimSpace(ref)); len(m) == 2 {
		return m[1]
	}
	return ""
}

// Channels.
//
// Read-mostly by design. WhatsApp owns the directory and the posts; this file
// asks it what a number follows, keeps a local copy of that answer so the screen
// has something to show before the network replies, and passes the four actions
// a subscriber actually has — open, follow, unfollow, mute — straight through.
//
// Posts are never stored. They belong to somebody else's publication, they are
// not replied to, and a copy in our database would be storage and a duty of care
// bought for nothing.

// NewsletterPost is one message in a channel, as the screen shows it.
type NewsletterPost struct {
	ServerID  string    `json:"server_id"`
	Text      string    `json:"text"`
	Kind      string    `json:"kind"`
	PostedAt  time.Time `json:"posted_at"`
	ViewCount int       `json:"view_count"`
	// Reactions is emoji to count, as WhatsApp reports it.
	Reactions map[string]int `json:"reactions"`

	// HasMedia says there is a file behind the post that the browser can ask for
	// through NewsletterMedia. Kind says what it is; the bytes are never sent in
	// this list, and never stored.
	HasMedia  bool    `json:"has_media"`
	FileName  string  `json:"file_name,omitempty"`
	Mime      string  `json:"mime,omitempty"`
	Thumbnail []byte  `json:"thumbnail,omitempty"`
	// PollOptions is the list of choices, for a poll.
	PollOptions []string `json:"poll_options,omitempty"`
	// PollVotes is the count per option, in the same order, as WhatsApp reported
	// it. Null when the server's tallies could not be matched to every option —
	// the panel then says the counts are unavailable rather than drawing zeros.
	PollVotes []int `json:"poll_votes"`
	// PollSelectable is 1 for a single choice and 0 for "as many as you like",
	// the same convention the chat poll uses.
	PollSelectable int `json:"poll_selectable_count"`
}

// SyncNewsletters refreshes one number's channel directory from WhatsApp.
func (m *Manager) SyncNewsletters(ctx context.Context, accountID uuid.UUID) (int, error) {
	s, ok := m.Session(accountID)
	if !ok {
		return 0, ErrSessionNotFound
	}
	if !s.IsConnected() {
		return 0, ErrNotConnected
	}

	list, err := s.client.GetSubscribedNewsletters(ctx)
	if err != nil {
		return 0, fmt.Errorf("baca daftar saluran: %w", err)
	}

	rows := make([]repository.Newsletter, 0, len(list))
	for _, n := range list {
		rows = append(rows, newsletterRow(accountID, n))
	}
	if err := m.repo.ReplaceNewsletters(ctx, s.WorkspaceID, accountID, rows); err != nil {
		return 0, err
	}
	return len(rows), nil
}

// newsletterRow flattens WhatsApp's nested metadata into the row we store.
func newsletterRow(accountID uuid.UUID, n *types.NewsletterMetadata) repository.Newsletter {
	meta := n.ThreadMeta

	out := repository.Newsletter{
		AccountID:  accountID,
		JID:        n.ID.String(),
		Name:       meta.Name.Text,
		ViewerRole: string(types.NewsletterRoleSubscriber),
		Verified:   meta.VerificationState == types.NewsletterVerificationStateVerified,
	}
	if d := meta.Description.Text; d != "" {
		out.Description = &d
	}
	if meta.SubscriberCount > 0 {
		c := meta.SubscriberCount
		out.Subscribers = &c
	}
	if meta.Picture != nil && meta.Picture.URL != "" {
		u := meta.Picture.URL
		out.PictureURL = &u
	} else if meta.Preview.URL != "" {
		u := meta.Preview.URL
		out.PictureURL = &u
	}
	if t := meta.CreationTime.Time; !t.IsZero() {
		out.CreatedOn = &t
	}
	// ViewerMeta is absent for a channel this number only has by invite link,
	// which is exactly when assuming "owner" would be worst.
	if n.ViewerMeta != nil {
		out.ViewerRole = string(n.ViewerMeta.Role)
		out.Muted = n.ViewerMeta.Mute == types.NewsletterMuteOn
	}
	return out
}

// NewsletterPosts reads a channel's recent messages straight from WhatsApp.
func (m *Manager) NewsletterPosts(
	ctx context.Context, accountID uuid.UUID, jid string, limit int,
) ([]NewsletterPost, error) {
	s, ok := m.Session(accountID)
	if !ok {
		return nil, ErrSessionNotFound
	}
	if !s.IsConnected() {
		return nil, ErrNotConnected
	}
	target, err := types.ParseJID(jid)
	if err != nil {
		return nil, fmt.Errorf("alamat saluran tidak valid: %w", err)
	}
	if limit <= 0 || limit > 100 {
		limit = 30
	}

	// Read raw rather than through whatsmeow, which drops the poll tallies. See
	// newsletterraw.go.
	msgs, err := fetchChannelPosts(ctx, s, target, "messages", limit)
	if err != nil {
		return nil, fmt.Errorf("baca isi saluran: %w", err)
	}

	/*
	 * Views, reactions and votes, from the updates call.
	 *
	 * The message list carries the posts but, for the channel's own admin, very
	 * often no counters — WhatsApp delivers those as "updates", the same thing
	 * its live subscription pushes. Asked for separately and laid over the list
	 * by server id. Best-effort: without it the posts still show, just without
	 * numbers, which is what the panel did before and is at least not wrong.
	 */
	counts := map[types.MessageServerID]rawChannelPost{}
	var updateList []rawChannelPost
	if updates, err := fetchChannelPosts(ctx, s, target, "message_updates", limit); err == nil {
		updateList = updates
		for _, u := range updates {
			counts[u.ServerID] = u
		}
	} else {
		s.log.Warn("newsletter updates", "jid", jid, "err", err)
	}
	dumpedUpdates := false

	out := make([]NewsletterPost, 0, len(msgs))
	for _, msg := range msgs {
		if msg.Message == nil {
			continue
		}
		c := extractContent(msg.Message)
		if c.Skip {
			continue
		}
		post := NewsletterPost{
			ServerID:  fmt.Sprint(msg.ServerID),
			Kind:      c.Type,
			PostedAt:  time.Unix(msg.Timestamp, 0).UTC(),
			ViewCount: msg.Views,
			Reactions: map[string]int{},
		}
		if c.Body != nil {
			post.Text = *c.Body
		} else if c.Caption != nil {
			post.Text = *c.Caption
		}
		for emoji, r := range msg.Reactions {
			post.Reactions[emoji] = r
		}
		tallies := msg.Tallies
		// The updates are newer than the list, so where both have a figure the
		// update wins.
		if u, ok := counts[msg.ServerID]; ok {
			if u.Views > post.ViewCount {
				post.ViewCount = u.Views
			}
			for emoji, r := range u.Reactions {
				post.Reactions[emoji] = r
			}
			if len(u.Tallies) > 0 {
				tallies = u.Tallies
			}
		}

		if p := extractMedia(msg.Message); p != nil && p.DirectPath != "" {
			post.HasMedia = true
			post.Kind = p.Kind
			post.Mime = p.Mime
			post.FileName = p.FileName
			post.Thumbnail = p.Thumbnail
		}
		if poll := pollCreation(msg.Message); poll != nil {
			votes := make([]int, 0, len(poll.GetOptions()))
			matched := 0
			for i, o := range poll.GetOptions() {
				post.PollOptions = append(post.PollOptions, o.GetOptionName())
				n, ok := matchTally(tallies, o.GetOptionName(), i)
				if ok {
					matched++
				}
				votes = append(votes, n)
			}
			post.PollSelectable = int(poll.GetSelectableOptionsCount())
			// Counts only when every option was matched to one. A partial match
			// would show real numbers beside invented zeros.
			if matched == len(votes) && matched > 0 {
				post.PollVotes = votes
			} else {
				var update *rawChannelPost
				if u, ok := counts[msg.ServerID]; ok {
					update = &u
				}
				dumpUnrecognisedPoll(msg, update)
				if !dumpedUpdates {
					dumpUpdates(updateList)
					dumpedUpdates = true
				}
			}
		}
		out = append(out, post)
	}
	return out, nil
}

// maxChannelMediaBytes caps what the viewer will pull through this server for
// one post. The same ceiling as a video WhatsApp accepts: past it the file could
// not have been posted, so a larger claim is either wrong or hostile.
const maxChannelMediaBytes = 64 << 20

// ChannelMedia is one post's file, fetched for display.
type ChannelMedia struct {
	Data     []byte
	Mime     string
	FileName string
}

// NewsletterMedia fetches the file behind one channel post.
//
// Read on demand and handed straight back — not written to our bucket, not to
// disk. A channel post is public on WhatsApp and lives there; keeping a copy
// would be storage bought for nothing, the same reason posts themselves are not
// stored. The file is found again by server id rather than trusting any path or
// URL the browser sends, so the browser can only ask for a post, never for an
// address.
func (m *Manager) NewsletterMedia(
	ctx context.Context, accountID uuid.UUID, jid, serverID string,
) (*ChannelMedia, error) {
	s, target, err := m.newsletterTarget(accountID, jid)
	if err != nil {
		return nil, err
	}
	var want types.MessageServerID
	if _, err := fmt.Sscan(serverID, &want); err != nil || want <= 0 {
		return nil, fmt.Errorf("%w: id postingan tidak valid", ErrMediaUnavailable)
	}

	// Before is exclusive, so this asks for exactly the one post.
	msgs, err := s.client.GetNewsletterMessages(ctx, target,
		&whatsmeow.GetNewsletterMessagesParams{Count: 1, Before: want + 1})
	if err != nil {
		return nil, fmt.Errorf("baca isi saluran: %w", err)
	}
	for _, msg := range msgs {
		if msg == nil || msg.MessageServerID != want {
			continue
		}
		p := extractMedia(msg.Message)
		if p == nil || p.DirectPath == "" {
			return nil, ErrMediaUnavailable
		}
		if p.Size > maxChannelMediaBytes {
			return nil, fmt.Errorf("%w: berkas terlalu besar untuk ditampilkan", ErrMediaUnavailable)
		}
		downloadable, ok := downloadableOf(msg.Message)
		if !ok {
			return nil, ErrMediaUnavailable
		}
		data, err := s.client.Download(ctx, downloadable)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrMediaUnavailable, err)
		}
		return &ChannelMedia{Data: data, Mime: p.Mime, FileName: p.FileName}, nil
	}
	return nil, ErrMediaUnavailable
}

// downloadableOf returns the part of a message whatsmeow can download.
func downloadableOf(msg *waE2E.Message) (whatsmeow.DownloadableMessage, bool) {
	switch {
	case msg.GetImageMessage() != nil:
		return msg.GetImageMessage(), true
	case msg.GetVideoMessage() != nil:
		return msg.GetVideoMessage(), true
	case msg.GetAudioMessage() != nil:
		return msg.GetAudioMessage(), true
	case msg.GetDocumentMessage() != nil:
		return msg.GetDocumentMessage(), true
	case msg.GetStickerMessage() != nil:
		return msg.GetStickerMessage(), true
	}
	return nil, false
}

// FollowNewsletter subscribes this number to a channel and records it.
func (m *Manager) FollowNewsletter(ctx context.Context, accountID uuid.UUID, jid string) error {
	s, target, err := m.newsletterTarget(accountID, jid)
	if err != nil {
		return err
	}
	if err := s.client.FollowNewsletter(ctx, target); err != nil {
		return fmt.Errorf("ikuti saluran: %w", err)
	}

	// Read back rather than assumed: following returns nothing, and the name,
	// picture and subscriber count are what the list has to show next.
	info, err := s.client.GetNewsletterInfo(ctx, target)
	if err != nil {
		// Followed successfully; only the description is missing. The next sync
		// fills it in, and refusing here would claim a failure that did not
		// happen.
		m.log.Warn("read newsletter after following", "jid", jid, "err", err)
		return nil
	}
	return m.repo.UpsertNewsletter(ctx, s.WorkspaceID, newsletterRow(accountID, info))
}

// UnfollowNewsletter unsubscribes and forgets the channel.
func (m *Manager) UnfollowNewsletter(ctx context.Context, accountID uuid.UUID, jid string) error {
	s, target, err := m.newsletterTarget(accountID, jid)
	if err != nil {
		return err
	}
	if err := s.client.UnfollowNewsletter(ctx, target); err != nil {
		return fmt.Errorf("berhenti mengikuti saluran: %w", err)
	}
	return m.repo.DeleteNewsletter(ctx, accountID, jid)
}

// MuteNewsletter turns a channel's notifications off or on.
func (m *Manager) MuteNewsletter(
	ctx context.Context, accountID uuid.UUID, jid string, muted bool,
) error {
	s, target, err := m.newsletterTarget(accountID, jid)
	if err != nil {
		return err
	}
	if err := s.client.NewsletterToggleMute(ctx, target, muted); err != nil {
		return fmt.Errorf("ubah pengaturan bisu: %w", err)
	}
	return m.repo.SetNewsletterMuted(ctx, accountID, jid, muted)
}

// PreviewNewsletter describes a channel this number does not follow yet, from
// its address or its invite link.
func (m *Manager) PreviewNewsletter(
	ctx context.Context, accountID uuid.UUID, ref string,
) (*repository.Newsletter, error) {
	s, ok := m.Session(accountID)
	if !ok {
		return nil, ErrSessionNotFound
	}
	if !s.IsConnected() {
		return nil, ErrNotConnected
	}

	// An invite link rather than an address: "whatsapp.com/channel/<key>" is
	// what people actually paste, so it is what this accepts.
	if key := inviteKey(ref); key != "" {
		info, err := s.client.GetNewsletterInfoWithInvite(ctx, key)
		if err != nil {
			return nil, fmt.Errorf("baca saluran dari tautan: %w", err)
		}
		row := newsletterRow(accountID, info)
		return &row, nil
	}

	target, err := types.ParseJID(ref)
	if err != nil {
		return nil, fmt.Errorf("alamat saluran tidak valid: %w", err)
	}
	info, err := s.client.GetNewsletterInfo(ctx, target)
	if err != nil {
		return nil, fmt.Errorf("baca saluran: %w", err)
	}
	row := newsletterRow(accountID, info)
	return &row, nil
}

func (m *Manager) newsletterTarget(accountID uuid.UUID, jid string) (*Session, types.JID, error) {
	s, ok := m.Session(accountID)
	if !ok {
		return nil, types.EmptyJID, ErrSessionNotFound
	}
	if !s.IsConnected() {
		return nil, types.EmptyJID, ErrNotConnected
	}
	target, err := types.ParseJID(jid)
	if err != nil {
		return nil, types.EmptyJID, fmt.Errorf("alamat saluran tidak valid: %w", err)
	}
	return s, target, nil
}
