'use client';

import clsx from 'clsx';
import { Plus, SmilePlus } from 'lucide-react';
import { useEffect, useRef, useState } from 'react';

import type { Reaction } from '@/lib/types';

/**
 * The six WhatsApp offers on a long-press, in its order.
 *
 * Six rather than a full picker: a reaction is a one-tap acknowledgement, and
 * anything that needs a search box is being used as a message instead.
 */
export const QUICK_REACTIONS = ['👍', '❤️', '😂', '😮', '😢', '🙏'];

/** A wider set behind the "+", for when none of the six fit. */
const MORE_REACTIONS = [
  '😀', '😅', '🤣', '😊', '😍', '🥰', '😎', '🤔',
  '😐', '🙄', '😴', '😭', '😡', '🤯', '🥳', '😱',
  '👌', '🤝', '💪', '👏', '🙌', '✌️', '🤙', '☝️',
  '🔥', '💯', '✅', '❌', '⭐', '🎉', '💡', '⚡',
];

/**
 * The picker that opens from a bubble.
 *
 * The account's current reaction is marked, and clicking it again clears it —
 * which is the only way to take a reaction back, so it has to be obvious.
 */
export function ReactionPicker({
  current,
  onPick,
  onClose,
  align,
}: {
  /** The emoji this account has already given, if any. */
  current: string | null;
  onPick: (emoji: string) => void;
  onClose: () => void;
  align: 'left' | 'right';
}) {
  const [expanded, setExpanded] = useState(false);
  const wrapRef = useRef<HTMLDivElement>(null);

  const closeRef = useRef(onClose);
  closeRef.current = onClose;

  useEffect(() => {
    const onPointerDown = (event: MouseEvent) => {
      if (!wrapRef.current?.contains(event.target as Node)) closeRef.current();
    };
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') closeRef.current();
    };
    document.addEventListener('mousedown', onPointerDown);
    document.addEventListener('keydown', onKeyDown);
    return () => {
      document.removeEventListener('mousedown', onPointerDown);
      document.removeEventListener('keydown', onKeyDown);
    };
  }, []);

  return (
    <div
      ref={wrapRef}
      role="menu"
      className={clsx(
        'absolute bottom-full z-30 mb-1 rounded-full border border-wa-border bg-wa-panel p-1 shadow-e3',
        expanded && 'rounded-2xl',
        align === 'right' ? 'right-0' : 'left-0',
      )}
    >
      <div className="flex items-center gap-0.5">
        {QUICK_REACTIONS.map((emoji) => (
          <button
            key={emoji}
            type="button"
            onClick={() => onPick(emoji)}
            aria-label={`Reaksi ${emoji}`}
            className={clsx(
              'grid size-9 place-items-center rounded-full text-[21px] leading-none transition-transform',
              'hover:scale-125 hover:bg-wa-active',
              current === emoji && 'bg-wa-accent/25',
            )}
          >
            {emoji}
          </button>
        ))}
        <button
          type="button"
          onClick={() => setExpanded((v) => !v)}
          aria-label="Emoji lainnya"
          aria-expanded={expanded}
          className="grid size-9 place-items-center rounded-full text-wa-text-2 transition-colors hover:bg-wa-active"
        >
          <Plus className={clsx('size-4 transition-transform', expanded && 'rotate-45')} />
        </button>
      </div>

      {expanded ? (
        <div className="mt-1 grid w-[268px] grid-cols-8 gap-0.5 border-t border-wa-border pt-1">
          {MORE_REACTIONS.map((emoji) => (
            <button
              key={emoji}
              type="button"
              onClick={() => onPick(emoji)}
              className={clsx(
                'grid size-8 place-items-center rounded-lg text-[19px] leading-none transition-colors hover:bg-wa-active',
                current === emoji && 'bg-wa-accent/25',
              )}
            >
              {emoji}
            </button>
          ))}
        </div>
      ) : null}
    </div>
  );
}

/**
 * The chips under a bubble.
 *
 * They overlap the bubble's bottom edge the way WhatsApp's do, so a reaction
 * reads as belonging to the message rather than following it.
 */
export function ReactionChips({
  reactions,
  mine,
  onToggle,
}: {
  reactions: Reaction[];
  /** Which side the bubble is on, so the chips sit under its near edge. */
  mine: boolean;
  onToggle: (emoji: string) => void;
}) {
  if (reactions.length === 0) return null;

  return (
    <div
      className={clsx(
        '-mt-1.5 -mb-1 flex flex-wrap gap-1',
        mine ? 'justify-end pr-1' : 'justify-start pl-1',
      )}
    >
      {reactions.map((reaction) => (
        <button
          key={reaction.emoji}
          type="button"
          onClick={() => onToggle(reaction.emoji)}
          title={reaction.names.join(', ')}
          aria-pressed={reaction.mine}
          className={clsx(
            'inline-flex items-center gap-0.5 rounded-full border px-1.5 py-[1px] text-xs leading-none transition-colors',
            reaction.mine
              ? 'border-wa-accent/50 bg-wa-accent/20 text-wa-text'
              : 'border-wa-border bg-wa-panel text-wa-text-2 hover:bg-wa-active',
          )}
        >
          <span className="text-sm leading-none">{reaction.emoji}</span>
          {reaction.count > 1 ? <span className="tabular-nums">{reaction.count}</span> : null}
        </button>
      ))}
    </div>
  );
}

/** The button on a bubble that opens the picker. */
export function ReactionButton({
  onClick,
  active,
  mine,
}: {
  onClick: () => void;
  active: boolean;
  mine: boolean;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-label="Beri reaksi"
      className={clsx(
        'absolute top-1/2 z-10 grid size-7 -translate-y-1/2 place-items-center rounded-full',
        'border border-wa-border bg-wa-panel text-wa-text-2 shadow transition-opacity',
        // Hidden until the bubble is hovered, so a thread at rest is just the
        // conversation.
        active ? 'opacity-100' : 'opacity-0 group-hover/bubble:opacity-100 focus-visible:opacity-100',
        mine ? '-left-9' : '-right-9',
      )}
    >
      <SmilePlus className="size-4" />
    </button>
  );
}
