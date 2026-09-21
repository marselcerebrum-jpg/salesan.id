package analytics

import (
	"time"

	"github.com/google/uuid"
)

// DefaultFollowUpGap is how long a chat must have been quiet before an admin
// writing into it counts as picking it back up rather than continuing.
//
// Six hours, because the alternative boundaries are both wrong: "same day"
// would miss an evening follow-up on a morning conversation, and a few minutes
// would count an admin's own second thought as a follow-up.
const DefaultFollowUpGap = 6 * time.Hour

// FollowUpConfig tunes the gap rule.
type FollowUpConfig struct {
	Gap time.Duration
}

// FollowUp is one day on which an admin restarted a conversation.
//
// Keyed by (conversation, local date) because that is the rule: several admin
// messages to the same contact on the same day are one follow-up activity, not
// several. TriggerMessageID keeps the message that opened it, so the
// classification can be audited rather than merely trusted.
type FollowUp struct {
	LocalDate         time.Time
	TriggerMessageID  uuid.UUID
	StartedAt         time.Time
	MessageCount      int
	GapSeconds        int
	LastInboundAt     *time.Time
	AdminID           *uuid.UUID
	AdminSource       string
	RespondedAt       *time.Time
	ResponseMessageID *uuid.UUID
}

// ComputeFollowUps finds every follow-up activity in one conversation.
//
// A manual admin message is a follow-up when all three hold:
//
//   - The conversation already existed. An admin's very first message to a new
//     contact is outreach, not a follow-up.
//   - Nothing was waiting for an answer. If a customer message is unanswered,
//     the admin's message is a reply — that is the SLA's business, not this
//     one's.
//   - The chat had gone quiet for at least the configured gap, or had never had
//     an inbound message at all.
//
// Broadcast, story, bot and system messages are excluded by IsManualReply, so a
// campaign that lands in a dormant chat is never reported as somebody having
// followed it up.
func ComputeFollowUps(timeline []Message, cfg FollowUpConfig) []FollowUp {
	if cfg.Gap <= 0 {
		cfg.Gap = DefaultFollowUpGap
	}

	sorted := SortTimeline(timeline)

	var (
		out           []FollowUp
		byDate        = map[time.Time]int{} // local date -> index into out
		lastInbound   *time.Time
		awaitingReply bool
		seenAny       bool
	)

	for _, m := range sorted {
		if !m.Countable() {
			continue
		}

		if m.NeedsReply() {
			at := m.Timestamp
			lastInbound = &at
			awaitingReply = true
			seenAny = true
			continue
		}

		if !m.IsManualReply() {
			// Still counts as prior activity in the thread — a broadcast having
			// been sent means the conversation is not brand new — but it can
			// never be the follow-up itself.
			seenAny = seenAny || m.FromMe
			continue
		}

		// Read before it is cleared: whether a customer was waiting is a fact
		// about the moment just before this message, not after it.
		isFollowUp := seenAny && !awaitingReply &&
			(lastInbound == nil || m.Timestamp.Sub(*lastInbound) >= cfg.Gap)
		awaitingReply = false

		if isFollowUp {
			date := LocalDate(m.Timestamp)
			if idx, ok := byDate[date]; ok {
				out[idx].MessageCount++
			} else {
				gap := 0
				if lastInbound != nil {
					gap = int(m.Timestamp.Sub(*lastInbound).Seconds())
				}
				out = append(out, FollowUp{
					LocalDate:        date,
					TriggerMessageID: m.ID,
					StartedAt:        m.Timestamp,
					MessageCount:     1,
					GapSeconds:       gap,
					LastInboundAt:    lastInbound,
					AdminID:          m.SentBy,
					AdminSource:      m.Source,
				})
				byDate[date] = len(out) - 1
			}
		}

		seenAny = true
	}

	attachFollowUpResponses(out, sorted)
	return out
}

// attachFollowUpResponses links each follow-up to the customer's answer.
//
// The first inbound message after the follow-up started, and only if it lands
// before the next follow-up — otherwise a reply three weeks later would be
// credited to the wrong day's work.
func attachFollowUpResponses(followUps []FollowUp, sorted []Message) {
	for i := range followUps {
		limit := time.Time{}
		if i+1 < len(followUps) {
			limit = followUps[i+1].StartedAt
		}
		for _, m := range sorted {
			if !m.NeedsReply() || !m.Timestamp.After(followUps[i].StartedAt) {
				continue
			}
			if !limit.IsZero() && !m.Timestamp.Before(limit) {
				break
			}
			at := m.Timestamp
			id := m.ID
			followUps[i].RespondedAt = &at
			followUps[i].ResponseMessageID = &id
			break
		}
	}
}
