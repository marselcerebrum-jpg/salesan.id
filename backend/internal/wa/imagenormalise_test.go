package wa

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"sync"
	"testing"

	"github.com/salesan/omnichannel/backend/internal/media"
)

func writeTemp(t *testing.T, name string, data []byte) *os.File {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(data); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	// Windows refuses to delete a file that is still open, and t.TempDir()
	// cleans up before the deferred cleanups in the tests themselves.
	t.Cleanup(func() { _ = f.Close() })
	return f
}

func squareImage() image.Image {
	img := image.NewRGBA(image.Rect(0, 0, 32, 32))
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 8), G: uint8(y * 8), B: 120, A: 255})
		}
	}
	return img
}

// A photo WhatsApp cannot draw is worse than no photo: the message arrives,
// the bubble is empty on the phone, and the browser shows it perfectly, so
// nobody can tell what went wrong. Anything that is not already a JPEG is
// converted before it is uploaded.
func TestImageThatIsNotJPEGIsConvertedBeforeSending(t *testing.T) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, squareImage()); err != nil {
		t.Fatal(err)
	}
	src := writeTemp(t, "shot-*.png", buf.Bytes())

	out, info, cleanup, err := normaliseImage(src, media.File{
		Kind: media.KindImage, MIME: "image/png", Name: "shot.png", Ext: ".png",
		Size: int64(buf.Len()),
	})
	if err != nil {
		t.Fatalf("normalise: %v", err)
	}
	defer cleanup()

	if info.MIME != "image/jpeg" {
		t.Errorf("mime = %q, want image/jpeg", info.MIME)
	}
	if info.Ext != ".jpg" || info.Name != "shot.jpg" {
		t.Errorf("name/ext = %q/%q, want shot.jpg/.jpg", info.Name, info.Ext)
	}
	if info.Size <= 0 {
		t.Errorf("size = %d, want the length of the converted file", info.Size)
	}

	// The file the caller goes on to upload has to be readable from the start
	// and actually be a JPEG, because that is the claim the message makes.
	if _, err := out.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := jpeg.Decode(out); err != nil {
		t.Errorf("converted file does not decode as JPEG: %v", err)
	}
}

// A JPEG is already what WhatsApp wants, so it is handed straight back. Copying
// it would cost a full re-encode on every device of every campaign.
func TestJPEGIsLeftAlone(t *testing.T) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, squareImage(), nil); err != nil {
		t.Fatal(err)
	}
	src := writeTemp(t, "shot-*.jpg", buf.Bytes())
	in := media.File{Kind: media.KindImage, MIME: "image/jpeg", Name: "shot.jpg", Ext: ".jpg"}

	out, info, cleanup, err := normaliseImage(src, in)
	if err != nil {
		t.Fatalf("normalise: %v", err)
	}
	defer cleanup()

	if out != src {
		t.Error("a JPEG should come back as the same file, not a copy")
	}
	if info != in {
		t.Errorf("info = %+v, want it unchanged", info)
	}
}

// Video, audio and documents go through untouched: only photos are the problem.
func TestNonImagesArePassedThrough(t *testing.T) {
	src := writeTemp(t, "clip-*.mp4", []byte("not really a video"))
	in := media.File{Kind: media.KindVideo, MIME: "video/mp4", Name: "clip.mp4", Ext: ".mp4"}

	out, info, cleanup, err := normaliseImage(src, in)
	if err != nil {
		t.Fatalf("normalise: %v", err)
	}
	defer cleanup()

	if out != src || info != in {
		t.Error("a video must be left exactly as it came")
	}
}

// An image Go cannot decode is still sent rather than refused, because
// unreadable here does not mean unreadable on the recipient's phone.
func TestUndecodableImageIsStillSent(t *testing.T) {
	src := writeTemp(t, "broken-*.png", []byte("this is not a png"))
	in := media.File{Kind: media.KindImage, MIME: "image/png", Name: "broken.png", Ext: ".png"}

	out, info, cleanup, err := normaliseImage(src, in)
	if err != nil {
		t.Fatalf("normalise: %v", err)
	}
	defer cleanup()

	if out != src || info != in {
		t.Error("an undecodable image must be passed through, not dropped")
	}
}

// A Story publishes its numbers in parallel, and every one of them prepares the
// same fetched file. They must not share a file handle: one *os.File carries one
// offset, so a Seek(0) in one goroutine rewinds the read another is in the
// middle of, and both upload a mixture of two reads. It reached a phone as
// coloured noise with the caption intact underneath.
//
// This pins the safe arrangement: separate handles on one file, prepared at the
// same time, produce byte-identical output.
func TestConcurrentPreparationFromSeparateHandlesIsIdentical(t *testing.T) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, squareImage()); err != nil {
		t.Fatal(err)
	}
	shared := writeTemp(t, "poster-*.png", buf.Bytes())
	in := media.File{
		Kind: media.KindImage, MIME: "image/png", Name: "poster.png", Ext: ".png",
		Size: int64(buf.Len()),
	}

	const workers = 8
	results := make([][]byte, workers)
	errs := make([]error, workers)

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()

			// What PrepareCampaignMedia does: its own handle, nobody else's offset.
			own, err := os.Open(shared.Name())
			if err != nil {
				errs[i] = err
				return
			}
			defer own.Close()

			out, _, cleanup, err := normaliseImage(own, in)
			if err != nil {
				errs[i] = err
				return
			}
			defer cleanup()

			data, err := io.ReadAll(out)
			if err != nil {
				errs[i] = err
				return
			}
			results[i] = data
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("worker %d: %v", i, err)
		}
	}
	for i, got := range results {
		if len(got) == 0 {
			t.Fatalf("worker %d produced nothing", i)
		}
		if !bytes.Equal(got, results[0]) {
			t.Errorf("worker %d produced different bytes; the readers are interfering", i)
		}
		if _, err := jpeg.Decode(bytes.NewReader(got)); err != nil {
			t.Errorf("worker %d produced something that is not a JPEG: %v", i, err)
		}
	}
}
