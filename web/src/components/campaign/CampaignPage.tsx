'use client';

import clsx from 'clsx';
import {
  CalendarDays,
  CheckCircle2,
  ChevronDown,
  CircleSlash,
  Clock,
  Eye,
  LayoutGrid,
  List,
  Play,
  Plus,
  RefreshCw,
  RotateCcw,
  Search,
  Send,
  Smartphone,
  X,
  XCircle,
} from 'lucide-react';
import Link from 'next/link';
import { useEffect, useMemo, useRef, useState } from 'react';
import useSWR from 'swr';

import { inputClassSm } from '@/components/ui/control';

import { EmptyState, ErrorState, PageShell, RowSkeleton } from '@/components/analytics/Primitives';
import { statusLabel } from '@/components/campaign/shared';
import { StoryGrid } from '@/components/campaign/StoryGrid';
import {
  campaignFacetsPath,
  campaignsPath,
  fetcher,
  type CampaignFacet,
} from '@/lib/api';
import { useRealtimeEvent } from '@/lib/realtime';
import type { AnalyticsScope, Campaign, CampaignType } from '@/lib/types';

/**
 * The Broadcast and WA Story lists.
 *
 * A table rather than a stack of cards. Two hundred campaigns is a list somebody
 * scans down one column at a time — which one failed, which is still scheduled,
 * which went to the most people — and cards put every field in a different place
 * on every row, so the eye has to start again each time.
 *
 * The chips above it count campaigns per application. They are counted by the
 * same query that fills the table, minus the application filter itself, so a
 * chip can never claim a number the table below it does not show, and pressing
 * one does not leave every other brand reading zero.
 */

const STATUS_TABS: { id: string; label: string; icon: typeof Clock | null }[] = [
  { id: '', label: 'Semua', icon: null },
  { id: 'scheduled', label: 'Terjadwal', icon: Clock },
  { id: 'running', label: 'Berjalan', icon: Play },
  { id: 'completed', label: 'Selesai', icon: CheckCircle2 },
  { id: 'partial', label: 'Sebagian', icon: CircleSlash },
  { id: 'failed', label: 'Gagal', icon: XCircle },
];

/** The icon on a status chip, in the colour that status deserves. */
const STATUS_ICON_TONE: Record<string, string> = {
  scheduled: 'text-info',
  running: 'text-info',
  completed: 'text-brand-600',
  partial: 'text-warn',
  failed: 'text-danger',
};

/** The dot beside a status, in the colour the status deserves. */
const STATUS_TONE: Record<string, string> = {
  draft: 'bg-ink-muted',
  scheduled: 'bg-info',
  running: 'bg-info',
  completed: 'bg-brand-600',
  partial: 'bg-warn',
  failed: 'bg-danger',
  cancelled: 'bg-ink-muted',
  expired: 'bg-ink-muted',
};

export function CampaignPage({ type }: { type: CampaignType }) {
  const isStory = type === 'story';
  const [status, setStatus] = useState('');
  const [applicationID, setApplicationID] = useState('');
  const [search, setSearch] = useState('');
  const [debounced, setDebounced] = useState('');
  const [from, setFrom] = useState('');
  const [to, setTo] = useState('');
  const [view, setView] = useState<'grid' | 'list'>(isStory ? 'grid' : 'list');

  useEffect(() => {
    const timer = setTimeout(() => setDebounced(search.trim()), 300);
    return () => clearTimeout(timer);
  }, [search]);

  const query = {
    type,
    status: status || undefined,
    application_id: applicationID || undefined,
    search: debounced || undefined,
    from: from || undefined,
    to: to || undefined,
  };

  const { data, error, isLoading, mutate } = useSWR<{
    campaigns: Campaign[];
    scope: AnalyticsScope;
  }>(campaignsPath(query), fetcher, { refreshInterval: 30_000, keepPreviousData: true });

  const facets = useSWR<{
    total: number;
    applications: CampaignFacet[];
    statuses: Record<string, number>;
  }>(
    campaignFacetsPath({
      type,
      status: status || undefined,
      search: debounced || undefined,
      application_id: applicationID || undefined,
    }),
    fetcher,
  );

  useRealtimeEvent('campaign.updated', () => {
    void mutate();
    void facets.mutate();
  });

  const campaigns = data?.campaigns ?? [];
  const total = facets.data?.total ?? 0;
  const chips = useMemo(() => facets.data?.applications ?? [], [facets.data]);
  const statuses = useMemo(() => facets.data?.statuses ?? {}, [facets.data]);
  const colorOf = useMemo(
    () => new Map(chips.filter((f) => f.code).map((f) => [f.code, f.color ?? null])),
    [chips],
  );

  const clearAll = () => {
    setStatus('');
    setApplicationID('');
    setSearch('');
    setFrom('');
    setTo('');
  };

  /** What is narrowing the list right now, each with its own way off. */
  const active: { label: string; clear: () => void }[] = [];
  if (applicationID) {
    const app = chips.find((f) => f.id === applicationID);
    active.push({
      label: `Aplikasi: ${app?.code || app?.name || 'dipilih'}`,
      clear: () => setApplicationID(''),
    });
  }
  if (from || to) {
    active.push({
      label: `Tanggal: ${from ? formatDay(from) : '…'} – ${to ? formatDay(to) : '…'}`,
      clear: () => {
        setFrom('');
        setTo('');
      },
    });
  }
  if (debounced) {
    active.push({ label: `Kata: ${debounced}`, clear: () => setSearch('') });
  }

  return (
    <PageShell>
      <header className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h1 className="text-2xl font-semibold tracking-[-0.02em] text-ink">
            {isStory ? 'WA Story' : 'Broadcast'}
          </h1>
          <p className="mt-0.5 text-sm text-ink-muted">
            Kelola dan pantau semua {isStory ? 'story' : 'pesan broadcast'} WhatsApp Anda.
          </p>
        </div>

        <div className="flex items-center gap-2">
          <Link
            href={isStory ? '/story/baru' : '/broadcast/baru'}
            className="inline-flex h-9 items-center justify-center gap-1.5 rounded-control bg-brand-800 px-3.5 text-sm font-medium text-white transition-colors hover:bg-brand-900"
          >
            <Plus className="size-4" />
            {isStory ? 'Buat Story' : 'Buat Broadcast'}
          </Link>
          <button
            type="button"
            onClick={() => {
              void mutate();
              void facets.mutate();
            }}
            aria-label="Muat ulang"
            className="grid size-9 place-items-center rounded-control border border-hairline bg-surface-raised text-ink-soft transition-colors hover:bg-surface-sunken"
          >
            <RefreshCw className={clsx('size-4', isLoading && 'animate-spin')} />
          </button>
        </div>
      </header>

      {/* Status as chips with their counts, not as tabs. A tab says "another
          page"; these are one list seen six ways, and the count is what makes
          pressing one worth the trip. */}
      <div className="mt-5 flex flex-wrap gap-1.5">
        {STATUS_TABS.map((t) => {
          const on = status === t.id;
          const Icon = t.icon;
          const count = t.id === '' ? total : (statuses[t.id] ?? 0);
          return (
            <button
              key={t.id}
              type="button"
              onClick={() => setStatus(t.id)}
              aria-pressed={on}
              className={clsx(
                'inline-flex items-center gap-1.5 rounded-control border px-3 py-1.5 text-sm transition-colors',
                on
                  ? 'border-brand-700 bg-brand-600/10 font-medium text-brand-800'
                  : 'border-hairline bg-surface-raised text-ink-soft hover:bg-surface-sunken',
              )}
            >
              {Icon ? (
                <Icon
                  className={clsx('size-3.5', on ? 'text-brand-700' : STATUS_ICON_TONE[t.id])}
                  aria-hidden
                />
              ) : null}
              {t.label}
              <span className={clsx('nums text-2xs', on ? 'text-brand-700/70' : 'text-ink-muted')}>
                {count.toLocaleString('id-ID')}
              </span>
            </button>
          );
        })}
      </div>

      {/* One bar for the three things that narrow the list, and the way out. */}
      <div className="mt-3 flex flex-wrap items-center gap-2">
        <label className="flex min-w-[220px] flex-1 items-center gap-2 rounded-card border border-hairline bg-surface-raised px-3.5 py-2.5 shadow-e1">
          <Search className="size-4 shrink-0 text-ink-muted" />
          <input
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder={isStory ? 'Cari story…' : 'Cari broadcast…'}
            aria-label="Cari broadcast"
            className="w-full bg-transparent text-sm text-ink outline-none placeholder:text-ink-muted"
          />
        </label>

        <span className="flex items-center gap-2 rounded-card border border-hairline bg-surface-raised px-3 py-2 shadow-e1">
          <LayoutGrid className="size-4 shrink-0 text-ink-muted" aria-hidden />
          <select
            value={applicationID}
            onChange={(e) => setApplicationID(e.target.value)}
            aria-label="Aplikasi"
            className="bg-transparent text-sm text-ink-soft outline-none"
          >
            <option value="">Semua aplikasi</option>
            {chips.map((f) => (
              <option key={f.id ?? 'none'} value={f.id ?? ''}>
                {(f.code || f.name) + ` (${f.count})`}
              </option>
            ))}
          </select>
        </span>

        <DateRange from={from} to={to} onChange={(a, b) => { setFrom(a); setTo(b); }} />

        {/* Cards or rows, and the choice is remembered for as long as the page
            is open. A Story is recognised by its picture and a broadcast by its
            numbers, so each starts where its own question is answered — but
            neither is locked there. */}
        <div className="flex rounded-card border border-hairline bg-surface-raised p-0.5 shadow-e1">
          {([
            { id: 'grid' as const, icon: LayoutGrid, label: 'Kartu' },
            { id: 'list' as const, icon: List, label: 'Tabel' },
          ]).map((v) => {
            const Icon = v.icon;
            return (
              <button
                key={v.id}
                type="button"
                onClick={() => setView(v.id)}
                aria-pressed={view === v.id}
                aria-label={v.label}
                className={clsx(
                  'rounded-[7px] p-2 transition-colors',
                  view === v.id
                    ? 'bg-brand-800 text-white'
                    : 'text-ink-muted hover:bg-surface-sunken',
                )}
              >
                <Icon className="size-4" />
              </button>
            );
          })}
        </div>

        {active.length > 0 ? (
          <button
            type="button"
            onClick={clearAll}
            className="inline-flex items-center gap-1.5 rounded-card border border-hairline bg-surface-raised px-3 py-2.5 text-sm text-ink-soft shadow-e1 transition-colors hover:bg-surface-sunken"
          >
            <RotateCcw className="size-4" />
            Reset
          </button>
        ) : null}
      </div>

      {/* What is currently narrowing the list, each with its own way off. The
          bar above says what can be chosen; this says what was. */}
      {active.length > 0 ? (
        <div className="mt-2.5 flex flex-wrap items-center gap-1.5">
          {active.map((a) => (
            <span
              key={a.label}
              className="inline-flex items-center gap-1.5 rounded-full border border-hairline bg-surface-sunken px-2.5 py-1 text-2xs text-ink-soft"
            >
              {a.label}
              <button
                type="button"
                onClick={a.clear}
                aria-label={`Hapus filter ${a.label}`}
                className="text-ink-muted transition-colors hover:text-ink"
              >
                <X className="size-3" />
              </button>
            </span>
          ))}
          <button
            type="button"
            onClick={clearAll}
            className="text-2xs font-medium text-brand-700 transition-colors hover:text-brand-800"
          >
            Hapus semua
          </button>
        </div>
      ) : null}

      <div className="mt-3">
        {error ? (
          <ErrorState
            message={error instanceof Error ? error.message : 'Terjadi kesalahan.'}
            onRetry={() => void mutate()}
          />
        ) : isLoading && campaigns.length === 0 ? (
          <RowSkeleton />
        ) : campaigns.length === 0 ? (
          <EmptyState
            title={isStory ? 'Belum ada WA Story.' : 'Belum ada Broadcast.'}
            hint={
              status || applicationID || debounced
                ? 'Tidak ada yang cocok dengan filter ini.'
                : 'Buat yang pertama lewat tombol di kanan atas.'
            }
          />
        ) : view === 'grid' ? (
          // Cards, not rows: a Story is recognised by its picture. See StoryGrid.
          <>
            <StoryGrid campaigns={campaigns} isStory={isStory} />
            <ShownCount shown={campaigns.length} total={total} isStory={isStory} />
          </>
        ) : (
          <>
            <div className="max-h-[70vh] overflow-auto rounded-card border border-hairline bg-surface-raised shadow-e1">
              <table className="w-full min-w-[920px] border-collapse text-sm">
                <thead>
                  <tr className="border-b border-hairline text-ink-muted">
                    <Th>{isStory ? 'Story' : 'Broadcast'}</Th>
                    <Th className="w-32">Aplikasi</Th>
                    <Th className="w-32">Status</Th>
                    {/* A Story has no recipient list of ours — WhatsApp decides
                        its audience from the account's own privacy settings —
                        so the unit it is measured in is the numbers it
                        publishes from. */}
                    <Th className="w-28">{isStory ? 'Nomor' : 'Penerima'}</Th>
                    <Th className="w-52">Progress</Th>
                    <Th className="w-40">Tanggal</Th>
                    <Th className="w-16 text-center">Aksi</Th>
                  </tr>
                </thead>
                <tbody>
                  {campaigns.map((c) => (
                    <Row
                      key={c.id}
                      campaign={c}
                      isStory={isStory}
                      // The campaign row carries its application's code but not
                      // its colour; the chip counts already know it, so the
                      // colour is looked up rather than asked for twice.
                      color={colorOf.get(c.application_code ?? '') ?? null}
                    />
                  ))}
                </tbody>
              </table>
            </div>

            <ShownCount shown={campaigns.length} total={total} isStory={isStory} />
          </>
        )}
      </div>
    </PageShell>
  );
}

function Row({
  campaign: c,
  isStory,
  color,
}: {
  campaign: Campaign;
  isStory: boolean;
  color: string | null;
}) {
  const href = `${isStory ? '/story' : '/broadcast'}/${c.id}`;
  const done = c.success_count + c.failed_count;
  // Story progress is counted in numbers published from, broadcast progress in
  // recipients reached. Using target_count for both left every Story row showing
  // 0 of 0 forever, because a Story never has targets.
  const total = isStory ? c.device_count : c.target_count;
  const percent = total > 0 ? Math.round((done / total) * 100) : 0;

  const when = new Date(c.scheduled_at ?? c.created_at);

  return (
    <tr className="h-[62px] border-b border-hairline transition-colors last:border-0 hover:bg-surface-sunken/40">
      <Td>
        <Link href={href} className="flex min-w-0 items-center gap-2.5">
          {/* Tinted with the application's own colour, so a row is recognised
              by its brand before it is read. */}
          <span
            aria-hidden
            className="grid size-9 shrink-0 place-items-center rounded-xl"
            style={{
              backgroundColor: `${color ?? '#4e8064'}1F`,
              color: color ?? '#4e8064',
            }}
          >
            <Send className="size-4" />
          </span>
          <span className="min-w-0">
            <span className="block truncate font-medium text-ink uppercase">{c.name}</span>
            {c.account_name ? (
              <span className="mt-0.5 flex items-center gap-1 truncate text-2xs text-ink-muted">
                <Smartphone className="size-3 shrink-0" />
                {c.account_name}
              </span>
            ) : null}
          </span>
        </Link>
      </Td>

      <Td>
        {c.application_code ? (
          <span
            className="rounded-md px-1.5 py-0.5 text-2xs font-medium"
            style={{
              backgroundColor: `${color ?? '#3a604b'}1F`,
              color: color ?? '#3a604b',
            }}
          >
            {c.application_code}
          </span>
        ) : (
          <span className="text-2xs text-ink-muted">Tanpa aplikasi</span>
        )}
      </Td>

      <Td>
        <span className="inline-flex items-center gap-1.5 text-xs text-ink-soft">
          <span
            aria-hidden
            className={clsx('size-1.5 rounded-full', STATUS_TONE[c.status] ?? 'bg-ink-muted')}
          />
          {statusLabel(c.status, isStory)}
        </span>
      </Td>

      <Td>
        <span className="nums block text-sm font-semibold text-ink">
          {total.toLocaleString('id-ID')}
        </span>
        <span className="block text-2xs text-ink-muted">{isStory ? 'nomor' : 'penerima'}</span>
      </Td>

      <Td>
        <span className="flex items-center gap-2">
          <span className="h-1.5 min-w-0 flex-1 overflow-hidden rounded-full bg-surface-sunken">
            <span
              className={clsx('block h-full', c.failed_count > 0 ? 'bg-warn' : 'bg-brand-600')}
              style={{ width: `${percent}%` }}
            />
          </span>
          {/* The percentage beside the bar, and the counts behind the tooltip:
              "12 dari 30" is what somebody checks when the bar looks wrong. */}
          <span
            title={`${done.toLocaleString('id-ID')} dari ${total.toLocaleString('id-ID')}`}
            className="nums w-9 shrink-0 text-right text-2xs text-ink-muted"
          >
            {percent}%
          </span>
        </span>
      </Td>

      <Td className="text-xs text-ink-soft">
        <span className="flex items-center gap-1">
          <CalendarDays className="size-3 shrink-0 text-ink-muted" aria-hidden />
          {when.toLocaleDateString('id-ID', {
            day: 'numeric',
            month: 'short',
            year: 'numeric',
            timeZone: 'Asia/Jakarta',
          })}
        </span>
        <span className="nums mt-0.5 flex items-center gap-1 text-2xs text-ink-muted">
          <Clock className="size-3 shrink-0" aria-hidden />
          {when.toLocaleTimeString('id-ID', {
            hour: '2-digit',
            minute: '2-digit',
            timeZone: 'Asia/Jakarta',
          })}
        </span>
      </Td>

      <Td className="text-center">
        <Link
          href={href}
          aria-label={`Lihat detail ${c.name}`}
          className="inline-flex rounded-lg p-1.5 text-ink-muted transition-colors hover:bg-surface-sunken hover:text-ink-soft"
        >
          <Eye className="size-4" />
        </Link>
      </Td>
    </tr>
  );
}

/**
 * The period the list is narrowed to.
 *
 * Two dates behind one button rather than two boxes always on screen: most of
 * the time nobody is filtering by date at all, and two empty date fields in the
 * toolbar read as something waiting to be filled in.
 */
function DateRange({
  from,
  to,
  onChange,
}: {
  from: string;
  to: string;
  onChange: (from: string, to: string) => void;
}) {
  const [open, setOpen] = useState(false);
  const box = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    function onDown(e: MouseEvent) {
      if (!box.current?.contains(e.target as Node)) setOpen(false);
    }
    document.addEventListener('mousedown', onDown);
    return () => document.removeEventListener('mousedown', onDown);
  }, [open]);

  const set = from || to;

  return (
    <div ref={box} className="relative">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
        className={clsx(
          'inline-flex items-center gap-2 rounded-card border bg-surface-raised px-3 py-2.5 text-sm shadow-e1 transition-colors',
          set ? 'border-brand-700 text-brand-800' : 'border-hairline text-ink-soft hover:bg-surface-sunken',
        )}
      >
        <CalendarDays className="size-4" />
        {set ? `${from ? formatDay(from) : '…'} – ${to ? formatDay(to) : '…'}` : 'Pilih tanggal'}
        <ChevronDown className="size-4 text-ink-muted" />
      </button>

      {open ? (
        <div className="absolute right-0 z-30 mt-1 w-64 rounded-card border border-hairline bg-surface-raised p-3 shadow-e2">
          <label className="block">
            <span className="mb-1 block text-2xs text-ink-muted">Dari tanggal</span>
            <input
              type="date"
              value={from}
              onChange={(e) => onChange(e.target.value, to)}
              className={`w-full ${inputClassSm}`}
            />
          </label>
          <label className="mt-2 block">
            <span className="mb-1 block text-2xs text-ink-muted">Sampai tanggal</span>
            <input
              type="date"
              value={to}
              onChange={(e) => onChange(from, e.target.value)}
              className={`w-full ${inputClassSm}`}
            />
          </label>
          {set ? (
            <button
              type="button"
              onClick={() => {
                onChange('', '');
                setOpen(false);
              }}
              className="mt-2 text-2xs font-medium text-brand-700 hover:text-brand-800"
            >
              Hapus tanggal
            </button>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}

/**
 * How much of the list is on screen.
 *
 * The server sends the fifty most recent and no more, so a page that showed
 * only what arrived would quietly present a slice as the whole. There is no
 * page 2 to offer yet; what can honestly be offered is the count and the way to
 * narrow it.
 */
function ShownCount({
  shown,
  total,
  isStory,
}: {
  shown: number;
  total: number;
  isStory: boolean;
}) {
  return (
    <p className="mt-3 text-xs text-ink-muted">
      Menampilkan <span className="nums text-ink-soft">{shown.toLocaleString('id-ID')}</span> dari{' '}
      <span className="nums text-ink-soft">{total.toLocaleString('id-ID')}</span>{' '}
      {isStory ? 'story' : 'broadcast'}
      {shown < total ? ' — persempit dengan filter di atas untuk melihat sisanya.' : ''}
    </p>
  );
}

/** "15 Sep 2026", the form the chips and the button have room for. */
function formatDay(iso: string): string {
  const [y, m, d] = iso.split('-').map(Number);
  const names = ['Jan', 'Feb', 'Mar', 'Apr', 'Mei', 'Jun', 'Jul', 'Agu', 'Sep', 'Okt', 'Nov', 'Des'];
  return `${d} ${names[m - 1] ?? ''} ${y}`;
}

function Th({ children, className }: { children?: React.ReactNode; className?: string }) {
  return (
    <th
      scope="col"
      // Pinned to the top of the scroll box. The border lives on the cell
      // because a border on the row does not travel with a sticky cell.
      className={clsx(
        'sticky top-0 z-10 h-[42px] border-b border-hairline bg-surface-raised px-4 text-left text-2xs font-semibold tracking-wide uppercase',
        className,
      )}
    >
      {children}
    </th>
  );
}

function Td({ children, className }: { children?: React.ReactNode; className?: string }) {
  return <td className={clsx('px-4 align-middle', className)}>{children}</td>;
}

