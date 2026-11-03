package httpapi

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"io"
	"net/http/httptest"
	"os"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/salesan/omnichannel/backend/internal/analytics"
	"github.com/salesan/omnichannel/backend/internal/repository"
)

func sp(s string) *string { return &s }

// sampleRows covers each shape a row can take: personal in and out, a group
// member with a number, one known only by LID, our own group reply, media,
// a revoked message, and text that would be a formula or an escape sequence.
func sampleRows() []repository.ChatExportRow {
	at := time.Date(2026, 11, 3, 9, 15, 30, 0, analytics.Jakarta)
	base := repository.ChatExportRow{
		AccountID:       uuid.MustParse("11111111-1111-4111-8111-111111111111"),
		ConversationID:  uuid.MustParse("22222222-2222-4222-8222-222222222222"),
		ApplicationName: sp("JADIASN"), AccountName: "HP UTAMA", AccountPhone: sp("6281200000001"),
		AccountJID:       sp("6281200000001:7@s.whatsapp.net"),
		ConversationType: "personal", ChatJID: "628123456789@s.whatsapp.net",
		ConversationName: sp("Rina"), CounterpartPhone: sp("628123456789"), MessageType: "text",
		Status: "read",
	}
	group := uuid.MustParse("33333333-3333-4333-8333-333333333333")
	n := 0
	row := func(mut func(*repository.ChatExportRow)) repository.ChatExportRow {
		r := base
		n++
		r.MessageID = uuid.New()
		r.WAMessageID = "3EB0" + strings.Repeat("0", 14) + string(rune('A'+n))
		r.Timestamp = at
		mut(&r)
		return r
	}
	inGroup := func(r *repository.ChatExportRow) {
		r.ConversationType, r.ChatJID, r.ConversationName = "group", "120363012345@g.us", sp("Grup LC")
		r.ConversationID = group
		r.CounterpartPhone = nil
	}
	return []repository.ChatExportRow{
		row(func(r *repository.ChatExportRow) {
			r.Body = sp("Kak, mau tanya paket\nyang 3 bulan")
			r.SenderJID, r.SenderName = sp("628123456789@s.whatsapp.net"), sp("Rina")
			r.Status = "delivered"
		}),
		row(func(r *repository.ChatExportRow) { r.FromMe = true; r.Body = sp("Silakan Kak, mau tanya apa?") }),
		row(func(r *repository.ChatExportRow) {
			inGroup(r)
			r.SenderJID, r.SenderPhone, r.SenderName = sp("628124000000@s.whatsapp.net"), sp("628124000000"), sp("Andi")
			r.Body = sp("Min, jadwal LC kapan?")
		}),
		row(func(r *repository.ChatExportRow) {
			inGroup(r)
			r.SenderJID, r.SenderName = sp("16801234567890@lid"), sp("Budi")
			r.Body = sp("=HYPERLINK(\"http://evil\",\"klik\")")
		}),
		row(func(r *repository.ChatExportRow) {
			inGroup(r)
			r.FromMe = true
			r.Body = sp("Besok jam 19.00")
		}),
		row(func(r *repository.ChatExportRow) { r.MessageType = "image"; r.Caption = sp("bukti transfer") }),
		row(func(r *repository.ChatExportRow) { r.Revoked = true }),
		row(func(r *repository.ChatExportRow) { r.Body = sp("+62 812 & <b>tebal</b> _x0041_ 😀") }),
	}
}

func longestText(rows []repository.ChatExportRow) int {
	max := 0
	for _, r := range rows {
		if n := len(deref(r.Body)) + len(deref(r.Caption)); n > max {
			max = n
		}
	}
	return max
}

// buildWorkbook lays the workbook out the way the handler does — from the
// longest message — and returns its parts. perSheet, when positive, shrinks
// the sheet so splitting can be seen without a million rows.
func buildWorkbook(t *testing.T, rows []repository.ChatExportRow, perSheet int) map[string]string {
	t.Helper()
	var buf bytes.Buffer
	sw := newChatExportSheet(&buf, longestText(rows))
	if perSheet > 0 {
		sw.perSheet = perSheet
	}
	for _, r := range rows {
		if err := sw.WriteRow(chatExportCells(r)); err != nil {
			t.Fatal(err)
		}
	}
	first, last := rows[0].Timestamp, rows[len(rows)-1].Timestamp
	sum := repository.ChatExportSummary{Rows: sw.Rows(), First: &first, Last: &last}
	req := chatExportRequest{startDate: "2026-11-01", endDate: "2026-11-03"}
	if err := sw.Close("Info Ekspor", chatExportInfo(req, "", nil, sum, sw.Sheets(), "a@b.c")); err != nil {
		t.Fatal(err)
	}

	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("not a zip: %v", err)
	}
	parts := map[string]string{}
	for _, f := range zr.File {
		rc, _ := f.Open()
		raw, _ := io.ReadAll(rc)
		_ = rc.Close()
		parts[f.Name] = string(raw)
		if err := xml.Unmarshal(raw, new(struct{ XMLName xml.Name })); err != nil {
			t.Errorf("%s is not well-formed XML: %v", f.Name, err)
		}
	}
	return parts
}

// sheetCells reads a worksheet back into rows of (text, style) by column.
type readCell struct{ Text, Style, Value string }

func sheetCells(t *testing.T, sheet string) [][]readCell {
	t.Helper()
	var ws struct {
		Rows []struct {
			Cells []struct {
				R     string `xml:"r,attr"`
				S     string `xml:"s,attr"`
				V     string `xml:"v"`
				Inner struct {
					T string `xml:"t"`
				} `xml:"is"`
			} `xml:"c"`
		} `xml:"sheetData>row"`
	}
	if err := xml.Unmarshal([]byte(sheet), &ws); err != nil {
		t.Fatal(err)
	}
	out := make([][]readCell, len(ws.Rows))
	for i, r := range ws.Rows {
		for _, c := range r.Cells {
			col := 0
			for _, ch := range strings.TrimRight(c.R, "0123456789") {
				col = col*26 + int(ch-'A'+1)
			}
			for len(out[i]) < col {
				out[i] = append(out[i], readCell{})
			}
			out[i][col-1] = readCell{Text: c.Inner.T, Style: c.S, Value: c.V}
		}
	}
	return out
}

// sheetReader looks cells up by header name, so a test reads like the sheet.
type sheetReader struct {
	t      *testing.T
	rows   [][]readCell
	header map[string]int
}

func readSheet(t *testing.T, sheet string) sheetReader {
	t.Helper()
	rows := sheetCells(t, sheet)
	h := map[string]int{}
	for i, c := range rows[0] {
		h[c.Text] = i
	}
	return sheetReader{t: t, rows: rows, header: h}
}

func (s sheetReader) cell(row int, name string) readCell {
	s.t.Helper()
	i, ok := s.header[name]
	if !ok {
		s.t.Fatalf("no column %q", name)
	}
	if r := s.rows[row]; i < len(r) {
		return r[i]
	}
	return readCell{}
}

func TestChatExportRowMatchesHeader(t *testing.T) {
	for i, r := range sampleRows() {
		if got := len(chatExportCells(r)); got != len(chatExportColumns) {
			t.Errorf("row %d has %d cells, header has %d", i, got, len(chatExportColumns))
		}
	}
	if chatExportColumns[colMessage].Header != "Keterangan Chat" {
		t.Errorf("colMessage points at %q", chatExportColumns[colMessage].Header)
	}
}

func TestChatExportWorkbook(t *testing.T) {
	parts := buildWorkbook(t, sampleRows(), 0)

	for _, want := range []string{"[Content_Types].xml", "_rels/.rels", "xl/workbook.xml",
		"xl/_rels/workbook.xml.rels", "xl/styles.xml", "xl/worksheets/sheet1.xml", "xl/worksheets/sheet2.xml"} {
		if _, ok := parts[want]; !ok {
			t.Fatalf("missing part %s", want)
		}
	}
	book := parts["xl/workbook.xml"]
	if !strings.Contains(book, `name="Detail Chat"`) || !strings.Contains(book, `name="Info Ekspor"`) {
		t.Errorf("sheet names wrong: %s", book)
	}
	// 13 named columns: A to M, and no continuation column for short text.
	if !strings.Contains(book, `&#39;Detail Chat&#39;!$A$1:$M$9`) {
		t.Errorf("filter defined name does not cover the rows and columns: %s", book)
	}

	sheet := parts["xl/worksheets/sheet1.xml"]
	for _, want := range []string{`state="frozen"`, `ySplit="1"`, `<autoFilter ref="A1:M9"/>`, `width="70"`} {
		if !strings.Contains(sheet, want) {
			t.Errorf("sheet lacks %s", want)
		}
	}

	s := readSheet(t, sheet)
	if len(s.rows) != 9 {
		t.Fatalf("%d rows, want header + 8", len(s.rows))
	}
	for i, c := range s.rows[0] {
		if c.Text != chatExportColumns[i].Header || c.Style != "1" {
			t.Errorf("header %d = %q style %s", i, c.Text, c.Style)
		}
	}

	// Date and time are real serials in WIB: 3 Nov 2026 is day 46329.
	if c := s.cell(1, "Tanggal"); c.Value != "46329" || c.Style != "2" {
		t.Errorf("date = %+v", c)
	}
	if c := s.cell(1, "Jam"); !strings.HasPrefix(c.Value, "0.3857") || c.Style != "3" {
		t.Errorf("time = %+v, want 09:15:30 as 0.3857…", c)
	}

	// Line breaks survive.
	if c := s.cell(1, "Keterangan Chat"); c.Text != "Kak, mau tanya paket\nyang 3 bulan" || c.Style != "6" {
		t.Errorf("multi-line text = %q style %s", c.Text, c.Style)
	}

	for _, gone := range []string{"ID Pesan", "ID Pesan WhatsApp", "ID Akun", "ID Percakapan", "JID Percakapan"} {
		if _, ok := s.header[gone]; ok {
			t.Errorf("column %q is still in the sheet", gone)
		}
	}
	if c := s.cell(1, "Status Pesan"); c.Text != "Diterima (delivered)" {
		t.Errorf("status = %q", c.Text)
	}
	if c := s.cell(2, "Status Pesan"); c.Text != "Dibaca (read)" {
		t.Errorf("status = %q", c.Text)
	}

	// Personal incoming: the customer is the customer and the author.
	if s.cell(1, "Nomor Customer").Text != "628123456789" || s.cell(1, "Nomor Pengirim").Text != "628123456789" ||
		s.cell(1, "Identitas Pengirim").Text != "628123456789@s.whatsapp.net" {
		t.Errorf("personal incoming parties = %+v", s.rows[1])
	}
	if c := s.cell(1, "Nomor Customer"); c.Value != "" {
		t.Errorf("a number became a numeric cell: %+v", c)
	}
	// Personal outgoing: still the customer's chat, written by our number,
	// whose identity loses its device part.
	if s.cell(2, "Nomor Customer").Text != "628123456789" || s.cell(2, "Nomor Pengirim").Text != "6281200000001" ||
		s.cell(2, "Nama Pengirim").Text != "HP UTAMA" || s.cell(2, "Identitas Pengirim").Text != "6281200000001@s.whatsapp.net" ||
		s.cell(2, "Arah Pesan").Text != "Keluar" {
		t.Errorf("personal outgoing parties = %+v", s.rows[2])
	}
	// Group member with a number: no customer, the member as author.
	if s.cell(3, "Nomor Customer").Text != "" || s.cell(3, "Nomor Pengirim").Text != "628124000000" ||
		s.cell(3, "Jenis Percakapan").Text != "Grup" ||
		s.cell(3, "Nama Pengirim").Text != "Andi" {
		t.Errorf("group member row = %+v", s.rows[3])
	}
	// LID only: no number made up; the LID is the identity, kept whole.
	if s.cell(4, "Nomor Pengirim").Text != "Nomor tidak tersedia" || s.cell(4, "Identitas Pengirim").Text != "16801234567890@lid" {
		t.Errorf("LID row = %+v", s.rows[4])
	}
	// Formula text is kept exactly, as a quote-prefixed inline string.
	if c := s.cell(4, "Keterangan Chat"); c.Text != `=HYPERLINK("http://evil","klik")` || c.Style != "7" {
		t.Errorf("formula cell = %+v", c)
	}
	if strings.Contains(sheet, "<f>") {
		t.Error("the sheet contains a formula element")
	}
	// Our own group reply carries our number.
	if c := s.cell(5, "Nomor Pengirim"); c.Text != "6281200000001" {
		t.Errorf("group outgoing number = %q", c.Text)
	}
	if c := s.cell(6, "Keterangan Chat"); c.Text != "[Foto] bukti transfer" {
		t.Errorf("media = %q", c.Text)
	}
	if c := s.cell(7, "Keterangan Chat"); c.Text != "[Pesan ini dihapus]" {
		t.Errorf("revoked = %q", c.Text)
	}
	// Special characters: escaped for XML, Excel's own escape neutralised.
	if c := s.cell(8, "Keterangan Chat"); c.Text != "+62 812 & <b>tebal</b> _x005F_x0041_ 😀" || c.Style != "7" {
		t.Errorf("special text = %+v", c)
	}

	info := parts["xl/worksheets/sheet2.xml"]
	for _, want := range []string{"Pesan pertama dalam file", "03/11/2026 09:15:30 WIB", "Pesan terakhir dalam file"} {
		if !strings.Contains(info, want) {
			t.Errorf("info sheet lacks %q", want)
		}
	}
}

// The reported file: nine long messages continued into column L with no
// header, outside the A:K filter. Now each continuation has a header, sits in
// the same place on every row, is wrapped, and is inside the filter, so a sort
// carries every piece with its message.
func TestLongMessageContinuesUnderAHeader(t *testing.T) {
	rows := sampleRows()
	long := strings.Repeat("a", excelMaxCellChars) + strings.Repeat("😀", 10)
	longer := strings.Repeat("b", 2*excelMaxCellChars+5)
	rows[0].Body = &long
	rows[2].Body = &longer
	parts := buildWorkbook(t, rows, 0)
	sheet := parts["xl/worksheets/sheet1.xml"]

	// The longest needs three cells, so two continuation columns, N and O,
	// right beside the message.
	s := readSheet(t, sheet)
	if got := len(s.rows[0]); got != len(chatExportColumns)+2 {
		t.Fatalf("header has %d columns, want %d", got, len(chatExportColumns)+2)
	}
	for i, want := range []string{"Keterangan Chat (lanjutan 1)", "Keterangan Chat (lanjutan 2)"} {
		if c := s.rows[0][len(chatExportColumns)+i]; c.Text != want || c.Style != "1" {
			t.Errorf("continuation header %d = %+v", i, c)
		}
	}
	if !strings.Contains(sheet, `<autoFilter ref="A1:O9"/>`) {
		t.Error("the filter does not cover the continuation columns")
	}
	if !strings.Contains(parts["xl/workbook.xml"], `!$A$1:$O$9`) {
		t.Error("the filter defined name does not cover the continuation columns")
	}
	if !strings.Contains(sheet, `<col min="14" max="14" width="70" customWidth="1" style="6"/>`) {
		t.Error("continuation columns are not wrapped text at the message width")
	}

	whole := func(row int) string {
		got := s.cell(row, "Keterangan Chat").Text
		for _, h := range []string{"Keterangan Chat (lanjutan 1)", "Keterangan Chat (lanjutan 2)"} {
			got += s.cell(row, h).Text
		}
		return got
	}
	if got := whole(1); got != long {
		t.Errorf("long text was altered: %d → %d bytes", len(long), len(got))
	}
	if got := whole(3); got != longer {
		t.Errorf("longer text was altered: %d → %d bytes", len(longer), len(got))
	}
	// Short rows simply leave the continuation columns empty.
	if s.cell(2, "Keterangan Chat (lanjutan 1)").Text != "" {
		t.Error("a short message has a continuation")
	}
}

// A layout too small for a message fails the export; it never cuts the text.
func TestTooLongForLayoutFails(t *testing.T) {
	r := sampleRows()[0]
	long := strings.Repeat("a", excelMaxCellChars+1)
	r.Body = &long
	sw := newChatExportSheet(io.Discard, 10)
	if err := sw.WriteRow(chatExportCells(r)); err != errCellTooLong {
		t.Fatalf("err = %v, want errCellTooLong", err)
	}
}

func TestContinuationColumns(t *testing.T) {
	for _, c := range []struct{ bytes, want int }{
		{0, 0}, {1000, 0}, {excelMaxCellChars - 100, 0},
		{excelMaxCellChars, 1}, {65536, 2}, {4 * 65536, 8},
	} {
		if got := len(continuationColumns("X", c.bytes, 70)); got != c.want {
			t.Errorf("%d bytes → %d columns, want %d", c.bytes, got, c.want)
		}
	}
	// The bound holds for the worst case splitForCell can produce: text whose
	// pieces each come out one unit short because of surrogate pairs.
	worst := strings.Repeat("a", excelMaxCellChars-1) + strings.Repeat("😀", excelMaxCellChars)
	need := len(splitForCell(worst)) - 1
	if got := len(continuationColumns("X", len(worst), 70)); got < need {
		t.Errorf("%d bytes laid out %d continuations, needs %d", len(worst), got, need)
	}
}

// More rows than a sheet holds continue on the next sheet, each with its own
// header and filter, and the info sheet comes last.
func TestRowsSplitAcrossSheets(t *testing.T) {
	var rows []repository.ChatExportRow
	for i := 0; i < 3; i++ {
		rows = append(rows, sampleRows()...)
	}
	for i := range rows {
		rows[i].MessageID = uuid.New()
		body := "pesan " + strconv.Itoa(i)
		rows[i].Body, rows[i].Revoked, rows[i].MessageType = &body, false, "text"
	}
	parts := buildWorkbook(t, rows, 10) // 24 rows → 10 + 10 + 4

	book := parts["xl/workbook.xml"]
	for i, name := range []string{"Detail Chat", "Detail Chat 2", "Detail Chat 3", "Info Ekspor"} {
		if !strings.Contains(book, `name="`+name+`" sheetId="`+string(rune('1'+i))+`"`) {
			t.Errorf("sheet %d is not %q: %s", i+1, name, book)
		}
	}
	for i, want := range []string{"Detail Chat&#39;!$A$1:$M$11", "Detail Chat 2&#39;!$A$1:$M$11", "Detail Chat 3&#39;!$A$1:$M$5"} {
		if !strings.Contains(book, `localSheetId="`+string(rune('0'+i))+`" hidden="1">&#39;`+want) {
			t.Errorf("sheet %d filter name missing (%s): %s", i+1, want, book)
		}
	}
	if !strings.Contains(parts["xl/worksheets/sheet4.xml"], "Sheet data") {
		t.Error("info sheet does not mention the split")
	}

	seen := map[string]int{}
	for i, part := range []string{"sheet1", "sheet2", "sheet3"} {
		s := readSheet(t, parts["xl/worksheets/"+part+".xml"])
		if s.rows[0][0].Text != "Tanggal" {
			t.Errorf("%s has no header", part)
		}
		want := []int{10, 10, 4}[i]
		if len(s.rows)-1 != want {
			t.Errorf("%s holds %d rows, want %d", part, len(s.rows)-1, want)
		}
		for r := 1; r < len(s.rows); r++ {
			seen[s.cell(r, "Keterangan Chat").Text]++
		}
	}
	if len(seen) != len(rows) {
		t.Errorf("%d distinct messages across sheets, want %d", len(seen), len(rows))
	}
	for id, n := range seen {
		if n != 1 {
			t.Errorf("message %s appears %d times", id, n)
		}
	}
}

func TestNeedsQuotePrefix(t *testing.T) {
	for _, s := range []string{"=1+1", "+628", "-5", "@SUM(A1)", "\tx", "＝1"} {
		if !needsQuotePrefix(s) {
			t.Errorf("%q should be quoted", s)
		}
	}
	for _, s := range []string{"", "Halo", "628123", " =1"} {
		if needsQuotePrefix(s) {
			t.Errorf("%q should not be quoted", s)
		}
	}
}

func TestParseChatExportRequest(t *testing.T) {
	id := uuid.New()
	cases := []struct {
		query string
		ok    bool
		code  string
	}{
		{"start_date=2026-11-01&end_date=2026-11-03", true, ""},
		{"start_date=2026-11-03&end_date=2026-11-03&type=group&application_id=" + id.String(), true, ""},
		{"start_date=2026-11-01&end_date=2026-11-03&account_ids=" + id.String() + "," + id.String(), true, ""},
		// No bound on the length of the period: the reported 28 Jan – 3 Nov,
		// a year and more, ten years.
		{"start_date=2026-01-28&end_date=2026-11-03", true, ""},
		{"start_date=2025-01-01&end_date=2026-11-03", true, ""},
		{"start_date=2016-01-01&end_date=2026-11-03", true, ""},
		{"end_date=2026-11-03", false, "missing_date"},
		{"start_date=03/11/2026&end_date=2026-11-03", false, "invalid_date"},
		{"start_date=2026-02-30&end_date=2026-11-03", false, "invalid_date"},
		{"start_date=2026-13-01&end_date=2026-11-03", false, "invalid_date"},
		{"start_date=2026-11-04&end_date=2026-11-03", false, "invalid_range"},
		{"start_date=2026-11-01&end_date=2026-11-03&type=status", false, "invalid_type"},
		{"start_date=2026-11-01&end_date=2026-11-03&application_id=x", false, "invalid_application"},
		{"start_date=2026-11-01&end_date=2026-11-03&account_ids=x", false, "invalid_account"},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		req, ok := parseChatExportRequest(rec, httptest.NewRequest("GET", "/chats/export?"+c.query, nil))
		if ok != c.ok {
			t.Errorf("%s: ok = %v, want %v (%s)", c.query, ok, c.ok, rec.Body.String())
			continue
		}
		if !ok && !strings.Contains(rec.Body.String(), `"`+c.code+`"`) {
			t.Errorf("%s: body %s, want code %s", c.query, rec.Body.String(), c.code)
		}
		if ok && strings.Contains(c.query, "account_ids") && len(req.filter.AccountIDs) != 1 {
			t.Errorf("duplicate account ids were not collapsed: %v", req.filter.AccountIDs)
		}
	}

	// The whole of the last day is inside, and nothing after it.
	rec := httptest.NewRecorder()
	req, _ := parseChatExportRequest(rec, httptest.NewRequest("GET",
		"/chats/export?start_date=2026-11-01&end_date=2026-11-03", nil))
	if want := time.Date(2026, 11, 1, 0, 0, 0, 0, analytics.Jakarta); !req.filter.From.Equal(want) {
		t.Errorf("from = %s, want %s", req.filter.From, want)
	}
	if want := time.Date(2026, 11, 4, 0, 0, 0, 0, analytics.Jakarta); !req.filter.To.Equal(want) {
		t.Errorf("to = %s, want %s (exclusive)", req.filter.To, want)
	}
	if got := req.filter.From.UTC().Format(time.RFC3339); got != "2026-10-31T17:00:00Z" {
		t.Errorf("WIB midnight in UTC = %s", got)
	}
}

// The reported gap: a filter from 28 January, messages only from 15
// September. The info sheet gives the real first and last message and says why
// nothing earlier is there, and it lists what was left out.
func TestInfoExplainsCoverage(t *testing.T) {
	first := time.Date(2026, 9, 15, 8, 53, 5, 0, analytics.Jakarta)
	last := time.Date(2026, 11, 3, 13, 0, 0, 0, analytics.Jakarta)
	req := chatExportRequest{startDate: "2026-01-28", endDate: "2026-11-03"}
	sum := repository.ChatExportSummary{Rows: 416552, First: &first, Last: &last,
		Excluded: repository.ExcludedCounts{Newsletter: 852, Bot: 186, Broadcast: 5}}
	got := map[string]string{}
	for _, kv := range chatExportInfo(req, "", nil, sum, 1, "") {
		got[kv[0]] = kv[1]
	}
	if got["Periode filter"] != "28/01/2026 – 03/11/2026" {
		t.Errorf("period = %q", got["Periode filter"])
	}
	if got["Pesan pertama dalam file"] != "15/09/2026 08:53:05 WIB" || got["Pesan terakhir dalam file"] != "03/11/2026 13:00:00 WIB" {
		t.Errorf("first/last = %q / %q", got["Pesan pertama dalam file"], got["Pesan terakhir dalam file"])
	}
	if !strings.Contains(got["Catatan cakupan"], "sejak nomor ditautkan") {
		t.Errorf("coverage note = %q", got["Catatan cakupan"])
	}
	if want := "852 pesan saluran (channel), 186 pesan bot, 5 pesan daftar broadcast"; !strings.HasPrefix(got["Tidak termasuk"], want) {
		t.Errorf("excluded = %q", got["Tidak termasuk"])
	}
	if got["Jumlah pesan"] != "416.552" {
		t.Errorf("count = %q", got["Jumlah pesan"])
	}

	// A period that starts on the first message's own day needs no note.
	req.startDate = "2026-09-15"
	for _, kv := range chatExportInfo(req, "", nil, sum, 1, "") {
		if kv[0] == "Catatan cakupan" {
			t.Error("coverage note on a period that starts with the data")
		}
	}
}

func TestBareJID(t *testing.T) {
	for in, want := range map[string]string{
		"628123:12@s.whatsapp.net": "628123@s.whatsapp.net",
		"628123@s.whatsapp.net":    "628123@s.whatsapp.net",
		"1680999:3@lid":            "1680999@lid",
		"":                         "",
	} {
		if got := bareJID(in); got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}
}

func TestFormatThousands(t *testing.T) {
	for in, want := range map[int]string{0: "0", 999: "999", 1000: "1.000", 1048575: "1.048.575"} {
		if got := formatThousands(in); got != want {
			t.Errorf("%d → %q, want %q", in, got, want)
		}
	}
}

// A large export streams: memory stays flat while the row count grows, every
// row written is a row in the file, and rows past a full sheet go to the next.
func TestLargeExportStreams(t *testing.T) {
	if testing.Short() {
		t.Skip("writes 150k rows")
	}
	const n, perSheet = 150_000, 100_000
	tmp, err := os.CreateTemp(t.TempDir(), "export-*.xlsx")
	if err != nil {
		t.Fatal(err)
	}
	defer tmp.Close()

	sw := newChatExportSheet(tmp, 100)
	sw.perSheet = perSheet
	rows := sampleRows()
	runtime.GC()
	var before, peak runtime.MemStats
	runtime.ReadMemStats(&before)
	for i := 0; i < n; i++ {
		if err := sw.WriteRow(chatExportCells(rows[i%len(rows)])); err != nil {
			t.Fatal(err)
		}
		if i%25_000 == 0 {
			var m runtime.MemStats
			runtime.ReadMemStats(&m)
			if m.HeapInuse > peak.HeapInuse {
				peak = m
			}
		}
	}
	if err := sw.Close("Info Ekspor", nil); err != nil {
		t.Fatal(err)
	}
	if grew := int64(peak.HeapInuse) - int64(before.HeapInuse); grew > 64<<20 {
		t.Errorf("heap grew by %d MiB while streaming; the rows are being held", grew>>20)
	}

	info, _ := tmp.Stat()
	zr, err := zip.NewReader(tmp, info.Size())
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, f := range zr.File {
		if f.Name != "xl/worksheets/sheet1.xml" && f.Name != "xl/worksheets/sheet2.xml" {
			continue
		}
		rc, _ := f.Open()
		dec := xml.NewDecoder(rc)
		for {
			tok, err := dec.Token()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("%s XML broken after %d rows: %v", f.Name, counts[f.Name], err)
			}
			if se, ok := tok.(xml.StartElement); ok && se.Name.Local == "row" {
				counts[f.Name]++
			}
		}
		_ = rc.Close()
	}
	if counts["xl/worksheets/sheet1.xml"] != perSheet+1 || counts["xl/worksheets/sheet2.xml"] != n-perSheet+1 {
		t.Errorf("rows per sheet = %v, want %d and %d with headers", counts, perSheet+1, n-perSheet+1)
	}
	t.Logf("%d rows → %d KiB over %d sheets", n, info.Size()>>10, sw.Sheets())
}

// Writes the sample rows, one of them long enough to continue, to
// $CHAT_EXPORT_SAMPLE, to open by hand in Excel.
func TestWriteSampleWorkbook(t *testing.T) {
	path := os.Getenv("CHAT_EXPORT_SAMPLE")
	if path == "" {
		t.Skip("set CHAT_EXPORT_SAMPLE to a file path to write a sample workbook")
	}
	rows := sampleRows()
	long := strings.Repeat("Ini pesan yang sangat panjang. ", 1200)
	rows[1].Body = &long
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sw := newChatExportSheet(f, longestText(rows))
	for _, r := range rows {
		if err := sw.WriteRow(chatExportCells(r)); err != nil {
			t.Fatal(err)
		}
	}
	first, last := rows[0].Timestamp, rows[len(rows)-1].Timestamp
	sum := repository.ChatExportSummary{Rows: sw.Rows(), First: &first, Last: &last,
		Excluded: repository.ExcludedCounts{Newsletter: 3, Bot: 1}}
	req := chatExportRequest{startDate: "2026-11-01", endDate: "2026-11-03"}
	if err := sw.Close("Info Ekspor", chatExportInfo(req, "", nil, sum, sw.Sheets(), "contoh@salesan.id")); err != nil {
		t.Fatal(err)
	}
}
