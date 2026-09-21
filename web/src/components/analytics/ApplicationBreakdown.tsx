'use client';

import { LayoutGrid, X } from 'lucide-react';
import { useMemo, useState } from 'react';
import useSWR from 'swr';

import { AppMark } from '@/components/analytics/AppBadge';
import { Disclosure } from '@/components/analytics/Disclosure';
import { GroupedBreakdown, type GroupedRow } from '@/components/analytics/GroupedBreakdown';
import { ErrorState, RowSkeleton } from '@/components/analytics/Primitives';
import {
  fetcher,
  performanceByApplicationPath,
  type AnalyticsQuery,
} from '@/lib/api';
import type { ApplicationPerformance } from '@/lib/types';

/**
 * The same period, one row per application.
 *
 * Built on the Rincian Per Hari column model, like the per-person tables: days,
 * people and applications are the same forty-one measurements asked of a
 * different subject, so they are one table with a different first column rather
 * than three that drift.
 *
 * The row is also the control. Pressing one filters the whole page to that
 * application, pressing it again clears the filter, which is why there is no
 * second dropdown duplicating it.
 *
 * The request deliberately drops `application_id`. The table always lists every
 * application in reach, including while one of them is selected, because a
 * comparison that hides everything except the row you already chose is not a
 * comparison.
 *
 * What the figures mean follows whatever narrowing is active above: on a
 * personal view they are what that account did, on a combined view they are
 * everything that happened on the application. The line under the heading says
 * which, because the two are easy to confuse and a reader who confuses them
 * reads every number wrong.
 */
export function ApplicationBreakdown({
  query,
  onPick,
  defaultOpen = false,
  hideWhenSingle = true,
  personal: personalProp,
}: {
  query: AnalyticsQuery;
  /** Sets or clears the page's application filter. */
  onPick?: (applicationId: string | null) => void;
  defaultOpen?: boolean;
  /**
   * Whether these figures are one account's work rather than an application's.
   *
   * Usually inferred from the filter, but a Freelance is narrowed to their own
   * account by the server rather than by a dropdown: nothing in the query says
   * so, and without this the table would tell them the numbers include work
   * they never did.
   */
  personal?: boolean;
  /**
   * Whether a workspace with one application draws nothing.
   *
   * True on Performa, where this table exists to compare brands and a
   * comparison of one is a row pretending to be a table. False on the
   * Dashboard, where a single brand's figures are still the answer to "what is
   * happening" and a gap where a section belongs reads as broken.
   */
  hideWhenSingle?: boolean;
}) {
  // Without the application filter: this table is what selects it, so asking
  // the server for "the application already chosen" would return one row and
  // make the other applications unreachable from here.
  const listQuery = useMemo<AnalyticsQuery>(
    () => ({ ...query, application_id: undefined }),
    [query],
  );
  const { data, error, isLoading, mutate } = useSWR<{ applications: ApplicationPerformance[] }>(
    performanceByApplicationPath(listQuery),
    fetcher,
    { refreshInterval: 300_000, keepPreviousData: true },
  );

  const apps = useMemo(() => data?.applications ?? [], [data]);
  const active = query.application_id ?? null;

  // Personal when the view is narrowed to one account, combined otherwise. The
  // page sets exactly one of these, so reading them back is how this table
  // states what its own numbers mean without being told twice.
  const personal = personalProp ?? Boolean(query.freelance_id);

  /*
   * On a person's view, an application they did nothing in is noise.
   *
   * The table lists every application in reach, which is right for a combined
   * view â€” that is the comparison. Read as one account's work it is the
   * opposite: seven rows of zeroes above the one row that means something, and
   * the whole thing stops looking like it is about that person at all.
   *
   * Hidden rather than dropped, with a count and a way back, because "did
   * nothing here" is itself an answer somebody may want to check.
   */
  const [showIdle, setShowIdle] = useState(false);
  const worked = useMemo(
    () => apps.filter((a) => hasActivity(a.summary)),
    [apps],
  );
  const idle = apps.length - worked.length;
  const shown = personal && !showIdle ? worked : apps;

  const rows: GroupedRow[] = shown.map(
    ({ application: app, summary, contacts_total: contacts }) => {
      const selected = active === app.id;
      return {
        id: app.id,
        label: app.name,
        summary,
        selected,
        onClick: onPick ? () => onPick(selected ? null : app.id) : undefined,
        subject: (
          <span className="flex min-w-0 items-center gap-2.5">
            <AppMark code={app.code} color={app.color} size={26} />
            <span className="min-w-0">
              <span className="block truncate font-medium text-ink">{app.name}</span>
              {/* The address book as it stands, under the name rather than in a
                  column of its own: every column in this table is a measurement
                  of the period, and a "now" figure among them would be read as
                  one. */}
              <span
                className="block truncate text-2xs text-ink-muted"
                title={`${contacts.toLocaleString('id-ID')} kontak tersimpan saat ini. Tidak mengikuti filter periode.`}
              >
                {selected ? 'sedang difilter' : app.code} Â· {contacts.toLocaleString('id-ID')} kontak
              </span>
            </span>
          </span>
        ),
      };
    },
  );

  if (error && apps.length === 0) {
    return (
      <section className="mt-8">
        <ErrorState
          message={error instanceof Error ? error.message : 'Gagal memuat rincian per aplikasi.'}
          onRetry={() => void mutate()}
        />
      </section>
    );
  }

  if (isLoading && apps.length === 0) {
    return (
      <section className="mt-8">
        <RowSkeleton count={3} />
      </section>
    );
  }

  // One application is not a breakdown, so the table hides itself rather than
  // drawing a comparison with nothing to compare against. The cards stay: on
  // the Dashboard a single brand's figures are still the answer to "what is
  // happening", and an empty space where a section belongs reads as broken.
  if (apps.length === 0 || (apps.length < 2 && hideWhenSingle)) return null;

  const caveat = personal
    ? 'Angkanya adalah yang dikerjakan akun ini, bukan seluruh aktivitas di aplikasi itu.'
    : 'Angkanya adalah seluruh aktivitas di aplikasi itu, termasuk pesan dari HP, bukan hanya kerja satu orang.';

  const actions =
    active && onPick ? (
      <button
        type="button"
        onClick={() => onPick(null)}
        className="inline-flex items-center gap-1.5 rounded-control border border-hairline bg-surface-raised px-2.5 py-1.5 text-xs font-medium text-ink-soft transition-colors hover:bg-surface-sunken"
      >
        <X className="size-3.5" />
        Tampilkan semua aplikasi
      </button>
    ) : null;

  const body = (
    <>
      {/* The caveat the heading has no room for: whose work these numbers are. */}
      <p className="mb-3 text-xs leading-relaxed text-ink-muted">{caveat}</p>

      {shown.length === 0 ? (
        <p className="py-3 text-sm text-ink-muted">
          Akun ini belum mengerjakan apa pun di aplikasi mana pun pada periode ini.
        </p>
      ) : (
        <GroupedBreakdown rows={rows} subjectLabel="Aplikasi" csvName="rincian-per-aplikasi" />
      )}

      {personal && idle > 0 ? (
        <button
          type="button"
          onClick={() => setShowIdle((v) => !v)}
          className="mt-2 rounded-lg text-xs font-medium text-brand-700 transition-colors hover:text-brand-800"
        >
          {showIdle
            ? `Sembunyikan ${idle} aplikasi tanpa aktivitas`
            : `Tampilkan ${idle} aplikasi tanpa aktivitas`}
        </button>
      ) : null}

      {/* Stated rather than solved with a total row. Contacts are counted
          distinct within each application, and one customer who writes to two
          of our numbers is a real contact in both, so a "total" row would be a
          number that is true of nothing. */}
      <p className="mt-2 text-xs leading-relaxed text-ink-muted">
        Kontak dihitung unik di dalam tiap aplikasi. Menjumlahkan baris di sini tidak selalu sama
        dengan angka gabungan di atas, karena satu pelanggan yang menghubungi dua aplikasi terhitung
        pada keduanya.
      </p>
    </>
  );

  return (
    <Disclosure
      defaultOpen={defaultOpen}
      icon={LayoutGrid}
      title={personal ? 'Pekerjaan Per Aplikasi' : 'Rincian Per Aplikasi'}
      description={
        personal
          ? 'Apa saja yang dikerjakan akun ini di tiap aplikasi. Tekan satu baris untuk memfilter seluruh halaman ke aplikasi itu.'
          : 'Periode yang sama, dipecah per aplikasi. Tekan satu baris untuk memfilter seluruh halaman ke aplikasi itu.'
      }
      summary={
        personal && !showIdle
          ? `${shown.length} dari ${apps.length} aplikasi`
          : `${apps.length} aplikasi`
      }
      actions={actions}
    >
      {body}
    </Disclosure>
  );
}

/**
 * Whether this account touched this application at all in the period.
 *
 * Deliberately broad: chats, groups, labels and campaigns all count. Somebody
 * who only moved labels in an application still worked in it, and hiding that
 * row because no message went out would be answering a narrower question than
 * the one the table asks.
 */
function hasActivity(s: ApplicationPerformance['summary']): boolean {
  return (
    s.inbound_personal > 0 ||
    s.outbound_manual_personal > 0 ||
    s.outbound_device_personal > 0 ||
    s.group_inbound > 0 ||
    s.group_replies > 0 ||
    s.label_changes_total > 0 ||
    s.follow_ups > 0 ||
    s.broadcasts_created > 0 ||
    s.stories_created > 0 ||
    s.work_seconds > 0
  );
}
