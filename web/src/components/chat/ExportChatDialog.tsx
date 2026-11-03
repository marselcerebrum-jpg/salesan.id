'use client';

import clsx from 'clsx';
import { Check, Download, Info, Search } from 'lucide-react';
import { useMemo, useRef, useState } from 'react';
import useSWR from 'swr';

import { Button } from '@/components/ui/Button';
import { Modal } from '@/components/ui/Modal';
import { Chip, ErrorNote } from '@/components/ui/Primitives';
import { inputClass, labelClass } from '@/components/ui/control';
import { ApiError, accountsPath, exportChats, fetcher, type ChatExportQuery } from '@/lib/api';
import type { Account, Application } from '@/lib/types';

/**
 * The export dialog on the Chat page: which application, which of its numbers,
 * which WIB dates, which kind of chat — then one download.
 *
 * Every filter is applied by the server. The dialog only collects them and
 * refuses the combinations the server would refuse anyway, so the reader hears
 * about a backwards date range before waiting on a request, not after.
 */

const TYPES: { id: ChatExportQuery['type']; label: string }[] = [
  { id: 'all', label: 'Semua Percakapan' },
  { id: 'personal', label: 'Chat Pribadi' },
  { id: 'group', label: 'Chat Grup' },
];

/** The server's bound on one export, repeated so the dialog can say it first. */
const MAX_DAYS = 366;

/** A calendar date in WIB, as YYYY-MM-DD, whatever zone the browser is in. */
function wibDate(at: Date): string {
  return new Intl.DateTimeFormat('en-CA', {
    timeZone: 'Asia/Jakarta',
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
  }).format(at);
}

/** Days from a to b, both YYYY-MM-DD. Read as UTC so no zone shifts either. */
function daysBetween(a: string, b: string): number {
  return Math.round((Date.parse(`${b}T00:00:00Z`) - Date.parse(`${a}T00:00:00Z`)) / 86_400_000);
}

export interface ExportResult {
  filename: string;
  rows: number | null;
}

export function ExportChatDialog({
  open,
  onClose,
  applications,
  onExported,
}: {
  open: boolean;
  onClose: () => void;
  applications: Application[];
  onExported: (result: ExportResult) => void;
}) {
  const [applicationId, setApplicationId] = useState('');
  const [selected, setSelected] = useState<string[]>([]);
  const [search, setSearch] = useState('');
  const [start, setStart] = useState(() => wibDate(new Date(Date.now() - 6 * 86_400_000)));
  const [end, setEnd] = useState(() => wibDate(new Date()));
  const [type, setType] = useState<ChatExportQuery['type']>('all');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [empty, setEmpty] = useState<string | null>(null);
  // A ref as well as state: two clicks inside one render would both read
  // busy as false, and the second would start a second export.
  const inFlight = useRef(false);

  const { data, isLoading } = useSWR<{ accounts: Account[] }>(
    open ? accountsPath({ application_id: applicationId || undefined }) : null,
    fetcher,
  );
  const accounts = useMemo(() => data?.accounts ?? [], [data]);

  // Only numbers still on the list count. Switching application empties the
  // selection outright, but this also covers a number removed meanwhile.
  const picked = useMemo(
    () => selected.filter((id) => accounts.some((a) => a.id === id)),
    [selected, accounts],
  );

  const visible = useMemo(() => {
    const q = search.trim().toLowerCase();
    if (!q) return accounts;
    return accounts.filter((a) =>
      [a.name, a.label, a.phone_number, a.application_name]
        .filter(Boolean)
        .some((v) => String(v).toLowerCase().includes(q)),
    );
  }, [accounts, search]);

  const today = wibDate(new Date());
  let invalid: string | null = null;
  if (!start || !end) invalid = 'Isi tanggal mulai dan tanggal akhir.';
  else if (start > end) invalid = 'Tanggal mulai tidak boleh melebihi tanggal akhir.';
  else if (daysBetween(start, end) >= MAX_DAYS)
    invalid = `Rentang tanggal paling panjang ${MAX_DAYS} hari.`;

  function reset() {
    setError(null);
    setEmpty(null);
  }

  function toggle(id: string) {
    reset();
    setSelected((prev) => (prev.includes(id) ? prev.filter((x) => x !== id) : [...prev, id]));
  }

  async function download() {
    if (inFlight.current || invalid) return;
    inFlight.current = true;
    setBusy(true);
    reset();
    try {
      const result = await exportChats({
        application_id: applicationId || undefined,
        account_ids: picked,
        start_date: start,
        end_date: end,
        type,
      });
      onExported(result);
      onClose();
    } catch (err) {
      if (err instanceof ApiError && err.code === 'no_data') {
        setEmpty(err.message);
      } else {
        setError(err instanceof Error ? err.message : 'Ekspor gagal.');
      }
    } finally {
      inFlight.current = false;
      setBusy(false);
    }
  }

  return (
    <Modal
      open={open}
      onClose={() => {
        // Closing mid-export would hide the result of a request still running.
        if (!busy) onClose();
      }}
      title="Export Data Chat"
      size="lg"
      footer={
        <>
          <Button onClick={onClose} disabled={busy}>
            Batal
          </Button>
          <Button
            variant="primary"
            icon={<Download className="size-4" />}
            loading={busy}
            disabled={invalid !== null}
            onClick={() => void download()}
          >
            {busy ? 'Menyiapkan file…' : 'Download Excel'}
          </Button>
        </>
      }
    >
      <div className="space-y-5">
        <p className="text-sm text-ink-muted">
          Setiap pesan masuk dan keluar menjadi satu baris pada file Excel (.xlsx). Waktu memakai WIB.
        </p>

        <div>
          <label htmlFor="export-app" className={labelClass}>
            Aplikasi
          </label>
          <select
            id="export-app"
            className={inputClass}
            value={applicationId}
            disabled={busy}
            onChange={(e) => {
              reset();
              setApplicationId(e.target.value);
              setSelected([]);
              setSearch('');
            }}
          >
            <option value="">Semua Aplikasi</option>
            {applications.map((app) => (
              <option key={app.id} value={app.id}>
                {app.name}
              </option>
            ))}
          </select>
        </div>

        <fieldset disabled={busy}>
          <div className="mb-1.5 flex items-baseline justify-between gap-3">
            <legend className="text-xs font-medium text-ink-soft">Nomor WhatsApp Bisnis</legend>
            <span className="text-2xs text-ink-muted">
              {picked.length === 0 ? 'Semua nomor' : `${picked.length} nomor dipilih`}
            </span>
          </div>

          {accounts.length > 6 ? (
            <div className="relative mb-2">
              <Search
                className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-ink-muted"
                aria-hidden
              />
              <input
                type="search"
                value={search}
                onChange={(e) => setSearch(e.target.value)}
                placeholder="Cari nama atau nomor"
                aria-label="Cari nomor WhatsApp"
                className={clsx(inputClass, 'pl-9')}
              />
            </div>
          ) : null}

          <div className="max-h-56 overflow-y-auto rounded-control border border-hairline p-1 scrollbar-slim">
            <CheckRow
              on={picked.length === 0}
              onClick={() => {
                reset();
                setSelected([]);
              }}
              title="Semua nomor"
              detail={
                applicationId
                  ? 'Seluruh nomor pada aplikasi ini'
                  : 'Seluruh nomor yang dapat Anda akses'
              }
            />
            {isLoading ? (
              <p className="px-3 py-2.5 text-sm text-ink-muted">Memuat nomor…</p>
            ) : accounts.length === 0 ? (
              <p className="px-3 py-2.5 text-sm text-ink-muted">Belum ada nomor pada aplikasi ini.</p>
            ) : visible.length === 0 ? (
              <p className="px-3 py-2.5 text-sm text-ink-muted">Tidak ada nomor yang cocok.</p>
            ) : (
              visible.map((a) => (
                <CheckRow
                  key={a.id}
                  on={picked.includes(a.id)}
                  onClick={() => toggle(a.id)}
                  title={a.label ? `${a.name} · ${a.label}` : a.name}
                  detail={[a.phone_number ?? 'Belum tertaut', applicationId ? null : a.application_name]
                    .filter(Boolean)
                    .join(' · ')}
                />
              ))
            )}
          </div>
        </fieldset>

        <div className="grid gap-4 sm:grid-cols-2">
          <div>
            <label htmlFor="export-start" className={labelClass}>
              Tanggal Mulai
            </label>
            <input
              id="export-start"
              type="date"
              className={inputClass}
              value={start}
              max={end || today}
              disabled={busy}
              onChange={(e) => {
                reset();
                setStart(e.target.value);
              }}
            />
          </div>
          <div>
            <label htmlFor="export-end" className={labelClass}>
              Tanggal Akhir
            </label>
            <input
              id="export-end"
              type="date"
              className={inputClass}
              value={end}
              min={start || undefined}
              disabled={busy}
              onChange={(e) => {
                reset();
                setEnd(e.target.value);
              }}
            />
          </div>
        </div>

        <div>
          <p className={labelClass}>Jenis Percakapan</p>
          <div className="flex flex-wrap gap-2" role="group" aria-label="Jenis percakapan">
            {TYPES.map((t) => (
              <Chip
                key={t.id}
                active={type === t.id}
                onClick={() => {
                  if (busy) return;
                  reset();
                  setType(t.id);
                }}
              >
                {t.label}
              </Chip>
            ))}
          </div>
        </div>

        {invalid ? <p className="text-sm text-danger">{invalid}</p> : null}
        {empty ? (
          <p
            role="status"
            className="flex items-start gap-2 rounded-control border border-info/20 bg-info-soft px-3 py-2 text-sm text-info"
          >
            <Info className="mt-0.5 size-4 shrink-0" aria-hidden />
            {empty}
          </p>
        ) : null}
        {error ? <ErrorNote message={error} /> : null}
        {busy ? (
          <p className="text-xs text-ink-muted" aria-live="polite">
            Periode panjang bisa memakan waktu beberapa menit. Biarkan jendela ini terbuka.
          </p>
        ) : null}
      </div>
    </Modal>
  );
}

function CheckRow({
  on,
  onClick,
  title,
  detail,
}: {
  on: boolean;
  onClick: () => void;
  title: string;
  detail?: string;
}) {
  return (
    <button
      type="button"
      role="checkbox"
      aria-checked={on}
      onClick={onClick}
      className={clsx(
        'flex w-full items-center gap-3 rounded-lg px-3 py-2 text-left transition-colors',
        'disabled:cursor-not-allowed disabled:opacity-60',
        on ? 'bg-brand-600/10' : 'hover:bg-surface-sunken',
      )}
    >
      <span
        className={clsx(
          'grid size-5 shrink-0 place-items-center rounded-md border transition-colors',
          on ? 'border-brand-800 bg-brand-800 text-white' : 'border-hairline',
        )}
      >
        {on ? <Check className="size-3.5" strokeWidth={3} /> : null}
      </span>
      <span className="min-w-0 flex-1">
        <span className="block truncate text-sm text-ink">{title}</span>
        {detail ? <span className="nums block truncate text-xs text-ink-muted">{detail}</span> : null}
      </span>
    </button>
  );
}
