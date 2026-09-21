'use client';

import clsx from 'clsx';
import {
  CircleDashed,
  History,
  Loader2,
  MessageSquare,
  Radio,
  RotateCcw,
  Tag,
  Users,
} from 'lucide-react';
import Link from 'next/link';
import { useCallback, useEffect, useMemo, useState } from 'react';

import { AppChip } from '@/components/analytics/AppBadge';
import { Disclosure } from '@/components/analytics/Disclosure';
import {
  EmptyState,
  ErrorState,
  RowSkeleton,
  SOURCE_LABEL,
  StateBadge,
  formatTime,
} from '@/components/analytics/Primitives';
import {
  ApiError,
  activityPath,
  fetcher,
  locateConversation,
  type AnalyticsQuery,
  type ActivityPage,
} from '@/lib/api';
import { useRealtimeEvent } from '@/lib/realtime';
import type { ActivityRow } from '@/lib/types';

/**
 * What somebody actually did, read directly under their own figures.
 *
 * "Aktivitas" used to be a screen of its own, which was the wrong shape: nobody
 * wants a list of everything that happened, they want to know what this person
 * did — and that belongs next to the numbers it explains, not one navigation
 * step away from them.
 *
 * Every row can be followed to its source. A history you cannot open is a claim
 * rather than a record, so a chat activity opens the room, a campaign activity
 * opens the campaign, and a label activity opens the contact's label history.
 * Where the destination no longer exists the row says so instead of guessing.
 *
 * Paged by keyset, appended in place. The feed is a union of four tables and new
 * rows land at the top constantly; an offset would repeat or skip an entry every
 * time somebody replied while a page was being read.
 */

const PAGE = 30;

const KIND_ICON = {
  message_personal: MessageSquare,
  message_group: Users,
  label: Tag,
  follow_up: RotateCcw,
  broadcast: Radio,
  story: CircleDashed,
} as const;

/** The words a row is described with, in the vocabulary the rest of the app uses. */
function describe(a: ActivityRow): string {
  switch (a.kind) {
    case 'message_personal':
      return a.source === 'whatsapp_device' ? 'Balasan dari HP' : 'Membalas chat pribadi';
    case 'message_group':
      return a.source === 'whatsapp_device' ? 'Balasan grup dari HP' : 'Membalas chat grup';
    case 'follow_up':
      return a.type === 'answered' ? 'Follow-up: dibalas' : 'Follow-up: belum dibalas';
    case 'label':
      return LABEL_TEXT[a.type] ?? 'Perubahan label';
    case 'broadcast':
      return `Broadcast: ${CAMPAIGN_TEXT[a.type] ?? a.type}`;
    case 'story':
      return `WA Story: ${STORY_TEXT[a.type] ?? CAMPAIGN_TEXT[a.type] ?? a.type}`;
  }
}

const LABEL_TEXT: Record<string, string> = {
  label_assigned: 'Memasang label',
  label_removed: 'Melepas label',
  label_moved: 'Memindahkan label',
  label_created: 'Membuat label',
  label_updated: 'Mengubah label',
  label_deleted: 'Menghapus label',
};

const CAMPAIGN_TEXT: Record<string, string> = {
  draft_created: 'dibuat',
  schedule_created: 'dijadwalkan',
  schedule_updated: 'jadwal diubah',
  draft_updated: 'diubah',
  schedule_cancelled: 'dibatalkan',
  cancelled: 'dibatalkan',
  execution_started: 'mulai dikirim',
  published: 'selesai',
  partially_published: 'selesai sebagian',
  failed: 'gagal',
  retried: 'dicoba ulang',
  expired: 'kedaluwarsa',
};

const STORY_TEXT: Record<string, string> = {
  execution_started: 'mulai diposting',
  published: 'terposting',
  partially_published: 'terposting sebagian',
};

export function ActivityHistory({
  query,
  title = 'Riwayat Aktivitas',
  description,
}: {
  query: AnalyticsQuery;
  title?: string;
  description?: string;
}) {
  const [pages, setPages] = useState<ActivityRow[][]>([]);
  const [cursor, setCursor] = useState<{ before?: string; before_id?: string } | null>({});
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // The filter is the identity of this feed: when it changes, what is on screen
  // is answering a question nobody asked any more.
  const key = useMemo(() => JSON.stringify(query), [query]);

  const load = useCallback(
    async (from: { before?: string; before_id?: string }, replace: boolean, signal: AbortSignal) => {
      setLoading(true);
      setError(null);
      try {
        const page = await fetcher<ActivityPage>(activityPath(query, from, PAGE));
        if (signal.aborted) return;
        setPages((prev) => (replace ? [page.activities] : [...prev, page.activities]));
        setCursor(
          page.next_before
            ? { before: page.next_before, before_id: page.next_before_id }
            : null,
        );
      } catch (e) {
        if (signal.aborted) return;
        setError(e instanceof ApiError || e instanceof Error ? e.message : 'Gagal memuat.');
      } finally {
        if (!signal.aborted) setLoading(false);
      }
    },
    [query],
  );

  // A fast sequence of filter changes must not leave the slowest response on
  // screen. Aborting on cleanup is what stops an old answer from winning.
  useEffect(() => {
    const controller = new AbortController();
    setPages([]);
    setCursor({});
    void load({}, true, controller.signal);
    return () => controller.abort();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key]);

  // New work by this person appears without a reload. Only the first page is
  // refetched: appending at the top of a list somebody is reading further down
  // would move the ground under them.
  const refreshTop = useCallback(() => {
    const controller = new AbortController();
    void load({}, true, controller.signal);
  }, [load]);

  useRealtimeEvent('metrics.updated', refreshTop);
  useRealtimeEvent('campaign.updated', refreshTop);

  const rows = pages.flat();

  return (
    <Disclosure
      icon={History}
      title={title}
      description={
        description ?? 'Setiap baris dapat dibuka menuju sumbernya, sebatas hak akses akun Anda.'
      }
      summary={rows.length > 0 ? `${rows.length}${cursor ? '+' : ''} aktivitas` : undefined}
    >
      {error && rows.length === 0 ? (
        <ErrorState message={error} onRetry={refreshTop} />
      ) : loading && rows.length === 0 ? (
        <RowSkeleton count={5} />
      ) : rows.length === 0 ? (
        <EmptyState
          title="Belum ada aktivitas pada periode ini."
          hint="Periode, aplikasi, atau jenis chat yang dipilih tidak memuat aktivitas. Ubah salah satunya di baris filter di atas."
        />
      ) : (
        <>
          <ul className="space-y-1.5">
            {rows.map((a) => (
              <ActivityItem key={a.id} row={a} />
            ))}
          </ul>

          <div className="mt-3 flex items-center justify-center">
            {cursor ? (
              <button
                type="button"
                disabled={loading}
                onClick={() => {
                  const controller = new AbortController();
                  void load(cursor, false, controller.signal);
                }}
                className="inline-flex items-center gap-1.5 rounded-lg border border-hairline bg-surface-raised px-3.5 py-2 text-sm font-medium text-ink-soft transition-colors hover:bg-surface-sunken disabled:opacity-50"
              >
                {loading ? <Loader2 className="size-3.5 animate-spin" /> : null}
                Muat lebih banyak
              </button>
            ) : (
              <p className="text-xs text-ink-muted">Sudah sampai aktivitas terakhir.</p>
            )}
          </div>
        </>
      )}
    </Disclosure>
  );
}

function ActivityItem({ row }: { row: ActivityRow }) {
  const Icon = KIND_ICON[row.kind] ?? MessageSquare;
  const [opening, setOpening] = useState(false);
  const [linkError, setLinkError] = useState<string | null>(null);

  const target = useMemo(() => {
    if (row.campaign_id) {
      return { href: row.kind === 'story' ? '/story' : '/broadcast', label: 'Buka campaign' };
    }
    return null;
  }, [row.campaign_id, row.kind]);

  // A chat activity resolves its room through the server, so an entry the
  // reader may not open answers 403 rather than navigating them somewhere.
  async function openChat() {
    if (!row.conversation_id) return;
    setOpening(true);
    setLinkError(null);
    try {
      const loc = await locateConversation(row.conversation_id);
      if (!loc.application_id) {
        setLinkError('Nomor ini belum ditugaskan ke aplikasi mana pun.');
        return;
      }
      const params = new URLSearchParams({ c: loc.conversation_id });
      if (row.message_id) params.set('m', row.message_id);
      window.location.href = `/chat/${loc.application_id}/${loc.account_id}?${params}`;
    } catch (e) {
      setLinkError(
        e instanceof ApiError && e.status === 403
          ? 'Percakapan ini di luar aplikasi yang ditugaskan kepada Anda.'
          : 'Percakapan ini sudah tidak tersedia.',
      );
    } finally {
      setOpening(false);
    }
  }

  return (
    <li className="rounded-card border border-hairline bg-surface-raised px-3.5 py-2.5">
      <div className="flex items-start gap-2.5">
        <span className="mt-0.5 grid size-7 shrink-0 place-items-center rounded-lg bg-surface-sunken text-ink-muted">
          <Icon className="size-[15px]" />
        </span>

        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
            <span className="text-sm font-medium text-ink">{describe(row)}</span>
            {row.subject ? (
              <span className="min-w-0 truncate text-sm text-ink-soft">{row.subject}</span>
            ) : null}
            {row.in_schedule === false ? (
              <StateBadge label="Di Luar Jadwal" tone="warn" />
            ) : null}
            {row.status ? <StateBadge label={row.status} tone="neutral" /> : null}
          </div>

          {row.detail ? (
            <p className="mt-1 line-clamp-2 text-xs text-ink-soft">{row.detail}</p>
          ) : null}

          <div className="mt-1.5 flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-ink-muted">
            <span>{formatTime(row.occurred_at)}</span>
            <span aria-hidden>·</span>
            <span>
              {row.actor_name
                ? `${row.actor_name}${row.actor_role ? ` (${ROLE_TEXT[row.actor_role] ?? row.actor_role})` : ''}`
                : row.source
                  ? SOURCE_LABEL[row.source] ?? row.source
                  : 'Tanpa pelaku'}
            </span>
            {row.application_code ? (
              <>
                <span aria-hidden>·</span>
                <AppChip
                  code={row.application_code}
                  color={row.application_color}
                  name={row.application_name}
                />
              </>
            ) : null}
            {row.account_name ? (
              <>
                <span aria-hidden>·</span>
                <span>{row.account_name}</span>
              </>
            ) : null}
          </div>

          {linkError ? (
            <p className="mt-1.5 text-xs text-danger">{linkError}</p>
          ) : null}
        </div>

        <div className="shrink-0">
          {row.conversation_id ? (
            <button
              type="button"
              onClick={openChat}
              disabled={opening}
              className="inline-flex items-center gap-1 rounded-lg px-2 py-1 text-xs font-medium text-brand-700 transition-colors hover:bg-brand-600/10 disabled:opacity-50"
            >
              {opening ? <Loader2 className="size-3 animate-spin" /> : null}
              Buka chat
            </button>
          ) : target ? (
            <Link
              href={target.href}
              className={clsx(
                'inline-flex items-center rounded-lg px-2 py-1 text-xs font-medium',
                'text-brand-700 transition-colors hover:bg-brand-600/10',
              )}
            >
              {target.label}
            </Link>
          ) : null}
        </div>
      </div>
    </li>
  );
}

const ROLE_TEXT: Record<string, string> = {
  leader: 'Leader',
  pic: 'PIC',
  freelance: 'Freelance',
};
