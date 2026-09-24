'use client';

import clsx from 'clsx';
import { Activity } from 'lucide-react';
import { useMemo, useState } from 'react';
import useSWR from 'swr';

import { shiftDays } from '@/components/analytics/period';
import { EmptyState, ErrorState, InfoTip, RowSkeleton } from '@/components/analytics/Primitives';
import { fetcher, trafficPath, type AnalyticsQuery, type TrafficPoint } from '@/lib/api';
import { todayWIB } from '@/lib/useAnalyticsFilter';

/**
 * Message volume over time: in and out, personal and group.
 *
 * Four series rather than one line, because the four move for different
 * reasons. A morning when customers write in is not the same event as a
 * morning when a group is busy, and a single "messages" line would hide which
 * of the two happened — which is the only thing anybody would look at this
 * chart to find out.
 *
 * Drawn as stacked bars rather than a line. The buckets are discrete counts
 * with real gaps in them (an hour with no traffic is a real zero, not a point
 * to interpolate through), and a line drawn across those gaps invents a slope
 * that nothing measured.
 */

/*
 * Two greens for the personal chat and two golds for the groups.
 *
 * The pairing is the point: the eye should separate "customers" from "groups"
 * before it separates "in" from "out", because that is the order the questions
 * come in. Within each pair the darker tone is what arrived and the lighter is
 * what we sent, so a bar that is mostly dark is a bar nobody has answered yet.
 */
const SERIES = [
  { key: 'inbound_personal', label: 'Masuk pribadi', color: 'var(--color-brand-800)', chat: 'personal' },
  { key: 'outbound_personal', label: 'Keluar pribadi', color: 'var(--color-brand-600)', chat: 'personal' },
  { key: 'group_inbound', label: 'Masuk grup', color: 'var(--color-amber-badge)', chat: 'group' },
  {
    key: 'group_outbound',
    label: 'Keluar grup',
    color: 'color-mix(in srgb, var(--color-amber-badge) 45%, var(--color-surface-raised))',
    chat: 'group',
  },
] as const;

type ChatScope = 'all' | 'personal' | 'group';

const SCOPES: { id: ChatScope; label: string }[] = [
  { id: 'all', label: 'Semua' },
  { id: 'personal', label: 'Pribadi' },
  { id: 'group', label: 'Grup' },
];

export function TrafficChart({
  query,
  workHours = false,
}: {
  query: AnalyticsQuery;
  /**
   * Count only what happened while somebody was on shift.
   *
   * Asked for on the views about one person or one team, where "how busy were
   * we" means the hours they were actually working: a message at 2am is not
   * their traffic, and drawing it as theirs makes a quiet shift look busy and a
   * busy shift look ordinary.
   */
  workHours?: boolean;
}) {
  /*
   * Hour when the period is one day, day otherwise — chosen once from the
   * filter rather than remembered, because the wrong grain is unreadable
   * either way: twenty-four bars for a month is meaningless, and one bar for a
   * day says nothing at all.
   */
  const isSingleDay = Boolean(query.date) && !query.month && !query.from;
  const [bucket, setBucket] = useState<'hour' | 'day' | null>(null);
  const grain = bucket ?? (isSingleDay ? 'hour' : 'day');

  /*
   * The grain decides the window, not only the bar width.
   *
   * "Per hari" over a period of one day is a single bar: technically correct,
   * and useless — nobody presses it to see today drawn once. The two buttons
   * are really two questions, "how did today go, hour by hour" and "how have
   * the last weeks gone, day by day", so each carries the span its question
   * needs: the chosen day for hours, and at least thirty days for days.
   *
   * The window is stated under the heading, because it is the one thing on this
   * page that can reach outside the period filter and nothing should do that
   * quietly.
   */
  const shownRange = useMemo(() => chartWindow(query, grain), [query, grain]);

  const { data, error, isLoading, mutate } = useSWR<{
    points: TrafficPoint[];
    /** Whether the server actually applied the working-hours filter. */
    work_hours?: boolean;
    /** False when no rota covers this period, which is why it can be empty. */
    schedule_configured?: boolean;
  }>(trafficPath(shownRange.query, grain, workHours), fetcher, { keepPreviousData: true });

  // Claimed only when the answer says so. A server that does not know the
  // parameter returns every hour of the day, and a heading promising working
  // hours over it would be a caption that lies about its own chart.
  const inHours = Boolean(data?.work_hours);
  const noRota = inHours && data?.schedule_configured === false;

  /*
   * Personal or group, filtered here rather than at the server.
   *
   * One request already carries all four series, so narrowing to one kind is
   * instant and costs nothing — and switching back and forth is exactly what
   * somebody does while reading a spike, which would be miserable at one round
   * trip per press.
   */
  const [scope, setScope] = useState<ChatScope>('all');
  const series = useMemo(
    () => SERIES.filter((s) => scope === 'all' || s.chat === scope),
    [scope],
  );

  const returned = useMemo(() => data?.points ?? [], [data]);

  /*
   * Every bucket of the period, including the empty ones.
   *
   * The server only returns buckets that carried a message, which drew a day
   * with three busy hours as three fat bars side by side — a shape that reads
   * as "busy all day" and hides the twenty-one hours of silence between them.
   * An hour with no messages is a real measurement and it belongs on the axis.
   */
  const points = useMemo(
    () => fillBuckets(returned, shownRange.query, grain),
    [returned, shownRange, grain],
  );

  // The scale follows what is drawn. Keeping the peak of all four while
  // showing two would squash the visible bars against the floor for no reason.
  const sum = (p: TrafficPoint) => series.reduce((n, s) => n + p[s.key], 0);
  const peak = useMemo(
    () => Math.max(1, ...points.map(sum)),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [points, series],
  );
  const totals = useMemo(
    () =>
      points.reduce(
        (acc, p) => ({
          inbound_personal: acc.inbound_personal + p.inbound_personal,
          outbound_personal: acc.outbound_personal + p.outbound_personal,
          group_inbound: acc.group_inbound + p.group_inbound,
          group_outbound: acc.group_outbound + p.group_outbound,
        }),
        { inbound_personal: 0, outbound_personal: 0, group_inbound: 0, group_outbound: 0 },
      ),
    [points],
  );
  const shown = useMemo(
    () => points.some((p) => sum(p) > 0),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [points, series],
  );
  const { scaleMax, ticks } = useMemo(() => niceScale(peak), [peak]);

  return (
    <section className="rounded-card border border-hairline bg-surface-raised px-4 py-4 shadow-e1">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h2 className="flex items-center gap-2 text-sm font-semibold text-ink">
            <Activity className="size-4 text-ink-muted" aria-hidden />
            Aktivitas Percakapan
            <InfoTip
              text={
                (inHours
                  ? 'Hanya pesan yang masuk dan keluar pada jam kerja.'
                  : 'Jumlah pesan masuk dan keluar di semua aplikasi.') +
                ' Broadcast dan story tidak dihitung di sini: keduanya bukan percakapan.'
              }
            />
          </h2>
          {/*
            The window stays on the face of the card. It is not an explanation
            but a statement of what is currently drawn, and a chart that hides
            which stretch of time it covers is a chart that misleads.
          */}
          <p className="mt-0.5 text-2xs text-ink-soft">{shownRange.note}</p>
        </div>

        <div className="flex flex-wrap items-center gap-2">
          {/* What kind of chat, then how finely. Two separate questions, so
              two separate controls rather than one list of four mixed
              choices. */}
          <div className="flex rounded-control border border-hairline p-0.5">
            {SCOPES.map((sc) => (
              <button
                key={sc.id}
                type="button"
                onClick={() => setScope(sc.id)}
                aria-pressed={scope === sc.id}
                className={clsx(
                  'rounded-[6px] px-2.5 py-1 text-xs font-medium transition-colors',
                  scope === sc.id
                    ? 'bg-brand-800 text-white'
                    : 'text-ink-soft hover:bg-surface-sunken',
                )}
              >
                {sc.label}
              </button>
            ))}
          </div>

          <div className="flex rounded-control border border-hairline p-0.5">
            {(['hour', 'day'] as const).map((g) => (
              <button
                key={g}
                type="button"
                onClick={() => setBucket(g)}
                aria-pressed={grain === g}
                className={clsx(
                  'rounded-[6px] px-2.5 py-1 text-xs font-medium transition-colors',
                  grain === g
                    ? 'bg-brand-800 text-white'
                    : 'text-ink-soft hover:bg-surface-sunken',
                )}
              >
                {g === 'hour' ? 'Per jam' : 'Per hari'}
              </button>
            ))}
          </div>
        </div>
      </div>

      {error ? (
        <div className="mt-3">
          <ErrorState
            message={error instanceof Error ? error.message : 'Gagal memuat trafik.'}
            onRetry={() => void mutate()}
          />
        </div>
      ) : isLoading && returned.length === 0 ? (
        <div className="mt-3">
          <RowSkeleton count={2} />
        </div>
      ) : !shown ? (
        <div className="mt-3">
          <EmptyState
            title={
              noRota
                ? 'Jam kerja belum diatur untuk periode ini.'
                : scope === 'all'
                  ? inHours
                    ? 'Belum ada pesan pada jam kerja periode ini.'
                    : 'Belum ada pesan pada periode ini.'
                  : scope === 'personal'
                    ? 'Belum ada chat pribadi pada periode ini.'
                    : 'Belum ada pesan grup pada periode ini.'
            }
            hint={
              noRota
                ? 'Grafik ini hanya menghitung jam kerja, dan belum ada jadwal yang menutupi periode ini. Atur di halaman Jam Kerja.'
                : undefined
            }
          />
        </div>
      ) : (
        <>
          {/*
           * A scale, not just bars.
           *
           * Without one the tallest bar is "the tallest bar" and nothing more:
           * every period looks equally busy, because the chart rescales itself
           * to whatever it was handed. The ticks are what turn a shape back
           * into a quantity, and they are rounded outward to a readable step so
           * the top gridline is a number somebody can hold in their head.
           */}
          <div className="mt-4 flex gap-2">
            <div
              className="nums relative w-7 shrink-0 text-right text-[10px] text-ink-muted"
              style={{ height: PLOT + 18 }}
              aria-hidden
            >
              {ticks.map((t) => (
                <span
                  key={t}
                  className="absolute right-0 -translate-y-1/2"
                  style={{ top: PLOT - (t / scaleMax) * PLOT }}
                >
                  {t.toLocaleString('id-ID')}
                </span>
              ))}
            </div>

            {/* Horizontally scrollable so a month of days, or a day of hours,
                keeps a readable bar width instead of collapsing to hairlines. */}
            <div className="min-w-0 flex-1 overflow-x-auto pb-1">
              <div className="relative min-w-full" style={{ height: PLOT + 18 }}>
                {/* The baseline only. Bars this short are read against the
                    axis labels beside them, and four rules drawn across the
                    plot add lines without adding precision. */}
                <span
                  aria-hidden
                  className="absolute inset-x-0 border-t border-hairline"
                  style={{ top: PLOT }}
                />

                <div className="absolute inset-x-0 top-0 flex items-end gap-1" style={{ height: PLOT }}>
                  {points.map((p) => {
                    const total = sum(p);
                    return (
                      <div
                        key={p.bucket}
                        title={`${labelOf(p.bucket, grain)} · ${total.toLocaleString('id-ID')} pesan${series
                          .filter((sr) => p[sr.key] > 0)
                          .map((sr) => `\n${sr.label}: ${p[sr.key].toLocaleString('id-ID')}`)
                          .join('')}`}
                        className="flex h-full min-w-[14px] flex-1 flex-col justify-end"
                      >
                        <span
                          className="flex w-full flex-col-reverse justify-start overflow-hidden rounded-t-[3px]"
                          style={{ height: `${total === 0 ? 0 : Math.max(2, (total / scaleMax) * PLOT)}px` }}
                        >
                          {series.map((s) => {
                            const value = p[s.key];
                            if (value === 0) return null;
                            return (
                              <span
                                key={s.key}
                                style={{
                                  height: `${(value / Math.max(total, 1)) * 100}%`,
                                  backgroundColor: s.color,
                                }}
                              />
                            );
                          })}
                        </span>
                      </div>
                    );
                  })}
                </div>

                {/* Thinned when the buckets are many: thirty labels under
                    thirty bars is a grey smear, and every third one still
                    tells the eye where it is. The first and the last are
                    always kept, because they are the ones that say what range
                    is being looked at. */}
                <div className="absolute inset-x-0 flex gap-1" style={{ top: PLOT + 4 }}>
                  {points.map((p, i) => (
                    <span
                      key={p.bucket}
                      className="nums min-w-[14px] flex-1 text-center text-[10px] whitespace-nowrap text-ink-muted"
                    >
                      {i % labelEvery(points.length) === 0 || i === points.length - 1
                        ? labelOf(p.bucket, grain)
                        : ''}
                    </span>
                  ))}
                </div>
              </div>
            </div>
          </div>

          <ul className="mt-3 flex flex-wrap gap-x-4 gap-y-1.5 border-t border-hairline pt-3">
            {series.map((s) => (
              <li key={s.key} className="flex items-center gap-1.5 text-2xs text-ink-soft">
                <span
                  aria-hidden
                  className="size-2.5 rounded-full"
                  style={{ backgroundColor: s.color }}
                />
                {s.label}
                <span className="nums font-semibold text-ink">
                  {totals[s.key].toLocaleString('id-ID')}
                </span>
              </li>
            ))}
          </ul>
        </>
      )}
    </section>
  );
}

const MONTHS_ID = [
  'Januari', 'Februari', 'Maret', 'April', 'Mei', 'Juni',
  'Juli', 'Agustus', 'September', 'Oktober', 'November', 'Desember',
];

/**
 * What the chart asks for, which is not always what the page is filtered to.
 *
 * Hours keep the period exactly: the chosen day, all twenty-four of them.
 * Days widen a short period to the whole calendar month it ends in, so "Per
 * hari" in September draws September — thirty bars, first to last, whether or
 * not a day carried a message. A period already longer than that month is kept
 * as it is rather than trimmed down to fit.
 */
function chartWindow(
  query: AnalyticsQuery,
  grain: 'hour' | 'day',
): { query: AnalyticsQuery; note: string } {
  const { start, end } = resolveRange(query);
  if (grain === 'hour') {
    const days = daysInclusive(start, end);
    return { query, note: days > 1 ? `${days} hari, per jam` : '24 jam' };
  }

  const [y, m] = end.split('-').map(Number);
  const month = `${y}-${String(m).padStart(2, '0')}`;
  const monthStart = `${month}-01`;
  // Day 0 of the next month is the last day of this one: 28, 29, 30 or 31
  // without a table of month lengths.
  const monthEnd = new Date(Date.UTC(y, m, 0)).toISOString().slice(0, 10);

  if (start < monthStart) {
    return { query, note: `${daysInclusive(start, end)} hari` };
  }
  return {
    query: { ...query, date: undefined, from: undefined, to: undefined, month },
    note: `${MONTHS_ID[m - 1]} ${y}, ${daysInclusive(monthStart, monthEnd)} hari`,
  };
}

function daysInclusive(start: string, end: string): number {
  const [sy, sm, sd] = start.split('-').map(Number);
  const [ey, em, ed] = end.split('-').map(Number);
  return Math.round((Date.UTC(ey, em - 1, ed) - Date.UTC(sy, sm - 1, sd)) / 86_400_000) + 1;
}

/**
 * The period as a complete list of buckets, zeros included.
 *
 * The range is read back out of the same query the request was built from, so
 * the axis covers exactly what was asked for. Days stop at today, because a
 * month view has no business drawing bars for dates that have not happened;
 * hours do not, because "24 jam" means the whole clock face and an empty
 * evening is the answer to what the evening was like.
 */
function fillBuckets(
  points: TrafficPoint[],
  query: AnalyticsQuery,
  grain: 'hour' | 'day',
): TrafficPoint[] {
  const { start, end } = resolveRange(query);
  if (!start || !end || start > end) return points;

  const byBucket = new Map(points.map((p) => [p.bucket, p]));
  const out: TrafficPoint[] = [];

  for (let day = start; day <= end; day = shiftDays(day, 1)) {
    if (grain === 'day') {
      out.push(byBucket.get(day) ?? empty(day));
      continue;
    }
    for (let h = 0; h < 24; h++) {
      const bucket = `${day}T${String(h).padStart(2, '0')}:00`;
      out.push(byBucket.get(bucket) ?? empty(bucket));
    }
  }

  // Anything the server returned that the range did not predict is kept rather
  // than dropped: a figure on screen that the chart cannot place is a bug worth
  // seeing, and silently discarding it would hide it.
  const placed = new Set(out.map((p) => p.bucket));
  for (const p of points) if (!placed.has(p.bucket)) out.push(p);

  return out.sort((a, b) => a.bucket.localeCompare(b.bucket));
}

function empty(bucket: string): TrafficPoint {
  return {
    bucket,
    inbound_personal: 0,
    outbound_personal: 0,
    group_inbound: 0,
    group_outbound: 0,
  };
}

/**
 * The period's first and last WIB day, inclusive.
 *
 * The end is not trimmed back to today. A month view is meant to show the
 * month: the days still to come are drawn empty, the same way an evening that
 * has not happened yet is still one of the twenty-four hours on a day view.
 * Cutting the axis at "now" made the shape of the month change every hour.
 */
function resolveRange(q: AnalyticsQuery): { start: string; end: string } {
  const today = todayWIB();
  if (q.date) return { start: q.date, end: q.date };
  if (q.month) {
    const [y, m] = q.month.split('-').map(Number);
    // Day 0 of the next month is the last day of this one, which is the only
    // way to get 28, 29, 30 or 31 right without a table of month lengths.
    return { start: `${q.month}-01`, end: new Date(Date.UTC(y, m, 0)).toISOString().slice(0, 10) };
  }
  if (q.from || q.to) {
    // `to` is exclusive everywhere in the API, so the last day it covers is the
    // day before it.
    return { start: q.from ?? q.to!, end: q.to ? shiftDays(q.to, -1) : today };
  }
  return { start: today, end: today };
}

/** Plot height in pixels, shared by the bars, the gridlines and the axis. */
const PLOT = 168;

/**
 * Axis ticks rounded outward to a step a reader can do arithmetic with.
 *
 * 1, 2 or 5 times a power of ten: the steps people actually count in. A scale
 * topping out at 37 with four equal divisions would label the gridlines 9.25,
 * 18.5 and 27.75, which is a worse answer than no axis at all.
 */
function niceScale(peak: number): { scaleMax: number; ticks: number[] } {
  const target = 4;
  const rough = Math.max(1, peak) / target;
  const mag = 10 ** Math.floor(Math.log10(rough));
  const step = [1, 2, 5, 10].map((m) => m * mag).find((s) => s >= rough) ?? mag * 10;
  const scaleMax = Math.max(step, Math.ceil(Math.max(1, peak) / step) * step);
  const ticks: number[] = [];
  for (let t = 0; t <= scaleMax + 1e-9; t += step) ticks.push(Math.round(t));
  return { scaleMax, ticks };
}

/** How often to print an axis label, so many buckets do not become a smear. */
function labelEvery(count: number): number {
  if (count <= 14) return 1;
  if (count <= 32) return 3;
  return Math.ceil(count / 10);
}

/** The bucket as a short axis label: "08:00" for an hour, "17 Sep" for a day. */
function labelOf(bucket: string, grain: 'hour' | 'day'): string {
  if (grain === 'hour') return `${bucket.slice(11, 13)}:00`;
  const [, month, day] = bucket.split('-');
  const names = ['Jan', 'Feb', 'Mar', 'Apr', 'Mei', 'Jun', 'Jul', 'Agu', 'Sep', 'Okt', 'Nov', 'Des'];
  return `${Number(day)} ${names[Number(month) - 1] ?? ''}`;
}
