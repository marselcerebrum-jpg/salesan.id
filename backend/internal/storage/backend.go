package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"time"
)

// Backend is where media bytes rest.
type Backend interface {
	// Name identifies the backend in logs and in the health endpoint.
	Name() string
	// EnsureReady prepares the destination — creating the bucket or the
	// directory — and reports whether media can be stored at all.
	EnsureReady(ctx context.Context) error
	// Upload streams an object, replacing anything already at that key.
	Upload(ctx context.Context, key, contentType string, r io.Reader, size int64) error
	// UploadBytes is the in-memory form, for files small enough that a stream
	// would cost more than it saves.
	UploadBytes(ctx context.Context, key, contentType string, data []byte) error
	// SignedURL mints a link that works without an Authorization header and
	// stops working when ttl elapses. `download` asks for a save dialog with
	// the given filename rather than inline display.
	SignedURL(ctx context.Context, key string, ttl time.Duration, download string) (string, error)
	// Download reads an object back, returning its bytes and content type.
	//
	// Whole-file rather than streamed: the only caller is the broadcast runner
	// fetching one document it has already size-capped, and a []byte it can hand
	// straight to the encryptor is simpler than a reader it must clean up on six
	// different error paths.
	Download(ctx context.Context, key string) ([]byte, string, error)
	// Remove deletes objects. A missing object is not an error.
	Remove(ctx context.Context, keys ...string) error
}

// ErrNotFound is returned when an object is absent.
var ErrNotFound = errors.New("storage: object not found")

// validKey is the shape every object key must have.
//
// Keys are built by the server from ids it controls, never from an uploaded
// filename — but the check is enforced here anyway, at the point where a key
// becomes a filesystem path. A rule that is only obeyed by convention is a rule
// that eventually is not.
var validKey = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._\-]*(/[A-Za-z0-9][A-Za-z0-9._\-]*)*$`)

func checkKey(key string) error {
	if key == "" || len(key) > 512 || !validKey.MatchString(key) {
		return fmt.Errorf("storage: refusing malformed object key %q", key)
	}
	// The pattern already forbids a segment starting with a dot, so "..' cannot
	// appear as a path element; this is belt and braces at the boundary.
	if bytes.Contains([]byte(key), []byte("..")) {
		return fmt.Errorf("storage: refusing traversal in object key %q", key)
	}
	return nil
}
