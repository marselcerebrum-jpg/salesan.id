'use client';

import clsx from 'clsx';
import {
  ArrowLeft,
  CircleDot,
  Eraser,
  Megaphone,
  MessageSquare,
  RefreshCw,
  Trash2,
  type LucideIcon,
} from 'lucide-react';
import Link from 'next/link';
import { useParams, useSearchParams } from 'next/navigation';
import { Suspense, useCallback, useEffect, useMemo, useState } from 'react';
import useSWR from 'swr';

import {
  ConversationList,
  EMPTY_FILTERS,
  type InboxFilters,
} from '@/components/chat/ConversationList';
import type { SendDraft } from '@/components/chat/MediaComposer';
import { ChannelList, ChannelViewer } from '@/components/chat/ChannelPanel';
import type { Newsletter } from '@/lib/api';
import { MessageThread } from '@/components/chat/MessageThread';
import {
  StatusList,
  StatusViewer,
  markGroupSeen,
  orderStatusGroups,
  useStatusGroups,
  type StatusGroup,
} from '@/components/chat/StatusPanel';
import { ConnectionIndicator } from '@/components/layout/AppShell';
import { Button } from '@/components/ui/Button';
import { ConfirmDialog, useConfirm } from '@/components/ui/ConfirmDialog';
import { StatusPill } from '@/components/ui/Primitives';
import {
  assignLabel,
  conversationsPath,
  createPoll,
  deleteConversation,
  deleteMessage,
  editMessage,
  fetcher,
  forwardMessage,
  getConversation,
  getFirstMention,
  labelsPath,
  listMessages,
  markConversationRead,
  markConversationUnread,
  reactToMessage,
  sendMedia,
  sendMessage,
  sendQuickReply,
  syncAccount,
  unassignLabel,
} from '@/lib/api';
import { ACCOUNT_STATUS_LABEL, accountStatusTone, conversationTitle } from '@/lib/format';
import { usePresenceAnnounce, useRealtimeEvent } from '@/lib/realtime';
import type {
  Account,
  Conversation,
  ConversationCounts,
  ConversationDeletedPayload,
  Label,
  LabelsUpdatedPayload,
  LabelSyncState,
  LabelSyncStatePayload,
  Me,
  Message,
  MessageHiddenPayload,
  MessageNewPayload,
  MessageStatusPayload,
  PresenceViewer,
  PresenceViewersPayload,
  PrivateReplyTarget,
  QuickReply,
} from '@/lib/types';

/**
 * Label sync indicator. Stays out of the way while idle: a badge that is always
 * on becomes furniture and stops being read.
 */
function LabelSyncBadge({ state }: { state: LabelSyncState }) {
  if (state === 'idle') return null;

  const tone = {
    syncing: { text: 'Menyinkronkan label…', className: 'bg-warn-soft text-warn' },
    synced: { text: 'Label tersinkron', className: 'bg-brand-600/10 text-brand-700' },
    failed: { text: 'Label gagal sinkron', className: 'bg-danger-soft text-danger' },
  }[state];

  return (
    <span
      className={`inline-flex items-center gap-1.5 rounded-full px-2.5 py-1 text-2xs font-medium ${tone.className}`}
    >
      {state === 'syncing' ? (
        <RefreshCw className="size-3 animate-spin" aria-hidden />
      ) : (
        <span
          className={`size-1.5 rounded-full ${state === 'synced' ? 'bg-online' : 'bg-danger'}`}
          aria-hidden
        />
      )}
      {tone.text}
    </span>
  );
}

/**
 * Finds where the unread run starts, so the thread can open there instead of
 * at the bottom.
 *
 * Two sources, in order of trust: the per-message `read_at` stamp is exact and
 * survives a partial read, while counting back from the newest inbound message
 * is the fallback for threads whose history was backfilled without receipts.
 */
function firstUnreadMessageId(messages: Message[], unreadCount: number): string | null {
  if (unreadCount <= 0 || messages.length === 0) return null;

  const stamped = messages.find((m) => !m.from_me && m.read_at === null);
  if (stamped) return stamped.id;

  let remaining = unreadCount;
  for (let i = messages.length - 1; i >= 0; i -= 1) {
    if (messages[i].from_me) continue;
    remaining -= 1;
    if (remaining === 0) return messages[i].id;
  }
  // Fewer inbound messages loaded than the counter claims: the run starts
  // before this page of history, so anchor at the top of what we have.
  return messages.find((m) => !m.from_me)?.id ?? null;
}

/** Reference screen 6 — the WhatsApp inbox for one account. */
export default function InboxPage() {
  return (
    // useSearchParams needs a boundary: a deep link from Performa carries the
    // conversation to open in the query string.
    <Suspense fallback={null}>
      <Inbox />
    </Suspense>
  );
}

function Inbox() {
  const params = useParams<{ applicationId: string; accountId: string }>();
  const { applicationId, accountId } = params;
  const search = useSearchParams();
  // Where a deep link wants to land: the thread, and the message that started
  // the wait. Both are resolved by the server before this page is reached, so
  // arriving here already means the reader is allowed to open it.
  const deepLinkConversation = search.get('c');
  const deepLinkMessage = search.get('m');
  const [deepLinked, setDeepLinked] = useState<Conversation | null>(null);
  const [deepLinkError, setDeepLinkError] = useState<string | null>(null);

  // Which of the three panels the left column is showing. Not a route: the
  // header above it belongs to all three, and routing would rebuild it on every
  // switch.
  const [view, setView] = useState<'chat' | 'status' | 'saluran'>('chat');
  // Who is being watched, by address rather than by object, so the open Story
  // follows the refreshed data instead of a copy taken at click time — a status
  // its poster deletes mid-reel then disappears here too.
  const [statusJID, setStatusJID] = useState<string | null>(null);
  // The playback order, snapshotted when a Story is opened. Taken once because
  // opening one marks it seen, which moves it from "Terbaru" to "Telah Dilihat"
  // on the next refresh; reading the live order mid-reel would reshuffle the
  // queue underneath the person watching it.
  const [statusQueue, setStatusQueue] = useState<string[]>([]);
  const [channel, setChannel] = useState<Newsletter | null>(null);
  const [deepLinkDone, setDeepLinkDone] = useState(false);

  const [filters, setFilters] = useState<InboxFilters>(EMPTY_FILTERS);
  const [debouncedSearch, setDebouncedSearch] = useState('');
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [messages, setMessages] = useState<Message[]>([]);
  const [messagesLoading, setMessagesLoading] = useState(false);
  const [sending, setSending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [syncing, setSyncing] = useState(false);
  const [syncNote, setSyncNote] = useState<string | null>(null);
  const [labelSync, setLabelSync] = useState<LabelSyncState>('idle');
  const confirm = useConfirm();
  // Where the "N pesan belum dibaca" divider sits, captured when the thread is
  // opened and held until another thread is opened — as WhatsApp does.
  const [unreadMark, setUnreadMark] = useState<{ messageId: string; count: number } | null>(null);
  // The message to scroll to when a chat is opened because of a mention.
  const [mentionAnchor, setMentionAnchor] = useState<string | null>(null);

  // Debounce the search box so typing does not fire a request per keystroke.
  useEffect(() => {
    const timer = setTimeout(() => setDebouncedSearch(filters.search.trim()), 300);
    return () => clearTimeout(timer);
  }, [filters.search]);

  const { data: account, mutate: mutateAccount } = useSWR<Account>(
    `/accounts/${accountId}`,
    fetcher,
  );
  // Labels are per WhatsApp account: WhatsApp identifies them by a per-device
  // numeric id, so another linked number's "Premium" is a different label.
  const { data: labelData, mutate: mutateLabels } = useSWR<{ labels: Label[] }>(
    labelsPath(accountId),
    fetcher,
  );
  const labels = useMemo(() => labelData?.labels ?? [], [labelData]);

  const listKey = conversationsPath(accountId, {
    search: debouncedSearch || undefined,
    type: filters.type || undefined,
    unread: filters.unread || undefined,
    mentions: filters.mentions || undefined,
    label_id: filters.labelId || undefined,
  });

  const {
    data: listData,
    isLoading: listLoading,
    mutate: mutateList,
  } = useSWR<{ conversations: Conversation[] }>(listKey, fetcher, { keepPreviousData: true });

  const { data: counts, mutate: mutateCounts } = useSWR<ConversationCounts>(
    `/accounts/${accountId}/conversations/counts`,
    fetcher,
  );

  const conversations = useMemo(() => listData?.conversations ?? [], [listData]);

  // Both addresses this account answers to, without device suffixes. A mention
  // of us arrives under whichever form the group uses, so matching one alone
  // would miss roughly half of them.
  const ownJids = useMemo(
    () =>
      [account?.jid, account?.lid]
        .filter((jid): jid is string => Boolean(jid))
        .map((jid) => jid.replace(/:\d+(?=@)/, '')),
    [account?.jid, account?.lid],
  );
  const selected = useMemo(
    () =>
      conversations.find((c) => c.id === selectedId) ??
      // A deep-linked thread may sit outside the loaded page of the list — an
      // older conversation still waiting for a reply, for instance. It is
      // fetched on its own so the link lands rather than silently doing nothing.
      (deepLinked && deepLinked.id === selectedId ? deepLinked : null),
    [conversations, selectedId, deepLinked],
  );

  const connected = account?.status === 'connected';

  /* --- presence: who else has a chat open ---------------------------------- */

  // Only while the Chat tab shows a thread. On Status or Saluran the thread is
  // not on screen, so claiming to be "in" it would mislead a colleague.
  usePresenceAnnounce(view === 'chat' ? selectedId : null);

  const { data: me } = useSWR<Me>('/me', fetcher);
  // conversation id -> people with it open, as the server last reported.
  const [viewers, setViewers] = useState<Record<string, PresenceViewer[]>>({});
  useRealtimeEvent<PresenceViewersPayload>('presence.viewers', (payload) => {
    setViewers((prev) => {
      const next = { ...prev };
      if (payload.viewers.length === 0) delete next[payload.conversation_id];
      else next[payload.conversation_id] = payload.viewers;
      return next;
    });
  });
  // Everyone but me: my own name on the thread I am reading says nothing.
  const othersViewing = useMemo(() => {
    const out: Record<string, PresenceViewer[]> = {};
    for (const [id, list] of Object.entries(viewers)) {
      const others = list.filter((v) => v.user_id !== me?.user.id);
      if (others.length > 0) out[id] = others;
    }
    return out;
  }, [viewers, me?.user.id]);

  /* --- status playback ----------------------------------------------------- */

  // Only while the tab is open; the list below shares this same cache entry.
  const { groups: statusGroups } = useStatusGroups(accountId, view === 'status');

  const statusGroup = useMemo(
    () => statusGroups.find((g) => g.senderJID === statusJID) ?? null,
    [statusGroups, statusJID],
  );

  function openStatus(group: StatusGroup) {
    setStatusQueue(orderStatusGroups(statusGroups).map((g) => g.senderJID));
    setStatusJID(group.senderJID);
    // Marked seen locally, and only locally. See markGroupSeen.
    void markGroupSeen(group);
  }

  /**
   * Moves on when one person's updates run out.
   *
   * Walks the snapshotted queue forward, skipping anybody whose status has since
   * been deleted or passed its 24 hours, and closes the viewer once there is
   * nobody left — which puts the screen back exactly as it was before the first
   * click, rather than holding on a frame that has already been watched.
   */
  const advanceStatus = useCallback(() => {
    const from = statusQueue.indexOf(statusJID ?? '');
    for (let i = from + 1; i < statusQueue.length; i += 1) {
      const next = statusGroups.find((g) => g.senderJID === statusQueue[i]);
      if (!next) continue;
      setStatusJID(next.senderJID);
      void markGroupSeen(next);
      return;
    }
    setStatusJID(null);
  }, [statusQueue, statusJID, statusGroups]);

  const loadMessages = useCallback(async (conversationId: string): Promise<Message[]> => {
    setMessagesLoading(true);
    setError(null);
    try {
      const res = await listMessages(conversationId, 80);
      setMessages(res.messages);
      return res.messages;
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Gagal memuat pesan.');
      return [];
    } finally {
      setMessagesLoading(false);
    }
  }, []);

  async function openConversation(conversation: Conversation) {
    setSelectedId(conversation.id);
    setMessages([]);
    setUnreadMark(null);
    setMentionAnchor(null);

    // Both boundaries are captured before anything clears them: opening a
    // thread marks it read and its mentions seen, which erases the very
    // information the divider and the jump target need.
    const unread = conversation.unread_count;
    const hadMention = conversation.mention_count > 0;

    let mentionId: string | null = null;
    if (hadMention) {
      try {
        mentionId = (await getFirstMention(conversation.id)).message_id;
      } catch {
        // Landing at the bottom is a lesser failure than not opening at all.
      }
    }

    const loaded = await loadMessages(conversation.id);
    const anchorId = firstUnreadMessageId(loaded, unread);
    setUnreadMark(anchorId ? { messageId: anchorId, count: unread } : null);
    // A mention outranks the unread divider as a scroll target: it is the
    // reason the operator opened this particular chat.
    setMentionAnchor(mentionId && loaded.some((m) => m.id === mentionId) ? mentionId : null);

    if (unread > 0 || conversation.marked_unread || hadMention) {
      try {
        await markConversationRead(conversation.id);
        void mutateList();
        void mutateCounts();
      } catch {
        // A failed read receipt should not block reading the thread.
      }
    }
  }

  /**
   * Opens the thread a deep link named.
   *
   * Runs once. The list is preferred when it already holds the conversation —
   * that row carries its labels and unread state — and the conversation is
   * fetched directly only when it is not there, which happens whenever somebody
   * follows "Masih Menunggu Balasan" to a chat that has scrolled out of the
   * first page.
   *
   * A thread that no longer exists says so. It never falls back to opening
   * something else: landing in the wrong customer's chat is worse than landing
   * nowhere, because the operator would not notice.
   */
  useEffect(() => {
    if (!deepLinkConversation || deepLinkDone) return;
    if (listLoading) return;

    setDeepLinkDone(true);
    const inList = conversations.find((c) => c.id === deepLinkConversation);
    if (inList) {
      void openConversation(inList);
      if (deepLinkMessage) setMentionAnchor(deepLinkMessage);
      return;
    }

    void (async () => {
      try {
        const conv = await getConversation(deepLinkConversation);
        setDeepLinked(conv);
        await openConversation(conv);
        if (deepLinkMessage) setMentionAnchor(deepLinkMessage);
      } catch {
        setDeepLinkError(
          'Percakapan yang dituju sudah tidak tersedia di inbox ini. Tidak ada chat lain yang dibuka.',
        );
      }
    })();
    // openConversation is stable enough for this one-shot effect; re-running on
    // its identity would re-open the thread on every list refresh.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [deepLinkConversation, deepLinkMessage, deepLinkDone, listLoading, conversations]);

  /* --- realtime ----------------------------------------------------------- */

  useRealtimeEvent<MessageNewPayload>('message.new', (payload) => {
    if (payload.account_id !== accountId) return;

    if (payload.message.conversation_id === selectedId) {
      setMessages((prev) =>
        // The optimistic row and the socket echo share a primary key.
        prev.some((m) => m.id === payload.message.id)
          ? prev.map((m) => (m.id === payload.message.id ? payload.message : m))
          : [...prev, payload.message],
      );
    }
    void mutateList();
    void mutateCounts();
  });

  useRealtimeEvent<MessageStatusPayload>('message.status', (payload) => {
    if (payload.account_id !== accountId) return;

    // The event carries the whole row when more than the status changed —
    // notably when an attachment finished downloading and its bubble can swap
    // the thumbnail for the real file. Prefer it over patching one field.
    const full = payload.message;
    const byId = new Map(payload.changes.map((c) => [c.id, c.status]));

    setMessages((prev) =>
      prev.map((m) => {
        if (full && m.id === full.id) return full;
        return byId.has(m.id) ? { ...m, status: byId.get(m.id)! } : m;
      }),
    );
  });

  useRealtimeEvent('conversation.updated', () => {
    void mutateList();
    void mutateCounts();
  });

  // Another tab hid a message. Drop it here too.
  useRealtimeEvent<MessageHiddenPayload>('message.hidden', (payload) => {
    if (payload.account_id !== accountId) return;
    setMessages((prev) => prev.filter((m) => m.id !== payload.message_id));
  });

  // Another tab deleted a thread. Drop it here too rather than leaving a chat
  // open that no longer exists.
  useRealtimeEvent<ConversationDeletedPayload>('conversation.deleted', (payload) => {
    if (selectedId === payload.conversation_id) {
      if (payload.cleared) setMessages([]);
      else setSelectedId(null);
    }
    void mutateList();
    void mutateCounts();
  });

  useRealtimeEvent('account.status', () => {
    void mutateAccount();
  });

  // A label created, renamed or deleted on the phone arrives as a whole
  // refreshed set, so there is no partial diff for the browser to reconcile.
  useRealtimeEvent<LabelsUpdatedPayload>('labels.updated', (payload) => {
    if (payload.account_id && payload.account_id !== accountId) return;
    void mutateLabels({ labels: payload.labels }, { revalidate: false });
    void mutateList();
  });

  useRealtimeEvent<LabelSyncStatePayload>('labels.sync_state', (payload) => {
    if (payload.account_id !== accountId) return;
    setLabelSync(payload.state);
    if (payload.state === 'failed' && payload.detail) setError(payload.detail);
  });

  /* --- actions ------------------------------------------------------------ */

  async function handleSend(body: string, replyTo: string | null) {
    if (!selected) return;
    setSending(true);
    setError(null);
    try {
      const message = await sendMessage(selected.id, body, replyTo);
      setMessages((prev) =>
        prev.some((m) => m.id === message.id)
          ? prev.map((m) => (m.id === message.id ? message : m))
          : [...prev, message],
      );
      void mutateList();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Pesan gagal dikirim.');
      if (selected) void loadMessages(selected.id);
    } finally {
      setSending(false);
    }
  }

  /**
   * Flags a thread unread on both sides.
   *
   * If it happens to be the open one, it is closed too: an open thread gets
   * marked read on view, so leaving it selected would undo the flag on the
   * spot.
   */
  /**
   * Sends one attached file.
   *
   * The composer owns progress, ordering and retry; this only performs the
   * request and folds the resulting message into the open thread. A failure is
   * rethrown so the composer can mark that one file and offer another go —
   * the same client token then reuses the row instead of duplicating it.
   */
  async function handleSendFile(
    file: SendDraft,
    onProgress: (fraction: number) => void,
    signal: AbortSignal,
  ) {
    if (!selected) return;
    setError(null);
    try {
      const message = await sendMedia(selected.id, {
        file: file.file,
        fileName: file.fileName,
        caption: file.caption,
        asDocument: file.asDocument,
        clientToken: file.token,
        replyTo: file.replyTo ?? null,
        onProgress,
        signal,
      });
      setMessages((prev) =>
        prev.some((m) => m.id === message.id)
          ? prev.map((m) => (m.id === message.id ? message : m))
          : [...prev, message],
      );
      void mutateList();
    } catch (err) {
      // The bubble may already exist in a failed state, so refresh the thread
      // before handing the failure back to the composer.
      void loadMessages(selected.id);
      throw err instanceof Error ? err : new Error('Berkas gagal dikirim.');
    }
  }

  /**
   * After a private reply to a group member has gone out.
   *
   * The message is already delivered, so this only decides where to stand
   * afterwards: open the one-to-one thread it landed in. An operator who has
   * just started a private conversation is going to want to see the answer,
   * and leaving them in the group means finding that thread by hand later.
   *
   * The list is refreshed first because the thread may not have existed a
   * second ago; this is the ordinary case for this feature.
   */
  function handlePrivateReplySent(target: PrivateReplyTarget) {
    void mutateList();
    void mutateCounts();
    if (target.account_id === accountId) {
      setSelectedId(target.conversation_id);
      return;
    }
    // Defensive: the private thread is opened on the same account the group
    // sits on, so this should not happen. Saying where it went beats silence.
    setSyncNote(`Balasan pribadi terkirim ke ${target.name}.`);
  }

  /**
   * Re-sends a message into other conversations.
   *
   * A partial result is reported rather than swallowed: forwarding to six
   * chats where one is a group we were removed from should say so, not claim
   * success for all six.
   */
  async function handleForward(message: Message, conversationIds: string[]) {
    setError(null);
    const result = await forwardMessage(message.id, conversationIds);
    void mutateList();
    void mutateCounts();

    if (result.failed.length > 0) {
      setError(
        `Diteruskan ke ${result.sent} percakapan. ${result.failed.length} gagal: ${result.failed[0].reason}`,
      );
    }
    // A forward into the open thread lands through the realtime feed, so
    // nothing is spliced into local state here.
  }

  /**
   * Folds a changed conversation back into the loaded list.
   *
   * Written into the cache directly rather than refetched: a group rename or a
   * description edit already returns the new row, and a round trip would only
   * make the panel flicker.
   */
  function handleConversationChange(updated: Conversation) {
    void mutateList(
      (current) =>
        current && {
          conversations: current.conversations.map((c) => (c.id === updated.id ? updated : c)),
        },
      { revalidate: false },
    );
  }

  /** Changes a message's text, or a media message's caption. */
  async function handleEditMessage(message: Message, text: string) {
    setError(null);
    try {
      const { message: updated } = await editMessage(message.id, text);
      setMessages((prev) => prev.map((m) => (m.id === updated.id ? updated : m)));
      void mutateList();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Pesan gagal diedit.');
      throw err; // keeps the edit box open so the text is not lost
    }
  }

  /**
   * Deletes a message. "everyone" reaches the other party's phone and leaves a
   * placeholder on both sides; "me" removes it from this inbox only.
   */
  async function handleDeleteMessage(message: Message, scope: 'everyone' | 'me') {
    setError(null);
    try {
      const result = await deleteMessage(message.id, scope);
      if (scope === 'me') {
        setMessages((prev) => prev.filter((m) => m.id !== message.id));
        if (result.pushed_to_phone === false) {
          setError('Pesan dihapus dari web. HP tidak bisa dihubungi, jadi di HP masih ada.');
        }
      } else if (result.message) {
        setMessages((prev) => prev.map((m) => (m.id === message.id ? result.message! : m)));
      }
      void mutateList();
      void mutateCounts();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Pesan gagal dihapus.');
    }
  }

  /**
   * Adds a reaction, or takes this account's back when the emoji is empty.
   *
   * The server returns the message with its whole reaction set recomputed, so
   * the bubble is replaced outright rather than patched — a second operator
   * reacting at the same moment then shows up too.
   */
  async function handleReactMessage(message: Message, emoji: string) {
    setError(null);
    try {
      const { message: updated } = await reactToMessage(message.id, emoji);
      setMessages((prev) => prev.map((m) => (m.id === updated.id ? updated : m)));
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Reaksi gagal dikirim.');
    }
  }

  async function handleSendPoll(poll: {
    name: string;
    options: string[];
    allowMultiple: boolean;
  }) {
    if (!selected) return;
    setError(null);

    const result = await createPoll(selected.id, {
      name: poll.name,
      options: poll.options,
      allow_multiple: poll.allowMultiple,
    });

    // The poll is stored even when WhatsApp refuses it, so the bubble goes into
    // the thread either way and the failure shows on it.
    setMessages((prev) =>
      prev.some((m) => m.id === result.message.id)
        ? prev.map((m) => (m.id === result.message.id ? result.message : m))
        : [...prev, result.message],
    );
    void mutateList();

    if (result.error) {
      throw new Error(result.detail ?? 'Polling gagal dikirim ke WhatsApp.');
    }
  }

  /**
   * Sends a picture quick reply.
   *
   * The whole thing happens on the server: it reads the reply, fetches the
   * image from the address stored on it, and sends the picture with the
   * reply's text as the caption. The browser sends two ids and nothing else —
   * it never sees the URL and never handles the bytes.
   *
   * The client token is what stops a double press from sending twice.
   */
  async function handleSendQuickReply(
    reply: QuickReply,
    caption: string,
    replyTo: string | null,
  ) {
    if (!selected) return;
    setError(null);
    try {
      const result = await sendQuickReply(
        selected.id,
        reply.id,
        crypto.randomUUID(),
        caption,
        replyTo,
      );
      setMessages((prev) =>
        prev.some((m) => m.id === result.message.id)
          ? prev.map((m) => (m.id === result.message.id ? result.message : m))
          : [...prev, result.message],
      );
      void mutateList();
      if (result.error) {
        setError(result.detail ?? 'Gambar balas cepat gagal dikirim ke WhatsApp.');
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Balas cepat gagal dikirim.');
    }
  }

  /**
   * Removes a thread, or empties it when only the history should go.
   *
   * Confirmed first, and the wording says plainly that this touches the web
   * inbox only — an operator who thinks they are deleting the customer's copy
   * would be badly surprised.
   */
  function handleDelete(conversation: Conversation, clearOnly: boolean) {
    const title = conversationTitle(conversation);

    confirm.ask({
      title: clearOnly ? 'Bersihkan isi chat?' : 'Hapus obrolan?',
      description: (
        <>
          <span className="font-medium text-ink">{title}</span>
          {clearOnly
            ? ': semua pesan hilang dari inbox ini, tetapi percakapannya tetap ada di daftar.'
            : ': percakapan hilang dari inbox ini beserta isinya.'}{' '}
          Chat tetap utuh di HP, dan pesan baru akan memunculkannya lagi.
        </>
      ),
      confirmLabel: clearOnly ? 'Bersihkan' : 'Hapus obrolan',
      tone: 'danger',
      icon: clearOnly ? Eraser : Trash2,
      onConfirm: async () => {
        setError(null);
        try {
          await deleteConversation(conversation.id, clearOnly);
          if (selectedId === conversation.id) {
            if (clearOnly) setMessages([]);
            else setSelectedId(null);
          }
          void mutateList();
          void mutateCounts();
        } catch (err) {
          setError(err instanceof Error ? err.message : 'Obrolan gagal dihapus.');
        }
      },
    });
  }

  async function handleMarkUnread(conversation: Conversation) {
    setError(null);
    try {
      await markConversationUnread(conversation.id);
      if (selectedId === conversation.id) {
        setSelectedId(null);
        setMessages([]);
      }
      void mutateList();
      void mutateCounts();
    } catch (err) {
      setError(
        err instanceof Error
          ? `Gagal menandai belum dibaca: ${err.message}`
          : 'Gagal menandai belum dibaca.',
      );
    }
  }

  /**
   * Toggles a label optimistically, then confirms against WhatsApp.
   *
   * The backend writes to WhatsApp before its own database, so a rejected
   * change leaves nothing stored anywhere — which means rolling the UI back to
   * the pre-click snapshot restores the true state exactly.
   */
  async function handleToggleLabel(
    conversation: Conversation,
    labelId: string,
    attached: boolean,
  ) {
    const label = labels.find((l) => l.id === labelId);
    if (!label) return;

    const conversationId = conversation.id;
    const snapshot = listData;

    setError(null);
    setLabelSync('syncing');

    await mutateList(
      (current) =>
        current && {
          conversations: current.conversations.map((c) =>
            c.id !== conversationId
              ? c
              : {
                  ...c,
                  labels: attached
                    ? c.labels.filter((l) => l.id !== labelId)
                    : [...c.labels, label],
                },
          ),
        },
      { revalidate: false },
    );

    try {
      if (attached) await unassignLabel(conversationId, labelId);
      else await assignLabel(conversationId, labelId);

      setLabelSync('synced');
      void mutateList();
    } catch (err) {
      await mutateList(snapshot, { revalidate: false });
      setLabelSync('failed');
      setError(
        err instanceof Error
          ? `Label gagal diubah: ${err.message}`
          : 'Label gagal diubah di WhatsApp.',
      );
    }
  }

  async function handleSync() {
    setSyncing(true);
    setSyncNote(null);
    setError(null);
    try {
      const result = await syncAccount(accountId);
      const parts = [
        `${result.contacts} kontak`,
        `${result.groups} grup`,
        `${result.labels} label`,
      ];
      if (result.history_pending) parts.push('riwayat menyusul');
      setSyncNote(`${result.window_days} hari terakhir · ${parts.join(' · ')}`);
      void mutateList();
      void mutateCounts();
      if (selectedId) void loadMessages(selectedId);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Sinkronisasi gagal.');
    } finally {
      setSyncing(false);
    }
  }

  const tone = accountStatusTone(account?.status ?? 'disconnected');

  return (
    <div className="flex h-dvh min-h-0 lg:h-dvh">
      <section className="flex w-full min-w-0 shrink-0 flex-col border-r border-wa-border bg-wa-panel lg:w-[420px] xl:w-[440px]">
        <div className="px-3 pt-4 pb-2">
          <Link
            href={`/chat/${applicationId}`}
            className="inline-flex items-center gap-1.5 text-xs text-ink-muted transition-colors hover:text-ink"
          >
            <ArrowLeft className="size-4" />
            {account?.application_name ?? 'Semua nomor'}
          </Link>

          <div className="mt-2 flex items-start justify-between gap-2">
            <div className="min-w-0">
              <h1 className="truncate text-2xl font-semibold tracking-[-0.02em] text-ink">
                {account?.label ?? account?.name ?? 'Inbox'}
              </h1>
              <div className="mt-1.5">
                <StatusPill
                  label={ACCOUNT_STATUS_LABEL[account?.status ?? 'disconnected']}
                  dotClass={tone.dot}
                  textClass={tone.text}
                  bgClass={tone.bg}
                />
              </div>
            </div>

            <div className="flex shrink-0 items-center gap-2">
              <Button
                size="sm"
                icon={<RefreshCw className={`size-4 ${syncing ? 'animate-spin' : ''}`} />}
                loading={false}
                disabled={syncing || !connected}
                onClick={handleSync}
                title={connected ? 'Tarik kontak, grup, dan riwayat chat' : 'Akun tidak terhubung'}
              >
                Sinkron
              </Button>
              <ConnectionIndicator />
            </div>
          </div>

          <div className="mt-2 flex flex-wrap items-center gap-2">
            <LabelSyncBadge state={labelSync} />
            {syncNote ? <p className="text-2xs text-brand-700">{syncNote}</p> : null}
          </div>

          {/*
           * Chat, Status and Saluran are three different things that happen to
           * arrive on one number, and the panel below shows one of them at a
           * time. A bar rather than three routes: the header above it — number,
           * connection, sync — belongs to all three, and routing would rebuild
           * it on every switch.
           */}
          <div className="mt-3 grid grid-cols-3 gap-1.5">
            {(
              [
                { id: 'chat', label: 'Chat', icon: MessageSquare },
                { id: 'status', label: 'Status', icon: CircleDot },
                { id: 'saluran', label: 'Saluran', icon: Megaphone },
              ] as const
            ).map((t) => {
              const on = view === t.id;
              const Icon = t.icon;
              return (
                <button
                  key={t.id}
                  type="button"
                  onClick={() => setView(t.id)}
                  aria-current={on ? 'page' : undefined}
                  className={clsx(
                    'inline-flex h-9 items-center justify-center gap-1.5 rounded-control border text-sm font-medium transition-colors',
                    on
                      ? 'border-brand-800 bg-brand-800 text-white'
                      : 'border-wa-border bg-wa-panel text-wa-text-2 hover:bg-wa-hover',
                  )}
                >
                  <Icon className="size-4" />
                  {t.label}
                </button>
              );
            })}
          </div>

          {deepLinkError ? (
            <p className="mt-2 rounded-lg bg-danger-soft px-3 py-2 text-xs text-danger">
              {deepLinkError}
            </p>
          ) : null}
        </div>

        {view === 'chat' ? (
          <ConversationList
            conversations={conversations}
            counts={counts}
            labels={labels}
            filters={filters}
            onFiltersChange={setFilters}
            selectedId={selectedId}
            onSelect={openConversation}
            onMarkUnread={handleMarkUnread}
            onDelete={handleDelete}
            onToggleLabel={handleToggleLabel}
            loading={listLoading}
            viewers={othersViewing}
          />
        ) : view === 'status' ? (
          <StatusList accountId={accountId} selected={statusJID} onSelect={openStatus} />
        ) : (
          <ChannelList
            accountId={accountId}
            selected={channel?.jid ?? null}
            onSelect={setChannel}
          />
        )}
      </section>

      <section className="hidden min-w-0 flex-1 lg:block">
        {view === 'status' ? (
          statusGroup ? (
            <StatusViewer
              accountId={accountId}
              group={statusGroup}
              onFinished={advanceStatus}
              onClose={() => setStatusJID(null)}
            />
          ) : (
            <Placeholder
              icon={CircleDot}
              title="Pilih status untuk melihat"
              hint="Klik satu status untuk langsung memutarnya. Setelah status orang itu habis, pemutaran lanjut sendiri ke orang berikutnya."
            />
          )
        ) : view === 'saluran' ? (
          channel ? (
            <ChannelViewer
              accountId={accountId}
              channel={channel}
              onChanged={(next) => setChannel(next.find((n) => n.jid === channel.jid) ?? null)}
            />
          ) : (
            <Placeholder
              icon={Megaphone}
              title="Pilih saluran untuk membaca"
              hint="Saluran hanya bisa dibaca. Postingannya diambil langsung dari WhatsApp saat dibuka."
            />
          )
        ) : (
          <MessageThread
          conversation={selected}
          messages={messages}
          loading={messagesLoading}
          sending={sending}
          error={error}
          canSend={Boolean(connected && selected)}
          onSend={handleSend}
          onForward={handleForward}
          conversations={conversations}
          onSendPoll={handleSendPoll}
          onSendQuickReply={handleSendQuickReply}
          onSendFile={handleSendFile}
          onEditMessage={handleEditMessage}
          onDeleteMessage={handleDeleteMessage}
          onReactMessage={handleReactMessage}
          unreadMark={unreadMark}
          mentionAnchor={mentionAnchor}
          ownJids={ownJids}
          applicationId={applicationId}
          onConversationChange={handleConversationChange}
          onPrivateReplySent={handlePrivateReplySent}
          viewers={selected ? othersViewing[selected.id] : undefined}
        />
        )}
      </section>

      {/* Below lg the thread takes over the whole screen once a chat is open. */}
      {selected ? (
        <section className="fixed inset-0 z-30 bg-wa-panel lg:hidden">
          <div className="flex h-full flex-col">
            <button
              type="button"
              onClick={() => setSelectedId(null)}
              className="flex items-center gap-1.5 border-b border-hairline px-4 py-3 text-sm text-ink-muted"
            >
              <ArrowLeft className="size-4" />
              Kembali ke daftar
            </button>
            <div className="min-h-0 flex-1">
              <MessageThread
                conversation={selected}
                messages={messages}
                loading={messagesLoading}
                sending={sending}
                error={error}
                canSend={Boolean(connected && selected)}
                onSend={handleSend}
                onForward={handleForward}
                conversations={conversations}
                onSendPoll={handleSendPoll}
                onSendQuickReply={handleSendQuickReply}
                onSendFile={handleSendFile}
                onEditMessage={handleEditMessage}
                onDeleteMessage={handleDeleteMessage}
                onReactMessage={handleReactMessage}
                unreadMark={unreadMark}
                mentionAnchor={mentionAnchor}
                ownJids={ownJids}
                applicationId={applicationId}
                onConversationChange={handleConversationChange}
                onPrivateReplySent={handlePrivateReplySent}
                viewers={othersViewing[selected.id]}
              />
            </div>
          </div>
        </section>
      ) : null}

      <ConfirmDialog request={confirm.request} onClose={confirm.close} />
    </div>
  );
}

/** The right-hand column before anything is picked, for Status and Saluran. */
function Placeholder({
  icon: Icon,
  title,
  hint,
}: {
  icon: LucideIcon;
  title: string;
  hint: string;
}) {
  return (
    <div className="grid h-full place-items-center bg-wa-chat px-6 text-center">
      <div>
        <Icon className="mx-auto size-8 text-wa-text-2" />
        <p className="mt-3 text-lg text-wa-text">{title}</p>
        <p className="mx-auto mt-1 max-w-[380px] text-sm text-wa-text-2">{hint}</p>
      </div>
    </div>
  );
}
