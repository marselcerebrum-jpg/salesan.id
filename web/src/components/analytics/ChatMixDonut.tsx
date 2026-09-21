'use client';

import { PieChart } from 'lucide-react';

import { EmptyState } from '@/components/analytics/Primitives';
import type { PerformanceDay } from '@/lib/types';

/**
 * How much of the period was personal chat and how much was groups.
 *
 * Counted in conversations, not messages: one contact who wrote forty times is
 * one conversation, and one group that carried four hundred messages is one
 * group. Measured in messages the chart would say almost nothing except which
 * side happens to type more, which is not the question anybody opens it with.
 *
 * Both halves come from figures that already exist under those names in Rincian
 * Per Hari — Kontak Masuk and Grup Aktif — so this is a second drawing of
 * numbers the page already shows, never a second definition of them.
 */
export function ChatMixDonut({ summary }: { summary: PerformanceDay | undefined }) {
  const personal = summary?.contacts_inbound ?? 0;
  const group = summary?.groups_active ?? 0;
  const total = personal + group;

  const R = 54;
  const C = 2 * Math.PI * R;
  const share = total > 0 ? personal / total : 0;

  return (
    <section className="flex flex-col rounded-card border border-hairline bg-surface-raised px-4 py-4 shadow-e1">
      <div className="flex items-center gap-2.5">
        <span className="flex size-8 shrink-0 items-center justify-center rounded-control bg-warn-soft text-amber-badge">
          <PieChart className="size-4" aria-hidden />
        </span>
        <div className="min-w-0">
          <h2
            className="truncate text-sm font-semibold text-ink"
            title="Pribadi dihitung dari kontak unik yang menulis; grup dari grup yang aktif. Satu kontak atau satu grup dihitung sekali, berapa pun pesannya."
          >
            Distribusi Jenis Percakapan
          </h2>
          <p className="text-2xs leading-snug text-ink-muted">
            Perbandingan chat pribadi dan grup.
          </p>
        </div>
      </div>

      {total === 0 ? (
        <div className="mt-3">
          <EmptyState title="Belum ada percakapan pada periode ini." />
        </div>
      ) : (
        <div className="mt-4 flex flex-1 flex-wrap items-center justify-center gap-5">
          <div className="relative shrink-0">
            <svg width="132" height="132" viewBox="0 0 132 132" role="img" aria-label={`${personal} percakapan pribadi, ${group} percakapan grup`}>
              {/* The group share is the full ring; the personal share is drawn
                  over it. Two arcs, no gap, so the two always sum to the whole
                  and no rounding can leave a sliver of background showing. */}
              <circle
                cx="66"
                cy="66"
                r={R}
                fill="none"
                stroke="color-mix(in srgb, var(--color-brand-600) 55%, var(--color-surface-raised))"
                strokeWidth="20"
              />
              <circle
                cx="66"
                cy="66"
                r={R}
                fill="none"
                stroke="var(--color-brand-800)"
                strokeWidth="20"
                strokeDasharray={`${C * share} ${C}`}
                transform="rotate(-90 66 66)"
              />
            </svg>
            <div className="pointer-events-none absolute inset-0 flex flex-col items-center justify-center">
              <span className="nums text-xl leading-none font-semibold text-ink">
                {total.toLocaleString('id-ID')}
              </span>
              <span className="mt-0.5 text-2xs text-ink-muted">percakapan</span>
            </div>
          </div>

          <ul className="min-w-0 space-y-3">
            <Legend
              color="var(--color-brand-800)"
              label="Chat pribadi"
              value={personal}
              total={total}
            />
            <Legend
              color="color-mix(in srgb, var(--color-brand-600) 55%, var(--color-surface-raised))"
              label="Chat grup"
              value={group}
              total={total}
            />
          </ul>
        </div>
      )}
    </section>
  );
}

function Legend({
  color,
  label,
  value,
  total,
}: {
  color: string;
  label: string;
  value: number;
  total: number;
}) {
  const pct = total > 0 ? (value / total) * 100 : 0;
  return (
    <li className="flex items-center gap-2">
      <span aria-hidden className="size-2.5 shrink-0 rounded-full" style={{ backgroundColor: color }} />
      <div className="min-w-0">
        <p className="text-xs font-medium text-ink">{label}</p>
        <p className="nums text-2xs text-ink-muted">
          {value.toLocaleString('id-ID')} ({pct.toLocaleString('id-ID', { maximumFractionDigits: 1 })}%)
        </p>
      </div>
    </li>
  );
}
