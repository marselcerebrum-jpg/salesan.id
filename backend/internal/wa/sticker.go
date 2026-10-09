package wa

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/google/uuid"

	"github.com/salesan/omnichannel/backend/internal/media"
	"github.com/salesan/omnichannel/backend/internal/models"
	"github.com/salesan/omnichannel/backend/internal/repository"
)

// stickerURLTTL is how long a link to a sticker lives.
//
// Longer than an attachment's, because the panel shows every sticker at once
// and re-signing forty links each time somebody opens it is forty round trips
// for pictures that do not change. Still short enough that a link copied out
// of the page stops working the same day.
const stickerURLTTL = 6 * time.Hour

// stickerKey is where a sticker's bytes live.
//
// Addressed by content hash rather than by id, which is what lets two people
// upload the same picture and end up sharing one object — the same rule the
// rest of the media here follows.
func stickerKey(workspaceID uuid.UUID, sha string) string {
	return fmt.Sprintf("%s/stickers/%s.webp", workspaceID, sha)
}

// AddSticker puts one picture into a workspace's sticker library.
//
// The file arrives already converted: WhatsApp takes WebP at 512 square and
// refuses everything else from its own server, so the browser does the
// conversion where it can show the result. This checks rather than trusts.
func (m *Manager) AddSticker(
	ctx context.Context,
	workspaceID, userID uuid.UUID,
	name string,
	file media.File,
	source *os.File,
) (*repository.Sticker, error) {
	if !m.MediaEnabled() {
		return nil, ErrMediaDisabled
	}
	if file.Kind != media.KindSticker {
		return nil, fmt.Errorf("%w: stiker harus WebP", media.ErrTypeBlocked)
	}

	if _, err := source.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	sum := sha256.New()
	if _, err := io.Copy(sum, source); err != nil {
		return nil, err
	}
	sha := hex.EncodeToString(sum.Sum(nil))

	key := stickerKey(workspaceID, sha)
	if _, err := source.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	if err := m.store.Upload(ctx, key, file.MIME, source, file.Size); err != nil {
		return nil, fmt.Errorf("simpan stiker: %w", err)
	}

	var namePtr *string
	if name != "" {
		namePtr = &name
	}
	return m.repo.AddSticker(ctx, workspaceID, namePtr, key, file.MIME, file.Size, sha, userID)
}

// StickerURL mints a link the panel can draw from.
func (m *Manager) StickerURL(ctx context.Context, path string) (string, error) {
	if !m.MediaEnabled() {
		return "", ErrMediaDisabled
	}
	return m.store.SignedURL(ctx, path, stickerURLTTL, "")
}

// SendSticker sends one sticker from the library into a conversation.
//
// The bytes come back out of our own bucket and go through SendMedia like any
// other file, which is the point: one send path means one set of rules about
// dedup, retries, the message row, the quote, and what the browser is told
// afterwards. A second path written for stickers would be a second place for
// all of that to drift.
func (m *Manager) SendSticker(
	ctx context.Context,
	workspaceID, conversationID, stickerID, sentBy uuid.UUID,
	clientToken string,
	replyTo *repository.MessageTarget,
) (*models.Message, error) {
	st, err := m.repo.GetSticker(ctx, workspaceID, stickerID)
	if err != nil {
		return nil, err
	}

	data, _, err := m.store.Download(ctx, st.StoragePath)
	if err != nil {
		return nil, fmt.Errorf("ambil stiker dari penyimpanan: %w", err)
	}

	// A temp file rather than the bytes in hand: SendMedia reads its source
	// more than once — for the upload and again for the hash — and rewinds
	// between. Removed before this returns whatever happens.
	tmp, err := os.CreateTemp("", "salesan-sticker-*.webp")
	if err != nil {
		return nil, err
	}
	defer func() {
		name := tmp.Name()
		_ = tmp.Close()
		_ = os.Remove(name)
	}()
	if _, err := tmp.Write(data); err != nil {
		return nil, err
	}

	label := "stiker.webp"
	if st.Name != nil && *st.Name != "" {
		label = *st.Name + ".webp"
	}

	return m.SendMedia(ctx, SendMediaInput{
		WorkspaceID:    workspaceID,
		ConversationID: conversationID,
		SentBy:         sentBy,
		ClientToken:    clientToken,
		ReplyTo:        replyTo,
		Source:         tmp,
		File: media.File{
			Kind: media.KindSticker,
			MIME: st.MIME,
			Name: label,
			Ext:  ".webp",
			Size: int64(len(data)),
		},
	})
}
