'use client';

import clsx from 'clsx';
import {
  CheckCircle2,
  ChevronRight,
  Clock,
  HelpCircle,
  LayoutGrid,
  MoreVertical,
  RotateCcw,
  Search,
  SlidersHorizontal,
} from 'lucide-react';
import { useEffect, useMemo, useRef, useState } from 'react';
import useSWR from 'swr';

import { AppMark } from '@/components/analytics/AppBadge';
import { EmptyState, ErrorState, RowSkeleton } from '@/components/analytics/Primitives';
import { useApplications } from '@/components/settings/AppScoped';
import { Button } from '@/components/ui/Button';
import { ConfirmDialog } from '@/components/ui/ConfirmDialog';
import { Modal } from '@/components/ui/Modal';
import { inputClassSm } from '@/components/ui/control';
import {
  deleteSLATarget,
  fetcher,
  slaTargetsPath,
  upsertSLATarget,
  type SLATargetsResponse,
} from '@/lib/api';
import type { Application } from '@/lib/types';

/**
 * Target SLA.
 *
 * Until now the response-time promise was a single number in the server's
 * environment: one value for the whole workspace, changeable only by touching
 * the server, and identical for every brand. A shop promising a reply in five
 * minutes and a B2B service where an hour is fine had to share it.
 *
 * Here it is a default plus per-application overrides. Everyone can read this
 * page, deliberately: a Freelance is measured against these numbers, and a rule
 * somebody is judged by but not allowed to see is a trap rather than a rule.
 *
 * The one thing this screen cannot do is change the past, and it says so. Each
 * target is copied onto an SLA cycle when the cycle is created, so raising the
 * target today does not turn last month's breaches into successes.
 */
export default function SLASettingsPage() {
  const { data, error, isLoading, mutate } = useSWR<SLATargetsResponse>(slaTargetsPath, fetcher);
  const applications = useApplications();
  const [failure, setFailure] = useState<string | null>(null);
  const [term, setTerm] = useState('');
  const [guide, setGuide] = useState(false);
  const [resetting, setResetting] = useState(false);

  const targets = useMemo(() => data?.targets ?? [], [data]);
  const byApp = useMemo(
    () => new Map(targets.filter((t) => t.application_id).map((t) => [t.application_id!, t])),
    [targets],
  );
  const fallback = targets.find((t) => t.application_id === null) ?? null;

  const effectiveDefault = fallback?.target_seconds ?? data?.fallback_seconds ?? 0;
  const defaultBusiness = fallback?.business_hours ?? data?.fallback_business_hours ?? true;

  const custom = applications.filter((a) => byApp.has(a.id));
  const shown = useMemo(() => {
    const q = term.trim().toLowerCase();
    if (!q) return applications;
    return applications.filter(
      (a) => a.code.toLowerCase().includes(q) || a.name.toLowerCase().includes(q),
    );
  }, [applications, term]);

  async function save(applicationID: string | null, seconds: number, business: boolean) {
    setFailure(null);
    try {
      await upsertSLATarget({
        application_id: applicationID,
        target_seconds: seconds,
        business_hours: business,
      });
      void mutate();
    } catch (e) {
      setFailure(e instanceof Error ? e.message : 'Gagal menyimpan target.');
    }
  }

  async function clear(id: string) {
    setFailure(null);
    try {
      await deleteSLATarget(id);
      void mutate();
    } catch (e) {
      setFailure(e instanceof Error ? e.message : 'Gagal menghapus target.');
    }
  }

  /** Drops every per-application target, leaving the workspace default alone. */
  async function resetAll() {
    setFailure(null);
    try {
      for (const a of custom) {
        const own = byApp.get(a.id);
        if (own) await deleteSLATarget(own.id);
      }
      void mutate();
    } catch (e) {
      setFailure(e instanceof Error ? e.message : 'Gagal mengembalikan ke bawaan.');
    } finally {
      setResetting(false);
    }
  }

  return (
    <div className="px-5 py-6 lg:px-8 lg:py-8">
      <div className="mx-auto w-full max-w-[1280px]">
        <nav aria-label="Jalur" className="flex items-center gap-1 text-2xs text-ink-muted">
          <span>Pengaturan</span>
          <ChevronRight className="size-3" aria-hidden />
          <span className="text-ink-soft">Target SLA</span>
        </nav>

        <header className="mt-1 flex flex-wrap items-start justify-between gap-x-4 gap-y-3">
          <div className="min-w-0">
            <h1 className="text-2xl font-semibold tracking-[-0.02em] text-ink">Target SLA</h1>
            <p className="mt-1 text-sm text-ink-muted">
              Atur target waktu respons pelanggan untuk setiap aplikasi. Target ini digunakan
              sebagai acuan pada laporan performa.
            </p>
          </div>
          <Button onClick={() => setGuide(true)}>
            <HelpCircle className="size-4" />
            Panduan
          </Button>
        </header>

        {failure ? (
          <div className="mt-4">
            <ErrorState message={failure} onRetry={() => setFailure(null)} />
          </div>
        ) : null}

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
        ) : (
          <>
            <div className="mt-5 grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
              <Stat
                icon={Clock}
                tone="brand"
                label="Target Default"
                value={`${Math.round(effectiveDefault / 60)} menit`}
                hint="Bawaan workspace"
              />
              <Stat
                icon={LayoutGrid}
                tone="info"
                label="Total Aplikasi"
                value={String(applications.length)}
                hint="Aplikasi terdaftar"
              />
              <Stat
                icon={SlidersHorizontal}
                tone="iris"
                label="Aplikasi Custom"
                value={String(custom.length)}
                hint="Memiliki target khusus"
              />
              <Stat
                icon={CheckCircle2}
                tone="good"
                label="SLA Aktif"
                value={String(effectiveDefault > 0 ? applications.length : custom.length)}
                hint="Aplikasi menggunakan SLA"
              />
            </div>

            {/* The number every application falls back to. Its own card rather
                than the first row of the table, because it is not one of them:
                changing it moves every application that has no target of its
                own. */}
            <section className="mt-4 rounded-card border border-hairline bg-surface-raised px-4 py-4 shadow-e1">
              <div className="flex flex-wrap items-center justify-between gap-4">
                <div className="flex min-w-0 items-center gap-3">
                  <span className="grid size-10 shrink-0 place-items-center rounded-control bg-brand-600/12 text-brand-700">
                    <Clock className="size-5" />
                  </span>
                  <div className="min-w-0">
                    <h2 className="text-base font-semibold text-ink">Bawaan Workspace</h2>
                    <p className="text-xs text-ink-muted">
                      Target ini digunakan oleh aplikasi yang tidak memiliki pengaturan khusus.
                      {fallback ? '' : ' Saat ini masih memakai bawaan sistem.'}
                    </p>
                  </div>
                </div>

                <TargetControls
                  label="bawaan workspace"
                  seconds={effectiveDefault}
                  business={defaultBusiness}
                  canEdit={data?.can_edit_default ?? false}
                  onSave={(s, b) => save(null, s, b)}
                />
              </div>
            </section>

            <section className="mt-4 overflow-hidden rounded-card border border-hairline bg-surface-raised shadow-e1">
              <div className="flex flex-wrap items-center justify-between gap-3 border-b border-hairline px-4 py-3.5">
                <div className="flex min-w-0 items-center gap-3">
                  <span className="grid size-10 shrink-0 place-items-center rounded-control bg-info-soft text-info">
                    <LayoutGrid className="size-5" />
                  </span>
                  <div className="min-w-0">
                    <h2 className="text-base font-semibold text-ink">Per Aplikasi</h2>
                    <p className="text-xs text-ink-muted">
                      Atur target waktu respons untuk setiap aplikasi. Jika tidak diatur, akan
                      mengikuti bawaan workspace.
                    </p>
                  </div>
                </div>

                <div className="flex flex-wrap items-center gap-2">
                  <label className="relative block w-full sm:w-[220px]">
                    <Search className="pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-ink-muted" />
                    <input
                      value={term}
                      onChange={(e) => setTerm(e.target.value)}
                      placeholder="Cari aplikasi…"
                      aria-label="Cari aplikasi"
                      className={clsx(inputClassSm, 'w-full pl-8')}
                    />
                  </label>
                  {custom.length > 0 ? (
                    <Button onClick={() => setResetting(true)}>
                      <RotateCcw className="size-4" />
                      Reset ke bawaan
                    </Button>
                  ) : null}
                </div>
              </div>

              {applications.length === 0 ? (
                <div className="px-4 py-6">
                  <EmptyState
                    title="Belum ada aplikasi"
                    hint="Buat aplikasi lebih dulu di halaman Pengaturan."
                  />
                </div>
              ) : shown.length === 0 ? (
                <p className="px-4 py-10 text-center text-sm text-ink-muted">
                  Tidak ada aplikasi yang cocok.
                </p>
              ) : (
                <div className="max-h-[70vh] overflow-auto">
                  <table className="w-full min-w-[880px] border-collapse text-xs">
                    <thead>
                      <tr className="text-left text-2xs tracking-wide text-ink-muted uppercase">
                        {['Aplikasi', 'Status', 'Target Respons', 'Hitung hanya saat jam kerja', ''].map(
                          (h, i) => (
                            <th
                              key={h || i}
                              className="sticky top-0 z-10 border-b border-hairline bg-surface-raised px-3 py-2.5 font-semibold"
                            >
                              {h || <span className="sr-only">Aksi</span>}
                            </th>
                          ),
                        )}
                      </tr>
                    </thead>
                    <tbody>
                      {shown.map((a) => {
                        const own = byApp.get(a.id) ?? null;
                        return (
                          <ApplicationRow
                            key={a.id}
                            app={a}
                            seconds={own?.target_seconds ?? effectiveDefault}
                            business={own?.business_hours ?? defaultBusiness}
                            custom={Boolean(own)}
                            defaultMinutes={Math.round(effectiveDefault / 60)}
                            onSave={(s, b) => save(a.id, s, b)}
                            onClear={own ? () => clear(own.id) : undefined}
                          />
                        );
                      })}
                    </tbody>
                  </table>
                </div>
              )}
            </section>
          </>
        )}
      </div>

      <Modal open={guide} onClose={() => setGuide(false)} title="Panduan Target SLA" size="md">
        <div className="space-y-3 text-sm leading-relaxed text-ink-soft">
          <p>
            Target SLA adalah batas waktu antara pesan pertama pelanggan dan balasan manual pertama
            dari tim. Yang dihitung hanya chat pribadi — grup tidak punya satu pelanggan yang
            menunggu dijawab.
          </p>
          <p>
            <span className="font-medium text-ink">Hitung hanya saat jam kerja</span> membuat jam
            berhenti berjalan di luar jadwal. Tanpa itu, pesan yang masuk pukul 2 pagi sudah
            melewati target sebelum ada yang bertugas. Pesan yang datang di luar jam kerja dilaporkan
            terpisah sebagai antrean, bukan sebagai pelanggaran SLA.
          </p>
          <p>
            Target disalin ke setiap siklus respons saat siklus itu dibuat. Mengubah angka di sini
            hanya mempengaruhi siklus baru: laporan bulan lalu tetap dinilai dengan target yang
            berlaku saat itu, dan tidak akan berubah.
          </p>
        </div>
      </Modal>

      <ConfirmDialog
        request={
          resetting
            ? {
                title: 'Kembalikan semua ke bawaan?',
                description: (
                  <>
                    Target khusus pada{' '}
                    <span className="font-medium text-ink">{custom.length} aplikasi</span> akan
                    dihapus, dan semuanya kembali mengikuti bawaan workspace. Siklus yang sudah
                    berjalan tidak berubah — target sudah tersalin ke masing-masing siklus saat
                    dibuat.
                  </>
                ),
                confirmLabel: 'Kembalikan ke bawaan',
                tone: 'danger',
                icon: RotateCcw,
                onConfirm: resetAll,
              }
            : null
        }
        onClose={() => setResetting(false)}
      />
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

function Stat({
  icon: Icon,
  tone,
  label,
  value,
  hint,
}: {
  icon: typeof Clock;
  tone: keyof typeof TILES;
  label: string;
  value: string;
  hint: string;
}) {
  return (
    <div className="flex items-center gap-3 rounded-card border border-hairline bg-surface-raised px-4 py-3.5 shadow-e1">
      <span
        className={clsx(
          'flex size-10 shrink-0 items-center justify-center rounded-control',
          TILES[tone],
        )}
      >
        <Icon className="size-5" aria-hidden />
      </span>
      <span className="min-w-0">
        <span className="block truncate text-xs text-ink-muted">{label}</span>
        <span className="nums block text-xl leading-tight font-semibold text-ink">{value}</span>
        <span className="block truncate text-2xs text-ink-muted">{hint}</span>
      </span>
    </div>
  );
}

/**
 * A switch, not a checkbox.
 *
 * The same control appears on every row and on the default above them, and at
 * a glance down a column a switch reads as on or off from its shape alone.
 */
function Toggle({
  checked,
  onChange,
  disabled,
  label,
}: {
  checked: boolean;
  onChange: (next: boolean) => void;
  disabled?: boolean;
  label: string;
}) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      aria-label={label}
      disabled={disabled}
      onClick={() => onChange(!checked)}
      className={clsx(
        'relative h-5 w-9 shrink-0 rounded-full transition-colors',
        checked ? 'bg-brand-700' : 'bg-hairline-strong',
        disabled ? 'cursor-not-allowed opacity-50' : 'cursor-pointer',
      )}
    >
      <span
        aria-hidden
        className={clsx(
          'absolute top-0.5 size-4 rounded-full bg-white shadow-e1 transition-[left]',
          checked ? 'left-[18px]' : 'left-0.5',
        )}
      />
    </button>
  );
}

/**
 * The minutes box, the switch, and the button that commits them.
 *
 * Minutes rather than seconds because nobody promises a customer 900 seconds.
 * The value is converted on the way in and out, and the stored unit stays
 * seconds so the API and the SLA cycles keep one unit between them.
 */
function TargetControls({
  label,
  seconds,
  business,
  canEdit,
  onSave,
  compact = false,
  actions,
}: {
  label: string;
  seconds: number;
  business: boolean;
  canEdit: boolean;
  onSave: (seconds: number, business: boolean) => Promise<void>;
  /** Renders the three cells of a table row instead of a standalone block. */
  compact?: boolean;
  /** Extra controls sharing the action cell, in the compact form. */
  actions?: React.ReactNode;
}) {
  const [minutes, setMinutes] = useState(String(Math.round(seconds / 60)));
  const [hours, setHours] = useState(business);
  const [busy, setBusy] = useState(false);

  // The saved value is the truth. When a save elsewhere changes it — the bulk
  // reset, or the default moving under an application that follows it — the
  // boxes follow instead of showing the number somebody typed and abandoned.
  const savedMinutes = Math.round(seconds / 60);
  useEffect(() => {
    setMinutes(String(savedMinutes));
    setHours(business);
  }, [savedMinutes, business]);

  const parsed = Number(minutes);
  const valid = Number.isFinite(parsed) && parsed >= 1 && parsed <= 1440;
  const dirty = parsed !== savedMinutes || hours !== business;

  async function submit() {
    if (!valid || !dirty) return;
    setBusy(true);
    try {
      await onSave(Math.round(parsed * 60), hours);
    } finally {
      setBusy(false);
    }
  }

  if (compact) {
    return (
      <>
        <td className="px-3 py-3">
          <span className="flex items-center gap-2">
            <input
              type="number"
              min={1}
              max={1440}
              value={minutes}
              disabled={!canEdit}
              onChange={(e) => setMinutes(e.target.value)}
              aria-label={`Target menit untuk ${label}`}
              className={clsx(inputClassSm, 'w-20 text-center', !valid && 'border-danger')}
            />
            <span className="text-ink-muted">menit</span>
          </span>
          {!valid ? <p className="mt-1 text-2xs text-danger">Antara 1 dan 1440.</p> : null}
        </td>

        <td className="px-3 py-3">
          <Toggle
            checked={hours}
            onChange={setHours}
            disabled={!canEdit}
            label={`Hitung hanya saat jam kerja untuk ${label}`}
          />
        </td>

        {/* Save and the row's other actions share one cell, so the row has as
            many cells as the header has columns. */}
        <td className="px-3 py-3">
          <span className="flex items-center gap-1">
            <Button
              size="sm"
              variant="primary"
              onClick={submit}
              disabled={!valid || !dirty}
              loading={busy}
            >
              Simpan
            </Button>
            {actions}
          </span>
        </td>
      </>
    );
  }

  return (
    <div className="flex flex-wrap items-end gap-4">
      <label className="block">
        <span className="mb-1 block text-2xs text-ink-muted">Target respons</span>
        <span className="flex items-center gap-2">
          <input
            type="number"
            min={1}
            max={1440}
            value={minutes}
            disabled={!canEdit}
            onChange={(e) => setMinutes(e.target.value)}
            aria-label={`Target menit untuk ${label}`}
            className={clsx(inputClassSm, 'w-20 text-center', !valid && 'border-danger')}
          />
          <span className="text-sm text-ink-muted">menit</span>
        </span>
      </label>

      <span className="flex items-center gap-2 pb-1.5">
        <Toggle
          checked={hours}
          onChange={setHours}
          disabled={!canEdit}
          label={`Hitung hanya saat jam kerja untuk ${label}`}
        />
        <span
          className="flex items-center gap-1 text-xs text-ink-soft"
          title="Di luar jam kerja jam berhenti berjalan. Tanpa ini, pesan pukul 2 pagi sudah melewati target sebelum ada yang bertugas."
        >
          Hitung hanya saat jam kerja
          <HelpCircle className="size-3.5 text-ink-muted" aria-hidden />
        </span>
      </span>

      {canEdit ? (
        <Button variant="primary" onClick={submit} disabled={!valid || !dirty} loading={busy}>
          Simpan
        </Button>
      ) : (
        <span className="pb-2 text-2xs text-ink-muted">Hanya Leader yang dapat mengubah ini</span>
      )}

      {!valid ? <p className="w-full text-xs text-danger">Target harus antara 1 dan 1440 menit.</p> : null}
    </div>
  );
}

function ApplicationRow({
  app,
  seconds,
  business,
  custom,
  defaultMinutes,
  onSave,
  onClear,
}: {
  app: Application;
  seconds: number;
  business: boolean;
  /** True when this application has a target of its own. */
  custom: boolean;
  defaultMinutes: number;
  onSave: (seconds: number, business: boolean) => Promise<void>;
  onClear?: () => Promise<void>;
}) {
  return (
    <tr className="border-b border-hairline last:border-0">
      <td className="px-3 py-3">
        <span className="flex min-w-0 items-center gap-2.5">
          <AppMark code={app.code} color={app.color} size={28} />
          <span className="truncate text-sm font-medium text-ink">{app.code}</span>
        </span>
      </td>

      <td className="px-3 py-3">
        <span
          className={clsx(
            'inline-block rounded-full px-2 py-0.5 text-2xs font-medium',
            custom
              ? 'bg-brand-600/12 text-brand-700'
              : 'border border-hairline bg-surface-sunken text-ink-soft',
          )}
        >
          {custom ? 'Custom' : 'Mengikuti workspace'}
        </span>
        <p className="mt-1 text-2xs text-ink-muted">
          {custom
            ? 'Target khusus untuk aplikasi ini'
            : `Menggunakan target bawaan (${defaultMinutes} menit)`}
        </p>
      </td>

      <TargetControls
        compact
        label={app.code}
        seconds={seconds}
        business={business}
        canEdit
        onSave={onSave}
        actions={
          onClear ? (
            <RowMenu
              label={`Aksi untuk ${app.code}`}
              items={[{ label: 'Ikuti bawaan workspace', onClick: () => void onClear() }]}
            />
          ) : null
        }
      />
    </tr>
  );
}

/** The row's own menu: one button, and what it can do behind it. */
function RowMenu({
  label,
  items,
}: {
  label: string;
  items: { label: string; onClick: () => void }[];
}) {
  const [open, setOpen] = useState(false);
  const box = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    function onDown(e: MouseEvent) {
      if (!box.current?.contains(e.target as Node)) setOpen(false);
    }
    function onKey(e: KeyboardEvent) {
      if (e.key === 'Escape') setOpen(false);
    }
    document.addEventListener('mousedown', onDown);
    document.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('mousedown', onDown);
      document.removeEventListener('keydown', onKey);
    };
  }, [open]);

  return (
    <div ref={box} className="relative">
      <button
        type="button"
        aria-label={label}
        aria-expanded={open}
        onClick={() => setOpen((v) => !v)}
        className="rounded-control p-1.5 text-ink-muted transition-colors hover:bg-surface-sunken hover:text-ink"
      >
        <MoreVertical className="size-4" />
      </button>

      {open ? (
        <div className="absolute right-0 z-30 mt-1 w-52 rounded-card border border-hairline bg-surface-raised p-1 shadow-e2">
          {items.map((item) => (
            <button
              key={item.label}
              type="button"
              onClick={() => {
                setOpen(false);
                item.onClick();
              }}
              className="block w-full rounded-control px-2.5 py-2 text-left text-xs text-ink-soft transition-colors hover:bg-surface-sunken"
            >
              {item.label}
            </button>
          ))}
        </div>
      ) : null}
    </div>
  );
}
