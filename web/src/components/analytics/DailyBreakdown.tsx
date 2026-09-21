'use client';

import { ArrowUpDown, CalendarDays, Download, X } from 'lucide-react';
import { useMemo, useState } from 'react';

import { ActivityHistory } from '@/components/analytics/ActivityHistory';
import { Disclosure } from '@/components/analytics/Disclosure';
import {
  COL_METRIC,
  DataCell,
  GridTd,
  GroupTh,
  SubTh,
} from '@/components/analytics/DataTable';
import {
  EmptyState,
  RowSkeleton,
  formatDateID,
  formatDuration,
  formatHours,
} from '@/components/analytics/Primitives';
import { DAILY_GROUPS, dailyCSV } from '@/components/analytics/dailyColumns';
import type { AnalyticsQuery } from '@/lib/api';
import type { PerformanceDay } from '@/lib/types';

/**
 * Every metric, one row per day.
 *
 * Forty-one columns under eight group headings, all of them always present.
 * An earlier version let the reader hide groups; it was removed because it
 * gave the table a different shape for every reader, and a table people
 * compare screenshots of has to be one table.
 *
 * Width is what makes it legible rather than merely wide. Every metric column
 * is exactly the same size, set by the colgroup and held by `table-fixed`,
 * and every header and body cell has a fixed height. The result reads as a
 * grid: the eye can run down a column or across a row without either one
 * wandering.
 *
 * The rows come from the same report the cards above are built from, so a
 * column here and a card there can never disagree: they are the same array.
 * The CSV comes from the same column model as the header, so the export and
 * the screen cannot drift apart either.
 */

const PAGE = 14;

/**
 * One width for every metric column, and a wider one for the date.
 *
 * Fixed rather than content-sized because the content is counts: most are one
 * or two digits, and letting the heading decide the width makes a column of
 * "0"s as wide as the words above it. 104px fits a four-digit figure with
 * room to spare and lets the longest heading wrap to two lines.
 */
const METRIC_W = COL_METRIC;
const PERIOD_W = 150;

type SortDir = 'desc' | 'asc';

export function DailyBreakdown({
  days,
  loading,
  query,
}: {
  days: PerformanceDay[];
  loading?: boolean;
  query: AnalyticsQuery;
}) {
  const [dir, setDir] = useState<SortDir>('desc');
  const [page, setPage] = useState(0);
  const [open, setOpen] = useState<PerformanceDay | null>(null);

  const sorted = useMemo(() => {
    const rows = [...days];
    rows.sort((a, b) => (dir === 'asc' ? a.date.localeCompare(b.date) : b.date.localeCompare(a.date)));
    return rows;
  }, [days, dir]);

  const pages = Math.max(1, Math.ceil(sorted.length / PAGE));
  const current = Math.min(page, pages - 1);
  const shown = sorted.slice(current * PAGE, current * PAGE + PAGE);

  return (
    <Disclosure
      icon={CalendarDays}
      title="Rincian Per Hari"
      description="Tekan satu baris untuk membuka seluruh angka hari itu."
      summary={`${sorted.length} hari`}
      actions={
        <>
          <button
            type="button"
            onClick={() => setDir((d) => (d === 'desc' ? 'asc' : 'desc'))}
            className="inline-flex items-center gap-1.5 rounded-lg border border-hairline bg-surface-raised px-2.5 py-1.5 text-xs font-medium text-ink-soft transition-colors hover:bg-surface-sunken"
          >
            <ArrowUpDown className="size-3.5" />
            {dir === 'desc' ? 'Terbaru dulu' : 'Terlama dulu'}
          </button>
          <button
            type="button"
            onClick={() => downloadCSV(sorted)}
            disabled={sorted.length === 0}
            className="inline-flex items-center gap-1.5 rounded-lg border border-hairline bg-surface-raised px-2.5 py-1.5 text-xs font-medium text-ink-soft transition-colors hover:bg-surface-sunken disabled:opacity-50"
          >
            <Download className="size-3.5" />
            Export CSV
          </button>
        </>
      }
    >
      {loading && days.length === 0 ? (
        <RowSkeleton count={5} />
      ) : sorted.length === 0 ? (
        <EmptyState
          title="Belum ada aktivitas pada periode ini."
          hint="Tidak ada satu pun hari dengan aktivitas pada periode ini. Ubah periode atau filter aplikasi di baris filter di atas."
        />
      ) : (
        <>
          {/*
            * Every column the same width, set by a colgroup and held there by
            * `table-fixed`.
            *
            * That is the whole fix. Left to size itself, the browser gave
            * "Aktivitas di Luar Jadwal" four times the width of "Gagal", so
            * the group headers sat off-centre over ragged spans and the
            * numbers underneath never lined up down the page. A table of
            * counts should read as a grid, and a grid needs one column width.
            *
            * Labels longer than the column wrap inside a header box of fixed
            * height, so a two-word and a four-word heading occupy the same
            * rectangle. Nothing is abbreviated: the wording stays exactly as
            * specified, and the shape is what is made regular.
            *
            * Both header rows stay put vertically; the date column stays put
            * horizontally; a firmer rule marks where one group ends.
            */}
          <div className="max-h-[70vh] overflow-auto rounded-card border border-hairline bg-surface-raised shadow-e1">
            <table className="w-full table-fixed border-collapse text-sm">
              <colgroup>
                <col style={{ width: PERIOD_W }} />
                {DAILY_GROUPS.map((g) =>
                  g.columns.map((c) => <col key={c.key} style={{ width: METRIC_W }} />),
                )}
              </colgroup>
              <thead>
                {/* The group row sits at the very top; the subheader row sits
                    directly beneath it, which is what `top-[33px]` matches.
                    Both are sticky, so scrolling never orphans the numbers. */}
                <tr className="sticky top-0 z-30">
                  <GroupTh rowSpan={2} sticky>
                    Periode
                  </GroupTh>
                  {DAILY_GROUPS.map((g, i) => (
                    <GroupTh
                      key={g.key}
                      colSpan={g.columns.length}
                      divide={i < DAILY_GROUPS.length - 1}
                    >
                      {g.label}
                    </GroupTh>
                  ))}
                </tr>
                <tr className="sticky top-[33px] z-20">
                  {DAILY_GROUPS.map((g, gi) =>
                    g.columns.map((c, ci) => (
                      <SubTh
                        key={c.key}
                        info={c.info}
                        divide={ci === g.columns.length - 1 && gi < DAILY_GROUPS.length - 1}
                      >
                        {c.label}
                      </SubTh>
                    )),
                  )}
                </tr>
              </thead>
              <tbody>
                {shown.map((d) => (
                  <tr
                    key={d.date}
                    onClick={() => setOpen(d)}
                    // A fixed row height, for the same reason the headers have
                    // one: every row in a grid of counts should be the same
                    // rectangle, whatever happens to be in it.
                    className="group h-[38px] cursor-pointer border-b border-hairline transition-colors last:border-0 hover:bg-surface-sunken/50"
                  >
                    <GridTd sticky className="font-medium whitespace-nowrap text-ink">
                      {formatDateID(d.date)}
                    </GridTd>
                    {DAILY_GROUPS.map((g, gi) =>
                      g.columns.map((c, ci) => (
                        <DataCell
                          key={c.key}
                          value={c.value(d)}
                          divide={ci === g.columns.length - 1 && gi < DAILY_GROUPS.length - 1}
                        />
                      )),
                    )}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>

          {pages > 1 ? (
            <div className="mt-2.5 flex items-center justify-between gap-3">
              <p className="text-xs text-ink-muted">
                {current * PAGE + 1}–{Math.min((current + 1) * PAGE, sorted.length)} dari{' '}
                {sorted.length} hari
              </p>
              <div className="flex gap-1.5">
                <PageButton disabled={current === 0} onClick={() => setPage(current - 1)}>
                  Sebelumnya
                </PageButton>
                <PageButton disabled={current >= pages - 1} onClick={() => setPage(current + 1)}>
                  Berikutnya
                </PageButton>
              </div>
            </div>
          ) : null}
        </>
      )}

      {open ? <DayDrawer day={open} query={query} onClose={() => setOpen(null)} /> : null}
    </Disclosure>
  );
}

/** Everything about one day, including what was done in it. */
function DayDrawer({
  day,
  query,
  onClose,
}: {
  day: PerformanceDay;
  query: AnalyticsQuery;
  onClose: () => void;
}) {
  // The activity feed is narrowed to this one date, keeping every other filter
  // the page already applies — same scope, same application, same person.
  const dayQuery: AnalyticsQuery = {
    ...query,
    date: day.date,
    month: undefined,
    from: undefined,
    to: undefined,
  };

  return (
    <>
      <div onClick={onClose} className="fixed inset-0 z-40 bg-ink/30" />
      <aside className="fixed inset-y-0 right-0 z-50 flex w-full max-w-[760px] flex-col bg-surface shadow-e4">
        <header className="flex items-center justify-between gap-3 border-b border-hairline bg-surface-raised px-5 py-3.5">
          <div>
            <h2 className="text-lg font-semibold tracking-[-0.01em] text-ink">{formatDateID(day.date)}</h2>
            <p className="text-xs text-ink-muted">Seluruh angka dan aktivitas hari ini.</p>
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
          <Group title="Chat Engagement">
            <Cell label="Kontak Masuk" value={day.contacts_inbound} />
            <Cell label="Leads Baru" value={day.verified_new_leads} />
            <Cell label="Kontak Ditangani" value={day.contacts_served} />
            <Cell label="Belum Ditangani" value={day.contacts_unserved} />
            <Cell label="Pesan Masuk" value={day.inbound_personal} />
            <Cell label="Keluar (Web)" value={day.outbound_manual_personal} />
            <Cell label="Keluar (HP)" value={day.outbound_device_personal} />
          </Group>

          <Group title="Group Engagement">
            <Cell label="Grup Aktif" value={day.groups_active} />
            <Cell label="Grup Ditangani" value={day.groups_handled} />
            <Cell label="Pesan Masuk Grup" value={day.group_inbound} />
            <Cell label="Pesan Keluar Grup" value={day.group_replies} />
          </Group>

          <Group title="Performa SLA">
            <Cell label="Rata-rata Respons" value={formatDuration(day.avg_first_response_seconds)} />
            <Cell label="Tercepat" value={formatDuration(day.fastest_response_seconds)} />
            <Cell label="Terlama" value={formatDuration(day.slowest_response_seconds)} />
            <Cell label="SLA Tercapai" value={day.sla_achieved} />
            <Cell label="SLA Terlewati" value={day.sla_breached} />
            <Cell label="Menunggu" value={day.sla_waiting} />
          </Group>

          <Group title="Follow-up">
            <Cell label="Total" value={day.follow_ups} />
            <Cell label="Kontak" value={day.follow_up_contacts} />
            <Cell label="Dibalas" value={day.follow_ups_answered} />
            <Cell label="Belum Dibalas" value={day.follow_ups_unanswered} />
          </Group>

          <Group title="Label">
            <Cell label="Mulai Berlabel" value={day.contacts_first_labeled} />
            <Cell label="Dipasang" value={day.labels_assigned} />
            <Cell label="Dilepas" value={day.labels_removed} />
            <Cell label="Perpindahan" value={day.labels_moved} />
            <Cell label="Kontak Berubah" value={day.label_contacts_changed} />
          </Group>

          <Group title="Broadcast & WA Story">
            <Cell label="Broadcast Dibuat" value={day.broadcasts_created} />
            <Cell label="Selesai" value={day.broadcasts_sent} />
            <Cell label="Sebagian" value={day.broadcasts_partial} />
            <Cell label="Gagal" value={day.broadcasts_failed} />
            <Cell label="Target Terkirim" value={day.broadcast_targets_sent} />
            <Cell label="Story Terposting" value={day.stories_published} />
            <Cell label="Views Terdeteksi" value={day.story_views_detected} />
          </Group>

          <Group title="Jadwal">
            <Cell label="Jam Kerja" value={formatHours(day.work_seconds)} />
            <Cell label="Di Dalam Jadwal" value={day.activities_in_schedule} />
            <Cell label="Di Luar Jadwal" value={day.activities_out_of_schedule} />
          </Group>

          <ActivityHistory
            query={dayQuery}
            title="Aktivitas hari ini"
            description="Mengikuti filter halaman, dipersempit ke tanggal ini."
          />
        </div>
      </aside>
    </>
  );
}

function Group({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section className="mb-4">
      <h3 className="mb-2 text-2xs font-semibold tracking-wide text-ink-muted uppercase">
        {title}
      </h3>
      <div className="grid grid-cols-2 gap-2 sm:grid-cols-3 lg:grid-cols-4">{children}</div>
    </section>
  );
}

function Cell({ label, value }: { label: string; value: React.ReactNode }) {
  return (
    <div className="rounded-lg border border-hairline bg-surface-raised px-3 py-2">
      <p className="truncate text-2xs text-ink-muted">{label}</p>
      <p className="mt-0.5 text-base font-semibold text-ink nums">{value}</p>
    </div>
  );
}

function PageButton({
  children,
  disabled,
  onClick,
}: {
  children: React.ReactNode;
  disabled?: boolean;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      disabled={disabled}
      onClick={onClick}
      className="rounded-lg border border-hairline bg-surface-raised px-2.5 py-1.5 text-xs font-medium text-ink-soft transition-colors hover:bg-surface-sunken disabled:opacity-40"
    >
      {children}
    </button>
  );
}

/**
 * Exports what is on screen.
 *
 * Built from the rows already fetched, so it carries exactly the scope the
 * reader is allowed to see — there is no second, wider query behind it that
 * could hand somebody data the page would not show them.
 */
/**
 * Export over every group, whatever is hidden on screen.
 *
 * The column model owns both the table and this file, so a column can never
 * appear under one name on screen and another in the spreadsheet.
 */
function downloadCSV(rows: PerformanceDay[]) {
  const blob = new Blob([dailyCSV(rows)], { type: 'text/csv;charset=utf-8' });
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = `performa-${rows[rows.length - 1]?.date ?? 'export'}.csv`;
  a.click();
  URL.revokeObjectURL(url);
}
