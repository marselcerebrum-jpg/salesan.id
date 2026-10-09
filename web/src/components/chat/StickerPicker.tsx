'use client';

import clsx from 'clsx';
import { Loader2, Plus, Sticker as StickerIcon, Trash2 } from 'lucide-react';
import { useEffect, useRef, useState } from 'react';
import useSWR from 'swr';

import { addSticker, deleteSticker, fetcher } from '@/lib/api';
import { toStickerFile } from '@/lib/media';
import type { Sticker } from '@/lib/types';

/**
 * The sticker tray, beside the emoji one.
 *
 * Where WhatsApp puts it, and for the same reason: a sticker is something you
 * reach for while writing, not a file you go and find. The first version of
 * this was a tick box inside the attachment screen, which meant picking a file,
 * ticking, then sending — three steps for the thing WhatsApp does in one tap.
 *
 * Pressing a tile sends it immediately. There is no preview step because the
 * tile is the preview, and nothing else about a sticker needs deciding: it
 * carries no caption and cannot be resized.
 */
export function StickerPicker({
  disabled,
  onSend,
}: {
  disabled: boolean;
  /** Sends the sticker; the composer owns which conversation that is. */
  onSend: (sticker: Sticker) => Promise<void>;
}) {
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const wrapRef = useRef<HTMLDivElement>(null);
  const fileRef = useRef<HTMLInputElement>(null);

  // Fetched only while the tray is open. A panel nobody has opened should not
  // cost a request on every thread they click through.
  const { data, mutate } = useSWR<{ stickers: Sticker[] }>(
    open ? '/stickers' : null,
    fetcher,
  );
  const stickers = data?.stickers ?? [];

  useEffect(() => {
    if (!open) return;
    const away = (event: MouseEvent) => {
      if (!wrapRef.current?.contains(event.target as Node)) setOpen(false);
    };
    const esc = (event: KeyboardEvent) => {
      if (event.key === 'Escape') setOpen(false);
    };
    document.addEventListener('mousedown', away);
    document.addEventListener('keydown', esc);
    return () => {
      document.removeEventListener('mousedown', away);
      document.removeEventListener('keydown', esc);
    };
  }, [open]);

  async function send(sticker: Sticker) {
    setBusy(sticker.id);
    setError(null);
    try {
      await onSend(sticker);
      setOpen(false);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Stiker gagal dikirim.');
    } finally {
      setBusy(null);
    }
  }

  async function add(file: File) {
    setBusy('add');
    setError(null);
    try {
      // Converted here, not on the server: only the browser can show what the
      // sticker will look like, and WhatsApp takes WebP at 512 square or
      // nothing at all.
      const webp = await toStickerFile(file);
      await addSticker(webp, file.name.replace(/\.[^.]+$/, ''));
      await mutate();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Stiker gagal ditambahkan.');
    } finally {
      setBusy(null);
    }
  }

  async function remove(sticker: Sticker) {
    setBusy(sticker.id);
    setError(null);
    try {
      await deleteSticker(sticker.id);
      await mutate();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Stiker gagal dihapus.');
    } finally {
      setBusy(null);
    }
  }

  return (
    <div ref={wrapRef} className="relative">
      <button
        type="button"
        disabled={disabled}
        onClick={() => setOpen((v) => !v)}
        aria-label="Stiker"
        aria-expanded={open}
        className="grid size-10 shrink-0 place-items-center rounded-full text-wa-text-2 transition-colors hover:bg-wa-active disabled:opacity-40"
      >
        <StickerIcon className="size-6" />
      </button>

      {open ? (
        <div className="absolute bottom-full left-0 z-30 mb-2 w-[340px] overflow-hidden rounded-xl border border-wa-border bg-wa-panel shadow-e4">
          <div className="flex items-center justify-between border-b border-wa-border px-3 py-2">
            <span className="text-sm font-medium text-wa-text">Stiker</span>
            <button
              type="button"
              disabled={busy !== null}
              onClick={() => fileRef.current?.click()}
              className="inline-flex items-center gap-1 rounded-md px-2 py-1 text-xs text-wa-accent hover:bg-wa-active disabled:opacity-50"
            >
              {busy === 'add' ? (
                <Loader2 className="size-3.5 animate-spin" />
              ) : (
                <Plus className="size-3.5" />
              )}
              Tambah
            </button>
          </div>

          <input
            ref={fileRef}
            type="file"
            hidden
            accept="image/*"
            onChange={(event) => {
              const file = event.target.files?.[0];
              event.target.value = '';
              if (file) void add(file);
            }}
          />

          {error ? (
            <p className="border-b border-wa-border bg-danger-soft/40 px-3 py-2 text-xs text-danger">
              {error}
            </p>
          ) : null}

          <div className="max-h-[300px] overflow-y-auto p-2">
            {stickers.length === 0 ? (
              <p className="px-2 py-8 text-center text-xs text-wa-text-2">
                Belum ada stiker. Tekan Tambah dan pilih gambar — gambarnya diubah
                jadi stiker otomatis.
              </p>
            ) : (
              <div className="grid grid-cols-4 gap-2">
                {stickers.map((sticker) => (
                  <div key={sticker.id} className="group relative">
                    <button
                      type="button"
                      disabled={busy !== null || !sticker.url}
                      onClick={() => void send(sticker)}
                      title={sticker.name ?? 'Stiker'}
                      className={clsx(
                        'grid aspect-square w-full place-items-center rounded-lg p-1 transition-colors',
                        'hover:bg-wa-active disabled:opacity-40',
                      )}
                    >
                      {busy === sticker.id ? (
                        <Loader2 className="size-5 animate-spin text-wa-text-2" />
                      ) : sticker.url ? (
                        // eslint-disable-next-line @next/next/no-img-element -- signed URL on another origin
                        <img
                          src={sticker.url}
                          alt={sticker.name ?? ''}
                          className="size-full object-contain"
                        />
                      ) : (
                        // The row came back without a link, which means the
                        // file could not be signed. Saying so beats a blank
                        // square the operator keeps pressing.
                        <span className="text-2xs text-wa-text-2">rusak</span>
                      )}
                    </button>

                    <button
                      type="button"
                      disabled={busy !== null}
                      onClick={() => void remove(sticker)}
                      aria-label={`Hapus stiker ${sticker.name ?? ''}`}
                      className="absolute -top-1 -right-1 hidden size-5 place-items-center rounded-full bg-danger text-white group-hover:grid disabled:opacity-50"
                    >
                      <Trash2 className="size-3" />
                    </button>
                  </div>
                ))}
              </div>
            )}
          </div>
        </div>
      ) : null}
    </div>
  );
}
