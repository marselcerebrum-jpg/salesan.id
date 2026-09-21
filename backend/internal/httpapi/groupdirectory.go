package httpapi

import (
	"archive/zip"
	"encoding/csv"
	"encoding/xml"
	"fmt"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/salesan/omnichannel/backend/internal/models"
)

// The group directory screen: one row per WhatsApp group, its members behind an
// arrow, and the whole thing exportable.

func groupFilterFrom(r *http.Request) models.GroupFilter {
	return models.GroupFilter{
		AccountID:     queryUUID(r, "account_id"),
		ApplicationID: queryUUID(r, "application_id"),
		Search:        strings.TrimSpace(r.URL.Query().Get("search")),
		Limit:         queryInt(r, "limit", 100),
		Offset:        queryInt(r, "offset", 0),
	}
}

// handleListGroups returns a page plus the two numbers the header states.
func (s *Server) handleListGroups(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	f := groupFilterFrom(r)

	groups, err := s.repo.ListGroups(r.Context(), user.WorkspaceID, f)
	if err != nil {
		writeAppError(w, err)
		return
	}
	total, fetched, err := s.repo.CountGroups(r.Context(), user.WorkspaceID, f)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"groups": groups, "total": total, "fetched": fetched,
	})
}

func (s *Server) handleGroupDirectoryFacets(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	facets, err := s.repo.GroupFacets(
		r.Context(), user.WorkspaceID, queryUUID(r, "application_id"))
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, facets)
}

// handleGroupDirectoryMembers returns one group and its participants.
//
// Both in one response because the page states both in one sentence: "994
// anggota · tersimpan · Bimbel BUMN". Two requests could disagree about which
// group is being looked at while one of them was still in flight.
func (s *Server) handleGroupDirectoryMembers(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	chatJID := strings.TrimSpace(r.URL.Query().Get("chat_jid"))
	if chatJID == "" {
		writeError(w, http.StatusBadRequest, "missing_group", "Grup tidak disebutkan")
		return
	}

	group, err := s.repo.GetGroup(r.Context(), user.WorkspaceID, chatJID)
	if err != nil {
		writeAppError(w, err)
		return
	}
	members, err := s.repo.GroupMembersByJID(r.Context(), user.WorkspaceID, chatJID)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"group": group, "members": members})
}

// handleRefreshOneGroup re-reads one group's metadata and membership.
//
// Tried through every number of ours inside it, stopping at the first that
// answers. A group we reach four ways only needs one working phone, and
// refusing because the first one is offline would be refusing on the strength
// of the least relevant fact available.
func (s *Server) handleRefreshOneGroup(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	chatJID := strings.TrimSpace(r.URL.Query().Get("chat_jid"))
	if chatJID == "" {
		writeError(w, http.StatusBadRequest, "missing_group", "Grup tidak disebutkan")
		return
	}

	ids, err := s.repo.GroupConversationIDs(r.Context(), user.WorkspaceID, chatJID)
	if err != nil {
		writeAppError(w, err)
		return
	}
	if len(ids) == 0 {
		writeError(w, http.StatusNotFound, "not_found", "Grup tidak ditemukan")
		return
	}

	var lastErr error
	for _, id := range ids {
		if err := s.manager.RefreshGroup(r.Context(), user.WorkspaceID, id); err != nil {
			lastErr = err
			continue
		}
		lastErr = nil
		break
	}
	if lastErr != nil {
		writeAppError(w, lastErr)
		return
	}

	group, err := s.repo.GetGroup(r.Context(), user.WorkspaceID, chatJID)
	if err != nil {
		writeAppError(w, err)
		return
	}
	members, err := s.repo.GroupMembersByJID(r.Context(), user.WorkspaceID, chatJID)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"group": group, "members": members})
}

// handleExportGroupMembers writes one group's participants as CSV or a sheet.
func (s *Server) handleExportGroupMembers(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	chatJID := strings.TrimSpace(r.URL.Query().Get("chat_jid"))
	if chatJID == "" {
		writeError(w, http.StatusBadRequest, "missing_group", "Grup tidak disebutkan")
		return
	}

	group, err := s.repo.GetGroup(r.Context(), user.WorkspaceID, chatJID)
	if err != nil {
		writeAppError(w, err)
		return
	}
	members, err := s.repo.GroupMembersByJID(r.Context(), user.WorkspaceID, chatJID)
	if err != nil {
		writeAppError(w, err)
		return
	}

	header := []string{"nama", "nomor", "admin"}
	rows := make([][]string, 0, len(members))
	for _, m := range members {
		// Both columns stay empty when the address book cannot answer. The old
		// fallback wrote the JID's local part here, which put a LID in a column
		// called "nomor" — a number nobody can dial, in a file made for dialling.
		phone := ""
		if m.PhoneNumber != nil {
			phone = *m.PhoneNumber
		}
		admin := ""
		if m.IsAdmin {
			admin = "admin"
		}
		rows = append(rows, []string{m.DisplayName, phone, admin})
	}

	base := safeFileName(group.Name)
	if base == "" {
		base = "grup"
	}

	if r.URL.Query().Get("format") == "xlsx" {
		name := base + ".xlsx"
		w.Header().Set("Content-Type",
			"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
		w.Header().Set("Content-Disposition",
			mime.FormatMediaType("attachment", map[string]string{"filename": name}))
		if err := writeXLSX(w, "Anggota", header, rows); err != nil {
			s.log.Error("write xlsx", "err", err)
		}
		return
	}

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition",
		mime.FormatMediaType("attachment", map[string]string{"filename": base + ".csv"}))

	cw := csv.NewWriter(w)
	defer cw.Flush()
	_ = cw.Write(header)
	for _, row := range rows {
		_ = cw.Write(row)
	}
}

// safeFileName keeps a group's own name on its download without letting that
// name decide anything about the filesystem. Group names carry emoji, slashes
// and quotes, and all three belong nowhere near a Content-Disposition.
func safeFileName(s string) string {
	var b strings.Builder
	for _, ch := range s {
		switch {
		case ch >= 'a' && ch <= 'z', ch >= 'A' && ch <= 'Z', ch >= '0' && ch <= '9':
			b.WriteRune(ch)
		case ch == ' ' || ch == '-' || ch == '_' || ch == '(' || ch == ')':
			b.WriteRune('-')
		}
		if b.Len() > 60 {
			break
		}
	}
	return strings.Trim(strings.ReplaceAll(b.String(), "--", "-"), "-")
}

// handleFetchGroups pulls member lists for groups that have none yet.
//
// A batch at a time, not the lot. Each group costs a round trip to WhatsApp, so
// nine hundred of them in one request would time out long before finishing and
// leave nothing to show for it. The response says how many are still waiting,
// and the screen asks again; progress that can be watched beats a spinner that
// might be stuck.
func (s *Server) handleFetchGroups(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	f := groupFilterFrom(r)

	// First correct which groups we are even in. History sync imports threads
	// for groups we left, and without this the batch below would spend its whole
	// budget asking WhatsApp about groups it will refuse to answer for.
	dropped, err := s.manager.SyncGroupMembership(r.Context(), user.WorkspaceID)
	if err != nil {
		s.log.Warn("group membership sweep", "err", err)
	}

	const batch = 25
	ids, err := s.repo.GroupsNeedingFetch(r.Context(), user.WorkspaceID, f, batch)
	if err != nil {
		writeAppError(w, err)
		return
	}

	done, failed := 0, 0
	var firstReason string
	for _, id := range ids {
		if r.Context().Err() != nil {
			break
		}
		if err := s.manager.RefreshGroup(r.Context(), user.WorkspaceID, id); err != nil {
			failed++
			if firstReason == "" {
				firstReason = err.Error()
			}
			continue
		}
		done++
	}

	total, fetched, err := s.repo.CountGroups(r.Context(), user.WorkspaceID, f)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"done":      done,
		"failed":    failed,
		"reason":    firstReason,
		"total":     total,
		"fetched":   fetched,
		"remaining": total - fetched,
		"corrected": dropped,
	})
}

// groupExportRows turns the directory into the table both file formats share,
// so the CSV and the spreadsheet can never carry different columns.
func groupExportRows(groups []models.GroupRow) ([]string, [][]string) {
	header := []string{"nama_grup", "jumlah_nomor", "nomor_kami", "aplikasi", "jumlah_anggota", "chat_jid"}

	rows := make([][]string, 0, len(groups))
	for _, g := range groups {
		names := make([]string, 0, len(g.Accounts))
		apps := map[string]struct{}{}
		for _, a := range g.Accounts {
			names = append(names, a.AccountName)
			if a.ApplicationCode != "" {
				apps[a.ApplicationCode] = struct{}{}
			}
		}
		codes := make([]string, 0, len(apps))
		for code := range apps {
			codes = append(codes, code)
		}

		members := fmt.Sprint(g.MemberCount)
		if !g.Fetched {
			// Never a bare zero for something nobody has looked at. A count of
			// zero and an unread group are different facts.
			members = "belum diambil"
		}
		rows = append(rows, []string{
			g.Name,
			fmt.Sprint(g.AccountCount),
			strings.Join(names, ", "),
			strings.Join(codes, ", "),
			members,
			g.ChatJID,
		})
	}
	return header, rows
}

// handleExportGroups writes the directory as CSV or as a spreadsheet.
func (s *Server) handleExportGroups(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	f := groupFilterFrom(r)

	var only []string
	if raw := strings.TrimSpace(r.URL.Query().Get("chat_jids")); raw != "" {
		only = strings.Split(raw, ",")
	}

	groups, err := s.repo.ExportGroups(r.Context(), user.WorkspaceID, f, only)
	if err != nil {
		writeAppError(w, err)
		return
	}
	header, rows := groupExportRows(groups)

	stamp := time.Now().Format("2006-01-02")
	if r.URL.Query().Get("format") == "xlsx" {
		name := fmt.Sprintf("grup-%s.xlsx", stamp)
		w.Header().Set("Content-Type",
			"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
		w.Header().Set("Content-Disposition",
			mime.FormatMediaType("attachment", map[string]string{"filename": name}))
		if err := writeXLSX(w, "Grup", header, rows); err != nil {
			s.log.Error("write xlsx", "err", err)
		}
		return
	}

	name := fmt.Sprintf("grup-%s.csv", stamp)
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition",
		mime.FormatMediaType("attachment", map[string]string{"filename": name}))

	cw := csv.NewWriter(w)
	defer cw.Flush()
	_ = cw.Write(header)
	for _, row := range rows {
		_ = cw.Write(row)
	}
}

// --- a spreadsheet, without a spreadsheet library ----------------------------
//
// An .xlsx is a zip of XML parts, and the subset Excel, LibreOffice and Google
// Sheets all open is small enough to write by hand: a content-type map, two
// relationship files, a workbook and one sheet. Every cell is an inline string,
// which costs a little size and removes the shared-string table entirely.
//
// Done this way rather than by adding a dependency because the whole need is
// "one sheet, one header row, no formatting". A library would be several
// megabytes and a supply-chain decision for something the standard library
// already does.

type xlsxRow struct {
	XMLName xml.Name   `xml:"row"`
	R       int        `xml:"r,attr"`
	Cells   []xlsxCell `xml:"c"`
}

type xlsxCell struct {
	XMLName xml.Name `xml:"c"`
	R       string   `xml:"r,attr"`
	T       string   `xml:"t,attr"`
	IS      xlsxIS   `xml:"is"`
}

type xlsxIS struct {
	T string `xml:"t"`
}

func writeXLSX(w http.ResponseWriter, sheet string, header []string, rows [][]string) error {
	zw := zip.NewWriter(w)

	put := func(name, body string) error {
		f, err := zw.Create(name)
		if err != nil {
			return err
		}
		_, err = f.Write([]byte(body))
		return err
	}

	if err := put("[Content_Types].xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`+
		`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">`+
		`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>`+
		`<Default Extension="xml" ContentType="application/xml"/>`+
		`<Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/>`+
		`<Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>`+
		`</Types>`); err != nil {
		return err
	}

	if err := put("_rels/.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`+
		`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`+
		`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/>`+
		`</Relationships>`); err != nil {
		return err
	}

	if err := put("xl/workbook.xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`+
		`<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" `+
		`xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">`+
		`<sheets><sheet name="`+xmlEscape(sheet)+`" sheetId="1" r:id="rId1"/></sheets>`+
		`</workbook>`); err != nil {
		return err
	}

	if err := put("xl/_rels/workbook.xml.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`+
		`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`+
		`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/>`+
		`</Relationships>`); err != nil {
		return err
	}

	sheetFile, err := zw.Create("xl/worksheets/sheet1.xml")
	if err != nil {
		return err
	}
	if _, err := sheetFile.Write([]byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>`)); err != nil {
		return err
	}

	enc := xml.NewEncoder(sheetFile)
	write := func(index int, values []string) error {
		row := xlsxRow{R: index, Cells: make([]xlsxCell, 0, len(values))}
		for i, v := range values {
			row.Cells = append(row.Cells, xlsxCell{
				R:  fmt.Sprintf("%s%d", columnName(i), index),
				T:  "inlineStr",
				IS: xlsxIS{T: v},
			})
		}
		return enc.Encode(row)
	}

	if err := write(1, header); err != nil {
		return err
	}
	for i, row := range rows {
		if err := write(i+2, row); err != nil {
			return err
		}
	}
	if err := enc.Flush(); err != nil {
		return err
	}

	if _, err := sheetFile.Write([]byte(`</sheetData></worksheet>`)); err != nil {
		return err
	}
	return zw.Close()
}

// columnName turns 0 into "A" and 26 into "AA", which is how a spreadsheet
// names its columns and what every cell reference has to use.
func columnName(index int) string {
	name := ""
	for index >= 0 {
		name = string(rune('A'+index%26)) + name
		index = index/26 - 1
	}
	return name
}

func xmlEscape(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}
