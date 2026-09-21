import clsx from 'clsx';

/**
 * The salesan.id brand, from the supplied artwork in /public/brand.
 *
 * Two files: the mark alone (the S, square, for small places — the sidebar,
 * the favicon, a phone-width header) and the full lock-up (mark, wordmark and
 * tagline together, for the login card). Both are on transparency, cut from
 * artwork that was delivered on a pale mint; that mint is kept here for the
 * surfaces designed to carry the logo.
 */
export const BRAND_MINT = '#F3FEF6';

export function SalesanMark({ className, size }: { className?: string; size?: number }) {
  return (
    // eslint-disable-next-line @next/next/no-img-element
    <img
      src="/brand/salesan-mark.png"
      alt=""
      width={size}
      height={size}
      draggable={false}
      className={clsx('shrink-0 select-none', className)}
    />
  );
}

/** Mark, "salesan.id" and the tagline, as one picture. */
export function SalesanLockup({ className }: { className?: string }) {
  return (
    // eslint-disable-next-line @next/next/no-img-element
    <img
      src="/brand/salesan-logo.png"
      alt="salesan.id — Kelola Sales, Wujudkan Lebih Banyak Peluang"
      draggable={false}
      className={clsx('select-none', className)}
    />
  );
}

/** "salesan" in ink, ".id" in the logo green — for places too small for the picture. */
export function SalesanWordmark({ className }: { className?: string }) {
  return (
    <span className={clsx('font-bold tracking-[-0.04em] text-ink', className)}>
      salesan<span className="text-[#22b16c]">.id</span>
    </span>
  );
}
