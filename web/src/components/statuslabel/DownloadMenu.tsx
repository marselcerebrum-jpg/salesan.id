'use client';

import clsx from 'clsx';
import { ChevronDown, Download, Loader2 } from 'lucide-react';
import { useEffect, useRef, useState } from 'react';

export type ReportScope = 'daily' | 'summary' | 'detail';

const SHAPES: { id: ReportScope; label: string; hint: string }[] = [
  { id: 'daily', label: 'Rekap Harian', hint: 'Cold, Warm, Hot per hari' },
  { id: 'summary', label: 'Rekap Perpindahan', hint: 'Perpindahan antar status per hari' },
  { id: 'detail', label: 'Detail Customer', hint: 'Satu baris per customer' },
];

/**
 * One button for the report, three shapes behind it.
 *
 * Three buttons in a row was the alternative and it asked the reader to choose
 * before they knew what they were choosing between. A menu can say what each
 * one contains, and the format sits beside it because CSV or XLSX is a smaller
 * decision than which report.
 */
export function DownloadMenu({
  busy,
  onPick,
}: {
  busy: string | null;
  onPick: (scope: ReportScope, format: 'csv' | 'xlsx') => void;
}) {
  const [open, setOpen] = useState(false);
  const box = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    const away = (e: MouseEvent) => {
      if (!box.current?.contains(e.target as Node)) setOpen(false);
    };
    const esc = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setOpen(false);
    };
    document.addEventListener('mousedown', away);
    document.addEventListener('keydown', esc);
    return () => {
      document.removeEventListener('mousedown', away);
      document.removeEventListener('keydown', esc);
    };
  }, [open]);

  return (
    <div ref={box} className="relative">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
        className="inline-flex h-9 items-center gap-1.5 rounded-control bg-brand-700 px-3.5 text-sm font-medium text-white transition-colors hover:bg-brand-800"
      >
        {busy ? <Loader2 className="size-4 animate-spin" /> : <Download className="size-4" />}
        Download Report
        <ChevronDown className="size-3.5 opacity-80" aria-hidden />
      </button>

      {open ? (
        <div
          role="menu"
          className="absolute right-0 z-30 mt-1.5 w-72 overflow-hidden rounded-card border border-hairline bg-surface-raised py-1 shadow-e3"
        >
          {SHAPES.map((s) => (
            <div key={s.id} className="flex items-center gap-2 px-3 py-2 hover:bg-surface-sunken">
              <span className="min-w-0 flex-1">
                <span className="block truncate text-sm font-medium text-ink">{s.label}</span>
                <span className="block truncate text-2xs text-ink-muted">{s.hint}</span>
              </span>
              {(['csv', 'xlsx'] as const).map((format) => (
                <button
                  key={format}
                  type="button"
                  onClick={() => {
                    setOpen(false);
                    onPick(s.id, format);
                  }}
                  disabled={busy === `${s.id}-${format}`}
                  className={clsx(
                    'rounded-control border border-hairline px-2 py-1 text-2xs font-semibold uppercase',
                    'text-ink-soft transition-colors hover:bg-surface-sunken disabled:opacity-50',
                  )}
                >
                  {format}
                </button>
              ))}
            </div>
          ))}
        </div>
      ) : null}
    </div>
  );
}
