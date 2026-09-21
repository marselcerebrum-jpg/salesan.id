package campaign

import (
	"time"

	"github.com/salesan/omnichannel/backend/internal/models"
)

// Working out when a recurring broadcast comes round again.
//
// Everything here is wall-clock arithmetic in Asia/Jakarta, because that is what
// the operator set: a broadcast at 09.00 has to stay at 09.00 through a clock
// change, and adding 24 hours to a timestamp is not the same thing as "tomorrow
// at nine". Go's AddDate works on calendar fields in a location, which is.

// Jakarta is the workspace's wall clock. Loaded once; a failure here would mean
// a build without zoneinfo, and falling back to UTC silently would shift every
// scheduled broadcast by seven hours.
var Jakarta = mustLoadJakarta()

func mustLoadJakarta() *time.Location {
	loc, err := time.LoadLocation("Asia/Jakarta")
	if err != nil {
		// +07:00 with no DST, which is what Asia/Jakarta is. Better than UTC.
		return time.FixedZone("WIB", 7*60*60)
	}
	return loc
}

// Recurrence is a series' shape: how often, on which day, at what time.
type Recurrence struct {
	Frequency string // daily, weekly, monthly
	// Hour and Minute are wall-clock WIB.
	Hour, Minute int
	// Weekday is 0 (Sunday) to 6, for weekly only.
	Weekday int
	// Day is 1 to 31, for monthly only. A month too short falls back to its
	// last day rather than skipping — "the 31st" means month-end to the person
	// who chose it, and silently missing February is the worse reading.
	Day int
}

// NextOccurrence returns the first firing strictly after `after`.
//
// Strictly after, so a series whose occurrence just ran cannot immediately
// produce another one for the same instant and send the same message twice.
func (r Recurrence) NextOccurrence(after time.Time) (time.Time, bool) {
	after = after.In(Jakarta)

	switch r.Frequency {
	case models.RecurrenceDaily:
		next := atTime(after, r.Hour, r.Minute)
		if !next.After(after) {
			next = next.AddDate(0, 0, 1)
		}
		return next, true

	case models.RecurrenceWeekly:
		next := atTime(after, r.Hour, r.Minute)
		// Days until the chosen weekday, 0 meaning today.
		delta := (r.Weekday - int(next.Weekday()) + 7) % 7
		next = next.AddDate(0, 0, delta)
		if !next.After(after) {
			next = next.AddDate(0, 0, 7)
		}
		return next, true

	case models.RecurrenceMonthly:
		next := monthlyOn(after.Year(), after.Month(), r.Day, r.Hour, r.Minute)
		if !next.After(after) {
			y, m := after.Year(), after.Month()
			if m == time.December {
				y, m = y+1, time.January
			} else {
				m++
			}
			next = monthlyOn(y, m, r.Day, r.Hour, r.Minute)
		}
		return next, true
	}
	return time.Time{}, false
}

// atTime is the same calendar day as t, at the given wall-clock time.
func atTime(t time.Time, hour, minute int) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), hour, minute, 0, 0, Jakarta)
}

// monthlyOn builds the chosen day of one month, clamped to the month's length.
//
// time.Date would happily turn 31 February into 3 March, which is not what
// somebody choosing "the 31st" asked for. Clamping keeps every occurrence inside
// the month it belongs to.
func monthlyOn(year int, month time.Month, day, hour, minute int) time.Time {
	last := daysIn(year, month)
	if day > last {
		day = last
	}
	if day < 1 {
		day = 1
	}
	return time.Date(year, month, day, hour, minute, 0, 0, Jakarta)
}

func daysIn(year int, month time.Month) int {
	// Day zero of next month is the last day of this one.
	return time.Date(year, month+1, 0, 0, 0, 0, 0, Jakarta).Day()
}
