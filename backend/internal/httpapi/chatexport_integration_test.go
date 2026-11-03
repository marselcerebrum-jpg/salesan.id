package httpapi

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/salesan/omnichannel/backend/internal/models"
	"github.com/salesan/omnichannel/backend/internal/repository"
)

// The whole export, request to workbook, against a real schema: the shapes the
// reported file got wrong, laid down as rows and read back out of the .xlsx the
// handler sends.
//
// Runs only against SALESAN_TEST_DATABASE_URL — a scratch database, never
// production — and needs to create an auth user there, which a Supabase
// database only lets its own service do; elsewhere it skips. With
// CHAT_EXPORT_SAMPLE_DB set to a path, the workbook is also written there.
func TestIntegrationChatExportHandler(t *testing.T) {
	dsn := os.Getenv("SALESAN_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SALESAN_TEST_DATABASE_URL is not set; skipping database integration tests")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	// A new auth user makes their own workspace, owner profile and the
	// standard applications, through the same trigger production uses.
	email := "export-" + uuid.NewString()[:8] + "@salesan.test"
	var userID, ws, app uuid.UUID
	if err := pool.QueryRow(ctx, `insert into auth.users (email) values ($1) returning id`, email).Scan(&userID); err != nil {
		t.Skipf("cannot create an auth user in this database: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `delete from public.workspaces where id = $1`, ws)
		_, _ = pool.Exec(context.Background(), `delete from auth.users where id = $1`, userID)
	})
	if err := pool.QueryRow(ctx, `select workspace_id from public.users where id = $1`, userID).Scan(&ws); err != nil {
		t.Fatalf("profile: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`select id from public.applications where workspace_id = $1 and code = 'JADIASN'`, ws).Scan(&app); err != nil {
		t.Fatalf("application: %v", err)
	}

	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", strings.Fields(q)[0], err)
		}
	}
	id := func(q string, args ...any) uuid.UUID {
		t.Helper()
		var v uuid.UUID
		if err := pool.QueryRow(ctx, q, args...).Scan(&v); err != nil {
			t.Fatalf("insert: %v", err)
		}
		return v
	}
	account := func(name, phone string) uuid.UUID {
		return id(`insert into public.whatsapp_accounts (workspace_id, application_id, name, device_id, phone_number, jid)
			values ($1, $2, $3, $4, $5, $5 || ':4@s.whatsapp.net') returning id`, ws, app, name, "D-"+phone, phone)
	}
	conv := func(acc uuid.UUID, jid, typ, name, pn string) uuid.UUID {
		return id(`insert into public.conversations (workspace_id, account_id, chat_jid, type, name, pn_jid)
			values ($1, $2, $3, $4::public.conversation_type, nullif($5, ''), nullif($6, '')) returning id`,
			ws, acc, jid, typ, name, pn)
	}
	type msg struct {
		acc, conv            uuid.UUID
		wa, at, typ, body    string
		fromMe               bool
		status, sender, name string
	}
	put := func(m msg) {
		t.Helper()
		if m.wa == "" {
			m.wa = "3EB0" + strings.ToUpper(strings.ReplaceAll(uuid.NewString(), "-", "")[:16])
		}
		if m.typ == "" {
			m.typ = "text"
		}
		if m.status == "" {
			m.status = "delivered"
		}
		exec(`insert into public.messages (workspace_id, account_id, conversation_id, wa_message_id, type, body,
				from_me, status, sender_jid, participant_jid, sender_name, timestamp)
			values ($1, $2, $3, $4, $5::public.message_type, nullif($6, ''), $7, $8::public.message_status,
				nullif($9, ''), case when $9 like '%@lid' then $9 end, nullif($10, ''), $11::timestamptz)`,
			ws, m.acc, m.conv, m.wa, m.typ, m.body, m.fromMe, m.status, m.sender, m.name, m.at)
	}

	a := account("HP UTAMA", "6281200000001")
	b := account("HP KEDUA", "6281200000002")
	rina := conv(a, "628123456789@s.whatsapp.net", "personal", "Rina", "628123456789@s.whatsapp.net")
	lidChat := conv(a, "16807770000@lid", "personal", "", "")
	groupA := conv(a, "120363012345@g.us", "group", "Grup LC Batch 7", "")
	groupB := conv(b, "120363012345@g.us", "group", "Grup LC Batch 7", "")
	channel := conv(a, "120363999000@newsletter", "personal", "Info Promo", "")
	bot := conv(a, "13135550002@bot", "personal", "Meta AI", "")
	list := conv(a, "1730000000@broadcast", "personal", "", "")
	status := conv(a, "status@broadcast", "status", "", "")

	long := strings.Repeat("Rincian paket bimbingan: modul, tryout, kelas live. ", 800) // 41,600 bytes
	put(msg{acc: a, conv: rina, at: "2026-11-01 08:15:00+07", body: "Kak, mau tanya paket\nyang 3 bulan", sender: "628123456789@s.whatsapp.net"})
	put(msg{acc: a, conv: rina, at: "2026-11-01 08:16:10+07", body: "Silakan Kak", fromMe: true, status: "read"})
	put(msg{acc: a, conv: rina, at: "2026-11-01 08:16:10+07", body: "Silakan Kak", fromMe: true, status: "read"})
	put(msg{acc: a, conv: rina, at: "2026-11-01 08:20:00+07", body: long, fromMe: true, status: "read"})
	put(msg{acc: a, conv: rina, at: "2026-11-01 09:00:05+07", typ: "image", sender: "628123456789@s.whatsapp.net"})
	put(msg{acc: a, conv: rina, at: "2026-11-01 09:00:05+07", typ: "image", sender: "628123456789@s.whatsapp.net"})
	put(msg{acc: a, conv: rina, at: "2026-11-02 10:00:00+07", body: `=HYPERLINK("http://contoh","klik")`, sender: "628123456789@s.whatsapp.net"})
	put(msg{acc: a, conv: lidChat, at: "2026-11-02 11:30:00+07", body: "Halo kak, info daftar", sender: "16807770000@lid", name: "Dewi"})
	put(msg{acc: a, conv: groupA, wa: "3EB0SHAREDGROUP01", at: "2026-11-03 19:00:00+07", body: "Min, jadwal LC kapan?", sender: "16801234567890@lid", name: "Budi"})
	put(msg{acc: b, conv: groupB, wa: "3EB0SHAREDGROUP01", at: "2026-11-03 19:00:00+07", body: "Min, jadwal LC kapan?", sender: "16801234567890@lid", name: "Budi"})
	put(msg{acc: a, conv: groupA, at: "2026-11-03 19:02:00+07", body: "Besok jam 19.00 ya", fromMe: true, status: "sent"})
	for _, c := range []uuid.UUID{channel, channel, bot, list, status} {
		put(msg{acc: a, conv: c, at: "2026-11-02 12:00:00+07", body: "bukan chat pelanggan"})
	}
	const wantRows = 11

	s := &Server{repo: repository.New(pool), log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	user := &models.User{ID: userID, WorkspaceID: ws, Email: email, Role: "owner"}
	get := func(query string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/chats/export?"+query, nil)
		req = req.WithContext(withUser(req.Context(), user))
		rec := httptest.NewRecorder()
		s.handleExportChats(rec, req)
		return rec
	}

	// The reported filter: 28 January to 3 November.
	rec := get("start_date=2026-01-28&end_date=2026-11-03")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("X-Export-Rows"); got != strconv.Itoa(wantRows) {
		t.Errorf("X-Export-Rows = %s, want %d", got, wantRows)
	}
	if got := rec.Header().Get("Content-Disposition"); !strings.Contains(got, "Salesan_Export_Chat_2026-01-28_2026-11-03.xlsx") {
		t.Errorf("Content-Disposition = %s", got)
	}
	body := rec.Body.Bytes()
	if got := rec.Header().Get("Content-Length"); got != strconv.Itoa(len(body)) {
		t.Errorf("Content-Length %s, body %d bytes", got, len(body))
	}
	if path := os.Getenv("CHAT_EXPORT_SAMPLE_DB"); path != "" {
		if err := os.WriteFile(path, body, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("not a zip: %v", err)
	}
	parts := map[string]string{}
	for _, f := range zr.File {
		rc, _ := f.Open()
		raw, _ := io.ReadAll(rc)
		_ = rc.Close()
		parts[f.Name] = string(raw)
	}
	sheet := readSheet(t, parts["xl/worksheets/sheet1.xml"])
	if len(sheet.rows)-1 != wantRows {
		t.Fatalf("sheet has %d rows, want %d", len(sheet.rows)-1, wantRows)
	}
	// One continuation column, with a header, inside the filter.
	if _, ok := sheet.header["Keterangan Chat (lanjutan 1)"]; !ok {
		t.Errorf("no continuation header: %v", sheet.rows[0])
	}
	if !strings.Contains(parts["xl/worksheets/sheet1.xml"], `<autoFilter ref="A1:N12"/>`) {
		t.Error("filter does not cover A:N")
	}

	for r := 1; r < len(sheet.rows); r++ {
		if name := sheet.cell(r, "Nama Percakapan").Text; name == "Info Promo" || name == "Meta AI" {
			t.Errorf("row %d is from %s", r, name)
		}
		if sheet.cell(r, "Keterangan Chat").Text == "bukan chat pelanggan" {
			t.Errorf("row %d is a channel, bot, broadcast or status message", r)
		}
		if sheet.cell(r, "Status Pesan").Text == "" {
			t.Errorf("row %d lacks a status", r)
		}
		if text := sheet.cell(r, "Keterangan Chat").Text; strings.HasPrefix(text, "Rincian paket") {
			if got := text + sheet.cell(r, "Keterangan Chat (lanjutan 1)").Text; got != long {
				t.Errorf("long message was altered: %d → %d bytes", len(long), len(got))
			}
		}
	}

	info := parts["xl/worksheets/sheet2.xml"]
	for _, want := range []string{
		"28/01/2026 – 03/11/2026",
		"01/11/2026 08:15:00 WIB", "03/11/2026 19:02:00 WIB",
		"Catatan cakupan",
		"2 pesan saluran (channel), 1 pesan bot, 1 pesan daftar broadcast, 1 pesan status",
	} {
		if !strings.Contains(info, want) {
			t.Errorf("info sheet lacks %q", want)
		}
	}

	// Personal only still keeps the LID chat and drops the channel and bot.
	if rec := get("start_date=2026-11-01&end_date=2026-11-03&type=personal"); rec.Header().Get("X-Export-Rows") != "8" {
		t.Errorf("personal export: %s rows (%d)", rec.Header().Get("X-Export-Rows"), rec.Code)
	}
	// A period with nothing but a channel post is empty, and says why.
	put(msg{acc: a, conv: channel, at: "2026-10-05 12:00:00+07", body: "promo"})
	rec = get("start_date=2026-10-05&end_date=2026-10-05")
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "saluran, bot, broadcast, atau status") {
		t.Errorf("empty period: %d %s", rec.Code, rec.Body.String())
	}
}
