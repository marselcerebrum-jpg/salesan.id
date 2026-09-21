'use client';

import clsx from 'clsx';
import {
  AlertCircle,
  Check,
  ChevronLeft,
  ChevronRight,
  FileText,
  Loader2,
  Pencil,
  Plus,
  RotateCw,
  SendHorizontal,
  Trash2,
  X,
} from 'lucide-react';
import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type Dispatch,
  type SetStateAction,
} from 'react';

import { ImageEditor } from '@/components/chat/ImageEditor';
import { formatBytes, KIND_LABEL, kindOfFile, validateFile } from '@/lib/media';
import type { AttachmentKind } from '@/lib/types';

/** One queued file, with its edits and its send state. */
export interface Draft {
  /**
   * Stable for the life of this draft, including across retries. It is sent to
   * the server as the idempotency key, which is what makes "coba lagi" resend
   * rather than duplicate.
   */
  token: string;
  file: File;
  name: string;
  kind: AttachmentKind;
  asDocument: boolean;
  caption: string;
  previewUrl: string | null;
  /** True once the editor has replaced `file` with an edited version. */
  edited: boolean;
  status: 'idle' | 'sending' | 'sent' | 'failed';
  progress: number;
  error: string | null;
}

export interface SendDraft {
  token: string;
  file: File;
  fileName: string;
  caption: string;
  asDocument: boolean;
  /** The message this file answers, quoted the way a text reply is. */
  replyTo?: string | null;
}

interface MediaComposerProps {
  drafts: Draft[];
  /**
   * The parent's state setter, not a plain callback.
   *
   * Sending issues several updates per file — status, then progress, then the
   * outcome — with awaits in between. Each must build on the newest queue, and
   * only the functional form of a setter can guarantee that; a callback taking
   * a finished array would base its update on whatever React had last
   * rendered, silently dropping the update before it.
   */
  onChange: Dispatch<SetStateAction<Draft[]>>;
  onClose: () => void;
  /** Sends one file; resolves on success, rejects with a message on failure. */
  onSendFile: (
    draft: SendDraft,
    onProgress: (fraction: number) => void,
    signal: AbortSignal,
  ) => Promise<void>;
  /** Adds more files to the queue from the same picker the menu uses. */
  onAddFiles: (asDocument: boolean) => void;
}

/**
 * Full-screen preview before anything is sent.
 *
 * Files go out one at a time, in the order shown. Sequential rather than
 * parallel because WhatsApp itself preserves send order, and a thread where
 * three photos arrive shuffled is worse than one that takes a moment longer.
 */
export function MediaComposer({
  drafts,
  onChange,
  onClose,
  onSendFile,
  onAddFiles,
}: MediaComposerProps) {
  const [active, setActive] = useState(0);
  const [sending, setSending] = useState(false);
  const [editing, setEditing] = useState<string | null>(null);
  const abortRef = useRef<AbortController | null>(null);

  const open = drafts.length > 0;
  const current = drafts[Math.min(active, drafts.length - 1)] ?? null;

  // Keep the selection valid as files are removed.
  useEffect(() => {
    if (active >= drafts.length && drafts.length > 0) setActive(drafts.length - 1);
  }, [active, drafts.length]);

  // Read through a ref so the key handler is bound once per opening rather
  // than re-bound on every keystroke in the caption field.
  const state = useRef({ drafts, sending, onClose });
  state.current = { drafts, sending, onClose };

  useEffect(() => {
    if (!open) return;

    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape' && !state.current.sending) state.current.onClose();
    };
    document.addEventListener('keydown', onKeyDown);

    const previousOverflow = document.body.style.overflow;
    document.body.style.overflow = 'hidden';

    return () => {
      document.removeEventListener('keydown', onKeyDown);
      document.body.style.overflow = previousOverflow;
    };
  }, [open]);

  // Abandoning the screen must not leak the object URLs behind the previews.
  useEffect(
    () => () => {
      abortRef.current?.abort();
    },
    [],
  );

  const patch = useCallback(
    (token: string, changes: Partial<Draft>) => {
      onChange((current) => current.map((d) => (d.token === token ? { ...d, ...changes } : d)));
    },
    [onChange],
  );

  const pending = useMemo(() => drafts.filter((d) => d.status !== 'sent'), [drafts]);
  const failed = useMemo(() => drafts.filter((d) => d.status === 'failed'), [drafts]);

  /**
   * Sends the given drafts one after another.
   *
   * A failure stops nothing: the remaining files still go, and the failed ones
   * stay on screen with a retry. Losing four photos because the second one
   * timed out would be the wrong trade.
   */
  const run = useCallback(
    async (queue: Draft[]) => {
      if (queue.length === 0 || sending) return;

      const controller = new AbortController();
      abortRef.current = controller;
      setSending(true);

      // Tracked here rather than read back from state at the end: the last
      // file's "sent" update has not been rendered yet by the time this loop
      // finishes, so state would still show it as in flight.
      let allDelivered = true;

      for (const draft of queue) {
        if (controller.signal.aborted) {
          allDelivered = false;
          break;
        }

        patch(draft.token, { status: 'sending', progress: 0, error: null });

        try {
          await onSendFile(
            {
              token: draft.token,
              file: draft.file,
              fileName: draft.name,
              caption: draft.caption.trim(),
              asDocument: draft.asDocument,
            },
            (fraction) => patch(draft.token, { progress: fraction }),
            controller.signal,
          );
          patch(draft.token, { status: 'sent', progress: 1, error: null });
        } catch (err) {
          allDelivered = false;
          if (controller.signal.aborted) break;
          patch(draft.token, {
            status: 'failed',
            error: err instanceof Error ? err.message : 'Gagal mengirim',
          });
        }
      }

      abortRef.current = null;
      setSending(false);

      // Close only when everything landed; a partial failure keeps the screen
      // open so the operator can see which file needs another go.
      if (allDelivered) state.current.onClose();
    },
    [onSendFile, patch, sending],
  );

  if (!open || !current) return null;

  const allSent = drafts.every((d) => d.status === 'sent');
  const beingEdited = drafts.find((d) => d.token === editing) ?? null;

  /** Replaces a draft's file with the editor's output. */
  function acceptEdit(token: string, file: File) {
    const draft = drafts.find((d) => d.token === token);
    if (draft?.previewUrl) URL.revokeObjectURL(draft.previewUrl);
    patch(token, {
      file,
      previewUrl: URL.createObjectURL(file),
      edited: true,
      // The editor re-encodes, so the extension may have changed with it.
      name: file.name,
    });
    setEditing(null);
  }

  return (
    <div className="fixed inset-0 z-[55] flex flex-col bg-wa-panel-2">
      <header className="flex items-center gap-3 border-b border-wa-border px-4 py-3">
        <button
          type="button"
          onClick={() => {
            if (sending) {
              abortRef.current?.abort();
              return;
            }
            onClose();
          }}
          aria-label={sending ? 'Batalkan pengiriman' : 'Tutup'}
          className="grid size-9 place-items-center rounded-full text-wa-text-2 hover:bg-wa-active"
        >
          <X className="size-5" />
        </button>
        <div className="min-w-0 flex-1">
          <p className="truncate text-base text-wa-text">
            {drafts.length === 1 ? current.name : `${drafts.length} berkas`}
          </p>
          <p className="text-xs text-wa-text-2">
            {sending
              ? `Mengirim ${drafts.filter((d) => d.status === 'sent').length + 1} dari ${drafts.length}…`
              : failed.length > 0
                ? `${failed.length} berkas gagal dikirim`
                : `${KIND_LABEL[current.kind]} · ${formatBytes(current.file.size)}`}
          </p>
        </div>
      </header>

      <div className="flex min-h-0 flex-1 items-center justify-center overflow-hidden p-4">
        <Preview draft={current} />
      </div>

      {/* Per-file caption. WhatsApp attaches a caption to each file rather than
          one to the batch, and so does this. */}
      <div className="border-t border-wa-border px-4 pt-3">
        <label className="sr-only" htmlFor="media-caption">
          Keterangan
        </label>
        <input
          id="media-caption"
          value={current.caption}
          onChange={(event) => patch(current.token, { caption: event.target.value })}
          disabled={sending || current.status === 'sent'}
          placeholder="Tambahkan keterangan"
          maxLength={1024}
          className="w-full rounded-lg bg-wa-panel px-4 py-[11px] text-base text-wa-text outline-none focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-wa-accent placeholder:text-wa-text-2 disabled:opacity-60"
        />
      </div>

      {current.kind === 'image' && current.status !== 'sent' ? (
        <div className="flex items-center gap-3 px-4 pt-2">
          <button
            type="button"
            disabled={sending}
            onClick={() => setEditing(current.token)}
            className="inline-flex items-center gap-1.5 rounded-full bg-wa-panel px-3 py-1.5 text-sm text-wa-text hover:bg-wa-active disabled:opacity-40"
          >
            <Pencil className="size-4" />
            Edit gambar
          </button>
          {current.edited ? (
            <span className="text-xs text-wa-text-2">Sudah diedit</span>
          ) : null}

          <label className="ml-auto inline-flex cursor-pointer items-center gap-2 text-xs text-wa-text-2">
            <input
              type="checkbox"
              checked={current.asDocument}
              disabled={sending}
              onChange={(event) =>
                patch(current.token, {
                  asDocument: event.target.checked,
                  kind: event.target.checked ? 'document' : kindOfFile(current.file, false),
                })
              }
              className="size-3.5 accent-wa-accent"
            />
            Kirim sebagai file (tanpa kompresi)
          </label>
        </div>
      ) : null}

      <footer className="flex items-center gap-3 px-4 py-3">
        <div className="scrollbar-slim flex min-w-0 flex-1 items-center gap-2 overflow-x-auto pb-1">
          {drafts.map((draft, index) => (
            <Thumb
              key={draft.token}
              draft={draft}
              selected={index === active}
              onSelect={() => setActive(index)}
              onRemove={
                sending || draft.status === 'sent'
                  ? undefined
                  : () => {
                      if (draft.previewUrl) URL.revokeObjectURL(draft.previewUrl);
                      onChange(drafts.filter((d) => d.token !== draft.token));
                    }
              }
              onMove={
                sending
                  ? undefined
                  : (direction) => {
                      const next = index + direction;
                      if (next < 0 || next >= drafts.length) return;
                      const reordered = [...drafts];
                      [reordered[index], reordered[next]] = [reordered[next], reordered[index]];
                      onChange(reordered);
                      setActive(next);
                    }
              }
            />
          ))}

          {!sending && !allSent ? (
            <button
              type="button"
              onClick={() => onAddFiles(current.asDocument)}
              aria-label="Tambah berkas"
              className="grid size-[56px] shrink-0 place-items-center rounded-lg border border-dashed border-wa-border text-wa-text-2 hover:bg-wa-active"
            >
              <Plus className="size-5" />
            </button>
          ) : null}
        </div>

        <button
          type="button"
          onClick={() => void run(failed.length > 0 && !sending ? failed : pending)}
          disabled={sending || pending.length === 0}
          className="grid size-[52px] shrink-0 place-items-center rounded-full bg-wa-accent text-white transition-opacity disabled:opacity-40"
          aria-label={failed.length > 0 ? 'Coba lagi yang gagal' : 'Kirim'}
        >
          {sending ? (
            <Loader2 className="size-6 animate-spin" />
          ) : failed.length > 0 ? (
            <RotateCw className="size-6" />
          ) : (
            <SendHorizontal className="size-6" />
          )}
        </button>
      </footer>

      {beingEdited ? (
        <ImageEditor
          file={beingEdited.file}
          onCancel={() => setEditing(null)}
          onApply={(file) => acceptEdit(beingEdited.token, file)}
        />
      ) : null}
    </div>
  );
}

/* --- pieces ---------------------------------------------------------------- */

function Preview({ draft }: { draft: Draft }) {
  if (draft.kind === 'image' && draft.previewUrl) {
    return (
      // eslint-disable-next-line @next/next/no-img-element -- a local object URL
      <img
        src={draft.previewUrl}
        alt={draft.name}
        className="max-h-full max-w-full object-contain"
      />
    );
  }

  if (draft.kind === 'video' && draft.previewUrl) {
    return <video src={draft.previewUrl} controls className="max-h-full max-w-full rounded-md" />;
  }

  return (
    <div className="flex flex-col items-center gap-3 text-center">
      <span className="grid size-24 place-items-center rounded-card bg-wa-panel text-wa-text-2">
        <FileText className="size-10" />
      </span>
      <div>
        <p className="max-w-sm truncate text-base text-wa-text">{draft.name}</p>
        <p className="text-sm text-wa-text-2">
          {[KIND_LABEL[draft.kind], formatBytes(draft.file.size)].filter(Boolean).join(' · ')}
        </p>
      </div>
    </div>
  );
}

function Thumb({
  draft,
  selected,
  onSelect,
  onRemove,
  onMove,
}: {
  draft: Draft;
  selected: boolean;
  onSelect: () => void;
  onRemove?: () => void;
  onMove?: (direction: -1 | 1) => void;
}) {
  return (
    <div
      className={clsx(
        'group relative size-[56px] shrink-0 overflow-hidden rounded-lg border-2 transition-colors',
        selected ? 'border-wa-accent' : 'border-transparent',
      )}
    >
      <button type="button" onClick={onSelect} className="block size-full" aria-label={draft.name}>
        {draft.previewUrl && draft.kind !== 'document' ? (
          // eslint-disable-next-line @next/next/no-img-element -- local object URL
          <img src={draft.previewUrl} alt="" className="size-full object-cover" />
        ) : (
          <span className="grid size-full place-items-center bg-wa-panel text-wa-text-2">
            <FileText className="size-5" />
          </span>
        )}
      </button>

      {/* Progress fills from the bottom while this file is uploading. */}
      {draft.status === 'sending' ? (
        <span className="pointer-events-none absolute inset-0 bg-black/50">
          <span
            className="absolute inset-x-0 bottom-0 bg-wa-accent/70 transition-[height] duration-150"
            style={{ height: `${Math.round(draft.progress * 100)}%` }}
          />
          <span className="absolute inset-0 grid place-items-center text-2xs font-semibold text-white">
            {Math.round(draft.progress * 100)}%
          </span>
        </span>
      ) : null}

      {draft.status === 'sent' ? (
        <span className="pointer-events-none absolute inset-0 grid place-items-center bg-black/45">
          <Check className="size-5 text-white" />
        </span>
      ) : null}

      {draft.status === 'failed' ? (
        <span
          title={draft.error ?? 'Gagal'}
          className="pointer-events-none absolute inset-0 grid place-items-center bg-danger/55"
        >
          <AlertCircle className="size-5 text-white" />
        </span>
      ) : null}

      {onRemove ? (
        <button
          type="button"
          onClick={onRemove}
          aria-label={`Hapus ${draft.name}`}
          className="absolute top-0.5 right-0.5 grid size-5 place-items-center rounded-full bg-black/60 text-white opacity-0 transition-opacity group-hover:opacity-100 focus-visible:opacity-100"
        >
          <Trash2 className="size-3" />
        </button>
      ) : null}

      {onMove ? (
        <span className="absolute inset-x-0 bottom-0 flex justify-between opacity-0 transition-opacity group-hover:opacity-100 focus-within:opacity-100">
          <button
            type="button"
            onClick={() => onMove(-1)}
            aria-label="Geser ke kiri"
            className="grid size-5 place-items-center bg-black/60 text-white"
          >
            <ChevronLeft className="size-3" />
          </button>
          <button
            type="button"
            onClick={() => onMove(1)}
            aria-label="Geser ke kanan"
            className="grid size-5 place-items-center bg-black/60 text-white"
          >
            <ChevronRight className="size-3" />
          </button>
        </span>
      ) : null}
    </div>
  );
}


/* --- draft construction ---------------------------------------------------- */

/**
 * Turns picked files into drafts, dropping any that fail the local checks and
 * reporting why.
 */
export function makeDrafts(
  files: File[],
  asDocument: boolean,
): { drafts: Draft[]; rejected: string[] } {
  const drafts: Draft[] = [];
  const rejected: string[] = [];

  for (const file of files) {
    const problem = validateFile(file, asDocument);
    if (problem) {
      rejected.push(`${file.name}: ${problem}`);
      continue;
    }
    const kind = kindOfFile(file, asDocument);
    drafts.push({
      token: crypto.randomUUID(),
      file,
      name: file.name,
      kind,
      asDocument,
      caption: '',
      // Only media gets an object URL; a 90 MB document has nothing to preview
      // and would hold the memory for nothing.
      previewUrl: kind === 'image' || kind === 'video' ? URL.createObjectURL(file) : null,
      edited: false,
      status: 'idle',
      progress: 0,
      error: null,
    });
  }
  return { drafts, rejected };
}

/** Releases every object URL a draft list holds. */
export function releaseDrafts(drafts: Draft[]): void {
  for (const draft of drafts) {
    if (draft.previewUrl) URL.revokeObjectURL(draft.previewUrl);
  }
}
