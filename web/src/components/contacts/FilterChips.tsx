'use client';

import clsx from 'clsx';
import { ChevronLeft, ChevronRight } from 'lucide-react';
import { useCallback, useEffect, useRef, useState } from 'react';

import type { ContactFacet } from '@/lib/types';

/**
 * One horizontally scrolling row of filter chips.
 *
 * A workspace can hold twenty applications and thirty numbers, which is more
 * than fits and far too many to wrap into a wall of buttons above the table.
 * Scrolling keeps the row one line tall at every width; the arrows appear only
 * when there is something past the edge, so a short row looks like a short row
 * rather than a scroller with nothing to scroll.
 *
 * Every chip carries its own count, including the zeroes. A brand with no
 * contacts is a fact worth seeing, and a chip that vanishes when it empties
 * looks like the filter broke.
 */
export function FilterChips({
  label,
  facets,
  total,
  totalLabel,
  totalHint,
  selected,
  onSelect,
  variant = 'compact',
  empty,
  noun = 'kontak',
}: {
  label: string;
  facets: ContactFacet[];
  /** The count for the "everything" chip that leads the row. */
  total: number;
  totalLabel: string;
  totalHint?: string;
  /** Null means the "everything" chip is active. */
  selected: string | null;
  onSelect: (id: string | null) => void;
  /**
   * "compact" puts the count beside the name, which suits a row of brands.
   * "detail" folds the count into the second line beside the phone number,
   * because a WhatsApp number needs both and a third column would not fit.
   */
  variant?: 'compact' | 'detail';
  /** Shown instead of the row when there is nothing to choose between. */
  empty?: string;
  /**
   * What is being counted, for the "detail" variant's second line. This row is
   * used by the address book and by the group directory, and a chip that says
   * "7 kontak" on the group screen is simply wrong.
   */
  noun?: string;
}) {
  const scrollRef = useRef<HTMLDivElement>(null);
  const [edges, setEdges] = useState({ start: false, end: false });

  const measure = useCallback(() => {
    const el = scrollRef.current;
    if (!el) return;
    setEdges({
      start: el.scrollLeft > 4,
      // A pixel of slack: fractional widths otherwise leave the arrow showing
      // on a row that is already fully scrolled.
      end: el.scrollLeft + el.clientWidth < el.scrollWidth - 4,
    });
  }, []);

  useEffect(() => {
    measure();
    const el = scrollRef.current;
    if (!el) return;
    const observer = new ResizeObserver(measure);
    observer.observe(el);
    return () => observer.disconnect();
  }, [measure, facets.length]);

  function nudge(direction: 1 | -1) {
    scrollRef.current?.scrollBy({ left: direction * 320, behavior: 'smooth' });
  }

  return (
    <section className="mt-4">
      <p className="mb-1.5 text-xs font-medium text-ink-soft">{label}</p>
      {facets.length === 0 && empty ? (
        <p className="rounded-control border border-dashed border-hairline-strong px-3 py-2.5 text-sm text-ink-muted">
          {empty}
        </p>
      ) : (
        <div className="relative">
          <div
            ref={scrollRef}
            onScroll={measure}
            className="scrollbar-slim flex gap-2 overflow-x-auto pb-2"
          >
            <Chip
              variant={variant}
              noun={noun}
              active={selected === null}
              title={totalLabel}
              hint={totalHint}
              count={total}
              onClick={() => onSelect(null)}
            />
            {facets.map((facet) => (
              <Chip
                key={facet.id ?? facet.label}
                variant={variant}
                noun={noun}
                active={selected === facet.id}
                title={facet.label}
                hint={facet.hint}
                color={facet.color}
                count={facet.count}
                onClick={() => onSelect(facet.id)}
              />
            ))}
          </div>

          {edges.start ? <Arrow side="start" onClick={() => nudge(-1)} /> : null}
          {edges.end ? <Arrow side="end" onClick={() => nudge(1)} /> : null}
        </div>
      )}
    </section>
  );
}

function Chip({
  variant,
  noun,
  active,
  title,
  hint,
  color,
  count,
  onClick,
}: {
  variant: 'compact' | 'detail';
  noun: string;
  active: boolean;
  title: string;
  hint?: string;
  color?: string | null;
  count: number;
  onClick: () => void;
}) {
  const reading = `${count.toLocaleString('id-ID')} ${noun}`;

  // A second line that repeats the first is not a hint, it is noise. Most
  // applications here are named after their own code, and an account is often
  // named after its own number, so both rows hit this.
  const sameAsTitle =
    (hint ?? '').trim().toLowerCase() === title.trim().toLowerCase();
  const detail = hint && !sameAsTitle ? `${hint} · ${reading}` : reading;
  const showHint = Boolean(hint) && !sameAsTitle;

  return (
    <button
      type="button"
      onClick={onClick}
      aria-pressed={active}
      className={clsx(
        'flex shrink-0 items-center gap-2 rounded-control border px-3 py-2 text-left transition-colors',
        active
          ? 'border-brand-600/45 bg-brand-600/10'
          : 'border-hairline bg-surface-raised hover:bg-surface-sunken',
      )}
    >
      {color ? (
        <span
          aria-hidden
          className="size-2 shrink-0 rounded-full"
          style={{ backgroundColor: color }}
        />
      ) : null}
      <span className="min-w-0">
        <span
          className={clsx(
            'block truncate text-sm font-medium',
            active ? 'text-brand-700' : 'text-ink',
          )}
        >
          {title}
        </span>
        {variant === 'detail' ? (
          <span className="nums block truncate text-2xs text-ink-muted">{detail}</span>
        ) : showHint ? (
          <span className="block truncate text-2xs text-ink-muted">{hint}</span>
        ) : null}
      </span>
      {variant === 'compact' ? (
        <span
          className={clsx(
            'nums shrink-0 text-xs',
            active ? 'font-semibold text-brand-700' : 'text-ink-muted',
          )}
        >
          {count.toLocaleString('id-ID')}
        </span>
      ) : null}
    </button>
  );
}

function Arrow({ side, onClick }: { side: 'start' | 'end'; onClick: () => void }) {
  const Icon = side === 'start' ? ChevronLeft : ChevronRight;
  return (
    <button
      type="button"
      onClick={onClick}
      aria-label={side === 'start' ? 'Geser ke kiri' : 'Geser ke kanan'}
      className={clsx(
        'absolute top-1/2 z-10 grid size-7 -translate-y-1/2 place-items-center rounded-full',
        'border border-hairline bg-surface-raised text-ink-soft shadow-e2 transition-colors hover:bg-surface-sunken',
        side === 'start' ? '-left-1' : '-right-1',
      )}
    >
      <Icon className="size-4" />
    </button>
  );
}
