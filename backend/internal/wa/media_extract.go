package wa

import (
	"path"
	"strings"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"

	"github.com/salesan/omnichannel/backend/internal/media"
)

// mediaPayload is the storable description of an attachment carried by an
// incoming WhatsApp message.
//
// It splits cleanly in two: the descriptive half (name, size, dimensions,
// thumbnail) is safe for the browser, and the DirectPath/MediaKey half is a
// credential that stays on the server.
type mediaPayload struct {
	Kind      string
	FileName  string
	Mime      string
	Size      int64
	Width     int
	Height    int
	Duration  int
	Thumbnail []byte

	DirectPath    string
	MediaKey      []byte
	FileEncSHA256 []byte
	FileSHA256    []byte
	MediaType     whatsmeow.MediaType
}

// mmsType maps a media type onto the string the download endpoint expects.
// whatsmeow keeps its own copy of this table unexported.
func mmsType(t whatsmeow.MediaType) string {
	switch t {
	case whatsmeow.MediaImage:
		return "image"
	case whatsmeow.MediaVideo:
		return "video"
	case whatsmeow.MediaAudio:
		return "audio"
	case whatsmeow.MediaDocument:
		return "document"
	}
	return "document"
}

// mediaTypeFor maps our attachment kind back onto whatsmeow's upload/download
// key derivation constant. Stickers use the image key set.
func mediaTypeFor(kind string) whatsmeow.MediaType {
	switch media.Kind(kind) {
	case media.KindImage, media.KindSticker:
		return whatsmeow.MediaImage
	case media.KindVideo:
		return whatsmeow.MediaVideo
	case media.KindAudio:
		return whatsmeow.MediaAudio
	default:
		return whatsmeow.MediaDocument
	}
}

// extractMedia pulls the attachment out of a message, or returns nil when the
// message carries none.
//
// Only the five media kinds WhatsApp actually transfers bytes for are handled.
// A location or contact card has no file behind it, so it stays a plain row.
func extractMedia(msg *waE2E.Message) *mediaPayload {
	if msg == nil {
		return nil
	}

	if m := msg.GetImageMessage(); m != nil {
		return &mediaPayload{
			Kind:          string(media.KindImage),
			Mime:          m.GetMimetype(),
			Size:          int64(m.GetFileLength()),
			Width:         int(m.GetWidth()),
			Height:        int(m.GetHeight()),
			Thumbnail:     m.GetJPEGThumbnail(),
			DirectPath:    m.GetDirectPath(),
			MediaKey:      m.GetMediaKey(),
			FileEncSHA256: m.GetFileEncSHA256(),
			FileSHA256:    m.GetFileSHA256(),
			MediaType:     whatsmeow.MediaImage,
		}
	}

	if m := msg.GetVideoMessage(); m != nil {
		return &mediaPayload{
			Kind:          string(media.KindVideo),
			Mime:          m.GetMimetype(),
			Size:          int64(m.GetFileLength()),
			Width:         int(m.GetWidth()),
			Height:        int(m.GetHeight()),
			Duration:      int(m.GetSeconds()),
			Thumbnail:     m.GetJPEGThumbnail(),
			DirectPath:    m.GetDirectPath(),
			MediaKey:      m.GetMediaKey(),
			FileEncSHA256: m.GetFileEncSHA256(),
			FileSHA256:    m.GetFileSHA256(),
			MediaType:     whatsmeow.MediaVideo,
		}
	}

	if m := msg.GetAudioMessage(); m != nil {
		return &mediaPayload{
			Kind:          string(media.KindAudio),
			Mime:          m.GetMimetype(),
			Size:          int64(m.GetFileLength()),
			Duration:      int(m.GetSeconds()),
			DirectPath:    m.GetDirectPath(),
			MediaKey:      m.GetMediaKey(),
			FileEncSHA256: m.GetFileEncSHA256(),
			FileSHA256:    m.GetFileSHA256(),
			MediaType:     whatsmeow.MediaAudio,
		}
	}

	if m := msg.GetDocumentMessage(); m != nil {
		return &mediaPayload{
			Kind: string(media.KindDocument),
			// The sender chose this name; it is used verbatim in a download
			// header later, so it goes through the same sanitiser as an upload.
			FileName:      media.SanitizeFileName(m.GetFileName()),
			Mime:          m.GetMimetype(),
			Size:          int64(m.GetFileLength()),
			Thumbnail:     m.GetJPEGThumbnail(),
			DirectPath:    m.GetDirectPath(),
			MediaKey:      m.GetMediaKey(),
			FileEncSHA256: m.GetFileEncSHA256(),
			FileSHA256:    m.GetFileSHA256(),
			MediaType:     whatsmeow.MediaDocument,
		}
	}

	if m := msg.GetStickerMessage(); m != nil {
		return &mediaPayload{
			Kind:          string(media.KindSticker),
			Mime:          m.GetMimetype(),
			Size:          int64(m.GetFileLength()),
			Width:         int(m.GetWidth()),
			Height:        int(m.GetHeight()),
			DirectPath:    m.GetDirectPath(),
			MediaKey:      m.GetMediaKey(),
			FileEncSHA256: m.GetFileEncSHA256(),
			FileSHA256:    m.GetFileSHA256(),
			MediaType:     whatsmeow.MediaImage,
		}
	}

	return nil
}

// downloadable reports whether the payload has enough material to fetch the
// bytes. A forwarded message occasionally arrives with the descriptive half
// only; without this check the download would fail with a confusing error
// deep inside whatsmeow.
func (p *mediaPayload) downloadable() bool {
	return p != nil && p.DirectPath != "" && len(p.MediaKey) > 0 && len(p.FileEncSHA256) > 0
}

// displayName produces the filename shown in the UI and used for downloads.
// WhatsApp only names documents; photos, video and audio arrive anonymous, so
// they get a stable name derived from their message id.
func (p *mediaPayload) displayName(waMessageID string) string {
	if p.FileName != "" {
		return p.FileName
	}

	ext := path.Ext(p.FileName)
	if ext == "" {
		ext = extensionForMime(p.Mime)
	}

	id := waMessageID
	if len(id) > 12 {
		id = id[:12]
	}
	switch media.Kind(p.Kind) {
	case media.KindImage:
		return "foto-" + id + ext
	case media.KindVideo:
		return "video-" + id + ext
	case media.KindAudio:
		return "audio-" + id + ext
	case media.KindSticker:
		return "stiker-" + id + ext
	default:
		return "berkas-" + id + ext
	}
}

// extensionForMime maps a media type to a file extension, falling back to the
// subtype itself so an unknown "image/heic" still yields ".heic" rather than a
// name with no extension at all.
func extensionForMime(mime string) string {
	if mime == "" {
		return ""
	}
	if i := strings.IndexByte(mime, ';'); i >= 0 {
		mime = strings.TrimSpace(mime[:i])
	}
	if e := media.ExtensionForMIME(mime); e != "" {
		return e
	}
	if i := strings.IndexByte(mime, '/'); i >= 0 {
		sub := mime[i+1:]
		if sub != "" && len(sub) <= 8 && !strings.ContainsAny(sub, "+.") {
			return "." + sub
		}
	}
	return ""
}
