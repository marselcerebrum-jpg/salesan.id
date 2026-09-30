package repository

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/salesan/omnichannel/backend/internal/models"
)

// Integration tests for Cold / Warm / Hot.
//
// The rule they protect is the one everything else rests on: a customer holds
// labels, not a status, and a quarter of them hold more than one. Which label
// decides is a choice, and a choice made in SQL is a choice only SQL can check.
//
//	$env:SALESAN_TEST_DATABASE_URL = "postgres://..."   # a scratch database
//	go test ./internal/repository -run IntegrationLabelCategory -v

// labelNamed creates a WhatsApp label and hangs it on the fixture's chat.
func (f *fixture) labelNamed(t *testing.T, name string, at time.Time) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	var id uuid.UUID
	if err := f.repo.pool.QueryRow(ctx, `
		insert into public.conversation_labels (workspace_id, account_id, name)
		values ($1, $2, $3) returning id`, f.workspaceID, f.accountID, name).Scan(&id); err != nil {
		t.Fatalf("create label %q: %v", name, err)
	}
	if _, err := f.repo.pool.Exec(ctx, `
		insert into public.conversation_label_assignments (conversation_id, label_id, assigned_at)
		values ($1, $2, $3)`, f.conversationID, id, at); err != nil {
		t.Fatalf("assign label %q: %v", name, err)
	}
	return id
}

func (f *fixture) analyticsScope() Scope {
	return Scope{UserID: uuid.New(), WorkspaceID: f.workspaceID, Role: models.RoleLeader, All: true}
}

// The names operators actually type. "Cold" and "FU3 COLD" and "Pindahan COLD"
// are one thing, and a report that treats them as three is a report nobody can
// read.
func TestIntegrationLabelCategoryMatchesNameVariants(t *testing.T) {
	f := newFixture(t, models.ConversationTypePersonal)
	ctx := context.Background()

	for name, want := range map[string]string{
		"Cold":          "cold",
		"FU3 COLD":      "cold",
		"Pindahan COLD": "cold",
		"fu warm":       "warm",
		"warmm":         "warm",
		"FU HOT":        "hot",
		"hot":           "hot",
		// Not a temperature, and must not be forced into one.
		"Premium": "",
		"Rabu":    "",
	} {
		var got *string
		if err := f.repo.pool.QueryRow(ctx,
			`select public.label_category_of($1)`, name).Scan(&got); err != nil {
			t.Fatalf("%q: %v", name, err)
		}
		have := ""
		if got != nil {
			have = *got
		}
		if have != want {
			t.Errorf("%q classified as %q, want %q", name, have, want)
		}
	}
}

// The rule that decides everything else: the most recent label wins.
//
// Priority by temperature was the alternative, and it would report a customer
// who has gone cold as Hot forever, because the old Hot label is still on them.
func TestIntegrationLabelCategoryTakesTheNewestLabel(t *testing.T) {
	f := newFixture(t, models.ConversationTypePersonal)
	ctx := context.Background()
	now := time.Now().UTC()

	f.labelNamed(t, "FU HOT", now.Add(-48*time.Hour))
	f.labelNamed(t, "Fu cold", now.Add(-1*time.Hour)) // the newer one

	got, err := f.repo.LabelCategorySummary(ctx, f.analyticsScope(), models.AnalyticsFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Cold != 1 || got.Hot != 0 {
		t.Errorf("summary = cold %d / warm %d / hot %d; the newer label was Cold",
			got.Cold, got.Warm, got.Hot)
	}
}

// A customer holding several labels is counted once, not once per label. Three
// numbers that add up to more than the number of customers are three numbers
// nobody can act on.
func TestIntegrationLabelCategoryCountsEachCustomerOnce(t *testing.T) {
	f := newFixture(t, models.ConversationTypePersonal)
	ctx := context.Background()
	now := time.Now().UTC()

	f.labelNamed(t, "Cold", now.Add(-3*time.Hour))
	f.labelNamed(t, "Warm", now.Add(-2*time.Hour))
	f.labelNamed(t, "Premium", now.Add(-1*time.Hour)) // no temperature, ignored

	got, err := f.repo.LabelCategorySummary(ctx, f.analyticsScope(), models.AnalyticsFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if total := got.Cold + got.Warm + got.Hot; total != 1 {
		t.Errorf("three categories total %d for one customer", total)
	}
	if got.Warm != 1 {
		t.Errorf("counted as cold %d / warm %d / hot %d; Warm was the newest temperature",
			got.Cold, got.Warm, got.Hot)
	}
}

// An override pins a label the pattern would read wrongly, and 'none' takes one
// out of the three altogether.
func TestIntegrationLabelCategoryOverrideWins(t *testing.T) {
	f := newFixture(t, models.ConversationTypePersonal)
	ctx := context.Background()

	id := f.labelNamed(t, "Hotel Bintang", time.Now().UTC())
	before, err := f.repo.LabelCategorySummary(ctx, f.analyticsScope(), models.AnalyticsFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if before.Hot != 1 {
		t.Fatalf("the pattern should have read 'Hotel' as hot; got %+v", before)
	}

	if _, err := f.repo.pool.Exec(ctx, `
		insert into public.label_category_overrides (workspace_id, label_id, category)
		values ($1, $2, 'none')`, f.workspaceID, id); err != nil {
		t.Fatal(err)
	}

	after, err := f.repo.LabelCategorySummary(ctx, f.analyticsScope(), models.AnalyticsFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if after.Cold+after.Warm+after.Hot != 0 {
		t.Errorf("override 'none' did not remove it: %+v", after)
	}
}

// The drill-down and the card must agree. They share one definition for exactly
// this reason, and the test exists to keep it that way.
func TestIntegrationLabelCategoryBreakdownMatchesSummary(t *testing.T) {
	f := newFixture(t, models.ConversationTypePersonal)
	ctx := context.Background()
	f.labelNamed(t, "FU HOT", time.Now().UTC())

	sc := f.analyticsScope()
	summary, err := f.repo.LabelCategorySummary(ctx, sc, models.AnalyticsFilter{})
	if err != nil {
		t.Fatal(err)
	}
	apps, err := f.repo.LabelCategoryByApplication(ctx, sc, models.AnalyticsFilter{}, "hot")
	if err != nil {
		t.Fatal(err)
	}

	total := 0
	for _, a := range apps {
		total += a.Contacts
	}
	if total != summary.Hot {
		t.Errorf("breakdown totals %d but the card says %d", total, summary.Hot)
	}

	contacts, n, err := f.repo.LabelCategoryContacts(ctx, sc, models.AnalyticsFilter{}, "hot", "", 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if n != summary.Hot || len(contacts) != summary.Hot {
		t.Errorf("customer list has %d rows (total %d) but the card says %d",
			len(contacts), n, summary.Hot)
	}
}
