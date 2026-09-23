package wa

import (
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"os"
	"path/filepath"
	"strings"

	_ "image/gif"  // registers the GIF decoder
	_ "image/jpeg" // registers the JPEG decoder
	_ "image/png"  // registers the PNG decoder

	_ "golang.org/x/image/webp" // registers the WebP decoder

	"github.com/salesan/omnichannel/backend/internal/media"
)

// A photo sent to WhatsApp has to be a JPEG.
//
// WhatsApp's own clients transcode before they send, so every photo that
// travels between two phones is a JPEG regardless of what the camera roll held.
// A message built from anything else is accepted by the server and then cannot
// be drawn: the browser here shows it, because browsers decode WebP and PNG
// natively, and the phone shows an empty bubble or nothing at all.
//
// That is what happened. A broadcast carrying a 160 KB image/webp from the
// operator's own CDN reached its groups, salesan displayed it, and the phone
// did not. The text-only copy of the same broadcast, sent a minute later, was
// delivered everywhere. It was never a delivery problem; it was a picture no
// phone could open.
//
// Two things went wrong together, and both are fixed here. The mimetype said
// WebP, and Go could not decode WebP at all, so the message also went out with
// no thumbnail and no dimensions — the three things a client needs before it
// will even lay out a photo bubble.

// jpegQuality is what the transcode writes at.
//
// Eighty-five is the point where a re-encode of a promotional graphic stops
// being distinguishable from the original at phone size, while staying well
// inside what WhatsApp will accept for a photo.
const jpegQuality = 85

// normaliseImage returns a JPEG copy of an image that is not already one.
//
// The returned file is a new temporary file, and the caller owns it: cleanup is
// the returned func, which is never nil. When nothing needs doing the original
// file and info come straight back and the cleanup is a no-op, so the caller
// can treat both cases identically.
//
// Anything that is not an image, or that cannot be decoded, is passed through
// untouched. A picture that fails to convert is still better sent as it arrived
// than not sent at all, and the operator sees the same message either way.
func normaliseImage(src *os.File, info media.File) (*os.File, media.File, func(), error) {
	noop := func() {}
	if src == nil || info.Kind != media.KindImage || info.MIME == "image/jpeg" {
		return src, info, noop, nil
	}

	if _, err := src.Seek(0, io.SeekStart); err != nil {
		return src, info, noop, nil
	}
	decoded, _, err := image.Decode(src)
	if _, seekErr := src.Seek(0, io.SeekStart); seekErr != nil {
		return src, info, noop, fmt.Errorf("berkas gambar tidak bisa dibaca ulang: %w", seekErr)
	}
	if err != nil {
		// Unreadable here does not mean unreadable everywhere, so it is sent
		// as it came rather than refused.
		return src, info, noop, nil
	}

	out, err := os.CreateTemp("", "salesan-image-*.jpg")
	if err != nil {
		return src, info, noop, fmt.Errorf("berkas sementara: %w", err)
	}
	cleanup := func() {
		name := out.Name()
		_ = out.Close()
		_ = os.Remove(name)
	}

	if err := jpeg.Encode(out, decoded, &jpeg.Options{Quality: jpegQuality}); err != nil {
		cleanup()
		return src, info, noop, nil
	}
	if _, err := out.Seek(0, io.SeekStart); err != nil {
		cleanup()
		return src, info, noop, nil
	}
	size, err := out.Seek(0, io.SeekEnd)
	if err != nil {
		cleanup()
		return src, info, noop, nil
	}
	if _, err := out.Seek(0, io.SeekStart); err != nil {
		cleanup()
		return src, info, noop, nil
	}

	converted := info
	converted.MIME = "image/jpeg"
	converted.Ext = ".jpg"
	converted.Size = size
	converted.Name = strings.TrimSuffix(info.Name, filepath.Ext(info.Name)) + ".jpg"
	return out, converted, cleanup, nil
}
