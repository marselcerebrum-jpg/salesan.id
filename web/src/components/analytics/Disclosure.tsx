'use client';

import clsx from 'clsx';
import { ChevronDown, type LucideIcon } from 'lucide-react';
import { useState, type ReactNode } from 'react';

/**
 * A section of the Performa page that opens when it is asked for.
 *
 * The page carries four large tables and a card grid. Rendered open, the ones
 * below the fold cost a scroll to get past and a query to fill, and most visits
 * are looking at the cards at the top and nothing else. Closed by default, each
 * one states what it holds and how much, so the decision to open it can be made
 * without opening it.
 *
 * Contents are not rendered until the section is opened, so a closed table is
 * not a table at all: no rows, no column headers, no work.
 */
export function Disclosure({
  icon: Icon,
  title,
  description,
  summary,
  actions,
  defaultOpen = false,
  children,
}: {
  icon: LucideIcon;
  title: string;
  description?: string;
  /** A count or a range, shown on the closed row so it need not be opened. */
  summary?: ReactNode;
  /** Controls that belong to the section, revealed with it. */
  actions?: ReactNode;
  defaultOpen?: boolean;
  children: ReactNode;
}) {
  const [open, setOpen] = useState(defaultOpen);

  return (
    <section className="rounded-card border border-hairline bg-surface-raised shadow-e1">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
        className="flex w-full items-center gap-3 rounded-card px-5 py-3.5 text-left focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-brand-600"
      >
        {/* Chevron on the left, where a disclosure belongs: it marks the row as
            something that opens before the eye reaches the content. */}
        <ChevronDown
          className={clsx(
            'size-4 shrink-0 text-ink-muted transition-transform',
            open ? 'rotate-0' : '-rotate-90',
          )}
        />
        <span className="grid size-9 shrink-0 place-items-center rounded-control bg-surface-sunken text-ink-soft">
          <Icon className="size-[18px]" />
        </span>
        <span className="min-w-0 flex-1">
          <span className="block text-base font-semibold text-ink">{title}</span>
          {description ? (
            <span className="block text-xs text-ink-muted">{description}</span>
          ) : null}
        </span>
        {summary ? (
          <span className="nums shrink-0 text-xs text-ink-muted">{summary}</span>
        ) : null}
      </button>

      {open ? (
        <div className="border-t border-hairline px-5 py-4">
          {/* The section's own controls sit inside it, not on the closed row:
              a sort button on a table nobody can see sorts nothing. */}
          {actions ? <div className="mb-3 flex flex-wrap justify-end gap-2">{actions}</div> : null}
          {children}
        </div>
      ) : null}
    </section>
  );
}
