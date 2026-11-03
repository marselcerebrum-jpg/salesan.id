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
// one batch at a time, and the temporary file is copied out in fixed-size
// chunks. There is no bound on the period. Rows past what one sheet can hold
// continue on the next sheet; the only bound is the request's time budget,
// and running out of it is an error, never a shorter file.

// chatExportSlots is how many exports may run at once across the server.
//
// Each one holds a database connection for as long as its query streams, and
// the pool is shared with the inbox. Two is enough for two people exporting at
// the same moment; a third waits a few seconds and tries again, which costs far
// less than chat queueing behind a spreadsheet.
var chatExportSlots = make(chan struct{}, 2)

// chatExportMaxAccounts bounds the number filter, which travels in the URL.
const chatExportMaxAccounts = 200

// colMessage is "Keterangan Chat", the one column that may continue into
// continuation columns.
const colMessage = 12

// chatExportColumns is the data sheet's header, in order. chatExportCells
// builds rows in the same order; TestChatExportRowMatchesHeader holds them
// together.
//
// The identity columns come after what a reader looks at: they are there to
// tell rows apart and to trace one back, not to be read. Every one is text, so
// an id never turns into a number or a date.
var chatExportColumns = []sheetColumn{
	{Header: "Tanggal", Width: 12},
	{Header: "Jam", Width: 10},
	{Header: "Aplikasi", Width: 16},
	{Header: "Akun WhatsApp Bisnis", Width: 30},
	{Header: "Jenis Percakapan", Width: 17},
	{Header: "Nama Percakapan", Width: 30},
	{Header: "Nomor Customer", Width: 20},
	{Header: "Nomor Pengirim", Width: 20},
	{Header: "Nama Pengirim", Width: 26},
	{Header: "Identitas Pengirim", Width: 32},
	{Header: "Arah Pesan", Width: 12},
	{Header: "Status Pesan", Width: 20},
	{Header: "Keterangan Chat", Width: 70},
	{Header: "ID Pesan", Width: 38},
	{Header: "ID Pesan WhatsApp", Width: 28},
	{Header: "ID Akun", Width: 38},
	{Header: "ID Percakapan", Width: 38},
	{Header: "JID Percakapan", Width: 32},
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

	started := time.Now()
	var sum repository.ChatExportSummary
	var sheet *styledSheetWriter
	appName := ""
	err = s.repo.ExportChatMessages(r.Context(), sc, f,
		func(got repository.ChatExportSummary) error {
			sum = got
			sheet = newChatExportSheet(tmp, got.MaxTextBytes)
			return nil
		},
		func(row repository.ChatExportRow) error {
			if appName == "" && row.ApplicationName != nil {
				appName = *row.ApplicationName
			}
			return sheet.WriteRow(chatExportCells(row))
		})
	written := 0
	if sheet != nil {
		written = sheet.Rows()
	}
	switch {
	case errors.Is(err, repository.ErrForbidden):
		writeAppError(w, err)
		return
	case errors.Is(err, context.DeadlineExceeded) || errors.Is(r.Context().Err(), context.DeadlineExceeded):
		s.log.Warn("chat export: out of time", "rows", written, "of", sum.Rows,
			"took_ms", time.Since(started).Milliseconds())
		writeError(w, http.StatusGatewayTimeout, "export_timeout",
			fmt.Sprintf("Ekspor %s pesan tidak selesai dalam batas waktu proses, jadi tidak ada berkas yang dibuat. "+
				"Pilih nomor tertentu atau bagi periodenya, lalu coba lagi.", formatThousands(sum.Rows)))
		return
	case r.Context().Err() != nil:
		// The browser went away; nobody is waiting for an answer.
		return
	case err != nil:
		// Only the error and the size are logged. Message text and numbers
		// never go into the application log.
		s.log.Error("chat export: query", "err", err, "rows", written)
		writeError(w, http.StatusInternalServerError, "export_failed", "Gagal mengambil data chat")
		return
	}

	if sum.Rows == 0 {
		msg := "Tidak ada pesan yang cocok dengan filter ini pada periode tersebut."
		if n := sum.Excluded.Total(); n > 0 {
			msg += fmt.Sprintf(" Ada %s pesan dari saluran, bot, broadcast, atau status, yang memang tidak termasuk ekspor.",
				formatThousands(n))
		}
		writeError(w, http.StatusNotFound, "no_data", msg)
		return
	}
	// The summary and the rows come from one snapshot, so they agree unless
	// something is wrong. A file holding fewer rows than it says would be
	// exactly the silent cut this export must never make.
	if written != sum.Rows {
		s.log.Error("chat export: row count mismatch", "rows", written, "expected", sum.Rows)
		writeError(w, http.StatusInternalServerError, "export_failed", "Gagal mengambil data chat")
		return
	}

	info := chatExportInfo(req, appName, accountNames, sum, sheet.Sheets(), userEmail(r))
	if err := sheet.Close("Info Ekspor", info); err != nil {
		s.log.Error("chat export: finish workbook", "err", err, "rows", written)
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
		"user_id", sc.UserID, "rows", written, "sheets", sheet.Sheets(), "bytes", size,
		"excluded", sum.Excluded.Total(), "took_ms", time.Since(started).Milliseconds())

	name := fmt.Sprintf("Salesan_Export_Chat_%s_%s.xlsx", req.startDate, req.endDate)
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	w.Header().Set("Cache-Control", "private, no-store")
	// The row count, for the success message, without reopening the file.
	w.Header().Set("X-Export-Rows", strconv.Itoa(written))
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, tmp)
}

// newChatExportSheet lays the workbook out for messages up to maxTextBytes
// long: the named columns, then as many continuation columns as the longest
// message needs — none, usually.
func newChatExportSheet(w io.Writer, maxTextBytes int) *styledSheetWriter {
	msg := chatExportColumns[colMessage]
	cols := append([]sheetColumn{}, chatExportColumns...)
	cols = append(cols, continuationColumns(msg.Header, maxTextBytes, msg.Width)...)
	return newStyledSheetWriter(w, "Detail Chat", cols, colMessage, len(chatExportColumns))
}

// parseChatExportRequest reads and validates the query string.
//
// Dates arrive as WIB calendar dates and leave as instants, with the end made
// exclusive at the following midnight — so every message on the last day is
// included, up to 23:59:59.999, and none from the day after. Any real calendar
// date is accepted, at any distance from the other: a period that reaches
// past the messages held simply has fewer rows, and the info sheet says where
// the messages actually begin and end.
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

	req.filter.From = start
	// AddDate rather than 24 hours: the boundary is the next calendar midnight.
	req.filter.To = end.AddDate(0, 0, 1)
	req.startDate = start.Format("2006-01-02")
	req.endDate = end.Format("2006-01-02")
	return req, true
}

// parseWIBDate reads one YYYY-MM-DD date as midnight WIB. A date that does not
// exist — 2026-02-30, month 13 — is refused, not rolled into the next month.
func parseWIBDate(w http.ResponseWriter, raw, label string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		writeError(w, http.StatusBadRequest, "missing_date", label+" wajib diisi")
		return time.Time{}, false
	}
	t, err := time.ParseInLocation("2006-01-02", raw, analytics.Jakarta)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_date", label+" harus tanggal yang valid (YYYY-MM-DD)")
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
	kind := "Pribadi"
	if isGroup {
		kind = "Grup"
	}

	direction := "Masuk"
	if row.FromMe {
		direction = "Keluar"
	}

	p := exportParty(row)

	name := deref(row.ConversationName)
	if name == "" && !isGroup {
		// A personal chat with nobody's name saved is still about somebody:
		// say who by number, the way the inbox does.
		name = deref(row.CounterpartPhone)
	}

	text := messageExportText(row)
	plain := func(s string) sheetCell { return textCell(s, quoted(s, styleText)) }
	return []sheetCell{
		{Num: date, Style: styleDate},
		{Num: clock, Style: styleTime},
		plain(app),
		plain(business),
		plain(kind),
		plain(name),
		plain(p.customerNumber),
		plain(p.senderNumber),
		plain(p.senderName),
		plain(p.senderIdentity),
		plain(direction),
		plain(messageStatusLabel(row.Status)),
		textCell(text, quoted(text, styleWrap)),
		plain(row.MessageID.String()),
		plain(row.WAMessageID),
		plain(row.AccountID.String()),
		plain(row.ConversationID.String()),
		plain(row.ChatJID),
	}
}

// exportParties is who a row is about and who wrote it.
type exportParties struct {
	// customerNumber is the customer of a personal chat, whichever way the
	// message went. Empty in a group, which has no one customer.
	customerNumber string
	// senderNumber, senderName and senderIdentity are the author: the
	// customer or group member on an incoming message, our own number on an
	// outgoing one. senderIdentity is the WhatsApp address itself — a phone
	// address or a LID — which still tells two authors apart when neither
	// number is known.
	senderNumber   string
	senderName     string
	senderIdentity string
}

// exportParty fills the "who" columns.
//
// A number that cannot be resolved — WhatsApp disclosed only a LID — is never
// made up. The number column says it is unavailable and the identity column
// carries the LID, so the row can still be told apart from the others and
// nobody dials a LID.
func exportParty(row repository.ChatExportRow) exportParties {
	var p exportParties
	isGroup := row.ConversationType == "group"
	if !isGroup {
		p.customerNumber = orUnavailable(deref(row.CounterpartPhone))
	}

	if row.FromMe {
		p.senderNumber = orUnavailable(deref(row.AccountPhone))
		p.senderName = row.AccountName
		p.senderIdentity = bareJID(deref(row.AccountJID))
		return p
	}

	if !isGroup {
		p.senderNumber = p.customerNumber
		// The customer as saved in the address book first, then whatever name
		// arrived with the message.
		p.senderName = deref(row.ConversationName)
		if p.senderName == "" {
			p.senderName = deref(row.SenderName)
		}
		p.senderIdentity = bareJID(deref(row.SenderJID))
		if p.senderIdentity == "" {
			p.senderIdentity = row.ChatJID
		}
		return p
	}

	p.senderNumber = orUnavailable(deref(row.SenderPhone))
	p.senderName = deref(row.SenderName)
	p.senderIdentity = bareJID(deref(row.SenderJID))
	return p
}

// orUnavailable is what a number column says when WhatsApp gave us an
// identity but not a phone number.
func orUnavailable(phone string) string {
	if phone == "" {
		return "Nomor tidak tersedia"
	}
	return phone
}

// bareJID drops the device part of an address: "628…:12@s.whatsapp.net" is
// the same person as "628…@s.whatsapp.net", on one of their devices.
func bareJID(jid string) string {
	user, server, ok := strings.Cut(jid, "@")
	if !ok {
		return jid
	}
	if i := strings.IndexByte(user, ':'); i >= 0 {
		user = user[:i]
	}
	return user + "@" + server
}

// messageStatusLabels are the inbox's own words for a message's status
// (MESSAGE_STATUS_LABEL in the web app).
var messageStatusLabels = map[string]string{
	"pending":   "Mengirim",
	"sent":      "Terkirim",
	"delivered": "Diterima",
	"read":      "Dibaca",
	"failed":    "Gagal",
}

// messageStatusLabel is "Dibaca (read)": the word a reader knows, and the
// stored value beside it so the column can be matched against the database.
func messageStatusLabel(status string) string {
	if label, ok := messageStatusLabels[status]; ok {
		return label + " (" + status + ")"
	}
	return status
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

// chatExportInfo is the info sheet's content.
//
// Besides the filter it records where the messages actually begin and end. A
// period reaching back before the oldest message held is not an error, but a
// reader comparing the filter with the rows deserves to know the gap is real:
// Salesan holds messages from when a number was linked, plus the short history
// WhatsApp hands over at linking, and nothing older.
func chatExportInfo(
	req chatExportRequest, appName string, accountNames []string,
	sum repository.ChatExportSummary, sheets int, by string,
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
	moment := func(t *time.Time) string {
		if t == nil {
			return "-"
		}
		return t.In(analytics.Jakarta).Format("02/01/2006 15:04:05") + " WIB"
	}

	info := [][2]string{
		{"Periode filter", day(req.startDate) + " – " + day(req.endDate)},
		{"Zona waktu", "WIB (Asia/Jakarta)"},
		{"Aplikasi", app},
		{"Nomor WhatsApp bisnis", accounts},
		{"Jenis percakapan", kind},
		{"Jumlah pesan", formatThousands(sum.Rows)},
		{"Pesan pertama dalam file", moment(sum.First)},
		{"Pesan terakhir dalam file", moment(sum.Last)},
	}
	if sum.First != nil && sum.First.In(analytics.Jakarta).Format("2006-01-02") > req.startDate {
		info = append(info, [2]string{"Catatan cakupan",
			"Tidak ada pesan yang cocok sebelum " + moment(sum.First) + ". Salesan hanya menyimpan pesan " +
				"sejak nomor ditautkan, ditambah riwayat singkat yang dikirim WhatsApp saat penautan; " +
				"pesan yang lebih lama tidak tersedia di sistem."})
	}
	if sheets > 1 {
		info = append(info, [2]string{"Sheet data",
			fmt.Sprintf("%d sheet (Detail Chat, Detail Chat 2, …), masing-masing paling banyak %s baris",
				sheets, formatThousands(excelMaxRows-1))})
	}
	info = append(info,
		[2]string{"Tidak termasuk", excludedSummary(sum.Excluded)},
		[2]string{"Satu baris", "Satu pesan, dikenali dari ID Pesan, yang muncul sekali dalam file. " +
			"Pesan grup yang diterima beberapa nomor bisnis tercatat sekali per nomor: " +
			"ID Pesan WhatsApp dan JID Percakapan sama, ID Akun berbeda."},
		[2]string{"Diekspor pada", time.Now().In(analytics.Jakarta).Format("02/01/2006 15:04:05") + " WIB"},
	)
	if by != "" {
		info = append(info, [2]string{"Diekspor oleh", by})
	}
	return info
}

// excludedSummary says what the export left out on purpose, and how much.
func excludedSummary(e repository.ExcludedCounts) string {
	parts := []string{}
	for _, p := range []struct {
		n    int
		what string
	}{
		{e.Newsletter, "saluran (channel)"},
		{e.Bot, "bot"},
		{e.Broadcast, "daftar broadcast"},
		{e.Status, "status"},
		{e.Other, "alamat lain"},
	} {
		if p.n > 0 {
			parts = append(parts, formatThousands(p.n)+" pesan "+p.what)
		}
	}
	if len(parts) == 0 {
		return "Tidak ada. Semua pesan pada filter ini berasal dari chat pribadi atau grup."
	}
	return strings.Join(parts, ", ") + " — bukan chat pribadi atau grup, jadi tidak diekspor."
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
