'use client';

import clsx from 'clsx';
import { Flame, Snowflake, Sun, type LucideIcon } from 'lucide-react';

import type { LabelCategory, LabelCategorySummary } from '@/lib/api';

/**
 * The three categories, in one place.
 *
 * Their order is the customer journey — cold, then warm, then hot — and it is
 * the same everywhere: the card on Performa, the pills here, the columns of the
 * movement table. A reader who learns the order once should not have to learn
 * it again on the next screen.
 */
export const CATEGORIES: {
  id: LabelCategory;
  label: string;
  icon: LucideIcon;
  /** Text colour for the name and the number. */
  tone: string;
  /** Background for the pill when it is the one being read. */
  active: string;
  /** Background for the summary banner. */
  banner: string;
}[] = [
  {
    id: 'cold',
    label: 'Cold',
    icon: Snowflake,
    tone: 'text-info',
    active: 'border-info bg-info/12 text-info',
    banner: 'border-info/25 bg-info/8',
  },
  {
    id: 'warm',
    label: 'Warm',
    icon: Sun,
    tone: 'text-warn',
    active: 'border-warn bg-warn-soft text-warn',
    banner: 'border-warn/25 bg-warn-soft/60',
  },
  {
    id: 'hot',
    label: 'Hot',
    icon: Flame,
    tone: 'text-danger',
    active: 'border-danger bg-danger-soft text-danger',
    banner: 'border-danger/25 bg-danger-soft/60',
  },
];

export const categoryOf = (id: LabelCategory) =>
  CATEGORIES.find((c) => c.id === id) ?? CATEGORIES[0];

/**
 * The category switcher: three pills, each carrying its own total.
 *
 * The count sits inside the pill rather than under it so the choice and the
 * size of what is being chosen are read in one glance — picking Hot when Hot
 * holds four customers is a different decision from picking it when it holds
 * two thousand.
 */
export function CategoryPills({
  value,
  summary,
  onChange,
}: {
  value: LabelCategory;
  summary: LabelCategorySummary | undefined;
  onChange: (c: LabelCategory) => void;
}) {
  return (
    <div className="flex flex-wrap items-center gap-2">
      {CATEGORIES.map((c) => {
        const active = value === c.id;
        return (
          <button
            key={c.id}
            type="button"
            onClick={() => onChange(c.id)}
            aria-pressed={active}
            className={clsx(
              'inline-flex items-center gap-1.5 rounded-full border px-3 py-1.5 text-sm font-medium transition-colors',
              active
                ? c.active
                : 'border-hairline bg-surface-raised text-ink-soft hover:bg-surface-sunken',
            )}
          >
            <c.icon className="size-3.5" aria-hidden />
            {c.label}
            <span className="nums opacity-70">
              ({summary ? summary[c.id].toLocaleString('id-ID') : '–'})
            </span>
          </button>
        );
      })}
    </div>
  );
}

/**
 * The headline figure for whichever category is open.
 *
 * Tinted in that category's own colour, because it is the one number the whole
 * screen is about and the tables underneath are ways of taking it apart.
 */
export function CategoryBanner({
  category,
  total,
  caption,
  unit = 'Customer',
}: {
  category: LabelCategory;
  total: number;
  caption?: string;
  /**
   * What is being counted.
   *
   * A prop rather than the word "Customer" baked in: the same banner heads the
   * customer's own page, where the number is how many times their label has
   * changed, and "5 Customer" under one person's name is simply wrong.
   */
  unit?: string;
}) {
  const c = categoryOf(category);
  return (
    <div className={clsx('rounded-card border p-4', c.banner)}>
      <span className={clsx('flex items-center gap-2 text-sm font-medium', c.tone)}>
        <c.icon className="size-4" aria-hidden />
        {caption ?? `Total Customer ${c.label}`}
      </span>
      <span className="nums mt-1 block text-3xl font-semibold tracking-[-0.02em] text-ink">
        {total.toLocaleString('id-ID')}{' '}
        <span className="text-base font-normal text-ink-muted">{unit}</span>
      </span>
    </div>
  );
}

/** One label as the operator sees it, tinted by the category it falls into. */
export function LabelChip({
  name,
  category,
  prefix,
}: {
  name: string;
  category: LabelCategory | '';
  prefix?: string;
}) {
  const tone = category ? categoryOf(category).tone : 'text-ink-soft';
  return (
    <span
      className={clsx(
        'inline-flex items-center gap-1 rounded-full border border-hairline bg-surface-sunken/50 px-2 py-0.5 text-xs font-medium',
        tone,
      )}
    >
      {prefix}
      {name}
    </span>
  );
}
