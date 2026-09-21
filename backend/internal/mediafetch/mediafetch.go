// Package mediafetch downloads campaign media from a URL, safely.
//
// Broadcast and Story take media as a link rather than an upload, which means
// this server is about to make an HTTP request to an address a user chose. That
// is a server-side request forgery primitive unless it is fenced in, because the
// backend sits inside a network the browser cannot reach: the database, the
// metadata endpoint of whatever cloud it runs on, other services on localhost.
// A URL like http://169.254.169.254/latest/meta-data/iam/security-credentials/
// would otherwise hand the caller this machine's cloud credentials.
//
// So the fence is built at the level where it cannot be bypassed — the socket.
// Checking the hostname before dialling is not enough: a name can resolve to a
// public address on the first lookup and a private one on the second (DNS
// rebinding), and a redirect can move the request somewhere else entirely. The
// Control hook below runs after the name has been resolved and immediately
// before connect, on every hop, so what is inspected is the address the kernel
// is about to reach.
//
// Everything the file needs to be judged safe is checked afterwards too:
// declared type, sniffed type, extension, and size. The bytes land in a
// temporary file that the caller removes.
package mediafetch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"syscall"
	"time"

	"github.com/salesan/omnichannel/backend/internal/media"
)

// Errors a caller may want to distinguish.
var (
	ErrScheme    = errors.New("mediafetch: hanya URL https yang diterima")
	ErrBlocked   = errors.New("mediafetch: alamat tujuan tidak diizinkan")
	ErrTooLarge  = errors.New("mediafetch: berkas melebihi batas ukuran")
	ErrRedirects = errors.New("mediafetch: terlalu banyak pengalihan")
)

// Limits bound one download.
type Limits struct {
	// MaxBytes caps the body. Zero uses the per-kind WhatsApp limit.
	MaxBytes int64
	// Timeout bounds the whole transfer, not just the handshake.
	Timeout time.Duration
	// MaxRedirects is how many hops are followed. Each one is re-validated.
	MaxRedirects int
	// TempDir is where the file lands. Empty uses the system temp directory.
	TempDir string
	// AllowPrivateHosts disables the address fence. Only ever set from a test.
	AllowPrivateHosts bool
}

func (l Limits) withDefaults() Limits {
	if l.Timeout <= 0 {
		l.Timeout = 2 * time.Minute
	}
	if l.MaxRedirects <= 0 {
		l.MaxRedirects = 3
	}
	if l.MaxBytes <= 0 {
		// The largest thing WhatsApp accepts at all. Classify narrows it per
		// kind once the type is known.
		l.MaxBytes = media.MaxBytesFor(media.KindVideo)
	}
	return l
}

// Result is a downloaded file, still on disk.
//
// The caller owns File and must Close and Remove it — Cleanup does both.
type Result struct {
	File   *os.File
	Info   media.File
	SHA256 string
	// FinalURL is where the download actually ended up, after redirects. Worth
	// recording: it is not always what the user typed.
	FinalURL string
}

// Cleanup closes and deletes the temporary file. Safe to call twice.
func (r *Result) Cleanup() {
	if r == nil || r.File == nil {
		return
	}
	name := r.File.Name()
	_ = r.File.Close()
	_ = os.Remove(name)
	r.File = nil
}

// Fetch downloads one URL and validates it as sendable media.
//
// asDocument sends an image or video through as a file rather than letting
// WhatsApp recompress it, mirroring the same option on the chat upload path.
func Fetch(ctx context.Context, raw string, limits Limits, asDocument bool) (*Result, error) {
	limits = limits.withDefaults()

	target, err := parseHTTPS(raw)
	if err != nil {
		return nil, err
	}

	client := newClient(limits)
	ctx, cancel := context.WithTimeout(ctx, limits.Timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, err
	}
	// No credentials of ours travel with this request: it goes to an address
	// somebody else chose.
	req.Header.Set("Accept", "*/*")
	req.Header.Set("User-Agent", "salesan-media-fetch/1")

	resp, err := client.Do(req)
	if err != nil {
		// The dial-time refusal arrives wrapped; surface it as what it is.
		if errors.Is(err, ErrBlocked) {
			return nil, ErrBlocked
		}
		if errors.Is(err, ErrRedirects) {
			return nil, ErrRedirects
		}
		return nil, fmt.Errorf("mediafetch: %w", err)
	}
	defer func() { _, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16)); _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("mediafetch: server menjawab %d", resp.StatusCode)
	}
	// An honest Content-Length that already exceeds the cap saves downloading it.
	if resp.ContentLength > 0 && resp.ContentLength > limits.MaxBytes {
		return nil, ErrTooLarge
	}

	tmp, err := os.CreateTemp(limits.TempDir, "salesan-fetch-*.bin")
	if err != nil {
		return nil, fmt.Errorf("mediafetch: berkas sementara: %w", err)
	}
	cleanup := func() {
		name := tmp.Name()
		_ = tmp.Close()
		_ = os.Remove(name)
	}

	// One byte past the cap, so a body that is exactly at the limit passes and
	// one byte over is caught rather than silently truncated.
	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(tmp, hash), io.LimitReader(resp.Body, limits.MaxBytes+1))
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("mediafetch: unduh: %w", err)
	}
	if written > limits.MaxBytes {
		cleanup()
		return nil, ErrTooLarge
	}
	if written == 0 {
		cleanup()
		return nil, errors.New("mediafetch: berkas kosong")
	}

	head := make([]byte, 512)
	n, _ := tmp.ReadAt(head, 0)
	head = head[:n]

	// The declared type and the filename are both attacker-controlled, so
	// Classify is given all three signals and decides for itself — it sniffs the
	// bytes and refuses an executable however it is labelled.
	info, err := media.Classify(
		fileNameFor(resp, target),
		resp.Header.Get("Content-Type"),
		head,
		written,
		asDocument,
	)
	if err != nil {
		cleanup()
		return nil, err
	}
	// Classify's own per-kind cap is the one WhatsApp enforces; re-check it now
	// that the kind is known, because the generic cap above is looser.
	if max := media.MaxBytesFor(info.Kind); written > max {
		cleanup()
		return nil, ErrTooLarge
	}

	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		cleanup()
		return nil, err
	}

	final := target.String()
	if resp.Request != nil && resp.Request.URL != nil {
		final = resp.Request.URL.String()
	}
	return &Result{
		File:     tmp,
		Info:     info,
		SHA256:   hex.EncodeToString(hash.Sum(nil)),
		FinalURL: final,
	}, nil
}

// parseHTTPS accepts only an absolute https URL with a host.
//
// Plain http is refused outright rather than upgraded: the link is going to be
// stored and shown back to whoever entered it, and quietly changing what they
// typed is worse than telling them it is not allowed.
func parseHTTPS(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("mediafetch: URL tidak valid: %w", err)
	}
	if !strings.EqualFold(u.Scheme, "https") {
		return nil, ErrScheme
	}
	if u.Host == "" {
		return nil, ErrScheme
	}
	// Credentials in the URL would be forwarded to whatever the redirect chain
	// ends at, which is not something the person pasting a link intends.
	if u.User != nil {
		return nil, fmt.Errorf("mediafetch: URL tidak boleh memuat kredensial")
	}
	return u, nil
}

// newClient builds a client that refuses to connect to anything internal.
func newClient(limits Limits) *http.Client {
	dialer := &net.Dialer{Timeout: 15 * time.Second, KeepAlive: 15 * time.Second}
	if !limits.AllowPrivateHosts {
		// Control runs after DNS resolution and before connect, for every
		// address the dialler tries. That placement is the whole point: it sees
		// the IP the kernel is about to reach, so a name that resolves
		// differently on a second lookup cannot slip past a check made earlier.
		dialer.Control = func(network, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return ErrBlocked
			}
			ip := net.ParseIP(host)
			if ip == nil || !Allowed(ip) {
				return ErrBlocked
			}
			return nil
		}
	}

	return &http.Client{
		Transport: &http.Transport{
			DialContext:           dialer.DialContext,
			TLSHandshakeTimeout:   15 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
			DisableKeepAlives:     true,
			MaxIdleConns:          1,
			// No proxy: a proxy would terminate the connection somewhere the
			// address check above cannot see.
			Proxy: nil,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= limits.MaxRedirects {
				return ErrRedirects
			}
			// Every hop must still be https. A redirect to http, or to a
			// different scheme entirely, ends here.
			if _, err := parseHTTPS(req.URL.String()); err != nil {
				return err
			}
			return nil
		},
	}
}

// Allowed reports whether an address may be reached.
//
// Everything that is not ordinary public internet is refused: loopback, link
// local (which is where cloud metadata lives, at 169.254.169.254), the RFC 1918
// ranges, carrier-grade NAT, multicast, unspecified, and IPv6's unique-local and
// mapped-IPv4 forms. Exported so the rule can be tested directly rather than
// only through a live socket.
func Allowed(ip net.IP) bool {
	if ip == nil {
		return false
	}
	// An IPv4 address wrapped in IPv6 notation is still that IPv4 address, and
	// ::ffff:127.0.0.1 is a well-worn way past a naive check.
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}

	switch {
	case ip.IsLoopback(),
		ip.IsUnspecified(),
		ip.IsLinkLocalUnicast(),
		ip.IsLinkLocalMulticast(),
		ip.IsInterfaceLocalMulticast(),
		ip.IsMulticast(),
		ip.IsPrivate():
		return false
	}

	if v4 := ip.To4(); v4 != nil {
		// 100.64.0.0/10 — carrier-grade NAT, and the address space several
		// container runtimes hand out.
		if v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127 {
			return false
		}
		// 192.0.0.0/24 and the documentation/benchmark ranges: never a real
		// media host, occasionally a way to reach something local.
		if v4[0] == 192 && v4[1] == 0 && v4[2] == 0 {
			return false
		}
		if v4[0] == 198 && (v4[1] == 18 || v4[1] == 19) {
			return false
		}
		// 240.0.0.0/4, reserved.
		if v4[0] >= 240 {
			return false
		}
		return true
	}

	// IPv6 unique-local, fc00::/7.
	if len(ip) == net.IPv6len && ip[0]&0xfe == 0xfc {
		return false
	}
	return true
}

// fileNameFor works out a filename for the download.
//
// Content-Disposition first, then the URL path, then a generic fallback. All
// three are attacker-controlled, so the result goes through SanitizeFileName,
// which is what stops "../../etc/passwd" from becoming a path.
func fileNameFor(resp *http.Response, target *url.URL) string {
	if cd := resp.Header.Get("Content-Disposition"); cd != "" {
		if _, params, err := parseDisposition(cd); err == nil {
			if name := params["filename"]; name != "" {
				return media.SanitizeFileName(name)
			}
		}
	}
	if base := path.Base(target.Path); base != "" && base != "/" && base != "." {
		if name := media.SanitizeFileName(base); name != "" {
			return name
		}
	}
	return "media"
}

// parseDisposition reads a Content-Disposition header. A malformed one is not
// an error worth reporting — the URL path is the next candidate.
func parseDisposition(v string) (string, map[string]string, error) {
	return mime.ParseMediaType(v)
}
