package httpapi

import (
	"errors"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/salesan/omnichannel/backend/internal/media"
	"github.com/salesan/omnichannel/backend/internal/repository"
	"github.com/salesan/omnichannel/backend/internal/wa"
)

func (s *Server) handleListApplications(w http.ResponseWriter, r *http.Request) {
	sc, ok := s.scopeFor(w, r)
	if !ok {
		return
	}
	apps, err := s.repo.ListApplications(r.Context(), sc)
	if err != nil {
		writeAppError(w, err)
		return
	}
	for i := range apps {
		s.signAppIcon(r, &apps[i].IconURL)
	}
	// The logo links expire; a proxy must not hand an old list to somebody else.
	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, http.StatusOK, map[string]any{"applications": apps})
}

// signAppIcon replaces a stored logo key with a link that expires. The key
// itself never leaves the server.
func (s *Server) signAppIcon(r *http.Request, icon **string) {
	if *icon == nil || **icon == "" {
		return
	}
	signed := s.manager.AppIconURL(r.Context(), **icon)
	if signed == "" {
		*icon = nil
		return
	}
	*icon = &signed
}

// handleUploadApplicationIcon sets an application's logo.
//
// Leader only, like every other change to an application. The file is checked
// the way every upload here is: its type is decided from its own bytes, and only
// a PNG, JPEG or WebP is accepted — no SVG, which can carry script, and no GIF,
// which is not a logo. The previous logo is deleted once the new one is in
// place, and the temporary copy never outlives the request.
func (s *Server) handleUploadApplicationIcon(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	if !s.requireLeader(w, r) {
		return
	}
	id, ok := parseUUIDParam(w, chi.URLParam(r, "id"), "application_id")
	if !ok {
		return
	}
	if !s.manager.MediaEnabled() {
		writeError(w, http.StatusServiceUnavailable, "media_disabled",
			"Penyimpanan media tidak siap. Periksa log backend.")
		return
	}
	if _, err := s.repo.GetApplication(r.Context(), user.WorkspaceID, id); err != nil {
		writeAppError(w, err)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, wa.MaxAppIconBytes+(1<<20))
	reader, err := r.MultipartReader()
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "Permintaan harus berupa multipart/form-data")
		return
	}

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
			_ = os.Remove(name)
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
		temp, err = os.CreateTemp("", "salesan-icon-*")
		if err != nil {
			_ = part.Close()
			writeError(w, http.StatusInternalServerError, "internal_error", "Tidak bisa menyiapkan berkas sementara")
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
		writeError(w, http.StatusBadRequest, "missing_file", "Berkas logo tidak ditemukan pada permintaan")
		return
	}
	if size > wa.MaxAppIconBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "file_too_large", "Logo melebihi batas 2 MB")
		return
	}

	head := make([]byte, 512)
	n, _ := temp.ReadAt(head, 0)
	file, err := media.Classify(fileName, declared, head[:n], size, false)
	if err != nil {
		writeMediaValidationError(w, err)
		return
	}
	switch file.MIME {
	case "image/png", "image/jpeg", "image/webp":
	default:
		writeError(w, http.StatusUnsupportedMediaType, "invalid_type",
			"Logo harus berupa gambar PNG, JPG, atau WebP")
		return
	}
	if _, err := temp.Seek(0, io.SeekStart); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Berkas tidak dapat dibaca ulang")
		return
	}

	key, err := s.manager.UploadAppIcon(r.Context(), user.WorkspaceID, id, file, io.NopCloser(temp), size)
	if err != nil {
		s.log.Error("upload app icon", "application_id", id, "err", err)
		writeError(w, http.StatusBadGateway, "upload_failed", "Logo gagal diunggah ke penyimpanan")
		return
	}
	previous, err := s.repo.SetApplicationIcon(r.Context(), user.WorkspaceID, id, &key)
	if err != nil {
		// The row did not take it, so the file would point at nothing.
		s.manager.RemoveAppIcon(r.Context(), key)
		writeAppError(w, err)
		return
	}
	if previous != nil {
		s.manager.RemoveAppIcon(r.Context(), *previous)
	}

	signed := s.manager.AppIconURL(r.Context(), key)
	writeJSON(w, http.StatusOK, map[string]any{"icon_url": signed})
}

// handleDeleteApplicationIcon removes a logo; the tile goes back to its code
// and colour.
func (s *Server) handleDeleteApplicationIcon(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	if !s.requireLeader(w, r) {
		return
	}
	id, ok := parseUUIDParam(w, chi.URLParam(r, "id"), "application_id")
	if !ok {
		return
	}
	previous, err := s.repo.SetApplicationIcon(r.Context(), user.WorkspaceID, id, nil)
	if err != nil {
		writeAppError(w, err)
		return
	}
	if previous != nil {
		s.manager.RemoveAppIcon(r.Context(), *previous)
	}
	writeJSON(w, http.StatusOK, map[string]any{"icon_url": nil})
}

type createApplicationRequest struct {
	Code  string `json:"code"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

// requireLeader gates the settings-level operations.
//
// An application is the top of the scoping hierarchy: everything a PIC or a
// Freelance may see hangs off one. Letting anybody create or delete them would
// make the access rules built on top of them decorative.
func (s *Server) requireLeader(w http.ResponseWriter, r *http.Request) bool {
	sc, ok := s.scopeFor(w, r)
	if !ok {
		return false
	}
	if !sc.IsLeader() {
		writeError(w, http.StatusForbidden, "forbidden",
			"Hanya Leader yang dapat mengubah daftar aplikasi")
		return false
	}
	return true
}

func (s *Server) handleCreateApplication(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	if !s.requireLeader(w, r) {
		return
	}

	var req createApplicationRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	req.Code = strings.ToUpper(strings.TrimSpace(req.Code))
	req.Name = strings.TrimSpace(req.Name)
	if req.Code == "" {
		writeError(w, http.StatusBadRequest, "invalid_body", "Kode aplikasi wajib diisi")
		return
	}
	if req.Name == "" {
		req.Name = req.Code
	}
	if req.Color == "" {
		req.Color = "#0F3D2E"
	}

	app, err := s.repo.CreateApplication(r.Context(), user.WorkspaceID, repository.CreateApplicationInput{
		Code:  req.Code,
		Name:  req.Name,
		Color: req.Color,
	})
	if err != nil {
		if strings.Contains(err.Error(), "uq_applications_workspace_code") {
			writeError(w, http.StatusConflict, "duplicate_code", "Kode aplikasi sudah dipakai")
			return
		}
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, app)
}

type updateApplicationRequest struct {
	Name     *string `json:"name"`
	Color    *string `json:"color"`
	IsActive *bool   `json:"is_active"`
}

func (s *Server) handleUpdateApplication(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	if !s.requireLeader(w, r) {
		return
	}
	id, ok := parseUUIDParam(w, chi.URLParam(r, "id"), "application_id")
	if !ok {
		return
	}

	var req updateApplicationRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	app, err := s.repo.UpdateApplication(r.Context(), user.WorkspaceID, id, repository.UpdateApplicationInput{
		Name:     req.Name,
		Color:    req.Color,
		IsActive: req.IsActive,
	})
	if err != nil {
		writeAppError(w, err)
		return
	}
	s.signAppIcon(r, &app.IconURL)
	writeJSON(w, http.StatusOK, app)
}

func (s *Server) handleDeleteApplication(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	if !s.requireLeader(w, r) {
		return
	}
	id, ok := parseUUIDParam(w, chi.URLParam(r, "id"), "application_id")
	if !ok {
		return
	}
	if err := s.repo.DeleteApplication(r.Context(), user.WorkspaceID, id); err != nil {
		writeAppError(w, err)
		return
	}
	// The assignments that pointed at it go with it; the database cascades
	// those. What stays is every WhatsApp account, which simply becomes
	// "Tanpa aplikasi" rather than being deleted along with the label on it.
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

// The address book handlers live in contacts.go, next to the import, export,
// merge and delete that belong with them.
