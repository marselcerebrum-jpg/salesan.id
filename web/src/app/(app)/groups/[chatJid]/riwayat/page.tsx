'use client';

import { ArrowLeft, Users } from 'lucide-react';
import Link from 'next/link';
import { useParams } from 'next/navigation';
import { useMemo, useState } from 'react';
import useSWR from 'swr';

import { EmptyState, ErrorNote, Spinner } from '@/components/ui/Primitives';
import {
  fetcher,
  groupMemberHistoryPath,
  groupMembersPath,
  type GroupDetail,
  type GroupMemberHistory,
} from '@/lib/api';

const MONTHS = [
  'Januari', 'Februari', 'Maret', 'April', 'Mei', 'Juni',
  'Juli', 'Agustus', 'September', 'Oktober', 'November', 'Desember',
];

/** The Jakarta calendar date, which is the one the server counts days by. */
function jakartaToday() {
  const parts = new Intl.DateTimeFormat('en-CA', {
    timeZone: 'Asia/Jakarta',
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
  }).format(new Date());
  const [y, m] = parts.split('-').map(Number);
  return { year: y, month: m };
}

/**
 * One group's membership over a month.
 *
 * A page rather than a dialog over the directory. A dialog was the wrong shape
 * for it twice over: there is no room for a month of days beside a chart, and a
 * period somebody is comparing across groups needs a URL they can keep and send.
 */
export default function GroupHistoryPage() {
  const params = useParams<{ chatJid: string }>();
  // Next decodes the segment, so this is the JID as stored, '@g.us' and all.
  const chatJid = decodeURIComponent(params.chatJid);

  // Both of these are read by the memos below, so they must not be rebuilt on
  // every render: a fresh object each time makes the memos run every time,
  // which is the opposite of what they are for.
  const today = useMemo(jakartaToday, []);
  const [year, setYear] = useState(today.year);
  const [month, setMonth] = useState(today.month);

  const { data: detail } = useSWR<GroupDetail>(groupMembersPath(chatJid), fetcher, {
    revalidateOnFocus: false,
  });

  const { data, isLoading, error } = useSWR<GroupMemberHistory>(
    groupMemberHistoryPath(chatJid, year, month),
    fetcher,
    { revalidateOnFocus: false },
  );

  const days = useMemo(() => data?.days ?? [], [data]);
  const groupName = detail?.group?.name || chatJid;

  // Only the months that could hold something. The first recorded day is the
  // day this began being kept; offering anything before it would promise a past
  // that was never watched.
  const earliest = useMemo(
    () => (data?.first_day ? new Date(`${data.first_day}T00:00:00`) : null),
    [data?.first_day],
  );
  const years = useMemo(() => {
    const from = earliest ? earliest.getFullYear() : today.year;
    const out: number[] = [];
    for (let y = from; y <= today.year; y++) out.push(y);
    return out;
  }, [earliest, today.year]);

  const monthsForYear = useMemo(() => {
    const first = earliest && year === earliest.getFullYear() ? earliest.getMonth() + 1 : 1;
    const last = year === today.year ? today.month : 12;
    const out: number[] = [];
    for (let m = first; m <= last; m++) out.push(m);
    return out;
  }, [earliest, year, today]);

  const joined = days.reduce((n, d) => n + d.joined, 0);
  const left = days.reduce((n, d) => n + d.left, 0);
  const net = joined - left;

  return (
    <div className="px-5 py-6 lg:px-8 lg:py-8">
      <Link
        href={`/groups/${encodeURIComponent(chatJid)}`}
        className="inline-flex items-center gap-1.5 text-sm text-ink-muted transition-colors hover:text-ink-soft"
      >
        <ArrowLeft className="size-4" />
        {groupName}
      </Link>

      <header className="mt-3 flex flex-wrap items-end justify-between gap-4">
        <div className="min-w-0">
          <h1 className="text-2xl font-semibold tracking-[-0.02em] text-ink">Anggota per hari</h1>
          <p className="mt-1 text-sm text-ink-muted">
            Jumlah anggota grup ini pada tiap akhir hari, beserta siapa saja yang masuk dan keluar.
          </p>
        </div>

        <div className="flex items-center gap-2">
          <Select
            label="Bulan"
            value={month}
            options={monthsForYear.map((m) => ({ value: m, label: MONTHS[m - 1] }))}
            onChange={setMonth}
          />
          <Select
            label="Tahun"
            value={year}
            options={years.map((y) => ({ value: y, label: String(y) }))}
            onChange={(y) => {
              setYear(y);
              // A month that does not exist in the new year would ask the server
              // for a period it will answer as empty, which looks like data loss.
              const last = y === today.year ? today.month : 12;
              if (month > last) setMonth(last);
            }}
          />
        </div>
      </header>

      <div className="mt-6 rounded-panel border border-hairline bg-surface-raised">
        {isLoading ? (
          <div className="grid h-56 place-items-center">
            <Spinner />
          </div>
        ) : error ? (
          <div className="p-4">
            <ErrorNote message="Riwayat anggota tidak dapat dimuat." />
          </div>
        ) : days.length === 0 ? (
          <div className="p-6">
            <EmptyState
              icon={<Users className="size-6" />}
              title={`Tidak ada catatan pada ${MONTHS[month - 1]} ${year}`}
              description={
                data?.first_day
                  ? `Pencatatan grup ini dimulai ${data.first_day}. Bulan sebelum itu memang belum pernah dicatat.`
                  : 'Perubahan anggota mulai dicatat sejak fitur ini aktif. Grup ini belum punya catatan sama sekali; angkanya akan muncul setelah ada yang masuk atau keluar.'
              }
            />
          </div>
        ) : (
          <>
            <div className="flex flex-wrap items-baseline gap-x-5 gap-y-1 border-b border-hairline px-4 py-3 text-sm">
              <span className="text-ink-soft">
                <span className="nums font-medium text-ink">{days.length}</span> hari tercatat
              </span>
              <span className="text-ink-soft">
                Masuk <span className="nums font-medium text-brand-800">+{joined}</span>
              </span>
              <span className="text-ink-soft">
                Keluar <span className="nums font-medium text-danger">−{left}</span>
              </span>
              <span className="text-ink-soft">
                Bersih{' '}
                <span
                  className={
                    net === 0
                      ? 'nums font-medium text-ink'
                      : net > 0
                        ? 'nums font-medium text-brand-800'
                        : 'nums font-medium text-danger'
                  }
                >
                  {net > 0 ? `+${net}` : net}
                </span>
              </span>
            </div>

            <CountChart days={days} />

            <table className="w-full text-sm">
              <thead>
                <tr className="border-y border-hairline bg-surface-sunken/40 text-2xs text-ink-muted uppercase">
                  <th className="px-4 py-2 text-left font-medium">Tanggal</th>
                  <th className="px-4 py-2 text-right font-medium">Masuk</th>
                  <th className="px-4 py-2 text-right font-medium">Keluar</th>
                  <th className="px-4 py-2 text-right font-medium">Total anggota</th>
                </tr>
              </thead>
              <tbody>
                {[...days].reverse().map((d) => (
                  <tr key={d.day} className="border-b border-hairline last:border-0">
                    <td className="px-4 py-2 text-ink-soft">{d.day}</td>
                    <td className="nums px-4 py-2 text-right text-brand-800">
                      {d.joined > 0 ? `+${d.joined}` : <span className="text-ink-muted">–</span>}
                    </td>
                    <td className="nums px-4 py-2 text-right text-danger">
                      {d.left > 0 ? `−${d.left}` : <span className="text-ink-muted">–</span>}
                    </td>
                    <td className="nums px-4 py-2 text-right font-medium text-ink">
                      {d.member_count.toLocaleString('id-ID')}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </>
        )}
      </div>
    </div>
  );
}

/**
 * The head count across the month.
 *
 * A line, not bars. Bars claim their height is the value measured from zero,
 * and a group of a thousand that loses five drew one bar six times the height
 * of the next — a half-percent change reading as a collapse. A line carries no
 * such claim, and the axis states the range it is drawn over, so the shape can
 * be read for what it is: the direction of travel, not the size of the group.
 */
function CountChart({ days }: { days: { day: string; member_count: number }[] }) {
  const counts = days.map((d) => d.member_count);
  const top = Math.max(...counts);
  const floor = Math.min(...counts);
  // A flat month would otherwise divide by zero and draw nothing.
  const pad = Math.max(1, Math.round((top - floor) * 0.25));
  const lo = Math.max(0, floor - pad);
  const hi = top + pad;
  const span = Math.max(1, hi - lo);

  const W = 100;
  const H = 34;
  const x = (i: number) => (days.length === 1 ? W / 2 : (i / (days.length - 1)) * W);
  const y = (v: number) => H - ((v - lo) / span) * H;
  const line = days.map((d, i) => `${x(i)},${y(d.member_count)}`).join(' ');

  return (
    <div className="px-4 py-4">
      <div className="flex gap-3">
        <div className="flex flex-col justify-between py-0.5 text-2xs text-ink-muted">
          <span className="nums">{hi.toLocaleString('id-ID')}</span>
          <span className="nums">{lo.toLocaleString('id-ID')}</span>
        </div>

        <svg
          viewBox={`0 0 ${W} ${H}`}
          preserveAspectRatio="none"
          role="img"
          aria-label={`Jumlah anggota dari ${days[0].day} sampai ${days[days.length - 1].day}`}
          className="h-32 flex-1"
        >
          <polyline
            points={line}
            fill="none"
            stroke="currentColor"
            strokeWidth={0.7}
            strokeLinejoin="round"
            strokeLinecap="round"
            className="text-brand-700"
            vectorEffect="non-scaling-stroke"
          />
          {days.map((d, i) => (
            <circle
              key={d.day}
              cx={x(i)}
              cy={y(d.member_count)}
              r={1}
              className="fill-brand-800"
              vectorEffect="non-scaling-stroke"
            />
          ))}
        </svg>
      </div>

      <p className="mt-2 flex justify-between text-2xs text-ink-muted">
        <span>{days[0].day}</span>
        {days.length > 1 ? <span>{days[days.length - 1].day}</span> : null}
      </p>
      <p className="mt-1 text-2xs text-ink-muted">
        Sumbu tegak mulai dari {lo.toLocaleString('id-ID')}, bukan dari nol, supaya perubahan kecil
        pada grup besar tetap terbaca. Hanya hari yang tercatat yang digambar.
      </p>
    </div>
  );
}

function Select<T extends number>({
  label,
  value,
  options,
  onChange,
}: {
  label: string;
  value: T;
  options: { value: T; label: string }[];
  onChange: (value: T) => void;
}) {
  return (
    <label className="flex flex-col gap-1">
      <span className="text-2xs font-medium text-ink-muted uppercase">{label}</span>
      <select
        value={value}
        onChange={(e) => onChange(Number(e.target.value) as T)}
        className="h-9 rounded-control border border-hairline bg-surface-raised px-2.5 text-sm text-ink"
      >
        {options.map((o) => (
          <option key={o.value} value={o.value}>
            {o.label}
          </option>
        ))}
      </select>
    </label>
  );
}
