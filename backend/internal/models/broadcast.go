package models

import (
	"time"

	"github.com/google/uuid"
)

// Campaign statuses added by migration 0029, beside the ones in analytics.go.
const (
	// CampaignPartial means some recipients got it and some did not. Neither
	// "completed" nor "failed" is true of such a run, and forcing it into one of
	// them either hides a failure or invites somebody to re-send messages that
	// already arrived.
	CampaignPartial = "partial"
	// CampaignExpired is a Story past its 24 hours. Not a failure: it ran.
	CampaignExpired = "expired"
)

// Per-target states.
const (
	TargetPending    = "pending"
	TargetProcessing = "processing"
	TargetSent       = "sent"
	TargetDelivered  = "delivered"
	TargetRead       = "read"
	TargetFailed     = "failed"
	TargetCancelled  = "cancelled"
	TargetSkipped    = "skipped"
	TargetRetryWait  = "retry_wait"
	// TargetInvalid is a recipient the campaign can never reach: a malformed
	// number, or one no selected device can address. Distinct from failed, which
	// is worth retrying.
	TargetInvalid = "invalid"
)

// Per-device states, for both Broadcast senders and Story publications.
const (
	DeviceStatePending    = "pending"
	DeviceStateRunning    = "running"
	DeviceStateProcessing = "processing"
	DeviceStatePublished  = "published"
	DeviceStateDone       = "done"
	DeviceStateFailed     = "failed"
	DeviceStateCancelled  = "cancelled"
	DeviceStateExpired    = "expired"
)

// How the message body was written.
const (
	ComposePlain            = "plain"
	ComposeSpintax          = "spintax"
	ComposeVariables        = "variables"
	ComposeSpintaxVariables = "spintax_variables"
	ComposeGPT              = "gpt"
)

// Where the recipient list came from.
const (
	TargetSourceManual       = "manual"
	TargetSourceCSV          = "csv"
	TargetSourceContacts     = "contacts"
	TargetSourceGroupMembers = "group_members"
	TargetSourceGroups       = "groups"
)

// Delay profiles.
//
// These pace a queue. They are not a claim about staying unblocked, and nothing
// in this software should ever suggest otherwise: WhatsApp does not publish what
// it looks for, so any promise of safety here would be invented.
const (
	DelaySuperCepat = "super_cepat"
	DelayCepat      = "cepat"
	DelayNormal     = "normal"
	DelayAman       = "aman"
	DelaySantai     = "santai"
)

// How often a recurring broadcast comes back around.
const (
	RecurrenceDaily   = "daily"
	RecurrenceWeekly  = "weekly"
	RecurrenceMonthly = "monthly"
)

// DelayRange is the interval a per-target pause is drawn from.
type DelayRange struct {
	Min time.Duration
	Max time.Duration
}

// delayProfiles are the five the interface offers, exactly as specified.
var delayProfiles = map[string]DelayRange{
	DelaySuperCepat: {Min: 1 * time.Second, Max: 6 * time.Second},
	DelayCepat:      {Min: 5 * time.Second, Max: 15 * time.Second},
	DelayNormal:     {Min: 30 * time.Second, Max: 60 * time.Second},
	DelayAman:       {Min: 1 * time.Minute, Max: 2 * time.Minute},
	DelaySantai:     {Min: 3 * time.Minute, Max: 5 * time.Minute},
}

// DelayProfile returns the interval for a profile name, falling back to Normal
// for anything unrecognised — a stored value from a future version should slow
// a campaign down, never speed it up.
func DelayProfile(name string) DelayRange {
	if r, ok := delayProfiles[name]; ok {
		return r
	}
	return delayProfiles[DelayNormal]
}

// DelayProfileNames lists the profiles in the order the interface shows them.
func DelayProfileNames() []string {
	return []string{DelaySuperCepat, DelayCepat, DelayNormal, DelayAman, DelaySantai}
}

// BroadcastDevice is one sending number on a campaign, with its own tally.
type BroadcastDevice struct {
	ID            uuid.UUID  `json:"id"`
	CampaignID    uuid.UUID  `json:"campaign_id"`
	AccountID     uuid.UUID  `json:"account_id"`
	AccountName   string     `json:"account_name"`
	PhoneNumber   *string    `json:"phone_number"`
	Position      int        `json:"position"`
	Status        string     `json:"status"`
	AssignedCount int        `json:"assigned_count"`
	SentCount     int        `json:"sent_count"`
	FailedCount   int        `json:"failed_count"`
	StartedAt     *time.Time `json:"started_at"`
	FinishedAt    *time.Time `json:"finished_at"`
	FailureReason *string    `json:"failure_reason"`
	// Connected is read from the live session, not from the database: a device
	// that was online when the campaign was made may not be now.
	Connected bool `json:"connected"`
}

// CampaignTarget is one recipient row.
type CampaignTarget struct {
	ID             uuid.UUID  `json:"id"`
	CampaignID     uuid.UUID  `json:"campaign_id"`
	ContactID      *uuid.UUID `json:"contact_id"`
	ConversationID *uuid.UUID `json:"conversation_id"`
	AccountID      *uuid.UUID `json:"account_id"`
	AccountName    *string    `json:"account_name"`
	ChatJID        string     `json:"chat_jid"`
	TargetType     string     `json:"target_type"`
	PhoneNumber    *string    `json:"phone_number"`
	DisplayName    *string    `json:"display_name"`
	Status         string     `json:"status"`
	Attempt        int        `json:"attempt"`
	RenderedBody   *string    `json:"rendered_body"`
	WAMessageID    *string    `json:"wa_message_id"`
	SentAt         *time.Time `json:"sent_at"`
	DeliveredAt    *time.Time `json:"delivered_at"`
	ReadAt         *time.Time `json:"read_at"`
	NextAttemptAt  *time.Time `json:"next_attempt_at"`
	FailureReason  *string    `json:"failure_reason"`
	ErrorCode      *string    `json:"error_code"`
	InvalidReason  *string    `json:"invalid_reason"`
	// Replied is whether this contact wrote back after the campaign message.
	// A normal inbound message in every other respect — it is counted as one
	// everywhere else, and shown here only so the report can say how many
	// answered.
	Replied bool `json:"replied"`
}

// BroadcastReport is everything the campaign detail screen shows.
type BroadcastReport struct {
	Campaign Campaign          `json:"campaign"`
	Devices  []BroadcastDevice `json:"devices"`
	Totals   TargetTotals      `json:"totals"`
	// DurationSeconds is wall-clock from first send to last, null while running
	// or when nothing has been sent.
	DurationSeconds *int `json:"duration_seconds"`
	// Replies is inbound messages from targeted contacts after their campaign
	// message went out.
	Replies int `json:"replies"`
	Labels  []CampaignLabel `json:"labels"`
}

// TargetTotals is the per-status breakdown.
type TargetTotals struct {
	Total     int `json:"total"`
	Valid     int `json:"valid"`
	Invalid   int `json:"invalid"`
	Pending   int `json:"pending"`
	Processing int `json:"processing"`
	Sent      int `json:"sent"`
	Delivered int `json:"delivered"`
	Read      int `json:"read"`
	Failed    int `json:"failed"`
	Cancelled int `json:"cancelled"`
	RetryWait int `json:"retry_wait"`
	// Retries is how many second-or-later attempts were made in total.
	Retries int `json:"retries"`
}

// TargetAttempt is one row of a target's send history.
type TargetAttempt struct {
	ID            uuid.UUID  `json:"id"`
	TargetID      uuid.UUID  `json:"target_id"`
	AccountID     uuid.UUID  `json:"account_id"`
	AccountName   *string    `json:"account_name"`
	Attempt       int        `json:"attempt"`
	Status        string     `json:"status"`
	WAMessageID   *string    `json:"wa_message_id"`
	ErrorCode     *string    `json:"error_code"`
	FailureReason *string    `json:"failure_reason"`
	StartedAt     time.Time  `json:"started_at"`
	FinishedAt    *time.Time `json:"finished_at"`
}

// StoryPublication is one Story on one device.
type StoryPublication struct {
	ID            uuid.UUID  `json:"id"`
	CampaignID    uuid.UUID  `json:"campaign_id"`
	AccountID     uuid.UUID  `json:"account_id"`
	AccountName   string     `json:"account_name"`
	PhoneNumber   *string    `json:"phone_number"`
	Status        string     `json:"status"`
	Attempt       int        `json:"attempt"`
	WAMessageID   *string    `json:"wa_message_id"`
	PublishedAt   *time.Time `json:"published_at"`
	ExpiresAt     *time.Time `json:"expires_at"`
	FailureReason *string    `json:"failure_reason"`
	ErrorCode     *string    `json:"error_code"`
	Connected     bool       `json:"connected"`
	// DetectedViews is the number of distinct viewers whose read receipt this
	// server actually received. A lower bound, never a view count: see
	// StoryViewNotice.
	DetectedViews int `json:"detected_views"`
	// FinalViews is the snapshot taken once the Story expired, null until then.
	FinalViews *int `json:"final_views"`
}

// StoryReport is the Story detail screen.
type StoryReport struct {
	Campaign     Campaign           `json:"campaign"`
	Publications []StoryPublication `json:"publications"`
	// ViewsPerDevice is the sum across publications: one person who watched on
	// two of our numbers counts twice, because two Stories were watched.
	ViewsPerDevice int `json:"views_per_device"`
	// UniqueViewers deduplicates by viewer identity across the whole campaign.
	// Deliberately labelled differently from the figure above so the two are
	// never read as the same thing.
	UniqueViewers int `json:"unique_viewers"`
	// UniqueKnown is false once any of the figures has been frozen. Keeping only
	// the number after 24 hours means the viewer identities are gone, and "how
	// many different people" cannot be recovered from a set of per-device totals.
	// The interface says so rather than showing a smaller number as if it were
	// still the answer.
	UniqueKnown bool `json:"unique_viewers_known"`
	// ViewsAvailable is false when nothing has been published yet, so the
	// interface can say "Data views belum tersedia" instead of showing a zero
	// that looks like a measurement.
	ViewsAvailable bool            `json:"views_available"`
	Labels         []CampaignLabel `json:"labels"`
}

// StoryViewNotice is the sentence shown under every Story view figure.
//
// It is not decoration. whatsmeow exposes no way to read who viewed a status;
// the only real signal is a read receipt, and a viewer with read receipts turned
// off never produces one. Presenting the number without this line would be
// presenting a lower bound as a total.
const StoryViewNotice = "Angka berdasarkan receipt yang berhasil diterima sistem. Sebagian penonton mungkin tidak terhitung."

// CampaignLabel is an internal tag on a campaign, unrelated to WhatsApp labels.
type CampaignLabel struct {
	ID          uuid.UUID  `json:"id"`
	Name        string     `json:"name"`
	Color       string     `json:"color"`
	ArchivedAt  *time.Time `json:"archived_at"`
	CampaignCount int      `json:"campaign_count"`
	CreatedAt   time.Time  `json:"created_at"`
}

// CustomVariable is a placeholder the workspace defined for itself.
type CustomVariable struct {
	ID  uuid.UUID `json:"id"`
	Key string    `json:"key"`
	// ApplicationID nil means the variable belongs to the whole workspace
	// rather than to one brand. See migration 0032 for why both are allowed.
	ApplicationID   *uuid.UUID `json:"application_id"`
	ApplicationCode *string    `json:"application_code"`
	ApplicationName *string    `json:"application_name"`
	ApplicationColor *string   `json:"application_color"`
	Label           string     `json:"label"`
	DefaultValue    *string    `json:"default_value"`
	Description     *string    `json:"description"`
	IsActive        bool       `json:"is_active"`
	CreatedAt       time.Time  `json:"created_at"`
}

// QuickReply is a canned message an operator drops into a chat by typing a
// slash followed by its shortcut.
//
// Deliberately the same shape as CustomVariable down to the nullable
// application: the two are configured on neighbouring screens, filtered the
// same way and grouped the same way, and a reader who has learned one should
// not have to learn the other.
type QuickReply struct {
	ID               uuid.UUID  `json:"id"`
	ApplicationID    *uuid.UUID `json:"application_id"`
	ApplicationCode  *string    `json:"application_code"`
	ApplicationName  *string    `json:"application_name"`
	ApplicationColor *string    `json:"application_color"`
	// Shortcut is stored without its leading slash: the slash belongs to the
	// interface that summons it, not to the record.
	Shortcut string `json:"shortcut"`
	Title    string `json:"title"`
	Body     string `json:"body"`
	// Category groups the list. Free text, optional, no meaning to the sender.
	Category *string `json:"category"`
	// MediaURL turns this into a picture reply: the image is sent with Body as
	// its caption. Only the address is stored — the file is fetched to a
	// temporary path at send time and deleted, exactly as Broadcast does it.
	MediaURL *string `json:"media_url"`
	// The measured facts about that image, taken when the address was saved.
	// Recorded so "this really is an image" is settled at save time rather than
	// in front of a customer, and so the list can say how big it is.
	MediaKind      *string `json:"media_kind"`
	MediaMime      *string `json:"media_mime"`
	MediaSizeBytes *int64  `json:"media_size_bytes"`
	// MediaSHA256 is never sent to the browser: it is a fingerprint of somebody
	// else's file, useful only for recognising the same image twice server-side.
	MediaSHA256 *string `json:"-"`

	// UsageCount is how many times this reply was actually SENT, not how many
	// times it was picked: picking one and then clearing the box is not use,
	// and counting it would flatter exactly the replies people pick by mistake.
	UsageCount int64      `json:"usage_count"`
	LastUsedAt *time.Time `json:"last_used_at"`

	IsActive  bool      `json:"is_active"`
	CreatedAt time.Time `json:"created_at"`
}

// BuiltInVariables are the placeholders that come from data rather than from
// the settings screen, listed so the composer can offer them.
var BuiltInVariables = []CustomVariable{
	{Key: "nama", Label: "Nama kontak"},
	{Key: "nomor", Label: "Nomor WhatsApp"},
	{Key: "aplikasi", Label: "Kode aplikasi"},
	{Key: "nama_grup", Label: "Nama grup"},
}

// TargetPlan is the review shown before a campaign runs: what will be sent,
// to whom, from which device, and how long it is expected to take.
type TargetPlan struct {
	Devices []DevicePlan `json:"devices"`
	// Valid, Invalid and Duplicates are counted at the campaign level, after
	// deduplication — one recipient may appear in a pasted list twice and in an
	// imported file once, and that is one recipient.
	Valid      int             `json:"valid"`
	Invalid    int             `json:"invalid"`
	Duplicates int             `json:"duplicates"`
	Unreachable int            `json:"unreachable"`
	Problems   []TargetProblem `json:"problems"`
	// EstimatedSeconds is derived from the delay profile and the per-device
	// share, since devices send in parallel.
	EstimatedSeconds int      `json:"estimated_seconds"`
	DelayProfile     string   `json:"delay_profile"`
	Preview          []string `json:"preview"`
	MissingVariables []string `json:"missing_variables"`
	TemplateProblems []string `json:"template_problems"`
}

// DevicePlan is one device's share of the recipients.
type DevicePlan struct {
	AccountID   uuid.UUID `json:"account_id"`
	AccountName string    `json:"account_name"`
	PhoneNumber *string   `json:"phone_number"`
	Connected   bool      `json:"connected"`
	Assigned    int       `json:"assigned"`
}

// TargetProblem explains one rejected entry, in words an operator can act on.
type TargetProblem struct {
	Input  string `json:"input"`
	Reason string `json:"reason"`
}

// ResolvedTarget is one validated recipient, ready to be written.
type ResolvedTarget struct {
	ChatJID        string
	PhoneNumber    string
	DisplayName    string
	ContactID      *uuid.UUID
	ConversationID *uuid.UUID
	TargetType     string
	AccountID      uuid.UUID
	Variables      map[string]string
	RenderedBody   string
}
