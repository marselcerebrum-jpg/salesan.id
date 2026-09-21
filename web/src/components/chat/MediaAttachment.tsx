'use client';

import clsx from 'clsx';
import {
  AlertCircle,
  Clock3,
  Download,
  FileArchive,
  FileSpreadsheet,
  FileText,
  File as FileIcon,
  Loader2,
  Play,
  RotateCw,
} from 'lucide-react';
import { useState } from 'react';

import { getAttachmentUrl } from '@/lib/api';
import {
  cacheLink,
  formatBytes,
  formatDuration,
  KIND_LABEL,
  mediaBoxStyle,
  stickerBoxStyle,
  thumbnailSrc,
} from '@/lib/media';
import { attachmentFailureText, useAttachmentUrl, useOnScreen } from '@/lib/useAttachmentUrl';
import type { Attachment } from '@/lib/types';

interface MediaAttachmentProps {
  attachment: Attachment;
  /** Opens the full-screen viewer; absent for kinds that have no viewer. */
  onOpen?: (attachment: Attachment) => void;
  /** True when the bubble has a caption below, which changes the corners. */
  hasCaption: boolean;
}

/** Routes an attachment to the right renderer. */
export function MediaAttachment({ attachment, onOpen, hasCaption }: MediaAttachmentProps) {
  // Past the retention window the file is deleted along with everything that
  // could fetch it again, so there is nothing to load and no retry to offer.
  if (attachment.status === 'expired') {
    return <ExpiredAttachment attachment={attachment} />;
  }

  switch (attachment.kind) {
    case 'image':
    case 'sticker':
      return <ImageAttachment attachment={attachment} onOpen={onOpen} hasCaption={hasCaption} />;
    case 'video':
      return <VideoAttachment attachment={attachment} onOpen={onOpen} hasCaption={hasCaption} />;
    case 'audio':
      return <AudioAttachment attachment={attachment} />;
    default:
      return <DocumentAttachment attachment={attachment} />;
  }
}

/* --- image ----------------------------------------------------------------- */

function ImageAttachment({ attachment, onOpen, hasCaption }: MediaAttachmentProps) {
  const [ref, onScreen] = useOnScreen<HTMLButtonElement>();
  const { url, loading, error, gone, expired, reload } = useAttachmentUrl(attachment, onScreen);
  const thumb = thumbnailSrc(attachment);
  const isSticker = attachment.kind === 'sticker';

  // The thumbnail carries the image while the full version loads, so the bubble
  // never collapses and then jumps back to size.
  const src = url ?? thumb;

  return (
    <button
      ref={ref}
      type="button"
      onClick={() => url && onOpen?.(attachment)}
      disabled={!url}
      aria-label={url ? 'Buka gambar' : 'Memuat gambar'}
      className={clsx(
        'group relative block max-w-full overflow-hidden bg-black/5 dark:bg-white/5',
        isSticker ? 'rounded-lg bg-transparent' : 'rounded-md',
        hasCaption ? '' : 'mb-[2px]',
        url ? 'cursor-zoom-in' : 'cursor-default',
      )}
      style={isSticker ? stickerBoxStyle(attachment) : mediaBoxStyle(attachment)}
    >
      {src ? (
        /* Signed URLs are short-lived and on another origin, so next/image
           cannot optimise them; a plain <img> is the right tool here. */
        // eslint-disable-next-line @next/next/no-img-element
        <img
          src={src}
          alt={attachment.file_name ?? 'Gambar'}
          className={clsx(
            'h-full w-full',
            isSticker ? 'object-contain' : 'object-cover',
            // Only the low-res placeholder is blurred, and only while the real
            // one is still on its way.
            !url && thumb ? 'scale-105 blur-[6px]' : '',
          )}
        />
      ) : (
        <div className="grid h-full w-full place-items-center bg-black/10 dark:bg-white/10" />
      )}

      {loading && !url ? <Veil><Loader2 className="size-6 animate-spin text-white" /></Veil> : null}
      {error ? (
        <Veil>
          <Failure message={attachmentFailureText(gone, expired)} onRetry={gone ? undefined : reload} />
        </Veil>
      ) : null}
    </button>
  );
}

/* --- video ----------------------------------------------------------------- */

function VideoAttachment({ attachment, onOpen, hasCaption }: MediaAttachmentProps) {
  const [ref, onScreen] = useOnScreen<HTMLButtonElement>();
  const { url, loading, error, gone, expired, reload } = useAttachmentUrl(attachment, onScreen);
  const thumb = thumbnailSrc(attachment);
  const duration = formatDuration(attachment.duration_secs);

  return (
    <button
      ref={ref}
      type="button"
      onClick={() => url && onOpen?.(attachment)}
      disabled={!url}
      aria-label={url ? 'Putar video' : 'Memuat video'}
      className={clsx(
        'relative block max-w-full overflow-hidden rounded-md bg-black/70',
        hasCaption ? '' : 'mb-[2px]',
        url ? 'cursor-pointer' : 'cursor-default',
      )}
      style={mediaBoxStyle(attachment)}
    >
      {thumb ? (
        // eslint-disable-next-line @next/next/no-img-element -- see above
        <img src={thumb} alt="" className="h-full w-full object-cover" />
      ) : null}

      {url && !error ? (
        <Veil>
          <span className="grid size-[52px] place-items-center rounded-full bg-black/55 ring-1 ring-white/25">
            <Play className="size-6 translate-x-[1px] fill-white text-white" />
          </span>
        </Veil>
      ) : null}

      {loading && !url ? <Veil><Loader2 className="size-6 animate-spin text-white" /></Veil> : null}
      {error ? (
        <Veil>
          <Failure
            message={expired ? attachmentFailureText(gone, expired) : gone ? 'Video tidak tersedia lagi' : 'Gagal memuat'}
            onRetry={gone ? undefined : reload}
          />
        </Veil>
      ) : null}

      {duration ? (
        <span className="absolute bottom-1.5 left-1.5 rounded bg-black/55 px-1.5 py-0.5 text-2xs font-medium text-white">
          {duration}
        </span>
      ) : null}
    </button>
  );
}

/* --- audio ----------------------------------------------------------------- */

function AudioAttachment({ attachment }: { attachment: Attachment }) {
  const [ref, onScreen] = useOnScreen<HTMLDivElement>();
  const { url, loading, error, gone, expired, reload } = useAttachmentUrl(attachment, onScreen);

  return (
    <div ref={ref} className="min-w-[240px] py-1">
      {url ? (
        // The native player is deliberate: it brings scrubbing, volume, speed
        // and keyboard control that a hand-rolled one would only approximate.
        <audio src={url} controls preload="metadata" className="h-9 w-full max-w-[320px]" />
      ) : error ? (
        <Failure
          message={
            expired
              ? attachmentFailureText(gone, expired)
              : gone
                ? 'Audio tidak tersedia lagi'
                : 'Gagal memuat audio'
          }
          onRetry={gone ? undefined : reload}
          tone="dark"
        />
      ) : (
        <p className="flex items-center gap-2 text-sm text-wa-text-2">
          {loading ? <Loader2 className="size-4 animate-spin" /> : null}
          Memuat audio…
        </p>
      )}
      {attachment.duration_secs ? (
        <p className="mt-0.5 text-2xs text-wa-text-2">{formatDuration(attachment.duration_secs)}</p>
      ) : null}
    </div>
  );
}

/* --- document -------------------------------------------------------------- */

const DOC_ICON: Record<string, typeof FileText> = {
  pdf: FileText,
  doc: FileText,
  docx: FileText,
  txt: FileText,
  md: FileText,
  csv: FileSpreadsheet,
  xls: FileSpreadsheet,
  xlsx: FileSpreadsheet,
  ods: FileSpreadsheet,
  zip: FileArchive,
};

function DocumentAttachment({ attachment }: { attachment: Attachment }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const name = attachment.file_name ?? 'Dokumen';
  const ext = name.includes('.') ? name.split('.').pop()!.toLowerCase() : '';
  const Icon = DOC_ICON[ext] ?? FileIcon;

  // The URL is fetched on click, not on render: a thread full of documents
  // should not mint dozens of signed URLs nobody opens.
  async function download() {
    if (busy) return;
    setBusy(true);
    setError(null);
    try {
      const link = await getAttachmentUrl(attachment.id, true);
      cacheLink(attachment.id, link.url, link.expires_at);
      // Content-Disposition on the signed URL supplies the filename; the
      // download attribute is ignored cross-origin, so it cannot do that job.
      window.open(link.url, '_blank', 'noopener,noreferrer');
    } catch {
      setError('Gagal mengunduh');
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="mb-[2px] w-[min(320px,100%)] rounded-md bg-black/[0.04] p-2 dark:bg-white/[0.06]">
      <button
        type="button"
        onClick={() => void download()}
        className="flex w-full items-center gap-3 text-left"
      >
        <span className="grid size-10 shrink-0 place-items-center rounded-md bg-wa-panel-2 text-wa-text-2">
          {busy ? <Loader2 className="size-5 animate-spin" /> : <Icon className="size-5" />}
        </span>
        <span className="min-w-0 flex-1">
          <span className="block truncate text-sm text-wa-text">{name}</span>
          <span className="block text-2xs text-wa-text-2 uppercase">
            {[ext, formatBytes(attachment.size_bytes)].filter(Boolean).join(' · ')}
          </span>
        </span>
        <Download className="size-[18px] shrink-0 text-wa-text-2" />
      </button>
      {error ? <p className="mt-1 text-2xs text-danger">{error}</p> : null}
    </div>
  );
}

/* --- expired --------------------------------------------------------------- */

/**
 * A file we deleted from our own side after the retention window.
 *
 * The wording matters more than it looks. Only our copy is gone: the message is
 * still in the chat on the phone that sent or received it, and the earlier
 * "sudah kedaluwarsa / otomatis dihapus" read as though the file itself had
 * ceased to exist. Somebody who needs it should be told where it still is, not
 * left thinking it is lost.
 */
function ExpiredAttachment({ attachment }: { attachment: Attachment }) {
  const label = KIND_LABEL[attachment.kind] ?? 'Berkas';
  return (
    <div className="mb-[2px] w-[min(320px,100%)] rounded-md border border-dashed border-wa-text-2/30 bg-black/[0.03] px-3 py-3 dark:bg-white/[0.04]">
      <p className="flex items-center gap-2 text-sm text-wa-text-2">
        <Clock3 className="size-4 shrink-0" />
        Berkas sudah expired, silakan cek di HP
      </p>
      <p className="mt-1 text-2xs text-wa-text-2">
        {label} disimpan di sini selama 7 hari, lalu dihapus dari server. Pesannya tetap ada di
        WhatsApp pada HP Anda.
        {attachment.file_name ? ` (${attachment.file_name})` : ''}
      </p>
    </div>
  );
}

/* --- shared ---------------------------------------------------------------- */

function Veil({ children }: { children: React.ReactNode }) {
  return <span className="absolute inset-0 grid place-items-center">{children}</span>;
}

function Failure({
  message,
  onRetry,
  tone = 'light',
}: {
  message: string;
  onRetry?: () => void;
  tone?: 'light' | 'dark';
}) {
  return (
    <span
      className={clsx(
        'flex items-center gap-2 rounded-md px-2.5 py-1.5 text-xs',
        tone === 'light' ? 'bg-black/60 text-white' : 'text-danger',
      )}
    >
      <AlertCircle className="size-4 shrink-0" />
      {message}
      {onRetry ? (
        <span
          role="button"
          tabIndex={0}
          onClick={(event) => {
            event.stopPropagation();
            onRetry();
          }}
          onKeyDown={(event) => {
            if (event.key === 'Enter' || event.key === ' ') {
              event.stopPropagation();
              onRetry();
            }
          }}
          className="inline-flex items-center gap-1 underline underline-offset-2"
        >
          <RotateCw className="size-3" />
          Coba lagi
        </span>
      ) : null}
    </span>
  );
}
