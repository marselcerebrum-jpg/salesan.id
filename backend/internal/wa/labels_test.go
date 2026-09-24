package wa

import (
	"testing"

	"go.mau.fi/whatsmeow/appstate"
)

// A collection that merely fell behind is repaired politely: the stored state
// stays, the phone resends, the patches apply. A collection whose stored hash
// no longer matches the server's cannot be repaired that way, because every
// resend is verified against the same broken base and fails at the same
// version. Counting consecutive failures is what tells the two apart, so the
// count has to survive repeated failures and reset the moment one succeeds.
func TestRepeatedAppStateFailuresEscalateThenReset(t *testing.T) {
	s := &Session{}

	for i := 1; i < appStateForceAfter; i++ {
		if n := s.countAppStateFailure(appstate.WAPatchRegular); n != i {
			t.Fatalf("failure %d counted as %d", i, n)
		}
		if i >= appStateForceAfter {
			t.Fatalf("escalated at %d, before the threshold", i)
		}
	}

	n := s.countAppStateFailure(appstate.WAPatchRegular)
	if n != appStateForceAfter {
		t.Errorf("run length = %d, want %d", n, appStateForceAfter)
	}

	// A successful decode ends the run, so the next hiccup starts over rather
	// than forcing a full re-read on its own.
	s.resetAppStateFailures(appstate.WAPatchRegular)
	if n := s.countAppStateFailure(appstate.WAPatchRegular); n != 1 {
		t.Errorf("after a success the run restarts at %d, want 1", n)
	}

	// Collections are counted apart: a wedged `regular` must not drag
	// `critical_block` into a forced re-read it does not need.
	if n := s.countAppStateFailure(appstate.WAPatchCriticalBlock); n != 1 {
		t.Errorf("a second collection starts at %d, want 1", n)
	}
}
