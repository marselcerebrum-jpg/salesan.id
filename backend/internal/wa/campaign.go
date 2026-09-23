package wa

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/appstate"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"

	"github.com/salesan/omnichannel/backend/internal/media"
)

// Sending for Broadcast and WA Story.
//
// Deliberately a separate path from SendText and SendMedia, and the difference
// is not incidental:
//
//   - No typing indicator and no read receipt. Those exist on the chat path
//     because a person really is reading and typing; a queue is not, and
//     pretending otherwise would be exactly the simulated behaviour this feature
//     is required not to have. The only pause a campaign takes is its delay
//     profile, which is queue pacing and is presented as nothing more.
//   - Media is uploaded to WhatsApp ONCE per device and the result reused for
//     every recipient on that device. The upload is an encrypted blob plus a
//     key; re-encrypting the same file a thousand times would cost a thousand
//     uploads for an identical result.
//   - The message row is written with sender_source = 'broadcast' (or 'story'),
//     which is what keeps campaign traffic out of Pesan Terkirim, Kontak
//     Terlayani, SLA and follow-up while still showing the operator, in the
//     thread, what their customer received.

// ErrDeviceBusy means another campaign already holds this device's send gate.
var ErrDeviceBusy = errors.New("wa: perangkat sedang dipakai pengiriman lain")

// PreparedMedia is one file already uploaded to WhatsApp for one device.
//
// Valid only for the device that produced it: the media key is per upload, and
// another account cannot address it.
type PreparedMedia struct {
	AccountID uuid.UUID
	Kind      media.Kind
	MIME      string
	Name      string

	upload    whatsmeow.UploadResponse
	thumbnail []byte
	width     int
	height    int
}

// PrepareCampaignMedia uploads a file to WhatsApp for one device.
//
// The source is read twice — once for the thumbnail, once for the upload — so it
// must be a seekable file rather than a stream. That is what mediafetch hands
// back.
func (m *Manager) PrepareCampaignMedia(
	ctx context.Context,
	accountID uuid.UUID,
	src *os.File,
	info media.File,
) (*PreparedMedia, error) {
	s, ok := m.Session(accountID)
	if !ok {
		return nil, ErrSessionNotFound
	}
	if !s.IsConnected() {
		return nil, ErrNotConnected
	}

	if _, err := src.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}

	// A photo has to be a JPEG before it goes anywhere. WhatsApp's own clients
	// transcode before sending, and a message built from WebP or PNG is
	// accepted by the server and then cannot be drawn on a phone. See
	// normaliseImage for the broadcast this cost us.
	src, info, releaseConverted, err := normaliseImage(src, info)
	if err != nil {
		return nil, err
	}
	defer releaseConverted()

	// whatsmeow encrypts into a scratch file rather than memory, so a large
	// video never has two full copies in RAM. Removed before this returns, on
	// every path.
	scratch, err := os.CreateTemp("", "salesan-campaign-*.enc")
	if err != nil {
		return nil, fmt.Errorf("berkas sementara: %w", err)
	}
	defer func() {
		name := scratch.Name()
		_ = scratch.Close()
		_ = os.Remove(name)
	}()

	up, err := s.client.UploadReader(ctx, src, scratch, mediaTypeFor(string(info.Kind)))
	if err != nil {
		return nil, fmt.Errorf("unggah ke whatsapp: %w", err)
	}

	prepared := &PreparedMedia{
		AccountID: accountID,
		Kind:      info.Kind,
		MIME:      info.MIME,
		Name:      info.Name,
		upload:    up,
	}
	if thumb, w, h := imagePreview(src); thumb != nil {
		prepared.thumbnail = thumb
		prepared.width = w
		prepared.height = h
	}
	return prepared, nil
}

// LocalCopy is what the workspace needs to show this file in its own thread.
//
// A campaign message was written into the customer's thread with its type and
// its MIME, and nothing else: no attachment row, so the chat screen had a bubble
// that said "Foto" and showed no photo. The operator could not see what their
// own customer had received, which is the one thing recording it was for.
//
// Everything needed is already here. Uploading to WhatsApp returns the direct
// path and the keys that decrypt the file, which is exactly the material the
// attachment pipeline already uses for incoming media, so the local copy is
// fetched on demand through the path that has always existed rather than a
// second store of the same bytes.
type LocalCopy struct {
	Kind          string
	MIME          string
	Name          string
	SizeBytes     int64
	Width, Height int
	// Thumbnail is base64, as the attachment row stores it.
	Thumbnail string
	// MediaType is whatsmeow's own name for the category, needed to decrypt.
	MediaType     string
	DirectPath    string
	MediaKey      []byte
	FileEncSHA256 []byte
	FileSHA256    []byte
}

// LocalCopy describes this upload for the workspace's own records.
func (p *PreparedMedia) LocalCopy() LocalCopy {
	out := LocalCopy{
		Kind:          string(p.Kind),
		MIME:          p.MIME,
		Name:          p.Name,
		SizeBytes:     int64(p.upload.FileLength),
		Width:         p.width,
		Height:        p.height,
		MediaType:     string(mediaTypeFor(string(p.Kind))),
		DirectPath:    p.upload.DirectPath,
		MediaKey:      p.upload.MediaKey,
		FileEncSHA256: p.upload.FileEncSHA256,
		FileSHA256:    p.upload.FileSHA256,
	}
	if len(p.thumbnail) > 0 {
		out.Thumbnail = base64.StdEncoding.EncodeToString(p.thumbnail)
	}
	return out
}

// CampaignMessage builds the protobuf for one campaign message.
//
// text is the already-rendered body: spintax resolved, variables filled. For
// media it becomes the caption, which is how WhatsApp models a photo with words
// under it.
func CampaignMessage(text string, prepared *PreparedMedia) (*waE2E.Message, error) {
	if prepared == nil {
		if text == "" {
			return nil, ErrEmptyMessage
		}
		return &waE2E.Message{Conversation: proto.String(text)}, nil
	}

	var caption *string
	if trimmed := media.TrimCaption(text); trimmed != "" {
		caption = proto.String(trimmed)
	}
	up := prepared.upload

	switch prepared.Kind {
	case media.KindImage:
		img := &waE2E.ImageMessage{
			URL:           proto.String(up.URL),
			DirectPath:    proto.String(up.DirectPath),
			MediaKey:      up.MediaKey,
			FileEncSHA256: up.FileEncSHA256,
			FileSHA256:    up.FileSHA256,
			FileLength:    proto.Uint64(up.FileLength),
			Mimetype:      proto.String(prepared.MIME),
			Caption:       caption,
		}
		if prepared.thumbnail != nil {
			img.JPEGThumbnail = prepared.thumbnail
			img.Width = proto.Uint32(uint32(prepared.width))
			img.Height = proto.Uint32(uint32(prepared.height))
		}
		return &waE2E.Message{ImageMessage: img}, nil

	case media.KindVideo:
		return &waE2E.Message{VideoMessage: &waE2E.VideoMessage{
			URL:           proto.String(up.URL),
			DirectPath:    proto.String(up.DirectPath),
			MediaKey:      up.MediaKey,
			FileEncSHA256: up.FileEncSHA256,
			FileSHA256:    up.FileSHA256,
			FileLength:    proto.Uint64(up.FileLength),
			Mimetype:      proto.String(prepared.MIME),
			Caption:       caption,
		}}, nil

	case media.KindAudio:
		return &waE2E.Message{AudioMessage: &waE2E.AudioMessage{
			URL:           proto.String(up.URL),
			DirectPath:    proto.String(up.DirectPath),
			MediaKey:      up.MediaKey,
			FileEncSHA256: up.FileEncSHA256,
			FileSHA256:    up.FileSHA256,
			FileLength:    proto.Uint64(up.FileLength),
			Mimetype:      proto.String(prepared.MIME),
		}}, nil

	default:
		doc := &waE2E.DocumentMessage{
			URL:           proto.String(up.URL),
			DirectPath:    proto.String(up.DirectPath),
			MediaKey:      up.MediaKey,
			FileEncSHA256: up.FileEncSHA256,
			FileSHA256:    up.FileSHA256,
			FileLength:    proto.Uint64(up.FileLength),
			Mimetype:      proto.String(prepared.MIME),
			FileName:      proto.String(prepared.Name),
			Caption:       caption,
		}
		if prepared.thumbnail != nil {
			doc.JPEGThumbnail = prepared.thumbnail
		}
		return &waE2E.Message{DocumentMessage: doc}, nil
	}
}

// StoryMessage builds the protobuf for one Story.
//
// A text-only Story is an ExtendedTextMessage carrying a background colour,
// which is what WhatsApp itself produces for a written status; a bare
// Conversation string is not rendered as a status by the clients.
func StoryMessage(text string, prepared *PreparedMedia, backgroundARGB uint32) (*waE2E.Message, error) {
	if prepared == nil {
		if text == "" {
			return nil, ErrEmptyMessage
		}
		if backgroundARGB == 0 {
			backgroundARGB = defaultStoryBackground
		}
		return &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{
			Text:           proto.String(text),
			BackgroundArgb: proto.Uint32(backgroundARGB),
		}}, nil
	}
	// Photo and video statuses are the same message types as in a chat, with the
	// caption as the words over them.
	return CampaignMessage(text, prepared)
}

// defaultStoryBackground is WhatsApp's own dark teal, used when the composer
// does not offer a colour choice.
const defaultStoryBackground = 0xFF075E54

// SendResult is what one campaign send produced.
type SendResult struct {
	WAMessageID string
	Timestamp   time.Time
}

// NewMessageID mints the WhatsApp message id a campaign send will use.
//
// Handed out before the send rather than returned by it, because the queue has
// to write down what it is about to do before it does it. That record is the
// only thing that makes an interrupted send investigable afterwards instead of
// simply lost.
func (m *Manager) NewMessageID(accountID uuid.UUID) (string, error) {
	s, ok := m.Session(accountID)
	if !ok {
		return "", ErrSessionNotFound
	}
	return string(s.client.GenerateMessageID()), nil
}

// OwnJID returns the account's own WhatsApp address, for stamping sender_jid on
// the message rows a campaign writes.
func (m *Manager) OwnJID(accountID uuid.UUID) string {
	s, ok := m.Session(accountID)
	if !ok || s.client.Store == nil || s.client.Store.ID == nil {
		return ""
	}
	return s.client.Store.ID.ToNonAD().String()
}

// SendCampaignMessage delivers one already-built message to one chat.
//
// No presence, no typing, no read receipt: see the note at the top of this file.
// The caller owns pacing, retry and idempotency — this function does exactly one
// network call, with the id it was given, and reports what happened.
// The deadline on ctx is handed to whatsmeow as well as enforced here.
// whatsmeow only applies a deadline of its own when SendRequestExtra.Timeout is
// set; without it, waiting for the server's acknowledgement has no end.
func (m *Manager) SendCampaignMessage(
	ctx context.Context,
	accountID uuid.UUID,
	chatJID string,
	msg *waE2E.Message,
	waMessageID string,
) (SendResult, error) {
	s, ok := m.Session(accountID)
	if !ok {
		return SendResult{}, ErrSessionNotFound
	}
	if !s.IsConnected() {
		return SendResult{}, ErrNotConnected
	}

	jid, err := types.ParseJID(chatJID)
	if err != nil {
		return SendResult{}, fmt.Errorf("jid tidak valid %q: %w", chatJID, err)
	}

	resp, err := s.client.SendMessage(ctx, jid, msg,
		whatsmeow.SendRequestExtra{
			ID:      types.MessageID(waMessageID),
			Timeout: remaining(ctx),
		})
	if err != nil {
		if jid.Server == types.GroupServer && isServerError(err, 420) {
			err = fmt.Errorf("%w (%v)", ErrGroupAdminsOnly, err)
		}
		return SendResult{WAMessageID: waMessageID}, err
	}
	return SendResult{WAMessageID: waMessageID, Timestamp: resp.Timestamp}, nil
}

// SurfaceChat takes a chat back out of the archive, on every device.
//
// A chat the operator archived stays archived when a new message arrives, as
// long as "Keep chats archived" is on, which is the WhatsApp default. Two
// thirds of the groups these numbers broadcast to sit in that folder, so a
// broadcast that genuinely arrived was invisible on the sender's own phone.
//
// This is an app state patch, the same mechanism the phone itself uses, so the
// change reaches the phone and every other linked device rather than only this
// server's copy. Sending was never affected by archiving; only seeing was.
//
// Best effort by design: the message has already been delivered by the time
// this runs, and failing to tidy the chat list is not a reason to call a
// delivered message failed.
func (m *Manager) SurfaceChat(ctx context.Context, accountID uuid.UUID, chatJID string) error {
	s, ok := m.Session(accountID)
	if !ok {
		return ErrSessionNotFound
	}
	if !s.IsConnected() {
		return ErrNotConnected
	}

	jid, err := types.ParseJID(chatJID)
	if err != nil {
		return fmt.Errorf("jid tidak valid %q: %w", chatJID, err)
	}

	// Zero timestamp and no message key: whatsmeow documents both as optional,
	// and the patch only has to say "not archived".
	return s.client.SendAppState(ctx, appstate.BuildArchive(jid, false, time.Time{}, nil))
}

// ErrGroupAdminsOnly is WhatsApp refusing a group message with error 420:
// the group only lets admins post and this number is not one, or the address
// is a community itself rather than one of its groups. Retrying cannot change
// either.
var ErrGroupAdminsOnly = errors.New("WhatsApp menolak pesan ke grup ini (error 420): hanya admin yang boleh mengirim di sana, atau ini komunitas yang bukan grup chat")

// isServerError reports whether err is whatsmeow's "server returned error N"
// for the given N. whatsmeow formats the code into the message rather than
// exposing it, so the number is read back from the text.
func isServerError(err error, code int) bool {
	return errors.Is(err, whatsmeow.ErrServerReturnedError) &&
		strings.HasSuffix(err.Error(), fmt.Sprintf(" %d", code))
}

// PublishStory posts one Story from one device.
//
// whatsmeow resolves the recipient list itself from the account's own status
// privacy settings when the destination is types.StatusBroadcastJID, so the
// audience is whatever the phone is configured for — this server does not choose
// it and must not appear to.
func (m *Manager) PublishStory(
	ctx context.Context,
	accountID uuid.UUID,
	msg *waE2E.Message,
	waMessageID string,
) (SendResult, error) {
	s, ok := m.Session(accountID)
	if !ok {
		return SendResult{}, ErrSessionNotFound
	}
	if !s.IsConnected() {
		return SendResult{}, ErrNotConnected
	}

	resp, err := s.client.SendMessage(ctx, types.StatusBroadcastJID, msg,
		whatsmeow.SendRequestExtra{
			ID:      types.MessageID(waMessageID),
			Timeout: remaining(ctx),
		})
	if err != nil {
		return SendResult{WAMessageID: waMessageID}, err
	}
	return SendResult{WAMessageID: waMessageID, Timestamp: resp.Timestamp}, nil
}

// remaining is how long ctx has left, or zero when it has no deadline. Zero is
// what whatsmeow reads as "wait indefinitely", which is the behaviour every
// caller without a deadline already had.
func remaining(ctx context.Context) time.Duration {
	deadline, ok := ctx.Deadline()
	if !ok {
		return 0
	}
	left := time.Until(deadline)
	if left < 0 {
		return 0
	}
	return left
}

// RevokeStory takes one Story down from WhatsApp's own Status display.
//
// The same delete-for-everyone a person would use on their phone, addressed to
// status@broadcast. The sender is left empty because this is always our own
// message: WhatsApp refuses a revoke of somebody else's Status, and there is no
// "Status admin" the way there is a group admin.
//
// Taking it down does not delete the report. The publication becomes 'deleted'
// and its viewer tally freezes at the moment it came down — which is the honest
// record of a Story that was up for three hours and then pulled.
func (m *Manager) RevokeStory(ctx context.Context, accountID uuid.UUID, waMessageID string) error {
	s, ok := m.Session(accountID)
	if !ok {
		return ErrSessionNotFound
	}
	if !s.IsConnected() {
		return ErrNotConnected
	}

	if _, err := s.client.SendMessage(ctx, types.StatusBroadcastJID,
		s.client.BuildRevoke(types.StatusBroadcastJID, types.EmptyJID,
			types.MessageID(waMessageID))); err != nil {
		return fmt.Errorf("%w: %v", ErrNotRevocable, err)
	}

	/*
	 * Ended here as well as in the incoming-revoke handler.
	 *
	 * WhatsApp echoes our own revoke back as an event, and that handler would
	 * eventually do this — but "eventually" is after a round trip, and the
	 * operator who just pressed the button is looking at the page now. Both
	 * paths call the same function, and it only ever moves a live publication,
	 * so running twice changes nothing the second time.
	 */
	now := time.Now().UTC()
	if _, err := m.repo.RevokeStoryPublication(ctx, accountID, waMessageID, now); err != nil {
		return err
	}
	// The message row too, which is what the Status panel lists. Without it the
	// Story was gone from WhatsApp but still sitting in our own "Status saya"
	// until the echo arrived — and the echo is exactly what cannot be relied on
	// to arrive promptly. Same function the incoming path uses, so a second run
	// from the echo changes nothing.
	updated, keys, err := m.repo.RevokeMessage(ctx, accountID, waMessageID, now)
	if err != nil {
		return err
	}
	m.removeStoredObjects(ctx, keys)
	if updated != nil {
		m.broadcastMessageStatus(s.WorkspaceID, accountID, updated.ConversationID, updated)
	}
	return nil
}

// RevokeOwnStatus takes one of this number's own Status posts down, addressed
// by our message id — the Status panel's delete button.
//
// Own posts only. WhatsApp refuses to delete somebody else's Status, so the
// check here is about saying so clearly rather than about authority.
func (m *Manager) RevokeOwnStatus(ctx context.Context, workspaceID, messageID uuid.UUID) error {
	target, err := m.repo.MessageTargetByID(ctx, workspaceID, messageID)
	if err != nil {
		return err
	}
	if target.ChatJID != types.StatusBroadcastJID.String() {
		return fmt.Errorf("%w: ini bukan status", ErrNotRevocable)
	}
	if !target.FromMe {
		return fmt.Errorf("%w: status orang lain tidak bisa dihapus", ErrNotRevocable)
	}
	if target.RevokedAt != nil {
		return fmt.Errorf("%w: status sudah dihapus", ErrNotRevocable)
	}
	return m.RevokeStory(ctx, target.AccountID, target.WAMessageID)
}

// DeviceOnline reports whether an account currently has a live connection.
//
// Read from the session rather than from the account row: the row records the
// last transition, and a campaign about to send needs to know about now.
func (m *Manager) DeviceOnline(accountID uuid.UUID) bool {
	s, ok := m.Session(accountID)
	return ok && s.IsConnected()
}

// CanReachGroup reports whether a device is a participant of a group.
//
// Asked before a group is assigned to a device, because a number that is not in
// the group cannot post to it and the failure would otherwise only surface one
// message at a time, at send time.
func (m *Manager) CanReachGroup(ctx context.Context, accountID uuid.UUID, groupJID string) bool {
	s, ok := m.Session(accountID)
	if !ok || !s.IsConnected() {
		return false
	}
	jid, err := types.ParseJID(groupJID)
	if err != nil || jid.Server != types.GroupServer {
		return false
	}
	info, err := s.client.GetGroupInfo(ctx, jid)
	if err != nil || info == nil {
		return false
	}
	own := s.client.Store.ID
	if own == nil {
		return false
	}
	mine := own.ToNonAD().User
	for _, p := range info.Participants {
		if p.JID.User == mine || (p.LID.User != "" && p.LID.User == own.User) {
			return true
		}
	}
	return false
}
