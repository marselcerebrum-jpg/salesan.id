package storage

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Local stores media on this machine's disk and serves it back through the
// API under a signed, expiring URL.
//
// This is the fallback when Supabase Storage has no service-role key. It is a
// genuine implementation, not a stub: the files are outside the database, the
// directory is not web-served, and a link cannot be guessed or replayed after
// it expires. What it does not give you is durability beyond this machine —
// the disk is the disk.
type Local struct {
	root    string
	baseURL string
	secret  []byte
}

// NewLocal builds the filesystem backend.
//
//   - root    : directory the files live in
//   - baseURL : how a browser reaches this API, e.g. http://localhost:8080
//   - secret  : HMAC key for signing links
func NewLocal(root, baseURL string, secret []byte) *Local {
	return &Local{
		root:    root,
		baseURL: strings.TrimRight(baseURL, "/"),
		secret:  secret,
	}
}

func (l *Local) Name() string { return "local:" + l.root }

// Root is the directory being used, for the setup log line.
func (l *Local) Root() string { return l.root }

func (l *Local) EnsureReady(_ context.Context) error {
	if err := os.MkdirAll(l.root, 0o700); err != nil {
		return fmt.Errorf("create media directory %s: %w", l.root, err)
	}
	// 0700 is the point: the files are customer photos and documents, and
	// nothing but this process has any business reading them.
	return os.Chmod(l.root, 0o700)
}

// path resolves a key to a filesystem path, refusing anything that could
// escape the root.
func (l *Local) path(key string) (string, error) {
	if err := checkKey(key); err != nil {
		return "", err
	}
	full := filepath.Join(l.root, filepath.FromSlash(key))

	// Second line of defence: whatever Join produced must still be inside the
	// root. A key that passed the pattern but resolved outside is a bug worth
	// failing loudly on.
	rel, err := filepath.Rel(l.root, full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("storage: key %q resolves outside the media directory", key)
	}
	return full, nil
}

func (l *Local) Upload(_ context.Context, key, _ string, r io.Reader, _ int64) error {
	full, err := l.path(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
		return err
	}

	// Written to a neighbouring temp file and renamed, so a reader never sees a
	// half-written image and a crash mid-upload leaves no truncated file
	// pretending to be complete.
	tmp, err := os.CreateTemp(filepath.Dir(full), ".partial-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName) // no-op once the rename has succeeded
	}()

	if _, err := io.Copy(tmp, r); err != nil {
		return fmt.Errorf("write %s: %w", key, err)
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		return err
	}
	return os.Rename(tmpName, full)
}

func (l *Local) UploadBytes(ctx context.Context, key, contentType string, data []byte) error {
	return l.Upload(ctx, key, contentType, strings.NewReader(string(data)), int64(len(data)))
}

// Download reads an object back. The content type is inferred from the
// extension, which is all a local directory knows about it.
func (l *Local) Download(_ context.Context, key string) ([]byte, string, error) {
	full, err := l.path(key)
	if err != nil {
		return nil, "", err
	}
	data, err := os.ReadFile(full)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, "", ErrNotFound
		}
		return nil, "", err
	}
	return data, mime.TypeByExtension(filepath.Ext(key)), nil
}

// Open returns a file for the media handler to serve.
func (l *Local) Open(key string) (*os.File, os.FileInfo, error) {
	full, err := l.path(key)
	if err != nil {
		return nil, nil, err
	}
	f, err := os.Open(full)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil, ErrNotFound
		}
		return nil, nil, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, nil, err
	}
	return f, info, nil
}

func (l *Local) Remove(_ context.Context, keys ...string) error {
	for _, key := range keys {
		full, err := l.path(key)
		if err != nil {
			continue // a malformed key names no file of ours
		}
		if err := os.Remove(full); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		// Tidy the now-empty per-message directory. Best effort: a non-empty
		// parent simply refuses, which is the desired outcome.
		_ = os.Remove(filepath.Dir(full))
	}
	return nil
}

// --- link signing -------------------------------------------------------------

// mediaClaim is what a signed link carries.
type mediaClaim struct {
	Key      string `json:"k"`
	Expires  int64  `json:"e"`
	Download string `json:"d,omitempty"`
}

// MediaURLPath is where the signed links point. The handler is mounted outside
// the authenticated group: an <img> tag cannot send an Authorization header, so
// the signature is the credential — exactly as it is for a Supabase signed URL.
const MediaURLPath = "/api/v1/media"

func (l *Local) SignedURL(_ context.Context, key string, ttl time.Duration, download string) (string, error) {
	if err := checkKey(key); err != nil {
		return "", err
	}
	if ttl < time.Minute {
		ttl = time.Minute
	}

	payload, err := json.Marshal(mediaClaim{
		Key:      key,
		Expires:  time.Now().Add(ttl).Unix(),
		Download: download,
	})
	if err != nil {
		return "", err
	}
	body := base64.RawURLEncoding.EncodeToString(payload)
	token := body + "." + l.sign(body)

	return l.baseURL + MediaURLPath + "/" + url.PathEscape(token), nil
}

// VerifyToken checks a signed link and returns what it grants.
//
// The signature is compared in constant time, and the expiry is checked after
// it — an attacker must not be able to learn anything from how long a rejection
// takes.
func (l *Local) VerifyToken(token string) (key, download string, err error) {
	body, sig, ok := strings.Cut(token, ".")
	if !ok {
		return "", "", errors.New("storage: malformed media token")
	}
	if !hmac.Equal([]byte(sig), []byte(l.sign(body))) {
		return "", "", errors.New("storage: bad media signature")
	}

	raw, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return "", "", errors.New("storage: malformed media token")
	}
	var claim mediaClaim
	if err := json.Unmarshal(raw, &claim); err != nil {
		return "", "", errors.New("storage: malformed media token")
	}
	if time.Now().Unix() > claim.Expires {
		return "", "", errors.New("storage: media link has expired")
	}
	if err := checkKey(claim.Key); err != nil {
		return "", "", err
	}
	return claim.Key, claim.Download, nil
}

func (l *Local) sign(body string) string {
	mac := hmac.New(sha256.New, l.secret)
	mac.Write([]byte(body))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// DeriveSecret produces a stable signing key from material the deployment
// already keeps secret.
//
// A random key per process would be simpler, but every link minted before a
// restart would break — including the ones a browser is still rendering. The
// database URL contains the database password, is already required, and is
// already secret; hashing it with a domain separator gives a key that survives
// restarts without adding a setting nobody would remember to fill in.
func DeriveSecret(explicit, databaseURL string) []byte {
	if explicit != "" {
		sum := sha256.Sum256([]byte("salesan-media-v1|" + explicit))
		return sum[:]
	}
	sum := sha256.Sum256([]byte("salesan-media-v1|" + databaseURL))
	return sum[:]
}
