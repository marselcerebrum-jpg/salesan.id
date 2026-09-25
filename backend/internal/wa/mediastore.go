package wa

import (
	"context"
	"encoding/hex"
	"fmt"

	"github.com/google/uuid"

	"github.com/salesan/omnichannel/backend/internal/repository"
	"github.com/salesan/omnichannel/backend/internal/storage"
)

// Storing one file once, however many messages carry it.
//
// A broadcast to sixty contacts writes sixty message rows and sixty attachment
// rows for one photo. Each of those attachments used to be stored under a key
// built from its own message id, so the same bytes were downloaded from WhatsApp
// and uploaded to the bucket sixty times over. The object's identity is its
// content, so that is what the key is made of now.

// objectIndex is the part of the repository this layer needs.
//
// Narrow on purpose: it is what lets the store-once rules be tested without a
// database, and it keeps this file honest about how little state it touches.
type objectIndex interface {
	ClaimStoredObject(
		ctx context.Context, workspaceID uuid.UUID, sha []byte, attachmentID uuid.UUID,
	) (*repository.StoredObject, error)
	RegisterStoredObject(
		ctx context.Context, workspaceID uuid.UUID, sha []byte, path string, size int64,
	) error
	MarkAttachmentStored(ctx context.Context, id uuid.UUID, path string, size int64) error
}

// hashStorageKey is where a file with this content lives.
//
// Computed from the hash and nothing else, so every caller that holds the same
// bytes computes the same key. That is what makes two simultaneous uploads
// harmless: they write identical bytes to one key instead of racing to create
// two objects, one of which nothing would ever point at or clean up.
//
// No extension. One would have to come from the file name or the declared type,
// both of which can differ between two rows describing the same content, and a
// key that varies is a key that duplicates. Nothing reads the type from the key:
// the browser is served a sniffed content type and the download name is applied
// through the signed URL.
func hashStorageKey(workspaceID uuid.UUID, sha []byte) string {
	return fmt.Sprintf("%s/by-hash/%s", workspaceID, hex.EncodeToString(sha))
}

// storeRequest is one attachment that needs its bytes in the bucket.
type storeRequest struct {
	WorkspaceID  uuid.UUID
	AttachmentID uuid.UUID

	// SHA256 is WhatsApp's own hash of the file as it is, already on the media
	// ref. Empty means this attachment cannot be deduplicated and lands at
	// FallbackKey instead; that is a broken ref rather than a normal case, since
	// the hash is also what verifies the download.
	SHA256      []byte
	FallbackKey string

	ContentType string

	// Fetch produces the bytes. Called only when the content is not in the
	// bucket already, which is the whole point: for the fifty-ninth recipient of
	// a broadcast it is never called at all.
	Fetch func(ctx context.Context) ([]byte, error)
}

// storeOnce makes the attachment point at its content in the bucket, uploading
// the bytes only if nothing there holds them yet.
//
// On return the attachment row is marked stored and points at the object, so the
// caller has nothing left to record.
func storeOnce(
	ctx context.Context,
	idx objectIndex,
	store storage.Backend,
	req storeRequest,
) (*repository.StoredObject, error) {
	if obj, err := idx.ClaimStoredObject(ctx, req.WorkspaceID, req.SHA256, req.AttachmentID); err != nil {
		return nil, err
	} else if obj != nil {
		return obj, nil
	}

	data, err := req.Fetch(ctx)
	if err != nil {
		return nil, err
	}

	key := req.FallbackKey
	if len(req.SHA256) > 0 {
		key = hashStorageKey(req.WorkspaceID, req.SHA256)
	}
	if err := store.UploadBytes(ctx, key, req.ContentType, data); err != nil {
		return nil, err
	}
	size := int64(len(data))

	if len(req.SHA256) == 0 {
		if err := idx.MarkAttachmentStored(ctx, req.AttachmentID, key, size); err != nil {
			return nil, err
		}
		return &repository.StoredObject{StoragePath: key, SizeBytes: size}, nil
	}

	if err := idx.RegisterStoredObject(ctx, req.WorkspaceID, req.SHA256, key, size); err != nil {
		return nil, err
	}

	// Claimed rather than assumed. Usually this finds the row just written; when
	// another caller won the race it finds theirs, which is the same path. What
	// it can also find is nothing at all, if the janitor released this content
	// between the upload and here — a corner narrow enough that the honest
	// answer is to report the file as not ready yet and let the next request
	// redo it, rather than mark it stored and be wrong about where it is.
	obj, err := idx.ClaimStoredObject(ctx, req.WorkspaceID, req.SHA256, req.AttachmentID)
	if err != nil {
		return nil, err
	}
	if obj == nil {
		return nil, ErrMediaNotReady
	}
	return obj, nil
}
