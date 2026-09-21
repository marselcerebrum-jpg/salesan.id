'use client';

import clsx from 'clsx';
import { Check, CheckCircle2, Loader2, X } from 'lucide-react';
import { useEffect, useState } from 'react';

import { votePoll } from '@/lib/api';
import type { Poll } from '@/lib/types';

const sameSelection = (a: number[], b: number[]) =>
  a.length === b.length && a.every((v, i) => v === b[i]);

/**
 * A poll, drawn the way WhatsApp draws one: the question, "Pilih satu", then
 * each option as a circle, its name, its count on the right and a bar under it,
 * and "Lihat jumlah suara" at the foot. Used by chat and by channels, so a poll
 * looks the same wherever it is.
 *
 * Voting is optimistic: the bar moves as soon as it is clicked, and rolls back
 * if the send fails. Waiting for a round trip on every tap would make the poll
 * feel broken on a slow connection, and the rollback is what keeps that honest.
 */
export function PollBubble({
  poll,
  canVote,
  tallies = true,
}: {
  poll: Poll;
  canVote: boolean;
  /**
   * False where the counts are not known. The bars and numbers are left out
   * rather than drawn at zero: "0" beside every option would be a claim nobody
   * made.
   */
  tallies?: boolean;
}) {
  const [selected, setSelected] = useState<number[] | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [showResults, setShowResults] = useState(false);

  // Local selection wins while a vote is in flight; otherwise the server's.
  const chosen = selected ?? poll.selected_idx;
  const multiple = poll.selectable_count !== 1;

  // Hand control back to the server once it agrees with us. Without this the
  // local value would stick for the life of the bubble, so a vote later changed
  // on the phone would never show up here.
  useEffect(() => {
    if (selected && sameSelection(selected, poll.selected_idx)) setSelected(null);
  }, [selected, poll.selected_idx]);

  async function toggle(index: number) {
    if (!canVote || busy) return;

    const next = chosen.includes(index)
      ? chosen.filter((i) => i !== index)
      : multiple
        ? [...chosen, index].sort((a, b) => a - b)
        : [index];

    const previous = selected;
    setSelected(next);
    setBusy(true);
    setError(null);
    try {
      await votePoll(poll.message_id, next);
    } catch (err) {
      setSelected(previous);
      setError(err instanceof Error ? err.message : 'Suara gagal dikirim');
    } finally {
      setBusy(false);
    }
  }

  // Bars are shares of all votes cast, the way WhatsApp fills them.
  const totalVotes = poll.options.reduce((sum, o) => sum + o.votes, 0);

  return (
    <div className="min-w-[260px] max-w-[360px] py-0.5">
      <p className="text-base leading-snug font-semibold break-words text-wa-text">{poll.name}</p>
      <p className="mt-1 flex items-center gap-1 text-xs text-wa-text-2">
        <CheckCircle2 className="size-3.5" />
        {multiple ? 'Pilih satu atau lebih' : 'Pilih satu'}
      </p>

      <div className="mt-3 space-y-3">
        {poll.options.map((option) => {
          const picked = chosen.includes(option.index);
          const share = totalVotes > 0 ? (option.votes / totalVotes) * 100 : 0;

          return (
            <button
              key={option.index}
              type="button"
              onClick={() => void toggle(option.index)}
              disabled={!canVote || busy}
              aria-pressed={picked}
              className={clsx('block w-full text-left', !canVote && 'cursor-default')}
            >
              <span className="flex items-center gap-2.5">
                <span
                  className={clsx(
                    'grid size-[22px] shrink-0 place-items-center border-2 transition-colors',
                    multiple ? 'rounded-md' : 'rounded-full',
                    picked ? 'border-wa-accent bg-wa-accent text-white' : 'border-wa-text-2/70',
                  )}
                >
                  {picked ? <Check className="size-3.5" strokeWidth={3} /> : null}
                </span>
                <span className="min-w-0 flex-1 text-sm break-words text-wa-text">
                  {option.name}
                </span>
                {tallies ? (
                  <span className="shrink-0 text-xs tabular-nums text-wa-text-2">
                    {option.votes}
                  </span>
                ) : null}
              </span>
              {/* The bar sits under the name, full width, as on the phone. */}
              {tallies ? (
                <span className="mt-1.5 ml-[32px] block h-1.5 overflow-hidden rounded-full bg-wa-text-2/15">
                  <span
                    className="block h-full rounded-full bg-wa-accent transition-[width] duration-300"
                    style={{ width: `${share}%` }}
                  />
                </span>
              ) : null}
            </button>
          );
        })}
      </div>

      {error ? <p className="mt-2 text-2xs text-danger">{error}</p> : null}

      <div className="-mx-1 mt-3 border-t border-wa-text-2/15 pt-1">
        {tallies ? (
          <button
            type="button"
            onClick={() => setShowResults(true)}
            className="flex w-full items-center justify-center gap-1.5 rounded-md py-1.5 text-sm text-wa-accent transition-colors hover:bg-black/[0.04] dark:hover:bg-white/[0.06]"
          >
            {busy ? <Loader2 className="size-3.5 animate-spin" /> : null}
            Lihat jumlah suara
          </button>
        ) : (
          <p className="py-1.5 text-center text-xs text-wa-text-2">
            Jumlah suara belum bisa dibaca dari WhatsApp
          </p>
        )}
      </div>

      {showResults ? (
        <PollResults poll={poll} totalVotes={totalVotes} onClose={() => setShowResults(false)} />
      ) : null}
    </div>
  );
}

/**
 * The results, option by option, most votes first.
 *
 * Counts and shares only. Who voted for what is known for a chat poll but not
 * for a channel poll, and a list that appeared on one and not the other would
 * make the two look like different features.
 */
function PollResults({
  poll,
  totalVotes,
  onClose,
}: {
  poll: Poll;
  totalVotes: number;
  onClose: () => void;
}) {
  const ranked = [...poll.options].sort((a, b) => b.votes - a.votes);

  return (
    <div
      className="fixed inset-0 z-[60] flex items-center justify-center bg-black/50 p-4"
      onMouseDown={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
    >
      <div
        role="dialog"
        aria-modal="true"
        aria-label="Jumlah suara"
        className="w-full max-w-[400px] rounded-card bg-wa-panel-2 p-5 shadow-e4"
      >
        <div className="flex items-start justify-between gap-3">
          <div className="min-w-0">
            <h2 className="text-base font-semibold text-wa-text">Jumlah suara</h2>
            <p className="mt-0.5 text-sm break-words text-wa-text-2">{poll.name}</p>
          </div>
          <button
            type="button"
            onClick={onClose}
            aria-label="Tutup"
            className="rounded-lg p-1 text-wa-text-2 hover:bg-wa-active"
          >
            <X className="size-5" />
          </button>
        </div>

        <ul className="mt-4 space-y-3">
          {ranked.map((o) => {
            const share = totalVotes > 0 ? Math.round((o.votes / totalVotes) * 100) : 0;
            return (
              <li key={o.index}>
                <div className="flex items-baseline justify-between gap-3">
                  <span className="min-w-0 text-sm break-words text-wa-text">{o.name}</span>
                  <span className="shrink-0 text-xs tabular-nums text-wa-text-2">
                    {o.votes} suara · {share}%
                  </span>
                </div>
                <span className="mt-1 block h-1.5 overflow-hidden rounded-full bg-wa-text-2/15">
                  <span
                    className="block h-full rounded-full bg-wa-accent"
                    style={{ width: `${share}%` }}
                  />
                </span>
              </li>
            );
          })}
        </ul>

        <p className="mt-4 text-xs text-wa-text-2">
          {poll.total_voters > 0
            ? `${poll.total_voters} orang memberi suara`
            : totalVotes > 0
              ? `${totalVotes} suara`
              : 'Belum ada suara'}
        </p>
      </div>
    </div>
  );
}
