'use client';

import clsx from 'clsx';
import { Download } from 'lucide-react';
import { useMemo, useState, type ReactNode } from 'react';

import {
  COL_METRIC,
  DataCell,
  GridTd,
  GroupTh,
  HeadRow,
  SubTh,
  TableFrame,
} from '@/components/analytics/DataTable';
import { DAILY_GROUPS, groupedCSV } from '@/components/analytics/dailyColumns';
import type { DashboardSummary } from '@/lib/types';

/**
 * Any table built on the Rincian Per Hari column model.
 *
 * Days, people and applications are the same forty-one measurements asked of a
 * different subject, so they are one table with a different first column rather
 * than three tables that drift. Every one of them is sortable by any metric,
 * exports the same CSV, and puts a metric under the same group heading.
 *
 * The subject column is a render function rather than a string: a day is a
 * date, a person is a name over an email, an application is a mark over a code.
 * Everything to the right of it is identical by construction.
 */

export interface GroupedRow {
  /** Stable across reorderings; also the row's React key. */
  id: string;
  /** How this row is named in the CSV. */
  label: string;
  summary: DashboardSummary;
  /** What the pinned first column draws. */
  subject: ReactNode;
  onClick?: () => void;
  selected?: boolean;
  dim?: boolean;
}

/** Width of the pinned subject column. Wide enough for a name over an email. */
const SUBJECT_W = 220;

export function GroupedBreakdown({
  rows,
  subjectLabel,
  csvName,
  /** Sorted by this column initially; "subject" means the first column. */
  initialSort = 'subject',
}: {
  rows: GroupedRow[];
  subjectLabel: string;
  csvName: string;
  initialSort?: string;
}) {
  const [sortKey, setSortKey] = useState(initialSort);
  const [desc, setDesc] = useState(false);

  const sorted = useMemo(() => {
    const column = DAILY_GROUPS.flatMap((g) => g.columns).find((c) => c.key === sortKey);
    const out = [...rows];
    out.sort((a, b) => {
      if (!column) return a.label.localeCompare(b.label);
      const read = column.raw ?? column.value;
      const av = read(a.summary);
      const bv = read(b.summary);
      // A metric with nothing behind it sorts as "no data" rather than as
      // zero: an application nobody has answered yet is not the fastest one.
      const an = typeof av === 'number' ? av : av === null ? -1 : Number.NaN;
      const bn = typeof bv === 'number' ? bv : bv === null ? -1 : Number.NaN;
      if (Number.isNaN(an) || Number.isNaN(bn)) {
        return String(av ?? '').localeCompare(String(bv ?? ''));
      }
      if (an === bn) return a.label.localeCompare(b.label);
      return an - bn;
    });
    return desc ? out.reverse() : out;
  }, [rows, sortKey, desc]);

  function toggleSort(key: string) {
    if (sortKey === key) {
      setDesc((v) => !v);
      return;
    }
    setSortKey(key);
    setDesc(key !== 'subject');
  }

  const widths = [SUBJECT_W, ...DAILY_GROUPS.flatMap((g) => g.columns.map(() => COL_METRIC))];

  return (
    <>
      <div className="mb-2 flex justify-end">
        <button
          type="button"
          onClick={() => downloadCSV(csvName, subjectLabel, sorted)}
          disabled={sorted.length === 0}
          className="inline-flex items-center gap-1.5 rounded-control border border-hairline bg-surface-raised px-2.5 py-1.5 text-xs font-medium text-ink-soft transition-colors hover:bg-surface-sunken disabled:opacity-50"
        >
          <Download className="size-3.5" />
          Export CSV
        </button>
      </div>

      <TableFrame widths={widths}>
        <thead>
          <HeadRow>
            <GroupTh rowSpan={2} sticky>
              <button
                type="button"
                onClick={() => toggleSort('subject')}
                className="transition-colors hover:text-brand-700"
              >
                {subjectLabel}
              </button>
            </GroupTh>
            {DAILY_GROUPS.map((g, i) => (
              <GroupTh key={g.key} colSpan={g.columns.length} divide={i < DAILY_GROUPS.length - 1}>
                {g.label}
              </GroupTh>
            ))}
          </HeadRow>
          {/* Directly beneath the group row, which is what `top-[33px]`
              matches. Both stay put, so scrolling never orphans the numbers. */}
          <tr className="sticky top-[33px] z-20">
            {DAILY_GROUPS.map((g, gi) =>
              g.columns.map((c, ci) => (
                <SubTh
                  key={c.key}
                  info={c.info}
                  divide={ci === g.columns.length - 1 && gi < DAILY_GROUPS.length - 1}
                  active={sortKey === c.key}
                  desc={desc}
                  onSort={() => toggleSort(c.key)}
                >
                  {c.label}
                </SubTh>
              )),
            )}
          </tr>
        </thead>
        <tbody>
          {sorted.map((row) => (
            <tr
              key={row.id}
              onClick={row.onClick}
              aria-selected={row.selected}
              // A fixed row height, for the same reason the headers have one.
              // Taller than the daily table's because the subject here is two
              // lines: a name over an email, a mark over a code.
              className={clsx(
                'group h-[52px] border-b border-hairline last:border-0',
                row.dim && 'opacity-60',
                row.onClick && 'cursor-pointer transition-colors hover:bg-surface-sunken/50',
                row.selected && 'bg-brand-600/8',
              )}
            >
              <GridTd sticky>{row.subject}</GridTd>
              {DAILY_GROUPS.map((g, gi) =>
                g.columns.map((c, ci) => (
                  <DataCell
                    key={c.key}
                    value={c.value(row.summary)}
                    divide={ci === g.columns.length - 1 && gi < DAILY_GROUPS.length - 1}
                  />
                )),
              )}
            </tr>
          ))}
        </tbody>
      </TableFrame>
    </>
  );
}

/**
 * Exports exactly the rows on screen.
 *
 * Built from what was already fetched, so it carries precisely the scope the
 * reader is allowed to see: there is no second, wider query behind it that
 * could hand somebody data the page would not show them.
 */
function downloadCSV(
  name: string,
  subjectLabel: string,
  // The minimum the export actually reads, so a caller that has rows but no
  // table to draw them in does not have to invent a `subject` cell for one.
  rows: { label: string; summary: DashboardSummary }[],
) {
  const blob = new Blob([groupedCSV(subjectLabel, rows)], { type: 'text/csv;charset=utf-8' });
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = `${name}.csv`;
  a.click();
  URL.revokeObjectURL(url);
}
