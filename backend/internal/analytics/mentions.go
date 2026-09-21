package analytics

import (
	"time"

	"github.com/google/uuid"
)

// Mention is one message in a group that named this account, together with the
// reply that answered it, if there was one.
//
// Groups have no SLA — the request is explicit about that, and it is right: a
// group is a room, not a queue, and nobody owes every message in it an answer.
// What a Leader does need to see is which mentions were picked up and which
// were left, which is what this records.
type Mention struct {
	MessageID      uuid.UUID
	MentionedAt    time.Time
	ParticipantJID string
	SenderPhone    string

	RespondedAt       *time.Time
	ResponseMessageID *uuid.UUID
	ResponderAdminID  *uuid.UUID
	ResponderSource   string
}

// ComputeMentions pairs every mention of this account with the first manual
// admin message that followed it in the same group.
//
// "Responded" is not "seen". Opening the group marks the mention seen
// (mention_seen_at on the message, set by the chat UI); answering it is a
// message going the other way. Conflating the two would report a team as
// having handled everything it merely looked at.
func ComputeMentions(timeline []Message) []Mention {
	sorted := SortTimeline(timeline)

	var out []Mention
	for _, m := range sorted {
		if !m.Countable() || m.FromMe || !m.MentionsMe {
			continue
		}
		out = append(out, Mention{
			MessageID:      m.ID,
			MentionedAt:    m.Timestamp,
			ParticipantJID: m.ParticipantJID,
			SenderPhone:    m.SenderPhone,
		})
	}

	for i := range out {
		for _, m := range sorted {
			if !m.IsManualReply() || !m.Timestamp.After(out[i].MentionedAt) {
				continue
			}
			at := m.Timestamp
			id := m.ID
			out[i].RespondedAt = &at
			out[i].ResponseMessageID = &id
			out[i].ResponderAdminID = m.SentBy
			out[i].ResponderSource = m.Source
			break
		}
	}
	return out
}
