package wa

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.mau.fi/whatsmeow"
	waBinary "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"

	"github.com/salesan/omnichannel/backend/internal/media"
)

// Posting to a channel.
//
// The rest of newsletter.go is read-only because that is all a subscriber has.
// A channel this number owns or administers is the exception: there we are the
// publisher, and a publisher can post anything WhatsApp lets a channel carry —
// text, a picture, a video, an audio clip, a document, a sticker, a poll.
//
// Three things make this its own file rather than a branch inside the chat send
// path:
//
//   - A channel post is not a conversation message. It has no recipient, no
//     delivery receipt, no read state, no SLA clock and nobody to reply to, so
//     none of the machinery around SendText applies to it. Threading a channel
//     through that path would mean a conversation row for something that is not
//     a conversation, and every report built on those rows would quietly start
//     counting broadcasts as customer contacts.
//   - Channel media is uploaded unencrypted, through a different endpoint
//     (UploadNewsletter), and the message carries a media handle instead of a
//     media key. The two upload paths share nothing but their name.
//   - Nothing is stored. Posts are read back from WhatsApp, exactly as they were
//     before this file existed, so publishing changes what we can do to a
//     channel without changing what we keep about one.

// ErrChannelNotAdmin reports a post attempted on somebody else's channel.
var ErrChannelNotAdmin = errors.New("wa: nomor ini hanya pengikut saluran, bukan pemilik atau adminnya")

// ErrInvalidPost reports a post this app refuses to send: no content, a sticker
// that is not a WebP, a kind that is not one of the seven.
var ErrInvalidPost = errors.New("wa: isi postingan saluran tidak valid")

// ErrChannelSend reports that WhatsApp itself refused or failed the post. Kept
// apart from a server fault so the operator reads WhatsApp's reason instead of
// "Terjadi kesalahan pada server", which is what hid why polls were failing.
var ErrChannelSend = errors.New("wa: whatsapp menolak postingan saluran")

// The kinds a channel post can be. Kept as its own list rather than reusing the
// attachment kinds, because "text" and "poll" are not attachments and "sticker"
// is not something the chat path can send.
const (
	PostText     = "text"
	PostImage    = "image"
	PostVideo    = "video"
	PostAudio    = "audio"
	PostDocument = "document"
	PostSticker  = "sticker"
	PostPoll     = "poll"
)

// NewsletterPostInput is one post to publish.
type NewsletterPostInput struct {
	AccountID uuid.UUID
	// JID is the channel's address, as stored.
	JID string
	// Kind is one of the Post* constants above.
	Kind string
	// Text is the body of a text post, or the caption of a media one.
	Text string

	// File and Source describe the attachment for a media post. Source holds the
	// plaintext bytes positioned anywhere; this function seeks it itself. The
	// caller owns its lifetime and removes it once this returns.
	File   *media.File
	Source *os.File

	// The poll, for Kind == PostPoll. Options are already trimmed and
	// deduplicated by normalizePoll.
	PollName            string
	PollOptions         []string
	PollSelectableCount int
}

// PostToNewsletter publishes one post to a channel this number runs.
//
// The role is read from WhatsApp rather than from our own table. The stored
// viewer_role is a copy of an answer given at the last sync, and an admin who
// was removed an hour ago would still look like an admin here — so the check
// that decides whether anything is sent asks the only party that knows. The
// fresh answer is written back, which is also how the interface stops offering a
// composer for a channel this number no longer runs.
func (m *Manager) PostToNewsletter(
	ctx context.Context, in NewsletterPostInput,
) (time.Time, error) {
	s, target, err := m.newsletterTarget(in.AccountID, in.JID)
	if err != nil {
		return time.Time{}, err
	}
	if target.Server != types.NewsletterServer {
		return time.Time{}, fmt.Errorf("%w: %s bukan alamat saluran", ErrInvalidPost, in.JID)
	}

	info, err := s.client.GetNewsletterInfo(ctx, target)
	if err != nil {
		return time.Time{}, fmt.Errorf("baca saluran: %w", err)
	}
	row := newsletterRow(in.AccountID, info)
	// Best-effort: a stale row is a cosmetic problem, and refusing to post
	// because our own copy could not be updated would not be.
	if err := m.repo.UpsertNewsletter(ctx, s.WorkspaceID, row); err != nil {
		s.log.Warn("refresh newsletter row", "jid", in.JID, "err", err)
	}
	switch types.NewsletterRole(row.ViewerRole) {
	case types.NewsletterRoleOwner, types.NewsletterRoleAdmin:
	default:
		return time.Time{}, fmt.Errorf("%w (%s)", ErrChannelNotAdmin, row.ViewerRole)
	}

	msg, handle, err := m.buildNewsletterPost(ctx, s, in)
	if err != nil {
		return time.Time{}, err
	}

	if in.Kind == PostPoll {
		return sendNewsletterPoll(ctx, s, target, msg)
	}

	resp, err := s.client.SendMessage(ctx, target, msg, whatsmeow.SendRequestExtra{
		ID: s.client.GenerateMessageID(),
		// Unlike a chat attachment, channel media is referenced by the handle the
		// upload returned. Empty for text and polls, which is what the send path
		// expects when there is no media.
		MediaHandle: handle,
	})
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: %v", ErrChannelSend, err)
	}
	return resp.Timestamp, nil
}

// sendNewsletterPoll sends a poll to a channel with the node WhatsApp requires.
//
// Not through SendMessage. A poll is announced to the server with a
// <meta polltype="creation"/> child beside its payload; whatsmeow adds it on the
// chat and group paths but its channel path builds the stanza without it, and
// the server then refuses the post. So the stanza is built here, in exactly the
// shape the channel path uses plus that one node, and the reply is read the same
// way SendMessage reads it: a timestamp on success, an error code otherwise.
func sendNewsletterPoll(
	ctx context.Context, s *Session, to types.JID, msg *waE2E.Message,
) (time.Time, error) {
	plaintext, err := proto.Marshal(msg)
	if err != nil {
		return time.Time{}, fmt.Errorf("susun polling: %w", err)
	}
	id := s.client.GenerateMessageID()
	node := waBinary.Node{
		Tag: "message",
		Attrs: waBinary.Attrs{
			"to":   to,
			"id":   id,
			"type": "poll",
		},
		Content: []waBinary.Node{
			{Tag: "plaintext", Attrs: waBinary.Attrs{}, Content: plaintext},
			{Tag: "meta", Attrs: waBinary.Attrs{"polltype": "creation"}},
		},
	}

	internals := s.client.DangerousInternals()
	respCh := internals.WaitResponse(string(id))
	if err := internals.SendNode(ctx, node); err != nil {
		internals.CancelResponse(string(id), respCh)
		return time.Time{}, fmt.Errorf("%w: %v", ErrChannelSend, err)
	}

	select {
	case resp := <-respCh:
		ag := resp.AttrGetter()
		if code := ag.OptionalInt("error"); code != 0 {
			return time.Time{}, fmt.Errorf("%w: kode %d", ErrChannelSend, code)
		}
		if t := ag.OptionalUnixTime("t"); !t.IsZero() {
			return t, nil
		}
		return time.Now().UTC(), nil
	case <-time.After(75 * time.Second):
		internals.CancelResponse(string(id), respCh)
		return time.Time{}, fmt.Errorf("%w: WhatsApp tidak menjawab", ErrChannelSend)
	case <-ctx.Done():
		internals.CancelResponse(string(id), respCh)
		return time.Time{}, ctx.Err()
	}
}

// buildNewsletterPost turns the request into the message WhatsApp receives,
// uploading the attachment on the way when there is one.
//
// The returned handle belongs with the message: sending one without the other
// produces a post whose media never loads.
func (m *Manager) buildNewsletterPost(
	ctx context.Context, s *Session, in NewsletterPostInput,
) (*waE2E.Message, string, error) {
	text := strings.TrimSpace(in.Text)

	if in.Kind == PostText {
		if text == "" {
			return nil, "", fmt.Errorf("%w: teks kosong", ErrInvalidPost)
		}
		return &waE2E.Message{Conversation: proto.String(text)}, "", nil
	}

	if in.Kind == PostPoll {
		name, options, err := normalizePoll(in.PollName, in.PollOptions)
		if err != nil {
			return nil, "", err
		}
		selectable := in.PollSelectableCount
		if selectable != 1 {
			selectable = 0 // anything but "pick one" is expressed as unlimited
		}
		return s.client.BuildPollCreation(name, options, selectable), "", nil
	}

	if in.File == nil || in.Source == nil {
		return nil, "", fmt.Errorf("%w: berkas tidak ada", ErrInvalidPost)
	}
	caption := media.TrimCaption(text)
	var capPtr *string
	if caption != "" {
		capPtr = proto.String(caption)
	}

	if _, err := in.Source.Seek(0, io.SeekStart); err != nil {
		return nil, "", err
	}
	// UploadNewsletter, not Upload: a channel post is public by construction, so
	// WhatsApp stores it in the clear and the message carries no media key. There
	// is no scratch file here for the same reason — nothing is encrypted, so
	// nothing needs a second copy on disk.
	up, err := s.client.UploadNewsletterReader(ctx, in.Source, mediaTypeFor(in.Kind))
	if err != nil {
		return nil, "", fmt.Errorf("%w: unggah berkas gagal (%v)", ErrChannelSend, err)
	}

	switch in.Kind {
	case PostImage:
		img := &waE2E.ImageMessage{
			URL:        proto.String(up.URL),
			DirectPath: proto.String(up.DirectPath),
			FileSHA256: up.FileSHA256,
			FileLength: proto.Uint64(up.FileLength),
			Mimetype:   proto.String(in.File.MIME),
			Caption:    capPtr,
		}
		if thumb, w, h := imagePreview(in.Source); thumb != nil {
			img.JPEGThumbnail = thumb
			img.Width = proto.Uint32(uint32(w))
			img.Height = proto.Uint32(uint32(h))
		}
		return &waE2E.Message{ImageMessage: img}, up.Handle, nil

	case PostVideo:
		return &waE2E.Message{VideoMessage: &waE2E.VideoMessage{
			URL:        proto.String(up.URL),
			DirectPath: proto.String(up.DirectPath),
			FileSHA256: up.FileSHA256,
			FileLength: proto.Uint64(up.FileLength),
			Mimetype:   proto.String(in.File.MIME),
			Caption:    capPtr,
		}}, up.Handle, nil

	case PostAudio:
		return &waE2E.Message{AudioMessage: &waE2E.AudioMessage{
			URL:        proto.String(up.URL),
			DirectPath: proto.String(up.DirectPath),
			FileSHA256: up.FileSHA256,
			FileLength: proto.Uint64(up.FileLength),
			Mimetype:   proto.String(in.File.MIME),
		}}, up.Handle, nil

	case PostSticker:
		// WebP only, and not converted here. WhatsApp shows a sticker that is not
		// a WebP as a broken square, and quietly re-encoding somebody's picture
		// into one would change what they posted without telling them.
		if in.File.MIME != "image/webp" {
			return nil, "", fmt.Errorf(
				"%w: stiker harus berkas WebP (berkas ini %s)", ErrInvalidPost, in.File.MIME)
		}
		return &waE2E.Message{StickerMessage: &waE2E.StickerMessage{
			URL:        proto.String(up.URL),
			DirectPath: proto.String(up.DirectPath),
			FileSHA256: up.FileSHA256,
			FileLength: proto.Uint64(up.FileLength),
			Mimetype:   proto.String(in.File.MIME),
		}}, up.Handle, nil

	case PostDocument:
		doc := &waE2E.DocumentMessage{
			URL:        proto.String(up.URL),
			DirectPath: proto.String(up.DirectPath),
			FileSHA256: up.FileSHA256,
			FileLength: proto.Uint64(up.FileLength),
			Mimetype:   proto.String(in.File.MIME),
			FileName:   proto.String(in.File.Name),
			Caption:    capPtr,
		}
		if thumb, _, _ := imagePreview(in.Source); thumb != nil {
			doc.JPEGThumbnail = thumb
		}
		return &waE2E.Message{DocumentMessage: doc}, up.Handle, nil

	default:
		return nil, "", fmt.Errorf("%w: jenis %q tidak dikenal", ErrInvalidPost, in.Kind)
	}
}
