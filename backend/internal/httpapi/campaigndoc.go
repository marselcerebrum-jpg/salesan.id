package httpapi

import (
	"errors"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/salesan/omnichannel/backend/internal/media"
	"github.com/salesan/omnichannel/backend/internal/wa"
)

// handleUploadCampaignDocument stores one document for a broadcast to send.
//
// Uploaded rather than linked because a document's filename is shown on the
// recipient's screen, and because the file usually does not live anywhere public
// to begin with. It lands in the private bucket, is sent from there, and is
// deleted when the campaign finishes.
//
// Nothing about the file is taken on trust. The name and the declared
// Content-Type came from the browser; the type is decided from the file's own
// leading bytes, executables are refused however they are named, and the name is
// sanitised before it is stored or shown.
func (s *Server) handleUploadCampaignDocument(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	if _, ok := s.scopeFor(w, r); !ok {
		return
	}
	if !s.manager.MediaEnabled() {
		writeError(w, http.StatusServiceUnavailable, "media_disabled",
			"Penyimpanan media tidak siap. Periksa log backend.")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, wa.MaxCampaignDocumentBytes+(1<<20))
	reader, err := r.MultipartReader()
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body",
			"Permintaan harus berupa multipart/form-data")
		return
	}

	// Streamed to a temporary file rather than held in memory: the bytes are
	// read twice, once to classify the head and once to upload.
	var (
		fileName string
		declared string
		temp     *os.File
		size     int64
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
		if part.FormName() != "file" || temp != nil {
			_ = part.Close()
			continue
		}

		fileName = part.FileName()
		declared = part.Header.Get("Content-Type")

		temp, err = os.CreateTemp("", "salesan-doc-*")
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
	}

	if temp == nil {
		writeError(w, http.StatusBadRequest, "missing_file", "Berkas tidak ditemukan pada permintaan")
		return
	}
	if size > wa.MaxCampaignDocumentBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "file_too_large",
			"Dokumen melebihi batas 16 MB")
		return
	}

	head := make([]byte, 512)
	n, _ := temp.ReadAt(head, 0)
	head = head[:n]

	name := media.SanitizeFileName(fileName)
	if name == "" {
		name = "dokumen"
	}

	// asDoc = true: a PDF or a spreadsheet must be sent as a document, not
	// re-interpreted as an image because its first bytes happen to look like one.
	file, err := media.Classify(name, declared, head, size, true)
	if err != nil {
		writeMediaValidationError(w, err)
		return
	}

	if _, err := temp.Seek(0, io.SeekStart); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Berkas tidak dapat dibaca ulang")
		return
	}

	key, err := s.manager.UploadCampaignDocument(
		r.Context(), user.WorkspaceID, file, io.NopCloser(temp), size)
	if err != nil {
		s.log.Error("upload campaign document", "err", err)
		writeError(w, http.StatusBadGateway, "upload_failed",
			"Dokumen gagal diunggah ke penyimpanan")
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		// The key, never a URL: the object is private, and the composer only has
		// to hand this back when the campaign is saved.
		"storage_path": key,
		"file_name":    file.Name,
		"mime":         file.MIME,
		"size":         size,
	})
}

// campaignDocumentName is what the recipient will see. Sanitised again on the
// way out, because the stored value is only as trustworthy as the day it was
// written and this is the string that reaches somebody else's phone.
func campaignDocumentName(raw string) string {
	name := media.SanitizeFileName(strings.TrimSpace(raw))
	if name == "" {
		return "dokumen"
	}
	return name
}
