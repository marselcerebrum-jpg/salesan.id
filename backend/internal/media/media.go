// Package media validates and classifies files before they are stored or sent
// to WhatsApp.
//
// Everything a browser uploads is treated as hostile input: the filename, the
// declared Content-Type and the extension are all attacker-controlled. The
// decision of what a file *is* therefore comes from sniffing its own leading
// bytes, and the declared type is only allowed to refine that answer, never to
// contradict it.
package media

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"path"
	"strings"
	"unicode"
)

// Kind mirrors the public.attachment_kind enum.
type Kind string

const (
	KindImage    Kind = "image"
	KindVideo    Kind = "video"
	KindAudio    Kind = "audio"
	KindDocument Kind = "document"
	KindSticker  Kind = "sticker"
)

// Per-kind ceilings, matching what WhatsApp itself accepts. Rejecting here
// costs one request; letting an oversized file through costs a full upload to
// WhatsApp's servers before it is refused.
const (
	MaxImageBytes    int64 = 16 << 20  // 16 MB
	MaxVideoBytes    int64 = 64 << 20  // 64 MB
	MaxAudioBytes    int64 = 16 << 20  // 16 MB
	MaxDocumentBytes int64 = 100 << 20 // 100 MB
)

// MaxCaptionRunes bounds a per-file caption, matching WhatsApp's own limit.
const MaxCaptionRunes = 1024

// MaxBatchFiles caps one send operation. The files go out sequentially, so a
// large batch is a long-running request; this keeps it bounded.
const MaxBatchFiles = 30

// Validation failures. All are reported to the caller as 400s.
var (
	ErrEmptyFile    = errors.New("media: file is empty")
	ErrTooLarge     = errors.New("media: file exceeds the size limit")
	ErrTypeBlocked  = errors.New("media: file type is not allowed")
	ErrNameRequired = errors.New("media: file name is required")
)

// MaxBytesFor returns the ceiling that applies to a kind.
func MaxBytesFor(k Kind) int64 {
	switch k {
	case KindImage, KindSticker:
		return MaxImageBytes
	case KindVideo:
		return MaxVideoBytes
	case KindAudio:
		return MaxAudioBytes
	default:
		return MaxDocumentBytes
	}
}

// imageMIME/videoMIME/audioMIME are the media types WhatsApp renders inline.
// Anything outside them can still be sent — as a document.
var (
	imageMIME = map[string]string{
		"image/jpeg": ".jpg",
		"image/png":  ".png",
		"image/webp": ".webp",
		"image/gif":  ".gif",
	}
	videoMIME = map[string]string{
		"video/mp4":       ".mp4",
		"video/3gpp":      ".3gp",
		"video/quicktime": ".mov",
		"video/webm":      ".webm",
	}
	audioMIME = map[string]string{
		"audio/ogg":  ".ogg",
		"audio/mpeg": ".mp3",
		"audio/mp4":  ".m4a",
		"audio/aac":  ".aac",
		"audio/amr":  ".amr",
		"audio/wav":  ".wav",
		"audio/webm": ".weba",
	}
)

// documentMIME is the allowlist for things sent as documents. An allowlist
// rather than a blocklist: new dangerous formats appear constantly, and a
// format nobody anticipated should default to "no".
var documentMIME = map[string]string{
	"application/pdf":  ".pdf",
	"application/zip":  ".zip",
	"application/rtf":  ".rtf",
	"application/json": ".json",
	"text/plain":       ".txt",
	"text/csv":         ".csv",
	"text/markdown":    ".md",

	"application/msword": ".doc",
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document": ".docx",
	"application/vnd.ms-excel": ".xls",
	"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":         ".xlsx",
	"application/vnd.ms-powerpoint":                                             ".ppt",
	"application/vnd.openxmlformats-officedocument.presentationml.presentation": ".pptx",
	"application/vnd.oasis.opendocument.text":                                   ".odt",
	"application/vnd.oasis.opendocument.spreadsheet":                            ".ods",
	"application/vnd.oasis.opendocument.presentation":                           ".odp",
}

// blockedExt refuses executables and script formats outright, whatever their
// bytes claim to be.
//
// A .exe renamed to .pdf sniffs as application/octet-stream and would be
// rejected by the allowlist anyway; this catches the reverse case, where a
// genuinely benign-sniffing container (a .zip, say) carries a name that some
// downstream client would happily execute.
var blockedExt = map[string]bool{
	".exe": true, ".com": true, ".bat": true, ".cmd": true, ".msi": true,
	".scr": true, ".pif": true, ".cpl": true, ".dll": true, ".sys": true,
	".vbs": true, ".vbe": true, ".js": true, ".jse": true, ".wsf": true,
	".wsh": true, ".ps1": true, ".psm1": true, ".sh": true, ".bash": true,
	".jar": true, ".apk": true, ".app": true, ".deb": true, ".rpm": true,
	".lnk": true, ".reg": true, ".hta": true, ".chm": true,
	".html": true, ".htm": true, ".svg": true, ".xhtml": true,
}

// File is one validated attachment ready to be stored and sent.
type File struct {
	Kind       Kind
	MIME       string
	Name       string
	Ext        string
	Size       int64
	Caption    string
	AsDocument bool // forced document treatment even though it sniffs as media
}

// Classify decides what a file is and whether it may be accepted.
//
//   - name    : the browser-supplied filename (untrusted)
//   - declared: the browser-supplied Content-Type (untrusted, may be empty)
//   - head    : the first bytes of the file, at least 512 where available
//   - size    : the exact byte length
//   - asDoc   : caller asks for document treatment (the "Dokumen" menu entry)
func Classify(name, declared string, head []byte, size int64, asDoc bool) (File, error) {
	if size <= 0 {
		return File{}, ErrEmptyFile
	}

	safeName := SanitizeFileName(name)
	if safeName == "" {
		return File{}, ErrNameRequired
	}
	ext := strings.ToLower(path.Ext(safeName))
	if blockedExt[ext] {
		return File{}, fmt.Errorf("%w: %s", ErrTypeBlocked, ext)
	}

	// Checked before anything else is believed. An executable renamed to
	// .pdf sniffs as application/octet-stream, at which point the declared
	// Content-Type would otherwise be the only evidence left — and it is
	// supplied by the same party that renamed the file.
	if isExecutable(head) {
		return File{}, fmt.Errorf("%w: berkas program", ErrTypeBlocked)
	}

	mime := resolveMIME(declared, head, ext)

	kind := kindOf(mime)
	// "Send as document" keeps the bytes but changes how WhatsApp presents
	// them — an image sent this way is not recompressed.
	if asDoc && kind != KindDocument {
		if !allowedAsDocument(mime) {
			return File{}, fmt.Errorf("%w: %s", ErrTypeBlocked, mime)
		}
		kind = KindDocument
	}
	if kind == "" {
		return File{}, fmt.Errorf("%w: %s", ErrTypeBlocked, mime)
	}

	if limit := MaxBytesFor(kind); size > limit {
		return File{}, fmt.Errorf("%w (%s: maks %d MB)", ErrTooLarge, kind, limit>>20)
	}

	// Give a nameless-but-typed file a sane extension so the download has one.
	if ext == "" {
		if guessed := extFor(mime); guessed != "" {
			safeName += guessed
			ext = guessed
		}
	}

	return File{
		Kind:       kind,
		MIME:       mime,
		Name:       safeName,
		Ext:        ext,
		Size:       size,
		AsDocument: asDoc,
	}, nil
}

// resolveMIME picks the media type to trust.
//
// Sniffed bytes win, because they are the only part of the input the uploader
// cannot lie about. The declared type is consulted only where sniffing is
// genuinely uninformative: http.DetectContentType knows a few dozen signatures
// and answers "application/octet-stream" for everything else — including every
// Office format, which are all ZIP containers underneath.
func resolveMIME(declared string, head []byte, ext string) string {
	declared = normalizeMIME(declared)

	sniffed := normalizeMIME(http.DetectContentType(head))
	switch {
	case sniffed == "application/octet-stream", sniffed == "":
		// No signature matched. Fall through to the declared type.
	case sniffed == "text/plain":
		// Sniffing collapses every text format to text/plain; a declared
		// text/* or JSON refinement is safe to accept.
		if strings.HasPrefix(declared, "text/") || declared == "application/json" {
			return declared
		}
		return sniffed
	case sniffed == "application/zip":
		// Office documents are ZIP archives. Accept a declared OOXML/ODF type,
		// but only one that is on the allowlist.
		if _, ok := documentMIME[declared]; ok {
			return declared
		}
		return sniffed
	default:
		return sniffed
	}

	// Nothing recognisable in the bytes. The declared type may stand in, but
	// only when the extension independently agrees with it: two pieces of
	// evidence that were both supplied by the uploader are still weak, yet
	// requiring them to match closes the case where a file is given a harmless
	// name and an unrelated harmless media type.
	if declared != "" && declared != "application/octet-stream" && kindOf(declared) != "" {
		if ext == "" || mimeForExt(ext) == declared {
			return declared
		}
	}
	// Last resort: the extension, and only for types we already allow.
	if ext != "" {
		if m := mimeForExt(ext); m != "" {
			return m
		}
	}
	return "application/octet-stream"
}

// isExecutable reports whether the leading bytes carry a program signature.
//
// This is a blocklist, which is normally the weaker tool — but here it backs up
// an allowlist rather than replacing it. Its job is narrow: catch the case
// where a program is dressed up as a document, before any of the uploader's own
// claims about the file are consulted.
func isExecutable(head []byte) bool {
	prefixes := [][]byte{
		[]byte("MZ"),                                           // DOS / Windows PE
		[]byte("\x7fELF"),                                      // Linux
		[]byte("\xfe\xed\xfa\xce"), []byte("\xfe\xed\xfa\xcf"), // Mach-O
		[]byte("\xce\xfa\xed\xfe"), []byte("\xcf\xfa\xed\xfe"), // Mach-O, byte-swapped
		[]byte("\xca\xfe\xba\xbe"), // Java class / Mach-O fat binary
		[]byte("dex\n"),            // Android dalvik
		[]byte("#!"),               // script with an interpreter line
	}
	for _, p := range prefixes {
		if bytes.HasPrefix(head, p) {
			return true
		}
	}
	return false
}

func normalizeMIME(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if i := strings.IndexByte(s, ';'); i >= 0 { // drop "; charset=utf-8"
		s = strings.TrimSpace(s[:i])
	}
	return s
}

func kindOf(mime string) Kind {
	switch {
	case imageMIME[mime] != "":
		return KindImage
	case videoMIME[mime] != "":
		return KindVideo
	case audioMIME[mime] != "":
		return KindAudio
	case documentMIME[mime] != "":
		return KindDocument
	}
	return ""
}

// allowedAsDocument permits inline-renderable media to be sent as a document.
func allowedAsDocument(mime string) bool {
	return imageMIME[mime] != "" || videoMIME[mime] != "" || audioMIME[mime] != ""
}

func extFor(mime string) string {
	for _, table := range []map[string]string{imageMIME, videoMIME, audioMIME, documentMIME} {
		if e := table[mime]; e != "" {
			return e
		}
	}
	return ""
}

func mimeForExt(ext string) string {
	for _, table := range []map[string]string{imageMIME, videoMIME, audioMIME, documentMIME} {
		for m, e := range table {
			if e == ext {
				return m
			}
		}
	}
	return ""
}

// MIMEForExtension exposes the allowlist lookup to callers that only have a
// filename — the incoming path, where WhatsApp may omit the mimetype.
func MIMEForExtension(ext string) string { return mimeForExt(strings.ToLower(ext)) }

// ExtensionForMIME is the inverse, used to name an incoming file that WhatsApp
// sent without one.
func ExtensionForMIME(mime string) string { return extFor(normalizeMIME(mime)) }

// KindForMIME classifies a media type that did not come from an upload, such as
// the mimetype WhatsApp attaches to an incoming message. Unlike Classify it
// never rejects: an incoming file is already in the conversation, so the worst
// case is presenting it as a document.
func KindForMIME(mime string) Kind {
	if k := kindOf(normalizeMIME(mime)); k != "" {
		return k
	}
	switch {
	case strings.HasPrefix(mime, "image/"):
		return KindImage
	case strings.HasPrefix(mime, "video/"):
		return KindVideo
	case strings.HasPrefix(mime, "audio/"):
		return KindAudio
	}
	return KindDocument
}

// SanitizeFileName reduces a browser-supplied name to a single safe path
// segment.
//
// Directory components are dropped rather than escaped, so neither "../" nor a
// Windows "..\" nor an absolute path can steer where the file lands. Control
// characters are removed because they can hide a real extension from a human
// reading the name ("invoice.pdf<RLO>exe.").
func SanitizeFileName(raw string) string {
	name := strings.TrimSpace(raw)

	// Take the last segment under either separator; a name arriving from a
	// Windows browser can use backslashes, which path.Base leaves intact.
	name = strings.ReplaceAll(name, "\\", "/")
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		name = name[i+1:]
	}

	name = strings.Map(func(r rune) rune {
		switch {
		case r < 0x20, r == 0x7F: // C0 controls and DEL
			return -1
		case r >= 0x200B && r <= 0x200F, // zero-width marks
			r >= 0x202A && r <= 0x202E, // bidi override — the classic disguise
			r >= 0x2066 && r <= 0x2069,
			r == 0xFEFF:
			return -1
		case strings.ContainsRune(`<>:"|?*`, r): // reserved on Windows
			return '_'
		case unicode.IsSpace(r):
			return ' '
		}
		return r
	}, name)

	name = strings.TrimSpace(name)
	// A name that is only dots would resolve to the current or parent directory.
	if strings.Trim(name, ". ") == "" {
		return ""
	}
	// Leading dots make a hidden file and add nothing; strip them.
	name = strings.TrimLeft(name, ".")

	const maxName = 180
	if len(name) > maxName {
		ext := path.Ext(name)
		if len(ext) > 12 { // not a real extension, just a long tail
			ext = ""
		}
		name = name[:maxName-len(ext)] + ext
	}
	return strings.TrimSpace(name)
}

// TrimCaption bounds a caption to what WhatsApp accepts.
func TrimCaption(s string) string {
	s = strings.TrimSpace(s)
	runes := []rune(s)
	if len(runes) > MaxCaptionRunes {
		return string(runes[:MaxCaptionRunes])
	}
	return s
}
