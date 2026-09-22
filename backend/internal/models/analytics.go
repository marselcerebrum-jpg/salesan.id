package models

import (
	"time"

	"github.com/google/uuid"
)

// Operational roles, mirroring the public.operational_role enum.
//
// Separate from the workspace role in User.Role, which answers a different
// question: that one says what a person may change, this one says what they are
// responsible for.
const (
	RoleLeader    = "leader"
	RolePIC       = "pic"
	RoleFreelance = "freelance"
)

// Sender sources, mirroring the public.sender_source enum.
const (
	SourceWebAdmin       = "web_admin"
	SourceWhatsAppDevice = "whatsapp_device"
	SourceBot            = "bot"
	SourceSystem         = "system"
	SourceBroadcast      = "broadcast"
	SourceStory          = "story"
)

// Campaign kinds and states, mirroring public.campaign_type / campaign_status.
const (
	CampaignStory     = "story"
	CampaignBroadcast = "broadcast"

	CampaignDraft     = "draft"
	CampaignScheduled = "scheduled"
	CampaignRunning   = "running"
	CampaignCompleted = "completed"
	CampaignFailed    = "failed"
	CampaignCancelled = "cancelled"
)

// OrgMember is one person in the operational hierarchy, with everything the
// org screen needs to draw a row.
type OrgMember struct {
	UserID    uuid.UUID `json:"user_id"`
	Email     string    `json:"email"`
	FullName  *string   `json:"full_name"`
	AvatarURL *string   `json:"avatar_url"`
	// WorkspaceRole is the ownership role (owner/admin/agent) from public.users.
	WorkspaceRole string `json:"workspace_role"`
	// Role is the operational role; empty when none has been assigned yet.
	Role     string `json:"role"`
	IsActive bool   `json:"is_active"`
	// PICUserID is the PIC a freelance reports to; null for anyone else.
	PICUserID    *uuid.UUID `json:"pic_user_id"`
	PICName      *string    `json:"pic_name"`
	Applications []AppRef   `json:"applications"`
	// FreelanceCount is how many people report to this PIC.
	FreelanceCount int       `json:"freelance_count"`
	CreatedAt      time.Time `json:"created_at"`
}

// AppRef is an application reduced to what a chip needs.
type AppRef struct {
	ID    uuid.UUID `json:"id"`
	Code  string    `json:"code"`
	Name  string    `json:"name"`
	Color string    `json:"color"`
}

// WorkSchedule is one block of scheduled duty.
type WorkSchedule struct {
	ID              uuid.UUID  `json:"id"`
	WorkspaceID     uuid.UUID  `json:"workspace_id"`
	UserID          uuid.UUID  `json:"user_id"`
	UserName        *string    `json:"user_name"`
	PICUserID       *uuid.UUID `json:"pic_user_id"`
	PICName         *string    `json:"pic_name"`
	ApplicationID   *uuid.UUID `json:"application_id"`
	ApplicationCode *string    `json:"application_code"`
	AccountID       *uuid.UUID `json:"account_id"`
	AccountName     *string    `json:"account_name"`
	// WorkDate is a calendar date in the schedule's own timezone, serialised as
	// YYYY-MM-DD. Null on a weekly pattern row.
	WorkDate *string `json:"work_date"`
	// Weekday is 0 (Sunday) to 6 on a weekly pattern row, null on a dated one.
	// Exactly one of the two is ever set: a row says either "this date" or
	// "every one of these days", never both.
	//
	// A dated row wins over the pattern for that person on that date, which is
	// how a public holiday or a swapped shift is written without disturbing the
	// pattern behind it.
	Weekday   *int      `json:"weekday"`
	StartsAt  string    `json:"starts_at"`
	EndsAt    string    `json:"ends_at"`
	Timezone  string    `json:"timezone"`
	IsActive  bool      `json:"is_active"`
	Note      *string   `json:"note"`
	CreatedAt time.Time `json:"created_at"`
}

// AnalyticsFilter narrows every dashboard and performance query.
//
// The From/To pair is always resolved to absolute UTC instants from WIB
// calendar dates before it reaches SQL, so a "day" means the same thing
// everywhere rather than depending on the server's clock.
type AnalyticsFilter struct {
	From time.Time
	To   time.Time
	// ChatType is "", "personal" or "group". Broadcast never enters either:
	// it is filtered by sender_source, not by conversation type.
	ChatType      string
	ApplicationID *uuid.UUID
	AccountID     *uuid.UUID
	PICUserID     *uuid.UUID
	AdminID       *uuid.UUID
	ScheduleID    *uuid.UUID
	// InSchedule narrows to work done inside the rota (true) or outside it
	// (false). Applied only where a row actually carries the flag — activity
	// history and the message drill-down. It is deliberately NOT applied to the
	// summary cards: an inbound message has no schedule flag at all, so a
	// "Pesan Masuk" filtered this way would silently mean something else.
	InSchedule *bool
	// WorkHoursOnly keeps only what happened inside the rota of whoever is
	// being read, incoming messages included.
	//
	// Deliberately not the same thing as InSchedule. That one reads a flag
	// stored on the row, which exists only where somebody can be credited for
	// the work — an inbound message carries none. This one asks a question about
	// the CLOCK: did this happen while somebody was on shift. It is answerable
	// for every row that has a timestamp, which is what makes "aktivitas
	// percakapan pada jam kerja" a thing that can be drawn at all.
	WorkHoursOnly bool
}

// DashboardSummary is one day's operational picture.
//
// Counts only. The request is explicit that no conversion rate is shown, and
// there is a good reason for it: a ratio hides the two numbers it came from,
// and those are what a Leader actually acts on.
type DashboardSummary struct {
	Date string `json:"date"`

	// --- chat pribadi -------------------------------------------------------
	//
	// Every message field counts BUBBLES, not conversations: three messages in a
	// row from a customer are three. Every contact field counts DISTINCT
	// contacts over the whole period, recomputed rather than summed from days —
	// the same person writing on two days is one contact for the month.

	// InboundPersonal is bubbles sent by customers in one-to-one chats.
	InboundPersonal int `json:"inbound_personal"`
	// OutboundManualPersonal is bubbles a person sent from the web. Broadcast,
	// bot, system and phone messages are all excluded: this is the figure a
	// PIC or Freelance is answerable for, and only the web can say who typed it.
	OutboundManualPersonal int `json:"outbound_manual_personal"`
	// OutboundDevicePersonal is bubbles sent from the phone itself. Real work,
	// but WhatsApp does not say who did it, so it is reported beside the figure
	// above rather than folded into it, and it is zero on any view about one
	// person: the phone's work belongs to the number, not to them.
	OutboundDevicePersonal int `json:"outbound_device_personal"`

	// ContactsInbound is distinct contacts who wrote at least once.
	ContactsInbound int `json:"contacts_inbound"`
	// ContactsInboundInSchedule and ContactsInboundOutOfSchedule split the
	// figure above by whether the contact's FIRST message of the period landed
	// inside a working window.
	//
	// Classified on the first message rather than on any message, because a
	// contact is one thing and belongs on one side: somebody who writes at 2am
	// and again at 10am arrived out of hours, and counting them twice would
	// make the two halves add up to more than the whole.
	//
	// The two always sum to ContactsInbound, which is kept as the true total
	// so nothing that already reads it changes meaning.
	ContactsInboundInSchedule    int `json:"contacts_inbound_in_schedule"`
	ContactsInboundOutOfSchedule int `json:"contacts_inbound_out_of_schedule"`
	// ScheduleConfigured is false when no working window covers this period at
	// all. Without it the split is unreadable: with no rota recorded every
	// contact falls outside one, and a card leading on the in-hours figure
	// would show zero and look like a collapse rather than a missing setting.
	ScheduleConfigured bool `json:"schedule_configured"`
	// ContactsServed is distinct contacts who got at least one manual reply.
	ContactsServed int `json:"contacts_served"`
	// ContactsUnserved is those who wrote and have no manual reply yet.
	ContactsUnserved int `json:"contacts_unserved"`

	// VerifiedNewLeads counts only contacts proven to be new; anything the
	// evidence cannot settle stays historical or unverified instead.
	VerifiedNewLeads int `json:"verified_new_leads"`

	// --- waktu respons & SLA -------------------------------------------------
	//
	// Personal chats only. A group has no single customer waiting for an answer,
	// and a Broadcast is not a question — applying a response target to either
	// would produce a number that means nothing.
	//
	// The two durations are null rather than zero when nothing was answered in
	// the period: "no cycle completed" and "answered instantly" are different
	// statements, and rendering both as 0 makes the wrong one look true.
	AvgFirstResponseSeconds    *int `json:"avg_first_response_seconds"`
	MedianFirstResponseSeconds *int `json:"median_first_response_seconds"`
	// FastestResponseSeconds and SlowestResponseSeconds come from COMPLETED
	// cycles only. A cycle still waiting has no duration yet, and letting one
	// in would make "terlama" mean "the oldest thing nobody has answered",
	// which is a different — and already separately reported — fact.
	FastestResponseSeconds *int `json:"fastest_response_seconds"`
	SlowestResponseSeconds *int `json:"slowest_response_seconds"`
	SLAAchieved            int  `json:"sla_achieved"`
	SLABreached            int  `json:"sla_breached"`
	// SLACompleted is the denominator for the achieved percentage: cycles that
	// have an answer. Waiting cycles are deliberately excluded — scoring a
	// conversation before it has been answered would let a busy hour look like
	// a failure it has not had time to become.
	SLACompleted int `json:"sla_completed"`
	// SLAWaiting is conversations still waiting for a first manual reply.
	SLAWaiting int `json:"sla_waiting"`
	// SLAConfigured is false when no response target has been set. The interface
	// then shows "Belum dikonfigurasi" rather than scoring against a hidden
	// default nobody agreed to.
	SLAConfigured bool `json:"sla_configured"`

	// --- antrean di luar jam kerja -------------------------------------------
	//
	// Messages that arrived when nobody was on shift. Deliberately NOT part of
	// any SLA figure above: the SLA asks how fast somebody on duty answers, and
	// a message that came in at 2am had nobody on duty to answer it. Folding the
	// two together made a busy night read as a failure, and hid a backlog that
	// took until noon to clear behind a healthy-looking SLA.
	//
	// Measured from the moment the shift opened, which is what the stored
	// business duration already means for these rows.

	// QueuedTotal is how many arrived outside working hours in this period.
	QueuedTotal int `json:"queued_total"`
	// QueuedAnswered is how many of them have been answered.
	QueuedAnswered int `json:"queued_answered"`
	// QueuedWaiting is how many are still unanswered.
	QueuedWaiting int `json:"queued_waiting"`
	// QueuedAvgSeconds and QueuedSlowestSeconds measure from the shift opening
	// to the reply. Null when none has been answered yet, for the same reason
	// the SLA durations are null: "nothing cleared" is not "cleared instantly".
	QueuedAvgSeconds     *int `json:"queued_avg_seconds"`
	QueuedSlowestSeconds *int `json:"queued_slowest_seconds"`

	// --- follow-up -----------------------------------------------------------
	//
	// Reaching back out to a contact who already had a conversation on an
	// earlier day. Counts only, never a rate: a ratio hides the two numbers it
	// came from, and those are what somebody acts on.
	FollowUps           int `json:"follow_ups"`
	FollowUpContacts    int `json:"follow_up_contacts"`
	FollowUpsAnswered   int `json:"follow_ups_answered"`
	FollowUpsUnanswered int `json:"follow_ups_unanswered"`

	// --- chat grup ----------------------------------------------------------
	//
	// Kept entirely apart from the personal figures above; nothing here is added
	// into them.

	// GroupInbound is bubbles from group members, excluding our own numbers.
	GroupInbound int `json:"group_inbound"`
	// GroupReplies is bubbles a person sent into a group from the web.
	GroupReplies int `json:"group_replies"`
	// GroupsActive is distinct groups with member activity.
	GroupsActive int `json:"groups_active"`
	// GroupsHandled is distinct groups that got at least one manual reply.
	GroupsHandled int `json:"groups_handled"`

	// --- label --------------------------------------------------------------
	ContactsFirstLabeled int `json:"contacts_first_labeled"`
	LabelChangesTotal    int `json:"label_changes_total"`
	LabelsAssigned       int `json:"labels_assigned"`
	LabelsRemoved        int `json:"labels_removed"`
	LabelsMoved          int `json:"labels_moved"`
	LabelContactsChanged int `json:"label_contacts_changed"`

	// --- aktivitas campaign -------------------------------------------------
	//
	// Counted as activity, never as chat: a Broadcast's outgoing messages carry
	// sender_source = 'broadcast' and are excluded from every figure above.
	BroadcastsCreated   int `json:"broadcasts_created"`
	BroadcastsScheduled int `json:"broadcasts_scheduled"`
	BroadcastsRunning   int `json:"broadcasts_running"`
	// BroadcastsSent is campaigns that finished completely. Partial runs are
	// their own figure because neither "selesai" nor "gagal" is true of them.
	BroadcastsSent      int `json:"broadcasts_sent"`
	BroadcastsPartial   int `json:"broadcasts_partial"`
	BroadcastsFailed    int `json:"broadcasts_failed"`
	BroadcastsCancelled int `json:"broadcasts_cancelled"`
	// BroadcastTargetsSent counts recipients, not campaigns.
	BroadcastTargetsSent int `json:"broadcast_targets_sent"`

	StoriesCreated   int `json:"stories_created"`
	StoriesScheduled int `json:"stories_scheduled"`
	StoriesRunning   int `json:"stories_running"`
	StoriesPublished int `json:"stories_published"`
	StoriesPartial   int `json:"stories_partial"`
	StoriesFailed    int `json:"stories_failed"`
	StoriesExpired   int `json:"stories_expired"`
	// StoryViewsDetected is a LOWER BOUND built from real receipts. whatsmeow
	// exposes no viewer list, so a viewer with read receipts off never appears.
	StoryViewsDetected int `json:"story_views_detected"`

	// --- jadwal -------------------------------------------------------------
	WorkSeconds int `json:"work_seconds"`
	ActiveDays  int `json:"active_days"`
	// ElapsedDays is how many calendar days of the chosen period have actually
	// happened, in WIB. It is the divisor for the per-day averages, and it is
	// computed here rather than in the browser so that "rata-rata bulan ini"
	// does not quietly divide by thirty-one on the third of the month.
	// Days with nothing on them still count; a quiet day is a real day.
	ElapsedDays int `json:"elapsed_days"`
	// ActivitiesInSchedule and ActivitiesOutOfSchedule compare when the work
	// happened against when it was scheduled. They never decide WHO did it —
	// that is always the logged-in account.
	ActivitiesInSchedule    int `json:"activities_in_schedule"`
	ActivitiesOutOfSchedule int `json:"activities_out_of_schedule"`
}

// PerformanceDay is one row of the daily breakdown on the Performa page. It
// carries the same shape as the summary so the table and the cards can never
// disagree about what a column means.
type PerformanceDay struct {
	Date string `json:"date"`
	DashboardSummary
}

// MemberBreakdown is one person's row in the per-person tables.
//
// Carries a full DashboardSummary rather than the smaller MemberMetrics, so the
// per-person table draws from the same column model as Rincian Per Hari and a
// metric cannot mean one thing in one table and another in the next.
//
// Scope says how the figures were narrowed, and it is not decoration: a PIC's
// row is their whole application, which is their own work plus their Freelance's
// plus the phone activity on those numbers, while everyone else's row is what
// their own account did. Reading a PIC's row as personal effort would be wrong,
// so the row says which it is.
type MemberBreakdown struct {
	UserID         *uuid.UUID `json:"user_id"`
	Name           string     `json:"name"`
	Email          string     `json:"email"`
	Role           string     `json:"role"`
	IsActive       bool       `json:"is_active"`
	OnDuty         bool       `json:"on_duty"`
	PICUserID      *uuid.UUID `json:"pic_user_id"`
	PICName        *string    `json:"pic_name"`
	FreelanceCount int        `json:"freelance_count"`
	Applications   []AppRef   `json:"applications"`
	// Scope is "team" for a PIC and "personal" for everyone else.
	Scope   string           `json:"scope"`
	Summary DashboardSummary `json:"summary"`
}

// ApplicationPerformance is one application's share of the view being read.
//
// Carries the same DashboardSummary as everything else on the page, so a row in
// the per-application table and the card above it are literally the same
// figures narrowed differently. A leaner struct with only the columns the table
// draws would have been cheaper to serialise and would have started drifting
// the first time a card gained a metric.
type ApplicationPerformance struct {
	Application AppRef           `json:"application"`
	Summary     DashboardSummary `json:"summary"`
	// ContactsTotal is how many contacts this application holds RIGHT NOW,
	// across every one of its numbers.
	//
	// Deliberately outside Summary: everything in there is a measurement of the
	// period being read, and this is a measurement of the address book as it
	// stands. Folding it in would mean a figure that ignores the date filter
	// sitting in a struct whose every other field obeys it, which is how a
	// reader ends up believing they gained 163 contacts today.
	ContactsTotal int `json:"contacts_total"`
}

// SLATarget is the response-time promise one application is measured against.
//
// ApplicationID nil is the workspace default, which every application without
// its own row falls back to. See migration 0035.
type SLATarget struct {
	ID               uuid.UUID  `json:"id"`
	ApplicationID    *uuid.UUID `json:"application_id"`
	ApplicationCode  *string    `json:"application_code"`
	ApplicationName  *string    `json:"application_name"`
	ApplicationColor *string    `json:"application_color"`
	TargetSeconds    int        `json:"target_seconds"`
	// BusinessHours limits the wait to scheduled working time. Counting the
	// clock straight through makes a 2am message breach before anybody could
	// have answered it.
	BusinessHours bool      `json:"business_hours"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// PerformanceReport is a month (or any range) plus its daily rows.
type PerformanceReport struct {
	From    string           `json:"from"`
	To      string           `json:"to"`
	Summary DashboardSummary `json:"summary"`
	Days    []PerformanceDay `json:"days"`
	// PerHour is the same effort expressed against scheduled time. Shown beside
	// the raw counts, never instead of them: comparing two people on totals
	// alone is unfair when one worked two hours and the other eight.
	PerHour PerHourMetrics `json:"per_hour"`

	// Previous is the same summary over the window of equal length immediately
	// before this one, so a figure can be read as a movement rather than only
	// as a level. Nil unless the caller asked with `compare=true`: it costs a
	// second pass over the same aggregates, and most callers do not want it.
	//
	// A delta is only ever shown when this is present. There is no fallback
	// that estimates one, because a made-up trend is worse than no trend.
	Previous     *DashboardSummary `json:"previous,omitempty"`
	PreviousFrom string            `json:"previous_from,omitempty"`
	PreviousTo   string            `json:"previous_to,omitempty"`
}

// PerHourMetrics are the rate figures. Null when nothing was scheduled, which
// is different from zero.
type PerHourMetrics struct {
	WorkHours           float64  `json:"work_hours"`
	MessagesPerHour     *float64 `json:"messages_per_hour"`
	ChatsPerHour        *float64 `json:"chats_per_hour"`
	GroupRepliesPerHour *float64 `json:"group_replies_per_hour"`
	FollowUpsPerHour    *float64 `json:"follow_ups_per_hour"`
}

// MemberMetrics is what one person did, as opposed to what happened.
//
// Deliberately a different shape from DashboardSummary rather than a reuse of
// it. Several figures there — inbound messages, active chats, contacts newly
// labelled — describe a workspace and cannot be attributed to anybody: nobody
// on our side wrote an inbound message. Reusing the same struct would leave
// those fields present and silently meaning something else on a person's row,
// which is the kind of quiet redefinition that makes a report untrustworthy.
//
// Every field here answers "what did this admin do", and every one of them is
// derived from a row that names them.
type MemberMetrics struct {
	// --- chat pribadi ---
	// OutboundManual is bubbles this person sent from the web.
	OutboundManual int `json:"outbound_manual"`
	// ContactsServed is distinct contacts this person replied to. On a team row
	// the same contact served by two people counts once, which is why team
	// totals are recomputed rather than summed.
	ContactsServed int `json:"contacts_served"`
	// VerifiedNewLeads is credited to whoever sent the first manual reply in
	// the contact's conversation. A lead is not "owned" by anybody in the data;
	// answering it first is the closest thing to having brought it in.
	VerifiedNewLeads int `json:"verified_new_leads"`

	// --- waktu respons & SLA ---
	//
	// Credited to the person who actually sent the first manual reply. A cycle
	// closed from a phone has no verifiable responder and is therefore counted
	// for the application and the device, never for an individual.
	AvgFirstResponseSeconds    *int `json:"avg_first_response_seconds"`
	MedianFirstResponseSeconds *int `json:"median_first_response_seconds"`
	SLAAchieved                int  `json:"sla_achieved"`
	SLABreached                int  `json:"sla_breached"`

	// --- follow-up ---
	FollowUps           int `json:"follow_ups"`
	FollowUpContacts    int `json:"follow_up_contacts"`
	FollowUpsAnswered   int `json:"follow_ups_answered"`
	FollowUpsUnanswered int `json:"follow_ups_unanswered"`

	// --- chat grup ---
	GroupReplies  int `json:"group_replies"`
	GroupsHandled int `json:"groups_handled"`

	// --- label ---
	LabelChanges  int `json:"label_changes"`
	LabelContacts int `json:"label_contacts"`

	// --- campaign ---
	BroadcastsCreated int `json:"broadcasts_created"`
	BroadcastsSent    int `json:"broadcasts_sent"`
	StoriesCreated    int `json:"stories_created"`
	StoriesPublished  int `json:"stories_published"`
	CampaignsFailed   int `json:"campaigns_failed"`

	// --- jadwal ---
	WorkSeconds             int `json:"work_seconds"`
	ActiveDays              int `json:"active_days"`
	ActivitiesInSchedule    int `json:"activities_in_schedule"`
	ActivitiesOutOfSchedule int `json:"activities_out_of_schedule"`

	// Rate figures, null when nothing was scheduled. Shown beside the counts,
	// never instead of them: ranking two people on totals alone is unfair when
	// one worked two hours and the other eight.
	MessagesPerHour     *float64 `json:"messages_per_hour"`
	ContactsPerHour     *float64 `json:"contacts_per_hour"`
	GroupRepliesPerHour *float64 `json:"group_replies_per_hour"`
	CampaignsPerHour    *float64 `json:"campaigns_per_hour"`
	FollowUpsPerHour    *float64 `json:"follow_ups_per_hour"`
}

// MemberPerformance is one row of the team table.
type MemberPerformance struct {
	// UserID is null for the unattributed bucket, which belongs to no person.
	UserID   *uuid.UUID `json:"user_id"`
	Name     string     `json:"name"`
	Email    string     `json:"email"`
	Role     string     `json:"role"`
	IsActive bool       `json:"is_active"`
	// OnDuty is whether a shift covers this moment.
	OnDuty       bool       `json:"on_duty"`
	PICUserID    *uuid.UUID `json:"pic_user_id"`
	PICName      *string    `json:"pic_name"`
	Applications []AppRef   `json:"applications"`
	// FreelanceCount is how many people report to this PIC.
	FreelanceCount int `json:"freelance_count"`

	// Personal is what this person did with their own hands.
	Personal MemberMetrics `json:"personal"`
	// Team is the PIC's own activity plus every Freelance under them. Null for
	// anyone who leads no team. Kept apart from Personal on purpose: a PIC whose
	// team is busy and who answers nothing themselves is a different situation
	// from one who carries the inbox alone, and one merged number hides which.
	Team *MemberMetrics `json:"team"`
}

// TeamReport is the per-person breakdown behind a period.
type TeamReport struct {
	From string `json:"from"`
	To   string `json:"to"`
	// Role of the caller, so the interface knows which tabs to offer.
	Role    string              `json:"role"`
	Members []MemberPerformance `json:"members"`
	// Unattributed is activity from a phone whose operator WhatsApp does not
	// identify. It is shown as its own row rather than divided among the people
	// who happened to be on shift, because dividing it would be a guess.
	Unattributed MemberMetrics `json:"unattributed"`
}

// MessageActivityRow is one message behind a chat figure.
//
// Carries the actor rather than inferring one: a message typed on the phone has
// no actor, and the row says so instead of naming whoever was on shift.
type MessageActivityRow struct {
	ID               uuid.UUID  `json:"id"`
	ConversationID   uuid.UUID  `json:"conversation_id"`
	ChatType         string     `json:"chat_type"`
	ConversationName *string    `json:"conversation_name"`
	PhoneNumber      *string    `json:"phone_number"`
	ApplicationCode  *string    `json:"application_code"`
	AccountName      *string    `json:"account_name"`
	Timestamp        time.Time  `json:"timestamp"`
	MessageType      string     `json:"message_type"`
	Preview          string     `json:"preview"`
	Source           *string    `json:"source"`
	ActorID          *uuid.UUID `json:"actor_id"`
	ActorName        *string    `json:"actor_name"`
	// InSchedule compares when it happened against the rota. Null for inbound
	// messages and for anything with no verifiable actor.
	InSchedule *bool `json:"in_schedule"`
}

// ActivityRow is one entry in a person's history.
//
// Deliberately one shape for five different kinds of event. The alternative —
// a union type per source — would push the job of deciding what to draw onto
// every screen that shows activity, and they would eventually disagree about
// what "status" means for a label change.
//
// The link fields are the point of the row. An activity nobody can follow back
// to the message, campaign or contact it describes is a claim, not a record;
// whichever of them is set says what this entry opens.
type ActivityRow struct {
	// ID is prefixed by source ("msg:", "lbl:", "fu:", "act:") so it stays
	// unique across a union of four tables.
	ID string `json:"id"`
	// Kind is what to draw: message_personal, message_group, label, follow_up,
	// broadcast, story.
	Kind string `json:"kind"`
	// Type is the machine detail within a kind, e.g. "label_assigned".
	Type       string    `json:"type"`
	OccurredAt time.Time `json:"occurred_at"`

	ActorID   *uuid.UUID `json:"actor_id"`
	ActorName *string    `json:"actor_name"`
	ActorRole *string    `json:"actor_role"`

	ApplicationID    *uuid.UUID `json:"application_id"`
	ApplicationCode  *string    `json:"application_code"`
	ApplicationName  *string    `json:"application_name"`
	ApplicationColor *string    `json:"application_color"`
	AccountID        *uuid.UUID `json:"account_id"`
	AccountName      *string    `json:"account_name"`

	Status *string `json:"status"`
	// InSchedule compares when it happened against the rota. Never used to
	// decide who did it.
	InSchedule *bool `json:"in_schedule"`
	// Subject is what the activity was about: a contact, a group, a campaign.
	Subject *string `json:"subject"`
	Detail  *string `json:"detail"`

	ConversationID *uuid.UUID `json:"conversation_id"`
	MessageID      *uuid.UUID `json:"message_id"`
	CampaignID     *uuid.UUID `json:"campaign_id"`
	ContactID      *uuid.UUID `json:"contact_id"`
	Source         *string    `json:"source"`
}

// SLACycleRow is one SLA cycle in the drill-down list.
type SLACycleRow struct {
	ID                      uuid.UUID  `json:"id"`
	ConversationID          uuid.UUID  `json:"conversation_id"`
	ConversationName        *string    `json:"conversation_name"`
	PhoneNumber             *string    `json:"phone_number"`
	ApplicationCode         *string    `json:"application_code"`
	AccountName             *string    `json:"account_name"`
	InboundMessageID        uuid.UUID  `json:"inbound_message_id"`
	StartedAt               time.Time  `json:"started_at"`
	InboundMessageCount     int        `json:"inbound_message_count"`
	FirstResponseMessageID  *uuid.UUID `json:"first_response_message_id"`
	RespondedAt             *time.Time `json:"responded_at"`
	RawDurationSeconds      *int       `json:"raw_duration_seconds"`
	BusinessDurationSeconds *int       `json:"business_duration_seconds"`
	TargetSeconds           int        `json:"target_seconds"`
	Status                  string     `json:"status"`
	ExclusionReason         *string    `json:"exclusion_reason"`
	ResponderAdminID        *uuid.UUID `json:"responder_admin_id"`
	ResponderName           *string    `json:"responder_name"`
	ResponderSource         *string    `json:"responder_source"`

	// The ids a deep link into the inbox needs. The chat route is
	// /chat/{applicationId}/{accountId} with the thread chosen inside it, so all
	// three travel together — a link built from the phone number alone opens the
	// wrong room whenever a customer has written to two of our numbers.
	ApplicationID    *uuid.UUID `json:"application_id"`
	AccountID        *uuid.UUID `json:"account_id"`
	ApplicationColor *string    `json:"application_color"`
	// WaitingSeconds is how long this cycle has been open, measured on the
	// server. Null once it has been answered.
	WaitingSeconds *int `json:"waiting_seconds"`
	// Breached is whether the wait has already passed the target this cycle was
	// created with. Null when it is not waiting.
	Breached *bool `json:"breached"`
}

// FollowUpRow is one follow-up activity in the drill-down list.
type FollowUpRow struct {
	ID               uuid.UUID  `json:"id"`
	ConversationID   uuid.UUID  `json:"conversation_id"`
	ConversationName *string    `json:"conversation_name"`
	PhoneNumber      *string    `json:"phone_number"`
	ApplicationCode  *string    `json:"application_code"`
	LocalDate        string     `json:"local_date"`
	StartedAt        time.Time  `json:"started_at"`
	MessageCount     int        `json:"message_count"`
	GapSeconds       *int       `json:"gap_seconds"`
	LastInboundAt    *time.Time `json:"last_inbound_at"`
	AdminID          *uuid.UUID `json:"admin_id"`
	AdminName        *string    `json:"admin_name"`
	AdminSource      *string    `json:"admin_source"`
	RespondedAt      *time.Time `json:"responded_at"`
	TriggerMessageID uuid.UUID  `json:"trigger_message_id"`
}

// GroupMentionRow is one mention of a connected number inside a group.
type GroupMentionRow struct {
	ID               uuid.UUID  `json:"id"`
	ConversationID   uuid.UUID  `json:"conversation_id"`
	GroupName        *string    `json:"group_name"`
	ApplicationCode  *string    `json:"application_code"`
	AccountName      *string    `json:"account_name"`
	MessageID        uuid.UUID  `json:"message_id"`
	ParticipantJID   *string    `json:"participant_jid"`
	SenderPhone      *string    `json:"sender_phone"`
	SenderName       *string    `json:"sender_name"`
	Body             *string    `json:"body"`
	MentionedAt      time.Time  `json:"mentioned_at"`
	RespondedAt      *time.Time `json:"responded_at"`
	ResponderAdminID *uuid.UUID `json:"responder_admin_id"`
	ResponderName    *string    `json:"responder_name"`
	ResponderSource  *string    `json:"responder_source"`
}

// LabelEventRow is one entry of the append-only label history.
type LabelEventRow struct {
	ID              uuid.UUID  `json:"id"`
	EventType       string     `json:"event_type"`
	ContactID       *uuid.UUID `json:"contact_id"`
	ContactName     *string    `json:"contact_name"`
	PhoneNumber     *string    `json:"phone_number"`
	ConversationID  *uuid.UUID `json:"conversation_id"`
	FromLabelName   *string    `json:"from_label_name"`
	ToLabelName     *string    `json:"to_label_name"`
	Source          string     `json:"source"`
	AdminID         *uuid.UUID `json:"admin_id"`
	AdminName       *string    `json:"admin_name"`
	ApplicationCode *string    `json:"application_code"`
	AccountName     *string    `json:"account_name"`
	OccurredAt      time.Time  `json:"occurred_at"`
}

// LabelUsage is one label's activity over the period.
//
// Attach and detach are counted separately rather than netted: a label put on
// forty contacts and taken off thirty-eight is not "two", it is seventy-eight
// decisions somebody made, and the two numbers are what a Leader acts on.
type LabelUsage struct {
	LabelID   *uuid.UUID `json:"label_id"`
	Name      string     `json:"name"`
	Color     string     `json:"color"`
	Assigned  int        `json:"assigned"`
	Removed   int        `json:"removed"`
	// Contacts is how many distinct contacts this label was moved on.
	Contacts int `json:"contacts"`
	// ActiveContacts is how many currently carry it, which is a fact about now
	// rather than about the period.
	ActiveContacts int `json:"active_contacts"`
}

// LabelTransition is one "Cold → Warm"-style move with its count.
type LabelTransition struct {
	FromLabel string `json:"from_label"`
	ToLabel   string `json:"to_label"`
	Count     int    `json:"count"`
	Contacts  int    `json:"contacts"`
}

// LeadRow is one classified contact.
type LeadRow struct {
	ContactID       uuid.UUID  `json:"contact_id"`
	Name            *string    `json:"name"`
	PhoneNumber     *string    `json:"phone_number"`
	ApplicationCode *string    `json:"application_code"`
	AccountName     *string    `json:"account_name"`
	LeadStatus      string     `json:"lead_status"`
	StatusReason    string     `json:"status_reason"`
	QualifiedDate   *string    `json:"qualified_date"`
	FirstInboundAt  *time.Time `json:"first_inbound_at"`
	ConversationID  *uuid.UUID `json:"conversation_id"`
}

// Campaign is one Story or Broadcast.
type Campaign struct {
	ID              uuid.UUID   `json:"id"`
	WorkspaceID     uuid.UUID   `json:"workspace_id"`
	ApplicationID   *uuid.UUID  `json:"application_id"`
	ApplicationCode *string     `json:"application_code"`
	AccountID       *uuid.UUID  `json:"account_id"`
	AccountName     *string     `json:"account_name"`
	CampaignType    string      `json:"campaign_type"`
	Name            string      `json:"name"`
	Body            *string     `json:"body"`
	AttachmentIDs   []uuid.UUID `json:"attachment_ids"`
	Status          string      `json:"status"`
	ScheduledAt     *time.Time  `json:"scheduled_at"`
	ExecutedAt      *time.Time  `json:"executed_at"`
	TargetCount     int         `json:"target_count"`
	SuccessCount    int         `json:"success_count"`
	FailedCount     int         `json:"failed_count"`
	FailureReason   *string     `json:"failure_reason"`
	CreatedBy       *uuid.UUID  `json:"created_by"`
	CreatorName     *string     `json:"creator_name"`
	CreatorRole     *string     `json:"creator_role"`
	CreatedAt       time.Time   `json:"created_at"`
	UpdatedAt       time.Time   `json:"updated_at"`

	// --- eksekusi (migrasi 0030) --------------------------------------------
	//
	// Added rather than replacing anything above: the fields the Dashboard and
	// the upcoming-schedule list already read keep their names and meanings.

	// DelayProfile paces the queue. It is not a claim about avoiding blocks.
	DelayProfile string `json:"delay_profile"`
	ComposeMode  string `json:"compose_mode"`
	// MessageTemplate is what the author wrote, before spintax was resolved and
	// variables filled. Body holds the same text for a plain message.
	MessageTemplate *string `json:"message_template"`
	// MediaURL is a link. The file itself is never stored here or in the
	// database; it is downloaded to a temporary file at send time and deleted.
	MediaURL        *string    `json:"media_url"`
	MediaKind       *string    `json:"media_kind"`
	MediaMime       *string    `json:"media_mime"`
	MediaSizeBytes *int64 `json:"media_size_bytes"`
	// MediaFileName is an uploaded document's display name, kept after the file
	// itself is deleted so the report can still say what was sent.
	MediaFileName   *string    `json:"media_file_name"`
	Caption         *string    `json:"caption"`
	TargetSource    *string    `json:"target_source"`
	StartedAt       *time.Time `json:"started_at"`
	FinishedAt      *time.Time `json:"finished_at"`
	CancelledAt     *time.Time `json:"cancelled_at"`
	CancelRequested bool       `json:"cancel_requested"`
	MaxAttempts     int        `json:"max_attempts"`
	RetryGapSeconds int        `json:"retry_gap_seconds"`
	ArchivedAt      *time.Time `json:"archived_at"`
	// DeviceCount is how many WhatsApp numbers this campaign sends from.
	DeviceCount int `json:"device_count"`
	// DeviceNames is those numbers by name, in the order they were chosen.
	// Never nil, so the browser can read it without a guard.
	DeviceNames []string `json:"device_names"`
}

// CampaignActivity is one entry of a campaign's audit trail.
type CampaignActivity struct {
	ID            uuid.UUID  `json:"id"`
	ActivityType  *string    `json:"activity_type"`
	Action        *string    `json:"action"`
	EntityName    *string    `json:"entity_name"`
	AdminID       *uuid.UUID `json:"admin_id"`
	AdminName     *string    `json:"admin_name"`
	AdminRole     *string    `json:"admin_role"`
	Status        *string    `json:"status"`
	FailureReason *string    `json:"failure_reason"`
	Detail        []byte     `json:"detail"`
	OccurredAt    time.Time  `json:"occurred_at"`
}

// CampaignSchedule is one entry of a campaign's scheduling history.
type CampaignSchedule struct {
	ID            uuid.UUID  `json:"id"`
	ScheduledAt   time.Time  `json:"scheduled_at"`
	Timezone      string     `json:"timezone"`
	Status        string     `json:"status"`
	Attempt       int        `json:"attempt"`
	StartedAt     *time.Time `json:"started_at"`
	FinishedAt    *time.Time `json:"finished_at"`
	FailureReason *string    `json:"failure_reason"`
	SupersededAt  *time.Time `json:"superseded_at"`
	CreatedBy     *uuid.UUID `json:"created_by"`
	CreatorName   *string    `json:"creator_name"`
	CreatedAt     time.Time  `json:"created_at"`
}

// FilterOptions is everything the filter row on the Dashboard may offer this
// caller — already narrowed to their scope, so a Freelance cannot even see the
// name of an application they are not on.
type FilterOptions struct {
	Role         string       `json:"role"`
	Applications []AppRef     `json:"applications"`
	Accounts     []AccountRef `json:"accounts"`
	PICs         []PersonRef  `json:"pics"`
	Freelancers  []PersonRef  `json:"freelancers"`
	Shifts       []ShiftRef   `json:"shifts"`
}

// AccountRef is an account reduced to what a dropdown needs.
type AccountRef struct {
	ID            uuid.UUID  `json:"id"`
	Name          string     `json:"name"`
	PhoneNumber   *string    `json:"phone_number"`
	ApplicationID *uuid.UUID `json:"application_id"`
}

// PersonRef is a member reduced to what a dropdown needs.
type PersonRef struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
	Role string    `json:"role"`
}

// ShiftRef is one distinct scheduled block available as a filter.
type ShiftRef struct {
	ID       uuid.UUID `json:"id"`
	Label    string    `json:"label"`
	UserID   uuid.UUID `json:"user_id"`
	UserName string    `json:"user_name"`
	WorkDate string    `json:"work_date"`
}
