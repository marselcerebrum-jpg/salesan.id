package httpapi

import (
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/salesan/omnichannel/backend/internal/media"
	"github.com/salesan/omnichannel/backend/internal/repository"
	"github.com/salesan/omnichannel/backend/internal/wa"
)

// maxUploadBytes is the hard ceiling on one request body, sitting above the
// largest per-kind limit so that an oversized file is rejected by the validator
// with a useful message rather than by the reader with a generic one.
const maxUploadBytes = media.MaxDocumentBytes + (2 << 20)

// handleSendMedia accepts one file and sends it to a conversation.
//
// One file per request, deliberately. It is what lets the browser show real
// per-file progress (the upload progress of this very request), and it makes a
// retry a repeat of one request rather than a partial replay of a batch. The
// client sends a group of files by issuing these in order.
func (s *Server) handleSendMedia(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	conversationID, ok := parseUUIDParam(w, chi.URLParam(r, "id"), "conversation_id")
	if !ok {
		return
	}
	if !s.manager.MediaEnabled() {
		writeError(w, http.StatusServiceUnavailable, "media_disabled",
			"Penyimpanan media tidak siap. Periksa log backend.")
		return
	}

	// The conversation is resolved through the workspace, so a caller cannot
	// send into a thread belonging to another tenant.
	if _, err := s.repo.GetConversation(r.Context(), user.WorkspaceID, conversationID); err != nil {
		writeAppError(w, err)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	reader, err := r.MultipartReader()
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body",
			"Permintaan harus berupa multipart/form-data")
		return
	}

	// Streamed rather than parsed into memory: a 64 MB video must never be held
	// in RAM in one piece, and the temp file is what whatsmeow's encryptor and
	// the storage upload both read from.
	var (
		fileName    string
		declared    string
		caption     string
		clientToken string
		replyToRaw  string
		asDocument  bool
		temp        *os.File
		size        int64
	)
	defer func() {
		if temp != nil {
			name := temp.Name()
			_ = temp.Close()
			_ = os.Remove(name) // temporary files never outlive the request
		}
	}()

	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			writeUploadError(w, err)
			return
		}

		switch part.FormName() {
		case "caption":
			caption = readField(part, 8<<10)
		case "client_token":
			clientToken = readField(part, 128)
		case "as_document":
			asDocument, _ = strconv.ParseBool(readField(part, 16))
		case "reply_to":
			replyToRaw = readField(part, 64)
		case "file":
			if temp != nil {
				_ = part.Close()
				continue // only the first file part is honoured
			}
			fileName = part.FileName()
			declared = part.Header.Get("Content-Type")

			temp, err = os.CreateTemp("", "salesan-up-*")
			if err != nil {
				_ = part.Close()
				writeError(w, http.StatusInternalServerError, "internal_error",
					"Tidak bisa menyiapkan berkas sementara")
				return
			}
			size, err = io.Copy(temp, part)
			_ = part.Close()
			if err != nil {
				writeUploadError(w, err)
				return
			}
		default:
			_ = part.Close()
		}
	}

	if temp == nil {
		writeError(w, http.StatusBadRequest, "missing_file", "Berkas tidak ditemukan pada permintaan")
		return
	}

	// Classify from the file's own leading bytes. The name and the declared
	// Content-Type came from the browser and are treated as hints, not facts.
	head := make([]byte, 512)
	n, _ := temp.ReadAt(head, 0)
	head = head[:n]

	// A filename can also arrive in the part header's disposition parameters.
	if fileName == "" {
		if _, params, err := mime.ParseMediaType(declared); err == nil {
			fileName = params["name"]
		}
	}
	if fileName == "" {
		fileName = "berkas"
	}

	file, err := media.Classify(fileName, declared, head, size, asDocument)
	if err != nil {
		writeMediaValidationError(w, err)
		return
	}

	// Resolved the same way a text reply is, through the same guard: quoting a
	// message from another thread produces a quote the recipient cannot open,
	// so it is refused here rather than sent half-broken.
	var replyTo *repository.MessageTarget
	if replyToRaw != "" {
		replyID, ok := parseUUIDParam(w, replyToRaw, "reply_to")
		if !ok {
			return
		}
		target, err := s.manager.ReplyTarget(r.Context(), user.WorkspaceID, conversationID, replyID)
		if err != nil {
			writeAppError(w, err)
			return
		}
		replyTo = target
	}

	msg, err := s.manager.SendMedia(r.Context(), wa.SendMediaInput{
		WorkspaceID:    user.WorkspaceID,
		ConversationID: conversationID,
		SentBy:         user.ID,
		File:           file,
		Caption:        caption,
		ClientToken:    clientToken,
		ReplyTo:        replyTo,
		Source:         temp,
	})
	if err != nil {
		// A failed send still returns the message row, so the UI can show the
		// failed bubble and offer a retry rather than losing the attempt.
		if msg != nil {
			writeJSON(w, http.StatusOK, map[string]any{
				"message": msg,
				"error":   "send_failed",
				"detail":  err.Error(),
			})
			return
		}
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"message": msg})
}

// handleAttachmentURL mints a short-lived signed URL for one attachment,
// downloading it from WhatsApp first if this is the first time it is opened.
//
// Access control is the workspace on the attachment row: a file belonging to
// another tenant is reported as not found, and no URL is created for it.
func (s *Server) handleAttachmentURL(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	attachmentID, ok := parseUUIDParam(w, chi.URLParam(r, "id"), "attachment_id")
	if !ok {
		return
	}

	link, err := s.manager.AttachmentURL(r.Context(), user.WorkspaceID, attachmentID, queryBool(r, "download"))
	if err != nil {
		writeAppError(w, err)
		return
	}
	// The URL expires, so it must not be cached by a proxy on the way back.
	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, http.StatusOK, link)
}

// handleServeMedia serves a file from the local storage backend.
//
// Mounted OUTSIDE the authenticated group on purpose: an <img> or <video> tag
// cannot send an Authorization header, so the signature in the URL is the
// credential. That is the same trade Supabase makes with its signed URLs, and
// it holds because the token names one object, is HMAC-signed with a key that
// never leaves the server, and stops working when it expires.
func (s *Server) handleServeMedia(w http.ResponseWriter, r *http.Request) {
	local := s.manager.LocalStore()
	if local == nil {
		http.NotFound(w, r) // Supabase is serving media; this route is inert
		return
	}

	key, download, err := local.VerifyToken(chi.URLParam(r, "token"))
	if err != nil {
		// Deliberately uniform: an expired link, a forged signature and a
		// malformed token all look the same from outside.
		writeError(w, http.StatusForbidden, "invalid_link", "Tautan tidak berlaku atau sudah kedaluwarsa.")
		return
	}

	file, info, err := local.Open(key)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "Berkas tidak ditemukan.")
		return
	}
	defer func() { _ = file.Close() }()

	// Sniffed from the bytes rather than taken from the request: the response
	// type decides how a browser treats the file, so it must not be steerable.
	head := make([]byte, 512)
	n, _ := file.ReadAt(head, 0)
	contentType := http.DetectContentType(head[:n])

	if download != "" {
		w.Header().Set("Content-Disposition",
			mime.FormatMediaType("attachment", map[string]string{"filename": download}))
	} else {
		// Anything not recognised as media is offered as a download rather than
		// rendered, so a file cannot be coaxed into executing in the origin.
		if !strings.HasPrefix(contentType, "image/") &&
			!strings.HasPrefix(contentType, "video/") &&
			!strings.HasPrefix(contentType, "audio/") {
			w.Header().Set("Content-Disposition", "attachment")
		}
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	// Cacheable only by the browser that holds the link, and never beyond it.
	w.Header().Set("Cache-Control", "private, max-age=300")

	// ServeContent handles range requests, which is what lets a video seek.
	http.ServeContent(w, r, key, info.ModTime(), file)
}

// readField reads a small text part, bounded so a hostile client cannot use a
// caption field to exhaust memory.
func readField(part io.ReadCloser, limit int64) string {
	defer func() { _ = part.Close() }()
	data, err := io.ReadAll(io.LimitReader(part, limit))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// writeUploadError distinguishes "you sent too much" from a genuine failure.
func writeUploadError(w http.ResponseWriter, err error) {
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		writeError(w, http.StatusRequestEntityTooLarge, "file_too_large",
			fmt.Sprintf("Berkas terlalu besar. Maksimal %d MB.", media.MaxDocumentBytes>>20))
		return
	}
	writeError(w, http.StatusBadRequest, "upload_failed", "Gagal membaca berkas yang diunggah")
}

// writeMediaValidationError maps a validation failure onto a message the
// operator can act on.
func writeMediaValidationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, media.ErrEmptyFile):
		writeError(w, http.StatusBadRequest, "empty_file", "Berkas kosong")
	case errors.Is(err, media.ErrNameRequired):
		writeError(w, http.StatusBadRequest, "invalid_name", "Nama berkas tidak valid")
	case errors.Is(err, media.ErrTooLarge):
		writeError(w, http.StatusRequestEntityTooLarge, "file_too_large", err.Error())
	case errors.Is(err, media.ErrTypeBlocked):
		writeError(w, http.StatusBadRequest, "type_not_allowed",
			"Tipe berkas ini tidak diizinkan")
	default:
		writeError(w, http.StatusBadRequest, "invalid_file", err.Error())
	}
}
