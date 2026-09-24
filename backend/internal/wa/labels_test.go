package wa

import (
	"errors"
	"io"
	"log/slog"
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

// Clearing a collection only repairs it if the phone answers with the
// snapshot. A phone that stays silent leaves it exactly as wedged, so without
// a floor the repair becomes a loop: three more failures, clear again, ask
// again. One account did that eighteen times in ten minutes.
func TestForcedReReadIsRateLimited(t *testing.T) {
	// A logger, because the refusal path says so in the log and a nil one
	// would panic before the assertion is reached.
	s := &Session{log: slog.New(slog.NewTextHandler(io.Discard, nil))}

	if !s.mayForceAppState(appstate.WAPatchRegular) {
		t.Fatal("the first forced re-read must be allowed")
	}
	if s.mayForceAppState(appstate.WAPatchRegular) {
		t.Error("a second forced re-read straight away must be refused")
	}

	// Another collection is judged on its own history.
	if !s.mayForceAppState(appstate.WAPatchCriticalBlock) {
		t.Error("a different collection must not inherit the refusal")
	}

	// A successful decode clears the history, because the next wedge is a new
	// problem rather than a continuation of the old one.
	s.resetAppStateFailures(appstate.WAPatchRegular)
	if !s.mayForceAppState(appstate.WAPatchRegular) {
		t.Error("after a success the collection may be forced again")
	}
}

// The write path has to tell two failures apart. A collection whose stored
// state no longer matches WhatsApp's is repaired and the write retried; an
// ordinary refusal is reported as it came, because repairing the whole
// collection for one of those costs the phone a full dump it did not need.
//
// WhatsApp reports the first as a 409 together with a failure to apply the
// patches it sent back, and both halves arrive in one error string. That is
// the only place the two are stated together, which is why this reads the text.
func TestOnlyAWedgedCollectionTriggersARepair(t *testing.T) {
	wedged := errors.New(`server returned error updating app state (regular): ` +
		`<error code="409" text="conflict"/> (also, applying patches in the response failed: ` +
		`failed to decode app state regular patches: failed to verify patch v1103: mismatching LTHash)`)
	if !wedgedCollection(wedged) {
		t.Error("the error the operator actually sees must be recognised as a wedge")
	}

	for name, err := range map[string]error{
		"nothing at all":                 nil,
		"a plain conflict a retry fixes": errors.New(`server returned error: <error code="409" text="conflict"/>`),
		"a hash failure while reading":   errors.New("failed to verify patch v1103: mismatching LTHash"),
		"being offline":                  errors.New("websocket disconnected"),
		"a refusal":                      errors.New(`<error code="403" text="forbidden"/>`),
	} {
		if wedgedCollection(err) {
			t.Errorf("%s must not trigger a repair of the whole collection", name)
		}
	}
}
