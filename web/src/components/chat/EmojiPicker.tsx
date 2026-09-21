'use client';

import clsx from 'clsx';
import { Clock, Smile } from 'lucide-react';
import { useEffect, useMemo, useRef, useState } from 'react';

/**
 * Emoji picker for the composer.
 *
 * A hand-picked set rather than the full Unicode table, and no library: the
 * complete set is a megabyte of data and a search index for a feature whose job
 * is to insert a smiley into a sales reply. These are the ones that actually
 * get used, grouped so they can be found by eye.
 */
const GROUPS: Array<{ id: string; label: string; emoji: string[] }> = [
  {
    id: 'smileys',
    label: 'Wajah',
    emoji: [
      '😀', '😃', '😄', '😁', '😆', '😅', '🤣', '😂', '🙂', '🙃',
      '😉', '😊', '😇', '🥰', '😍', '😘', '😗', '😙', '😚', '😋',
      '😛', '😝', '😜', '🤪', '🤨', '🧐', '🤓', '😎', '🥳', '😏',
      '😒', '😞', '😔', '😟', '😕', '🙁', '😣', '😖', '😫', '😩',
      '🥺', '😢', '😭', '😤', '😠', '😡', '🤬', '🤯', '😳', '🥵',
      '😱', '😨', '😰', '😥', '😓', '🤗', '🤔', '🤭', '🤫', '🤥',
      '😶', '😐', '😑', '😬', '🙄', '😯', '😴', '🤤', '😪', '😵',
      '🤐', '🥴', '🤢', '🤮', '🤧', '😷', '🤒', '🤕', '🤑', '🤠',
    ],
  },
  {
    id: 'gestures',
    label: 'Tangan',
    emoji: [
      '👍', '👎', '👌', '🤌', '✌️', '🤞', '🤟', '🤘', '🤙', '👈',
      '👉', '👆', '👇', '☝️', '✋', '🤚', '🖐️', '🖖', '👋', '🤝',
      '🙏', '✍️', '💪', '🦾', '👏', '🙌', '👐', '🤲', '🫶', '✊',
      '👊', '🤛', '🤜',
    ],
  },
  {
    id: 'hearts',
    label: 'Hati',
    emoji: [
      '❤️', '🧡', '💛', '💚', '💙', '💜', '🖤', '🤍', '🤎', '💔',
      '❣️', '💕', '💞', '💓', '💗', '💖', '💘', '💝', '💟', '♥️',
    ],
  },
  {
    id: 'business',
    label: 'Bisnis',
    emoji: [
      '✅', '❌', '⚠️', '❗', '❓', '💯', '🔥', '⭐', '🌟', '✨',
      '🎉', '🎊', '🎁', '📌', '📍', '📎', '🔗', '📅', '⏰', '⏳',
      '💰', '💵', '💳', '🧾', '📊', '📈', '📉', '📝', '📄', '📁',
      '📦', '🚚', '🛒', '🏷️', '💡', '🔔', '📢', '☎️', '📱', '💬',
    ],
  },
  {
    id: 'objects',
    label: 'Lainnya',
    emoji: [
      '🍽️', '☕', '🍵', '🥤', '🍰', '🎂', '🍕', '🍔', '🏠', '🏢',
      '🚗', '✈️', '🌍', '☀️', '🌙', '⛅', '🌧️', '❄️', '🌈', '🐱',
      '🐶', '🌸', '🌹', '🍀', '⚽', '🎵', '🎬', '📷', '🔒', '🔑',
    ],
  },
];

const RECENT_KEY = 'salesan.emoji.recent';
const MAX_RECENT = 24;

/** Reads the recent list, tolerating a browser that refuses storage. */
function loadRecent(): string[] {
  try {
    const raw = localStorage.getItem(RECENT_KEY);
    const parsed: unknown = raw ? JSON.parse(raw) : null;
    return Array.isArray(parsed) ? parsed.filter((e): e is string => typeof e === 'string') : [];
  } catch {
    return [];
  }
}

export function EmojiPicker({
  disabled,
  onPick,
}: {
  disabled: boolean;
  /** Inserts the emoji at the caret; the composer owns where that is. */
  onPick: (emoji: string) => void;
}) {
  const [open, setOpen] = useState(false);
  const [tab, setTab] = useState<string>('smileys');
  const [recent, setRecent] = useState<string[]>([]);
  const wrapRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    setRecent(loadRecent());

    const onPointerDown = (event: MouseEvent) => {
      if (!wrapRef.current?.contains(event.target as Node)) setOpen(false);
    };
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') setOpen(false);
    };
    document.addEventListener('mousedown', onPointerDown);
    document.addEventListener('keydown', onKeyDown);
    return () => {
      document.removeEventListener('mousedown', onPointerDown);
      document.removeEventListener('keydown', onKeyDown);
    };
  }, [open]);

  const tabs = useMemo(
    () => (recent.length > 0 ? [{ id: 'recent', label: 'Baru' }, ...GROUPS] : GROUPS),
    [recent.length],
  );

  const shown = tab === 'recent' ? recent : (GROUPS.find((g) => g.id === tab)?.emoji ?? []);

  function pick(emoji: string) {
    onPick(emoji);
    // Most-recent-first, deduplicated, so the ones actually used surface.
    const next = [emoji, ...recent.filter((e) => e !== emoji)].slice(0, MAX_RECENT);
    setRecent(next);
    try {
      localStorage.setItem(RECENT_KEY, JSON.stringify(next));
    } catch {
      // A browser blocking storage costs the recent list, nothing more.
    }
  }

  return (
    <div ref={wrapRef} className="relative">
      <button
        type="button"
        disabled={disabled}
        onClick={() => setOpen((v) => !v)}
        aria-label="Emoji"
        aria-expanded={open}
        className={clsx(
          'grid size-[42px] shrink-0 place-items-center rounded-full transition-colors',
          'hover:bg-wa-active disabled:opacity-40 disabled:hover:bg-transparent',
          open ? 'text-wa-accent' : 'text-wa-text-2',
        )}
      >
        <Smile className="size-[22px]" />
      </button>

      {open ? (
        <div className="absolute bottom-[52px] left-0 z-20 w-[340px] overflow-hidden rounded-xl border border-wa-border bg-wa-panel-2 shadow-e3">
          <div className="scrollbar-slim flex gap-1 overflow-x-auto border-b border-wa-border px-2 py-1.5">
            {tabs.map((t) => (
              <button
                key={t.id}
                type="button"
                onClick={() => setTab(t.id)}
                className={clsx(
                  'shrink-0 rounded-lg px-2.5 py-1 text-xs font-medium transition-colors',
                  tab === t.id ? 'bg-wa-accent text-white' : 'text-wa-text-2 hover:bg-wa-active',
                )}
              >
                {t.id === 'recent' ? <Clock className="size-3.5" /> : t.label}
              </button>
            ))}
          </div>

          <div className="scrollbar-slim grid max-h-[240px] grid-cols-8 gap-0.5 overflow-y-auto p-2">
            {shown.map((emoji, index) => (
              <button
                key={`${emoji}-${index}`}
                type="button"
                onClick={() => pick(emoji)}
                className="grid size-9 place-items-center rounded-lg text-[22px] leading-none transition-colors hover:bg-wa-active"
              >
                {emoji}
              </button>
            ))}
          </div>
        </div>
      ) : null}
    </div>
  );
}
