'use client';

import clsx from 'clsx';
import { X } from 'lucide-react';
import { useEffect, useId, useRef, type ReactNode } from 'react';

interface ModalProps {
  open: boolean;
  onClose: () => void;
  title: string;
  children: ReactNode;
  footer?: ReactNode;
  size?: 'sm' | 'md' | 'lg' | 'xl';
}

const SIZES = {
  sm: 'max-w-[380px]',
  md: 'max-w-[460px]',
  lg: 'max-w-[640px]',
  // Two columns of form beside each other. Anything narrower stacks them, and
  // a preview under the field it previews is a preview nobody looks at.
  xl: 'max-w-[980px]',
};

/**
 * Centred dialog matching reference screens 2 and 3: white card, hairline
 * border, close affordance top-right. Closes on Escape and backdrop click.
 */
export function Modal({ open, onClose, title, children, footer, size = 'md' }: ModalProps) {
  const titleId = useId();
  const panelRef = useRef<HTMLDivElement>(null);

  // Callers pass an inline arrow for onClose, so its identity changes on every
  // render. Reading it through a ref keeps it out of the dependency arrays
  // below — otherwise each keystroke inside the dialog would re-run the effects
  // and the focus call would yank the caret out of the field being typed in.
  const onCloseRef = useRef(onClose);
  onCloseRef.current = onClose;

  useEffect(() => {
    if (!open) return;

    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') onCloseRef.current();
    };
    document.addEventListener('keydown', onKeyDown);

    const previousOverflow = document.body.style.overflow;
    document.body.style.overflow = 'hidden';

    return () => {
      document.removeEventListener('keydown', onKeyDown);
      document.body.style.overflow = previousOverflow;
    };
  }, [open]);

  // Move focus into the dialog exactly once per opening. The first field is
  // preferred so the dialog is immediately typeable; dialogs without fields
  // (the QR sheet) fall back to the panel itself.
  useEffect(() => {
    if (!open) return;
    const panel = panelRef.current;
    if (!panel) return;

    const field = panel.querySelector<HTMLElement>(
      'input:not([type="hidden"]):not([disabled]), textarea:not([disabled]), select:not([disabled])',
    );
    (field ?? panel).focus();
  }, [open]);

  if (!open) return null;

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-ink/40 p-4"
      onMouseDown={(event) => {
        if (event.target === event.currentTarget) onClose();
      }}
    >
      <div
        ref={panelRef}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        tabIndex={-1}
        className={clsx(
          'w-full rounded-card bg-surface-raised shadow-e4 outline-none',
          'max-h-[calc(100dvh-2rem)] overflow-y-auto scrollbar-slim',
          SIZES[size],
        )}
      >
        <div className="flex items-start justify-between gap-4 px-6 pt-6">
          <h2 id={titleId} className="text-lg font-semibold text-ink">
            {title}
          </h2>
          <button
            type="button"
            onClick={onClose}
            aria-label="Tutup"
            className="-mr-1 -mt-1 rounded-lg p-1.5 text-ink-muted transition-colors hover:bg-surface-sunken hover:text-ink"
          >
            <X className="size-5" />
          </button>
        </div>

        <div className="px-6 py-5">{children}</div>

        {footer ? <div className="flex justify-end gap-2 px-6 pb-6">{footer}</div> : null}
      </div>
    </div>
  );
}
