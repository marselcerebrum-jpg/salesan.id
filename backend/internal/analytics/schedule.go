package analytics

import (
	"sort"
	"time"

	"github.com/google/uuid"
)

// Window is one block of scheduled working time, already resolved to absolute
// moments in UTC.
//
// The database stores a date, two clock times and a timezone; turning that into
// a Window is the repository's job, because it needs the timezone name. Once
// resolved, everything here is plain interval arithmetic.
type Window struct {
	// ScheduleID is the row this window came from, so an SLA cycle can record
	// which shift it was measured against.
	ScheduleID uuid.UUID
	UserID     uuid.UUID
	Start      time.Time
	End        time.Time
}

// MergeWindows collapses overlapping and touching windows into a disjoint set.
//
// This is not an optimisation. A Freelance may be scheduled on two applications
// at the same hour; adding those two blocks together would report ten worked
// hours for a five-hour shift, and every "per jam" metric derived from it would
// be halved.
func MergeWindows(in []Window) []Window {
	if len(in) == 0 {
		return nil
	}
	sorted := make([]Window, 0, len(in))
	for _, w := range in {
		if w.End.After(w.Start) {
			sorted = append(sorted, w)
		}
	}
	if len(sorted) == 0 {
		return nil
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Start.Before(sorted[j].Start) })

	out := []Window{sorted[0]}
	for _, w := range sorted[1:] {
		last := &out[len(out)-1]
		if !w.Start.After(last.End) {
			if w.End.After(last.End) {
				last.End = w.End
			}
			continue
		}
		out = append(out, w)
	}
	return out
}

// WorkedSeconds is the total scheduled time covered by the windows, counting
// overlapping blocks once.
func WorkedSeconds(windows []Window) int {
	total := 0
	for _, w := range MergeWindows(windows) {
		total += int(w.End.Sub(w.Start).Seconds())
	}
	return total
}

// BusinessSeconds is how much of [from, to) falls inside the working windows.
//
// Returns (seconds, covered). `covered` is false when no window overlaps the
// interval at all, which the SLA rules treat differently from "zero seconds of
// a shift that does exist": a wait that happened entirely outside working hours
// is excluded from the SLA rather than scored as an instant answer.
func BusinessSeconds(windows []Window, from, to time.Time) (int, bool) {
	if !to.After(from) {
		return 0, true
	}
	total := 0
	covered := false
	for _, w := range MergeWindows(windows) {
		start := w.Start
		if from.After(start) {
			start = from
		}
		end := w.End
		if to.Before(end) {
			end = to
		}
		if end.After(start) {
			total += int(end.Sub(start).Seconds())
			covered = true
		}
	}
	return total, covered
}

// WindowsFor narrows a set of windows to one person.
func WindowsFor(windows []Window, userID uuid.UUID) []Window {
	out := make([]Window, 0, len(windows))
	for _, w := range windows {
		if w.UserID == userID {
			out = append(out, w)
		}
	}
	return out
}

// ScheduleAt returns the window covering a moment, or nil.
//
// Used to stamp an SLA cycle with the shift it started in — which is what makes
// "admin berganti shift saat SLA masih berjalan" answerable after the fact: the
// cycle keeps the shift it began under, and the responder is recorded
// separately from it.
func ScheduleAt(windows []Window, at time.Time) *Window {
	for i := range windows {
		if !at.Before(windows[i].Start) && at.Before(windows[i].End) {
			return &windows[i]
		}
	}
	return nil
}
