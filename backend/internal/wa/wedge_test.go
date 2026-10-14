package wa

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/appstate"

	"github.com/salesan/omnichannel/backend/internal/repository"
)

// The exact strings production produced, because the patch version is parsed
// out of a sentence and a sentence can be reworded. If whatsmeow ever changes
// this wording, the number stops being readable and every wedge starts looking
// like a new one — which is survivable, but it has to be noticed here rather
// than by somebody wondering why a number is read every ten minutes again.
func TestWedgedPatchReadsTheVersionFromTheRealErrors(t *testing.T) {
	tests := []struct {
		name  string
		err   error
		patch uint64
		is    bool
	}{
		{
			// CEREBRUM UTAMA, 13 October 2026, eighteen times.
			name:  "snapshot failure as logged",
			err:   fmt.Errorf("failed to verify snapshot: failed to verify patch v1176: %w", appstate.ErrMismatchingLTHash),
			patch: 1176,
			is:    true,
		},
		{
			name:  "wrapped once more by whatsmeow's decode path",
			err:   fmt.Errorf("failed to decode app state regular patches: failed to verify snapshot: failed to verify patch v1176: %w", appstate.ErrMismatchingLTHash),
			patch: 1176,
			is:    true,
		},
		{
			// regular_low, same account, same minute, different collection.
			name:  "a different collection's version",
			err:   fmt.Errorf("failed to verify snapshot: failed to verify patch v388: %w", appstate.ErrMismatchingLTHash),
			patch: 388,
			is:    true,
		},
		{
			// Still a wedge, but unnameable. Reported as one with version zero,
			// which noteWedge treats as movement — erring towards trying again
			// rather than going quiet about a failure we cannot identify.
			name:  "mismatch with no version in the text",
			err:   fmt.Errorf("something else entirely: %w", appstate.ErrMismatchingLTHash),
			patch: 0,
			is:    true,
		},

		// Not wedges. The sentinel decides, not the wording: a message that
		// happens to mention a patch must not be mistaken for this.
		{name: "nil", err: nil, is: false},
		{name: "key not found", err: appstate.ErrKeyNotFound, is: false},
		{
			name: "unrelated error that mentions a patch",
			err:  errors.New("failed to verify patch v99: something unrelated"),
			is:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patch, is := wedgedPatch(tt.err)
			if is != tt.is {
				t.Fatalf("wedgedPatch(%v) is-wedge = %v, want %v", tt.err, is, tt.is)
			}
			if patch != tt.patch {
				t.Errorf("wedgedPatch(%v) = v%d, want v%d", tt.err, patch, tt.patch)
			}
		})
	}
}

// The rule that was missing, and the one this whole change rests on.
//
// whatsmeow deletes the stored version before a full read and does not put it
// back when the read fails. Version zero then forces every later read to be a
// full one, so a single failed repair locks the collection into re-reading the
// same unverifiable snapshot for ever. A full read that already failed has
// proved the stored version was not the cause, so clearing it cannot be the
// remedy.
func TestClearingTheStoredVersionIsOnlyConsideredWhenItWasUsed(t *testing.T) {
	tests := []struct {
		failures int
		fullSync bool
		want     bool
	}{
		// Not yet sure it is wedged rather than unlucky.
		{failures: 1, fullSync: false, want: false},
		{failures: appStateForceAfter - 1, fullSync: false, want: false},

		// Sure, and the failed read used the stored version.
		{failures: appStateForceAfter, fullSync: false, want: true},
		{failures: appStateForceAfter + 9, fullSync: false, want: true},

		// Sure, but the failed read started from zero. However many times this
		// repeats, the answer stays no.
		{failures: appStateForceAfter, fullSync: true, want: false},
		{failures: 50, fullSync: true, want: false},
	}

	for _, tt := range tests {
		got := clearingCouldHelp(tt.failures, tt.fullSync)
		if got != tt.want {
			t.Errorf("clearingCouldHelp(failures=%d, fullSync=%v) = %v, want %v",
				tt.failures, tt.fullSync, got, tt.want)
		}
	}
}

// Same patch version means nothing changed on WhatsApp's side, so the interval
// has to hold. A different one means it rebuilt the snapshot, which is the only
// moment another read can succeed — and the only way a wedged number recovers
// without somebody watching it.
func TestTheIntervalHoldsUntilThePatchVersionMoves(t *testing.T) {
	s := &Session{}
	name := appstate.WAPatchRegular
	t0 := time.Date(2026, 10, 13, 9, 22, 0, 0, time.UTC)

	// First sighting: read it.
	w, act := s.noteWedge(name, 1176, t0)
	if !act {
		t.Fatal("the first sighting of a wedge must be read, not deferred")
	}
	if w.patch != 1176 || !w.since.Equal(t0) {
		t.Fatalf("first sighting recorded as v%d since %v", w.patch, w.since)
	}

	// Same version, a second later: hold.
	if _, act := s.noteWedge(name, 1176, t0.Add(time.Second)); act {
		t.Error("read again one second after the same patch failed")
	}

	// Same version, past the second step: read.
	if _, act := s.noteWedge(name, 1176, t0.Add(3*time.Minute)); !act {
		t.Error("the interval never came due")
	}

	// The interval must actually grow. Having spent two attempts, three
	// minutes is no longer enough.
	if _, act := s.noteWedge(name, 1176, t0.Add(6*time.Minute)); act {
		t.Error("the interval did not grow between attempts")
	}

	// WhatsApp rebuilt its snapshot: read immediately, and the history of the
	// old one is forgotten.
	w, act = s.noteWedge(name, 1301, t0.Add(7*time.Minute))
	if !act {
		t.Error("a moved patch version must be read at once")
	}
	if w.patch != 1301 {
		t.Errorf("still recorded as v%d after the version moved", w.patch)
	}
	if !w.since.Equal(t0.Add(7 * time.Minute)) {
		t.Errorf("since = %v, want the moment the new version was first seen", w.since)
	}
	if w.attempts != 1 {
		t.Errorf("attempts = %d after the version moved, want 1", w.attempts)
	}
}

// A reconnect or a deploy must not hand a long-wedged number a fresh
// full-speed schedule. This is what the persisted columns are for.
func TestARestoredWedgeKeepsItsPlaceInTheBackoff(t *testing.T) {
	s := &Session{}
	name := appstate.WAPatchRegular
	since := time.Date(2026, 9, 22, 4, 0, 0, 0, time.UTC)
	lastTry := time.Date(2026, 10, 13, 9, 20, 0, 0, time.UTC)

	s.loadWedge(name, wedgeState{patch: 1176, since: since, attempts: 12, lastTry: lastTry})

	// Two minutes after the last attempt, with twelve behind it, nothing is due.
	if _, act := s.noteWedge(name, 1176, lastTry.Add(2*time.Minute)); act {
		t.Error("a restored wedge was read again immediately, losing the backoff")
	}

	// And `since` is the old date, not today, so the screen can say how long
	// this has been going on.
	w, ok := s.wedgedAt(name)
	if !ok {
		t.Fatal("the restored wedge is not there")
	}
	if !w.since.Equal(since) {
		t.Errorf("since = %v, want the original %v", w.since, since)
	}

	// Past the ceiling, it is due again: a wedge does end, and nothing notices
	// if nothing ever looks.
	if _, act := s.noteWedge(name, 1176, lastTry.Add(7*time.Hour)); !act {
		t.Error("the backoff never comes due, so a healed wedge would never be noticed")
	}
}

// A successful read has to forget the wedge, otherwise the next failure looks
// like a continuation and is deferred for hours.
func TestASuccessfulReadForgetsTheWedge(t *testing.T) {
	s := &Session{}
	name := appstate.WAPatchRegular
	t0 := time.Date(2026, 10, 13, 9, 22, 0, 0, time.UTC)

	s.noteWedge(name, 1176, t0)
	s.clearWedge(name)

	if _, ok := s.wedgedAt(name); ok {
		t.Fatal("the wedge survived a successful read")
	}
	if _, act := s.noteWedge(name, 1176, t0.Add(time.Second)); !act {
		t.Error("a failure after a successful read was treated as a continuation")
	}
}

// The lie this whole mechanism exists to prevent, told by the badge itself.
//
// CEREBRUM UTAMA read "synced" on 14 October with labels_read_at null, zero
// labels stored, and its collection wedged at v1176. One branch was settling
// two different situations to the same cheerful word: "your label set is a
// refresh behind" and "this number has never held a label".
func TestTheBadgeMayNotClaimSyncedWithoutHavingRead(t *testing.T) {
	wedge := &wedgeState{
		patch:    1176,
		since:    time.Date(2026, 10, 14, 1, 56, 0, 0, time.UTC),
		attempts: 2,
	}
	bypassed := time.Date(2026, 10, 14, 3, 10, 0, 0, time.UTC)

	read := repository.LabelReadFacts{EverRead: true}
	never := repository.LabelReadFacts{EverRead: false}
	lenient := repository.LabelReadFacts{EverRead: false, UnverifiedAt: &bypassed, UnverifiedCount: 31}

	tests := []struct {
		name   string
		wedged *wedgeState
		facts  repository.LabelReadFacts
		want   string
	}{
		// The case that was wrong, in both of its forms.
		{"wedged and never read", wedge, never, LabelSyncFailed},
		{"never read", nil, never, LabelSyncFailed},

		// Wedged but holding a set from before. Still not synced: the set is
		// frozen at the patch before the wedge and will not move again until
		// WhatsApp rebuilds, which is a different thing from being up to date.
		// JADIBUMN HP IKLAN is this, with 25 labels stuck at v488.
		{"wedged but has read before", wedge, read, LabelSyncFailed},

		// Read the lenient way since. Usable, so not failed; vouched for by
		// nothing and one-way, so not synced either. This case has to be
		// decided before the plain wedge above, or the next reconciliation pass
		// would overwrite it ten minutes after every bypass read.
		{"wedged and read without verification", wedge, lenient, LabelSyncPartial},

		// The one case that may claim synced: a set is on screen and a refresh
		// is merely outstanding. Saying failed here would cry wolf every ten
		// minutes on an account that works.
		{"read before and not wedged", nil, read, LabelSyncSynced},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state, detail := labelIndicatorState(tt.wedged, tt.facts)
			if state != tt.want {
				t.Errorf("state = %q, want %q", state, tt.want)
			}
			if state != LabelSyncSynced && strings.TrimSpace(detail) == "" {
				t.Error("a badge that is not synced with nothing to read is the silence this replaced")
			}
			if state == LabelSyncSynced && detail != "" {
				t.Errorf("a synced badge carries an explanation: %q", detail)
			}
		})
	}

	// The wedge sentence has to carry the patch version, because that number is
	// what lets anybody tell a repeat from a new problem.
	if _, detail := labelIndicatorState(wedge, never); !strings.Contains(detail, "v1176") {
		t.Errorf("the wedge detail does not name the patch version: %q", detail)
	}
	// And it must not tell anyone to re-pair. That instruction was wrong, was
	// acted on, and cost a QR scan.
	if _, detail := labelIndicatorState(wedge, never); !strings.Contains(detail, "tidak menolong") {
		t.Errorf("the wedge detail does not say re-pairing will not help: %q", detail)
	}
	// The partial sentence has to admit both halves of what it is: unverified,
	// and one-way. An operator who reads only "label dibaca" will expect a
	// label they set here to show up on the phone.
	_, partial := labelIndicatorState(wedge, lenient)
	for _, want := range []string{"tanpa verifikasi", "v1176", "belum tentu sampai ke HP"} {
		if !strings.Contains(partial, want) {
			t.Errorf("the partial detail omits %q: %q", want, partial)
		}
	}
}
