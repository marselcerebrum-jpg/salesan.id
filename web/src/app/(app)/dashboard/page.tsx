'use client';

import clsx from 'clsx';
import {
  ChevronRight,
  Contact as ContactIcon,
  MessageCircle,
  Smartphone,
  TriangleAlert,
  Users,
} from 'lucide-react';
import Link from 'next/link';
import { Suspense, useEffect, useMemo, useState } from 'react';
import useSWR from 'swr';

import { ApplicationBreakdown } from '@/components/analytics/ApplicationBreakdown';
import { ChatMixDonut } from '@/components/analytics/ChatMixDonut';
import { DrilldownPanel, type DrilldownKind } from '@/components/analytics/DrilldownPanel';
import { FilterFields } from '@/components/analytics/FilterFields';
import { previousPeriod } from '@/components/analytics/period';
import { GroupRecap } from '@/components/analytics/GroupRecap';
import { PeriodMenu } from '@/components/analytics/PeriodMenu';
import { TeamRank } from '@/components/analytics/TeamRank';
import { TrafficChart } from '@/components/analytics/TrafficChart';
import { ErrorState, PageShell } from '@/components/analytics/Primitives';
import {
  analyticsFiltersPathFor,
  analyticsPath,
  dashboardPath,
  fetcher,
  type DashboardStats,
} from '@/lib/api';
import { todayWIB, useAnalyticsFilter } from '@/lib/useAnalyticsFilter';
import type { DashboardSummary, FilterOptions, LabelUsage } from '@/lib/types';

/**
 * Dashboard — the whole operation on one screen.
 *
 * Deliberately not a second Performa. Performa answers "how is this person, or
 * this team, doing"; this answers "how is the operation, right now, across
 * every application". The two overlap in figures on purpose, and where they do
 * they read the SAME endpoint rather than computing it twice — a number that
 * disagrees with itself across two screens is worse than a number missing from
 * one of them.
 *
 * Reads top to bottom as urgency descending: what is broken, then what is
 * waiting, then the shape of the traffic, then the period's recap, then the
 * same period split per application.
 */
export default function DashboardPage() {
  return (
    <Suspense fallback={null}>
      <Dashboard />
    </Suspense>
  );
}

function Dashboard() {
  // Today, like Performa. The question somebody opens this with is "how are we
  // doing right now", and a month-to-date figure hides today inside an average.
  const { query: url, setQuery } = useAnalyticsFilter({ date: todayWIB() });

  /*
   * Never narrowed to one person, whatever the link said.
   *
   * This screen has no PIC or Freelance control any more, and a filter that is
   * applied with nothing on screen able to show or clear it is the worst kind:
   * every figure quietly means something narrower than it claims.
   */
  const query = useMemo(
    () => ({ ...url, pic_id: undefined, freelance_id: undefined }),
    [url],
  );

  /*
   * One request, two answers.
   *
   * The period summary and the counts of how things stand now travel together
   * because one screen reads both at one moment — and because both already
   * lived behind this endpoint, so the Dashboard adds no new way to ask for a
   * figure Performa also shows.
   */
  const report = useSWR<{ summary: DashboardSummary; stats: DashboardStats }>(
    dashboardPath(query),
    fetcher,
    { refreshInterval: 60_000, keepPreviousData: true },
  );

  /*
   * The same figures over the period immediately before this one.
   *
   * This is what every "+12%" on the screen is measured against: real figures
   * from the same endpoint over the previous stretch of calendar, carrying the
   * same narrowings. Nothing here is modelled, and where the previous period
   * held nothing the comparison is simply not drawn.
   */
  const prior = useSWR<{ summary: DashboardSummary }>(
    dashboardPath(useMemo(() => previousPeriod(query), [query])),
    fetcher,
    { keepPreviousData: true },
  );

  /*
   * The workspace's own labels, with how many contacts carry each.
   *
   * Same endpoint the Status Label card on Performa reads, asked without its
   * event list: `events=false` skips the expensive half, so the Dashboard gets
   * the breakdown without paying for a history nobody opened yet.
   */
  const labels = useSWR<{ usage: LabelUsage[] }>(
    analyticsPath('label-events', query, { events: 'false' }),
    fetcher,
    { keepPreviousData: true },
  );

  /*
   * Which of the three people is reading.
   *
   * Not a permission check — the server decides what this account may see, and
   * it already does: a Freelance's every figure is pinned to their own account
   * before any query runs, and a PIC sees themselves and the Freelance under
   * them. This is only so the page says which of those it is showing. A screen
   * that shows one person's work while calling it "seluruh aplikasi" is wrong
   * even when every number on it is right.
   *
   * Same request the filter row makes, so it costs nothing.
   */
  const options = useSWR<FilterOptions>(analyticsFiltersPathFor(), fetcher);
  const role = options.data?.role ?? '';

  const [drill, setDrill] = useState<DrilldownKind | null>(null);

  const s = report.data?.summary;
  const d = report.data?.stats;

  // When the figures on screen were last actually fetched. Set from an effect
  // so the server and the first client render agree, then filled in.
  const [updatedAt, setUpdatedAt] = useState<Date | null>(null);
  useEffect(() => {
    if (report.data) setUpdatedAt(new Date());
  }, [report.data]);

  return (
    <PageShell>
      <header className="flex flex-wrap items-start justify-between gap-x-4 gap-y-3">
        <div className="min-w-0">
          <h1 className="text-2xl font-semibold tracking-[-0.02em] text-ink">Dashboard</h1>
          <p className="mt-1 text-sm text-ink-muted">
            {role === 'freelance'
              ? 'Pantau pekerjaan Anda sendiri di seluruh aplikasi yang Anda pegang.'
              : role === 'pic'
                ? 'Pantau aktivitas WhatsApp di aplikasi yang Anda pegang, termasuk pekerjaan Freelance di bawah Anda.'
                : 'Pantau aktivitas WhatsApp dari seluruh aplikasi secara real-time.'}
          </p>
        </div>

        <div className="flex flex-wrap items-start gap-4">
          <p className="text-2xs leading-snug text-ink-muted">
            <span className="flex items-center gap-1.5">
              <span
                aria-hidden
                className={clsx(
                  'size-1.5 rounded-full',
                  report.isValidating ? 'bg-warn' : 'bg-brand-600',
                )}
              />
              Data terakhir diperbarui
            </span>
            <span className="nums mt-0.5 block">
              {updatedAt
                ? updatedAt.toLocaleString('id-ID', {
                    day: 'numeric',
                    month: 'short',
                    year: 'numeric',
                    hour: '2-digit',
                    minute: '2-digit',
                  })
                : 'memuat…'}
            </span>
          </p>

          <PeriodMenu value={query} onChange={setQuery} />
        </div>
      </header>

      {/* Where and what kind. Open rather than folded behind a button: on a
          screen covering every application at once, "which slice am I looking
          at" is the first question, not an advanced one.
          Who did it is not asked here — that is Performa's question, and the
          team ranking at the bottom of this page is the whole-organisation
          answer to it. */}
      <section
        aria-label="Filter"
        className="mt-5 rounded-card border border-hairline bg-surface-raised px-4 py-3.5 shadow-e1"
      >
        <FilterFields value={query} onChange={setQuery} people={false} />
      </section>

      {/* What is broken, said before anything else it could interrupt. */}
      <DisconnectNotice stats={d} loading={report.isLoading} />

      {/*
       * These four are the state of things right now, and no date range can
       * change them: the period filter above governs everything BELOW this row
       * and nothing in it. Each card says so in its own tooltip rather than
       * under a band heading, so the screen stays the shape it was asked for
       * without the four figures quietly implying they moved with the filter.
       *
       * For a PIC or a Freelance there is a second thing to say, and it cannot
       * live in a tooltip. These four are the totals of every application they
       * hold, while everything below is their own work — so the same screen
       * carries two different scopes, and the line below is what keeps a
       * Freelance from reading "163 kontak" as 163 contacts of their own.
       */}
      {role === 'pic' || role === 'freelance' ? (
        <p className="mt-5 text-2xs text-ink-muted">
          Empat angka berikut adalah total seluruh aplikasi yang Anda pegang, bukan pekerjaan Anda
          sendiri.
        </p>
      ) : null}

      <div
        className={clsx(
          'grid gap-4 sm:grid-cols-2 xl:grid-cols-4',
          role === 'pic' || role === 'freelance' ? 'mt-2' : 'mt-4',
        )}
      >
        <StatCard
          icon={Smartphone}
          tone="brand"
          label="Nomor Terhubung"
          value={d ? `${d.devices_connected}/${d.devices_total}` : null}
          hint={
            d && d.devices_total - d.devices_connected > 0
              ? `${d.devices_total - d.devices_connected} terputus`
              : 'Semua tersambung'
          }
          alert={Boolean(d && d.devices_connected < d.devices_total)}
          dot
          href="/accounts"
        />
        <StatCard
          icon={MessageCircle}
          tone="info"
          label="Pesan Belum Dibalas"
          value={d ? d.unanswered.toLocaleString('id-ID') : null}
          hint="Pelanggan bicara terakhir dan belum dibaca"
          alert={Boolean(d && d.unanswered > 0)}
          href="/chat"
        />
        <StatCard
          icon={Users}
          tone="iris"
          label="Total Grup"
          value={d ? d.groups.toLocaleString('id-ID') : null}
          hint="Grup aktif di seluruh nomor"
          href="/groups"
        />
        <StatCard
          icon={ContactIcon}
          tone="amber"
          label="Total Kontak"
          value={d ? d.contacts.toLocaleString('id-ID') : null}
          hint="Kontak tersimpan di seluruh nomor"
          href="/contacts"
        />
      </div>

      {report.error ? (
        <div className="mt-4">
          <ErrorState
            message={report.error instanceof Error ? report.error.message : 'Gagal memuat.'}
            onRetry={() => void report.mutate()}
          />
        </div>
      ) : null}

      <div className="mt-4 grid gap-4 xl:grid-cols-3">
        <div className="min-w-0 xl:col-span-2">
          <TrafficChart query={query} />
        </div>
        <ChatMixDonut summary={s} />
      </div>

      {/*
       * Everything Rincian Per Hari measures, asked of the whole period at once
       * instead of one day at a time. Broadcast and WA Story are two of those
       * groups, so they are recapped here rather than in separate cards saying
       * the same thing a second time.
       */}
      <div className="mt-4">
        <GroupRecap
          summary={s}
          previous={prior.data?.summary}
          loading={report.isLoading}
          labelUsage={labels.data?.usage ?? []}
          onDrill={setDrill}
        />
      </div>

      {/*
       * The same period split per application, drawn by the same component
       * Performa uses for Rincian Per Hari: same two-level header, same sort,
       * same export, same forty-one columns under the same names. One table
       * definition, three subjects — a day, a person, an application.
       *
       * Open on arrival: nothing on this screen is a detail somebody has to go
       * looking for. A dashboard that needs unfolding shows nothing.
       */}
      <div className="mt-4">
        <ApplicationBreakdown
          query={query}
          defaultOpen
          hideWhenSingle={false}
          personal={role === 'freelance'}
          onPick={(id) => setQuery({ ...query, application_id: id ?? undefined })}
        />
      </div>

      {/*
       * Who is carrying the operation, under what it was carrying.
       *
       * Directly below the per-application split on purpose: the table above
       * says which brand was busy, and this says which team was behind it.
       */}
      <div className="mt-4">
        <TeamRank query={query} />
      </div>

      {/* The same panel Performa opens, reading the same endpoint under the
          same filter: the label history with its transitions counted, and the
          follow-up list behind its own card. */}
      <DrilldownPanel target={drill} query={query} onClose={() => setDrill(null)} />
    </PageShell>
  );
}

/**
 * What is broken, said first and said plainly.
 *
 * Named, not counted. "2 nomor terputus" tells somebody there is a problem
 * without telling them which phone to go and pick up, and the whole value of
 * putting this at the top of the screen is that it can be acted on from there.
 */
function DisconnectNotice({
  stats,
  loading,
}: {
  stats: DashboardStats | undefined;
  loading: boolean;
}) {
  if (loading || !stats || stats.disconnected.length === 0) return null;

  return (
    <section className="mt-4 rounded-card border border-warn/30 bg-warn-soft px-4 py-3.5">
      <div className="flex items-start gap-2.5">
        <TriangleAlert className="mt-0.5 size-4 shrink-0 text-warn" aria-hidden />
        <div className="min-w-0 flex-1">
          <p className="text-sm font-semibold text-warn">
            {stats.disconnected.length} nomor tidak tersambung
          </p>
          <p className="mt-0.5 text-xs leading-relaxed text-ink-soft">
            Pesan masuk tidak terbaca dan broadcast maupun story di nomor ini tidak akan terkirim
            sampai perangkatnya tersambung kembali.
          </p>
          <ul className="mt-2 flex flex-wrap gap-1.5">
            {stats.disconnected.map((dev) => (
              <li key={dev.id}>
                <Link
                  href="/accounts"
                  className="inline-flex items-center gap-1.5 rounded-full border border-warn/30 bg-surface-raised px-2.5 py-1 text-xs text-ink transition-colors hover:bg-surface-sunken"
                >
                  <span className="font-medium">{dev.name}</span>
                  {dev.application_code ? (
                    <span className="text-2xs text-ink-muted">{dev.application_code}</span>
                  ) : null}
                  <span className="text-2xs text-warn">{dev.status}</span>
                </Link>
              </li>
            ))}
          </ul>
        </div>
      </div>
    </section>
  );
}

/** Icon tiles. Identity only: none of these tints carries a status meaning. */
const TILES = {
  brand: 'bg-brand-600/12 text-brand-700',
  info: 'bg-info-soft text-info',
  iris: 'bg-iris-soft text-iris',
  amber: 'bg-warn-soft text-amber-badge',
} as const;

/**
 * One live reading.
 *
 * Colour is the only thing separating a figure needing action from one that is
 * context. Not size: these four are read against each other, and a number
 * shrunk for being calm is a number harder to compare.
 */
function StatCard({
  icon: Icon,
  tone,
  label,
  value,
  hint,
  alert = false,
  dot = false,
  href,
}: {
  icon: typeof Smartphone;
  tone: keyof typeof TILES;
  label: string;
  value: string | null;
  hint: string;
  /** True when this figure is asking to be acted on. */
  alert?: boolean;
  /** Draws a status dot beside the hint, for the figure that is a state. */
  dot?: boolean;
  href: string;
}) {
  return (
    <Link
      href={href}
      title={`${hint}. Keadaan saat ini, tidak mengikuti filter periode.`}
      className="group flex items-center gap-3 rounded-card border border-hairline bg-surface-raised px-4 py-3.5 shadow-e1 transition-colors hover:bg-surface-sunken/50"
    >
      <span
        className={clsx(
          'flex size-11 shrink-0 items-center justify-center rounded-control',
          TILES[tone],
        )}
      >
        <Icon className="size-5" aria-hidden />
      </span>

      <span className="min-w-0 flex-1">
        <span className="block truncate text-xs text-ink-muted">{label}</span>
        {value === null ? (
          <span className="mt-1 block h-7 w-14 animate-pulse rounded bg-surface-sunken" />
        ) : (
          <span
            className={clsx(
              'nums mt-0.5 flex items-baseline gap-1 text-2xl leading-tight font-semibold',
              alert ? 'text-warn' : 'text-ink',
            )}
          >
            {value}
            <ChevronRight
              aria-hidden
              className="size-4 self-center text-ink-muted transition-transform group-hover:translate-x-0.5"
            />
          </span>
        )}
        <span
          className={clsx(
            'mt-0.5 flex items-center gap-1.5 truncate text-2xs',
            alert ? 'text-warn' : 'text-ink-muted',
          )}
        >
          {dot ? (
            <span
              aria-hidden
              className={clsx('size-1.5 shrink-0 rounded-full', alert ? 'bg-warn' : 'bg-brand-600')}
            />
          ) : null}
          {hint}
        </span>
      </span>
    </Link>
  );
}
