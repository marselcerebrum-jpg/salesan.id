'use client';

import clsx from 'clsx';

export { inputClass, textareaClass } from '@/components/ui/control';
import type { LucideIcon } from 'lucide-react';
import type { ReactNode } from 'react';

import { StateBadge } from '@/components/analytics/Primitives';
import type { CampaignStatus, DelayProfile, TargetStatus } from '@/lib/types';

/**
 * Vocabulary shared by Broadcast and WA Story.
 *
 * One place for the words, because the two screens describe the same states and
 * a status that reads "Selesai Sebagian" on one page and "Sebagian" on the other
 * is a status people stop trusting.
 *
 * The colour carries the same five meanings everywhere, and nothing else is
 * allowed to use them on these screens:
 *
 *   info (biru)     menunggu giliran, atau menunggu jamnya
 *   warn (kuning)   sedang berjalan, atau berjalan tapi belum tuntas
 *   good (hijau)    berhasil
 *   danger (merah)  gagal
 *   neutral (abu)   draf, batal, atau sudah lewat masanya
 */

/** Campaign states, one row per state the backend can actually produce. */
type Tone = 'good' | 'warn' | 'danger' | 'neutral' | 'info';

export const CAMPAIGN_STATUS: Record<
  CampaignStatus,
  { label: string; storyLabel?: string; tone: Tone; storyTone?: Tone }
> = {
  draft: { label: 'Draf', tone: 'neutral' },
  scheduled: { label: 'Terjadwal', tone: 'info' },
  running: { label: 'Berjalan', tone: 'warn' },
  completed: { label: 'Selesai', storyLabel: 'Terposting', tone: 'good' },
  // Some recipients got it and some did not. Calling this "Selesai" would hide
  // the failures; calling it "Gagal" would invite somebody to send the whole
  // thing again to people who already have it.
  partial: { label: 'Selesai Sebagian', storyLabel: 'Sebagian', tone: 'warn' },
  // "Gagal kirim" on a broadcast, because nothing was sent; on a Story there is
  // no sending, only posting, so it stays the shorter word.
  failed: { label: 'Gagal Kirim', storyLabel: 'Gagal', tone: 'danger' },
  cancelled: { label: 'Dibatalkan', tone: 'neutral' },
  /*
   * A Story that ran its full 24 hours.
   *
   * In the list it reads as "Terposting", because that is what happened: it
   * went out, it was watched, and then it ended the way every Story ends.
   * Labelling that "Kedaluwarsa" put a grey failure-shaped word on the one
   * outcome that is a complete success, and made a finished Story look like a
   * problem to go and check.
   *
   * Nothing is hidden by it: opening the Story still shows, per number, the
   * status "Kedaluwarsa" and the exact moment it expired. The list says what
   * happened; the detail says when it ended.
   */
  expired: { label: 'Kedaluwarsa', storyLabel: 'Terposting', tone: 'neutral', storyTone: 'good' },
};

export function statusLabel(status: CampaignStatus, isStory: boolean): string {
  const entry = CAMPAIGN_STATUS[status];
  if (!entry) return status;
  return isStory ? (entry.storyLabel ?? entry.label) : entry.label;
}

export function CampaignStatusBadge({
  status,
  isStory,
}: {
  status: CampaignStatus;
  isStory: boolean;
}) {
  const entry = CAMPAIGN_STATUS[status];
  // The colour follows the label. A badge reading "Terposting" in the grey of
  // a cancelled campaign is a badge arguing with itself.
  const tone = (isStory ? entry?.storyTone : undefined) ?? entry?.tone ?? 'neutral';
  return <StateBadge label={statusLabel(status, isStory)} tone={tone} />;
}

/** Per-recipient states, in the Broadcast detail table. */
export const TARGET_STATUS: Record<
  TargetStatus,
  { label: string; tone: 'good' | 'warn' | 'danger' | 'neutral' | 'info' }
> = {
  pending: { label: 'Antre', tone: 'info' },
  processing: { label: 'Sedang Dikirim', tone: 'warn' },
  sent: { label: 'Terkirim', tone: 'good' },
  // Terkirim means WhatsApp accepted it; Sampai means it reached the phone.
  // The old label for this was the English word, the only one on the screen.
  delivered: { label: 'Sampai', tone: 'good' },
  read: { label: 'Dibaca', tone: 'good' },
  failed: { label: 'Gagal', tone: 'danger' },
  cancelled: { label: 'Batal', tone: 'neutral' },
  skipped: { label: 'Dilewati', tone: 'neutral' },
  // Failed once and waiting for its turn to be tried again. Named "Tunda"
  // because that is what the operator sees happen; nothing here is about a
  // daily quota, and this app has no quota feature to be about.
  retry_wait: { label: 'Tunda', tone: 'warn' },
  invalid: { label: 'Tidak Valid', tone: 'danger' },
};

/**
 * Per-number states, in the WA Story detail table.
 *
 * Lives here beside the other two rather than in the Story page, so all three
 * vocabularies are read and changed together.
 */
export const PUBLICATION_STATUS: Record<
  string,
  { label: string; tone: 'good' | 'warn' | 'danger' | 'neutral' | 'info' }
> = {
  pending: { label: 'Terjadwal', tone: 'info' },
  processing: { label: 'Berjalan', tone: 'warn' },
  published: { label: 'Terposting', tone: 'good' },
  failed: { label: 'Gagal', tone: 'danger' },
  cancelled: { label: 'Dibatalkan', tone: 'neutral' },
  expired: { label: 'Kedaluwarsa', tone: 'neutral' },
  // Taken down by whoever posted it, before its 24 hours were up. Distinct from
  // 'expired' on purpose: one ran out, the other was stopped.
  deleted: { label: 'Dihapus', tone: 'neutral' },
};

/**
 * The five delay profiles.
 *
 * The description says what each one does to the queue and nothing else. None of
 * them makes an account safe from WhatsApp's limits, and the interface must not
 * suggest otherwise — the line under the picker says so outright.
 */
export const DELAY_PROFILES: { id: DelayProfile; label: string; range: string }[] = [
  { id: 'super_cepat', label: 'Super Cepat', range: '1–6 detik' },
  { id: 'cepat', label: 'Cepat', range: '5–15 detik' },
  { id: 'normal', label: 'Normal', range: '30–60 detik' },
  { id: 'aman', label: 'Aman', range: '1–2 menit' },
  { id: 'santai', label: 'Santai', range: '3–5 menit' },
];

export const DELAY_NOTICE =
  'Profil jeda mengatur ritme antrean pengiriman. Ini bukan jaminan akun terhindar dari pembatasan WhatsApp.';

/** A duration in words, for an estimate rather than a measurement. */
export function formatEstimate(seconds: number): string {
  if (seconds <= 0) return 'Kurang dari satu menit';
  if (seconds < 60) return `± ${seconds} detik`;
  const minutes = Math.round(seconds / 60);
  if (minutes < 60) return `± ${minutes} menit`;
  const hours = Math.floor(minutes / 60);
  const rest = minutes % 60;
  return rest ? `± ${hours} jam ${rest} menit` : `± ${hours} jam`;
}

/* --- small form pieces ----------------------------------------------------- */

export function Field({
  label,
  hint,
  children,
  required,
}: {
  label: ReactNode;
  hint?: ReactNode;
  children: ReactNode;
  required?: boolean;
}) {
  return (
    <label className="block">
      <span className="mb-1.5 flex items-center gap-1 text-xs font-medium text-ink-soft">
        {label}
        {required ? <span className="text-danger">*</span> : null}
      </span>
      {children}
      {hint ? <span className="mt-1 block text-2xs text-ink-muted">{hint}</span> : null}
    </label>
  );
}

/** The shared control styling, so a form built here matches one built there. */

export function Button({
  children,
  variant = 'secondary',
  ...props
}: React.ButtonHTMLAttributes<HTMLButtonElement> & {
  variant?: 'primary' | 'secondary' | 'danger' | 'ghost';
}) {
  const variants = {
    primary: 'bg-brand-800 text-white hover:bg-brand-900 disabled:bg-brand-800/50',
    secondary:
      'border border-hairline-strong bg-surface-raised text-ink hover:bg-surface-sunken disabled:opacity-50',
    danger: 'border border-danger/30 bg-surface-raised text-danger hover:bg-danger-soft disabled:opacity-50',
    ghost: 'text-ink-soft hover:bg-surface-sunken disabled:opacity-50',
  };
  return (
    <button
      type="button"
      {...props}
      className={clsx(
        'inline-flex h-9 items-center justify-center gap-1.5 rounded-control px-3.5 text-sm font-medium transition-colors disabled:cursor-not-allowed',
        variants[variant],
        props.className,
      )}
    >
      {children}
    </button>
  );
}

/** A choice among a handful of options, drawn as segmented buttons. */
export function Choice<T extends string>({
  value,
  options,
  onChange,
}: {
  value: T;
  options: { id: T; label: string; hint?: string }[];
  onChange: (next: T) => void;
}) {
  return (
    <div className="flex flex-wrap gap-1.5">
      {options.map((o) => (
        <button
          key={o.id}
          type="button"
          onClick={() => onChange(o.id)}
          aria-pressed={value === o.id}
          className={clsx(
            'rounded-lg border px-3 py-1.5 text-xs transition-colors',
            value === o.id
              ? 'border-brand-700 bg-brand-600/10 font-medium text-brand-800'
              : 'border-hairline bg-surface-raised text-ink-soft hover:bg-surface-sunken',
          )}
        >
          {o.label}
          {o.hint ? <span className="ml-1.5 text-ink-muted">{o.hint}</span> : null}
        </button>
      ))}
    </div>
  );
}

/**
 * One numbered step of the composer.
 *
 * Numbered rather than paged: a wizard would hide the sender behind a Back
 * button at the moment somebody is choosing recipients, and those two decisions
 * are read together. The number says what order to fill them in; the page still
 * shows all of it at once.
 */
export function StepCard({
  step,
  title,
  aside,
  children,
  id,
  invalid,
}: {
  step: number;
  title: string;
  aside?: ReactNode;
  children: ReactNode;
  /** Anchor, so a failed submit can scroll the reader to the step that failed. */
  id?: string;
  /** Marks the step the submit stopped on. */
  invalid?: boolean;
}) {
  return (
    <section
      id={id}
      // tabIndex so the step can take focus when scrolled to: somebody working
      // by keyboard has to land on the problem, not just have the page move.
      tabIndex={-1}
      className={clsx(
        // scroll-mt so the card's own top is not left under the page padding
        // when it is scrolled into view.
        'scroll-mt-6 rounded-card border bg-surface-raised shadow-e1 outline-none transition-colors',
        invalid ? 'border-danger/50' : 'border-hairline',
      )}
    >
      {/*
       * 16 + 28 + 12 = 56px of header, exactly. The preview panel beside these
       * cards uses the same three numbers, so the first line of each column sits
       * on the same baseline instead of a dozen pixels apart.
       */}
      <header className="flex min-h-7 items-center gap-3 px-5 pt-4 pb-3">
        <span
          aria-hidden
          className="grid size-7 shrink-0 place-items-center rounded-full bg-surface-sunken text-xs font-semibold text-ink-soft tabular-nums"
        >
          {step}
        </span>
        <h2 className="flex-1 text-base font-semibold text-ink">{title}</h2>
        {aside}
      </header>
      <div className="px-5 pb-5">{children}</div>
    </section>
  );
}

/** The count badge a step shows once it has something in it. */
export function StepTally({ icon: Icon, value }: { icon: LucideIcon; value: number }) {
  if (value <= 0) return null;
  return (
    <span className="inline-flex items-center gap-1.5 rounded-full bg-brand-600/10 px-2.5 py-1 text-2xs font-semibold text-brand-800 tabular-nums">
      <Icon className="size-3.5" />
      {value.toLocaleString('id-ID')}
    </span>
  );
}

/** An on/off switch for a setting that is a sentence, not a field. */
export function Toggle({
  checked,
  onChange,
  label,
  disabled,
}: {
  checked: boolean;
  onChange: (next: boolean) => void;
  label: string;
  disabled?: boolean;
}) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      aria-label={label}
      disabled={disabled}
      onClick={() => onChange(!checked)}
      className={clsx(
        'relative h-6 w-11 shrink-0 rounded-full transition-colors disabled:opacity-50',
        checked ? 'bg-brand-700' : 'bg-ink-muted/35',
      )}
    >
      <span
        aria-hidden
        className={clsx(
          'absolute top-0.5 size-5 rounded-full bg-white shadow-e1 transition-[left]',
          checked ? 'left-[22px]' : 'left-0.5',
        )}
      />
    </button>
  );
}

/** A short, dismissible message. Used for what the operator must read. */
export function Notice({
  tone = 'info',
  children,
}: {
  tone?: 'info' | 'warn' | 'danger';
  children: ReactNode;
}) {
  const tones = {
    info: 'border-hairline-strong bg-surface-sunken text-ink-soft',
    warn: 'border-warn/30 bg-warn-soft text-warn',
    danger: 'border-danger/25 bg-danger-soft text-danger',
  };
  return (
    <p className={clsx('rounded-lg border px-3 py-2 text-xs leading-snug', tones[tone])}>
      {children}
    </p>
  );
}
