package wa

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/salesan/omnichannel/backend/internal/config"
)

func testSession(cfg *config.Config) *Session {
	return &Session{
		mgr: &Manager{cfg: cfg, log: slog.Default()},
		log: slog.Default(),
	}
}

func defaultTimings() *config.Config {
	return &config.Config{
		HumanizeSending: true,
		TypingMin:       900 * time.Millisecond,
		TypingMax:       4 * time.Second,
		TypingCPS:       25,
		SendMinGap:      1500 * time.Millisecond,
	}
}

// The typing indicator has to stay inside its bounds whatever it is given —
// including an empty message and one far longer than anyone would type.
func TestTypingDurationStaysWithinBounds(t *testing.T) {
	cfg := defaultTimings()
	s := testSession(cfg)

	// jitter is ±20%, so the observable range is wider than the configured one.
	const spread = 0.2
	lowest := time.Duration(float64(cfg.TypingMin) * (1 - spread))
	highest := time.Duration(float64(cfg.TypingMax) * (1 + spread))

	for _, text := range []string{"", "ok", "halo, ada yang bisa dibantu?", strings.Repeat("a", 5000)} {
		for i := 0; i < 50; i++ {
			got := s.typingDuration(text)
			if got < lowest || got > highest {
				t.Fatalf("typingDuration(%d chars) = %v, want within [%v, %v]",
					len(text), got, lowest, highest)
			}
		}
	}
}

// A longer message should visibly take longer to "write" than a short one —
// that proportionality is the whole point of scaling by length.
func TestTypingDurationGrowsWithLength(t *testing.T) {
	s := testSession(defaultTimings())

	// Averaged, because each call is deliberately jittered.
	average := func(text string) time.Duration {
		var total time.Duration
		const runs = 200
		for i := 0; i < runs; i++ {
			total += s.typingDuration(text)
		}
		return total / runs
	}

	short := average("ok")
	long := average(strings.Repeat("a", 60))
	if long <= short {
		t.Fatalf("60 chars averaged %v, 2 chars averaged %v — longer text must take longer", long, short)
	}
}

// Every delay is jittered so replies do not arrive on a metronome, which is its
// own giveaway.
func TestTypingDurationIsNotConstant(t *testing.T) {
	s := testSession(defaultTimings())

	seen := map[time.Duration]bool{}
	for i := 0; i < 40; i++ {
		seen[s.typingDuration("halo")] = true
	}
	if len(seen) < 5 {
		t.Fatalf("only %d distinct durations in 40 calls; delays look fixed", len(seen))
	}
}

func TestJitterStaysInRange(t *testing.T) {
	base := time.Second
	for i := 0; i < 500; i++ {
		got := jitter(base, 0.3)
		if got < 700*time.Millisecond || got > 1300*time.Millisecond {
			t.Fatalf("jitter(1s, 0.3) = %v, want within [700ms, 1300ms]", got)
		}
	}
	if jitter(0, 0.3) != 0 {
		t.Error("jitter of zero must stay zero")
	}
}

// Turning the behaviour off must actually skip the waiting, not merely shorten
// it — an operator who disables it is asking for the fast path.
func TestSleepForRespectsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	sleepFor(ctx, 5*time.Second)
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Fatalf("sleepFor ignored a cancelled context: waited %v", elapsed)
	}
}

func TestSleepForZeroReturnsImmediately(t *testing.T) {
	start := time.Now()
	sleepFor(context.Background(), 0)
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Fatalf("sleepFor(0) waited %v", elapsed)
	}
}
