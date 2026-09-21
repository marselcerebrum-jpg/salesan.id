'use client';

import clsx from 'clsx';
import { AlertTriangle, Clock, Hourglass, Zap } from 'lucide-react';
import type { ReactNode } from 'react';

import { MetricCardGroup } from '@/components/analytics/MetricCardGroup';
import { EmptyState, InfoTip, formatDuration } from '@/components/analytics/Primitives';
import { ProgressRing } from '@/components/analytics/Viz';
import type { DrilldownKind } from '@/components/analytics/DrilldownPanel';
import type { DashboardSummary } from '@/lib/types';

/*
 * The one card of the old Performa set that outlived the rest.
 *
 * Everything else that file held is now drawn from the shared column model, so
 * each metric is defined once. This one is not in that model and could not be:
 * the out-of-hours queue sits deliberately outside every SLA figure, and a
 * measurement that belongs to no group had nowhere in the table to live.
 */
/**
 * Antrean Awal Jam Kerja — messages that arrived while nobody was on shift.
 *
 * Its own card, not a row inside the SLA one, because it answers a different
 * question and belongs to a different person. The SLA asks "when somebody
 * writes while we are open, how fast do we answer" — that is the shift's
 * responsibility. This asks "how long does the overnight pile take to clear
 * once we open" — that is a question about how many people are on and when the
 * doors open, which is not the fault of whoever is on shift.
 *
 * Folding them together made both unreadable: one busy night dragged the SLA
 * down as if somebody had been slow, and a healthy SLA hid a backlog that was
 * not cleared until noon.
 *
 * Every duration here is measured from the moment the shift OPENED, not from
 * when the message arrived. A message waiting since 2am has not been ignored
 * for seven hours by anybody; the number that can be acted on is how long it
 * sat after there was somebody to answer it.
 */
export function QueueCard({
  summary: s,
  loading,
  onDrill,
}: {
  summary: DashboardSummary | undefined;
  loading: boolean;
  onDrill: (k: DrilldownKind) => void;
}) {
  const total = s?.queued_total ?? 0;
  const answered = s?.queued_answered ?? 0;
  const waiting = s?.queued_waiting ?? 0;
  // The queue is only cleared when nothing is left waiting. Until then the
  // longest duration is how far it has got, not how long it took — and the
  // label has to say which, or the number reads as a finished job.
  const cleared = total > 0 && waiting === 0;

  return (
    <MetricCardGroup
      icon={Hourglass}
      title="Antrean Awal Jam Kerja"
      description="Chat pribadi yang masuk di luar jam kerja. Dihitung sejak jam kerja dibuka, bukan sejak pesannya datang."
      loading={loading}
      footer={
        total > 0 ? (
          <button
            type="button"
            onClick={() => onDrill({ kind: 'sla', status: 'queued' })}
            className="rounded-lg text-sm font-medium text-brand-700 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-brand-600"
          >
            Lihat Daftar Antrean
          </button>
        ) : null
      }
    >
      {total === 0 ? (
        <div className="py-4">
          <EmptyState
            title="Tidak ada pesan yang masuk di luar jam kerja."
            hint="Semua pesan pada periode ini datang saat ada yang bertugas, jadi seluruhnya dinilai sebagai SLA."
          />
        </div>
      ) : (
        <>
          <div className="flex flex-wrap items-center gap-5 pt-1">
            <ProgressRing achieved={answered} total={total} size={100} />

            <ul className="min-w-[190px] flex-1 space-y-0.5">
              <SlaRow
                icon={Clock}
                label="Rata-rata Durasi Membalas"
                value={formatDuration(s?.queued_avg_seconds ?? null)}
                info="Rata-rata durasi yang dibutuhkan untuk membalas satu chat antrean, dihitung sejak jam kerja dibuka. Waktu sebelum jam kerja tidak dihitung, karena belum ada yang bertugas."
                onClick={() => onDrill({ kind: 'sla', status: 'queued' })}
              />
              {/*
               * The longest single duration is also the moment the queue was
               * emptied: every item starts counting at the same instant — when
               * the shift opened — so the one that took longest is the one that
               * finished last.
               */}
              <SlaRow
                icon={Hourglass}
                label={cleared ? 'Durasi Menghabiskan Antrean' : 'Durasi Terpakai Sejauh Ini'}
                value={formatDuration(s?.queued_slowest_seconds ?? null)}
                info={
                  cleared
                    ? 'Sejak jam kerja dibuka sampai chat antrean terakhir dibalas. Seluruh antrean pada periode ini sudah habis.'
                    : 'Sejak jam kerja dibuka sampai chat antrean terakhir yang sudah dibalas. Masih ada yang belum dibalas, jadi angkanya belum final.'
                }
                tone={waiting > 0 ? 'warn' : undefined}
              />
              <SlaRow
                icon={Zap}
                label="Sudah Dibalas"
                value={`${answered.toLocaleString('id-ID')} dari ${total.toLocaleString('id-ID')}`}
                info="Berapa dari antrean ini yang sudah terjawab."
                onClick={() => onDrill({ kind: 'sla', status: 'queued' })}
              />
              <SlaRow
                icon={AlertTriangle}
                label="Masih Mengantre"
                value={waiting}
                info="Masuk di luar jam kerja dan sampai sekarang belum dibalas."
                tone={waiting > 0 ? 'danger' : undefined}
                onClick={() => onDrill({ kind: 'sla', status: 'queued' })}
              />
            </ul>
          </div>

          <p className="mt-2 border-t border-hairline pt-2 text-2xs leading-relaxed text-ink-muted">
            Tidak ada satu pun angka di sini yang masuk ke Performa SLA. Pesan yang datang saat
            tidak ada yang bertugas bukan pelanggaran, dan bukan pencapaian. Chat grup tidak masuk
            ke sini sama sekali — grup tidak punya satu pelanggan yang menunggu dijawab.
          </p>
        </>
      )}
    </MetricCardGroup>
  );
}

/** One duration, with a tinted mark so the four read as a set. */
function SlaRow({
  icon: Icon,
  label,
  value,
  info,
  tone,
  onClick,
}: {
  icon: typeof Clock;
  label: string;
  value: ReactNode;
  info?: string;
  tone?: 'warn' | 'danger';
  onClick?: () => void;
}) {
  const mark =
    tone === 'danger'
      ? 'bg-danger-soft text-danger'
      : tone === 'warn'
        ? 'bg-warn-soft text-warn'
        : 'bg-surface-sunken text-ink-soft';

  const body = (
    <>
      <span className={clsx('pointer-events-none grid size-6 shrink-0 place-items-center rounded-md', mark)}>
        <Icon className="size-3.5" />
      </span>
      <span className="pointer-events-none flex min-w-0 flex-1 items-center gap-1">
        <span className="truncate text-xs text-ink-muted">{label}</span>
        {info ? (
          <span className="pointer-events-auto">
            <InfoTip text={info} />
          </span>
        ) : null}
      </span>
      <span className="nums pointer-events-none shrink-0 text-sm font-semibold text-ink">
        {value}
      </span>
    </>
  );

  return (
    <li>
      {onClick ? (
        <button
          type="button"
          onClick={onClick}
          className="-mx-1.5 flex w-full items-center gap-2 rounded-lg px-1.5 py-1 text-left transition-colors hover:bg-surface-sunken/60 focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-brand-600"
        >
          {body}
        </button>
      ) : (
        // The same box as the clickable row above, minus the interaction. With
        // different padding the four rows were four different heights and the
        // set stopped reading as a set.
        <div className="-mx-1.5 flex items-center gap-2 px-1.5 py-1">{body}</div>
      )}
    </li>
  );
}
