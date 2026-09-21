'use client';

import clsx from 'clsx';
import { Check, Loader2, Search, Share2, Users, X } from 'lucide-react';
import { useEffect, useMemo, useRef, useState } from 'react';

import { conversationTitle, initials } from '@/lib/format';
import type { Conversation, Message } from '@/lib/types';

const MAX_TARGETS = 20;

/**
 * Picker for forwarding a message.
 *
 * Multi-select with a search box, because forwarding a price list to six
 * customers is the common case and doing it one dialog at a time would be six
 * times the work.
 */
export function ForwardDialog({
  message,
  conversations,
  onClose,
  onForward,
}: {
  /** Null closes the dialog. */
  message: Message | null;
  conversations: Conversation[];
  onClose: () => void;
  onForward: (message: Message, conversationIds: string[]) => Promise<void>;
}) {
  const [search, setSearch] = useState('');
  const [picked, setPicked] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const searchRef = useRef<HTMLInputElement>(null);

  const open = message !== null;

  const closeRef = useRef(onClose);
  closeRef.current = onClose;

  useEffect(() => {
    if (!open) return;
    setSearch('');
    setPicked([]);
    setError(null);
    searchRef.current?.focus();

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

  const matches = useMemo(() => {
    const needle = search.trim().toLowerCase();
    // The thread the message came from is excluded: forwarding a message back
    // into its own conversation is never what someone means to do.
    const pool = conversations.filter((c) => c.id !== message?.conversation_id);
    if (!needle) return pool;
    return pool.filter((c) => conversationTitle(c).toLowerCase().includes(needle));
  }, [conversations, search, message?.conversation_id]);

  if (!message) return null;

  const preview =
    message.body ??
    message.caption ??
    (message.attachments.length > 0 ? message.attachments[0].file_name : null) ??
    'Media';

  function toggle(id: string) {
    setPicked((current) =>
      current.includes(id)
        ? current.filter((c) => c !== id)
        : current.length >= MAX_TARGETS
          ? current
          : [...current, id],
    );
  }

  async function submit() {
    if (picked.length === 0 || busy || !message) return;
    setBusy(true);
    setError(null);
    try {
      await onForward(message, picked);
      onClose();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Gagal meneruskan pesan.');
    } finally {
      setBusy(false);
    }
  }

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
        aria-label="Teruskan pesan"
        className="flex max-h-[calc(100dvh-2rem)] w-full max-w-[440px] flex-col rounded-card bg-surface-raised shadow-e4"
      >
        <header className="flex items-start gap-3 px-5 pt-5">
          <span className="grid size-10 shrink-0 place-items-center rounded-full bg-brand-600/10">
            <Share2 className="size-5 text-brand-700" />
          </span>
          <div className="min-w-0 flex-1">
            <h2 className="text-lg font-semibold text-ink">Teruskan pesan</h2>
            <p className="mt-0.5 truncate text-sm text-ink-muted">{preview}</p>
          </div>
          <button
            type="button"
            onClick={onClose}
            disabled={busy}
            aria-label="Tutup"
            className="-mt-1 -mr-1 rounded-lg p-1.5 text-ink-muted hover:bg-surface-sunken"
          >
            <X className="size-5" />
          </button>
        </header>

        <div className="px-5 pt-4">
          <label className="relative block">
            <Search className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-ink-muted" />
            <input
              ref={searchRef}
              value={search}
              onChange={(event) => setSearch(event.target.value)}
              placeholder="Cari percakapan"
              className="h-10 w-full rounded-control bg-surface-sunken pr-3 pl-9 text-sm text-ink outline-none focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-brand-600 placeholder:text-ink-muted"
            />
          </label>
        </div>

        <div className="scrollbar-slim mt-2 min-h-0 flex-1 overflow-y-auto px-2 py-1">
          {matches.length === 0 ? (
            <p className="px-3 py-8 text-center text-sm text-ink-muted">
              Tidak ada percakapan yang cocok.
            </p>
          ) : (
            matches.map((conversation) => {
              const title = conversationTitle(conversation);
              const on = picked.includes(conversation.id);
              const full = !on && picked.length >= MAX_TARGETS;
              return (
                <button
                  key={conversation.id}
                  type="button"
                  role="checkbox"
                  aria-checked={on}
                  disabled={full || busy}
                  onClick={() => toggle(conversation.id)}
                  className={clsx(
                    'flex w-full items-center gap-3 rounded-xl px-3 py-2.5 text-left transition-colors',
                    on ? 'bg-brand-600/10' : 'hover:bg-surface-sunken',
                    full && 'opacity-40',
                  )}
                >
                  <span
                    className={clsx(
                      'grid size-9 shrink-0 place-items-center rounded-full text-sm font-medium',
                      conversation.type === 'group'
                        ? 'bg-surface-sunken text-ink-soft'
                        : 'bg-brand-800 text-white',
                    )}
                  >
                    {conversation.type === 'group' ? <Users className="size-4" /> : initials(title)}
                  </span>
                  <span className="min-w-0 flex-1 truncate text-sm text-ink">{title}</span>
                  <span
                    className={clsx(
                      'grid size-5 shrink-0 place-items-center rounded-md border transition-colors',
                      on ? 'border-brand-800 bg-brand-800 text-white' : 'border-hairline',
                    )}
                  >
                    {on ? <Check className="size-3.5" strokeWidth={3} /> : null}
                  </span>
                </button>
              );
            })
          )}
        </div>

        <footer className="border-t border-hairline px-5 py-4">
          {error ? <p className="mb-2 text-sm text-danger">{error}</p> : null}
          <div className="flex items-center justify-between gap-3">
            <span className="text-xs text-ink-muted">
              {picked.length === 0
                ? `Pilih tujuan (maks ${MAX_TARGETS})`
                : `${picked.length} dipilih`}
            </span>
            <button
              type="button"
              onClick={() => void submit()}
              disabled={picked.length === 0 || busy}
              className="inline-flex h-10 items-center gap-2 rounded-control bg-brand-800 px-4 text-sm font-medium text-white transition-opacity hover:bg-brand-900 disabled:opacity-40"
            >
              {busy ? <Loader2 className="size-4 animate-spin" /> : <Share2 className="size-4" />}
              Teruskan
            </button>
          </div>
        </footer>
      </div>
    </div>
  );
}
