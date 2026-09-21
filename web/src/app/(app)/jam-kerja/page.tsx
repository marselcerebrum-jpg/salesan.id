'use client';

import clsx from 'clsx';
import {
  Briefcase,
  ChevronDown,
  ChevronRight,
  Copy,
  Crown,
  HelpCircle,
  Info,
  Plus,
  Trash2,
  User,
  X,
} from 'lucide-react';
import { useMemo, useState } from 'react';
import useSWR from 'swr';

import { EmptyState, ErrorState, RowSkeleton } from '@/components/analytics/Primitives';
import { Button } from '@/components/ui/Button';
import { Modal } from '@/components/ui/Modal';
import { inputClassSm } from '@/components/ui/control';
import {
  deleteSchedule,
  fetcher,
  orgMembersPath,
  saveSchedule,
  schedulesPath,
  type OrgMembersResponse,
} from '@/lib/api';
import type { AnalyticsScope, OrgMember, WorkSchedule } from '@/lib/types';

/**
 * Jam Kerja — the hours the SLA is measured against.
 *
 * A weekly pattern rather than a rota to fill in: "Senin 08:00–17:00" holds
 * until somebody changes it. The alternative was a row per person per day
 * forever, and the day nobody fills it in is the day the SLA silently falls
 * back to wall-clock and starts scoring 2am messages against a shift that did
 * not exist.
 *
 * Only a Leader may write here. That is enforced by the server, not by hiding
 * the buttons: these hours decide whose response times get scored, and a rule
 * somebody can change about themselves is not a rule. Everyone else can read
 * it, deliberately — being measured against hours you are not allowed to see is
 * a trap.
 */

/**
 * Monday first, Sunday last — the order a working week is read in.
 *
 * The stored number is Postgres' own (0 = Sunday), because that is what
 * extract(dow) returns and translating it in SQL would be a second place for
 * the two to disagree. Only the reading order is rearranged, here, once.
 */
const DAYS: { weekday: number; label: string }[] = [
  { weekday: 1, label: 'Senin' },
  { weekday: 2, label: 'Selasa' },
  { weekday: 3, label: 'Rabu' },
  { weekday: 4, label: 'Kamis' },
  { weekday: 5, label: 'Jumat' },
  { weekday: 6, label: 'Sabtu' },
  { weekday: 0, label: 'Minggu' },
];

const DEFAULT_START = '08:00';
const DEFAULT_END = '17:00';

export default function WorkHoursPage() {
  const schedules = useSWR<{ schedules: WorkSchedule[]; scope: AnalyticsScope }>(
    schedulesPath({ kind: 'weekly' }),
    fetcher,
  );
  const org = useSWR<OrgMembersResponse>(orgMembersPath, fetcher);
  const [failure, setFailure] = useState<string | null>(null);
  const [guide, setGuide] = useState(false);
  const [banner, setBanner] = useState(true);
  const [open, setOpen] = useState<string | null>(null);
  const [copying, setCopying] = useState<OrgMember | null>(null);

  const canEdit = schedules.data?.scope.is_leader ?? false;

  // Only people who actually hold the inbox. A Leader who never answers chats
  // has no response time to measure, but they may still be given hours, so the
  // list is by role rather than by guesswork about who works.
  const members = useMemo(
    () => (org.data?.members ?? []).filter((m) => m.is_active && m.role !== ''),
    [org.data],
  );

  /*
   * Every session, by person and by day.
   *
   * A list rather than one row per day: the pattern allows a split shift —
   * 08:00–12:00 and 13:00–17:00 — and the unique key in the database includes
   * the start time precisely so that is possible. Keeping one row per day here
   * would have silently hidden the second session.
   */
  const byUser = useMemo(() => {
    const map = new Map<string, Map<number, WorkSchedule[]>>();
    for (const s of schedules.data?.schedules ?? []) {
      if (s.weekday === null) continue;
      if (!map.has(s.user_id)) map.set(s.user_id, new Map());
      const days = map.get(s.user_id)!;
      const list = days.get(s.weekday) ?? [];
      list.push(s);
      days.set(
        s.weekday,
        list.sort((a, b) => a.starts_at.localeCompare(b.starts_at)),
      );
    }
    return map;
  }, [schedules.data]);

  async function run(work: () => Promise<unknown>) {
    setFailure(null);
    try {
      await work();
      await schedules.mutate();
    } catch (e) {
      setFailure(e instanceof Error ? e.message : 'Gagal menyimpan jam kerja.');
    }
  }

  /**
   * Copies one person's whole week onto others.
   *
   * Their existing pattern is removed first. "Copy" that merged two patterns
   * would produce hours nobody chose: a day the source has off but the target
   * works would stay working, and the two would silently differ from then on.
   */
  async function copyPattern(source: OrgMember, targets: string[]) {
    const pattern = byUser.get(source.user_id) ?? new Map<number, WorkSchedule[]>();
    await run(async () => {
      for (const userId of targets) {
        for (const list of (byUser.get(userId) ?? new Map<number, WorkSchedule[]>()).values()) {
          for (const row of list) await deleteSchedule(row.id);
        }
        for (const [weekday, list] of pattern) {
          for (const row of list) {
            await saveSchedule({
              user_id: userId,
              weekday,
              starts_at: row.starts_at,
              ends_at: row.ends_at,
            });
          }
        }
      }
    });
    setCopying(null);
  }

  return (
    <div className="px-5 py-6 lg:px-8 lg:py-8">
      <div className="mx-auto w-full max-w-[1280px]">
        <nav aria-label="Jalur" className="flex items-center gap-1 text-2xs text-ink-muted">
          <span>Pengaturan</span>
          <ChevronRight className="size-3" aria-hidden />
          <span className="text-ink-soft">Jam Kerja</span>
        </nav>

        <header className="mt-1 flex flex-wrap items-start justify-between gap-x-4 gap-y-3">
          <div className="min-w-0">
            <h1 className="text-2xl font-semibold tracking-[-0.02em] text-ink">Jam Kerja</h1>
            <p className="mt-1 text-sm text-ink-muted">
              Atur jam kerja tiap anggota tim, berulang setiap pekan. Ini yang dipakai untuk menilai
              SLA.
            </p>
          </div>
          <Button onClick={() => setGuide(true)}>
            <HelpCircle className="size-4" />
            Panduan
          </Button>
        </header>

        {failure ? (
          <div className="mt-4">
            <ErrorState message={failure} onRetry={() => setFailure(null)} />
          </div>
        ) : null}

        {/* The one thing somebody has to know before touching this screen, and
            the one thing that is true while it is still empty. Dismissible,
            because it is an explanation rather than a warning. */}
        {banner ? (
          <div className="mt-4 flex items-start gap-2.5 rounded-card border border-info/25 bg-info-soft px-4 py-3">
            <Info className="mt-0.5 size-4 shrink-0 text-info" />
            <div className="min-w-0 flex-1 text-xs leading-relaxed text-ink-soft">
              <p>
                SLA hanya menilai pesan yang <span className="font-medium text-ink">masuk</span> di
                jam kerja dan <span className="font-medium text-ink">dibalas</span> di jam kerja.
                Pesan yang masuk di luar jam kerja dihitung terpisah sebagai antrean, bukan sebagai
                pelanggaran.
              </p>
              <p className="mt-1">
                Selama belum ada jam kerja yang diisi, SLA dinilai dengan jam dinding — pesan
                tengah malam ikut dihitung seolah ada yang bertugas.
              </p>
              {!canEdit ? (
                <p className="mt-1">
                  Hanya Leader yang dapat mengubah halaman ini. Anda tetap dapat melihatnya, karena
                  angka performa Anda dinilai terhadap jam-jam ini.
                </p>
              ) : null}
            </div>
            <button
              type="button"
              onClick={() => setBanner(false)}
              aria-label="Tutup"
              className="shrink-0 rounded-control p-1 text-ink-muted transition-colors hover:bg-surface-raised hover:text-ink"
            >
              <X className="size-4" />
            </button>
          </div>
        ) : null}

        {schedules.error || org.error ? (
          <div className="mt-5">
            <ErrorState
              message={
                (schedules.error ?? org.error) instanceof Error
                  ? ((schedules.error ?? org.error) as Error).message
                  : 'Gagal memuat.'
              }
              onRetry={() => {
                void schedules.mutate();
                void org.mutate();
              }}
            />
          </div>
        ) : schedules.isLoading || org.isLoading ? (
          <div className="mt-5 space-y-2">
            <RowSkeleton />
            <RowSkeleton />
          </div>
        ) : members.length === 0 ? (
          <div className="mt-5">
            <EmptyState
              title="Belum ada anggota yang punya peran."
              hint="Beri peran PIC atau Freelance di halaman Anggota & Peran lebih dulu."
            />
          </div>
        ) : (
          <div className="mt-4 space-y-3">
            {members.map((m, i) => {
              // The first person is open until somebody chooses otherwise. A
              // page of seven accordions all open is the wall this replaced.
              const expanded = open === null ? i === 0 : open === m.user_id;
              return (
              <PersonCard
                key={m.user_id}
                member={m}
                days={byUser.get(m.user_id) ?? new Map()}
                canEdit={canEdit}
                expanded={expanded}
                onToggle={() => setOpen(expanded ? '' : m.user_id)}
                onCopy={() => setCopying(m)}
                onSave={(weekday, starts, ends, replacing) =>
                  run(async () => {
                    await saveSchedule({
                      user_id: m.user_id,
                      weekday,
                      starts_at: starts,
                      ends_at: ends,
                    });
                    /*
                     * The row is keyed on its start time, so moving the start
                     * writes a new one rather than changing the old. Saving
                     * first and deleting second means a failure leaves the day
                     * with its old hours rather than with none.
                     */
                    if (replacing && replacing.starts_at !== starts) {
                      await deleteSchedule(replacing.id);
                    }
                  })
                }
                onClear={(rows) =>
                  run(async () => {
                    for (const row of rows) await deleteSchedule(row.id);
                  })
                }
              />
              );
            })}
          </div>
        )}
      </div>

      <Modal open={guide} onClose={() => setGuide(false)} title="Panduan Jam Kerja" size="md">
        <div className="space-y-3 text-sm leading-relaxed text-ink-soft">
          <p>
            Jam kerja di sini adalah pola mingguan: &ldquo;Senin 08:00–17:00&rdquo; berlaku setiap
            Senin sampai diubah. Tidak perlu diisi ulang tiap pekan.
          </p>
          <p>
            <span className="font-medium text-ink">Libur</span> berbeda dari{' '}
            <span className="font-medium text-ink">belum diisi</span> hanya dalam satu hal yang
            penting: pada hari libur tidak ada satu pun pesan yang dinilai SLA. Itulah sebabnya hari
            kosong ditandai Libur, bukan 00:00–00:00.
          </p>
          <p>
            Satu hari boleh punya lebih dari satu sesi, misalnya 08:00–12:00 dan 13:00–17:00. Waktu
            di antaranya tidak dihitung sebagai jam kerja.
          </p>
          <p>
            <span className="font-medium text-ink">Salin ke anggota lain</span> menimpa pola orang
            yang dipilih dengan pola ini, bukan menggabungkannya.
          </p>
        </div>
      </Modal>

      {copying ? (
        <CopyDialog
          source={copying}
          members={members}
          onClose={() => setCopying(null)}
          onCopy={(targets) => copyPattern(copying, targets)}
        />
      ) : null}
    </div>
  );
}

/* --- one person ---------------------------------------------------------- */

const ROLE_BADGE: Record<string, { label: string; icon: typeof Crown; tone: string }> = {
  leader: { label: 'Leader', icon: Crown, tone: 'bg-warn-soft text-amber-badge' },
  pic: { label: 'PIC', icon: User, tone: 'bg-info-soft text-info' },
  freelance: { label: 'Freelance', icon: Briefcase, tone: 'bg-surface-sunken text-ink-soft' },
};

function PersonCard({
  member,
  days,
  canEdit,
  expanded,
  onToggle,
  onCopy,
  onSave,
  onClear,
}: {
  member: OrgMember;
  days: Map<number, WorkSchedule[]>;
  canEdit: boolean;
  expanded: boolean;
  onToggle: () => void;
  onCopy: () => void;
  onSave: (
    weekday: number,
    starts: string,
    ends: string,
    replacing?: WorkSchedule,
  ) => Promise<void>;
  onClear: (rows: WorkSchedule[]) => Promise<void>;
}) {
  const name = member.full_name ?? member.email;
  const working = DAYS.filter(({ weekday }) => (days.get(weekday)?.length ?? 0) > 0).length;
  const badge = ROLE_BADGE[member.role] ?? ROLE_BADGE.freelance;
  const Icon = badge.icon;

  return (
    <section className="overflow-hidden rounded-card border border-hairline bg-surface-raised shadow-e1">
      <div className="flex flex-wrap items-center gap-3 px-4 py-3.5">
        <span className="flex size-9 shrink-0 items-center justify-center rounded-full bg-brand-600/12 text-xs font-semibold text-brand-700">
          {name
            .split(/\s+/)
            .slice(0, 2)
            .map((w) => w[0] ?? '')
            .join('')
            .toUpperCase() || '?'}
        </span>

        <div className="min-w-0 flex-1">
          <p className="truncate text-sm font-semibold text-ink">{name}</p>
          <p className="truncate text-2xs text-ink-muted">{member.email}</p>
        </div>

        <span
          className={clsx(
            'inline-flex shrink-0 items-center gap-1 rounded-full px-2 py-0.5 text-2xs font-medium',
            badge.tone,
          )}
        >
          <Icon className="size-3" aria-hidden />
          {badge.label}
        </span>

        <span className="shrink-0 text-2xs text-ink-muted">
          {working === 0 ? 'Belum ada jam kerja' : `${working} hari kerja per pekan`}
        </span>

        {canEdit ? (
          <Button size="sm" onClick={onCopy} disabled={working === 0}>
            <Copy className="size-3.5" />
            Salin ke anggota lain
          </Button>
        ) : null}

        <button
          type="button"
          onClick={onToggle}
          aria-expanded={expanded}
          aria-label={expanded ? `Tutup jam kerja ${name}` : `Buka jam kerja ${name}`}
          className="shrink-0 rounded-control border border-hairline p-1.5 text-ink-muted transition-colors hover:bg-surface-sunken hover:text-ink"
        >
          <ChevronDown className={clsx('size-4 transition-transform', expanded && 'rotate-180')} />
        </button>
      </div>

      {expanded ? (
        <div className="overflow-x-auto border-t border-hairline">
          <table className="w-full min-w-[720px] border-collapse text-xs">
            <thead>
              <tr className="text-left text-2xs tracking-wide text-ink-muted uppercase">
                {['Hari', 'Status', 'Jam kerja', ''].map((h, i) => (
                  <th key={h || i} className="border-b border-hairline px-4 py-2.5 font-semibold">
                    {h || <span className="sr-only">Aksi</span>}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {DAYS.map(({ weekday, label }) => (
                <DayRow
                  key={weekday}
                  label={label}
                  sessions={days.get(weekday) ?? []}
                  canEdit={canEdit}
                  onSave={(starts, ends, replacing) => onSave(weekday, starts, ends, replacing)}
                  onClear={onClear}
                />
              ))}
            </tbody>
          </table>
        </div>
      ) : null}
    </section>
  );
}

/**
 * One weekday, with however many sessions it has.
 *
 * A day with no session is a day off, and it reads as one rather than as 00:00
 * to 00:00. That distinction is the whole reason the SLA can tell "nobody was
 * on shift" apart from "somebody was on shift and did nothing".
 */
function DayRow({
  label,
  sessions,
  canEdit,
  onSave,
  onClear,
}: {
  label: string;
  sessions: WorkSchedule[];
  canEdit: boolean;
  onSave: (starts: string, ends: string, replacing?: WorkSchedule) => Promise<void>;
  onClear: (rows: WorkSchedule[]) => Promise<void>;
}) {
  const active = sessions.length > 0;
  // A session being added, held here until its hours are committed so nothing
  // is written the moment the button is pressed.
  const [adding, setAdding] = useState(false);

  return (
    <tr className="border-b border-hairline last:border-0">
      <td className="px-4 py-2.5 text-sm text-ink-soft">{label}</td>

      <td className="px-4 py-2.5">
        <span className="flex items-center gap-2">
          <Toggle
            checked={active}
            disabled={!canEdit}
            label={`${label} aktif`}
            onChange={(next) => {
              if (next) void onSave(DEFAULT_START, DEFAULT_END);
              else void onClear(sessions);
            }}
          />
          <span className={clsx('text-xs', active ? 'text-ink-soft' : 'text-ink-muted')}>
            {active ? 'Aktif' : 'Libur'}
          </span>
        </span>
      </td>

      <td className="px-4 py-2.5">
        {active || adding ? (
          <div className="space-y-1.5">
            {sessions.map((s) => (
              <Session
                key={s.id}
                starts={s.starts_at}
                ends={s.ends_at}
                canEdit={canEdit}
                onCommit={(starts, ends) => onSave(starts, ends, s)}
              />
            ))}
            {adding ? (
              <Session
                starts={sessions.at(-1)?.ends_at ?? DEFAULT_START}
                ends={DEFAULT_END}
                canEdit={canEdit}
                autoFocus
                onCommit={async (starts, ends) => {
                  await onSave(starts, ends);
                  setAdding(false);
                }}
                onCancel={() => setAdding(false)}
              />
            ) : null}
          </div>
        ) : (
          <span className="text-ink-muted">–</span>
        )}

        {canEdit && active && !adding ? (
          <button
            type="button"
            onClick={() => setAdding(true)}
            className="mt-1.5 inline-flex items-center gap-1 rounded-control text-2xs font-medium text-brand-700 transition-colors hover:text-brand-800"
          >
            <Plus className="size-3" />
            Tambah sesi
          </button>
        ) : null}
      </td>

      <td className="px-4 py-2.5 text-right">
        {canEdit && active ? (
          <button
            type="button"
            onClick={() => void onClear(sessions)}
            aria-label={`Jadikan ${label} libur`}
            title="Jadikan libur"
            className="rounded-control p-1.5 text-ink-muted transition-colors hover:bg-danger-soft hover:text-danger"
          >
            <Trash2 className="size-4" />
          </button>
        ) : null}
      </td>
    </tr>
  );
}

/**
 * One stretch of hours.
 *
 * Committed when the field is left rather than on every keystroke: a time box
 * passes through "0", "08", "08:0" on the way to a value, and saving those
 * would write three shifts nobody asked for.
 */
function Session({
  starts,
  ends,
  canEdit,
  autoFocus,
  onCommit,
  onCancel,
}: {
  starts: string;
  ends: string;
  canEdit: boolean;
  autoFocus?: boolean;
  onCommit: (starts: string, ends: string) => Promise<void>;
  onCancel?: () => void;
}) {
  const [from, setFrom] = useState(starts);
  const [to, setTo] = useState(ends);
  const valid = to > from;
  const dirty = from !== starts || to !== ends;

  function commit() {
    if (!valid || (!dirty && !onCancel)) return;
    void onCommit(from, to);
  }

  return (
    <span className="flex flex-wrap items-center gap-2">
      <input
        type="time"
        value={from}
        disabled={!canEdit}
        autoFocus={autoFocus}
        onChange={(e) => setFrom(e.target.value)}
        onBlur={commit}
        aria-label="Jam mulai"
        className={clsx(inputClassSm, 'w-[108px]', !valid && 'border-danger')}
      />
      <span className="text-ink-muted">→</span>
      <input
        type="time"
        value={to}
        disabled={!canEdit}
        onChange={(e) => setTo(e.target.value)}
        onBlur={commit}
        aria-label="Jam selesai"
        className={clsx(inputClassSm, 'w-[108px]', !valid && 'border-danger')}
      />
      {onCancel ? (
        <button
          type="button"
          onClick={onCancel}
          className="text-2xs text-ink-muted hover:text-ink-soft"
        >
          Batal
        </button>
      ) : null}
      {!valid ? (
        <span className="text-2xs text-danger">Jam selesai harus setelah jam mulai</span>
      ) : null}
    </span>
  );
}

/** A switch, so a column of days reads as on or off from its shape alone. */
function Toggle({
  checked,
  onChange,
  disabled,
  label,
}: {
  checked: boolean;
  onChange: (next: boolean) => void;
  disabled?: boolean;
  label: string;
}) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      aria-label={label}
      disabled={disabled}
      onClick={() => onChange(!checked)}
      className={clsx(
        'relative h-5 w-9 shrink-0 rounded-full transition-colors',
        checked ? 'bg-brand-700' : 'bg-hairline-strong',
        disabled ? 'cursor-not-allowed opacity-50' : 'cursor-pointer',
      )}
    >
      <span
        aria-hidden
        className={clsx(
          'absolute top-0.5 size-4 rounded-full bg-white shadow-e1 transition-[left]',
          checked ? 'left-[18px]' : 'left-0.5',
        )}
      />
    </button>
  );
}

/** Choosing who gets this person's week. */
function CopyDialog({
  source,
  members,
  onClose,
  onCopy,
}: {
  source: OrgMember;
  members: OrgMember[];
  onClose: () => void;
  onCopy: (targets: string[]) => Promise<void>;
}) {
  const [picked, setPicked] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const others = members.filter((m) => m.user_id !== source.user_id);

  return (
    <Modal
      open
      onClose={onClose}
      size="md"
      title={`Salin jam kerja ${source.full_name ?? source.email}`}
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            Batal
          </Button>
          <Button
            variant="primary"
            loading={busy}
            disabled={picked.length === 0}
            onClick={async () => {
              setBusy(true);
              try {
                await onCopy(picked);
              } finally {
                setBusy(false);
              }
            }}
          >
            Salin ke {picked.length} anggota
          </Button>
        </>
      }
    >
      <p className="mb-3 text-xs leading-relaxed text-ink-muted">
        Pola pekan orang yang dipilih akan <span className="font-medium text-ink">ditimpa</span>,
        bukan digabung — termasuk hari liburnya.
      </p>

      {others.length === 0 ? (
        <p className="text-sm text-ink-muted">Belum ada anggota lain.</p>
      ) : (
        <ul className="space-y-1">
          {others.map((m) => {
            const on = picked.includes(m.user_id);
            return (
              <li key={m.user_id}>
                <label
                  className={clsx(
                    'flex cursor-pointer items-center gap-2.5 rounded-control border px-3 py-2 transition-colors',
                    on ? 'border-brand-600/40 bg-brand-600/[0.07]' : 'border-hairline hover:bg-surface-sunken/60',
                  )}
                >
                  <input
                    type="checkbox"
                    checked={on}
                    onChange={() =>
                      setPicked((prev) =>
                        prev.includes(m.user_id)
                          ? prev.filter((x) => x !== m.user_id)
                          : [...prev, m.user_id],
                      )
                    }
                    className="size-3.5 accent-brand-700"
                  />
                  <span className="min-w-0">
                    <span className="block truncate text-sm text-ink">
                      {m.full_name ?? m.email}
                    </span>
                    <span className="block truncate text-2xs text-ink-muted">{m.email}</span>
                  </span>
                </label>
              </li>
            );
          })}
        </ul>
      )}
    </Modal>
  );
}
