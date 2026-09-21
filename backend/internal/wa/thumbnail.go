package wa

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"os"

	_ "image/gif"  // registers the GIF decoder
	_ "image/jpeg" // registers the JPEG decoder
	_ "image/png"  // registers the PNG decoder
)

// thumbMaxEdge bounds the longest side of a generated preview. WhatsApp embeds
// the thumbnail inside the message stanza itself, so it has to stay small — a
// few kilobytes, not a few hundred.
const thumbMaxEdge = 320

// thumbMaxPixels refuses absurd images before allocating anything.
//
// A decompression bomb is a tiny PNG that expands to hundreds of megapixels; it
// costs almost nothing to send and gigabytes to decode. Reading the header
// first and checking the dimensions is what stops it.
const thumbMaxPixels = 50 << 20 // 50 megapixels

// imagePreview builds a small JPEG preview of an image file, returning the
// thumbnail bytes and the source dimensions.
//
// Best effort throughout: anything that is not a decodable image — a video, a
// PDF, a HEIC photo Go cannot read — yields (nil, 0, 0) and the message is sent
// without a thumbnail. The file position is restored so the caller can go on to
// upload the same reader.
func imagePreview(f *os.File) (jpegBytes []byte, width, height int) {
	if f == nil {
		return nil, 0, 0
	}
	// Whatever happens below, leave the file where the caller expects it.
	defer func() { _, _ = f.Seek(0, io.SeekStart) }()

	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, 0, 0
	}
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return nil, 0, 0
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width*cfg.Height > thumbMaxPixels {
		return nil, 0, 0
	}

	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, cfg.Width, cfg.Height
	}
	src, _, err := image.Decode(f)
	if err != nil {
		return nil, cfg.Width, cfg.Height
	}

	small := downscale(src, thumbMaxEdge)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, small, &jpeg.Options{Quality: 72}); err != nil {
		return nil, cfg.Width, cfg.Height
	}
	return buf.Bytes(), cfg.Width, cfg.Height
}

// downscale shrinks an image so its longest edge is at most maxEdge, averaging
// each source block into one destination pixel.
//
// Box averaging rather than nearest-neighbour: sampling a single pixel out of
// every 8×8 block produces visible speckle on photographs, and the whole point
// of the thumbnail is to look like the photo.
func downscale(src image.Image, maxEdge int) image.Image {
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	if sw <= maxEdge && sh <= maxEdge {
		return src
	}

	dw, dh := sw, sh
	if sw >= sh {
		dw = maxEdge
		dh = sh * maxEdge / sw
	} else {
		dh = maxEdge
		dw = sw * maxEdge / sh
	}
	if dw < 1 {
		dw = 1
	}
	if dh < 1 {
		dh = 1
	}

	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < dh; y++ {
		y0 := b.Min.Y + y*sh/dh
		y1 := b.Min.Y + (y+1)*sh/dh
		if y1 <= y0 {
			y1 = y0 + 1
		}
		for x := 0; x < dw; x++ {
			x0 := b.Min.X + x*sw/dw
			x1 := b.Min.X + (x+1)*sw/dw
			if x1 <= x0 {
				x1 = x0 + 1
			}

			var r, g, bl, a uint64
			var n uint64
			for sy := y0; sy < y1; sy++ {
				for sx := x0; sx < x1; sx++ {
					cr, cg, cb, ca := src.At(sx, sy).RGBA()
					r += uint64(cr)
					g += uint64(cg)
					bl += uint64(cb)
					a += uint64(ca)
					n++
				}
			}
			if n == 0 {
				continue
			}
			dst.Set(x, y, color.RGBA64{
				R: uint16(r / n),
				G: uint16(g / n),
				B: uint16(bl / n),
				A: uint16(a / n),
			})
		}
	}
	return dst
}
