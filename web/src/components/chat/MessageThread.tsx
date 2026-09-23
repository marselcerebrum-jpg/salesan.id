'use client';

import clsx from 'clsx';
import {
  ChevronDown,
  Eye,
  ImageIcon,
  Loader2,
  MessagesSquare,
  MoreVertical,
  SendHorizontal,
  Tag,
  UserMinus,
  Users,
  X,
} from 'lucide-react';
import { useCallback, useEffect, useMemo, useRef, useState, type FormEvent } from 'react';
import useSWR from 'swr';

import { AttachmentMenu, pickerAttributes, type PickerKind } from '@/components/chat/AttachmentMenu';
import {
  MediaComposer,
  makeDrafts,
  releaseDrafts,
  type Draft,
  type SendDraft,
} from '@/components/chat/MediaComposer';
import { EmojiPicker } from '@/components/chat/EmojiPicker';
import { ForwardDialog } from '@/components/chat/ForwardDialog';
import { GroupPanel } from '@/components/chat/GroupPanel';
import { MediaViewer } from '@/components/chat/MediaViewer';
import { DaySeparator, MessageBubble } from '@/components/chat/MessageBubble';
import type { MessageAction } from '@/components/chat/MessageMenu';
import { PollComposer } from '@/components/chat/PollComposer';
import { PrivateReplyDialog } from '@/components/chat/PrivateReplyDialog';
import { QuickReplyMenu, slashQuery } from '@/components/chat/QuickReplyMenu';
import { ConfirmDialog, useConfirm } from '@/components/ui/ConfirmDialog';
import { EmptyState, ErrorNote, Spinner } from '@/components/ui/Primitives';
import {
  conversationTitle,
  formatDayLabel,
  formatMessageTime,
  initials,
  jidToDisplay,
} from '@/lib/format';
import { filesFromClipboard, nameClipboardFile } from '@/lib/media';
import { fetcher, markQuickReplyUsed } from '@/lib/api';
import type {
  Attachment,
  Conversation,
  GroupMember,
  Message,
  PresenceViewer,
  PrivateReplyTarget,
  QuickReply,
} from '@/lib/types';

interface MessageThreadProps {
  conversation: Conversation | null;
  /**
   * Which application this inbox belongs to, so the slash menu offers this
   * brand's canned replies and not another brand's. Comes from the route,
   * which is where the reader's own choice of inbox already lives.
   */
  applicationId: string | null;
  messages: Message[];
  loading: boolean;
  sending: boolean;
  error: string | null;
  canSend: boolean;
  /** `replyTo` is the id of a message in this thread being quoted. */
  onSend: (body: string, replyTo: string | null) => Promise<void>;
  /** Re-sends a message into other conversations. */
  onForward: (message: Message, conversationIds: string[]) => Promise<void>;
  /** Every thread on this account, for the forward picker. */
  conversations: Conversation[];
  /** Creates a poll in this conversation. */
  onSendPoll: (poll: { name: string; options: string[]; allowMultiple: boolean }) => Promise<void>;
  /**
   * Sends a picture quick reply whole.
   *
   * Only picture replies reach this: a text reply is pasted into the composer
   * and edited before it goes. The image is fetched server-side from the
   * address stored on the reply, so nothing about it passes through here.
   */
  onSendQuickReply: (
    reply: QuickReply,
    caption: string,
    replyTo: string | null,
  ) => Promise<void>;
  /** Sends one attached file; the composer drives progress and retry. */
  onSendFile: (
    draft: SendDraft,
    onProgress: (fraction: number) => void,
    signal: AbortSignal,
  ) => Promise<void>;
  /** Changes a message's text, or a media message's caption. */
  onEditMessage: (message: Message, text: string) => Promise<void>;
  /** Deletes a message for everyone, or from this inbox only. */
  onDeleteMessage: (message: Message, scope: 'everyone' | 'me') => Promise<void>;
  /** Adds or clears a reaction; an empty emoji takes this account's back. */
  onReactMessage: (message: Message, emoji: string) => Promise<void>;
  /** Where the unread run begins, and how many messages it covers. */
  unreadMark: { messageId: string; count: number } | null;
  /** A mention to open the thread at, taking precedence over the divider. */
  mentionAnchor?: string | null;
  /** This account's own addresses, so a mention of us is highlighted harder. */
  ownJids?: string[];
  /** Called when a group edit changes the conversation itself. */
  onConversationChange: (conversation: Conversation) => void;
  /**
   * Called after a private reply to a group member has been sent, with the
   * one-to-one thread it landed in. The page decides what to do with that:
   * the reply is already delivered either way, so this is an offer to follow
   * it, not part of sending it.
   */
  onPrivateReplySent: (target: PrivateReplyTarget) => void;
  /**
   * Colleagues who have this same thread open right now, on any browser using
   * this number. Shown so two people do not answer the same customer at once.
   */
  viewers?: PresenceViewer[];
}

/** Right column of the inbox: header, scrolling thread, composer. */
export function MessageThread({
  conversation,
  applicationId,
  messages,
  loading,
  sending,
  error,
  canSend,
  onSend,
  onForward,
  conversations,
  onSendPoll,
  onSendQuickReply,
  onSendFile,
  onEditMessage,
  onDeleteMessage,
  onReactMessage,
  unreadMark,
  mentionAnchor,
  ownJids,
  viewers,
  onConversationChange,
  onPrivateReplySent,
}: MessageThreadProps) {
  const [draft, setDraft] = useState('');
  const [drafts, setDrafts] = useState<Draft[]>([]);
  const [pickerKind, setPickerKind] = useState<PickerKind>('photo-video');
  const [pickError, setPickError] = useState<string | null>(null);
  const [pollOpen, setPollOpen] = useState(false);
  // Bumped to request a file dialog; the effect below opens it once the input
  // has been re-rendered with the right filters.
  const [pendingPick, setPendingPick] = useState(0);
  const [editingId, setEditingId] = useState<string | null>(null);
  const [replyTo, setReplyTo] = useState<Message | null>(null);
  // A picture quick reply chosen but not yet sent. Its words sit in the draft
  // where they can be edited; this holds the picture that goes with them.
  const [pendingPicture, setPendingPicture] = useState<QuickReply | null>(null);
  // The text reply the draft came from, so it can be counted once it actually
  // goes. Cleared when the box is emptied: at that point whatever is typed
  // next is not that reply any more.
  const [usedQuickReply, setUsedQuickReply] = useState<string | null>(null);
  const [forwarding, setForwarding] = useState<Message | null>(null);
  // The group message being answered in private, or null.
  const [privateReply, setPrivateReply] = useState<Message | null>(null);
  // Whether the slash menu is showing. Separate from the draft so Escape can
  // dismiss it without also having to clear what was typed.
  const [slashOpen, setSlashOpen] = useState(false);
  const composerRef = useRef<HTMLTextAreaElement>(null);
  const confirm = useConfirm();
  const [viewerIndex, setViewerIndex] = useState<number | null>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const bottomRef = useRef<HTMLDivElement>(null);
  const unreadRef = useRef<HTMLDivElement>(null);
  // Tracks which thread has already been positioned, so later messages scroll
  // to the bottom instead of yanking the view back to the unread divider.
  const positionedFor = useRef<string | null>(null);
  // Bubbles that can be scrolled to, keyed by message id.
  const mentionRefs = useRef(new Map<string, HTMLDivElement>());
  const [flash, setFlash] = useState<string | null>(null);
  const scrollRef = useRef<HTMLDivElement>(null);
  const [atBottom, setAtBottom] = useState(true);
  const [groupPanelOpen, setGroupPanelOpen] = useState(false);

  // Messages that arrived while the operator was scrolled up. Counted rather
  // than merely flagged, so the button says how much is waiting.
  const unreadBelow = conversation?.unread_count ?? 0;

  // Opening a thread lands on the first unread message, matching WhatsApp —
  // landing at the bottom would make the operator scroll back up through the
  // very messages they came to read. Runs once per thread; afterwards new
  // messages scroll to the bottom as usual.
  //
  // A mention outranks the unread divider: it is the reason this particular
  // chat was opened, and in a group with two hundred unread it would otherwise
  // be buried.
  useEffect(() => {
    const id = conversation?.id ?? null;
    if (!id || messages.length === 0) return;

    if (positionedFor.current !== id) {
      positionedFor.current = id;

      if (mentionAnchor) {
        const node = mentionRefs.current.get(mentionAnchor);
        if (node) {
          node.scrollIntoView({ block: 'center' });
          // A brief highlight, because scrolling alone does not say which of
          // the messages on screen is the one being pointed at.
          setFlash(mentionAnchor);
          const timer = setTimeout(() => setFlash(null), 2200);
          return () => clearTimeout(timer);
        }
      }
      if (unreadMark && unreadRef.current) {
        unreadRef.current.scrollIntoView({ block: 'center' });
        return;
      }
    }
    bottomRef.current?.scrollIntoView({ block: 'end' });
  }, [conversation?.id, messages.length, unreadMark, mentionAnchor]);

  // A queued file belongs to the thread it was picked for. Switching threads
  // drops the queue rather than silently sending those files somewhere else,
  // and releases the object URLs holding their previews in memory.
  const draftsRef = useRef<Draft[]>([]);
  draftsRef.current = drafts;

  // Which message the queued files answer, if any. See acceptFiles.
  const mediaReplyRef = useRef<string | null>(null);

  useEffect(() => {
    setDraft('');
    setDrafts([]);
    setViewerIndex(null);
    setPickError(null);
    setPollOpen(false);
    setEditingId(null);
    setReplyTo(null);
    setForwarding(null);
    setPrivateReply(null);
    setGroupPanelOpen(false);
    setAtBottom(true);
    return () => releaseDrafts(draftsRef.current);
  }, [conversation?.id]);

  useEffect(() => {
    if (pendingPick === 0) return;
    fileInputRef.current?.click();
  }, [pendingPick, pickerKind]);

  // Group members, fetched only for groups and only to put names on mentions.
  // Without it a mention renders as "@6285171593270", which is correct but not
  // what anyone reading the chat is looking for.
  const { data: memberData } = useSWR<{ members: GroupMember[] }>(
    conversation && conversation.type === 'group'
      ? `/conversations/${conversation.id}/members`
      : null,
    fetcher,
  );

  const memberNames = useMemo(() => {
    const byUser = new Map<string, string>();
    for (const member of memberData?.members ?? []) {
      const user = member.jid.split('@')[0]?.split(':')[0];
      if (user && member.display_name) byUser.set(user, member.display_name);
    }
    return byUser;
  }, [memberData]);

  // Falls back to null so MentionText shows the number — a readable fallback
  // beats an empty highlight.
  const resolveMention = useCallback(
    (jid: string) => memberNames.get(jid.split('@')[0]?.split(':')[0] ?? '') ?? null,
    [memberNames],
  );

  // Every image and video in the thread, so the viewer's arrows walk the whole
  // conversation rather than one message's files.
  //
  // Expired files are left out: their bytes are gone, so stepping onto one
  // would show an error where a photo was expected.
  const viewable = useMemo(
    () =>
      messages.flatMap((message) =>
        (message.attachments ?? []).filter(
          (a) => (a.kind === 'image' || a.kind === 'video') && a.status !== 'expired',
        ),
      ),
    [messages],
  );

  // Object URLs behind the previews are released when the queue is dropped, so
  // an abandoned composer does not leak them for the life of the page.
  function discardDrafts() {
    releaseDrafts(draftsRef.current);
    setDrafts([]);
    if (mediaReplyRef.current) {
      // The quote belonged to this queue. Leaving the banner up afterwards
      // would silently attach it to whatever is typed next.
      mediaReplyRef.current = null;
      setReplyTo(null);
    }
  }

  /**
   * The file send, with the quote attached.
   *
   * Wrapped here rather than inside the composer because the reply belongs to
   * the thread, not to the picker: the composer's job is progress, ordering and
   * retry, and it should not have to know what a quote is.
   */
  const sendFileQuoting = useCallback(
    (file: SendDraft, onProgress: (fraction: number) => void, signal: AbortSignal) =>
      onSendFile({ ...file, replyTo: file.replyTo ?? mediaReplyRef.current }, onProgress, signal),
    [onSendFile],
  );

  function openPicker(kind: PickerKind) {
    setPickError(null);
    if (kind === 'poll') {
      setPollOpen(true);
      return; // a poll is built in a dialog, not picked from disk
    }
    setPickerKind(kind);
    // Opening is deferred to an effect rather than fired here, because the
    // input's accept/capture attributes come from `pickerKind` and are only on
    // the element after the next render. Clicking now would open the previous
    // picker's dialog — the document filter after choosing photos, say.
    setPendingPick((n) => n + 1);
  }

  function acceptFiles(files: FileList | null) {
    if (!files || files.length === 0) return;
    queueFiles(Array.from(files), pickerKind === 'document');
  }

  /**
   * Puts files into the attachment queue, whatever brought them in.
   *
   * One door for the file picker and for a paste, so a pasted screenshot goes
   * through exactly the same validation, preview, captioning, progress and
   * retry as one chosen from disk. A second path would have been a second set
   * of rules to keep in step.
   */
  function queueFiles(files: File[], asDocument: boolean) {
    if (files.length === 0) return;
    // Held in a ref rather than read from state at send time. The composer owns
    // retry, and a retry runs after the banner is gone: reading state then would
    // send the second attempt without the quote the first one had.
    if (draftsRef.current.length === 0) {
      mediaReplyRef.current = replyTo?.id ?? null;
    }
    const { drafts: added, rejected } = makeDrafts(files, asDocument);
    setPickError(rejected.length > 0 ? rejected.join(' · ') : null);
    if (added.length > 0) setDrafts((current) => [...current, ...added]);
  }

  function openMedia(attachment: Attachment) {
    const index = viewable.findIndex((a) => a.id === attachment.id);
    if (index >= 0) setViewerIndex(index);
  }

  function handleMessageAction(message: Message, action: MessageAction) {
    switch (action) {
      case 'copy':
        void navigator.clipboard?.writeText(message.body ?? message.caption ?? '');
        break;
      case 'reply':
        setReplyTo(message);
        composerRef.current?.focus();
        break;
      case 'forward':
        setForwarding(message);
        break;
      case 'private-reply':
        setPrivateReply(message);
        break;
      case 'edit':
        setEditingId(message.id);
        break;
      case 'delete-everyone':
        // Confirmed because it is not undoable and it reaches the other
        // person's phone — the one action here with consequences outside
        // this workspace. Deleting someone else's message says so plainly:
        // WhatsApp shows the group who did it.
        confirm.ask({
          title: message.from_me ? 'Hapus untuk semua?' : 'Hapus pesan anggota ini?',
          description: message.from_me
            ? 'Pesan ini akan hilang dari percakapan, termasuk di HP lawan bicara. Tindakan ini tidak bisa dibatalkan.'
            : `Anda menghapus pesan dari ${message.display_name ?? 'anggota lain'} sebagai admin grup. Semua anggota akan melihat bahwa pesan dihapus oleh admin, dan ini tidak bisa dibatalkan.`,
          confirmLabel: 'Hapus untuk semua',
          tone: 'danger',
          onConfirm: () => onDeleteMessage(message, 'everyone'),
        });
        break;
      case 'delete-me':
        confirm.ask({
          title: 'Hapus untuk saya?',
          description: 'Pesan hilang dari inbox ini. Di HP lawan bicara pesannya tetap ada.',
          confirmLabel: 'Hapus',
          tone: 'danger',
          icon: UserMinus,
          onConfirm: () => onDeleteMessage(message, 'me'),
        });
        break;
    }
  }

  async function submitEdit(message: Message, text: string) {
    await onEditMessage(message, text);
    setEditingId(null);
  }

  if (!conversation) {
    return (
      <div className="grid h-full place-items-center">
        <EmptyState
          icon={<MessagesSquare className="size-8" />}
          title="Pilih percakapan untuk mulai membalas."
          description="Daftar percakapan ada di panel kiri. Pesan baru akan muncul otomatis."
        />
      </div>
    );
  }

  const title = conversationTitle(conversation);
  const isGroup = conversation.type === 'group';
  // WhatsApp lets a group admin delete anyone's message. This decides whether
  // the option appears; the server still has the final say.
  const isGroupAdmin = isGroup && conversation.self_is_admin;

  /**
   * Hands the message off and clears the box straight away.
   *
   * Not awaited on purpose. Messages are paced on the server so they do not
   * look like a bot — a few seconds each — and blocking the composer for that
   * long would make the app feel broken. The bubble appears immediately with a
   * clock on it and gains its tick when the send completes, which is what
   * WhatsApp itself does on a slow connection.
   */
  function send() {
    const body = draft.trim();

    // A picture quick reply is waiting to go with whatever caption is in the
    // box — including none, because a picture with no words is a real message.
    // That is why this is checked before the empty-draft guard below.
    if (pendingPicture) {
      const reply = pendingPicture;
      // Read before the state is cleared. Clearing first is what sent a canned
      // picture as a fresh message while the operator had a customer's message
      // selected to answer, leaving the customer with no idea what it replied to.
      const quoted = replyTo?.id ?? null;
      setPendingPicture(null);
      setDraft('');
      setReplyTo(null);
      void onSendQuickReply(reply, body, quoted);
      return;
    }

    if (!body) return;

    const quoted = replyTo?.id ?? null;
    // Counted on the way out, not when the reply was picked: somebody who
    // loads a canned answer and then thinks better of it has not used it.
    const used = usedQuickReply;
    setDraft('');
    setReplyTo(null);
    setUsedQuickReply(null);
    void onSend(body, quoted);
    if (used) void markQuickReplyUsed(used);
  }

  function submit(event: FormEvent) {
    event.preventDefault();
    send();
  }

  /**
   * Inserts an emoji at the caret rather than appending it.
   *
   * Appending would be simpler and wrong: someone who clicks back into the
   * middle of a sentence to add a smiley expects it where the cursor is.
   */
  function insertEmoji(emoji: string) {
    const box = composerRef.current;
    if (!box) {
      setDraft((d) => d + emoji);
      return;
    }
    const start = box.selectionStart ?? draft.length;
    const end = box.selectionEnd ?? start;
    const next = draft.slice(0, start) + emoji + draft.slice(end);
    setDraft(next);

    // The caret has to be restored after React has written the new value,
    // otherwise it snaps to the end of the box.
    requestAnimationFrame(() => {
      box.focus();
      const at = start + emoji.length;
      box.setSelectionRange(at, at);
    });
  }

  let lastDay = '';

  return (
    <div className="flex h-full min-h-0 flex-col">
      <header className="flex items-center gap-3 border-b border-wa-border bg-wa-panel-2 px-4 py-[10px]">
        <span
          className={clsx(
            'grid size-10 shrink-0 place-items-center rounded-full text-sm font-medium',
            isGroup ? 'bg-wa-active text-wa-text-2' : 'bg-wa-accent text-white',
          )}
        >
          {isGroup ? <Users className="size-5" /> : initials(title)}
        </span>

        <div className="min-w-0 flex-1">
          <p className="truncate text-base text-wa-text">{title}</p>
          <p className="truncate text-sm text-wa-text-2">
            {isGroup ? 'Grup' : (conversation.phone_number ?? jidToDisplay(conversation.chat_jid))}
          </p>
          {/* Somebody else is in this thread. Named, not counted: the point
              is to know whom to check with before typing. */}
          {viewers && viewers.length > 0 ? (
            <p
              className="mt-0.5 flex items-center gap-1 truncate text-xs text-wa-accent"
              title={`Sedang dibuka oleh ${viewers.map((v) => v.name).join(', ')}`}
            >
              <Eye className="size-3.5 shrink-0" aria-hidden />
              <span className="truncate">
                Sedang dibuka oleh {viewers.map((v) => v.name).join(', ')}
              </span>
            </p>
          ) : null}
        </div>

        {/* Labels applied to this thread, shown but not edited here: the
            controls live on the conversation row, where the operator is
            already deciding what to do with a chat. */}
        {conversation.labels.length > 0 ? (
          <div className="flex flex-wrap justify-end gap-1">
            {conversation.labels.map((label) => (
              <span
                key={label.id}
                style={{
                  color: label.color,
                  borderColor: `${label.color}40`,
                  backgroundColor: `${label.color}14`,
                }}
                className="inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-2xs font-medium"
              >
                <Tag className="size-2.5" />
                {label.name}
              </span>
            ))}
          </div>
        ) : null}

        {/* Group tools live behind one control rather than spread across the
            header: viewing members and editing the description are occasional
            actions, and the header belongs to the conversation. */}
        {isGroup ? (
          <button
            type="button"
            onClick={() => setGroupPanelOpen(true)}
            aria-label="Info grup"
            title="Info grup"
            className="grid size-9 shrink-0 place-items-center rounded-full text-wa-text-2 transition-colors hover:bg-wa-active"
          >
            <MoreVertical className="size-5" />
          </button>
        ) : null}
      </header>

      {/* Bubbles run to the edges of the panel rather than sitting in a
          centred column. On a wide screen the centred version left a narrow
          strip of conversation with empty margins either side; hugging the
          edges gives the thread the whole width and makes the direction of a
          message readable at a glance. */}
      <div
        ref={scrollRef}
        onScroll={(event) => {
          // "Near enough" rather than exactly at the bottom: a thread settles a
          // pixel or two off after images finish loading, and a button that
          // appears then would be noise.
          const el = event.currentTarget;
          setAtBottom(el.scrollHeight - el.scrollTop - el.clientHeight < 120);
        }}
        className="wa-wallpaper scrollbar-slim relative min-h-0 flex-1 overflow-y-auto px-3 py-4"
      >
        {loading && messages.length === 0 ? (
          <Spinner label="Memuat pesan…" />
        ) : messages.length === 0 ? (
          <EmptyState
            title="Belum ada pesan di percakapan ini"
            description="Kirim pesan pertama, atau tekan Sinkron untuk menarik riwayat dari HP."
          />
        ) : (
          <div className="flex w-full flex-col gap-[2px]">
            {messages.map((message, index) => {
              const day = formatDayLabel(message.timestamp);
              const showDay = day !== lastDay;
              lastDay = day;
              const startsUnread = unreadMark?.messageId === message.id;

              // A run of messages from the same sender is drawn as one block:
              // only its first bubble gets a tail, the rest sit tight beneath.
              const prev = messages[index - 1];
              const grouped =
                Boolean(prev) &&
                prev.from_me === message.from_me &&
                prev.sender_jid === message.sender_jid &&
                !showDay &&
                !startsUnread;

              return (
                <div
                  key={message.id}
                  ref={(node) => {
                    if (node) mentionRefs.current.set(message.id, node);
                    else mentionRefs.current.delete(message.id);
                  }}
                  className={clsx(
                    grouped ? '' : 'mt-[10px] first:mt-0',
                    flash === message.id &&
                      'rounded-lg ring-2 ring-wa-accent/60 transition-shadow duration-500',
                  )}
                >
                  {showDay ? <DaySeparator label={day} /> : null}
                  {startsUnread ? (
                    <div
                      ref={unreadRef}
                      role="separator"
                      className="my-3 flex justify-center"
                    >
                      <span className="w-full rounded-lg bg-wa-panel py-[5px] text-center text-xs font-medium text-wa-text-2 shadow-[0_1px_0.5px_rgba(11,20,26,0.13)]">
                        {unreadMark.count} pesan belum dibaca
                      </span>
                    </div>
                  ) : null}
                  <MessageBubble
                    message={message}
                    showSender={isGroup}
                    grouped={grouped}
                    onOpenMedia={openMedia}
                    canVote={canSend}
                    onAction={handleMessageAction}
                    editing={editingId === message.id}
                    onEditSubmit={submitEdit}
                    onEditCancel={() => setEditingId(null)}
                    ownJids={ownJids}
                    resolveMention={resolveMention}
                    canRevokeAny={isGroupAdmin}
                    // A reaction is a send like any other, so it is offered
                    // only while the account is actually connected.
                    onReact={
                      canSend
                        ? (target, emoji) => {
                            void onReactMessage(target, emoji);
                          }
                        : undefined
                    }
                  />
                </div>
              );
            })}
          </div>
        )}
        <div ref={bottomRef} />
      </div>

      {/* Only while there is somewhere to go. A permanent button pointing at
          the bottom of a thread already at the bottom is furniture. */}
      {!atBottom && messages.length > 0 ? (
        <button
          type="button"
          onClick={() => bottomRef.current?.scrollIntoView({ behavior: 'smooth', block: 'end' })}
          aria-label="Ke pesan terbaru"
          title="Ke pesan terbaru"
          className="absolute right-5 bottom-[86px] z-10 grid size-10 place-items-center rounded-full border border-wa-border bg-wa-panel text-wa-text-2 shadow-e3 transition-colors hover:bg-wa-active"
        >
          <ChevronDown className="size-5" />
          {unreadBelow > 0 ? (
            <span className="absolute -top-1.5 -right-1.5 grid h-[19px] min-w-[19px] place-items-center rounded-full bg-wa-badge px-1 text-2xs font-medium text-[#111b21]">
              {unreadBelow > 99 ? '99+' : unreadBelow}
            </span>
          ) : null}
        </button>
      ) : null}

      <form onSubmit={submit} className="border-t border-wa-border bg-wa-panel-2 px-4 py-[10px]">
        {error ? (
          <div className="mb-2">
            <ErrorNote message={error} />
          </div>
        ) : null}

        {pickError ? (
          <div className="mb-2">
            <ErrorNote message={pickError} />
          </div>
        ) : null}

        {!canSend ? (
          <p className="mb-2 rounded-lg bg-wa-panel px-3 py-2 text-xs text-wa-text-2">
            Akun ini sedang tidak terhubung. Hubungkan perangkat dulu untuk membalas.
          </p>
        ) : null}

        {/* The message being answered sits above the box, so it is visible
            while typing rather than only implied by a highlighted bubble. */}
        {replyTo ? (
          <div className="mb-2 flex items-stretch gap-2 overflow-hidden rounded-lg bg-wa-panel">
            <span className="w-1 shrink-0 bg-wa-accent" aria-hidden />
            <div className="min-w-0 flex-1 py-1.5">
              <p className="text-xs font-medium text-wa-accent">
                {replyTo.from_me ? 'Kamu' : (replyTo.sender_name ?? 'Pengirim')}
              </p>
              <p className="truncate text-xs text-wa-text-2">
                {replyTo.body ??
                  replyTo.caption ??
                  (replyTo.attachments[0]?.file_name || 'Media')}
              </p>
            </div>
            <button
              type="button"
              onClick={() => setReplyTo(null)}
              aria-label="Batalkan balasan"
              className="grid w-9 shrink-0 place-items-center text-wa-text-2 hover:text-wa-text"
            >
              <X className="size-4" />
            </button>
          </div>
        ) : null}

        {/*
         * The picture waiting to go with whatever is typed below.
         *
         * Shown for the same reason the quoted message above is: something is
         * about to be attached to this send, and it must be visible while the
         * words are being written rather than only discovered afterwards. The
         * cross removes the picture and leaves the words, which is what
         * somebody who picked the wrong shortcut wants.
         */}
        {pendingPicture ? (
          <div className="mb-2 flex items-center gap-2 overflow-hidden rounded-lg bg-wa-panel px-3 py-2">
            <ImageIcon className="size-4 shrink-0 text-wa-accent" aria-hidden />
            <div className="min-w-0 flex-1">
              <p className="truncate text-xs font-medium text-wa-text">
                Gambar dari <span className="font-mono">/{pendingPicture.shortcut}</span>
              </p>
              <p className="truncate text-2xs text-wa-text-2">
                Teks di bawah jadi captionnya, dan masih bisa diubah.
              </p>
            </div>
            <button
              type="button"
              onClick={() => setPendingPicture(null)}
              aria-label="Batalkan gambar"
              className="grid size-7 shrink-0 place-items-center rounded-full text-wa-text-2 hover:bg-wa-active hover:text-wa-text"
            >
              <X className="size-4" />
            </button>
          </div>
        ) : null}

        {/* `relative` is what the slash menu below positions against, so it
            rises out of the composer row rather than out of the page. */}
        <div className="relative flex items-end gap-1">
          <AttachmentMenu disabled={!canSend} onPick={openPicker} />
          <EmojiPicker disabled={!canSend} onPick={insertEmoji} />
          {/* One hidden input serves every menu entry; its accept/capture
              attributes change with the entry that opened it. */}
          <input
            ref={fileInputRef}
            type="file"
            hidden
            {...pickerAttributes(pickerKind)}
            onChange={(event) => {
              acceptFiles(event.target.files);
              // Reset so picking the same file twice fires change again.
              event.target.value = '';
            }}
          />
          {/* Anchored to the composer row, which is what `relative` on the
              wrapper below is for. */}
          <QuickReplyMenu
            query={slashOpen ? slashQuery(draft) : null}
            applicationId={applicationId}
            onPick={(reply) => {
              setSlashOpen(false);

              // A picture reply loads the same way a text one does: its words
              // go into the composer so they can be changed, and the picture
              // waits above the box until send.
              //
              // It used to send immediately, which meant the caption was
              // whatever was stored and could not be adjusted for the person
              // being answered — the opposite of what a canned reply is for.
              if (reply.media_url) {
                setPendingPicture(reply);
              } else {
                setUsedQuickReply(reply.id);
              }

              setDraft(reply.body);
              // Focus returns to the composer with the caret at the end, so
              // the reply can be adjusted immediately.
              requestAnimationFrame(() => {
                const box = composerRef.current;
                if (!box) return;
                box.focus();
                box.setSelectionRange(box.value.length, box.value.length);
              });
            }}
            onClose={() => setSlashOpen(false)}
          />

          <textarea
            ref={composerRef}
            value={draft}
            onChange={(event) => {
              setDraft(event.target.value);
              // Reopen whenever the draft becomes a slash query again, so
              // deleting back to "/" brings the menu back rather than
              // leaving it shut until the field is cleared.
              setSlashOpen(slashQuery(event.target.value) !== null);
              // Emptying the box abandons the canned reply. Editing it does
              // not: adjusting the wording before sending is the point, and
              // that still counts as using it.
              if (event.target.value.trim() === '') setUsedQuickReply(null);
            }}
            onKeyDown={(event) => {
              // The slash menu owns the arrows and Enter while it is open; it
              // listens in the capture phase, so nothing reaches here.
              if (event.key === 'Enter' && !event.shiftKey) {
                event.preventDefault();
                send();
              }
            }}
            onPaste={(event) => {
              if (!canSend) return;
              const pasted = filesFromClipboard(event.clipboardData);
              if (pasted.length === 0) return; // plain text: let the browser paste
              // The browser would otherwise drop the image on the floor and
              // paste whatever text came with it, which is how a screenshot
              // turns into a filename or into nothing at all.
              event.preventDefault();
              // Always as media, never as a document: a screenshot pasted from
              // the clipboard is a picture, and `kindOfFile` sorts anything
              // that is not an image, video or audio into a document anyway.
              queueFiles(pasted.map((file) => nameClipboardFile(file)), false);
            }}
            rows={1}
            disabled={!canSend}
            placeholder={canSend ? 'Ketik pesan' : 'Tidak bisa mengirim saat terputus'}
            aria-label="Ketik pesan"
            className="max-h-40 min-h-[42px] flex-1 resize-y rounded-lg bg-wa-panel px-4 py-[11px] text-base text-wa-text outline-none focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-wa-accent placeholder:text-wa-text-2 disabled:opacity-60"
          />
          <button
            type="submit"
            // A picture with no caption is a real message, so the button stays
            // live for one even when the box is empty.
            disabled={!canSend || (draft.trim() === '' && !pendingPicture)}
            aria-label="Kirim"
            className="grid size-[42px] shrink-0 place-items-center rounded-full text-wa-text-2 transition-colors hover:bg-wa-active disabled:opacity-40 disabled:hover:bg-transparent"
          >
            {sending ? (
              <Loader2 className="size-5 animate-spin" aria-hidden />
            ) : (
              <SendHorizontal className="size-5" aria-hidden />
            )}
          </button>
        </div>
      </form>

      <MediaComposer
        drafts={drafts}
        onChange={setDrafts}
        onClose={discardDrafts}
        onSendFile={sendFileQuoting}
        onAddFiles={(asDocument) => openPicker(asDocument ? 'document' : 'photo-video')}
      />

      <PollComposer open={pollOpen} onClose={() => setPollOpen(false)} onSubmit={onSendPoll} />

      <PrivateReplyDialog
        message={privateReply}
        onClose={() => setPrivateReply(null)}
        onSent={onPrivateReplySent}
      />

      <ForwardDialog
        message={forwarding}
        conversations={conversations}
        onClose={() => setForwarding(null)}
        onForward={onForward}
      />

      <GroupPanel
        conversation={conversation}
        open={groupPanelOpen}
        onClose={() => setGroupPanelOpen(false)}
        onConversationChange={onConversationChange}
      />

      <ConfirmDialog request={confirm.request} onClose={confirm.close} />

      <MediaViewer
        items={viewable}
        index={viewerIndex}
        onIndexChange={setViewerIndex}
        onClose={() => setViewerIndex(null)}
        captionFor={(attachment) =>
          messages.find((m) => m.id === attachment.message_id)?.caption ?? null
        }
        senderFor={(attachment) => {
          const owner = messages.find((m) => m.id === attachment.message_id);
          if (!owner) return null;
          return {
            name: owner.from_me ? 'Kamu' : (owner.sender_name ?? conversationTitle(conversation)),
            time: formatDayLabel(owner.timestamp) + ' ' + formatMessageTime(owner.timestamp),
          };
        }}
        onAction={(attachment, action) => {
          const owner = messages.find((m) => m.id === attachment.message_id);
          if (!owner) return;
          setViewerIndex(null);
          handleMessageAction(owner, action);
        }}
      />
    </div>
  );
}
