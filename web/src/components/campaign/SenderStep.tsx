'use client';

import clsx from 'clsx';
import { Check, ChevronDown, ChevronUp, Search, Send, Smartphone, X } from 'lucide-react';
import { useEffect, useMemo, useRef, useState } from 'react';

import { Notice } from '@/components/campaign/shared';
import type { Account } from '@/lib/types';

/**
 * Step 1: who this goes out from.
 *
 * One number or twenty, chosen from a single list of every device the reader is
 * allowed to send from, grouped by the application that owns it. Grouping is
 * what makes twenty numbers findable; "semua nomor" is what makes picking all of
 * one brand's numbers a single click rather than eight.
 *
 * The application boundary is enforced, not suggested. A campaign that spans two
 * applications cannot be reported on, retried or authorised — a PIC holds one
 * application, so half of it would sit outside their remit — and the database
 * refuses it outright (trigger `campaign_device_matches_application`, migration
 * 0031). Rather than let somebody build such a campaign and meet the refusal at
 * the very end, the other applications go quiet as soon as the first number is
 * picked, and say why, with one click to switch.
 */

/** The three ways a message can leave. Two of them are not built yet. */
const MODES = [
  {
    id: 'qr' as const,
    title: 'WhatsApp QR',
    hint: 'Pesan bebas via device QR',
    ready: true,
  },
  {
    id: 'waba_official' as const,
    title: 'WABA Official',
    hint: 'Template approved Meta',
    ready: false,
  },
  {
    id: 'waba_flow' as const,
    title: 'WABA Flow',
    hint: 'Form interaktif Meta',
    ready: false,
  },
];

export type SenderMode = (typeof MODES)[number]['id'];

export interface AppGroup {
  id: string;
  code: string;
  name: string;
  color: string | null;
  devices: Account[];
}

export function SenderStep({
  accounts,
  mode,
  onMode,
  selected,
  onSelected,
  applicationID,
  onApplication,
  isStory = false,
}: {
  accounts: Account[];
  /** Omitted for a Story, which only ever leaves through a QR device. */
  mode?: SenderMode;
  onMode?: (next: SenderMode) => void;
  selected: string[];
  onSelected: (next: string[]) => void;
  applicationID: string;
  onApplication: (next: string) => void;
  /**
   * A Story picks its numbers by exactly the same rules as a broadcast — one
   * application, many numbers — so it uses this component rather than one of its
   * own. Only the words change, and the sender-mode cards are dropped: WABA has
   * no status feature, so there is nothing for those three cards to choose
   * between.
   */
  isStory?: boolean;
}) {
  const [open, setOpen] = useState(false);
  const [search, setSearch] = useState('');
  const wrap = useRef<HTMLDivElement>(null);

  // Grouped by application, each group ordered by name, so the same device is
  // always in the same place however many are connected.
  const groups = useMemo<AppGroup[]>(() => {
    const byID = new Map<string, AppGroup>();
    for (const a of accounts) {
      if (!a.application_id) continue;
      let g = byID.get(a.application_id);
      if (!g) {
        g = {
          id: a.application_id,
          code: a.application_code ?? '-',
          name: a.application_name ?? a.application_code ?? 'Aplikasi',
          color: a.application_color,
          devices: [],
        };
        byID.set(a.application_id, g);
      }
      g.devices.push(a);
    }
    for (const g of byID.values()) {
      g.devices.sort((x, y) => x.name.localeCompare(y.name));
    }
    return [...byID.values()].sort((x, y) => x.code.localeCompare(y.code));
  }, [accounts]);

  useEffect(() => {
    if (!open) return;
    function onDown(event: MouseEvent) {
      if (wrap.current && !wrap.current.contains(event.target as Node)) setOpen(false);
    }
    function onKey(event: KeyboardEvent) {
      if (event.key === 'Escape') setOpen(false);
    }
    document.addEventListener('mousedown', onDown);
    document.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('mousedown', onDown);
      document.removeEventListener('keydown', onKey);
    };
  }, [open]);

  const chosen = useMemo(
    () => accounts.filter((a) => selected.includes(a.id)),
    [accounts, selected],
  );

  const needle = search.trim().toLowerCase();
  const digits = needle.replace(/\D/g, '');

  function matches(a: Account) {
    if (!needle) return true;
    if (a.name.toLowerCase().includes(needle)) return true;
    if (a.device_id.toLowerCase().includes(needle)) return true;
    return digits.length > 0 && (a.phone_number ?? '').replace(/\D/g, '').includes(digits);
  }

  const visible = groups
    .map((g) => ({ ...g, devices: g.devices.filter(matches) }))
    .filter((g) => g.devices.length > 0 || (needle && g.name.toLowerCase().includes(needle)));

  /** Picking a device in a new application replaces the whole selection. */
  function switchTo(appID: string, ids: string[]) {
    onApplication(appID);
    onSelected(ids);
  }

  function toggleDevice(a: Account) {
    const appID = a.application_id ?? '';
    if (applicationID && appID !== applicationID) {
      switchTo(appID, [a.id]);
      return;
    }
    const next = selected.includes(a.id)
      ? selected.filter((id) => id !== a.id)
      : [...selected, a.id];
    onApplication(next.length > 0 ? appID : '');
    onSelected(next);
  }

  function toggleGroup(g: AppGroup) {
    const ids = g.devices.map((d) => d.id);
    if (applicationID && g.id !== applicationID) {
      switchTo(g.id, ids);
      return;
    }
    const allOn = ids.every((id) => selected.includes(id));
    const next = allOn ? [] : ids;
    onApplication(next.length > 0 ? g.id : '');
    onSelected(next);
  }

  const activeName = groups.find((g) => g.id === applicationID)?.name ?? null;

  return (
    <>
      {!isStory ? (
        <>
      <p className="flex items-center gap-1.5 text-xs font-medium text-ink-soft">
        <Send className="size-3.5" />
        Mode Pengirim
      </p>
      <div className="mt-2 grid gap-2 sm:grid-cols-3">
        {MODES.map((m) => {
          const on = mode === m.id;
          return (
            <button
              key={m.id}
              type="button"
              disabled={!m.ready}
              onClick={() => m.ready && onMode?.(m.id)}
              aria-pressed={on}
              className={clsx(
                'rounded-lg border px-3.5 py-3 text-left transition-colors',
                !m.ready
                  ? 'cursor-not-allowed border-hairline bg-surface-raised opacity-60'
                  : on
                    ? 'border-brand-700 bg-brand-600/10'
                    : 'border-hairline bg-surface-raised hover:bg-surface-sunken',
              )}
            >
              <span
                className={clsx(
                  'block text-sm font-semibold',
                  on && m.ready ? 'text-ink' : 'text-ink-soft',
                )}
              >
                {m.title}
              </span>
              <span className="mt-0.5 block text-2xs text-ink-muted">{m.hint}</span>
              {/* Said plainly instead of leaving a card that does nothing when
                  pressed. Neither mode is built, and pretending otherwise would
                  only be discovered at the moment somebody needed it. */}
              {!m.ready ? (
                <span className="mt-1.5 inline-block rounded-full bg-surface-sunken px-2 py-0.5 text-3xs text-ink-muted">
                  segera
                </span>
              ) : null}
            </button>
          );
        })}
      </div>
        </>
      ) : null}

      <p className={clsx('text-xs font-medium text-ink-soft', !isStory && 'mt-5')}>
        Perangkat Pengirim
      </p>

      <div ref={wrap} className="relative mt-2">
        <button
          type="button"
          onClick={() => setOpen((v) => !v)}
          aria-expanded={open}
          className="flex w-full items-center gap-2 rounded-lg border border-hairline bg-surface-raised px-3.5 py-2.5 text-left transition-colors hover:bg-surface-sunken"
        >
          <span className="min-w-0 flex-1">
            {chosen.length === 0 ? (
              <span className="text-sm text-ink-muted">Pilih perangkat…</span>
            ) : (
              <span className="text-sm text-ink">
                <span className="font-medium tabular-nums">{chosen.length}</span> nomor dipilih
                {activeName ? <span className="text-ink-muted"> · {activeName}</span> : null}
              </span>
            )}
          </span>
          {open ? (
            <ChevronUp className="size-4 shrink-0 text-ink-muted" />
          ) : (
            <ChevronDown className="size-4 shrink-0 text-ink-muted" />
          )}
        </button>

        {open ? (
          <div className="absolute z-30 mt-1.5 w-full overflow-hidden rounded-lg border border-hairline bg-surface-raised shadow-e3">
            <label className="flex items-center gap-2 border-b border-hairline px-3 py-2.5">
              <Search className="size-4 shrink-0 text-ink-muted" />
              <input
                autoFocus
                value={search}
                onChange={(e) => setSearch(e.target.value)}
                placeholder="Cari nama / nomor / ID…"
                aria-label="Cari perangkat"
                className="w-full bg-transparent text-sm text-ink outline-none placeholder:text-ink-muted"
              />
            </label>

            <div className="max-h-[300px] overflow-y-auto py-1">
              {visible.length === 0 ? (
                <p className="px-3.5 py-6 text-center text-xs text-ink-muted">
                  Tidak ada perangkat yang cocok.
                </p>
              ) : (
                visible.map((g) => {
                  const ids = g.devices.map((d) => d.id);
                  const allOn = ids.length > 0 && ids.every((id) => selected.includes(id));
                  const blocked = Boolean(applicationID) && g.id !== applicationID;
                  return (
                    <div key={g.id} className="pb-1">
                      <p className="px-3.5 pt-2 pb-1 text-3xs font-semibold tracking-wide text-ink-muted uppercase">
                        {g.name}
                      </p>

                      <Row
                        checked={allOn}
                        dimmed={blocked}
                        onClick={() => toggleGroup(g)}
                        label={
                          <span className="text-sm font-medium text-brand-800">
                            {g.name} · semua nomor
                          </span>
                        }
                      />

                      {g.devices.map((a) => (
                        <Row
                          key={a.id}
                          checked={selected.includes(a.id)}
                          dimmed={blocked}
                          onClick={() => toggleDevice(a)}
                          label={
                            <>
                              <span className="block truncate text-sm text-ink">{a.name}</span>
                              <span className="mt-0.5 flex items-center gap-2">
                                <span className="rounded bg-surface-sunken px-1.5 py-px text-3xs text-ink-muted">
                                  {a.device_id}
                                </span>
                                <span className="truncate text-2xs text-ink-muted tabular-nums">
                                  {a.phone_number ?? 'belum terhubung'}
                                </span>
                                {a.status !== 'connected' ? (
                                  <span className="text-2xs text-warn">tidak terhubung</span>
                                ) : null}
                              </span>
                            </>
                          }
                        />
                      ))}
                    </div>
                  );
                })
              )}
            </div>

            {applicationID ? (
              <p className="border-t border-hairline bg-surface-sunken/60 px-3.5 py-2 text-2xs text-ink-muted">
                Satu {isStory ? 'story' : 'broadcast'} memakai nomor dari satu aplikasi. Memilih
                nomor aplikasi lain akan menggantikan pilihan sekarang.
              </p>
            ) : null}
          </div>
        ) : null}
      </div>

      {chosen.length > 0 ? (
        <div className="mt-2.5 flex flex-wrap gap-1.5">
          {chosen.map((a) => (
            <span
              key={a.id}
              className="inline-flex items-center gap-1.5 rounded-full border border-hairline bg-surface-sunken/60 py-1 pr-1 pl-2.5 text-2xs text-ink-soft"
            >
              <Smartphone className="size-3 shrink-0" />
              {a.name}
              <button
                type="button"
                onClick={() => toggleDevice(a)}
                aria-label={`Hapus ${a.name}`}
                className="rounded-full p-0.5 text-ink-muted transition-colors hover:bg-surface-raised hover:text-ink"
              >
                <X className="size-3" />
              </button>
            </span>
          ))}
        </div>
      ) : null}

      {chosen.length > 0 && chosen.every((a) => a.status !== 'connected') ? (
        <div className="mt-2.5">
          <Notice tone="warn">
            Tidak ada nomor terpilih yang sedang terhubung.{' '}
            {isStory
              ? 'Story tetap bisa dijadwalkan, tetapi baru terbit setelah nomornya tersambung.'
              : 'Broadcast tetap bisa disimpan, tetapi pengiriman baru berjalan setelah nomornya tersambung.'}
          </Notice>
        </div>
      ) : null}
    </>
  );
}

function Row({
  checked,
  dimmed,
  label,
  onClick,
}: {
  checked: boolean;
  dimmed: boolean;
  label: React.ReactNode;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={clsx(
        'flex w-full items-center gap-2.5 px-3.5 py-1.5 text-left transition-colors hover:bg-surface-sunken',
        dimmed && 'opacity-45',
      )}
    >
      <span
        aria-hidden
        className={clsx(
          'grid size-4 shrink-0 place-items-center rounded border',
          checked ? 'border-brand-700 bg-brand-700 text-white' : 'border-hairline-strong',
        )}
      >
        {checked ? <Check className="size-3" strokeWidth={3} /> : null}
      </span>
      <span className="min-w-0 flex-1">{label}</span>
    </button>
  );
}
