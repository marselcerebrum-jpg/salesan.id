'use client';

import clsx from 'clsx';
import { AlertTriangle, Loader2, X } from 'lucide-react';
import { useEffect, useState } from 'react';
import useSWR from 'swr';

import { AppChip } from '@/components/analytics/AppBadge';
import {
  EmptyState,
  ErrorState,
  RowSkeleton,
  formatDuration,
  formatTime,
} from '@/components/analytics/Primitives';
import { ApiError, analyticsPath, fetcher, locateConversation, type AnalyticsQuery } from '@/lib/api';
import { useRealtimeEvent } from '@/lib/realtime';
import type { SLACycleRow } from '@/lib/types';

/**
 * The conversations still waiting for a first manual reply.
 *
 * This is the one figure on the page that is not a report — it is a queue of
 * people who have not been answered yet. So it is ordered longest-wait-first,
 * and every row opens the actual room rather than describing it.
 *
 * The route is resolved by the server, not built here. Two reasons, and the
 * second is the important one:
 *
 *   - The inbox lives at /chat/{applicationId}/{accountId} with the thread
 *     chosen inside it. A link built from a phone number alone lands in the
 *     wrong room whenever the same customer has written to two of our numbers.
 *   - Asking the server makes it an authorization decision. A reader who may
 *     not open that application gets a refusal, instead of a URL that the chat
 *     page would then have to decline after the fact.
 *
 * The list is refreshed on the realtime metrics event, so answering a customer
 * removes them from here without a reload.
 */
export function WaitingPanel({
  open,
  query,
  onClose,
}: {
  open: boolean;
  query: AnalyticsQuery;
  onClose: () => void;
}) {
  const { data, error, isLoading, mutate } = useSWR<{ cycles: SLACycleRow[] }>(
    open ? analyticsPath('sla', query, { status: 'waiting', limit: '100' }) : null,
    fetcher,
  );

  // A reply closes the cycle server-side; this is what makes the row disappear
  // without anybody pressing refresh.
  useRealtimeEvent('metrics.updated', () => {
    if (open) void mutate();
  });

  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose();
    };
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, [open, onClose]);

  const rows = data?.cycles ?? [];

  return (
    <>
      <div
        aria-hidden={!open}
        onClick={onClose}
        className={clsx(
          'fixed inset-0 z-40 bg-ink/30 transition-opacity',
          open ? 'opacity-100' : 'pointer-events-none opacity-0',
        )}
      />
      <aside
        aria-hidden={!open}
        className={clsx(
          'fixed inset-y-0 right-0 z-50 flex w-full max-w-[720px] flex-col bg-surface shadow-e4 transition-transform',
          open ? 'translate-x-0' : 'translate-x-full',
        )}
      >
        <header className="flex items-center justify-between gap-3 border-b border-hairline bg-surface-raised px-5 py-3.5">
          <div>
            <h2 className="text-lg font-semibold tracking-[-0.01em] text-ink">Masih Menunggu Balasan</h2>
            <p className="text-xs text-ink-muted">
              Diurutkan dari yang paling lama menunggu.
            </p>
          </div>
          <button
            type="button"
            onClick={onClose}
            aria-label="Tutup"
            className="rounded-lg p-1.5 text-ink-muted transition-colors hover:bg-surface-sunken"
          >
            <X className="size-5" />
          </button>
        </header>

        <div className="flex-1 overflow-y-auto px-5 py-4">
          {error ? (
            <ErrorState
              message={error instanceof Error ? error.message : 'Terjadi kesalahan.'}
              onRetry={() => void mutate()}
            />
          ) : isLoading ? (
            <RowSkeleton count={5} />
          ) : rows.length === 0 ? (
            <EmptyState
              title="Tidak ada percakapan yang menunggu."
              hint="Semua pesan masuk pada periode ini sudah dibalas."
            />
          ) : (
            <ul className="space-y-2">
              {rows.map((r) => (
                <WaitingRow key={r.id} row={r} />
              ))}
            </ul>
          )}
        </div>
      </aside>
    </>
  );
}

function WaitingRow({ row }: { row: SLACycleRow }) {
  const [opening, setOpening] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function open() {
    setOpening(true);
    setError(null);
    try {
      const loc = await locateConversation(row.conversation_id);
      if (!loc.application_id) {
        setError('Nomor ini belum ditugaskan ke aplikasi mana pun.');
        return;
      }
      // The message that started the wait travels along, so the room opens on
      // the thing the customer is waiting for an answer to.
      const params = new URLSearchParams({ c: loc.conversation_id, m: row.inbound_message_id });
      window.location.href = `/chat/${loc.application_id}/${loc.account_id}?${params}`;
    } catch (e) {
      setError(
        e instanceof ApiError && e.status === 403
          ? 'Percakapan ini di luar aplikasi yang ditugaskan kepada akun Anda.'
          : 'Percakapan ini sudah tidak tersedia. Mungkin sudah dihapus dari inbox.',
      );
    } finally {
      setOpening(false);
    }
  }

  const breached = row.breached === true;

  return (
    <li
      className={clsx(
        'rounded-card border bg-surface-raised px-3.5 py-3',
        breached ? 'border-danger/30' : 'border-hairline',
      )}
    >
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <p className="truncate text-sm font-medium text-ink">
            {row.conversation_name ?? row.phone_number ?? 'Tanpa nama'}
          </p>
          <div className="mt-0.5 flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-ink-muted">
            {row.application_code ? (
              <AppChip code={row.application_code} color={row.application_color} />
            ) : null}
            {row.account_name ? <span>{row.account_name}</span> : null}
            {row.phone_number ? <span>{row.phone_number}</span> : null}
          </div>
        </div>

        <div className="shrink-0 text-right">
          <p
            className={clsx(
              'text-base font-semibold nums',
              breached ? 'text-danger' : 'text-warn',
            )}
          >
            {formatDuration(row.waiting_seconds)}
          </p>
          <p className="text-2xs text-ink-muted">
            target {formatDuration(row.target_seconds)}
          </p>
        </div>
      </div>

      <div className="mt-2 flex flex-wrap items-center justify-between gap-2">
        <p className="text-xs text-ink-muted">
          Pesan pertama {formatTime(row.started_at)}
          {row.inbound_message_count > 1 ? ` · ${row.inbound_message_count} pesan` : ''}
        </p>

        <div className="flex items-center gap-2">
          {breached ? (
            <span className="inline-flex items-center gap-1 text-xs font-medium text-danger">
              <AlertTriangle className="size-3.5" />
              Lewat SLA
            </span>
          ) : null}
          <button
            type="button"
            onClick={open}
            disabled={opening}
            className="inline-flex items-center gap-1.5 rounded-lg bg-brand-800 px-3 py-1.5 text-xs font-medium text-white transition-colors hover:bg-brand-900 disabled:opacity-50"
          >
            {opening ? <Loader2 className="size-3.5 animate-spin" /> : null}
            Buka chat
          </button>
        </div>
      </div>

      {error ? <p className="mt-1.5 text-xs text-danger">{error}</p> : null}
    </li>
  );
}
