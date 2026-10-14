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
  onReadUnverified,
  busyAccountId,
}: {
  report: LabelHealthReport | undefined;
  /** Opens the confirmation for rebuilding this number's collection. */
  onReset: (account: LabelHealth) => void;
  /**
   * Reads a wedged number's labels without verifying WhatsApp's MACs.
   *
   * Offered only on `tersangkut`, and only as a second-best: it hands back
   * labels nothing vouched for, on a number where the alternative has been no
   * labels at all for a day.
   */
  onReadUnverified: (account: LabelHealth) => void;
  /** The number a bypass read is running for, so its button can show it. */
  busyAccountId?: string | null;
}) {
  if (!report || report.needs_eyes === 0) return null;

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
        {report.needs_eyes} nomor tidak bisa membaca label dari WhatsApp
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

      {/* The third situation, and the only one where the honest instruction is
          "do nothing". Said as its own paragraph rather than folded into the
          one above, because the paragraph above ends in a button and this one
          must not: CEREBRUM UTAMA was paired again from a clean QR on 13
          October, with a new device and new keys, and WhatsApp refused the
          identical patch v1176 within twenty seconds. Pressing anything here
          costs a QR scan and changes nothing. */}
      {report.wedged > 0 ? (
        <p className="mt-1 text-xs text-ink-soft">
          <strong className="text-ink">Tersangkut di data WhatsApp</strong> — datanya
          sendiri tidak konsisten, jadi tidak ada yang bisa dikerjakan dari sisi kita.
          Jangan pasang ulang nomornya dan jangan bangun ulang labelnya: keduanya sudah
          dicoba dan gagal di titik yang sama. Sistem mencoba lagi sendiri dengan jeda
          yang melebar, dan langsung mencoba begitu WhatsApp menyusun ulang datanya.
          Kalau tidak bisa menunggu, &ldquo;Baca tanpa verifikasi&rdquo; mengambil labelnya
          apa adanya — bisa dipakai, tetapi tidak ada yang menjamin isinya lengkap, dan
          label yang diubah dari salesan belum tentu sampai ke HP.
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
            {a.severity === 'tersangkut' ? (
              // The patch version is on screen on purpose. It is what turns
              // "labelnya error lagi" into a question with an answer: the same
              // number means the same wedge, a different one means WhatsApp
              // rebuilt its data and this is already being retried.
              <span className="font-medium text-danger">
                tersangkut di patch v{a.wedged_patch} · {formatStale(a.stale_hours)} ·{' '}
                {a.wedged_attempts}x dicoba · {a.label_count} label
                {/* Said on the row, not only in the badge: this is the list
                    somebody scans to decide what still needs doing, and a
                    number already read the lenient way needs nothing. */}
                {a.labels_unverified_at ? (
                  <span className="font-normal text-warn">
                    {' '}
                    · dibaca tanpa verifikasi ({a.labels_unverified_count} perubahan)
                  </span>
                ) : null}
              </span>
            ) : a.severity === 'buta' ? (
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

            {/* Not offered on `buta`, where the ordinary repairs have not been
                tried yet, and not on `tertinggal`, which fixes itself. Only
                here, where every proper route has been exhausted and the choice
                is between unproven labels and none. */}
            {a.severity === 'tersangkut' ? (
              <Button
                size="sm"
                loading={busyAccountId === a.account_id}
                onClick={() => onReadUnverified(a)}
              >
                {a.labels_unverified_at ? 'Baca ulang tanpa verifikasi' : 'Baca tanpa verifikasi'}
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
