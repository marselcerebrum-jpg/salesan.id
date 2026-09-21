'use client';

import clsx from 'clsx';
import {
  ChevronRight,
  Copy,
  FileSpreadsheet,
  FileText,
  Loader2,
  RefreshCw,
  Search,
  Smartphone,
} from 'lucide-react';
import Link from 'next/link';
import { useEffect, useMemo, useState } from 'react';
import useSWR from 'swr';

import { FilterChips } from '@/components/contacts/FilterChips';
import { EmptyState, ErrorNote, Spinner } from '@/components/ui/Primitives';
import {
  exportGroups,
  fetchGroups,
  fetcher,
  groupDetailHref,
  groupFacetsPath,
  groupsPath,
  type GroupQuery,
} from '@/lib/api';
import type { ContactFacets, GroupRow } from '@/lib/types';

const PAGE = 100;

/**
 * Fetch Grup: the WhatsApp groups our numbers are inside.
 *
 * One row per group, not per thread. Three of our numbers in the same group is
 * one group reached three ways, so the numbers become a column and the row
 * count stays honest. That is also why the header total and the chip counts
 * differ: the header counts groups, the chips count memberships, and both are
 * labelled as what they are.
 *
 * Member lists are pulled on demand rather than kept current. Each group costs
 * a round trip to WhatsApp, and nine hundred of them is not something to do on
 * a page load; the fetch runs in batches and reports what is left.
 */
export default function GroupsPage() {
  const [applicationId, setApplicationId] = useState<string | null>(null);
  const [accountId, setAccountId] = useState<string | null>(null);
  const [search, setSearch] = useState('');
  const [debounced, setDebounced] = useState('');
  const [page, setPage] = useState(0);
  const [picked, setPicked] = useState<Set<string>>(new Set());
  const [busy, setBusy] = useState<null | 'fetch' | 'csv' | 'xlsx' | 'copy'>(null);
  const [note, setNote] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    const timer = setTimeout(() => setDebounced(search.trim()), 300);
    return () => clearTimeout(timer);
  }, [search]);

  useEffect(() => {
    setPage(0);
    setPicked(new Set());
  }, [applicationId, accountId, debounced]);

  const query: GroupQuery = useMemo(
    () => ({
      application_id: applicationId ?? undefined,
      account_id: accountId ?? undefined,
      search: debounced || undefined,
      limit: PAGE,
      offset: page * PAGE,
    }),
    [applicationId, accountId, debounced, page],
  );

  const { data: facets } = useSWR<ContactFacets>(groupFacetsPath(applicationId), fetcher);

  // A number belongs to one application, so changing the brand makes a chosen
  // number meaningless. Left set, it would filter the table to nothing while
  // both chips still looked active.
  useEffect(() => {
    if (!accountId || !facets) return;
    if (!facets.accounts.some((a) => a.id === accountId)) setAccountId(null);
  }, [facets, accountId]);

  const {
    data,
    isLoading,
    mutate,
  } = useSWR<{ groups: GroupRow[]; total: number; fetched: number }>(
    groupsPath(query),
    fetcher,
    { keepPreviousData: true },
  );

  const groups = useMemo(() => data?.groups ?? [], [data]);
  const total = data?.total ?? 0;
  const fetched = data?.fetched ?? 0;
  const pending = Math.max(0, total - fetched);
  const pages = Math.max(1, Math.ceil(total / PAGE));

  const applicationName =
    facets?.applications.find((f) => f.id === applicationId)?.label ?? null;

  const scopeLabel = useMemo(() => {
    const parts: string[] = [];
    if (applicationName) parts.push(applicationName);
    const acc = facets?.accounts.find((f) => f.id === accountId);
    if (acc) parts.push(acc.label);
    if (debounced) parts.push(`"${debounced}"`);
    return parts.length > 0 ? parts.join(' · ') : 'keseluruhan';
  }, [applicationName, facets, accountId, debounced]);

  function togglePick(jid: string) {
    setPicked((current) => {
      const next = new Set(current);
      if (next.has(jid)) next.delete(jid);
      else next.add(jid);
      return next;
    });
  }

  const allOnPagePicked = groups.length > 0 && groups.every((g) => picked.has(g.chat_jid));

  function toggleAllOnPage() {
    setPicked((current) => {
      const next = new Set(current);
      if (allOnPagePicked) groups.forEach((g) => next.delete(g.chat_jid));
      else groups.forEach((g) => next.add(g.chat_jid));
      return next;
    });
  }

  const selection = useMemo(() => [...picked], [picked]);

  /**
   * Pulling member lists, a batch at a time.
   *
   * Loops until nothing is left or a batch achieves nothing, refreshing the
   * count between rounds so the operator watches it move. A batch that fetches
   * zero means the rest are failing, and grinding through nine hundred of them
   * to prove it would take a quarter of an hour.
   */
  async function runFetch() {
    setBusy('fetch');
    setError(null);
    setNote(null);
    try {
      let done = 0;
      let failed = 0;
      let corrected = 0;
      let reason = '';
      for (;;) {
        const result = await fetchGroups(query);
        done += result.done;
        failed += result.failed;
        corrected += result.corrected ?? 0;
        if (result.reason && !reason) reason = result.reason;
        await mutate();
        if (result.remaining === 0 || result.done === 0) break;
      }
      const parts = [`${done} grup diambil`];
      if (failed > 0) parts.push(`${failed} gagal${reason ? `: ${reason}` : ''}`);
      // Worth saying out loud: rows disappearing from the list without
      // explanation looks like data loss, not like a correction.
      if (corrected > 0) parts.push(`${corrected} grup yang tidak Anda ikuti disembunyikan`);
      setNote(parts.join(' · '));
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Fetch gagal.');
    } finally {
      setBusy(null);
    }
  }

  async function copySelection() {
    const rows = groups.filter((g) => picked.has(g.chat_jid));
    const source = rows.length > 0 ? rows : groups;
    if (source.length === 0) return;

    setBusy('copy');
    setError(null);
    setNote(null);
    try {
      // Tab separated, so it lands in a spreadsheet as columns rather than one
      // long cell. The header goes with it; a pasted block with no header is a
      // block somebody has to label by hand.
      const lines = [
        ['Nama Grup', 'Jumlah Nomor', 'Nomor Kami', 'Jumlah Anggota'].join('\t'),
        ...source.map((g) =>
          [
            g.name || g.chat_jid,
            String(g.account_count),
            g.accounts.map((a) => a.account_name).join(', '),
            g.fetched ? String(g.member_count) : 'belum diambil',
          ].join('\t'),
        ),
      ];
      await navigator.clipboard.writeText(lines.join('\n'));
      setNote(
        `${source.length} grup disalin${rows.length === 0 ? ' (seluruh halaman ini)' : ''}.`,
      );
    } catch {
      setError('Tidak bisa menyalin. Browser menolak akses clipboard.');
    } finally {
      setBusy(null);
    }
  }

  async function download(format: 'csv' | 'xlsx') {
    setBusy(format);
    setError(null);
    setNote(null);
    try {
      await exportGroups(query, format, selection);
      setNote(
        selection.length > 0
          ? `${selection.length} grup terpilih diekspor.`
          : `${total.toLocaleString('id-ID')} grup diekspor.`,
      );
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Ekspor gagal.');
    } finally {
      setBusy(null);
    }
  }

  return (
    <div className="px-5 py-6 lg:px-8 lg:py-8">
      <header className="flex flex-wrap items-start justify-between gap-4">
        <div>
          <h1 className="text-2xl font-semibold tracking-[-0.02em] text-ink">Fetch Grup</h1>
          <p className="mt-1 text-sm text-ink-muted">
            <span className="nums font-medium text-ink-soft">{total.toLocaleString('id-ID')}</span>{' '}
            grup · {scopeLabel}
          </p>
          {/* Stated on the page rather than left to be discovered: a member
              count of zero on an unfetched group is not a fact about the group. */}
          {pending > 0 ? (
            <p className="mt-0.5 text-xs text-warn">
              Anggota ter-fetch: <span className="nums">{fetched.toLocaleString('id-ID')}</span>/
              <span className="nums">{total.toLocaleString('id-ID')}</span> grup ·{' '}
              <span className="nums">{pending.toLocaleString('id-ID')}</span> grup belum — tekan
              &ldquo;Fetch / Refresh&rdquo;.
            </p>
          ) : null}
        </div>

        <div className="flex flex-wrap items-center gap-2">
          <Action icon={Copy} busy={busy === 'copy'} onClick={() => void copySelection()}>
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
          <button
            type="button"
            onClick={() => void runFetch()}
            disabled={busy === 'fetch'}
            className="inline-flex h-9 items-center gap-1.5 rounded-control bg-brand-700 px-3.5 text-sm font-medium text-white transition-colors hover:bg-brand-800 disabled:opacity-60"
          >
            {busy === 'fetch' ? (
              <Loader2 className="size-4 animate-spin" />
            ) : (
              <RefreshCw className="size-4" />
            )}
            Fetch / Refresh
          </button>
        </div>
      </header>

      {note ? (
        <p className="mt-3 rounded-control border border-brand-600/25 bg-brand-600/8 px-3 py-2 text-sm text-brand-700">
          {note}
        </p>
      ) : null}
      {error ? (
        <div className="mt-3">
          <ErrorNote message={error} />
        </div>
      ) : null}

      <FilterChips
        label="Filter per Aplikasi"
        facets={facets?.applications ?? []}
        total={facets?.total ?? 0}
        totalLabel="Keseluruhan"
        selected={applicationId}
        onSelect={setApplicationId}
      />

      <FilterChips
        label="Filter per Nomor"
        variant="detail"
        noun="grup"
        facets={facets?.accounts ?? []}
        total={facets?.scoped_total ?? 0}
        totalLabel={applicationName ? 'Semua nomor aplikasi ini' : 'Semua nomor'}
        selected={accountId}
        onSelect={setAccountId}
        empty={
          applicationName
            ? `Belum ada nomor WhatsApp yang ditugaskan ke ${applicationName}.`
            : 'Belum ada nomor WhatsApp yang terhubung.'
        }
      />

      <div className="mt-4 rounded-card border border-hairline bg-surface-raised shadow-e1">
        <div className="flex flex-wrap items-center gap-3 border-b border-hairline px-4 py-3">
          <label className="relative min-w-0 flex-1">
            <Search className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-ink-muted" />
            <input
              value={search}
              onChange={(event) => setSearch(event.target.value)}
              placeholder="Cari nama grup…"
              aria-label="Cari nama grup"
              className="h-9 w-full rounded-control bg-surface-sunken/60 pr-3 pl-9 text-sm text-ink outline-none placeholder:text-ink-muted focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-brand-600"
            />
          </label>
          {picked.size > 0 ? (
            <p className="shrink-0 text-xs text-ink-muted">
              <span className="nums font-medium text-ink-soft">{picked.size}</span> grup dipilih
            </p>
          ) : null}
        </div>

        {isLoading && groups.length === 0 ? (
          <div className="p-6">
            <Spinner label="Memuat grup…" />
          </div>
        ) : groups.length === 0 ? (
          <div className="p-6">
            <EmptyState
              title="Tidak ada grup"
              description={
                debounced || applicationId || accountId
                  ? 'Tidak ada yang cocok dengan filter ini.'
                  : 'Belum ada grup yang tercatat. Sinkronkan akun WhatsApp lebih dulu di halaman Akun WhatsApp.'
              }
            />
          </div>
        ) : (
          <div className="max-h-[70vh] overflow-auto">
            {/* The box scrolls, not the page, which is what lets the header
                stay put. `overflow-x-auto` alone never could: it makes the
                element a scroll container that has no height to scroll. */}
            <table className="w-full min-w-[820px] border-collapse text-sm">
              <thead>
                <tr className="border-b border-hairline text-ink-muted">
                  <Th className="w-10">
                    <input
                      type="checkbox"
                      checked={allOnPagePicked}
                      onChange={toggleAllOnPage}
                      aria-label="Pilih semua grup di halaman ini"
                      className="size-4 accent-brand-700"
                    />
                  </Th>
                  <Th className="w-12">No</Th>
                  <Th>Nama Grup</Th>
                  <Th>Nomor</Th>
                  <Th className="text-right">Jumlah Anggota</Th>
                  <Th className="w-12" />
                </tr>
              </thead>
              <tbody>
                {groups.map((group, index) => {
                  const checked = picked.has(group.chat_jid);
                  const single = group.accounts[0];
                  const label = group.name || group.chat_jid;
                  const href = groupDetailHref(group.chat_jid);
                  return (
                    <tr
                      key={group.chat_jid}
                      className={clsx(
                        'h-[52px] border-b border-hairline transition-colors',
                        checked ? 'bg-brand-600/8' : 'hover:bg-surface-sunken/50',
                      )}
                    >
                      <Td>
                        <input
                          type="checkbox"
                          checked={checked}
                          onChange={() => togglePick(group.chat_jid)}
                          aria-label={`Pilih ${label}`}
                          className="size-4 accent-brand-700"
                        />
                      </Td>
                      <Td className="nums text-ink-muted">{page * PAGE + index + 1}</Td>
                      <Td>
                        <span className="flex flex-wrap items-center gap-2">
                          {/* The name is the link, not only the arrow: it is
                              what the eye lands on, and a row whose only target
                              is a 28px chevron is a row that has to be aimed at. */}
                          <Link
                            href={href}
                            className="font-medium text-ink underline-offset-2 hover:underline"
                          >
                            {label}
                          </Link>
                          {!group.fetched ? (
                            <span className="rounded-full bg-warn-soft px-2 py-0.5 text-2xs font-medium text-warn">
                              anggota belum diambil
                            </span>
                          ) : null}
                        </span>
                      </Td>
                      <Td>
                        <span
                          title={group.accounts.map((a) => a.account_name).join(', ')}
                          className="inline-flex items-center gap-1 rounded-full bg-surface-sunken px-2 py-0.5 text-2xs text-ink-soft"
                        >
                          <Smartphone className="size-3 shrink-0" />
                          {/* One number is named; several are counted. Listing
                              four names would wrap the row and say less. */}
                          {group.account_count === 1 && single
                            ? single.account_name
                            : `${group.account_count} nomor`}
                        </span>
                      </Td>
                      <Td className="nums text-right text-ink-soft">
                        {group.fetched ? group.member_count.toLocaleString('id-ID') : '–'}
                      </Td>
                      <Td>
                        <Link
                          href={href}
                          aria-label={`Lihat anggota ${label}`}
                          className="inline-flex rounded-lg p-1.5 text-ink-muted transition-colors hover:bg-surface-sunken hover:text-ink-soft"
                        >
                          <ChevronRight className="size-4" />
                        </Link>
                      </Td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}

        {pages > 1 ? (
          <div className="flex flex-wrap items-center justify-between gap-3 border-t border-hairline px-4 py-3">
            <p className="text-xs text-ink-muted">
              <span className="nums">{(page * PAGE + 1).toLocaleString('id-ID')}</span>–
              <span className="nums">
                {Math.min((page + 1) * PAGE, total).toLocaleString('id-ID')}
              </span>{' '}
              dari <span className="nums">{total.toLocaleString('id-ID')}</span>
            </p>
            <div className="flex gap-1.5">
              <PageButton disabled={page === 0} onClick={() => setPage(page - 1)}>
                Sebelumnya
              </PageButton>
              <PageButton disabled={page >= pages - 1} onClick={() => setPage(page + 1)}>
                Berikutnya
              </PageButton>
            </div>
          </div>
        ) : null}
      </div>
    </div>
  );
}

function Action({
  icon: Icon,
  children,
  onClick,
  busy = false,
}: {
  icon: typeof Copy;
  children: React.ReactNode;
  onClick: () => void;
  busy?: boolean;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={busy}
      className="inline-flex h-9 items-center gap-1.5 rounded-control border border-hairline bg-surface-raised px-3 text-sm font-medium text-ink-soft transition-colors hover:bg-surface-sunken disabled:opacity-50"
    >
      {busy ? <Loader2 className="size-4 animate-spin" /> : <Icon className="size-4" />}
      {children}
    </button>
  );
}

function Th({ children, className }: { children?: React.ReactNode; className?: string }) {
  return (
    <th
      scope="col"
      // Pinned to the top of the scroll box, opaque, and carrying its own
      // bottom border: a border on the row does not travel with a sticky cell.
      className={clsx(
        'sticky top-0 z-10 h-[42px] border-b border-hairline bg-surface-raised px-3 text-left text-2xs font-semibold tracking-wide uppercase',
        className,
      )}
    >
      {children}
    </th>
  );
}

function Td({ children, className }: { children?: React.ReactNode; className?: string }) {
  return <td className={clsx('px-3 align-middle', className)}>{children}</td>;
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
