'use client';

import clsx from 'clsx';
import {
  BadgeCheck,
  BarChart3,
  Bell,
  BellOff,
  Eye,
  FileText,
  Image as ImageIcon,
  Loader2,
  Megaphone,
  Mic,
  Paperclip,
  Plus,
  RefreshCw,
  Search,
  Send,
  Smile,
  UserMinus,
  X,
} from 'lucide-react';
import { useEffect, useRef, useState } from 'react';
import useSWR from 'swr';

import {
  ApiError,
  canPostToChannel,
  channelMediaBlob,
  fetcher,
  newsletterAction,
  newsletterPosts,
  newslettersPath,
  postMediaToChannel,
  postToChannel,
  syncNewsletters,
  type Newsletter,
  type NewsletterPost,
} from '@/lib/api';
import { initials } from '@/lib/format';

import { PollBubble } from './PollBubble';
import { PollComposer } from './PollComposer';

/**
 * Channels.
 *
 * A directory and a reader, not a chat. There is nothing to reply to, so the
 * screen offers only what a subscriber actually has: open it, follow one, leave
 * one, and silence one.
 *
 * Posts are fetched from WhatsApp when a channel is opened and are not stored.
 * They belong to somebody else's publication; a copy in our database would be
 * storage and a duty of care bought for nothing.
 */

export function ChannelList({
  accountId,
  selected,
  onSelect,
}: {
  accountId: string;
  selected: string | null;
  onSelect: (n: Newsletter) => void;
}) {
  const { data, isLoading, mutate } = useSWR<{ newsletters: Newsletter[] }>(
    newslettersPath(accountId),
    fetcher,
  );
  const [search, setSearch] = useState('');
  const [busy, setBusy] = useState(false);
  const [adding, setAdding] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const list = data?.newsletters ?? [];
  const needle = search.trim().toLowerCase();
  const shown = list.filter((n) => !needle || n.name.toLowerCase().includes(needle));

  async function sync() {
    setBusy(true);
    setError(null);
    try {
      await mutate(await syncNewsletters(accountId), { revalidate: false });
    } catch (e) {
      setError(e instanceof ApiError || e instanceof Error ? e.message : 'Gagal menyinkronkan.');
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="mx-3 mb-2 flex items-center gap-2">
        <label className="flex min-w-0 flex-1 items-center gap-2 rounded-lg bg-wa-panel-2 px-3 py-2">
          <Search className="size-4 shrink-0 text-wa-text-2" />
          <input
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder="Cari saluran"
            aria-label="Cari saluran"
            className="w-full bg-transparent text-sm text-wa-text outline-none placeholder:text-wa-text-2"
          />
        </label>
        <button
          type="button"
          onClick={() => void sync()}
          disabled={busy}
          aria-label="Sinkronkan saluran"
          title="Tarik ulang daftar saluran dari WhatsApp"
          className="grid size-9 shrink-0 place-items-center rounded-lg text-wa-text-2 transition-colors hover:bg-wa-hover disabled:opacity-50"
        >
          <RefreshCw className={clsx('size-4', busy && 'animate-spin')} />
        </button>
        <button
          type="button"
          onClick={() => setAdding(true)}
          aria-label="Ikuti saluran"
          className="grid size-9 shrink-0 place-items-center rounded-lg text-wa-text-2 transition-colors hover:bg-wa-hover"
        >
          <Plus className="size-4" />
        </button>
      </div>

      {error ? <p className="mx-3 mb-2 text-xs text-danger">{error}</p> : null}

      <div className="min-h-0 flex-1 overflow-y-auto pb-4">
        {isLoading && list.length === 0 ? (
          <p className="flex items-center gap-2 px-4 py-6 text-sm text-wa-text-2">
            <Loader2 className="size-4 animate-spin" />
            Memuat saluran…
          </p>
        ) : shown.length === 0 ? (
          <div className="px-4 py-8 text-center">
            <Megaphone className="mx-auto size-7 text-wa-text-2" />
            <p className="mt-2 text-sm text-wa-text">Belum ada saluran</p>
            <p className="mt-1 text-xs text-wa-text-2">
              Tekan Sinkron untuk menarik saluran yang sudah diikuti nomor ini, atau tanda tambah
              untuk mengikuti yang baru.
            </p>
          </div>
        ) : (
          shown.map((n) => (
            <button
              key={n.jid}
              type="button"
              onClick={() => onSelect(n)}
              className={clsx(
                'flex w-full items-center gap-3 px-4 py-2.5 text-left transition-colors',
                selected === n.jid ? 'bg-wa-active' : 'hover:bg-wa-hover',
              )}
            >
              <span className="grid size-10 shrink-0 place-items-center overflow-hidden rounded-full bg-wa-panel-2">
                {n.picture_url ? (
                  // eslint-disable-next-line @next/next/no-img-element
                  <img src={n.picture_url} alt="" className="size-full object-cover" />
                ) : (
                  <Megaphone className="size-4 text-wa-text-2" />
                )}
              </span>
              <span className="min-w-0 flex-1">
                <span className="flex items-center gap-1">
                  <span className="truncate text-sm text-wa-text">{n.name || n.jid}</span>
                  {n.verified ? (
                    <BadgeCheck className="size-3.5 shrink-0 text-wa-tick-read" />
                  ) : null}
                  {n.muted ? <BellOff className="size-3 shrink-0 text-wa-text-2" /> : null}
                </span>
                <span className="block truncate text-xs text-wa-text-2">
                  {n.subscriber_count !== null
                    ? `${n.subscriber_count.toLocaleString('id-ID')} pengikut`
                    : 'Jumlah pengikut tidak diketahui'}
                  {n.viewer_role === 'owner' || n.viewer_role === 'admin'
                    ? ` · ${n.viewer_role}`
                    : ''}
                </span>
              </span>
            </button>
          ))
        )}
      </div>

      {adding ? (
        <FollowDialog
          accountId={accountId}
          onClose={() => setAdding(false)}
          onDone={(next) => {
            void mutate({ newsletters: next }, { revalidate: false });
            setAdding(false);
          }}
        />
      ) : null}
    </div>
  );
}

/* --- viewer ---------------------------------------------------------------- */

export function ChannelViewer({
  accountId,
  channel,
  onChanged,
}: {
  accountId: string;
  channel: Newsletter;
  onChanged: (next: Newsletter[]) => void;
}) {
  const { data, isLoading, error, mutate } = useSWR<{ posts: NewsletterPost[] }>(
    ['newsletter-posts', accountId, channel.jid],
    () => newsletterPosts(accountId, channel.jid),
    {
      revalidateOnFocus: false,
      /*
       * Re-read while the channel is open. Posts, views, reactions and votes
       * all live on WhatsApp, and the phone changes them without telling this
       * panel — so what the phone shows reaches the web within this interval,
       * without a reload. Only while open: a closed panel asks for nothing.
       */
      refreshInterval: 15_000,
    },
  );
  const [busy, setBusy] = useState<string | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);

  async function act(action: 'unfollow' | 'mute', muted?: boolean) {
    setBusy(action);
    setActionError(null);
    try {
      const res = await newsletterAction(accountId, { ref: channel.jid, action, muted });
      if (res.newsletters) onChanged(res.newsletters);
    } catch (e) {
      setActionError(e instanceof ApiError || e instanceof Error ? e.message : 'Gagal.');
    } finally {
      setBusy(null);
    }
  }

  const posts = data?.posts ?? [];

  return (
    <div className="flex h-full min-h-0 flex-col bg-wa-chat">
      <header className="flex items-start gap-3 border-b border-wa-border bg-wa-panel px-4 py-3">
        <span className="grid size-11 shrink-0 place-items-center overflow-hidden rounded-full bg-wa-panel-2">
          {channel.picture_url ? (
            // eslint-disable-next-line @next/next/no-img-element
            <img src={channel.picture_url} alt="" className="size-full object-cover" />
          ) : (
            <span className="text-sm text-wa-text-2">{initials(channel.name)}</span>
          )}
        </span>
        <div className="min-w-0 flex-1">
          <p className="flex items-center gap-1 text-sm font-medium text-wa-text">
            <span className="truncate">{channel.name || channel.jid}</span>
            {channel.verified ? <BadgeCheck className="size-4 shrink-0 text-wa-tick-read" /> : null}
          </p>
          <p className="text-xs text-wa-text-2">
            {channel.subscriber_count !== null
              ? `${channel.subscriber_count.toLocaleString('id-ID')} pengikut`
              : 'Jumlah pengikut tidak diketahui'}
            {' · '}
            {channel.viewer_role}
          </p>
          {channel.description ? (
            <p className="mt-1 text-xs whitespace-pre-wrap text-wa-text-2">{channel.description}</p>
          ) : null}
        </div>

        <div className="flex shrink-0 items-center gap-1">
          <button
            type="button"
            onClick={() => void act('mute', !channel.muted)}
            disabled={busy !== null}
            title={channel.muted ? 'Bunyikan notifikasi' : 'Bisukan notifikasi'}
            aria-label={channel.muted ? 'Bunyikan' : 'Bisukan'}
            className="grid size-9 place-items-center rounded-lg text-wa-text-2 transition-colors hover:bg-wa-hover disabled:opacity-50"
          >
            {busy === 'mute' ? (
              <Loader2 className="size-4 animate-spin" />
            ) : channel.muted ? (
              <BellOff className="size-4" />
            ) : (
              <Bell className="size-4" />
            )}
          </button>
          <button
            type="button"
            onClick={() => void act('unfollow')}
            disabled={busy !== null}
            title="Berhenti mengikuti"
            aria-label="Berhenti mengikuti"
            className="grid size-9 place-items-center rounded-lg text-wa-text-2 transition-colors hover:bg-danger-soft hover:text-danger disabled:opacity-50"
          >
            {busy === 'unfollow' ? (
              <Loader2 className="size-4 animate-spin" />
            ) : (
              <UserMinus className="size-4" />
            )}
          </button>
        </div>
      </header>

      {actionError ? (
        <p className="border-b border-wa-border bg-danger-soft px-4 py-2 text-xs text-danger">
          {actionError}
        </p>
      ) : null}

      <div className="min-h-0 flex-1 space-y-2 overflow-y-auto px-4 py-4">
        {isLoading && posts.length === 0 ? (
          <p className="flex items-center gap-2 text-sm text-wa-text-2">
            <Loader2 className="size-4 animate-spin" />
            Memuat isi saluran…
          </p>
        ) : error ? (
          <p className="text-sm text-danger">
            {error instanceof Error ? error.message : 'Gagal memuat isi saluran.'}
          </p>
        ) : posts.length === 0 ? (
          <p className="text-sm text-wa-text-2">Saluran ini belum punya postingan.</p>
        ) : (
          posts.map((p) => (
            <ChannelPostCard
              key={p.server_id}
              accountId={accountId}
              jid={channel.jid}
              post={p}
              // Views are only ever reported to the channel's own admins, so
              // for a follower a zero would be a claim nobody made.
              showViews={canPostToChannel(channel)}
            />
          ))
        )}
      </div>

      {/*
       * A composer only where there is something to compose with.
       *
       * WhatsApp gives a follower no way to post, so a disabled input on a
       * channel we merely follow would be a control that can never work. The
       * line below takes its place and says why.
       */}
      {canPostToChannel(channel) ? (
        <ChannelComposer
          accountId={accountId}
          channel={channel}
          onPosted={(posts) => {
            if (posts) void mutate({ posts }, { revalidate: false });
            else void mutate();
          }}
        />
      ) : (
        <p className="border-t border-wa-border bg-wa-panel px-4 py-2 text-2xs text-wa-text-2">
          Nomor ini hanya mengikuti saluran ini, jadi tidak bisa memposting. Isinya diambil langsung
          dari WhatsApp dan tidak disimpan di server kami.
        </p>
      )}
    </div>
  );
}

/* --- post ------------------------------------------------------------------ */

/**
 * One channel post, drawn the way it looks on WhatsApp: the picture, the video,
 * the file, the poll — not "(image)".
 *
 * The file itself is only fetched when the card is on screen, through the
 * authenticated endpoint, and kept as an object URL for as long as the card
 * lives. The thumbnail WhatsApp embeds in the post is shown until then, so the
 * list is recognisable before a single file has loaded.
 */
function ChannelPostCard({
  accountId,
  jid,
  post: p,
  showViews,
}: {
  accountId: string;
  jid: string;
  post: NewsletterPost;
  showViews: boolean;
}) {
  const reactions = Object.entries(p.reactions ?? {}).filter(([, n]) => n > 0);
  const reactionTotal = reactions.reduce((sum, [, n]) => sum + n, 0);

  return (
    <article className="max-w-[520px] overflow-hidden rounded-lg bg-wa-in shadow-e1">
      {p.has_media ? <ChannelMedia accountId={accountId} jid={jid} post={p} /> : null}

      <div className="px-3 py-2.5">
        {p.kind === 'poll' ? (
          // The chat's own poll, so a poll looks the same wherever it is. Not
          // votable here and without counts: see PollBubble's `tallies`.
          <PollBubble
            canVote={false}
            tallies={Array.isArray(p.poll_votes)}
            poll={{
              message_id: p.server_id,
              name: p.text,
              selectable_count: p.poll_selectable_count ?? 0,
              options: (p.poll_options ?? []).map((name, index) => ({
                index,
                name,
                votes: p.poll_votes?.[index] ?? 0,
              })),
              // A channel reports counts per option, not how many people voted.
              total_voters: 0,
              selected_idx: [],
            }}
          />
        ) : p.text ? (
          <p className="text-sm whitespace-pre-wrap text-wa-text">{p.text}</p>
        ) : !p.has_media ? (
          <p className="text-sm text-wa-text-2 italic">({p.kind})</p>
        ) : null}

        <p className="mt-1.5 flex flex-wrap items-center gap-x-3 gap-y-1 text-2xs text-wa-text-2">
          <span>{formatWhen(p.posted_at)}</span>
          {showViews ? (
            <span className="inline-flex items-center gap-1" title="Jumlah yang melihat">
              <Eye className="size-3.5" />
              <span className="nums">{p.view_count.toLocaleString('id-ID')}</span> dilihat
            </span>
          ) : null}
        </p>

        {reactions.length > 0 ? (
          <div className="mt-2 flex flex-wrap items-center gap-1.5">
            {reactions.map(([emoji, n]) => (
              <span
                key={emoji}
                className="inline-flex items-center gap-1 rounded-full bg-wa-panel-2 px-2 py-0.5 text-xs text-wa-text"
              >
                {emoji}
                <span className="nums text-2xs text-wa-text-2">{n.toLocaleString('id-ID')}</span>
              </span>
            ))}
            <span className="text-2xs text-wa-text-2">
              {reactionTotal.toLocaleString('id-ID')} reaksi
            </span>
          </div>
        ) : null}
      </div>
    </article>
  );
}

function ChannelMedia({
  accountId,
  jid,
  post: p,
}: {
  accountId: string;
  jid: string;
  post: NewsletterPost;
}) {
  const [url, setUrl] = useState<string | null>(null);
  const [failed, setFailed] = useState<string | null>(null);

  // Fetched once per card. Documents are not fetched until asked for: a PDF
  // shown as a filename does not need its bytes on the way in.
  const eager = p.kind === 'image' || p.kind === 'sticker' || p.kind === 'video' || p.kind === 'audio';

  useEffect(() => {
    if (!eager) return;
    let alive = true;
    let objectUrl: string | null = null;
    channelMediaBlob(accountId, jid, p.server_id)
      .then((blob) => {
        if (!alive) return;
        objectUrl = URL.createObjectURL(blob);
        setUrl(objectUrl);
      })
      .catch((e) => {
        if (alive) setFailed(e instanceof Error ? e.message : 'Berkas gagal dimuat.');
      });
    return () => {
      alive = false;
      if (objectUrl) URL.revokeObjectURL(objectUrl);
    };
  }, [accountId, jid, p.server_id, eager]);

  async function openDocument() {
    try {
      const blob = await channelMediaBlob(accountId, jid, p.server_id);
      const objectUrl = URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = objectUrl;
      a.download = p.file_name || 'berkas';
      a.click();
      setTimeout(() => URL.revokeObjectURL(objectUrl), 10_000);
    } catch (e) {
      setFailed(e instanceof Error ? e.message : 'Berkas gagal diunduh.');
    }
  }

  const thumb = p.thumbnail ? `data:image/jpeg;base64,${p.thumbnail}` : null;

  if (p.kind === 'document') {
    return (
      <button
        type="button"
        onClick={() => void openDocument()}
        className="flex w-full items-center gap-3 border-b border-wa-border bg-wa-panel-2/60 px-3 py-3 text-left transition-colors hover:bg-wa-hover"
      >
        <FileText className="size-8 shrink-0 text-wa-text-2" />
        <span className="min-w-0 flex-1">
          <span className="block truncate text-sm text-wa-text">{p.file_name || 'Dokumen'}</span>
          <span className="block text-2xs text-wa-text-2">
            {failed ?? 'Klik untuk mengunduh'}
          </span>
        </span>
      </button>
    );
  }

  if (failed) {
    return (
      <div className="bg-wa-panel-2/60 px-3 py-6 text-center text-xs text-wa-text-2">{failed}</div>
    );
  }

  if (p.kind === 'audio') {
    return url ? (
      <audio src={url} controls className="w-full px-2 pt-2" />
    ) : (
      <MediaLoading />
    );
  }

  if (p.kind === 'video') {
    return url ? (
      <video src={url} controls playsInline className="max-h-[360px] w-full bg-black" />
    ) : thumb ? (
      // eslint-disable-next-line @next/next/no-img-element
      <img src={thumb} alt="" className="max-h-[360px] w-full object-cover blur-[1px]" />
    ) : (
      <MediaLoading />
    );
  }

  // Image or sticker. A sticker keeps its transparency and its small size.
  const src = url ?? thumb;
  if (!src) return <MediaLoading />;
  return (
    // eslint-disable-next-line @next/next/no-img-element
    <img
      src={src}
      alt=""
      className={
        p.kind === 'sticker'
          ? 'm-2 size-36 object-contain'
          : clsx('max-h-[360px] w-full object-cover', !url && 'blur-[1px]')
      }
    />
  );
}

function MediaLoading() {
  return (
    <div className="flex items-center justify-center gap-2 bg-wa-panel-2/60 py-8 text-xs text-wa-text-2">
      <Loader2 className="size-4 animate-spin" />
      Memuat berkas…
    </div>
  );
}

/* --- composer -------------------------------------------------------------- */

/**
 * Posting to a channel we run.
 *
 * Everything WhatsApp lets a channel carry: a sentence, a picture, a video, an
 * audio clip, a document, a sticker, a poll. There is no recipient and no reply,
 * so this is a publish box rather than a chat composer: no read receipts, no
 * typing indicator, nothing pretending a person is at a phone.
 *
 * Nothing is stored on our side. The post goes to WhatsApp and the panel then
 * shows the channel's own list, which is the same list anybody following it
 * sees.
 */
function ChannelComposer({
  accountId,
  channel,
  onPosted,
}: {
  accountId: string;
  channel: Newsletter;
  onPosted: (posts?: NewsletterPost[]) => void;
}) {
  const [text, setText] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [menuOpen, setMenuOpen] = useState(false);
  const [pollOpen, setPollOpen] = useState(false);

  const fileInput = useRef<HTMLInputElement>(null);
  // Which button opened the picker. Only "sticker" changes what the server does
  // with the bytes; the rest let it classify the file itself.
  const pickedFor = useRef<'media' | 'document' | 'sticker'>('media');

  async function run<T>(work: () => Promise<T>, after: (result: T) => void) {
    setBusy(true);
    setError(null);
    try {
      after(await work());
    } catch (e) {
      setError(e instanceof ApiError || e instanceof Error ? e.message : 'Gagal memposting.');
    } finally {
      setBusy(false);
    }
  }

  function sendText() {
    const body = text.trim();
    if (body === '' || busy) return;
    void run(
      () => postToChannel(accountId, { jid: channel.jid, kind: 'text', text: body }),
      (res) => {
        setText('');
        onPosted(res.posts);
      },
    );
  }

  function pick(mode: 'media' | 'document' | 'sticker', accept: string) {
    pickedFor.current = mode;
    setMenuOpen(false);
    const input = fileInput.current;
    if (!input) return;
    input.accept = accept;
    input.value = ''; // so choosing the same file twice still fires onChange
    input.click();
  }

  function sendFile(file: File) {
    const caption = text.trim();
    void run(
      () =>
        postMediaToChannel(accountId, channel.jid, file, {
          caption: pickedFor.current === 'sticker' ? '' : caption,
          kind: pickedFor.current === 'sticker' ? 'sticker' : undefined,
          asDocument: pickedFor.current === 'document',
        }),
      (res) => {
        if (pickedFor.current !== 'sticker') setText('');
        onPosted(res.posts);
      },
    );
  }

  return (
    <div className="border-t border-wa-border bg-wa-panel">
      {error ? <p className="px-4 pt-2 text-xs text-danger">{error}</p> : null}

      <div className="flex items-end gap-2 px-3 py-2.5">
        <div className="relative shrink-0">
          <button
            type="button"
            onClick={() => setMenuOpen((v) => !v)}
            disabled={busy}
            aria-label="Lampirkan"
            aria-expanded={menuOpen}
            className="grid size-10 place-items-center rounded-lg text-wa-text-2 transition-colors hover:bg-wa-hover disabled:opacity-50"
          >
            <Paperclip className="size-5" />
          </button>

          {menuOpen ? (
            <div className="absolute bottom-12 left-0 z-30 w-52 rounded-lg border border-wa-border bg-wa-panel-2 p-1 shadow-e3">
              <AttachItem
                icon={ImageIcon}
                label="Foto atau video"
                onClick={() => pick('media', 'image/*,video/*')}
              />
              <AttachItem
                icon={Mic}
                label="Audio"
                onClick={() => pick('media', 'audio/*')}
              />
              <AttachItem
                icon={FileText}
                label="Dokumen"
                onClick={() => pick('document', '')}
              />
              <AttachItem
                icon={Smile}
                label="Stiker (WebP)"
                onClick={() => pick('sticker', 'image/webp')}
              />
              <AttachItem
                icon={BarChart3}
                label="Polling"
                onClick={() => {
                  setMenuOpen(false);
                  setPollOpen(true);
                }}
              />
            </div>
          ) : null}
        </div>

        <input
          ref={fileInput}
          type="file"
          hidden
          onChange={(e) => {
            const file = e.target.files?.[0];
            if (file) sendFile(file);
          }}
        />

        <textarea
          value={text}
          onChange={(e) => setText(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter' && !e.shiftKey) {
              e.preventDefault();
              sendText();
            }
          }}
          rows={1}
          placeholder="Tulis postingan untuk saluran ini"
          aria-label="Isi postingan"
          className="max-h-32 min-h-10 flex-1 resize-none rounded-lg bg-wa-panel-2 px-3 py-2.5 text-sm text-wa-text outline-none placeholder:text-wa-text-2"
        />

        <button
          type="button"
          onClick={sendText}
          disabled={busy || text.trim() === ''}
          aria-label="Posting"
          className="grid size-10 shrink-0 place-items-center rounded-lg bg-wa-accent text-white transition-opacity disabled:opacity-40"
        >
          {busy ? <Loader2 className="size-5 animate-spin" /> : <Send className="size-5" />}
        </button>
      </div>

      {/* Said where it is relevant: this panel reads from WhatsApp and keeps
          nothing, and posting does not change that. */}
      <p className="px-4 pb-2 text-2xs text-wa-text-2">
        Postingan dikirim langsung ke WhatsApp dan tidak disimpan di server kami.
      </p>

      {/* The chat's poll builder, unchanged. It shows its own error and keeps
          the form open when WhatsApp refuses, so nothing typed is lost. */}
      <PollComposer
        open={pollOpen}
        onClose={() => setPollOpen(false)}
        onSubmit={async ({ name, options, allowMultiple }) => {
          const res = await postToChannel(accountId, {
            jid: channel.jid,
            kind: 'poll',
            text: name,
            options,
            selectable_count: allowMultiple ? 0 : 1,
          });
          onPosted(res.posts);
        }}
      />
    </div>
  );
}

function AttachItem({
  icon: Icon,
  label,
  onClick,
}: {
  icon: typeof ImageIcon;
  label: string;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className="flex w-full items-center gap-2.5 rounded-md px-2.5 py-2 text-left text-sm text-wa-text transition-colors hover:bg-wa-hover"
    >
      <Icon className="size-4 shrink-0 text-wa-text-2" />
      {label}
    </button>
  );
}

/* --- follow ---------------------------------------------------------------- */

function FollowDialog({
  accountId,
  onClose,
  onDone,
}: {
  accountId: string;
  onClose: () => void;
  onDone: (next: Newsletter[]) => void;
}) {
  const [ref, setRef] = useState('');
  const [found, setFound] = useState<Newsletter | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function run(action: 'preview' | 'follow') {
    setBusy(true);
    setError(null);
    try {
      const res = await newsletterAction(accountId, { ref: ref.trim(), action });
      if (action === 'preview') {
        setFound(res.newsletter ?? null);
      } else if (res.newsletters) {
        onDone(res.newsletters);
      }
    } catch (e) {
      setError(e instanceof ApiError || e instanceof Error ? e.message : 'Gagal.');
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="fixed inset-0 z-50 grid place-items-center bg-ink/40 px-4">
      <div className="w-full max-w-[440px] rounded-card border border-hairline bg-surface p-5 shadow-e4">
        <div className="flex items-start justify-between gap-3">
          <h2 className="text-base font-semibold text-ink">Ikuti saluran</h2>
          <button
            type="button"
            onClick={onClose}
            aria-label="Tutup"
            className="rounded-lg p-1 text-ink-muted transition-colors hover:bg-surface-sunken"
          >
            <X className="size-4" />
          </button>
        </div>

        <p className="mt-1 text-xs text-ink-muted">
          Tempel tautan undangan saluran, atau alamatnya langsung.
        </p>
        <input
          value={ref}
          onChange={(e) => {
            setRef(e.target.value);
            setFound(null);
          }}
          placeholder="https://whatsapp.com/channel/…"
          aria-label="Tautan atau alamat saluran"
          className="mt-2 h-10 w-full rounded-control border border-hairline bg-surface-raised px-3 text-sm text-ink outline-none focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-brand-600"
        />

        {found ? (
          <div className="mt-3 rounded-lg border border-hairline bg-surface-raised px-3 py-2.5">
            <p className="text-sm font-medium text-ink">{found.name || found.jid}</p>
            <p className="text-2xs text-ink-muted">
              {found.subscriber_count !== null
                ? `${found.subscriber_count.toLocaleString('id-ID')} pengikut`
                : 'Jumlah pengikut tidak diketahui'}
            </p>
            {found.description ? (
              <p className="mt-1 text-2xs text-ink-muted">{found.description}</p>
            ) : null}
          </div>
        ) : null}

        {error ? <p className="mt-2 text-xs text-danger">{error}</p> : null}

        <div className="mt-4 flex justify-end gap-2">
          <button
            type="button"
            onClick={() => void run('preview')}
            disabled={busy || ref.trim() === ''}
            className="inline-flex h-9 items-center gap-1.5 rounded-control border border-hairline bg-surface-raised px-3.5 text-sm font-medium text-ink-soft transition-colors hover:bg-surface-sunken disabled:opacity-50"
          >
            {busy ? <Loader2 className="size-3.5 animate-spin" /> : null}
            Lihat dulu
          </button>
          <button
            type="button"
            onClick={() => void run('follow')}
            disabled={busy || ref.trim() === ''}
            className="inline-flex h-9 items-center gap-1.5 rounded-control bg-brand-800 px-3.5 text-sm font-medium text-white transition-colors hover:bg-brand-900 disabled:opacity-50"
          >
            {busy ? <Loader2 className="size-3.5 animate-spin" /> : null}
            Ikuti
          </button>
        </div>
      </div>
    </div>
  );
}

function formatWhen(iso: string): string {
  return new Date(iso).toLocaleString('id-ID', {
    day: 'numeric',
    month: 'short',
    hour: '2-digit',
    minute: '2-digit',
    timeZone: 'Asia/Jakarta',
  });
}
