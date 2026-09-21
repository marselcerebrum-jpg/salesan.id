'use client';

import { usePathname, useRouter, useSearchParams } from 'next/navigation';
import { useCallback, useMemo } from 'react';

import type { AnalyticsQuery } from '@/lib/api';

/**
 * The filter row's state, kept in the URL.
 *
 * Previously this lived in sessionStorage, which made a report impossible to
 * share: two people looking at "the same page" were looking at whatever each
 * of them had last clicked. A filtered view is a question, and a question
 * should have an address — so the query string is the single source of truth,
 * and pasting a link reproduces exactly what the sender was reading.
 *
 * Written with `replace` rather than `push`: changing a date should not fill
 * the back button with twenty near-identical entries. The browser's back button
 * then leaves the page, which is what people expect it to do.
 */

/**
 * Which subtab is open. Only a Leader ever has more than one.
 *
 * "freelance" is the flat list of every Freelance in reach, for the Leader who
 * wants to find one person without knowing which PIC they report to. It is the
 * same table the PIC walk ends at, entered from the other side.
 */
export type PerformanceTab = 'saya' | 'pic' | 'freelance';

/**
 * Personal work versus everything in reach.
 *
 * The same two words mean slightly different sets depending on who is asking —
 * a Leader's "gabungan" is the whole organisation, a PIC's is their team — but
 * it is one question either way, so it is one control.
 */
export type PerformanceMode = 'pribadi' | 'gabungan';

/** The non-filter parts of the URL: which tab, and how far into a team. */
export interface ViewState {
  tab: PerformanceTab;
  mode: PerformanceMode;
  /** The PIC a Leader has opened, if any. */
  pic?: string;
  /** Whether that PIC's own work or their whole team is being read. */
  picMode: PerformanceMode;
  /** The Freelance whose detail is open. */
  member?: string;
}

/**
 * What setView accepts.
 *
 * The empty string means "clear this", which is distinct from leaving a key out
 * — that means "leave it as it is". Without the distinction, walking back up a
 * team would either clear nothing or clear everything.
 */
export type ViewPatch = {
  tab?: PerformanceTab;
  mode?: PerformanceMode;
  pic?: string;
  picMode?: PerformanceMode;
  member?: string;
};

/** Every key this hook owns, so it can rewrite them without touching others. */
const FILTER_KEYS = [
  'date',
  'month',
  'from',
  'to',
  'chat_type',
  'application_id',
  'account_id',
  'pic_id',
  'freelance_id',
  'schedule_id',
  'in_schedule',
] as const;

const VIEW_KEYS = ['tab', 'mode', 'pic', 'picmode', 'member'] as const;

/**
 * The four keys that express a period. Exactly one of them is ever meaningful,
 * and the whole group has to be treated as a single choice: a default period
 * merged key-by-key with a chosen one produces a query that is neither.
 */
const PERIOD_KEYS = ['date', 'month', 'from', 'to'] as const;

export function useAnalyticsFilter(
  defaults: AnalyticsQuery = {},
): {
  query: AnalyticsQuery;
  view: ViewState;
  setQuery: (next: AnalyticsQuery) => void;
  setView: (next: ViewPatch) => void;
  reset: () => void;
} {
  const params = useSearchParams();
  const router = useRouter();
  const pathname = usePathname();

  const query = useMemo<AnalyticsQuery>(() => {
    const out: AnalyticsQuery = {};
    for (const key of FILTER_KEYS) {
      const value = params.get(key);
      if (value) (out as Record<string, string>)[key] = value;
    }

    // The period is taken as a whole, never merged.
    //
    // Merging is what broke this: the default was spread in first, so `date`
    // was always present, and the precedence rule below then deleted `month`
    // on every render. Choosing "Bulan" wrote month= to the URL and the query
    // still asked for today, so the filter looked dead and Rincian Per Hari
    // never left the current date.
    //
    // So: if the URL names a period, that period is the answer and the
    // default is not consulted at all. The default only fills a URL that has
    // said nothing about time yet.
    const chosen = PERIOD_KEYS.some((k) => params.get(k));
    if (!chosen) {
      for (const key of PERIOD_KEYS) {
        const value = (defaults as Record<string, string | undefined>)[key];
        if (value) (out as Record<string, string>)[key] = value;
      }
    }

    // Within a chosen period the more specific key still wins, which matters
    // while the URL is mid-rewrite between two shapes.
    if (out.date || out.from) delete out.month;

    // Everything that is not a period merges normally: those are independent
    // narrowings, not alternatives to each other.
    for (const [key, value] of Object.entries(defaults)) {
      if ((PERIOD_KEYS as readonly string[]).includes(key)) continue;
      if (value !== undefined && (out as Record<string, unknown>)[key] === undefined) {
        (out as Record<string, unknown>)[key] = value;
      }
    }

    return out;
    // `defaults` is a literal at every call site; spreading it into the
    // dependency list would rebuild this on every render for no reason.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [params]);

  const view = useMemo<ViewState>(() => {
    const mode = params.get('mode');
    const picMode = params.get('picmode');
    return {
      tab:
        params.get('tab') === 'pic'
          ? 'pic'
          : params.get('tab') === 'freelance'
            ? 'freelance'
            : 'saya',
      // Personal is the default everywhere: "what did I do" is the question
      // somebody opens this page with, and an aggregate shown first invites
      // them to read the team's work as their own.
      mode: mode === 'gabungan' ? 'gabungan' : 'pribadi',
      pic: params.get('pic') ?? undefined,
      picMode: picMode === 'gabungan' ? 'gabungan' : 'pribadi',
      member: params.get('member') ?? undefined,
    };
  }, [params]);

  const write = useCallback(
    (mutate: (next: URLSearchParams) => void) => {
      const next = new URLSearchParams(params.toString());
      mutate(next);
      const search = next.toString();
      router.replace(search ? `${pathname}?${search}` : pathname, { scroll: false });
    },
    [params, pathname, router],
  );

  const setQuery = useCallback(
    (value: AnalyticsQuery) => {
      write((next) => {
        for (const key of FILTER_KEYS) next.delete(key);
        for (const [key, raw] of Object.entries(value)) {
          if (raw) next.set(key, String(raw));
        }
      });
    },
    [write],
  );

  const setView = useCallback(
    (value: ViewPatch) => {
      write((next) => {
        // Walking out of a team clears what was open inside it, so the URL
        // never describes a member who is no longer being shown.
        for (const [key, raw] of [
          ['tab', value.tab],
          ['mode', value.mode],
          ['pic', value.pic],
          ['picmode', value.picMode],
          ['member', value.member],
        ] as const) {
          if (raw === undefined) continue;
          if (raw === '') next.delete(key);
          else next.set(key, raw);
        }
      });
    },
    [write],
  );

  const reset = useCallback(() => {
    write((next) => {
      for (const key of [...FILTER_KEYS, ...VIEW_KEYS]) next.delete(key);
    });
  }, [write]);

  return { query, view, setQuery, setView, reset };
}

/** The current month in WIB, which is what the page opens on. */
export function currentMonth(): string {
  // Not the browser's zone: somebody abroad must still see the month their
  // colleagues do.
  return new Date().toLocaleDateString('en-CA', { timeZone: 'Asia/Jakarta' }).slice(0, 7);
}

/** Today in WIB, as YYYY-MM-DD. */
export function todayWIB(): string {
  return new Date().toLocaleDateString('en-CA', { timeZone: 'Asia/Jakarta' });
}
