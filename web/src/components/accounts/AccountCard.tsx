'use client';

import { Check, Copy, Info, Link2, MessageCircle, RefreshCw, Trash2, Unlink } from 'lucide-react';
import { useState } from 'react';

import { AppLogo } from '@/components/ui/AppLogo';
import { Button } from '@/components/ui/Button';
import { Badge, StatusPill } from '@/components/ui/Primitives';
import { ACCOUNT_STATUS_LABEL, accountStatusTone, formatRelative } from '@/lib/format';
import type { Account } from '@/lib/types';
import { useAppIcon } from '@/lib/useAppIcon';

/**
 * The application's logo, or a coloured monogram when it has none.
 *
 * The logo is looked up by code from the shared application list, so every
 * screen that draws this picks up an upload without being told about it.
 */
export function AppMark({
  code,
  color,
  size = 44,
}: {
  code: string | null;
  color: string | null;
  size?: number;
}) {
  const icon = useAppIcon(code);
  const label = (code ?? '-').slice(0, 2).toUpperCase();
  const monogram = (
    <span
      style={{
        width: size,
        height: size,
        backgroundColor: `${color ?? '#4e8064'}1A`,
        color: color ?? '#4e8064',
        fontSize: Math.round(size * 0.32),
      }}
      className="inline-grid shrink-0 place-items-center rounded-xl font-bold tracking-tight"
      aria-hidden
    >
      {label}
    </span>
  );
  return <AppLogo src={icon} size={size} fallback={monogram} />;
}

interface AccountCardProps {
  account: Account;
  busy?: boolean;
  syncing?: boolean;
  onConnect: (account: Account) => void;
  onDisconnect: (account: Account) => void;
  onSync: (account: Account) => void;
  onDetail: (account: Account) => void;
  onDelete: (account: Account) => void;
}

/**
 * One card in the account grid (reference screen 1).
 *
 * The primary action flips between "Putuskan" and "Hubungkan" depending on
 * whether a live session exists.
 */
export function AccountCard({
  account,
  busy = false,
  syncing = false,
  onConnect,
  onDisconnect,
  onSync,
  onDetail,
  onDelete,
}: AccountCardProps) {
  const [action, setAction] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);
  const tone = accountStatusTone(account.status);
  const live = account.status === 'connected' || account.status === 'connecting';
  const connected = account.status === 'connected';

  const run = (name: string, fn: () => void) => {
    setAction(name);
    fn();
  };

  return (
    <article className="rounded-card border border-hairline shadow-e1 transition-shadow hover:shadow-e2 bg-surface-raised p-5 shadow-[0_1px_2px_rgba(28,25,23,0.04)]">
      <div className="flex items-start gap-3">
        <AppMark code={account.application_code} color={account.application_color} />

        <div className="min-w-0 flex-1">
          <div className="flex items-start justify-between gap-3">
            <h3 className="truncate text-base font-semibold text-ink">{account.name}</h3>
            <StatusPill
              label={ACCOUNT_STATUS_LABEL[account.status]}
              dotClass={tone.dot}
              textClass={tone.text}
              bgClass={tone.bg}
            />
          </div>

          <div className="mt-2 flex flex-wrap items-center gap-1.5">
            {account.label ? (
              <Badge className="uppercase">{account.label}</Badge>
            ) : account.application_code ? (
              <Badge className="uppercase">{account.application_code}</Badge>
            ) : (
              <Badge tone="amber">Tanpa aplikasi</Badge>
            )}
            {account.phone_number ? <Badge>{account.phone_number}</Badge> : null}
            <Badge tone="green">
              {account.connection_method === 'waba' ? 'WABA' : 'QR'}
            </Badge>
          </div>

          <p className="mt-2 font-mono text-2xs tracking-wide text-ink-muted">
            {account.device_id}
          </p>
        </div>
      </div>

      {/* The number, with the one thing anybody ever does to it. It gets
          pasted into a form, a chat, or a message to a colleague, and typing
          fifteen digits from a screen is how a digit gets lost. */}
      <div className="mt-4 flex items-center gap-3 rounded-xl bg-surface-sunken px-4 py-3">
        <span className="grid size-9 shrink-0 place-items-center rounded-full bg-surface-raised text-brand-700">
          <MessageCircle className="size-4" />
        </span>
        <div className="min-w-0 flex-1">
          <p className="text-2xs text-ink-muted">Nomor WhatsApp</p>
          <p className="nums truncate text-lg font-semibold text-ink">
            {account.phone_number ?? 'Belum tertaut'}
          </p>
        </div>
        {account.phone_number ? (
          <button
            type="button"
            onClick={() => {
              void navigator.clipboard?.writeText(account.phone_number ?? '');
              setCopied(true);
              window.setTimeout(() => setCopied(false), 1500);
            }}
            aria-label="Salin nomor"
            title={copied ? 'Tersalin' : 'Salin nomor'}
            className="shrink-0 rounded-control p-1.5 text-ink-muted transition-colors hover:bg-surface-raised hover:text-ink"
          >
            {copied ? <Check className="size-4 text-brand-700" /> : <Copy className="size-4" />}
          </button>
        ) : null}
      </div>

      {account.status_detail ? (
        <p className="mt-2 text-xs text-ink-muted">{account.status_detail}</p>
      ) : null}

      {connected ? (
        <div className="mt-3 flex flex-wrap items-center justify-between gap-2 rounded-xl border border-hairline px-3 py-2">
          <p className="text-2xs text-ink-muted">
            Sinkron terakhir:{' '}
            <span className="font-medium text-ink-soft">
              {formatRelative(account.last_synced_at)}
            </span>
            {' · '}riwayat {account.sync_window_days} hari
          </p>
          <Button
            size="sm"
            icon={<RefreshCw className={`size-4 ${syncing ? 'animate-spin' : ''}`} />}
            loading={false}
            disabled={syncing}
            onClick={() => onSync(account)}
            title="Tarik kontak, grup, label, status baca, dan riwayat chat 7 hari terakhir"
          >
            {syncing ? 'Menyinkron…' : 'Sinkron'}
          </Button>
        </div>
      ) : null}

      <div className="mt-4 flex flex-wrap items-center gap-2">
        {live ? (
          <Button
            className="min-w-0 flex-1"
            icon={<Unlink className="size-4" />}
            loading={busy && action === 'disconnect'}
            onClick={() => run('disconnect', () => onDisconnect(account))}
          >
            Putuskan
          </Button>
        ) : (
          <Button
            className="min-w-0 flex-1"
            variant="primary"
            icon={<Link2 className="size-4" />}
            loading={busy && action === 'connect'}
            onClick={() => run('connect', () => onConnect(account))}
          >
            Hubungkan
          </Button>
        )}

        <Button icon={<Info className="size-4" />} onClick={() => onDetail(account)}>
          Detail
        </Button>
        <Button
          variant="danger"
          icon={<Trash2 className="size-4" />}
          loading={busy && action === 'delete'}
          onClick={() => run('delete', () => onDelete(account))}
        >
          Hapus
        </Button>
      </div>
    </article>
  );
}
