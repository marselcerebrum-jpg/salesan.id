package analytics

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

var base = time.Date(2026, 9, 9, 2, 0, 0, 0, time.UTC) // 09:00 WIB

func inbound(offset time.Duration) Message {
	return Message{ID: uuid.New(), Timestamp: base.Add(offset), Type: "text"}
}

func outbound(offset time.Duration, source string) Message {
	m := Message{ID: uuid.New(), Timestamp: base.Add(offset), FromMe: true, Type: "text", Source: source}
	if source == SourceWebAdmin {
		id := uuid.New()
		m.SentBy = &id
	}
	return m
}

func TestSLAConsecutiveInboundIsOneCycle(t *testing.T) {
	// Three customer messages in a row, then one answer. That is one wait, and
	// it started with the first message — not the last, which would flatter the
	// number by however long the customer kept typing.
	first := inbound(0)
	timeline := []Message{
		first,
		inbound(2 * time.Minute),
		inbound(4 * time.Minute),
		outbound(10*time.Minute, SourceWebAdmin),
	}

	cycles := ComputeSLACycles(timeline, SLAConfig{TargetSeconds: 15 * 60})
	if len(cycles) != 1 {
		t.Fatalf("got %d cycles, want 1", len(cycles))
	}
	c := cycles[0]
	if c.InboundMessageID != first.ID {
		t.Error("cycle must be keyed on the first unanswered message")
	}
	if c.InboundCount != 3 {
		t.Errorf("InboundCount = %d, want 3", c.InboundCount)
	}
	if *c.RawDurationSeconds != 600 {
		t.Errorf("raw duration = %d, want 600", *c.RawDurationSeconds)
	}
	if c.Status != StatusAchieved {
		t.Errorf("status = %q, want achieved", c.Status)
	}
}

func TestSLABreachedPastTarget(t *testing.T) {
	cycles := ComputeSLACycles([]Message{
		inbound(0),
		outbound(40*time.Minute, SourceWebAdmin),
	}, SLAConfig{TargetSeconds: 15 * 60})

	if len(cycles) != 1 || cycles[0].Status != StatusBreached {
		t.Fatalf("got %+v, want one breached cycle", cycles)
	}
}

// The whole point of separating sender_source: none of these is a person
// answering, so none of them may close a customer's wait.
func TestSLAAutomatedMessagesNeverClose(t *testing.T) {
	for _, source := range []string{SourceBot, SourceSystem, SourceBroadcast, SourceStory} {
		cycles := ComputeSLACycles([]Message{
			inbound(0),
			outbound(1*time.Minute, source),
		}, SLAConfig{TargetSeconds: 15 * 60})

		if len(cycles) != 1 {
			t.Fatalf("source %s: got %d cycles, want 1", source, len(cycles))
		}
		if cycles[0].Status != StatusWaiting {
			t.Errorf("source %s closed the cycle; only a manual reply may", source)
		}
	}
}

// A reply typed on the phone is still a person answering, even though WhatsApp
// will not say which person.
func TestSLAPhoneReplyClosesButLeavesAdminUnattributed(t *testing.T) {
	cycles := ComputeSLACycles([]Message{
		inbound(0),
		outbound(5*time.Minute, SourceWhatsAppDevice),
	}, SLAConfig{TargetSeconds: 15 * 60})

	if len(cycles) != 1 || cycles[0].Status != StatusAchieved {
		t.Fatalf("got %+v, want one achieved cycle", cycles)
	}
	if cycles[0].ResponderAdminID != nil {
		t.Error("a phone reply must not be attributed to any admin")
	}
	if cycles[0].ResponderSource != SourceWhatsAppDevice {
		t.Errorf("responder source = %q", cycles[0].ResponderSource)
	}
}

func TestSLAUnansweredStaysWaiting(t *testing.T) {
	cycles := ComputeSLACycles([]Message{inbound(0)}, SLAConfig{TargetSeconds: 60})
	if len(cycles) != 1 || cycles[0].Status != StatusWaiting {
		t.Fatalf("got %+v, want one waiting cycle", cycles)
	}
	if cycles[0].RespondedAt != nil {
		t.Error("a waiting cycle must have no response time")
	}
}

// An admin writing into a chat nobody was waiting in opens nothing. That case
// belongs to follow-up, not to SLA.
func TestSLAOutboundFirstOpensNothing(t *testing.T) {
	cycles := ComputeSLACycles([]Message{
		outbound(0, SourceWebAdmin),
		outbound(1*time.Minute, SourceWebAdmin),
	}, SLAConfig{TargetSeconds: 60})

	if len(cycles) != 0 {
		t.Fatalf("got %d cycles, want none", len(cycles))
	}
}

func TestSLASecondCycleAfterAnswer(t *testing.T) {
	cycles := ComputeSLACycles([]Message{
		inbound(0),
		outbound(5*time.Minute, SourceWebAdmin),
		inbound(20 * time.Minute),
		outbound(21*time.Minute, SourceWebAdmin),
	}, SLAConfig{TargetSeconds: 15 * 60})

	if len(cycles) != 2 {
		t.Fatalf("got %d cycles, want 2", len(cycles))
	}
	for i, c := range cycles {
		if c.Status != StatusAchieved {
			t.Errorf("cycle %d status = %q, want achieved", i, c.Status)
		}
	}
}

// Messages arrive out of order after a reconnect and a history sync inserts a
// week of them at once. Recomputing must give the same answer either way.
func TestSLAIsOrderIndependent(t *testing.T) {
	first := inbound(0)
	second := inbound(3 * time.Minute)
	reply := outbound(9*time.Minute, SourceWebAdmin)

	ordered := ComputeSLACycles([]Message{first, second, reply}, SLAConfig{TargetSeconds: 900})
	shuffled := ComputeSLACycles([]Message{reply, second, first}, SLAConfig{TargetSeconds: 900})

	if len(ordered) != 1 || len(shuffled) != 1 {
		t.Fatalf("got %d and %d cycles, want 1 each", len(ordered), len(shuffled))
	}
	if ordered[0].InboundMessageID != shuffled[0].InboundMessageID ||
		*ordered[0].RawDurationSeconds != *shuffled[0].RawDurationSeconds {
		t.Error("shuffling the timeline changed the result")
	}
}

// A message that arrives before the shift opens is a queue item, not an SLA
// cycle — even though it is answered well inside the shift.
//
// This used to be scored on business seconds, which meant a busy night dragged
// down the day's SLA while nobody had been on duty to answer it.
func TestSLAArrivedBeforeShiftIsQueued(t *testing.T) {
	// Shift 09:00–17:00 WIB = 02:00–10:00 UTC. The message arrives at 09:00 WIB
	// and is answered at 10:00 WIB, but the shift only starts at 09:30 WIB.
	shiftStart := base.Add(30 * time.Minute)
	windows := []Window{{ScheduleID: uuid.New(), Start: shiftStart, End: base.Add(8 * time.Hour)}}

	cycles := ComputeSLACycles([]Message{
		inbound(0),
		outbound(60*time.Minute, SourceWebAdmin),
	}, SLAConfig{TargetSeconds: 40 * 60, UseBusinessHours: true, Windows: windows})

	c := cycles[0]
	if c.Status != StatusQueued {
		t.Errorf("status = %q, want queued: it arrived before the shift opened", c.Status)
	}
	if c.ExclusionReason == "" {
		t.Error("a queued cycle must say why it is not an SLA cycle")
	}
	// Both durations are still measured. The raw hour is what the customer
	// waited; the 30 business minutes are how long it took to clear once the
	// shift opened, which is the figure the queue card reports.
	if *c.RawDurationSeconds != 3600 {
		t.Errorf("raw = %d, want 3600", *c.RawDurationSeconds)
	}
	if *c.BusinessDurationSeconds != 1800 {
		t.Errorf("business = %d, want 1800", *c.BusinessDurationSeconds)
	}
}

// Arriving inside the shift and being answered inside it is the only shape that
// gets an SLA verdict.
func TestSLAInHoursBothEndsIsScored(t *testing.T) {
	windows := []Window{{ScheduleID: uuid.New(), Start: base, End: base.Add(8 * time.Hour)}}

	cycles := ComputeSLACycles([]Message{
		inbound(10 * time.Minute),
		outbound(20*time.Minute, SourceWebAdmin),
	}, SLAConfig{TargetSeconds: 15 * 60, UseBusinessHours: true, Windows: windows})

	if cycles[0].Status != StatusAchieved {
		t.Errorf("status = %q, want achieved", cycles[0].Status)
	}
}

// Answered after the shift closed. Not a breach and not an achievement: nobody
// was on duty at the moment the answer went out either.
func TestSLAAnsweredAfterShiftIsExcluded(t *testing.T) {
	windows := []Window{{ScheduleID: uuid.New(), Start: base, End: base.Add(time.Hour)}}

	cycles := ComputeSLACycles([]Message{
		inbound(10 * time.Minute),
		outbound(90*time.Minute, SourceWebAdmin),
	}, SLAConfig{TargetSeconds: 15 * 60, UseBusinessHours: true, Windows: windows})

	if cycles[0].Status != StatusExcluded {
		t.Fatalf("status = %q, want excluded", cycles[0].Status)
	}
	if cycles[0].ExclusionReason == "" {
		t.Error("an excluded cycle must say why")
	}
}

func TestSLAWaitEntirelyOffShiftIsQueued(t *testing.T) {
	// Shift is much later in the day; the whole wait happens before it.
	windows := []Window{{ScheduleID: uuid.New(), Start: base.Add(6 * time.Hour), End: base.Add(14 * time.Hour)}}

	cycles := ComputeSLACycles([]Message{
		inbound(0),
		outbound(30*time.Minute, SourceWebAdmin),
	}, SLAConfig{TargetSeconds: 900, UseBusinessHours: true, Windows: windows})

	if cycles[0].Status != StatusQueued {
		t.Fatalf("status = %q, want queued", cycles[0].Status)
	}
	if cycles[0].ExclusionReason == "" {
		t.Error("a queued cycle must say why")
	}
}

// Still unanswered, and it arrived out of hours: the queue, not somebody
// keeping a customer waiting on a shift nobody was working.
func TestSLAUnansweredOffShiftIsQueued(t *testing.T) {
	windows := []Window{{ScheduleID: uuid.New(), Start: base.Add(6 * time.Hour), End: base.Add(14 * time.Hour)}}

	cycles := ComputeSLACycles([]Message{
		inbound(0),
	}, SLAConfig{TargetSeconds: 900, UseBusinessHours: true, Windows: windows})

	if cycles[0].Status != StatusQueued {
		t.Fatalf("status = %q, want queued", cycles[0].Status)
	}
	if cycles[0].RespondedAt != nil {
		t.Error("a queued cycle with no reply must stay unanswered")
	}
}

// An account with no schedule at all must not be scored as if every answer were
// instantaneous.
func TestSLAWithoutScheduleFallsBackToWallClock(t *testing.T) {
	cycles := ComputeSLACycles([]Message{
		inbound(0),
		outbound(40*time.Minute, SourceWebAdmin),
	}, SLAConfig{TargetSeconds: 900, UseBusinessHours: true})

	c := cycles[0]
	if *c.BusinessDurationSeconds != *c.RawDurationSeconds {
		t.Errorf("business = %d, raw = %d; want them equal without a schedule",
			*c.BusinessDurationSeconds, *c.RawDurationSeconds)
	}
	if c.Status != StatusBreached {
		t.Errorf("status = %q, want breached", c.Status)
	}
}

func TestSLAIgnoresHiddenAndRevoked(t *testing.T) {
	hidden := inbound(0)
	hidden.Hidden = true
	revoked := inbound(1 * time.Minute)
	revoked.Revoked = true

	if cycles := ComputeSLACycles([]Message{hidden, revoked}, SLAConfig{}); len(cycles) != 0 {
		t.Fatalf("got %d cycles, want none", len(cycles))
	}
}
