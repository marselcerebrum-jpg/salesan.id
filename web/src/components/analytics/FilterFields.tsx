'use client';

import clsx from 'clsx';
import { RotateCcw } from 'lucide-react';
import useSWR from 'swr';

import type { ReactNode } from 'react';

import { analyticsFiltersPathFor, fetcher, type AnalyticsQuery } from '@/lib/api';
import { inputClassSm } from '@/components/ui/control';
import type { AppRef, FilterOptions } from '@/lib/types';

/**
 * Who, where, and what kind: every narrowing except the period.
 *
 * Lifted out of the toolbar so two screens can arrange them differently without
 * owning two copies of the list. Performa keeps them folded behind a button,
 * because the period is what somebody changes there twenty times an hour and
 * these maybe once; the Dashboard leaves them open, because on a screen that
 * covers every application at once "which slice am I looking at" is the first
 * question, not an advanced one.
 *
 * One grid, every cell the same width, every control filling its cell. Sized
 * from their longest option they read as five unrelated things rather than one
 * panel.
 */
export function FilterFields({
  value,
  onChange,
  className,
  people = true,
  applications,
  leading,
}: {
  value: AnalyticsQuery;
  onChange: (next: AnalyticsQuery) => void;
  className?: string;
  /**
   * The applications to offer, when the view is already about somebody.
   *
   * Without this the list is every application the reader may see, which on a
   * page reading one person means offering to filter their figures by an
   * application they do not hold — a filter whose only possible result is
   * zeroes. Narrowing it to what they hold makes every option answerable.
   */
  applications?: AppRef[];
  /**
   * A field to place before the rest, in the same grid.
   *
   * Passed rather than rendered above, so the person picker on Performa is one
   * of the row's fields instead of a control floating over it at a different
   * width.
   */
  leading?: ReactNode;
  /**
   * Whether the PIC and Freelance pickers are offered.
   *
   * False on the Dashboard, which answers "how is the operation" and is read
   * across every person at once; who did what is Performa's question, and
   * having the same two dropdowns on both screens made it look as though the
   * Dashboard were a second, weaker Performa.
   */
  people?: boolean;
}) {
  const { data, error, mutate } = useSWR<FilterOptions>(
    analyticsFiltersPathFor(value.pic_id),
    fetcher,
  );

  const role = data?.role ?? '';
  const showPIC = people && (role === 'leader' || role === '') && (data?.pics.length ?? 0) > 0;
  const showFreelance = people && role !== 'freelance' && (data?.freelancers.length ?? 0) > 0;

  const apps = applications ?? data?.applications ?? [];

  /*
   * Numbers follow the applications on offer, and then the one chosen.
   *
   * Two narrowings, in that order: a page about one person lists the numbers
   * of the applications they hold, and choosing one application narrows it to
   * that application's numbers. Without the first, a PIC's page offers every
   * number in the workspace as something to filter their own figures by.
   */
  const reachable = new Set(apps.map((a) => a.id));
  const accounts = (data?.accounts ?? []).filter((a) => {
    if (value.application_id) return a.application_id === value.application_id;
    return applications ? reachable.has(a.application_id ?? '') : true;
  });

  function set<K extends keyof AnalyticsQuery>(key: K, raw: string) {
    onChange({ ...value, [key]: raw || undefined });
  }

  return (
    <>
      {error ? (
        <div className="mb-3 flex items-center justify-between gap-3 text-sm text-danger">
          <span>Pilihan filter gagal dimuat.</span>
          <button
            type="button"
            onClick={() => void mutate()}
            className="inline-flex items-center gap-1 font-medium underline-offset-2 hover:underline"
          >
            <RotateCcw className="size-3.5" />
            Coba lagi
          </button>
        </div>
      ) : null}

      <div
        className={clsx(
          'grid gap-x-4 gap-y-3 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-5',
          className,
        )}
      >
        {leading}

        {showPIC ? (
          <Field label="PIC">
            <select
              value={value.pic_id ?? ''}
              onChange={(e) =>
                /*
                 * Everything under a PIC is re-scoped to them.
                 *
                 * Committed, not just drafted. This used to set local state and
                 * stop there, so picking a PIC moved the dropdown and left every
                 * figure on the page reporting the old scope.
                 */
                onChange({
                  ...value,
                  pic_id: e.target.value || undefined,
                  freelance_id: undefined,
                  application_id: undefined,
                  account_id: undefined,
                })
              }
              className={selectClass}
            >
              <option value="">Semua PIC</option>
              {data?.pics.map((p) => (
                <option key={p.id} value={p.id}>
                  {p.name}
                </option>
              ))}
            </select>
          </Field>
        ) : null}

        {showFreelance ? (
          <Field label="Freelance">
            <select
              value={value.freelance_id ?? ''}
              onChange={(e) => set('freelance_id', e.target.value)}
              className={selectClass}
            >
              <option value="">
                {value.pic_id ? 'Semua Freelance PIC ini' : 'Semua Freelance'}
              </option>
              {data?.freelancers.map((p) => (
                <option key={p.id} value={p.id}>
                  {p.name}
                </option>
              ))}
            </select>
          </Field>
        ) : null}

        <Field label="Aplikasi">
          <select
            value={value.application_id ?? ''}
            onChange={(e) =>
              onChange({
                ...value,
                application_id: e.target.value || undefined,
                account_id: undefined,
              })
            }
            className={selectClass}
          >
            <option value="">Semua aplikasi</option>
            {apps.map((a) => (
              <option key={a.id} value={a.id}>
                {a.code} · {a.name}
              </option>
            ))}
          </select>
        </Field>

        <Field label="Nomor WhatsApp">
          <select
            value={value.account_id ?? ''}
            onChange={(e) => set('account_id', e.target.value)}
            className={selectClass}
          >
            <option value="">Semua nomor</option>
            {accounts.map((a) => (
              <option key={a.id} value={a.id}>
                {a.name}
              </option>
            ))}
          </select>
        </Field>

        <Field label="Jenis percakapan">
          <select
            value={value.chat_type ?? ''}
            onChange={(e) => set('chat_type', e.target.value)}
            className={selectClass}
          >
            <option value="">Semua</option>
            <option value="personal">Chat pribadi</option>
            <option value="group">Chat grup</option>
          </select>
        </Field>
      </div>
    </>
  );
}

/**
 * Full width inside its grid cell.
 *
 * `inputClassSm` deliberately carries no width: it is used in forms where a
 * field is sized to what it holds, and a minutes box has no business being as
 * wide as the page. In a filter grid the opposite is true, so the width is
 * added here rather than changed there.
 */
const selectClass = `w-full min-w-0 ${inputClassSm}`;

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label className="block min-w-0">
      <span className="mb-1.5 block truncate text-2xs font-medium text-ink-muted">{label}</span>
      {children}
    </label>
  );
}
