package wa

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path"

	"github.com/google/uuid"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"

	"github.com/salesan/omnichannel/backend/internal/media"
	"github.com/salesan/omnichannel/backend/internal/models"
	"github.com/salesan/omnichannel/backend/internal/repository"
)

// ErrNotForwardable covers messages there is nothing to forward from.
var ErrNotForwardable = errors.New("wa: this message cannot be forwarded")

// quoteContext builds the ContextInfo that turns a message into a reply.
//
// The quoted message is embedded rather than merely referenced because that is
// what the recipient's client renders in the quote bubble — a bare id would
// show an empty box on a device that never received the original.
func quoteContext(s *Session, target *repository.MessageTarget) *waE2E.ContextInfo {
	ctxInfo := &waE2E.ContextInfo{
		StanzaID:      proto.String(target.WAMessageID),
		QuotedMessage: quotedPayload(target),
	}
	// Participant names who wrote the quoted message. WhatsApp requires it, and
	// for our own messages it is this account.
	participant := target.SenderJID
	if participant == "" {
		if own := s.client.Store.ID; own != nil {
			participant = own.ToNonAD().String()
		}
	}
	if participant != "" {
		ctxInfo.Participant = proto.String(participant)
	}
	return ctxInfo
}

// quotedPayload reconstructs enough of the original for the quote to render.
//
// Only the visible part is rebuilt: the quote bubble shows a line of text or a
// "photo" label, never the media itself, so there is nothing to gain from
// carrying the original's keys around.
func quotedPayload(t *repository.MessageTarget) *waE2E.Message {
	text := ""
	if t.Body != nil {
		text = *t.Body
	}
	if text == "" && t.Caption != nil {
		text = *t.Caption
	}

	switch t.Type {
	case "image":
		return &waE2E.Message{ImageMessage: &waE2E.ImageMessage{Caption: proto.String(text)}}
	case "video":
		return &waE2E.Message{VideoMessage: &waE2E.VideoMessage{Caption: proto.String(text)}}
	case "audio":
		return &waE2E.Message{AudioMessage: &waE2E.AudioMessage{}}
	case "document":
		return &waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{
			FileName: proto.String(text),
			Caption:  proto.String(text),
		}}
	default:
		if text == "" {
			text = " " // an empty quote renders as a broken box
		}
		return &waE2E.Message{Conversation: proto.String(text)}
	}
}

// ReplyTarget loads the message a reply is answering, checking it belongs to
// the same conversation the reply is going to.
func (m *Manager) ReplyTarget(
	ctx context.Context,
	workspaceID, conversationID, messageID uuid.UUID,
) (*repository.MessageTarget, error) {
	target, err := m.repo.MessageTargetByID(ctx, workspaceID, messageID)
	if err != nil {
		return nil, err
	}
	// Quoting a message from another thread would produce a quote the recipient
	// cannot resolve, so it is refused rather than sent half-broken.
	if target.ConversationID != conversationID {
		return nil, fmt.Errorf("%w: pesan berasal dari percakapan lain", ErrNotForwardable)
	}
	return target, nil
}

// ForwardMessage re-sends a message into other conversations.
//
// Text is re-sent as text. Media is re-sent by reading the file back out of our
// own storage and uploading it again, rather than by replaying the original's
// media keys: those keys expire with WhatsApp's copy, and a forward that worked
// only for a fortnight would be worse than one that always works.
//
// Each target is independent — one failure does not stop the rest, and the
// caller is told which ones did not go.
func (m *Manager) ForwardMessage(
	ctx context.Context,
	workspaceID, messageID uuid.UUID,
	targets []uuid.UUID,
	sentBy uuid.UUID,
) ([]*models.Message, map[uuid.UUID]string, error) {
	source, err := m.repo.MessageTargetByID(ctx, workspaceID, messageID)
	if err != nil {
		return nil, nil, err
	}
	if source.RevokedAt != nil {
		return nil, nil, fmt.Errorf("%w: pesan sudah dihapus", ErrNotForwardable)
	}

	sent := []*models.Message{}
	failures := map[uuid.UUID]string{}

	if source.HasAttachment {
		file, cleanup, spec, err := m.stageForwardedMedia(ctx, workspaceID, source)
		if err != nil {
			return nil, nil, err
		}
		defer cleanup()

		for _, target := range targets {
			msg, err := m.SendMedia(ctx, SendMediaInput{
				WorkspaceID:    workspaceID,
				ConversationID: target,
				SentBy:         sentBy,
				File:           spec,
				Caption:        derefString(source.Caption),
				// A fresh token per target: each forward is its own message.
				ClientToken: uuid.NewString(),
				Source:      file,
			})
			if err != nil {
				failures[target] = err.Error()
				continue
			}
			sent = append(sent, msg)
		}
		return sent, failures, nil
	}

	text := derefString(source.Body)
	if text == "" {
		text = derefString(source.Caption)
	}
	if text == "" {
		return nil, nil, fmt.Errorf("%w: tidak ada isi yang bisa diteruskan", ErrNotForwardable)
	}

	for _, target := range targets {
		msg, err := m.SendText(ctx, workspaceID, target, text, sentBy, nil)
		if err != nil {
			failures[target] = err.Error()
			continue
		}
		sent = append(sent, msg)
	}
	return sent, failures, nil
}

// stageForwardedMedia copies the stored file into a temp file that SendMedia
// can read, and describes it well enough to be sent again.
func (m *Manager) stageForwardedMedia(
	ctx context.Context,
	workspaceID uuid.UUID,
	source *repository.MessageTarget,
) (*os.File, func(), media.File, error) {
	noop := func() {}

	attachments, err := m.repo.AttachmentsForMessage(ctx, source.MessageID)
	if err != nil {
		return nil, noop, media.File{}, err
	}
	if len(attachments) == 0 {
		return nil, noop, media.File{}, fmt.Errorf("%w: lampiran tidak ada", ErrNotForwardable)
	}
	att := attachments[0]

	// Makes sure the bytes are actually in storage — an incoming file that
	// nobody has opened yet is only a row until this runs.
	loc, err := m.EnsureStored(ctx, workspaceID, att.ID)
	if err != nil {
		return nil, noop, media.File{}, err
	}
	if loc.StoragePath == nil || *loc.StoragePath == "" {
		return nil, noop, media.File{}, ErrMediaUnavailable
	}

	data, _, err := m.readStored(ctx, *loc.StoragePath)
	if err != nil {
		return nil, noop, media.File{}, fmt.Errorf("baca berkas tersimpan: %w", err)
	}

	tmp, err := os.CreateTemp("", "salesan-fwd-*")
	if err != nil {
		return nil, noop, media.File{}, err
	}
	cleanup := func() {
		name := tmp.Name()
		_ = tmp.Close()
		_ = os.Remove(name)
	}
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return nil, noop, media.File{}, err
	}

	name := "berkas"
	if att.FileName != nil && *att.FileName != "" {
		name = *att.FileName
	}
	mime := "application/octet-stream"
	if att.MimeType != nil && *att.MimeType != "" {
		mime = *att.MimeType
	}

	return tmp, cleanup, media.File{
		Kind: media.Kind(att.Kind),
		MIME: mime,
		Name: name,
		Ext:  path.Ext(name),
		Size: int64(len(data)),
	}, nil
}

// readStored pulls an object back out of whichever backend holds it.
func (m *Manager) readStored(ctx context.Context, key string) ([]byte, string, error) {
	if m.store == nil {
		return nil, "", ErrMediaDisabled
	}
	if local := m.LocalStore(); local != nil {
		file, info, err := local.Open(key)
		if err != nil {
			return nil, "", err
		}
		defer func() { _ = file.Close() }()

		buf := bytes.NewBuffer(make([]byte, 0, info.Size()))
		if _, err := buf.ReadFrom(file); err != nil {
			return nil, "", err
		}
		return buf.Bytes(), "", nil
	}
	if remote, ok := m.store.(interface {
		Download(ctx context.Context, key string) ([]byte, string, error)
	}); ok {
		return remote.Download(ctx, key)
	}
	return nil, "", ErrMediaUnavailable
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
