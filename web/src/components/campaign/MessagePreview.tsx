'use client';

import { CheckCheck, FileText, ImageOff, RefreshCw, VideoOff } from 'lucide-react';
import { useEffect, useState } from 'react';

import type { MediaKind } from '@/components/campaign/MessageStep';
import type { CustomVariable } from '@/lib/types';

/**
 * How the message will look in the recipient's chat.
 *
 * An approximation, and labelled as one. Spintax is chosen again for every
 * recipient and variables are filled from their own row, so this shows one of
 * the messages that could go out, not the one that will. The count that can be
 * trusted comes from "Tinjau", which is rendered by the same server code that
 * does the sending.
 *
 * It is still worth having. WhatsApp's bubble is narrow, its bold and italic
 * markers are easy to get wrong, and a caption that reads fine in a textarea
 * can arrive as a wall. Seeing the shape is what catches that before four
 * hundred people do.
 */
export function MessagePreview({
  body,
  mediaKind,
  mediaURL,
  documentName,
  senderName,
  variables,
  values,
}: {
  body: string;
  mediaKind: MediaKind | null;
  mediaURL: string;
  documentName: string | null;
  senderName: string | null;
  variables: CustomVariable[];
  values: Record<string, string>;
}) {
  // A seed, so "Acak ulang" gives a different spin of the same template rather
  // than re-rendering the same one.
  const [seed, setSeed] = useState(0);
  // Keyed by URL so pasting a corrected link clears the previous failure rather
  // than leaving "tidak dapat dimuat" under a link that is now fine.
  const [brokenURL, setBrokenURL] = useState<string | null>(null);

  /*
   * The clock is filled in after mount, never during render.
   *
   * `new Date().toLocaleTimeString(...)` is not a pure function of the props:
   * it changes with the minute, and Node and the browser do not always format
   * the same locale identically - one can give "13.57" where the other gives
   * "1:57 PM", depending on which ICU data the runtime was built with. Either
   * difference makes the server's HTML disagree with the client's first render,
   * and React treats that as a hydration failure, which takes the whole page to
   * the error boundary.
   *
   * Rendering nothing on the server and the real time on the client is the fix:
   * both agree, because the server never committed to a value.
   */
  const [time, setTime] = useState('');
  useEffect(() => setTime(clock()), []);

  const rendered = renderTemplate(body, seed, variables, values);
  const hasSpintax = /\{[^{}]*\|[^{}]*\}/.test(stripVars(body));
  const url = mediaURL.trim();

  return (
    <div className="overflow-hidden rounded-card border border-hairline bg-surface-raised shadow-e1">
      {/* The same 16 / 28 / 12 as StepCard's header, so the two columns start on
          one line rather than a dozen pixels apart. */}
      <div className="flex min-h-7 items-center justify-between gap-3 px-5 pt-4 pb-3">
        <p className="text-base font-semibold text-ink">Pratinjau</p>
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

      {/* WhatsApp's own colours, from the tokens the chat screen already uses, so
          the preview and the inbox cannot drift apart. */}
      {/* Inset from the panel's edge by the same 20px the header uses, so the
          chat surface lines up with the title above it. */}
      <div className="mx-5 min-h-[240px] overflow-hidden rounded-lg bg-wa-chat px-4 py-4">
        {/*
         * 360px: a WhatsApp Web bubble, which is the width most people reading
         * this will actually see. Still capped rather than filling the panel:
         * the bubble is the thing being judged, and one stretched to 480px would
         * show a line length no recipient ever gets.
         */}
        <div className="ml-auto max-w-[360px]">
          <div className="rounded-lg rounded-tr-sm bg-wa-out px-2 pt-2 pb-1.5 shadow-e1">
            {url && brokenURL === url ? (
              <Placeholder
                icon={mediaKind === 'video' ? VideoOff : ImageOff}
                label={
                  mediaKind === 'video'
                    ? 'Video tidak dapat dimuat'
                    : 'Gambar tidak dapat dimuat'
                }
                detail={fileNameOf(url)}
              />
            ) : mediaKind === 'image' && url ? (
              // eslint-disable-next-line @next/next/no-img-element
              <img
                src={url}
                alt=""
                onError={() => setBrokenURL(url)}
                className="mb-1.5 max-h-[320px] w-full rounded object-cover"
              />
            ) : mediaKind === 'video' && url ? (
              // A real player, not a placeholder with a play icon. The point of
              // pasting a video link here is to find out whether it plays, and a
              // decorative thumbnail answers the one question being asked with a
              // picture of an answer.
              //
              // preload="metadata" so a 60 MB file is not pulled down in full
              // just to draw a first frame.
              <video
                src={url}
                controls
                muted
                playsInline
                preload="metadata"
                onError={() => setBrokenURL(url)}
                className="mb-1.5 max-h-[320px] w-full rounded bg-black/10 object-contain"
              />
            ) : null}

            {mediaKind === 'document' && documentName ? (
              <div className="mb-1.5 flex items-center gap-2 rounded bg-black/5 px-2.5 py-2">
                <FileText className="size-5 shrink-0 text-wa-text-2" />
                <span className="min-w-0 flex-1 truncate text-xs text-wa-text">
                  {documentName}
                </span>
              </div>
            ) : null}

            {rendered ? (
              <p className="text-sm leading-snug whitespace-pre-wrap text-wa-text">
                <Formatted text={rendered} />
              </p>
            ) : (
              <p className="text-sm text-wa-text-2 italic">Isi pesan masih kosong</p>
            )}

            <span className="mt-0.5 flex h-4 items-center justify-end gap-1">
              <span className="text-[10px] text-wa-text-2">{time}</span>
              <CheckCheck className="size-3.5 text-wa-tick-read" />
            </span>
          </div>
        </div>
      </div>

      <p className="px-5 pt-3 pb-5 text-2xs leading-snug text-ink-muted">
        {senderName ? `Dikirim dari ${senderName}. ` : ''}
        {hasSpintax
          ? 'Spintax dipilih ulang untuk tiap penerima, jadi ini salah satu kemungkinan, bukan pesan yang pasti dikirim.'
          : 'Variabel diisi contoh; nilai sebenarnya diambil dari data tiap penerima.'}
      </p>
    </div>
  );
}

function Placeholder({
  icon: Icon,
  label,
  detail,
}: {
  icon: typeof ImageOff;
  label: string;
  detail?: string;
}) {
  return (
    <div className="mb-1.5 grid h-[180px] place-items-center rounded bg-black/8">
      <span className="px-3 text-center">
        <Icon className="mx-auto size-6 text-wa-text-2" />
        <span className="mt-1 block text-2xs text-wa-text-2">{label}</span>
        {detail ? (
          <span className="mt-0.5 block truncate text-[10px] text-wa-text-2">{detail}</span>
        ) : null}
      </span>
    </div>
  );
}

/**
 * WhatsApp's own text markers, so bold and italic show as bold and italic.
 *
 * Worth rendering rather than leaving as asterisks: *PROMO* in a textarea and
 * **PROMO** in a bubble are different mistakes, and only one of them is visible
 * before it is sent.
 */
export function Formatted({ text }: { text: string }) {
  const parts = text.split(/(\*[^*\n]+\*|_[^_\n]+_|~[^~\n]+~|```[^`]+```)/g);
  return (
    <>
      {parts.map((part, i) => {
        if (/^\*[^*\n]+\*$/.test(part)) {
          return (
            <strong key={i} className="font-semibold">
              {part.slice(1, -1)}
            </strong>
          );
        }
        if (/^_[^_\n]+_$/.test(part)) {
          return <em key={i}>{part.slice(1, -1)}</em>;
        }
        if (/^~[^~\n]+~$/.test(part)) {
          return (
            <span key={i} className="line-through">
              {part.slice(1, -1)}
            </span>
          );
        }
        if (/^```[^`]+```$/.test(part)) {
          return (
            <code key={i} className="font-mono text-xs">
              {part.slice(3, -3)}
            </code>
          );
        }
        return <span key={i}>{part}</span>;
      })}
    </>
  );
}

/* --- rendering ------------------------------------------------------------- */

/** Variables out of the way before spintax is read, the same order the server uses. */
export function stripVars(s: string) {
  return s.replace(/<<[^<>]*>>/g, '').replace(/\{\{[^}]*\}\}/g, '');
}

/**
 * Exported so the Story preview shows the same text this one does.
 *
 * Spintax and variables are resolved in an order that matters — see the comment
 * inside — and two previews that disagreed about what a template produces would
 * be worse than one preview.
 */
export function renderTemplate(
  body: string,
  seed: number,
  variables: CustomVariable[],
  values: Record<string, string>,
): string {
  // Spin first, then fill. The other order lets a value containing a brace or a
  // pipe alter the message, which is exactly the bug this order prevents on
  // the server, so the preview must not do it differently.
  const spun = spin(body, mulberry(seed + 1));
  return fill(spun, variables, values);
}

/** Picks one branch of each {a|b} group, stepping over variables. */
function spin(template: string, rand: () => number): string {
  let out = template;
  // Innermost groups first, so nesting resolves outward.
  const group = /\{([^{}]*\|[^{}]*)\}/;
  for (let guard = 0; guard < 200; guard++) {
    const m = group.exec(out);
    if (!m) break;
    // A {{variable}} is not a spintax group; leave it alone.
    if (out[m.index - 1] === '{' || out[m.index + m[0].length] === '}') break;
    const options = m[1].split('|');
    const pick = options[Math.floor(rand() * options.length)] ?? '';
    out = out.slice(0, m.index) + pick + out.slice(m.index + m[0].length);
  }
  return out;
}

/** Fills <<key>> and <<key|fallback>>, and the older {{key}} spelling. */
function fill(
  text: string,
  variables: CustomVariable[],
  values: Record<string, string>,
): string {
  const sample = (key: string, fallback: string | null): string => {
    const set = values[key]?.trim();
    if (set) return set;
    const known = variables.find((v) => v.key === key);
    if (known?.default_value?.trim()) return known.default_value.trim();
    if (fallback) return fallback;
    // Named rather than blanked: a hole in a sentence is what the review screen
    // refuses to send, and the preview should show the same problem.
    return `<<${key}>>`;
  };

  const one = (_m: string, key: string, rest: string | undefined) =>
    sample(key.toLowerCase(), rest ? rest.slice(1).trim() : null);

  return text
    .replace(/<<\s*([A-Za-z0-9_]+)\s*(\|[^<>]*)?>>/g, one)
    .replace(/\{\{\s*([A-Za-z0-9_]+)\s*(\|[^}]*)?\}\}/g, one);
}

/** A small seeded generator, so re-rendering does not reshuffle the text. */
function mulberry(seed: number) {
  let a = seed >>> 0;
  return () => {
    a = (a + 0x6d2b79f5) >>> 0;
    let t = a;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

export function fileNameOf(url: string): string {
  try {
    return decodeURIComponent(new URL(url).pathname.split('/').pop() ?? url);
  } catch {
    return url;
  }
}

function clock(): string {
  return new Date().toLocaleTimeString('id-ID', {
    hour: '2-digit',
    minute: '2-digit',
    timeZone: 'Asia/Jakarta',
  });
}
