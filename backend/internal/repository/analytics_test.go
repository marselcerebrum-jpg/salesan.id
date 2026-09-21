package repository

import (
	"testing"
	"time"

	"github.com/salesan/omnichannel/backend/internal/analytics"
	"github.com/salesan/omnichannel/backend/internal/models"
)

// The divisor behind every per-day average.
//
// Worth its own test because the failure is silent and plausible: on the third
// of the month, dividing a month-to-date figure by thirty-one reports a third
// of the real rate, and the number still looks like a number.
func TestElapsedDays(t *testing.T) {
	now := time.Now().In(analytics.Jakarta)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, analytics.Jakarta)

	cases := []struct {
		name string
		from time.Time
		to   time.Time
		want int
	}{
		{
			name: "satu hari",
			from: today,
			to:   today.AddDate(0, 0, 1),
			want: 1,
		},
		{
			name: "tujuh hari yang sudah lewat",
			from: today.AddDate(0, 0, -7),
			to:   today,
			want: 7,
		},
		{
			// The case the function exists for: a month-to-date period runs to
			// the end of the month, but only the days that happened count.
			name: "bulan berjalan tidak menghitung tanggal yang belum terjadi",
			from: time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, analytics.Jakarta),
			to:   time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, analytics.Jakarta).AddDate(0, 1, 0),
			want: now.Day(),
		},
		{
			name: "periode yang seluruhnya di masa depan tetap satu",
			from: today.AddDate(0, 0, 3),
			to:   today.AddDate(0, 0, 10),
			want: 1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := elapsedDays(tc.from, tc.to); got != tc.want {
				t.Errorf("got %d, want %d", got, tc.want)
			}
		})
	}

	if got := elapsedDays(time.Time{}, time.Time{}); got != 0 {
		t.Errorf("an unbounded period has no divisor, got %d", got)
	}
}

// A quiet day is still a day. Averaging over "days on which something
// happened" would make somebody who worked once and rested six times look
// seven times better than somebody who worked steadily.
func TestElapsedDaysCountsQuietDaysToo(t *testing.T) {
	now := time.Now().In(analytics.Jakarta)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, analytics.Jakarta)

	got := elapsedDays(today.AddDate(0, 0, -9), today.AddDate(0, 0, 1))
	if got != 10 {
		t.Fatalf("got %d, want 10 — every calendar day in the window counts", got)
	}
}

// Counts add; the derived figures do not.
//
// addDay folds the daily rows into the period total, and the distinction it has
// to keep is which figures may be summed. A count of events may. An average, a
// median, an extreme, and a distinct count may not — those are recomputed over
// the whole range, and summing them would be wrong in four different ways.
func TestAddDaySumsCountsAndLeavesDerivedFiguresAlone(t *testing.T) {
	var sum models.DashboardSummary

	fast30, fast90 := 30, 90
	day1 := models.DashboardSummary{
		InboundPersonal:        10,
		OutboundManualPersonal: 4,
		SLAAchieved:            2,
		SLABreached:            1,
		SLACompleted:           3,
		FollowUps:              5,
		BroadcastsSent:         1,
		StoryViewsDetected:     20,
		FastestResponseSeconds: &fast30,
	}
	day2 := models.DashboardSummary{
		InboundPersonal:        6,
		OutboundManualPersonal: 3,
		SLAAchieved:            4,
		SLABreached:            0,
		SLACompleted:           4,
		FollowUps:              2,
		BroadcastsSent:         2,
		StoryViewsDetected:     5,
		FastestResponseSeconds: &fast90,
	}

	addDay(&sum, &day1)
	addDay(&sum, &day2)

	if sum.InboundPersonal != 16 || sum.OutboundManualPersonal != 7 {
		t.Errorf("message counts must add: got %d in, %d out",
			sum.InboundPersonal, sum.OutboundManualPersonal)
	}
	if sum.SLAAchieved != 6 || sum.SLABreached != 1 {
		t.Errorf("SLA outcomes must add: got %d/%d", sum.SLAAchieved, sum.SLABreached)
	}
	if sum.FollowUps != 7 {
		t.Errorf("follow-ups must add: got %d", sum.FollowUps)
	}
	if sum.BroadcastsSent != 3 || sum.StoryViewsDetected != 25 {
		t.Errorf("campaign counts must add: got %d sent, %d views",
			sum.BroadcastsSent, sum.StoryViewsDetected)
	}

	// The two that must NOT be folded here. fillRangeResponse recomputes both
	// over the whole range so that the average, the median, the extremes and
	// the denominator all describe exactly the same set of cycles.
	if sum.SLACompleted != 0 {
		t.Errorf("sla_completed must be recomputed over the range, not summed; got %d", sum.SLACompleted)
	}
	if sum.FastestResponseSeconds != nil {
		t.Error("the fastest response is not the sum of daily fastests")
	}
}

// Contact figures are distinct counts, so they are deliberately absent from
// addDay: the same person writing on two days is one contact for the month.
func TestAddDayLeavesDistinctContactsToTheRangeQuery(t *testing.T) {
	var sum models.DashboardSummary
	day := models.DashboardSummary{ContactsInbound: 5, ContactsServed: 4, GroupsActive: 2}

	addDay(&sum, &day)
	addDay(&sum, &day)

	if sum.ContactsInbound != 0 || sum.ContactsServed != 0 || sum.GroupsActive != 0 {
		t.Fatalf("distinct counts must not be summed from daily rows: got in=%d served=%d groups=%d",
			sum.ContactsInbound, sum.ContactsServed, sum.GroupsActive)
	}
}
