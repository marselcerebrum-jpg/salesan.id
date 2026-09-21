'use client';

import clsx from 'clsx';
import type { ComponentType, ReactNode } from 'react';

/**
 * A group of related figures in one card.
 *
 * The shape this replaces was a grid of forty equal tiles, each holding one
 * number. That reads as a wall: everything the same size means nothing is
 * important, and the eye has no idea where a question is answered.
 *
 * Here one card is one subject. The leading figure is large, the supporting
 * ones are rows, and a hairline separates them. So "how is SLA doing" is
 * answered by glancing at one card, and the detail is there when the answer
 * prompts a second question.
 *
 * Restraint is the whole design, and the first version of it got restraint
 * wrong. It gave each of the six cards its own hue — sky, violet, amber, rose,
 * teal — which is six colour families on one screen, and one of them the
 * generic purple. Worse, they were raw palette classes rather than theme
 * tokens, so none of them changed when the lights went off: in dark mode every
 * card's headline figure was a dark colour on a dark surface.
 *
 * Colour is not what tells these cards apart. The icon and the position do
 * that, and they do it in both themes. So there are two tones now, and they
 * mean something: `brand` marks the card a reader acts on, `neutral` marks the
 * ones that report. Both resolve through tokens, so both follow the theme.
 */

export type Accent = 'brand' | 'neutral';

const ACCENTS: Record<Accent, { icon: string; value: string; bar: string }> = {
  brand: {
    icon: 'bg-brand-600/12 text-brand-700',
    value: 'text-brand-700',
    bar: 'bg-brand-600',
  },
  neutral: {
    icon: 'bg-surface-sunken text-ink-soft',
    value: 'text-ink',
    bar: 'bg-ink-soft',
  },
};

export function MetricCardGroup({
  icon: Icon,
  title,
  description,
  accent = 'neutral',
  className,
  action,
  children,
  footer,
  loading,
}: {
  icon: ComponentType<{ className?: string }>;
  title: string;
  /** One short line under the title. Longer explanations belong in a tooltip. */
  description?: string;
  accent?: Accent;
  /** Grid placement, so one card can be wider than the rest. */
  className?: string;
  /**
   * A control in the card's top-right, for something that changes what the
   * card shows rather than acting on it. A period or metric picker belongs
   * here; a button that opens a list belongs in the footer.
   */
  action?: ReactNode;
  children: ReactNode;
  footer?: ReactNode;
  loading?: boolean;
}) {
  const a = ACCENTS[accent];
  const lead = accent === 'brand';

  return (
    <section
      className={clsx(
        'flex flex-col rounded-card border',
        // Elevation, not just colour, separates the card a reader acts on from
        // the ones that report. e2 against e1 is a single visible step: it
        // reads as "this one is nearer" before anybody has read a word, which
        // is the whole point of DESIGN.md asking for Material elevation on a
        // screen this dense.
        //
        // The tint and the firmer border do the rest, and neither adds a
        // colour to the palette: both are the brand token at low opacity, so
        // both follow the theme.
        lead
          ? 'border-brand-600/25 bg-brand-600/[0.04] shadow-e2'
          : 'border-hairline bg-surface-raised shadow-e1',
        className,
      )}
    >
      <header className="flex items-start gap-3 px-5 pt-5 pb-4">
        <span
          className={clsx(
            'grid shrink-0 place-items-center rounded-control',
            lead ? 'size-11' : 'size-9',
            a.icon,
          )}
        >
          <Icon className={lead ? 'size-[22px]' : 'size-[18px]'} />
        </span>
        <div className="min-w-0 flex-1 pt-0.5">
          <h3
            className={clsx(
              'font-semibold tracking-[-0.01em] text-ink',
              lead ? 'text-lg' : 'text-base',
            )}
          >
            {title}
          </h3>
          {description ? (
            <p className="mt-1 text-xs text-ink-muted">{description}</p>
          ) : null}
        </div>
        {action ? <div className="shrink-0">{action}</div> : null}
      </header>

      {loading ? (
        <div className="space-y-3 px-5 pb-5">
          <div className="h-9 w-28 animate-pulse rounded-md bg-surface-sunken" />
          <div className="h-4 w-full animate-pulse rounded bg-surface-sunken" />
          <div className="h-4 w-3/5 animate-pulse rounded bg-surface-sunken" />
        </div>
      ) : (
        <div className="flex-1 px-5 pb-2">{children}</div>
      )}

      {footer && !loading ? (
        <div
          className={clsx(
            'border-t px-5 py-3',
            lead ? 'border-brand-600/20' : 'border-hairline',
          )}
        >
          {footer}
        </div>
      ) : null}
    </section>
  );
}

