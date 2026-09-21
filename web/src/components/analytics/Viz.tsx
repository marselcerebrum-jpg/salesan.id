'use client';

import clsx from 'clsx';

/*
 * The one visual piece still drawn by hand.
 *
 * This file used to hold four: a delta arrow, an area trend, a funnel and this
 * ring, all built for the Performa cards. Those cards are gone and their
 * figures are drawn from the shared column model now, so only the ring is
 * left — kept for the same reason it was written rather than imported: it is
 * an arc and two numbers, which is less code than configuring a charting
 * library to draw them, and it costs no bundle on a page people keep open all
 * day.
 */
/* --- SLA ring -------------------------------------------------------------- */

/**
 * A proportion as a ring, with the two counts it came from underneath.
 *
 * The ring is the glanceable part and the counts are the honest part: 95% of
 * twenty is a different morning from 95% of two thousand, and a ring alone
 * cannot tell them apart.
 */
export function ProgressRing({
  achieved,
  total,
  size = 128,
}: {
  achieved: number;
  total: number;
  size?: number;
}) {
  const pct = total > 0 ? Math.round((achieved / total) * 100) : 0;
  const stroke = 10;
  const r = (size - stroke) / 2;
  const c = 2 * Math.PI * r;

  // Thresholds, not a gradient: the question is which of three states this is
  // in, and a continuous colour makes 79% and 80% look the same.
  const tone = pct >= 90 ? 'brand' : pct >= 70 ? 'warn' : 'danger';
  const strokeClass =
    tone === 'brand' ? 'stroke-brand-600' : tone === 'warn' ? 'stroke-warn' : 'stroke-danger';
  const textClass =
    tone === 'brand' ? 'text-brand-700' : tone === 'warn' ? 'text-warn' : 'text-danger';

  return (
    <div className="relative shrink-0" style={{ width: size, height: size }}>
      <svg width={size} height={size} viewBox={`0 0 ${size} ${size}`} aria-hidden>
        <circle
          cx={size / 2}
          cy={size / 2}
          r={r}
          fill="none"
          strokeWidth={stroke}
          className="stroke-surface-sunken"
        />
        <circle
          cx={size / 2}
          cy={size / 2}
          r={r}
          fill="none"
          strokeWidth={stroke}
          strokeLinecap="round"
          strokeDasharray={`${(c * pct) / 100} ${c}`}
          transform={`rotate(-90 ${size / 2} ${size / 2})`}
          className={clsx(strokeClass, 'transition-[stroke-dasharray] duration-700')}
        />
      </svg>
      {/*
       * Flex column, not `grid place-items-center`.
       *
       * A grid with two children makes two implicit rows and centres each one
       * inside its own row, so the percentage sat in the middle of the top half
       * of the ring rather than the middle of the ring. The `-mt-1` that used to
       * be on the caption was compensating for that by eye. A centred column
       * centres the pair, which is what was meant.
       */}
      <div className="absolute inset-0 flex flex-col items-center justify-center leading-none">
        <span
          className={clsx('nums font-semibold tracking-[-0.02em]', textClass)}
          // Scaled to the ring rather than fixed: "100%" at a fixed 28px ran
          // into the stroke once the ring came down to 100px.
          style={{ fontSize: Math.round(size * 0.24) }}
        >
          {total > 0 ? `${pct}%` : '-'}
        </span>
        <span className="nums mt-1 text-2xs text-ink-muted">
          {total > 0 ? `${achieved} / ${total}` : 'belum ada'}
        </span>
      </div>
    </div>
  );
}
