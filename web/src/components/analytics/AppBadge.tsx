'use client';

import clsx from 'clsx';

import { AppLogo } from '@/components/ui/AppLogo';
import { useTheme } from '@/lib/theme';
import { useAppIcon } from '@/lib/useAppIcon';

/* --- Making a colour somebody else picked readable ------------------------ */

/**
 * The application colour is chosen by the workspace, not by us, and it has to
 * stay legible on both themes. A mid-blue is fine on white and disappears on
 * the dark card; a pale yellow does the opposite. Printing it as-is means the
 * two-letter code fails WCAG for whoever picked an unlucky colour, and it
 * fails it silently, on their screen only.
 *
 * So the hue is kept and the lightness is moved until the contrast passes: a
 * step toward black on the light theme, toward white on the dark one. The
 * application stays recognisable because the hue is what identifies it, and
 * the code stays readable because the lightness is what carries contrast.
 */
function srgbToLinear(c: number): number {
  const v = c / 255;
  return v <= 0.04045 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4;
}

function luminance([r, g, b]: [number, number, number]): number {
  return 0.2126 * srgbToLinear(r) + 0.7152 * srgbToLinear(g) + 0.0722 * srgbToLinear(b);
}

function contrast(a: number, b: number): number {
  const [hi, lo] = a > b ? [a, b] : [b, a];
  return (hi + 0.05) / (lo + 0.05);
}

function parseHex(hex: string): [number, number, number] {
  const h = hex.replace('#', '');
  const full = h.length === 3 ? h.split('').map((c) => c + c).join('') : h;
  const n = Number.parseInt(full.slice(0, 6), 16);
  return Number.isNaN(n) ? [15, 61, 46] : [(n >> 16) & 255, (n >> 8) & 255, n & 255];
}

function mix(a: [number, number, number], b: [number, number, number], t: number) {
  return a.map((v, i) => Math.round(v + (b[i] - v) * t)) as [number, number, number];
}

/** The accent, nudged toward the theme's extreme until it clears 4.5:1. */
function readable(hex: string, dark: boolean): string {
  const surface: [number, number, number] = dark ? [22, 30, 25] : [255, 255, 255];
  const toward: [number, number, number] = dark ? [255, 255, 255] : [0, 0, 0];
  const surfaceL = luminance(surface);
  const base = parseHex(hex);

  for (let step = 0; step <= 20; step += 1) {
    const candidate = mix(base, toward, step / 20);
    if (contrast(luminance(candidate), surfaceL) >= 4.5) {
      return `rgb(${candidate.join(' ')})`;
    }
  }
  return dark ? 'rgb(255 255 255)' : 'rgb(0 0 0)';
}

/** Hook form, so a row can ask for both halves of the treatment at once. */
function useAccent(color: string | null | undefined) {
  const { resolved } = useTheme();
  const dark = resolved === 'dark';
  const hex = color ?? '#0f3d2e';
  return {
    hex,
    ink: readable(hex, dark),
    // The ground stays a tint of the raw colour: it is behind text, not text,
    // so it only has to be visible, and a solid block at this size would fight
    // every neighbouring element for attention.
    tint: `${hex}${dark ? '2E' : '1A'}`,
    edge: `${hex}${dark ? '59' : '40'}`,
  };
}

/**
 * How an application identifies itself.
 *
 * The rule this follows is worth stating, because it is easy to get wrong in
 * the other direction: an application must be recognisable at a glance, and it
 * must not redecorate the page. Thirteen applications each recolouring the
 * whole screen would give thirteen products with one codebase, and the person
 * switching between them would lose every habit they had built.
 *
 * So the accent appears only on small things — a mark, a badge, a rule down the
 * side of a card. Layout, spacing, typography and interaction stay identical
 * everywhere.
 */

/** The square mark: the application's icon, or its code on a tinted ground. */
export function AppMark({
  code,
  color,
  iconUrl,
  size = 26,
}: {
  code: string | null | undefined;
  color: string | null | undefined;
  iconUrl?: string | null;
  size?: number;
}) {
  const accent = useAccent(color);
  // An explicit icon wins; otherwise the uploaded logo for this code, from the
  // shared application list.
  const looked = useAppIcon(code);
  const icon = iconUrl ?? looked;

  const monogram = (
    <span
      aria-hidden
      style={{
        width: size,
        height: size,
        backgroundColor: accent.tint,
        color: accent.ink,
        fontSize: Math.round(size * 0.4),
      }}
      // 10px, the control corner from DESIGN.md. The mark is the identity
      // motif, so it wears the same corner wherever it appears.
      className="inline-grid shrink-0 place-items-center rounded-control font-bold tracking-tight"
    >
      {(code ?? '-').slice(0, 2).toUpperCase()}
    </span>
  );
  return <AppLogo src={icon} size={size} radius="rounded-control" fallback={monogram} />;
}

/** Mark plus name, for a row that has space for both. */
export function AppBadge({
  code,
  name,
  color,
  iconUrl,
  device,
  size = 22,
  className,
}: {
  code: string | null | undefined;
  name?: string | null;
  color?: string | null;
  iconUrl?: string | null;
  /** The WhatsApp number in use, shown after the application name. */
  device?: string | null;
  size?: number;
  className?: string;
}) {
  if (!code && !name) return null;
  return (
    <span className={clsx('inline-flex min-w-0 items-center gap-1.5', className)}>
      <AppMark code={code} color={color} iconUrl={iconUrl} size={size} />
      <span className="min-w-0 truncate text-xs text-ink-soft">
        {name ?? code}
        {device ? <span className="text-ink-muted"> · {device}</span> : null}
      </span>
    </span>
  );
}

/**
 * A card's left edge, tinted to the application it belongs to.
 *
 * Enough to tell two applications apart while scanning a list, and small enough
 * that a screen holding several of them still reads as one page.
 */
export function AppAccent({ color }: { color?: string | null }) {
  return (
    <span
      aria-hidden
      className="absolute inset-y-0 left-0 w-[3px] rounded-l-card"
      style={{ backgroundColor: color ?? '#0f3d2e' }}
    />
  );
}

/** A compact pill, for a table cell or a dense header. */
export function AppChip({
  code,
  color,
  name,
}: {
  code: string | null | undefined;
  color?: string | null;
  name?: string | null;
}) {
  const accent = useAccent(color);
  if (!code) return <span className="text-xs text-ink-muted">-</span>;
  return (
    <span
      title={name ?? code}
      className="inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-2xs font-medium"
      style={{ borderColor: accent.edge, backgroundColor: accent.tint, color: accent.ink }}
    >
      {/* The dot keeps the raw colour: it is a shape, not text, so contrast
          only has to make it visible, and the unadjusted hue is the truest
          statement of which application this is. */}
      <span aria-hidden className="size-1.5 rounded-full" style={{ backgroundColor: accent.hex }} />
      {code}
    </span>
  );
}
