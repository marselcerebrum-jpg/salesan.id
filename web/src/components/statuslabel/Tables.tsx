'use client';

import clsx from 'clsx';
import { Search } from 'lucide-react';
import { useMemo } from 'react';

import { AppMark } from '@/components/analytics/AppBadge';
import { EmptyState, Spinner } from '@/components/ui/Primitives';
import { CATEGORIES, LabelChip, categoryOf } from '@/components/statuslabel/parts';
import type {
  ContactLabelHistoryRow,
  LabelCategory,
  LabelCategoryApplication,
  LabelCategoryContact,
  LabelCategoryDay,
  LabelCategorySummary,
  LabelCategoryTransition,
} from '@/lib/api';

/* --- tahap 2: sebaran per aplikasi -------------------------------------------- */

export function ApplicationTable({
  loading,
  rows,
  total,
  onPick,
}: {
  loading: boolean;
  rows: LabelCategoryApplication[];
  total: number;
  onPick: (id: string) => void;
}) {
  return (
    <section className="rounded-card border border-hairline bg-surface-raised">
      {loading && rows.length === 0 ? (
        <div className="p-6">
          <Spinner />
        </div>
      ) : rows.length === 0 ? (
        <div className="p-6">
          <EmptyState
            title="Belum ada customer pada kategori ini"
            description="Label dengan kata Cold, Warm atau Hot belum terpasang pada kontak mana pun di lingkup ini."
          />
        </div>
      ) : (
        <table className="w-full text-sm">
          <thead>
            <tr className="border-b border-hairline text-2xs text-ink-muted uppercase">
              <th className="w-12 px-4 py-2.5 text-left font-semibold">No.</th>
              <th className="px-4 py-2.5 text-left font-semibold">Aplikasi</th>
              <th className="px-4 py-2.5 text-right font-semibold">Jumlah Customer</th>
              <th className="px-4 py-2.5 text-right font-semibold">Persentase</th>
              <th className="w-32 px-4 py-2.5 text-right font-semibold">Aksi</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((a, i) => (
              <tr
                key={a.application_id ?? a.code}
                className="border-b border-hairline last:border-0 hover:bg-surface-sunken/40"
              >
                <td className="nums px-4 py-2.5 text-ink-muted">{i + 1}</td>
                <td className="px-4 py-2.5">
                  <span className="flex items-center gap-2.5">
                    <AppMark code={a.code} color={a.color} size={24} />
                    <span className="font-medium text-ink">{a.name}</span>
                  </span>
                </td>
                <td className="nums px-4 py-2.5 text-right font-medium text-ink">
                  {a.contacts.toLocaleString('id-ID')}
                </td>
                <td className="nums px-4 py-2.5 text-right text-ink-soft">
                  {total > 0 ? `${Math.round((a.contacts / total) * 100)}%` : '–'}
                </td>
                <td className="px-4 py-2.5 text-right">
                  {a.application_id ? (
                    <button
                      type="button"
                      onClick={() => onPick(a.application_id!)}
                      className="rounded-control bg-brand-700 px-3 py-1.5 text-xs font-medium text-white transition-colors hover:bg-brand-800"
                    >
                      Lihat Detail
                    </button>
                  ) : null}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </section>
  );
}

/* --- tahap 3: daftar nomor ---------------------------------------------------- */

export function ContactTable({
  loading,
  rows,
  total,
  page,
  pageSize,
  search,
  onSearch,
  onPage,
  onOpen,
}: {
  loading: boolean;
  rows: LabelCategoryContact[];
  total: number;
  page: number;
  pageSize: number;
  search: string;
  onSearch: (v: string) => void;
  onPage: (p: number) => void;
  onOpen: (id: string) => void;
}) {
  const pages = Math.max(1, Math.ceil(total / pageSize));
  // A window around the current page: a thousand customers is forty pages, and
  // forty buttons is not navigation.
  const shown = useMemo(() => {
    const from = Math.max(0, Math.min(page - 2, pages - 5));
    return Array.from({ length: Math.min(5, pages) }, (_, i) => from + i);
  }, [page, pages]);

  return (
    <section className="rounded-card border border-hairline bg-surface-raised">
      <div className="border-b border-hairline px-4 py-3">
        <label className="relative block">
          <Search className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-ink-muted" />
          <input
            value={search}
            onChange={(e) => onSearch(e.target.value)}
            placeholder="Cari nomor / nama…"
            aria-label="Cari customer"
            className="h-9 w-full rounded-control bg-surface-sunken/60 pr-3 pl-9 text-sm text-ink outline-none placeholder:text-ink-muted focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-brand-600"
          />
        </label>
      </div>

      {loading && rows.length === 0 ? (
        <div className="p-6">
          <Spinner />
        </div>
      ) : rows.length === 0 ? (
        <div className="p-6">
          <EmptyState
            title="Tidak ada customer"
            description={
              search.trim()
                ? `Tidak ada yang cocok dengan "${search.trim()}".`
                : 'Belum ada customer pada kategori ini di aplikasi tersebut.'
            }
          />
        </div>
      ) : (
        <>
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-hairline text-2xs text-ink-muted uppercase">
                <th className="w-12 px-4 py-2.5 text-left font-semibold">No.</th>
                <th className="px-4 py-2.5 text-left font-semibold">Nomor WhatsApp</th>
                <th className="px-4 py-2.5 text-left font-semibold">Nama Kontak</th>
                <th className="px-4 py-2.5 text-left font-semibold">Label Terakhir</th>
                <th className="px-4 py-2.5 text-right font-semibold">Total Perubahan</th>
                <th className="w-32 px-4 py-2.5 text-right font-semibold">Aksi</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((c, i) => (
                <tr
                  key={c.contact_id}
                  className="border-b border-hairline last:border-0 hover:bg-surface-sunken/40"
                >
                  <td className="nums px-4 py-2.5 text-ink-muted">{page * pageSize + i + 1}</td>
                  <td className="nums px-4 py-2.5 text-ink-soft">
                    {c.phone ? `+${c.phone}` : <span className="text-ink-muted">–</span>}
                  </td>
                  <td className="px-4 py-2.5 font-medium text-ink">
                    {c.name || <span className="text-ink-muted italic">Belum tersimpan</span>}
                  </td>
                  <td className="px-4 py-2.5">
                    {/* The real WhatsApp label, not the category it falls into:
                        the operator is looking for the words on their own
                        screen, and "Hot" alone would hide that this one came
                        from "FU3 HOT". */}
                    <LabelChip name={c.label_name} category={c.category} />
                  </td>
                  <td className="nums px-4 py-2.5 text-right text-ink-soft">{c.change_count}</td>
                  <td className="px-4 py-2.5 text-right">
                    <button
                      type="button"
                      onClick={() => onOpen(c.contact_id)}
                      className="rounded-control bg-brand-700 px-3 py-1.5 text-xs font-medium text-white transition-colors hover:bg-brand-800"
                    >
                      Lihat Riwayat
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>

          <div className="flex flex-wrap items-center justify-between gap-3 border-t border-hairline px-4 py-3 text-xs text-ink-muted">
            <span>
              Menampilkan {page * pageSize + 1}–{Math.min((page + 1) * pageSize, total)} dari{' '}
              {total.toLocaleString('id-ID')} data
            </span>
            {pages > 1 ? (
              <span className="flex items-center gap-1">
                <PageStep disabled={page === 0} onClick={() => onPage(page - 1)}>
                  ‹
                </PageStep>
                {shown.map((p) => (
                  <button
                    key={p}
                    type="button"
                    onClick={() => onPage(p)}
                    aria-current={p === page ? 'page' : undefined}
                    className={clsx(
                      'size-7 rounded-control text-xs font-medium transition-colors',
                      p === page
                        ? 'bg-brand-700 text-white'
                        : 'border border-hairline text-ink-soft hover:bg-surface-sunken',
                    )}
                  >
                    {p + 1}
                  </button>
                ))}
                <PageStep disabled={page >= pages - 1} onClick={() => onPage(page + 1)}>
                  ›
                </PageStep>
              </span>
            ) : null}
          </div>
        </>
      )}
    </section>
  );
}

function PageStep({
  children,
  disabled,
  onClick,
}: {
  children: React.ReactNode;
  disabled: boolean;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      disabled={disabled}
      onClick={onClick}
      className="size-7 rounded-control border border-hairline text-xs text-ink-soft transition-colors hover:bg-surface-sunken disabled:opacity-40"
    >
      {children}
    </button>
  );
}

/* --- tahap 4: riwayat satu customer ------------------------------------------- */

export function CustomerJourney({
  loading,
  history,
}: {
  loading: boolean;
  history: ContactLabelHistoryRow[];
}) {
  // Newest first in the table, because that is what somebody opening it wants
  // to know; the journey strip below reads forwards, because that is what a
  // journey is.
  const newestFirst = useMemo(() => [...history].reverse(), [history]);
  const journey = useMemo(
    () => history.filter((h) => h.event_type === 'label_assigned' && h.category !== ''),
    [history],
  );

  if (loading) {
    return (
      <section className="rounded-card border border-hairline bg-surface-raised p-6">
        <Spinner />
      </section>
    );
  }

  if (history.length === 0) {
    return (
      <section className="rounded-card border border-hairline bg-surface-raised p-6">
        <EmptyState
          title="Belum ada riwayat"
          description="Riwayat label mulai dicatat 22 September 2026. Perubahan sebelum itu tidak pernah tersimpan, dan tidak akan dikarang."
        />
      </section>
    );
  }

  return (
    <>
      <section className="rounded-card border border-hairline bg-surface-raised">
        <h2 className="border-b border-hairline px-4 py-3 text-sm font-medium text-ink">
          Riwayat Perubahan Label
        </h2>
        <table className="w-full text-sm">
          <thead>
            <tr className="border-b border-hairline text-2xs text-ink-muted uppercase">
              <th className="px-4 py-2.5 text-left font-semibold">Tanggal &amp; Waktu</th>
              <th className="px-4 py-2.5 text-left font-semibold">Label</th>
              <th className="px-4 py-2.5 text-left font-semibold">Diubah Oleh</th>
              <th className="px-4 py-2.5 text-left font-semibold">Sumber</th>
            </tr>
          </thead>
          <tbody>
            {newestFirst.map((h, i) => (
              <tr key={`${h.at}-${i}`} className="border-b border-hairline last:border-0">
                <td className="px-4 py-2.5 text-ink-soft">
                  {/* A rail down the left, so a journey reads as one line of
                      travel rather than as a stack of unrelated rows. */}
                  <span className="flex items-center gap-2.5">
                    <span
                      aria-hidden
                      className={clsx(
                        'size-2 shrink-0 rounded-full',
                        h.category ? RAIL[h.category] : 'bg-ink-muted/40',
                      )}
                    />
                    {new Date(h.at).toLocaleString('id-ID', {
                      dateStyle: 'medium',
                      timeStyle: 'short',
                      timeZone: 'Asia/Jakarta',
                    })}
                  </span>
                </td>
                <td className="px-4 py-2.5">
                  <LabelChip
                    name={h.label_name}
                    category={h.category}
                    prefix={h.event_type === 'label_removed' ? '− ' : '+ '}
                  />
                </td>
                <td className="px-4 py-2.5 text-ink-soft">
                  {/* Blank for anything done on the phone. WhatsApp does not say
                      who was holding it, and a guess in an audit trail is worse
                      than an admission. */}
                  {h.changed_by || <span className="text-ink-muted">Tidak diketahui</span>}
                </td>
                <td className="px-4 py-2.5 text-ink-muted">
                  {h.source === 'whatsapp' ? 'WhatsApp' : h.source === 'web' ? 'Web' : h.source}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </section>

      {journey.length > 1 ? (
        <section className="mt-4 rounded-card border border-hairline bg-surface-raised p-4">
          <h2 className="text-sm font-medium text-ink">Perjalanan Label</h2>
          <div className="mt-3 flex flex-wrap items-center gap-2">
            {journey.map((h, i) => (
              <span key={`${h.at}-${i}`} className="flex items-center gap-2">
                <LabelChip name={h.label_name} category={h.category} />
                {i < journey.length - 1 ? (
                  <span className="text-ink-muted" aria-hidden>
                    →
                  </span>
                ) : null}
              </span>
            ))}
          </div>
        </section>
      ) : null}
    </>
  );
}

/* --- rekap perpindahan --------------------------------------------------------- */

/** Every direction a customer can move, in journey order. */
/** The dot on the rail, one colour per category. */
const RAIL: Record<LabelCategory, string> = {
  cold: 'bg-info',
  warm: 'bg-warn',
  hot: 'bg-danger',
};

const MOVES: { from: LabelCategory; to: LabelCategory }[] = [];
for (const a of CATEGORIES) {
  for (const b of CATEGORIES) {
    if (a.id !== b.id) MOVES.push({ from: a.id, to: b.id });
  }
}

export function TransitionRecap({ rows }: { rows: LabelCategoryTransition[] }) {
  // Only the directions that actually happened get a column. All six would make
  // a table three-quarters empty, and the mockup's three would quietly drop the
  // other half of the movement.
  const used = useMemo(
    () => MOVES.filter((m) => rows.some((r) => r.from === m.from && r.to === m.to)),
    [rows],
  );

  const days = useMemo(() => {
    const byDay = new Map<string, Map<string, number>>();
    for (const r of rows) {
      const key = `${r.from}>${r.to}`;
      if (!byDay.has(r.day)) byDay.set(r.day, new Map());
      byDay.get(r.day)!.set(key, (byDay.get(r.day)!.get(key) ?? 0) + r.count);
    }
    return [...byDay.entries()].sort((a, b) => b[0].localeCompare(a[0]));
  }, [rows]);

  const totals = useMemo(() => {
    const t = new Map<string, number>();
    for (const r of rows) {
      const key = `${r.from}>${r.to}`;
      t.set(key, (t.get(key) ?? 0) + r.count);
    }
    return t;
  }, [rows]);

  return (
    <section className="rounded-card border border-hairline bg-surface-raised">
      <div className="border-b border-hairline px-4 py-3">
        <h2 className="text-sm font-medium text-ink">Rekap Perpindahan Label</h2>
        <p className="mt-0.5 text-xs text-ink-muted">
          Jumlah perpindahan yang terjadi setiap hari, mengikuti periode yang dipilih.
        </p>
      </div>

      {days.length === 0 ? (
        <div className="p-6">
          <EmptyState
            title="Belum ada perpindahan pada periode ini"
            description="Riwayat label mulai dicatat 22 September 2026. Perpindahan sebelum tanggal itu memang tidak pernah tersimpan."
          />
        </div>
      ) : (
        <table className="w-full text-sm">
          <thead>
            <tr className="border-b border-hairline text-2xs text-ink-muted uppercase">
              <th className="px-4 py-2.5 text-left font-semibold">Tanggal</th>
              {used.map((m) => (
                <th key={`${m.from}>${m.to}`} className="px-4 py-2.5 text-right font-semibold">
                  <span className={categoryOf(m.from).tone}>{categoryOf(m.from).label}</span>
                  {' → '}
                  <span className={categoryOf(m.to).tone}>{categoryOf(m.to).label}</span>
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {days.map(([day, counts]) => (
              <tr key={day} className="border-b border-hairline">
                <td className="px-4 py-2 text-ink-soft">{day}</td>
                {used.map((m) => {
                  const n = counts.get(`${m.from}>${m.to}`);
                  return (
                    <td key={`${m.from}>${m.to}`} className="nums px-4 py-2 text-right text-ink">
                      {n ?? <span className="text-ink-muted">0</span>}
                    </td>
                  );
                })}
              </tr>
            ))}
            <tr className="bg-surface-sunken/50">
              <td className="px-4 py-2 text-sm font-medium text-ink">Total</td>
              {used.map((m) => (
                <td
                  key={`${m.from}>${m.to}`}
                  className="nums px-4 py-2 text-right font-semibold text-ink"
                >
                  {totals.get(`${m.from}>${m.to}`) ?? 0}
                </td>
              ))}
            </tr>
          </tbody>
        </table>
      )}
    </section>
  );
}

/* --- rekap harian -------------------------------------------------------------- */

/**
 * How many customers moved into each category, day by day.
 *
 * A flow, and the heading says so. The three numbers at the top of the page are
 * a stock — how many customers stand in each category now — and putting the two
 * in one table without a word would invite them to be read as the same thing on
 * different days.
 */
export function DailyRecap({ rows }: { rows: LabelCategoryDay[] }) {
  const totals = useMemo(
    () =>
      rows.reduce(
        (a, r) => ({ cold: a.cold + r.cold, warm: a.warm + r.warm, hot: a.hot + r.hot }),
        { cold: 0, warm: 0, hot: 0 },
      ),
    [rows],
  );

  if (rows.length === 0) {
    return (
      <div className="p-6">
        <EmptyState
          title="Belum ada perubahan label pada periode ini"
          description="Riwayat label mulai dicatat 22 September 2026. Sebelum tanggal itu memang tidak pernah tersimpan."
        />
      </div>
    );
  }

  return (
    <table className="w-full text-sm">
      <thead>
        <tr className="border-b border-hairline text-2xs text-ink-muted uppercase">
          <th className="px-4 py-2.5 text-left font-semibold">Tanggal</th>
          {CATEGORIES.map((c) => (
            <th key={c.id} className={clsx('px-4 py-2.5 text-right font-semibold', c.tone)}>
              {c.label}
            </th>
          ))}
          <th className="px-4 py-2.5 text-right font-semibold">Total</th>
        </tr>
      </thead>
      <tbody>
        {[...rows].reverse().map((r) => (
          <tr key={r.day} className="border-b border-hairline">
            <td className="px-4 py-2 text-ink-soft">{r.day}</td>
            {CATEGORIES.map((c) => (
              <td key={c.id} className="nums px-4 py-2 text-right text-ink">
                {r[c.id] || <span className="text-ink-muted">0</span>}
              </td>
            ))}
            <td className="nums px-4 py-2 text-right text-ink-soft">
              {r.cold + r.warm + r.hot}
            </td>
          </tr>
        ))}
        <tr className="bg-surface-sunken/50">
          <td className="px-4 py-2 text-sm font-medium text-ink">Total</td>
          {CATEGORIES.map((c) => (
            <td key={c.id} className="nums px-4 py-2 text-right font-semibold text-ink">
              {totals[c.id]}
            </td>
          ))}
          <td className="nums px-4 py-2 text-right font-semibold text-ink">
            {totals.cold + totals.warm + totals.hot}
          </td>
        </tr>
      </tbody>
    </table>
  );
}

/* --- status saat ini ----------------------------------------------------------- */

/** The three totals as they stand, each a way into its own drill-down. */
export function CurrentState({
  summary,
  active,
  onPick,
}: {
  summary: LabelCategorySummary | undefined;
  active: LabelCategory;
  onPick: (c: LabelCategory) => void;
}) {
  return (
    <div className="grid gap-3 p-4 sm:grid-cols-3">
      {CATEGORIES.map((c) => (
        <button
          key={c.id}
          type="button"
          onClick={() => onPick(c.id)}
          aria-pressed={active === c.id}
          className={clsx(
            'flex items-center gap-3 rounded-card border p-4 text-left transition-colors',
            c.banner,
            active === c.id ? 'ring-2 ring-brand-700 ring-offset-2 ring-offset-surface-raised' : '',
          )}
        >
          <span
            className={clsx(
              'grid size-10 shrink-0 place-items-center rounded-control bg-surface-raised',
              c.tone,
            )}
          >
            <c.icon className="size-5" aria-hidden />
          </span>
          <span className="min-w-0">
            <span className={clsx('block text-sm font-medium', c.tone)}>{c.label}</span>
            <span className="nums block text-2xl font-semibold text-ink">
              {summary ? summary[c.id].toLocaleString('id-ID') : '–'}
            </span>
            <span className="block text-xs text-ink-muted">Customer</span>
          </span>
        </button>
      ))}
    </div>
  );
}
