package analytics

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func win(startHour, endHour int) Window {
	day := time.Date(2026, 9, 9, 0, 0, 0, 0, time.UTC)
	return Window{
		ScheduleID: uuid.New(),
		Start:      day.Add(time.Duration(startHour) * time.Hour),
		End:        day.Add(time.Duration(endHour) * time.Hour),
	}
}

// A Freelance covering two applications in the same hour worked one hour, not
// two. Getting this wrong halves every per-hour metric derived from it.
func TestWorkedSecondsCountsOverlapOnce(t *testing.T) {
	got := WorkedSeconds([]Window{win(9, 17), win(13, 18)})
	want := 9 * 3600
	if got != want {
		t.Fatalf("WorkedSeconds = %d, want %d", got, want)
	}
}

func TestWorkedSecondsAddsDisjointBlocks(t *testing.T) {
	got := WorkedSeconds([]Window{win(8, 12), win(13, 17)})
	if want := 8 * 3600; got != want {
		t.Fatalf("WorkedSeconds = %d, want %d", got, want)
	}
}

func TestMergeWindowsDropsEmptyRanges(t *testing.T) {
	empty := win(9, 9)
	if got := MergeWindows([]Window{empty}); len(got) != 0 {
		t.Fatalf("got %d windows, want none", len(got))
	}
}

func TestBusinessSecondsClipsToWindows(t *testing.T) {
	day := time.Date(2026, 9, 9, 0, 0, 0, 0, time.UTC)
	windows := []Window{win(9, 17)}

	// 08:00 to 10:00 — only the hour from 09:00 counts.
	secs, covered := BusinessSeconds(windows, day.Add(8*time.Hour), day.Add(10*time.Hour))
	if !covered {
		t.Fatal("the interval overlaps the shift, so it is covered")
	}
	if secs != 3600 {
		t.Fatalf("BusinessSeconds = %d, want 3600", secs)
	}
}

func TestBusinessSecondsSpansTwoBlocks(t *testing.T) {
	day := time.Date(2026, 9, 9, 0, 0, 0, 0, time.UTC)
	windows := []Window{win(8, 12), win(13, 17)}

	// 11:00 to 14:00 crosses the break: one hour before, one after.
	secs, _ := BusinessSeconds(windows, day.Add(11*time.Hour), day.Add(14*time.Hour))
	if secs != 2*3600 {
		t.Fatalf("BusinessSeconds = %d, want %d", secs, 2*3600)
	}
}

// "Zero seconds of a shift that exists" and "no shift at all" are different
// answers, and the SLA rules treat them differently.
func TestBusinessSecondsReportsNoCoverage(t *testing.T) {
	day := time.Date(2026, 9, 9, 0, 0, 0, 0, time.UTC)
	secs, covered := BusinessSeconds([]Window{win(9, 17)}, day.Add(2*time.Hour), day.Add(4*time.Hour))
	if secs != 0 {
		t.Fatalf("BusinessSeconds = %d, want 0", secs)
	}
	if covered {
		t.Error("an interval entirely outside every window is not covered")
	}
}

func TestScheduleAtFindsTheCoveringShift(t *testing.T) {
	day := time.Date(2026, 9, 9, 0, 0, 0, 0, time.UTC)
	w := win(9, 17)

	if got := ScheduleAt([]Window{w}, day.Add(10*time.Hour)); got == nil || got.ScheduleID != w.ScheduleID {
		t.Error("a moment inside the shift must resolve to it")
	}
	if got := ScheduleAt([]Window{w}, day.Add(20*time.Hour)); got != nil {
		t.Error("a moment outside every shift must resolve to nothing")
	}
}

func TestWindowsForNarrowsToOnePerson(t *testing.T) {
	me, other := uuid.New(), uuid.New()
	a, b := win(9, 12), win(9, 12)
	a.UserID, b.UserID = me, other

	got := WindowsFor([]Window{a, b}, me)
	if len(got) != 1 || got[0].UserID != me {
		t.Fatalf("got %d windows, want only mine", len(got))
	}
}
