'use client';

import clsx from 'clsx';
import {
  ArrowLeft,
  ChevronRight,
  Download,
  FileSpreadsheet,
  Flame,
  Search,
  Snowflake,
  Sun,
} from 'lucide-react';
import Link from 'next/link';
import { useEffect, useMemo, useState } from 'react';
import useSWR from 'swr';

import { EmptyState, ErrorNote, Spinner } from '@/components/ui/Primitives';
import {
  contactLabelHistoryPath,
  exportLabelCategory,
  fetcher,
  labelCategoryContactsPath,
  labelCategoryPath,
  type ContactLabelHistoryRow,
  type LabelCategory,
  type LabelCategoryContact,
  type LabelCategoryResponse,
} from '@/lib/api';

const CATEGORIES: { id: LabelCategory; label: string; icon: typeof Flame }[] = [
  { id: 'cold', label: 'Cold', icon: Snowflake },
  { id: 'warm', label: 'Warm', icon: Sun },
  { id: 'hot', label: 'Hot', icon: Flame },
];

/** One place for each category's colour, so the tiles and the badges agree. */
const TONE: Record<LabelCategory, string> = {
  cold: 'text-info',
  warm: 'text-warn',
  hot: 'text-danger',
};

const PAGE = 25;

/**
 * Status Label: Cold, Warm and Hot, and the way down to one customer.
 *
 * Four depths on one route rather than four routes, because they are one
 * question asked with more and more precision — which category, which brand,
 * which customer, what happened to them — and the reader should be able to step
 * back up without losing where they were. Each depth is in the address bar, so
 * any of them can be sent to somebody else.
 */
export default function StatusLabelPage() {
  const [category, setCategory] = useState<LabelCategory>('hot');
  const [appId, setAppId] = useState<string | null>(null);
  const [contactId, setContactId] = useState<string | null>(null);
  const [search, setSearch] = useState('');
  const [page, setPage] = useState(0);
  const [busy, setBusy] = useState<string | null>(null);
  const [failure, setFailure] = useState<string | null>(null);

  // Read once, from whatever link brought the reader here.
  useEffect(() => {
    const q = new URLSearchParams(window.location.search);
    const c = q.get('kategori');
    if (c === 'cold' || c === 'warm' || c === 'hot') setCategory(c);
    setAppId(q.get('app'));
    setContactId(q.get('kontak'));
  }, []);

  // Mirrored back without navigating: replaceState leaves the page alone.
  useEffect(() => {
    const url = new URL(window.location.href);
    url.searchParams.set('kategori', category);
    if (appId) url.searchParams.set('app', appId);
    else url.searchParams.delete('app');
    if (contactId) url.searchParams.set('kontak', contactId);
    else url.searchParams.delete('kontak');
    window.history.replaceState(null, '', url);
  }, [category, appId, contactId]);

  const { data, isLoading, error } = useSWR<LabelCategoryResponse>(
    labelCategoryPath({ category }),
    fetcher,
    { revalidateOnFocus: false },
  );

  const summary = data?.summary;
  const apps = useMemo(() => data?.applications ?? [], [data]);
  const transitions = useMemo(() => data?.transitions ?? [], [data]);
  const app = apps.find((a) => a.application_id === appId) ?? null;

  async function download(scope: 'summary' | 'detail', format: 'csv' | 'xlsx') {
    setBusy(`${scope}-${format}`);
    setFailure(null);
    try {
      await exportLabelCategory({ scope, format, category, applicationId: appId });
    } catch (err) {
      setFailure(err instanceof Error ? err.message : 'Gagal mengunduh laporan.');
    } finally {
      setBusy(null);
    }
  }

  return (
    <div className="px-5 py-6 lg:px-8 lg:py-8">
      {/* Breadcrumb, not a back button: four depths need to say where they are,
          not only how to leave. */}
      <nav className="flex flex-wrap items-center gap-1.5 text-sm text-ink-muted">
        <Link href="/performa" className="transition-colors hover:text-ink-soft">
          Performa
        </Link>
        <ChevronRight className="size-3.5" aria-hidden />
        <button
          type="button"
          onClick={() => {
            setAppId(null);
            setContactId(null);
          }}
          className={clsx(
            'transition-colors hover:text-ink-soft',
            !appId && !contactId ? 'text-ink' : '',
          )}
        >
          Status Label
        </button>
        {app ? (
          <>
            <ChevronRight className="size-3.5" aria-hidden />
            <button
              type="button"
              onClick={() => setContactId(null)}
              className={clsx('transition-colors hover:text-ink-soft', !contactId ? 'text-ink' : '')}
            >
              {app.name}
            </button>
          </>
        ) : null}
        {contactId ? (
          <>
            <ChevronRight className="size-3.5" aria-hidden />
            <span className="text-ink">Riwayat customer</span>
          </>
        ) : null}
      </nav>

      <header className="mt-3 flex flex-wrap items-end justify-between gap-4">
        <div>
          <h1 className="text-2xl font-semibold tracking-[-0.02em] text-ink">Status Label</h1>
          <p className="mt-1 text-sm text-ink-muted">
            Kondisi customer saat ini, dikelompokkan dari label WhatsApp yang paling terakhir
            dipasang padanya.
          </p>
        </div>

        <div className="flex flex-wrap items-center gap-2">
          <ExportButton
            icon={Download}
            busy={busy === 'summary-csv'}
            onClick={() => void download('summary', 'csv')}
          >
            Rekap CSV
          </ExportButton>
          <ExportButton
            icon={FileSpreadsheet}
            busy={busy === 'detail-xlsx'}
            onClick={() => void download('detail', 'xlsx')}
          >
            Detail XLSX
          </ExportButton>
        </div>
      </header>

      {failure ? (
        <div className="mt-3">
          <ErrorNote message={failure} />
        </div>
      ) : null}

      {/* The three tiles stay visible at every depth: they are the thing being
          drilled into, and hiding them would make the lower levels read as
          screens of their own rather than as a view of the same number. */}
      <div className="mt-5 grid gap-3 sm:grid-cols-3">
        {CATEGORIES.map((c) => {
          const value = summary?.[c.id] ?? 0;
          const active = category === c.id;
          return (
            <button
              key={c.id}
              type="button"
              onClick={() => {
                setCategory(c.id);
                setAppId(null);
                setContactId(null);
                setPage(0);
              }}
              aria-pressed={active}
              className={clsx(
                'rounded-card border p-4 text-left transition-colors',
                active
                  ? 'border-brand-700 bg-brand-600/8'
                  : 'border-hairline bg-surface-raised hover:bg-surface-sunken/50',
              )}
            >
              <span className="flex items-center gap-2">
                <c.icon className={clsx('size-4', TONE[c.id])} aria-hidden />
                <span className="text-sm font-medium text-ink-soft">{c.label}</span>
              </span>
              <span className="nums mt-1.5 block text-3xl font-semibold tracking-[-0.02em] text-ink">
                {isLoading ? '–' : value.toLocaleString('id-ID')}
              </span>
              <span className="text-xs text-ink-muted">Customer</span>
            </button>
          );
        })}
      </div>

      {/*
        Said plainly because the period picker sits on the page above this one,
        and the habit of a filtered dashboard is to assume everything obeys it.
        These three are a count of how things stand, not of what happened in a
        month: a customer tagged Hot in June is still Hot today.
      */}
      <p className="mt-2 text-2xs text-ink-muted">
        Ketiga angka ini adalah kondisi sekarang, bukan kejadian dalam periode tertentu. Rekap
        perpindahan di bawah yang mengikuti periode.
      </p>

      {error ? (
        <div className="mt-5">
          <ErrorNote message="Status label tidak dapat dimuat." />
        </div>
      ) : contactId ? (
        <ContactJourney contactId={contactId} onBack={() => setContactId(null)} />
      ) : appId ? (
        <ContactList
          category={category}
          applicationId={appId}
          applicationName={app?.name ?? 'Aplikasi'}
          search={search}
          onSearch={(v) => {
            setSearch(v);
            setPage(0);
          }}
          page={page}
          onPage={setPage}
          onOpen={setContactId}
        />
      ) : (
        <ApplicationTable
          isLoading={isLoading}
          apps={apps}
          total={summary?.[category] ?? 0}
          transitions={transitions}
          onPick={setAppId}
        />
      )}
    </div>
  );
}

/* --- level 2: per aplikasi ---------------------------------------------------- */

function ApplicationTable({
  isLoading,
  apps,
  total,
  transitions,
  onPick,
}: {
  isLoading: boolean;
  apps: LabelCategoryResponse['applications'];
  total: number;
  transitions: NonNullable<LabelCategoryResponse['transitions']>;
  onPick: (id: string) => void;
}) {
  const rows = apps ?? [];

  // One row per day, one column per movement actually seen. The columns are
  // built from the data rather than fixed at Cold→Warm, Warm→Hot, Hot→Cold:
  // customers move in every direction, and a table that only admits three of
  // the six would quietly drop the rest.
  const days = useMemo(() => {
    const byDay = new Map<string, Map<string, number>>();
    for (const t of transitions) {
      const key = `${t.from}→${t.to}`;
      if (!byDay.has(t.day)) byDay.set(t.day, new Map());
      byDay.get(t.day)!.set(key, (byDay.get(t.day)!.get(key) ?? 0) + t.count);
    }
    return [...byDay.entries()].sort((a, b) => b[0].localeCompare(a[0]));
  }, [transitions]);

  const moves = useMemo(
    () => [...new Set(transitions.map((t) => `${t.from}→${t.to}`))].sort(),
    [transitions],
  );

  return (
    <>
      <section className="mt-6 rounded-card border border-hairline bg-surface-raised">
        <h2 className="border-b border-hairline px-4 py-3 text-sm font-medium text-ink">
          Sebaran per aplikasi
        </h2>
        {isLoading ? (
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
                <th className="px-4 py-2 text-left font-semibold">Aplikasi</th>
                <th className="px-4 py-2 text-right font-semibold">Jumlah customer</th>
                <th className="px-4 py-2 text-right font-semibold">Persentase</th>
                <th className="w-28 px-4 py-2" />
              </tr>
            </thead>
            <tbody>
              {rows.map((a) => (
                <tr key={a.application_id ?? a.code} className="border-b border-hairline last:border-0">
                  <td className="px-4 py-2.5">
                    <span className="flex items-center gap-2">
                      <span
                        aria-hidden
                        className="size-2.5 shrink-0 rounded-full"
                        style={{ backgroundColor: a.color }}
                      />
                      <span className="font-medium text-ink">{a.name}</span>
                    </span>
                  </td>
                  <td className="nums px-4 py-2.5 text-right text-ink">
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
                        className="rounded-control border border-hairline px-2.5 py-1 text-xs font-medium text-ink-soft transition-colors hover:bg-surface-sunken"
                      >
                        Lihat detail
                      </button>
                    ) : null}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </section>

      <section className="mt-6 rounded-card border border-hairline bg-surface-raised">
        <div className="border-b border-hairline px-4 py-3">
          <h2 className="text-sm font-medium text-ink">Rekap perpindahan label</h2>
          <p className="mt-0.5 text-xs text-ink-muted">
            Perpindahan antar kategori per hari, mengikuti periode yang dipilih di Performa.
          </p>
        </div>
        {days.length === 0 ? (
          <div className="p-6">
            <EmptyState
              title="Belum ada perpindahan tercatat"
              description="Riwayat label mulai dicatat 22 September 2026. Perpindahan sebelum tanggal itu memang tidak pernah tersimpan."
            />
          </div>
        ) : (
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-hairline text-2xs text-ink-muted uppercase">
                <th className="px-4 py-2 text-left font-semibold">Tanggal</th>
                {moves.map((m) => (
                  <th key={m} className="px-4 py-2 text-right font-semibold">
                    {m}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {days.map(([day, counts]) => (
                <tr key={day} className="border-b border-hairline last:border-0">
                  <td className="px-4 py-2 text-ink-soft">{day}</td>
                  {moves.map((m) => (
                    <td key={m} className="nums px-4 py-2 text-right text-ink">
                      {counts.get(m) ?? <span className="text-ink-muted">–</span>}
                    </td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </section>
    </>
  );
}

/* --- level 3: per nomor ------------------------------------------------------- */

function ContactList({
  category,
  applicationId,
  applicationName,
  search,
  onSearch,
  page,
  onPage,
  onOpen,
}: {
  category: LabelCategory;
  applicationId: string;
  applicationName: string;
  search: string;
  onSearch: (v: string) => void;
  page: number;
  onPage: (p: number) => void;
  onOpen: (id: string) => void;
}) {
  const { data, isLoading } = useSWR<{ contacts: LabelCategoryContact[]; total: number }>(
    labelCategoryContactsPath({
      category,
      applicationId,
      q: search,
      limit: PAGE,
      offset: page * PAGE,
    }),
    fetcher,
    { revalidateOnFocus: false, keepPreviousData: true },
  );

  const rows = data?.contacts ?? [];
  const total = data?.total ?? 0;
  const pages = Math.max(1, Math.ceil(total / PAGE));

  return (
    <section className="mt-6 rounded-card border border-hairline bg-surface-raised">
      <div className="flex flex-wrap items-center gap-3 border-b border-hairline px-4 py-3">
        <h2 className="text-sm font-medium text-ink">
          {applicationName} · {total.toLocaleString('id-ID')} customer
        </h2>
        <label className="relative ml-auto min-w-[200px] flex-1">
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

      {isLoading && rows.length === 0 ? (
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
                <th className="w-12 px-4 py-2 text-left font-semibold">No</th>
                <th className="px-4 py-2 text-left font-semibold">Nomor WhatsApp</th>
                <th className="px-4 py-2 text-left font-semibold">Nama</th>
                <th className="px-4 py-2 text-left font-semibold">Label terakhir</th>
                <th className="px-4 py-2 text-right font-semibold">Total perubahan</th>
                <th className="w-28 px-4 py-2" />
              </tr>
            </thead>
            <tbody>
              {rows.map((c, i) => (
                <tr key={c.contact_id} className="border-b border-hairline last:border-0">
                  <td className="nums px-4 py-2.5 text-ink-muted">{page * PAGE + i + 1}</td>
                  <td className="nums px-4 py-2.5 text-ink-soft">
                    {c.phone ? `+${c.phone}` : <span className="text-ink-muted">–</span>}
                  </td>
                  <td className="px-4 py-2.5 font-medium text-ink">
                    {c.name || <span className="text-ink-muted italic">Belum tersimpan</span>}
                  </td>
                  <td className="px-4 py-2.5">
                    {/* The real label, not the category: it is what is written
                        on the operator's own screen, and a category alone would
                        hide that this Hot came from "FU3 HOT". */}
                    <span className={clsx('text-xs font-medium', TONE[c.category])}>
                      {c.label_name}
                    </span>
                  </td>
                  <td className="nums px-4 py-2.5 text-right text-ink-soft">{c.change_count}</td>
                  <td className="px-4 py-2.5 text-right">
                    <button
                      type="button"
                      onClick={() => onOpen(c.contact_id)}
                      className="rounded-control border border-hairline px-2.5 py-1 text-xs font-medium text-ink-soft transition-colors hover:bg-surface-sunken"
                    >
                      Lihat riwayat
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>

          {pages > 1 ? (
            <div className="flex items-center justify-between border-t border-hairline px-4 py-3 text-xs text-ink-muted">
              <span>
                Menampilkan {page * PAGE + 1}–{Math.min((page + 1) * PAGE, total)} dari{' '}
                {total.toLocaleString('id-ID')}
              </span>
              <span className="flex gap-1.5">
                <PageButton disabled={page === 0} onClick={() => onPage(page - 1)}>
                  Sebelumnya
                </PageButton>
                <PageButton disabled={page >= pages - 1} onClick={() => onPage(page + 1)}>
                  Berikutnya
                </PageButton>
              </span>
            </div>
          ) : null}
        </>
      )}
    </section>
  );
}

/* --- level 4: riwayat satu customer ------------------------------------------- */

function ContactJourney({ contactId, onBack }: { contactId: string; onBack: () => void }) {
  const { data, isLoading } = useSWR<{ history: ContactLabelHistoryRow[] }>(
    contactLabelHistoryPath(contactId),
    fetcher,
    { revalidateOnFocus: false },
  );

  const history = useMemo(() => data?.history ?? [], [data]);
  // Newest first on screen; the journey strip below reads the other way, which
  // is how a journey reads.
  const newestFirst = useMemo(() => [...history].reverse(), [history]);
  const journey = useMemo(
    () => history.filter((h) => h.event_type === 'label_assigned' && h.category !== ''),
    [history],
  );

  return (
    <section className="mt-6 rounded-card border border-hairline bg-surface-raised">
      <div className="flex items-center gap-3 border-b border-hairline px-4 py-3">
        <button
          type="button"
          onClick={onBack}
          className="inline-flex items-center gap-1.5 text-sm text-ink-muted transition-colors hover:text-ink-soft"
        >
          <ArrowLeft className="size-4" />
          Kembali
        </button>
        <h2 className="text-sm font-medium text-ink">Riwayat perubahan label</h2>
        <span className="ml-auto text-xs text-ink-muted">{history.length} perubahan tercatat</span>
      </div>

      {isLoading ? (
        <div className="p-6">
          <Spinner />
        </div>
      ) : history.length === 0 ? (
        <div className="p-6">
          <EmptyState
            title="Belum ada riwayat"
            description="Riwayat label mulai dicatat 22 September 2026. Perubahan sebelum itu tidak pernah tersimpan, dan tidak akan dikarang."
          />
        </div>
      ) : (
        <>
          {journey.length > 1 ? (
            <div className="flex flex-wrap items-center gap-2 border-b border-hairline px-4 py-3">
              {journey.map((h, i) => (
                <span key={`${h.at}-${i}`} className="flex items-center gap-2">
                  <span
                    className={clsx(
                      'rounded-full border border-hairline px-2.5 py-1 text-xs font-medium',
                      TONE[h.category as LabelCategory],
                    )}
                  >
                    {h.label_name}
                  </span>
                  {i < journey.length - 1 ? (
                    <ChevronRight className="size-3.5 text-ink-muted" aria-hidden />
                  ) : null}
                </span>
              ))}
            </div>
          ) : null}

          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-hairline text-2xs text-ink-muted uppercase">
                <th className="px-4 py-2 text-left font-semibold">Tanggal &amp; waktu</th>
                <th className="px-4 py-2 text-left font-semibold">Label</th>
                <th className="px-4 py-2 text-left font-semibold">Diubah oleh</th>
                <th className="px-4 py-2 text-left font-semibold">Sumber</th>
              </tr>
            </thead>
            <tbody>
              {newestFirst.map((h, i) => (
                <tr key={`${h.at}-${i}`} className="border-b border-hairline last:border-0">
                  <td className="px-4 py-2 text-ink-soft">
                    {new Date(h.at).toLocaleString('id-ID', {
                      dateStyle: 'medium',
                      timeStyle: 'short',
                      timeZone: 'Asia/Jakarta',
                    })}
                  </td>
                  <td className="px-4 py-2">
                    <span
                      className={clsx(
                        'text-xs font-medium',
                        h.category ? TONE[h.category] : 'text-ink-soft',
                      )}
                    >
                      {h.event_type === 'label_removed' ? '− ' : '+ '}
                      {h.label_name}
                    </span>
                  </td>
                  <td className="px-4 py-2 text-ink-soft">
                    {/* Empty for anything done on the phone. WhatsApp does not
                        say who was holding it, and a guess in an audit trail is
                        worse than a blank. */}
                    {h.changed_by || <span className="text-ink-muted">Tidak diketahui</span>}
                  </td>
                  <td className="px-4 py-2 text-ink-muted">
                    {h.source === 'whatsapp' ? 'HP' : h.source === 'web' ? 'Web' : h.source}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </>
      )}
    </section>
  );
}

/* --- shared ------------------------------------------------------------------- */

function ExportButton({
  icon: Icon,
  children,
  onClick,
  busy,
}: {
  icon: typeof Download;
  children: React.ReactNode;
  onClick: () => void;
  busy: boolean;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={busy}
      className="inline-flex h-9 items-center gap-1.5 rounded-control border border-hairline bg-surface-raised px-3 text-sm font-medium text-ink-soft transition-colors hover:bg-surface-sunken disabled:opacity-50"
    >
      <Icon className="size-4" />
      {children}
    </button>
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
      className="rounded-control border border-hairline bg-surface-raised px-2.5 py-1.5 text-xs font-medium text-ink-soft transition-colors hover:bg-surface-sunken disabled:opacity-40"
    >
      {children}
    </button>
  );
}
