package httpapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/salesan/omnichannel/backend/internal/analytics"
	"github.com/salesan/omnichannel/backend/internal/repository"
)

// GET /chats/export — every message in a period, one row each, as .xlsx.
//
// The file is built in a temporary file and only then sent. Streaming the zip
// straight into the response would save the disk write, but a failure halfway
// through would leave the browser holding a 200 and a truncated workbook that
// looks like a finished download. Built first, a failure is an ordinary error
// response and the half-written file is deleted; the download that does arrive
// carries a Content-Length, so a connection dropped in transit shows as a
// failed download rather than a short file.
//
// Memory stays flat either way: rows go from the database cursor into the zip
// one at a time, and the temporary file is copied out in fixed-size chunks.

// chatExportSlots is how many exports may run at once across the server.
//
// Each one holds a database connection for as long as its query streams, and
// the pool is shared with the inbox. Two is enough for two people exporting at
// the same moment; a third waits a few seconds and tries again, which costs far
// less than chat queueing behind a spreadsheet.
var chatExportSlots = make(chan struct{}, 2)

// chatExportMaxDays bounds one export. A year is more than any report here
// covers, and a bound means a typo in a date cannot ask for a decade.
const chatExportMaxDays = 366

// chatExportMaxAccounts bounds the number filter, which travels in the URL.
const chatExportMaxAccounts = 200

// chatExportColumns is the sheet's header, in order. chatExportCells builds
// rows in the same order; TestChatExportRowMatchesHeader holds them together.
var chatExportColumns = []sheetColumn{
	{Header: "Tanggal", Width: 12},
	{Header: "Jam", Width: 10},
	{Header: "Aplikasi", Width: 16},
	{Header: "Akun WhatsApp Bisnis", Width: 30},
	{Header: "Jenis Percakapan", Width: 17},
	{Header: "Nama Percakapan", Width: 30},
	{Header: "Nomor Customer/Pengirim", Width: 24},
	{Header: "Nama Pengirim", Width: 26},
	{Header: "Arah Pesan", Width: 12},
	{Header: "Keterangan Chat", Width: 70},
	{Header: "ID Grup", Width: 30},
}

// chatExportRequest is the validated filter, plus what the info sheet repeats.
type chatExportRequest struct {
	filter    repository.ChatExportFilter
	startDate string
	endDate   string
}

func (s *Server) handleExportChats(w http.ResponseWriter, r *http.Request) {
	sc, ok := s.scopeFor(w, r)
	if !ok {
		return
	}
	req, ok := parseChatExportRequest(w, r)
	if !ok {
		return
	}
	f := req.filter

	if f.ApplicationID != nil && !sc.CanSeeApplication(*f.ApplicationID) {
		writeError(w, http.StatusForbidden, "forbidden", "Aplikasi ini di luar wewenang akun Anda")
		return
	}

	// Every number asked for must exist, be visible, and sit in the chosen
	// application. Refused by name rather than dropped: an export that quietly
	// skipped one number would look complete and not be.
	accountNames := []string{}
	if len(f.AccountIDs) > 0 {
		found, err := s.repo.AccountsForExport(r.Context(), sc, f.AccountIDs)
		if err != nil {
			writeAppError(w, err)
			return
		}
		byID := make(map[uuid.UUID]repository.ExportAccountOption, len(found))
		for _, a := range found {
			byID[a.ID] = a
		}
		for _, id := range f.AccountIDs {
			a, ok := byID[id]
			if !ok {
				s.log.Warn("blocked out-of-scope chat export",
					"user_id", sc.UserID, "role", sc.Role, "account_id", id)
				writeError(w, http.StatusForbidden, "forbidden",
					"Salah satu nomor yang dipilih berada di luar wewenang akun Anda")
				return
			}
			if f.ApplicationID != nil && (a.ApplicationID == nil || *a.ApplicationID != *f.ApplicationID) {
				writeError(w, http.StatusBadRequest, "account_not_in_application",
					"Nomor "+a.Name+" tidak termasuk aplikasi yang dipilih")
				return
			}
			accountNames = append(accountNames, a.Name)
		}
	}

	select {
	case chatExportSlots <- struct{}{}:
		defer func() { <-chatExportSlots }()
	default:
		writeError(w, http.StatusTooManyRequests, "export_busy",
			"Server sedang menyiapkan ekspor lain. Coba lagi dalam beberapa detik.")
		return
	}

	tmp, err := os.CreateTemp("", "salesan-chat-export-*.xlsx")
	if err != nil {
		s.log.Error("chat export: temp file", "err", err)
		writeError(w, http.StatusInternalServerError, "export_failed", "Gagal menyiapkan berkas ekspor")
		return
	}
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
	}()

	sheet, err := newStyledSheetWriter(tmp, "Detail Chat", chatExportColumns)
	if err != nil {
		s.log.Error("chat export: open sheet", "err", err)
		writeError(w, http.StatusInternalServerError, "export_failed", "Gagal menyiapkan berkas ekspor")
		return
	}

	started := time.Now()
	appName := ""
	err = s.repo.ExportChatMessages(r.Context(), sc, f, func(row repository.ChatExportRow) error {
		if appName == "" && row.ApplicationName != nil {
			appName = *row.ApplicationName
		}
		return sheet.WriteRow(chatExportCells(row))
	})
	switch {
	case errors.Is(err, errSheetFull):
		writeError(w, http.StatusUnprocessableEntity, "too_many_rows",
			fmt.Sprintf("Lebih dari %s pesan pada filter ini — melebihi batas satu sheet Excel. "+
				"Persempit rentang tanggal atau pilih nomor tertentu.", formatThousands(excelMaxRows-1)))
		return
	case errors.Is(err, repository.ErrForbidden):
		writeAppError(w, err)
		return
	case errors.Is(err, context.DeadlineExceeded) || errors.Is(r.Context().Err(), context.DeadlineExceeded):
		writeError(w, http.StatusGatewayTimeout, "export_timeout",
			"Ekspor terlalu lama. Persempit rentang tanggal lalu coba lagi.")
		return
	case r.Context().Err() != nil:
		// The browser went away; nobody is waiting for an answer.
		return
	case err != nil:
		// Only the error and the size are logged. Message text and numbers
		// never go into the application log.
		s.log.Error("chat export: query", "err", err, "rows", sheet.Rows())
		writeError(w, http.StatusInternalServerError, "export_failed", "Gagal mengambil data chat")
		return
	}

	if sheet.Rows() == 0 {
		writeError(w, http.StatusNotFound, "no_data",
			"Tidak ada pesan yang cocok dengan filter ini pada periode tersebut.")
		return
	}

	if err := sheet.Close("Info Ekspor", chatExportInfo(req, appName, accountNames, sheet.Rows(), userEmail(r))); err != nil {
		s.log.Error("chat export: finish workbook", "err", err, "rows", sheet.Rows())
		writeError(w, http.StatusInternalServerError, "export_failed", "Gagal menyusun berkas Excel")
		return
	}
	size, err := tmp.Seek(0, io.SeekCurrent)
	if err == nil {
		_, err = tmp.Seek(0, io.SeekStart)
	}
	if err != nil {
		s.log.Error("chat export: rewind", "err", err)
		writeError(w, http.StatusInternalServerError, "export_failed", "Gagal menyusun berkas Excel")
		return
	}

	s.log.Info("chat export",
		"user_id", sc.UserID, "rows", sheet.Rows(), "bytes", size,
		"took_ms", time.Since(started).Milliseconds())

	name := fmt.Sprintf("Salesan_Export_Chat_%s_%s.xlsx", req.startDate, req.endDate)
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	w.Header().Set("Cache-Control", "private, no-store")
	// The row count, for the success message, without reopening the file.
	w.Header().Set("X-Export-Rows", strconv.Itoa(sheet.Rows()))
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, tmp)
}

// parseChatExportRequest reads and validates the query string.
//
// Dates arrive as WIB calendar dates and leave as instants, with the end made
// exclusive at the following midnight — so every message on the last day is
// included, up to 23:59:59.999, and none from the day after.
func parseChatExportRequest(w http.ResponseWriter, r *http.Request) (chatExportRequest, bool) {
	q := r.URL.Query()
	var req chatExportRequest

	if raw := strings.TrimSpace(q.Get("application_id")); raw != "" && raw != "all" {
		id, err := uuid.Parse(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_application", "Aplikasi tidak valid")
			return req, false
		}
		req.filter.ApplicationID = &id
	}

	if raw := strings.TrimSpace(q.Get("account_ids")); raw != "" {
		seen := map[uuid.UUID]bool{}
		for _, part := range strings.Split(raw, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			id, err := uuid.Parse(part)
			if err != nil {
				writeError(w, http.StatusBadRequest, "invalid_account", "Nomor WhatsApp tidak valid")
				return req, false
			}
			if !seen[id] {
				seen[id] = true
				req.filter.AccountIDs = append(req.filter.AccountIDs, id)
			}
		}
		if len(req.filter.AccountIDs) > chatExportMaxAccounts {
			writeError(w, http.StatusBadRequest, "too_many_accounts",
				fmt.Sprintf("Pilih paling banyak %d nomor sekaligus", chatExportMaxAccounts))
			return req, false
		}
	}

	switch t := strings.TrimSpace(q.Get("type")); t {
	case "", "all":
	case "personal", "group":
		req.filter.Type = t
	default:
		writeError(w, http.StatusBadRequest, "invalid_type", "Jenis percakapan harus all, personal, atau group")
		return req, false
	}

	start, ok := parseWIBDate(w, q.Get("start_date"), "Tanggal mulai")
	if !ok {
		return req, false
	}
	end, ok := parseWIBDate(w, q.Get("end_date"), "Tanggal akhir")
	if !ok {
		return req, false
	}
	if start.After(end) {
		writeError(w, http.StatusBadRequest, "invalid_range", "Tanggal mulai tidak boleh melebihi tanggal akhir")
		return req, false
	}
	// Both ends count, so 1–366 January is 366 days.
	if !end.Before(start.AddDate(0, 0, chatExportMaxDays)) {
		writeError(w, http.StatusBadRequest, "range_too_long",
			fmt.Sprintf("Rentang tanggal paling panjang %d hari", chatExportMaxDays))
		return req, false
	}

	req.filter.From = start
	// AddDate rather than 24 hours: the boundary is the next calendar midnight.
	req.filter.To = end.AddDate(0, 0, 1)
	req.startDate = start.Format("2006-01-02")
	req.endDate = end.Format("2006-01-02")
	return req, true
}

func parseWIBDate(w http.ResponseWriter, raw, label string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		writeError(w, http.StatusBadRequest, "missing_date", label+" wajib diisi")
		return time.Time{}, false
	}
	t, err := time.ParseInLocation("2006-01-02", raw, analytics.Jakarta)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_date", label+" harus berformat YYYY-MM-DD")
		return time.Time{}, false
	}
	return t, true
}

// chatExportCells turns one message into one row, in chatExportColumns order.
func chatExportCells(row repository.ChatExportRow) []sheetCell {
	local := row.Timestamp.In(analytics.Jakarta)
	date, clock := excelSerial(local)

	app := "Tanpa aplikasi"
	if row.ApplicationName != nil && *row.ApplicationName != "" {
		app = *row.ApplicationName
	}

	business := row.AccountName
	if row.AccountPhone != nil && *row.AccountPhone != "" {
		business = *row.AccountPhone + " (" + row.AccountName + ")"
	}

	isGroup := row.ConversationType == "group"
	kind, groupID := "Pribadi", ""
	if isGroup {
		kind, groupID = "Grup", row.ChatJID
	}

	direction := "Masuk"
	if row.FromMe {
		direction = "Keluar"
	}

	number, sender := exportParty(row)

	name := deref(row.ConversationName)
	if name == "" && !isGroup {
		// A personal chat with nobody's name saved is still about somebody:
		// say who by number, the way the inbox does.
		name = number
	}

	text := messageExportText(row)
	return []sheetCell{
		{Num: date, Style: styleDate},
		{Num: clock, Style: styleTime},
		textCell(app, quoted(app, styleText)),
		textCell(business, quoted(business, styleText)),
		textCell(kind, styleText),
		textCell(name, quoted(name, styleText)),
		textCell(number, quoted(number, styleText)),
		textCell(sender, quoted(sender, styleText)),
		textCell(direction, styleText),
		textCell(text, quoted(text, styleWrap)),
		textCell(groupID, styleText),
	}
}

// exportParty is the number and the name for the "who" columns.
//
// A personal chat is always about the customer, so its number is the
// counterpart's whichever way the message went. In a group the number is the
// author's: the member who wrote it, or our own number when we did.
//
// A number that cannot be resolved — WhatsApp disclosed only a LID — is never
// made up. The cell says the number is unavailable and quotes the LID, so the
// row can still be told apart from the others and nobody dials a LID.
func exportParty(row repository.ChatExportRow) (number, sender string) {
	if row.FromMe {
		sender = row.AccountName
		if row.ConversationType == "group" {
			return deref(row.AccountPhone), sender
		}
		return counterpartNumber(row), sender
	}

	if row.ConversationType != "group" {
		// The customer as saved in the address book first, then whatever name
		// arrived with the message.
		sender = deref(row.ConversationName)
		if sender == "" {
			sender = deref(row.SenderName)
		}
		return counterpartNumber(row), sender
	}
	sender = deref(row.SenderName)
	if p := deref(row.SenderPhone); p != "" {
		return p, sender
	}
	return unavailableNumber(deref(row.SenderJID)), sender
}

func counterpartNumber(row repository.ChatExportRow) string {
	if p := deref(row.CounterpartPhone); p != "" {
		return p
	}
	return unavailableNumber(row.ChatJID)
}

// unavailableNumber is what the number column says when WhatsApp gave us an
// identity but not a phone number.
func unavailableNumber(jid string) string {
	local, server, _ := strings.Cut(jid, "@")
	if local == "" {
		return "Nomor tidak tersedia"
	}
	if server == "lid" {
		return "Nomor tidak tersedia (LID " + local + ")"
	}
	return "Nomor tidak tersedia (" + jid + ")"
}

// mediaLabels names the message kinds that are not plain text, in the same
// words message_preview_text uses for the inbox.
var mediaLabels = map[string]string{
	"image":       "Foto",
	"video":       "Video",
	"audio":       "Pesan suara",
	"document":    "Dokumen",
	"sticker":     "Stiker",
	"location":    "Lokasi",
	"contact":     "Kontak",
	"poll":        "Polling",
	"unsupported": "Pesan tidak didukung",
}

// messageExportText is the "Keterangan Chat" cell: the message's own text,
// whole and with its line breaks, preceded by what kind of message it was when
// it was not plain text. A photo with a caption is "[Foto] caption", not just
// the caption, so a reader can tell the customer sent a picture.
func messageExportText(row repository.ChatExportRow) string {
	if row.Revoked {
		return "[Pesan ini dihapus]"
	}
	body, caption := deref(row.Body), deref(row.Caption)

	label, isMedia := mediaLabels[row.MessageType]
	if !isMedia {
		if body != "" {
			return body
		}
		return caption
	}
	if row.MessageType == "unsupported" {
		return "[" + label + "]"
	}

	// A document's body is its file name; the caption, if any, is the message.
	parts := []string{}
	for _, p := range []string{body, caption} {
		if strings.TrimSpace(p) != "" {
			parts = append(parts, p)
		}
	}
	if len(parts) == 0 {
		return "[" + label + "]"
	}
	return "[" + label + "] " + strings.Join(parts, "\n")
}

// chatExportInfo is the second sheet's content.
func chatExportInfo(
	req chatExportRequest, appName string, accountNames []string, rows int, by string,
) [][2]string {
	app := "Semua Aplikasi"
	if req.filter.ApplicationID != nil {
		app = appName
	}
	accounts := "Semua nomor WhatsApp bisnis"
	if len(accountNames) > 0 {
		accounts = strings.Join(accountNames, ", ")
	}
	kind := "Semua Percakapan (Pribadi dan Grup)"
	switch req.filter.Type {
	case "personal":
		kind = "Chat Pribadi"
	case "group":
		kind = "Chat Grup"
	}
	day := func(s string) string {
		t, _ := time.Parse("2006-01-02", s)
		return t.Format("02/01/2006")
	}
	info := [][2]string{
		{"Periode", day(req.startDate) + " – " + day(req.endDate)},
		{"Zona waktu", "WIB (Asia/Jakarta)"},
		{"Aplikasi", app},
		{"Nomor WhatsApp bisnis", accounts},
		{"Jenis percakapan", kind},
		{"Jumlah pesan", formatThousands(rows)},
		{"Diekspor pada", time.Now().In(analytics.Jakarta).Format("02/01/2006 15:04:05") + " WIB"},
	}
	if by != "" {
		info = append(info, [2]string{"Diekspor oleh", by})
	}
	return info
}

func userEmail(r *http.Request) string {
	if u := userFrom(r.Context()); u != nil {
		return u.Email
	}
	return ""
}

// formatThousands writes 12345 as "12.345", the Indonesian way.
func formatThousands(n int) string {
	s := strconv.Itoa(n)
	if len(s) <= 3 {
		return s
	}
	var b strings.Builder
	lead := len(s) % 3
	if lead > 0 {
		b.WriteString(s[:lead])
	}
	for i := lead; i < len(s); i += 3 {
		if b.Len() > 0 {
			b.WriteByte('.')
		}
		b.WriteString(s[i : i+3])
	}
	return b.String()
}
