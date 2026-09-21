'use client';

import clsx from 'clsx';
import { Loader2 } from 'lucide-react';
import type { ButtonHTMLAttributes, ReactNode } from 'react';

type Variant = 'primary' | 'secondary' | 'danger' | 'ghost' | 'subtle';
type Size = 'sm' | 'md';

const VARIANTS: Record<Variant, string> = {
  primary:
    'bg-brand-800 text-white hover:bg-brand-900 focus-visible:outline-brand-800 disabled:bg-brand-800/50',
  secondary:
    'bg-surface-raised text-ink border border-hairline hover:bg-surface-sunken focus-visible:outline-brand-800',
  danger: 'bg-danger text-white hover:brightness-110 focus-visible:outline-danger',
  ghost: 'text-ink-soft hover:bg-surface-sunken focus-visible:outline-brand-800',
  subtle:
    'bg-surface-sunken text-ink-soft border border-hairline hover:bg-hairline/60 focus-visible:outline-brand-800',
};

const SIZES: Record<Size, string> = {
  sm: 'h-9 px-3 text-sm gap-1.5',
  md: 'h-10 px-4 text-sm gap-2',
};

interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: Variant;
  size?: Size;
  loading?: boolean;
  icon?: ReactNode;
}

export function Button({
  variant = 'secondary',
  size = 'md',
  loading = false,
  icon,
  className,
  children,
  disabled,
  ...rest
}: ButtonProps) {
  return (
    <button
      {...rest}
      disabled={disabled || loading}
      className={clsx(
        'inline-flex items-center justify-center rounded-control font-medium transition-colors',
        'focus-visible:outline-2 focus-visible:outline-offset-2',
        'disabled:cursor-not-allowed disabled:opacity-60',
        VARIANTS[variant],
        SIZES[size],
        className,
      )}
    >
      {loading ? <Loader2 className="size-4 animate-spin" aria-hidden /> : icon}
      {children}
    </button>
  );
}
