'use client';

import clsx from 'clsx';
import {
  AlertCircle,
  AtSign,
  Ban,
  Check,
  CheckCheck,
  Clock,
  FileText,
  Loader2,
  Image as ImageIcon,
  MapPin,
  Mic,
  Smile,
  Sticker,
  UserRound,
  Video,
} from 'lucide-react';
import { useRef, useState, type ComponentType } from 'react';

import { useAutoGrow } from '@/components/chat/autogrow';
import { MediaAttachment } from '@/components/chat/MediaAttachment';
import { MentionText } from '@/components/chat/MentionText';
import { MessageMenu, type MessageAction } from '@/components/chat/MessageMenu';
import { PollBubble } from '@/components/chat/PollBubble';
import { ReactionButton, ReactionChips, ReactionPicker } from '@/components/chat/Reactions';
import { MESSAGE_STATUS_LABEL, formatMessageTime } from '@/lib/format';
import type { Attachment, Message, MessageType, QuotedMessage } from '@/lib/types';

const MEDIA_ICON: Partial<Record<MessageType, ComponentType<{ className?: string }>>> = {
  image: ImageIcon,
  video: Video,
  audio: Mic,
  document: FileText,
  sticker: Sticker,
  location: MapPin,
  contact: UserRound,
  reaction: Smile,
};

const MEDIA_LABEL: Partial<Record<MessageType, string>> = {
  image: 'Foto',
  video: 'Video',
  audio: 'Pesan suara',
  document: 'Dokumen',
  sticker: 'Stiker',
  location: 'Lokasi',
  contact: 'Kontak',
  reaction: 'Reaksi',
};

/**
 * Tick indicator, following WhatsApp exactly: one grey tick sent, two grey
 * delivered, two blue read. The blue is the single strongest signal in the
 * whole thread, so it stays reserved for that one meaning.
 */
function StatusIcon({ status }: { status: Message['status'] }) {
  const common = 'size-[15px] shrink-0';
  switch (status) {
    case 'pending':
      return <Clock className={clsx(common, 'text-wa-tick')} aria-hidden />;
    case 'sent':
      return <Check className={clsx(common, 'text-wa-tick')} aria-hidden />;
    case 'delivered':
      return <CheckCheck className={clsx(common, 'text-wa-tick')} aria-hidden />;
    case 'read':
      return <CheckCheck className={clsx(common, 'text-wa-tick-read')} aria-hidden />;
    case 'failed':
      return <AlertCircle className={clsx(common, 'text-danger')} aria-hidden />;
    default:
      return null;
  }
}

/**
 * One chat bubble. Media is not downloaded in the MVP, so non-text messages
 * render as a labelled placeholder plus any caption that came with them.
 */
/** Deterministic colour for a group participant's name, as WhatsApp does. */
const SENDER_COLORS = [
  '#e542a3', '#02a698', '#7f66ff', '#d9762b', '#3d9ae8',
  '#c4532d', '#1f9d55', '#b4308f', '#0f7b6c', '#a34a9c',
];

function senderColor(name: string): string {
  let hash = 0;
  for (let i = 0; i < name.length; i += 1) hash = (hash * 31 + name.charCodeAt(i)) | 0;
  return SENDER_COLORS[Math.abs(hash) % SENDER_COLORS.length];
}

export function MessageBubble({
  message,
  showSender,
  grouped = false,
  onOpenMedia,
  canVote = false,
  onAction,
  editing = false,
  onEditSubmit,
  onEditCancel,
  ownJids,
  resolveMention,
  canRevokeAny = false,
  canManageMembers = false,
  senderMember = null,
  onReact,
  onJumpToQuoted,
  loadedMessageIds,
}: {
  message: Message;
  showSender: boolean;
  /** True when the previous bubble came from the same sender — no tail. */
  grouped?: boolean;
  /** Opens the full-screen viewer on an image or video. */
  onOpenMedia?: (attachment: Attachment) => void;
  /** False while the account is offline, which disables poll voting. */
  canVote?: boolean;
  /** Opens the menu on the bubble; omitted where actions do not apply. */
  onAction?: (message: Message, action: MessageAction) => void;
  /** True while this message is being edited in place. */
  editing?: boolean;
  onEditSubmit?: (message: Message, text: string) => Promise<void>;
  onEditCancel?: () => void;
  /** This account's own addresses, so a mention of us stands out further. */
  ownJids?: string[];
  /** Turns a mentioned JID into a readable name; falls back to the number. */
  resolveMention?: (jid: string) => string | null;
  /** True in a group this account administers: any message may be deleted. */
  canRevokeAny?: boolean;
  /** Passed straight through; see MessageMenu for what each one governs. */
  canManageMembers?: boolean;
  senderMember?: { jid: string; isAdmin: boolean } | null;
  /** Adds or clears a reaction; an empty emoji means "take mine back". */
  onReact?: (message: Message, emoji: string) => void;
  /** Scrolls the thread to the message this one is answering. */
  onJumpToQuoted?: (messageId: string) => void;
  /**
   * Which messages the thread currently holds.
   *
   * The thread loads the last fifty, so a reply to something older names a
   * message that exists in the database and not on screen. Without this the
   * quote would become a button that scrolls nowhere, which is the one outcome
   * worth more trouble than no button at all.
   */
  loadedMessageIds?: ReadonlySet<string>;
}) {
  const [pickerOpen, setPickerOpen] = useState(false);
  const mine = message.from_me;
  const revoked = Boolean(message.revoked_at);
  const attachments = revoked ? [] : (message.attachments ?? []);
  const hasMedia = attachments.length > 0;
  const poll = revoked ? null : message.poll;

  // With a real file present the icon placeholder is noise, and the filename
  // already appears on the document card — so only the caption is text.
  const Icon = hasMedia || poll || revoked ? undefined : MEDIA_ICON[message.type];
  // A poll renders its own question, so the body must not be repeated below it.
  const text = poll ? '' : hasMedia ? (message.caption ?? '') : (message.body ?? message.caption ?? '');

  // Who wrote it. The server resolves the name — saved contact, then push
  // name, then whatever came with the message, then the number — so this is
  // already the finished answer rather than a guess made here.
  const senderLabel = message.display_name ?? message.sender_name ?? message.sender_phone ?? '';
  // Names appear on group messages only. On a one-to-one chat there is exactly
  // one other person and repeating their name above every bubble is noise.
  const withSender = showSender && !mine && Boolean(senderLabel) && !grouped;
  // Outgoing group messages say who on our side sent them, which matters when
  // several operators share one number.
  const withOperator = showSender && mine && Boolean(senderLabel) && !grouped;

  // A sticker floats on the wallpaper without a bubble, as WhatsApp draws it.
  const bare = hasMedia && attachments[0].kind === 'sticker';

  const reactions = message.reactions ?? [];
  const myReaction = reactions.find((r) => r.mine)?.emoji ?? null;

  return (
    <div
      className={clsx(
        // A column so the chips sit under the bubble rather than beside it;
        // the width caps below still resolve against this full-width row.
        'group/bubble relative flex w-full flex-col',
        mine ? 'items-end' : 'items-start',
        // Room under the bubble for the chips, which overlap its lower edge.
        reactions.length > 0 && 'mb-3',
      )}
    >
      <div
        className={clsx(
          // Capped so a long paragraph stays readable — a line that spans a
          // wide monitor is hard to follow — but narrow messages still sit
          // flush against their own edge. Media is capped tighter still: a
          // picture sized for a text paragraph dominates the thread.
          'relative text-sm leading-snug',
          // Reading and editing want different shapes. The reading cap keeps a
          // paragraph from spanning a wide monitor, which is right until the
          // operator has to work inside it: a 4,800-character promo message
          // edited in a 250px column is a keyhole, and no amount of height
          // fixes a narrow one. So the bubble gives way while the editor is
          // open and takes its shape back when it closes.
          editing
            ? 'max-w-[min(680px,94%)]'
            : hasMedia
              ? 'max-w-[min(320px,80%)]'
              : 'max-w-[min(560px,72%)]',
          bare
            ? 'p-0'
            : 'px-[9px] pt-[6px] pb-[8px] shadow-[0_1px_0.5px_rgba(11,20,26,0.13)]',
          bare ? '' : mine ? 'bg-wa-out text-wa-text' : 'bg-wa-in text-wa-text',
          // Only the first bubble of a run carries the tail, so a burst of
          // messages reads as one block instead of a row of arrows.
          grouped
            ? 'rounded-lg'
            : mine
              ? 'wa-tail-out rounded-lg rounded-tr-none'
              : 'wa-tail-in rounded-lg rounded-tl-none',
        )}
      >
        {onReact && !revoked ? (
          <>
            <ReactionButton
              mine={mine}
              active={pickerOpen}
              onClick={() => setPickerOpen((v) => !v)}
            />
            {pickerOpen ? (
              <ReactionPicker
                current={myReaction}
                align={mine ? 'right' : 'left'}
                onClose={() => setPickerOpen(false)}
                onPick={(emoji) => {
                  setPickerOpen(false);
                  // Picking the emoji already given takes it back, which is the
                  // only way to un-react and so must be the obvious one.
                  onReact(message, emoji === myReaction ? '' : emoji);
                }}
              />
            ) : null}
          </>
        ) : null}

        {onAction && !bare ? (
          <MessageMenu
            message={message}
            mine={mine}
            // `showSender` is true exactly in groups, which is also the only
            // place a private reply means anything.
            inGroup={showSender}
            canRevokeAny={canRevokeAny}
            canManageMembers={canManageMembers}
            senderMember={senderMember}
            onAction={(a) => onAction(message, a)}
          />
        ) : null}

        {withSender ? (
          <p
            className="mb-0.5 text-xs font-medium"
            // Keyed on the address rather than the name, so a member who
            // changes their display name keeps the colour the operator has
            // already learned to associate with them.
            style={{ color: senderColor(message.participant_jid ?? message.sender_jid ?? senderLabel) }}
          >
            {senderLabel}
          </p>
        ) : null}

        {withOperator ? (
          <p className="mb-0.5 text-xs font-medium text-wa-text-2">{senderLabel}</p>
        ) : null}

        {/* The mention badge sits above the text: in a group of two hundred
            messages it is the reason the operator opened the chat at all. */}
        {message.mentions_me && !revoked ? (
          <p className="mb-1 inline-flex items-center gap-1 rounded-full bg-wa-accent/20 px-2 py-0.5 text-2xs font-medium text-wa-accent">
            <AtSign className="size-3" />
            Anda disebut
          </p>
        ) : null}

        {Icon ? (
          <p className="mb-1 inline-flex items-center gap-1.5 text-xs font-medium text-wa-text-2">
            <Icon className="size-3.5" />
            {MEDIA_LABEL[message.type] ?? message.type}
          </p>
        ) : null}

        {message.quoted && !revoked ? (
          <QuotedPreview
            quoted={message.quoted}
            mine={mine}
            onJump={onJumpToQuoted}
            loaded={loadedMessageIds}
          />
        ) : null}

        {/* Negative margins pull the media out to the bubble's edge, which is
            how WhatsApp frames a photo — the padding only applies to text. */}
        {hasMedia && !bare ? (
          <div className="-mx-[6px] -mt-[3px] mb-1 flex flex-col gap-[2px]">
            {attachments.map((attachment) => (
              <MediaAttachment
                key={attachment.id}
                attachment={attachment}
                onOpen={onOpenMedia}
                hasCaption={Boolean(text)}
              />
            ))}
          </div>
        ) : null}

        {bare ? (
          <MediaAttachment attachment={attachments[0]} onOpen={onOpenMedia} hasCaption={false} />
        ) : null}

        {poll ? <PollBubble poll={poll} canVote={canVote} /> : null}

        {/* A deleted message keeps its place in the thread rather than closing
            the gap — a bubble that vanishes leaves the reader wondering what
            they missed, which is why WhatsApp leaves the marker too. */}
        {revoked ? (
          <p className="flex items-center gap-1.5 text-sm italic text-wa-text-2">
            <Ban className="size-3.5 shrink-0" />
            Pesan ini dihapus
            <span className="inline-block w-[62px]" aria-hidden />
          </p>
        ) : editing && onEditSubmit && onEditCancel ? (
          <EditBox
            initial={text}
            placeholder={hasMedia ? 'Ubah keterangan' : 'Ubah pesan'}
            onSubmit={(value) => onEditSubmit(message, value)}
            onCancel={onEditCancel}
          />
        ) : text ? (
          // The trailing pad reserves room for the time, which WhatsApp floats
          // into the last line rather than putting on a line of its own.
          <p className="break-words whitespace-pre-wrap">
            <MentionText
              text={text}
              mentionedJids={message.mentioned_jids ?? []}
              ownJids={ownJids}
              resolveName={resolveMention}
            />
            <span className="inline-block w-[68px]" aria-hidden />
          </p>
        ) : null}

        {message.status === 'failed' && message.error_message ? (
          <p className="mt-1 text-2xs text-danger">Gagal: {message.error_message}</p>
        ) : null}

        {/* Uncaptioned media has no text line for the stamp to float into, so
            it gets a legible pill over the image instead — the same treatment
            WhatsApp gives a bare photo or sticker. */}
        <div
          className={clsx(
            'flex items-center gap-[3px] text-2xs',
            bare || (hasMedia && !text)
              ? 'absolute right-2 bottom-2 rounded-full bg-black/45 px-1.5 py-0.5 text-white/90'
              : poll
                // A poll has no last line for the stamp to tuck into, so it
                // sits on its own beneath rather than over the tally.
                ? 'mt-1 justify-end text-wa-text-2'
                : 'float-right -mt-[14px] ml-2 text-wa-text-2',
          )}
        >
          {message.edited_at && !revoked ? <span className="italic">diedit</span> : null}
          <time dateTime={message.timestamp}>{formatMessageTime(message.timestamp)}</time>
          {mine && !revoked ? (
            <>
              <StatusIcon status={message.status} />
              <span className="sr-only">{MESSAGE_STATUS_LABEL[message.status]}</span>
            </>
          ) : null}
        </div>
      </div>

      {/* Clicking a chip toggles this account's own reaction, so the tally is
          also the control — the same gesture WhatsApp gives it. */}
      <ReactionChips
        reactions={reactions}
        mine={mine}
        onToggle={(emoji) => onReact?.(message, emoji === myReaction ? '' : emoji)}
      />
    </div>
  );
}

/**
 * The quoted message above a reply.
 *
 * A coloured bar down the left and a dimmed panel, as WhatsApp draws it. The
 * quote is deliberately small: it is there to say what is being answered, not
 * to repeat the message.
 */
function QuotedPreview({
  quoted,
  mine,
  onJump,
  loaded,
}: {
  quoted: QuotedMessage;
  mine: boolean;
  /** Scrolls the thread to the message being answered. */
  onJump?: (messageId: string) => void;
  /** The ids currently rendered in the thread. */
  loaded?: ReadonlySet<string>;
}) {
  const who = quoted.from_me ? 'Kamu' : (quoted.sender_name ?? 'Pengirim');
  const accent = quoted.from_me ? '#4fc08d' : senderColor(quoted.sender_name ?? who);
  const thumb = quoted.thumbnail_b64 ? `data:image/jpeg;base64,${quoted.thumbnail_b64}` : null;

  const label =
    quoted.text ||
    (
      {
        image: 'Foto',
        video: 'Video',
        audio: 'Pesan suara',
        document: 'Dokumen',
        sticker: 'Stiker',
        poll: 'Polling',
      } as Record<string, string>
    )[quoted.type] ||
    'Pesan';

  // Two different reasons the original cannot be reached, and both end the
  // same way: the quote stays a plain panel. `quoted.id` is null once the
  // message has aged out of the synced window entirely; a id that the thread
  // has not loaded belongs to a message older than the fifty on screen. Either
  // way a button here would scroll nowhere, and the title says which it is
  // rather than leaving the operator pressing a dead panel.
  const target = quoted.id;
  const onScreen = Boolean(target && (!loaded || loaded.has(target)));
  const jumpable = Boolean(target && onJump && onScreen);

  const body = (
    <>
      <div className="min-w-0 flex-1 py-1.5 pr-1">
        <p className="truncate text-xs font-medium" style={{ color: accent }}>
          {who}
        </p>
        <p className="line-clamp-2 text-xs text-wa-text-2">{label}</p>
      </div>
      {thumb ? (
        // eslint-disable-next-line @next/next/no-img-element -- inline data URI
        <img src={thumb} alt="" className="size-[50px] shrink-0 object-cover" />
      ) : null}
    </>
  );

  const shell = clsx(
    'mb-1 flex w-full gap-2 overflow-hidden rounded-md pl-2 text-left',
    mine ? 'bg-black/10 dark:bg-black/25' : 'bg-black/[0.06] dark:bg-white/[0.07]',
    jumpable && 'cursor-pointer transition-colors hover:brightness-95',
  );
  const edge = { borderLeft: `3.5px solid ${accent}` };

  if (!jumpable) {
    return (
      <div
        className={shell}
        style={edge}
        title={
          target
            ? 'Pesan aslinya lebih lama dari bagian percakapan yang terbuka'
            : 'Pesan aslinya sudah di luar riwayat yang tersimpan'
        }
      >
        {body}
      </div>
    );
  }

  return (
    <button
      type="button"
      className={shell}
      style={edge}
      title="Lihat pesan yang dibalas"
      onClick={(event) => {
        // The bubble above this one opens the media viewer and the menu; a
        // press meant for the quote must not reach either.
        event.stopPropagation();
        onJump?.(target as string);
      }}
    >
      {body}
    </button>
  );
}

/**
 * In-place editor for a bubble's text.
 *
 * Enter saves and Escape cancels, matching the composer below it — an editor
 * that needed the mouse would be slower than retyping the message.
 */
function EditBox({
  initial,
  placeholder,
  onSubmit,
  onCancel,
}: {
  initial: string;
  placeholder: string;
  onSubmit: (text: string) => Promise<void>;
  onCancel: () => void;
}) {
  const [value, setValue] = useState(initial);
  const [busy, setBusy] = useState(false);
  const box = useRef<HTMLTextAreaElement>(null);

  // The text is already written, so the box opens at the size it needs rather
  // than making the operator scroll their own message to find the typo.
  useAutoGrow(box, value, 18, 0.5);

  async function save() {
    if (busy) return;
    setBusy(true);
    try {
      await onSubmit(value);
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="w-full min-w-[min(420px,70vw)]">
      <textarea
        ref={box}
        autoFocus
        value={value}
        disabled={busy}
        onChange={(event) => setValue(event.target.value)}
        onKeyDown={(event) => {
          if (event.key === 'Enter' && !event.shiftKey) {
            event.preventDefault();
            void save();
          }
          if (event.key === 'Escape') onCancel();
        }}
        rows={1}
        placeholder={placeholder}
        className="w-full resize-none rounded-md bg-wa-panel px-2.5 py-1.5 text-sm text-wa-text outline-none focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-wa-accent placeholder:text-wa-text-2 disabled:opacity-60"
      />
      {/* mt-2, not mt-1: at this text size four pixels between the last line
          of a scrolled box and the buttons reads as the two touching, and a
          Save button that looks stuck to the text looks like part of it. */}
      <div className="mt-2 flex items-center justify-end gap-3 text-xs">
        <button type="button" onClick={onCancel} disabled={busy} className="text-wa-text-2">
          Batal
        </button>
        <button
          type="button"
          onClick={() => void save()}
          disabled={busy}
          className="inline-flex items-center gap-1 font-medium text-wa-accent disabled:opacity-50"
        >
          {busy ? <Loader2 className="size-3 animate-spin" /> : null}
          Simpan
        </button>
      </div>
    </div>
  );
}

/** Centred date pill, as WhatsApp floats over the wallpaper. */
export function DaySeparator({ label }: { label: string }) {
  return (
    <div className="my-3 flex justify-center">
      <span className="rounded-lg bg-wa-panel px-3 py-[5px] text-xs font-medium text-wa-text-2 uppercase shadow-[0_1px_0.5px_rgba(11,20,26,0.13)]">
        {label}
      </span>
    </div>
  );
}
