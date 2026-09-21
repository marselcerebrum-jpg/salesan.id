'use client';

import { AlertTriangle, CheckCircle2, Loader2, RefreshCw } from 'lucide-react';
import { QRCodeSVG } from 'qrcode.react';
import { useCallback, useEffect, useRef, useState } from 'react';

import { Modal } from '@/components/ui/Modal';
import { ErrorNote } from '@/components/ui/Primitives';
import { pairAccount } from '@/lib/api';
import { useRealtimeEvent } from '@/lib/realtime';
import type { Account, AccountQRPayload, AccountStatusPayload } from '@/lib/types';

interface QrModalProps {
  account: Account | null;
  onClose: () => void;
  /** Fired once the device reports itself paired. */
  onLinked: (accountId: string) => void;
}

type Phase = 'requesting' | 'waiting' | 'expired' | 'linked' | 'error';

/**
 * Reference screen 3 — live QR pairing.
 *
 * The first code arrives in the POST /pair response; every rotation after that
 * is pushed over the realtime socket, so the code refreshes itself without a
 * reload. `account.status` transitioning to connecting/connected is what closes
 * the dialog.
 */
export function QrModal({ account, onClose, onLinked }: QrModalProps) {
  const [qr, setQr] = useState('');
  const [phase, setPhase] = useState<Phase>('requesting');
  const [error, setError] = useState<string | null>(null);
  const closeTimer = useRef<number | undefined>(undefined);

  const accountId = account?.id ?? null;

  // Cancel the pending auto-close when the dialog goes away, whichever way it
  // went away.
  useEffect(() => {
    return () => {
      if (closeTimer.current !== undefined) window.clearTimeout(closeTimer.current);
    };
  }, []);

  const requestQR = useCallback(async () => {
    if (!accountId) return;
    setPhase('requesting');
    setError(null);
    try {
      const res = await pairAccount(accountId);
      setQr(res.qr ?? '');
      // An empty code is not a failure: whatsmeow simply had not emitted one
      // within the request window, and it will arrive over the socket.
      setPhase('waiting');
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Gagal memulai penautan.');
      setPhase('error');
    }
  }, [accountId]);

  useEffect(() => {
    if (!accountId) return;
    setQr('');
    void requestQR();
  }, [accountId, requestQR]);

  useRealtimeEvent<AccountQRPayload>('account.qr', (payload) => {
    if (!accountId || payload.account_id !== accountId) return;
    if (payload.expired) {
      setQr('');
      setPhase('expired');
      return;
    }
    if (payload.qr) {
      setQr(payload.qr);
      setPhase('waiting');
    }
  });

  useRealtimeEvent<AccountStatusPayload>('account.status', (payload) => {
    if (!accountId || payload.account_id !== accountId) return;

    if (payload.status === 'connected' || payload.status === 'connecting') {
      setPhase('linked');
      onLinked(accountId);
      // Let the success state be visible for a beat before closing. The handle
      // is kept so the effect below can cancel it: without that, closing the
      // dialog by hand inside those 1.4 seconds and reopening it immediately
      // would have the old timer close the new dialog.
      closeTimer.current = window.setTimeout(onClose, 1400);
      return;
    }
    if (payload.status === 'error') {
      setError(payload.status_detail ?? 'Penautan gagal.');
      setPhase('error');
    }
  });

  return (
    <Modal
      open={Boolean(account)}
      onClose={onClose}
      title={`Hubungkan WhatsApp: ${account?.name ?? 'Akun Baru'}`}
      size="sm"
    >
      <div className="space-y-4">
        <div className="flex gap-2.5 rounded-xl border border-warn/30 bg-amber-badge/10 px-3.5 py-3">
          <AlertTriangle className="mt-0.5 size-4 shrink-0 text-warn" aria-hidden />
          <p className="text-xs leading-relaxed text-warn">
            Gunakan nomor WhatsApp yang memang aktif dipakai sehari-hari. QR ini hanya untuk
            menautkan nomor ke salesan.id, bukan mengganti akun WhatsApp kamu.
          </p>
        </div>

        <div className="flex flex-col items-center gap-3">
          <div className="grid size-[232px] place-items-center rounded-xl border border-hairline bg-surface-raised p-3">
            {phase === 'linked' ? (
              <div className="flex flex-col items-center gap-2 text-brand-700">
                <CheckCircle2 className="size-10" aria-hidden />
                <p className="text-sm font-medium">Berhasil terhubung</p>
              </div>
            ) : qr ? (
              <QRCodeSVG value={qr} size={200} level="M" marginSize={0} />
            ) : (
              <div className="flex flex-col items-center gap-2 text-ink-muted">
                {phase === 'expired' ? (
                  <>
                    <AlertTriangle className="size-8" aria-hidden />
                    <p className="text-sm">QR kedaluwarsa</p>
                  </>
                ) : (
                  <>
                    <Loader2 className="size-6 animate-spin" aria-hidden />
                    <p className="text-sm">Menyiapkan QR…</p>
                  </>
                )}
              </div>
            )}
          </div>

          <button
            type="button"
            onClick={requestQR}
            disabled={phase === 'requesting' || phase === 'linked'}
            className="inline-flex items-center gap-1.5 text-sm font-medium text-brand-700 transition-colors hover:text-brand-900 disabled:opacity-50"
          >
            <RefreshCw className={`size-4 ${phase === 'requesting' ? 'animate-spin' : ''}`} />
            Refresh QR
          </button>
        </div>

        {error ? <ErrorNote message={error} /> : null}

        <div className="rounded-xl bg-surface-sunken px-4 py-3.5">
          <p className="text-sm font-semibold text-ink">Cara scan QR dengan aman:</p>
          <ol className="mt-2 space-y-1.5 text-xs leading-relaxed text-ink-soft">
            <li>1. Buka aplikasi WhatsApp di HP kamu</li>
            <li>2. Ketuk Titik tiga (⋮) → Perangkat Tertaut</li>
            <li>3. Ketuk Tautkan Perangkat → Arahkan kamera ke QR di atas</li>
          </ol>
        </div>
      </div>
    </Modal>
  );
}
