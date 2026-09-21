import { currentMonth, todayWIB } from '@/lib/useAnalyticsFilter';
import type { AnalyticsQuery } from '@/lib/api';

/*
 * The period, as the API speaks it.
 *
 * Plain functions, no component: the toolbar that used to live here was
 * replaced by a menu in the page header and a field row of its own, but the
 * arithmetic underneath it belongs to neither of them. One preset must resolve
 * to the same two dates wherever it is offered, so the resolving lives in one
 * place and the controls only call it.
 */
export type PresetID = 'today' | 'yesterday' | 'last7' | 'last30' | 'month' | 'custom';

export const PRESETS: { id: PresetID; label: string }[] = [
  { id: 'today', label: 'Hari Ini' },
  { id: 'yesterday', label: 'Kemarin' },
  { id: 'last7', label: '7 Hari' },
  { id: 'last30', label: '30 Hari' },
  { id: 'month', label: 'Bulan Ini' },
  { id: 'custom', label: 'Kustom' },
];

/** WIB calendar arithmetic, in the same YYYY-MM-DD strings the API speaks. */
export function shiftDays(iso: string, days: number): string {
  const [y, m, d] = iso.split('-').map(Number);
  const at = new Date(Date.UTC(y, m - 1, d));
  at.setUTCDate(at.getUTCDate() + days);
  return at.toISOString().slice(0, 10);
}

/** Turns a preset into the query keys the API understands. */
export function presetToQuery(id: PresetID, base: AnalyticsQuery): AnalyticsQuery {
  const rest = { ...base, date: undefined, month: undefined, from: undefined, to: undefined };
  const today = todayWIB();
  switch (id) {
    case 'today':
      return { ...rest, date: today };
    case 'yesterday':
      return { ...rest, date: shiftDays(today, -1) };
    case 'last7':
      // `to` is exclusive everywhere, so tomorrow's date closes today.
      return { ...rest, from: shiftDays(today, -6), to: shiftDays(today, 1) };
    case 'last30':
      return { ...rest, from: shiftDays(today, -29), to: shiftDays(today, 1) };
    case 'month':
      return { ...rest, month: currentMonth() };
    case 'custom':
      return { ...rest, from: base.from ?? today, to: base.to ?? shiftDays(today, 1) };
  }
}

/** Reads back which preset a query represents, so a pasted URL lands right. */
export function queryToPreset(q: AnalyticsQuery): PresetID {
  const today = todayWIB();
  if (q.date) return q.date === today ? 'today' : q.date === shiftDays(today, -1) ? 'yesterday' : 'custom';
  if (q.month) return q.month === currentMonth() ? 'month' : 'custom';
  if (q.from && q.to) {
    if (q.to === shiftDays(today, 1)) {
      if (q.from === shiftDays(today, -6)) return 'last7';
      if (q.from === shiftDays(today, -29)) return 'last30';
    }
    return 'custom';
  }
  // Half a range is still a custom range. Reading it as "today" was a trap:
  // clearing the end date made the preset jump back to Hari ini, which hid
  // the two date inputs, which left the half-set range on screen with no
  // control able to undo it.
  if (q.from || q.to) return 'custom';
  return 'today';
}

const MONTHS_SHORT = ['Jan', 'Feb', 'Mar', 'Apr', 'Mei', 'Jun', 'Jul', 'Agu', 'Sep', 'Okt', 'Nov', 'Des'];
const MONTHS_LONG = [
  'Januari', 'Februari', 'Maret', 'April', 'Mei', 'Juni',
  'Juli', 'Agustus', 'September', 'Oktober', 'November', 'Desember',
];

function fmtDay(iso: string): string {
  const [y, m, d] = iso.split('-').map(Number);
  return `${d} ${MONTHS_SHORT[m - 1] ?? ''} ${y}`;
}

/**
 * The period in words.
 *
 * The preset chips say which button is pressed; they do not say what dates that
 * turned into, and "30 Hari" plus a highlighted chip is not an answer to "what
 * am I looking at" on a screenshot, a printout, or a link somebody was sent.
 * A custom range was worse: readable only by reading two date inputs.
 */
export function periodLabel(q: AnalyticsQuery): string {
  if (q.month) {
    const [y, m] = q.month.split('-').map(Number);
    return `${MONTHS_LONG[m - 1] ?? ''} ${y}`;
  }
  if (q.from || q.to) {
    // `to` is exclusive everywhere in the API, so the last day it covers is the
    // day before it. Printing the raw value would claim a day that is not in
    // any of the figures.
    const from = q.from;
    const to = q.to ? shiftDays(q.to, -1) : undefined;
    if (from && to) {
      if (from === to) return fmtDay(from);
      const [fy, fm] = from.split('-');
      const [ty, tm] = to.split('-');
      if (fy === ty && fm === tm) return `${Number(from.split('-')[2])}–${fmtDay(to)}`;
      return `${fmtDay(from)} – ${fmtDay(to)}`;
    }
    if (from) return `Sejak ${fmtDay(from)}`;
    if (to) return `Sampai ${fmtDay(to)}`;
  }
  return fmtDay(q.date ?? todayWIB());
}

/** Whole days between two YYYY-MM-DD dates. */
function daysBetween(from: string, to: string): number {
  const [fy, fm, fd] = from.split('-').map(Number);
  const [ty, tm, td] = to.split('-').map(Number);
  return Math.round((Date.UTC(ty, tm - 1, td) - Date.UTC(fy, fm - 1, fd)) / 86_400_000);
}

/**
 * The period immediately before this one, of the same length.
 *
 * What a "+12%" is measured against. Stated as a real query rather than a
 * fudge factor, so the comparison is the same figures computed the same way
 * over the previous stretch of calendar: yesterday for today, last month for
 * this month, the seven days before these seven.
 *
 * Narrowings are carried over untouched. Comparing one PIC's week against the
 * whole team's previous week would be a number that means nothing.
 */
export function previousPeriod(q: AnalyticsQuery): AnalyticsQuery {
  const rest = { ...q, date: undefined, month: undefined, from: undefined, to: undefined };
  if (q.month) {
    const [y, m] = q.month.split('-').map(Number);
    const prev = m === 1 ? { y: y - 1, m: 12 } : { y, m: m - 1 };
    return { ...rest, month: `${prev.y}-${String(prev.m).padStart(2, '0')}` };
  }
  if (q.from && q.to) {
    const len = Math.max(1, daysBetween(q.from, q.to));
    return { ...rest, from: shiftDays(q.from, -len), to: q.from };
  }
  const day = q.date ?? todayWIB();
  return { ...rest, date: shiftDays(day, -1) };
}
