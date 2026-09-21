'use client';

import clsx from 'clsx';
import { ImageIcon } from 'lucide-react';
import { useEffect, useMemo, useRef, useState } from 'react';
import useSWR from 'swr';

import { activeQuickRepliesPath, fetcher } from '@/lib/api';
import type { QuickReply } from '@/lib/types';

/**
 * The slash menu in the chat composer.
 *
 * Typing "/" at the start of an empty-ish draft opens a list of canned
 * replies; typing more filters it; Enter or a click drops the text into the
 * composer, where it can still be edited before it is sent. Nothing is sent
 * automatically, which matters: a canned reply is a starting point, and an
 * operator who cannot adjust it before it goes out will stop using it.
 *
 * Only fires when the slash begins the draft. Someone typing a URL or a date
 * mid-sentence is not asking for this menu, and popping one open over their
 * text would be worse than not having the feature.
 */

/** Reads the trailing "/word" the caret is completing, or null. */
export function slashQuery(draft: string): string | null {
  if (!draft.startsWith('/')) return null;
  const rest = draft.slice(1);
  // A space means they have moved on to writing a sentence that happens to
  // begin with a slash, not choosing a shortcut.
  if (/\s/.test(rest)) return null;
  return rest.toLowerCase();
}

export function QuickReplyMenu({
  query,
  applicationId,
  onPick,
  onClose,
}: {
  /** The text after the slash, or null when the menu should not be open. */
  query: string | null;
  /** Narrows to this application's replies plus the workspace-wide ones. */
  applicationId: string | null;
  onPick: (reply: QuickReply) => void;
  onClose: () => void;
}) {
  const { data } = useSWR<{ quick_replies: QuickReply[] }>(
    query === null ? null : activeQuickRepliesPath,
    fetcher,
    { revalidateOnFocus: false },
  );

  const matches = useMemo(() => {
    if (query === null) return [];
    const all = data?.quick_replies ?? [];
    return all
      .filter((q) => !q.application_id || q.application_id === applicationId)
      .filter(
        (q) =>
          query === '' ||
          q.shortcut.includes(query) ||
          q.title.toLowerCase().includes(query),
      )
      .slice(0, 8);
  }, [data, query, applicationId]);

  const [active, setActive] = useState(0);
  const listRef = useRef<HTMLUListElement>(null);

  // A shorter query is a different list, so an index into the old one means
  // nothing. Resetting avoids the selection appearing to jump as they type.
  useEffect(() => {
    setActive(0);
  }, [query]);

  // Arrow keys and Enter are captured while the menu is open, so the composer
  // below does not also act on them. Escape closes without choosing.
  useEffect(() => {
    if (query === null || matches.length === 0) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'ArrowDown') {
        e.preventDefault();
        setActive((i) => (i + 1) % matches.length);
      } else if (e.key === 'ArrowUp') {
        e.preventDefault();
        setActive((i) => (i - 1 + matches.length) % matches.length);
      } else if (e.key === 'Enter' || e.key === 'Tab') {
        e.preventDefault();
        onPick(matches[active]);
      } else if (e.key === 'Escape') {
        e.preventDefault();
        onClose();
      }
    };
    document.addEventListener('keydown', onKey, true);
    return () => document.removeEventListener('keydown', onKey, true);
  }, [query, matches, active, onPick, onClose]);

  if (query === null) return null;

  return (
    <div className="absolute bottom-full left-0 z-30 mb-2 w-[420px] max-w-[calc(100vw-2rem)] overflow-hidden rounded-card border border-wa-border bg-wa-panel shadow-e3">
      <p className="border-b border-wa-border px-3 py-2 text-2xs text-wa-text-2">
        Balas cepat{query ? ` · /${query}` : ''}
      </p>

      {matches.length === 0 ? (
        <p className="px-3 py-4 text-sm text-wa-text-2">
          {data
            ? 'Tidak ada balas cepat yang cocok. Buat di menu Balas Cepat.'
            : 'Memuat balas cepat…'}
        </p>
      ) : (
        <ul ref={listRef} className="max-h-72 overflow-y-auto py-1">
          {matches.map((q, i) => (
            <li key={q.id}>
              <button
                type="button"
                // onMouseDown, not onClick: the composer must not lose focus
                // before the text is inserted, or the caret ends up nowhere.
                onMouseDown={(e) => {
                  e.preventDefault();
                  onPick(q);
                }}
                onMouseEnter={() => setActive(i)}
                className={clsx(
                  'block w-full px-3 py-2 text-left transition-colors',
                  i === active ? 'bg-wa-active' : 'hover:bg-wa-hover',
                )}
              >
                <span className="flex flex-wrap items-center gap-2">
                  <span className="font-mono text-sm text-wa-accent">/{q.shortcut}</span>
                  {/*
                   * A picture reply does not paste, it sends. That is a real
                   * difference in what pressing Enter does, so it has to be
                   * visible before Enter is pressed.
                   */}
                  {q.media_url ? (
                    <span className="inline-flex items-center gap-1 rounded-full bg-wa-hover px-1.5 py-0.5 text-2xs text-wa-text-2">
                      <ImageIcon className="size-3" />
                      Gambar
                    </span>
                  ) : null}
                  {q.category ? (
                    <span className="text-2xs text-wa-text-2">{q.category}</span>
                  ) : null}
                  {q.application_code ? (
                    <span
                      className="rounded-full px-1.5 py-0.5 text-2xs"
                      style={{
                        backgroundColor: `${q.application_color ?? '#0f3d2e'}1F`,
                        color: q.application_color ?? '#0f3d2e',
                      }}
                    >
                      {q.application_code}
                    </span>
                  ) : null}
                </span>
                <span className="mt-0.5 line-clamp-2 block text-xs text-wa-text-2">{q.body}</span>
              </button>
            </li>
          ))}
        </ul>
      )}

      <p className="border-t border-wa-border px-3 py-1.5 text-2xs text-wa-text-2">
        ↑↓ pilih · Enter sisipkan · Esc tutup. Teks masih bisa diubah sebelum dikirim.
      </p>
    </div>
  );
}
