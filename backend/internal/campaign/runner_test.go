package campaign

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"testing"
	"time"

	"go.mau.fi/whatsmeow"

	"github.com/salesan/omnichannel/backend/internal/models"
	"github.com/salesan/omnichannel/backend/internal/wa"
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

// A send that ran out of time is not a send that WhatsApp refused. The two are
// recorded differently — refused is retried, timed out is left for a person —
// so the classification is worth pinning down.
func TestSendTimeoutIsDistinguishedFromRefusal(t *testing.T) {
	timedOut := []error{
		context.DeadlineExceeded,
		fmt.Errorf("kirim: %w", context.DeadlineExceeded),
		whatsmeow.ErrMessageTimedOut,
		fmt.Errorf("kirim: %w", whatsmeow.ErrMessageTimedOut),
	}
	for _, err := range timedOut {
		if !isSendTimeout(err) {
			t.Errorf("expected a timeout for %v", err)
		}
	}

	other := []error{
		nil,
		context.Canceled,
		errors.New("server returned error 420"),
		wa.ErrGroupAdminsOnly,
		wa.ErrNotConnected,
	}
	for _, err := range other {
		if isSendTimeout(err) {
			t.Errorf("did not expect a timeout for %v", err)
		}
	}
}
