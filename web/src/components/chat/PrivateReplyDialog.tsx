'use client';

import clsx from 'clsx';
import { Loader2, MessageCircle, Send, X } from 'lucide-react';
import { useCallback, useEffect, useRef, useState } from 'react';

import { ErrorNote } from '@/components/ui/Primitives';
import { privateReplyTarget, sendPrivateReply } from '@/lib/api';
import { textareaClass } from '@/components/ui/control';
import type { Message, PrivateReplyTarget } from '@/lib/types';

/**
 * Answering someone in private about what they wrote in a group.
 *
 * A dialog rather than a jump to their chat, because the quote is the point.
 * Navigating to the one-to-one thread and typing there would send a message
 * with no context, and the recipient would be reading an unexplained line from
 * a number they may not have saved. The quote has to travel with the text, so
 * the text is written here.
 *
 * Where it lands is resolved by the server before a word is typed. Guessing the
 * thread in the browser would be a second answer to a question only the server
 * can settle, and getting it wrong means writing to the wrong customer.
 */
export function PrivateReplyDialog({
  message,
  onClose,
  onSent,
}: {
  /** Null closes the dialog. */
  message: Message | null;
  onClose: () => void;
  /** Called after a successful send, with where it went. */
  onSent: (target: PrivateReplyTarget) => void;
}) {
  const [target, setTarget] = useState<PrivateReplyTarget | null>(null);
  const [resolving, setResolving] = useState(false);
  const [draft, setDraft] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const inputRef = useRef<HTMLTextAreaElement>(null);

  const open = message !== null;
  const messageId = message?.id ?? null;

  const closeRef = useRef(onClose);
  closeRef.current = onClose;

  useEffect(() => {
    if (!open) return;
    setDraft('');
    setError(null);
    setTarget(null);

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

  // Resolved once per message. Cancelled on close so a slow answer cannot
  // arrive into a dialog the operator has already dismissed and name somebody
  // they are no longer writing to.
  useEffect(() => {
    if (!messageId) return;
    let live = true;
    setResolving(true);
    privateReplyTarget(messageId)
      .then((found) => {
        if (!live) return;
        setTarget(found);
        inputRef.current?.focus();
      })
      .catch((err: unknown) => {
        if (!live) return;
        setError(err instanceof Error ? err.message : 'Tidak bisa membuka chat pribadinya.');
      })
      .finally(() => {
        if (live) setResolving(false);
      });
    return () => {
      live = false;
    };
  }, [messageId]);

  const submit = useCallback(async () => {
    if (!messageId || !target || busy) return;
    const text = draft.trim();
    if (!text) return;

    setBusy(true);
    setError(null);
    try {
      const result = await sendPrivateReply(messageId, text);
      onSent(result.target);
      closeRef.current();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Balasan pribadi gagal dikirim.');
    } finally {
      setBusy(false);
    }
  }, [messageId, target, busy, draft, onSent]);

  if (!message) return null;

  const quoted =
    message.body ??
    message.caption ??
    (message.attachments.length > 0 ? message.attachments[0].file_name : null) ??
    'Media';

  const who = target?.name || message.display_name || message.sender_name || 'orang ini';

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
        aria-label="Balas pribadi"
        className="flex max-h-[calc(100dvh-2rem)] w-full max-w-[440px] flex-col rounded-card bg-surface-raised shadow-e4"
      >
        <header className="flex items-start gap-3 px-5 pt-5">
          <span className="grid size-10 shrink-0 place-items-center rounded-full bg-brand-600/10">
            <MessageCircle className="size-5 text-brand-700" />
          </span>
          <div className="min-w-0 flex-1">
            <h2 className="text-lg font-semibold text-ink">Balas pribadi</h2>
            <p className="mt-0.5 text-sm text-ink-muted">
              {resolving ? (
                'Mencari chat pribadinya…'
              ) : target ? (
                <>
                  Ke <span className="font-medium text-ink-soft">{who}</span>
                  {target.phone_number ? ` · ${target.phone_number}` : null}
                </>
              ) : (
                'Tidak bisa menentukan tujuannya.'
              )}
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
          {/* The quote the recipient will see, shown here so the operator is
              answering the thing they think they are answering. */}
          <div className="rounded-control border-l-2 border-brand-600 bg-surface-sunken/60 px-3 py-2">
            <p className="text-2xs font-medium text-brand-700">
              {message.display_name ?? message.sender_name ?? 'Pengirim'}
              {target?.group_name ? ` · di ${target.group_name}` : null}
            </p>
            <p className="mt-0.5 line-clamp-3 text-sm text-ink-soft">{quoted}</p>
          </div>

          <label className="mt-3 block">
            <span className="sr-only">Pesan</span>
            <textarea
              ref={inputRef}
              rows={3}
              value={draft}
              disabled={!target || busy}
              onChange={(event) => setDraft(event.target.value)}
              onKeyDown={(event) => {
                // Enter sends, Shift+Enter breaks the line: the same contract
                // the thread composer uses, so the habit carries over.
                if (event.key === 'Enter' && !event.shiftKey) {
                  event.preventDefault();
                  void submit();
                }
              }}
              placeholder={`Tulis balasan untuk ${who}…`}
              className={clsx(textareaClass, 'resize-none')}
            />
          </label>

          <p className="mt-2 text-xs leading-relaxed text-ink-muted">
            Pesan ini masuk ke chat pribadi, bukan ke grup. Yang menerima tetap melihat kutipan
            pesan grupnya, jadi dia tahu apa yang sedang dijawab.
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
            onClick={onClose}
            disabled={busy}
            className="h-9 rounded-control px-3 text-sm font-medium text-ink-muted transition-colors hover:bg-surface-sunken disabled:opacity-50"
          >
            Batal
          </button>
          <button
            type="button"
            onClick={() => void submit()}
            disabled={!target || busy || draft.trim() === ''}
            className="inline-flex h-9 items-center gap-1.5 rounded-control bg-brand-700 px-3.5 text-sm font-medium text-white transition-colors hover:bg-brand-800 disabled:opacity-50"
          >
            {busy ? <Loader2 className="size-4 animate-spin" /> : <Send className="size-4" />}
            Kirim
          </button>
        </footer>
      </div>
    </div>
  );
}
