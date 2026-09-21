'use client';

import clsx from 'clsx';
import {
  ArrowLeft,
  Copy,
  FileSpreadsheet,
  FileText,
  Loader2,
  RefreshCw,
  Search,
  ShieldCheck,
} from 'lucide-react';
import Link from 'next/link';
import { useParams } from 'next/navigation';
import { useMemo, useState } from 'react';
import useSWR from 'swr';

import { EmptyState, ErrorNote, Spinner } from '@/components/ui/Primitives';
import {
  exportGroupMembers,
  fetcher,
  groupMembersPath,
  refreshDirectoryGroup,
  type GroupDetail,
} from '@/lib/api';

/**
 * One group's participants, on its own page.
 *
 * A page rather than a panel inside the directory row: a thousand members is
 * not something to read through a gap between two table rows, and it needs its
 * own search, its own export and a URL somebody can send to a colleague.
 *
 * The list is merged across every one of our numbers inside the group. A group
 * has one membership list, and which of our phones happened to read it is not a
 * fact about the members.
 */
export default function GroupDetailPage() {
  const params = useParams<{ chatJid: string }>();
  // Next decodes the segment, so this is the JID as stored, '@g.us' and all.
  const chatJid = decodeURIComponent(params.chatJid);

  const [search, setSearch] = useState('');
  const [busy, setBusy] = useState<null | 'copy' | 'csv' | 'xlsx' | 'refresh'>(null);
  const [note, setNote] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  const { data, isLoading, error: loadError, mutate } = useSWR<GroupDetail>(
    groupMembersPath(chatJid),
    fetcher,
    { revalidateOnFocus: false },
  );

  const group = data?.group;
  const members = useMemo(() => data?.members ?? [], [data]);

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

  const admins = useMemo(() => members.filter((m) => m.is_admin).length, [members]);

  async function copyAll() {
    if (shown.length === 0) return;
    setBusy('copy');
    setError(null);
    setNote(null);
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
      // The server exports the whole membership, not the search: a file named
      // after the group should hold the group.
      await exportGroupMembers(chatJid, format);
      setNote(`${members.length.toLocaleString('id-ID')} anggota diekspor.`);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Ekspor gagal.');
    } finally {
      setBusy(null);
    }
  }

  async function refresh() {
    setBusy('refresh');
    setError(null);
    setNote(null);
    try {
      const fresh = await refreshDirectoryGroup(chatJid);
      await mutate(fresh, { revalidate: false });
      setNote(`${fresh.members.length.toLocaleString('id-ID')} anggota diperbarui.`);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Gagal memperbarui grup.');
    } finally {
      setBusy(null);
    }
  }

  return (
    <div className="px-5 py-6 lg:px-8 lg:py-8">
      <Link
        href="/groups"
        className="inline-flex items-center gap-1.5 text-sm text-ink-muted transition-colors hover:text-ink-soft"
      >
        <ArrowLeft className="size-4" />
        Semua grup
      </Link>

      <header className="mt-3 flex flex-wrap items-start justify-between gap-4">
        <div className="min-w-0">
          <h1 className="text-2xl font-semibold tracking-[-0.02em] text-ink">
            {group?.name || (isLoading ? 'Memuat…' : chatJid)}
          </h1>
          <p className="mt-1 text-sm text-ink-muted">
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
              tekan &ldquo;Perbarui&rdquo; untuk menarik ulang.
            </p>
          ) : null}
        </div>

        <div className="flex flex-wrap items-center gap-2">
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
          <button
            type="button"
            onClick={() => void refresh()}
            disabled={busy === 'refresh'}
            className="inline-flex h-9 items-center gap-1.5 rounded-control bg-brand-700 px-3.5 text-sm font-medium text-white transition-colors hover:bg-brand-800 disabled:opacity-60"
          >
            {busy === 'refresh' ? (
              <Loader2 className="size-4 animate-spin" />
            ) : (
              <RefreshCw className="size-4" />
            )}
            Perbarui
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

      <div className="mt-4 rounded-card border border-hairline bg-surface-raised shadow-e1">
        <div className="flex flex-wrap items-center gap-3 border-b border-hairline px-4 py-3">
          <label className="relative min-w-0 flex-1">
            <Search className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-ink-muted" />
            <input
              value={search}
              onChange={(event) => setSearch(event.target.value)}
              placeholder="Cari nama/nomor…"
              aria-label="Cari anggota"
              className="h-9 w-full rounded-control bg-surface-sunken/60 pr-3 pl-9 text-sm text-ink outline-none placeholder:text-ink-muted focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-brand-600"
            />
          </label>
          {search.trim() ? (
            <p className="shrink-0 text-xs text-ink-muted">
              <span className="nums font-medium text-ink-soft">
                {shown.length.toLocaleString('id-ID')}
              </span>{' '}
              dari <span className="nums">{members.length.toLocaleString('id-ID')}</span>
            </p>
          ) : null}
        </div>

        {isLoading ? (
          <div className="p-6">
            <Spinner label="Memuat anggota…" />
          </div>
        ) : loadError ? (
          <div className="p-6">
            <ErrorNote
              message={
                loadError instanceof Error ? loadError.message : 'Gagal memuat anggota grup.'
              }
            />
          </div>
        ) : members.length === 0 ? (
          <div className="p-6">
            <EmptyState
              title="Anggota belum diambil"
              description={'Daftar anggota grup ini belum pernah ditarik dari WhatsApp. Tekan "Perbarui" di atas.'}
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
          <div className="max-h-[70vh] overflow-auto">
            {/* Scrolls in its own box so the header can stay put. */}
            <table className="w-full min-w-[520px] border-collapse text-sm">
              <thead>
                <tr className="border-b border-hairline text-ink-muted">
                  <Th className="w-14">No</Th>
                  <Th>Nama</Th>
                  <Th className="w-56">Nomor</Th>
                </tr>
              </thead>
              <tbody>
                {shown.map((member, index) => (
                  <tr
                    key={member.jid}
                    className="h-[52px] border-b border-hairline transition-colors last:border-0 hover:bg-surface-sunken/50"
                  >
                    <Td className="nums text-ink-muted">{index + 1}</Td>
                    <Td>
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
                    </Td>
                    <Td>
                      {/* Empty when we cannot resolve a real number. WhatsApp's
                          masked placeholder shows a few real digits and hides
                          the rest, which reads as a number without being one. */}
                      {member.phone_number ? (
                        <span className="nums text-ink-soft">+{member.phone_number}</span>
                      ) : (
                        <span className="text-ink-muted">–</span>
                      )}
                    </Td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
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
