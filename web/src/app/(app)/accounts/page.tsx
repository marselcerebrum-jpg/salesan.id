'use client';

import {
  Building2,
  ChevronLeft,
  ChevronRight,
  HelpCircle,
  LayoutGrid,
  MessageCircle,
  Plus,
  Search,
  Smartphone,
  Wifi,
} from 'lucide-react';
import Link from 'next/link';
import { useMemo, useState } from 'react';
import useSWR from 'swr';

import { AccountCard } from '@/components/accounts/AccountCard';
import { AccountDetailModal } from '@/components/accounts/AccountDetailModal';
import { AddAccountModal } from '@/components/accounts/AddAccountModal';
import { QrModal } from '@/components/accounts/QrModal';
import { ConnectionIndicator } from '@/components/layout/AppShell';
import { Button } from '@/components/ui/Button';
import { Modal } from '@/components/ui/Modal';
import { Chip, EmptyState, ErrorNote, Spinner } from '@/components/ui/Primitives';
import {
  accountsPath,
  connectAccount,
  deleteAccount,
  disconnectAccount,
  fetcher,
  syncAccount,
} from '@/lib/api';
import { useRealtimeEvent } from '@/lib/realtime';
import type { Account, AccountStats, Application } from '@/lib/types';

type Tab = 'all' | 'qr' | 'waba';
type SortKey = 'terbaru' | 'nama' | 'status';
const UNASSIGNED = '__none__';

/** Retention promised by the UI; the backend enforces the same default. */
const SYNC_WINDOW_DAYS = 7;

export default function AccountsPage() {
  const [tab, setTab] = useState<Tab>('all');
  const [appFilter, setAppFilter] = useState<string | null>(null);
  const [search, setSearch] = useState('');

  const [addOpen, setAddOpen] = useState(false);
  const [qrAccount, setQrAccount] = useState<Account | null>(null);
  const [detailAccount, setDetailAccount] = useState<Account | null>(null);
  const [pendingDelete, setPendingDelete] = useState<Account | null>(null);
  const [busyId, setBusyId] = useState<string | null>(null);
  const [syncingId, setSyncingId] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [guide, setGuide] = useState(false);
  const [sort, setSort] = useState<SortKey>('terbaru');
  const [page, setPage] = useState(1);
  const [perPage, setPerPage] = useState(10);

  const { data: appsData, mutate: mutateApps } = useSWR<{ applications: Application[] }>(
    '/applications',
    fetcher,
  );
  const applications = useMemo(() => appsData?.applications ?? [], [appsData]);

  const accountsKey = accountsPath({
    application_id: appFilter && appFilter !== UNASSIGNED ? appFilter : undefined,
    unassigned: appFilter === UNASSIGNED,
    method: tab === 'all' ? undefined : tab,
    search: search.trim() || undefined,
  });

  const {
    data: accountsData,
    isLoading,
    mutate: mutateAccounts,
  } = useSWR<{ accounts: Account[] }>(accountsKey, fetcher, { keepPreviousData: true });

  const { data: stats, mutate: mutateStats } = useSWR<AccountStats>('/accounts/stats', fetcher);

  const accounts = useMemo(() => accountsData?.accounts ?? [], [accountsData]);

  /*
   * Order and paging, both here rather than at the server.
   *
   * The list arrives whole — it is bounded by the device limit, not by the
   * size of the workspace — so sorting it costs nothing and paging it is a
   * reading convenience rather than a way of avoiding work.
   */
  const sorted = useMemo(() => {
    const rows = [...accounts];
    if (sort === 'nama') return rows.sort((a, b) => a.name.localeCompare(b.name));
    if (sort === 'status') {
      const rank = (s: string) => (s === 'connected' ? 0 : s === 'connecting' ? 1 : 2);
      return rows.sort((a, b) => rank(a.status) - rank(b.status) || a.name.localeCompare(b.name));
    }
    return rows.sort((a, b) => (b.created_at ?? '').localeCompare(a.created_at ?? ''));
  }, [accounts, sort]);

  const pages = Math.max(1, Math.ceil(sorted.length / perPage));
  const current = Math.min(page, pages);
  const paged = sorted.slice((current - 1) * perPage, current * perPage);

  function refreshAll() {
    void mutateAccounts();
    void mutateStats();
    void mutateApps();
  }

  // Connection state changes arrive over the socket; re-read rather than trying
  // to patch the cached list in place.
  useRealtimeEvent(['account.status', 'account.deleted'], () => {
    void mutateAccounts();
    void mutateStats();
  });

  // App-state recovery finishes seconds after the sync request returned, so the
  // labels it carries would otherwise sit unseen until the next manual Sinkron.
  useRealtimeEvent<{ phase?: string; result?: { labels?: number } }>(
    'sync.progress',
    (payload) => {
      if (payload.phase !== 'app_state') return;
      refreshAll();
      const labels = payload.result?.labels ?? 0;
      if (labels > 0) {
        setNotice(`Label selesai dipulihkan dari HP: ${labels} label tersinkron.`);
        setError(null);
      }
    },
  );

  async function guard(id: string, fn: () => Promise<void>) {
    setBusyId(id);
    setError(null);
    try {
      await fn();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Aksi gagal.');
    } finally {
      setBusyId(null);
    }
  }

  const handleConnect = (account: Account) =>
    guard(account.id, async () => {
      // A never-paired account goes straight to the QR dialog; a known device
      // just reconnects.
      if (!account.jid) {
        setQrAccount(account);
        return;
      }
      await connectAccount(account.id);
      refreshAll();
    });

  const handleDisconnect = (account: Account) =>
    guard(account.id, async () => {
      await disconnectAccount(account.id);
      refreshAll();
    });

  const handleDelete = (account: Account) =>
    guard(account.id, async () => {
      await deleteAccount(account.id);
      setPendingDelete(null);
      refreshAll();
    });

  async function handleSync(account: Account) {
    setSyncingId(account.id);
    setError(null);
    setNotice(null);
    try {
      const result = await syncAccount(account.id, { days: SYNC_WINDOW_DAYS });
      const recovering = (result.app_state_recovering ?? []).length > 0;

      const parts = [`${result.contacts} kontak`, `${result.groups} grup`];
      if (result.linked > 0) parts.push(`${result.linked} chat dapat nama`);

      // Three distinct outcomes, deliberately not collapsed into one:
      //   - hard failure  -> the count is unknown, do not print it as fact
      //   - recovering    -> normal, self-healing, finishes in a few seconds
      //   - plain success -> just the number
      if (result.app_state_error) {
        parts.push('label gagal dibaca');
      } else if (recovering) {
        parts.push(
          result.labels > 0
            ? `${result.labels} label · memperbarui lewat HP`
            : 'label sedang ditarik dari HP',
        );
      } else {
        parts.push(`${result.labels} label`);
      }

      if (result.skipped_out_of_window > 0) {
        parts.push(
          `${result.skipped_out_of_window} pesan di luar ${result.window_days} hari dilewati`,
        );
      }
      if (result.history_pending) {
        parts.push('riwayat sedang ditarik dari HP');
      }

      setNotice(`${account.name}: ${parts.join(' · ')}`);

      // Only a genuine dead end deserves the red banner. A recovery in flight
      // is expected on accounts whose app-state hash is permanently broken.
      if (result.app_state_error) {
        setError(
          `Label & status baca tidak bisa dibaca dari WhatsApp: ${result.app_state_error}. ` +
            'Kontak, grup, dan riwayat chat tetap tersinkron.',
        );
      }
      refreshAll();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Sinkronisasi gagal.');
    } finally {
      setSyncingId(null);
    }
  }

  const tabs: { key: Tab; label: string; count: number }[] = [
    { key: 'all', label: 'Semua Akun', count: stats?.total ?? 0 },
    { key: 'qr', label: 'QR Scan', count: stats?.qr_accounts ?? 0 },
    { key: 'waba', label: 'WABA', count: stats?.waba_accounts ?? 0 },
  ];

  return (
    <div className="px-5 py-6 lg:px-8 lg:py-8">
      <nav aria-label="Jalur" className="flex items-center gap-1 text-2xs text-ink-muted">
        <span>Pengaturan</span>
        <ChevronRight className="size-3" aria-hidden />
        <span className="text-ink-soft">Akun WhatsApp</span>
      </nav>

      <header className="mt-1 flex flex-wrap items-start justify-between gap-4">
        <div className="min-w-0">
          <h1 className="text-2xl font-semibold tracking-[-0.02em] text-ink">Akun WhatsApp</h1>
          <p className="mt-1 text-sm text-ink-muted">
            Kelola akun WhatsApp, hubungkan perangkat, dan atur penugasan aplikasinya.
          </p>
        </div>

        <div className="flex flex-wrap items-center gap-2">
          <Button onClick={() => setGuide(true)}>
            <HelpCircle className="size-4" />
            Panduan
          </Button>
          {/* Applications live in Settings: they are the top of the access
              hierarchy, and creating one is the same kind of decision as
              creating an account or handing out a role. */}
          <Link
            href="/pengaturan#aplikasi"
            className="inline-flex h-10 items-center justify-center gap-2 rounded-control border border-hairline bg-surface-raised px-4 text-sm font-medium text-ink transition-colors hover:bg-surface-sunken"
          >
            <LayoutGrid className="size-4" />
            Kelola Aplikasi
          </Link>
          <Button
            variant="primary"
            icon={<Plus className="size-4" />}
            onClick={() => setAddOpen(true)}
          >
            Tambah Akun
          </Button>
        </div>
      </header>

      {/* The four numbers that decide what somebody can do next: how many
          accounts exist, how much of each connection quota is left, and how
          many are actually live right now. */}
      <div className="mt-5 grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
        <StatCard
          icon={MessageCircle}
          tone="brand"
          value={String(stats?.total ?? 0)}
          label="Total akun"
        />
        <StatCard
          icon={Smartphone}
          tone="info"
          value={`${stats?.qr_accounts ?? 0} / ${stats?.device_limit ?? 10}`}
          label="Perangkat aktif"
          ratio={(stats?.qr_accounts ?? 0) / Math.max(1, stats?.device_limit ?? 10)}
        />
        <StatCard
          icon={Building2}
          tone="iris"
          value={`${stats?.waba_accounts ?? 0} / ${stats?.waba_limit ?? 5}`}
          label="Akun WABA"
          ratio={(stats?.waba_accounts ?? 0) / Math.max(1, stats?.waba_limit ?? 5)}
        />
        <StatCard
          icon={Wifi}
          tone="good"
          value={String(stats?.connected ?? 0)}
          label="Terhubung"
          trailing={<ConnectionIndicator />}
        />
      </div>

      <nav className="mt-6 flex gap-6 border-b border-hairline" aria-label="Metode koneksi">
        {tabs.map((item) => {
          const active = tab === item.key;
          return (
            <button
              key={item.key}
              type="button"
              onClick={() => setTab(item.key)}
              aria-current={active ? 'page' : undefined}
              className={`-mb-px flex items-center gap-2 border-b-2 px-1 pb-3 text-base transition-colors ${
                active
                  ? 'border-brand-800 font-semibold text-ink'
                  : 'border-transparent text-ink-muted hover:text-ink'
              }`}
            >
              {item.label}
              <span className={active ? 'text-ink-soft' : 'text-ink-muted'}>{item.count}</span>
            </button>
          );
        })}
      </nav>

      <div className="mt-4 flex flex-wrap items-center gap-2">
        <Chip active={appFilter === null} onClick={() => setAppFilter(null)}>
          Semua
          <span className="rounded-full bg-hairline/60 px-1.5 text-2xs">{stats?.total ?? 0}</span>
        </Chip>

        {applications.map((app) => (
          <Chip
            key={app.id}
            active={appFilter === app.id}
            onClick={() => setAppFilter(appFilter === app.id ? null : app.id)}
          >
            <span
              className="size-2 rounded-full"
              style={{ backgroundColor: app.color }}
              aria-hidden
            />
            {app.name}
            <span className="rounded-full bg-hairline/60 px-1.5 text-2xs">{app.account_count}</span>
          </Chip>
        ))}

        <Chip
          active={appFilter === UNASSIGNED}
          onClick={() => setAppFilter(appFilter === UNASSIGNED ? null : UNASSIGNED)}
        >
          Tanpa aplikasi
          <span className="rounded-full bg-hairline/60 px-1.5 text-2xs">
            {stats?.unassigned ?? 0}
          </span>
        </Chip>

        <div className="ml-auto flex items-center gap-2">
          <label className="relative">
            <Search className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-ink-muted" />
            <input
              value={search}
              onChange={(event) => setSearch(event.target.value)}
              placeholder="Cari akun / nomor…"
              aria-label="Cari akun"
              className="h-9 w-56 rounded-full border border-hairline bg-surface-raised pr-3 pl-9 text-sm outline-none placeholder:text-ink-muted/70 focus:border-brand-700 focus:ring-2 focus:ring-brand-700/15"
            />
          </label>

          <label className="flex items-center gap-1.5 text-xs text-ink-muted">
            Urutkan
            <select
              value={sort}
              onChange={(e) => setSort(e.target.value as SortKey)}
              className="h-9 rounded-control border border-hairline bg-surface-raised px-2 text-xs text-ink-soft outline-none focus:border-brand-600"
            >
              <option value="terbaru">Terbaru</option>
              <option value="nama">Nama</option>
              <option value="status">Terhubung dulu</option>
            </select>
          </label>
        </div>
      </div>

      {error ? (
        <div className="mt-4">
          <ErrorNote message={error} />
        </div>
      ) : null}

      {notice ? (
        <p className="mt-4 rounded-lg border border-brand-600/20 bg-brand-600/10 px-3 py-2 text-sm text-brand-700">
          {notice}
        </p>
      ) : null}

      {isLoading && accounts.length === 0 ? (
        <Spinner label="Memuat akun…" />
      ) : accounts.length === 0 ? (
        <EmptyState
          title="Belum ada akun di filter ini"
          description="Tambahkan akun WhatsApp lalu pindai QR untuk mulai menerima pesan."
          action={
            <Button
              variant="primary"
              icon={<Plus className="size-4" />}
              onClick={() => setAddOpen(true)}
            >
              Tambah Akun
            </Button>
          }
        />
      ) : (
        <>
          <div className="mt-5 grid gap-4 xl:grid-cols-2">
            {paged.map((account) => (
              <AccountCard
                key={account.id}
                account={account}
                busy={busyId === account.id}
                syncing={syncingId === account.id}
                onConnect={handleConnect}
                onDisconnect={handleDisconnect}
                onSync={handleSync}
                onDetail={setDetailAccount}
                onDelete={setPendingDelete}
              />
            ))}

            {/* The empty half of the last row, used for the one thing somebody
                would want next. An odd number of accounts leaves a gap there
                anyway; this makes the gap say something. */}
            {paged.length % 2 === 1 ? (
              <button
                type="button"
                onClick={() => setAddOpen(true)}
                className="flex min-h-[220px] flex-col items-center justify-center gap-2 rounded-card border border-dashed border-hairline-strong px-6 py-8 text-center transition-colors hover:bg-surface-sunken/40"
              >
                <span className="grid size-12 place-items-center rounded-full bg-surface-sunken text-ink-muted">
                  <MessageCircle className="size-5" />
                </span>
                <span className="text-sm font-medium text-ink">Belum ada akun lainnya</span>
                <span className="max-w-[260px] text-xs text-ink-muted">
                  Tambahkan akun WhatsApp untuk mulai terhubung dengan pelanggan Anda.
                </span>
                <span className="mt-1 inline-flex items-center gap-1.5 rounded-control bg-brand-800 px-3 py-1.5 text-xs font-medium text-white">
                  <Plus className="size-3.5" />
                  Tambah Akun
                </span>
              </button>
            ) : null}
          </div>

          <div className="mt-5 flex flex-wrap items-center justify-between gap-3 text-xs text-ink-muted">
            <p>
              Menampilkan <span className="nums text-ink-soft">{paged.length}</span> dari{' '}
              <span className="nums text-ink-soft">{sorted.length}</span> akun
            </p>

            <div className="flex items-center gap-2">
              {pages > 1 ? (
                <div className="flex items-center gap-1">
                  <PageButton
                    label="Sebelumnya"
                    disabled={page === 1}
                    onClick={() => setPage(page - 1)}
                  >
                    <ChevronLeft className="size-4" />
                  </PageButton>
                  {Array.from({ length: pages }, (_, i) => i + 1).map((n) => (
                    <button
                      key={n}
                      type="button"
                      onClick={() => setPage(n)}
                      aria-current={n === page ? 'page' : undefined}
                      className={
                        n === page
                          ? 'nums size-8 rounded-control bg-brand-600/12 text-xs font-semibold text-brand-700'
                          : 'nums size-8 rounded-control text-xs text-ink-soft transition-colors hover:bg-surface-sunken'
                      }
                    >
                      {n}
                    </button>
                  ))}
                  <PageButton
                    label="Berikutnya"
                    disabled={page === pages}
                    onClick={() => setPage(page + 1)}
                  >
                    <ChevronRight className="size-4" />
                  </PageButton>
                </div>
              ) : null}

              <select
                value={perPage}
                onChange={(e) => {
                  setPerPage(Number(e.target.value));
                  setPage(1);
                }}
                aria-label="Akun per halaman"
                className="h-9 rounded-control border border-hairline bg-surface-raised px-2 text-xs text-ink-soft outline-none focus:border-brand-600"
              >
                {[10, 25, 50].map((n) => (
                  <option key={n} value={n}>
                    {n} / halaman
                  </option>
                ))}
              </select>
            </div>
          </div>
        </>
      )}

      <AddAccountModal
        open={addOpen}
        onClose={() => setAddOpen(false)}
        applications={applications}
        onCreated={(account) => {
          setAddOpen(false);
          refreshAll();
          setQrAccount(account);
        }}
      />

      <QrModal
        account={qrAccount}
        onClose={() => {
          setQrAccount(null);
          refreshAll();
        }}
        onLinked={() => refreshAll()}
      />

      <AccountDetailModal
        account={detailAccount}
        applications={applications}
        onClose={() => setDetailAccount(null)}
        onChanged={refreshAll}
      />

      <Modal
        open={Boolean(pendingDelete)}
        onClose={() => setPendingDelete(null)}
        title="Hapus akun ini?"
        size="sm"
        footer={
          <>
            <Button onClick={() => setPendingDelete(null)}>Batal</Button>
            <Button
              variant="danger"
              loading={busyId === pendingDelete?.id}
              onClick={() => pendingDelete && handleDelete(pendingDelete)}
            >
              Hapus permanen
            </Button>
          </>
        }
      >
        <p className="text-sm leading-relaxed text-ink-soft">
          <strong className="text-ink">{pendingDelete?.name}</strong> akan dilepas dari WhatsApp dan
          dihapus beserta seluruh kontak, percakapan, dan pesannya. Tindakan ini tidak bisa
          dibatalkan.
        </p>
      </Modal>

      <Modal open={guide} onClose={() => setGuide(false)} title="Panduan Akun WhatsApp" size="md">
        <div className="space-y-3 text-sm leading-relaxed text-ink-soft">
          <p>
            <span className="font-medium text-ink">QR Scan</span> menautkan nomor WhatsApp biasa
            dengan memindai kode dari HP, sama seperti WhatsApp Web. Nomor itu harus tetap aktif di
            HP-nya.
          </p>
          <p>
            <span className="font-medium text-ink">WABA</span> adalah akun WhatsApp Business resmi
            lewat penyedia API. Keduanya punya kuota terpisah, dan itulah dua angka di kartu atas.
          </p>
          <p>
            <span className="font-medium text-ink">Sinkron</span> menarik kontak, grup, label, status
            baca, dan riwayat chat {SYNC_WINDOW_DAYS} hari terakhir dari HP. Pesan yang lebih lama
            dari itu tidak ditarik.
          </p>
          <p>
            <span className="font-medium text-ink">Putuskan</span> hanya melepas sesi; datanya tetap
            ada dan nomor bisa dihubungkan lagi. <span className="font-medium text-ink">Hapus</span>{' '}
            menghilangkan akun beserta seluruh percakapannya, dan itu tidak bisa dibatalkan.
          </p>
        </div>
      </Modal>
    </div>
  );
}

/* --- pieces -------------------------------------------------------------- */

const TILES = {
  brand: 'bg-brand-600/12 text-brand-700',
  info: 'bg-info-soft text-info',
  iris: 'bg-iris-soft text-iris',
  good: 'bg-brand-600/12 text-brand-700',
} as const;

/**
 * One reading, with its quota drawn underneath when it has one.
 *
 * The bar is the part that matters: "1 / 10" is a fraction somebody has to do
 * arithmetic on, and a bar says how close to full it is before it is read.
 */
function StatCard({
  icon: Icon,
  tone,
  value,
  label,
  ratio,
  trailing,
}: {
  icon: typeof Smartphone;
  tone: keyof typeof TILES;
  value: string;
  label: string;
  /** 0 to 1. Draws the quota bar; omitted when the figure has no ceiling. */
  ratio?: number;
  trailing?: React.ReactNode;
}) {
  const pct = Math.round(Math.min(1, Math.max(0, ratio ?? 0)) * 100);
  return (
    <div className="rounded-card border border-hairline bg-surface-raised px-4 py-3.5 shadow-e1">
      <div className="flex items-center gap-3">
        <span
          className={`flex size-11 shrink-0 items-center justify-center rounded-control ${TILES[tone]}`}
        >
          <Icon className="size-5" aria-hidden />
        </span>
        <div className="min-w-0 flex-1">
          <p className="nums text-xl leading-tight font-semibold text-ink">{value}</p>
          <p className="truncate text-xs text-ink-muted">{label}</p>
        </div>
        {trailing}
      </div>

      {ratio === undefined ? null : (
        <div className="mt-2.5 flex items-center gap-2">
          <span className="h-1.5 min-w-0 flex-1 overflow-hidden rounded-full bg-surface-sunken">
            <span
              aria-hidden
              className="block h-full rounded-full bg-brand-600 transition-[width]"
              style={{ width: `${pct}%` }}
            />
          </span>
          <span className="nums shrink-0 text-2xs text-ink-muted">{pct}%</span>
        </div>
      )}
    </div>
  );
}

function PageButton({
  label,
  disabled,
  onClick,
  children,
}: {
  label: string;
  disabled: boolean;
  onClick: () => void;
  children: React.ReactNode;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled}
      aria-label={label}
      className="grid size-8 place-items-center rounded-control text-ink-muted transition-colors hover:bg-surface-sunken hover:text-ink disabled:opacity-40 disabled:hover:bg-transparent"
    >
      {children}
    </button>
  );
}
