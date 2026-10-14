package wa

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"time"

	"go.mau.fi/whatsmeow/appstate"
)

// A wedge is an app-state collection WhatsApp's own data will not let us read.
//
// It is a different thing from a collection that fell behind, and the two want
// opposite treatment. A collection that fell behind is repaired by asking for
// the patches it missed. A wedge is WhatsApp serving a snapshot whose MAC does
// not match the mutations inside it, and nothing we do on this side changes
// that: not a fresh read, not clearing our stored version, not pairing the
// phone again. CEREBRUM UTAMA was paired again on 13 October with new keys and
// a new device, and the snapshot still failed at the same patch, v1176.
//
// What makes a wedge tractable at all is that the patch version is in the error
// message. The same number means WhatsApp has not rebuilt anything, so another
// attempt reads the same bytes and fails the same way. A different number means
// it has, and that is the moment an attempt is worth making — which is how a
// number gets to recover without anybody sitting and watching it.

// wedgedPatch reads the patch version out of a verification failure.
//
// The sentinel is the fact and the number is only a label: the version is
// parsed from the message because whatsmeow carries it nowhere else, so a
// change in that wording must not be able to turn an unrelated error into a
// wedge. errors.Is decides whether this is a wedge at all; the regex only
// decides which one.
func wedgedPatch(err error) (uint64, bool) {
	if err == nil || !errors.Is(err, appstate.ErrMismatchingLTHash) {
		return 0, false
	}
	m := wedgedPatchRe.FindStringSubmatch(err.Error())
	if m == nil {
		// A mismatch we cannot name. Still a wedge, and reported as one, but
		// with no version to compare against — so it is always treated as
		// "something changed", which errs towards trying rather than giving up.
		return 0, true
	}
	v, convErr := strconv.ParseUint(m[1], 10, 64)
	if convErr != nil {
		return 0, true
	}
	return v, true
}

var wedgedPatchRe = regexp.MustCompile(`patch v(\d+)`)

// wedgeBackoff is how long to wait before reading a collection that is wedged
// at a patch version we have already seen fail.
//
// The first few attempts stay close together, because a snapshot can also fail
// for a reason that passes: a half-written patch on WhatsApp's side, a reply
// that arrived truncated. Past that the interval has to grow, and grow a long
// way. The previous behaviour was a ten minute safety net with no escalation,
// which on a number wedged for three weeks is three thousand downloads of a
// snapshot that was never going to verify, and three thousand chances to look
// like a client worth rate-limiting.
//
// The ceiling is deliberately not infinite. A wedge does end — WhatsApp rebuilds
// its snapshots as patches pile up — and the version check cannot notice that
// if nothing ever looks again.
var wedgeBackoff = []time.Duration{
	0,
	2 * time.Minute,
	10 * time.Minute,
	30 * time.Minute,
	2 * time.Hour,
	6 * time.Hour,
}

// wedgeConfirmAfter is how long a number must be unable to read its labels
// before the failure is reported as a wedge rather than merely recorded as one.
//
// Recording starts at the first failure, because the backoff needs it from
// then. Reporting must not, and the difference is what went wrong: five numbers
// appeared in the warning box within minutes of each other, four of them
// holding nineteen to twenty-five labels they had read perfectly well, three of
// them having failed exactly once. One of those was offered a button that
// skips cryptographic verification — for a problem that had not been shown to
// exist yet.
//
// An LTHash mismatch turns out to be ordinary weather across this fleet. What
// is not ordinary is one that is still there half an hour later: CEREBRUM UTAMA
// failed at the same patch eighteen times across a day, and JADIBUMN HP IKLAN
// has been at it for twenty-two hours. Thirty minutes separates those from a
// blip and still leaves most of an hour before anybody could have acted anyway.
const wedgeConfirmAfter = 30 * time.Minute

// wedgeIsProven reports whether a recorded wedge has lasted long enough to be
// worth a person's attention.
//
// Measured from when the run of failures began rather than from the wedge's own
// first sighting, because the wedge resets whenever WhatsApp rebuilds its
// snapshot and fails at a new version. A number that has been failing for a day
// at a moving target is not having a blip, and resetting its clock every time
// the patch number changes would hide exactly the worst case.
func wedgeIsProven(staleSince *time.Time, now time.Time) bool {
	return staleSince != nil && now.Sub(*staleSince) >= wedgeConfirmAfter
}

// wedgeDetail is the one sentence the operator reads about a wedge.
//
// One function because there are three places that have to say it — the badge
// in the chat header, the row in the health notice, and the record a health
// check reads — and three copies of a sentence is three chances for one of them
// to go on telling somebody to re-pair a phone.
func wedgeDetail(w wedgeState) string {
	return fmt.Sprintf(
		"Label tidak bisa dibaca: data label di WhatsApp sendiri tidak konsisten pada patch v%d. "+
			"Memasangkan ulang HP dan menekan Sinkron tidak menolong. Akan dicoba lagi otomatis "+
			"begitu WhatsApp menyusun ulang datanya; percobaan ke-%d sejak %s.",
		w.patch, w.attempts, w.since.Format("2 Jan 15:04"))
}

// wedgeState is what one collection's wedge looks like from this session.
type wedgeState struct {
	// patch is the version WhatsApp's data fails at, 0 when unknown.
	patch uint64
	// since is when this version was first seen failing.
	since time.Time
	// attempts counts the reads made since then, which sets the interval.
	attempts int
	// lastTry is when the most recent one was made.
	lastTry time.Time
}

// due reports whether another read is worth making now.
func (w wedgeState) due(now time.Time) bool {
	if w.lastTry.IsZero() {
		return true
	}
	i := w.attempts
	if i >= len(wedgeBackoff) {
		i = len(wedgeBackoff) - 1
	}
	return now.Sub(w.lastTry) >= wedgeBackoff[i]
}

// wait reports how long until the next read is due, for saying so out loud.
func (w wedgeState) wait(now time.Time) time.Duration {
	i := w.attempts
	if i >= len(wedgeBackoff) {
		i = len(wedgeBackoff) - 1
	}
	left := wedgeBackoff[i] - now.Sub(w.lastTry)
	if left < 0 {
		return 0
	}
	return left
}

// noteWedge records one verification failure and says whether to act on it.
//
// Returns the state as it now stands, and whether this read should be followed
// by an attempt at repair. A patch version that differs from the one on record
// resets the count: WhatsApp has rebuilt something, so the history of a
// different snapshot's failures says nothing about this one.
func (s *Session) noteWedge(name appstate.WAPatchName, patch uint64, now time.Time) (wedgeState, bool) {
	s.wedgeMu.Lock()
	defer s.wedgeMu.Unlock()

	if s.wedges == nil {
		s.wedges = map[string]wedgeState{}
	}
	key := string(name)
	w, seen := s.wedges[key]

	// Unknown version counts as movement, because the alternative is to go
	// quiet about a failure we cannot identify.
	if !seen || w.patch != patch || patch == 0 {
		w = wedgeState{patch: patch, since: now}
	}

	act := w.due(now)
	if act {
		w.attempts++
		w.lastTry = now
	}
	s.wedges[key] = w
	return w, act
}

// clearWedge forgets a collection's wedge after a successful read.
func (s *Session) clearWedge(name appstate.WAPatchName) {
	s.wedgeMu.Lock()
	defer s.wedgeMu.Unlock()
	delete(s.wedges, string(name))
}

// wedgedAt reports the recorded wedge for a collection, if there is one.
func (s *Session) wedgedAt(name appstate.WAPatchName) (wedgeState, bool) {
	s.wedgeMu.Lock()
	defer s.wedgeMu.Unlock()
	w, ok := s.wedges[string(name)]
	return w, ok
}

// loadWedge seeds this session from what the last one learned.
//
// Without it, every reconnect and every deploy starts the backoff from zero, so
// a number wedged for three weeks is read at full speed again after each one.
// The wedge outlives the process because the thing causing it does.
func (s *Session) loadWedge(name appstate.WAPatchName, w wedgeState) {
	if w.patch == 0 && w.since.IsZero() {
		return
	}
	s.wedgeMu.Lock()
	defer s.wedgeMu.Unlock()
	if s.wedges == nil {
		s.wedges = map[string]wedgeState{}
	}
	s.wedges[string(name)] = w
}
