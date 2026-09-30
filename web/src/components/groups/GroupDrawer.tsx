'use client';

import clsx from 'clsx';
import {
  Copy,
  FileSpreadsheet,
  FileText,
  Loader2,
  RefreshCw,
  Search,
  ShieldCheck,
  Users,
  X,
} from 'lucide-react';
import { useEffect, useMemo, useRef, useState } from 'react';
import useSWR from 'swr';

import { EmptyState, ErrorNote, Spinner } from '@/components/ui/Primitives';
import {
  exportGroupMembers,
  fetcher,
  groupMemberHistoryPath,
  groupMembersPath,
  refreshDirectoryGroup,
  type GroupDetail,
  type GroupMemberHistory,
} from '@/lib/api';

export type GroupTab = 'anggota' | 'aktivitas';

const MONTHS = [
  'Januari', 'Februari', 'Maret', 'April', 'Mei', 'Juni',
  'Juli', 'Agustus', 'September', 'Oktober', 'November', 'Desember',
];

/** The Jakarta calendar date, which is the one the server counts days by. */
function jakartaToday() {
  const [y, m] = new Intl.DateTimeFormat('en-CA', {
    timeZone: 'Asia/Jakarta',
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
  })
    .format(new Date())
    .split('-')
    .map(Number);
  return { year: y, month: m };
}

/**
 * One group, beside the directory rather than instead of it.
 *
 * An operator watching nine hundred groups is comparing them, and a page swap
 * loses the row they came from along with their place in the list. The drawer
 * keeps the table visible and puts the group's own detail next to it.
 *
 * Which group and which tab live in the URL, so the thing a drawer usually
 * costs — a view nobody can send to a colleague — is not paid here.
 */
export function GroupDrawer({
  chatJid,
  tab,
  onTab,
  onClose,
}: {
  chatJid: string | null;
  tab: GroupTab;
  onTab: (tab: GroupTab) => void;
  onClose: () => void;
}) {
  const open = chatJid !== null;

  // Callers pass an inline arrow, so its identity changes every render. Read
  // through a ref to keep it out of the effect's dependencies.
  const onCloseRef = useRef(onClose);
  onCloseRef.current = onClose;

  useEffect(() => {
    if (!open) return;
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') onCloseRef.current();
    };
    document.addEventListener('keydown', onKeyDown);
    return () => document.removeEventListener('keydown', onKeyDown);
  }, [open]);

  if (!open) return null;

  return (
    <>
      <div
        className="fixed inset-0 z-40 bg-black/25"
        onClick={onClose}
        aria-hidden
      />
      <aside
        role="dialog"
        aria-label="Detail grup"
        className="fixed inset-y-0 right-0 z-50 flex w-full max-w-[720px] flex-col border-l border-hairline bg-surface-raised shadow-e4"
      >
        <Body chatJid={chatJid} tab={tab} onTab={onTab} onClose={onClose} />
      </aside>
    </>
  );
}

/**
 * Split out so every piece of state inside it is discarded when the drawer
 * closes. Keeping it mounted would carry one group's search box and chosen
 * month onto the next group the operator opens.
 */
function Body({
  chatJid,
  tab,
  onTab,
  onClose,
}: {
  chatJid: string;
  tab: GroupTab;
  onTab: (tab: GroupTab) => void;
  onClose: () => void;
}) {
  const { data, isLoading, error, mutate } = useSWR<GroupDetail>(
    groupMembersPath(chatJid),
    fetcher,
    { revalidateOnFocus: false },
  );

  const group = data?.group;
  const members = useMemo(() => data?.members ?? [], [data]);
  const admins = useMemo(() => members.filter((m) => m.is_admin).length, [members]);

  const [busy, setBusy] = useState<null | 'copy' | 'csv' | 'xlsx' | 'refresh'>(null);
  const [note, setNote] = useState<string | null>(null);
  const [failure, setFailure] = useState<string | null>(null);

  async function refresh() {
    setBusy('refresh');
    setNote(null);
    setFailure(null);
    try {
      const fresh = await refreshDirectoryGroup(chatJid);
      await mutate(fresh, { revalidate: false });
      setNote(`${fresh.members.length.toLocaleString('id-ID')} anggota diperbarui.`);
    } catch (err) {
      setFailure(err instanceof Error ? err.message : 'Gagal memperbarui grup.');
    } finally {
      setBusy(null);
    }
  }

  return (
    <>
      <header className="flex items-start gap-3 border-b border-hairline px-5 py-4">
        <div className="min-w-0 flex-1">
          <h2 className="truncate text-lg font-semibold tracking-[-0.01em] text-ink">
            {group?.name || (isLoading ? 'Memuat…' : chatJid)}
          </h2>
          <p className="mt-0.5 text-sm text-ink-muted">
            <span className="nums font-medium text-ink-soft">
              {members.length.toLocaleString('id-ID')}
            </span>{' '}
            anggota tersimpan
            {admins > 0 ? (
              <>
                {' · '}
                <span className="nums font-medium text-ink-soft">{admins}</span> admin
              </>
            ) : null}
            {group && group.accounts.length > 0
              ? ` · ${group.accounts.map((a) => a.account_name).join(', ')}`
              : null}
          </p>
          {/* Said rather than hidden: the directory's count comes from
              WhatsApp's own metadata, and the stored list lags it whenever
              somebody joins between two fetches. */}
          {group && group.fetched && group.member_count !== members.length ? (
            <p className="mt-0.5 text-xs text-warn">
              WhatsApp menyebut{' '}
              <span className="nums">{group.member_count.toLocaleString('id-ID')}</span> anggota —
              tekan Perbarui.
            </p>
          ) : null}
        </div>

        <button
          type="button"
          onClick={onClose}
          aria-label="Tutup"
          className="grid size-8 shrink-0 place-items-center rounded-full text-ink-muted transition-colors hover:bg-surface-sunken hover:text-ink-soft"
        >
          <X className="size-4" />
        </button>
      </header>

      <div className="flex items-center gap-1 border-b border-hairline px-3">
        <Tab active={tab === 'anggota'} onClick={() => onTab('anggota')}>
          Anggota
        </Tab>
        <Tab active={tab === 'aktivitas'} onClick={() => onTab('aktivitas')}>
          Aktivitas
        </Tab>
      </div>

      {note ? (
        <p className="mx-5 mt-3 rounded-control border border-brand-600/25 bg-brand-600/8 px-3 py-2 text-sm text-brand-700">
          {note}
        </p>
      ) : null}
      {failure ? (
        <div className="mx-5 mt-3">
          <ErrorNote message={failure} />
        </div>
      ) : null}

      {tab === 'anggota' ? (
        <MembersTab
          chatJid={chatJid}
          members={members}
          isLoading={isLoading}
          loadError={error}
          busy={busy}
          setBusy={setBusy}
          setNote={setNote}
          setFailure={setFailure}
          onRefresh={refresh}
        />
      ) : (
        <ActivityTab chatJid={chatJid} />
      )}
    </>
  );
}

function Tab({
  active,
  onClick,
  children,
}: {
  active: boolean;
  onClick: () => void;
  children: React.ReactNode;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-current={active ? 'page' : undefined}
      className={clsx(
        'relative px-3 py-2.5 text-sm font-medium transition-colors',
        active
          ? 'text-ink after:absolute after:inset-x-3 after:-bottom-px after:h-0.5 after:rounded-full after:bg-brand-700'
          : 'text-ink-muted hover:text-ink-soft',
      )}
    >
      {children}
    </button>
  );
}

/* --- anggota ---------------------------------------------------------------- */

type Member = GroupDetail['members'][number];

function MembersTab({
  chatJid,
  members,
  isLoading,
  loadError,
  busy,
  setBusy,
  setNote,
  setFailure,
  onRefresh,
}: {
  chatJid: string;
  members: Member[];
  isLoading: boolean;
  loadError: unknown;
  busy: null | 'copy' | 'csv' | 'xlsx' | 'refresh';
  setBusy: (v: null | 'copy' | 'csv' | 'xlsx' | 'refresh') => void;
  setNote: (v: string | null) => void;
  setFailure: (v: string | null) => void;
  onRefresh: () => void;
}) {
  const [search, setSearch] = useState('');

  const shown = useMemo(() => {
    const needle = search.trim().toLowerCase();
    if (!needle) return members;
    // Digits only on both sides, so "0812 345" finds "62812345…". Typing a
    // number the way it is written down should not depend on the format we
    // happened to store it in.
    const digits = needle.replace(/\D/g, '');
    return members.filter((m) => {
      if (m.display_name.toLowerCase().includes(needle)) return true;
      if (!m.phone_number) return false;
      return digits.length > 0 && m.phone_number.replace(/\D/g, '').includes(digits);
    });
  }, [members, search]);

  async function copyAll() {
    if (shown.length === 0) return;
    setBusy('copy');
    setNote(null);
    setFailure(null);
    try {
      const lines = [
        ['Nama', 'Nomor', 'Admin'].join('\t'),
        // Blank where the address book has nothing. A LID pasted under "Nomor"
        // would be a column of numbers nobody can dial.
        ...shown.map((m) =>
          [m.display_name, m.phone_number ?? '', m.is_admin ? 'admin' : ''].join('\t'),
        ),
      ];
      await navigator.clipboard.writeText(lines.join('\n'));
      setNote(
        `${shown.length.toLocaleString('id-ID')} anggota disalin${
          shown.length !== members.length ? ' (hasil pencarian ini)' : ''
        }.`,
      );
    } catch {
      setFailure('Tidak bisa menyalin. Browser menolak akses clipboard.');
    } finally {
      setBusy(null);
    }
  }

  async function download(format: 'csv' | 'xlsx') {
    setBusy(format);
    setNote(null);
    setFailure(null);
    try {
      // The server exports the whole membership, not the search: a file named
      // after the group should hold the group.
      await exportGroupMembers(chatJid, format);
      setNote(`${members.length.toLocaleString('id-ID')} anggota diekspor.`);
    } catch (err) {
      setFailure(err instanceof Error ? err.message : 'Ekspor gagal.');
    } finally {
      setBusy(null);
    }
  }

  return (
    <>
      <div className="flex flex-wrap items-center gap-2 px-5 py-3">
        <label className="relative min-w-[180px] flex-1">
          <Search className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-ink-muted" />
          <input
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder="Cari nama/nomor…"
            aria-label="Cari anggota"
            className="h-9 w-full rounded-control bg-surface-sunken/60 pr-3 pl-9 text-sm text-ink outline-none placeholder:text-ink-muted focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-brand-600"
          />
        </label>
        <Action icon={Copy} busy={busy === 'copy'} onClick={() => void copyAll()}>
          Salin
        </Action>
        <Action icon={FileText} busy={busy === 'csv'} onClick={() => void download('csv')}>
          CSV
        </Action>
        <Action
          icon={FileSpreadsheet}
          busy={busy === 'xlsx'}
          onClick={() => void download('xlsx')}
        >
          XLSX
        </Action>
        <Action icon={RefreshCw} busy={busy === 'refresh'} onClick={onRefresh} primary>
          Perbarui
        </Action>
      </div>

      <div className="min-h-0 flex-1 overflow-auto border-t border-hairline">
        {isLoading ? (
          <div className="p-6">
            <Spinner label="Memuat anggota…" />
          </div>
        ) : loadError ? (
          <div className="p-6">
            <ErrorNote message="Gagal memuat anggota grup." />
          </div>
        ) : members.length === 0 ? (
          <div className="p-6">
            <EmptyState
              icon={<Users className="size-6" />}
              title="Anggota belum diambil"
              description="Daftar anggota grup ini belum pernah ditarik dari WhatsApp. Tekan Perbarui di atas."
            />
          </div>
        ) : shown.length === 0 ? (
          <div className="p-6">
            <EmptyState
              title="Tidak ada yang cocok"
              description={`Tidak ada anggota dengan nama atau nomor "${search.trim()}".`}
            />
          </div>
        ) : (
          <table className="w-full border-collapse text-sm">
            <thead>
              <tr className="sticky top-0 z-10 border-b border-hairline bg-surface-raised text-2xs text-ink-muted uppercase">
                <th className="w-12 px-4 py-2 text-left font-semibold">No</th>
                <th className="px-4 py-2 text-left font-semibold">Nama</th>
                <th className="w-48 px-4 py-2 text-left font-semibold">Nomor</th>
              </tr>
            </thead>
            <tbody>
              {shown.map((member, index) => (
                <tr
                  key={member.jid}
                  className="border-b border-hairline last:border-0 hover:bg-surface-sunken/50"
                >
                  <td className="nums px-4 py-2 text-ink-muted">{index + 1}</td>
                  <td className="px-4 py-2">
                    <span className="flex flex-wrap items-center gap-2">
                      {/* The name comes from the address book. When nobody has
                          saved this person we say so, rather than printing the
                          LID WhatsApp addresses them by. */}
                      {member.display_name ? (
                        <span className="font-medium text-ink">{member.display_name}</span>
                      ) : (
                        <span className="text-ink-muted italic">Belum tersimpan</span>
                      )}
                      {member.is_admin ? (
                        <span className="inline-flex items-center gap-1 rounded-full bg-brand-600/10 px-2 py-0.5 text-2xs font-semibold text-brand-700">
                          <ShieldCheck className="size-3" />
                          Admin
                        </span>
                      ) : null}
                    </span>
                  </td>
                  <td className="px-4 py-2">
                    {/* Empty when we cannot resolve a real number. WhatsApp's
                        masked placeholder shows a few real digits and hides the
                        rest, which reads as a number without being one. */}
                    {member.phone_number ? (
                      <span className="nums text-ink-soft">+{member.phone_number}</span>
                    ) : (
                      <span className="text-ink-muted">–</span>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </>
  );
}

/* --- aktivitas -------------------------------------------------------------- */

function ActivityTab({ chatJid }: { chatJid: string }) {
  const today = useMemo(jakartaToday, []);
  const [year, setYear] = useState(today.year);
  const [month, setMonth] = useState(today.month);

  const { data, isLoading, error } = useSWR<GroupMemberHistory>(
    groupMemberHistoryPath(chatJid, year, month),
    fetcher,
    { revalidateOnFocus: false },
  );

  const days = useMemo(() => data?.days ?? [], [data]);

  // Only the months that could hold something. This began being kept the day it
  // shipped, and a filter offering last year would promise a past nobody watched.
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
    <>
      <div className="flex flex-wrap items-end gap-2 px-5 py-3">
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
            // A month that cannot exist in the new year would ask the server for
            // a period it answers as empty, which reads as data loss.
            const last = y === today.year ? today.month : 12;
            if (month > last) setMonth(last);
          }}
        />
      </div>

      <div className="min-h-0 flex-1 overflow-auto border-t border-hairline">
        {isLoading ? (
          <div className="p-6">
            <Spinner label="Memuat riwayat…" />
          </div>
        ) : error ? (
          <div className="p-6">
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
                  : 'Perubahan anggota mulai dicatat sejak fitur ini aktif. Grup ini belum punya catatan; angkanya muncul setelah ada yang masuk atau keluar.'
              }
            />
          </div>
        ) : (
          <>
            <div className="flex flex-wrap items-baseline gap-x-5 gap-y-1 px-5 py-3 text-sm">
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
                  className={clsx(
                    'nums font-medium',
                    net === 0 ? 'text-ink' : net > 0 ? 'text-brand-800' : 'text-danger',
                  )}
                >
                  {net > 0 ? `+${net}` : net}
                </span>
              </span>
            </div>

            <CountChart days={days} />

            <table className="w-full text-sm">
              <thead>
                <tr className="border-y border-hairline bg-surface-sunken/40 text-2xs text-ink-muted uppercase">
                  <th className="px-5 py-2 text-left font-semibold">Tanggal</th>
                  <th className="px-4 py-2 text-right font-semibold">Masuk</th>
                  <th className="px-4 py-2 text-right font-semibold">Keluar</th>
                  <th className="px-5 py-2 text-right font-semibold">Total anggota</th>
                </tr>
              </thead>
              <tbody>
                {[...days].reverse().map((d) => (
                  <tr key={d.day} className="border-b border-hairline last:border-0">
                    <td className="px-5 py-2 text-ink-soft">{d.day}</td>
                    <td className="nums px-4 py-2 text-right text-brand-800">
                      {d.joined > 0 ? `+${d.joined}` : <span className="text-ink-muted">–</span>}
                    </td>
                    <td className="nums px-4 py-2 text-right text-danger">
                      {d.left > 0 ? `−${d.left}` : <span className="text-ink-muted">–</span>}
                    </td>
                    <td className="nums px-5 py-2 text-right font-medium text-ink">
                      {d.member_count.toLocaleString('id-ID')}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </>
        )}
      </div>
    </>
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

  return (
    <div className="px-5 pb-4">
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
          className="h-28 flex-1"
        >
          <polyline
            points={days.map((d, i) => `${x(i)},${y(d.member_count)}`).join(' ')}
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

/* --- shared ----------------------------------------------------------------- */

function Action({
  icon: Icon,
  children,
  onClick,
  busy = false,
  primary = false,
}: {
  icon: typeof Copy;
  children: React.ReactNode;
  onClick: () => void;
  busy?: boolean;
  primary?: boolean;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={busy}
      className={clsx(
        'inline-flex h-9 shrink-0 items-center gap-1.5 rounded-control px-3 text-sm font-medium transition-colors disabled:opacity-50',
        primary
          ? 'bg-brand-700 text-white hover:bg-brand-800'
          : 'border border-hairline bg-surface-raised text-ink-soft hover:bg-surface-sunken',
      )}
    >
      {busy ? <Loader2 className="size-4 animate-spin" /> : <Icon className="size-4" />}
      {children}
    </button>
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
