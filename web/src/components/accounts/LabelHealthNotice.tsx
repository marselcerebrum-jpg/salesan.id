'use client';

import { AlertTriangle, Smartphone } from 'lucide-react';

import { Button } from '@/components/ui/Button';
import type { LabelHealth, LabelHealthReport } from '@/lib/types';

/**
 * The warning that was missing.
 *
 * Seven numbers went six days unable to read their labels — two of them holding
 * none at all — while every screen in the product reported them synced. Nothing
 * was broken about the detection; there simply was no detection. The badge in
 * the chat header is per-number and only visible to whoever happens to open that
 * number's thread, which nobody does for a number that has gone quiet.
 *
 * So this sits on the page that lists every number, says how many need a hand,
 * names them, and gives the one instruction that fixes the common cause. It is
 * absent entirely when there is nothing to say, because a warning that is always
 * on screen is furniture.
 */
export function LabelHealthNotice({
  report,
  onReset,
}: {
  report: LabelHealthReport | undefined;
  /** Opens the confirmation for rebuilding this number's collection. */
  onReset: (account: LabelHealth) => void;
}) {
  if (!report || report.needs_phone === 0) return null;

  const hurt = report.accounts
    .filter((a) => a.severity !== 'sehat')
    .sort((a, b) => b.stale_hours - a.stale_hours);

  return (
    <section
      role="status"
      className="rounded-card border border-danger/25 bg-danger-soft/50 p-4"
    >
      <h2 className="flex items-center gap-2 text-sm font-semibold text-danger">
        <AlertTriangle className="size-4" aria-hidden />
        {report.needs_phone} nomor tidak bisa membaca label dari HP
      </h2>

      {/* Two situations, two instructions, and giving the wrong one costs real
          time. A number that is merely behind is waiting on a phone, and opening
          WhatsApp there is the whole fix. A number that has never read its
          collection is not waiting on anything: two of these were unlinked and
          re-paired from a clean QR and failed again at the identical patch, so
          the broken data is in WhatsApp's record and no amount of Sinkron
          reaches it. Telling someone to keep pressing it would be sending them
          back to a door that is already known to be locked. */}
      {report.stale > 0 ? (
        <p className="mt-1 text-xs text-ink-soft">
          <strong className="text-ink">Tertinggal</strong> — buka WhatsApp di HP nomor
          itu, biarkan terbuka, lalu tekan Sinkron.
        </p>
      ) : null}

      {report.blind > 0 ? (
        <p className="mt-1 text-xs text-ink-soft">
          <strong className="text-ink">Belum pernah terbaca</strong> — koleksi labelnya
          rusak di sisi WhatsApp. Memasang ulang nomornya tidak menolong; yang sudah
          di-scan ulang pun gagal lagi di titik yang sama. Yang menyelesaikan hanya
          membangun ulang koleksinya.
        </p>
      ) : null}

      <ul className="mt-3 space-y-1.5">
        {hurt.map((a) => (
          <li
            key={a.account_id}
            className="flex flex-wrap items-baseline gap-x-2 gap-y-0.5 text-xs"
          >
            <Smartphone className="size-3.5 shrink-0 translate-y-0.5 text-ink-muted" aria-hidden />
            <span className="font-medium text-ink">{a.label ?? a.name}</span>
            {a.application_name ? (
              <span className="text-ink-muted">{a.application_name}</span>
            ) : null}

            {/* Never-read and merely-behind are different problems and the
                operator treats them differently, so they are never phrased the
                same. "0 label" is the whole story for a number that has one. */}
            {a.severity === 'buta' ? (
              <span className="font-medium text-danger">
                belum pernah terbaca · {a.label_count} label
              </span>
            ) : (
              <span className="text-warn">
                tertinggal {formatStale(a.stale_hours)} · {a.label_count} label
              </span>
            )}

            {/* Offered only where it is the answer. A number that is merely
                behind recovers on its own and does not need every linked device
                logged out to get there. */}
            {a.severity === 'buta' ? (
              <Button size="sm" variant="danger" onClick={() => onReset(a)}>
                Bangun ulang label
              </Button>
            ) : null}
          </li>
        ))}
      </ul>
    </section>
  );
}

/** Hours are unreadable past a day and meaningless under one. */
function formatStale(hours: number): string {
  if (hours >= 48) return `${Math.round(hours / 24)} hari`;
  if (hours >= 1) return `${Math.round(hours)} jam`;
  return 'kurang dari sejam';
}
