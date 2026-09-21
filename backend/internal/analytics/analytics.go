// Package analytics turns a conversation's message timeline into the
// operational facts the Dashboard and the Performa page report on: SLA cycles,
// follow-up activity, group mention responses, and lead classification.
//
// Everything here is a pure function over an in-memory timeline. That is
// deliberate. These rules decide what a person's working day looks like in a
// report, so they have to be testable without a database, reproducible when a
// conversation is recomputed, and identical whether a message arrived live or
// was replayed by a history sync three days later.
//
// The repository layer loads the timeline, calls in here, and persists what
// comes back. It never re-implements a rule.
package analytics

import (
	"sort"
	"time"

	"github.com/google/uuid"
)

// Sender sources, mirroring the public.sender_source enum.
//
// The distinction that matters most is between a human at a keyboard and
// everything else: only the first two close an SLA cycle or count as a
// follow-up.
const (
	SourceWebAdmin       = "web_admin"
	SourceWhatsAppDevice = "whatsapp_device"
	SourceBot            = "bot"
	SourceSystem         = "system"
	SourceBroadcast      = "broadcast"
	SourceStory          = "story"
)

// SLA cycle states, mirroring the public.sla_status enum.
const (
	StatusWaiting  = "waiting"
	StatusAchieved = "achieved"
	StatusBreached = "breached"
	// StatusQueued is a message that arrived outside every working window. Not
	// a breach and not an achievement: nobody was on shift when it came in.
	// It is measured as a queue — how long after the shift opened it was
	// cleared — and counted apart from the SLA figures.
	StatusQueued   = "queued"
	StatusExcluded = "excluded"
)

// Lead states, mirroring the public.lead_status enum.
const (
	LeadVerifiedNew = "verified_new"
	LeadHistorical  = "historical"
	LeadUnknown     = "unknown"
)

// Jakarta is the workspace's reporting timezone. Every "today", every daily
// bucket, and every follow-up date is decided in it — not in the server's
// local zone, which is an accident of where the process happens to run.
var Jakarta = mustLoad("Asia/Jakarta")

func mustLoad(name string) *time.Location {
	// A Go binary on Windows may have no tzdata on disk. The import of
	// time/tzdata in the server's main package embeds it; falling back to a
	// fixed +07:00 offset keeps this package usable in tests either way.
	if loc, err := time.LoadLocation(name); err == nil {
		return loc
	}
	return time.FixedZone("WIB", 7*60*60)
}

// Message is one row of a conversation's timeline, reduced to the fields these
// rules actually consult.
type Message struct {
	ID        uuid.UUID
	Timestamp time.Time
	FromMe    bool
	// Source is the sender_source of an outgoing message; empty for inbound.
	Source string
	SentBy *uuid.UUID
	Type   string
	// MentionsMe is WhatsApp's own metadata resolved against the receiving
	// account, not a guess made from the text.
	MentionsMe     bool
	ParticipantJID string
	SenderPhone    string
	// Hidden and Revoked messages stay in the table but are no longer part of
	// the conversation, so they neither start nor close anything.
	Hidden  bool
	Revoked bool
}

// Message types that are bookkeeping rather than conversation.
var nonConversational = map[string]struct{}{
	"system":      {},
	"reaction":    {},
	"unsupported": {},
}

// Countable reports whether a message is part of the conversation at all.
func (m Message) Countable() bool {
	if m.Hidden || m.Revoked {
		return false
	}
	_, skip := nonConversational[m.Type]
	return !skip
}

// NeedsReply reports whether an inbound message opens (or extends) a wait.
//
// A customer's reply to a broadcast is an ordinary inbound message and lands
// here like any other — which is the point: the broadcast is excluded from our
// outgoing metrics, the answer to it is not excluded from theirs.
func (m Message) NeedsReply() bool {
	return !m.FromMe && m.Countable()
}

// IsManualReply reports whether an outgoing message is a person answering.
//
// A message sent from the phone qualifies: a human wrote it, even though
// WhatsApp will not say which human. Bots, system notices, broadcasts and
// stories do not qualify, and must never close an SLA cycle — a campaign that
// happens to land in an unanswered chat is not a reply to it.
func (m Message) IsManualReply() bool {
	if !m.FromMe || !m.Countable() {
		return false
	}
	return m.Source == SourceWebAdmin || m.Source == SourceWhatsAppDevice
}

// SortTimeline orders messages oldest first, breaking ties on ID so the result
// is stable.
//
// Sorting rather than trusting the caller is what makes every rule in this
// package order-independent. WhatsApp delivers messages out of order after a
// reconnect, and a history sync inserts a week of them at once; a rule that
// depended on arrival order would produce a different answer each time the same
// conversation was recomputed.
func SortTimeline(in []Message) []Message {
	out := make([]Message, len(in))
	copy(out, in)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Timestamp.Equal(out[j].Timestamp) {
			return out[i].ID.String() < out[j].ID.String()
		}
		return out[i].Timestamp.Before(out[j].Timestamp)
	})
	return out
}

// LocalDate is the Asia/Jakarta calendar date a moment falls on.
func LocalDate(t time.Time) time.Time {
	local := t.In(Jakarta)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, Jakarta)
}
