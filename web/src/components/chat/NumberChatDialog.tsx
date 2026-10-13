'use client';

import { Check, Copy, Loader2, MessageSquarePlus, Phone, X } from 'lucide-react';
import { useCallback, useEffect, useRef, useState } from 'react';

import { ErrorNote } from '@/components/ui/Primitives';
import { resolveNumberChat } from '@/lib/api';
import type { NumberTarget } from '@/lib/types';

/** What the operator pressed, as written and as read. */
export interface NumberPick {
  /** Digits only, country code first. */
  phone: string;
  /** Exactly how it appeared in the message. */
  written: string;
}

/**
 * The menu for a number somebody wrote inside a message.
 *
 * A dialog rather than a one-tap jump, because opening a chat is not a
 * navigation: on a miss it creates a thread for a number that was never a
 * customer, and on a hit it may be a thread the operator did not realise
 * already existed. Both are worth one press to confirm, and the result says
 * which of the two happened.
 *
 * Copying is here too, and it is the quieter half of the point: plenty of the
 * numbers in these messages belong to a courier, a bank or a colleague, and
 * what the operator actually wants is the digits, not a conversation.
 */
export function NumberChatDialog({
  pick,
  accountId,
  onClose,
  onOpened,
}: {
  /** Null closes the dialog. */
  pick: NumberPick | null;
  /** Which of our numbers would do the writing. */
  accountId: string;
  onClose: () => void;
  /**
   * Called once the thread is resolved, with where it is.
   *
   * Awaited, so this dialog stays up until the room is actually open. Opening
   * it can fail on its own — the thread is new, so the inbox list has not got
   * it yet and it has to be fetched — and a dialog that had already closed
   * would leave the operator looking at the chat they came from with nothing
   * saying why.
   */
  onOpened: (target: NumberTarget) => void | Promise<void>;
}) {
  const [busy, setBusy] = useState(false);
  const [copied, setCopied] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const closeRef = useRef(onClose);
  closeRef.current = onClose;

  const open = pick !== null;

  useEffect(() => {
    if (!open) return;
    setError(null);
    setCopied(false);
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') closeRef.current();
    };
    document.addEventListener('keydown', onKeyDown);
    return () => document.removeEventListener('keydown', onKeyDown);
  }, [open]);

  const phone = pick?.phone ?? '';

  const openChat = useCallback(async () => {
    if (!phone || busy) return;
    setBusy(true);
    setError(null);
    try {
      const { target } = await resolveNumberChat(accountId, phone);
      await onOpened(target);
      closeRef.current();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Chat tidak bisa dibuka.');
    } finally {
      setBusy(false);
    }
  }, [accountId, phone, busy, onOpened]);

  async function copy() {
    try {
      await navigator.clipboard.writeText('+' + phone);
      setCopied(true);
    } catch {
      // Blocked clipboard, or an insecure origin. Said rather than swallowed:
      // the operator is standing there waiting for something to happen.
      setError('Nomor tidak bisa disalin dari browser ini. Silakan salin manual.');
    }
  }

  if (!pick) return null;

  return (
    <div
      className="fixed inset-0 z-[65] flex items-center justify-center bg-black/55 p-4"
      onMouseDown={(event) => {
        if (event.target === event.currentTarget && !busy) onClose();
      }}
    >
      <div
        role="dialog"
        aria-modal="true"
        aria-label="Nomor di dalam pesan"
        className="w-full max-w-[400px] rounded-card bg-surface-raised shadow-e4"
      >
        <header className="flex items-start gap-3 px-5 pt-5">
          <span className="grid size-10 shrink-0 place-items-center rounded-full bg-brand-600/10">
            <Phone className="size-5 text-brand-700" />
          </span>
          <div className="min-w-0 flex-1">
            <h2 className="text-lg font-semibold text-ink">Nomor di dalam pesan</h2>
            {/* Both forms, because they can differ and the difference matters:
                the operator chose the one on the left and the chat will be
                opened for the one on the right. */}
            <p className="mt-0.5 truncate text-sm text-ink-muted" title={pick.written}>
              {pick.written} · dibaca sebagai +{phone}
            </p>
          </div>
          <button
            type="button"
            onClick={onClose}
            aria-label="Tutup"
            disabled={busy}
            className="rounded-lg p-1.5 text-ink-muted transition-colors hover:bg-surface-sunken disabled:opacity-50"
          >
            <X className="size-5" />
          </button>
        </header>

        <div className="px-5 pt-4">
          <p className="text-xs leading-relaxed text-ink-muted">
            Kalau nomornya belum pernah dihubungi dari nomor ini, percakapan barunya dibuat
            sekarang. Nomor yang tidak terdaftar di WhatsApp ditolak, jadi tidak ada chat kosong
            yang gagal saat pesan pertama dikirim.
          </p>

          {error ? (
            <div className="mt-3">
              <ErrorNote message={error} />
            </div>
          ) : null}
        </div>

        <footer className="flex items-center justify-end gap-2 px-5 pt-4 pb-5">
          <button
            type="button"
            onClick={() => void copy()}
            disabled={busy}
            className="inline-flex h-9 items-center gap-1.5 rounded-control px-3 text-sm font-medium text-ink-muted transition-colors hover:bg-surface-sunken disabled:opacity-50"
          >
            {copied ? <Check className="size-4" /> : <Copy className="size-4" />}
            {copied ? 'Tersalin' : 'Salin nomor'}
          </button>
          <button
            type="button"
            onClick={() => void openChat()}
            disabled={busy}
            className="inline-flex h-9 items-center gap-1.5 rounded-control bg-brand-700 px-3.5 text-sm font-medium text-white transition-colors hover:bg-brand-800 disabled:opacity-50"
          >
            {busy ? (
              <Loader2 className="size-4 animate-spin" />
            ) : (
              <MessageSquarePlus className="size-4" />
            )}
            Chat dengan nomor ini
          </button>
        </footer>
      </div>
    </div>
  );
}
