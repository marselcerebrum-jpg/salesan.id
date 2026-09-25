'use client';

import clsx from 'clsx';
import { CircleDashed, Eye, Loader2, Pause, Play, Search, Trash2, X } from 'lucide-react';
import { useEffect, useMemo, useRef, useState } from 'react';
import useSWR, { mutate } from 'swr';

import { ConfirmDialog, useConfirm } from '@/components/ui/ConfirmDialog';
import { attachmentUnreadableText, useAttachmentUrl } from '@/lib/useAttachmentUrl';
import {
  fetcher,
  markStatusSeen,
  revokeStatus,
  statusPath,
  type StatusPost,
} from '@/lib/api';
import { initials } from '@/lib/format';
import { useRealtimeEvent } from '@/lib/realtime';
import type { MessageNewPayload } from '@/lib/types';

/**
 * Status, the WhatsApp kind.
 *
 * Every contact's Status arrives on one address, status@broadcast, which is why
 * it is stored as one conversation and read back grouped by who posted rather
 * than as a thread. Nobody reads Status as a chronological list of everybody at
 * once; they read it one person at a time.
 *
 * Three groups, in the order they are wanted: what we posted, what has not been
 * opened, and what has. Same order WhatsApp uses, for the same reason — the
 * unopened ones are the only ones anybody is looking for.
 */

export interface StatusGroup {
  senderJID: string;
  name: string;
  avatar: string | null;
  posts: StatusPost[];
  latest: string;
  unseen: number;
  fromMe: boolean;
}

/**
 * `enabled` is how the inbox reads the same list without paying for it: the
 * page needs the playback order only while the Status tab is open, and SWR
 * shares one cache entry with the list below it rather than fetching twice.
 */
export function useStatusGroups(accountId: string, enabled = true) {
  const { data, isLoading, mutate } = useSWR<{ status: StatusPost[] }>(
    enabled ? statusPath(accountId) : null,
    fetcher,
    { refreshInterval: 60_000 },
  );

  /*
   * A Status arriving is an ordinary message.new, whether somebody else posted
   * it or this workspace published a Story from the Story menu — the queue
   * announces its own on the same event. So the list follows the socket and the
   * minute-long poll becomes the floor rather than the mechanism.
   *
   * Throttled, because message.new also fires for every chat message while this
   * tab happens to be open, and a busy inbox would otherwise refetch the Status
   * list dozens of times a minute for nothing.
   */
  const lastRefresh = useRef(0);
  useRealtimeEvent<MessageNewPayload>('message.new', (payload) => {
    if (!enabled || payload.account_id !== accountId) return;
    const now = Date.now();
    if (now - lastRefresh.current < 3_000) return;
    lastRefresh.current = now;
    void mutate();
  });

  const groups = useMemo(() => {
    const byJID = new Map<string, StatusGroup>();
    for (const p of data?.status ?? []) {
      let g = byJID.get(p.sender_jid);
      if (!g) {
        g = {
          senderJID: p.sender_jid,
          name: p.from_me ? 'Status Saya' : p.sender_name,
          avatar: p.avatar_url,
          posts: [],
          latest: p.posted_at,
          unseen: 0,
          fromMe: p.from_me,
        };
        byJID.set(p.sender_jid, g);
      }
      g.posts.push(p);
      if (p.posted_at > g.latest) g.latest = p.posted_at;
      if (!p.seen_at && !p.from_me) g.unseen += 1;
    }
    return [...byJID.values()].sort((a, b) => b.latest.localeCompare(a.latest));
  }, [data]);

  return { groups, isLoading, mutate };
}

/**
 * The three groups the list draws, in the order it draws them.
 *
 * One function rather than two lists because the same order is the playback
 * order: when one person's updates run out the viewer moves on to whoever is
 * next on screen. If the two could drift, a Story would continue with somebody
 * the reader cannot see in the list.
 */
function partitionStatusGroups(groups: StatusGroup[]) {
  return {
    mine: groups.filter((g) => g.fromMe),
    fresh: groups.filter((g) => !g.fromMe && g.unseen > 0),
    seen: groups.filter((g) => !g.fromMe && g.unseen === 0),
  };
}

/** The whole list flattened, which is the order one Story follows another in. */
export function orderStatusGroups(groups: StatusGroup[]): StatusGroup[] {
  const p = partitionStatusGroups(groups);
  return [...p.mine, ...p.fresh, ...p.seen];
}

export function StatusList({
  accountId,
  selected,
  onSelect,
}: {
  accountId: string;
  selected: string | null;
  onSelect: (group: StatusGroup) => void;
}) {
  const { groups, isLoading } = useStatusGroups(accountId);
  const [search, setSearch] = useState('');

  const needle = search.trim().toLowerCase();
  const shown = groups.filter((g) => !needle || g.name.toLowerCase().includes(needle));

  const { mine, fresh, seen } = partitionStatusGroups(shown);

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <label className="mx-3 mb-2 flex items-center gap-2 rounded-lg bg-wa-panel-2 px-3 py-2">
        <Search className="size-4 shrink-0 text-wa-text-2" />
        <input
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          placeholder="Cari status"
          aria-label="Cari status"
          className="w-full bg-transparent text-sm text-wa-text outline-none placeholder:text-wa-text-2"
        />
      </label>

      <div className="min-h-0 flex-1 overflow-y-auto pb-4">
        {isLoading && groups.length === 0 ? (
          <p className="flex items-center gap-2 px-4 py-6 text-sm text-wa-text-2">
            <Loader2 className="size-4 animate-spin" />
            Memuat status…
          </p>
        ) : shown.length === 0 ? (
          <div className="px-4 py-8 text-center">
            <CircleDashed className="mx-auto size-7 text-wa-text-2" />
            <p className="mt-2 text-sm text-wa-text">Belum ada status</p>
            {/* The last sentence is the one that matters: a deleted status is
                filtered out, and without saying so an empty list reads as a
                sync failure rather than as the rule working. */}
            <p className="mt-1 text-xs text-wa-text-2">
              Status Anda dan status kontak muncul di sini, lalu hilang sendiri setelah 24 jam.
              Status yang sudah dihapus tidak ditampilkan.
            </p>
          </div>
        ) : (
          <>
            <Section title="Status Saya" groups={mine} selected={selected} onSelect={onSelect} />
            <Section title="Terbaru" groups={fresh} selected={selected} onSelect={onSelect} />
            <Section title="Telah Dilihat" groups={seen} selected={selected} onSelect={onSelect} />
          </>
        )}
      </div>
    </div>
  );
}

function Section({
  title,
  groups,
  selected,
  onSelect,
}: {
  title: string;
  groups: StatusGroup[];
  selected: string | null;
  onSelect: (g: StatusGroup) => void;
}) {
  if (groups.length === 0) return null;
  return (
    <>
      <p className="px-4 pt-3 pb-1 text-2xs font-semibold tracking-wide text-wa-text-2 uppercase">
        {title}
      </p>
      {groups.map((g) => (
        <button
          key={g.senderJID}
          type="button"
          onClick={() => onSelect(g)}
          className={clsx(
            'flex w-full items-center gap-3 px-4 py-2.5 text-left transition-colors',
            selected === g.senderJID ? 'bg-wa-active' : 'hover:bg-wa-hover',
          )}
        >
          <Avatar group={g} />
          <span className="min-w-0 flex-1">
            <span className="block truncate text-sm text-wa-text">{g.name}</span>
            <span className="block truncate text-xs text-wa-text-2">
              {formatWhen(g.latest)}
              {g.posts.length > 1 ? ` · ${g.posts.length} update` : ''}
            </span>
          </span>
        </button>
      ))}
    </>
  );
}

/**
 * The ring around an avatar, which is the whole status list's only signal.
 *
 * Solid when something is unopened, faint once everything has been. Drawn as a
 * ring rather than a badge because that is what people already read it as.
 */
function Avatar({ group }: { group: StatusGroup }) {
  return (
    <span
      className={clsx(
        'grid size-10 shrink-0 place-items-center rounded-full p-[2px]',
        group.unseen > 0 ? 'bg-wa-accent' : 'bg-wa-divider',
      )}
    >
      <span className="grid size-full place-items-center overflow-hidden rounded-full bg-wa-panel">
        {group.avatar ? (
          // eslint-disable-next-line @next/next/no-img-element
          <img src={group.avatar} alt="" className="size-full object-cover" />
        ) : (
          <span className="text-xs font-medium text-wa-text-2">{initials(group.name)}</span>
        )}
      </span>
    </span>
  );
}

/* --- viewer ---------------------------------------------------------------- */

/** How long a Status without a duration of its own is held on screen. */
const SLIDE_MS = 5000;

/**
 * One person's Status, one post at a time, on a timer.
 *
 * The timer is the thing that makes it a Story rather than a gallery: each post
 * gets its own bar across the top, the bar fills, and the next one starts. A
 * video runs for as long as the video does, because cutting it off at a fixed
 * five seconds would be showing part of something and calling it seen.
 *
 * Holding the pointer down pauses it, which is the one gesture everybody
 * already knows from the app this imitates.
 *
 * When the last update runs out it does not stop: `onFinished` carries the
 * viewer on to the next person, the way a Story reel does. Stopping on the
 * final frame would make watching everybody a matter of going back to the list
 * and clicking again for each one.
 *
 * Marked seen locally when it is opened, and only locally: telling WhatsApp
 * would send the poster a read receipt, and a receipt should mean somebody
 * watched, not that a panel rendered.
 */
export function StatusViewer({
  accountId,
  group,
  onFinished,
  onClose,
}: {
  /** The number whose Status list is refreshed after a delete. */
  accountId: string;
  group: StatusGroup;
  /** Called when the last update ends, whether by timer or by tapping past it. */
  onFinished?: () => void;
  /** Returns to the list-only view this screen started in. */
  onClose?: () => void;
}) {
  const [index, setIndex] = useState(0);
  const [progress, setProgress] = useState(0);
  const [paused, setPaused] = useState(false);
  // Held in a ref so handing the viewer a fresh callback on every parent render
  // cannot restart the running timer.
  const finished = useRef(onFinished);
  useEffect(() => {
    finished.current = onFinished;
  }, [onFinished]);
  // A media post holds the timer until its file is on screen. Starting the
  // clock while a photo is still downloading spends the slide on a spinner.
  const [ready, setReady] = useState(false);
  const confirm = useConfirm();
  const [deleteError, setDeleteError] = useState<string | null>(null);

  const post = group.posts[index] ?? group.posts[0];

  // Back to the first post whenever the person changes, and the clock with it.
  useEffect(() => {
    setIndex(0);
    setProgress(0);
    setReady(false);
  }, [group.senderJID]);

  useEffect(() => {
    setProgress(0);
    setReady(false);
  }, [index]);

  const total = post?.duration_secs ? post.duration_secs * 1000 : SLIDE_MS;

  useEffect(() => {
    if (!post || paused || !ready) return;

    const started = Date.now();
    const from = progress;
    const timer = window.setInterval(() => {
      const pct = from + ((Date.now() - started) / total) * 100;
      if (pct >= 100) {
        setProgress(100);
        window.clearInterval(timer);
        // Never loops back to the first update: a Story that restarts by itself
        // makes it impossible to tell what has already been watched. The end of
        // this person is the start of the next one instead.
        if (index + 1 < group.posts.length) setIndex(index + 1);
        else finished.current?.();
        return;
      }
      setProgress(pct);
    }, 50);

    return () => window.clearInterval(timer);
    // `progress` is the resume point, read once when the effect starts; adding
    // it to the deps would restart the interval on every tick.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [post?.message_id, index, paused, ready, total, group.posts.length]);

  if (!post) return null;

  return (
    <div className="flex h-full min-h-0 flex-col bg-wa-chat">
      {/* One bar per post, the way the app this imitates draws it. */}
      <div className="flex gap-1 bg-wa-panel px-4 pt-3">
        {group.posts.map((p, i) => (
          <button
            key={p.message_id}
            type="button"
            onClick={() => setIndex(i)}
            aria-label={`Status ${i + 1} dari ${group.posts.length}`}
            className="h-1 min-w-0 flex-1 overflow-hidden rounded-full bg-wa-divider"
          >
            <span
              className="block h-full bg-wa-accent"
              style={{
                width: i < index ? '100%' : i === index ? `${progress}%` : '0%',
                transition: i === index ? 'width 50ms linear' : undefined,
              }}
            />
          </button>
        ))}
      </div>

      <header className="flex items-center gap-3 border-b border-wa-border bg-wa-panel px-4 py-3">
        <Avatar group={group} />
        <div className="min-w-0 flex-1">
          <p className="truncate text-sm font-medium text-wa-text">{group.name}</p>
          <p className="text-xs text-wa-text-2">
            {formatWhen(post.posted_at)}
            {group.posts.length > 1 ? ` · ${index + 1}/${group.posts.length}` : ''}
          </p>
        </div>
        <button
          type="button"
          onClick={() => setPaused((v) => !v)}
          aria-label={paused ? 'Lanjutkan' : 'Jeda'}
          className="grid size-9 place-items-center rounded-lg text-wa-text-2 transition-colors hover:bg-wa-hover"
        >
          {paused ? <Play className="size-4" /> : <Pause className="size-4" />}
        </button>
        {/* Our own status only: WhatsApp lets nobody delete somebody else's. */}
        {group.fromMe ? (
          <button
            type="button"
            onClick={() => {
              setPaused(true);
              confirm.ask({
                title: 'Hapus status ini?',
                description:
                  'Status langsung hilang dari WhatsApp untuk semua yang bisa melihatnya, dan tidak bisa dikembalikan. Jumlah penonton yang sudah terdeteksi tetap tercatat.',
                confirmLabel: 'Hapus untuk semua',
                tone: 'danger',
                icon: Trash2,
                onConfirm: async () => {
                  try {
                    await revokeStatus(post.message_id);
                    setDeleteError(null);
                    // The last one gone means there is nothing left to watch.
                    if (group.posts.length <= 1) onClose?.();
                    else if (index > 0) setIndex(index - 1);
                    await mutate(statusPath(accountId));
                  } catch (e) {
                    setDeleteError(e instanceof Error ? e.message : 'Status gagal dihapus.');
                  }
                },
              });
            }}
            aria-label="Hapus status"
            title="Hapus status untuk semua"
            className="grid size-9 place-items-center rounded-lg text-wa-text-2 transition-colors hover:bg-danger-soft hover:text-danger"
          >
            <Trash2 className="size-4" />
          </button>
        ) : null}
        {onClose ? (
          <button
            type="button"
            onClick={onClose}
            aria-label="Tutup status"
            className="grid size-9 place-items-center rounded-lg text-wa-text-2 transition-colors hover:bg-wa-hover"
          >
            <X className="size-4" />
          </button>
        ) : null}
      </header>

      {deleteError ? (
        <p className="border-b border-wa-border bg-danger-soft px-4 py-2 text-xs text-danger">
          {deleteError}
        </p>
      ) : null}

      <ConfirmDialog
        request={confirm.request}
        onClose={() => {
          confirm.close();
          setPaused(false);
        }}
      />

      <div
        className="relative flex min-h-0 flex-1 items-center justify-center p-6"
        onPointerDown={() => setPaused(true)}
        onPointerUp={() => setPaused(false)}
        onPointerLeave={() => setPaused(false)}
      >
        {/* Half the frame steps back, half steps on, which is the gesture the
            app this imitates uses and the one people try first. */}
        {index > 0 ? (
          <button
            type="button"
            onClick={() => setIndex(index - 1)}
            aria-label="Status sebelumnya"
            className="absolute inset-y-0 left-0 z-10 w-1/4"
          />
        ) : null}
        {/* Always present, including on the last update: tapping past the end
            moves to the next person rather than doing nothing. */}
        <button
          type="button"
          onClick={() => {
            if (index + 1 < group.posts.length) setIndex(index + 1);
            else finished.current?.();
          }}
          aria-label={
            index + 1 < group.posts.length ? 'Status berikutnya' : 'Status orang berikutnya'
          }
          className="absolute inset-y-0 right-0 z-10 w-1/4"
        />

        <StatusMedia post={post} onReady={() => setReady(true)} />
      </div>

      {/*
        * Only on our own status, because it is the only one the number can be
        * about: a receipt for somebody else's status goes to them, not here.
        *
        * The caveat sits beside the figure rather than in a tooltip. WhatsApp
        * gives a linked device no viewer list, so this is a count of receipts
        * that reached this server — a lower bound. A bare number here would be
        * read as "this many people watched", and on a quiet day it would read as
        * "almost nobody did", which is a different and possibly false claim.
        */}
      {group.fromMe ? (
        <footer className="flex flex-wrap items-center gap-x-2 gap-y-0.5 border-t border-wa-border bg-wa-panel px-4 py-2.5">
          <Eye className="size-4 shrink-0 text-wa-text-2" />
          <span className="text-sm text-wa-text">
            {(post.viewers ?? 0) > 0
              ? `Dilihat ${post.viewers.toLocaleString('id-ID')} orang`
              : 'Belum ada penonton yang terdeteksi'}
          </span>
          <span className="w-full text-2xs text-wa-text-2 sm:ml-auto sm:w-auto">
            Dihitung dari receipt yang sampai ke sistem. Sebagian penonton mungkin tidak terhitung.
          </span>
        </footer>
      ) : null}
    </div>
  );
}

/**
 * The three kinds a Status can be: words, a photo, or a video.
 *
 * `onReady` is what holds the timer until there is something to look at. A slide
 * that starts counting while its file is still downloading spends itself on a
 * spinner, and the person never sees the thing they opened.
 */
function StatusMedia({ post, onReady }: { post: StatusPost; onReady: () => void }) {
  const attachment = post.attachment_id
    ? {
        id: post.attachment_id,
        status: post.attachment_status ?? 'stored',
        thumbnail_b64: post.thumbnail_b64,
      }
    : null;
  // The hook reads only the id, the status and the thumbnail, so the shape is
  // narrowed here rather than widening the Attachment type for one caller.
  const { url, loading, gone, expired } = useAttachmentUrl(
    attachment as never,
    Boolean(attachment),
  );

  const text = post.caption ?? post.body ?? '';

  // Text: nothing to wait for, so the clock starts as soon as it is drawn.
  useEffect(() => {
    if (!post.attachment_id) onReady();
  }, [post.attachment_id, post.message_id, onReady]);

  // A file that will never arrive must not stall the timer either.
  useEffect(() => {
    if (gone || expired) onReady();
  }, [gone, expired, onReady]);

  if (!post.attachment_id) {
    return (
      <p className="max-w-[520px] text-center text-lg leading-relaxed whitespace-pre-wrap text-wa-text">
        {text || <span className="text-wa-text-2 italic">Status tanpa teks</span>}
      </p>
    );
  }

  return (
    <figure className="flex max-h-full max-w-full flex-col items-center gap-3">
      {loading && !url ? (
        <Loader2 className="size-6 animate-spin text-wa-text-2" />
      ) : gone || expired ? (
        <p className="text-sm text-wa-text-2">
          {attachmentUnreadableText(post.type === 'video' ? 'video' : 'image')}
        </p>
      ) : url && post.type === 'video' ? (
        <video
          src={url}
          autoPlay
          playsInline
          controls
          onLoadedData={onReady}
          className="max-h-[70vh] rounded-lg"
        />
      ) : url ? (
        // eslint-disable-next-line @next/next/no-img-element
        <img
          src={url}
          alt=""
          onLoad={onReady}
          onError={onReady}
          className="max-h-[70vh] rounded-lg object-contain"
        />
      ) : null}

      {text ? (
        <figcaption className="max-w-[520px] text-center text-sm whitespace-pre-wrap text-wa-text">
          {text}
        </figcaption>
      ) : null}
    </figure>
  );
}

/** Marks a group's unopened posts as seen. Local only. */
export async function markGroupSeen(group: StatusGroup): Promise<void> {
  await Promise.all(
    group.posts.filter((p) => !p.seen_at && !p.from_me).map((p) => markStatusSeen(p.message_id)),
  );
}

function formatWhen(iso: string): string {
  const d = new Date(iso);
  const today = new Date();
  const sameDay =
    d.getFullYear() === today.getFullYear() &&
    d.getMonth() === today.getMonth() &&
    d.getDate() === today.getDate();
  const time = d.toLocaleTimeString('id-ID', {
    hour: '2-digit',
    minute: '2-digit',
    timeZone: 'Asia/Jakarta',
  });
  return sameDay ? `Hari ini ${time}` : `${d.toLocaleDateString('id-ID')} ${time}`;
}
