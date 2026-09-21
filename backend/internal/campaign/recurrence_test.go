package campaign

import (
	"testing"
	"time"

	"github.com/salesan/omnichannel/backend/internal/models"
)

func wib(y int, m time.Month, d, h, min int) time.Time {
	return time.Date(y, m, d, h, min, 0, 0, Jakarta)
}

func TestDailyRecurrence(t *testing.T) {
	r := Recurrence{Frequency: models.RecurrenceDaily, Hour: 9}

	// Before today's firing: today.
	if got, _ := r.NextOccurrence(wib(2026, time.March, 10, 7, 0)); !got.Equal(wib(2026, time.March, 10, 9, 0)) {
		t.Fatalf("morning before 09:00 should fire today, got %s", got)
	}
	// Exactly on it: tomorrow. Equal must not count, or a series that just ran
	// would immediately schedule the same instant again and send twice.
	if got, _ := r.NextOccurrence(wib(2026, time.March, 10, 9, 0)); !got.Equal(wib(2026, time.March, 11, 9, 0)) {
		t.Fatalf("exactly on the hour should roll to tomorrow, got %s", got)
	}
	// Across a month boundary.
	if got, _ := r.NextOccurrence(wib(2026, time.March, 31, 10, 0)); !got.Equal(wib(2026, time.April, 1, 9, 0)) {
		t.Fatalf("month end should roll into the next month, got %s", got)
	}
}

func TestWeeklyRecurrence(t *testing.T) {
	// Monday at 09:00.
	r := Recurrence{Frequency: models.RecurrenceWeekly, Weekday: int(time.Monday), Hour: 9}

	// 2026-03-10 is a Tuesday, so the next Monday is the 16th.
	if got, _ := r.NextOccurrence(wib(2026, time.March, 10, 12, 0)); !got.Equal(wib(2026, time.March, 16, 9, 0)) {
		t.Fatalf("Tuesday should wait for next Monday, got %s", got)
	}
	// On the day, before the time: today.
	if got, _ := r.NextOccurrence(wib(2026, time.March, 16, 8, 0)); !got.Equal(wib(2026, time.March, 16, 9, 0)) {
		t.Fatalf("Monday morning should fire the same day, got %s", got)
	}
	// On the day, after the time: a week later.
	if got, _ := r.NextOccurrence(wib(2026, time.March, 16, 10, 0)); !got.Equal(wib(2026, time.March, 23, 9, 0)) {
		t.Fatalf("Monday after the time should wait a week, got %s", got)
	}
}

func TestMonthlyRecurrence(t *testing.T) {
	r := Recurrence{Frequency: models.RecurrenceMonthly, Day: 1, Hour: 9}

	if got, _ := r.NextOccurrence(wib(2026, time.March, 10, 12, 0)); !got.Equal(wib(2026, time.April, 1, 9, 0)) {
		t.Fatalf("mid-month should roll to the 1st of next month, got %s", got)
	}
	if got, _ := r.NextOccurrence(wib(2026, time.December, 5, 0, 0)); !got.Equal(wib(2027, time.January, 1, 9, 0)) {
		t.Fatalf("December should roll into next year, got %s", got)
	}
}

// Choosing the 31st means month-end. A month that has no 31st must still fire,
// on its last day — skipping February entirely would be the worse answer.
func TestMonthlyRecurrenceClampsShortMonths(t *testing.T) {
	r := Recurrence{Frequency: models.RecurrenceMonthly, Day: 31, Hour: 9}

	if got, _ := r.NextOccurrence(wib(2026, time.January, 31, 10, 0)); !got.Equal(wib(2026, time.February, 28, 9, 0)) {
		t.Fatalf("February should clamp to the 28th, got %s", got)
	}
	// 2028 is a leap year.
	if got, _ := r.NextOccurrence(wib(2028, time.January, 31, 10, 0)); !got.Equal(wib(2028, time.February, 29, 9, 0)) {
		t.Fatalf("a leap February should clamp to the 29th, got %s", got)
	}
	if got, _ := r.NextOccurrence(wib(2026, time.March, 31, 10, 0)); !got.Equal(wib(2026, time.April, 30, 9, 0)) {
		t.Fatalf("April should clamp to the 30th, got %s", got)
	}
}

func TestUnknownFrequencyHasNoNext(t *testing.T) {
	if _, ok := (Recurrence{Frequency: "yearly", Hour: 9}).NextOccurrence(time.Now()); ok {
		t.Fatal("an unrecognised frequency must not produce an occurrence")
	}
}
