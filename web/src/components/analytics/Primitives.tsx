'use client';

import clsx from 'clsx';
import { AlertTriangle, HelpCircle, Inbox, RotateCcw } from 'lucide-react';
import { useId, useState, type ReactNode } from 'react';

/**
 * Shared pieces for the Dashboard and Performa pages.
 *
 * Same visual language as the rest of the app — cream ground, white cards,
 * hairline borders — so the reporting screens read as part of the product
 * rather than as a bolted-on admin tool.
 */

/** Page frame: consistent padding, and a cap so text does not span a wide monitor. */
export function PageShell({ children }: { children: ReactNode }) {
  return (
    <div className="px-5 py-6 lg:px-8 lg:py-8">
      <div className="mx-auto w-full max-w-[1280px]">{children}</div>
    </div>
  );
}

/**
 * The header row of a panel: what this group of figures is, and what governs it.
 *
 * One definition rather than one per page, because its whole job is to make two
 * panels that answer different questions look like the same kind of object. A
 * heading floating above a bordered card reads as a title over an unrelated
 * control; the same heading inside the card's own top edge reads as one
 * labelled section, which is what it is.
 */
export function PanelHead({
  title,
  hint,
  right,
}: {
  title: string;
  hint?: string;
  /** Anything that states the panel's current state, such as the period read. */
  right?: ReactNode;
}) {
  return (
    <div className="flex flex-wrap items-center justify-between gap-x-3 gap-y-1.5 border-b border-hairline px-4 py-2.5">
      <div className="min-w-0">
        <h2 className="text-sm leading-tight font-semibold text-ink">{title}</h2>
        {hint ? <p className="mt-0.5 text-xs leading-snug text-ink-muted">{hint}</p> : null}
      </div>
      {right ? <div className="shrink-0">{right}</div> : null}
    </div>
  );
}


/** A titled band, so the page reads as a few sections rather than a wall. */
export function MetricSection({
  title,
  description,
  children,
  action,
  id,
}: {
  title: string;
  /** One short line, or leave it out. */
  description?: ReactNode;
  children: ReactNode;
  action?: ReactNode;
  id?: string;
}) {
  return (
    <section id={id} className="mt-8 scroll-mt-6">
      <div className="mb-3 flex items-end justify-between gap-3">
        <div className="min-w-0">
          <h2 className="text-lg font-semibold text-ink">{title}</h2>
          {description ? (
            <p className="mt-0.5 text-sm text-ink-muted">{description}</p>
          ) : null}
        </div>
        {action}
      </div>
      {children}
    </section>
  );
}

/**
 * A "?" that explains a term on hover or focus.
 *
 * This is where the definitions live. A dashboard carrying its own
 * documentation has to be read before it can be scanned, which defeats it.
 */
export function InfoTip({ text }: { text: string }) {
  const id = useId();
  const [open, setOpen] = useState(false);

  return (
    // `z-10` and `pointer-events-auto` are what let this survive inside a row
    // that is itself clickable: see ActionOverlay below, which covers the row
    // from underneath and needs this control to stay on top of it.
    <span className="pointer-events-auto relative z-10 inline-flex">
      <button
        type="button"
        aria-label="Penjelasan"
        aria-describedby={open ? id : undefined}
        onMouseEnter={() => setOpen(true)}
        onMouseLeave={() => setOpen(false)}
        onFocus={() => setOpen(true)}
        onBlur={() => setOpen(false)}
        onClick={(e) => {
          e.stopPropagation();
          setOpen((v) => !v);
        }}
        className="text-ink-muted/70 transition-colors hover:text-ink-soft"
      >
        <HelpCircle className="size-3.5" />
      </button>
      {open ? (
        <span
          id={id}
          role="tooltip"
          className="absolute bottom-full left-1/2 z-30 mb-1.5 w-[230px] -translate-x-1/2 rounded-lg border border-hairline bg-surface-raised px-2.5 py-2 text-xs leading-snug font-normal text-ink-soft shadow-e3"
        >
          {text}
        </span>
      ) : null}
    </span>
  );
}

/**
 * Makes a whole block clickable without wrapping it in a button.
 *
 * A `<button>` around content that itself contains a button is invalid HTML,
 * and the browser recovers from it by restructuring the DOM, which is why it
 * surfaces as a hydration error rather than a quiet layout bug. Every metric
 * row carrying both an `info` tooltip and an `onClick` was doing exactly that:
 * the tooltip trigger is a button, and it sat inside the row's own button.
 *
 * Nesting also breaks the inner control on its own terms. A click on the
 * tooltip is a click on the row, so opening a definition would drill into the
 * figure at the same time.
 *
 * So the action is laid underneath the content instead of wrapped around it.
 * The overlay is a real button with a real name, the content above it is
 * transparent to the pointer, and anything that wants its own clicks turns
 * them back on (`InfoTip` does).
 *
 * The caller's container needs `relative`, and should carry the hover styling:
 * the pointer is over the container, not over this.
 */
export function ActionOverlay({ label, onClick }: { label: string; onClick: () => void }) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-label={label}
      className="absolute inset-0 rounded-lg focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-brand-600"
    />
  );
}

/** Rows-shaped loading placeholder, for tables and lists. */
export function RowSkeleton({ count = 4 }: { count?: number }) {
  return (
    <div className="space-y-2">
      {Array.from({ length: count }).map((_, i) => (
        <div key={i} className="h-14 animate-pulse rounded-card bg-surface-sunken/70" />
      ))}
    </div>
  );
}

/** Shown when a query succeeded and there is genuinely nothing to report. */
export function EmptyState({ title, hint }: { title: string; hint?: string }) {
  return (
    <div className="rounded-card border border-dashed border-hairline-strong bg-surface-raised px-4 py-8 text-center">
      <Inbox className="mx-auto size-5 text-ink-muted" />
      <p className="mt-2 text-sm font-medium text-ink">{title}</p>
      {hint ? <p className="mt-1 text-xs text-ink-muted">{hint}</p> : null}
    </div>
  );
}

/**
 * Shown when a query failed.
 *
 * Deliberately distinct from the empty state: "nothing happened today" and "we
 * could not find out what happened today" are different, and rendering the
 * second as the first is worse than showing nothing.
 */
export function ErrorState({ message, onRetry }: { message: string; onRetry?: () => void }) {
  return (
    <div className="rounded-card border border-danger/25 bg-danger-soft px-4 py-5 text-center">
      <AlertTriangle className="mx-auto size-5 text-danger" />
      <p className="mt-2 text-sm font-medium text-danger">Data gagal dimuat</p>
      <p className="mt-1 text-xs text-ink-soft">{message}</p>
      {onRetry ? (
        <button
          type="button"
          onClick={onRetry}
          className="mt-3 inline-flex items-center gap-1.5 rounded-lg border border-danger/30 bg-surface-raised px-3 py-1.5 text-sm font-medium text-danger transition-colors hover:bg-danger-soft"
        >
          <RotateCcw className="size-3.5" />
          Coba lagi
        </button>
      ) : null}
    </div>
  );
}

/** Status chip. */
export function StateBadge({
  label,
  tone,
}: {
  label: string;
  tone: 'good' | 'warn' | 'danger' | 'neutral' | 'info';
}) {
  const tones = {
    good: 'border-brand-600/25 bg-brand-600/10 text-brand-700',
    warn: 'border-warn/30 bg-warn-soft text-warn',
    danger: 'border-danger/20 bg-danger-soft text-danger',
    neutral: 'border-hairline bg-surface-sunken text-ink-soft',
    // Blue, not another grey. These two carried nearly the same styling before,
    // which made "menunggu" and "batal" look alike on the campaign screens where
    // the colour is meant to be the quick read.
    info: 'border-info/30 bg-info-soft text-info',
  };
  return (
    <span
      className={clsx(
        'inline-flex items-center rounded-full border px-2 py-0.5 text-2xs font-medium whitespace-nowrap',
        tones[tone],
      )}
    >
      {label}
    </span>
  );
}

/* --- formatting ----------------------------------------------------------- */

/** Scheduled time as hours, which is how a rota is discussed. */
export function formatHours(seconds: number): string {
  if (!seconds) return 'Belum diatur';
  const hours = seconds / 3600;
  return `${hours.toFixed(hours < 10 ? 1 : 0)} jam`;
}

const MONTHS = [
  'Januari', 'Februari', 'Maret', 'April', 'Mei', 'Juni',
  'Juli', 'Agustus', 'September', 'Oktober', 'November', 'Desember',
];

/** "9 September 2026" from a YYYY-MM-DD string. */
export function formatDateID(iso: string): string {
  const [y, m, d] = iso.split('-').map(Number);
  if (!y || !m || !d) return iso;
  return `${d} ${MONTHS[m - 1]} ${y}`;
}

/** The same with the weekday, for a page heading. */
export function formatLongDateID(iso: string): string {
  const [y, m, d] = iso.split('-').map(Number);
  if (!y || !m || !d) return iso;
  const day = new Date(Date.UTC(y, m - 1, d)).toLocaleDateString('id-ID', {
    weekday: 'long',
    timeZone: 'UTC',
  });
  return `${day}, ${d} ${MONTHS[m - 1]} ${y}`;
}

/** "1–30 September 2026", collapsing the parts the two dates share. */
export function formatRangeID(from: string, to: string): string {
  if (!from || !to) return from || to;
  if (from === to) return formatDateID(from);

  const [fy, fm, fd] = from.split('-').map(Number);
  const [ty, tm, td] = to.split('-').map(Number);
  if (!fy || !ty) return `${formatDateID(from)} – ${formatDateID(to)}`;

  if (fy === ty && fm === tm) return `${fd}–${td} ${MONTHS[fm - 1]} ${fy}`;
  if (fy === ty) return `${fd} ${MONTHS[fm - 1]} – ${td} ${MONTHS[tm - 1]} ${fy}`;
  return `${formatDateID(from)} – ${formatDateID(to)}`;
}

/** A clock time in WIB, for a row that lists moments. */
export function formatTime(iso: string | null): string {
  if (!iso) return '-';
  return new Date(iso).toLocaleString('id-ID', {
    day: '2-digit',
    month: 'short',
    hour: '2-digit',
    minute: '2-digit',
    timeZone: 'Asia/Jakarta',
  });
}

/**
 * A response time, in the largest unit that still reads naturally.
 *
 * Null renders as "Belum ada", not as "0 detik": no cycle was completed, which
 * is a different statement from an instant answer and must not look like one.
 */
export function formatDuration(seconds: number | null | undefined): string {
  if (seconds === null || seconds === undefined) return 'Belum ada';
  if (seconds < 60) return `${Math.round(seconds)} dtk`;
  if (seconds < 3600) {
    const m = Math.floor(seconds / 60);
    const s = Math.round(seconds % 60);
    return s ? `${m} mnt ${s} dtk` : `${m} mnt`;
  }
  const h = Math.floor(seconds / 3600);
  const m = Math.round((seconds % 3600) / 60);
  return m ? `${h} jam ${m} mnt` : `${h} jam`;
}

/** Where a message came from, in plain language. */
/** How an operational role is named on screen. */
export const ROLE_LABEL: Record<string, string> = {
  leader: 'Leader',
  pic: 'PIC',
  freelance: 'Freelance',
};

export const SOURCE_LABEL: Record<string, string> = {
  web_admin: 'Web',
  whatsapp_device: 'HP (tanpa pelaku)',
  bot: 'Bot',
  system: 'Sistem',
  broadcast: 'Broadcast',
  story: 'WA Story',
};

/* --- glossary -------------------------------------------------------------- */

/**
 * The definitions, in one place so the same term is explained the same way
 * wherever it appears, and shown only when somebody asks for it.
 */
export const TIP = {
  inbound: 'Jumlah bubble pesan yang dikirim pelanggan. Tiga pesan beruntun dihitung tiga.',
  outbound:
    'Jumlah bubble yang dikirim admin lewat web. Broadcast, bot, sistem, dan pesan dari HP tidak dihitung di sini.',
  contactsInbound: 'Kontak unik yang mengirim minimal satu pesan pada periode ini.',
  contactsServed:
    'Kontak unik yang mendapat minimal satu balasan manual. Pada level tim, satu kontak dihitung sekali.',
  unserved: 'Kontak yang menghubungi tetapi belum mendapat balasan manual.',
  leads:
    'Kontak yang benar-benar baru: belum pernah ada percakapan dan belum berlabel. Kontak dari sinkronisasi riwayat tidak dihitung.',
  groupInbound: 'Bubble dari anggota grup, tidak termasuk pesan dari nomor sistem sendiri.',
  groupReplies: 'Bubble yang dikirim admin di dalam grup. Broadcast ke grup tidak termasuk.',
  workHours: 'Total jam kerja terjadwal. Jadwal yang waktunya bertumpuk dihitung satu kali.',
  outOfSchedule:
    'Aktivitas yang terjadi di luar jam jadwal. Tetap tercatat penuh pada akun pelaksananya; jadwal hanya pembanding, tidak pernah dipakai menebak pelaku.',
  unattributed:
    'Dikirim langsung dari HP. WhatsApp tidak memberi tahu siapa pengirimnya, jadi tidak dibebankan ke admin mana pun.',
  firstResponse:
    'Jarak dari pesan pertama pelanggan sampai balasan manual pertama. Hanya chat pribadi. Membaca pesan tidak menghentikan hitungan; Broadcast, bot, dan pesan sistem juga tidak.',
  slaAchieved: 'Siklus yang dibalas dalam batas waktu yang berlaku saat siklus itu dibuat.',
  slaBreached: 'Siklus yang dibalas melewati batas waktu, atau belum dibalas sampai batas terlewati.',
  slaWaiting:
    'Percakapan yang masih menunggu balasan manual pertama. Melekat pada percakapan, bukan pada orang. Menebak siapa yang seharusnya membalas bukan tugas sistem ini.',
  followUp:
    'Admin menghubungi kembali kontak yang sudah punya percakapan di hari sebelumnya. Beberapa bubble berturut-turut dihitung satu aktivitas. Pesan pertama ke kontak baru, Broadcast, bot, dan pesan grup tidak termasuk.',
  followUpAnswered:
    'Follow-up yang mendapat pesan balasan dari pelanggan sebelum follow-up berikutnya ke kontak yang sama.',
  storyViews:
    'Dihitung dari receipt yang benar-benar diterima sistem. Penonton yang mematikan read receipt tidak akan muncul, jadi angka ini adalah batas bawah.',
  delayProfile:
    'Mengatur jeda antar pengiriman untuk mengendalikan ritme antrean. Bukan jaminan akun aman dari pembatasan WhatsApp.',
  perDay:
    'Kontak Ditangani dibagi jumlah hari kalender yang sudah berjalan pada periode ini, termasuk hari yang nol, tidak termasuk tanggal yang belum terjadi.',
  fastestSlowest:
    'Diambil hanya dari siklus yang sudah dibalas. Percakapan yang masih menunggu belum punya durasi, dan dihitung terpisah sebagai Masih Menunggu Balasan.',
} as const;
