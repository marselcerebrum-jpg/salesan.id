package repository

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/salesan/omnichannel/backend/internal/models"
)

// The export against a real schema: what goes in, what stays out, and that the
// filters and the scope cut where they should. Runs only against
// SALESAN_TEST_DATABASE_URL, like the other integration tests in this package.

// exportFixture adds, beside newFixture's personal chat:
//
//   - a group on the same number, and a second number of the same application
//     sitting in the same group — the shape that stores one WhatsApp message
//     once per number;
//   - a personal chat WhatsApp knows only by LID;
//   - threads typed 'personal' that are not chats with a person: a channel, a
//     bot, a broadcast list — as conversationType() files them in production;
//   - a Status thread, and a number in a second application.
//
// Every timestamp is fixed in WIB so the date boundaries can be checked
// exactly.
type exportFixture struct {
	*fixture
	groupID      uuid.UUID
	statusID     uuid.UUID
	lidChatID    uuid.UUID
	channelID    uuid.UUID
	botID        uuid.UUID
	broadcastID  uuid.UUID
	sameApp      uuid.UUID // second number in f.applicationID
	sameAppGroup uuid.UUID // its copy of the group
	otherAppID   uuid.UUID
	otherAccount uuid.UUID
	otherConv    uuid.UUID
}

var wib = time.FixedZone("WIB", 7*3600)

func newExportFixture(t *testing.T) *exportFixture {
	t.Helper()
	ctx := context.Background()
	f := &exportFixture{fixture: newFixture(t, models.ConversationTypePersonal)}
	pool := f.repo.pool

	conv := func(account uuid.UUID, jid, typ, name string) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		if err := pool.QueryRow(ctx, `
			insert into public.conversations (workspace_id, account_id, chat_jid, type, name)
			values ($1, $2, $3, $4::public.conversation_type, nullif($5, '')) returning id`,
			f.workspaceID, account, jid, typ, name).Scan(&id); err != nil {
			t.Fatalf("create conversation %s: %v", jid, err)
		}
		return id
	}
	account := func(app uuid.UUID, name, phone string) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		if err := pool.QueryRow(ctx, `
			insert into public.whatsapp_accounts (workspace_id, application_id, name, device_id, phone_number, jid)
			values ($1, $2, $3, $4, nullif($5, ''), nullif($5, '') || ':3@s.whatsapp.net') returning id`,
			f.workspaceID, app, name, "D-"+uuid.NewString()[:6], phone).Scan(&id); err != nil {
			t.Fatalf("create account: %v", err)
		}
		return id
	}

	if _, err := pool.Exec(ctx,
		`update public.whatsapp_accounts set phone_number = '628900', jid = '628900:5@s.whatsapp.net' where id = $1`,
		f.accountID); err != nil {
		t.Fatalf("set phone: %v", err)
	}
	f.groupID = conv(f.accountID, "120363000@g.us", "group", "Grup Uji")
	f.statusID = conv(f.accountID, "status@broadcast", "status", "")
	f.lidChatID = conv(f.accountID, "1680777@lid", "personal", "")
	f.channelID = conv(f.accountID, "120363999@newsletter", "personal", "Saluran Promo")
	f.botID = conv(f.accountID, "13135550002@bot", "personal", "Meta AI")
	f.broadcastID = conv(f.accountID, "1700000000@broadcast", "personal", "")

	f.sameApp = account(f.applicationID, "Kedua", "628901")
	f.sameAppGroup = conv(f.sameApp, "120363000@g.us", "group", "Grup Uji")

	if err := pool.QueryRow(ctx,
		`insert into public.applications (workspace_id, code, name) values ($1, $2, $2) returning id`,
		f.workspaceID, "OTHER"+uuid.NewString()[:6]).Scan(&f.otherAppID); err != nil {
		t.Fatalf("create application: %v", err)
	}
	f.otherAccount = account(f.otherAppID, "Other", "")
	f.otherConv = conv(f.otherAccount, "628222@s.whatsapp.net", "personal", "")

	// One group member saved in the address book of both our numbers. A join
	// on the address book that is not one-to-one would export each of their
	// messages twice.
	for _, acc := range []uuid.UUID{f.accountID, f.sameApp} {
		if _, err := pool.Exec(ctx, `
			insert into public.contacts (workspace_id, account_id, jid, lid_jid, phone_number, name)
			values ($1, $2, '628555@s.whatsapp.net', '1680555@lid', '628555', 'Sari')`,
			f.workspaceID, acc); err != nil {
			t.Fatalf("create contact: %v", err)
		}
	}
	return f
}

func (f *exportFixture) put(t *testing.T, account, conv uuid.UUID, at time.Time, in InsertMessageInput) uuid.UUID {
	t.Helper()
	in.WorkspaceID, in.AccountID, in.ConversationID, in.Timestamp = f.workspaceID, account, conv, at
	if in.WAMessageID == "" {
		in.WAMessageID = uuid.NewString()
	}
	if in.Type == "" {
		in.Type = "text"
	}
	msg, _, err := f.repo.InsertMessage(context.Background(), in)
	if err != nil {
		t.Fatalf("insert message: %v", err)
	}
	return msg.ID
}

func (f *exportFixture) export(t *testing.T, sc Scope, cf ChatExportFilter) (ChatExportSummary, []ChatExportRow) {
	t.Helper()
	if cf.From.IsZero() {
		cf.From = time.Date(2026, 11, 1, 0, 0, 0, 0, wib)
		cf.To = time.Date(2026, 11, 4, 0, 0, 0, 0, wib)
	}
	var sum ChatExportSummary
	var out []ChatExportRow
	if err := f.repo.ExportChatMessages(context.Background(), sc, cf,
		func(s ChatExportSummary) error { sum = s; return nil },
		func(r ChatExportRow) error { out = append(out, r); return nil },
	); err != nil {
		t.Fatalf("export: %v", err)
	}
	if sum.Rows != len(out) {
		t.Fatalf("summary says %d rows, %d streamed", sum.Rows, len(out))
	}
	return sum, out
}

func TestIntegrationChatExport(t *testing.T) {
	f := newExportFixture(t)
	ctx := context.Background()
	day := func(d, h, m int) time.Time { return time.Date(2026, 11, d, h, m, 0, 0, wib) }

	// Personal: one in, one out, and the same text again as a second, real
	// message — identical on screen, a different message.
	in1 := f.put(t, f.accountID, f.conversationID, day(3, 9, 0), InsertMessageInput{
		SenderJID: strPtrTest("628111@s.whatsapp.net"), Body: strPtrTest("Kak, mau tanya paket"),
	})
	out1 := f.put(t, f.accountID, f.conversationID, day(3, 9, 1), InsertMessageInput{
		FromMe: true, SenderJID: strPtrTest("628900@s.whatsapp.net"), Body: strPtrTest("Silakan Kak"),
		Status: "read",
	})
	same := f.put(t, f.accountID, f.conversationID, day(3, 9, 1), InsertMessageInput{
		FromMe: true, SenderJID: strPtrTest("628900@s.whatsapp.net"), Body: strPtrTest("Silakan Kak"),
	})
	// An album: two photos in the same second, no caption, nothing on screen
	// to tell them apart.
	album1 := f.put(t, f.accountID, f.conversationID, day(3, 9, 2), InsertMessageInput{Type: "image"})
	album2 := f.put(t, f.accountID, f.conversationID, day(3, 9, 2), InsertMessageInput{Type: "image"})

	// Group: a member known only by LID, a member saved on both our numbers,
	// and our own reply. The LID member's message also reached our second
	// number, under the same WhatsApp id.
	lidIn := f.put(t, f.accountID, f.groupID, day(3, 10, 0), InsertMessageInput{
		WAMessageID: "3EB0SHARED", SenderJID: strPtrTest("1680999@lid"), ParticipantJID: strPtrTest("1680999@lid"),
		SenderName: strPtrTest("Budi"), Body: strPtrTest("Min, jadwal LC kapan?"),
	})
	copyIn := f.put(t, f.sameApp, f.sameAppGroup, day(3, 10, 0), InsertMessageInput{
		WAMessageID: "3EB0SHARED", SenderJID: strPtrTest("1680999@lid"), ParticipantJID: strPtrTest("1680999@lid"),
		SenderName: strPtrTest("Budi"), Body: strPtrTest("Min, jadwal LC kapan?"),
	})
	sari := f.put(t, f.accountID, f.groupID, day(3, 10, 2), InsertMessageInput{
		SenderJID: strPtrTest("1680555@lid"), ParticipantJID: strPtrTest("1680555@lid"), Body: strPtrTest("Siap"),
	})
	f.put(t, f.accountID, f.groupID, day(3, 10, 5), InsertMessageInput{
		FromMe: true, SenderJID: strPtrTest("628900@s.whatsapp.net"), Body: strPtrTest("Besok"),
	})

	// A customer WhatsApp addresses only by LID: a person, so exported.
	lidChat := f.put(t, f.accountID, f.lidChatID, day(3, 11, 30), InsertMessageInput{
		SenderJID: strPtrTest("1680777@lid"), SenderName: strPtrTest("Dewi"), Body: strPtrTest("Halo"),
	})

	// Boundaries: first and last moment of the period count, the next day not.
	first := f.put(t, f.accountID, f.conversationID, day(1, 0, 0), InsertMessageInput{Body: strPtrTest("awal")})
	last := f.put(t, f.accountID, f.conversationID, time.Date(2026, 11, 3, 23, 59, 59, 0, wib),
		InsertMessageInput{Body: strPtrTest("akhir")})
	f.put(t, f.accountID, f.conversationID, day(4, 0, 0), InsertMessageInput{Body: strPtrTest("besoknya")})

	// Out of the export, and counted as such: a channel's posts, a bot, a
	// broadcast list, all typed 'personal' as production types them, and a
	// Status post.
	excluded := map[uuid.UUID]bool{}
	for _, c := range []uuid.UUID{f.channelID, f.channelID, f.botID, f.broadcastID, f.statusID} {
		excluded[f.put(t, f.accountID, c, day(3, 11, 0), InsertMessageInput{Body: strPtrTest("bukan chat")})] = true
	}
	// Out, and not counted: a message hidden from the inbox.
	hidden := f.put(t, f.accountID, f.conversationID, day(3, 12, 0), InsertMessageInput{Body: strPtrTest("disembunyikan")})
	if _, err := f.repo.pool.Exec(ctx, `update public.messages set hidden_at = now() where id = $1`, hidden); err != nil {
		t.Fatal(err)
	}

	// Another application's number.
	f.put(t, f.otherAccount, f.otherConv, day(2, 8, 0), InsertMessageInput{Body: strPtrTest("lain")})

	leader := f.scope()
	const total = 13 // 5 personal + 4 group + 1 LID chat + 2 boundaries + 1 other app

	t.Run("all", func(t *testing.T) {
		sum, rows := f.export(t, leader, ChatExportFilter{})
		if len(rows) != total {
			t.Fatalf("rows = %d, want %d", len(rows), total)
		}
		seen := map[uuid.UUID]bool{}
		for i, r := range rows {
			if seen[r.MessageID] {
				t.Errorf("message %s appears twice", r.MessageID)
			}
			seen[r.MessageID] = true
			if i > 0 && r.Timestamp.Before(rows[i-1].Timestamp) {
				t.Errorf("row %d is out of order", i)
			}
			if excluded[r.MessageID] || r.MessageID == hidden {
				t.Errorf("row %d (%s) should not be exported", i, r.ChatJID)
			}
			if r.ConversationType != "personal" && r.ConversationType != "group" {
				t.Errorf("row %d has type %s", i, r.ConversationType)
			}
		}
		for _, id := range []uuid.UUID{in1, out1, same, album1, album2, lidIn, copyIn, sari, lidChat, first, last} {
			if !seen[id] {
				t.Errorf("message %s missing", id)
			}
		}

		if want := (ExcludedCounts{Newsletter: 2, Bot: 1, Broadcast: 1, Status: 1}); sum.Excluded != want {
			t.Errorf("excluded = %+v, want %+v", sum.Excluded, want)
		}
		if sum.First == nil || !sum.First.Equal(day(1, 0, 0)) {
			t.Errorf("first = %v, want %v", sum.First, day(1, 0, 0))
		}
		if wantLast := time.Date(2026, 11, 3, 23, 59, 59, 0, wib); sum.Last == nil || !sum.Last.Equal(wantLast) {
			t.Errorf("last = %v, want %v", sum.Last, wantLast)
		}
		if sum.MaxTextBytes != len("Min, jadwal LC kapan?") {
			t.Errorf("max text = %d", sum.MaxTextBytes)
		}
	})

	t.Run("identity", func(t *testing.T) {
		_, rows := f.export(t, leader, ChatExportFilter{})
		byID := map[uuid.UUID]ChatExportRow{}
		for _, r := range rows {
			byID[r.MessageID] = r
		}
		o := byID[out1]
		if o.AccountID != f.accountID || o.ConversationID != f.conversationID || o.WAMessageID == "" ||
			o.Status != "read" || o.AccountJID == nil || *o.AccountJID != "628900:5@s.whatsapp.net" {
			t.Errorf("identity of an outgoing message = %+v", o)
		}
		// Displayed the same, told apart by id.
		if byID[out1].WAMessageID == byID[same].WAMessageID || byID[album1].WAMessageID == byID[album2].WAMessageID {
			t.Error("two different messages share a WhatsApp id")
		}
		// One WhatsApp message held by two of our numbers: two rows, the same
		// WhatsApp id and chat, different accounts and conversations.
		a, b := byID[lidIn], byID[copyIn]
		if a.WAMessageID != b.WAMessageID || a.ChatJID != b.ChatJID || a.AccountID == b.AccountID ||
			a.ConversationID == b.ConversationID {
			t.Errorf("cross-account copy: %+v / %+v", a, b)
		}
	})

	t.Run("stable order", func(t *testing.T) {
		_, a := f.export(t, leader, ChatExportFilter{})
		_, b := f.export(t, leader, ChatExportFilter{})
		for i := range a {
			if a[i].MessageID != b[i].MessageID {
				t.Fatalf("order differs at row %d between two exports", i)
			}
		}
	})

	t.Run("type", func(t *testing.T) {
		ps, p := f.export(t, leader, ChatExportFilter{Type: "personal"})
		gs, g := f.export(t, leader, ChatExportFilter{Type: "group"})
		if len(p) != 9 || len(g) != 4 {
			t.Fatalf("personal=%d group=%d, want 9 and 4", len(p), len(g))
		}
		for _, r := range p {
			if r.ConversationType != "personal" || excluded[r.MessageID] {
				t.Errorf("%s in the personal export", r.ChatJID)
			}
		}
		for _, r := range g {
			if r.ConversationType != "group" {
				t.Errorf("personal message in group export")
			}
		}
		// What is out of scope is out of scope whatever the filter; groups are
		// not "excluded" from a personal export, only filtered.
		if ps.Excluded.Total() != 5 || gs.Excluded.Total() != 5 {
			t.Errorf("excluded personal=%d group=%d, want 5", ps.Excluded.Total(), gs.Excluded.Total())
		}
	})

	t.Run("parties", func(t *testing.T) {
		_, rows := f.export(t, leader, ChatExportFilter{})
		for _, r := range rows {
			switch r.MessageID {
			case in1, out1:
				// The customer's number, whichever way the message went.
				if r.CounterpartPhone == nil || *r.CounterpartPhone != "628111" {
					t.Errorf("personal counterpart = %v, want 628111", r.CounterpartPhone)
				}
			case lidIn:
				if r.SenderPhone != nil {
					t.Errorf("LID-only member got a number: %q", *r.SenderPhone)
				}
				if r.SenderJID == nil || *r.SenderJID != "1680999@lid" {
					t.Errorf("LID member lost their identity: %v", r.SenderJID)
				}
				if r.SenderName == nil || *r.SenderName != "Budi" {
					t.Errorf("sender name = %v, want Budi", r.SenderName)
				}
			case sari:
				if r.SenderPhone == nil || *r.SenderPhone != "628555" || r.SenderName == nil || *r.SenderName != "Sari" {
					t.Errorf("member saved on two numbers = %v / %v", r.SenderPhone, r.SenderName)
				}
			case lidChat:
				if r.CounterpartPhone != nil {
					t.Errorf("LID chat got a number: %q", *r.CounterpartPhone)
				}
				if r.ChatJID != "1680777@lid" || r.ConversationType != "personal" {
					t.Errorf("LID chat = %s %s", r.ConversationType, r.ChatJID)
				}
			}
		}
	})

	t.Run("application and accounts", func(t *testing.T) {
		if _, mine := f.export(t, leader, ChatExportFilter{ApplicationID: &f.applicationID}); len(mine) != total-1 {
			t.Errorf("one application: %d rows, want %d", len(mine), total-1)
		}
		if _, other := f.export(t, leader, ChatExportFilter{AccountIDs: []uuid.UUID{f.otherAccount}}); len(other) != 1 {
			t.Errorf("one number: %d rows, want 1", len(other))
		}
		if _, second := f.export(t, leader, ChatExportFilter{AccountIDs: []uuid.UUID{f.sameApp}}); len(second) != 1 {
			t.Errorf("second number: %d rows, want 1", len(second))
		}
		if _, both := f.export(t, leader, ChatExportFilter{AccountIDs: []uuid.UUID{f.accountID, f.otherAccount}}); len(both) != total-1 {
			t.Errorf("two numbers: %d rows, want %d", len(both), total-1)
		}
	})

	t.Run("no rows", func(t *testing.T) {
		far := time.Date(2020, 1, 1, 0, 0, 0, 0, wib)
		sum, rows := f.export(t, leader, ChatExportFilter{From: far, To: far.AddDate(0, 0, 1)})
		if len(rows) != 0 || sum.First != nil || sum.Last != nil {
			t.Errorf("empty period: %d rows, first %v", len(rows), sum.First)
		}
	})

	t.Run("scope", func(t *testing.T) {
		pic := Scope{UserID: uuid.New(), WorkspaceID: f.workspaceID, Role: models.RolePIC,
			ApplicationIDs: []uuid.UUID{f.otherAppID}}
		if _, rows := f.export(t, pic, ChatExportFilter{}); len(rows) != 1 {
			t.Errorf("a PIC of the other application sees %d rows, want 1", len(rows))
		}
		err := f.repo.ExportChatMessages(ctx, pic, ChatExportFilter{
			ApplicationID: &f.applicationID, From: day(1, 0, 0), To: day(4, 0, 0),
		}, func(ChatExportSummary) error { return nil }, func(ChatExportRow) error { return nil })
		if err != ErrForbidden {
			t.Errorf("asking for an application out of scope: %v, want ErrForbidden", err)
		}
		visible, err := f.repo.AccountsForExport(ctx, pic, []uuid.UUID{f.accountID, f.otherAccount})
		if err != nil {
			t.Fatal(err)
		}
		if len(visible) != 1 || visible[0].ID != f.otherAccount {
			t.Errorf("AccountsForExport returned %d numbers, want only the PIC's own", len(visible))
		}

		stranger := Scope{UserID: uuid.New(), WorkspaceID: uuid.New(), All: true}
		if sum, rows := f.export(t, stranger, ChatExportFilter{}); len(rows) != 0 || sum.Excluded.Total() != 0 {
			t.Errorf("another workspace sees %d rows, %d excluded", len(rows), sum.Excluded.Total())
		}
		if v, _ := f.repo.AccountsForExport(ctx, stranger, []uuid.UUID{f.accountID}); len(v) != 0 {
			t.Errorf("another workspace can resolve this number")
		}
	})
}

// More rows than one cursor batch, over a long period, with a message too long
// for one cell: every row comes back once, in order, and the summary agrees
// with what was streamed.
func TestIntegrationChatExportBatches(t *testing.T) {
	f := newExportFixture(t)
	ctx := context.Background()
	const n = 2*chatExportBatch + 500

	if _, err := f.repo.pool.Exec(ctx, `
		insert into public.messages (workspace_id, account_id, conversation_id, wa_message_id, type, body, timestamp)
		select $1, $2, $3, 'BULK' || g, 'text', 'pesan ' || g,
		       timestamptz '2026-01-28 08:00+07' + (g * interval '60 minutes')
		  from generate_series(1, $4::int) g`,
		f.workspaceID, f.accountID, f.conversationID, n); err != nil {
		t.Fatalf("bulk insert: %v", err)
	}
	long := strings.Repeat("panjang ", 9000) // 72,000 bytes
	f.put(t, f.accountID, f.conversationID, time.Date(2026, 6, 1, 12, 0, 0, 0, wib), InsertMessageInput{Body: &long})

	sum, rows := f.export(t, f.scope(), ChatExportFilter{
		From: time.Date(2026, 1, 28, 0, 0, 0, 0, wib), To: time.Date(2026, 11, 4, 0, 0, 0, 0, wib),
	})
	if len(rows) != n+1 {
		t.Fatalf("rows = %d, want %d", len(rows), n+1)
	}
	seen := make(map[uuid.UUID]bool, len(rows))
	for i, r := range rows {
		if seen[r.MessageID] {
			t.Fatalf("message %s appears twice", r.MessageID)
		}
		seen[r.MessageID] = true
		if i > 0 && r.Timestamp.Before(rows[i-1].Timestamp) {
			t.Fatalf("row %d is out of order", i)
		}
	}
	if sum.MaxTextBytes != len(long) {
		t.Errorf("max text = %d, want %d", sum.MaxTextBytes, len(long))
	}
	if !sum.First.Equal(rows[0].Timestamp) || !sum.Last.Equal(rows[len(rows)-1].Timestamp) {
		t.Errorf("first/last = %v / %v, rows run %v – %v", sum.First, sum.Last, rows[0].Timestamp, rows[len(rows)-1].Timestamp)
	}

	// A cancelled export stops between batches rather than running on.
	cctx, cancel := context.WithCancel(ctx)
	got := 0
	err := f.repo.ExportChatMessages(cctx, f.scope(), ChatExportFilter{
		From: time.Date(2026, 1, 28, 0, 0, 0, 0, wib), To: time.Date(2026, 11, 4, 0, 0, 0, 0, wib),
	}, func(ChatExportSummary) error { return nil }, func(ChatExportRow) error {
		got++
		if got == 10 {
			cancel()
		}
		return nil
	})
	if err == nil || got > chatExportBatch {
		t.Errorf("cancelled export: err %v after %d rows", err, got)
	}
}

// The statements plan against the real schema for every filter shape, so a
// renamed column fails here and not in production.
func TestIntegrationChatExportStatementsPlan(t *testing.T) {
	f := newExportFixture(t)
	ctx := context.Background()
	app := f.applicationID
	from, to := time.Date(2026, 1, 1, 0, 0, 0, 0, wib), time.Date(2026, 12, 1, 0, 0, 0, 0, wib)
	pic := Scope{WorkspaceID: f.workspaceID, ApplicationIDs: []uuid.UUID{app}}
	for _, c := range []struct {
		sc Scope
		f  ChatExportFilter
	}{
		{f.scope(), ChatExportFilter{From: from, To: to}},
		{f.scope(), ChatExportFilter{From: from, To: to, Type: "personal", ApplicationID: &app}},
		{pic, ChatExportFilter{From: from, To: to, Type: "group", AccountIDs: []uuid.UUID{f.accountID}}},
	} {
		for _, build := range []func(Scope, ChatExportFilter) (string, []any){chatExportQuery, chatExportSummaryQuery} {
			q, args := build(c.sc, c.f)
			rows, err := f.repo.pool.Query(ctx, "explain "+q, args...)
			if err != nil {
				t.Fatalf("explain: %v", err)
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				t.Fatalf("explain: %v", err)
			}
		}
	}
}
