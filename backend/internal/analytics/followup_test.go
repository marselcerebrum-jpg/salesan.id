package analytics

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestFollowUpAfterQuietPeriod(t *testing.T) {
	timeline := []Message{
		inbound(0),
		outbound(5*time.Minute, SourceWebAdmin),
		// Next day, admin picks it back up with nothing new from the customer.
		outbound(26*time.Hour, SourceWebAdmin),
	}

	got := ComputeFollowUps(timeline, FollowUpConfig{})
	if len(got) != 1 {
		t.Fatalf("got %d follow-ups, want 1", len(got))
	}
	if got[0].GapSeconds < int(24*time.Hour.Seconds()) {
		t.Errorf("gap = %ds, want at least a day", got[0].GapSeconds)
	}
}

// A reply to a waiting customer is a reply, not a follow-up. Counting it as
// both would make every answered chat look like proactive outreach.
func TestFollowUpNotCountedWhenCustomerIsWaiting(t *testing.T) {
	timeline := []Message{
		inbound(0),
		outbound(5*time.Minute, SourceWebAdmin),
		inbound(30 * time.Hour),
		outbound(31*time.Hour, SourceWebAdmin),
	}

	if got := ComputeFollowUps(timeline, FollowUpConfig{}); len(got) != 0 {
		t.Fatalf("got %d follow-ups, want none: %+v", len(got), got)
	}
}

// The first message an admin ever sends to a contact is outreach. There is no
// earlier session to follow up on.
func TestFollowUpNotCountedOnFirstContact(t *testing.T) {
	timeline := []Message{outbound(0, SourceWebAdmin)}
	if got := ComputeFollowUps(timeline, FollowUpConfig{}); len(got) != 0 {
		t.Fatalf("got %d follow-ups, want none", len(got))
	}
}

func TestFollowUpSeveralMessagesSameDayIsOneActivity(t *testing.T) {
	timeline := []Message{
		inbound(0),
		outbound(5*time.Minute, SourceWebAdmin),
		outbound(26*time.Hour, SourceWebAdmin),
		outbound(26*time.Hour+2*time.Minute, SourceWebAdmin),
		outbound(26*time.Hour+9*time.Minute, SourceWebAdmin),
	}

	got := ComputeFollowUps(timeline, FollowUpConfig{})
	if len(got) != 1 {
		t.Fatalf("got %d follow-ups, want 1", len(got))
	}
	if got[0].MessageCount != 3 {
		t.Errorf("MessageCount = %d, want 3", got[0].MessageCount)
	}
}

func TestFollowUpOnSeparateDaysAreSeparateActivities(t *testing.T) {
	timeline := []Message{
		inbound(0),
		outbound(5*time.Minute, SourceWebAdmin),
		outbound(26*time.Hour, SourceWebAdmin),
		outbound(50*time.Hour, SourceWebAdmin),
	}

	if got := ComputeFollowUps(timeline, FollowUpConfig{}); len(got) != 2 {
		t.Fatalf("got %d follow-ups, want 2", len(got))
	}
}

// A broadcast reaching a dormant chat is a campaign, not somebody following it
// up, and must never appear in anyone's follow-up figure.
func TestFollowUpExcludesBroadcastAndAutomation(t *testing.T) {
	for _, source := range []string{SourceBroadcast, SourceStory, SourceBot, SourceSystem} {
		timeline := []Message{
			inbound(0),
			outbound(5*time.Minute, SourceWebAdmin),
			outbound(40*time.Hour, source),
		}
		if got := ComputeFollowUps(timeline, FollowUpConfig{}); len(got) != 0 {
			t.Errorf("source %s produced %d follow-ups, want none", source, len(got))
		}
	}
}

func TestFollowUpRecordsTheCustomerAnswer(t *testing.T) {
	answer := inbound(27 * time.Hour)
	timeline := []Message{
		inbound(0),
		outbound(5*time.Minute, SourceWebAdmin),
		outbound(26*time.Hour, SourceWebAdmin),
		answer,
	}

	got := ComputeFollowUps(timeline, FollowUpConfig{})
	if len(got) != 1 {
		t.Fatalf("got %d follow-ups, want 1", len(got))
	}
	if got[0].ResponseMessageID == nil || *got[0].ResponseMessageID != answer.ID {
		t.Error("the customer's answer was not linked to the follow-up")
	}
}

func TestFollowUpIsOrderIndependent(t *testing.T) {
	a := inbound(0)
	b := outbound(5*time.Minute, SourceWebAdmin)
	c := outbound(26*time.Hour, SourceWebAdmin)

	ordered := ComputeFollowUps([]Message{a, b, c}, FollowUpConfig{})
	shuffled := ComputeFollowUps([]Message{c, a, b}, FollowUpConfig{})

	if len(ordered) != len(shuffled) || len(ordered) != 1 {
		t.Fatalf("got %d and %d, want 1 each", len(ordered), len(shuffled))
	}
	if ordered[0].TriggerMessageID != shuffled[0].TriggerMessageID {
		t.Error("shuffling the timeline changed which message triggered it")
	}
}

func TestFollowUpGapIsConfigurable(t *testing.T) {
	timeline := []Message{
		inbound(0),
		outbound(5*time.Minute, SourceWebAdmin),
		outbound(2*time.Hour, SourceWebAdmin),
	}

	if got := ComputeFollowUps(timeline, FollowUpConfig{}); len(got) != 0 {
		t.Fatalf("two hours is inside the default six-hour gap; got %d", len(got))
	}
	if got := ComputeFollowUps(timeline, FollowUpConfig{Gap: time.Hour}); len(got) != 1 {
		t.Fatalf("with a one-hour gap it is a follow-up; got %d", len(got))
	}
}

func TestLocalDateUsesJakarta(t *testing.T) {
	// 17:30 UTC is already the next day in WIB. A report bucketed in UTC would
	// file the evening's work under yesterday.
	at := time.Date(2026, 9, 9, 17, 30, 0, 0, time.UTC)
	got := LocalDate(at)
	if got.Day() != 10 || got.Month() != time.September {
		t.Fatalf("LocalDate = %s, want 2026-09-10 WIB", got.Format("2006-01-02"))
	}
}

func TestSortTimelineIsStable(t *testing.T) {
	same := time.Now()
	a := Message{ID: uuid.MustParse("00000000-0000-0000-0000-00000000000a"), Timestamp: same}
	b := Message{ID: uuid.MustParse("00000000-0000-0000-0000-00000000000b"), Timestamp: same}

	if got := SortTimeline([]Message{b, a}); got[0].ID != a.ID {
		t.Error("ties must break on ID so the order is reproducible")
	}
}
