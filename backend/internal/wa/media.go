package wa

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"github.com/salesan/omnichannel/backend/internal/media"
	"github.com/salesan/omnichannel/backend/internal/models"
	"github.com/salesan/omnichannel/backend/internal/realtime"
	"github.com/salesan/omnichannel/backend/internal/repository"
	"github.com/salesan/omnichannel/backend/internal/storage"
)

// Media errors surfaced to the HTTP layer.
var (
	// ErrMediaDisabled means SUPABASE_SERVICE_ROLE_KEY is not set, so there is
	// nowhere to put the bytes. Text chat is unaffected.
	ErrMediaDisabled = errors.New("wa: media storage is not configured")
	// ErrMediaUnavailable means the file is gone from WhatsApp's servers, which
	// happens to media older than roughly two weeks that nobody has fetched.
	ErrMediaUnavailable = errors.New("wa: media is no longer available from whatsapp")
	// ErrMediaExpired means we deleted the file ourselves, because its message
	// fell outside the retention window.
	ErrMediaExpired = errors.New("wa: media has passed the retention window")
	// ErrMediaNotReady means the file is still on its way into storage.
	//
	// Kept apart from ErrMediaUnavailable because the two are opposite advice.
	// "Unavailable" is final and the interface stops offering a retry; this one
	// resolves by itself in a second or two. An outgoing file spent that second
	// or two reported as permanently gone, so the bubble that had just been
	// sent successfully showed "Berkas tidak tersedia lagi" and stayed that way.
	ErrMediaNotReady = errors.New("wa: media is still being stored")
)

// MediaEnabled reports whether media can be stored and served.
func (m *Manager) MediaEnabled() bool { return m.store != nil }

// storageKey is the object key for one attachment.
//
// Derived entirely from ids the server controls — never from the uploaded
// filename — so a hostile name cannot influence where the file lands. The
// display name lives in the database and is applied at download time through
// the signed URL instead.
func storageKey(workspaceID, accountID, messageID uuid.UUID, idx int, ext string) string {
	if ext != "" && !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	// Guard against an extension that somehow still carries a separator.
	ext = strings.ReplaceAll(strings.ReplaceAll(ext, "/", ""), "\\", "")
	return fmt.Sprintf("%s/%s/%s/%d%s", workspaceID, accountID, messageID, idx, ext)
}

// --- incoming ----------------------------------------------------------------

// captureIncomingMedia records the attachment carried by an inbound message.
//
// The row is created immediately with WhatsApp's own thumbnail, so the bubble
// renders at the right size straight away. Fetching the full file is a separate
// step: live messages are downloaded eagerly in the background, while a message
// arriving through a history sync waits until someone actually opens it. That
// keeps a seven-day backfill from pulling hundreds of megabytes nobody asked
// for.
func (s *Session) captureIncomingMedia(
	ctx context.Context,
	evt *events.Message,
	msg *models.Message,
	eager bool,
) {
	payload := extractMedia(evt.Message)
	if payload == nil {
		return
	}
	att, err := s.mgr.recordAttachment(ctx, s.WorkspaceID, s.AccountID, msg.ID, evt.Info.ID, payload)
	if err != nil {
		s.log.Warn("record attachment", "wa_id", evt.Info.ID, "err", err)
		return
	}
	if !eager || att.Status == models.AttachmentStored {
		return
	}
	s.mgr.queueDownload(s.WorkspaceID, s.AccountID, att.ID)
}

// attachHistoryMedia records attachments for a completed backfill batch.
//
// The rows are created `pending` with no download queued: history can span
// hundreds of files, and fetching them all would trade a fast sync for a slow
// one nobody asked for. Each is fetched when it is first opened.
func (s *Session) attachHistoryMedia(ctx context.Context, byWAID map[string]*mediaPayload) {
	if len(byWAID) == 0 {
		return
	}
	waIDs := make([]string, 0, len(byWAID))
	for id := range byWAID {
		waIDs = append(waIDs, id)
	}

	ids, err := s.mgr.repo.MessageIDsByWAIDs(ctx, s.AccountID, waIDs)
	if err != nil {
		s.log.Warn("resolve history message ids", "err", err)
		return
	}
	for waID, payload := range byWAID {
		messageID, ok := ids[waID]
		if !ok {
			continue // fell outside the window, or the insert was rejected
		}
		if _, err := s.mgr.recordAttachment(ctx, s.WorkspaceID, s.AccountID, messageID, waID, payload); err != nil {
			s.log.Warn("record history attachment", "wa_id", waID, "err", err)
		}
	}
}

// recordAttachment writes the attachment row plus its server-only download
// material. Idempotent: replaying the same message returns the existing row.
func (m *Manager) recordAttachment(
	ctx context.Context,
	workspaceID, accountID, messageID uuid.UUID,
	waMessageID string,
	payload *mediaPayload,
) (*models.Attachment, error) {
	name := payload.displayName(waMessageID)

	var thumb *string
	if len(payload.Thumbnail) > 0 {
		encoded := base64.StdEncoding.EncodeToString(payload.Thumbnail)
		thumb = &encoded
	}
	var size *int64
	if payload.Size > 0 {
		size = &payload.Size
	}

	att, created, err := m.repo.InsertAttachment(ctx, repository.InsertAttachmentInput{
		WorkspaceID: workspaceID,
		AccountID:   accountID,
		MessageID:   messageID,
		Index:       0,
		Kind:        payload.Kind,
		FileName:    &name,
		MimeType:    strPtr(payload.Mime),
		SizeBytes:   size,
		Width:       intPtr(payload.Width),
		Height:      intPtr(payload.Height),
		Duration:    intPtr(payload.Duration),
		Thumbnail:   thumb,
		Status:      models.AttachmentPending,
	})
	if err != nil {
		return nil, err
	}

	// Refresh the download material even on a replay: a re-sent message can
	// carry a new direct path for the same file.
	if created || att.Status != models.AttachmentStored {
		if payload.downloadable() {
			if err := m.repo.SaveMediaRef(ctx, att.ID, repository.MediaRef{
				DirectPath:    payload.DirectPath,
				MediaKey:      payload.MediaKey,
				FileEncSHA256: payload.FileEncSHA256,
				FileSHA256:    payload.FileSHA256,
				MediaType:     string(payload.MediaType),
			}); err != nil {
				return nil, fmt.Errorf("save media ref: %w", err)
			}
		}
	}
	return att, nil
}

// queueDownload fetches an attachment in the background, bounded by a
// process-wide semaphore so a burst of incoming photos cannot exhaust the
// network or the memory of the box.
func (m *Manager) queueDownload(workspaceID, accountID, attachmentID uuid.UUID) {
	if m.store == nil {
		return
	}
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()

		select {
		case m.mediaSem <- struct{}{}:
			defer func() { <-m.mediaSem }()
		case <-m.rootCtx.Done():
			return
		}

		ctx, cancel := context.WithTimeout(m.rootCtx, 3*time.Minute)
		defer cancel()

		if _, err := m.EnsureStored(ctx, workspaceID, attachmentID); err != nil {
			m.log.Warn("download attachment", "attachment_id", attachmentID, "err", err)
			return
		}
		m.broadcastAttachment(ctx, workspaceID, attachmentID)
	}()
}

// broadcastAttachment pushes the message the attachment belongs to, so an open
// chat swaps its placeholder for the real thing without a refresh.
func (m *Manager) broadcastAttachment(ctx context.Context, workspaceID, attachmentID uuid.UUID) {
	loc, err := m.repo.GetAttachmentLocation(ctx, workspaceID, attachmentID)
	if err != nil {
		return
	}
	msg, err := m.repo.GetMessageByID(ctx, workspaceID, loc.MessageID)
	if err != nil {
		return
	}
	m.hub.Broadcast(workspaceID, realtime.EventMessageStatus, map[string]any{
		"account_id": loc.AccountID,
		"changes": []repository.MessageStatusChange{{
			ID:             msg.ID,
			ConversationID: msg.ConversationID,
			WAMessageID:    msg.WAMessageID,
			Status:         msg.Status,
		}},
		"message": msg,
	})
}

// EnsureStored guarantees an attachment's bytes are in the bucket, downloading
// them from WhatsApp on first use.
//
// Safe to call concurrently for the same attachment: the object key is fixed,
// so two racing downloads write identical bytes to the same place, and the
// second simply overwrites the first.
func (m *Manager) EnsureStored(ctx context.Context, workspaceID, attachmentID uuid.UUID) (*repository.AttachmentLocation, error) {
	if m.store == nil {
		return nil, ErrMediaDisabled
	}

	loc, err := m.repo.GetAttachmentLocation(ctx, workspaceID, attachmentID)
	if err != nil {
		return nil, err
	}
	if loc.Status == models.AttachmentStored && loc.StoragePath != nil && *loc.StoragePath != "" {
		return loc, nil
	}
	// Past the retention window the file is gone on purpose, and the material
	// that could fetch it again has been deleted with it.
	if loc.Status == models.AttachmentExpired {
		return nil, ErrMediaExpired
	}

	ref, err := m.repo.GetMediaRef(ctx, attachmentID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			// No download material. For a file still on its way into the bucket
			// that is expected and temporary: we are the sender, so there is
			// nothing at WhatsApp to fetch and nothing has gone wrong. Only a
			// settled attachment with no material is genuinely unavailable.
			if loc.Status == models.AttachmentPending || loc.Status == models.AttachmentUploading {
				return nil, ErrMediaNotReady
			}
			// An outbound file whose upload failed, or an inbound one WhatsApp
			// described without addressing.
			return nil, ErrMediaUnavailable
		}
		return nil, err
	}

	s, ok := m.Session(loc.AccountID)
	if !ok || !s.IsConnected() {
		return nil, ErrNotConnected
	}

	_ = m.repo.MarkAttachmentStatus(ctx, attachmentID, models.AttachmentUploading, nil)

	mediaType := whatsmeow.MediaType(ref.MediaType)
	if mediaType == "" {
		mediaType = mediaTypeFor(loc.Kind)
	}

	data, err := s.client.DownloadMediaWithPath(
		ctx, ref.DirectPath, ref.FileEncSHA256, ref.FileSHA256, ref.MediaKey,
		mediaType, mmsType(mediaType), false,
	)
	if err != nil {
		detail := truncate(err.Error(), 400)
		_ = m.repo.MarkAttachmentStatus(ctx, attachmentID, models.AttachmentFailed, &detail)
		if errors.Is(err, whatsmeow.ErrMediaDownloadFailedWith404) ||
			errors.Is(err, whatsmeow.ErrMediaDownloadFailedWith410) {
			return nil, ErrMediaUnavailable
		}
		return nil, fmt.Errorf("download from whatsapp: %w", err)
	}

	contentType := "application/octet-stream"
	if loc.MimeType != nil && *loc.MimeType != "" {
		contentType = *loc.MimeType
	}
	ext := ""
	if loc.FileName != nil {
		ext = path.Ext(*loc.FileName)
	}
	key := storageKey(workspaceID, loc.AccountID, loc.MessageID, 0, ext)

	if err := m.store.UploadBytes(ctx, key, contentType, data); err != nil {
		detail := truncate(err.Error(), 400)
		_ = m.repo.MarkAttachmentStatus(ctx, attachmentID, models.AttachmentFailed, &detail)
		return nil, err
	}
	if err := m.repo.MarkAttachmentStored(ctx, attachmentID, key, int64(len(data))); err != nil {
		return nil, err
	}

	loc.StoragePath = &key
	loc.Status = models.AttachmentStored
	return loc, nil
}

// MediaLink is a short-lived URL plus the moment it stops working, so the
// browser can cache it and know when to ask again.
type MediaLink struct {
	URL       string    `json:"url"`
	ExpiresAt time.Time `json:"expires_at"`
	FileName  string    `json:"file_name"`
	MimeType  string    `json:"mime_type"`
	Kind      string    `json:"kind"`
}

// AttachmentURL mints a signed URL for one attachment, fetching the file from
// WhatsApp first if this is the first time anyone has asked for it.
//
// The workspace argument is the access control: an attachment belonging to
// another tenant is not found, so no URL is ever minted for it.
func (m *Manager) AttachmentURL(
	ctx context.Context,
	workspaceID, attachmentID uuid.UUID,
	asDownload bool,
) (MediaLink, error) {
	loc, err := m.EnsureStored(ctx, workspaceID, attachmentID)
	if err != nil {
		return MediaLink{}, err
	}
	if loc.StoragePath == nil || *loc.StoragePath == "" {
		return MediaLink{}, ErrMediaUnavailable
	}

	name := ""
	if loc.FileName != nil {
		name = *loc.FileName
	}
	// The filename travels inside the link so the file saves under its real
	// name; the HTML download attribute is ignored across origins, which both
	// backends are.
	wantName := ""
	if asDownload {
		wantName = name
	}

	ttl := m.cfg.MediaURLTTL
	signed, err := m.store.SignedURL(ctx, *loc.StoragePath, ttl, wantName)
	if err != nil {
		return MediaLink{}, err
	}

	mime := ""
	if loc.MimeType != nil {
		mime = *loc.MimeType
	}
	return MediaLink{
		URL:       signed,
		ExpiresAt: time.Now().Add(ttl),
		FileName:  name,
		MimeType:  mime,
		Kind:      loc.Kind,
	}, nil
}

// --- outgoing ----------------------------------------------------------------

// SendMediaInput describes one file to send. One file per call: that is what
// makes per-file progress and per-file retry possible on the client.
type SendMediaInput struct {
	WorkspaceID    uuid.UUID
	ConversationID uuid.UUID
	SentBy         uuid.UUID
	File           media.File
	Caption        string
	// ClientToken is the browser's idempotency key for this file. A retry
	// carries the same token, which is what stops a duplicate message.
	ClientToken string
	// ReplyTo is the message this file answers, or nil.
	//
	// Text has carried a quote since replies existed; media never did, so a
	// photo sent as a reply arrived as a photo sent to nobody in particular. In
	// a group that is not a cosmetic loss: the quote is the only thing saying
	// which of forty messages the picture is about.
	ReplyTo *repository.MessageTarget
	// Source holds the plaintext bytes, positioned at the start. The caller owns
	// its lifetime and removes it once this returns.
	Source *os.File
}

// SendMedia uploads one file and sends it as a WhatsApp message.
//
// The order of operations is deliberate:
//
//  1. The message row is written first, as `pending`, so the bubble appears
//     immediately and a crash mid-send leaves a visible failure rather than
//     nothing at all.
//  2. The file goes to our own bucket next, so the sender sees their own image
//     even while WhatsApp's upload is still running.
//  3. Only then does it go to WhatsApp.
//
// A failure at step 3 leaves the row `failed` with the file already stored, so
// retrying with the same client token re-sends without re-doing any of it.
func (m *Manager) SendMedia(ctx context.Context, in SendMediaInput) (*models.Message, error) {
	if m.store == nil {
		return nil, ErrMediaDisabled
	}

	conv, err := m.repo.GetConversation(ctx, in.WorkspaceID, in.ConversationID)
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

	caption := media.TrimCaption(in.Caption)

	// Resolve an existing attempt before creating anything.
	msg, attachmentID, err := m.claimOutgoing(ctx, s, conv, in, caption)
	if err != nil {
		return nil, err
	}
	if msg == nil {
		return nil, errors.New("wa: could not claim a message row for this file")
	}
	if msg.Status != models.MessageStatusPending && msg.Status != models.MessageStatusFailed {
		return msg, nil // already sent: a duplicate request, not a retry
	}

	// Step 2 — our bucket. Doing this before WhatsApp means the operator's own
	// image is visible immediately, and a retry never re-uploads it.
	key := storageKey(in.WorkspaceID, conv.AccountID, msg.ID, 0, in.File.Ext)
	if err := m.storeOutgoing(ctx, key, in, attachmentID); err != nil {
		return m.failOutgoing(ctx, conv, msg, fmt.Errorf("simpan berkas: %w", err))
	}

	// Step 3 — WhatsApp.
	waMsg, err := m.buildOutgoing(ctx, s, in, caption)
	if err != nil {
		return m.failOutgoing(ctx, conv, msg, err)
	}

	// The upload above already took real time, so the typing indicator is
	// driven by the caption alone rather than by the file — a person attaching
	// a photo does not type for the length of the picture.
	m.readBeforeReplying(ctx, in.WorkspaceID, conv)
	presence := types.ChatPresenceMediaText
	if in.File.Kind == media.KindAudio {
		presence = types.ChatPresenceMediaAudio
	}
	release := s.beginSend(ctx, chatJID, caption, presence)
	defer release()

	resp, sendErr := s.client.SendMessage(ctx, chatJID, waMsg,
		whatsmeow.SendRequestExtra{ID: types.MessageID(msg.WAMessageID)})
	if sendErr != nil {
		return m.failOutgoing(ctx, conv, msg, sendErr)
	}

	sentAt := resp.Timestamp
	updated, err := m.repo.SetMessageOutcome(ctx, msg.ID, models.MessageStatusSent, &sentAt, nil)
	if err != nil {
		return msg, nil // it went out; the receipt handler will correct the row
	}
	// Read back in full rather than returning the row as written.
	//
	// The row carries the id of the message being answered; the quote bubble
	// needs the name and the line of text behind it, and only this read joins
	// them. Returning the bare row sent the browser a reply with quoted: null,
	// so a photo answering a customer drew no quote until the operator left the
	// conversation and came back, at which point the list query filled it in.
	// The text path has always read back this way, which is why a typed reply
	// showed its quote at once and a picture did not.
	if full, err := m.repo.GetMessageByID(ctx, in.WorkspaceID, msg.ID); err == nil {
		updated = full
	} else if list, err := m.repo.AttachmentsForMessage(ctx, msg.ID); err == nil {
		updated.Attachments = list
	}

	m.broadcastMessageStatus(in.WorkspaceID, conv.AccountID, conv.ID, updated)
	if refreshed, err := m.repo.GetConversationByID(ctx, conv.ID); err == nil {
		m.hub.Broadcast(in.WorkspaceID, realtime.EventConversationUpdate, refreshed)
	}
	// A photo with a caption answers a customer exactly as a text message does,
	// so it closes an SLA cycle the same way.
	m.RefreshConversationMetrics(ctx, in.WorkspaceID, conv.ID)
	return updated, nil
}

// claimOutgoing returns the message row this send should use, creating it when
// the token is new and reusing the existing row when it is a retry.
func (m *Manager) claimOutgoing(
	ctx context.Context,
	s *Session,
	conv *models.Conversation,
	in SendMediaInput,
	caption string,
) (*models.Message, uuid.UUID, error) {
	body := outgoingBody(in)

	if in.ClientToken != "" {
		existing, err := m.repo.GetMessageByClientToken(ctx, conv.AccountID, in.ClientToken)
		if err == nil {
			// A retry may carry a caption the operator changed after the first
			// attempt failed. WhatsApp is about to receive the new one, so the
			// stored row has to match it or the two sides would disagree about
			// what was sent.
			if existing.Status == models.MessageStatusFailed {
				if updated, err := m.repo.SetMessageContent(ctx, existing.ID, body, strPtr(caption)); err == nil {
					existing = updated
				}
			}
			if len(existing.Attachments) > 0 {
				return existing, existing.Attachments[0].ID, nil
			}
			// The row exists without its attachment, which means a previous
			// attempt died between the two inserts. Complete it rather than
			// sending a file the thread has no record of.
			att, err := m.insertOutgoingAttachment(ctx, conv, existing.ID, in)
			if err != nil {
				return nil, uuid.Nil, err
			}
			existing.Attachments = []models.Attachment{*att}
			return existing, att.ID, nil
		}
		if !errors.Is(err, repository.ErrNotFound) {
			return nil, uuid.Nil, err
		}
	}

	waID := s.client.GenerateMessageID()
	senderJID := ""
	if own := s.client.Store.ID; own != nil {
		senderJID = own.ToNonAD().String()
	}

	// Stored as well as sent. WhatsApp gets the quote through ContextInfo; this
	// column is what draws it in our own thread, and a bubble that shows no
	// quote here while the customer sees one is the same bug wearing the other
	// half of its clothes.
	var quotedID *string
	if in.ReplyTo != nil {
		quotedID = &in.ReplyTo.WAMessageID
	}

	msg, _, err := m.repo.InsertMessage(ctx, repository.InsertMessageInput{
		WorkspaceID:     conv.WorkspaceID,
		AccountID:       conv.AccountID,
		ConversationID:  conv.ID,
		WAMessageID:     waID,
		SenderJID:       strPtr(senderJID),
		FromMe:          true,
		Type:            string(in.File.Kind),
		Body:            body,
		Caption:         strPtr(caption),
		MediaMime:       strPtr(in.File.MIME),
		QuotedMessageID: quotedID,
		Status:          models.MessageStatusPending,
		Timestamp:       time.Now().UTC(),
		SentBy:          &in.SentBy,
		ClientToken:     strPtr(in.ClientToken),
	})
	if err != nil {
		if errors.Is(err, repository.ErrDuplicateClientToken) {
			// A concurrent retry won the race. Return its row; nothing is sent
			// twice because the send happens after this point.
			existing, lookupErr := m.repo.GetMessageByClientToken(ctx, conv.AccountID, in.ClientToken)
			if lookupErr != nil {
				return nil, uuid.Nil, err
			}
			id := uuid.Nil
			if len(existing.Attachments) > 0 {
				id = existing.Attachments[0].ID
			}
			return existing, id, nil
		}
		return nil, uuid.Nil, fmt.Errorf("persist outgoing message: %w", err)
	}

	att, err := m.insertOutgoingAttachment(ctx, conv, msg.ID, in)
	if err != nil {
		return nil, uuid.Nil, err
	}
	msg.Attachments = []models.Attachment{*att}

	// Show the pending bubble right away.
	m.hub.Broadcast(conv.WorkspaceID, realtime.EventMessageNew, map[string]any{
		"account_id": conv.AccountID,
		"message":    msg,
	})
	return msg, att.ID, nil
}

// outgoingBody is what goes in the message body column.
//
// A document keeps its filename there so the conversation preview and search
// have something to work with; other media rely on their caption instead.
func outgoingBody(in SendMediaInput) *string {
	if in.File.Kind != media.KindDocument {
		return nil
	}
	name := in.File.Name
	return &name
}

func (m *Manager) insertOutgoingAttachment(
	ctx context.Context,
	conv *models.Conversation,
	messageID uuid.UUID,
	in SendMediaInput,
) (*models.Attachment, error) {
	size := in.File.Size
	att, _, err := m.repo.InsertAttachment(ctx, repository.InsertAttachmentInput{
		WorkspaceID: conv.WorkspaceID,
		AccountID:   conv.AccountID,
		MessageID:   messageID,
		Index:       0,
		Kind:        string(in.File.Kind),
		FileName:    &in.File.Name,
		MimeType:    strPtr(in.File.MIME),
		SizeBytes:   &size,
		Status:      models.AttachmentUploading,
	})
	if err != nil {
		return nil, fmt.Errorf("persist attachment: %w", err)
	}
	return att, nil
}

// storeOutgoing copies the plaintext into the private bucket. It is skipped
// when a previous attempt already stored it, which is what makes a retry cheap.
func (m *Manager) storeOutgoing(ctx context.Context, key string, in SendMediaInput, attachmentID uuid.UUID) error {
	if attachmentID != uuid.Nil {
		if loc, err := m.repo.GetAttachmentLocation(ctx, in.WorkspaceID, attachmentID); err == nil &&
			loc.Status == models.AttachmentStored {
			return nil
		}
	}
	if _, err := in.Source.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if err := m.store.Upload(ctx, key, in.File.MIME, in.Source, in.File.Size); err != nil {
		return err
	}
	if attachmentID != uuid.Nil {
		return m.repo.MarkAttachmentStored(ctx, attachmentID, key, in.File.Size)
	}
	return nil
}

// buildOutgoing uploads the file to WhatsApp and wraps it in the right message
// type.
func (m *Manager) buildOutgoing(
	ctx context.Context,
	s *Session,
	in SendMediaInput,
	caption string,
) (*waE2E.Message, error) {
	mediaType := mediaTypeFor(string(in.File.Kind))

	if _, err := in.Source.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}

	// A photo has to reach WhatsApp as a JPEG or the phone cannot draw it. Only
	// the copy going to WhatsApp is converted; the bucket keeps what the
	// operator actually uploaded, so the chat screen still shows the original.
	// See normaliseImage.
	source, file, releaseConverted, err := normaliseImage(in.Source, in.File)
	if err != nil {
		return nil, err
	}
	defer releaseConverted()
	in.Source, in.File = source, file

	// whatsmeow encrypts into a scratch file rather than memory, so a large
	// video never has two full copies in RAM. Removed before this returns —
	// including on the error paths.
	scratch, err := os.CreateTemp("", "salesan-wa-*.enc")
	if err != nil {
		return nil, fmt.Errorf("temp file: %w", err)
	}
	defer func() {
		name := scratch.Name()
		_ = scratch.Close()
		_ = os.Remove(name)
	}()

	up, err := s.client.UploadReader(ctx, in.Source, scratch, mediaType)
	if err != nil {
		return nil, fmt.Errorf("unggah ke whatsapp: %w", err)
	}

	var capPtr *string
	if caption != "" {
		capPtr = proto.String(caption)
	}

	// The quote, built by the same function text replies use. Every media kind
	// carries its own ContextInfo field rather than one shared place, so it has
	// to be set on each of them: forgetting one is how a reply survives on a
	// photo and vanishes on a video.
	var ctxInfo *waE2E.ContextInfo
	if in.ReplyTo != nil {
		ctxInfo = quoteContext(s, in.ReplyTo)
	}

	switch in.File.Kind {
	case media.KindImage:
		img := &waE2E.ImageMessage{
			URL:           proto.String(up.URL),
			DirectPath:    proto.String(up.DirectPath),
			MediaKey:      up.MediaKey,
			FileEncSHA256: up.FileEncSHA256,
			FileSHA256:    up.FileSHA256,
			FileLength:    proto.Uint64(up.FileLength),
			Mimetype:      proto.String(in.File.MIME),
			Caption:       capPtr,
			ContextInfo:   ctxInfo,
		}
		if thumb, w, h := imagePreview(in.Source); thumb != nil {
			img.JPEGThumbnail = thumb
			img.Width = proto.Uint32(uint32(w))
			img.Height = proto.Uint32(uint32(h))
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
			Mimetype:      proto.String(in.File.MIME),
			Caption:       capPtr,
			ContextInfo:   ctxInfo,
		}}, nil

	case media.KindAudio:
		return &waE2E.Message{AudioMessage: &waE2E.AudioMessage{
			URL:           proto.String(up.URL),
			DirectPath:    proto.String(up.DirectPath),
			MediaKey:      up.MediaKey,
			FileEncSHA256: up.FileEncSHA256,
			FileSHA256:    up.FileSHA256,
			FileLength:    proto.Uint64(up.FileLength),
			Mimetype:      proto.String(in.File.MIME),
			ContextInfo:   ctxInfo,
		}}, nil

	default:
		doc := &waE2E.DocumentMessage{
			URL:           proto.String(up.URL),
			DirectPath:    proto.String(up.DirectPath),
			MediaKey:      up.MediaKey,
			FileEncSHA256: up.FileEncSHA256,
			FileSHA256:    up.FileSHA256,
			FileLength:    proto.Uint64(up.FileLength),
			Mimetype:      proto.String(in.File.MIME),
			FileName:      proto.String(in.File.Name),
			Caption:       capPtr,
			ContextInfo:   ctxInfo,
		}
		// A document sent from the "send as file" path may still be an image;
		// giving it a thumbnail makes the bubble useful on the other end.
		if thumb, _, _ := imagePreview(in.Source); thumb != nil {
			doc.JPEGThumbnail = thumb
		}
		return &waE2E.Message{DocumentMessage: doc}, nil
	}
}

// failOutgoing records a send failure and pushes it to the UI, returning the
// original error so the HTTP layer can report it.
func (m *Manager) failOutgoing(
	ctx context.Context,
	conv *models.Conversation,
	msg *models.Message,
	cause error,
) (*models.Message, error) {
	detail := truncate(cause.Error(), 400)
	if failed, err := m.repo.SetMessageOutcome(ctx, msg.ID, models.MessageStatusFailed, nil, &detail); err == nil {
		msg = failed
		if list, err := m.repo.AttachmentsForMessage(ctx, msg.ID); err == nil {
			msg.Attachments = list
		}
	} else {
		m.log.Error("record media send failure", "err", err)
	}
	m.broadcastMessageStatus(conv.WorkspaceID, conv.AccountID, conv.ID, msg)
	return msg, fmt.Errorf("kirim media: %w", cause)
}

// --- helpers -----------------------------------------------------------------

func intPtr(v int) *int {
	if v <= 0 {
		return nil
	}
	return &v
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// RemoveStoredMedia deletes files whose rows have gone, so the store does not
// accumulate media nothing points at.
func (m *Manager) RemoveStoredMedia(ctx context.Context, keys []string) {
	m.removeStoredObjects(ctx, keys)
}

// removeStoredObjects deletes a batch of objects, chunked to stay inside the
// backend's limits.
func (m *Manager) removeStoredObjects(ctx context.Context, keys []string) {
	if m.store == nil || len(keys) == 0 {
		return
	}
	// Supabase caps a delete batch; chunk to stay well inside it.
	const chunk = 100
	for start := 0; start < len(keys); start += chunk {
		end := start + chunk
		if end > len(keys) {
			end = len(keys)
		}
		if err := m.store.Remove(ctx, keys[start:end]...); err != nil {
			m.log.Warn("remove stored media", "err", err)
		}
	}
}

// ensureStorage prepares wherever media is going to live, so a fresh install
// works without a manual step.
func (m *Manager) ensureStorage(ctx context.Context) {
	if m.store == nil {
		m.log.Error("media storage unavailable: photo, video and document messages will be refused")
		return
	}
	if err := m.store.EnsureReady(ctx); err != nil {
		m.log.Error("prepare media storage", "backend", m.store.Name(), "err", err)
		m.store = nil // refuse cleanly rather than fail mid-upload
		return
	}
	m.log.Info("media storage ready", "backend", m.store.Name())
}

// LocalStore returns the filesystem backend when that is what is in use, so
// the API can serve signed links from it. Nil on Supabase.
func (m *Manager) LocalStore() *storage.Local {
	local, _ := m.store.(*storage.Local)
	return local
}
