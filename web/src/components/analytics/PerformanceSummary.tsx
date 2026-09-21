'use client';

import clsx from 'clsx';
import { Hourglass } from 'lucide-react';
import { useMemo } from 'react';
import useSWR from 'swr';

import { ApplicationBreakdown } from '@/components/analytics/ApplicationBreakdown';
import { ChatMixDonut } from '@/components/analytics/ChatMixDonut';
import { DailyBreakdown } from '@/components/analytics/DailyBreakdown';
import { DrilldownPanel, type DrilldownKind } from '@/components/analytics/DrilldownPanel';
import { GroupRecap } from '@/components/analytics/GroupRecap';
import { QueueCard } from '@/components/analytics/QueueCard';
import { ErrorState } from '@/components/analytics/Primitives';
import { TrafficChart } from '@/components/analytics/TrafficChart';
import { analyticsPath, fetcher, performancePath, type AnalyticsQuery } from '@/lib/api';
import { useRealtimeEvent } from '@/lib/realtime';
import type { AnalyticsScope, LabelTransition, LabelUsage, PerformanceReport } from '@/lib/types';

/**
 * The figures behind one scope: a person, a team, or everything visible.
 *
 * Drawn with the Dashboard's cards, from the Dashboard's column model. The two
 * screens ask different questions — the Dashboard asks how the operation is
 * doing, this asks how one person or one team is doing — but they ask them of
 * the same forty-one measurements, and reading the same measurement in two
 * different shapes on two screens is how a reader ends up believing they are
 * two different measurements.
 *
 * What is added here, and is not on the Dashboard: every figure with a list
 * behind it opens that list, because this is the screen somebody comes to when
 * a number looks wrong and wants to see the rows it was counted from.
 *
 * One component, one query, rendered wherever a summary is needed. Two copies
 * would eventually disagree, and the place it would show is two screens
 * quoting different numbers for the same day.
 */

/**
 * Which figures open a list, and which list.
 *
 * Keyed by the column key of the shared model, so a metric renamed there keeps
 * its drilldown, and a metric that loses one fails loudly at the type level
 * rather than opening the wrong panel.
 */
const COLUMN_DRILLS: Record<string, DrilldownKind> = {
  leads: { kind: 'leads', status: 'verified_new' },
  inbound: { kind: 'messages', direction: 'in', chatType: 'personal', title: 'Pesan Masuk' },
  outbound: { kind: 'messages', direction: 'out', chatType: 'personal', title: 'Pesan Keluar' },
  group_in: { kind: 'messages', direction: 'in', chatType: 'group', title: 'Pesan Masuk Grup' },
  group_out: { kind: 'messages', direction: 'out', chatType: 'group', title: 'Pesan Keluar Grup' },
  avg: { kind: 'sla' },
  fastest: { kind: 'sla' },
  slowest: { kind: 'sla' },
  sla_ratio: { kind: 'sla', status: 'breached' },
  fu_done: { kind: 'follow-ups' },
  fu_answered: { kind: 'follow-ups', status: 'yes' },
  fu_unanswered: { kind: 'follow-ups', status: 'no' },
  changes_total: { kind: 'label-events' },
  assigned: { kind: 'label-events' },
  removed: { kind: 'label-events' },
};

export function PerformanceSummary({
  query,
  onOpenWaiting,
  onDrill,
  drilldown,
  onCloseDrill,
  showDaily = true,
  showApplicationSplit = false,
  onPickApplication,
  workHours = false,
}: {
  query: AnalyticsQuery;
  onOpenWaiting: () => void;
  onDrill: (kind: DrilldownKind) => void;
  drilldown: DrilldownKind | null;
  onCloseDrill: () => void;
  showDaily?: boolean;
  /**
   * Whether to split these figures per application.
   *
   * Off by default because it costs the server one aggregate pass per
   * application. The scope views ask for it, where the reader is looking at
   * several applications at once; a single person's detail does not.
   */
  showApplicationSplit?: boolean;
  onPickApplication?: (applicationId: string | null) => void;
  /**
   * Draw the conversation chart from working hours only.
   *
   * Set on the views about one person or one team. Their own tab and the
   * organisation-wide view leave it off, where the question is what happened
   * rather than what happened on somebody's shift.
   */
  workHours?: boolean;
}) {
  // `compare=true` costs a second aggregate pass on the server, so only this
  // view asks for it. It is what every delta on the page is drawn from; when
  // it is absent no delta is shown at all.
  const path = useMemo(() => performancePath(query, { compare: true }), [query]);
  const { data, error, isLoading, mutate } = useSWR<{
    report: PerformanceReport;
    scope: AnalyticsScope;
  }>(path, fetcher, { refreshInterval: 120_000, keepPreviousData: true });

  // The label card needs the per-label breakdown, which lives on the same
  // endpoint as the label drill-down, so the two share one definition.
  // `events=false` skips the expensive half.
  const labels = useSWR<{
    usage: LabelUsage[];
    transitions: LabelTransition[];
    /** Changes this filter is hiding because nobody can be named for them. */
    unattributed: number;
  }>(analyticsPath('label-events', query, { events: 'false' }), fetcher, {
    keepPreviousData: true,
  });

  /*
   * More than one day in the period.
   *
   * Read off the filter rather than off the rows that came back: a month with
   * one busy day returns one row, and hiding the day-by-day table for it would
   * hide the very thing somebody opened a month view to see.
   */
  const multiDay = !query.date || Boolean(query.from || query.to || query.month);

  useRealtimeEvent('metrics.updated', () => {
    void mutate();
    void labels.mutate();
  });
  useRealtimeEvent('campaign.updated', () => void mutate());
  useRealtimeEvent('schedule.updated', () => void mutate());

  const report = data?.report;
  const s = report?.summary;
  const previous = report?.previous;
  const days = report?.days ?? [];

  const chatType = query.chat_type ?? 'all';
  const showPersonal = chatType !== 'group';
  const showGroup = chatType !== 'personal';

  /*
   * Which cards a narrowed view drops.
   *
   * A view narrowed to one kind of chat has nothing to say about the other, and
   * a card of certain zeroes is worse than no card. SLA and follow-up go with
   * the personal chats: a group has no single customer waiting for an answer,
   * so neither is ever measured on one.
   */
  const omit = [
    ...(showPersonal ? [] : ['chat', 'followup', 'sla']),
    ...(showGroup ? [] : ['group']),
  ];
  // Never show the previous filter's numbers while the next ones load: a
  // figure that is wrong for two seconds is a figure somebody screenshots.
  const loading = isLoading || !s;

  // A failed refetch keeps whatever is already on screen and says so above
  // it. Replacing a working page with an error box because the newest
  // request timed out throws away figures that are still perfectly readable,
  // and leaves nothing to compare the retry against.
  const stale = Boolean(error && s);

  if (error && !s) {
    return (
      <div className="mt-5">
        <ErrorState
          message={error instanceof Error ? error.message : 'Terjadi kesalahan.'}
          onRetry={() => void mutate()}
        />
      </div>
    );
  }

  return (
    <>
      {stale ? (
        <div className="mt-4 flex flex-wrap items-center justify-between gap-3 rounded-card border border-danger/25 bg-danger-soft px-4 py-2.5 text-sm text-danger">
          <span>
            Gagal memperbarui data. Angka di bawah masih dari pemuatan sebelumnya.
          </span>
          <button
            type="button"
            onClick={() => void mutate()}
            className="rounded-control px-2.5 py-1 text-sm font-medium underline-offset-2 hover:underline"
          >
            Coba Lagi
          </button>
        </div>
      ) : null}

      {/*
       * 1. The shape of the period, before the totals of it.
       *
       * The same two panels the Dashboard leads with, reading the same filter:
       * when the work happened, and how much of it was groups. On this screen
       * they are narrowed to whoever is being read, so a Freelance's chart is
       * the hours that Freelance worked rather than the operation's.
       */}
      <div className="mt-5 grid gap-4 xl:grid-cols-3">
        <div className="min-w-0 xl:col-span-2">
          <TrafficChart query={query} workHours={workHours} />
        </div>
        <ChatMixDonut summary={s} />
      </div>

      {/* 2. The period, group by group — the same cards the Dashboard shows. */}
      <div className="mt-4">
        <GroupRecap
          summary={s}
          previous={previous}
          loading={loading}
          labelUsage={labels.data?.usage ?? []}
          onDrill={onDrill}
          columnDrills={COLUMN_DRILLS}
          omit={omit}
        />
      </div>

      {/*
       * 3. What the SLA leaves out, kept.
       *
       * Messages that arrived with nobody on shift are not an SLA failure and
       * are deliberately outside every figure above; without this card a clean
       * SLA reads as "no backlog", which is exactly the thing it does not say.
       */}
      {showPersonal ? (
        <div className="mt-4 grid gap-4 lg:grid-cols-2">
          <QueueCard summary={s} loading={loading} onDrill={onDrill} />
          <WaitingLink onOpenWaiting={onOpenWaiting} waiting={s?.sla_waiting ?? 0} />
        </div>
      ) : null}

      {/*
       * 4. The tables under the cards.
       *
       * Rincian Per Hari only when the period is more than one day: on a single
       * day it is the cards above written out again as one row, which is how a
       * page ends up twice as long as the question it answers.
       */}
      <div className="mt-4 space-y-3">
        {showDaily && multiDay ? (
          <DailyBreakdown days={days} loading={loading} query={query} />
        ) : null}

        {showApplicationSplit ? (
          <ApplicationBreakdown query={query} onPick={onPickApplication} />
        ) : null}
      </div>

      <DrilldownPanel target={drilldown} query={query} onClose={onCloseDrill} />
    </>
  );
}

/**
 * The conversations still waiting for a first reply.
 *
 * Its own card beside the queue, because the two are the halves of "what is
 * still owed": the queue is what arrived out of hours, this is what nobody has
 * answered yet whenever it arrived. Both are absent from the SLA figures above,
 * which count only cycles that were closed.
 *
 * The list itself is a panel rather than a drilldown, because it is about now
 * rather than about the period — it is the only thing on this screen that does
 * not move with the filter, and it says so.
 */
function WaitingLink({
  waiting,
  onOpenWaiting,
}: {
  waiting: number;
  onOpenWaiting: () => void;
}) {
  return (
    <section className="flex flex-col justify-between rounded-card border border-hairline bg-surface-raised px-4 py-4 shadow-e1">
      <div>
        <h3 className="flex items-center gap-2 text-sm font-semibold text-ink">
          <Hourglass className="size-4 text-ink-muted" aria-hidden />
          Menunggu Dibalas
        </h3>
        <p className="mt-0.5 text-2xs leading-snug text-ink-muted">
          Siklus yang dimulai pada periode ini dan sampai sekarang belum mendapat balasan manual
          pertama. Belum masuk hitungan SLA, karena SLA hanya menilai yang sudah dibalas.
        </p>
        <p
          className={clsx(
            'nums mt-3 text-2xl leading-none font-semibold',
            waiting > 0 ? 'text-warn' : 'text-ink-muted',
          )}
        >
          {waiting.toLocaleString('id-ID')}
        </p>
      </div>

      <button
        type="button"
        onClick={onOpenWaiting}
        className="mt-3 self-start rounded-control text-xs font-medium text-brand-700 transition-colors hover:text-brand-800"
      >
        Lihat yang menunggu dibalas
      </button>
    </section>
  );
}
