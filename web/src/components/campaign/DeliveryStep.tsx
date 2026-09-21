'use client';

import clsx from 'clsx';

import { DELAY_NOTICE, DELAY_PROFILES, Notice, Toggle, inputClass } from '@/components/campaign/shared';
import type { DelayProfile, Recurrence } from '@/lib/types';

/**
 * Step 4: how fast it goes out, what happens when a number drops, and when it
 * starts.
 *
 * The five profiles stay, because "Normal" is a shorter thing to agree on than
 * "30 to 60 seconds". The explicit min and max sit underneath because what
 * actually keeps a number out of trouble is its real rhythm, not the name of a
 * preset — and pressing a preset fills them in, so the two never disagree about
 * what is going to happen.
 */

/**
 * When it leaves.
 *
 * There is no "save as draft" here any more. A broadcast is either scheduled or
 * run, and a campaign sitting in the list with no departure time was a thing
 * nobody could tell apart from one that had quietly failed to start.
 */
export const WHEN_OPTIONS = [
  { id: 'now', label: 'Sekarang' },
  { id: '5', label: '5 menit lagi' },
  { id: '15', label: '15 menit lagi' },
  { id: '30', label: '30 menit lagi' },
  { id: '60', label: '1 jam lagi' },
  { id: '120', label: '2 jam lagi' },
  { id: 'custom', label: 'Pilih waktu' },
] as const;

export type When = (typeof WHEN_OPTIONS)[number]['id'];

const FREQUENCIES: { id: Recurrence; label: string; hint: string }[] = [
  { id: 'daily', label: 'Harian', hint: 'setiap hari pada jam yang sama' },
  { id: 'weekly', label: 'Mingguan', hint: 'setiap pekan pada hari yang sama' },
  { id: 'monthly', label: 'Bulanan', hint: 'setiap bulan pada tanggal yang sama' },
];

/** Sunday first, as Indonesian calendars print it. */
const WEEKDAYS = ['Min', 'Sen', 'Sel', 'Rab', 'Kam', 'Jum', 'Sab'];

export interface DeliveryValue {
  profile: DelayProfile;
  minSeconds: number;
  maxSeconds: number;
  autoRetry: boolean;
  recurring: boolean;
  frequency: Recurrence;
  /** WIB wall clock, "HH:MM". Stays 09:00 whatever happens to the server. */
  recurTime: string;
  /** 0 = Sunday. Weekly only. */
  recurWeekday: number;
  /** 1–31. Monthly only. */
  recurDay: number;
  when: When;
  customAt: string;
}

export const defaultDelivery: DeliveryValue = {
  profile: 'normal',
  minSeconds: 30,
  maxSeconds: 60,
  autoRetry: true,
  recurring: false,
  frequency: 'daily',
  recurTime: '09:00',
  recurWeekday: 1,
  recurDay: 1,
  when: 'now',
  customAt: '',
};

/** Seconds for a profile, so pressing a preset fills the two boxes truthfully. */
const PROFILE_SECONDS: Record<DelayProfile, [number, number]> = {
  super_cepat: [1, 5],
  cepat: [5, 15],
  normal: [30, 60],
  aman: [60, 120],
  santai: [180, 300],
};

export function DeliveryStep({
  value,
  onChange,
  isStory,
}: {
  value: DeliveryValue;
  onChange: (next: DeliveryValue) => void;
  isStory: boolean;
}) {
  function set(patch: Partial<DeliveryValue>) {
    onChange({ ...value, ...patch });
  }

  const invalid = value.maxSeconds < value.minSeconds || value.minSeconds < 1;

  return (
    <>
      {!isStory ? (
        <>
          <div className="flex flex-wrap items-center justify-between gap-2">
            <p className="text-xs font-medium text-ink-soft">Jeda Antar Pesan</p>
            <p className="text-2xs text-ink-muted tabular-nums">
              {value.minSeconds} dtk – {value.maxSeconds} dtk
            </p>
          </div>

          <div className="mt-1.5 flex flex-wrap gap-1.5">
            {DELAY_PROFILES.map((p) => {
              const on = value.profile === p.id;
              const [min, max] = PROFILE_SECONDS[p.id];
              return (
                <button
                  key={p.id}
                  type="button"
                  onClick={() => set({ profile: p.id, minSeconds: min, maxSeconds: max })}
                  aria-pressed={on}
                  className={clsx(
                    'rounded-lg border px-3 py-2 text-xs transition-colors',
                    on
                      ? 'border-brand-800 bg-brand-800 font-medium text-white'
                      : 'border-hairline bg-surface-raised text-ink-soft hover:bg-surface-sunken',
                  )}
                >
                  {p.label} · {p.range}
                </button>
              );
            })}
          </div>

          <div className="mt-2.5 grid gap-2 sm:grid-cols-2">
            <label className="block">
              <span className="mb-1 block text-2xs text-ink-muted">Min (detik)</span>
              <input
                type="number"
                min={1}
                max={3600}
                className={inputClass}
                value={value.minSeconds}
                onChange={(e) => set({ minSeconds: Number(e.target.value) })}
              />
            </label>
            <label className="block">
              <span className="mb-1 block text-2xs text-ink-muted">Max (detik)</span>
              <input
                type="number"
                min={1}
                max={3600}
                className={inputClass}
                value={value.maxSeconds}
                onChange={(e) => set({ maxSeconds: Number(e.target.value) })}
              />
            </label>
          </div>

          {invalid ? (
            <div className="mt-2">
              <Notice tone="danger">
                Jeda harus 1–3600 detik dan maksimum tidak boleh di bawah minimum.
              </Notice>
            </div>
          ) : null}

          <div className="mt-2">
            <Notice>{DELAY_NOTICE}</Notice>
          </div>

          <SettingRow
            title="Auto-retry jika device disconnect"
            hint="Otomatis lanjut kirim saat device connect kembali"
            checked={value.autoRetry}
            onChange={(autoRetry) => set({ autoRetry })}
          />
          {!value.autoRetry ? (
            <div className="mt-2">
              {/* Spelled out because the alternative is silent data loss from the
                  operator's point of view: recipients simply never arrive. */}
              <Notice tone="warn">
                Dimatikan: bila nomor terputus di tengah jalan, sisa penerimanya ditandai gagal
                dengan alasan tertulis, bukan menunggu nomor tersambung lagi.
              </Notice>
            </div>
          ) : null}

          <SettingRow
            title="Broadcast Berulang"
            hint="Kirim broadcast ini otomatis sesuai jadwal berulang"
            checked={value.recurring}
            onChange={(recurring) => set({ recurring })}
          />

          {value.recurring ? (
            <div className="mt-2 rounded-lg border border-hairline bg-surface px-3.5 py-3">
              <p className="text-xs font-medium text-ink-soft">Frekuensi</p>
              <div className="mt-1.5 grid gap-2 sm:grid-cols-3">
                {FREQUENCIES.map((f) => {
                  const on = value.frequency === f.id;
                  return (
                    <button
                      key={f.id}
                      type="button"
                      onClick={() => set({ frequency: f.id })}
                      aria-pressed={on}
                      className={clsx(
                        'rounded-lg border px-3 py-2 text-left transition-colors',
                        on
                          ? 'border-brand-700 bg-brand-600/10'
                          : 'border-hairline bg-surface-raised hover:bg-surface-sunken',
                      )}
                    >
                      <span
                        className={clsx(
                          'block text-sm font-medium',
                          on ? 'text-brand-800' : 'text-ink',
                        )}
                      >
                        {f.label}
                      </span>
                      <span className="block text-2xs text-ink-muted">{f.hint}</span>
                    </button>
                  );
                })}
              </div>
              {value.frequency === 'weekly' ? (
                <>
                  <p className="mt-3 text-xs font-medium text-brand-800">Hari kirim</p>
                  <div className="mt-1.5 flex flex-wrap gap-1.5">
                    {WEEKDAYS.map((d, i) => {
                      const on = value.recurWeekday === i;
                      return (
                        <button
                          key={d}
                          type="button"
                          onClick={() => set({ recurWeekday: i })}
                          aria-pressed={on}
                          className={clsx(
                            'min-w-[52px] rounded-lg border px-3 py-1.5 text-xs transition-colors',
                            on
                              ? 'border-brand-800 bg-brand-800 font-medium text-white'
                              : 'border-hairline bg-surface-raised text-ink-soft hover:bg-surface-sunken',
                          )}
                        >
                          {d}
                        </button>
                      );
                    })}
                  </div>
                </>
              ) : null}

              {value.frequency === 'monthly' ? (
                <>
                  <p className="mt-3 text-xs font-medium text-brand-800">Tanggal kirim</p>
                  <select
                    className={clsx(inputClass, 'mt-1.5')}
                    value={value.recurDay}
                    onChange={(e) => set({ recurDay: Number(e.target.value) })}
                    aria-label="Tanggal kirim"
                  >
                    {Array.from({ length: 31 }, (_, i) => i + 1).map((d) => (
                      <option key={d} value={d}>
                        Tanggal {d}
                      </option>
                    ))}
                  </select>
                  {value.recurDay > 28 ? (
                    // Said here rather than discovered in February.
                    <p className="mt-1 text-2xs text-ink-muted">
                      Bulan yang lebih pendek dari tanggal ini dikirim pada hari terakhirnya.
                    </p>
                  ) : null}
                </>
              ) : null}

              <p className="mt-3 text-xs font-medium text-brand-800">Jam Kirim (WIB)</p>
              <input
                type="time"
                className={clsx(inputClass, 'mt-1.5')}
                value={value.recurTime}
                onChange={(e) => set({ recurTime: e.target.value })}
                aria-label="Jam kirim"
              />

              <p className="mt-2.5 rounded-lg bg-surface-sunken/70 px-3 py-2 text-xs text-ink-soft">
                {summarise(value)}
              </p>

              <p className="mt-2 text-2xs text-ink-muted">
                Tiap pengulangan menjadi broadcast tersendiri, supaya laporan pekan ini tidak
                menimpa laporan pekan lalu. Pengulangan berhenti saat Anda mematikannya.
              </p>
            </div>
          ) : null}
        </>
      ) : null}

      <p className={clsx('text-xs font-medium text-ink-soft', !isStory && 'mt-4')}>Waktu Mulai</p>
      <div className="mt-1.5 grid gap-2 sm:grid-cols-4">
        {WHEN_OPTIONS.map((o) => {
          const on = value.when === o.id;
          return (
            <button
              key={o.id}
              type="button"
              onClick={() => set({ when: o.id })}
              aria-pressed={on}
              className={clsx(
                'rounded-lg border px-3 py-2.5 text-sm transition-colors',
                on
                  ? 'border-brand-700 bg-brand-600/10 font-medium text-brand-800'
                  : 'border-hairline bg-surface-raised text-ink-soft hover:bg-surface-sunken',
              )}
            >
              {o.label}
            </button>
          );
        })}
      </div>

      {value.when === 'custom' ? (
        <input
          type="datetime-local"
          className={clsx(inputClass, 'mt-2 max-w-[260px]')}
          value={value.customAt}
          onChange={(e) => set({ customAt: e.target.value })}
          aria-label="Waktu mulai"
        />
      ) : null}

      <p className="mt-1.5 text-2xs text-ink-muted">
        Waktu WIB. Jadwal berjalan di server, jadi tab browser tidak perlu tetap terbuka.
      </p>
    </>
  );
}

function SettingRow({
  title,
  hint,
  checked,
  onChange,
}: {
  title: string;
  hint: string;
  checked: boolean;
  onChange: (next: boolean) => void;
}) {
  return (
    <div className="mt-2.5 flex items-start justify-between gap-3 rounded-lg border border-hairline bg-surface-sunken/60 px-3.5 py-3">
      <div className="min-w-0">
        <p className="text-sm font-medium text-ink">{title}</p>
        <p className="mt-0.5 text-2xs text-ink-muted">{hint}</p>
      </div>
      <Toggle checked={checked} onChange={onChange} label={title} />
    </div>
  );
}

/**
 * The schedule as a sentence.
 *
 * Read back rather than left as three separate controls: "Mingguan", "Sen" and
 * "09:00" are each true on their own and still easy to misread together, and
 * this is the one thing on the page that will happen without anybody watching.
 */
function summarise(v: DeliveryValue): React.ReactNode {
  const time = (
    <span className="font-semibold text-ink">{v.recurTime} WIB</span>
  );
  if (v.frequency === 'daily') {
    return <>Broadcast dikirim <span className="font-semibold text-ink">setiap hari</span> jam {time}</>;
  }
  if (v.frequency === 'weekly') {
    return (
      <>
        Broadcast dikirim setiap{' '}
        <span className="font-semibold text-ink">{WEEKDAYS[v.recurWeekday]}</span> jam {time}
      </>
    );
  }
  return (
    <>
      Broadcast dikirim{' '}
      <span className="font-semibold text-ink">tanggal {v.recurDay} tiap bulan</span> jam {time}
    </>
  );
}

/** Turns the chosen departure into an absolute instant, or null for a draft. */
export function scheduledAtOf(v: DeliveryValue): string | null {
  switch (v.when) {
    case 'now':
      // Null plus run_now: the server stamps the instant, so a slow browser
      // cannot schedule a campaign a minute into the past.
      return null;
    case 'custom':
      return v.customAt ? new Date(v.customAt).toISOString() : null;
    default:
      return new Date(Date.now() + Number(v.when) * 60_000).toISOString();
  }
}
