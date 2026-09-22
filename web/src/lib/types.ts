/**
 * Wire types. These mirror the JSON tags on the Go structs in
 * backend/internal/models — keep the two files in step when either changes.
 */

export type AccountStatus =
  | 'disconnected'
  | 'connecting'
  | 'qr_pending'
  | 'connected'
  | 'logged_out'
  | 'error';

export type ConnectionMethod = 'qr' | 'waba';

export type ConversationStatus = 'new' | 'in_progress' | 'done';
export type ConversationType = 'personal' | 'group';

export type MessageStatus = 'pending' | 'sent' | 'delivered' | 'read' | 'failed';

export type MessageType =
  | 'text'
  | 'image'
  | 'video'
  | 'audio'
  | 'document'
  | 'sticker'
  | 'location'
  | 'contact'
  | 'reaction'
  | 'poll'
  | 'system'
  | 'unsupported';

export interface User {
  id: string;
  workspace_id: string;
  email: string;
  full_name: string | null;
  avatar_url: string | null;
  role: string;
}

/**
 * What /me returns: the signed-in person, their workspace, and the operational
 * role the server resolved for them. `scope.all` is true for a Leader (or an
 * owner nobody has given a role yet), who sees the whole workspace.
 */
export interface Me {
  user: User;
  workspace: Workspace;
  scope?: { role: '' | 'leader' | 'pic' | 'freelance'; all: boolean };
}

export interface Workspace {
  id: string;
  name: string;
  slug: string;
}

export interface Application {
  id: string;
  workspace_id: string;
  code: string;
  name: string;
  color: string;
  icon_url: string | null;
  sort_order: number;
  is_active: boolean;
  account_count: number;
  conversation_count: number;
  unread_count: number;
  /** Chats waiting for an answer, summed across this application's numbers. */
  unanswered_count: number;
}

export interface Account {
  id: string;
  workspace_id: string;
  application_id: string | null;
  application_code: string | null;
  application_name: string | null;
  application_color: string | null;
  name: string;
  label: string | null;
  phone_number: string | null;
  device_id: string;
  jid: string | null;
  /** The account's second address; WhatsApp uses it on many chats. */
  lid: string | null;
  connection_method: ConnectionMethod;
  status: AccountStatus;
  status_detail: string | null;
  last_connected_at: string | null;
  last_synced_at: string | null;
  sync_window_days: number;
  labels_synced_at: string | null;
  label_sync_state: LabelSyncState;
  label_sync_error: string | null;
  created_at: string;
  conversation_count: number;
  unread_count: number;
  /**
   * Chats where the customer spoke last and nobody has answered. Personal
   * chats only, the same question "Masih Menunggu Balasan" answers.
   *
   * This is what the inbox badges count. Not unread_count — reading a message
   * is not answering it — and not conversation_count, which is how many chats
   * exist and does not go down however much work gets done.
   */
  unanswered_count: number;
  message_count: number;
}

export interface AccountStats {
  total: number;
  connected: number;
  qr_accounts: number;
  waba_accounts: number;
  waba_limit: number;
  device_limit: number;
  unassigned: number;
}

/** Per-account label sync state, driving the Menyinkronkan/Tersinkron/Gagal badge. */
export type LabelSyncState = 'idle' | 'syncing' | 'synced' | 'failed';

export interface Label {
  id: string;
  workspace_id: string;
  /** The WhatsApp account this label belongs to; null for a not-yet-pushed tag. */
  account_id: string | null;
  /** WhatsApp's own id — the real identity of the label, not its name. */
  wa_label_id: string | null;
  name: string;
  color: string;
  /** WhatsApp palette slot; `color` is derived from it. */
  color_index: number | null;
  sort_order: number;
  /** "whatsapp" once the phone knows about it, "manual" before that. */
  source: 'manual' | 'whatsapp';
  /** Timestamp of the app-state mutation behind this row; newest wins. */
  wa_updated_at: string | null;
}

export interface Conversation {
  id: string;
  workspace_id: string;
  account_id: string;
  contact_id: string | null;
  chat_jid: string;
  type: ConversationType;
  name: string | null;
  avatar_url: string | null;
  status: ConversationStatus;
  unread_count: number;
  /**
   * Unseen mentions of this account, counted separately from unread messages:
   * a group can be read through without its mention being seen.
   */
  mention_count: number;
  /** Flagged unread by hand on the phone, even with no unread messages. */
  marked_unread: boolean;
  /**
   * The conversation head, recomputed server-side from the newest message —
   * never from whichever row happened to be written last.
   */
  last_message_id: string | null;
  last_message_at: string | null;
  last_message_text: string | null;
  last_message_direction: 'in' | 'out' | null;
  /** WhatsApp's own activity stamp; drives inbox ordering. */
  wa_conversation_at: string | null;
  is_archived: boolean;
  is_pinned: boolean;
  phone_number: string | null;
  labels: Label[];
  updated_at: string;

  /** The phone-number form of this chat, shared by its LID twin. */
  pn_jid: string | null;
  /** Group metadata; null on a one-to-one chat. */
  group_description: string | null;
  group_owner_jid: string | null;
  /**
   * Whether this account is an admin here — decides which group controls are
   * offered. WhatsApp remains the authority: a stale value produces a refusal
   * from the server, not a silent no-op.
   */
  self_is_admin: boolean;
}

/** A group participant, with the name resolved the same way bubbles resolve it. */
export interface GroupMember {
  jid: string;
  /** The real number, or null when the address book has none. Never a LID. */
  phone_number: string | null;
  /** The contact's name, or '' when nobody has saved them. Never a LID. */
  display_name: string;
  is_admin: boolean;
  last_message_at: string | null;
}

export interface ConversationCounts {
  all: number;
  personal: number;
  group: number;
  unread: number;
  new: number;
  in_progress: number;
  done: number;
}

export type AttachmentKind = 'image' | 'video' | 'audio' | 'document' | 'sticker';

/**
 * Whether the file's bytes have reached our storage yet. An incoming file is a
 * row before it is a download, so a bubble can render from `thumbnail_b64`
 * while still `pending` and gain its full-size view once `stored`.
 */
export type AttachmentStatus = 'pending' | 'uploading' | 'stored' | 'failed' | 'expired';

/**
 * One file attached to a message. It deliberately carries no URL: the bucket is
 * private, so the browser asks for a short-lived signed URL per file when it
 * actually needs to show or download it.
 */
/** One of our WhatsApp numbers sitting inside a group. */
export interface GroupAccount {
  conversation_id: string;
  account_id: string;
  account_name: string;
  account_phone: string;
  application_code: string;
  application_color: string;
}

/**
 * One WhatsApp group, however many of our numbers are inside it.
 *
 * Keyed by chat_jid, not by conversation: three of our numbers in one group is
 * one group, and the numbers that reach it are a column rather than three rows.
 */
export interface GroupRow {
  chat_jid: string;
  name: string;
  account_count: number;
  member_count: number;
  /**
   * False until the member list has been pulled from WhatsApp. A count of zero
   * because nobody has looked is a different fact from an empty group, and the
   * row says which.
   */
  fetched: boolean;
  accounts: GroupAccount[];
}

/** One participant of a group, merged across the numbers that can see them. */
export type GroupDirectoryMember = GroupMember;

/** One chip on the address book's filter rows, with what it covers. */
export interface ContactFacet {
  id: string | null;
  label: string;
  hint: string;
  color: string | null;
  count: number;
}

export interface ContactFacets {
  /** The whole workspace. Stands behind "Keseluruhan" on the application row. */
  total: number;
  /**
   * Everything inside the application currently chosen, or the whole workspace
   * when none is. Stands behind "Semua nomor aplikasi ini" on the number row,
   * which means something different from the line above it.
   */
  scoped_total: number;
  applications: ContactFacet[];
  /** Only the numbers of the chosen application, counted inside it. */
  accounts: ContactFacet[];
}

/** What an import actually did, split three ways rather than summarised. */
export interface ImportResult {
  added: number;
  updated: number;
  rejected: number;
  reasons: string[];
}

/** What a duplicate merge actually did. */
export interface MergeResult {
  normalised: number;
  merged: number;
}

export interface Attachment {
  id: string;
  message_id: string;
  index: number;
  kind: AttachmentKind;
  file_name: string | null;
  mime_type: string | null;
  size_bytes: number | null;
  width: number | null;
  height: number | null;
  duration_secs: number | null;
  /** Base64 JPEG from WhatsApp; lets the bubble render before any download. */
  thumbnail_b64: string | null;
  status: AttachmentStatus;
  storage_error: string | null;
}

/** A time-limited URL for one attachment. */
export interface MediaLink {
  url: string;
  expires_at: string;
  file_name: string;
  mime_type: string;
  kind: AttachmentKind;
}

export interface Message {
  id: string;
  conversation_id: string;
  account_id: string;
  wa_message_id: string;
  sender_jid: string | null;
  sender_name: string | null;
  from_me: boolean;
  type: MessageType;
  body: string | null;
  caption: string | null;
  media_url: string | null;
  media_mime: string | null;
  quoted_message_id: string | null;
  status: MessageStatus;
  error_message: string | null;
  /** When WhatsApp reported each milestone; survives a restart. */
  delivered_at: string | null;
  read_at: string | null;
  /** Set when the text was changed after sending; shown as "diedit". */
  edited_at: string | null;
  /** Set when deleted for everyone. The bubble stays as a placeholder. */
  revoked_at: string | null;
  timestamp: string;
  created_at: string;
  sent_by: string | null;

  /** The group member who wrote this; null in a one-to-one chat. */
  participant_jid: string | null;
  sender_phone: string | null;
  /**
   * Who to show above the bubble. Resolved server-side when the message is
   * read — saved contact name, then push name, then the name that came with
   * the message, then the number — so renaming a contact on the phone updates
   * old messages too.
   */
  display_name: string | null;

  /** WhatsApp's own list of who the message names. Never null. */
  mentioned_jids: string[];
  /** That list resolved against the account that received the message. */
  mentions_me: boolean;
  /** When the mention was looked at; distinct from read_at. */
  mention_seen_at: string | null;
  /** Empty for text messages; never null. */
  attachments: Attachment[];
  /** Set only on poll messages. */
  poll: Poll | null;
  /** The message this one replies to, when there is one. */
  quoted: QuotedMessage | null;
  /** Reactions grouped by emoji. Never null. */
  reactions: Reaction[];
}

/**
 * One emoji on a message, with everyone who gave it.
 *
 * Grouped by emoji rather than listed per person, because that is how it reads:
 * "four thumbs up", not four rows saying the same thing.
 */
export interface Reaction {
  emoji: string;
  count: number;
  /** True when this account is among them — a second click takes it back. */
  mine: boolean;
  /** Who reacted, capped server-side; a list of two hundred helps nobody. */
  names: string[];
}

/** The small preview shown above a reply. */
export interface QuotedMessage {
  /** Null when the original has aged out of the synced window. */
  id: string | null;
  wa_message_id: string;
  sender_name: string | null;
  from_me: boolean;
  type: MessageType;
  text: string;
  thumbnail_b64: string | null;
}

export interface PollOption {
  index: number;
  name: string;
  votes: number;
}

export interface Poll {
  message_id: string;
  name: string;
  /** 1 for a single choice, 0 for "as many as you like". */
  selectable_count: number;
  options: PollOption[];
  /**
   * Distinct people who have voted — not the sum of the option counts, which
   * would double-count anyone who picked more than one answer.
   */
  total_voters: number;
  /** The options this account itself chose. */
  selected_idx: number[];
}

/* --- operational reporting ------------------------------------------------ */

/**
 * The operational hierarchy, separate from the workspace ownership role in
 * `User.role`. One answers "what may this person change", the other "what are
 * they responsible for".
 */
export type OperationalRole = 'leader' | 'pic' | 'freelance';

/** Where an outgoing message came from. Null on anything inbound. */
export type SenderSource =
  | 'web_admin'
  | 'whatsapp_device'
  | 'bot'
  | 'system'
  | 'broadcast'
  | 'story';

export type SLAStatus = 'waiting' | 'achieved' | 'breached' | 'excluded';
export type LeadStatus = 'verified_new' | 'historical' | 'unknown';
export type CampaignType = 'story' | 'broadcast';
export type CampaignStatus =
  | 'draft'
  | 'scheduled'
  | 'running'
  | 'completed'
  // Some recipients got it and some did not. Neither "completed" nor "failed"
  // is true of such a run: one hides the failures, the other invites somebody
  // to re-send messages that already arrived.
  | 'partial'
  | 'failed'
  | 'cancelled'
  // A Story past its 24 hours. Not a failure — it ran, then its time was up.
  | 'expired';

export interface AppRef {
  id: string;
  code: string;
  name: string;
  color: string;
}

/**
 * One entry in a person's history.
 *
 * Deliberately one shape for five kinds of event. Whichever link field is set
 * says what the row opens — an activity nobody can follow back to the message,
 * campaign or contact it describes is a claim rather than a record.
 */
export interface ActivityRow {
  /** Prefixed by source ("msg:", "lbl:", "fu:", "act:"): unique across tables. */
  id: string;
  kind: 'message_personal' | 'message_group' | 'label' | 'follow_up' | 'broadcast' | 'story';
  /** The machine detail within a kind, e.g. "label_assigned". */
  type: string;
  occurred_at: string;

  actor_id: string | null;
  actor_name: string | null;
  actor_role: OperationalRole | null;

  application_id: string | null;
  application_code: string | null;
  application_name: string | null;
  application_color: string | null;
  account_id: string | null;
  account_name: string | null;

  status: string | null;
  /** Compares when it happened against the rota. Never decides who did it. */
  in_schedule: boolean | null;
  /** What the activity was about: a contact, a group, a campaign. */
  subject: string | null;
  detail: string | null;

  conversation_id: string | null;
  message_id: string | null;
  campaign_id: string | null;
  contact_id: string | null;
  source: string | null;
}

/** Where a conversation lives, so a deep link can open the right room. */
export interface ConversationLocation {
  conversation_id: string;
  account_id: string;
  application_id: string | null;
}

export interface OrgMember {
  user_id: string;
  email: string;
  full_name: string | null;
  avatar_url: string | null;
  workspace_role: string;
  /** Empty until a Leader assigns one. */
  role: OperationalRole | '';
  /**
   * False when the member has been deactivated. The profile is kept so past
   * messages and reports stay attributable; what they lose is access.
   */
  is_active: boolean;
  pic_user_id: string | null;
  pic_name: string | null;
  applications: AppRef[];
  freelance_count: number;
  created_at: string;
}

export interface WorkSchedule {
  id: string;
  workspace_id: string;
  user_id: string;
  user_name: string | null;
  pic_user_id: string | null;
  pic_name: string | null;
  application_id: string | null;
  application_code: string | null;
  account_id: string | null;
  account_name: string | null;
  /**
   * YYYY-MM-DD in the schedule's own timezone. Null on a weekly pattern row.
   *
   * Exactly one of work_date and weekday is ever set: a row says either "this
   * date" or "every one of these days". A dated row wins over the pattern for
   * that person on that date, which is how a holiday or a swapped shift is
   * written without disturbing the pattern behind it.
   */
  work_date: string | null;
  /** 0 (Minggu) to 6 on a weekly pattern row; null on a dated one. */
  weekday: number | null;
  starts_at: string;
  ends_at: string;
  timezone: string;
  is_active: boolean;
  note: string | null;
  created_at: string;
}

/**
 * One day's operational figures. Counts only — no conversion rate anywhere,
 * because a ratio hides the two numbers a Leader actually acts on.
 */
export interface DashboardSummary {
  date: string;

  /* --- chat pribadi ---
   * Message fields count BUBBLES; contact fields count DISTINCT contacts over
   * the whole period, recomputed rather than summed from the daily rows. */
  inbound_personal: number;
  /** Bubbles a person sent from the web. Broadcast, bot, system and phone excluded. */
  outbound_manual_personal: number;
  /** Bubbles typed on the phone: real work, but nobody verifiable to credit. */
  outbound_device_personal: number;

  contacts_inbound: number;
  /**
   * The figure above split by whether the contact's FIRST message of the
   * period landed inside a working window. The two always sum to
   * `contacts_inbound`, which stays the true total.
   */
  contacts_inbound_in_schedule: number;
  contacts_inbound_out_of_schedule: number;
  /** False when no rota covers this period, which makes the split meaningless. */
  schedule_configured: boolean;
  contacts_served: number;
  contacts_unserved: number;
  verified_new_leads: number;

  /* --- waktu respons & SLA ---
   * Personal chats only: a group has no single customer waiting for an answer,
   * and a Broadcast is not a question. The two durations are null rather than
   * zero when nothing was answered — "no cycle completed" and "answered
   * instantly" are different statements. */
  avg_first_response_seconds: number | null;
  median_first_response_seconds: number | null;
  /**
   * From COMPLETED cycles only. A cycle still waiting has no duration yet, and
   * letting one in would make "terlama" mean "the oldest thing nobody has
   * answered" — a different fact, reported separately as sla_waiting.
   */
  fastest_response_seconds: number | null;
  slowest_response_seconds: number | null;
  sla_achieved: number;
  sla_breached: number;
  /** The denominator for the achieved percentage: cycles that have an answer. */
  sla_completed: number;
  /** Conversations still waiting for a first manual reply. */
  sla_waiting: number;
  /** False when no cycle in range carried a target: show "Belum dikonfigurasi". */
  sla_configured: boolean;

  /* --- antrean di luar jam kerja ---
   *
   * Messages that arrived when nobody was on shift. Deliberately NOT part of
   * any sla_ figure above: the SLA asks how fast somebody on duty answers, and
   * these had nobody on duty to answer them. Every duration here is measured
   * from the moment the shift opened. */
  queued_total: number;
  queued_answered: number;
  queued_waiting: number;
  /** Null until one has been answered: "belum ada" is not "instan". */
  queued_avg_seconds: number | null;
  queued_slowest_seconds: number | null;

  /* --- follow-up: counts only, never a rate --- */
  follow_ups: number;
  follow_up_contacts: number;
  follow_ups_answered: number;
  follow_ups_unanswered: number;

  /* --- chat grup: kept entirely apart from the personal figures --- */
  group_inbound: number;
  group_replies: number;
  groups_active: number;
  groups_handled: number;

  /* --- label --- */
  contacts_first_labeled: number;
  label_changes_total: number;
  labels_assigned: number;
  labels_removed: number;
  labels_moved: number;
  label_contacts_changed: number;

  /* --- aktivitas campaign ---
   * Counted as activity, never as chat: a Broadcast's outgoing messages carry
   * sender_source = 'broadcast' and are excluded from every figure above. */
  broadcasts_created: number;
  broadcasts_scheduled: number;
  broadcasts_running: number;
  /** Campaigns that finished completely; partial runs are their own figure. */
  broadcasts_sent: number;
  broadcasts_partial: number;
  broadcasts_failed: number;
  broadcasts_cancelled: number;
  /** Recipients, not campaigns. */
  broadcast_targets_sent: number;

  stories_created: number;
  stories_scheduled: number;
  stories_running: number;
  stories_published: number;
  stories_partial: number;
  stories_failed: number;
  stories_expired: number;
  /** A LOWER BOUND from real receipts: whatsmeow exposes no viewer list. */
  story_views_detected: number;

  /* --- jadwal: pembanding kepatuhan, tidak pernah penentu pelaku --- */
  work_seconds: number;
  active_days: number;
  /**
   * WIB calendar days of the period that have actually happened — the divisor
   * for per-day averages. On the third of the month this is 3, not 31.
   */
  elapsed_days: number;
  activities_in_schedule: number;
  activities_out_of_schedule: number;
}

/** One label's activity over the period. */
export interface LabelUsage {
  label_id: string | null;
  name: string;
  color: string;
  assigned: number;
  removed: number;
  /** Distinct contacts this label moved on during the period. */
  contacts: number;
  /** How many carry it right now — a fact about today, not about the period. */
  active_contacts: number;
}
export type PerformanceDay = DashboardSummary;

/**
 * Rate figures, shown beside the raw counts and never instead of them.
 * Null rather than zero when nothing was scheduled: "no rota recorded" and
 * "worked all day and achieved nothing" are different statements.
 */
export interface PerHourMetrics {
  work_hours: number;
  messages_per_hour: number | null;
  chats_per_hour: number | null;
  group_replies_per_hour: number | null;
  follow_ups_per_hour: number | null;
}

export interface PerformanceReport {
  from: string;
  to: string;
  summary: DashboardSummary;
  days: PerformanceDay[];
  per_hour: PerHourMetrics;
  /**
   * The same summary over the window of equal length immediately before this
   * one. Present only when the request asked with `compare=true`, and absent
   * rather than zeroed when it could not be computed: a delta is drawn only
   * from a real measurement.
   */
  previous?: DashboardSummary;
  previous_from?: string;
  previous_to?: string;
}

/**
 * One person's row in the per-person tables.
 *
 * Carries a full DashboardSummary rather than the smaller MemberMetrics, so the
 * table draws from the same column model as Rincian Per Hari and a metric
 * cannot mean one thing in one table and another in the next.
 */
export interface MemberBreakdown {
  user_id: string | null;
  name: string;
  email: string;
  role: OperationalRole | '';
  is_active: boolean;
  on_duty: boolean;
  pic_user_id: string | null;
  pic_name: string | null;
  freelance_count: number;
  applications: AppRef[];
  /**
   * How the figures were narrowed, and not decoration. A PIC's row is their
   * whole application: their own work, their Freelance's, and the phone
   * activity on those numbers. Everyone else's row is what their own account
   * did. Reading a PIC's row as personal effort would be wrong.
   */
  scope: 'team' | 'personal';
  summary: DashboardSummary;
}

/**
 * One application's share of the period being read.
 *
 * The same DashboardSummary the cards are drawn from, narrowed to one
 * application, so a row of the breakdown and the card above it can never mean
 * two different things.
 */
export interface ApplicationPerformance {
  application: AppRef;
  summary: DashboardSummary;
  /**
   * Contacts this application holds right now, across all of its numbers.
   *
   * Outside `summary` on purpose: every figure in there answers "during the
   * period", and this one answers "as of now". The two live apart so a reader
   * cannot take an address-book total for a day's growth.
   */
  contacts_total: number;
}

/**
 * Where a private reply lands: the one-to-one thread with whoever wrote a
 * group message. Resolved by the server, which also opens the thread when
 * there is not one yet.
 */
export interface PrivateReplyTarget {
  conversation_id: string;
  account_id: string;
  /** Completes the chat route, which is /chat/{applicationId}/{accountId}. */
  application_id: string | null;
  name: string;
  phone_number: string;
  /** The group the quoted message was written in. */
  group_name: string;
}

/** What the signed-in user may see, mirroring the backend's scope resolution. */
export interface AnalyticsScope {
  role: OperationalRole | '';
  is_leader: boolean;
  can_manage: boolean;
  application_ids: string[];
  admin_ids: string[];
}

export interface AccountRef {
  id: string;
  name: string;
  phone_number: string | null;
  application_id: string | null;
}

export interface PersonRef {
  id: string;
  name: string;
  role: OperationalRole;
}

export interface ShiftRef {
  id: string;
  label: string;
  user_id: string;
  user_name: string;
  work_date: string;
}

/** Already narrowed to the caller's scope by the server. */
export interface FilterOptions {
  role: OperationalRole | '';
  applications: AppRef[];
  accounts: AccountRef[];
  pics: PersonRef[];
  freelancers: PersonRef[];
  shifts: ShiftRef[];
}

/**
 * What one person did, as opposed to what happened.
 *
 * A different shape from DashboardSummary on purpose: figures there like
 * inbound messages or newly-labelled contacts describe a workspace and cannot
 * be attributed to anybody — nobody on our side wrote an inbound message.
 * Every field here is derived from a row that names the admin.
 */
export interface MemberMetrics {
  /* --- chat pribadi --- */
  /** Bubbles this person sent from the web. */
  outbound_manual: number;
  /** Distinct contacts they replied to. On a team row the same contact served
   *  by two people counts once, which is why team totals are recomputed. */
  contacts_served: number;
  /** Credited to whoever sent the first manual reply in the conversation. */
  verified_new_leads: number;

  /* --- waktu respons & SLA ---
   * Credited to whoever actually sent the first manual reply. A cycle closed
   * from a phone has no verifiable responder and counts for the device, never
   * for a person. */
  avg_first_response_seconds: number | null;
  median_first_response_seconds: number | null;
  sla_achieved: number;
  sla_breached: number;

  /* --- follow-up --- */
  follow_ups: number;
  follow_up_contacts: number;
  follow_ups_answered: number;
  follow_ups_unanswered: number;

  /* --- chat grup --- */
  group_replies: number;
  groups_handled: number;

  /* --- label --- */
  label_changes: number;
  label_contacts: number;

  /* --- campaign --- */
  broadcasts_created: number;
  broadcasts_sent: number;
  stories_created: number;
  stories_published: number;
  campaigns_failed: number;

  /* --- jadwal --- */
  work_seconds: number;
  active_days: number;
  activities_in_schedule: number;
  activities_out_of_schedule: number;

  /** Null when nothing was scheduled, which is not the same as zero. */
  messages_per_hour: number | null;
  contacts_per_hour: number | null;
  group_replies_per_hour: number | null;
  campaigns_per_hour: number | null;
  follow_ups_per_hour: number | null;
}
export interface MemberPerformance {
  /** Null on the unattributed bucket, which belongs to no person. */
  user_id: string | null;
  name: string;
  email: string;
  role: OperationalRole | '';
  is_active: boolean;
  /** Whether a shift covers this moment. */
  on_duty: boolean;
  pic_user_id: string | null;
  pic_name: string | null;
  applications: AppRef[];
  freelance_count: number;

  /** What this person did with their own hands. */
  personal: MemberMetrics;
  /**
   * A PIC's own activity plus every Freelance under them. Null for anyone who
   * leads no team. Kept apart from `personal`: a PIC whose team is busy and who
   * answers nothing themselves is a different situation from one carrying the
   * inbox alone, and one merged number hides which.
   */
  team: MemberMetrics | null;
}

export interface TeamReport {
  from: string;
  to: string;
  role: OperationalRole | '';
  members: MemberPerformance[];
  /**
   * Activity from a phone whose operator WhatsApp does not identify. Its own
   * row rather than divided among whoever was on shift, because dividing it
   * would be a guess.
   */
  unattributed: MemberMetrics;
}

/**
 * One message behind a chat figure.
 *
 * Carries the actor rather than inferring one: a message typed on the phone has
 * no actor, and the row says so instead of naming whoever was on shift.
 */
export interface MessageActivityRow {
  id: string;
  conversation_id: string;
  chat_type: ConversationType;
  conversation_name: string | null;
  phone_number: string | null;
  application_code: string | null;
  account_name: string | null;
  timestamp: string;
  message_type: MessageType;
  preview: string;
  source: SenderSource | null;
  actor_id: string | null;
  actor_name: string | null;
  /** Null for inbound messages and for anything with no verifiable actor. */
  in_schedule: boolean | null;
}

export interface SLACycleRow {
  id: string;
  conversation_id: string;
  conversation_name: string | null;
  phone_number: string | null;
  application_code: string | null;
  account_name: string | null;
  inbound_message_id: string;
  started_at: string;
  inbound_message_count: number;
  first_response_message_id: string | null;
  responded_at: string | null;
  raw_duration_seconds: number | null;
  business_duration_seconds: number | null;
  target_seconds: number;
  status: SLAStatus;
  exclusion_reason: string | null;
  responder_admin_id: string | null;
  responder_name: string | null;
  responder_source: SenderSource | null;

  /**
   * The ids a deep link into the inbox needs. The chat route is
   * /chat/{applicationId}/{accountId} with the thread chosen inside it, so all
   * three travel together — a link built from the phone number alone opens the
   * wrong room whenever a customer has written to two of our numbers.
   */
  application_id: string | null;
  account_id: string | null;
  application_color: string | null;
  /** How long this cycle has been open, measured on the server. */
  waiting_seconds: number | null;
  /** Whether the wait already passed the target this cycle was created with. */
  breached: boolean | null;
}

export interface FollowUpRow {
  id: string;
  conversation_id: string;
  conversation_name: string | null;
  phone_number: string | null;
  application_code: string | null;
  local_date: string;
  started_at: string;
  message_count: number;
  gap_seconds: number | null;
  last_inbound_at: string | null;
  admin_id: string | null;
  admin_name: string | null;
  admin_source: SenderSource | null;
  responded_at: string | null;
  trigger_message_id: string;
}

export interface GroupMentionRow {
  id: string;
  conversation_id: string;
  group_name: string | null;
  application_code: string | null;
  account_name: string | null;
  message_id: string;
  participant_jid: string | null;
  sender_phone: string | null;
  sender_name: string | null;
  body: string | null;
  mentioned_at: string;
  responded_at: string | null;
  responder_admin_id: string | null;
  responder_name: string | null;
  responder_source: SenderSource | null;
}

export interface LabelEventRow {
  id: string;
  event_type:
    | 'label_created'
    | 'label_updated'
    | 'label_deleted'
    | 'label_assigned'
    | 'label_removed'
    | 'label_moved';
  contact_id: string | null;
  contact_name: string | null;
  phone_number: string | null;
  conversation_id: string | null;
  from_label_name: string | null;
  to_label_name: string | null;
  /** "whatsapp" carries no admin: the phone does not say who was holding it. */
  source: 'web' | 'whatsapp' | 'system';
  admin_id: string | null;
  admin_name: string | null;
  application_code: string | null;
  account_name: string | null;
  occurred_at: string;
}

export interface LabelTransition {
  from_label: string;
  to_label: string;
  count: number;
  contacts: number;
}

export interface LeadRow {
  contact_id: string;
  name: string | null;
  phone_number: string | null;
  application_code: string | null;
  account_name: string | null;
  lead_status: LeadStatus;
  /** Why the classifier decided this, so a Leader can check rather than trust. */
  status_reason: string;
  qualified_date: string | null;
  first_inbound_at: string | null;
  conversation_id: string | null;
}

export interface Campaign {
  id: string;
  workspace_id: string;
  application_id: string | null;
  application_code: string | null;
  account_id: string | null;
  account_name: string | null;
  campaign_type: CampaignType;
  name: string;
  body: string | null;
  attachment_ids: string[];
  status: CampaignStatus;
  scheduled_at: string | null;
  executed_at: string | null;
  target_count: number;
  success_count: number;
  failed_count: number;
  failure_reason: string | null;
  created_by: string | null;
  creator_name: string | null;
  creator_role: OperationalRole | null;
  created_at: string;
  updated_at: string;

  /* --- eksekusi --- */
  /** Paces the queue. Not a claim about staying unblocked. */
  delay_profile: DelayProfile;
  compose_mode: ComposeMode;
  /** What the author wrote, before spintax was resolved and variables filled. */
  message_template: string | null;
  /** A link. The file is never stored — it is fetched at send time and deleted. */
  media_url: string | null;
  media_kind: string | null;
  media_mime: string | null;
  media_size_bytes: number | null;
  /** An uploaded document's display name. Kept after the file is deleted. */
  media_file_name: string | null;
  caption: string | null;
  target_source: TargetSource | null;
  started_at: string | null;
  finished_at: string | null;
  cancelled_at: string | null;
  cancel_requested: boolean;
  max_attempts: number;
  retry_gap_seconds: number;
  archived_at: string | null;
  device_count: number;
  /** The sending numbers by name, in the order they were chosen. Never null. */
  device_names: string[];
}

/**
 * One campaign read back into the shape the composer takes.
 *
 * Everything here is reconstructed from the rows the campaign already has —
 * its sender devices, its resolved targets, the variables rendered onto them.
 * Nothing is stored a second time for the composer's benefit.
 */
export interface CampaignSource {
  campaign_type: CampaignType;
  status: CampaignStatus;
  /** False once it has started. Its content can be copied, not rewritten. */
  editable: boolean;

  name: string;
  body: string;
  compose_mode: ComposeMode;
  application_id: string | null;
  account_ids: string[];

  media_url: string | null;
  media_kind: string | null;
  media_mime: string | null;
  media_size_bytes: number | null;
  media_storage_path: string | null;
  media_file_name: string | null;
  caption: string | null;

  delay_profile: DelayProfile;
  delay_min_seconds: number | null;
  delay_max_seconds: number | null;
  auto_retry_on_disconnect: boolean;

  recurrence: '' | Recurrence;
  /** WIB wall clock, "HH:MM". Empty when it does not repeat. */
  recurrence_time: string;
  recurrence_weekday: number | null;
  recurrence_day: number | null;

  target_source: TargetSource | '';
  numbers: string[];
  contact_ids: string[];
  group_ids: string[];

  custom_values: Record<string, string>;
  label_ids: string[];
}

export type DelayProfile = 'super_cepat' | 'cepat' | 'normal' | 'aman' | 'santai';
export type ComposeMode = 'plain' | 'spintax' | 'variables' | 'spintax_variables' | 'gpt';
export type TargetSource = 'manual' | 'csv' | 'contacts' | 'group_members' | 'groups';
/** How often a recurring broadcast comes back around. */
export type Recurrence = 'daily' | 'weekly' | 'monthly';
export type TargetStatus =
  | 'pending'
  | 'processing'
  | 'sent'
  | 'delivered'
  | 'read'
  | 'failed'
  | 'cancelled'
  | 'skipped'
  | 'retry_wait'
  | 'invalid';

/** One sending number on a campaign, with its own tally. */
export interface BroadcastDevice {
  id: string;
  campaign_id: string;
  account_id: string;
  account_name: string;
  phone_number: string | null;
  position: number;
  status: string;
  assigned_count: number;
  sent_count: number;
  failed_count: number;
  started_at: string | null;
  finished_at: string | null;
  failure_reason: string | null;
  /** Read from the live session, not the database: a number online yesterday
   *  may not be online now. */
  connected: boolean;
}

export interface CampaignTarget {
  id: string;
  campaign_id: string;
  contact_id: string | null;
  conversation_id: string | null;
  account_id: string | null;
  account_name: string | null;
  chat_jid: string;
  target_type: string;
  phone_number: string | null;
  display_name: string | null;
  status: TargetStatus;
  attempt: number;
  /** The exact text this person received, after spintax and variables. */
  rendered_body: string | null;
  wa_message_id: string | null;
  sent_at: string | null;
  delivered_at: string | null;
  read_at: string | null;
  next_attempt_at: string | null;
  failure_reason: string | null;
  error_code: string | null;
  invalid_reason: string | null;
  /** Whether this contact wrote back. An ordinary inbound message everywhere
   *  else; shown here only so the report can say how many answered. */
  replied: boolean;
}

export interface TargetTotals {
  total: number;
  valid: number;
  invalid: number;
  pending: number;
  processing: number;
  sent: number;
  delivered: number;
  read: number;
  failed: number;
  cancelled: number;
  retry_wait: number;
  /** Second-or-later attempts made in total. */
  retries: number;
}

export interface BroadcastReport {
  campaign: Campaign;
  devices: BroadcastDevice[];
  totals: TargetTotals;
  duration_seconds: number | null;
  replies: number;
  labels: CampaignLabel[];
}

export interface StoryPublication {
  id: string;
  campaign_id: string;
  account_id: string;
  account_name: string;
  phone_number: string | null;
  status: string;
  attempt: number;
  wa_message_id: string | null;
  published_at: string | null;
  expires_at: string | null;
  failure_reason: string | null;
  error_code: string | null;
  connected: boolean;
  /**
   * Distinct viewers whose read receipt this server actually received. A LOWER
   * BOUND, never a view count: a viewer with read receipts off never produces
   * one, and a receipt arriving while the backend is down is not replayed.
   */
  detected_views: number;
  /** Frozen once the Story expired; null until then. Kept for good after that:
   *  the viewer list is dropped, the number is not. */
  final_views: number | null;
}

export interface StoryReport {
  campaign: Campaign;
  publications: StoryPublication[];
  /** Sum across publications: one person watching on two of our numbers
   *  watched two Stories, and both are real. */
  views_per_device: number;
  /** Deduplicated by viewer across the campaign. Labelled differently from the
   *  figure above on purpose, so the two are never read as the same thing. */
  unique_viewers: number;
  /** False once any figure has been frozen: after 24 hours only the number is
   *  kept, so "how many different people" can no longer be worked out. */
  unique_viewers_known: boolean;
  /** False before anything is published, so the interface can say
   *  "Data views belum tersedia" instead of drawing a zero. */
  views_available: boolean;
  labels: CampaignLabel[];
}

/** An internal tag on a campaign. Unrelated to WhatsApp's own labels — this one
 *  never reaches a customer's phone. */
export interface CampaignLabel {
  id: string;
  name: string;
  color: string;
  archived_at: string | null;
  campaign_count: number;
  created_at: string;
}

export interface CustomVariable {
  id: string;
  key: string;
  /** null means the variable belongs to the workspace, not to one brand. */
  application_id: string | null;
  application_code: string | null;
  application_name: string | null;
  application_color: string | null;
  label: string;
  default_value: string | null;
  description: string | null;
  is_active: boolean;
  created_at: string;
}

/** The response-time promise one application is measured against. */
export interface SLATarget {
  id: string;
  /** null is the workspace default, used by applications without a row. */
  application_id: string | null;
  application_code: string | null;
  application_name: string | null;
  application_color: string | null;
  target_seconds: number;
  /** Limits the wait to scheduled working time. */
  business_hours: boolean;
  updated_at: string;
}

/**
 * A canned message summoned in a chat by typing a slash and its shortcut.
 *
 * Same application scoping as CustomVariable, deliberately: the two are
 * configured on neighbouring screens and filtered the same way.
 */
export interface QuickReply {
  id: string;
  application_id: string | null;
  application_code: string | null;
  application_name: string | null;
  application_color: string | null;
  /** Stored without the leading slash. */
  shortcut: string;
  title: string;
  body: string;
  /** Free grouping for the list. Optional, and means nothing to the sender. */
  category: string | null;
  /**
   * When set, the reply is sent as a PICTURE with `body` as its caption.
   *
   * Only the address is stored. The file is fetched server-side at send time
   * and deleted; the browser never sees the URL and never handles the bytes.
   */
  media_url: string | null;
  /** What was measured when the address was saved. Always 'image' when set. */
  media_kind: string | null;
  media_mime: string | null;
  media_size_bytes: number | null;
  /**
   * How many times this reply was actually SENT — not how many times it was
   * picked. Picking one and clearing the box again is not use.
   */
  usage_count: number;
  last_used_at: string | null;
  is_active: boolean;
  created_at: string;
}

/** One device's share of the recipients, on the review screen. */
export interface DevicePlan {
  account_id: string;
  account_name: string;
  phone_number: string | null;
  connected: boolean;
  assigned: number;
}

export interface TargetProblem {
  input: string;
  reason: string;
}

/** The review shown before a campaign runs. */
export interface TargetPlan {
  devices: DevicePlan[];
  valid: number;
  invalid: number;
  duplicates: number;
  unreachable: number;
  problems: TargetProblem[];
  /** Derived from the delay profile and the busiest device's share, since
   *  devices send in parallel. */
  estimated_seconds: number;
  delay_profile: DelayProfile;
  preview: string[];
  missing_variables: string[];
  template_problems: string[];
}

/** One candidate recipient, tied to the device that can reach it. */
export interface AudienceEntry {
  account_id: string;
  chat_jid: string;
  phone_number: string;
  name: string;
  contact_id: string | null;
  conversation_id: string | null;
  kind: string;
}

export interface CampaignSchedule {
  id: string;
  scheduled_at: string;
  timezone: string;
  status: CampaignStatus;
  attempt: number;
  started_at: string | null;
  finished_at: string | null;
  failure_reason: string | null;
  superseded_at: string | null;
  created_by: string | null;
  creator_name: string | null;
  created_at: string;
}

export interface CampaignActivity {
  id: string;
  activity_type: string | null;
  action: string | null;
  entity_name: string | null;
  admin_id: string | null;
  admin_name: string | null;
  admin_role: OperationalRole | null;
  status: string | null;
  failure_reason: string | null;
  detail: unknown;
  occurred_at: string;
}

export interface Contact {
  id: string;
  account_id: string;
  jid: string;
  phone_number: string | null;
  name: string | null;
  push_name: string | null;
  avatar_url: string | null;
  is_business: boolean;

  /** Which WhatsApp number holds this contact, and which brand it belongs to. */
  account_name: string | null;
  account_phone: string | null;
  application_id: string | null;
  application_code: string | null;
  application_color: string | null;

  /** WhatsApp's own labels on this contact, the same ones the phone shows. */
  labels: string[];
}

export interface SyncResult {
  window_days: number;
  contacts: number;
  groups: number;
  /** Threads that gained a contact link or display name from the address book. */
  linked: number;
  conversations: number;
  messages: number;
  skipped_out_of_window: number;
  labels: number;
  pruned: number;
  /** True when the phone was asked for more history; it arrives asynchronously. */
  history_pending: boolean;
  /**
   * App-state collections whose plaintext copy was requested from the phone
   * because the encrypted sync could not be verified. This is a normal
   * self-healing step, not a failure — the data lands a few seconds later and
   * arrives as a `sync.progress` event with phase "app_state".
   */
  app_state_recovering?: string[];
  /**
   * Set only when app-state could not be read AND recovery could not even be
   * requested. Labels and per-chat read state are genuinely stale then.
   */
  app_state_error?: string;
}

export interface MessageStatusChange {
  id: string;
  conversation_id: string;
  wa_message_id: string;
  status: MessageStatus;
}

/* --- realtime envelope ---------------------------------------------------- */

export type RealtimeEventType =
  | 'account.status'
  | 'account.qr'
  | 'account.deleted'
  | 'message.new'
  | 'message.status'
  | 'conversation.updated'
  | 'conversation.deleted'
  | 'message.hidden'
  | 'sync.progress'
  | 'labels.updated'
  | 'labels.sync_state'
  | 'metrics.updated'
  | 'campaign.updated'
  | 'schedule.updated'
  | 'presence.viewers';

/** One person with a conversation open in another browser. */
export interface PresenceViewer {
  user_id: string;
  name: string;
}

/**
 * Who has a conversation open right now. Sent whenever that changes, and once
 * per open conversation to a browser that has just connected. An empty list
 * means the last person left.
 */
export interface PresenceViewersPayload {
  conversation_id: string;
  viewers: PresenceViewer[];
}

/**
 * A conversation's derived reporting rows were rebuilt.
 *
 * Carries only the id: the figures are computed server-side, and pushing
 * partial numbers would invite the browser to start adding them up.
 */
export interface MetricsUpdatedPayload {
  conversation_id: string;
}

/** Whole label set for an account, pushed after any change on either side. */
export interface LabelsUpdatedPayload {
  account_id?: string;
  reason: string;
  labels: Label[];
}

export interface LabelSyncStatePayload {
  account_id: string;
  state: LabelSyncState;
  detail: string;
}

export interface RealtimeEvent<T = unknown> {
  type: RealtimeEventType;
  payload: T;
  timestamp: string;
}

export interface AccountStatusPayload {
  account_id: string;
  status: AccountStatus;
  status_detail: string | null;
  account?: Account;
}

export interface AccountQRPayload {
  account_id: string;
  qr: string;
  expires_in?: number;
  expired?: boolean;
}

export interface MessageNewPayload {
  account_id: string;
  message: Message;
}

export interface MessageStatusPayload {
  account_id: string;
  changes: MessageStatusChange[];
  message?: Message;
}

export interface AccountDeletedPayload {
  account_id: string;
}

export interface MessageHiddenPayload {
  account_id: string;
  conversation_id: string;
  message_id: string;
}

export interface ConversationDeletedPayload {
  conversation_id: string;
  /** True when only the history was cleared and the thread itself stays. */
  cleared: boolean;
}
