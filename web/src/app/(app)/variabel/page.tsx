'use client';

import { Plus, Trash2 } from 'lucide-react';
import { useState } from 'react';
import useSWR from 'swr';

import {
  AppFilter,
  AppGroupHeader,
  AppSelect,
  SettingsPageShell,
  filterByApp,
  groupByApp,
  useAppFilter,
  useApplications,
} from '@/components/settings/AppScoped';
import { EmptyState, ErrorState, RowSkeleton } from '@/components/analytics/Primitives';
import { Button } from '@/components/ui/Button';
import { ConfirmDialog } from '@/components/ui/ConfirmDialog';
import { inputClass, inputClassSm, labelClass } from '@/components/ui/control';
import {
  customVariablesPath,
  deleteCustomVariable,
  fetcher,
  upsertCustomVariable,
} from '@/lib/api';
import type { AnalyticsScope, CustomVariable } from '@/lib/types';

/**
 * Variabel Pesan.
 *
 * This used to sit inside Akun & Peran, which is a screen about who works here
 * and what they may touch. A placeholder rendered into an outgoing message has
 * nothing to do with that, and burying it there meant the people who write
 * campaigns had to go looking in a permissions screen to change their own
 * vocabulary.
 *
 * Everything here is scoped to one application, because a workspace holds a
 * dozen brands and "nama_toko" means a different shop in each. A variable with
 * no application is allowed and means the company's own, which only a Leader
 * can create.
 */
export default function VariablesPage() {
  const { data, error, isLoading, mutate } = useSWR<{
    variables: CustomVariable[];
    built_in: { key: string; label: string }[];
  }>(customVariablesPath, fetcher);
  const org = useSWR<{ scope: AnalyticsScope }>('/org/members', fetcher);
  const applications = useApplications();

  const [filter, setFilter] = useAppFilter();
  const [composing, setComposing] = useState(false);
  const [pending, setPending] = useState<CustomVariable | null>(null);
  const [failure, setFailure] = useState<string | null>(null);

  const role = org.data?.scope.role ?? '';
  const canEdit = role === 'leader' || role === 'pic' || role === '';
  const isLeader = role === 'leader' || role === '';

  const rows = data?.variables ?? [];
  const visible = filterByApp(rows, filter);
  const groups = groupByApp(visible);

  async function remove(v: CustomVariable) {
    setFailure(null);
    try {
      await deleteCustomVariable(v.id);
      setPending(null);
      void mutate();
    } catch (e) {
      setFailure(e instanceof Error ? e.message : 'Gagal menghapus variabel.');
    }
  }

  return (
    <SettingsPageShell
      title="Variabel Pesan"
      description="Potongan teks yang diisi otomatis saat Broadcast dan WA Story dikirim. Ditulis di pesan sebagai {{nama_variabel}}."
      actions={
        canEdit ? (
          <Button variant="primary" onClick={() => setComposing(true)}>
            <Plus className="size-4" />
            Variabel baru
          </Button>
        ) : undefined
      }
    >
      {failure ? (
        <div className="mt-4">
          <ErrorState message={failure} onRetry={() => setFailure(null)} />
        </div>
      ) : null}

      {composing ? (
        <VariableForm
          applications={applications}
          canPickAll={isLeader}
          onClose={() => setComposing(false)}
          onSaved={() => {
            setComposing(false);
            void mutate();
          }}
        />
      ) : null}

      <div className="mt-5">
        <AppFilter rows={rows} value={filter} onChange={setFilter} applications={applications} />
      </div>

      {error ? (
        <div className="mt-5">
          <ErrorState
            message={error instanceof Error ? error.message : 'Gagal memuat.'}
            onRetry={() => void mutate()}
          />
        </div>
      ) : isLoading ? (
        <div className="mt-5">
          <RowSkeleton count={4} />
        </div>
      ) : visible.length === 0 ? (
        <div className="mt-5">
          <EmptyState
            title={rows.length === 0 ? 'Belum ada variabel' : 'Tidak ada di aplikasi ini'}
            hint={
              rows.length === 0
                ? 'Buat variabel agar satu pesan bisa menyapa tiap penerima dengan datanya sendiri.'
                : 'Pilih aplikasi lain di baris filter, atau buat variabel untuk aplikasi ini.'
            }
          />
        </div>
      ) : (
        <div className="mt-5 space-y-6">
          {groups.map((g) => (
            <section key={g.key}>
              <AppGroupHeader group={g} />
              <ul className="divide-y divide-hairline overflow-hidden rounded-card border border-hairline bg-surface-raised shadow-e1">
                {g.rows.map((v) => (
                  <li key={v.id} className="flex items-start gap-3 px-4 py-3">
                    <div className="min-w-0 flex-1">
                      <p className="font-mono text-sm text-ink">{`{{${v.key}}}`}</p>
                      <p className="mt-0.5 text-sm text-ink-soft">{v.label}</p>
                      {v.description ? (
                        <p className="mt-0.5 text-xs text-ink-muted">{v.description}</p>
                      ) : null}
                      {v.default_value ? (
                        <p className="mt-1 text-xs text-ink-muted">
                          Nilai bawaan: <span className="text-ink-soft">{v.default_value}</span>
                        </p>
                      ) : null}
                    </div>
                    {!v.is_active ? (
                      <span className="shrink-0 rounded-full bg-surface-sunken px-2 py-0.5 text-2xs text-ink-muted">
                        Nonaktif
                      </span>
                    ) : null}
                    {canEdit ? (
                      <button
                        type="button"
                        onClick={() => setPending(v)}
                        aria-label={`Hapus ${v.key}`}
                        className="shrink-0 rounded-lg p-1.5 text-ink-muted transition-colors hover:bg-danger-soft hover:text-danger"
                      >
                        <Trash2 className="size-4" />
                      </button>
                    ) : null}
                  </li>
                ))}
              </ul>
            </section>
          ))}
        </div>
      )}

      {(data?.built_in.length ?? 0) > 0 ? (
        <section className="mt-8">
          <h2 className="text-lg font-semibold tracking-[-0.01em] text-ink">Variabel bawaan</h2>
          <p className="mt-0.5 text-sm text-ink-muted">
            Diisi dari data kontak, tersedia di semua aplikasi dan tidak bisa diubah.
          </p>
          <ul className="mt-3 flex flex-wrap gap-1.5">
            {(data?.built_in ?? []).map((b) => (
              <li
                key={b.key}
                className="rounded-md border border-hairline bg-surface-raised px-2 py-1 text-2xs text-ink-soft"
              >
                <span className="font-mono">{`{{${b.key}}}`}</span> {b.label}
              </li>
            ))}
          </ul>
        </section>
      ) : null}

      <ConfirmDialog
        request={
          pending
            ? {
                title: `Hapus variabel ${pending.key}?`,
                description:
                  'Pesan yang sudah terkirim tidak berubah: nilainya sudah tersimpan di baris pengirimannya. Yang hilang hanya definisinya.',
                confirmLabel: 'Hapus variabel',
                tone: 'danger',
                onConfirm: () => remove(pending),
              }
            : null
        }
        onClose={() => setPending(null)}
      />
    </SettingsPageShell>
  );
}

function VariableForm({
  applications,
  canPickAll,
  onClose,
  onSaved,
}: {
  applications: { id: string; code: string; name: string; color: string | null }[];
  canPickAll: boolean;
  onClose: () => void;
  onSaved: () => void;
}) {
  const [applicationID, setApplicationID] = useState('');
  const [key, setKey] = useState('');
  const [label, setLabel] = useState('');
  const [defaultValue, setDefaultValue] = useState('');
  const [description, setDescription] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function save() {
    setBusy(true);
    setError(null);
    try {
      await upsertCustomVariable({
        key: key.trim().toLowerCase(),
        application_id: applicationID || null,
        label: label.trim() || key.trim(),
        default_value: defaultValue.trim() || null,
        description: description.trim() || null,
      });
      onSaved();
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Gagal menyimpan.');
    } finally {
      setBusy(false);
    }
  }

  const canSave = /^[a-z0-9_]{2,40}$/.test(key.trim().toLowerCase()) && !busy;

  return (
    <div className="mt-5 rounded-card border border-hairline bg-surface-raised p-4 shadow-e1">
      <div className="grid gap-3 sm:grid-cols-2">
        <label className="block">
          <span className={labelClass}>Aplikasi</span>
          <AppSelect
            value={applicationID}
            onChange={setApplicationID}
            applications={applications as never}
            canPickAll={canPickAll}
            className={inputClass}
          />
        </label>
        <label className="block">
          <span className={labelClass}>Nama variabel</span>
          <input
            value={key}
            onChange={(e) => setKey(e.target.value)}
            placeholder="nama_toko"
            className={inputClass}
          />
        </label>
        <label className="block">
          <span className={labelClass}>Keterangan singkat</span>
          <input
            value={label}
            onChange={(e) => setLabel(e.target.value)}
            placeholder="Nama toko yang muncul di pesan"
            className={inputClass}
          />
        </label>
        <label className="block">
          <span className={labelClass}>Nilai bawaan</span>
          <input
            value={defaultValue}
            onChange={(e) => setDefaultValue(e.target.value)}
            placeholder="Dipakai kalau tidak diisi saat kirim"
            className={inputClass}
          />
        </label>
        <label className="block sm:col-span-2">
          <span className={labelClass}>Catatan</span>
          <input
            value={description}
            onChange={(e) => setDescription(e.target.value)}
            className={inputClass}
          />
        </label>
      </div>

      {error ? <p className="mt-3 text-sm text-danger">{error}</p> : null}

      <div className="mt-4 flex flex-wrap items-center gap-2">
        <Button variant="primary" onClick={save} disabled={!canSave} loading={busy}>
          Simpan variabel
        </Button>
        <button
          type="button"
          onClick={onClose}
          className={`${inputClassSm} cursor-pointer font-medium text-ink-soft`}
        >
          Batal
        </button>
        <span className="text-xs text-ink-muted">
          Huruf kecil, angka dan garis bawah saja. Dipakai di pesan sebagai{' '}
          <span className="font-mono">{`{{${key.trim() || 'nama'}}}`}</span>
        </span>
      </div>
    </div>
  );
}
