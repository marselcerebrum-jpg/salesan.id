package repository

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/salesan/omnichannel/backend/internal/models"
)

// The export against a real schema: what goes in, what stays out, and that the
// filters and the scope cut where they should. Runs only against
// SALESAN_TEST_DATABASE_URL, like the other integration tests in this package.

// exportFixture adds, beside newFixture's personal chat, a group on the same
// number, a second number in a second application, a Status thread, and a
// message hidden from the inbox. Every timestamp is fixed in WIB so the date
// boundaries can be checked exactly.
type exportFixture struct {
	*fixture
	groupID      uuid.UUID
	statusID     uuid.UUID
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

	if _, err := pool.Exec(ctx,
		`update public.whatsapp_accounts set phone_number = '628900' where id = $1`, f.accountID); err != nil {
		t.Fatalf("set phone: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		insert into public.conversations (workspace_id, account_id, chat_jid, type, name)
		values ($1, $2, '120363000@g.us', 'group', 'Grup Uji') returning id`,
		f.workspaceID, f.accountID).Scan(&f.groupID); err != nil {
		t.Fatalf("create group: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		insert into public.conversations (workspace_id, account_id, chat_jid, type)
		values ($1, $2, 'status@broadcast', 'status') returning id`,
		f.workspaceID, f.accountID).Scan(&f.statusID); err != nil {
		t.Fatalf("create status: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`insert into public.applications (workspace_id, code, name) values ($1, $2, $2) returning id`,
		f.workspaceID, "OTHER"+uuid.NewString()[:6]).Scan(&f.otherAppID); err != nil {
		t.Fatalf("create application: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		insert into public.whatsapp_accounts (workspace_id, application_id, name, device_id)
		values ($1, $2, 'Other', $3) returning id`,
		f.workspaceID, f.otherAppID, "D-"+uuid.NewString()[:6]).Scan(&f.otherAccount); err != nil {
		t.Fatalf("create account: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		insert into public.conversations (workspace_id, account_id, chat_jid, type)
		values ($1, $2, '628222@s.whatsapp.net', 'personal') returning id`,
		f.workspaceID, f.otherAccount).Scan(&f.otherConv); err != nil {
		t.Fatalf("create conversation: %v", err)
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

func (f *exportFixture) export(t *testing.T, sc Scope, cf ChatExportFilter) []ChatExportRow {
	t.Helper()
	if cf.From.IsZero() {
		cf.From = time.Date(2026, 11, 1, 0, 0, 0, 0, wib)
		cf.To = time.Date(2026, 11, 4, 0, 0, 0, 0, wib)
	}
	var out []ChatExportRow
	if err := f.repo.ExportChatMessages(context.Background(), sc, cf, func(r ChatExportRow) error {
		out = append(out, r)
		return nil
	}); err != nil {
		t.Fatalf("export: %v", err)
	}
	return out
}

func TestIntegrationChatExport(t *testing.T) {
	f := newExportFixture(t)
	ctx := context.Background()
	day := func(d, h, m int) time.Time { return time.Date(2026, 11, d, h, m, 0, 0, wib) }

	// Personal: one in, one out, the same text twice as two real messages.
	in1 := f.put(t, f.accountID, f.conversationID, day(3, 9, 0), InsertMessageInput{
		SenderJID: strPtrTest("628111@s.whatsapp.net"), Body: strPtrTest("Kak, mau tanya paket"),
	})
	out1 := f.put(t, f.accountID, f.conversationID, day(3, 9, 1), InsertMessageInput{
		FromMe: true, SenderJID: strPtrTest("628900@s.whatsapp.net"), Body: strPtrTest("Silakan Kak"),
	})
	same := f.put(t, f.accountID, f.conversationID, day(3, 9, 1), InsertMessageInput{
		FromMe: true, SenderJID: strPtrTest("628900@s.whatsapp.net"), Body: strPtrTest("Silakan Kak"),
	})

	// Group: a member known only by LID, and our own reply.
	lidIn := f.put(t, f.accountID, f.groupID, day(3, 10, 0), InsertMessageInput{
		SenderJID: strPtrTest("1680999@lid"), ParticipantJID: strPtrTest("1680999@lid"),
		SenderName: strPtrTest("Budi"), Body: strPtrTest("Min, jadwal LC kapan?"),
	})
	f.put(t, f.accountID, f.groupID, day(3, 10, 5), InsertMessageInput{
		FromMe: true, SenderJID: strPtrTest("628900@s.whatsapp.net"), Body: strPtrTest("Besok"),
	})

	// Boundaries: first and last moment of the period count, the next day not.
	first := f.put(t, f.accountID, f.conversationID, day(1, 0, 0), InsertMessageInput{Body: strPtrTest("awal")})
	last := f.put(t, f.accountID, f.conversationID, time.Date(2026, 11, 3, 23, 59, 59, 0, wib),
		InsertMessageInput{Body: strPtrTest("akhir")})
	f.put(t, f.accountID, f.conversationID, day(4, 0, 0), InsertMessageInput{Body: strPtrTest("besoknya")})

	// Out of the export: a Status post, and a message hidden from the inbox.
	f.put(t, f.accountID, f.statusID, day(3, 11, 0), InsertMessageInput{Body: strPtrTest("status")})
	hidden := f.put(t, f.accountID, f.conversationID, day(3, 12, 0), InsertMessageInput{Body: strPtrTest("disembunyikan")})
	if _, err := f.repo.pool.Exec(ctx, `update public.messages set hidden_at = now() where id = $1`, hidden); err != nil {
		t.Fatal(err)
	}

	// Another application's number.
	f.put(t, f.otherAccount, f.otherConv, day(2, 8, 0), InsertMessageInput{Body: strPtrTest("lain")})

	leader := f.scope()

	t.Run("all", func(t *testing.T) {
		rows := f.export(t, leader, ChatExportFilter{})
		if len(rows) != 8 {
			t.Fatalf("rows = %d, want 8 (5 on the 3rd, 2 boundaries, 1 other app)", len(rows))
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
			if r.ConversationType == "status" || r.MessageID == hidden {
				t.Errorf("row %d should not be exported", i)
			}
		}
		for _, id := range []uuid.UUID{in1, out1, same, first, last} {
			if !seen[id] {
				t.Errorf("message %s missing", id)
			}
		}
	})

	t.Run("stable order", func(t *testing.T) {
		a := f.export(t, leader, ChatExportFilter{})
		b := f.export(t, leader, ChatExportFilter{})
		for i := range a {
			if a[i].MessageID != b[i].MessageID {
				t.Fatalf("order differs at row %d between two exports", i)
			}
		}
	})

	t.Run("type", func(t *testing.T) {
		p := f.export(t, leader, ChatExportFilter{Type: "personal"})
		g := f.export(t, leader, ChatExportFilter{Type: "group"})
		if len(p) != 6 || len(g) != 2 {
			t.Fatalf("personal=%d group=%d, want 6 and 2", len(p), len(g))
		}
		for _, r := range g {
			if r.ConversationType != "group" {
				t.Errorf("personal message in group export")
			}
		}
	})

	t.Run("parties", func(t *testing.T) {
		for _, r := range f.export(t, leader, ChatExportFilter{}) {
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
			}
		}
	})

	t.Run("application and accounts", func(t *testing.T) {
		mine := f.export(t, leader, ChatExportFilter{ApplicationID: &f.applicationID})
		if len(mine) != 7 {
			t.Errorf("one application: %d rows, want 7", len(mine))
		}
		other := f.export(t, leader, ChatExportFilter{AccountIDs: []uuid.UUID{f.otherAccount}})
		if len(other) != 1 {
			t.Errorf("one number: %d rows, want 1", len(other))
		}
		both := f.export(t, leader, ChatExportFilter{AccountIDs: []uuid.UUID{f.accountID, f.otherAccount}})
		if len(both) != 8 {
			t.Errorf("two numbers: %d rows, want 8", len(both))
		}
	})

	t.Run("scope", func(t *testing.T) {
		pic := Scope{UserID: uuid.New(), WorkspaceID: f.workspaceID, Role: models.RolePIC,
			ApplicationIDs: []uuid.UUID{f.otherAppID}}
		if rows := f.export(t, pic, ChatExportFilter{}); len(rows) != 1 {
			t.Errorf("a PIC of the other application sees %d rows, want 1", len(rows))
		}
		err := f.repo.ExportChatMessages(ctx, pic, ChatExportFilter{
			ApplicationID: &f.applicationID, From: day(1, 0, 0), To: day(4, 0, 0),
		}, func(ChatExportRow) error { return nil })
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
		if rows := f.export(t, stranger, ChatExportFilter{}); len(rows) != 0 {
			t.Errorf("another workspace sees %d rows", len(rows))
		}
		if v, _ := f.repo.AccountsForExport(ctx, stranger, []uuid.UUID{f.accountID}); len(v) != 0 {
			t.Errorf("another workspace can resolve this number")
		}
	})
}
