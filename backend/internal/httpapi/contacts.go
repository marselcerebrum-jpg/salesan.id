package httpapi

import (
	"encoding/csv"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/salesan/omnichannel/backend/internal/models"
	"github.com/salesan/omnichannel/backend/internal/repository"
)

// The address book screen.
//
// Everything here is workspace-scoped in the query rather than checked first,
// which is the same rule the rest of the API follows: a row belonging to
// another tenant is not found, so it can be neither read, exported nor deleted.

// contactFilterFrom reads the three narrowings the screen offers.
func contactFilterFrom(r *http.Request) models.ContactFilter {
	return models.ContactFilter{
		AccountID:     queryUUID(r, "account_id"),
		ApplicationID: queryUUID(r, "application_id"),
		Search:        strings.TrimSpace(r.URL.Query().Get("search")),
		Limit:         queryInt(r, "limit", 100),
		Offset:        queryInt(r, "offset", 0),
	}
}

// handleListContacts returns one page plus the size of the whole filtered set.
//
// The total travels with the page because the header states it. Counting the
// page instead would make "16.321 kontak" mean "100 kontak", which is a
// different sentence.
func (s *Server) handleListContacts(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	f := contactFilterFrom(r)

	contacts, err := s.repo.ListContacts(r.Context(), user.WorkspaceID, f)
	if err != nil {
		writeAppError(w, err)
		return
	}
	total, err := s.repo.CountContacts(r.Context(), user.WorkspaceID, f)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"contacts": contacts, "total": total})
}

// handleContactFacets returns the two chip rows with their counts.
//
// `application_id` narrows the second row only. The numbers offered are the
// numbers of the chosen brand, counted inside it, because picking a number from
// another brand is not a narrowing of what is on screen.
func (s *Server) handleContactFacets(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	facets, err := s.repo.ContactFacets(
		r.Context(), user.WorkspaceID, queryUUID(r, "application_id"))
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, facets)
}

type deleteContactsRequest struct {
	IDs []string `json:"ids"`
}

// handleDeleteContacts removes the rows the operator ticked.
//
// Only what was named. There is deliberately no "delete everything" here: a
// button that empties an address book in one press is one slip away from
// undoing a day of syncing, and the per-row and per-selection paths cover every
// real need.
func (s *Server) handleDeleteContacts(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())

	var req deleteContactsRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if len(req.IDs) == 0 {
		writeError(w, http.StatusBadRequest, "no_target", "Pilih minimal satu kontak")
		return
	}
	const maxDelete = 1000
	if len(req.IDs) > maxDelete {
		writeError(w, http.StatusBadRequest, "too_many",
			"Maksimal 1000 kontak sekaligus. Persempit filternya lalu ulangi.")
		return
	}

	ids := make([]uuid.UUID, 0, len(req.IDs))
	for _, raw := range req.IDs {
		parsed, err := uuid.Parse(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_uuid", "Ada id kontak yang tidak valid")
			return
		}
		ids = append(ids, parsed)
	}

	removed, err := s.repo.DeleteContacts(r.Context(), user.WorkspaceID, ids)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": removed})
}

// handleDeleteContact removes one row, for the bin icon on a line.
func (s *Server) handleDeleteContact(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	id, ok := parseUUIDParam(w, chi.URLParam(r, "id"), "contact_id")
	if !ok {
		return
	}
	removed, err := s.repo.DeleteContacts(r.Context(), user.WorkspaceID, []uuid.UUID{id})
	if err != nil {
		writeAppError(w, err)
		return
	}
	if removed == 0 {
		writeError(w, http.StatusNotFound, "not_found", "Kontak tidak ditemukan")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": removed})
}

// handleMergeDuplicateContacts folds numbers stored in more than one shape.
func (s *Server) handleMergeDuplicateContacts(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	result, err := s.repo.MergeDuplicateContacts(r.Context(), user.WorkspaceID)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// maxContactImportBytes bounds one upload. A CSV of a hundred thousand numbers
// is well under this; anything larger is a mistake worth refusing early.
const maxContactImportBytes = 8 << 20

// handleImportContacts reads a CSV into one WhatsApp account's address book.
func (s *Server) handleImportContacts(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())

	reader, err := r.MultipartReader()
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_upload", "Permintaan bukan unggahan berkas")
		return
	}

	var (
		accountID *uuid.UUID
		rows      []repository.ImportContactRow
		parsed    bool
		parseErr  string
	)

	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_upload", "Berkas gagal dibaca")
			return
		}

		switch part.FormName() {
		case "account_id":
			raw := readField(part, 64)
			id, perr := uuid.Parse(strings.TrimSpace(raw))
			if perr != nil {
				_ = part.Close()
				writeError(w, http.StatusBadRequest, "invalid_uuid", "Nomor WhatsApp tujuan tidak valid")
				return
			}
			accountID = &id
		case "file":
			rows, parseErr = parseContactCSV(io.LimitReader(part, maxContactImportBytes))
			parsed = true
			_ = part.Close()
		default:
			_ = part.Close()
		}
	}

	if accountID == nil {
		writeError(w, http.StatusBadRequest, "missing_account",
			"Pilih nomor WhatsApp tujuan terlebih dahulu")
		return
	}
	if !parsed {
		writeError(w, http.StatusBadRequest, "missing_file", "Berkas CSV tidak ditemukan")
		return
	}
	if parseErr != "" {
		writeError(w, http.StatusBadRequest, "invalid_csv", parseErr)
		return
	}
	if len(rows) == 0 {
		writeError(w, http.StatusBadRequest, "empty_csv",
			"Tidak ada baris yang bisa dibaca. Pastikan ada kolom nomor.")
		return
	}

	result, err := s.repo.ImportContacts(r.Context(), user.WorkspaceID, *accountID, rows)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// parseContactCSV reads a two-column file without insisting on its shape.
//
// The header is read if there is one and guessed past if there is not: a file
// exported from a phone, from a spreadsheet, or typed by hand all arrive here,
// and refusing one for lacking a header would be pedantry rather than safety.
// Only the number is required; a name is a nicety.
func parseContactCSV(src io.Reader) ([]repository.ImportContactRow, string) {
	reader := csv.NewReader(src)
	// Rows are allowed to have different widths: a trailing comma or a missing
	// name should not fail the whole file.
	reader.FieldsPerRecord = -1
	reader.TrimLeadingSpace = true

	records, err := reader.ReadAll()
	if err != nil {
		return nil, "Format CSV tidak terbaca: " + err.Error()
	}
	if len(records) == 0 {
		return nil, ""
	}

	phoneCol, nameCol := 0, 1
	start := 0
	if head := records[0]; looksLikeHeader(head) {
		start = 1
		phoneCol, nameCol = -1, -1
		for i, cell := range head {
			switch normaliseHeader(cell) {
			case "nomor", "phone", "number", "telepon", "hp", "whatsapp", "msisdn":
				phoneCol = i
			case "nama", "name", "fullname", "contact":
				nameCol = i
			}
		}
		if phoneCol < 0 {
			return nil, "Tidak ada kolom nomor. Gunakan judul kolom: nomor, phone, atau number."
		}
	}

	out := make([]repository.ImportContactRow, 0, len(records)-start)
	for _, rec := range records[start:] {
		var row repository.ImportContactRow
		if phoneCol >= 0 && phoneCol < len(rec) {
			row.Phone = strings.TrimSpace(rec[phoneCol])
		}
		if nameCol >= 0 && nameCol < len(rec) {
			row.Name = strings.TrimSpace(rec[nameCol])
		}
		if row.Phone == "" && row.Name == "" {
			continue // a blank line, not a rejection
		}
		out = append(out, row)
	}
	return out, ""
}

// looksLikeHeader reports whether the first row names its columns rather than
// holding data. A header has no cell that reads as a phone number.
func looksLikeHeader(rec []string) bool {
	for _, cell := range rec {
		if repository.NormalizePhone(cell) != "" {
			return false
		}
	}
	return len(rec) > 0
}

func normaliseHeader(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.NewReplacer(" ", "", "_", "", "-", "").Replace(s)
	return s
}

// handleExportContacts writes the current filter out as a CSV.
//
// Built from the same filter the screen is showing, so the file matches the
// page: exporting while a number is selected exports that number's contacts and
// says so in the filename.
func (s *Server) handleExportContacts(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	f := contactFilterFrom(r)

	contacts, err := s.repo.ExportContacts(r.Context(), user.WorkspaceID, f)
	if err != nil {
		writeAppError(w, err)
		return
	}

	name := fmt.Sprintf("kontak-%s.csv", time.Now().Format("2006-01-02"))
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition",
		mime.FormatMediaType("attachment", map[string]string{"filename": name}))

	cw := csv.NewWriter(w)
	defer cw.Flush()

	_ = cw.Write([]string{"nomor", "nama", "aplikasi", "nomor_whatsapp", "label"})
	for _, c := range contacts {
		_ = cw.Write([]string{
			deref(c.PhoneNumber),
			firstNonBlank(deref(c.Name), deref(c.PushName)),
			deref(c.ApplicationCode),
			deref(c.AccountName),
			strings.Join(c.Labels, "; "),
		})
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func firstNonBlank(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
