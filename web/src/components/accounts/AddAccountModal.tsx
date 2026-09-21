'use client';

import { useState } from 'react';

import { Button } from '@/components/ui/Button';
import { Modal } from '@/components/ui/Modal';
import { ErrorNote } from '@/components/ui/Primitives';
import { createAccount } from '@/lib/api';
import type { Account, Application } from '@/lib/types';

interface AddAccountModalProps {
  open: boolean;
  onClose: () => void;
  applications: Application[];
  /** Called with the freshly created account so the caller can open the QR modal. */
  onCreated: (account: Account) => void;
}

/**
 * Reference screen 2 — optional account name plus the application it should be
 * classified under. Creating the row is the first half of the linking flow; the
 * caller opens the QR modal with the returned account.
 */
export function AddAccountModal({ open, onClose, applications, onCreated }: AddAccountModalProps) {
  const [name, setName] = useState('');
  const [applicationId, setApplicationId] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  function reset() {
    setName('');
    setApplicationId('');
    setError(null);
    setBusy(false);
  }

  function close() {
    if (busy) return;
    reset();
    onClose();
  }

  async function submit() {
    setBusy(true);
    setError(null);
    try {
      const account = await createAccount({
        name: name.trim() || 'Akun WhatsApp',
        label: name.trim() ? name.trim().toUpperCase() : null,
        application_id: applicationId || null,
        connection_method: 'qr',
      });
      reset();
      onCreated(account);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Gagal membuat akun.');
      setBusy(false);
    }
  }

  return (
    <Modal
      open={open}
      onClose={close}
      title="Tambah Akun WhatsApp"
      size="sm"
      footer={
        <>
          <Button onClick={close} disabled={busy}>
            Batal
          </Button>
          <Button variant="primary" loading={busy} onClick={submit}>
            Tambah Akun
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        <label className="block">
          <span className="mb-1.5 block text-sm font-medium text-ink">
            Nama Akun (opsional)
          </span>
          <input
            value={name}
            onChange={(event) => setName(event.target.value)}
            placeholder="Contoh: HP Kantor"
            maxLength={80}
            className="h-10 w-full rounded-control border border-hairline bg-surface-raised px-3 text-sm outline-none transition-colors placeholder:text-ink-muted/70 focus:border-brand-700 focus:ring-2 focus:ring-brand-700/15"
          />
        </label>

        <label className="block">
          <span className="mb-1.5 block text-sm font-medium text-ink">
            Aplikasi (klasifikasi)
          </span>
          <select
            value={applicationId}
            onChange={(event) => setApplicationId(event.target.value)}
            className="h-10 w-full rounded-control border border-hairline bg-surface-raised px-3 text-sm outline-none transition-colors focus:border-brand-700 focus:ring-2 focus:ring-brand-700/15"
          >
            <option value="">(pilih aplikasi)</option>
            {applications.map((app) => (
              <option key={app.id} value={app.id}>
                {app.name}
              </option>
            ))}
          </select>
        </label>

        <p className="text-xs leading-relaxed text-ink-muted">
          Nama aplikasi otomatis jadi tag &amp; kontak yang chat nomor ini akan terklasifikasi ke
          aplikasi tsb.
        </p>

        {error ? <ErrorNote message={error} /> : null}
      </div>
    </Modal>
  );
}
