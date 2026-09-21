'use client';

import clsx from 'clsx';
import { ChevronDown, Trophy } from 'lucide-react';
import { useMemo, useState } from 'react';
import useSWR from 'swr';

import {
  EmptyState,
  ErrorState,
  RowSkeleton,
  formatDuration,
} from '@/components/analytics/Primitives';
import { fetcher, teamPerformancePath, type AnalyticsQuery } from '@/lib/api';
import type { MemberMetrics, MemberPerformance, TeamReport } from '@/lib/types';

/**
 * The team, ranked, with the people behind each row one press away.
 *
 * A PIC's headline is the recap of their whole patch: their own work plus every
 * Freelance under them, across every application and number they hold. Opening
 * the row takes that recap apart — the PIC's own hands first, then each
 * Freelance — which is the only way to tell a PIC carrying the inbox alone from
 * one whose team is carrying it.
 *
 * Read from `/performance/team`, which computes both halves in one pass. The
 * per-member endpoint beside it runs one aggregate per person and cannot report
 * a PIC's personal share at all, so it could not answer this question at any
 * price.
 *
 * Three figures, not forty. This is the Dashboard's summary of the team;
 * Performa is where somebody goes when these three raise a question.
 */

/** The three figures, read off the same fields at both levels. */
const FIGURES: { key: string; label: string; info: string; of: (m: MemberMetrics) => string }[] = [
  {
    key: 'served',
    label: 'Kontak Ditangani',
    info: 'Kontak unik yang mendapat minimal satu balasan manual. Dihitung per kontak, bukan per pesan.',
    of: (m) => m.contacts_served.toLocaleString('id-ID'),
  },
  {
    key: 'outbound',
    label: 'Chat Keluar',
    info: 'Bubble keluar yang dikirim dari web. Pesan yang diketik di HP tidak punya pelaku yang bisa dipastikan, jadi tidak dihitung pada siapa pun.',
    of: (m) => m.outbound_manual.toLocaleString('id-ID'),
  },
  {
    key: 'response',
    label: 'Rata-rata Respons',
    info: 'Jarak dari pesan pertama pelanggan sampai balasan manual pertama, untuk siklus yang orang ini balas.',
    of: (m) =>
      m.avg_first_response_seconds === null ? '—' : formatDuration(m.avg_first_response_seconds),
  },
];

/** What a PIC's headline is: the team when they lead one, their own work if not. */
function recap(m: MemberPerformance): MemberMetrics {
  return m.team ?? m.personal;
}

export function TeamRank({ query }: { query: AnalyticsQuery }) {
  const { data, error, isLoading, mutate } = useSWR<TeamReport>(
    teamPerformancePath(query),
    fetcher,
    { refreshInterval: 300_000, keepPreviousData: true },
  );

  const [open, setOpen] = useState<string | null>(null);
  const members = useMemo(() => data?.members ?? [], [data]);

  /*
   * Who is reading, straight from the response.
   *
   * The server already decided what this account may see — a Leader gets every
   * PIC, a PIC gets themselves and their Freelance, a Freelance gets only
   * themselves — so this is never used to hide anything. It is used to name
   * what is on screen: a card headed "Performa Tim", ranked #1 of one, is a
   * strange way to show somebody their own work.
   */
  const role = data?.role ?? '';
  const self = useMemo(
    () => members.find((m) => m.role === 'freelance') ?? members[0],
    [members],
  );

  /*
   * Ranked by contacts served.
   *
   * One order, not a control: the ranking is the card's whole claim, and three
   * buttons that silently redefine what "rank 1" means is a claim that changes
   * under the reader. Chat keluar rewards whoever types most, and a response
   * average rewards whoever answered one message quickly; contacts served is
   * the closest of the three to "how much of the work did this team take".
   */
  const pics = useMemo(
    () =>
      members
        .filter((m) => m.role === 'pic' && m.user_id)
        .sort(
          (a, b) =>
            recap(b).contacts_served - recap(a).contacts_served || a.name.localeCompare(b.name),
        ),
    [members],
  );

  const freelancersOf = useMemo(() => {
    const by = new Map<string, MemberPerformance[]>();
    for (const m of members) {
      if (m.role !== 'freelance' || !m.pic_user_id) continue;
      const list = by.get(m.pic_user_id) ?? [];
      list.push(m);
      by.set(m.pic_user_id, list);
    }
    for (const list of by.values()) {
      list.sort(
        (a, b) =>
          b.personal.contacts_served - a.personal.contacts_served || a.name.localeCompare(b.name),
      );
    }
    return by;
  }, [members]);

  /*
   * Freelance who report to nobody.
   *
   * Listed rather than dropped: their work is real and it is inside none of the
   * PIC rows above, so leaving them out would make the card quietly smaller
   * than the operation it claims to recap.
   */
  const orphans = useMemo(
    () => members.filter((m) => m.role === 'freelance' && !m.pic_user_id),
    [members],
  );

  return (
    <section className="rounded-card border border-hairline bg-surface-raised px-4 py-4 shadow-e1">
      <div className="flex min-w-0 items-center gap-2.5">
        <span className="flex size-8 shrink-0 items-center justify-center rounded-control bg-warn-soft text-amber-badge">
          <Trophy className="size-4" aria-hidden />
        </span>
        <div className="min-w-0">
          <h2 className="truncate text-sm font-semibold text-ink">
            {role === 'freelance' ? 'Performa Saya' : role === 'pic' ? 'Tim Saya' : 'Performa Tim'}
          </h2>
          <p className="text-2xs text-ink-muted">
            {role === 'freelance'
              ? 'Pekerjaan Anda sendiri pada periode ini.'
              : role === 'pic'
                ? 'Gabungan Anda dan Freelance di bawah Anda. Tekan untuk memecahnya menjadi hasil Anda sendiri dan hasil tiap Freelance.'
                : 'Urutan PIC menurut kontak ditangani. Tekan satu baris untuk memecahnya menjadi hasil PIC dan hasil tiap Freelance.'}
          </p>
        </div>
      </div>

      {error ? (
        <div className="mt-3">
          <ErrorState
            message={error instanceof Error ? error.message : 'Gagal memuat performa tim.'}
            onRetry={() => void mutate()}
          />
        </div>
      ) : isLoading && members.length === 0 ? (
        <div className="mt-3">
          <RowSkeleton count={3} />
        </div>
      ) : role === 'freelance' ? (
        /*
         * One person, no ranking.
         *
         * A Freelance sees only themselves — the server pins every figure on
         * this screen to their account — so there is nothing to rank and
         * nothing to expand. Drawn as the same line the PIC card uses inside,
         * because it is the same measurement of the same kind of person.
         */
        self ? (
          <>
            <ul className="mt-3">
              <PersonLine name={self.name} role="Freelance" metrics={self.personal} />
            </ul>
            <p className="mt-3 border-t border-hairline pt-2 text-2xs leading-relaxed text-ink-muted">
              Seluruh angka di halaman ini adalah pekerjaan akun Anda sendiri. Pesan yang diketik
              langsung di HP tidak punya pelaku yang bisa dipastikan, jadi tidak dihitung pada siapa
              pun.
            </p>
          </>
        ) : (
          <div className="mt-3">
            <EmptyState title="Belum ada aktivitas Anda pada periode ini." />
          </div>
        )
      ) : pics.length === 0 && orphans.length === 0 ? (
        <div className="mt-3">
          <EmptyState title="Belum ada PIC atau Freelance pada jangkauan Anda." />
        </div>
      ) : (
        <>
          <ul className="mt-3 space-y-2">
            {pics.map((pic, i) => (
              <PicCard
                key={pic.user_id}
                // A number only where it means something. One PIC looking at
                // their own team is not "#1 of 1"; they are simply the team.
                rank={pics.length > 1 ? i + 1 : null}
                pic={pic}
                team={freelancersOf.get(pic.user_id!) ?? []}
                expanded={open === pic.user_id}
                onToggle={() => setOpen(open === pic.user_id ? null : pic.user_id)}
              />
            ))}
          </ul>

          {orphans.length > 0 ? (
            <div className="mt-4">
              <p className="text-2xs font-semibold text-ink-muted">Freelance tanpa PIC</p>
              <ul className="mt-1.5 space-y-px">
                {orphans.map((f) => (
                  <PersonLine key={f.user_id ?? f.name} name={f.name} role="Freelance" metrics={f.personal} />
                ))}
              </ul>
            </div>
          ) : null}

          <p className="mt-3 border-t border-hairline pt-2 text-2xs leading-relaxed text-ink-muted">
            Angka besar pada baris PIC adalah gabungan: pekerjaan PIC sendiri ditambah seluruh
            Freelance di bawahnya, di semua aplikasi dan nomor yang dipegangnya. Kontak dihitung unik
            di dalam gabungan itu, jadi satu pelanggan yang dilayani dua orang tetap satu. Pesan yang
            diketik langsung di HP tidak punya pelaku yang bisa dipastikan, jadi tidak dihitung pada
            siapa pun di sini.
          </p>
        </>
      )}
    </section>
  );
}

/** One PIC: the recap, and the people it is made of. */
function PicCard({
  rank,
  pic,
  team,
  expanded,
  onToggle,
}: {
  rank: number | null;
  pic: MemberPerformance;
  team: MemberPerformance[];
  expanded: boolean;
  onToggle: () => void;
}) {
  return (
    <li
      className={clsx(
        'overflow-hidden rounded-card border transition-colors',
        expanded ? 'border-brand-700 bg-surface-raised' : 'border-hairline bg-surface-raised',
      )}
    >
      <button
        type="button"
        onClick={onToggle}
        aria-expanded={expanded}
        className="flex w-full flex-wrap items-center gap-x-4 gap-y-3 px-4 py-3 text-left transition-colors hover:bg-surface-sunken/50"
      >
        {rank === null ? null : (
          <span
            className={clsx(
              'nums flex size-7 shrink-0 items-center justify-center rounded-full text-xs font-semibold',
              rank === 1 ? 'bg-warn-soft text-amber-badge' : 'bg-surface-sunken text-ink-soft',
            )}
          >
            {rank}
          </span>
        )}

        <span className="min-w-0 flex-1">
          <span className="block truncate text-sm font-semibold text-ink">{pic.name}</span>
          <span className="block truncate text-2xs text-ink-muted">
            PIC · {team.length > 0 ? `${team.length} Freelance` : 'tanpa Freelance'}
            {pic.on_duty ? ' · sedang bertugas' : ''}
          </span>
        </span>

        {/* The three figures as blocks rather than table cells: the same shape
            the recap cards above use, so the page reads as one screen. */}
        <span className="flex shrink-0 flex-wrap gap-x-6 gap-y-2">
          {FIGURES.map((f) => (
            <Figure key={f.key} label={f.label} info={f.info} value={f.of(recap(pic))} />
          ))}
        </span>

        <ChevronDown
          aria-hidden
          className={clsx(
            'size-4 shrink-0 text-ink-muted transition-transform',
            expanded && 'rotate-180',
          )}
        />
      </button>

      {expanded ? (
        <div className="border-t border-hairline bg-surface-sunken/30 px-4 py-3">
          {/* The PIC's own hands first: the difference between this line and
              the headline above it is exactly what the team contributed. */}
          <p className="text-2xs font-semibold text-ink-muted">Hasil PIC</p>
          <ul className="mt-1.5 space-y-px">
            <PersonLine name={pic.name} role="PIC, pekerjaan sendiri" metrics={pic.personal} />
          </ul>

          <p className="mt-3 text-2xs font-semibold text-ink-muted">Hasil Freelance</p>
          {team.length === 0 ? (
            <p className="mt-1 text-xs text-ink-muted">Belum ada Freelance di bawah PIC ini.</p>
          ) : (
            <ul className="mt-1.5 space-y-px">
              {team.map((f) => (
                <PersonLine
                  key={f.user_id ?? f.name}
                  name={f.name}
                  role={f.on_duty ? 'Freelance · sedang bertugas' : 'Freelance'}
                  metrics={f.personal}
                />
              ))}
            </ul>
          )}
        </div>
      ) : null}
    </li>
  );
}

/** One person's own work, at the size of a supporting figure. */
function PersonLine({
  name,
  role,
  metrics,
}: {
  name: string;
  role: string;
  metrics: MemberMetrics;
}) {
  return (
    <li className="flex flex-wrap items-center gap-x-4 gap-y-2 rounded-control bg-surface-raised px-3 py-2">
      <span className="min-w-0 flex-1">
        <span className="block truncate text-xs font-medium text-ink">{name}</span>
        <span className="block truncate text-2xs text-ink-muted">{role}</span>
      </span>
      <span className="flex shrink-0 flex-wrap gap-x-6 gap-y-2">
        {FIGURES.map((f) => (
          <Figure key={f.key} label={f.label} info={f.info} value={f.of(metrics)} small />
        ))}
      </span>
    </li>
  );
}

/**
 * One figure over its name.
 *
 * Fixed width so the three columns line up down the card whatever they hold:
 * without it a row whose response average is "—" pulls its neighbours left and
 * the ranking stops being scannable.
 */
function Figure({
  label,
  info,
  value,
  small,
}: {
  label: string;
  info: string;
  value: string;
  small?: boolean;
}) {
  const empty = value === '0' || value === '—';
  return (
    <span className="block w-[104px]" title={`${label} — ${info}`}>
      <span
        className={clsx(
          'nums block leading-tight font-semibold whitespace-nowrap',
          small ? 'text-sm' : 'text-lg',
          empty ? 'text-ink-muted' : 'text-ink',
        )}
      >
        {value}
      </span>
      <span className="block truncate text-2xs text-ink-muted">{label}</span>
    </span>
  );
}
