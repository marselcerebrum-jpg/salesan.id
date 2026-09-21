'use client';

import useSWR from 'swr';

import { GroupedBreakdown, type GroupedRow } from '@/components/analytics/GroupedBreakdown';
import { EmptyState, ErrorState, RowSkeleton } from '@/components/analytics/Primitives';
import { fetcher, memberBreakdownPath, type AnalyticsQuery } from '@/lib/api';
import type { MemberBreakdown, OperationalRole } from '@/lib/types';

/**
 * One person per row, measured exactly the way a day is measured.
 *
 * A PIC's row is their whole application: their own work, their Freelance's,
 * and the phone activity on those numbers. That is what a PIC is answerable
 * for, and it is the same narrowing the "Gabungan Tim PIC" toggle applies, so
 * opening a row shows the figures the row promised. A Freelance's row is what
 * their own account did.
 *
 * The distinction is stated under the heading rather than left for the reader
 * to infer, because a table where two kinds of row mean two different things
 * and says so is honest, and one that stays quiet about it is a trap.
 */
export function MemberBreakdownTable({
  query,
  role,
  title,
  description,
  subjectLabel,
  csvName,
  emptyTitle,
  emptyHint,
  onOpen,
}: {
  query: AnalyticsQuery;
  /** Which role the table draws. The server computes only these rows. */
  role: Extract<OperationalRole, 'pic' | 'freelance'>;
  title: string;
  description: string;
  subjectLabel: string;
  csvName: string;
  emptyTitle: string;
  emptyHint?: string;
  onOpen?: (member: MemberBreakdown) => void;
}) {
  const { data, error, isLoading, mutate } = useSWR<{ members: MemberBreakdown[] }>(
    memberBreakdownPath(query, role),
    fetcher,
    { refreshInterval: 300_000, keepPreviousData: true },
  );

  const members = data?.members ?? [];

  const rows: GroupedRow[] = members.map((m) => ({
    id: m.user_id ?? m.email,
    label: m.name,
    summary: m.summary,
    dim: !m.is_active,
    onClick: onOpen && m.user_id ? () => onOpen(m) : undefined,
    subject: (
      <span className="flex min-w-0 items-center gap-2">
        {/* A dot rather than a column: whether somebody is on shift is one bit,
            and spending a whole column of a forty-one column table on one bit
            would push a real metric off the screen. */}
        <span
          aria-hidden
          title={m.on_duty ? 'Bertugas' : 'Tidak bertugas'}
          className={
            m.on_duty
              ? 'size-1.5 shrink-0 rounded-full bg-brand-600'
              : 'size-1.5 shrink-0 rounded-full bg-ink-muted/35'
          }
        />
        <span className="min-w-0">
          <span className="block truncate font-medium text-ink">{m.name}</span>
          <span className="block truncate text-2xs text-ink-muted">
            {m.scope === 'team' ? `${m.freelance_count} Freelance · tim` : m.email}
          </span>
        </span>
      </span>
    ),
  }));

  return (
    <section className="mt-8">
      <div className="mb-3">
        <h2 className="text-lg font-semibold tracking-[-0.01em] text-ink">{title}</h2>
        <p className="mt-0.5 text-sm text-ink-muted">{description}</p>
      </div>

      {error && members.length === 0 ? (
        <ErrorState
          message={error instanceof Error ? error.message : 'Gagal memuat.'}
          onRetry={() => void mutate()}
        />
      ) : isLoading && members.length === 0 ? (
        <RowSkeleton count={3} />
      ) : members.length === 0 ? (
        <EmptyState title={emptyTitle} hint={emptyHint} />
      ) : (
        <GroupedBreakdown rows={rows} subjectLabel={subjectLabel} csvName={csvName} />
      )}
    </section>
  );
}
