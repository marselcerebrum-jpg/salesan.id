'use client';

import clsx from 'clsx';
import { Loader2 } from 'lucide-react';
import type { ReactNode } from 'react';

/** Small pill used for tags, counts and status. */
export function Badge({
  children,
  className,
  tone = 'neutral',
}: {
  children: ReactNode;
  className?: string;
  tone?: 'neutral' | 'green' | 'amber' | 'danger';
}) {
  const tones = {
    neutral: 'bg-surface-sunken text-ink-soft border-hairline',
    green: 'bg-brand-600/10 text-brand-700 border-brand-600/20',
    amber: 'bg-warn-soft text-warn border-warn/30',
    danger: 'bg-danger-soft text-danger border-danger/20',
  };
  return (
    <span
      className={clsx(
        'inline-flex items-center gap-1 rounded-full border px-2.5 py-0.5 text-2xs font-medium whitespace-nowrap',
        tones[tone],
        className,
      )}
    >
      {children}
    </span>
  );
}

/** Coloured dot + label, used for connection state. */
export function StatusPill({
  label,
  dotClass,
  textClass,
  bgClass,
}: {
  label: string;
  dotClass: string;
  textClass: string;
  bgClass: string;
}) {
  return (
    <span
      className={clsx(
        'inline-flex items-center gap-1.5 rounded-full px-2.5 py-1 text-2xs font-medium',
        bgClass,
        textClass,
      )}
    >
      <span className={clsx('size-1.5 rounded-full', dotClass)} aria-hidden />
      {label}
    </span>
  );
}

/** Circular monogram avatar. */
export function Avatar({
  label,
  size = 40,
  className,
  tone = 'brand',
}: {
  label: string;
  size?: number;
  className?: string;
  tone?: 'brand' | 'muted';
}) {
  return (
    <span
      style={{ width: size, height: size, fontSize: Math.round(size * 0.36) }}
      className={clsx(
        'inline-flex shrink-0 items-center justify-center rounded-full font-semibold',
        tone === 'brand' ? 'bg-brand-800 text-white' : 'bg-surface-sunken text-ink-soft',
        className,
      )}
    >
      {label}
    </span>
  );
}

/** Centred spinner for panel-level loading. */
export function Spinner({ label }: { label?: string }) {
  return (
    <div className="flex flex-col items-center justify-center gap-2 py-10 text-ink-muted">
      <Loader2 className="size-5 animate-spin" aria-hidden />
      {label ? <p className="text-sm">{label}</p> : null}
    </div>
  );
}

/** Illustration-free empty state. */
export function EmptyState({
  title,
  description,
  action,
  icon,
}: {
  title: string;
  description?: string;
  action?: ReactNode;
  icon?: ReactNode;
}) {
  return (
    <div className="flex flex-col items-center justify-center gap-3 px-6 py-16 text-center">
      {icon ? <div className="text-ink-muted">{icon}</div> : null}
      <p className="text-lg font-medium text-ink">{title}</p>
      {description ? (
        <p className="max-w-sm text-sm leading-relaxed text-ink-muted">{description}</p>
      ) : null}
      {action}
    </div>
  );
}

/** Inline error banner. */
export function ErrorNote({ message }: { message: string }) {
  return (
    <p
      role="alert"
      className="rounded-lg border border-danger/20 bg-danger-soft px-3 py-2 text-sm text-danger"
    >
      {message}
    </p>
  );
}

/** Segmented filter chip, as used above the conversation list. */
export function Chip({
  active,
  onClick,
  children,
  className,
}: {
  active: boolean;
  onClick: () => void;
  children: ReactNode;
  className?: string;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-pressed={active}
      className={clsx(
        'inline-flex items-center gap-1.5 rounded-full border px-3 py-1.5 text-xs font-medium transition-colors whitespace-nowrap',
        active
          ? 'border-brand-700/30 bg-brand-600/10 text-brand-700'
          : 'border-hairline bg-surface-raised text-ink-soft hover:bg-surface-sunken',
        className,
      )}
    >
      {children}
    </button>
  );
}
