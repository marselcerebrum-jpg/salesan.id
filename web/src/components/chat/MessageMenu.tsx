'use client';

import clsx from 'clsx';
import {
  ChevronDown,
  Copy,
  CornerUpLeft,
  MessageCircle,
  Pencil,
  Share2,
  Trash2,
  UserMinus,
} from 'lucide-react';
import { useEffect, useRef, useState } from 'react';

import type { Message } from '@/lib/types';

/** WhatsApp's own limit; past it the server rejects an edit. */
const EDIT_WINDOW_MS = 15 * 60 * 1000;

export type MessageAction =
  | 'reply'
  | 'private-reply'
  | 'forward'
  | 'edit'
  | 'delete-everyone'
  | 'delete-me'
  | 'copy';

/**
 * The chevron on a bubble and the menu it opens.
 *
 * Which entries appear is decided from the message itself rather than being
 * shown-and-disabled: an operator does not need to be told they cannot edit a
 * message someone else sent, only not to be offered it.
 */
export function MessageMenu({
  message,
  mine,
  inGroup = false,
  canRevokeAny = false,
  onAction,
}: {
  message: Message;
  mine: boolean;
  /**
   * True in a group. Only a group has a "reply privately": in a one-to-one
   * chat the private thread is the one already open.
   */
  inGroup?: boolean;
  /**
   * True in a group where this account is an admin, which WhatsApp lets delete
   * anyone's message. It governs what is offered, not what is permitted —
   * WhatsApp decides that, and a stale flag ends in a refusal the operator can
   * read rather than a button that quietly does nothing.
   */
  canRevokeAny?: boolean;
  onAction: (action: MessageAction) => void;
}) {
  const [open, setOpen] = useState(false);
  const [anchor, setAnchor] = useState<DOMRect | null>(null);
  const buttonRef = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    if (!open) return;
    const close = () => setOpen(false);
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') setOpen(false);
    };
    // Scrolling the thread would leave a fixed-position menu stranded away
    // from its bubble, so it closes instead of chasing it.
    window.addEventListener('scroll', close, true);
    window.addEventListener('resize', close);
    document.addEventListener('keydown', onKeyDown);
    return () => {
      window.removeEventListener('scroll', close, true);
      window.removeEventListener('resize', close);
      document.removeEventListener('keydown', onKeyDown);
    };
  }, [open]);

  // A deleted message has nothing left to act on but its own removal.
  const revoked = Boolean(message.revoked_at);
  const text = message.body ?? message.caption ?? '';
  const withinEditWindow = Date.now() - new Date(message.timestamp).getTime() < EDIT_WINDOW_MS;
  const editable =
    mine &&
    !revoked &&
    withinEditWindow &&
    ['text', 'image', 'video', 'document'].includes(message.type);

  const hasContent = Boolean(text) || message.attachments.length > 0;

  const items: Array<{ action: MessageAction; label: string; icon: typeof Copy; danger?: boolean }> = [];
  if (!revoked) items.push({ action: 'reply', label: 'Balas', icon: CornerUpLeft });
  // Someone else's message in a group. Answering our own message privately
  // would be a chat with ourselves, which is never what this means.
  if (!revoked && inGroup && !mine) {
    items.push({ action: 'private-reply', label: 'Balas pribadi', icon: MessageCircle });
  }
  if (!revoked && hasContent) items.push({ action: 'forward', label: 'Teruskan', icon: Share2 });
  if (text && !revoked) items.push({ action: 'copy', label: 'Salin teks', icon: Copy });
  if (editable) items.push({ action: 'edit', label: 'Edit pesan', icon: Pencil });
  items.push({ action: 'delete-me', label: 'Hapus untuk saya', icon: UserMinus });
  // Own messages anywhere; anyone's message in a group we administer — the
  // same rule WhatsApp applies.
  if (!revoked && (mine || canRevokeAny)) {
    items.push({ action: 'delete-everyone', label: 'Hapus untuk semua', icon: Trash2, danger: true });
  }

  const menuHeight = 12 + items.length * 38;
  const opensUp = anchor ? anchor.bottom + menuHeight > window.innerHeight - 12 : false;

  return (
    <>
      <button
        ref={buttonRef}
        type="button"
        aria-label="Opsi pesan"
        aria-expanded={open}
        onClick={() => {
          setAnchor(buttonRef.current?.getBoundingClientRect() ?? null);
          setOpen((v) => !v);
        }}
        className={clsx(
          'absolute top-0.5 right-0.5 z-10 grid size-6 place-items-center rounded-full',
          'text-wa-text-2 transition-opacity',
          // Hidden until the bubble is hovered, so a thread at rest is just
          // the conversation. Focus reveals it for keyboard users.
          open ? 'opacity-100' : 'opacity-0 group-hover/bubble:opacity-100 focus-visible:opacity-100',
          mine ? 'bg-wa-out' : 'bg-wa-in',
        )}
      >
        <ChevronDown className="size-4" />
      </button>

      {open && anchor ? (
        <>
          <div className="fixed inset-0 z-40" onClick={() => setOpen(false)} aria-hidden />
          <div
            role="menu"
            style={{
              position: 'fixed',
              left: Math.max(12, Math.min(anchor.right - 200, window.innerWidth - 212)),
              width: 200,
              ...(opensUp
                ? { bottom: window.innerHeight - anchor.top + 6 }
                : { top: anchor.bottom + 6 }),
            }}
            className="z-50 overflow-hidden rounded-xl border border-wa-border bg-wa-panel py-1 shadow-e3"
          >
            {items.map(({ action, label, icon: Icon, danger }) => (
              <button
                key={action}
                type="button"
                role="menuitem"
                onClick={() => {
                  setOpen(false);
                  onAction(action);
                }}
                className={clsx(
                  'flex w-full items-center gap-2.5 px-3 py-2.5 text-left text-sm transition-colors',
                  danger ? 'text-danger hover:bg-danger-soft' : 'text-wa-text hover:bg-wa-active',
                )}
              >
                <Icon className={clsx('size-4', danger ? '' : 'text-wa-text-2')} />
                {label}
              </button>
            ))}
          </div>
        </>
      ) : null}
    </>
  );
}
