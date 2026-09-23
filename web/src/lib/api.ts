'use client';

import { getSupabaseBrowserClient } from '@/lib/supabase/client';
import type {
  Account,
  AccountStats,
  ActivityRow,
  AnalyticsScope,
  Application,
  AudienceEntry,
  ConversationLocation,
  Campaign,
  CampaignActivity,
  CampaignLabel,
  CampaignSchedule,
  CampaignSource,
  CampaignType,
  ComposeMode,
  Contact,
  CustomVariable,
  QuickReply,
  SLATarget,
  DelayProfile,
  TargetPlan,
  TargetSource,
  Conversation,
  ConversationCounts,
  GroupDirectoryMember,
  GroupMember,
  GroupRow,
  ImportResult,
  Label,
  MergeResult,
  MediaLink,
  Message,
  OperationalRole,
  OrgMember,
  PrivateReplyTarget,
  Recurrence,
  StoryReport,
  SyncResult,
  User,
  Workspace,
  WorkSchedule,
} from '@/lib/types';

const API_URL = (process.env.NEXT_PUBLIC_API_URL ?? 'http://localhost:8080').replace(/\/+$/, '');

/** An error carrying the backend's machine-readable code and HTTP status. */
export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly data: unknown;

  constructor(status: number, code: string, message: string, data?: unknown) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
    this.code = code;
    this.data = data;
  }
}

async function accessToken(): Promise<string | null> {
  const supabase = getSupabaseBrowserClient();
  const { data } = await supabase.auth.getSession();
  return data.session?.access_token ?? null;
}

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const token = await accessToken();
  const headers = new Headers(init.headers);
  headers.set('Accept', 'application/json');
  if (init.body) headers.set('Content-Type', 'application/json');
  if (token) headers.set('Authorization', `Bearer ${token}`);

  const res = await fetch(`${API_URL}/api/v1${path}`, { ...init, headers });

  if (res.status === 204) return undefined as T;

  const text = await res.text();
  const payload: unknown = text ? JSON.parse(text) : null;

  if (!res.ok) {
    const body = (payload ?? {}) as { error?: string; message?: string; data?: unknown };
    throw new ApiError(
      res.status,
      body.error ?? 'unknown_error',
      body.message ?? `Permintaan gagal (${res.status})`,
      body.data,
    );
  }
  return payload as T;
}

/** Fetcher shaped for SWR: the cache key is the API path. */
export const fetcher = <T>(path: string) => request<T>(path);

/* --- session -------------------------------------------------------------- */

export const getMe = () => request<{ user: User; workspace: Workspace }>('/me');

/* --- applications --------------------------------------------------------- */

export const listApplications = () =>
  request<{ applications: Application[] }>('/applications');

export const createApplication = (body: { code: string; name?: string; color?: string }) =>
  request<Application>('/applications', { method: 'POST', body: JSON.stringify(body) });

export const updateApplication = (
  id: string,
  body: { name?: string; color?: string; is_active?: boolean },
) => request<Application>(`/applications/${id}`, { method: 'PATCH', body: JSON.stringify(body) });

export const deleteApplication = (id: string) =>
  request<{ deleted: boolean }>(`/applications/${id}`, { method: 'DELETE' });

/** PNG, JPG or WebP, at most 2 MB — checked again on the server from the bytes. */
export const APP_ICON_TYPES = 'image/png,image/jpeg,image/webp';
export const MAX_APP_ICON_BYTES = 2 * 1024 * 1024;

/**
 * Sets an application's logo. Leader only.
 *
 * The file goes to the private bucket; what comes back is a link that expires,
 * never the storage key. The list of applications re-signs it every time it is
 * read, so the logo keeps working after this particular link lapses.
 */
export async function uploadApplicationIcon(id: string, file: File): Promise<string | null> {
  const token = await accessToken();
  const headers = new Headers();
  if (token) headers.set('Authorization', `Bearer ${token}`);
  const body = new FormData();
  body.append('file', file);

  const res = await fetch(`${API_URL}/api/v1/applications/${id}/icon`, {
    method: 'POST',
    headers,
    body,
  });
  const detail = await res.json().catch(() => null);
  if (!res.ok) {
    throw new ApiError(
      res.status,
      detail?.error ?? 'upload_failed',
      detail?.message ?? 'Logo gagal diunggah.',
    );
  }
  return (detail?.icon_url as string | null) ?? null;
}

/** Removes the logo; the tile goes back to the code and colour. */
export const deleteApplicationIcon = (id: string) =>
  request<{ icon_url: null }>(`/applications/${id}/icon`, { method: 'DELETE' });

/* --- accounts ------------------------------------------------------------- */

export interface AccountQuery {
  application_id?: string;
  unassigned?: boolean;
  method?: string;
  search?: string;
}

/** Builds the accounts path; also used directly as an SWR cache key. */
export function accountsPath(params: AccountQuery = {}) {
  const q = new URLSearchParams();
  if (params.application_id) q.set('application_id', params.application_id);
  if (params.unassigned) q.set('unassigned', 'true');
  if (params.method) q.set('method', params.method);
  if (params.search) q.set('search', params.search);
  const suffix = q.toString() ? `?${q}` : '';
  return `/accounts${suffix}`;
}

export const listAccounts = (params: AccountQuery = {}) =>
  request<{ accounts: Account[] }>(accountsPath(params));

export const getAccount = (id: string) => request<Account>(`/accounts/${id}`);

export const getAccountStats = () => request<AccountStats>('/accounts/stats');

export const createAccount = (body: {
  name: string;
  label?: string | null;
  application_id?: string | null;
  connection_method?: string;
}) => request<Account>('/accounts', { method: 'POST', body: JSON.stringify(body) });

export const updateAccount = (
  id: string,
  body: {
    name?: string;
    label?: string | null;
    application_id?: string | null;
  },
) => request<Account>(`/accounts/${id}`, { method: 'PATCH', body: JSON.stringify(body) });

export const deleteAccount = (id: string) =>
  request<{ deleted: boolean }>(`/accounts/${id}`, { method: 'DELETE' });

export const pairAccount = (id: string) =>
  request<{ account_id: string; qr: string; status: string }>(`/accounts/${id}/pair`, {
    method: 'POST',
  });

export const getAccountQR = (id: string) =>
  request<{ account_id: string; qr: string; available: boolean }>(`/accounts/${id}/qr`);

export const connectAccount = (id: string) =>
  request<{ account_id: string; qr?: string; needs_pairing?: boolean; status: string }>(
    `/accounts/${id}/connect`,
    { method: 'POST' },
  );

export const disconnectAccount = (id: string) =>
  request<Account>(`/accounts/${id}/disconnect`, { method: 'POST' });

export const logoutAccount = (id: string) =>
  request<Account>(`/accounts/${id}/logout`, { method: 'POST' });

/**
 * Refreshes contacts, groups, labels, read state and chat history for one
 * account. `days` bounds the history window (default 7 on the server); `prune`
 * additionally removes already-stored messages older than that window.
 */
export const syncAccount = (id: string, opts: { days?: number; prune?: boolean } = {}) => {
  const q = new URLSearchParams();
  if (opts.days) q.set('days', String(opts.days));
  if (opts.prune) q.set('prune', 'true');
  const suffix = q.toString() ? `?${q}` : '';
  return request<SyncResult>(`/accounts/${id}/sync${suffix}`, { method: 'POST' });
};

export const listApplicationAccounts = (applicationId: string) =>
  request<{ accounts: Account[] }>(`/applications/${applicationId}/accounts`);

/* --- conversations -------------------------------------------------------- */

export interface ConversationQuery {
  search?: string;
  type?: string;
  status?: string;
  unread?: boolean;
  /** Only chats that named this account and have not been looked at yet. */
  mentions?: boolean;
  label_id?: string;
  limit?: number;
}

export function conversationsPath(accountId: string, query: ConversationQuery = {}) {
  const q = new URLSearchParams();
  if (query.search) q.set('search', query.search);
  if (query.type) q.set('type', query.type);
  if (query.status) q.set('status', query.status);
  if (query.unread) q.set('unread', 'true');
  if (query.mentions) q.set('mentions', 'true');
  if (query.label_id) q.set('label_id', query.label_id);
  if (query.limit) q.set('limit', String(query.limit));
  const suffix = q.toString() ? `?${q}` : '';
  return `/accounts/${accountId}/conversations${suffix}`;
}

export const listConversations = (accountId: string, query: ConversationQuery = {}) =>
  request<{ conversations: Conversation[] }>(conversationsPath(accountId, query));

/* --- status & channels ------------------------------------------------------ */

/** One Status somebody published, live for 24 hours. */
export interface StatusPost {
  message_id: string;
  sender_jid: string;
  sender_name: string;
  phone_number: string | null;
  avatar_url: string | null;
  type: string;
  body: string | null;
  caption: string | null;
  posted_at: string;
  expires_at: string;
  from_me: boolean;
  /** When this workspace opened it. Local only; nothing is sent to WhatsApp. */
  seen_at: string | null;
  attachment_id: string | null;
  /** pending, uploading, stored, failed or expired. A file still on its way is
   *  not a missing one, and the viewer has to tell the two apart. */
  attachment_status: string | null;
  /** Times the progress bar for a video. Null means use the fixed interval. */
  duration_secs: number | null;
  thumbnail_b64: string | null;
  /**
   * Distinct people this server saw a read receipt from. Meaningful only on our
   * own status, and a LOWER BOUND even there: WhatsApp gives a linked device no
   * viewer list, so a viewer with read receipts switched off never counts.
   * Always 0 on somebody else's status — their viewers report to them, not us.
   */
  viewers: number;
}

export function statusPath(accountId: string) {
  return `/accounts/${accountId}/status`;
}

/**
 * Records locally that a Status was opened.
 *
 * Nothing is sent to WhatsApp. A read receipt tells somebody their Status was
 * watched, and minting one because a list scrolled past would be inventing an
 * action nobody took.
 */
export const markStatusSeen = (messageId: string) =>
  request<{ seen: boolean }>(`/status/${messageId}/seen`, { method: 'POST' });

/**
 * Deletes one of our own Status posts for everyone, the way the phone does.
 * A Story posted by the scheduler comes down the same way, and its report
 * freezes the viewer count at that moment.
 */
export const revokeStatus = (messageId: string) =>
  request<{ revoked: boolean }>(`/status/${messageId}/revoke`, { method: 'POST' });

/** One channel this number follows. */
export interface Newsletter {
  id: string;
  account_id: string;
  jid: string;
  name: string;
  description: string | null;
  subscriber_count: number | null;
  picture_url: string | null;
  /** owner, admin, subscriber or guest, as WhatsApp reports it. */
  viewer_role: string;
  muted: boolean;
  verified: boolean;
  created_on: string | null;
  synced_at: string;
}

/** One post in a channel. Read from WhatsApp on demand, never stored. */
export interface NewsletterPost {
  server_id: string;
  text: string;
  kind: string;
  posted_at: string;
  view_count: number;
  reactions: Record<string, number>;
  /** A file sits behind the post; fetch it with channelMediaBlob. */
  has_media?: boolean;
  file_name?: string;
  mime?: string;
  /** Tiny JPEG preview, base64, drawn while the full file loads. */
  thumbnail?: string;
  /** The choices of a poll. WhatsApp sends no per-option tallies for channels. */
  poll_options?: string[];
  /** 1 for a single choice, 0 for "as many as you like". */
  poll_selectable_count?: number;
  /** Votes per option, same order as poll_options. Null when unreadable. */
  poll_votes?: number[] | null;
}

/**
 * The file behind one channel post, as a blob.
 *
 * Fetched with the caller's token and turned into an object URL by the caller,
 * so no link to it exists anywhere — the server passes it through from WhatsApp
 * and keeps nothing. Revoke the object URL when the post leaves the screen.
 */
export async function channelMediaBlob(
  accountId: string,
  jid: string,
  serverId: string,
): Promise<Blob> {
  const token = await accessToken();
  const headers = new Headers();
  if (token) headers.set('Authorization', `Bearer ${token}`);
  const q = new URLSearchParams({ jid, server_id: serverId });
  const res = await fetch(`${API_URL}/api/v1/accounts/${accountId}/newsletters/media?${q}`, {
    headers,
  });
  if (!res.ok) {
    const detail = await res.json().catch(() => null);
    throw new ApiError(
      res.status,
      detail?.error ?? 'media_failed',
      detail?.message ?? 'Berkas gagal dimuat.',
    );
  }
  return res.blob();
}

export function newslettersPath(accountId: string) {
  return `/accounts/${accountId}/newsletters`;
}

export const syncNewsletters = (accountId: string) =>
  request<{ newsletters: Newsletter[]; synced: number }>(
    `/accounts/${accountId}/newsletters/sync`,
    { method: 'POST' },
  );

export const newsletterPosts = (accountId: string, jid: string, limit = 30) =>
  request<{ posts: NewsletterPost[] }>(
    `/accounts/${accountId}/newsletters/posts?jid=${encodeURIComponent(jid)}&limit=${limit}`,
  );

export type NewsletterAction = 'preview' | 'follow' | 'unfollow' | 'mute';

/** `ref` is a channel address, or the invite link somebody pasted. */
export const newsletterAction = (
  accountId: string,
  body: { ref: string; action: NewsletterAction; muted?: boolean },
) =>
  request<{ newsletters?: Newsletter[]; newsletter?: Newsletter }>(
    `/accounts/${accountId}/newsletters/action`,
    { method: 'POST', body: JSON.stringify({ muted: false, ...body }) },
  );

/**
 * Whether this number may post to a channel.
 *
 * Presentation only. The server asks WhatsApp itself before it sends anything,
 * because a role read at the last sync can be an hour out of date — this decides
 * whether a composer is drawn, not whether a post is allowed.
 */
export function canPostToChannel(n: Newsletter): boolean {
  return n.viewer_role === 'owner' || n.viewer_role === 'admin';
}

/** What a channel post can be. A sticker is a WebP file sent as a sticker. */
export type ChannelPostKind =
  | 'text'
  | 'poll'
  | 'image'
  | 'video'
  | 'audio'
  | 'document'
  | 'sticker';

/**
 * Publishes a sentence or a poll to a channel we run.
 *
 * `text` is the message for a text post and the question for a poll. The reply
 * carries the channel's posts as WhatsApp lists them straight afterwards, which
 * may not include this one yet: `posted_at` is the proof it went out.
 */
export const postToChannel = (
  accountId: string,
  body: {
    jid: string;
    kind: 'text' | 'poll';
    text: string;
    options?: string[];
    selectable_count?: number;
  },
) =>
  request<{ posted_at: string; posts?: NewsletterPost[] }>(
    `/accounts/${accountId}/newsletters/post`,
    { method: 'POST', body: JSON.stringify(body) },
  );

/**
 * Publishes one file to a channel we run.
 *
 * The file is classified on the server from its own bytes, so `kind` is only
 * consulted for "sticker" — the one case where the same WebP means two different
 * things depending on which button was pressed.
 */
export async function postMediaToChannel(
  accountId: string,
  jid: string,
  file: File,
  opts: { caption?: string; kind?: ChannelPostKind; asDocument?: boolean } = {},
) {
  const token = await accessToken();
  const headers = new Headers();
  if (token) headers.set('Authorization', `Bearer ${token}`);

  const body = new FormData();
  body.append('jid', jid);
  body.append('file', file);
  if (opts.caption) body.append('caption', opts.caption);
  if (opts.kind) body.append('kind', opts.kind);
  if (opts.asDocument) body.append('as_document', 'true');

  const res = await fetch(`${API_URL}/api/v1/accounts/${accountId}/newsletters/media`, {
    method: 'POST',
    headers,
    body,
  });
  const detail = await res.json().catch(() => null);
  if (!res.ok) {
    throw new ApiError(
      res.status,
      detail?.error ?? 'post_failed',
      detail?.message ?? 'Postingan gagal dikirim.',
    );
  }
  return detail as { posted_at: string; posts?: NewsletterPost[] };
}

export const getConversationCounts = (accountId: string) =>
  request<ConversationCounts>(`/accounts/${accountId}/conversations/counts`);

export const getConversation = (id: string) => request<Conversation>(`/conversations/${id}`);

/** Participants of a group, with names resolved as the bubbles resolve them. */
export const listGroupMembers = (conversationId: string) =>
  request<{ members: GroupMember[] }>(`/conversations/${conversationId}/members`);

export type GroupMemberAction = 'promote' | 'demote' | 'remove';

/**
 * Promotes, demotes or removes a participant.
 *
 * Only an admin may do any of it, and WhatsApp is what enforces that — a
 * refusal comes back as 403 with the server's own reason.
 */
export const updateGroupMember = (
  conversationId: string,
  memberJid: string,
  action: GroupMemberAction,
) =>
  request<{ members: GroupMember[] }>(`/conversations/${conversationId}/members`, {
    method: 'POST',
    body: JSON.stringify({ member_jid: memberJid, action }),
  });

/** Changes a group's subject or description. */
export const updateGroup = (
  conversationId: string,
  body: { name?: string; description?: string },
) =>
  request<Conversation>(`/conversations/${conversationId}/group`, {
    method: 'PATCH',
    body: JSON.stringify(body),
  });

/** Re-reads the group from WhatsApp, for when the panel wants what is true now. */
export const refreshGroup = (conversationId: string) =>
  request<{ conversation: Conversation; members: GroupMember[] }>(
    `/conversations/${conversationId}/group/refresh`,
    { method: 'POST' },
  );

/**
 * The earliest mention not yet looked at, so opening a chat from the mention
 * filter lands on the message rather than at the bottom of a busy group.
 */
export const getFirstMention = (conversationId: string) =>
  request<{ message_id: string | null }>(`/conversations/${conversationId}/first-mention`);

export const setConversationStatus = (id: string, status: string) =>
  request<Conversation>(`/conversations/${id}`, {
    method: 'PATCH',
    body: JSON.stringify({ status }),
  });

export const markConversationRead = (id: string) =>
  request<Conversation>(`/conversations/${id}/read`, { method: 'POST' });

/**
 * Raises WhatsApp's manual "unread" flag — the same thing as long-pressing a
 * chat on the phone and choosing "Mark as unread". Distinct from having unread
 * messages, which is why it has its own endpoint.
 */
export const markConversationUnread = (id: string) =>
  request<{ conversation: Conversation; pushed_to_phone: boolean }>(
    `/conversations/${id}/unread`,
    { method: 'POST' },
  );

/**
 * Removes a thread from this inbox, or empties it when `clearOnly` is set.
 *
 * Local only: WhatsApp keeps its own copy on the phone, and a new message in
 * the same chat brings the thread straight back.
 */
export const deleteConversation = (id: string, clearOnly = false) =>
  request<{ deleted: boolean; cleared: boolean; files_removed: number }>(
    `/conversations/${id}${clearOnly ? '?clear=true' : ''}`,
    { method: 'DELETE' },
  );

export const listMessages = (conversationId: string, limit = 50) =>
  request<{ messages: Message[] }>(`/conversations/${conversationId}/messages?limit=${limit}`);

/** `replyTo` quotes a message from the same conversation. */
export const sendMessage = (conversationId: string, body: string, replyTo?: string | null) =>
  request<Message>(`/conversations/${conversationId}/messages`, {
    method: 'POST',
    body: JSON.stringify({ body, reply_to: replyTo ?? null }),
  });

/**
 * Re-sends a message into other conversations.
 *
 * Each destination is independent: the response reports how many went and
 * which ones did not, rather than failing the whole batch on one bad target.
 */
export const forwardMessage = (messageId: string, conversations: string[]) =>
  request<{ sent: number; failed: Array<{ conversation_id: string; reason: string }> }>(
    `/messages/${messageId}/forward`,
    { method: 'POST', body: JSON.stringify({ conversations }) },
  );

/**
 * Where a private reply to a group message would go.
 *
 * Asked before the composer opens, so the operator sees whose chat they are
 * about to land in. It also opens the one-to-one thread if there is not one
 * yet, which is the ordinary case: this exists for somebody who has only ever
 * written in the group.
 */
export const privateReplyTarget = (messageId: string) =>
  request<PrivateReplyTarget>(`/messages/${messageId}/private-reply`);

/**
 * Sends a one-to-one answer that quotes a group message.
 *
 * Not the same as opening the person's chat and typing: the quote points back
 * into the group, so the recipient can see which of forty messages is being
 * answered rather than getting an unexplained message from a number they may
 * not recognise.
 */
export const sendPrivateReply = (messageId: string, body: string) =>
  request<{ message: Message; target: PrivateReplyTarget }>(
    `/messages/${messageId}/private-reply`,
    { method: 'POST', body: JSON.stringify({ body }) },
  );

/* --- media ---------------------------------------------------------------- */

export interface SendMediaOptions {
  file: Blob;
  fileName: string;
  caption?: string;
  /** Send an image or video as a file, without WhatsApp recompressing it. */
  asDocument?: boolean;
  /**
   * Idempotency key for this file. Retrying with the same token reuses the
   * message the first attempt created instead of sending a second copy.
   */
  clientToken: string;
  /**
   * Quotes a message from the same conversation.
   *
   * Text replies have carried this since replies existed; media did not, so a
   * photo sent as a reply arrived attached to nothing. In a group that is the
   * whole meaning of the message.
   */
  replyTo?: string | null;
  /** Fraction uploaded, 0..1. */
  onProgress?: (fraction: number) => void;
  signal?: AbortSignal;
}

/**
 * Uploads one file and sends it as a message.
 *
 * XMLHttpRequest rather than fetch: it is the only way to observe upload
 * progress in a browser, and a 60 MB video with no progress bar is
 * indistinguishable from a hang. One file per request, so progress and retry
 * both apply to exactly one thing.
 */
export async function sendMedia(
  conversationId: string,
  opts: SendMediaOptions,
): Promise<Message> {
  const token = await accessToken();

  const form = new FormData();
  form.append('client_token', opts.clientToken);
  if (opts.caption) form.append('caption', opts.caption);
  if (opts.asDocument) form.append('as_document', 'true');
  if (opts.replyTo) form.append('reply_to', opts.replyTo);
  // The filename is the last argument; without it the browser sends "blob".
  form.append('file', opts.file, opts.fileName);

  return new Promise<Message>((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.open('POST', `${API_URL}/api/v1/conversations/${conversationId}/media`);
    xhr.responseType = 'text';
    xhr.setRequestHeader('Accept', 'application/json');
    if (token) xhr.setRequestHeader('Authorization', `Bearer ${token}`);

    if (opts.onProgress) {
      xhr.upload.onprogress = (event) => {
        if (event.lengthComputable) opts.onProgress?.(event.loaded / event.total);
      };
    }

    const abort = () => xhr.abort();
    opts.signal?.addEventListener('abort', abort);
    const cleanup = () => opts.signal?.removeEventListener('abort', abort);

    xhr.onload = () => {
      cleanup();
      let payload: unknown = null;
      try {
        payload = xhr.responseText ? JSON.parse(xhr.responseText) : null;
      } catch {
        payload = null;
      }
      const body = (payload ?? {}) as {
        message?: Message;
        error?: string;
        message_text?: string;
        detail?: string;
      };

      if (xhr.status >= 200 && xhr.status < 300) {
        // The server answers 200 with both a message and an error when the file
        // was stored but WhatsApp refused it — the bubble exists and can be
        // retried, so that is a failure the caller must see.
        if (body.error && body.message) {
          reject(new ApiError(xhr.status, body.error, body.detail ?? 'Gagal mengirim berkas', body.message));
          return;
        }
        if (body.message) {
          resolve(body.message);
          return;
        }
        reject(new ApiError(xhr.status, 'invalid_response', 'Jawaban server tidak dikenali'));
        return;
      }

      const err = (payload ?? {}) as { error?: string; message?: string };
      reject(
        new ApiError(
          xhr.status,
          err.error ?? 'unknown_error',
          err.message ?? `Permintaan gagal (${xhr.status})`,
        ),
      );
    };

    xhr.onerror = () => {
      cleanup();
      reject(new ApiError(0, 'network_error', 'Koneksi terputus saat mengunggah'));
    };
    xhr.onabort = () => {
      cleanup();
      reject(new ApiError(0, 'aborted', 'Pengiriman dibatalkan'));
    };

    xhr.send(form);
  });
}

/**
 * Mints a signed URL for one attachment. The first call for an incoming file
 * also makes the server fetch it from WhatsApp, so it can take a moment.
 */
export const getAttachmentUrl = (attachmentId: string, download = false) =>
  request<MediaLink>(`/attachments/${attachmentId}/url${download ? '?download=true' : ''}`);

/* --- message actions ------------------------------------------------------ */

/**
 * Changes a message's text. For a photo, video or document this is its caption
 * — WhatsApp treats both as the message's content.
 */
export const editMessage = (messageId: string, text: string) =>
  request<{ message: Message }>(`/messages/${messageId}`, {
    method: 'PATCH',
    body: JSON.stringify({ text }),
  });

/**
 * Deletes a message.
 *
 * `everyone` reaches the other party's phone and leaves a "deleted" placeholder
 * on both sides. `me` removes it from this inbox only.
 */
export const deleteMessage = (messageId: string, scope: 'everyone' | 'me') =>
  request<{ scope: string; message?: Message; deleted?: boolean; pushed_to_phone?: boolean }>(
    `/messages/${messageId}?scope=${scope}`,
    { method: 'DELETE' },
  );

/**
 * Reacts to a message, or takes the reaction back with an empty emoji.
 *
 * One call for both, mirroring how WhatsApp itself expresses the difference.
 */
export const reactToMessage = (messageId: string, emoji: string) =>
  request<{ message: Message }>(`/messages/${messageId}/react`, {
    method: 'POST',
    body: JSON.stringify({ emoji }),
  });

/* --- operational reporting ------------------------------------------------ */

/**
 * The filter row's state, in the shape the API expects.
 *
 * Dates are WIB calendar dates (YYYY-MM-DD); the server resolves them to
 * absolute instants, so nothing here has to think about timezones.
 */
export interface AnalyticsQuery {
  date?: string;
  month?: string;
  from?: string;
  to?: string;
  chat_type?: 'all' | 'personal' | 'group';
  application_id?: string;
  account_id?: string;
  pic_id?: string;
  freelance_id?: string;
  schedule_id?: string;
  /**
   * "true" or "false". Honoured only where a row carries the schedule flag —
   * the activity history and the message drill-down. The summary cards ignore
   * it on purpose: an inbound message has no flag at all, so a "Pesan Masuk"
   * narrowed this way would silently mean something else.
   */
  in_schedule?: string;
}

function analyticsSearch(params: AnalyticsQuery, extra: Record<string, string> = {}) {
  const q = new URLSearchParams();
  for (const [key, value] of Object.entries({ ...params, ...extra })) {
    if (value) q.set(key, value);
  }
  const suffix = q.toString();
  return suffix ? `?${suffix}` : '';
}

/** Builds the dashboard path; also used directly as an SWR cache key. */
export function dashboardPath(params: AnalyticsQuery = {}) {
  return `/dashboard${analyticsSearch(params)}`;
}

export function performancePath(params: AnalyticsQuery = {}, opts: { compare?: boolean } = {}) {
  const search = analyticsSearch(params);
  // Opt-in because it repeats the whole aggregate pass on the server. Only
  // the summary cards want it; the per-person table and the drill-downs do
  // not, and should not pay for it.
  if (!opts.compare) return `/performance${search}`;
  return `/performance${search}${search ? '&' : '?'}compare=true`;
}

/** The per-person breakdown behind a period, already narrowed to the caller. */
export interface DisconnectedDevice {
  id: string;
  name: string;
  phone_number: string | null;
  status: string;
  status_detail: string | null;
  application_code: string | null;
  last_connected_at: string | null;
}

/**
 * How things stand right now, carried on the same response as the period
 * summary because one screen reads both at one moment.
 *
 * None of it takes a date range: "berapa nomor tersambung" has no yesterday.
 */
export interface DashboardStats {
  devices_total: number;
  devices_connected: number;
  /** The same column the inbox badges read, so the two cannot disagree. */
  unanswered: number;
  contacts: number;
  groups: number;
  disconnected: DisconnectedDevice[];
}

/** One bucket of the message-volume curve. */
export interface TrafficPoint {
  /** "YYYY-MM-DD" by day, "YYYY-MM-DDTHH:MM" by hour, in Jakarta. */
  bucket: string;
  inbound_personal: number;
  outbound_personal: number;
  group_inbound: number;
  group_outbound: number;
}

export function trafficPath(
  params: AnalyticsQuery = {},
  bucket: 'hour' | 'day' = 'day',
  /** Keep only what happened inside the rota of whoever is being read. */
  workHours = false,
) {
  return `/analytics/traffic${analyticsSearch(params, workHours ? { bucket, work_hours: 'true' } : { bucket })}`;
}

export function teamPerformancePath(params: AnalyticsQuery = {}) {
  return `/performance/team${analyticsSearch(params)}`;
}

/**
 * The same period split one row per application.
 *
 * Its own request rather than a field on the performance report: the server
 * runs one aggregate pass per application to produce it, and the cards should
 * appear without waiting for a breakdown that sits further down the page.
 */
export function performanceByApplicationPath(params: AnalyticsQuery = {}) {
  return `/performance/by-application${analyticsSearch(params)}`;
}

/**
 * One full summary per person, for the tables that read people the way
 * Rincian Per Hari reads days.
 *
 * `role` is not cosmetic: the server pays one aggregate pass per row returned,
 * so asking for only the role the table draws is what keeps Daftar PIC from
 * computing every Freelance nobody is about to look at.
 */
export function memberBreakdownPath(params: AnalyticsQuery = {}, role?: string) {
  return `/performance/members${analyticsSearch(params, role ? { role } : {})}`;
}

export type DrilldownResource =
  | 'messages'
  | 'sla'
  | 'follow-ups'
  | 'group-mentions'
  | 'label-events'
  | 'leads';

/**
 * One page of the merged activity history.
 *
 * Paged with a keyset rather than an offset: the feed is a union of four tables
 * and new rows land at the top constantly, so an offset would show the same
 * activity twice — or skip one — whenever somebody replied mid-read.
 */
export function activityPath(
  params: AnalyticsQuery = {},
  cursor?: { before?: string; before_id?: string },
  limit = 40,
) {
  return `/analytics/activity${analyticsSearch(params, {
    limit: String(limit),
    ...(cursor?.before ? { before: cursor.before } : {}),
    ...(cursor?.before_id ? { before_id: cursor.before_id } : {}),
  })}`;
}

export interface ActivityPage {
  activities: ActivityRow[];
  next_before?: string;
  next_before_id?: string;
}

/**
 * Where a conversation lives, so a deep link can open the right room.
 *
 * Asked of the server rather than assembled in the browser, because the answer
 * is an authorization decision: the caller learns the route only if they are
 * allowed to open it.
 */
export const locateConversation = (conversationId: string) =>
  request<ConversationLocation>(
    `/analytics/conversation-location?conversation_id=${encodeURIComponent(conversationId)}`,
  );

export function analyticsPath(
  resource: DrilldownResource,
  params: AnalyticsQuery = {},
  extra: Record<string, string> = {},
) {
  return `/analytics/${resource}${analyticsSearch(params, extra)}`;
}

/**
 * Filter options, narrowed to the caller and — when a PIC is chosen — to that
 * PIC's applications, accounts and team.
 */
export function analyticsFiltersPathFor(picId?: string) {
  return picId ? `/analytics/filters?pic_id=${encodeURIComponent(picId)}` : '/analytics/filters';
}

export const analyticsFiltersPath = '/analytics/filters';

/* --- organisation --------------------------------------------------------- */

export const orgMembersPath = '/org/members';

/** The org listing, plus whether this caller can create accounts at all. */
export interface OrgMembersResponse {
  members: OrgMember[];
  scope: AnalyticsScope;
  /** True only when the key is configured AND this caller may hire. */
  can_create_accounts: boolean;
  /**
   * Roles this caller may hand out: a Leader gets all three, a PIC gets
   * Freelance, anyone else gets none. Decided server-side so the form never
   * offers a choice the API would refuse.
   */
  creatable_roles: OperationalRole[];
  /** False when SUPABASE_SERVICE_ROLE_KEY is not configured on the backend. */
  admin_available: boolean;
}

export const createMember = (body: {
  email: string;
  full_name: string;
  password: string;
  role: OperationalRole;
}) =>
  request<{ user_id: string; members: OrgMember[] }>('/org/members', {
    method: 'POST',
    body: JSON.stringify(body),
  });

export const setMemberActive = (userId: string, isActive: boolean) =>
  request<{ members: OrgMember[] }>(`/org/members/${userId}/active`, {
    method: 'PATCH',
    body: JSON.stringify({ is_active: isActive }),
  });

/** Resets a member's password. Leader for anybody, PIC for their own Freelance. */
export const setMemberPassword = (userId: string, password: string) =>
  request<{ updated: boolean }>(`/org/members/${userId}/password`, {
    method: 'PATCH',
    body: JSON.stringify({ password }),
  });

export const setOperationalRole = (userId: string, role: OperationalRole) =>
  request<{ members: OrgMember[] }>(`/org/members/${userId}/role`, {
    method: 'PATCH',
    body: JSON.stringify({ role }),
  });

export const setMemberAssignments = (
  userId: string,
  body: { application_ids: string[]; pic_user_id?: string | null },
) =>
  request<{ members: OrgMember[] }>(`/org/members/${userId}/assignments`, {
    method: 'PATCH',
    body: JSON.stringify(body),
  });

/* --- schedules ------------------------------------------------------------ */

export function schedulesPath(params: {
  /** "weekly" for the standing pattern, "dated" for one-off shifts. */
  kind?: 'weekly' | 'dated';
  from?: string;
  to?: string;
  user_id?: string;
  application_id?: string;
  account_id?: string;
  pic_id?: string;
} = {}) {
  const q = new URLSearchParams();
  for (const [key, value] of Object.entries(params)) if (value) q.set(key, value);
  const suffix = q.toString();
  return `/schedules${suffix ? `?${suffix}` : ''}`;
}

/**
 * Writes one shift, or one day of the standing weekly pattern.
 *
 * Exactly one of work_date and weekday says when. A weekly row holds until it
 * is changed and is the Leader's to set; a dated row covers a single day and
 * wins over the pattern for that person on that date.
 */
export const saveSchedule = (body: {
  user_id: string;
  work_date?: string;
  /** 0 (Minggu) to 6 (Sabtu). */
  weekday?: number;
  starts_at: string;
  ends_at: string;
  application_id?: string | null;
  account_id?: string | null;
  pic_user_id?: string | null;
  timezone?: string;
  is_active?: boolean;
  note?: string | null;
}) =>
  request<{ schedule: WorkSchedule }>('/schedules', {
    method: 'POST',
    body: JSON.stringify(body),
  });

export const deleteSchedule = (id: string) =>
  request<{ deleted: boolean }>(`/schedules/${id}`, { method: 'DELETE' });

/* --- campaigns (Story / Broadcast) ---------------------------------------- */

export function campaignsPath(
  params: {
    type?: string;
    status?: string;
    application_id?: string;
    account_id?: string;
    created_by?: string;
    from?: string;
    to?: string;
    search?: string;
    /** 'true' lists only repeating series, 'false' only one-off sends. */
    recurring?: string;
  } = {},
) {
  const q = new URLSearchParams();
  for (const [key, value] of Object.entries(params)) if (value) q.set(key, value);
  const suffix = q.toString();
  return `/campaigns${suffix ? `?${suffix}` : ''}`;
}

/**
 * The composer's payload, shared by the preview and create calls.
 *
 * `body` is the template, with spintax and variables unresolved — the server
 * spins and fills it once per recipient, so the same campaign genuinely varies
 * across the list rather than being resolved once for everybody.
 */
export interface CampaignDraft {
  campaign_type: CampaignType;
  name: string;
  body: string;
  compose_mode?: ComposeMode;
  application_id?: string | null;
  account_ids: string[];
  /** A link. The file is never uploaded here or stored; the server fetches it
   *  to a temporary path at send time and deletes it. */
  media_url?: string | null;
  /** Set instead of `media_url` for an uploaded document. Comes back from
   *  uploadCampaignDocument; the file lives in the private bucket and is
   *  deleted once the campaign is finished. */
  media_storage_path?: string | null;
  media_file_name?: string | null;
  caption?: string | null;
  delay_profile?: DelayProfile;
  /** Overrides the profile. Send both or neither; the server refuses half. */
  delay_min_seconds?: number | null;
  delay_max_seconds?: number | null;
  /** Keep a disconnected number's remaining share waiting for it to come back. */
  auto_retry_on_disconnect?: boolean;
  /** 'daily' | 'weekly' | 'monthly'; omit for a single run. */
  recurrence?: Recurrence | '';
  recurrence_until?: string | null;
  /** WIB wall clock, "HH:MM". Required whenever `recurrence` is set. */
  recurrence_time?: string;
  /** 0 = Sunday, for weekly. 1–31 for monthly. */
  recurrence_weekday?: number;
  recurrence_day?: number;
  target_source?: TargetSource;
  numbers?: string[];
  group_ids?: string[];
  contact_ids?: string[];
  custom_values?: Record<string, string>;
  label_ids?: string[];
  scheduled_at?: string | null;
  run_now?: boolean;
}

/**
 * The review step: validate, deduplicate, distribute across devices and render
 * a preview. Writes nothing — and it runs the same server code the create call
 * runs, so the numbers approved here are the numbers that get written.
 */
export const previewCampaign = (draft: CampaignDraft) =>
  request<{ plan: TargetPlan; delay_profile: DelayProfile; media_kind: string | null }>(
    '/campaigns/preview',
    { method: 'POST', body: JSON.stringify(draft) },
  );

export const createCampaign = (draft: CampaignDraft) =>
  request<{ campaign: Campaign; plan: TargetPlan }>('/campaigns', {
    method: 'POST',
    body: JSON.stringify(draft),
  });

/** Candidate recipients, scoped to the chosen devices — contacts and groups
 *  belong to a device, not to the workspace. */
export const listAudiences = (accountIds: string[], kind: 'contacts' | 'groups') => {
  const q = new URLSearchParams({ account_ids: accountIds.join(','), kind });
  return request<{ contacts?: AudienceEntry[]; groups?: AudienceEntry[] }>(
    `/campaigns/audiences?${q}`,
  );
};

export const getCampaign = (id: string) =>
  request<{
    campaign: Campaign;
    schedules: CampaignSchedule[];
    activity: CampaignActivity[];
  }>(`/campaigns/${id}`);

export function campaignReportPath(id: string) {
  return `/campaigns/${id}/report`;
}

/**
 * Takes one number's Story off the WhatsApp Status display.
 *
 * Per publication, not per campaign: a Story posted from four numbers is four
 * Stories, and each comes down on its own. The campaign and its report stay —
 * the publication becomes "dihapus" and its viewer count freezes at the moment
 * it came down, which is the true record of a Story that ran for three hours.
 *
 * Returns the whole report again so the page redraws from the server's answer
 * rather than from a guess about what changed.
 */
export const revokeStoryPublication = (campaignId: string, publicationId: string) =>
  request<{ story: StoryReport; views_notice?: string }>(
    `/campaigns/${campaignId}/publications/${publicationId}/revoke`,
    { method: 'POST' },
  );

export function campaignTargetsPath(id: string, status = '', limit = 100, offset = 0) {
  const q = new URLSearchParams({ limit: String(limit), offset: String(offset) });
  if (status) q.set('status', status);
  return `/campaigns/${id}/targets?${q}`;
}

export const scheduleCampaign = (id: string, scheduledAt: string) =>
  request<{ campaign: Campaign }>(`/campaigns/${id}/schedule`, {
    method: 'POST',
    body: JSON.stringify({ scheduled_at: scheduledAt }),
  });

/** Starts a draft now. The queue does the sending; this only makes it due. */
export const runCampaign = (id: string) =>
  request<{ campaign: Campaign }>(`/campaigns/${id}/run`, { method: 'POST' });

/**
 * Cancels a campaign. Cooperative: recipients already sent stay sent, because
 * there is no unsending a WhatsApp message.
 */
export const cancelCampaign = (id: string) =>
  request<{ campaign: Campaign }>(`/campaigns/${id}/cancel`, { method: 'POST' });

/** Requeues only the recipients that failed. Never the whole list. */
export const retryCampaign = (id: string) =>
  request<{ requeued: number; campaign?: Campaign; message?: string }>(
    `/campaigns/${id}/retry`,
    { method: 'POST' },
  );

export const setCampaignLabels = (id: string, labelIds: string[]) =>
  request<{ labels: CampaignLabel[] }>(`/campaigns/${id}/labels`, {
    method: 'PUT',
    body: JSON.stringify({ label_ids: labelIds }),
  });

export const deleteCampaign = (id: string) =>
  request<{ deleted: boolean }>(`/campaigns/${id}`, { method: 'DELETE' });

/** GPT writes a draft and nothing else — it is returned to the composer for a
 *  person to read and approve. Nothing here sends. */
export const generateDraft = (body: {
  brief: string;
  tone?: string;
  language?: string;
  variables?: string[];
  with_spintax?: boolean;
  /** 'spintax' | 'variable' | 'both'. Decides whether the model may invent
   *  placeholders of its own, and whether it spins the wording. */
  mode?: DraftMode;
  /** Where a newly invented variable is filed. Null files it workspace-wide. */
  application_id?: string | null;
}) =>
  request<{
    draft: { text: string; model: string; prompt_tokens: number; completion_tokens: number };
    /** Variables the model invented that did not exist yet, now registered. */
    saved_variables: CustomVariable[];
    notice: string;
  }>('/campaigns/draft', { method: 'POST', body: JSON.stringify(body) });

export type DraftMode = 'spintax' | 'variable' | 'both';

/** What an uploaded broadcast document is, once it is in the private bucket. */
export interface CampaignDocument {
  storage_path: string;
  file_name: string;
  mime: string;
  size: number;
}

export const MAX_DOCUMENT_BYTES = 16 * 1024 * 1024;

/**
 * Uploads one document for a broadcast to send.
 *
 * Images and videos travel as a link; a document is uploaded, because its
 * filename is what the recipient sees on their phone. The server decides the
 * file's type from its own leading bytes rather than from what the browser
 * claimed, refuses executables however they are named, and sanitises the name.
 */
export async function uploadCampaignDocument(file: File): Promise<CampaignDocument> {
  const token = await accessToken();
  const headers = new Headers();
  if (token) headers.set('Authorization', `Bearer ${token}`);

  const body = new FormData();
  body.append('file', file);

  const res = await fetch(`${API_URL}/api/v1/campaigns/documents`, {
    method: 'POST',
    headers,
    body,
  });
  if (!res.ok) {
    const detail = await res.json().catch(() => null);
    throw new ApiError(
      res.status,
      detail?.code ?? 'upload_failed',
      detail?.message ?? 'Dokumen gagal diunggah.',
    );
  }
  return (await res.json()) as CampaignDocument;
}

/* --- campaign labels & variables ------------------------------------------ */

/** One application chip above the broadcast table. */
export interface CampaignFacet {
  id: string | null;
  code: string;
  name: string;
  color: string | null;
  count: number;
}

/** Chip counts: applications under the chosen status, statuses under the chosen application. */
export function campaignFacetsPath(
  q: {
    type?: string;
    status?: string;
    search?: string;
    recurring?: string;
    application_id?: string;
  } = {},
) {
  return `/campaigns/facets${toSearch(q)}`;
}

/**
 * Queues every recipient again, including the ones who already received it.
 *
 * Not the same as retryCampaign, which only picks up what failed. The interface
 * asks before calling this, because a second copy of a message cannot be
 * unsent.
 */
export const resendCampaign = (id: string) =>
  request<{ campaign: Campaign; queued: number }>(`/campaigns/${id}/resend`, { method: 'POST' });

/**
 * Puts a campaign away, or brings it back.
 *
 * The action for anything that is not a draft: only drafts can be deleted, and
 * a campaign that reached two hundred people is a record of something that
 * happened. Archiving hides it from the list and stops the scheduler from ever
 * picking it up, without losing the report.
 */
export const archiveCampaign = (id: string, archived = true) =>
  request<{ campaign: Campaign }>(`/campaigns/${id}/archive`, {
    method: 'POST',
    body: JSON.stringify({ archived }),
  });

/**
 * Reads a campaign back into the shape the composer takes.
 *
 * What both "Duplikat" and "Edit" load. Neither writes anything: a row is only
 * created, or changed, when the composer is saved. Duplicating used to make the
 * copy on the server the moment the button was pressed, which left a campaign
 * behind every time somebody opened one, looked, and changed their mind.
 */
export const campaignSource = (id: string) =>
  request<{ source: CampaignSource }>(`/campaigns/${id}/source`);

/**
 * Rewrites a campaign that has not gone out yet, in place.
 *
 * The same row: editing a broadcast before it leaves is not making a second
 * broadcast. Refused with 409 once it has started, because what it said is what
 * people received.
 */
export const updateCampaign = (id: string, draft: CampaignDraft) =>
  request<{ campaign: Campaign }>(`/campaigns/${id}`, {
    method: 'PUT',
    body: JSON.stringify(draft),
  });

/** Downloads the whole delivery log, not the page currently on screen. */
export async function exportCampaignTargets(
  id: string,
  format: 'csv' | 'xlsx' = 'csv',
): Promise<void> {
  const token = await accessToken();
  const headers = new Headers();
  if (token) headers.set('Authorization', `Bearer ${token}`);

  const res = await fetch(`${API_URL}/api/v1/campaigns/${id}/targets/export?format=${format}`, {
    headers,
  });
  if (!res.ok) throw new ApiError(res.status, 'export_failed', 'Ekspor gagal');

  const blob = await res.blob();
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = filenameFrom(res.headers.get('Content-Disposition')) ?? `log-pengiriman.${format}`;
  a.click();
  URL.revokeObjectURL(url);
}

export const campaignLabelsPath = '/campaign-labels';

export const createCampaignLabel = (body: { name: string; color?: string }) =>
  request<{ label: CampaignLabel }>('/campaign-labels', {
    method: 'POST',
    body: JSON.stringify(body),
  });

export const updateCampaignLabel = (
  id: string,
  body: { name?: string; color?: string; archived?: boolean },
) =>
  request<{ label: CampaignLabel }>(`/campaign-labels/${id}`, {
    method: 'PATCH',
    body: JSON.stringify(body),
  });

export const customVariablesPath = '/custom-variables';

export const upsertCustomVariable = (body: {
  key: string;
  application_id?: string | null;
  label: string;
  default_value?: string | null;
  description?: string | null;
  is_active?: boolean;
}) =>
  request<{ variable: CustomVariable }>('/custom-variables', {
    method: 'POST',
    body: JSON.stringify(body),
  });

export const deleteCustomVariable = (id: string) =>
  request<{ deleted: boolean }>(`/custom-variables/${id}`, { method: 'DELETE' });

/* --- quick replies -------------------------------------------------------- */

export const quickRepliesPath = '/quick-replies';

/** The chat composer only ever wants the enabled ones. */
export const activeQuickRepliesPath = '/quick-replies?active=true';

export const upsertQuickReply = (body: {
  /**
   * Present means edit that row — which is what lets the shortcut or the
   * application be changed. Absent means create, or replace one that already
   * has this shortcut in this application.
   */
  id?: string;
  shortcut: string;
  application_id?: string | null;
  title: string;
  body: string;
  category?: string | null;
  /**
   * An image address. The server fetches it once to check it really is an
   * image before saving — so a dead or mistyped link is refused here, at the
   * settings screen, rather than in front of a customer.
   */
  media_url?: string | null;
  is_active?: boolean;
}) =>
  request<{ quick_reply: QuickReply }>('/quick-replies', {
    method: 'POST',
    body: JSON.stringify(body),
  });

export const deleteQuickReply = (id: string) =>
  request<void>(`/quick-replies/${id}`, { method: 'DELETE' });

/**
 * Clears quick replies in bulk, and says how many went.
 *
 * Bounded by the caller's own scope on the server; `applicationId` narrows it
 * further to the one brand on screen. Without it, everything the caller may
 * delete goes — which for a PIC still excludes the workspace-wide replies.
 */
export const deleteAllQuickReplies = (applicationId?: string | null) =>
  request<{ deleted: number }>(
    `/quick-replies${applicationId ? `?application_id=${encodeURIComponent(applicationId)}` : ''}`,
    { method: 'DELETE' },
  );

/**
 * Records that a text quick reply was actually sent.
 *
 * Called after the message has gone, not when the reply was picked: picking
 * one and then clearing the box is not use. Fire and forget — the message is
 * already delivered, and a failed tally must never surface as a failed send.
 */
export const markQuickReplyUsed = (id: string) =>
  request<void>(`/quick-replies/${id}/used`, { method: 'POST' }).catch(() => undefined);

/** What one line of an imported file did. */
export interface QuickReplyImportRow {
  line: number;
  shortcut: string;
  /** "disimpan" or "dilewati". */
  status: string;
  reason?: string;
}

export interface QuickReplyImportResult {
  saved: number;
  skipped: number;
  rows: QuickReplyImportRow[];
}

/**
 * Downloads every quick reply the caller can see, as a CSV.
 *
 * Fetched rather than linked: the endpoint needs the Authorization header, and
 * an anchor cannot send one.
 */
export async function exportQuickReplies(): Promise<void> {
  const token = await accessToken();
  const headers = new Headers();
  if (token) headers.set('Authorization', `Bearer ${token}`);

  const res = await fetch(`${API_URL}/api/v1/quick-replies/export`, { headers });
  if (!res.ok) throw new ApiError(res.status, 'export_failed', 'Gagal mengunduh balas cepat.');

  const blob = await res.blob();
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = 'balas-cepat.csv';
  a.click();
  URL.revokeObjectURL(url);
}

/**
 * Reads a CSV back in.
 *
 * Row by row: a file is never rejected whole for one bad line, and every
 * refused line comes back saying why.
 */
export async function importQuickReplies(file: File): Promise<QuickReplyImportResult> {
  const form = new FormData();
  form.append('file', file);
  const token = await accessToken();
  const headers = new Headers();
  if (token) headers.set('Authorization', `Bearer ${token}`);

  const res = await fetch(`${API_URL}/api/v1/quick-replies/import`, {
    method: 'POST',
    headers,
    body: form,
  });
  const payload = await res.json().catch(() => null);
  if (!res.ok) {
    throw new ApiError(
      res.status,
      payload?.code ?? 'import_failed',
      payload?.message ?? 'Gagal membaca berkas.',
    );
  }
  return payload as QuickReplyImportResult;
}

/**
 * Sends a picture quick reply into a chat, whole.
 *
 * Only for replies that carry an image: a text reply is pasted into the
 * composer and edited before it goes, which is the point of a canned answer.
 * The image is fetched server-side from the stored address — the browser never
 * touches the URL or the bytes.
 */
export const sendQuickReply = (
  conversationId: string,
  quickReplyId: string,
  clientToken: string,
  /**
   * What the operator actually typed, which may differ from the stored text —
   * the point of loading a canned reply is being able to adjust it. An empty
   * string is a real choice: a picture with no words.
   */
  caption: string,
  /**
   * The message this canned answer replies to, when the operator picked one
   * before reaching for the reply. Without it the picture went out as a fresh
   * message and the customer could not tell what it answered.
   */
  replyTo?: string | null,
) =>
  request<{ message: Message; error?: string; detail?: string }>(
    `/conversations/${conversationId}/quick-replies/${quickReplyId}`,
    {
      method: 'POST',
      body: JSON.stringify({
        client_token: clientToken,
        caption,
        reply_to: replyTo ?? null,
      }),
    },
  );

/* --- SLA targets ---------------------------------------------------------- */

export const slaTargetsPath = '/sla-targets';

export interface SLATargetsResponse {
  targets: SLATarget[];
  /** What applies when the workspace has configured nothing at all. */
  fallback_seconds: number;
  fallback_business_hours: boolean;
  can_edit_default: boolean;
}

export const upsertSLATarget = (body: {
  application_id?: string | null;
  target_seconds: number;
  business_hours?: boolean;
}) =>
  request<{ target: SLATarget }>('/sla-targets', {
    method: 'POST',
    body: JSON.stringify(body),
  });

export const deleteSLATarget = (id: string) =>
  request<void>(`/sla-targets/${id}`, { method: 'DELETE' });

/* --- polls ---------------------------------------------------------------- */

export const createPoll = (
  conversationId: string,
  body: { name: string; options: string[]; allow_multiple: boolean },
) =>
  request<{ message: Message; error?: string; detail?: string }>(
    `/conversations/${conversationId}/polls`,
    { method: 'POST', body: JSON.stringify(body) },
  );

/**
 * Casts a vote. `options` is the complete selection, not a change to it — an
 * empty array clears the vote, which is what tapping your own answer off does
 * on the phone.
 */
export const votePoll = (messageId: string, options: number[]) =>
  request<{ message: Message }>(`/messages/${messageId}/vote`, {
    method: 'POST',
    body: JSON.stringify({ options }),
  });

/**
 * Result of a label toggle. `pushed_to_phone` is false when the change is only
 * stored locally — the account was offline, or WhatsApp rejected the patch.
 */
export interface LabelToggleResult {
  conversation: Conversation;
  pushed_to_phone: boolean;
  reason?: string;
}

export const assignLabel = (conversationId: string, labelId: string) =>
  request<LabelToggleResult>(`/conversations/${conversationId}/labels`, {
    method: 'POST',
    body: JSON.stringify({ label_id: labelId }),
  });

export const unassignLabel = (conversationId: string, labelId: string) =>
  request<LabelToggleResult>(`/conversations/${conversationId}/labels/${labelId}`, {
    method: 'DELETE',
  });

/* --- group directory -------------------------------------------------------- */

export interface GroupQuery {
  application_id?: string;
  account_id?: string;
  search?: string;
  limit?: number;
  offset?: number;
}

function toSearch(q: object): string {
  const params = new URLSearchParams();
  for (const [key, value] of Object.entries(q)) {
    if (value !== undefined && value !== '' && value !== null) params.set(key, String(value));
  }
  const out = params.toString();
  return out ? `?${out}` : '';
}

/** One page of the directory, plus the totals the header states. */
export function groupsPath(q: GroupQuery = {}) {
  return `/groups${toSearch(q)}`;
}

export function groupFacetsPath(applicationId?: string | null) {
  return `/groups/facets${applicationId ? `?application_id=${applicationId}` : ''}`;
}

/**
 * A group and its participants, merged across every number of ours inside it.
 *
 * The group comes back with the members because the detail page states both in
 * one line ("994 anggota · Bimbel BUMN"); two requests could disagree about
 * which group is on screen while one was still in flight.
 */
export function groupMembersPath(chatJid: string) {
  return `/groups/members?chat_jid=${encodeURIComponent(chatJid)}`;
}

export interface GroupDetail {
  group: GroupRow;
  members: GroupDirectoryMember[];
}

/** The detail page's own route, for linking a directory row to it. */
export function groupDetailHref(chatJid: string) {
  return `/groups/${encodeURIComponent(chatJid)}`;
}

/**
 * Re-reads one group from WhatsApp.
 *
 * Distinct from `refreshGroup`, which works from a conversation inside the chat
 * panel. This one is addressed by chat JID and tries every number of ours in
 * the group, so a group reached four ways only needs one phone online.
 */
export const refreshDirectoryGroup = (chatJid: string) =>
  request<GroupDetail>(`/groups/refresh?chat_jid=${encodeURIComponent(chatJid)}`, {
    method: 'POST',
  });

/** Downloads one group's participants as a sheet. */
export async function exportGroupMembers(
  chatJid: string,
  format: 'csv' | 'xlsx' = 'csv',
): Promise<void> {
  const token = await accessToken();
  const headers = new Headers();
  if (token) headers.set('Authorization', `Bearer ${token}`);

  const search = toSearch({ chat_jid: chatJid, format });
  const res = await fetch(`${API_URL}/api/v1/groups/members/export${search}`, { headers });
  if (!res.ok) throw new ApiError(res.status, 'export_failed', 'Ekspor gagal');

  const blob = await res.blob();
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = filenameFrom(res.headers.get('Content-Disposition')) ?? `anggota.${format}`;
  a.click();
  URL.revokeObjectURL(url);
}

/** What one batch of fetching achieved, and how much is left. */
export interface FetchGroupsResult {
  done: number;
  failed: number;
  reason: string;
  total: number;
  fetched: number;
  remaining: number;
  /**
   * Group rows whose membership was corrected by this call: almost always
   * threads that history sync imported for groups we had already left.
   */
  corrected: number;
}

/**
 * Pulls member lists for groups that have none yet.
 *
 * One batch per call by design: each group is a round trip to WhatsApp, and
 * nine hundred in one request would time out with nothing to show. The caller
 * loops on `remaining` so progress is visible while it runs.
 */
export const fetchGroups = (q: GroupQuery = {}) =>
  request<FetchGroupsResult>(`/groups/fetch${toSearch(q)}`, { method: 'POST' });

/**
 * Downloads the directory.
 *
 * `chatJids` narrows it to what was ticked; empty means the whole filter. The
 * request carries a bearer token, so it is fetched and handed to the browser as
 * a blob rather than linked.
 */
export async function exportGroups(
  q: GroupQuery = {},
  format: 'csv' | 'xlsx' = 'csv',
  chatJids: string[] = [],
): Promise<void> {
  const token = await accessToken();
  const headers = new Headers();
  if (token) headers.set('Authorization', `Bearer ${token}`);

  const search = toSearch({
    ...q,
    limit: undefined,
    offset: undefined,
    format,
    chat_jids: chatJids.length > 0 ? chatJids.join(',') : undefined,
  });

  const res = await fetch(`${API_URL}/api/v1/groups/export${search}`, { headers });
  if (!res.ok) throw new ApiError(res.status, 'export_failed', 'Ekspor gagal');

  const blob = await res.blob();
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = filenameFrom(res.headers.get('Content-Disposition')) ?? `grup.${format}`;
  a.click();
  URL.revokeObjectURL(url);
}

/* --- address book ---------------------------------------------------------- */

/** Which slice of the address book a screen is showing. */
export interface ContactQuery {
  application_id?: string;
  account_id?: string;
  search?: string;
  limit?: number;
  offset?: number;
}

function contactSearch(q: ContactQuery): string {
  const params = new URLSearchParams();
  for (const [key, value] of Object.entries(q)) {
    if (value !== undefined && value !== '') params.set(key, String(value));
  }
  const out = params.toString();
  return out ? `?${out}` : '';
}

/** One page, plus how many the filter matches in total. */
export function contactsPath(q: ContactQuery = {}) {
  return `/contacts${contactSearch(q)}`;
}

/**
 * The two chip rows and their counts.
 *
 * `applicationId` narrows the second row only: the numbers offered are that
 * brand's numbers, counted inside it. Passing it also makes the path the SWR
 * cache key, so switching brands refetches instead of showing the last one's
 * numbers.
 */
export function contactFacetsPath(applicationId?: string | null) {
  return `/contacts/facets${applicationId ? `?application_id=${applicationId}` : ''}`;
}

export const deleteContact = (id: string) =>
  request<{ deleted: number }>(`/contacts/${id}`, { method: 'DELETE' });

export const deleteContacts = (ids: string[]) =>
  request<{ deleted: number }>('/contacts/delete', {
    method: 'POST',
    body: JSON.stringify({ ids }),
  });

export const mergeDuplicateContacts = () =>
  request<MergeResult>('/contacts/merge-duplicates', { method: 'POST' });

/**
 * Uploads a CSV into one WhatsApp account's address book.
 *
 * The account is chosen in the dialog rather than read from the file: a contact
 * belongs to the number that will message it, and a spreadsheet has no way of
 * knowing which of ours that is.
 */
export async function importContacts(accountId: string, file: File): Promise<ImportResult> {
  const token = await accessToken();

  const form = new FormData();
  form.append('account_id', accountId);
  form.append('file', file, file.name);

  const headers = new Headers();
  headers.set('Accept', 'application/json');
  if (token) headers.set('Authorization', `Bearer ${token}`);

  const res = await fetch(`${API_URL}/api/v1/contacts/import`, {
    method: 'POST',
    headers,
    body: form,
  });

  const text = await res.text();
  const payload = text ? (JSON.parse(text) as Record<string, unknown>) : {};
  if (!res.ok) {
    throw new ApiError(
      res.status,
      String(payload.error ?? 'import_failed'),
      String(payload.message ?? 'Import gagal'),
    );
  }
  return payload as unknown as ImportResult;
}

/**
 * Downloads the current filter as a CSV.
 *
 * Fetched rather than linked, because the endpoint needs the bearer token and
 * a plain anchor cannot carry one. The blob is handed to the browser under the
 * filename the server chose.
 */
export async function exportContacts(q: ContactQuery = {}): Promise<void> {
  const token = await accessToken();
  const headers = new Headers();
  if (token) headers.set('Authorization', `Bearer ${token}`);

  const res = await fetch(`${API_URL}/api/v1/contacts/export${contactSearch(q)}`, { headers });
  if (!res.ok) {
    throw new ApiError(res.status, 'export_failed', 'Ekspor gagal');
  }

  const blob = await res.blob();
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = filenameFrom(res.headers.get('Content-Disposition')) ?? 'kontak.csv';
  a.click();
  URL.revokeObjectURL(url);
}

/** Reads the filename the server chose, so the download is not called "export". */
function filenameFrom(disposition: string | null): string | null {
  if (!disposition) return null;
  const match = /filename\*?=(?:UTF-8'')?"?([^";]+)"?/i.exec(disposition);
  return match ? decodeURIComponent(match[1]) : null;
}

/* --- labels & contacts ---------------------------------------------------- */

/** Every label mutation reports whether WhatsApp actually received it. */
export interface LabelMutationResult {
  label?: Label;
  deleted?: boolean;
  pushed_to_phone: boolean;
  reason?: string;
}

/** Labels are per WhatsApp account; pass the account to get its own set. */
export const labelsPath = (accountId?: string) =>
  accountId ? `/labels?account_id=${encodeURIComponent(accountId)}` : '/labels';

export const listLabels = (accountId?: string) =>
  request<{ labels: Label[] }>(labelsPath(accountId));

export const createLabel = (body: { name: string; color?: string; account_id?: string }) =>
  request<LabelMutationResult>('/labels', { method: 'POST', body: JSON.stringify(body) });

export const updateLabel = (id: string, body: { name?: string; color?: string }) =>
  request<LabelMutationResult>(`/labels/${id}`, { method: 'PATCH', body: JSON.stringify(body) });

export const deleteLabel = (id: string) =>
  request<LabelMutationResult>(`/labels/${id}`, { method: 'DELETE' });

export const listContacts = (params?: { account_id?: string; search?: string }) => {
  const q = new URLSearchParams();
  if (params?.account_id) q.set('account_id', params.account_id);
  if (params?.search) q.set('search', params.search);
  const suffix = q.toString() ? `?${q}` : '';
  return request<{ contacts: Contact[] }>(`/contacts${suffix}`);
};

/* --- realtime ------------------------------------------------------------- */

/** Builds the authenticated WebSocket URL (the token cannot go in a header). */
export async function realtimeURL(): Promise<string | null> {
  const token = await accessToken();
  if (!token) return null;
  const base = API_URL.replace(/^http/, 'ws');
  return `${base}/api/v1/ws?token=${encodeURIComponent(token)}`;
}
