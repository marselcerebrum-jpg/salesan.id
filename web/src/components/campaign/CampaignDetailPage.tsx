'use client';

import clsx from 'clsx';
import {
  Archive,
  ArrowLeft,
  BarChart3,
  CheckCircle2,
  ChevronRight,
  Clock,
  Copy,
  Eye,
  FileSpreadsheet,
  FileText,
  Loader2,
  MessageCircle,
  Paperclip,
  Pencil,
  RotateCcw,
  Send,
  Timer,
  Trash2,
  Users,
  XCircle,
} from 'lucide-react';
import Link from 'next/link';
import { useRouter } from 'next/navigation';
import { useState } from 'react';
import useSWR from 'swr';

import { EmptyState, ErrorState, StateBadge } from '@/components/analytics/Primitives';
import {
  Button,
  CampaignStatusBadge,
  Notice,
  TARGET_STATUS,
  formatEstimate,
} from '@/components/campaign/shared';
import { StoryThumbnail } from '@/components/campaign/StoryGrid';
import { ConfirmDialog, useConfirm } from '@/components/ui/ConfirmDialog';
import {
  ApiError,
  archiveCampaign,
  campaignReportPath,
  campaignTargetsPath,
  cancelCampaign,
  deleteCampaign,
  exportCampaignTargets,
  fetcher,
  resendCampaign,
  retryCampaign,
  runCampaign,
} from '@/lib/api';
import { useRealtimeEvent } from '@/lib/realtime';
import type { BroadcastReport, CampaignTarget, CampaignType } from '@/lib/types';

/**
 * One broadcast's own page.
 *
 * Everything here is a stored outcome rather than a derived guess: what went
 * out, from which number, when, and what came back. Where the system does not
 * know something it says so — a send that was interrupted is labelled unknown
 * rather than quietly retried.
 *
 * A page rather than the panel this used to be. A delivery log of four hundred
 * rows is a thing people read, sort and export, and a drawer sliding over the
 * list it came from is the wrong shape for that — it also had no URL, so nobody
 * could send a colleague the campaign that went wrong.
 */
export function CampaignDetailPage({ id, type }: { id: string; type: CampaignType }) {
  const isStory = type === 'story';
  const router = useRouter();
  const confirm = useConfirm();
  const home = isStory ? '/story' : '/broadcast';

  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [statusFilter, setStatusFilter] = useState('');

  const report = useSWR<{ broadcast?: BroadcastReport }>(campaignReportPath(id), fetcher, {
    refreshInterval: 15_000,
  });
  // A Story has no recipient list of ours — WhatsApp decides its audience from
  // the account's own privacy settings — so there is no delivery log to fetch.
  const targets = useSWR<{ targets: CampaignTarget[] }>(
    isStory ? null : campaignTargetsPath(id, statusFilter, 500, 0),
    fetcher,
  );

  useRealtimeEvent('campaign.updated', () => {
    void report.mutate();
    void targets.mutate();
  });

  const b = report.data?.broadcast;
  const c = b?.campaign;

  if (report.error) {
    return (
      <div className="px-5 py-6 lg:px-8 lg:py-8">
        <ErrorState
          message={report.error instanceof Error ? report.error.message : 'Gagal memuat.'}
          onRetry={() => void report.mutate()}
        />
      </div>
    );
  }
  if (!c) {
    return (
      <div className="px-5 py-6 lg:px-8 lg:py-8">
        <p className="flex items-center gap-2 text-sm text-ink-muted">
          <Loader2 className="size-4 animate-spin" />
          Memuat campaign…
        </p>
      </div>
    );
  }

  const t = b?.totals ?? null;
  const processed = t ? t.sent + t.failed : 0;
  const percent = t && t.total > 0 ? Math.round((processed / t.total) * 100) : 0;
  const remaining = t ? Math.max(0, t.total - processed) : 0;

  const isRunning = c.status === 'running';
  const runnable = ['draft', 'cancelled', 'failed', 'partial'].includes(c.status);
  const cancellable = ['draft', 'scheduled', 'running'].includes(c.status);
  const retryable = (t?.failed ?? 0) > 0;
  // Only a draft can be deleted — everything else has a send history, and a
  // record of what reached two hundred people is not somebody's to throw away
  // because they want a shorter list. Archive is the action for those, so the
  // button changes rather than staying and failing.
  const deletable = c.status === 'draft';
  // Editable until it starts. After that its content is a record of what people
  // received, and the answer to "I want it different" is Duplikat, not Edit.
  const editable =
    ['draft', 'scheduled'].includes(c.status) && !c.executed_at && !c.started_at;

  async function act(kind: string, run: () => Promise<unknown>) {
    setBusy(kind);
    setError(null);
    try {
      await run();
      await report.mutate();
      await targets.mutate();
    } catch (e) {
      setError(e instanceof ApiError || e instanceof Error ? e.message : 'Terjadi kesalahan.');
    } finally {
      setBusy(null);
    }
  }

  // Arrow consts rather than function declarations: a declaration is hoisted
  // above the `if (!c) return` guard, so TypeScript analyses it with `c` still
  // possibly undefined.
  const askResend = () => {
    if (!t) return; // Story: no recipient list to resend to
    // A second copy of a WhatsApp message cannot be unsent, so this asks first
    // and says the number out loud rather than hiding it behind "semua".
    confirm.ask({
      title: 'Kirim ulang ke semua penerima?',
      description: `${t.total.toLocaleString('id-ID')} penerima akan menerima pesan ini lagi, termasuk ${t.sent.toLocaleString('id-ID')} yang sudah menerimanya. Pesan yang sudah terkirim tidak bisa ditarik kembali.`,
      confirmLabel: 'Kirim ulang semua',
      tone: 'danger',
      icon: RotateCcw,
      onConfirm: () => act('resend', () => resendCampaign(id)),
    });
  };

  const askDelete = () => {
    confirm.ask({
      title: `Hapus ${c.name}?`,
      description:
        'Draft ini dihapus permanen beserta daftar penerimanya. Tidak ada yang pernah dikirim, jadi tidak ada riwayat yang hilang.',
      confirmLabel: 'Hapus',
      tone: 'danger',
      onConfirm: async () => {
        setBusy('delete');
        try {
          await deleteCampaign(id);
          router.push(home);
        } catch (e) {
          setError(e instanceof ApiError || e instanceof Error ? e.message : 'Terjadi kesalahan.');
          setBusy(null);
        }
      },
    });
  };

  const askArchive = () => {
    confirm.ask({
      title: `Arsipkan ${c.name}?`,
      description: isRunning
        ? 'Campaign dihentikan lebih dulu, lalu disembunyikan dari daftar. Laporan pengirimannya tetap tersimpan dan bisa dibuka lewat tautan ini.'
        : 'Campaign disembunyikan dari daftar. Laporan pengirimannya tetap tersimpan dan bisa dibuka lewat tautan ini.',
      confirmLabel: 'Arsipkan',
      // Not danger: nothing is destroyed, and a red dialog would teach people to
      // hesitate over the one action here that is safe.
      tone: 'neutral',
      icon: Archive,
      onConfirm: async () => {
        setBusy('archive');
        try {
          await archiveCampaign(id);
          router.push(home);
        } catch (e) {
          setError(e instanceof ApiError || e instanceof Error ? e.message : 'Terjadi kesalahan.');
          setBusy(null);
        }
      },
    });
  };

  return (
    <div className="px-5 py-6 lg:px-8 lg:py-8">
      {/* Where this page sits, and the way back out of it. */}
      <nav aria-label="Jalur" className="flex items-center gap-1.5 text-2xs text-ink-muted">
        <Link
          href={home}
          className="inline-flex items-center gap-1 transition-colors hover:text-ink"
        >
          <ArrowLeft className="size-3.5" />
          Kembali
        </Link>
        <ChevronRight className="size-3" aria-hidden />
        <Link href={home} className="transition-colors hover:text-ink">
          {isStory ? 'WA Story' : 'Broadcast'}
        </Link>
        <ChevronRight className="size-3" aria-hidden />
        <span className="text-ink-soft">Detail</span>
      </nav>

      <div className="mt-1 flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          <h1 className="flex flex-wrap items-center gap-2.5 text-2xl font-semibold tracking-[-0.02em] text-ink">
            {c.name}
            <CampaignStatusBadge status={c.status} isStory={isStory} />
          </h1>
          <p className="mt-1 text-xs text-ink-muted">
            {formatWhen(c.scheduled_at ?? c.created_at)}
            {c.creator_name ? ` · ${c.creator_name}` : ''}
          </p>
        </div>

        <div className="flex flex-wrap items-center gap-2">
          {runnable ? (
            <Button variant="primary" onClick={() => void act('run', () => runCampaign(id))} disabled={busy !== null}>
              {busy === 'run' ? <Loader2 className="size-3.5 animate-spin" /> : <Send className="size-3.5" />}
              Jalankan sekarang
            </Button>
          ) : null}
          {cancellable ? (
            <Button variant="danger" onClick={() => void act('cancel', () => cancelCampaign(id))} disabled={busy !== null}>
              {busy === 'cancel' ? <Loader2 className="size-3.5 animate-spin" /> : null}
              Batalkan
            </Button>
          ) : null}
          {retryable ? (
            <Button onClick={() => void act('retry', () => retryCampaign(id))} disabled={busy !== null}>
              {busy === 'retry' ? <Loader2 className="size-3.5 animate-spin" /> : <RotateCcw className="size-3.5" />}
              Coba ulang yang gagal
            </Button>
          ) : null}
          {t ? (
            <Button onClick={askResend} disabled={busy !== null || t.total === 0}>
              {busy === 'resend' ? <Loader2 className="size-3.5 animate-spin" /> : <RotateCcw className="size-3.5" />}
              Kirim Ulang Semua
            </Button>
          ) : null}
          {/*
           * Two doors into the composer, not one button that promised both and
           * delivered a read-only page. Edit re-opens this campaign; Duplikat
           * opens a copy of it. Neither writes anything on the way in — a row
           * appears, or changes, when the composer is saved.
           */}
          {editable ? (
            <Button onClick={() => router.push(`${home}/baru?edit=${id}`)} disabled={busy !== null}>
              <Pencil className="size-3.5" />
              Edit
            </Button>
          ) : null}
          <Button onClick={() => router.push(`${home}/baru?dari=${id}`)} disabled={busy !== null}>
            <Copy className="size-3.5" />
            Duplikat
          </Button>
          {deletable ? (
            <button
              type="button"
              onClick={askDelete}
              disabled={busy !== null}
              aria-label="Hapus draft"
              className="grid size-9 place-items-center rounded-control border border-danger/30 bg-surface-raised text-danger transition-colors hover:bg-danger-soft disabled:opacity-50"
            >
              {busy === 'delete' ? (
                <Loader2 className="size-4 animate-spin" />
              ) : (
                <Trash2 className="size-4" />
              )}
            </button>
          ) : (
            <button
              type="button"
              onClick={askArchive}
              disabled={busy !== null}
              aria-label="Arsipkan campaign"
              title="Arsipkan — laporannya tetap tersimpan"
              className="grid size-9 place-items-center rounded-control border border-hairline bg-surface-raised text-ink-soft transition-colors hover:bg-surface-sunken disabled:opacity-50"
            >
              {busy === 'archive' ? (
                <Loader2 className="size-4 animate-spin" />
              ) : (
                <Archive className="size-4" />
              )}
            </button>
          )}
        </div>
      </div>

      {error ? (
        <div className="mt-3">
          <Notice tone="danger">{error}</Notice>
        </div>
      ) : null}

      {c.cancel_requested && c.status === 'running' ? (
        <div className="mt-3">
          <Notice tone="warn">
            Pembatalan diminta; pengiriman berhenti setelah penerima yang sedang diproses.
          </Notice>
        </div>
      ) : null}

      {b && t ? (
        <>
      <div className="mt-4 grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
        <Stat icon={Users} label="Total Penerima" value={t.total} />
        <Stat icon={CheckCircle2} label="Terkirim" value={t.sent} tone="good" />
        <Stat icon={XCircle} label="Gagal" value={t.failed} tone={t.failed > 0 ? 'danger' : undefined} />
        <Stat icon={Clock} label="Sisa" value={remaining} tone={remaining > 0 ? 'warn' : undefined} />
      </div>

      <div className="mt-3 grid gap-3 sm:grid-cols-3">
        <Stat icon={Eye} label="Dibaca" value={t.read} tone="info" />
        <Stat icon={MessageCircle} label="Dibalas" value={b.replies} />
        <div className="rounded-card border border-hairline bg-surface-raised px-4 py-3 shadow-e1">
          <p className="flex items-center gap-1.5 text-2xs text-ink-muted">
            <Timer className="size-3.5" />
            Estimasi selesai
          </p>
          <p className="mt-1 text-lg font-semibold text-warn">
            {remaining === 0
              ? 'Selesai'
              : b.duration_seconds !== null
                ? formatEstimate(b.duration_seconds)
                : 'Belum diketahui'}
          </p>
        </div>
      </div>

      <section className="mt-3 rounded-card border border-hairline bg-surface-raised px-4 py-3.5 shadow-e1">
        <div className="flex items-center justify-between gap-3">
          <p className="flex items-center gap-1.5 text-sm font-semibold text-ink">
            <BarChart3 className="size-4 text-ink-muted" />
            Progress Pengiriman
          </p>
          <p className="nums text-sm font-semibold text-ink">{percent}%</p>
        </div>
        <span className="mt-2 block h-2 overflow-hidden rounded-full bg-surface-sunken">
          <span
            className={clsx('block h-full', t.failed > 0 ? 'bg-warn' : 'bg-brand-600')}
            style={{ width: `${percent}%` }}
          />
        </span>
        <div className="mt-1.5 flex flex-wrap items-center justify-between gap-2 text-2xs text-ink-muted">
          <span className="nums">
            {processed.toLocaleString('id-ID')} dari {t.total.toLocaleString('id-ID')} diproses
          </span>
          {c.started_at ? <span>Mulai: {formatWhen(c.started_at)}</span> : null}
        </div>
      </section>
        </>
      ) : null}

      <section className="mt-3 rounded-card border border-hairline bg-surface-raised px-4 py-3.5 shadow-e1">
        <p className="flex items-center gap-1.5 text-sm font-semibold text-ink">
          <MessageCircle className="size-4 text-ink-muted" />
          {isStory ? 'Isi Story' : 'Pesan'}
        </p>
        <p className="mt-2 rounded-lg bg-surface-sunken/60 px-3.5 py-3 text-xs whitespace-pre-wrap text-ink-soft">
          {c.message_template ?? c.body ?? '(tanpa teks)'}
        </p>
        {/* The attachment, shown rather than described. "Media: video" says
            there is one; the frame says which one, and that is what somebody
            checks before pressing Kirim Ulang Semua. */}
        {c.media_kind || c.media_url || c.media_file_name ? (
          <div className="mt-2 flex items-start gap-3 rounded-lg border border-hairline p-2">
            {c.media_url ? (
              <span className="w-16 shrink-0 overflow-hidden rounded-lg">
                <StoryThumbnail
                  url={c.media_url}
                  kind={c.media_kind}
                  caption=""
                  className="h-16"
                />
              </span>
            ) : null}
            <p className="min-w-0 flex-1 text-2xs text-ink-muted">
              <span className="flex items-center gap-1.5 text-ink-soft">
                <Paperclip className="size-3.5 shrink-0" />
                Media: {c.media_kind ?? 'lampiran'}
                {c.media_file_name ? ` · ${c.media_file_name}` : ''}
              </span>
              {c.media_url ? (
                <span className="mt-0.5 block break-all">{c.media_url}</span>
              ) : null}
            </p>
          </div>
        ) : null}
      </section>

      {b ? (
        <DeliveryLog
          id={id}
          rows={targets.data?.targets ?? []}
          loading={targets.isLoading}
          status={statusFilter}
          onStatus={setStatusFilter}
        />
      ) : null}

      <ConfirmDialog request={confirm.request} onClose={confirm.close} />
    </div>
  );
}

// The words come from TARGET_STATUS, not from here. A filter chip that says
// "Menunggu" filtering rows that say "Antre" is two names for one state, and the
// person reading the table has no way to know they are the same.
const TARGET_FILTERS = [
  { id: '', label: 'Semua' },
  ...(['sent', 'failed', 'pending', 'retry_wait', 'cancelled'] as const).map((id) => ({
    id,
    label: TARGET_STATUS[id].label,
  })),
];

function DeliveryLog({
  id,
  rows,
  loading,
  status,
  onStatus,
}: {
  id: string;
  rows: CampaignTarget[];
  loading: boolean;
  status: string;
  onStatus: (next: string) => void;
}) {
  const [busy, setBusy] = useState<'csv' | 'xlsx' | null>(null);
  const [error, setError] = useState<string | null>(null);

  async function download(format: 'csv' | 'xlsx') {
    setBusy(format);
    setError(null);
    try {
      // The whole log, not the rows on screen: a file called "log pengiriman"
      // that silently holds the first page is worse than no file.
      await exportCampaignTargets(id, format);
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Ekspor gagal.');
    } finally {
      setBusy(null);
    }
  }

  return (
    <section className="mt-3 rounded-card border border-hairline bg-surface-raised shadow-e1">
      <div className="flex flex-wrap items-center justify-between gap-2 border-b border-hairline px-4 py-3">
        <p className="flex items-center gap-1.5 text-sm font-semibold text-ink">
          <Send className="size-4 text-ink-muted" />
          Log Pengiriman
          <span className="font-normal text-ink-muted">({rows.length} baris)</span>
        </p>
        <div className="flex items-center gap-2">
          <Button onClick={() => void download('csv')} disabled={busy !== null}>
            {busy === 'csv' ? <Loader2 className="size-3.5 animate-spin" /> : <FileText className="size-3.5" />}
            CSV
          </Button>
          <Button onClick={() => void download('xlsx')} disabled={busy !== null}>
            {busy === 'xlsx' ? (
              <Loader2 className="size-3.5 animate-spin" />
            ) : (
              <FileSpreadsheet className="size-3.5" />
            )}
            Excel
          </Button>
        </div>
      </div>

      <div className="flex flex-wrap gap-1 border-b border-hairline px-4 py-2">
        {TARGET_FILTERS.map((f) => (
          <button
            key={f.id}
            type="button"
            onClick={() => onStatus(f.id)}
            className={clsx(
              'rounded-lg px-2.5 py-1 text-xs transition-colors',
              status === f.id
                ? 'bg-brand-600/10 font-medium text-brand-800'
                : 'text-ink-muted hover:bg-surface-sunken',
            )}
          >
            {f.label}
          </button>
        ))}
      </div>

      {error ? (
        <div className="px-4 py-3">
          <Notice tone="danger">{error}</Notice>
        </div>
      ) : null}

      {loading && rows.length === 0 ? (
        <p className="px-4 py-8 text-center text-xs text-ink-muted">Memuat…</p>
      ) : rows.length === 0 ? (
        <div className="p-6">
          <EmptyState title="Tidak ada penerima pada filter ini." />
        </div>
      ) : (
        <div className="max-h-[70vh] overflow-auto">
          <table className="w-full min-w-[820px] border-collapse text-sm">
            <thead>
              <tr className="border-b border-hairline text-ink-muted">
                <Th>Nama</Th>
                <Th className="w-56">Nomor / ID Grup</Th>
                <Th className="w-32">Status</Th>
                <Th>Catatan</Th>
                <Th className="w-40">Waktu</Th>
              </tr>
            </thead>
            <tbody>
              {rows.map((t) => {
                const s = TARGET_STATUS[t.status] ?? { label: t.status, tone: 'neutral' as const };
                return (
                  <tr key={t.id} className="border-b border-hairline last:border-0">
                    <Td className="font-medium text-ink">
                      {t.display_name || <span className="text-ink-muted">—</span>}
                    </Td>
                    <Td className="nums text-xs text-ink-soft">
                      {t.target_type === 'group' ? t.chat_jid : (t.phone_number ?? t.chat_jid)}
                    </Td>
                    <Td>
                      <StateBadge label={s.label} tone={s.tone} />
                    </Td>
                    <Td className="text-xs text-ink-muted">
                      {t.failure_reason ? (
                        <span className="text-danger">{t.failure_reason}</span>
                      ) : (
                        <span className="nums break-all">{t.wa_message_id ?? '—'}</span>
                      )}
                      {t.attempt > 1 ? (
                        <span className="ml-1 text-ink-muted">· percobaan {t.attempt}</span>
                      ) : null}
                    </Td>
                    <Td className="text-xs text-ink-soft">
                      {t.sent_at ? formatWhen(t.sent_at) : '—'}
                    </Td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}

      {/* How much of the log is on screen.
          The server sends a bounded page of recipients, so a count that only
          described what arrived would present a slice as the whole. Exporting
          is the way to the rest, and the buttons for it are at the top. */}
      {rows.length > 0 ? (
        <p className="border-t border-hairline px-4 py-2.5 text-2xs text-ink-muted">
          Menampilkan <span className="nums text-ink-soft">{rows.length.toLocaleString('id-ID')}</span>{' '}
          baris pada filter ini. Unduh CSV atau Excel untuk seluruh log.
        </p>
      ) : null}
    </section>
  );
}

function Stat({
  icon: Icon,
  label,
  value,
  tone,
}: {
  icon: typeof Users;
  label: string;
  value: number;
  tone?: 'good' | 'warn' | 'danger' | 'info';
}) {
  return (
    <div className="rounded-card border border-hairline bg-surface-raised px-4 py-3 shadow-e1">
      <p className="flex items-center gap-1.5 text-2xs text-ink-muted">
        <Icon className="size-3.5" />
        {label}
      </p>
      <p
        className={clsx(
          'nums mt-1 text-2xl font-semibold',
          tone === 'good'
            ? 'text-brand-700'
            : tone === 'warn'
              ? 'text-warn'
              : tone === 'danger'
                ? 'text-danger'
                : tone === 'info'
                  ? 'text-info'
                  : 'text-ink',
        )}
      >
        {value.toLocaleString('id-ID')}
      </p>
    </div>
  );
}

function Th({ children, className }: { children?: React.ReactNode; className?: string }) {
  return (
    <th
      scope="col"
      // Pinned to the top of the scroll box. The border lives on the cell
      // because a border on the row does not travel with a sticky cell.
      className={clsx(
        'sticky top-0 z-10 h-[40px] border-b border-hairline bg-surface-raised px-4 text-left text-2xs font-semibold tracking-wide uppercase',
        className,
      )}
    >
      {children}
    </th>
  );
}

function Td({ children, className }: { children?: React.ReactNode; className?: string }) {
  return <td className={clsx('px-4 py-2.5 align-middle', className)}>{children}</td>;
}

function formatWhen(iso: string): string {
  return new Date(iso).toLocaleString('id-ID', {
    day: 'numeric',
    month: 'short',
    year: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
    timeZone: 'Asia/Jakarta',
  });
}
