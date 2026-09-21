'use client';

import clsx from 'clsx';
import { BarChart3, Camera, FileText, Headphones, ImagePlus, Paperclip, Plus } from 'lucide-react';
import { useEffect, useRef, useState } from 'react';

import { PHOTO_VIDEO_ACCEPT } from '@/lib/media';

export type PickerKind = 'photo-video' | 'document' | 'audio' | 'camera' | 'poll';

interface AttachmentMenuProps {
  disabled: boolean;
  onPick: (kind: PickerKind) => void;
}

const ITEMS: Array<{
  kind: PickerKind;
  label: string;
  icon: typeof FileText;
  tint: string;
}> = [
  { kind: 'document', label: 'Dokumen', icon: FileText, tint: '#7f66ff' },
  { kind: 'photo-video', label: 'Foto & video', icon: ImagePlus, tint: '#007bfc' },
  { kind: 'camera', label: 'Kamera', icon: Camera, tint: '#e5486f' },
  { kind: 'audio', label: 'Audio', icon: Headphones, tint: '#e5a900' },
  { kind: 'poll', label: 'Polling', icon: BarChart3, tint: '#00a884' },
];

/**
 * The composer's "+" button and its menu.
 *
 * Photo/video and document come first, and they are the two entries that lead
 * to the full preview flow. Camera reuses the same file input with a capture
 * hint, which is all a browser can offer without a permission prompt of its
 * own. Polling opens its own builder rather than a file picker.
 */
export function AttachmentMenu({ disabled, onPick }: AttachmentMenuProps) {
  const [open, setOpen] = useState(false);
  const wrapRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;

    const onPointerDown = (event: MouseEvent) => {
      if (!wrapRef.current?.contains(event.target as Node)) setOpen(false);
    };
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') setOpen(false);
    };

    document.addEventListener('mousedown', onPointerDown);
    document.addEventListener('keydown', onKeyDown);
    return () => {
      document.removeEventListener('mousedown', onPointerDown);
      document.removeEventListener('keydown', onKeyDown);
    };
  }, [open]);

  return (
    <div ref={wrapRef} className="relative">
      <button
        type="button"
        disabled={disabled}
        onClick={() => setOpen((v) => !v)}
        aria-label="Lampirkan"
        aria-expanded={open}
        aria-haspopup="menu"
        className={clsx(
          'grid size-[42px] shrink-0 place-items-center rounded-full text-wa-text-2 transition-all',
          'hover:bg-wa-active disabled:opacity-40 disabled:hover:bg-transparent',
          open ? 'rotate-45' : '',
        )}
      >
        {open ? <Plus className="size-6" /> : <Paperclip className="size-[22px]" />}
      </button>

      {open ? (
        <div
          role="menu"
          className="absolute bottom-[52px] left-0 z-20 w-52 overflow-hidden rounded-xl border border-wa-border bg-wa-panel-2 py-1.5 shadow-e3"
        >
          {ITEMS.map(({ kind, label, icon: Icon, tint }) => (
            <button
              key={kind}
              type="button"
              role="menuitem"
              onClick={() => {
                setOpen(false);
                onPick(kind);
              }}
              className="flex w-full items-center gap-3 px-4 py-2.5 text-left text-sm text-wa-text transition-colors hover:bg-wa-active"
            >
              <Icon className="size-5 shrink-0" style={{ color: tint }} />
              {label}
            </button>
          ))}
        </div>
      ) : null}
    </div>
  );
}

/** The `accept` and `capture` attributes each picker needs. */
export function pickerAttributes(kind: PickerKind): {
  accept: string;
  capture?: 'environment';
  multiple: boolean;
} {
  switch (kind) {
    case 'photo-video':
      return { accept: PHOTO_VIDEO_ACCEPT, multiple: true };
    case 'camera':
      // capture asks a phone browser for the camera directly; a desktop browser
      // ignores it and opens the usual file dialog.
      return { accept: 'image/*', capture: 'environment', multiple: false };
    case 'audio':
      return { accept: 'audio/*', multiple: true };
    default:
      // Documents are unrestricted here on purpose: the server holds the real
      // allowlist, and a narrow accept filter would hide files it would accept.
      return { accept: '', multiple: true };
  }
}
