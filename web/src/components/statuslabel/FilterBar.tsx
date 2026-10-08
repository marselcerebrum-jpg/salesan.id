'use client';

import type { AccountRef, AppRef } from '@/lib/types';

export interface StatusLabelFilter {
  applicationId: string;
  accountId: string;
}

/**
 * Application and WhatsApp number, applied the moment either one moves.
 *
 * There was a button here, on the reasoning that two dropdowns refetching on
 * every change would send three requests for one decision and rearrange the
 * table twice while the operator was still choosing. The reasoning holds and
 * the conclusion did not: what actually happened is that people changed a
 * dropdown, read the table, and believed what they read — because a filter
 * that has visibly moved and a table that has not is indistinguishable from a
 * filter that has been applied.
 *
 * A wasted request costs nothing anybody can feel. A number read under the
 * wrong filter costs a decision. So the choice applies itself, and the one
 * case the button existed for — picking a brand and then one of its numbers —
 * is handled by clearing the number when the brand changes, which this has
 * always done anyway.
 */
export function FilterBar({
  applications,
  accounts,
  value,
  onApply,
}: {
  applications: AppRef[];
  accounts: AccountRef[];
  value: StatusLabelFilter;
  onApply: (next: StatusLabelFilter) => void;
}) {
  // Read straight from `value`, with no draft in between. The draft existed to
  // hold a half-made choice until the button confirmed it; with no button there
  // is no half-made choice, and a second copy of the state would only be
  // something for the URL to disagree with.
  const shown = value.applicationId
    ? accounts.filter((a) => a.application_id === value.applicationId)
    : accounts;

  return (
    <div className="flex flex-wrap items-end gap-3">
      <Field label="Aplikasi">
        <select
          value={value.applicationId}
          onChange={(e) =>
            // Clearing the number is not tidiness: keeping it would leave a
            // brand and a number that do not belong together.
            onApply({ applicationId: e.target.value, accountId: '' })
          }
          className="h-9 w-48 rounded-control border border-hairline bg-surface-raised px-2.5 text-sm text-ink"
        >
          <option value="">Semua App</option>
          {applications.map((a) => (
            <option key={a.id} value={a.id}>
              {a.name}
            </option>
          ))}
        </select>
      </Field>

      <Field label="Nomor WhatsApp">
        <select
          value={value.accountId}
          onChange={(e) => onApply({ ...value, accountId: e.target.value })}
          className="h-9 w-56 rounded-control border border-hairline bg-surface-raised px-2.5 text-sm text-ink"
        >
          <option value="">Semua Nomor</option>
          {shown.map((a) => (
            <option key={a.id} value={a.id}>
              {a.name}
            </option>
          ))}
        </select>
      </Field>
    </div>
  );
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label className="flex flex-col gap-1">
      <span className="text-2xs font-medium text-ink-muted uppercase">{label}</span>
      {children}
    </label>
  );
}
