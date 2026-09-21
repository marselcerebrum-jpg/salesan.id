'use client';

import { useState, type ReactNode } from 'react';

/**
 * An application's uploaded logo, or whatever the caller would draw without one.
 *
 * On a white plate in both themes. Logos are made for a light ground — several
 * are transparent PNGs with black or navy strokes that would vanish on the dark
 * card — and a plate is how app icons are shown everywhere else too.
 *
 * A link that fails (expired, removed, or a file the browser cannot decode)
 * falls back to the monogram rather than leaving a broken-image box.
 */
export function AppLogo({
  src,
  size,
  radius = 'rounded-xl',
  fallback,
}: {
  src: string | null;
  size: number;
  radius?: string;
  fallback: ReactNode;
}) {
  const [failed, setFailed] = useState<string | null>(null);

  if (!src || failed === src) return <>{fallback}</>;

  return (
    <span
      aria-hidden
      style={{ width: size, height: size, padding: Math.max(2, Math.round(size * 0.1)) }}
      className={`inline-grid shrink-0 place-items-center overflow-hidden bg-white ring-1 ring-black/5 ${radius}`}
    >
      {/* eslint-disable-next-line @next/next/no-img-element */}
      <img
        src={src}
        alt=""
        className="size-full object-contain"
        onError={() => setFailed(src)}
        draggable={false}
      />
    </span>
  );
}
