// Package models contains the API-facing representations of the database rows.
// JSON tags are the contract the Next.js frontend types mirror.
package models

import (
	"time"

	"github.com/google/uuid"
)

// Account status values, mirroring the public.account_status enum.
const (
	AccountStatusDisconnected = "disconnected"
	AccountStatusConnecting   = "connecting"
	AccountStatusQRPending    = "qr_pending"
	AccountStatusConnected    = "connected"
	AccountStatusLoggedOut    = "logged_out"
	AccountStatusError        = "error"
)

// Message status values, mirroring the public.message_status enum.
const (
	MessageStatusPending   = "pending"
	MessageStatusSent      = "sent"
	MessageStatusDelivered = "delivered"
	MessageStatusRead      = "read"
	MessageStatusFailed    = "failed"
)

// Conversation status values, mirroring the public.conversation_status enum.
const (
	ConversationStatusNew        = "new"
	ConversationStatusInProgress = "in_progress"
	ConversationStatusDone       = "done"
)

// Conversation types.
const (
	ConversationTypePersonal = "personal"
	ConversationTypeGroup    = "group"
	// ConversationTypeStatus is status@broadcast: one thread per number holding
	// every contact's Status, told apart by participant_jid the way a group's
	// senders are. Not an inbox thread, and kept out of every inbox query.
	ConversationTypeStatus = "status"
)

// User is the caller's profile plus their workspace binding.
type User struct {
	ID          uuid.UUID `json:"id"`
	WorkspaceID uuid.UUID `json:"workspace_id"`
	Email       string    `json:"email"`
	FullName    *string   `json:"full_name"`
	AvatarURL   *string   `json:"avatar_url"`
	Role        string    `json:"role"`
}

// Workspace is the tenancy boundary.
type Workspace struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
	Slug string    `json:"slug"`
}

// Application is one product/brand ("JADIASN", "JADIBUMN", ...).
type Application struct {
	ID          uuid.UUID `json:"id"`
	WorkspaceID uuid.UUID `json:"workspace_id"`
	Code        string    `json:"code"`
	Name        string    `json:"name"`
	Color       string    `json:"color"`
	IconURL     *string   `json:"icon_url"`
	SortOrder   int       `json:"sort_order"`
	IsActive    bool      `json:"is_active"`

	// Aggregates, filled by the list query.
	AccountCount      int `json:"account_count"`
	ConversationCount int `json:"conversation_count"`
	UnreadCount       int `json:"unread_count"`
	// UnansweredCount is what the badge on this application shows: chats
	// waiting for an answer, summed across its numbers.
	UnansweredCount int `json:"unanswered_count"`
}

// Account is a linked WhatsApp device.
type Account struct {
	ID               uuid.UUID  `json:"id"`
	WorkspaceID      uuid.UUID  `json:"workspace_id"`
	ApplicationID    *uuid.UUID `json:"application_id"`
	ApplicationCode  *string    `json:"application_code"`
	ApplicationName  *string    `json:"application_name"`
	ApplicationColor *string    `json:"application_color"`
	Name             string     `json:"name"`
	Label            *string    `json:"label"`
	PhoneNumber      *string    `json:"phone_number"`
	DeviceID         string     `json:"device_id"`
	JID              *string    `json:"jid"`
	// LID is the account's second address. WhatsApp uses it instead of the
	// phone number on many chats, so anything asking "was this us?" — a mention
	// most of all — has to check both.
	LID              *string    `json:"lid"`
	ConnectionMethod string     `json:"connection_method"`
	Status           string     `json:"status"`
	StatusDetail     *string    `json:"status_detail"`
	LastConnectedAt  *time.Time `json:"last_connected_at"`
	LastSyncedAt     *time.Time `json:"last_synced_at"`
	SyncWindowDays   int        `json:"sync_window_days"`
	LabelsSyncedAt   *time.Time `json:"labels_synced_at"`
	// LabelSyncState is one of idle / syncing / synced / failed, driving the
	// "Menyinkronkan / Tersinkron / Gagal" indicator.
	LabelSyncState    string    `json:"label_sync_state"`
	LabelSyncError    *string   `json:"label_sync_error"`
	CreatedAt         time.Time `json:"created_at"`
	ConversationCount int       `json:"conversation_count"`
	UnreadCount       int       `json:"unread_count"`
	// UnansweredCount is chats where the customer spoke last and nobody has
	// answered. Personal chats only, matching "Masih Menunggu Balasan".
	//
	// Not UnreadCount: reading a message is not answering it, and one already
	// opened but still unanswered is precisely the one somebody needs to see.
	// Not ConversationCount either, which is simply how many chats exist and
	// does not go down however much work gets done.
	UnansweredCount int `json:"unanswered_count"`
	MessageCount    int `json:"message_count"`
}

// AccountStats powers the counters in the page header.
type AccountStats struct {
	Total        int `json:"total"`
	Connected    int `json:"connected"`
	QRAccounts   int `json:"qr_accounts"`
	WABAAccounts int `json:"waba_accounts"`
	WABALimit    int `json:"waba_limit"`
	DeviceLimit  int `json:"device_limit"`
	Unassigned   int `json:"unassigned"`
}

// Contact is a WhatsApp user known to one account.
type Contact struct {
	ID          uuid.UUID `json:"id"`
	AccountID   uuid.UUID `json:"account_id"`
	JID         string    `json:"jid"`
	PhoneNumber *string   `json:"phone_number"`
	Name        *string   `json:"name"`
	PushName    *string   `json:"push_name"`
	AvatarURL   *string   `json:"avatar_url"`
	IsBusiness  bool      `json:"is_business"`

	// Which WhatsApp number holds this contact, and which brand that number
	// belongs to. Both are joined at read time rather than copied onto the row:
	// moving a number to another application must not need a rewrite of every
	// contact on it.
	AccountName      *string    `json:"account_name"`
	AccountPhone     *string    `json:"account_phone"`
	ApplicationID    *uuid.UUID `json:"application_id"`
	ApplicationCode  *string    `json:"application_code"`
	ApplicationColor *string    `json:"application_color"`

	// Labels currently on this contact, read from contact_label_state. These
	// are WhatsApp's own labels, the same ones the phone shows.
	Labels []string `json:"labels"`
}

// ContactFilter is what the address book screen narrows by.
type ContactFilter struct {
	AccountID     *uuid.UUID
	ApplicationID *uuid.UUID
	Search        string
	Limit         int
	Offset        int
}

// ContactFacet is one row of the filter chips: a name, and how many contacts
// sit behind it.
type ContactFacet struct {
	ID    *uuid.UUID `json:"id"`
	Label string     `json:"label"`
	Hint  string     `json:"hint"`
	Color *string    `json:"color"`
	Count int        `json:"count"`
}

// GroupFilter is what the group directory narrows by.
type GroupFilter struct {
	AccountID     *uuid.UUID
	ApplicationID *uuid.UUID
	// ChatJID narrows to exactly one group, for its own page.
	ChatJID string
	Search  string
	Limit   int
	Offset  int
}

// GroupAccount is one of our numbers sitting inside a group.
type GroupAccount struct {
	ConversationID   uuid.UUID `json:"conversation_id"`
	AccountID        uuid.UUID `json:"account_id"`
	AccountName      string    `json:"account_name"`
	AccountPhone     string    `json:"account_phone"`
	ApplicationCode  string    `json:"application_code"`
	ApplicationColor string    `json:"application_color"`
}

// GroupRow is one WhatsApp group, however many of our numbers are in it.
//
// Keyed by ChatJID rather than by conversation id, because three of our numbers
// in one group is one group. The numbers that reach it are a column on the row,
// which is what lets the screen say "3 nomor" instead of listing it three times.
type GroupRow struct {
	ChatJID string `json:"chat_jid"`
	Name    string `json:"name"`
	// AccountCount is how many of our numbers are inside.
	AccountCount int            `json:"account_count"`
	MemberCount  int            `json:"member_count"`
	// Fetched is false until the member list has been pulled from WhatsApp.
	// Said plainly on the row: a member count of zero because nobody has looked
	// is a different fact from a group that is genuinely empty.
	Fetched  bool           `json:"fetched"`
	Accounts []GroupAccount `json:"accounts"`
}

// ContactFacets powers both chip rows.
//
// Two totals, because the two rows lead with different questions. "Keseluruhan"
// on the application row means the whole workspace and must not move when a
// brand is picked, or the reader loses the sense of how big the book is.
// "Semua nomor aplikasi ini" on the number row means everything inside the
// brand currently chosen. One number serving both would be wrong for one of
// them at all times.
type ContactFacets struct {
	Total        int            `json:"total"`
	ScopedTotal  int            `json:"scoped_total"`
	Applications []ContactFacet `json:"applications"`
	Accounts     []ContactFacet `json:"accounts"`
}

// Label is a coloured tag applied to conversations.
//
// Identity for a WhatsApp label is (AccountID, WALabelID) — never the name.
// Renaming a label on the phone keeps its id, and two linked numbers may each
// have a label called "Premium" that must stay independent of one another.
//
// Source is "manual" for tags created in this app before they reach WhatsApp,
// and "whatsapp" once the phone knows about them.
type Label struct {
	ID          uuid.UUID  `json:"id"`
	WorkspaceID uuid.UUID  `json:"workspace_id"`
	AccountID   *uuid.UUID `json:"account_id"`
	WALabelID   *string    `json:"wa_label_id"`
	Name        string     `json:"name"`
	Color       string     `json:"color"`
	// ColorIndex is WhatsApp's palette slot; Color is derived from it. Keeping
	// the index means a round trip to the phone does not lose the exact shade.
	ColorIndex *int   `json:"color_index"`
	SortOrder  int    `json:"sort_order"`
	Source     string `json:"source"`
	// WAUpdatedAt is the timestamp of the app-state mutation that produced this
	// row. It decides conflicts: the newest WhatsApp change wins.
	WAUpdatedAt *time.Time `json:"wa_updated_at"`
}

// Conversation is one chat thread on one account.
type Conversation struct {
	ID          uuid.UUID  `json:"id"`
	WorkspaceID uuid.UUID  `json:"workspace_id"`
	AccountID   uuid.UUID  `json:"account_id"`
	ContactID   *uuid.UUID `json:"contact_id"`
	ChatJID     string     `json:"chat_jid"`
	Type        string     `json:"type"`
	Name        *string    `json:"name"`
	AvatarURL   *string    `json:"avatar_url"`
	Status      string     `json:"status"`
	UnreadCount int        `json:"unread_count"`
	// MentionCount is unseen mentions of this account, counted separately from
	// unread messages. A group can have two hundred unread and no mention, or
	// none unread and one mention that is the only thing worth opening for.
	MentionCount int  `json:"mention_count"`
	MarkedUnread bool `json:"marked_unread"`
	// The conversation head, recomputed from the newest message rather than
	// from whichever row happened to be inserted last.
	LastMessageID   *uuid.UUID `json:"last_message_id"`
	LastMessageAt   *time.Time `json:"last_message_at"`
	LastMessageText *string    `json:"last_message_text"`
	// "in" or "out"; null when the thread has no messages yet.
	LastMessageDirection *string `json:"last_message_direction"`
	// WAConversationAt is WhatsApp's own "last activity" stamp. It drives the
	// inbox ordering, because a chat whose newest message falls outside the
	// synced window still has to sit where the phone shows it.
	WAConversationAt *time.Time `json:"wa_conversation_at"`
	IsArchived       bool       `json:"is_archived"`
	IsPinned         bool       `json:"is_pinned"`
	PhoneNumber      *string    `json:"phone_number"`
	Labels           []Label    `json:"labels"`
	UpdatedAt        time.Time  `json:"updated_at"`

	// PNJID is the phone-number form of this chat, shared by its LID twin.
	PNJID *string `json:"pn_jid"`
	// Group metadata. Null on a one-to-one chat.
	GroupDescription *string `json:"group_description"`
	GroupOwnerJID    *string `json:"group_owner_jid"`
	// SelfIsAdmin decides which group controls the interface offers. WhatsApp
	// remains the authority — a stale value produces a refusal from the server,
	// not a silent no-op.
	SelfIsAdmin bool `json:"self_is_admin"`
}

// Message is a single WhatsApp message.
type Message struct {
	ID              uuid.UUID `json:"id"`
	ConversationID  uuid.UUID `json:"conversation_id"`
	AccountID       uuid.UUID `json:"account_id"`
	WAMessageID     string    `json:"wa_message_id"`
	SenderJID       *string   `json:"sender_jid"`
	SenderName      *string   `json:"sender_name"`
	FromMe          bool      `json:"from_me"`
	Type            string    `json:"type"`
	Body            *string   `json:"body"`
	Caption         *string   `json:"caption"`
	MediaURL        *string   `json:"media_url"`
	MediaMime       *string   `json:"media_mime"`
	QuotedMessageID *string   `json:"quoted_message_id"`
	Status          string    `json:"status"`
	ErrorMessage    *string   `json:"error_message"`
	// DeliveredAt and ReadAt record when WhatsApp reported each milestone, so
	// the state survives a restart instead of living only in `status`.
	DeliveredAt *time.Time `json:"delivered_at"`
	ReadAt      *time.Time `json:"read_at"`
	// EditedAt is set when the text was changed after sending. WhatsApp shows
	// such a message with a "diedit" marker on both sides.
	EditedAt *time.Time `json:"edited_at"`
	// RevokedAt is set when the message was deleted for everyone. The row
	// stays so the conversation keeps its shape, with the content cleared.
	RevokedAt *time.Time `json:"revoked_at"`
	Timestamp time.Time  `json:"timestamp"`
	CreatedAt time.Time  `json:"created_at"`
	SentBy    *uuid.UUID `json:"sent_by"`

	// ParticipantJID and SenderPhone identify the group member who wrote this.
	// Null in a one-to-one chat, where the sender is the chat itself.
	ParticipantJID *string `json:"participant_jid"`
	SenderPhone    *string `json:"sender_phone"`
	// DisplayName is who to show above the bubble, resolved when the message is
	// read rather than frozen when it arrived: saved contact name, then push
	// name, then the name that came with the message, then the number. That is
	// why renaming a contact on the phone updates old messages too.
	DisplayName *string `json:"display_name"`

	// MentionedJIDs is WhatsApp's own list of who the message names — metadata,
	// not text scraped for "@digits". Never null in JSON.
	MentionedJIDs []string `json:"mentioned_jids"`
	// MentionsMe is that list resolved against the account that received the
	// message. Per-account on purpose: one group may hold several of our
	// numbers, and the answer differs for each.
	MentionsMe bool `json:"mentions_me"`
	// MentionSeenAt is when the mention was actually looked at. Distinct from
	// ReadAt: a busy group can be read through without the mention being seen.
	MentionSeenAt *time.Time `json:"mention_seen_at"`

	// Attachments is empty for text messages. It is never null in JSON so the
	// frontend can map over it without a guard.
	Attachments []Attachment `json:"attachments"`
	// Poll is set only on poll messages.
	Poll *Poll `json:"poll"`
	// Quoted describes the message this one replies to, when there is one and
	// it is still within the synced window.
	Quoted *QuotedMessage `json:"quoted"`
	// Reactions on this message, grouped by emoji. Never null.
	Reactions []Reaction `json:"reactions"`
}

// Reaction is one emoji on a message, with everyone who gave it.
//
// Grouped by emoji rather than listed per person, because that is how it is
// read: "four thumbs up", not four separate rows saying the same thing.
type Reaction struct {
	Emoji string `json:"emoji"`
	Count int    `json:"count"`
	// Mine is true when this account is among them — it is what lets a second
	// click take the reaction back.
	Mine bool `json:"mine"`
	// Names of the people who reacted, resolved the same way sender names are.
	// Capped: a tooltip listing two hundred people helps nobody.
	Names []string `json:"names"`
}

// QuotedMessage is the little preview shown above a reply.
//
// Deliberately not the whole message: the quote bubble shows a name and a line
// of text, so sending the rest would be weight for nothing — and a quoted
// message that has since been deleted must not carry its content back.
type QuotedMessage struct {
	// ID is our row id when the original is still stored, letting the UI jump
	// to it. Null for a quote of something outside the window.
	ID          *uuid.UUID `json:"id"`
	WAMessageID string     `json:"wa_message_id"`
	SenderName  *string    `json:"sender_name"`
	FromMe      bool       `json:"from_me"`
	Type        string     `json:"type"`
	Text        string     `json:"text"`
	// Thumbnail lets a quoted photo show its picture, as WhatsApp does.
	Thumbnail *string `json:"thumbnail_b64"`
}

// Poll is a WhatsApp poll carried by one message.
type Poll struct {
	MessageID uuid.UUID `json:"message_id"`
	Name      string    `json:"name"`
	// SelectableCount is 1 for single choice, 0 for "as many as you like".
	SelectableCount int          `json:"selectable_count"`
	Options         []PollOption `json:"options"`
	// TotalVoters counts distinct people who have voted, which is not the sum
	// of the option counts when several answers are allowed.
	TotalVoters int `json:"total_voters"`
	// SelectedIdx lists the options this account itself chose.
	SelectedIdx []int `json:"selected_idx"`
}

// PollOption is one answer, with its running tally.
type PollOption struct {
	Index int    `json:"index"`
	Name  string `json:"name"`
	Votes int    `json:"votes"`
}

// Attachment storage states, mirroring public.attachment_storage_status.
const (
	AttachmentPending   = "pending"
	AttachmentUploading = "uploading"
	AttachmentStored    = "stored"
	AttachmentFailed    = "failed"
	// AttachmentExpired means the file was deleted because its message fell
	// outside the retention window. It cannot be fetched again.
	AttachmentExpired = "expired"
)

// Attachment is one media or document file hanging off a message.
//
// It carries no URL: the bucket is private, so the browser asks for a signed
// URL per file when it actually needs to render or download it. What is safe to
// expose — dimensions, duration, size, name, and a tiny inline thumbnail — is
// enough to lay the bubble out at the right size before any bytes arrive.
type Attachment struct {
	ID        uuid.UUID `json:"id"`
	MessageID uuid.UUID `json:"message_id"`
	Index     int       `json:"index"`
	Kind      string    `json:"kind"`
	FileName  *string   `json:"file_name"`
	MimeType  *string   `json:"mime_type"`
	SizeBytes *int64    `json:"size_bytes"`
	Width     *int      `json:"width"`
	Height    *int      `json:"height"`
	Duration  *int      `json:"duration_secs"`
	Thumbnail *string   `json:"thumbnail_b64"`
	// Status tells the UI whether the bytes are available yet. An incoming file
	// is a row before it is a download, so a bubble can render from the
	// thumbnail while `pending` and gain its full-size view on `stored`.
	Status     string  `json:"status"`
	StoreError *string `json:"storage_error"`
}

// ConversationFilter narrows an inbox listing.
type ConversationFilter struct {
	AccountID  uuid.UUID
	Search     string
	Type       string // "", "personal", "group"
	Status     string // "", "new", "in_progress", "done"
	UnreadOnly bool
	// MentionsOnly narrows to chats that named this account and have not been
	// looked at yet.
	MentionsOnly bool
	LabelID      *uuid.UUID
	Limit        int
	Offset       int
}

// AccountFilter narrows the account grid.
type AccountFilter struct {
	ApplicationID    *uuid.UUID
	Unassigned       bool
	ConnectionMethod string
	Search           string
}
