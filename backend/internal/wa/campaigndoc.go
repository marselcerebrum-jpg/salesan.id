package wa

import (
	"context"
	"fmt"
	"io"
	"path"

	"github.com/google/uuid"

	"github.com/salesan/omnichannel/backend/internal/media"
)

// Documents attached to a broadcast.
//
// Images and videos travel as a link: the server fetches the URL just before
// sending and throws the bytes away. A document cannot, because its filename is
// shown on the recipient's screen — "Katalog-Produk.pdf" has to arrive as that,
// not as whatever the last path segment of some URL happened to be — and because
// the file usually does not live anywhere public to begin with.
//
// So the file is uploaded once into the private bucket, sent from there, and
// deleted when the campaign is over. It never touches the database: the row
// keeps the object key and the display name, nothing else.

// MaxCampaignDocumentBytes caps a broadcast document.
//
// Well under WhatsApp's own limit. The number that matters here is not what
// WhatsApp accepts but what a campaign can afford to move: the file is uploaded
// once per sending number, so a large document on twenty numbers is twenty
// uploads before a single message goes out.
const MaxCampaignDocumentBytes = 16 << 20

// campaignDocKey is where one campaign document lives.
//
// Built from ids the server controls and never from the uploaded name, so a
// hostile filename cannot decide where the object lands. The display name is
// kept in the database and applied when the file is sent.
func campaignDocKey(workspaceID, docID uuid.UUID, ext string) string {
	return path.Join("campaign-docs", workspaceID.String(), docID.String()+ext)
}

// UploadCampaignDocument stores a validated document and returns its key.
//
// `file` has already been through media.Classify, so its type was decided by the
// file's own leading bytes rather than by what the browser claimed.
func (m *Manager) UploadCampaignDocument(
	ctx context.Context, workspaceID uuid.UUID, file media.File, src io.Reader, size int64,
) (string, error) {
	if !m.MediaEnabled() {
		return "", ErrMediaDisabled
	}
	if size > MaxCampaignDocumentBytes {
		return "", fmt.Errorf("wa: dokumen melebihi batas %d MB", MaxCampaignDocumentBytes>>20)
	}

	key := campaignDocKey(workspaceID, uuid.New(), media.ExtensionForMIME(file.MIME))
	if err := m.store.Upload(ctx, key, file.MIME, src, size); err != nil {
		return "", err
	}
	return key, nil
}

// OpenCampaignDocument reads a stored document back for sending.
func (m *Manager) OpenCampaignDocument(ctx context.Context, key string) ([]byte, string, error) {
	if m.store == nil {
		return nil, "", ErrMediaDisabled
	}
	return m.store.Download(ctx, key)
}

// RemoveCampaignDocument deletes a stored document.
//
// Called once a campaign is finished. A missing object is not an error: the
// point is that it is gone, not that this call is what removed it.
func (m *Manager) RemoveCampaignDocument(ctx context.Context, key string) error {
	if m.store == nil || key == "" {
		return nil
	}
	return m.store.Remove(ctx, key)
}
