package wa

import (
	"testing"
	"time"
)

func TestWALabelColor(t *testing.T) {
	// WhatsApp sends a palette index, not a colour, and the index is unbounded
	// from our point of view: it must always map to a renderable hex value.
	seen := map[string]bool{}
	for i := int32(0); i < 64; i++ {
		got := waLabelColor(i)
		if len(got) != 7 || got[0] != '#' {
			t.Fatalf("waLabelColor(%d) = %q, want a #rrggbb value", i, got)
		}
		seen[got] = true
	}
	if len(seen) != len(waLabelPalette) {
		t.Errorf("palette produced %d distinct colours, want %d", len(seen), len(waLabelPalette))
	}

	// A negative index must not panic or index out of range.
	if got := waLabelColor(-3); got != waLabelPalette[0] {
		t.Errorf("waLabelColor(-3) = %q, want the first palette entry", got)
	}
}

func TestWALabelColorRoundTrip(t *testing.T) {
	// A colour pushed to the phone and read back must land on the same palette
	// slot, or every round trip through WhatsApp would drift the shade.
	for i := range waLabelPalette {
		hex := waLabelColor(int32(i))
		if got := waLabelColorIndex(hex); got != int32(i) {
			t.Errorf("index %d -> %q -> %d, want %d", i, hex, got, i)
		}
	}

	// An unknown colour must fall back rather than fail: a label existing on
	// both sides matters more than its exact shade.
	if got := waLabelColorIndex("#123456"); got != 0 {
		t.Errorf("unknown colour mapped to %d, want the 0 fallback", got)
	}
}

func TestSessionSyncWindow(t *testing.T) {
	var s Session

	if got := s.syncWindow(); got != DefaultSyncWindowDays {
		t.Errorf("unset window = %d, want the %d-day default", got, DefaultSyncWindowDays)
	}

	s.setSyncWindow(30)
	if got := s.syncWindow(); got != 30 {
		t.Errorf("window = %d, want 30", got)
	}

	// A non-positive value is meaningless and must not wipe the window.
	s.setSyncWindow(0)
	if got := s.syncWindow(); got != 30 {
		t.Errorf("window after setSyncWindow(0) = %d, want it unchanged at 30", got)
	}
	s.setSyncWindow(-1)
	if got := s.syncWindow(); got != 30 {
		t.Errorf("window after setSyncWindow(-1) = %d, want it unchanged at 30", got)
	}
}

func TestSessionWindowStart(t *testing.T) {
	var s Session
	s.setSyncWindow(7)

	start := s.windowStart()
	elapsed := time.Since(start)

	// Allow a wide tolerance so the test cannot flake on a slow machine.
	if elapsed < 7*24*time.Hour-time.Minute || elapsed > 7*24*time.Hour+time.Minute {
		t.Errorf("windowStart() is %v ago, want ~7 days", elapsed)
	}
	if !start.Before(time.Now()) {
		t.Error("windowStart() must be in the past")
	}
}
