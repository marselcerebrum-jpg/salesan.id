'use client';

import clsx from 'clsx';
import { CalendarDays, Check, ChevronDown } from 'lucide-react';
import { useEffect, useRef, useState } from 'react';

import {
  PRESETS,
  periodLabel,
  presetToQuery,
  queryToPreset,
} from '@/components/analytics/period';
import { inputClassSm } from '@/components/ui/control';
import type { AnalyticsQuery } from '@/lib/api';

/**
 * The period, as one control in the page header.
 *
 * The same presets the toolbar uses, read from the same two functions, so a
 * link opened from anywhere lands on the same reading. Drawn as a menu rather
 * than a row of chips because on the Dashboard the period is one line of the
 * header, not a band of its own: the page already carries five filters below
 * it, and six more buttons beside them would bury the one that matters most.
 *
 * The resolved dates sit under the button. "30 Hari" is the button's promise;
 * which thirty days is the thing somebody needs when they screenshot the page
 * or send the link on.
 */
export function PeriodMenu({
  value,
  onChange,
}: {
  value: AnalyticsQuery;
  onChange: (next: AnalyticsQuery) => void;
}) {
  const [open, setOpen] = useState(false);
  const box = useRef<HTMLDivElement>(null);
  const preset = queryToPreset(value);
  const label = PRESETS.find((p) => p.id === preset)?.label ?? 'Kustom';

  // Closes on a click anywhere else and on Escape, the two things every menu
  // is expected to do and the two that make one feel broken when missing.
  useEffect(() => {
    if (!open) return;
    function onDown(e: MouseEvent) {
      if (!box.current?.contains(e.target as Node)) setOpen(false);
    }
    function onKey(e: KeyboardEvent) {
      if (e.key === 'Escape') setOpen(false);
    }
    document.addEventListener('mousedown', onDown);
    document.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('mousedown', onDown);
      document.removeEventListener('keydown', onKey);
    };
  }, [open]);

  return (
    <div ref={box} className="relative">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
        aria-haspopup="menu"
        className="inline-flex h-10 w-full items-center gap-2 rounded-control border border-hairline bg-surface-raised px-3 text-sm font-medium text-ink shadow-e1 transition-colors hover:bg-surface-sunken sm:w-auto"
      >
        <CalendarDays className="size-4 shrink-0 text-ink-muted" aria-hidden />
        <span className="flex-1 text-left">{label}</span>
        <ChevronDown
          className={clsx('size-4 shrink-0 text-ink-muted transition-transform', open && 'rotate-180')}
          aria-hidden
        />
      </button>

      {open ? (
        <div
          role="menu"
          className="absolute right-0 z-40 mt-1.5 w-64 rounded-card border border-hairline bg-surface-raised p-1.5 shadow-e2"
        >
          {/* Which dates the chosen preset actually resolved to. Inside the
              menu rather than under the button: it is what somebody checks
              while choosing, and a second line under the header would be one
              more thing to read every time they are not. */}
          <p className="nums px-2.5 pt-1 pb-2 text-2xs text-ink-muted">{periodLabel(value)}</p>

          {PRESETS.map((p) => {
            const on = preset === p.id;
            return (
              <button
                key={p.id}
                type="button"
                role="menuitem"
                onClick={() => {
                  onChange(presetToQuery(p.id, value));
                  // A custom range is not chosen yet when its button is
                  // pressed: the two dates are the choice, and they are in the
                  // panel that press reveals.
                  if (p.id !== 'custom') setOpen(false);
                }}
                className={clsx(
                  'flex w-full items-center justify-between gap-2 rounded-control px-2.5 py-2 text-left text-sm transition-colors',
                  on
                    ? 'bg-brand-600/12 font-medium text-brand-700'
                    : 'text-ink-soft hover:bg-surface-sunken',
                )}
              >
                {p.label}
                {on ? <Check className="size-4" aria-hidden /> : null}
              </button>
            );
          })}

          {preset === 'custom' ? (
            <div className="mt-1.5 space-y-2 border-t border-hairline px-1 pt-2.5">
              <label className="block">
                <span className="mb-1 block text-2xs font-medium text-ink-muted">Dari tanggal</span>
                <input
                  type="date"
                  value={value.from ?? value.date ?? ''}
                  onChange={(e) =>
                    onChange({ ...value, date: undefined, month: undefined, from: e.target.value })
                  }
                  className={`w-full ${inputClassSm}`}
                />
              </label>
              <label className="block">
                <span className="mb-1 block text-2xs font-medium text-ink-muted">
                  Sampai tanggal
                </span>
                <input
                  type="date"
                  value={value.to ?? ''}
                  onChange={(e) => onChange({ ...value, to: e.target.value })}
                  className={`w-full ${inputClassSm}`}
                />
              </label>
            </div>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}
