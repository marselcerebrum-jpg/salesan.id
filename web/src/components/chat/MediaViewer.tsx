'use client';

import clsx from 'clsx';
import {
  ChevronLeft,
  ChevronRight,
  CornerUpLeft,
  Download,
  Loader2,
  Maximize2,
  RotateCw,
  Share2,
  X,
  ZoomIn,
  ZoomOut,
} from 'lucide-react';
import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react';

import { getAttachmentUrl } from '@/lib/api';
import { cacheLink, formatBytes } from '@/lib/media';
import { attachmentFailureText, useAttachmentUrl } from '@/lib/useAttachmentUrl';
import type { Attachment } from '@/lib/types';

export type ViewerAction = 'reply' | 'forward';

interface MediaViewerProps {
  /** Everything openable in this thread, so arrow keys can move between them. */
  items: Attachment[];
  /** Index into `items`; null closes the viewer. */
  index: number | null;
  onIndexChange: (index: number) => void;
  onClose: () => void;
  /** Rendered as the caption strip under the media. */
  captionFor?: (attachment: Attachment) => string | null;
  /** Who sent it and when, shown top-left as WhatsApp does. */
  senderFor?: (attachment: Attachment) => { name: string; time: string } | null;
  onAction?: (attachment: Attachment, action: ViewerAction) => void;
}

/** Multipliers applied on top of the fit-to-screen scale. */
const ZOOM_STEPS = [1, 1.5, 2, 3, 4];

/**
 * Full-screen viewer for images and video.
 *
 * The image is drawn at its natural size and scaled by a factor computed from
 * the space available, so it always fits whichever way round it is — and, after
 * a rotation, fits the *rotated* box rather than the original one. CSS
 * `max-height` alone cannot do that: it is applied before the transform, which
 * is why a rotated landscape photo used to be cut off at the sides.
 *
 * Nothing is ever enlarged past 100%. A small image shown at four times its
 * size is not more visible, only blurrier.
 *
 * Zoom and rotation are view-only — they never touch the stored file.
 */
export function MediaViewer({
  items,
  index,
  onIndexChange,
  onClose,
  captionFor,
  senderFor,
  onAction,
}: MediaViewerProps) {
  const open = index !== null && index >= 0 && index < items.length;
  const attachment = open ? items[index] : null;

  const [zoom, setZoom] = useState(1);
  const [rotation, setRotation] = useState(0);
  const [offset, setOffset] = useState({ x: 0, y: 0 });
  const [downloading, setDownloading] = useState(false);
  const [natural, setNatural] = useState<{ w: number; h: number } | null>(null);
  const [box, setBox] = useState({ w: 0, h: 0 });

  const drag = useRef<{ x: number; y: number; ox: number; oy: number } | null>(null);
  const stageRef = useRef<HTMLDivElement>(null);
  const { url, loading, error, gone, expired, reload, reportUnreadable } =
    useAttachmentUrl(attachment, open);

  // Each new item starts from a clean view rather than inheriting the last
  // one's zoom and rotation.
  useEffect(() => {
    setZoom(1);
    setRotation(0);
    setOffset({ x: 0, y: 0 });
    setNatural(null);
  }, [attachment?.id]);

  // The fit depends on the space available, so it has to be measured rather
  // than assumed — and re-measured when the window changes.
  useLayoutEffect(() => {
    const node = stageRef.current;
    if (!node) return;

    const measure = () => setBox({ w: node.clientWidth, h: node.clientHeight });
    measure();

    if (typeof ResizeObserver === 'undefined') {
      window.addEventListener('resize', measure);
      return () => window.removeEventListener('resize', measure);
    }
    const observer = new ResizeObserver(measure);
    observer.observe(node);
    return () => observer.disconnect();
  }, [open]);

  const step = useCallback(
    (delta: number) => {
      if (index === null) return;
      const next = index + delta;
      if (next >= 0 && next < items.length) onIndexChange(next);
    },
    [index, items.length, onIndexChange],
  );

  // Callers pass inline handlers, so the effect reads them through refs to
  // avoid re-binding the key listener on every render.
  const handlers = useRef({ onClose, step });
  handlers.current = { onClose, step };

  useEffect(() => {
    if (!open) return;

    const onKeyDown = (event: KeyboardEvent) => {
      switch (event.key) {
        case 'Escape':
          handlers.current.onClose();
          break;
        case 'ArrowLeft':
          handlers.current.step(-1);
          break;
        case 'ArrowRight':
          handlers.current.step(1);
          break;
        case '+':
        case '=':
          setZoom((z) => nextZoom(z, 1));
          break;
        case '-':
          setZoom((z) => nextZoom(z, -1));
          break;
        case '0':
          setZoom(1);
          setOffset({ x: 0, y: 0 });
          break;
        case 'r':
        case 'R':
          setRotation((r) => (r + 90) % 360);
          setOffset({ x: 0, y: 0 });
          break;
        default:
          break;
      }
    };
    document.addEventListener('keydown', onKeyDown);

    const previousOverflow = document.body.style.overflow;
    document.body.style.overflow = 'hidden';

    return () => {
      document.removeEventListener('keydown', onKeyDown);
      document.body.style.overflow = previousOverflow;
    };
  }, [open]);

  if (!open || !attachment) return null;

  const isVideo = attachment.kind === 'video';
  const caption = captionFor?.(attachment) ?? null;
  const sender = senderFor?.(attachment) ?? null;

  // Scale that makes the whole picture fit, accounting for a quarter turn
  // having swapped its width and height. Never above 1: upscaling adds pixels,
  // not detail.
  const quarterTurned = rotation === 90 || rotation === 270;
  const fit =
    natural && box.w > 0 && box.h > 0
      ? Math.min(
          box.w / (quarterTurned ? natural.h : natural.w),
          box.h / (quarterTurned ? natural.w : natural.h),
          1,
        )
      : 1;
  const scale = fit * zoom;
  const zoomed = zoom > 1;

  async function download() {
    if (!attachment || downloading) return;
    setDownloading(true);
    try {
      const link = await getAttachmentUrl(attachment.id, true);
      cacheLink(attachment.id, link.url, link.expires_at);
      window.open(link.url, '_blank', 'noopener,noreferrer');
    } finally {
      setDownloading(false);
    }
  }

  return (
    <div className="fixed inset-0 z-[60] flex flex-col bg-[#0b141a]">
      <header className="flex items-center gap-1 border-b border-white/10 px-3 py-2 text-white/85">
        <div className="min-w-0 flex-1 pr-2">
          <p className="truncate text-sm font-medium">
            {sender?.name ?? attachment.file_name ?? 'Media'}
          </p>
          <p className="truncate text-xs text-white/50">
            {[
              sender?.time,
              formatBytes(attachment.size_bytes),
              items.length > 1 ? `${(index ?? 0) + 1} dari ${items.length}` : '',
            ]
              .filter(Boolean)
              .join(' · ')}
          </p>
        </div>

        {!isVideo ? (
          <>
            <ToolButton label="Perkecil" onClick={() => setZoom((z) => nextZoom(z, -1))} disabled={zoom <= 1}>
              <ZoomOut className="size-5" />
            </ToolButton>
            <button
              type="button"
              onClick={() => {
                setZoom(1);
                setOffset({ x: 0, y: 0 });
              }}
              title="Paskan ke layar"
              className="w-14 rounded-md py-1 text-center text-xs tabular-nums text-white/60 hover:bg-white/10"
            >
              {Math.round(scale * 100)}%
            </button>
            <ToolButton
              label="Perbesar"
              onClick={() => setZoom((z) => nextZoom(z, 1))}
              disabled={zoom >= ZOOM_STEPS[ZOOM_STEPS.length - 1]}
            >
              <ZoomIn className="size-5" />
            </ToolButton>
            <ToolButton
              label="Paskan ke layar"
              disabled={zoom === 1 && offset.x === 0 && offset.y === 0}
              onClick={() => {
                setZoom(1);
                setOffset({ x: 0, y: 0 });
              }}
            >
              <Maximize2 className="size-5" />
            </ToolButton>
            <ToolButton
              label="Putar"
              onClick={() => {
                setRotation((r) => (r + 90) % 360);
                setOffset({ x: 0, y: 0 });
              }}
            >
              <RotateCw className="size-5" />
            </ToolButton>
            <span className="mx-1 h-6 w-px bg-white/15" />
          </>
        ) : null}

        {onAction ? (
          <>
            <ToolButton label="Balas" onClick={() => onAction(attachment, 'reply')}>
              <CornerUpLeft className="size-5" />
            </ToolButton>
            <ToolButton label="Teruskan" onClick={() => onAction(attachment, 'forward')}>
              <Share2 className="size-5" />
            </ToolButton>
          </>
        ) : null}

        <ToolButton label="Unduh" onClick={() => void download()} disabled={downloading}>
          {downloading ? <Loader2 className="size-5 animate-spin" /> : <Download className="size-5" />}
        </ToolButton>
        <ToolButton label="Tutup" onClick={onClose}>
          <X className="size-5" />
        </ToolButton>
      </header>

      <div
        ref={stageRef}
        className={clsx(
          'relative flex min-h-0 flex-1 items-center justify-center overflow-hidden p-4',
          zoomed ? 'cursor-grab active:cursor-grabbing' : '',
        )}
        onMouseDown={(event) => {
          if (!zoomed) return;
          drag.current = { x: event.clientX, y: event.clientY, ox: offset.x, oy: offset.y };
        }}
        onMouseMove={(event) => {
          const d = drag.current;
          if (!d) return;
          setOffset({ x: d.ox + (event.clientX - d.x), y: d.oy + (event.clientY - d.y) });
        }}
        onMouseUp={() => {
          drag.current = null;
        }}
        onMouseLeave={() => {
          drag.current = null;
        }}
        // Clicking the empty space closes, but only the empty space itself —
        // not the picture, and not while panning a zoomed one.
        onClick={(event) => {
          if (event.target === event.currentTarget && !zoomed) onClose();
        }}
      >
        {loading && !url ? (
          <Loader2 className="size-8 animate-spin text-white/70" />
        ) : error ? (
          <div className="text-center text-white/75">
            <p className="text-sm">
              {attachmentFailureText(attachment?.kind ?? 'image', gone, expired, error)}
            </p>
            {gone || expired ? (
              <p className="mt-1 text-xs text-white/55">
                Berkas hanya disimpan sementara di server. Pesannya tetap ada di WhatsApp pada HP
                Anda.
              </p>
            ) : null}
            {!gone ? (
              <button
                type="button"
                onClick={reload}
                className="mt-3 rounded-lg bg-white/10 px-3 py-1.5 text-sm hover:bg-white/15"
              >
                Coba lagi
              </button>
            ) : null}
          </div>
        ) : url && isVideo ? (
          <video src={url} controls autoPlay playsInline className="max-h-full max-w-full rounded-md" />
        ) : url ? (
          /* Short-lived signed URL on another origin — see MediaAttachment. */
          // eslint-disable-next-line @next/next/no-img-element
          <img
            src={url}
            alt={attachment.file_name ?? 'Gambar'}
            draggable={false}
            // Same reason as the bubble: only the browser finds out that the
            // object behind a perfectly valid signed URL is not there.
            onError={reportUnreadable}
            onLoad={(event) =>
              setNatural({
                w: event.currentTarget.naturalWidth,
                h: event.currentTarget.naturalHeight,
              })
            }
            onDoubleClick={() => {
              setZoom((z) => (z > 1 ? 1 : 2));
              setOffset({ x: 0, y: 0 });
            }}
            style={{
              // Drawn at natural size; the transform does all the sizing, so a
              // rotation is measured against the box the picture actually
              // occupies rather than the one it started in.
              width: natural ? natural.w : undefined,
              height: natural ? natural.h : undefined,
              maxWidth: natural ? 'none' : '100%',
              maxHeight: natural ? 'none' : '100%',
              transform: `translate(${offset.x}px, ${offset.y}px) scale(${scale}) rotate(${rotation}deg)`,
              transition: drag.current ? 'none' : 'transform 160ms ease-out',
            }}
            className="max-w-none select-none"
          />
        ) : null}

        {items.length > 1 ? (
          <>
            <NavButton side="left" disabled={(index ?? 0) === 0} onClick={() => step(-1)} />
            <NavButton
              side="right"
              disabled={(index ?? 0) >= items.length - 1}
              onClick={() => step(1)}
            />
          </>
        ) : null}
      </div>

      {caption ? (
        <footer className="max-h-32 overflow-y-auto border-t border-white/10 px-6 py-3 text-center text-sm whitespace-pre-wrap text-white/85">
          {caption}
        </footer>
      ) : null}
    </div>
  );
}

function nextZoom(current: number, direction: 1 | -1): number {
  const i = ZOOM_STEPS.findIndex((z) => z >= current - 0.001);
  const next = Math.min(Math.max(i + direction, 0), ZOOM_STEPS.length - 1);
  return ZOOM_STEPS[next];
}

function ToolButton({
  label,
  onClick,
  disabled,
  children,
}: {
  label: string;
  onClick: () => void;
  disabled?: boolean;
  children: React.ReactNode;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled}
      aria-label={label}
      title={label}
      className="grid size-9 shrink-0 place-items-center rounded-full text-white/80 transition-colors hover:bg-white/10 disabled:opacity-30 disabled:hover:bg-transparent"
    >
      {children}
    </button>
  );
}

function NavButton({
  side,
  onClick,
  disabled,
}: {
  side: 'left' | 'right';
  onClick: () => void;
  disabled: boolean;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled}
      aria-label={side === 'left' ? 'Sebelumnya' : 'Berikutnya'}
      className={clsx(
        'absolute top-1/2 grid size-11 -translate-y-1/2 place-items-center rounded-full bg-black/40 text-white/85',
        'transition-opacity hover:bg-black/60 disabled:pointer-events-none disabled:opacity-0',
        side === 'left' ? 'left-3' : 'right-3',
      )}
    >
      {side === 'left' ? <ChevronLeft className="size-6" /> : <ChevronRight className="size-6" />}
    </button>
  );
}
