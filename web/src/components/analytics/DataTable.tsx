'use client';

import clsx from 'clsx';
import { ChevronDown, ChevronUp } from 'lucide-react';
import type { ReactNode } from 'react';

import { InfoTip } from '@/components/analytics/Primitives';

/**
 * One shape for every table of figures on the Performa page.
 *
 * Rincian Per Hari was fixed this way once already: every metric column the
 * same width, set by a colgroup and held there by `table-fixed`, and every
 * header and body cell a fixed height. The tables beside it were still sizing
 * themselves from their content, so "FU Belum Dibalas" got three lines and four
 * times the width of "Follow-up", the numbers under them never lined up, and
 * the last column fell off the edge.
 *
 * That is what "belum seragam" looks like from the outside. A table of counts
 * should read as a grid, and a grid needs one column width and one row height,
 * so those live here rather than being written out again per table.
 */

/** One width for every metric column. Fits four digits with room to spare. */
export const COL_METRIC = 104;
/** The name column: a person or an application, with a second line under it. */
export const COL_NAME = 210;
/** A column of words rather than counts: applications, a status badge. */
export const COL_TEXT = 150;

/** Row heights. Two lines of text needs the taller one. */
export const ROW_SINGLE = 'h-[38px]';
export const ROW_DOUBLE = 'h-[52px]';

// Solid on hover too. A half-transparent hover on a pinned column lets the
// columns sliding under it show through, which looks like a rendering fault.
const STICKY =
  'sticky left-0 z-10 bg-surface-raised shadow-[1px_0_0_var(--color-hairline)] group-hover:bg-surface-sunken';

const STICKY_HEAD =
  'sticky left-0 z-30 bg-surface-sunken shadow-[1px_0_0_var(--color-hairline)]';

/**
 * The scroll container and the table itself.
 *
 * `widths` is the whole column model: one number per column, in order. Passing
 * it rather than letting each table write its own colgroup is what keeps two
 * tables on the same screen from choosing two different grids.
 */
export function TableFrame({ widths, children }: { widths: number[]; children: ReactNode }) {
  return (
    <div className="max-h-[70vh] overflow-auto rounded-card border border-hairline bg-surface-raised shadow-e1">
      <table className="w-full table-fixed border-collapse text-sm">
        <colgroup>
          {widths.map((w, i) => (
            <col key={i} style={{ width: w }} />
          ))}
        </colgroup>
        {children}
      </table>
    </div>
  );
}

/** The header row, pinned so scrolling never orphans the numbers. */
export function HeadRow({ children }: { children: ReactNode }) {
  return <tr className="sticky top-0 z-20">{children}</tr>;
}

/**
 * One column heading.
 *
 * `h-[46px]` on every one of them is what makes the row regular: without it a
 * heading that wraps to two lines makes its own cell taller and drags the whole
 * row with it. Nothing is abbreviated; the shape is what is made regular.
 */
export function Th({
  children,
  align = 'center',
  sticky,
  active,
  desc,
  onSort,
}: {
  children: ReactNode;
  align?: 'left' | 'center';
  sticky?: boolean;
  /** Set when this column is the one being sorted by. */
  active?: boolean;
  desc?: boolean;
  onSort?: () => void;
}) {
  const inner = (
    <span
      className={clsx(
        'flex w-full items-center gap-1',
        align === 'left' ? 'justify-start text-left' : 'justify-center text-center',
      )}
    >
      <span className="line-clamp-2">{children}</span>
      {active ? (
        desc ? (
          <ChevronDown className="size-3 shrink-0" />
        ) : (
          <ChevronUp className="size-3 shrink-0" />
        )
      ) : null}
    </span>
  );

  return (
    <th
      scope="col"
      className={clsx(
        'h-[46px] border-b border-hairline bg-surface-sunken px-2 align-middle text-2xs leading-tight font-semibold tracking-wide uppercase',
        active ? 'text-ink' : 'text-ink-muted',
        sticky && STICKY_HEAD,
      )}
    >
      {onSort ? (
        <button
          type="button"
          onClick={onSort}
          className="flex w-full items-center transition-colors hover:text-ink"
        >
          {inner}
        </button>
      ) : (
        inner
      )}
    </th>
  );
}

/** A body row. `group` is what lets the pinned first cell follow the hover. */
export function Row({
  children,
  tall,
  selected,
  onClick,
  dim,
}: {
  children: ReactNode;
  /** True when a cell carries two lines, e.g. a name over an email. */
  tall?: boolean;
  selected?: boolean;
  onClick?: () => void;
  /** An inactive account, kept in the table and visibly set back. */
  dim?: boolean;
}) {
  return (
    <tr
      onClick={onClick}
      aria-selected={selected}
      className={clsx(
        'group border-b border-hairline last:border-0',
        tall ? ROW_DOUBLE : ROW_SINGLE,
        dim && 'opacity-60',
        onClick && 'cursor-pointer transition-colors hover:bg-surface-sunken/50',
        selected && 'bg-brand-600/8',
      )}
    >
      {children}
    </tr>
  );
}

/** One body cell. Counts are centred, under a centred heading of equal width. */
export function Td({
  children,
  align = 'center',
  sticky,
  tone,
  className,
}: {
  children: ReactNode;
  align?: 'left' | 'center';
  sticky?: boolean;
  /** "muted" for a zero or a missing measurement, "warn" for a figure to act on. */
  tone?: 'muted' | 'warn';
  className?: string;
}) {
  return (
    <td
      className={clsx(
        'overflow-hidden px-3 align-middle text-ellipsis',
        align === 'center' ? 'text-center' : 'text-left',
        tone === 'muted' ? 'text-ink-muted/70' : tone === 'warn' ? 'text-warn' : 'text-ink-soft',
        sticky && STICKY,
        className,
      )}
    >
      {children}
    </td>
  );
}

/** A count or a duration: monospaced figures, dimmed when there is nothing. */
export function NumberCell({
  value,
  empty,
  tone,
}: {
  value: ReactNode;
  /** True when the figure is zero or has no measurement behind it. */
  empty?: boolean;
  tone?: 'warn';
}) {
  return (
    <Td tone={empty ? 'muted' : tone} className="nums whitespace-nowrap">
      {value}
    </Td>
  );
}

/* --- the two-level grid ---------------------------------------------------
 *
 * Forty-one metric columns under eight group headings. Rincian Per Hari was
 * built this way first and the per-person and per-application tables now read
 * from the same column model, so a metric cannot appear under one heading in
 * one table and another heading in the next.
 *
 * These lived inside DailyBreakdown until three tables needed them.
 */

/** Top header row: the name of a group, centred over its own columns. */
export function GroupTh({
  children,
  colSpan,
  rowSpan,
  sticky,
  divide,
}: {
  children: ReactNode;
  colSpan?: number;
  rowSpan?: number;
  sticky?: boolean;
  divide?: boolean;
}) {
  return (
    <th
      scope="colgroup"
      colSpan={colSpan}
      rowSpan={rowSpan}
      // A pale brand tint, which is the only colour in the header. Tinting
      // each subheader differently as well would turn the top of the table
      // into a paint chart and stop the grouping from reading at all.
      //
      // Solid, not a translucent brand tint: this band is pinned, so rows
      // scroll underneath it and anything see-through prints them through the
      // heading.
      className={clsx(
        'h-[33px] border-b border-hairline bg-th-group px-3 text-center text-2xs font-semibold whitespace-nowrap text-ink',
        divide && 'border-r border-r-hairline-strong',
        // The first cell spans both header rows, so it carries the full height
        // of the two and stays pinned in both directions at once.
        sticky && 'sticky left-0 z-40 text-left shadow-[1px_0_0_var(--color-hairline)]',
      )}
    >
      {children}
    </th>
  );
}

/**
 * Second header row: one metric, with its definition on the tooltip.
 *
 * `h-[46px]` on every one of them is what makes the row regular. Without it a
 * heading that wraps to two lines makes its own cell taller and drags the
 * whole row with it, so a table of forty headings ends up as tall as its
 * longest label.
 */
export function SubTh({
  children,
  info,
  divide,
  active,
  desc,
  onSort,
}: {
  children: ReactNode;
  info: string;
  divide?: boolean;
  /** Set when this column is the one the table is sorted by. */
  active?: boolean;
  desc?: boolean;
  onSort?: () => void;
}) {
  const label = (
    <span className="flex items-center justify-center gap-1 text-center">
      <span className="line-clamp-2">{children}</span>
      {active ? (
        desc ? (
          <ChevronDown className="size-3 shrink-0" />
        ) : (
          <ChevronUp className="size-3 shrink-0" />
        )
      ) : null}
    </span>
  );

  return (
    <th
      scope="col"
      className={clsx(
        'h-[46px] border-b border-hairline bg-surface-sunken px-2 align-middle text-2xs leading-tight font-medium',
        active ? 'text-ink' : 'text-ink-muted',
        divide && 'border-r border-r-hairline-strong',
      )}
    >
      <span className="flex items-center justify-center gap-1">
        {onSort ? (
          <button type="button" onClick={onSort} className="transition-colors hover:text-ink">
            {label}
          </button>
        ) : (
          label
        )}
        <InfoTip text={info} />
      </span>
    </th>
  );
}

/** A body cell inside the two-level grid. */
export function GridTd({
  children,
  center,
  sticky,
  divide,
  className,
}: {
  children: ReactNode;
  center?: boolean;
  sticky?: boolean;
  divide?: boolean;
  className?: string;
}) {
  return (
    <td
      className={clsx(
        'overflow-hidden px-3 align-middle text-ellipsis text-ink-soft',
        center && 'text-center',
        divide && 'border-r border-hairline-strong',
        sticky && STICKY,
        className,
      )}
    >
      {children}
    </td>
  );
}

/**
 * One measured cell.
 *
 * `null` means the metric does not apply to that row and prints a dash; zero
 * means it applies and nothing happened, and prints 0. Collapsing the two would
 * turn "no rota that day" and "worked and achieved nothing" into the same
 * statement.
 */
export function DataCell({
  value,
  divide,
}: {
  value: number | string | null;
  divide?: boolean;
}) {
  if (value === null) {
    return (
      <GridTd center divide={divide} className="text-ink-muted/60">
        –
      </GridTd>
    );
  }
  const zero = value === 0;
  return (
    <GridTd center divide={divide} className={clsx('nums', zero && 'text-ink-muted/60')}>
      {value}
    </GridTd>
  );
}
