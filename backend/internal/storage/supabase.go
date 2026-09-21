// Package storage keeps media files somewhere other than the database.
//
// Two backends implement the same contract:
//
//   - Supabase Storage, in a private bucket, when a service-role key is
//     configured. Preferred: the files live outside this machine and survive it.
//   - The local filesystem otherwise, served back through this API under a
//     signed, expiring URL.
//
// Both share the security model, and it is the model that matters: no file is
// ever public, no permanent URL is ever handed out, and every link the browser
// receives carries an expiry. Swapping the backend changes where bytes rest,
// not who can read them.
package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client is a minimal Supabase Storage client.
type Client struct {
	baseURL string // ".../storage/v1"
	key     string
	bucket  string
	http    *http.Client
	created bool
}

// New builds a client. supabaseURL is the project URL, e.g.
// https://abcd.supabase.co — the storage path is appended here.
func New(supabaseURL, serviceRoleKey, bucket string) *Client {
	return &Client{
		baseURL: strings.TrimRight(supabaseURL, "/") + "/storage/v1",
		key:     serviceRoleKey,
		bucket:  bucket,
		// Generous: a 64 MB video upload over a domestic connection is slow, and
		// a timeout mid-upload leaves a half-written object.
		http: &http.Client{Timeout: 5 * time.Minute},
	}
}

// Bucket returns the configured bucket name.
func (c *Client) Bucket() string { return c.bucket }

// Name identifies this backend in logs.
func (c *Client) Name() string { return "supabase:" + c.bucket }

// EnsureReady creates the private bucket if it is missing.
func (c *Client) EnsureReady(ctx context.Context) error {
	created, err := c.EnsureBucket(ctx)
	if err != nil {
		return err
	}
	if created {
		c.created = true
	}
	return nil
}

// Created reports whether EnsureReady had to make the bucket, so the caller can
// say so once rather than on every boot.
func (c *Client) Created() bool { return c.created }

func (c *Client) request(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("apikey", c.key)
	return req, nil
}

// escapePath percent-encodes each segment while keeping the separators, so a
// key like "wa/<uuid>/my report.pdf" survives intact.
func escapePath(key string) string {
	parts := strings.Split(key, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}

// EnsureBucket creates the bucket if it does not exist, always as private.
//
// Idempotent, and safe to call on every boot: an existing bucket returns 409,
// which is treated as success. It is never *made* private if it already exists
// as public — that would be a surprising change to someone's project — but the
// caller is told, so the mistake is visible rather than silent.
func (c *Client) EnsureBucket(ctx context.Context) (created bool, err error) {
	payload, _ := json.Marshal(map[string]any{
		"id":     c.bucket,
		"name":   c.bucket,
		"public": false,
	})
	req, err := c.request(ctx, http.MethodPost, "/bucket", bytes.NewReader(payload))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return false, fmt.Errorf("create bucket: %w", err)
	}
	defer drain(resp)

	switch {
	case resp.StatusCode == http.StatusConflict:
		return false, nil // already there
	case resp.StatusCode < 300:
		return true, nil
	}

	// A failure here is not conclusive. Supabase answers 400 "The resource
	// already exists" rather than 409 when the bucket is present, so treating
	// every non-2xx as fatal disables media on the second boot after the bucket
	// was created — the first boot makes it, and every boot after that refuses
	// to start storage at all.
	//
	// Rather than matching on the message text, ask whether the bucket is
	// there. That answers the question the caller actually has.
	failure := apiError("create bucket", resp)
	if exists, err := c.bucketExists(ctx); err == nil && exists {
		return false, nil
	}
	return false, failure
}

// bucketExists reports whether the configured bucket is already present.
func (c *Client) bucketExists(ctx context.Context) (bool, error) {
	req, err := c.request(ctx, http.MethodGet, "/bucket/"+url.PathEscape(c.bucket), nil)
	if err != nil {
		return false, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return false, err
	}
	defer drain(resp)
	return resp.StatusCode == http.StatusOK, nil
}

// Upload writes an object, replacing any object already at that key.
//
// Upsert is deliberate: the key is derived from the message the file belongs
// to, so a retry of the same attachment must overwrite rather than accumulate
// orphans. Two different files can never collide on one key.
//
// The body is streamed from r, so a 64 MB video is never held in memory in one
// piece. size must be exact — Supabase rejects a chunked upload.
func (c *Client) Upload(ctx context.Context, key, contentType string, r io.Reader, size int64) error {
	// Wrapped so the caller keeps its file.
	//
	// http.NewRequest uses a body that is already an io.ReadCloser as the
	// request body verbatim, and http.Client.Do closes the request body when it
	// is done, success or not. An *os.File is an io.ReadCloser, so handing one
	// straight to this call had the HTTP client close the caller's temp file
	// out from under it. The next read of that file failed with "file already
	// closed", which is exactly how outgoing media died: our own bucket took
	// the picture, and the upload to WhatsApp immediately afterwards could no
	// longer read the bytes it had just stored.
	//
	// NopCloser makes Close a no-op, so the body still streams and the file
	// stays open for whoever opened it. Reading is what this function was
	// given the reader for; closing it was never part of the bargain.
	req, err := c.request(ctx, http.MethodPost, "/object/"+c.bucket+"/"+escapePath(key), io.NopCloser(r))
	if err != nil {
		return err
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("x-upsert", "true")
	req.ContentLength = size

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("upload %s: %w", key, err)
	}
	defer drain(resp)

	if resp.StatusCode >= 300 {
		return apiError("upload "+key, resp)
	}
	return nil
}

// UploadBytes is the in-memory convenience form, used for files small enough
// that a temp file would cost more than it saves.
func (c *Client) UploadBytes(ctx context.Context, key, contentType string, data []byte) error {
	return c.Upload(ctx, key, contentType, bytes.NewReader(data), int64(len(data)))
}

type signResponse struct {
	SignedURL string `json:"signedURL"`
}

// SignedURL mints a time-limited URL for one object.
//
// The returned URL carries its own token and needs no Authorization header,
// which is what makes it usable directly in an <img> or <video> tag. It stops
// working when the TTL elapses.
func (c *Client) SignedURL(ctx context.Context, key string, ttl time.Duration, download string) (string, error) {
	if err := checkKey(key); err != nil {
		return "", err
	}
	seconds := int(ttl.Seconds())
	if seconds < 60 {
		seconds = 60
	}
	payload, _ := json.Marshal(map[string]any{"expiresIn": seconds})

	req, err := c.request(ctx, http.MethodPost,
		"/object/sign/"+c.bucket+"/"+escapePath(key), bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("sign %s: %w", key, err)
	}
	defer drain(resp)

	if resp.StatusCode == http.StatusNotFound {
		return "", ErrNotFound
	}
	if resp.StatusCode >= 300 {
		return "", apiError("sign "+key, resp)
	}

	var out signResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("decode sign response: %w", err)
	}
	if out.SignedURL == "" {
		return "", errors.New("storage: empty signed url")
	}
	// The API returns a path relative to the storage root, with or without a
	// leading slash depending on version.
	signed := c.baseURL + "/" + strings.TrimPrefix(out.SignedURL, "/")

	// `download` makes Supabase send Content-Disposition: attachment with this
	// name, which is what lets a cross-origin document save under its real
	// filename — the HTML download attribute is ignored across origins.
	if download != "" {
		sep := "&"
		if !strings.Contains(signed, "?") {
			sep = "?"
		}
		signed += sep + "download=" + url.QueryEscape(download)
	}
	return signed, nil
}

// Download fetches an object's bytes using the service role.
func (c *Client) Download(ctx context.Context, key string) ([]byte, string, error) {
	req, err := c.request(ctx, http.MethodGet, "/object/"+c.bucket+"/"+escapePath(key), nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("download %s: %w", key, err)
	}
	defer drain(resp)

	if resp.StatusCode == http.StatusNotFound {
		return nil, "", ErrNotFound
	}
	if resp.StatusCode >= 300 {
		return nil, "", apiError("download "+key, resp)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", err
	}
	return data, resp.Header.Get("Content-Type"), nil
}

// Remove deletes objects. A missing object is not an error — the desired end
// state is "gone", and it already is.
func (c *Client) Remove(ctx context.Context, keys ...string) error {
	if len(keys) == 0 {
		return nil
	}
	payload, _ := json.Marshal(map[string]any{"prefixes": keys})

	req, err := c.request(ctx, http.MethodDelete, "/object/"+c.bucket, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("remove objects: %w", err)
	}
	defer drain(resp)

	if resp.StatusCode == http.StatusNotFound {
		return nil
	}
	if resp.StatusCode >= 300 {
		return apiError("remove objects", resp)
	}
	return nil
}

// apiError turns a failed response into an error carrying Supabase's own
// message, truncated so a stray HTML error page cannot flood the log.
func apiError(op string, resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	msg := strings.TrimSpace(string(body))

	// Supabase reports errors as JSON; pull out the human part when it is.
	var parsed struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &parsed) == nil {
		if parsed.Message != "" {
			msg = parsed.Message
		} else if parsed.Error != "" {
			msg = parsed.Error
		}
	}
	return fmt.Errorf("storage: %s: %s: %s", op, resp.Status, msg)
}

// drain consumes and closes a response body so the connection can be reused.
func drain(resp *http.Response) {
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	_ = resp.Body.Close()
}
