'use client';

import clsx from 'clsx';
import { Loader2, Upload, X } from 'lucide-react';
import { useRef, useState } from 'react';

import { ErrorNote } from '@/components/ui/Primitives';
import { inputClassSm } from '@/components/ui/control';
import { importContacts } from '@/lib/api';
import type { Account, ImportResult } from '@/lib/types';

/**
 * Uploading a CSV into one WhatsApp number's address book.
 *
 * The destination is asked for here rather than read from the file. A contact
 * belongs to the number that will message it, and a spreadsheet has no way of
 * knowing which of ours that is; guessing would put a thousand numbers on the
 * wrong brand and nobody would notice until a broadcast went out.
 */
export function ImportDialog({
  open,
  accounts,
  onClose,
  onDone,
}: {
  open: boolean;
  accounts: Account[];
  onClose: () => void;
  /** Called after a successful import so the page can refresh its counts. */
  onDone: (result: ImportResult) => void;
}) {
  const [accountId, setAccountId] = useState('');
  const [file, setFile] = useState<File | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [result, setResult] = useState<ImportResult | null>(null);
  const fileRef = useRef<HTMLInputElement>(null);

  if (!open) return null;

  async function submit() {
    if (!accountId || !file || busy) return;
    setBusy(true);
    setError(null);
    try {
      const out = await importContacts(accountId, file);
      setResult(out);
      onDone(out);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Import gagal.');
    } finally {
      setBusy(false);
    }
  }

  function reset() {
    setFile(null);
    setResult(null);
    setError(null);
    if (fileRef.current) fileRef.current.value = '';
  }

  return (
    <div
      className="fixed inset-0 z-[70] flex items-center justify-center bg-ink/45 p-4"
      onMouseDown={(event) => {
        if (event.target === event.currentTarget && !busy) onClose();
      }}
    >
      <div
        role="dialog"
        aria-modal="true"
        aria-label="Import kontak"
        className="w-full max-w-[460px] rounded-card border border-hairline bg-surface-raised shadow-e4"
      >
        <header className="flex items-start gap-3 px-5 pt-5">
          <span className="grid size-10 shrink-0 place-items-center rounded-full bg-brand-600/10">
            <Upload className="size-5 text-brand-700" />
          </span>
          <div className="min-w-0 flex-1">
            <h2 className="text-lg font-semibold text-ink">Import kontak</h2>
            <p className="mt-0.5 text-sm text-ink-muted">
              Berkas CSV dengan kolom nomor, dan nama kalau ada.
            </p>
          </div>
          <button
            type="button"
            onClick={onClose}
            disabled={busy}
            aria-label="Tutup"
            className="rounded-lg p-1.5 text-ink-muted transition-colors hover:bg-surface-sunken disabled:opacity-50"
          >
            <X className="size-5" />
          </button>
        </header>

        <div className="px-5 pt-4">
          {result ? (
            <div className="rounded-control border border-hairline bg-surface-sunken/60 p-3.5">
              <p className="text-sm font-medium text-ink">Selesai.</p>
              <ul className="mt-2 space-y-1 text-sm text-ink-soft">
                <li>
                  <span className="nums font-semibold text-ink">{result.added}</span> ditambahkan
                </li>
                <li>
                  <span className="nums font-semibold text-ink">{result.updated}</span> sudah ada,
                  namanya diperbarui
                </li>
                <li>
                  <span className="nums font-semibold text-ink">{result.rejected}</span> ditolak
                </li>
              </ul>
              {result.reasons.length > 0 ? (
                <div className="mt-2.5 border-t border-hairline pt-2.5">
                  <p className="text-2xs font-medium text-ink-muted">Contoh yang ditolak</p>
                  <ul className="mt-1 space-y-0.5 text-xs text-ink-muted">
                    {result.reasons.map((reason) => (
                      <li key={reason}>{reason}</li>
                    ))}
                  </ul>
                </div>
              ) : null}
            </div>
          ) : (
            <>
              <label className="block">
                <span className="mb-1.5 block text-xs font-medium text-ink-soft">
                  Masuk ke nomor WhatsApp
                </span>
                <select
                  value={accountId}
                  onChange={(event) => setAccountId(event.target.value)}
                  className={clsx(inputClassSm, 'w-full')}
                >
                  <option value="">Pilih nomor…</option>
                  {accounts.map((account) => (
                    <option key={account.id} value={account.id}>
                      {account.name}
                      {account.phone_number ? ` · ${account.phone_number}` : ''}
                    </option>
                  ))}
                </select>
              </label>

              <label className="mt-3 block">
                <span className="mb-1.5 block text-xs font-medium text-ink-soft">Berkas CSV</span>
                <input
                  ref={fileRef}
                  type="file"
                  accept=".csv,text/csv"
                  onChange={(event) => setFile(event.target.files?.[0] ?? null)}
                  className="block w-full text-sm text-ink-soft file:mr-3 file:rounded-control file:border-0 file:bg-brand-600/10 file:px-3 file:py-2 file:text-sm file:font-medium file:text-brand-700"
                />
              </label>

              <p className="mt-2.5 text-xs leading-relaxed text-ink-muted">
                Nomor yang sudah ada tidak diduplikasi, hanya namanya yang dilengkapi. Format
                nomor bebas: 0812…, +62 812…, dan 62812… dibaca sebagai nomor yang sama.
              </p>
            </>
          )}

          {error ? (
            <div className="mt-3">
              <ErrorNote message={error} />
            </div>
          ) : null}
        </div>

        <footer className="flex items-center justify-end gap-2 px-5 pt-4 pb-5">
          {result ? (
            <>
              <button
                type="button"
                onClick={reset}
                className="h-9 rounded-control px-3 text-sm font-medium text-ink-muted transition-colors hover:bg-surface-sunken"
              >
                Import lagi
              </button>
              <button
                type="button"
                onClick={onClose}
                className="h-9 rounded-control bg-brand-700 px-3.5 text-sm font-medium text-white transition-colors hover:bg-brand-800"
              >
                Tutup
              </button>
            </>
          ) : (
            <>
              <button
                type="button"
                onClick={onClose}
                disabled={busy}
                className="h-9 rounded-control px-3 text-sm font-medium text-ink-muted transition-colors hover:bg-surface-sunken disabled:opacity-50"
              >
                Batal
              </button>
              <button
                type="button"
                onClick={() => void submit()}
                disabled={!accountId || !file || busy}
                className="inline-flex h-9 items-center gap-1.5 rounded-control bg-brand-700 px-3.5 text-sm font-medium text-white transition-colors hover:bg-brand-800 disabled:opacity-50"
              >
                {busy ? <Loader2 className="size-4 animate-spin" /> : <Upload className="size-4" />}
                Import
              </button>
            </>
          )}
        </footer>
      </div>
    </div>
  );
}
