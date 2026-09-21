'use client';

import { ImageOff, RefreshCw, VideoOff } from 'lucide-react';
import { useState } from 'react';

import type { MediaKind } from '@/components/campaign/MessageStep';
import {
  Formatted,
  fileNameOf,
  renderTemplate,
  stripVars,
} from '@/components/campaign/MessagePreview';
import type { CustomVariable } from '@/lib/types';
import { initials } from '@/lib/format';

/**
 * How the Story will look on somebody's phone.
 *
 * A separate shape from MessagePreview rather than a variant of it, because a
 * Story is not a chat bubble and showing one as the other teaches the wrong
 * thing. A bubble is narrow, left-aligned and grows downward; a Story is a full
 * portrait frame where the picture is the whole screen and the words sit over
 * it. A caption that reads well in a bubble can be unreadable across a photo,
 * and that is exactly the mistake this is here to catch before it is published.
 *
 * The text itself is rendered by the same functions MessagePreview uses, so the
 * two can never disagree about what a template produces.
 */
export function StoryPreview({
  body,
  mediaKind,
  mediaURL,
  senderName,
  variables,
  values,
}: {
  body: string;
  mediaKind: MediaKind | null;
  mediaURL: string;
  senderName: string | null;
  variables: CustomVariable[];
  values: Record<string, string>;
}) {
  const [seed, setSeed] = useState(0);
  // Keyed by URL so correcting a link clears the previous failure instead of
  // leaving "tidak dapat dimuat" under a link that is now fine.
  const [brokenURL, setBrokenURL] = useState<string | null>(null);

  const rendered = renderTemplate(body, seed, variables, values);
  const hasSpintax = /\{[^{}]*\|[^{}]*\}/.test(stripVars(body));
  const url = mediaURL.trim();
  const broken = Boolean(url) && brokenURL === url;
  // A document cannot be a Story: WhatsApp has no file status. The composer
  // still offers the kind, so the frame says so rather than drawing an empty
  // screen the author would read as "fine".
  const isDocument = mediaKind === 'document';
  const hasMedia = !isDocument && Boolean(url) && (mediaKind === 'image' || mediaKind === 'video');

  return (
    <div className="overflow-hidden rounded-card border border-hairline bg-surface-raised shadow-e1">
      {/* The same 20 / 16 / 12 as StepCard's header, so this column starts on the
          same line as the form beside it. */}
      <div className="flex min-h-7 items-center justify-between gap-3 px-5 pt-4 pb-3">
        <p className="text-base font-semibold text-ink">Pratinjau Story</p>
        {hasSpintax ? (
          <button
            type="button"
            onClick={() => setSeed((s) => s + 1)}
            className="inline-flex items-center gap-1.5 rounded-lg px-2 py-1 text-2xs text-ink-muted transition-colors hover:bg-surface-sunken hover:text-ink-soft"
          >
            <RefreshCw className="size-3" />
            Acak ulang
          </button>
        ) : null}
      </div>

      <div className="px-5">
        {/*
         * 9:16, capped at 300px wide. A Story is only ever seen in portrait, and
         * a frame stretched to the panel's full width would show a line length
         * and a crop nobody actually gets.
         */}
        <div className="relative mx-auto aspect-[9/16] w-full max-w-[300px] overflow-hidden rounded-xl bg-[#0b141a]">
          {/* Full width, because a Story that is still being written is one
              update, not several. */}
          <div className="absolute inset-x-0 top-0 z-20 flex gap-1 px-2.5 pt-2.5">
            <span className="h-[3px] flex-1 rounded-full bg-white/70" />
          </div>

          <div className="absolute inset-x-0 top-0 z-20 flex items-center gap-2 px-3 pt-6">
            <span className="grid size-7 shrink-0 place-items-center rounded-full bg-white/20 text-[10px] font-medium text-white">
              {initials(senderName ?? 'Story')}
            </span>
            <span className="min-w-0 flex-1">
              <span className="block truncate text-xs font-medium text-white">
                {senderName ?? 'Nomor pengirim'}
              </span>
              <span className="block text-[10px] text-white/70">Baru saja</span>
            </span>
          </div>

          {/* --- the frame itself --- */}
          {isDocument ? (
            <Note
              icon={ImageOff}
              label="Dokumen tidak bisa jadi Story"
              detail="WhatsApp hanya menerima teks, gambar, atau video di status."
            />
          ) : broken ? (
            <Note
              icon={mediaKind === 'video' ? VideoOff : ImageOff}
              label={mediaKind === 'video' ? 'Video tidak dapat dimuat' : 'Gambar tidak dapat dimuat'}
              detail={fileNameOf(url)}
            />
          ) : mediaKind === 'image' && url ? (
            // object-contain, not cover. A Story is letterboxed by WhatsApp when
            // the picture is not 9:16, and cropping it here would hide exactly
            // the part the author needs to see is going to be cut.
            // eslint-disable-next-line @next/next/no-img-element
            <img
              src={url}
              alt=""
              onError={() => setBrokenURL(url)}
              className="absolute inset-0 size-full object-contain"
            />
          ) : mediaKind === 'video' && url ? (
            <video
              src={url}
              controls
              muted
              playsInline
              preload="metadata"
              onError={() => setBrokenURL(url)}
              className="absolute inset-0 size-full object-contain"
            />
          ) : (
            /*
             * Text-only status, on WhatsApp's own dark teal.
             *
             * The exact colour the backend publishes with — defaultStoryBackground
             * in wa/campaign.go is 0xFF075E54 — so the preview and the published
             * status are the same screen, not two guesses at it.
             */
            <div className="absolute inset-0 grid place-items-center bg-[#075E54] px-6">
              {rendered ? (
                <p className="max-h-full overflow-hidden text-center text-lg leading-snug font-medium break-words whitespace-pre-wrap text-white">
                  <Formatted text={rendered} />
                </p>
              ) : (
                <p className="text-center text-sm text-white/60 italic">Isi story masih kosong</p>
              )}
            </div>
          )}

          {/* The caption sits over the picture, which is where WhatsApp puts it
              and the reason a long one is a problem worth seeing early. */}
          {hasMedia && rendered ? (
            <div className="absolute inset-x-0 bottom-0 z-10 bg-gradient-to-t from-black/75 to-transparent px-3.5 pt-10 pb-4">
              <p className="max-h-[38%] overflow-hidden text-sm leading-snug break-words whitespace-pre-wrap text-white">
                <Formatted text={rendered} />
              </p>
            </div>
          ) : null}
        </div>
      </div>

      <p className="px-5 pt-3 pb-5 text-2xs leading-snug text-ink-muted">
        {senderName ? `Diterbitkan dari ${senderName}. ` : ''}
        Siapa yang bisa melihat ditentukan pengaturan privasi status di nomor itu sendiri, bukan di
        sini. Story hilang otomatis setelah 24 jam.
        {hasSpintax ? ' Spintax dipilih sekali untuk satu story, jadi ini salah satu kemungkinan.' : ''}
      </p>
    </div>
  );
}

function Note({
  icon: Icon,
  label,
  detail,
}: {
  icon: typeof ImageOff;
  label: string;
  detail?: string;
}) {
  return (
    <div className="absolute inset-0 grid place-items-center px-6">
      <span className="text-center">
        <Icon className="mx-auto size-7 text-white/50" />
        <span className="mt-2 block text-xs text-white/80">{label}</span>
        {detail ? (
          <span className="mt-1 block truncate text-[10px] text-white/50">{detail}</span>
        ) : null}
      </span>
    </div>
  );
}
