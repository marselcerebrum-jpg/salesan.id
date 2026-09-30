'use client';

import { ArrowLeft, ChevronRight, Download, FileSpreadsheet } from 'lucide-react';
import Link from 'next/link';
import { useEffect, useMemo, useState } from 'react';
import useSWR from 'swr';

import { PeriodMenu } from '@/components/analytics/PeriodMenu';
import { FilterBar, type StatusLabelFilter } from '@/components/statuslabel/FilterBar';
import { CategoryBanner, CategoryPills, categoryOf } from '@/components/statuslabel/parts';
import {
  ApplicationTable,
  ContactTable,
  CustomerJourney,
  TransitionRecap,
} from '@/components/statuslabel/Tables';
import { ErrorNote } from '@/components/ui/Primitives';
import {
  analyticsFiltersPathFor,
  contactLabelHistoryPath,
  exportLabelCategory,
  fetcher,
  labelCategoryContactsPath,
  labelCategoryPath,
  type AnalyticsQuery,
  type ContactLabelHistoryRow,
  type LabelCategory,
  type LabelCategoryContact,
  type LabelCategoryResponse,
} from '@/lib/api';
import type { FilterOptions } from '@/lib/types';

const PAGE = 25;

/**
 * Status Label, four depths on one route.
 *
 * Which category, which brand, which customer, what happened to them: one
 * question asked with more and more precision. Four routes would rebuild the
 * page on every step and lose the reader's place going back up; one route with
 * the depth written into the address keeps the step cheap and the view
 * something that can still be sent to somebody else.
 *
 * This file is the orchestrator and nothing else. Each depth is its own
 * component, so a change to the customer table cannot reach the one above it.
 */
export default function StatusLabelPage() {
  const [category, setCategory] = useState<LabelCategory>('hot');
  const [filter, setFilter] = useState<StatusLabelFilter>({ applicationId: '', accountId: '' });
  const [contactId, setContactId] = useState<string | null>(null);
  const [period, setPeriod] = useState<AnalyticsQuery>({});
  const [search, setSearch] = useState('');
  const [page, setPage] = useState(0);
  const [busy, setBusy] = useState<string | null>(null);
  const [failure, setFailure] = useState<string | null>(null);

  // Read once, from whatever link brought the reader here.
  useEffect(() => {
    const q = new URLSearchParams(window.location.search);
    const c = q.get('kategori');
    if (c === 'cold' || c === 'warm' || c === 'hot') setCategory(c);
    setFilter({ applicationId: q.get('app') ?? '', accountId: q.get('nomor') ?? '' });
    setContactId(q.get('kontak'));
  }, []);

  // Mirrored back without navigating: replaceState leaves the page alone.
  useEffect(() => {
    const url = new URL(window.location.href);
    url.searchParams.set('kategori', category);
    for (const [key, value] of [
      ['app', filter.applicationId],
      ['nomor', filter.accountId],
      ['kontak', contactId ?? ''],
    ] as const) {
      if (value) url.searchParams.set(key, value);
      else url.searchParams.delete(key);
    }
    window.history.replaceState(null, '', url);
  }, [category, filter, contactId]);

  const options = useSWR<FilterOptions>(analyticsFiltersPathFor(), fetcher);

  const query = useMemo<AnalyticsQuery>(
    () => ({
      ...period,
      application_id: filter.applicationId || undefined,
      account_id: filter.accountId || undefined,
    }),
    [period, filter],
  );

  const { data, isLoading, error } = useSWR<LabelCategoryResponse>(
    labelCategoryPath(query, { category }),
    fetcher,
    { revalidateOnFocus: false, keepPreviousData: true },
  );

  const summary = data?.summary;
  const apps = useMemo(() => data?.applications ?? [], [data]);
  const transitions = useMemo(() => data?.transitions ?? [], [data]);

  // Which brand the reader has drilled into. Held apart from the filter above:
  // the filter narrows what is counted, this picks one row to open.
  const [openApp, setOpenApp] = useState<string | null>(null);
  const app = apps.find((a) => a.application_id === openApp) ?? null;

  const contacts = useSWR<{ contacts: LabelCategoryContact[]; total: number }>(
    openApp && !contactId
      ? labelCategoryContactsPath(
          { ...query, application_id: openApp },
          { category, q: search, limit: PAGE, offset: page * PAGE },
        )
      : null,
    fetcher,
    { revalidateOnFocus: false, keepPreviousData: true },
  );

  const history = useSWR<{ history: ContactLabelHistoryRow[] }>(
    contactId ? contactLabelHistoryPath(contactId) : null,
    fetcher,
    { revalidateOnFocus: false },
  );

  const openContact = useMemo(
    () => contacts.data?.contacts.find((c) => c.contact_id === contactId) ?? null,
    [contacts.data, contactId],
  );

  async function download(scope: 'summary' | 'detail', format: 'csv' | 'xlsx') {
    setBusy(`${scope}-${format}`);
    setFailure(null);
    try {
      await exportLabelCategory(
        { ...query, application_id: openApp ?? (filter.applicationId || undefined) },
        { scope, format, category },
      );
    } catch (err) {
      setFailure(err instanceof Error ? err.message : 'Gagal mengunduh laporan.');
    } finally {
      setBusy(null);
    }
  }

  const tone = categoryOf(category);

  return (
    <div className="px-5 py-6 lg:px-8 lg:py-8">
      <nav className="flex flex-wrap items-center gap-1.5 text-sm text-ink-muted">
        <Link href="/performa" className="transition-colors hover:text-ink-soft">
          Performa
        </Link>
        <Crumb />
        <Step
          active={!openApp && !contactId}
          onClick={() => {
            setOpenApp(null);
            setContactId(null);
          }}
        >
          Status Label
        </Step>
        <Crumb />
        <Step
          active={!openApp && !contactId}
          onClick={() => {
            setOpenApp(null);
            setContactId(null);
          }}
        >
          {tone.label}
        </Step>
        {app ? (
          <>
            <Crumb />
            <Step active={!contactId} onClick={() => setContactId(null)}>
              {app.name}
            </Step>
          </>
        ) : null}
        {contactId ? (
          <>
            <Crumb />
            <span className="nums text-ink">{openContact?.phone ?? 'Customer'}</span>
          </>
        ) : null}
      </nav>

      <header className="mt-3 flex flex-wrap items-start justify-between gap-4">
        <div className="flex items-start gap-3">
          {openApp || contactId ? (
            <button
              type="button"
              onClick={() => (contactId ? setContactId(null) : setOpenApp(null))}
              aria-label="Kembali"
              className="mt-1 grid size-8 place-items-center rounded-full text-ink-muted transition-colors hover:bg-surface-sunken hover:text-ink-soft"
            >
              <ArrowLeft className="size-4" />
            </button>
          ) : null}
          <div>
            <h1 className="text-2xl font-semibold tracking-[-0.02em] text-ink">
              {contactId
                ? (openContact?.name || 'Riwayat Label Customer')
                : app
                  ? `${app.name} — Status ${tone.label}`
                  : 'Detail Status Label'}
            </h1>
            <p className="mt-1 text-sm text-ink-muted">
              {contactId
                ? openContact?.phone
                  ? `+${openContact.phone}`
                  : 'Perjalanan label pelanggan ini.'
                : 'Kondisi customer saat ini, dikelompokkan dari label WhatsApp yang paling terakhir dipasang padanya.'}
            </p>
          </div>
        </div>

        <div className="flex flex-wrap items-center gap-2">
          <PeriodMenu value={period} onChange={setPeriod} />
          <Action
            icon={Download}
            busy={busy === 'summary-csv'}
            onClick={() => void download('summary', 'csv')}
          >
            Rekap CSV
          </Action>
          <Action
            icon={FileSpreadsheet}
            busy={busy === 'detail-xlsx'}
            onClick={() => void download('detail', 'xlsx')}
          >
            Detail XLSX
          </Action>
        </div>
      </header>

      {failure ? (
        <div className="mt-3">
          <ErrorNote message={failure} />
        </div>
      ) : null}

      <div className="mt-5">
        <CategoryPills
          value={category}
          summary={summary}
          onChange={(c) => {
            setCategory(c);
            setOpenApp(null);
            setContactId(null);
            setPage(0);
          }}
        />
      </div>

      {!contactId ? (
        <div className="mt-4">
          <FilterBar
            applications={options.data?.applications ?? []}
            accounts={options.data?.accounts ?? []}
            value={filter}
            onApply={(next) => {
              setFilter(next);
              setOpenApp(null);
              setPage(0);
            }}
          />
        </div>
      ) : null}

      {error ? (
        <div className="mt-5">
          <ErrorNote message="Status label tidak dapat dimuat." />
        </div>
      ) : contactId ? (
        <>
          <div className="mt-5 grid gap-3 sm:grid-cols-3">
            <CategoryBanner
              category={category}
              total={history.data?.history.length ?? 0}
              caption="Total Perubahan Tercatat"
              unit="kali"
            />
            {openContact ? (
              <>
                <Card title="Label Terakhir" value={openContact.label_name} tone={tone.tone} />
                <Card
                  title="Nomor WhatsApp"
                  value={openContact.phone ? `+${openContact.phone}` : '–'}
                />
              </>
            ) : null}
          </div>
          <div className="mt-4">
            <CustomerJourney
              loading={history.isLoading}
              history={history.data?.history ?? []}
            />
          </div>
        </>
      ) : openApp ? (
        <>
          <div className="mt-5">
            <CategoryBanner category={category} total={contacts.data?.total ?? 0} />
          </div>
          <div className="mt-4">
            <ContactTable
              loading={contacts.isLoading}
              rows={contacts.data?.contacts ?? []}
              total={contacts.data?.total ?? 0}
              page={page}
              pageSize={PAGE}
              search={search}
              onSearch={(v) => {
                setSearch(v);
                setPage(0);
              }}
              onPage={setPage}
              onOpen={setContactId}
            />
          </div>
        </>
      ) : (
        <>
          <div className="mt-5">
            <CategoryBanner category={category} total={summary?.[category] ?? 0} />
          </div>
          {/*
            Said plainly because the period picker sits right above it, and the
            habit of a filtered dashboard is to assume everything obeys it.
            These are a count of how things stand, not of what happened in a
            month: a customer tagged Hot in June is still Hot today.
          */}
          <p className="mt-2 text-2xs text-ink-muted">
            Angka kategori adalah kondisi sekarang dan tidak mengikuti periode. Rekap perpindahan di
            bawah yang mengikuti periode.
          </p>

          <div className="mt-4">
            <ApplicationTable
              loading={isLoading}
              rows={apps}
              total={summary?.[category] ?? 0}
              onPick={(id) => {
                setOpenApp(id);
                setPage(0);
                setSearch('');
              }}
            />
          </div>

          <div className="mt-4">
            <TransitionRecap rows={transitions} />
          </div>
        </>
      )}
    </div>
  );
}

function Crumb() {
  return <ChevronRight className="size-3.5" aria-hidden />;
}

function Step({
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
      className={active ? 'text-ink' : 'transition-colors hover:text-ink-soft'}
    >
      {children}
    </button>
  );
}

function Card({ title, value, tone }: { title: string; value: string; tone?: string }) {
  return (
    <div className="rounded-card border border-hairline bg-surface-raised p-4">
      <span className="text-sm text-ink-muted">{title}</span>
      <span className={`mt-1 block truncate text-xl font-semibold text-ink ${tone ?? ''}`}>
        {value}
      </span>
    </div>
  );
}

function Action({
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
