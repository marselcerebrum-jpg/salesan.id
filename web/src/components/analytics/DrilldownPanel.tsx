'use client';

import clsx from 'clsx';
import { X } from 'lucide-react';
import { useEffect } from 'react';
import useSWR from 'swr';

import {
  EmptyState,
  ErrorState,
  SOURCE_LABEL,
  StateBadge,
  formatDuration,
  formatTime,
} from '@/components/analytics/Primitives';
import { analyticsPath, fetcher, type AnalyticsQuery } from '@/lib/api';
import type {
  FollowUpRow,
  GroupMentionRow,
  LabelEventRow,
  LabelTransition,
  LeadRow,
  MessageActivityRow,
  SLACycleRow,
} from '@/lib/types';

/**
 * The drawer behind every number.
 *
 * A figure somebody is measured on has to be openable, or it is a claim rather
 * than a finding. Each list applies the same restrictions as the aggregate it
 * came from, so the rows and the number always agree.
 */

export type DrilldownKind =
  | { kind: 'messages'; direction: 'in' | 'out' | 'device'; chatType?: 'personal' | 'group'; title: string }
  | { kind: 'sla'; status?: string }
  | { kind: 'follow-ups'; status?: string }
  | { kind: 'group-mentions'; answered?: string }
  | { kind: 'label-events' }
  | { kind: 'leads'; status?: string };

export function DrilldownPanel({
  target,
  query,
  onClose,
}: {
  target: DrilldownKind | null;
  query: AnalyticsQuery;
  onClose: () => void;
}) {
  useEffect(() => {
    if (!target) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose();
    };
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, [target, onClose]);

  const extra: Record<string, string> = {};
  if (target?.kind === 'messages') {
    extra.direction = target.direction;
    if (target.chatType) extra.chat_type = target.chatType;
  }
  if (target && 'answered' in target && target.answered) extra.answered = target.answered;
  if (target && 'status' in target && target.status) {
    // The follow-up endpoint filters on whether the customer wrote back, which
    // is a yes/no question rather than a status; every other list uses `status`.
    if (target.kind === 'follow-ups') extra.replied = target.status;
    else extra.status = target.status;
  }

  const path = target ? analyticsPath(target.kind, query, extra) : null;
  const { data, error, isLoading, mutate } = useSWR<Record<string, unknown>>(path, fetcher);

  return (
    <>
      <div
        aria-hidden={!target}
        onClick={onClose}
        className={clsx(
          'fixed inset-0 z-40 bg-ink/30 transition-opacity',
          target ? 'opacity-100' : 'pointer-events-none opacity-0',
        )}
      />
      <aside
        aria-hidden={!target}
        className={clsx(
          'fixed inset-y-0 right-0 z-50 flex w-full max-w-[720px] flex-col bg-surface shadow-e4 transition-transform',
          target ? 'translate-x-0' : 'translate-x-full',
        )}
      >
        <header className="flex items-center justify-between gap-3 border-b border-hairline bg-surface-raised px-5 py-3.5">
          <div>
            <h2 className="text-lg font-semibold tracking-[-0.01em] text-ink">{target ? titleOf(target) : ''}</h2>
            <p className="text-xs text-ink-muted">Mengikuti filter halaman di belakangnya.</p>
          </div>
          <button
            type="button"
            onClick={onClose}
            aria-label="Tutup"
            className="rounded-lg p-1.5 text-ink-muted transition-colors hover:bg-surface-sunken"
          >
            <X className="size-5" />
          </button>
        </header>

        <div className="flex-1 overflow-y-auto px-5 py-4">
          {error ? (
            <ErrorState
              message={error instanceof Error ? error.message : 'Terjadi kesalahan.'}
              onRetry={() => void mutate()}
            />
          ) : isLoading ? (
            <RowSkeleton />
          ) : target ? (
            <Body target={target} data={data} />
          ) : null}
        </div>
      </aside>
    </>
  );
}

function titleOf(t: DrilldownKind): string {
  switch (t.kind) {
    case 'messages':
      return t.title;
    case 'sla':
      return 'Waktu Respons Pertama';
    case 'follow-ups':
      return 'Follow-up';
    case 'group-mentions':
      return 'Nomor disebut di grup';
    case 'label-events':
      return 'Perubahan Label';
    case 'leads':
      return 'Leads Baru';
  }
}

function Body({
  target,
  data,
}: {
  target: DrilldownKind;
  data: Record<string, unknown> | undefined;
}) {
  switch (target.kind) {
    case 'messages':
      return <MessageList rows={(data?.messages as MessageActivityRow[]) ?? []} />;
    case 'sla':
      return <SLAList rows={(data?.cycles as SLACycleRow[]) ?? []} />;
    case 'follow-ups':
      return <FollowUpList rows={(data?.follow_ups as FollowUpRow[]) ?? []} />;
    case 'group-mentions':
      return <MentionList rows={(data?.mentions as GroupMentionRow[]) ?? []} />;
    case 'label-events':
      return (
        <LabelList
          rows={(data?.events as LabelEventRow[]) ?? []}
          transitions={(data?.transitions as LabelTransition[]) ?? []}
          unattributed={(data?.unattributed as number) ?? 0}
        />
      );
    case 'leads':
      return <LeadList rows={(data?.leads as LeadRow[]) ?? []} />;
  }
}

function MessageList({ rows }: { rows: MessageActivityRow[] }) {
  if (rows.length === 0) return <EmptyState title="Belum ada pesan pada filter ini." />;
  return (
    <ul className="space-y-2">
      {rows.map((r) => (
        <li key={r.id} className="rounded-card border border-hairline bg-surface-raised px-3.5 py-3">
          <div className="flex items-start justify-between gap-3">
            <div className="min-w-0">
              <p className="truncate text-sm font-medium text-ink">
                {r.conversation_name ?? r.phone_number ?? 'Tanpa nama'}
              </p>
              <p className="truncate text-xs text-ink-muted">
                {[
                  r.chat_type === 'group' ? 'Grup' : 'Chat pribadi',
                  r.application_code,
                  r.account_name,
                ]
                  .filter(Boolean)
                  .join(' · ')}
              </p>
            </div>
            <span className="shrink-0 text-xs text-ink-muted">
              {formatTime(r.timestamp)}
            </span>
          </div>

          {r.preview ? (
            <p className="mt-2 line-clamp-2 rounded-lg bg-surface-sunken px-2.5 py-1.5 text-sm text-ink-soft">
              {r.preview}
            </p>
          ) : (
            <p className="mt-2 text-xs text-ink-muted italic">[{r.message_type}]</p>
          )}

          <div className="mt-2 flex flex-wrap items-center gap-2 text-xs text-ink-muted">
            <span>
              {r.actor_name
                ? `Oleh ${r.actor_name}`
                : r.source
                  ? SOURCE_LABEL[r.source] ?? r.source
                  : 'Dari pelanggan'}
            </span>
            {r.in_schedule === false ? <StateBadge label="Di Luar Jadwal" tone="warn" /> : null}
            {r.in_schedule === true ? <StateBadge label="Di Dalam Jadwal" tone="good" /> : null}
          </div>
        </li>
      ))}
    </ul>
  );
}

const SLA_LABEL: Record<string, { text: string; tone: 'good' | 'warn' | 'danger' | 'neutral' }> = {
  achieved: { text: 'SLA Tercapai', tone: 'good' },
  breached: { text: 'SLA Terlewati', tone: 'danger' },
  waiting: { text: 'Menunggu Balasan', tone: 'warn' },
  excluded: { text: 'Dikecualikan', tone: 'neutral' },
};

function SLAList({ rows }: { rows: SLACycleRow[] }) {
  if (rows.length === 0) return <EmptyState title="Belum ada siklus respons pada filter ini." />;
  return (
    <ul className="space-y-2">
      {rows.map((r) => {
        const badge = SLA_LABEL[r.status] ?? { text: r.status, tone: 'neutral' as const };
        // The duration shown is the one the cycle was actually judged on:
        // business-hours time when it was scored that way, wall clock otherwise.
        const measured = r.business_duration_seconds ?? r.raw_duration_seconds;
        return (
          <li key={r.id} className="rounded-card border border-hairline bg-surface-raised px-3.5 py-3">
            <div className="flex items-start justify-between gap-3">
              <div className="min-w-0">
                <p className="truncate text-sm font-medium text-ink">
                  {r.conversation_name ?? r.phone_number ?? 'Tanpa nama'}
                </p>
                <p className="truncate text-xs text-ink-muted">
                  {[r.application_code, r.account_name].filter(Boolean).join(' · ')}
                </p>
              </div>
              <StateBadge label={badge.text} tone={badge.tone} />
            </div>

            <dl className="mt-2 grid grid-cols-2 gap-x-4 gap-y-1 text-xs sm:grid-cols-4">
              <Field label="Mulai" value={formatTime(r.started_at)} />
              <Field label="Dibalas" value={formatTime(r.responded_at)} />
              <Field label="Durasi" value={formatDuration(measured)} />
              {/* The target is the snapshot taken when the cycle opened, so
                  changing the setting later never rescores this row. */}
              <Field label="Target" value={formatDuration(r.target_seconds)} />
            </dl>

            <p className="mt-2 text-xs text-ink-muted">
              {r.inbound_message_count > 1
                ? `${r.inbound_message_count} pesan pelanggan dalam satu siklus · `
                : ''}
              {r.responder_name
                ? `dijawab ${r.responder_name}`
                : r.responder_source
                  ? `dijawab dari ${SOURCE_LABEL[r.responder_source] ?? r.responder_source}`
                  : 'belum dijawab'}
            </p>
          </li>
        );
      })}
    </ul>
  );
}

function FollowUpList({ rows }: { rows: FollowUpRow[] }) {
  if (rows.length === 0) return <EmptyState title="Belum ada follow-up pada filter ini." />;
  return (
    <ul className="space-y-2">
      {rows.map((r) => (
        <li key={r.id} className="rounded-card border border-hairline bg-surface-raised px-3.5 py-3">
          <div className="flex items-start justify-between gap-3">
            <div className="min-w-0">
              <p className="truncate text-sm font-medium text-ink">
                {r.conversation_name ?? r.phone_number ?? 'Tanpa nama'}
              </p>
              <p className="truncate text-xs text-ink-muted">
                {[r.application_code, r.local_date].filter(Boolean).join(' · ')}
              </p>
            </div>
            <StateBadge
              label={r.responded_at ? 'Dibalas' : 'Belum dibalas'}
              tone={r.responded_at ? 'good' : 'warn'}
            />
          </div>
          <p className="mt-2 text-xs text-ink-muted">
            {formatTime(r.started_at)}
            {r.message_count > 1 ? ` · ${r.message_count} bubble` : ''}
            {r.admin_name
              ? ` · oleh ${r.admin_name}`
              : r.admin_source
                ? ` · ${SOURCE_LABEL[r.admin_source] ?? r.admin_source}`
                : ''}
          </p>
        </li>
      ))}
    </ul>
  );
}

/** One labelled figure inside a drill-down row. */
function Field({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <dt className="text-ink-muted">{label}</dt>
      <dd className="text-ink-soft nums">{value}</dd>
    </div>
  );
}

function MentionList({ rows }: { rows: GroupMentionRow[] }) {
  if (rows.length === 0) return <EmptyState title="Belum ada mention pada filter ini." />;
  return (
    <ul className="space-y-2">
      {rows.map((r) => (
        <li key={r.id} className="rounded-card border border-hairline bg-surface-raised px-3.5 py-3">
          <div className="flex items-start justify-between gap-3">
            <div className="min-w-0">
              <p className="truncate text-sm font-medium text-ink">{r.group_name ?? 'Grup'}</p>
              <p className="truncate text-xs text-ink-muted">
                {r.sender_name ?? r.sender_phone ?? 'Anggota'} ·{' '}
                {[r.application_code, r.account_name].filter(Boolean).join(' · ')}
              </p>
            </div>
            <StateBadge
              label={r.responded_at ? 'Sudah ditanggapi' : 'Belum ditanggapi'}
              tone={r.responded_at ? 'good' : 'warn'}
            />
          </div>
          {r.body ? (
            <p className="mt-2 line-clamp-2 rounded-lg bg-surface-sunken px-2.5 py-1.5 text-sm text-ink-soft">
              {r.body}
            </p>
          ) : null}
          <p className="mt-2 text-xs text-ink-muted">
            {formatTime(r.mentioned_at)}
            {r.responder_name ? ` · dijawab ${r.responder_name}` : ''}
          </p>
        </li>
      ))}
    </ul>
  );
}

const LABEL_EVENT_TEXT: Record<string, string> = {
  label_created: 'Label dibuat',
  label_updated: 'Label diubah',
  label_deleted: 'Label dihapus',
  label_assigned: 'Label dipasang',
  label_removed: 'Label dilepas',
  label_moved: 'Label dipindah',
};

const SOURCE_TEXT: Record<string, string> = {
  web: 'Web',
  whatsapp: 'HP WhatsApp',
  system: 'Sistem',
};

function LabelList({
  rows,
  transitions,
  unattributed = 0,
}: {
  rows: LabelEventRow[];
  transitions: LabelTransition[];
  /** Changes hidden because WhatsApp does not say who made them. */
  unattributed?: number;
}) {
  return (
    <div className="space-y-5">
      {transitions.length > 0 ? (
        <section>
          <h3 className="mb-2 text-base font-semibold text-ink">Perpindahan label</h3>
          <ul className="flex flex-wrap gap-2">
            {transitions.map((t) => (
              <li
                key={`${t.from_label}-${t.to_label}`}
                className="rounded-full border border-hairline bg-surface-raised px-3 py-1 text-xs text-ink-soft"
              >
                {t.from_label} → {t.to_label}
                <span className="ml-1.5 font-semibold text-ink nums">{t.count}</span>
                <span className="ml-1 text-ink-muted">({t.contacts} kontak)</span>
              </li>
            ))}
          </ul>
        </section>
      ) : null}

      <section>
        <h3 className="mb-2 text-base font-semibold text-ink">Riwayat</h3>
        {/* What this person's figures leave out. A change made on the phone
            names nobody, so it is counted for the number, never for a person;
            saying so here keeps an empty list from reading as "nothing
            happened". */}
        {unattributed > 0 ? (
          <p className="mb-2 rounded-lg border border-hairline-strong bg-surface-sunken px-3 py-2 text-xs leading-relaxed text-ink-soft">
            <span className="font-medium text-ink">
              {unattributed.toLocaleString('id-ID')} perubahan
            </span>{' '}
            lain pada periode ini dilakukan lewat HP. WhatsApp tidak menyebut siapa pelakunya,
            jadi perubahan itu tidak dihitung ke orang ini; angkanya tetap ada di tampilan
            gabungan nomor tersebut.
          </p>
        ) : null}
        {rows.length === 0 ? (
          <EmptyState title="Belum ada perubahan label pada filter ini." />
        ) : (
          <ul className="space-y-1.5">
            {rows.map((r) => (
              <li
                key={r.id}
                className="flex items-start justify-between gap-3 rounded-card border border-hairline bg-surface-raised px-3.5 py-2.5"
              >
                <div className="min-w-0">
                  <p className="text-sm text-ink">
                    <span className="font-medium">
                      {LABEL_EVENT_TEXT[r.event_type] ?? r.event_type}
                    </span>
                    {r.to_label_name ? (
                      <>
                        {' '}
                        <span className="rounded bg-surface-sunken px-1.5 py-0.5 text-xs">
                          {r.to_label_name}
                        </span>
                      </>
                    ) : null}
                    {r.from_label_name ? (
                      <>
                        {' '}
                        <span className="rounded bg-surface-sunken px-1.5 py-0.5 text-xs line-through">
                          {r.from_label_name}
                        </span>
                      </>
                    ) : null}
                  </p>
                  <p className="truncate text-xs text-ink-muted">
                    {r.contact_name ?? r.phone_number ?? 'Tanpa kontak'} ·{' '}
                    {SOURCE_TEXT[r.source] ?? r.source}
                    {r.admin_name ? ` · ${r.admin_name}` : ''}
                  </p>
                </div>
                <span className="shrink-0 text-xs text-ink-muted">
                  {formatTime(r.occurred_at)}
                </span>
              </li>
            ))}
          </ul>
        )}
      </section>
    </div>
  );
}

const LEAD_LABEL: Record<string, string> = {
  verified_new: 'Baru terverifikasi',
  historical: 'Historis',
  unknown: 'Belum terverifikasi',
};

function LeadList({ rows }: { rows: LeadRow[] }) {
  if (rows.length === 0) return <EmptyState title="Belum ada leads pada filter ini." />;
  return (
    <ul className="space-y-2">
      {rows.map((r) => (
        <li
          key={r.contact_id}
          className="rounded-card border border-hairline bg-surface-raised px-3.5 py-3"
        >
          <div className="flex items-start justify-between gap-3">
            <div className="min-w-0">
              <p className="truncate text-sm font-medium text-ink">
                {r.name ?? r.phone_number ?? 'Tanpa nama'}
              </p>
              <p className="text-xs text-ink-muted">
                {[r.application_code, r.account_name].filter(Boolean).join(' · ')}
              </p>
            </div>
            <StateBadge
              label={LEAD_LABEL[r.lead_status] ?? r.lead_status}
              tone={r.lead_status === 'verified_new' ? 'good' : 'neutral'}
            />
          </div>
          <p className="mt-2 text-xs text-ink-soft">{r.status_reason}</p>
          <p className="mt-1 text-xs text-ink-muted">
            Pesan masuk pertama: {formatTime(r.first_inbound_at)}
          </p>
        </li>
      ))}
    </ul>
  );
}

function RowSkeleton() {
  return (
    <ul className="space-y-2">
      {Array.from({ length: 5 }).map((_, i) => (
        <li key={i} className="rounded-card border border-hairline bg-surface-raised px-3.5 py-4">
          <div className="h-4 w-40 animate-pulse rounded bg-surface-sunken" />
          <div className="mt-2 h-3 w-56 animate-pulse rounded bg-surface-sunken" />
          <div className="mt-3 h-3 w-full animate-pulse rounded bg-surface-sunken" />
        </li>
      ))}
    </ul>
  );
}
