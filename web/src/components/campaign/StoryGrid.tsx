'use client';

import clsx from 'clsx';
import { Clock, Eye, MoreVertical, Smartphone, Users } from 'lucide-react';
import Link from 'next/link';
import { useEffect, useRef, useState } from 'react';

import { CampaignStatusBadge } from '@/components/campaign/shared';
import type { Campaign } from '@/lib/types';

/**
 * The Story list, as cards rather than as a table.
 *
 * A Story is a picture. A table row can show a name, a count and a date, which
 * is the right shape for a broadcast where the interesting facts are numbers —
 * but for a Story the first question is always "which one is this", and the only
 * answer that works is the image itself. Fifty rows of text would be fifty
 * lookups; fifty thumbnails are recognised at a glance.
 *
 * Each card carries the four things needed to tell one Story from another: what
 * it looks like, what it says, which numbers posted it, and when.
 */
export function StoryGrid({
  campaigns,
  isStory = true,
}: {
  campaigns: Campaign[];
  /**
   * Which list these cards belong to.
   *
   * The grid is offered on both screens now, and every link on a card is built
   * from this. Without it a broadcast card sent the reader to /story/<id> — the
   * right id at the wrong page, which is the worst kind of wrong link because
   * it loads something that looks almost right.
   */
  isStory?: boolean;
}) {
  return (
    <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-4">
      {campaigns.map((c) => (
        <StoryCard key={c.id} campaign={c} isStory={isStory} />
      ))}
    </div>
  );
}

function StoryCard({ campaign: c, isStory }: { campaign: Campaign; isStory: boolean }) {
  const home = isStory ? '/story' : '/broadcast';
  const href = `${home}/${c.id}`;
  const caption = (c.caption ?? c.message_template ?? c.body ?? '').trim();
  const url = (c.media_url ?? '').trim();

  // What a card of each kind is actually about. A Story is "which of my numbers
  // is it live on"; a broadcast is "how many people got it".
  const done = c.success_count + c.failed_count;
  const percent = c.target_count > 0 ? Math.round((done / c.target_count) * 100) : 0;

  return (
    <article className="flex flex-col overflow-hidden rounded-card border border-hairline bg-surface-raised shadow-e1 transition-shadow hover:shadow-e2">
      <div className="relative">
        <StoryThumbnail url={url} kind={c.media_kind} caption={caption} className="h-[168px]" />
        {/*
         * The badge sits on its own opaque plate, not straight on the picture.
         * Every status colour is a translucent tint meant for the page's own
         * pale ground; over a photo or over WhatsApp's dark teal it composites
         * into whatever happens to be underneath, and "Terposting" — dark green
         * text on a ten-percent green tint — came out as dark on dark.
         *
         * The plate is the card's own surface rather than literal white, so it
         * stays legible in dark mode instead of glaring.
         */}
        <span className="absolute top-2.5 left-2.5 inline-flex rounded-full bg-surface-raised shadow-e1">
          <CampaignStatusBadge status={c.status} isStory={isStory} />
        </span>
      </div>

      <div className="flex min-w-0 flex-1 flex-col px-4 pt-3 pb-3.5">
        {/* The name first, because it is what somebody gave it and what they
            will look for. The caption is already legible in the picture above
            on a text Story, and on a picture Story it is not the thing that
            identifies it. */}
        <div className="flex items-start justify-between gap-2">
          <p className="min-w-0 flex-1 truncate text-sm font-semibold text-ink">
            {c.name || <span className="text-ink-muted italic">(tanpa nama)</span>}
          </p>
          <CardMenu
            href={href}
            duplicateHref={`${home}/baru?dari=${c.id}`}
            label={`Aksi untuk ${c.name}`}
          />
        </div>

        {/*
         * A Story names the numbers it published from — "3 nomor" does not
         * answer the question anybody is asking, which is whether it went out
         * from the right phone. A broadcast is measured in people instead, so
         * the card carries the count and how far it got.
         */}
        {isStory ? (
          c.device_names.length > 0 ? (
            <p className="mt-2 flex items-start gap-1.5 text-2xs text-ink-muted">
              <Smartphone className="mt-px size-3 shrink-0" />
              <span className="line-clamp-1 min-w-0">{c.device_names.join(', ')}</span>
            </p>
          ) : null
        ) : (
          <p className="mt-2 flex items-center gap-1.5 text-2xs text-ink-muted">
            <Users className="size-3 shrink-0" />
            <span className="nums">{c.target_count.toLocaleString('id-ID')} penerima</span>
            {c.target_count > 0 ? <span className="nums">· {percent}% terkirim</span> : null}
          </p>
        )}

        <p className="mt-1.5 flex items-center gap-1.5 text-2xs text-ink-muted">
          <Clock className="size-3 shrink-0" />
          {formatWhen(c.scheduled_at ?? c.executed_at ?? c.created_at)}
        </p>

        <Link
          href={href}
          className="mt-3 inline-flex items-center justify-center gap-1.5 rounded-control border border-hairline px-3 py-1.5 text-xs font-medium text-ink-soft transition-colors hover:bg-surface-sunken"
        >
          <Eye className="size-3.5" />
          Lihat Detail
        </Link>
      </div>
    </article>
  );
}

/** The card's own menu: what can be done without opening it. */
function CardMenu({
  href,
  duplicateHref,
  label,
}: {
  href: string;
  duplicateHref: string;
  label: string;
}) {
  const [open, setOpen] = useState(false);
  const box = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    function onDown(e: MouseEvent) {
      if (!box.current?.contains(e.target as Node)) setOpen(false);
    }
    document.addEventListener('mousedown', onDown);
    return () => document.removeEventListener('mousedown', onDown);
  }, [open]);

  return (
    <div ref={box} className="relative shrink-0">
      <button
        type="button"
        aria-label={label}
        aria-expanded={open}
        onClick={() => setOpen((v) => !v)}
        className="rounded-control p-1 text-ink-muted transition-colors hover:bg-surface-sunken hover:text-ink"
      >
        <MoreVertical className="size-4" />
      </button>

      {open ? (
        <div className="absolute right-0 z-30 mt-1 w-40 rounded-card border border-hairline bg-surface-raised p-1 shadow-e2">
          <Link
            href={href}
            className="block rounded-control px-2.5 py-2 text-xs text-ink-soft transition-colors hover:bg-surface-sunken"
          >
            Lihat detail
          </Link>
          {/* Duplicating opens the composer filled in. Nothing is written until
              it is saved, which is why this is a link rather than an action. */}
          <Link
            href={duplicateHref}
            className="block rounded-control px-2.5 py-2 text-xs text-ink-soft transition-colors hover:bg-surface-sunken"
          >
            Duplikat
          </Link>
        </div>
      ) : null}
    </div>
  );
}

/**
 * What the Story looks like.
 *
 * A text Story is drawn on WhatsApp's own dark teal, the same colour the backend
 * publishes it with, so the card is a small version of the real thing rather
 * than a label describing it.
 */
export function StoryThumbnail({
  url,
  kind,
  caption,
  className,
  contain = false,
}: {
  url: string;
  kind: string | null;
  caption: string;
  className?: string;
  /** The detail page shows the whole picture; a card crops it to stay uniform. */
  contain?: boolean;
}) {
  const fit = contain ? 'object-contain' : 'object-cover';
  if (url && kind === 'video') {
    return (
      // preload="metadata" draws the first frame without pulling the whole file
      // down for a thumbnail.
      <video
        src={url}
        muted
        playsInline
        preload="metadata"
        controls={contain}
        className={clsx('w-full bg-[#0b141a]', fit, className)}
      />
    );
  }
  if (url && kind !== 'document') {
    return (
      // eslint-disable-next-line @next/next/no-img-element
      <img src={url} alt="" className={clsx('w-full bg-[#0b141a]', fit, className)} />
    );
  }
  return (
    <div className={clsx('flex w-full items-center bg-[#075E54] px-4 py-6', className)}>
      <p
        className={clsx(
          'w-full text-sm leading-snug font-medium whitespace-pre-wrap text-white',
          contain ? 'text-center' : 'line-clamp-5',
        )}
      >
        {caption || <span className="text-white/60 italic">Story teks</span>}
      </p>
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
