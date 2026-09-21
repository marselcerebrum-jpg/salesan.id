'use client';

import { LogOut, RefreshCw } from 'lucide-react';
import { useEffect, useState } from 'react';

import { Button } from '@/components/ui/Button';
import { Modal } from '@/components/ui/Modal';
import { ErrorNote, StatusPill } from '@/components/ui/Primitives';
import { logoutAccount, syncAccount, updateAccount } from '@/lib/api';
import { ACCOUNT_STATUS_LABEL, accountStatusTone } from '@/lib/format';
import type { Account, Application } from '@/lib/types';

interface AccountDetailModalProps {
  account: Account | null;
  applications: Application[];
  onClose: () => void;
  onChanged: () => void;
}

/**
 * Detail sheet behind the "Detail" button: rename, re-classify, force a sync,
 * and unlink the device.
 */
export function AccountDetailModal({
  account,
  applications,
  onClose,
  onChanged,
}: AccountDetailModalProps) {
  const [name, setName] = useState('');
  const [label, setLabel] = useState('');
  const [applicationId, setApplicationId] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);

  useEffect(() => {
    if (!account) return;
    setName(account.name);
    setLabel(account.label ?? '');
    setApplicationId(account.application_id ?? '');
    setError(null);
    setNotice(null);
  }, [account]);

  if (!account) return null;
  const tone = accountStatusTone(account.status);

  async function guard(key: string, fn: () => Promise<void>) {
    setBusy(key);
    setError(null);
    setNotice(null);
    try {
      await fn();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Aksi gagal.');
    } finally {
      setBusy(null);
    }
  }

  const save = () =>
    guard('save', async () => {
      await updateAccount(account.id, {
        name: name.trim() || account.name,
        label: label.trim() || null,
        application_id: applicationId || null,
      });
      onChanged();
      setNotice('Perubahan tersimpan.');
    });

  const sync = () =>
    guard('sync', async () => {
      const result = await syncAccount(account.id);
      onChanged();
      const parts = [
        `${result.contacts} kontak`,
        `${result.groups} grup`,
        `${result.labels} label`,
      ];
      if (result.history_pending) parts.push('riwayat sedang ditarik dari HP');
      setNotice(`Sinkron ${result.window_days} hari terakhir: ${parts.join(', ')}.`);
    });

  const unlink = () =>
    guard('logout', async () => {
      await logoutAccount(account.id);
      onChanged();
      setNotice('Perangkat sudah dilepas. Scan QR lagi untuk menghubungkan.');
    });

  return (
    <Modal
      open
      onClose={onClose}
      title="Detail Akun"
      size="md"
      footer={
        <>
          <Button onClick={onClose}>Tutup</Button>
          <Button variant="primary" loading={busy === 'save'} onClick={save}>
            Simpan
          </Button>
        </>
      }
    >
      <div className="space-y-5">
        <div className="flex items-center justify-between gap-3 rounded-xl bg-surface-sunken px-4 py-3">
          <div className="min-w-0">
            <p className="text-2xs text-ink-muted">Device ID</p>
            <p className="font-mono text-sm text-ink">{account.device_id}</p>
          </div>
          <StatusPill
            label={ACCOUNT_STATUS_LABEL[account.status]}
            dotClass={tone.dot}
            textClass={tone.text}
            bgClass={tone.bg}
          />
        </div>

        <dl className="grid grid-cols-2 gap-x-4 gap-y-3 text-sm">
          <Info term="Nomor WA" value={account.phone_number ?? 'Belum tertaut'} />
          <Info term="Metode" value={account.connection_method === 'waba' ? 'WABA' : 'QR Scan'} />
          <Info term="Percakapan" value={String(account.conversation_count)} />
          <Info term="Total pesan" value={String(account.message_count)} />
          <Info
            term="Terakhir online"
            value={
              account.last_connected_at
                ? new Date(account.last_connected_at).toLocaleString('id-ID')
                : '-'
            }
          />
          <Info term="Belum dibaca" value={String(account.unread_count)} />
        </dl>

        <div className="space-y-4 border-t border-hairline pt-4">
          <Field label="Nama akun" value={name} onChange={setName} />
          <Field label="Label" value={label} onChange={setLabel} placeholder="Contoh: HP UTAMA" />

          <label className="block">
            <span className="mb-1.5 block text-sm font-medium text-ink">Aplikasi</span>
            <select
              value={applicationId}
              onChange={(event) => setApplicationId(event.target.value)}
              className="h-10 w-full rounded-control border border-hairline bg-surface-raised px-3 text-sm outline-none focus:border-brand-700 focus:ring-2 focus:ring-brand-700/15"
            >
              <option value="">(tanpa aplikasi)</option>
              {applications.map((app) => (
                <option key={app.id} value={app.id}>
                  {app.name}
                </option>
              ))}
            </select>
          </label>
        </div>

        <div className="flex flex-wrap gap-2 border-t border-hairline pt-4">
          <Button
            icon={<RefreshCw className="size-4" />}
            loading={busy === 'sync'}
            disabled={account.status !== 'connected'}
            onClick={sync}
          >
            Sinkron kontak &amp; grup
          </Button>
          <Button
            variant="danger"
            icon={<LogOut className="size-4" />}
            loading={busy === 'logout'}
            disabled={!account.jid}
            onClick={unlink}
          >
            Lepas perangkat
          </Button>
        </div>

        {notice ? (
          <p className="rounded-lg border border-brand-600/20 bg-brand-600/10 px-3 py-2 text-sm text-brand-700">
            {notice}
          </p>
        ) : null}
        {error ? <ErrorNote message={error} /> : null}
      </div>
    </Modal>
  );
}

function Info({ term, value }: { term: string; value: string }) {
  return (
    <div>
      <dt className="text-2xs text-ink-muted">{term}</dt>
      <dd className="truncate font-medium text-ink">{value}</dd>
    </div>
  );
}

function Field({
  label,
  value,
  onChange,
  placeholder,
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
}) {
  return (
    <label className="block">
      <span className="mb-1.5 block text-sm font-medium text-ink">{label}</span>
      <input
        value={value}
        placeholder={placeholder}
        onChange={(event) => onChange(event.target.value)}
        className="h-10 w-full rounded-control border border-hairline bg-surface-raised px-3 text-sm outline-none placeholder:text-ink-muted/70 focus:border-brand-700 focus:ring-2 focus:ring-brand-700/15"
      />
    </label>
  );
}
