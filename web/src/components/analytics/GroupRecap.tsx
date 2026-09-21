'use client';

import clsx from 'clsx';
import {
  ChevronRight,
  CircleDashed,
  Clock,
  MessageCircle,
  Radio,
  Tag,
  Ticket,
  Users,
  type LucideIcon,
} from 'lucide-react';
import Link from 'next/link';

import { DAILY_GROUPS, type DailyColumn } from '@/components/analytics/dailyColumns';
import type { DrilldownKind } from '@/components/analytics/DrilldownPanel';
import { RowSkeleton } from '@/components/analytics/Primitives';
import type { LabelUsage, PerformanceDay } from '@/lib/types';

/**
 * The period's recap, group by group, over every application at once.
 *
 * Every figure is looked up out of DAILY_GROUPS — the column model behind
 * Rincian Per Hari, the per-person tables and the CSV — so this screen cannot
 * drift from those. A metric renamed or redefined once is renamed everywhere,
 * which is the only reason two screens showing the same number can be trusted
 * to agree.
 *
 * Each card carries a comparison against the period immediately before it,
 * computed from the same endpoint over the previous stretch of calendar. No
 * figure here is estimated, and a comparison is simply absent where the
 * previous period had nothing to compare against: a percentage from zero is not
 * a large improvement, it is an undefined one.
 */

interface CardSpec {
  group: string;
  /**
   * Which of the group's columns this card shows, in this order.
   *
   * Empty means all of them: a list has room for everything the group measures,
   * where four big figures never did.
   */
  keys: string[];
  icon: LucideIcon;
  tone: Tone;
  href: string;
  /** Twelfths of the row. */
  span: string;
  /**
   * How the card reads.
   *
   * `figures` is four numbers at a glance, two over two, and it earns its space
   * only where every figure is one somebody watches all day. `list` is a name
   * and a number per line: quieter, holds the whole group instead of four of
   * it, and is the honest shape for a card whose figures are usually zero.
   */
  variant?: 'figures' | 'list';
  /** What opening the card shows. Absent means the card only links out. */
  drill?: DrilldownKind;
  /** The label on the button that opens the drilldown. */
  drillLabel?: string;
  /**
   * A shorter name for this card only.
   *
   * "Pesan Masuk Grup" is the right label in a table where it sits beside the
   * personal one; on a card titled Group Engagement the word is already said,
   * and repeating it costs the line that made the label wrap. The tooltip still
   * carries the full definition.
   */
  labels?: Record<string, string>;
  note?: string;
}

const CARDS: CardSpec[] = [
  {
    group: 'chat',
    keys: ['contacts_inbound', 'contacts_served', 'inbound', 'outbound'],
    icon: MessageCircle,
    tone: 'brand',
    href: '/chat',
    span: 'lg:col-span-4',
  },
  {
    group: 'group',
    keys: ['groups_active', 'groups_handled', 'group_in', 'group_out'],
    icon: Users,
    tone: 'info',
    href: '/groups',
    span: 'lg:col-span-4',
    labels: { group_in: 'Pesan Masuk', group_out: 'Pesan Keluar' },
  },
  {
    group: 'sla',
    keys: ['avg', 'fastest', 'slowest', 'sla_ratio'],
    icon: Clock,
    tone: 'warn',
    href: '/sla',
    span: 'lg:col-span-4',
    labels: { avg: 'Rata-rata', fastest: 'Tercepat', slowest: 'Terlama', sla_ratio: 'Tercapai' },
  },
  {
    group: 'followup',
    keys: [],
    icon: Ticket,
    tone: 'amber',
    href: '/contacts',
    span: 'lg:col-span-6',
    variant: 'list',
    drill: { kind: 'follow-ups' },
    drillLabel: 'Lihat daftar follow-up',
  },
  {
    group: 'label',
    keys: [],
    icon: Tag,
    tone: 'iris',
    href: '/contacts',
    span: 'lg:col-span-6',
    variant: 'list',
    drill: { kind: 'label-events' },
    drillLabel: 'Lihat perpindahan label',
  },
  {
    group: 'broadcast',
    keys: [],
    icon: Radio,
    tone: 'brand',
    href: '/broadcast',
    span: 'lg:col-span-6',
    variant: 'list',
    note: 'Pesan broadcast bukan chat: tidak masuk Pesan Keluar, SLA, maupun follow-up.',
  },
  {
    group: 'story',
    keys: [],
    icon: CircleDashed,
    tone: 'info',
    href: '/story',
    span: 'lg:col-span-6',
    variant: 'list',
    note: 'Views dihitung dari receipt yang benar-benar diterima. Batas bawah, bukan jumlah pasti.',
  },
];

type Tone = 'brand' | 'info' | 'warn' | 'amber' | 'iris';

/** The icon tints. Identity only: none of them carries a status meaning. */
const TONES: Record<Tone, string> = {
  brand: 'bg-brand-600/12 text-brand-700',
  info: 'bg-info-soft text-info',
  warn: 'bg-warn-soft text-warn',
  amber: 'bg-warn-soft text-amber-badge',
  iris: 'bg-iris-soft text-iris',
};

/**
 * Where a rise is an improvement and where it is not.
 *
 * Green on "Label Dilepas" would be an opinion the product has no business
 * having, so anything without a defined direction is printed in neutral grey:
 * the movement is the fact, the judgement is the reader's.
 */
const DOWN_IS_BETTER = new Set(['avg', 'fastest', 'slowest', 'fu_unanswered', 'bc_failed', 'st_failed']);
const NO_DIRECTION = new Set([
  'first_labeled', 'assigned', 'removed', 'contacts_changed', 'bc_created', 'st_created',
]);

export function GroupRecap({
  summary,
  previous,
  loading,
  labelUsage = [],
  onDrill,
  columnDrills,
  omit = [],
}: {
  summary: PerformanceDay | undefined;
  /** The same figures over the period before this one. Undefined until loaded. */
  previous: PerformanceDay | undefined;
  loading: boolean;
  /**
   * The workspace's own labels with how many contacts carry each.
   *
   * Passed in rather than fetched here because the page already asks for it,
   * and the Status Label card is the one place where "label dan jumlahnya"
   * means the operator's actual vocabulary — Cold, Warm, Closing — not the
   * names of our metrics.
   */
  labelUsage?: LabelUsage[];
  /** Opens the drilldown a card asks for. Without it the cards only link out. */
  onDrill?: (kind: DrilldownKind) => void;
  /**
   * Which single figures have a list behind them, by column key.
   *
   * Performa passes these; the Dashboard does not. There a figure is a reading,
   * here it is a question — "which forty-eight contacts", "which nine that
   * nobody answered" — and the answer is one press away rather than a filter to
   * reconstruct by hand.
   */
  columnDrills?: Record<string, DrilldownKind>;
  /** Group keys to leave out, for a view already narrowed past them. */
  omit?: string[];
}) {
  if (loading && !summary) {
    return (
      <div className="grid gap-4 lg:grid-cols-12">
        {CARDS.slice(0, 3).map((c) => (
          <div
            key={c.group}
            className={clsx(
              'rounded-card border border-hairline bg-surface-raised p-4 shadow-e1',
              c.span,
            )}
          >
            <RowSkeleton count={2} />
          </div>
        ))}
      </div>
    );
  }
  if (!summary) return null;

  return (
    <div className="grid gap-4 lg:grid-cols-12">
      {CARDS.filter((c) => !omit.includes(c.group)).map((card) => {
        const group = DAILY_GROUPS.find((g) => g.key === card.group);
        if (!group) return null;
        const columns = card.keys.length
          ? card.keys
              .map((key) => group.columns.find((c) => c.key === key))
              .filter((c): c is DailyColumn => Boolean(c))
          : group.columns;
        const Icon = card.icon;
        const drill = card.drill && onDrill ? () => onDrill(card.drill!) : undefined;

        return (
          <section
            key={card.group}
            className={clsx(
              'flex flex-col rounded-card border border-hairline bg-surface-raised shadow-e1',
              card.span,
            )}
          >
            <div className="flex items-center gap-2.5 px-4 pt-4 pb-3">
              <span
                className={clsx(
                  'flex size-8 shrink-0 items-center justify-center rounded-control',
                  TONES[card.tone],
                )}
              >
                <Icon className="size-4" aria-hidden />
              </span>
              {/* The heading is the control where there is something to open,
                  because "click the card" is what somebody tries first. */}
              {drill ? (
                <button
                  type="button"
                  onClick={drill}
                  className="min-w-0 flex-1 truncate text-left text-sm font-semibold text-ink hover:text-brand-700"
                >
                  {group.label}
                </button>
              ) : (
                <h3 className="flex-1 truncate text-sm font-semibold text-ink">{group.label}</h3>
              )}

              {drill ? (
                <button
                  type="button"
                  onClick={drill}
                  aria-label={card.drillLabel ?? `Buka ${group.label}`}
                  className="shrink-0 rounded-control p-1 text-ink-muted transition-colors hover:bg-surface-sunken hover:text-ink"
                >
                  <ChevronRight className="size-4" aria-hidden />
                </button>
              ) : (
                <Link
                  href={card.href}
                  aria-label={`Buka ${group.label}`}
                  className="shrink-0 rounded-control p-1 text-ink-muted transition-colors hover:bg-surface-sunken hover:text-ink"
                >
                  <ChevronRight className="size-4" aria-hidden />
                </Link>
              )}
            </div>

            {card.variant === 'list' ? (
              <div className="flex flex-1 flex-col">
                {/* The operator's own labels, where there are any: on a card
                    called Status Label, "label" means Cold and Closing, not the
                    names of our counters. The counters follow underneath. */}
                {card.group === 'label' && labelUsage.length > 0 ? (
                  <ul className="border-t border-hairline">
                    {labelUsage.slice(0, 8).map((u) => (
                      <li
                        key={u.label_id ?? u.name}
                        title={`${u.assigned} dipasang, ${u.removed} dilepas pada periode ini`}
                        className="flex items-center gap-2.5 border-b border-hairline px-4 py-1.5 last:border-b-0"
                      >
                        <span
                          aria-hidden
                          className="size-2 shrink-0 rounded-full"
                          style={{ backgroundColor: u.color }}
                        />
                        <span className="min-w-0 flex-1 truncate text-xs text-ink-soft">
                          {u.name}
                        </span>
                        <span className="nums shrink-0 text-sm font-semibold text-ink">
                          {u.active_contacts.toLocaleString('id-ID')}
                        </span>
                      </li>
                    ))}
                  </ul>
                ) : null}

                <ul className="flex-1 border-t border-hairline">
                  {columns.map((column) => (
                    <Row
                      key={column.key}
                      column={column}
                      label={card.labels?.[column.key] ?? column.label}
                      summary={summary}
                      previous={previous}
                      onOpen={openerFor(column.key, columnDrills, onDrill)}
                    />
                  ))}
                </ul>

                {card.drillLabel && drill ? (
                  <button
                    type="button"
                    onClick={drill}
                    className="border-t border-hairline px-4 py-2 text-left text-xs font-medium text-brand-700 transition-colors hover:bg-surface-sunken"
                  >
                    {card.drillLabel}
                  </button>
                ) : null}
              </div>
            ) : (
              /*
               * Two over two.
               *
               * Four abreast only ever worked for cards whose figures were one
               * glyph wide; the moment one of them was a duration the values
               * broke across lines and the labels across three. Half the width
               * fits every figure the model can produce.
               */
              <div className="grid flex-1 grid-cols-2 items-start gap-x-4 gap-y-4 px-4 pb-4">
                {columns.map((column) => (
                  <Figure
                    key={column.key}
                    column={column}
                    label={card.labels?.[column.key] ?? column.label}
                    summary={summary}
                    previous={previous}
                    onOpen={openerFor(column.key, columnDrills, onDrill)}
                  />
                ))}
              </div>
            )}

            {card.note ? (
              <p className="border-t border-hairline px-4 py-2 text-2xs leading-relaxed text-ink-muted">
                {card.note}
              </p>
            ) : null}
          </section>
        );
      })}
    </div>
  );
}

/**
 * One measurement as a line: what it is, how it moved, how many.
 *
 * The shape for the groups where most figures are zero most days. Four big
 * zeroes make a card look like a dashboard for something that is not
 * happening; the same four as lines read as a checklist that is simply quiet,
 * and the space saved is what lets the card carry the whole group instead of
 * the four fields that fitted.
 */
function Row({
  column,
  label,
  summary,
  previous,
  onOpen,
}: {
  column: DailyColumn;
  label: string;
  summary: PerformanceDay;
  previous: PerformanceDay | undefined;
  onOpen?: () => void;
}) {
  const shown = display(column, summary);
  const now = (column.raw ?? column.value)(summary);
  const before = previous ? (column.raw ?? column.value)(previous) : null;

  const body = (
    <>
      <span className="min-w-0 flex-1 truncate text-left text-xs text-ink-soft">{label}</span>
      <Delta columnKey={column.key} now={now} before={before} />
      <span
        className={clsx(
          'nums shrink-0 text-sm font-semibold whitespace-nowrap',
          shown === '—' || shown === '0' ? 'text-ink-muted' : 'text-ink',
        )}
      >
        {shown}
      </span>
    </>
  );

  return (
    <li className="border-b border-hairline last:border-b-0" title={`${column.label} — ${column.info}`}>
      {onOpen ? (
        <button
          type="button"
          onClick={onOpen}
          className="flex w-full items-baseline gap-2 px-4 py-1.5 transition-colors hover:bg-surface-sunken/60"
        >
          {body}
        </button>
      ) : (
        <span className="flex items-baseline gap-2 px-4 py-1.5">{body}</span>
      )}
    </li>
  );
}

/** The opener for one figure, or nothing when it has no list behind it. */
function openerFor(
  key: string,
  drills: Record<string, DrilldownKind> | undefined,
  onDrill: ((k: DrilldownKind) => void) | undefined,
): (() => void) | undefined {
  const target = drills?.[key];
  if (!target || !onDrill) return undefined;
  return () => onDrill(target);
}

/** One figure, its name, and how it moved. */
function Figure({
  column,
  label,
  summary,
  previous,
  onOpen,
}: {
  column: DailyColumn;
  label: string;
  summary: PerformanceDay;
  previous: PerformanceDay | undefined;
  onOpen?: () => void;
}) {
  const shown = display(column, summary);

  /*
   * The SLA moves in percentage points, not in percent of a percent.
   *
   * "87% became 92%" is a rise of five points; calling it "+5.7%" is true of
   * the ratio between two ratios and of nothing anybody cares about. Every
   * other column is a count, where a percentage of the previous count is
   * exactly the right comparison.
   */
  const points = column.key === 'sla_ratio';
  const now = points ? slaPct(summary) : (column.raw ?? column.value)(summary);
  const before = previous
    ? points
      ? slaPct(previous)
      : (column.raw ?? column.value)(previous)
    : null;

  /* One size for every figure. Half a card is wide enough for the longest
     string the model produces, so shrinking the durations only made two
     figures in the same card look like two different kinds of thing. */
  const value = (
    <span
      className={clsx(
        'nums block text-xl leading-tight font-semibold whitespace-nowrap',
        shown === '—' || shown === '0' ? 'text-ink-muted' : 'text-ink',
        onOpen && 'underline-offset-4 group-hover:underline',
      )}
    >
      {shown}
    </span>
  );

  return (
    <div className="min-w-0" title={`${column.label} — ${column.info}`}>
      {onOpen ? (
        <button type="button" onClick={onOpen} className="group block text-left">
          {value}
        </button>
      ) : (
        value
      )}
      <p className="mt-0.5 truncate text-2xs leading-snug text-ink-muted">{label}</p>
      <p className="mt-1 h-4">
        <Delta columnKey={column.key} now={now} before={before} points={points} />
      </p>
    </div>
  );
}

/** Achieved cycles as a percentage, or null when nothing completed. */
function slaPct(d: PerformanceDay): number | null {
  return d.sla_completed === 0 ? null : Math.round((d.sla_achieved / d.sla_completed) * 100);
}

/**
 * What the figure reads as on a card.
 *
 * One exception to printing the column's own value: the SLA is shown as a
 * percentage here and as "13/15" in the tables. Both are the same two numbers,
 * and on a card that carries one figure per column a ratio is the harder of the
 * two to compare against yesterday. The ratio stays in the tooltip, because the
 * percentage alone hides how few cycles it might be counting.
 */
function display(column: DailyColumn, summary: PerformanceDay): string {
  if (column.key === 'sla_ratio') {
    if (summary.sla_completed === 0) return '—';
    return `${Math.round((summary.sla_achieved / summary.sla_completed) * 100)}%`;
  }
  const value = column.value(summary);
  if (value === null) return '—';
  return typeof value === 'number' ? value.toLocaleString('id-ID') : value;
}

/**
 * The change against the previous period, or nothing at all.
 *
 * Nothing is the honest answer more often than it looks: a figure that was zero
 * before has no percentage, a duration nobody has recorded yet has no trend,
 * and a ratio like "3/4" is not a quantity that can be divided.
 */
function Delta({
  columnKey,
  now,
  before,
  points = false,
}: {
  columnKey: string;
  now: number | string | null;
  before: number | string | null;
  /** Compare by subtraction rather than by ratio, for figures already in %. */
  points?: boolean;
}) {
  if (typeof now !== 'number' || typeof before !== 'number' || (!points && before === 0)) {
    return null;
  }

  const pct = points ? now - before : Math.round(((now - before) / before) * 100);
  const rising = pct > 0;
  const good =
    pct === 0 || NO_DIRECTION.has(columnKey)
      ? null
      : DOWN_IS_BETTER.has(columnKey)
        ? !rising
        : rising;

  return (
    <span
      className={clsx(
        'nums shrink-0 text-2xs font-medium',
        good === null ? 'text-ink-muted' : good ? 'text-brand-700' : 'text-danger',
      )}
      title={`Periode sebelumnya: ${before.toLocaleString('id-ID')}`}
    >
      {pct === 0 ? '' : rising ? '▲ ' : '▼ '}
      {rising ? '+' : ''}
      {pct}
      {points ? ' poin' : '%'}
    </span>
  );
}
