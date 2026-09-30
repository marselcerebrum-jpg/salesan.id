'use client';

import { useEffect, useState } from 'react';

import type { AccountRef, AppRef } from '@/lib/types';

export interface StatusLabelFilter {
  applicationId: string;
  accountId: string;
}

/**
 * Application and WhatsApp number, applied on a press rather than on a change.
 *
 * Two dropdowns that each refetch the moment they move would send three
 * requests for one decision — pick a brand, pick a number, wait, pick again —
 * and the table underneath would rearrange itself twice while the operator was
 * still choosing. The button is what makes it one decision.
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
  const [draft, setDraft] = useState(value);

  // The URL is allowed to change underneath this — a link somebody was sent, or
  // stepping back up the breadcrumb — and the draft has to follow it, or the
  // boxes would show one thing while the table shows another.
  useEffect(() => setDraft(value), [value]);

  // A number belongs to one brand. Offering all of them under a chosen brand
  // would let somebody build a filter that cannot match anything.
  const shown = draft.applicationId
    ? accounts.filter((a) => a.application_id === draft.applicationId)
    : accounts;

  const dirty =
    draft.applicationId !== value.applicationId || draft.accountId !== value.accountId;

  return (
    <div className="flex flex-wrap items-end gap-3">
      <Field label="Aplikasi">
        <select
          value={draft.applicationId}
          onChange={(e) => {
            const applicationId = e.target.value;
            // Clearing the number is not tidiness: keeping it would leave a
            // brand and a number that do not belong together.
            setDraft({ applicationId, accountId: '' });
          }}
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
          value={draft.accountId}
          onChange={(e) => setDraft({ ...draft, accountId: e.target.value })}
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

      <button
        type="button"
        onClick={() => onApply(draft)}
        disabled={!dirty}
        className="h-9 rounded-control bg-brand-700 px-4 text-sm font-medium text-white transition-colors hover:bg-brand-800 disabled:opacity-40"
      >
        Terapkan
      </button>
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
