package media

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// Minimal but genuine file signatures — http.DetectContentType reads these.
var (
	pngHead  = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")
	jpegHead = []byte("\xff\xd8\xff\xe0\x00\x10JFIF\x00")
	pdfHead  = []byte("%PDF-1.7\n1 0 obj\n")
	zipHead  = []byte("PK\x03\x04\x14\x00\x00\x00\x08\x00")
	exeHead  = []byte("MZ\x90\x00\x03\x00\x00\x00\x04\x00\x00\x00\xff\xff")
	textHead = []byte("nama,email\nari,ari@example.com\n")
)

func TestSanitizeFileNameStripsPathComponents(t *testing.T) {
	cases := map[string]string{
		"../../etc/passwd":               "passwd",
		`..\..\windows\system32\cmd.txt`: "cmd.txt",
		"/absolute/path/report.pdf":      "report.pdf",
		`C:\Users\ari\laporan.xlsx`:      "laporan.xlsx",
		"....//....//secret.txt":         "secret.txt",
		"  spasi.png  ":                  "spasi.png",
		".hidden.txt":                    "hidden.txt",
	}
	for in, want := range cases {
		if got := SanitizeFileName(in); got != want {
			t.Errorf("SanitizeFileName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSanitizeFileNameRejectsDotOnlyNames(t *testing.T) {
	for _, in := range []string{"", "   ", ".", "..", "../", "./.", `..\`} {
		if got := SanitizeFileName(in); got != "" {
			t.Errorf("SanitizeFileName(%q) = %q, want empty", in, got)
		}
	}
}

// A right-to-left override makes "invoice<RLO>fdp.exe" render as
// "invoice exe.pdf" — the classic way to disguise an executable.
func TestSanitizeFileNameStripsBidiOverride(t *testing.T) {
	raw := "invoice" + string(rune(0x202E)) + "fdp.exe"
	got := SanitizeFileName(raw)
	if strings.ContainsRune(got, 0x202E) {
		t.Fatalf("bidi override survived sanitisation: %q", got)
	}
	if got != "invoicefdp.exe" {
		t.Fatalf("got %q, want %q", got, "invoicefdp.exe")
	}
}

func TestSanitizeFileNameStripsControlCharacters(t *testing.T) {
	got := SanitizeFileName("re\x00port\n.pdf")
	if got != "report.pdf" {
		t.Fatalf("got %q, want %q", got, "report.pdf")
	}
}

func TestClassifyAcceptsImages(t *testing.T) {
	f, err := Classify("foto.png", "image/png", pngHead, 2048, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f.Kind != KindImage {
		t.Errorf("kind = %q, want image", f.Kind)
	}
	if f.MIME != "image/png" {
		t.Errorf("mime = %q, want image/png", f.MIME)
	}
}

// The sniffed bytes decide, not the declared type: a JPEG announced as a PDF is
// still a JPEG.
func TestClassifyPrefersSniffedTypeOverDeclared(t *testing.T) {
	f, err := Classify("laporan.pdf", "application/pdf", jpegHead, 4096, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f.MIME != "image/jpeg" || f.Kind != KindImage {
		t.Fatalf("got kind=%q mime=%q, want image/image/jpeg", f.Kind, f.MIME)
	}
}

// An .exe renamed to .pdf sniffs as octet-stream and must not be accepted just
// because its name looks harmless.
func TestClassifyRejectsDisguisedExecutable(t *testing.T) {
	_, err := Classify("laporan.pdf", "application/pdf", exeHead, 90_000, false)
	if !errors.Is(err, ErrTypeBlocked) {
		t.Fatalf("err = %v, want ErrTypeBlocked", err)
	}
}

func TestClassifyRejectsProgramSignatures(t *testing.T) {
	programs := map[string][]byte{
		"elf":     []byte("\x7fELF\x02\x01\x01\x00"),
		"macho":   []byte("\xcf\xfa\xed\xfe\x07\x00\x00\x01"),
		"class":   []byte("\xca\xfe\xba\xbe\x00\x00\x004"),
		"shebang": []byte("#!/bin/sh\nrm -rf /\n"),
		"dex":     []byte("dex\n035\x00"),
	}
	for label, head := range programs {
		if _, err := Classify("dokumen.pdf", "application/pdf", head, 4096, false); !errors.Is(err, ErrTypeBlocked) {
			t.Errorf("%s disguised as pdf: err = %v, want ErrTypeBlocked", label, err)
		}
	}
}

// For an opaque body the declared type is believed only when the extension
// agrees. When they disagree the extension decides, because it is the thing
// that determines how the file will actually be opened.
func TestClassifyIgnoresMismatchedDeclaredType(t *testing.T) {
	opaque := bytes.Repeat([]byte{0x01, 0x02, 0x03, 0x04}, 64)

	f, err := Classify("arsip.pdf", "application/msword", opaque, 4096, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f.MIME != "application/pdf" {
		t.Fatalf("mime = %q, want application/pdf from the extension", f.MIME)
	}

	f, err = Classify("arsip.doc", "application/msword", opaque, 4096, false)
	if err != nil {
		t.Fatalf("matching name and type: unexpected error %v", err)
	}
	if f.MIME != "application/msword" {
		t.Fatalf("mime = %q, want application/msword", f.MIME)
	}

	// An extension nobody recognises leaves nothing to fall back on.
	if _, err := Classify("arsip.unknown", "application/msword", opaque, 4096, false); !errors.Is(err, ErrTypeBlocked) {
		t.Fatalf("unknown extension: err = %v, want ErrTypeBlocked", err)
	}
}

func TestClassifyRejectsBlockedExtensions(t *testing.T) {
	for _, name := range []string{"setup.exe", "run.bat", "script.ps1", "app.apk", "page.html", "icon.svg"} {
		if _, err := Classify(name, "application/pdf", pdfHead, 1024, false); !errors.Is(err, ErrTypeBlocked) {
			t.Errorf("Classify(%q) err = %v, want ErrTypeBlocked", name, err)
		}
	}
}

// Office files are ZIP containers; sniffing alone cannot tell them apart, so a
// declared OOXML type is allowed to refine "application/zip" — but only when it
// is on the allowlist.
func TestClassifyRefinesZipToOfficeType(t *testing.T) {
	const docx = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	f, err := Classify("proposal.docx", docx, zipHead, 20_000, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f.MIME != docx || f.Kind != KindDocument {
		t.Fatalf("got kind=%q mime=%q", f.Kind, f.MIME)
	}

	// An unknown declared type falls back to plain zip, which is allowed.
	f, err = Classify("bundle.zip", "application/x-made-up", zipHead, 20_000, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f.MIME != "application/zip" {
		t.Fatalf("mime = %q, want application/zip", f.MIME)
	}
}

func TestClassifyAllowsTextRefinement(t *testing.T) {
	f, err := Classify("kontak.csv", "text/csv", textHead, int64(len(textHead)), false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f.MIME != "text/csv" || f.Kind != KindDocument {
		t.Fatalf("got kind=%q mime=%q", f.Kind, f.MIME)
	}

	// A declared image type must not survive text bytes.
	f, err = Classify("notes.txt", "image/png", textHead, int64(len(textHead)), false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f.MIME != "text/plain" {
		t.Fatalf("mime = %q, want text/plain", f.MIME)
	}
}

func TestClassifyEnforcesPerKindSizeLimits(t *testing.T) {
	if _, err := Classify("besar.png", "image/png", pngHead, MaxImageBytes+1, false); !errors.Is(err, ErrTooLarge) {
		t.Errorf("oversized image err = %v, want ErrTooLarge", err)
	}
	if _, err := Classify("besar.pdf", "application/pdf", pdfHead, MaxDocumentBytes+1, false); !errors.Is(err, ErrTooLarge) {
		t.Errorf("oversized document err = %v, want ErrTooLarge", err)
	}
	// A file that is too big as an image is still fine as a document.
	if _, err := Classify("besar.png", "image/png", pngHead, MaxImageBytes+1, true); err != nil {
		t.Errorf("image sent as document: unexpected error %v", err)
	}
}

func TestClassifyRejectsEmptyFile(t *testing.T) {
	if _, err := Classify("kosong.png", "image/png", nil, 0, false); !errors.Is(err, ErrEmptyFile) {
		t.Fatalf("err = %v, want ErrEmptyFile", err)
	}
}

func TestClassifySendAsDocumentKeepsBytesAndName(t *testing.T) {
	f, err := Classify("foto.jpg", "image/jpeg", jpegHead, 5000, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f.Kind != KindDocument {
		t.Errorf("kind = %q, want document", f.Kind)
	}
	// The media type is preserved so the receiver still knows it is a JPEG.
	if f.MIME != "image/jpeg" {
		t.Errorf("mime = %q, want image/jpeg", f.MIME)
	}
	if f.Name != "foto.jpg" {
		t.Errorf("name = %q, want foto.jpg", f.Name)
	}
}

func TestClassifyAddsMissingExtension(t *testing.T) {
	f, err := Classify("tanpaekstensi", "image/png", pngHead, 1024, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f.Ext != ".png" || !strings.HasSuffix(f.Name, ".png") {
		t.Fatalf("got name=%q ext=%q, want a .png suffix", f.Name, f.Ext)
	}
}

func TestTrimCaptionBoundsLength(t *testing.T) {
	long := strings.Repeat("a", MaxCaptionRunes+50)
	if got := TrimCaption(long); len([]rune(got)) != MaxCaptionRunes {
		t.Fatalf("length = %d, want %d", len([]rune(got)), MaxCaptionRunes)
	}
	if got := TrimCaption("  halo  "); got != "halo" {
		t.Fatalf("got %q, want %q", got, "halo")
	}
}

func TestKindForMIMENeverRejects(t *testing.T) {
	cases := map[string]Kind{
		"image/heic":              KindImage, // not on the upload allowlist
		"video/x-matroska":        KindVideo,
		"audio/ogg; codecs=opus":  KindAudio,
		"application/vnd.unknown": KindDocument,
		"":                        KindDocument,
	}
	for mime, want := range cases {
		if got := KindForMIME(mime); got != want {
			t.Errorf("KindForMIME(%q) = %q, want %q", mime, got, want)
		}
	}
}

func TestExtensionRoundTrip(t *testing.T) {
	if got := ExtensionForMIME("image/jpeg"); got != ".jpg" {
		t.Errorf("ExtensionForMIME(image/jpeg) = %q", got)
	}
	if got := MIMEForExtension(".PDF"); got != "application/pdf" {
		t.Errorf("MIMEForExtension(.PDF) = %q", got)
	}
}

// A long name must stay under the limit while keeping its extension, so the
// download still opens in the right application.
func TestSanitizeFileNameTruncatesButKeepsExtension(t *testing.T) {
	long := strings.Repeat("x", 400) + ".pdf"
	got := SanitizeFileName(long)
	if len(got) > 180 {
		t.Fatalf("length = %d, want <= 180", len(got))
	}
	if !strings.HasSuffix(got, ".pdf") {
		t.Fatalf("got %q, want a .pdf suffix", got)
	}
}

func TestDetectHeadShorterThan512(t *testing.T) {
	// Real uploads are sniffed from whatever prefix is available; a tiny file
	// gives fewer than 512 bytes and must still classify.
	head := bytes.Clone(pngHead)
	if _, err := Classify("kecil.png", "", head, int64(len(head)), false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
