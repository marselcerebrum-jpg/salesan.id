package repository

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/salesan/omnichannel/backend/internal/analytics"
	"github.com/salesan/omnichannel/backend/internal/models"
)

// Integration tests for the derived reporting tables.
//
// These write real rows, so they run ONLY against a database named by
// SALESAN_TEST_DATABASE_URL — deliberately not DATABASE_URL. Pointing a test
// that creates and deletes workspaces at the production DSN by accident is a
// mistake nobody should be one typo away from, and the cost of the extra
// variable is one line in a CI config.
//
//	$env:SALESAN_TEST_DATABASE_URL = "postgres://..."   # a scratch database
//	go test ./internal/repository -run Integration -v
//
// Everything is created inside a fresh workspace and removed afterwards, so a
// re-run starts from the same place.

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("SALESAN_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SALESAN_TEST_DATABASE_URL is not set; skipping database integration tests")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// fixture is one disposable workspace with an account, a contact and a chat.
type fixture struct {
	repo           *Repo
	workspaceID    uuid.UUID
	applicationID  uuid.UUID
	accountID      uuid.UUID
	contactID      uuid.UUID
	conversationID uuid.UUID
}

func newFixture(t *testing.T, convType string) *fixture {
	t.Helper()
	ctx := context.Background()
	pool := testPool(t)
	repo := New(pool)

	f := &fixture{repo: repo}
	suffix := uuid.NewString()[:8]

	if err := pool.QueryRow(ctx,
		`insert into public.workspaces (name, slug) values ($1, $2) returning id`,
		"test-"+suffix, "test-"+suffix).Scan(&f.workspaceID); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	t.Cleanup(func() {
		// One delete: every table below cascades from the workspace.
		_, _ = pool.Exec(context.Background(),
			`delete from public.workspaces where id = $1`, f.workspaceID)
	})

	if err := pool.QueryRow(ctx,
		`insert into public.applications (workspace_id, code, name) values ($1, $2, $2) returning id`,
		f.workspaceID, "TEST"+suffix).Scan(&f.applicationID); err != nil {
		t.Fatalf("create application: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		insert into public.whatsapp_accounts
			(workspace_id, application_id, name, device_id, tracking_started_at)
		values ($1, $2, 'Test', $3, now() - interval '30 days') returning id`,
		f.workspaceID, f.applicationID, "D-"+suffix[:6]).Scan(&f.accountID); err != nil {
		t.Fatalf("create account: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		insert into public.contacts (workspace_id, account_id, jid, phone_number, name)
		values ($1, $2, $3, '628111', 'Kontak Uji') returning id`,
		f.workspaceID, f.accountID, "628111@s.whatsapp.net").Scan(&f.contactID); err != nil {
		t.Fatalf("create contact: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		insert into public.conversations (workspace_id, account_id, contact_id, chat_jid, type, name)
		values ($1, $2, $3, $4, $5::public.conversation_type, 'Kontak Uji') returning id`,
		f.workspaceID, f.accountID, f.contactID, "628111@s.whatsapp.net", convType,
	).Scan(&f.conversationID); err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	return f
}

// insertMessage writes one message straight through the repository, so the
// attribution trigger from migration 0021 runs exactly as it does in production.
func (f *fixture) insertMessage(t *testing.T, in InsertMessageInput) *models.Message {
	t.Helper()
	in.WorkspaceID = f.workspaceID
	in.AccountID = f.accountID
	in.ConversationID = f.conversationID
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
	return msg
}

func (f *fixture) scope() Scope {
	return Scope{UserID: uuid.New(), WorkspaceID: f.workspaceID, Role: models.RoleLeader, All: true}
}

// The attribution rule that everything else rests on: a message with a logged-in
// admin is web_admin, one without is an unattributed device message.
func TestIntegrationAttributionDefaults(t *testing.T) {
	f := newFixture(t, models.ConversationTypePersonal)
	ctx := context.Background()

	admin := f.seedUser(t)
	web := f.insertMessage(t, InsertMessageInput{FromMe: true, SentBy: &admin, Timestamp: time.Now()})
	phone := f.insertMessage(t, InsertMessageInput{FromMe: true, Timestamp: time.Now()})

	var webSource, phoneSource string
	var phoneAdmin *uuid.UUID
	if err := f.repo.pool.QueryRow(ctx,
		`select sender_source::text from public.messages where id = $1`, web.ID,
	).Scan(&webSource); err != nil {
		t.Fatal(err)
	}
	if err := f.repo.pool.QueryRow(ctx,
		`select sender_source::text, sent_by from public.messages where id = $1`, phone.ID,
	).Scan(&phoneSource, &phoneAdmin); err != nil {
		t.Fatal(err)
	}

	if webSource != analytics.SourceWebAdmin {
		t.Errorf("web message source = %q, want web_admin", webSource)
	}
	if phoneSource != analytics.SourceWhatsAppDevice {
		t.Errorf("phone message source = %q, want whatsapp_device", phoneSource)
	}
	if phoneAdmin != nil {
		t.Error("a message from the phone must stay unattributed")
	}
}

func (f *fixture) seedUser(t *testing.T) uuid.UUID {
	t.Helper()
	// public.users references auth.users, which a test cannot create rows in.
	// Reusing an existing profile keeps the foreign key satisfied without
	// touching the auth schema.
	var id uuid.UUID
	err := f.repo.pool.QueryRow(context.Background(),
		`select id from public.users limit 1`).Scan(&id)
	if err != nil {
		t.Skip("no user profile available to attribute messages to")
	}
	return id
}

func TestIntegrationSLACycleIsPersistedAndIdempotent(t *testing.T) {
	f := newFixture(t, models.ConversationTypePersonal)
	ctx := context.Background()

	start := time.Now().Add(-time.Hour)
	inbound := f.insertMessage(t, InsertMessageInput{Timestamp: start})
	f.insertMessage(t, InsertMessageInput{Timestamp: start.Add(2 * time.Minute)})
	admin := f.seedUser(t)
	f.insertMessage(t, InsertMessageInput{
		FromMe: true, SentBy: &admin, Timestamp: start.Add(5 * time.Minute)})

	cfg := MetricsConfig{TargetSeconds: 15 * 60}
	for i := 0; i < 3; i++ {
		// Three times, because a reconnect replays events and the sweep runs on
		// a timer: recomputing must converge, not accumulate.
		if err := f.repo.RecomputeConversation(ctx, f.conversationID, cfg); err != nil {
			t.Fatalf("recompute %d: %v", i, err)
		}
	}

	var count, inboundCount int
	var status string
	var raw int
	var target uuid.UUID
	if err := f.repo.pool.QueryRow(ctx, `
		select count(*) over (), inbound_message_count, status::text,
		       raw_duration_seconds, inbound_message_id
		  from public.sla_cycles where conversation_id = $1 limit 1`,
		f.conversationID).Scan(&count, &inboundCount, &status, &raw, &target); err != nil {
		t.Fatalf("read cycle: %v", err)
	}

	if count != 1 {
		t.Fatalf("got %d cycles after three recomputes, want exactly 1", count)
	}
	if target != inbound.ID {
		t.Error("the cycle must be keyed on the first unanswered message")
	}
	if inboundCount != 2 {
		t.Errorf("inbound_message_count = %d, want 2", inboundCount)
	}
	if status != analytics.StatusAchieved {
		t.Errorf("status = %q, want achieved", status)
	}
	if raw != 300 {
		t.Errorf("raw_duration_seconds = %d, want 300", raw)
	}
}

// A broadcast landing in a waiting chat must not close the cycle, and must not
// appear in the outgoing message count.
func TestIntegrationBroadcastDoesNotPolluteChatMetrics(t *testing.T) {
	f := newFixture(t, models.ConversationTypePersonal)
	ctx := context.Background()

	start := time.Now().Add(-time.Hour)
	f.insertMessage(t, InsertMessageInput{Timestamp: start})

	if _, err := f.repo.pool.Exec(ctx, `
		insert into public.messages
			(workspace_id, account_id, conversation_id, wa_message_id, from_me, type,
			 body, status, timestamp, sender_source)
		values ($1, $2, $3, $4, true, 'text', 'promo', 'sent', $5, 'broadcast')`,
		f.workspaceID, f.accountID, f.conversationID, uuid.NewString(),
		start.Add(time.Minute)); err != nil {
		t.Fatalf("insert broadcast: %v", err)
	}

	if err := f.repo.RecomputeConversation(ctx, f.conversationID, MetricsConfig{}); err != nil {
		t.Fatalf("recompute: %v", err)
	}

	var status string
	if err := f.repo.pool.QueryRow(ctx,
		`select status::text from public.sla_cycles where conversation_id = $1`,
		f.conversationID).Scan(&status); err != nil {
		t.Fatalf("read cycle: %v", err)
	}
	if status != analytics.StatusWaiting {
		t.Fatalf("status = %q, want waiting: a broadcast is not a reply", status)
	}

	report, err := f.repo.Performance(ctx, f.scope(), models.AnalyticsFilter{
		From: start.Add(-time.Hour), To: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("performance: %v", err)
	}
	if report.Summary.OutboundManualPersonal != 0 {
		t.Errorf("outbound manual = %d, want 0: a broadcast is not a manual reply",
			report.Summary.OutboundManualPersonal)
	}
	if report.Summary.InboundPersonal != 1 {
		t.Errorf("inbound = %d, want 1", report.Summary.InboundPersonal)
	}
}

func TestIntegrationFollowUpIsOnePerDay(t *testing.T) {
	f := newFixture(t, models.ConversationTypePersonal)
	ctx := context.Background()
	admin := f.seedUser(t)

	day1 := time.Now().Add(-48 * time.Hour)
	f.insertMessage(t, InsertMessageInput{Timestamp: day1})
	f.insertMessage(t, InsertMessageInput{FromMe: true, SentBy: &admin, Timestamp: day1.Add(time.Minute)})

	day2 := day1.Add(26 * time.Hour)
	f.insertMessage(t, InsertMessageInput{FromMe: true, SentBy: &admin, Timestamp: day2})
	f.insertMessage(t, InsertMessageInput{FromMe: true, SentBy: &admin, Timestamp: day2.Add(3 * time.Minute)})

	if err := f.repo.RecomputeConversation(ctx, f.conversationID, MetricsConfig{}); err != nil {
		t.Fatalf("recompute: %v", err)
	}

	var rows, messages int
	if err := f.repo.pool.QueryRow(ctx, `
		select count(*) over (), message_count
		  from public.follow_up_events where conversation_id = $1 limit 1`,
		f.conversationID).Scan(&rows, &messages); err != nil {
		t.Fatalf("read follow-up: %v", err)
	}
	if rows != 1 {
		t.Fatalf("got %d follow-up rows, want 1 for one day", rows)
	}
	if messages != 2 {
		t.Errorf("message_count = %d, want 2", messages)
	}
}

func TestIntegrationGroupMentionIsRecordedAndAnswered(t *testing.T) {
	f := newFixture(t, models.ConversationTypeGroup)
	ctx := context.Background()
	admin := f.seedUser(t)

	at := time.Now().Add(-time.Hour)
	mention := f.insertMessage(t, InsertMessageInput{
		Timestamp:      at,
		MentionsMe:     true,
		ParticipantJID: strPtrTest("628999@s.whatsapp.net"),
		MentionedJIDs:  []string{"628111@s.whatsapp.net"},
	})
	reply := f.insertMessage(t, InsertMessageInput{
		FromMe: true, SentBy: &admin, Timestamp: at.Add(4 * time.Minute)})

	if err := f.repo.RecomputeConversation(ctx, f.conversationID, MetricsConfig{}); err != nil {
		t.Fatalf("recompute: %v", err)
	}

	var messageID, responseID uuid.UUID
	if err := f.repo.pool.QueryRow(ctx, `
		select message_id, response_message_id
		  from public.group_mentions where conversation_id = $1`,
		f.conversationID).Scan(&messageID, &responseID); err != nil {
		t.Fatalf("read mention: %v", err)
	}
	if messageID != mention.ID {
		t.Error("the mention was recorded against the wrong message")
	}
	if responseID != reply.ID {
		t.Error("the answering message was not linked to the mention")
	}
}

// A contact whose first message came in through a history sync is historical,
// however recent that message looks.
func TestIntegrationImportedContactIsNotANewLead(t *testing.T) {
	f := newFixture(t, models.ConversationTypePersonal)
	ctx := context.Background()

	batchID, err := f.repo.StartImportBatch(ctx, f.workspaceID, f.accountID, 7, time.Now().Add(-7*24*time.Hour))
	if err != nil {
		t.Fatalf("start batch: %v", err)
	}
	f.insertMessage(t, InsertMessageInput{Timestamp: time.Now().Add(-time.Hour), ImportBatchID: &batchID})

	if err := f.repo.RefreshLead(ctx, f.contactID); err != nil {
		t.Fatalf("refresh lead: %v", err)
	}

	var status, reason string
	if err := f.repo.pool.QueryRow(ctx,
		`select lead_status::text, status_reason from public.lead_classifications where contact_id = $1`,
		f.contactID).Scan(&status, &reason); err != nil {
		t.Fatalf("read classification: %v", err)
	}
	if status != analytics.LeadHistorical {
		t.Fatalf("status = %q (%s), want historical", status, reason)
	}
	if reason == "" {
		t.Error("a classification must record why")
	}
}

func TestIntegrationLabelHistoryIsAppendOnlyAndIdempotent(t *testing.T) {
	f := newFixture(t, models.ConversationTypePersonal)
	ctx := context.Background()

	var labelID uuid.UUID
	if err := f.repo.pool.QueryRow(ctx, `
		insert into public.conversation_labels (workspace_id, account_id, wa_label_id, name, color)
		values ($1, $2, '1', 'Warm', '#B45309') returning id`,
		f.workspaceID, f.accountID).Scan(&labelID); err != nil {
		t.Fatalf("create label: %v", err)
	}
	if _, err := f.repo.pool.Exec(ctx, `
		insert into public.conversation_label_assignments (conversation_id, label_id)
		values ($1, $2)`, f.conversationID, labelID); err != nil {
		t.Fatalf("assign label: %v", err)
	}

	in := LabelEventInput{
		AccountID:      f.accountID,
		ConversationID: &f.conversationID,
		EventType:      LabelEventAssigned,
		ToLabelID:      &labelID,
		Source:         ChangeSourceWhatsApp,
		OccurredAt:     time.Now(),
		EventKey:       "assign:test:1",
	}

	first, err := f.repo.RecordLabelEvent(ctx, in)
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	second, err := f.repo.RecordLabelEvent(ctx, in)
	if err != nil {
		t.Fatalf("record again: %v", err)
	}
	if !first || second {
		t.Fatalf("first=%v second=%v; a replayed event must be recorded once", first, second)
	}

	var events int
	var name string
	var firstLabeled *time.Time
	if err := f.repo.pool.QueryRow(ctx,
		`select count(*) from public.contact_label_events where account_id = $1`,
		f.accountID).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != 1 {
		t.Errorf("got %d history rows, want 1", events)
	}
	if err := f.repo.pool.QueryRow(ctx,
		`select to_label_name from public.contact_label_events where account_id = $1`,
		f.accountID).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != "Warm" {
		t.Errorf("snapshot name = %q, want the name as it was at the time", name)
	}
	if err := f.repo.pool.QueryRow(ctx,
		`select first_labeled_at from public.contact_label_state where contact_id = $1`,
		f.contactID).Scan(&firstLabeled); err != nil {
		t.Fatalf("read state: %v", err)
	}
	if firstLabeled == nil {
		t.Error("first_labeled_at must be set the first time a contact gets a label")
	}
}

// A Freelance must not be able to read a colleague's figures, even by asking
// for them directly.
func TestIntegrationScopeHidesOtherPeoplesWork(t *testing.T) {
	f := newFixture(t, models.ConversationTypePersonal)
	ctx := context.Background()

	f.insertMessage(t, InsertMessageInput{Timestamp: time.Now().Add(-time.Hour)})

	outsider := Scope{
		UserID:      uuid.New(),
		WorkspaceID: f.workspaceID,
		Role:        models.RoleFreelance,
		// Assigned to a different application entirely.
		ApplicationIDs: []uuid.UUID{uuid.New()},
	}
	report, err := f.repo.Performance(ctx, outsider, models.AnalyticsFilter{
		From: time.Now().Add(-24 * time.Hour), To: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("performance: %v", err)
	}
	if report.Summary.InboundPersonal != 0 {
		t.Fatalf("inbound = %d, want 0: this application is out of scope",
			report.Summary.InboundPersonal)
	}
}

func strPtrTest(s string) *string { return &s }
