'use client';

import clsx from 'clsx';
import {
  Combine,
  Download,
  Loader2,
  RefreshCw,
  Search,
  Trash2,
  Upload,
} from 'lucide-react';
import { useEffect, useMemo, useState } from 'react';
import useSWR from 'swr';

import { FilterChips } from '@/components/contacts/FilterChips';
import { ImportDialog } from '@/components/contacts/ImportDialog';
import { ConfirmDialog, useConfirm } from '@/components/ui/ConfirmDialog';
import { EmptyState, ErrorNote, Spinner } from '@/components/ui/Primitives';
import {
  contactFacetsPath,
  contactsPath,
  deleteContact,
  deleteContacts,
  exportContacts,
  fetcher,
  mergeDuplicateContacts,
  syncAccount,
  type ContactQuery,
} from '@/lib/api';
import type { Account, Contact, ContactFacets } from '@/lib/types';

/** How many rows one page of the address book holds. */
const PAGE = 100;

/**
 * The address book.
 *
 * Sixteen thousand rows is the working size, so nothing here loads everything:
 * the table is paged, the counts come from their own query, and the export
 * streams server-side. The two chip rows are the primary navigation, because
 * with that many contacts the question is almost never "show me all of them"
 * but "show me this brand on this number".
 *
 * There is deliberately no button that empties the address book. Deleting is
 * per row or per selection, both of which say how many and ask first. A single
 * press that undoes a day of syncing is not a feature.
 */
export default function ContactsPage() {
  const [applicationId, setApplicationId] = useState<string | null>(null);
  const [accountId, setAccountId] = useState<string | null>(null);
  const [search, setSearch] = useState('');
  const [debounced, setDebounced] = useState('');
  const [page, setPage] = useState(0);
  const [picked, setPicked] = useState<Set<string>>(new Set());
  const [importOpen, setImportOpen] = useState(false);
  const [busy, setBusy] = useState<null | 'sync' | 'export' | 'merge' | 'delete'>(null);
  const [note, setNote] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const confirm = useConfirm();

  useEffect(() => {
    const timer = setTimeout(() => setDebounced(search.trim()), 300);
    return () => clearTimeout(timer);
  }, [search]);

  // Any change of filter starts again at the first page: staying on page seven
  // of a narrower result is how somebody concludes the filter returned nothing.
  useEffect(() => {
    setPage(0);
    setPicked(new Set());
  }, [applicationId, accountId, debounced]);

  const query: ContactQuery = useMemo(
    () => ({
      application_id: applicationId ?? undefined,
      account_id: accountId ?? undefined,
      search: debounced || undefined,
      limit: PAGE,
      offset: page * PAGE,
    }),
    [applicationId, accountId, debounced, page],
  );

  const { data: accountsData } = useSWR<{ accounts: Account[] }>('/accounts', fetcher);
  const accounts = useMemo(() => accountsData?.accounts ?? [], [accountsData]);

  // Keyed by the chosen application, so switching brands refetches the number
  // row instead of leaving the previous brand's numbers on screen.
  const { data: facets, mutate: mutateFacets } = useSWR<ContactFacets>(
    contactFacetsPath(applicationId),
    fetcher,
  );

  // A number belongs to one brand. Choosing another brand makes the selected
  // number meaningless, and leaving it set would filter the table down to
  // nothing while both chips still looked active.
  useEffect(() => {
    if (!accountId || !facets) return;
    if (!facets.accounts.some((a) => a.id === accountId)) setAccountId(null);
  }, [facets, accountId]);

  const applicationName =
    facets?.applications.find((f) => f.id === applicationId)?.label ?? null;

  const {
    data,
    isLoading,
    mutate: mutateList,
  } = useSWR<{ contacts: Contact[]; total: number }>(contactsPath(query), fetcher, {
    keepPreviousData: true,
  });

  const contacts = useMemo(() => data?.contacts ?? [], [data]);
  const total = data?.total ?? 0;
  const pages = Math.max(1, Math.ceil(total / PAGE));

  function refresh() {
    void mutateList();
    void mutateFacets();
  }

  function togglePick(id: string) {
    setPicked((current) => {
      const next = new Set(current);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }

  const allOnPagePicked = contacts.length > 0 && contacts.every((c) => picked.has(c.id));

  function toggleAllOnPage() {
    setPicked((current) => {
      const next = new Set(current);
      if (allOnPagePicked) contacts.forEach((c) => next.delete(c.id));
      else contacts.forEach((c) => next.add(c.id));
      return next;
    });
  }

  async function run(kind: NonNullable<typeof busy>, work: () => Promise<string>) {
    setBusy(kind);
    setError(null);
    setNote(null);
    try {
      setNote(await work());
      refresh();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Gagal.');
    } finally {
      setBusy(null);
    }
  }

  /**
   * Pulling the address book from the phones.
   *
   * One number when one is selected, every number otherwise. Run in sequence
   * rather than at once: each sync reads a whole contact store from its phone,
   * and firing five of them together would queue them behind each other anyway
   * while making the failure harder to attribute.
   */
  function sync() {
    const targets = accountId ? accounts.filter((a) => a.id === accountId) : accounts;
    if (targets.length === 0) {
      setError('Belum ada akun WhatsApp yang terhubung.');
      return;
    }
    void run('sync', async () => {
      let contactCount = 0;
      const failed: string[] = [];
      for (const account of targets) {
        try {
          const result = await syncAccount(account.id);
          contactCount += result.contacts;
        } catch {
          failed.push(account.name);
        }
      }
      const done = `${contactCount.toLocaleString('id-ID')} kontak dari ${targets.length - failed.length} nomor`;
      return failed.length > 0 ? `${done}. Gagal: ${failed.join(', ')}.` : done;
    });
  }

  function merge() {
    confirm.ask({
      title: 'Gabungkan nomor yang sama?',
      description:
        'Nomor yang tersimpan dalam format berbeda (0812…, +62 812…, 62812…) disamakan lalu digabung menjadi satu kontak. Riwayat chat, label, dan performa dari keduanya ikut dipindahkan. Tindakan ini tidak bisa dibatalkan.',
      confirmLabel: 'Gabungkan',
      icon: Combine,
      onConfirm: () =>
        run('merge', async () => {
          const result = await mergeDuplicateContacts();
          if (result.merged === 0 && result.normalised === 0) {
            return 'Tidak ada nomor kembar yang ditemukan.';
          }
          return `${result.normalised} nomor dirapikan formatnya, ${result.merged} kontak digabung.`;
        }),
    });
  }

  function removePicked() {
    const ids = [...picked];
    if (ids.length === 0) return;
    confirm.ask({
      title: `Hapus ${ids.length} kontak?`,
      description:
        'Kontak hilang dari buku alamat ini. Riwayat chat, label, dan angka performanya tetap utuh, dan buku alamat di HP tidak ikut berubah. Tindakan ini tidak bisa dibatalkan.',
      confirmLabel: `Hapus ${ids.length}`,
      tone: 'danger',
      onConfirm: () =>
        run('delete', async () => {
          const result = await deleteContacts(ids);
          setPicked(new Set());
          return `${result.deleted} kontak dihapus.`;
        }),
    });
  }

  function removeOne(contact: Contact) {
    const name = contact.name ?? contact.push_name ?? contact.phone_number ?? 'kontak ini';
    confirm.ask({
      title: `Hapus ${name}?`,
      description:
        'Hilang dari buku alamat ini. Riwayat chat dan angka performanya tetap utuh, dan buku alamat di HP tidak ikut berubah.',
      confirmLabel: 'Hapus',
      tone: 'danger',
      onConfirm: () =>
        run('delete', async () => {
          await deleteContact(contact.id);
          return `${name} dihapus.`;
        }),
    });
  }

  const scopeLabel = useMemo(() => {
    const parts: string[] = [];
    const app = facets?.applications.find((f) => f.id === applicationId);
    const acc = facets?.accounts.find((f) => f.id === accountId);
    if (app) parts.push(app.label);
    if (acc) parts.push(acc.label);
    if (debounced) parts.push(`"${debounced}"`);
    return parts.length > 0 ? parts.join(' · ') : 'keseluruhan';
  }, [facets, applicationId, accountId, debounced]);

  return (
    <div className="px-5 py-6 lg:px-8 lg:py-8">
      <header className="flex flex-wrap items-start justify-between gap-4">
        <div>
          <h1 className="text-2xl font-semibold tracking-[-0.02em] text-ink">Kontak</h1>
          <p className="mt-1 text-sm text-ink-muted">
            <span className="nums font-medium text-ink-soft">{total.toLocaleString('id-ID')}</span>{' '}
            kontak · {scopeLabel}
          </p>
        </div>

        <div className="flex flex-wrap items-center gap-2">
          <Action icon={RefreshCw} onClick={sync} busy={busy === 'sync'}>
            Sinkronisasi Kontak
          </Action>
          <Action icon={Upload} onClick={() => setImportOpen(true)}>
            Import CSV
          </Action>
          <Action
            icon={Download}
            busy={busy === 'export'}
            onClick={() =>
              void run('export', async () => {
                // Without the paging keys: the file is the whole filtered set,
                // not the hundred rows that happen to be on screen.
                await exportContacts({ ...query, limit: undefined, offset: undefined });
                return `${total.toLocaleString('id-ID')} kontak diekspor.`;
              })
            }
          >
            Ekspor CSV
          </Action>
          <Action icon={Combine} onClick={merge} busy={busy === 'merge'}>
            Gabung Duplikat
          </Action>
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

      {/* The brand, counted across the whole workspace. This row never moves
          when a number is chosen, so the reader keeps a fixed sense of scale. */}
      <FilterChips
        label="Filter per Aplikasi"
        facets={facets?.applications ?? []}
        total={facets?.total ?? 0}
        totalLabel="Keseluruhan"
        selected={applicationId}
        onSelect={setApplicationId}
      />

      {/* The numbers inside that brand, counted inside it. Picking a number
          from another brand would not narrow this view, it would replace it. */}
      <FilterChips
        label="Filter per Nomor"
        variant="detail"
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
              placeholder="Cari kontak…"
              aria-label="Cari kontak"
              className="h-9 w-full rounded-control bg-surface-sunken/60 pr-3 pl-9 text-sm text-ink outline-none placeholder:text-ink-muted focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-brand-600"
            />
          </label>

          {picked.size > 0 ? (
            <button
              type="button"
              onClick={removePicked}
              disabled={busy === 'delete'}
              className="inline-flex h-9 shrink-0 items-center gap-1.5 rounded-control bg-danger px-3 text-sm font-medium text-white transition-opacity hover:opacity-90 disabled:opacity-50"
            >
              {busy === 'delete' ? (
                <Loader2 className="size-4 animate-spin" />
              ) : (
                <Trash2 className="size-4" />
              )}
              Hapus {picked.size} terpilih
            </button>
          ) : null}
        </div>

        {isLoading && contacts.length === 0 ? (
          <div className="p-6">
            <Spinner label="Memuat kontak…" />
          </div>
        ) : contacts.length === 0 ? (
          <div className="p-6">
            <EmptyState
              title="Tidak ada kontak"
              description={
                debounced || applicationId || accountId
                  ? 'Tidak ada yang cocok dengan filter ini. Ubah filternya, atau kosongkan pencarian.'
                  : 'Tekan Sinkronisasi Kontak untuk menarik buku alamat dari HP, atau Import CSV untuk menambahkan sendiri.'
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
                      aria-label="Pilih semua di halaman ini"
                      className="size-4 accent-brand-700"
                    />
                  </Th>
                  <Th>Nama</Th>
                  <Th>Nomor</Th>
                  <Th>Aplikasi</Th>
                  <Th>Label</Th>
                  <Th className="w-12" />
                </tr>
              </thead>
              <tbody>
                {contacts.map((contact) => {
                  const name = contact.name ?? contact.push_name ?? '-';
                  const checked = picked.has(contact.id);
                  return (
                    <tr
                      key={contact.id}
                      className={clsx(
                        'h-[52px] border-b border-hairline last:border-0 transition-colors',
                        checked ? 'bg-brand-600/8' : 'hover:bg-surface-sunken/50',
                      )}
                    >
                      <Td>
                        <input
                          type="checkbox"
                          checked={checked}
                          onChange={() => togglePick(contact.id)}
                          aria-label={`Pilih ${name}`}
                          className="size-4 accent-brand-700"
                        />
                      </Td>
                      <Td className="font-medium text-ink">
                        <span className="block max-w-[260px] truncate">{name}</span>
                      </Td>
                      <Td className="nums whitespace-nowrap text-ink-soft">
                        {contact.phone_number ?? '-'}
                      </Td>
                      <Td>
                        {contact.application_code ? (
                          <span
                            className="inline-flex items-center rounded-full px-2 py-0.5 text-2xs font-semibold text-white"
                            style={{ backgroundColor: contact.application_color ?? '#4e8064' }}
                          >
                            {contact.application_code}
                          </span>
                        ) : (
                          <span className="text-2xs text-ink-muted">Tanpa aplikasi</span>
                        )}
                      </Td>
                      <Td>
                        {contact.labels.length > 0 ? (
                          <span className="flex flex-wrap gap-1">
                            {contact.labels.map((label) => (
                              <span
                                key={label}
                                className="rounded-full bg-surface-sunken px-2 py-0.5 text-2xs text-ink-soft"
                              >
                                {label}
                              </span>
                            ))}
                          </span>
                        ) : (
                          <span className="text-2xs text-ink-muted">–</span>
                        )}
                      </Td>
                      <Td>
                        <button
                          type="button"
                          onClick={() => removeOne(contact)}
                          aria-label={`Hapus ${name}`}
                          className="rounded-lg p-1.5 text-ink-muted transition-colors hover:bg-danger-soft hover:text-danger"
                        >
                          <Trash2 className="size-4" />
                        </button>
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

      <ImportDialog
        open={importOpen}
        accounts={accounts}
        onClose={() => setImportOpen(false)}
        onDone={(result) => {
          setNote(
            `${result.added} ditambahkan, ${result.updated} diperbarui, ${result.rejected} ditolak.`,
          );
          refresh();
        }}
      />

      <ConfirmDialog request={confirm.request} onClose={confirm.close} />
    </div>
  );
}

function Action({
  icon: Icon,
  children,
  onClick,
  busy = false,
}: {
  icon: typeof Upload;
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
