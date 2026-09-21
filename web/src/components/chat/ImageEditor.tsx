'use client';

import clsx from 'clsx';
import {
  Check,
  Crop,
  Loader2,
  Pencil,
  RotateCw,
  Smile,
  Sparkles,
  Square,
  Type,
  Undo2,
  X,
} from 'lucide-react';
import { useCallback, useEffect, useRef, useState } from 'react';

/**
 * Full-screen image editor.
 *
 * Everything the operator draws is kept as a list of shapes in *image*
 * coordinates rather than painted straight onto pixels. That is what makes undo
 * exact, and what lets a rotation or a crop move the annotations with the photo
 * instead of leaving them stranded where they were drawn.
 *
 * Crop, rotate and enhance are different: they change the picture itself, so
 * they are baked into a new base canvas and the annotation coordinates are
 * transformed to match.
 */

type Tool = 'pen' | 'text' | 'shape' | 'emoji' | 'crop';

interface Stroke {
  kind: 'stroke';
  points: Array<{ x: number; y: number }>;
  color: string;
  width: number;
}
interface TextMark {
  kind: 'text';
  x: number;
  y: number;
  text: string;
  color: string;
  size: number;
}
interface ShapeMark {
  kind: 'shape';
  x: number;
  y: number;
  w: number;
  h: number;
  color: string;
  width: number;
}
interface EmojiMark {
  kind: 'emoji';
  x: number;
  y: number;
  char: string;
  size: number;
}
type Mark = Stroke | TextMark | ShapeMark | EmojiMark;

/** WhatsApp's own drawing palette, in its order. */
const COLORS = [
  '#ff3b30', '#ff9500', '#ffcc00', '#34c759',
  '#32ade6', '#007aff', '#af52de', '#ffffff',
];

const EMOJIS = ['😀', '😂', '😍', '👍', '🙏', '🔥', '🎉', '❤️', '✅', '❌', '⭐', '💯'];

/** Output ceilings. HD keeps the detail; standard is what WhatsApp would do. */
const STANDARD_MAX_EDGE = 1600;
const HD_MAX_EDGE = 3000;

interface ImageEditorProps {
  /** The image being edited. */
  file: File;
  onCancel: () => void;
  /** Receives the re-encoded image; the caller replaces the draft with it. */
  onApply: (edited: File) => void;
}

export function ImageEditor({ file, onCancel, onApply }: ImageEditorProps) {
  const [base, setBase] = useState<HTMLCanvasElement | null>(null);
  const [marks, setMarks] = useState<Mark[]>([]);
  const [tool, setTool] = useState<Tool>('pen');
  const [color, setColor] = useState(COLORS[0]);
  const [width, setWidth] = useState(6);
  const [hd, setHd] = useState(false);
  const [saving, setSaving] = useState(false);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [saveError, setSaveError] = useState<string | null>(null);
  const [emojiOpen, setEmojiOpen] = useState(false);

  // A pending text box: placed by clicking, committed by typing and pressing
  // Enter. Kept out of `marks` so an abandoned box leaves nothing behind.
  const [pendingText, setPendingText] = useState<{ x: number; y: number; value: string } | null>(
    null,
  );
  const [cropRect, setCropRect] = useState<{ x: number; y: number; w: number; h: number } | null>(
    null,
  );

  const canvasRef = useRef<HTMLCanvasElement>(null);
  const wrapRef = useRef<HTMLDivElement>(null);
  const drawing = useRef<Mark | null>(null);
  const history = useRef<Array<{ base: HTMLCanvasElement; marks: Mark[] }>>([]);
  // The stack itself is a ref because pushing to it must not re-render, but the
  // undo button's enabled state has to. Mutating the ref alone left the button
  // permanently greyed out no matter how much had been drawn.
  const [historyDepth, setHistoryDepth] = useState(0);

  /* --- loading ------------------------------------------------------------ */

  useEffect(() => {
    let cancelled = false;

    async function load() {
      try {
        const bitmap = await decode(file);
        if (cancelled) return;
        const canvas = document.createElement('canvas');
        canvas.width = bitmap.width;
        canvas.height = bitmap.height;
        canvas.getContext('2d')?.drawImage(bitmap, 0, 0);
        if ('close' in bitmap && typeof bitmap.close === 'function') bitmap.close();
        setBase(canvas);
      } catch {
        if (!cancelled) setLoadError('Gambar ini tidak bisa dibuka untuk diedit.');
      }
    }
    void load();

    return () => {
      cancelled = true;
    };
  }, [file]);

  /* --- rendering ---------------------------------------------------------- */

  // Scale from image pixels to the pixels actually on screen.
  const [scale, setScale] = useState(1);

  const render = useCallback(() => {
    const canvas = canvasRef.current;
    const wrap = wrapRef.current;
    if (!canvas || !base || !wrap) return;

    const box = wrap.getBoundingClientRect();
    const fit = Math.min(box.width / base.width, box.height / base.height, 1);
    const dpr = window.devicePixelRatio || 1;

    canvas.style.width = `${base.width * fit}px`;
    canvas.style.height = `${base.height * fit}px`;
    canvas.width = Math.round(base.width * fit * dpr);
    canvas.height = Math.round(base.height * fit * dpr);
    setScale(fit);

    const ctx = canvas.getContext('2d');
    if (!ctx) return;
    ctx.setTransform(fit * dpr, 0, 0, fit * dpr, 0, 0);
    ctx.clearRect(0, 0, base.width, base.height);
    ctx.drawImage(base, 0, 0);

    for (const mark of marks) paintMark(ctx, mark);
    // While cropping, the in-progress shape describes the crop window, not
    // something to draw — the dimmed overlay below is its only rendering.
    if (drawing.current && !cropRect) paintMark(ctx, drawing.current);

    if (cropRect) paintCropOverlay(ctx, base.width, base.height, cropRect);
  }, [base, marks, cropRect]);

  useEffect(() => {
    render();
  }, [render]);

  useEffect(() => {
    const onResize = () => render();
    window.addEventListener('resize', onResize);
    return () => window.removeEventListener('resize', onResize);
  }, [render]);

  /* --- history ------------------------------------------------------------ */

  const snapshot = useCallback(() => {
    if (!base) return;
    history.current.push({ base, marks });
    // Bounded so a long session cannot pin many full-size canvases in memory.
    if (history.current.length > 30) history.current.shift();
    setHistoryDepth(history.current.length);
  }, [base, marks]);

  function undo() {
    const previous = history.current.pop();
    if (!previous) return;
    setHistoryDepth(history.current.length);
    setBase(previous.base);
    setMarks(previous.marks);
    setCropRect(null);
    setPendingText(null);
  }

  /* --- pointer handling --------------------------------------------------- */

  function toImage(event: React.PointerEvent<HTMLCanvasElement>) {
    const rect = event.currentTarget.getBoundingClientRect();
    return {
      x: (event.clientX - rect.left) / scale,
      y: (event.clientY - rect.top) / scale,
    };
  }

  function onPointerDown(event: React.PointerEvent<HTMLCanvasElement>) {
    if (!base || pendingText) return;
    const p = toImage(event);
    event.currentTarget.setPointerCapture(event.pointerId);

    switch (tool) {
      case 'pen':
        drawing.current = { kind: 'stroke', points: [p], color, width };
        break;
      case 'shape':
        drawing.current = { kind: 'shape', x: p.x, y: p.y, w: 0, h: 0, color, width };
        break;
      case 'crop':
        setCropRect({ x: p.x, y: p.y, w: 0, h: 0 });
        drawing.current = { kind: 'shape', x: p.x, y: p.y, w: 0, h: 0, color, width };
        break;
      case 'text':
        setPendingText({ x: p.x, y: p.y, value: '' });
        break;
      default:
        break;
    }
  }

  function onPointerMove(event: React.PointerEvent<HTMLCanvasElement>) {
    const active = drawing.current;
    if (!active) return;
    const p = toImage(event);

    if (active.kind === 'stroke') {
      active.points.push(p);
    } else if (active.kind === 'shape') {
      active.w = p.x - active.x;
      active.h = p.y - active.y;
      if (tool === 'crop') {
        setCropRect(normalizeRect(active.x, active.y, active.w, active.h));
        drawing.current = active;
        return; // the crop overlay is drawn from cropRect, not as a mark
      }
    }
    render();
  }

  function onPointerUp() {
    const active = drawing.current;
    drawing.current = null;
    if (!active) return;

    if (tool === 'crop') {
      render();
      return;
    }
    // A tap with no movement leaves nothing to keep.
    if (active.kind === 'stroke' && active.points.length < 2) {
      render();
      return;
    }
    if (active.kind === 'shape' && (Math.abs(active.w) < 4 || Math.abs(active.h) < 4)) {
      render();
      return;
    }

    snapshot();
    setMarks((current) => [...current, active]);
  }

  /* --- transforms --------------------------------------------------------- */

  function rotate() {
    if (!base) return;
    snapshot();

    const rotated = document.createElement('canvas');
    rotated.width = base.height;
    rotated.height = base.width;
    const ctx = rotated.getContext('2d');
    if (!ctx) return;
    ctx.translate(rotated.width / 2, rotated.height / 2);
    ctx.rotate(Math.PI / 2);
    ctx.drawImage(base, -base.width / 2, -base.height / 2);

    // 90° clockwise sends (x, y) to (H - y, x), where H is the old height.
    const h = base.height;
    setMarks((current) => current.map((mark) => mapMark(mark, (x, y) => ({ x: h - y, y: x }))));
    setBase(rotated);
    setCropRect(null);
  }

  function applyCrop() {
    if (!base || !cropRect) return;
    const r = clampRect(cropRect, base.width, base.height);
    if (r.w < 8 || r.h < 8) {
      setCropRect(null);
      return;
    }
    snapshot();

    const cropped = document.createElement('canvas');
    cropped.width = Math.round(r.w);
    cropped.height = Math.round(r.h);
    cropped.getContext('2d')?.drawImage(base, -Math.round(r.x), -Math.round(r.y));

    setMarks((current) => current.map((mark) => mapMark(mark, (x, y) => ({ x: x - r.x, y: y - r.y }))));
    setBase(cropped);
    setCropRect(null);
    setTool('pen');
  }

  /**
   * A gentle contrast and saturation lift.
   *
   * Deliberately mild: this is for a photo taken in poor light, not a filter.
   * Applied to the base so it composes with everything else and undoes cleanly.
   */
  function enhance() {
    if (!base) return;
    snapshot();

    const out = document.createElement('canvas');
    out.width = base.width;
    out.height = base.height;
    const ctx = out.getContext('2d');
    if (!ctx) return;
    ctx.filter = 'contrast(1.12) saturate(1.14) brightness(1.05)';
    ctx.drawImage(base, 0, 0);
    setBase(out);
  }

  /** The mark an open text box would become, or null when it is empty. */
  function pendingTextMark(): TextMark | null {
    const value = pendingText?.value.trim();
    if (!pendingText || !value || !base) return null;
    return {
      kind: 'text',
      x: pendingText.x,
      y: pendingText.y,
      text: value,
      color,
      // Scaled to the image so text is the same relative size on a 4000px
      // photo as on a 800px screenshot.
      size: Math.max(18, Math.round(base.width * 0.045)),
    };
  }

  function commitText() {
    const mark = pendingTextMark();
    setPendingText(null);
    if (!mark) return;
    snapshot();
    setMarks((current) => [...current, mark]);
  }

  function placeEmoji(char: string) {
    if (!base) return;
    snapshot();
    setEmojiOpen(false);
    setMarks((current) => [
      ...current,
      {
        kind: 'emoji',
        // Stepped away from dead centre so a second emoji does not land
        // exactly on the first and look like nothing happened.
        x: base.width / 2 + (current.length % 4) * base.width * 0.06,
        y: base.height / 2 + (current.length % 3) * base.height * 0.06,
        char,
        size: Math.max(48, Math.round(base.width * 0.14)),
      },
    ]);
  }

  /* --- output ------------------------------------------------------------- */

  async function apply() {
    if (!base || saving) return;
    setSaving(true);
    // A text box still open would otherwise be lost: its blur handler commits
    // through setState, which has not landed by the time this reads `marks`.
    const pending = pendingTextMark();
    const finalMarks = pending ? [...marks, pending] : marks;
    try {
      const maxEdge = hd ? HD_MAX_EDGE : STANDARD_MAX_EDGE;
      const longest = Math.max(base.width, base.height);
      const ratio = longest > maxEdge ? maxEdge / longest : 1;

      const out = document.createElement('canvas');
      out.width = Math.round(base.width * ratio);
      out.height = Math.round(base.height * ratio);
      const ctx = out.getContext('2d');
      if (!ctx) throw new Error('no canvas context');
      ctx.scale(ratio, ratio);
      ctx.drawImage(base, 0, 0);
      for (const mark of finalMarks) paintMark(ctx, mark);

      // PNG survives only when nothing was drawn: annotations are what make a
      // screenshot's flat colours worth keeping lossless, and a photo with
      // strokes on it is still a photo.
      const keepPNG = file.type === 'image/png' && finalMarks.length === 0;
      const type = keepPNG ? 'image/png' : 'image/jpeg';
      const blob = await new Promise<Blob | null>((resolve) => {
        out.toBlob(resolve, type, type === 'image/jpeg' ? (hd ? 0.95 : 0.86) : undefined);
      });
      if (!blob) throw new Error('encode failed');

      onApply(new File([blob], renameFor(file.name, type), { type, lastModified: Date.now() }));
    } catch {
      // Reported separately from loadError, which replaces the canvas — a
      // failure to encode must not take the picture off the screen.
      setSaveError('Gagal menyimpan hasil edit. Coba matikan HD atau pakai gambar yang lebih kecil.');
      setSaving(false);
    }
  }

  /* --- markup ------------------------------------------------------------- */

  const cropping = tool === 'crop';

  return (
    <div className="fixed inset-0 z-[58] flex flex-col bg-black">
      <header className="flex items-center gap-1 px-3 py-2">
        <ToolBtn label="Batal" onClick={onCancel}>
          <X className="size-5" />
        </ToolBtn>
        <span className="mx-1 h-6 w-px bg-white/15" />

        <ToolBtn label="Potong" active={cropping} onClick={() => setTool(cropping ? 'pen' : 'crop')}>
          <Crop className="size-5" />
        </ToolBtn>
        <ToolBtn label="Putar" onClick={rotate}>
          <RotateCw className="size-5" />
        </ToolBtn>
        <ToolBtn label="Perjelas" onClick={enhance}>
          <Sparkles className="size-5" />
        </ToolBtn>

        <span className="mx-1 h-6 w-px bg-white/15" />

        <ToolBtn label="Gambar" active={tool === 'pen'} onClick={() => setTool('pen')}>
          <Pencil className="size-5" />
        </ToolBtn>
        <ToolBtn label="Teks" active={tool === 'text'} onClick={() => setTool('text')}>
          <Type className="size-5" />
        </ToolBtn>
        <ToolBtn label="Kotak" active={tool === 'shape'} onClick={() => setTool('shape')}>
          <Square className="size-5" />
        </ToolBtn>
        <ToolBtn label="Emoji" active={emojiOpen} onClick={() => setEmojiOpen((v) => !v)}>
          <Smile className="size-5" />
        </ToolBtn>

        <span className="mx-1 h-6 w-px bg-white/15" />

        <button
          type="button"
          onClick={() => setHd((v) => !v)}
          aria-pressed={hd}
          title={hd ? 'Kualitas tinggi' : 'Kualitas standar'}
          className={clsx(
            'rounded-md px-2 py-1 text-xs font-bold tracking-wide transition-colors',
            hd ? 'bg-white text-black' : 'text-white/60 hover:bg-white/10',
          )}
        >
          HD
        </button>

        <ToolBtn label="Urungkan" onClick={undo} disabled={historyDepth === 0}>
          <Undo2 className="size-5" />
        </ToolBtn>

        <span className="flex-1" />

        {cropping && cropRect ? (
          <button
            type="button"
            onClick={applyCrop}
            className="rounded-full bg-white/15 px-3 py-1.5 text-sm text-white hover:bg-white/25"
          >
            Terapkan potongan
          </button>
        ) : null}

        <button
          type="button"
          onClick={() => void apply()}
          disabled={!base || saving}
          aria-label="Selesai"
          className="ml-1 grid size-9 place-items-center rounded-full bg-wa-accent text-white disabled:opacity-40"
        >
          {saving ? <Loader2 className="size-5 animate-spin" /> : <Check className="size-5" />}
        </button>
      </header>

      {emojiOpen ? (
        <div className="flex flex-wrap gap-1 border-y border-white/10 px-3 py-2">
          {EMOJIS.map((char) => (
            <button
              key={char}
              type="button"
              onClick={() => placeEmoji(char)}
              className="grid size-9 place-items-center rounded-lg text-[22px] hover:bg-white/10"
            >
              {char}
            </button>
          ))}
        </div>
      ) : null}

      {saveError ? (
        <p role="alert" className="bg-danger/20 px-4 py-2 text-center text-sm text-white">
          {saveError}
        </p>
      ) : null}

      <div ref={wrapRef} className="relative flex min-h-0 flex-1 items-center justify-center p-4">
        {loadError ? (
          <p className="text-center text-sm text-white/70">{loadError}</p>
        ) : !base ? (
          <Loader2 className="size-8 animate-spin text-white/60" />
        ) : (
          <>
            <canvas
              ref={canvasRef}
              onPointerDown={onPointerDown}
              onPointerMove={onPointerMove}
              onPointerUp={onPointerUp}
              onPointerCancel={onPointerUp}
              className={clsx(
                'max-h-full max-w-full touch-none rounded-sm',
                cropping ? 'cursor-crosshair' : tool === 'text' ? 'cursor-text' : 'cursor-crosshair',
              )}
            />

            {/* The text box is a real input positioned over the canvas, so the
                operator gets their own keyboard, IME and selection behaviour
                rather than a hand-rolled approximation. */}
            {pendingText ? (
              <input
                autoFocus
                value={pendingText.value}
                onChange={(event) =>
                  setPendingText((current) =>
                    current ? { ...current, value: event.target.value } : current,
                  )
                }
                onBlur={commitText}
                onKeyDown={(event) => {
                  if (event.key === 'Enter') {
                    event.preventDefault();
                    commitText();
                  }
                  if (event.key === 'Escape') setPendingText(null);
                }}
                placeholder="Ketik teks"
                style={{
                  left: `calc(50% + ${(pendingText.x - base.width / 2) * scale}px)`,
                  top: `calc(50% + ${(pendingText.y - base.height / 2) * scale}px)`,
                  color,
                }}
                className="absolute -translate-y-1/2 rounded bg-black/60 px-2 py-1 text-[18px] font-semibold outline-none focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-white placeholder:text-white/40"
              />
            ) : null}
          </>
        )}
      </div>

      <footer className="flex items-center gap-3 px-4 pb-4">
        <div className="flex flex-1 flex-wrap gap-2">
          {COLORS.map((c) => (
            <button
              key={c}
              type="button"
              onClick={() => setColor(c)}
              aria-label={`Warna ${c}`}
              aria-pressed={color === c}
              style={{ backgroundColor: c }}
              className={clsx(
                'size-6 rounded-full transition-transform',
                color === c ? 'scale-110 ring-2 ring-white ring-offset-2 ring-offset-black' : '',
              )}
            />
          ))}
        </div>

        <label className="flex items-center gap-2">
          <span className="sr-only">Ketebalan</span>
          <input
            type="range"
            min={2}
            max={28}
            value={width}
            onChange={(event) => setWidth(Number(event.target.value))}
            className="w-28 accent-wa-accent"
          />
          <span
            aria-hidden
            style={{ width: Math.max(4, width / 2), height: Math.max(4, width / 2), backgroundColor: color }}
            className="rounded-full"
          />
        </label>
      </footer>
    </div>
  );
}

/* --- painting -------------------------------------------------------------- */

function paintMark(ctx: CanvasRenderingContext2D, mark: Mark): void {
  ctx.save();
  switch (mark.kind) {
    case 'stroke': {
      ctx.strokeStyle = mark.color;
      ctx.lineWidth = mark.width;
      ctx.lineCap = 'round';
      ctx.lineJoin = 'round';
      ctx.beginPath();
      mark.points.forEach((p, i) => (i === 0 ? ctx.moveTo(p.x, p.y) : ctx.lineTo(p.x, p.y)));
      ctx.stroke();
      break;
    }
    case 'shape': {
      ctx.strokeStyle = mark.color;
      ctx.lineWidth = mark.width;
      ctx.strokeRect(mark.x, mark.y, mark.w, mark.h);
      break;
    }
    case 'text': {
      ctx.fillStyle = mark.color;
      ctx.font = `600 ${mark.size}px system-ui, sans-serif`;
      ctx.textBaseline = 'middle';
      // A dark rim keeps light text readable over a light photo without
      // needing a filled box behind it.
      ctx.strokeStyle = 'rgba(0,0,0,0.55)';
      ctx.lineWidth = Math.max(2, mark.size * 0.09);
      ctx.lineJoin = 'round';
      ctx.strokeText(mark.text, mark.x, mark.y);
      ctx.fillText(mark.text, mark.x, mark.y);
      break;
    }
    case 'emoji': {
      ctx.font = `${mark.size}px system-ui, "Apple Color Emoji", "Segoe UI Emoji", sans-serif`;
      ctx.textAlign = 'center';
      ctx.textBaseline = 'middle';
      ctx.fillText(mark.char, mark.x, mark.y);
      break;
    }
  }
  ctx.restore();
}

/** Dims everything outside the crop rectangle so the keep-area is obvious. */
function paintCropOverlay(
  ctx: CanvasRenderingContext2D,
  imageW: number,
  imageH: number,
  rect: { x: number; y: number; w: number; h: number },
): void {
  ctx.save();
  ctx.fillStyle = 'rgba(0,0,0,0.5)';
  ctx.beginPath();
  ctx.rect(0, 0, imageW, imageH);
  ctx.rect(rect.x, rect.y, rect.w, rect.h);
  ctx.fill('evenodd');

  ctx.strokeStyle = '#ffffff';
  ctx.lineWidth = Math.max(1, imageW * 0.002);
  ctx.strokeRect(rect.x, rect.y, rect.w, rect.h);
  ctx.restore();
}

/* --- geometry -------------------------------------------------------------- */

function mapMark(mark: Mark, fn: (x: number, y: number) => { x: number; y: number }): Mark {
  switch (mark.kind) {
    case 'stroke':
      return { ...mark, points: mark.points.map((p) => fn(p.x, p.y)) };
    case 'shape': {
      // Both corners are mapped, because a rotation turns a width into a
      // height; deriving the new size from the mapped corners keeps that right.
      const a = fn(mark.x, mark.y);
      const b = fn(mark.x + mark.w, mark.y + mark.h);
      return { ...mark, x: Math.min(a.x, b.x), y: Math.min(a.y, b.y), w: Math.abs(b.x - a.x), h: Math.abs(b.y - a.y) };
    }
    default: {
      const p = fn(mark.x, mark.y);
      return { ...mark, x: p.x, y: p.y };
    }
  }
}

function normalizeRect(x: number, y: number, w: number, h: number) {
  return {
    x: w < 0 ? x + w : x,
    y: h < 0 ? y + h : y,
    w: Math.abs(w),
    h: Math.abs(h),
  };
}

function clampRect(
  rect: { x: number; y: number; w: number; h: number },
  maxW: number,
  maxH: number,
) {
  const x = Math.max(0, Math.min(rect.x, maxW));
  const y = Math.max(0, Math.min(rect.y, maxH));
  return { x, y, w: Math.min(rect.w, maxW - x), h: Math.min(rect.h, maxH - y) };
}

/* --- helpers --------------------------------------------------------------- */

async function decode(file: File): Promise<ImageBitmap | HTMLImageElement> {
  if (typeof createImageBitmap === 'function') {
    try {
      // from-image honours the EXIF orientation flag, so a phone photo does
      // not open sideways before anything has been rotated.
      return await createImageBitmap(file, { imageOrientation: 'from-image' });
    } catch {
      // Fall through.
    }
  }
  const url = URL.createObjectURL(file);
  try {
    return await new Promise<HTMLImageElement>((resolve, reject) => {
      const img = new Image();
      img.onload = () => resolve(img);
      img.onerror = () => reject(new Error('decode failed'));
      img.src = url;
    });
  } finally {
    URL.revokeObjectURL(url);
  }
}

function renameFor(name: string, type: string): string {
  const ext = type === 'image/png' ? '.png' : '.jpg';
  const dot = name.lastIndexOf('.');
  const stem = dot > 0 ? name.slice(0, dot) : name;
  return stem + ext;
}

function ToolBtn({
  label,
  onClick,
  active,
  disabled,
  children,
}: {
  label: string;
  onClick: () => void;
  active?: boolean;
  disabled?: boolean;
  children: React.ReactNode;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled}
      aria-label={label}
      aria-pressed={active}
      title={label}
      className={clsx(
        'grid size-9 shrink-0 place-items-center rounded-full transition-colors disabled:opacity-30',
        active ? 'bg-white text-black' : 'text-white/80 hover:bg-white/10',
      )}
    >
      {children}
    </button>
  );
}
