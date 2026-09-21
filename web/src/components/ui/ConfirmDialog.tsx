'use client';

import clsx from 'clsx';
import { AlertTriangle, Info, Loader2, Trash2, type LucideIcon } from 'lucide-react';
import { useEffect, useRef, useState, type ReactNode } from 'react';

export type ConfirmTone = 'danger' | 'neutral';

export interface ConfirmRequest {
  title: string;
  /** One or two sentences on what will actually happen. */
  description?: ReactNode;
  confirmLabel?: string;
  cancelLabel?: string;
  tone?: ConfirmTone;
  icon?: LucideIcon;
  onConfirm: () => void | Promise<void>;
}

const TONES: Record<
  ConfirmTone,
  { icon: LucideIcon; ring: string; iconClass: string; button: string }
> = {
  danger: {
    icon: Trash2,
    ring: 'bg-danger-soft',
    iconClass: 'text-danger',
    button: 'bg-danger text-white hover:brightness-110 focus-visible:outline-danger',
  },
  neutral: {
    icon: Info,
    ring: 'bg-brand-600/10',
    iconClass: 'text-brand-700',
    button: 'bg-brand-800 text-white hover:bg-brand-900 focus-visible:outline-brand-800',
  },
};

/**
 * Confirmation dialog.
 *
 * Replaces window.confirm, which cannot be styled, cannot say what it is about
 * beyond one line of plain text, and looks like the browser rather than like
 * the app. The wording matters more than the styling here: the difference
 * between deleting a chat from this inbox and deleting it from the customer's
 * phone is exactly the sort of thing a two-line native prompt cannot convey.
 */
export function ConfirmDialog({
  request,
  onClose,
}: {
  request: ConfirmRequest | null;
  onClose: () => void;
}) {
  const [busy, setBusy] = useState(false);
  const confirmRef = useRef<HTMLButtonElement>(null);

  const open = request !== null;

  // Read through a ref so the key handler binds once per opening.
  const closeRef = useRef(onClose);
  closeRef.current = onClose;

  useEffect(() => {
    if (!open) return;
    setBusy(false);
    confirmRef.current?.focus();

    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') closeRef.current();
    };
    document.addEventListener('keydown', onKeyDown);

    const previousOverflow = document.body.style.overflow;
    document.body.style.overflow = 'hidden';

    return () => {
      document.removeEventListener('keydown', onKeyDown);
      document.body.style.overflow = previousOverflow;
    };
  }, [open]);

  if (!request) return null;

  const tone = TONES[request.tone ?? 'neutral'];
  const Icon = request.icon ?? tone.icon;

  async function confirm() {
    if (busy || !request) return;
    setBusy(true);
    try {
      await request.onConfirm();
      onClose();
    } finally {
      setBusy(false);
    }
  }

  return (
    <div
      className="fixed inset-0 z-[70] flex items-center justify-center bg-black/55 p-4"
      onMouseDown={(event) => {
        if (event.target === event.currentTarget && !busy) onClose();
      }}
    >
      <div
        role="alertdialog"
        aria-modal="true"
        aria-label={request.title}
        className="w-full max-w-[400px] rounded-card bg-surface-raised p-6 shadow-e4"
      >
        <div className="flex gap-4">
          <span className={clsx('grid size-10 shrink-0 place-items-center rounded-full', tone.ring)}>
            <Icon className={clsx('size-5', tone.iconClass)} />
          </span>
          <div className="min-w-0 flex-1 pt-1">
            <h2 className="text-lg font-semibold text-ink">{request.title}</h2>
            {request.description ? (
              <div className="mt-1.5 text-sm leading-relaxed text-ink-muted">
                {request.description}
              </div>
            ) : null}
          </div>
        </div>

        <div className="mt-6 flex justify-end gap-2">
          <button
            type="button"
            onClick={onClose}
            disabled={busy}
            className="h-10 rounded-control border border-hairline bg-surface-raised px-4 text-sm font-medium text-ink transition-colors hover:bg-surface-sunken disabled:opacity-60"
          >
            {request.cancelLabel ?? 'Batal'}
          </button>
          <button
            ref={confirmRef}
            type="button"
            onClick={() => void confirm()}
            disabled={busy}
            className={clsx(
              'inline-flex h-10 items-center gap-2 rounded-control px-4 text-sm font-medium transition-all',
              'focus-visible:outline-2 focus-visible:outline-offset-2 disabled:opacity-60',
              tone.button,
            )}
          >
            {busy ? <Loader2 className="size-4 animate-spin" /> : null}
            {request.confirmLabel ?? 'Lanjutkan'}
          </button>
        </div>
      </div>
    </div>
  );
}

/**
 * Holds the pending confirmation.
 *
 * A hook rather than a context: the two screens that need it each own one
 * dialog, and threading a provider through the tree for that would be more
 * machinery than the problem deserves.
 */
export function useConfirm() {
  const [request, setRequest] = useState<ConfirmRequest | null>(null);
  return {
    request,
    ask: setRequest,
    close: () => setRequest(null),
  };
}

export { AlertTriangle };
