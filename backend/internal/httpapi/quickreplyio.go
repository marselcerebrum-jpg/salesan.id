package httpapi

import (
	"encoding/csv"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/salesan/omnichannel/backend/internal/mediafetch"
	"github.com/salesan/omnichannel/backend/internal/models"
)

// Moving quick replies in and out as a spreadsheet.
//
// Written by hand one at a time, a set of canned replies is slow to build and
// slower to keep in step across brands. A CSV is the form most people already
// know how to edit in bulk, and it round-trips: what Export writes, Import
// reads back.
//
// The columns are the same in both directions and named in the operator's own
// language, so a file edited in a spreadsheet and handed back is still
// readable by the importer.
var quickReplyColumns = []string{
	"shortcut", "isi_pesan", "aplikasi", "kategori", "url_gambar", "aktif",
}

// maxQuickReplyImportBytes bounds one upload. Quick replies are short; a file
// larger than this is a mistake worth refusing before it is parsed.
const maxQuickReplyImportBytes = 2 << 20

// maxQuickReplyImportRows caps one import.
//
// Not an arbitrary round number: every row carrying an image costs one HTTP
// fetch to somebody else's server, and this endpoint holds a request open for
// all of them. Two hundred is comfortably more than any real set of canned
// replies and small enough that the slowest possible file still finishes
// inside the media timeout.
const maxQuickReplyImportRows = 200

// handleExportQuickReplies writes the caller's quick replies as a CSV.
//
// The same scope the list endpoint uses, so an export can never contain a reply
// the person could not already read on screen.
func (s *Server) handleExportQuickReplies(w http.ResponseWriter, r *http.Request) {
	sc, ok := s.scopeFor(w, r)
	if !ok {
		return
	}
	replies, err := s.repo.ListQuickReplies(
		r.Context(), sc.WorkspaceID, sc.ApplicationIDs, sc.All, false)
	if err != nil {
		writeAppError(w, err)
		return
	}

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="balas-cepat.csv"`)
	// The file carries workspace content; a proxy must not keep a copy.
	w.Header().Set("Cache-Control", "private, no-store")

	// A BOM, so Excel opens the file as UTF-8 instead of mangling every
	// accented character in the replies.
	_, _ = w.Write([]byte{0xEF, 0xBB, 0xBF})

	out := csv.NewWriter(w)
	_ = out.Write(quickReplyColumns)
	for _, q := range replies {
		app := ""
		if q.ApplicationCode != nil {
			app = *q.ApplicationCode
		}
		_ = out.Write([]string{
			q.Shortcut,
			q.Body,
			app,
			derefString(q.Category),
			derefString(q.MediaURL),
			strconv.FormatBool(q.IsActive),
		})
	}
	out.Flush()
}

// quickReplyImportRow is one line of the file, before it is checked.
type quickReplyImportRow struct {
	Line     int
	Shortcut string
	Body     string
	App      string
	Category string
	MediaURL string
	Active   bool
}

// quickReplyImportOutcome is what happened to one line, reported back so a
// partly-good file tells the operator exactly which lines to fix.
type quickReplyImportOutcome struct {
	Line     int    `json:"line"`
	Shortcut string `json:"shortcut"`
	Status   string `json:"status"` // disimpan | dilewati
	Reason   string `json:"reason,omitempty"`
}

// handleImportQuickReplies reads a CSV back into the workspace.
//
// Row by row rather than all or nothing. A file of two hundred replies that is
// rejected whole because line 137 has a dead image link is a file nobody can
// use; every good line is saved, and every refused line says why.
//
// An existing shortcut in the same application is UPDATED, not duplicated —
// the database key is (workspace, application, shortcut), so re-importing an
// edited export is how you edit in bulk.
func (s *Server) handleImportQuickReplies(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	sc, ok := s.scopeFor(w, r)
	if !ok {
		return
	}
	if !canManageQuickReplies(sc) {
		writeError(w, http.StatusForbidden, "forbidden",
			"Anda belum diberi peran, jadi belum dapat mengelola balas cepat")
		return
	}

	reader, err := r.MultipartReader()
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_upload", "Permintaan bukan unggahan berkas")
		return
	}

	var (
		rows     []quickReplyImportRow
		parsed   bool
		parseErr string
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
		if part.FormName() == "file" {
			rows, parseErr = parseQuickReplyCSV(io.LimitReader(part, maxQuickReplyImportBytes))
			parsed = true
		}
		_ = part.Close()
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
			"Tidak ada baris yang bisa dibaca. Pastikan ada kolom shortcut dan isi_pesan.")
		return
	}
	if len(rows) > maxQuickReplyImportRows {
		writeError(w, http.StatusBadRequest, "too_many_rows", fmt.Sprintf(
			"Berkas berisi %d baris; maksimal %d sekali impor. Pecah filenya.",
			len(rows), maxQuickReplyImportRows))
		return
	}

	// Application codes resolved once, not per row: the same brand appears on
	// most lines, and one lookup each would be a query per line.
	apps, err := s.repo.ListApplications(r.Context(), sc)
	if err != nil {
		writeAppError(w, err)
		return
	}
	byCode := make(map[string]uuid.UUID, len(apps))
	for _, a := range apps {
		byCode[strings.ToLower(strings.TrimSpace(a.Code))] = a.ID
	}

	outcomes := make([]quickReplyImportOutcome, 0, len(rows))
	saved := 0

	// Shortcuts already written by THIS file, so a second row aiming at the
	// same (application, shortcut) does not silently replace the first.
	//
	// Only within one import. Re-importing an edited export must still update
	// the rows it names — that is the whole point of the round trip — but two
	// different replies inside one file are two replies, and a real file has
	// them: the same shortcut used twice on one application, with different
	// words under each.
	usedInFile := map[string]bool{}

	for _, row := range rows {
		skip := func(reason string) {
			outcomes = append(outcomes, quickReplyImportOutcome{
				Line: row.Line, Shortcut: row.Shortcut, Status: "dilewati", Reason: reason,
			})
		}

		// Repaired, not refused: "FOLLOW UP" becomes follow_up. A space cannot
		// work as a shortcut — the slash menu closes on one — and refusing
		// those rows would mean refusing most of a real file.
		shortcut := normaliseShortcut(row.Shortcut)
		if shortcut == "" {
			skip("Pintasan harus mengandung minimal satu huruf atau angka")
			continue
		}
		body := strings.TrimSpace(row.Body)
		if body == "" {
			skip("Isi pesan kosong")
			continue
		}
		if len([]rune(body)) > 4096 {
			skip("Isi pesan lebih dari 4096 karakter")
			continue
		}

		// An empty application column means workspace-wide, which only a Leader
		// may write — the same rule the form enforces, applied here too so a
		// file cannot be the way around it.
		var appID *uuid.UUID
		if code := strings.ToLower(strings.TrimSpace(row.App)); code != "" {
			id, found := byCode[code]
			if !found {
				skip("Kode aplikasi \"" + row.App + "\" tidak dikenal atau di luar wewenang Anda")
				continue
			}
			appID = &id
		} else if !sc.All {
			skip("Kolom aplikasi kosong berarti berlaku di semua aplikasi, dan itu hanya untuk Leader")
			continue
		}

		// Two rows in one file aiming at the same shortcut on the same
		// application are two replies, so the second gets a suffix rather than
		// overwriting the first. Nothing in the file is lost, and the operator
		// can rename it afterwards from the list.
		scopeKey := "workspace"
		if appID != nil {
			scopeKey = appID.String()
		}
		unique := shortcut
		for n := 2; usedInFile[scopeKey+"/"+unique]; n++ {
			suffix := "_" + strconv.Itoa(n)
			trimmed := shortcut
			if len([]rune(trimmed))+len(suffix) > 40 {
				trimmed = string([]rune(trimmed)[:40-len(suffix)])
			}
			unique = trimmed + suffix
		}
		usedInFile[scopeKey+"/"+unique] = true
		shortcut = unique

		in := models.QuickReply{
			ApplicationID: appID,
			Shortcut:      shortcut,
			Title:         shortcut,
			Body:          body,
			IsActive:      row.Active,
		}
		if c := strings.TrimSpace(row.Category); c != "" {
			if len([]rune(c)) > 60 {
				skip("Kategori lebih dari 60 karakter")
				continue
			}
			in.Category = &c
		}

		// The image is checked the same way the form checks it: by fetching it.
		// A row whose link is dead or is not an image is refused on its own,
		// and the rest of the file still imports.
		if link := strings.TrimSpace(row.MediaURL); link != "" {
			res, ferr := mediafetch.Fetch(r.Context(), link, mediaLimitsFor(s), false)
			if ferr != nil {
				skip("Gambar tidak bisa diambil: " + mediaErrorText(ferr))
				continue
			}
			kindOK := string(res.Info.Kind) == "image"
			kind := string(res.Info.Kind)
			mime := res.Info.MIME
			size := res.Info.Size
			sha := res.SHA256
			res.Cleanup()

			if !kindOK {
				skip("Alamat itu bukan gambar")
				continue
			}
			in.MediaURL = &link
			in.MediaKind = &kind
			in.MediaMime = &mime
			in.MediaSizeBytes = &size
			in.MediaSHA256 = &sha
		}

		if _, err := s.repo.UpsertQuickReply(r.Context(), sc.WorkspaceID, user.ID, in); err != nil {
			skip("Gagal disimpan: " + err.Error())
			continue
		}
		saved++
		// A shortcut that had to be repaired is reported even though the row
		// succeeded: the operator has to know what to type to summon it, and
		// "follow_up" is not what they wrote in the file.
		note := ""
		if original := strings.TrimSpace(row.Shortcut); !strings.EqualFold(original, shortcut) {
			note = "Pintasan disesuaikan dari \"" + original + "\""
		}
		outcomes = append(outcomes, quickReplyImportOutcome{
			Line: row.Line, Shortcut: shortcut, Status: "disimpan", Reason: note,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"saved":   saved,
		"skipped": len(rows) - saved,
		"rows":    outcomes,
	})
}

// parseQuickReplyCSV reads the file into rows, by column name.
//
// The header is required here, unlike the contact importer which can guess a
// column of phone numbers. Six columns of free text have nothing to recognise
// them by, and guessing which is the message and which is the category would
// be worse than asking.
func parseQuickReplyCSV(src io.Reader) ([]quickReplyImportRow, string) {
	reader := csv.NewReader(src)
	reader.FieldsPerRecord = -1
	reader.TrimLeadingSpace = true

	records, err := reader.ReadAll()
	if err != nil {
		return nil, "Format CSV tidak terbaca: " + err.Error()
	}
	if len(records) == 0 {
		return nil, ""
	}

	col := map[string]int{}
	for i, cell := range records[0] {
		// Excel writes a BOM at the head of the first cell; without stripping
		// it the first column name never matches anything. Written as an escape
		// rather than the character itself: a literal BOM mid-file is not valid
		// Go source.
		name := normaliseHeader(strings.TrimPrefix(cell, "\ufeff"))
		switch name {
		case "shortcut", "pintasan", "kode":
			col["shortcut"] = i
		case "isipesan", "isi", "pesan", "body", "message":
			col["body"] = i
		case "aplikasi", "application", "app":
			col["app"] = i
		case "kategori", "category":
			col["category"] = i
		case "urlgambar", "gambar", "image", "mediaurl":
			col["media"] = i
		case "aktif", "active", "status":
			col["active"] = i
		}
	}
	if _, ok := col["shortcut"]; !ok {
		return nil, "Tidak ada kolom shortcut. Unduh contohnya lewat tombol Ekspor."
	}
	if _, ok := col["body"]; !ok {
		return nil, "Tidak ada kolom isi_pesan. Unduh contohnya lewat tombol Ekspor."
	}

	at := func(rec []string, key string) string {
		i, ok := col[key]
		if !ok || i >= len(rec) {
			return ""
		}
		return strings.TrimSpace(rec[i])
	}

	out := make([]quickReplyImportRow, 0, len(records)-1)
	for n, rec := range records[1:] {
		row := quickReplyImportRow{
			// +2: one for the header, one because people count from 1.
			Line:     n + 2,
			Shortcut: at(rec, "shortcut"),
			Body:     at(rec, "body"),
			App:      at(rec, "app"),
			Category: at(rec, "category"),
			MediaURL: at(rec, "media"),
			// Absent means active. A file written by hand should not have to
			// say "true" on every line to switch its replies on.
			Active: true,
		}
		if raw := at(rec, "active"); raw != "" {
			switch strings.ToLower(raw) {
			case "false", "0", "no", "tidak", "nonaktif", "off":
				row.Active = false
			}
		}
		if row.Shortcut == "" && row.Body == "" {
			continue // a blank line, not a rejection
		}
		out = append(out, row)
	}
	return out, ""
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
