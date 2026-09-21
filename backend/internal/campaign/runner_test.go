package campaign

import (
	"context"
	"math/rand"
	"testing"
	"time"

	"github.com/salesan/omnichannel/backend/internal/models"
)

func deterministic() *rand.Rand { return rand.New(rand.NewSource(7)) }

func TestSleepHonoursCancellation(t *testing.T) {
	// A campaign on the Santai profile waits minutes between recipients. A
	// shutdown that had to wait those minutes out would be a shutdown nobody
	// uses, so the pause has to be interruptible.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	sleep(ctx, 5*time.Second)
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("a cancelled context still waited %v", elapsed)
	}
}

func TestActivityForMapsSettledStatuses(t *testing.T) {
	cases := map[string]string{
		models.CampaignCompleted: "published",
		models.CampaignPartial:   "partially_published",
		models.CampaignCancelled: "cancelled",
		models.CampaignFailed:    "failed",
	}
	for status, want := range cases {
		if got := activityFor(status); got != want {
			t.Errorf("%s: got %q, want %q", status, got, want)
		}
	}
}

func TestTruncateKeepsErrorsStorable(t *testing.T) {
	long := make([]byte, 1000)
	for i := range long {
		long[i] = 'x'
	}
	got := truncate(string(long), 400)
	if len(got) <= 400 || len(got) > 410 {
		t.Fatalf("truncate produced %d bytes", len(got))
	}
	if truncate("pendek", 400) != "pendek" {
		t.Fatal("a short string should be returned unchanged")
	}
}
