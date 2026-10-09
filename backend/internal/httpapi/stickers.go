package httpapi

import (
	"errors"
	"io"
	"net/http"
	"os"

	"github.com/go-chi/chi/v5"

	"github.com/salesan/omnichannel/backend/internal/media"
	"github.com/salesan/omnichannel/backend/internal/repository"
)

// handleListStickers returns the workspace's sticker library.
//
//	GET /stickers
//
// Each row carries a freshly signed link. Minted here rather than stored,
// because a link has an expiry and a row does not — a URL written into the
// table would be correct for a few hours and wrong for ever after.
func (s *Server) handleListStickers(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	rows, err := s.repo.ListStickers(r.Context(), user.WorkspaceID)
	if err != nil {
		writeAppError(w, err)
		return
	}

	for i := range rows {
		link, err := s.manager.StickerURL(r.Context(), rows[i].StoragePath)
		if err != nil {
			// One unreadable file does not make the panel unusable. The tile
			// comes back without a link and the browser draws it as broken,
			// which is the truth and still leaves the others sendable.
			s.log.Warn("sign sticker url", "sticker_id", rows[i].ID, "err", err)
			continue
		}
		rows[i].URL = link
	}
	writeJSON(w, http.StatusOK, map[string]any{"stickers": rows})
}

// handleAddSticker puts one picture into the library.
//
//	POST /stickers   (multipart: file, name)
//
// The browser converts to WebP before uploading, because only it can show the
// operator what the sticker will look like. This re-checks rather than trusts:
// the conversion happens on the client, and a client is not evidence.
func (s *Server) handleAddSticker(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())

	reader, err := r.MultipartReader()
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "Butuh unggahan multipart")
		return
	}

	var (
		name     string
		fileName string
		declared string
		size     int64
		temp     *os.File
	)
	defer func() {
		if temp != nil {
			n := temp.Name()
			_ = temp.Close()
			_ = os.Remove(n)
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
		case "name":
			name = readField(part, 128)
		case "file":
			if temp != nil {
				_ = part.Close()
				continue
			}
			fileName = part.FileName()
			declared = part.Header.Get("Content-Type")
			temp, err = os.CreateTemp("", "salesan-sticker-up-*")
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
		writeError(w, http.StatusBadRequest, "invalid_request", "Berkas stiker tidak ada")
		return
	}

	head := make([]byte, 512)
	if _, err := temp.ReadAt(head, 0); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid_request", "Berkas tidak terbaca")
		return
	}

	file, err := media.ClassifyAs(fileName, declared, head, size, false, true)
	if err != nil {
		writeMediaValidationError(w, err)
		return
	}

	sticker, err := s.manager.AddSticker(r.Context(), user.WorkspaceID, user.ID, name, file, temp)
	if err != nil {
		writeAppError(w, err)
		return
	}
	if link, err := s.manager.StickerURL(r.Context(), sticker.StoragePath); err == nil {
		sticker.URL = link
	}
	writeJSON(w, http.StatusCreated, map[string]any{"sticker": sticker})
}

// handleDeleteSticker takes a sticker out of the library.
//
//	DELETE /stickers/{id}
//
// The tile goes; the bytes stay until nothing points at them. Messages already
// sent carry the same file, and deleting it from under them would blank a
// bubble somebody sent last week.
func (s *Server) handleDeleteSticker(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	id, ok := parseUUIDParam(w, chi.URLParam(r, "id"), "sticker_id")
	if !ok {
		return
	}
	if err := s.repo.DeleteSticker(r.Context(), user.WorkspaceID, id); err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

// handleSendSticker sends one sticker from the library.
//
//	POST /conversations/{id}/stickers   {"sticker_id": "...", "client_token": "...", "reply_to": "..."}
func (s *Server) handleSendSticker(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	conversationID, ok := parseUUIDParam(w, chi.URLParam(r, "id"), "conversation_id")
	if !ok {
		return
	}

	var req struct {
		StickerID   string `json:"sticker_id"`
		ClientToken string `json:"client_token"`
		ReplyTo     string `json:"reply_to"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	stickerID, ok := parseUUIDParam(w, req.StickerID, "sticker_id")
	if !ok {
		return
	}

	var replyTo *repository.MessageTarget
	if req.ReplyTo != "" {
		replyID, ok := parseUUIDParam(w, req.ReplyTo, "reply_to")
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

	msg, err := s.manager.SendSticker(
		r.Context(), user.WorkspaceID, conversationID, stickerID, user.ID, req.ClientToken, replyTo)
	if err != nil {
		if msg != nil {
			writeJSON(w, http.StatusOK, map[string]any{
				"message": msg, "error": "send_failed", "detail": err.Error(),
			})
			return
		}
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"message": msg})
}
