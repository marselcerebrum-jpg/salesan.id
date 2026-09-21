package analytics

import (
	"time"

	"github.com/google/uuid"
)

// SLAConfig is how a workspace wants response time measured.
type SLAConfig struct {
	// TargetSeconds is the promise: answered within this, the cycle is met.
	TargetSeconds int
	// UseBusinessHours decides which duration is scored. With it on, only time
	// inside a working window counts, so a message that arrives at 2am does not
	// consume the target while nobody is on shift.
	UseBusinessHours bool
	// Windows are the working blocks covering the period being computed.
	Windows []Window
}

// Cycle is one wait-then-answer round on a personal chat.
//
// Its identity is InboundMessageID — the first unanswered customer message.
// Several customer messages in a row before an answer are one cycle, not
// several, and keying on the first of them is what makes recomputing a
// conversation produce the same rows rather than a new set every time.
type Cycle struct {
	InboundMessageID uuid.UUID
	StartedAt        time.Time
	// InboundCount is how many customer messages piled up before the answer.
	// Context, not a second cycle: waiting forty minutes after five messages
	// reads differently from waiting forty minutes after one.
	InboundCount int

	FirstResponseMessageID  *uuid.UUID
	RespondedAt             *time.Time
	RawDurationSeconds      *int
	BusinessDurationSeconds *int

	TargetSeconds    int
	Status           string
	ExclusionReason  string
	ResponderAdminID *uuid.UUID
	ResponderSource  string
	ScheduleID       *uuid.UUID
}

// ComputeSLACycles derives every SLA cycle in one personal conversation.
//
// The rules, in the order they are applied:
//
//   - A cycle opens on an inbound message when none is already open. Reading
//     the chat does not open, close, or affect a cycle at all — WhatsApp's read
//     receipt says the message was seen, which is not the same as answered.
//   - Consecutive inbound messages extend the open cycle instead of starting
//     another, and the FIRST of them stays the start time.
//   - Only a manual reply closes a cycle. A bot, a system notice, a broadcast
//     or a story landing in the chat leaves it open, because none of them
//     answered anybody.
//   - A cycle still open at the end of the timeline stays `waiting`. That is a
//     real state, not missing data: somebody is still waiting right now.
//
// The caller passes the whole conversation; ordering is normalised here.
func ComputeSLACycles(timeline []Message, cfg SLAConfig) []Cycle {
	if cfg.TargetSeconds <= 0 {
		cfg.TargetSeconds = DefaultTargetSeconds
	}

	var (
		cycles []Cycle
		open   *Cycle
	)

	for _, m := range SortTimeline(timeline) {
		switch {
		case m.NeedsReply():
			if open != nil {
				open.InboundCount++
				continue
			}
			cycles = append(cycles, Cycle{
				InboundMessageID: m.ID,
				StartedAt:        m.Timestamp,
				InboundCount:     1,
				TargetSeconds:    cfg.TargetSeconds,
				Status:           StatusWaiting,
			})
			open = &cycles[len(cycles)-1]

		case m.IsManualReply():
			if open == nil {
				continue // an admin writing first is outreach, not a response
			}
			closeCycle(open, m, cfg)
			open = nil
		}
	}

	// A cycle still unanswered belongs to the queue too when it arrived outside
	// working hours. Without this, a message that came in at 2am and has not
	// been answered at 3am counts as somebody keeping a customer waiting — on a
	// shift nobody was working.
	if cfg.UseBusinessHours {
		for i := range cycles {
			if cycles[i].Status != StatusWaiting {
				continue
			}
			if ScheduleAt(cfg.Windows, cycles[i].StartedAt) == nil && len(cfg.Windows) > 0 {
				cycles[i].Status = StatusQueued
				cycles[i].ExclusionReason = "pesan masuk di luar jam kerja"
			}
		}
	}

	return cycles
}

// DefaultTargetSeconds is the response-time promise used when a workspace has
// not set its own: fifteen minutes.
const DefaultTargetSeconds = 15 * 60

func closeCycle(c *Cycle, reply Message, cfg SLAConfig) {
	id := reply.ID
	at := reply.Timestamp

	c.FirstResponseMessageID = &id
	c.RespondedAt = &at
	c.ResponderAdminID = reply.SentBy
	c.ResponderSource = reply.Source

	raw := int(at.Sub(c.StartedAt).Seconds())
	if raw < 0 {
		raw = 0
	}
	c.RawDurationSeconds = &raw

	business, covered := BusinessSeconds(cfg.Windows, c.StartedAt, at)
	if len(cfg.Windows) == 0 {
		// No schedule was recorded for this period. Reporting zero business
		// seconds would make every unscheduled account look instantaneous, so
		// the honest fallback is the wall-clock figure.
		business = raw
		covered = true
	}
	c.BusinessDurationSeconds = &business

	if w := ScheduleAt(cfg.Windows, c.StartedAt); w != nil {
		id := w.ScheduleID
		c.ScheduleID = &id
	}

	scored := raw
	// No schedule recorded at all. Nothing here can say whether the message
	// arrived in hours or out of them, so the wall-clock fallback above stands
	// and the cycle is scored as it always was. Treating "we do not know the
	// hours" as "it arrived out of hours" would put every unscheduled account's
	// entire traffic into the queue.
	if cfg.UseBusinessHours && len(cfg.Windows) > 0 {
		// SLA is for a message that ARRIVED during working hours and was
		// ANSWERED during working hours. Both halves matter, and neither can be
		// inferred from the duration: a wait can overlap a shift without either
		// end of it being inside one.
		//
		// Anything else is not a verdict this system is entitled to give. Nobody
		// was on duty when the message came in, or nobody was on duty when it
		// was answered, and scoring it either way would praise or blame
		// somebody for a moment they were not being paid for.
		inHoursArrival := ScheduleAt(cfg.Windows, c.StartedAt) != nil
		inHoursReply := ScheduleAt(cfg.Windows, at) != nil

		switch {
		case !inHoursArrival:
			// The queue. Measured, but as its own question: business seconds
			// between arrival and reply is exactly "how long after the shift
			// opened this was cleared", because none of the time before the
			// shift counts.
			c.Status = StatusQueued
			c.ExclusionReason = "pesan masuk di luar jam kerja"
			return
		case !inHoursReply:
			c.Status = StatusExcluded
			c.ExclusionReason = "dibalas di luar jam kerja"
			return
		case !covered:
			c.Status = StatusExcluded
			c.ExclusionReason = "seluruh waktu tunggu berada di luar jam kerja"
			return
		}
		scored = business
	}

	if scored <= c.TargetSeconds {
		c.Status = StatusAchieved
	} else {
		c.Status = StatusBreached
	}
}
