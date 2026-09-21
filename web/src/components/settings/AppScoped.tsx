'use client';

import clsx from 'clsx';
import { useMemo, useState, type ReactNode } from 'react';
import useSWR from 'swr';

import { AppMark } from '@/components/analytics/AppBadge';
import { fetcher } from '@/lib/api';
import type { Application } from '@/lib/types';

/**
 * The shape shared by everything configured per application.
 *
 * Message variables and quick replies are different things that are filtered,
 * grouped and read identically, so the filtering and grouping live here once.
 * Both screens showing the same control in the same place is the point: an
 * operator who has learned one has learned the other.
 *
 * The grouping key is the application, and a row without one is not an
 * accident. It means "belongs to the company rather than to a brand", which
 * is a real and common case, so it gets its own group at the top instead of
 * being hidden or forced into a bucket it does not belong in.
 */

/** What both records have in common, as far as this file is concerned. */
export interface AppScopedRow {
  id: string;
  application_id: string | null;
  application_code: string | null;
  application_name: string | null;
  application_color: string | null;
  is_active: boolean;
}

const ALL_APPS = '__all__';
const NO_APP = '__none__';

/**
 * The filter row.
 *
 * Counts are shown against each option because the useful question here is
 * never "does this application exist" but "does it have any of these yet":
 * with a dozen brands, an empty one is worth seeing before it is opened.
 */
export function AppFilter({
  rows,
  value,
  onChange,
  applications,
}: {
  rows: AppScopedRow[];
  value: string;
  onChange: (next: string) => void;
  applications: Application[];
}) {
  const counts = useMemo(() => {
    const out = new Map<string, number>();
    for (const r of rows) {
      const key = r.application_id ?? NO_APP;
      out.set(key, (out.get(key) ?? 0) + 1);
    }
    return out;
  }, [rows]);

  const options = [
    { id: ALL_APPS, label: 'Semua', count: rows.length, color: null as string | null },
    ...(counts.get(NO_APP)
      ? [{ id: NO_APP, label: 'Semua aplikasi', count: counts.get(NO_APP) ?? 0, color: null }]
      : []),
    ...applications.map((a) => ({
      id: a.id,
      label: a.code,
      count: counts.get(a.id) ?? 0,
      color: a.color,
    })),
  ];

  return (
    <div className="flex flex-wrap items-center gap-1.5">
      {options.map((o) => {
        const on = value === o.id;
        return (
          <button
            key={o.id}
            type="button"
            onClick={() => onChange(o.id)}
            aria-pressed={on}
            className={clsx(
              'inline-flex h-9 items-center gap-1.5 rounded-control border px-2.5 text-sm font-medium transition-colors',
              on
                ? 'border-brand-600/40 bg-brand-600/10 text-brand-700'
                : 'border-hairline bg-surface-raised text-ink-soft hover:bg-surface-sunken',
            )}
          >
            {o.color ? (
              <span
                aria-hidden
                className="size-2 shrink-0 rounded-full"
                style={{ backgroundColor: o.color }}
              />
            ) : null}
            {o.label}
            <span className={clsx('nums text-2xs', on ? 'text-brand-700/70' : 'text-ink-muted')}>
              {o.count}
            </span>
          </button>
        );
      })}
    </div>
  );
}

/** Narrows a list to the chosen application. */
export function filterByApp<T extends AppScopedRow>(rows: T[], value: string): T[] {
  if (value === ALL_APPS) return rows;
  if (value === NO_APP) return rows.filter((r) => r.application_id === null);
  return rows.filter((r) => r.application_id === value);
}

export interface AppGroup<T> {
  key: string;
  code: string | null;
  name: string;
  color: string | null;
  rows: T[];
}

/**
 * Buckets rows by application, workspace-wide first.
 *
 * Sorted by code rather than by name because the code is what the mark shows
 * and therefore what the eye is scanning for.
 */
export function groupByApp<T extends AppScopedRow>(rows: T[]): AppGroup<T>[] {
  const byKey = new Map<string, AppGroup<T>>();
  for (const r of rows) {
    const key = r.application_id ?? NO_APP;
    let group = byKey.get(key);
    if (!group) {
      group = {
        key,
        code: r.application_code,
        name: r.application_name ?? (r.application_id ? 'Aplikasi' : 'Semua aplikasi'),
        color: r.application_color,
        rows: [],
      };
      byKey.set(key, group);
    }
    group.rows.push(r);
  }
  return [...byKey.values()].sort((a, b) => {
    if (a.key === NO_APP) return -1;
    if (b.key === NO_APP) return 1;
    return (a.code ?? '').localeCompare(b.code ?? '');
  });
}

/** One application's heading above its rows. */
export function AppGroupHeader({ group }: { group: AppGroup<unknown> }) {
  return (
    <div className="mb-2 flex items-center gap-2">
      {group.key === NO_APP ? (
        <span className="grid size-6 shrink-0 place-items-center rounded-control bg-surface-sunken text-2xs font-bold text-ink-soft">
          ∗
        </span>
      ) : (
        <AppMark code={group.code} color={group.color} size={24} />
      )}
      <span className="text-base font-semibold text-ink">{group.name}</span>
      <span className="nums text-xs text-ink-muted">{group.rows.length}</span>
    </div>
  );
}

/**
 * The application picker used in both forms.
 *
 * "Semua aplikasi" is offered only to a Leader, matching the API: a PIC
 * writing something visible to brands they do not hold would be reaching past
 * their own remit, so the option is absent rather than present and refused.
 */
export function AppSelect({
  value,
  onChange,
  applications,
  canPickAll,
  className,
}: {
  value: string;
  onChange: (next: string) => void;
  applications: Application[];
  canPickAll: boolean;
  className?: string;
}) {
  return (
    <select
      aria-label="Aplikasi"
      value={value}
      onChange={(e) => onChange(e.target.value)}
      className={className}
    >
      {canPickAll ? <option value="">Semua aplikasi</option> : <option value="">Pilih aplikasi</option>}
      {applications.map((a) => (
        <option key={a.id} value={a.id}>
          {a.code} · {a.name}
        </option>
      ))}
    </select>
  );
}

/** The applications this reader may attach things to. */
export function useApplications(): Application[] {
  const { data } = useSWR<{ applications: Application[] }>('/applications', fetcher);
  return useMemo(
    () => [...(data?.applications ?? [])].sort((a, b) => a.code.localeCompare(b.code)),
    [data],
  );
}

/** Shared page frame, so the two screens are laid out identically. */
export function SettingsPageShell({
  title,
  description,
  actions,
  children,
}: {
  title: string;
  description: string;
  actions?: ReactNode;
  children: ReactNode;
}) {
  return (
    <div className="px-5 py-6 lg:px-8 lg:py-8">
      <div className="mx-auto w-full max-w-[1280px]">
        <header className="flex flex-wrap items-start justify-between gap-3">
          <div>
            <h1 className="text-2xl font-semibold tracking-[-0.02em] text-ink">{title}</h1>
            <p className="mt-1 max-w-[620px] text-sm text-ink-muted">{description}</p>
          </div>
          {actions}
        </header>
        {children}
      </div>
    </div>
  );
}

export { ALL_APPS, NO_APP };

/** Local state for the filter, so neither page has to repeat it. */
export function useAppFilter() {
  return useState<string>(ALL_APPS);
}
