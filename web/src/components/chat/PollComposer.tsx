'use client';

import clsx from 'clsx';
import { GripVertical, Loader2, Plus, X } from 'lucide-react';
import { useEffect, useRef, useState } from 'react';

import { Button } from '@/components/ui/Button';
import { ErrorNote } from '@/components/ui/Primitives';

/** Mirrors the server's limits so the form can refuse before a round trip. */
export const MIN_OPTIONS = 2;
export const MAX_OPTIONS = 12;
const MAX_QUESTION = 255;
const MAX_OPTION = 100;

/**
 * An answer carries an id, and the id is the point.
 *
 * The options were a plain `string[]` rendered with `key={index}`, which is
 * fine for a list that only grows at the end and is wrong for this one: these
 * can be reordered and deleted from the middle. With an index as the key React
 * keeps the same DOM node at each position and only rewrites its value, so
 * moving the answer you are typing in leaves the caret behind on the answer
 * that took its place, and deleting an answer above the one you are editing
 * slides the text under your cursor.
 *
 * An id that belongs to the answer rather than to the position makes React
 * move the node instead of rewriting it, and the caret goes with it.
 */
interface PollOption {
  id: string;
  text: string;
}

let optionSeq = 0;
function newOption(): PollOption {
  optionSeq += 1;
  return { id: `opt-${optionSeq}`, text: '' };
}

interface PollComposerProps {
  open: boolean;
  onClose: () => void;
  onSubmit: (poll: { name: string; options: string[]; allowMultiple: boolean }) => Promise<void>;
}

/**
 * Poll builder, following WhatsApp's own: a question, two to twelve answers,
 * and a switch for whether more than one may be chosen.
 */
export function PollComposer({ open, onClose, onSubmit }: PollComposerProps) {
  const [question, setQuestion] = useState('');
  const [options, setOptions] = useState<PollOption[]>(() => [newOption(), newOption()]);
  const [allowMultiple, setAllowMultiple] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const questionRef = useRef<HTMLInputElement>(null);

  // Read through a ref so the key handler binds once per opening rather than
  // on every keystroke — otherwise focus would be yanked back mid-typing.
  const closeRef = useRef(onClose);
  closeRef.current = onClose;

  useEffect(() => {
    if (!open) return;

    setQuestion('');
    setOptions([newOption(), newOption()]);
    setAllowMultiple(false);
    setError(null);
    questionRef.current?.focus();

    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') closeRef.current();
    };
    document.addEventListener('keydown', onKeyDown);

    const previousOverflow = document.body.style.overflow;
    document.body.style.overflow = 'hidden';

    return () => {
      document.removeEventListener('keydown', onKeyDown);
      document.body.style.overflow = previousOverflow;
    };
  }, [open]);

  if (!open) return null;

  const filled = options.map((o) => o.text.trim()).filter(Boolean);
  const duplicate = new Set(filled).size !== filled.length;
  const canSubmit = question.trim() !== '' && filled.length >= MIN_OPTIONS && !duplicate && !busy;

  function setOption(index: number, value: string) {
    setOptions((current) => current.map((o, i) => (i === index ? { ...o, text: value } : o)));
  }

  function addOption() {
    setOptions((current) => (current.length >= MAX_OPTIONS ? current : [...current, newOption()]));
  }

  function removeOption(index: number) {
    // Never below the minimum: the field is emptied instead of removed, so the
    // form cannot reach a state it could not be submitted from.
    setOptions((current) =>
      current.length <= MIN_OPTIONS
        ? current.map((o, i) => (i === index ? { ...o, text: '' } : o))
        : current.filter((_, i) => i !== index),
    );
  }

  function move(index: number, direction: -1 | 1) {
    const next = index + direction;
    if (next < 0 || next >= options.length) return;
    setOptions((current) => {
      const reordered = [...current];
      [reordered[index], reordered[next]] = [reordered[next], reordered[index]];
      return reordered;
    });
  }

  async function submit() {
    if (!canSubmit) return;
    setBusy(true);
    setError(null);
    try {
      await onSubmit({ name: question.trim(), options: filled, allowMultiple });
      onClose();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Polling gagal dibuat.');
    } finally {
      setBusy(false);
    }
  }

  return (
    <div
      className="fixed inset-0 z-[55] flex items-center justify-center bg-black/50 p-4"
      onMouseDown={(event) => {
        if (event.target === event.currentTarget && !busy) onClose();
      }}
    >
      <div
        role="dialog"
        aria-modal="true"
        aria-label="Buat polling"
        className="flex max-h-[calc(100dvh-2rem)] w-full max-w-[460px] flex-col rounded-card bg-wa-panel-2 shadow-e4"
      >
        <header className="flex items-center justify-between gap-4 px-5 pt-5">
          <h2 className="text-lg font-semibold text-wa-text">Buat polling</h2>
          <button
            type="button"
            onClick={onClose}
            disabled={busy}
            aria-label="Tutup"
            className="-mt-1 -mr-1 rounded-lg p-1.5 text-wa-text-2 hover:bg-wa-active"
          >
            <X className="size-5" />
          </button>
        </header>

        <div className="scrollbar-slim min-h-0 flex-1 space-y-4 overflow-y-auto px-5 py-4">
          {error ? <ErrorNote message={error} /> : null}

          <label className="block">
            <span className="mb-1.5 block text-xs font-medium text-wa-text-2">Pertanyaan</span>
            <input
              ref={questionRef}
              value={question}
              onChange={(event) => setQuestion(event.target.value)}
              maxLength={MAX_QUESTION}
              placeholder="Contoh: Jadwal meeting minggu depan?"
              className="w-full rounded-lg bg-wa-panel px-3.5 py-2.5 text-sm text-wa-text outline-none focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-wa-accent placeholder:text-wa-text-2"
            />
          </label>

          <div>
            <span className="mb-1.5 block text-xs font-medium text-wa-text-2">Pilihan</span>
            <div className="space-y-2">
              {options.map((option, index) => (
                <div key={option.id} className="flex items-center gap-1.5">
                  <span className="flex flex-col text-wa-text-2">
                    <button
                      type="button"
                      onClick={() => move(index, -1)}
                      disabled={index === 0}
                      aria-label={`Naikkan pilihan ${index + 1}`}
                      className="disabled:opacity-25"
                    >
                      <GripVertical className="size-4 rotate-90" />
                    </button>
                  </span>
                  <input
                    value={option.text}
                    onChange={(event) => setOption(index, event.target.value)}
                    onKeyDown={(event) => {
                      // Enter moves on to the next answer, adding one when it
                      // is the last — the same rhythm as typing a list.
                      if (event.key === 'Enter') {
                        event.preventDefault();
                        if (index === options.length - 1) addOption();
                      }
                    }}
                    maxLength={MAX_OPTION}
                    placeholder={`Pilihan ${index + 1}`}
                    className="flex-1 rounded-lg bg-wa-panel px-3.5 py-2.5 text-sm text-wa-text outline-none focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-wa-accent placeholder:text-wa-text-2"
                  />
                  <button
                    type="button"
                    onClick={() => removeOption(index)}
                    aria-label={`Hapus pilihan ${index + 1}`}
                    className="grid size-8 place-items-center rounded-full text-wa-text-2 hover:bg-wa-active"
                  >
                    <X className="size-4" />
                  </button>
                </div>
              ))}
            </div>

            {options.length < MAX_OPTIONS ? (
              <button
                type="button"
                onClick={addOption}
                className="mt-2 inline-flex items-center gap-1.5 text-sm text-wa-accent"
              >
                <Plus className="size-4" />
                Tambah pilihan
              </button>
            ) : (
              <p className="mt-2 text-xs text-wa-text-2">Maksimal {MAX_OPTIONS} pilihan.</p>
            )}

            {duplicate ? (
              <p className="mt-2 text-xs text-danger">
                Ada pilihan yang ditulis dua kali. WhatsApp mengenali pilihan dari teksnya, jadi
                dua yang sama tidak bisa dibedakan saat dihitung.
              </p>
            ) : null}
          </div>

          <label className="flex cursor-pointer items-center gap-2.5 text-sm text-wa-text">
            <input
              type="checkbox"
              checked={allowMultiple}
              onChange={(event) => setAllowMultiple(event.target.checked)}
              className="size-4 accent-wa-accent"
            />
            Boleh pilih lebih dari satu
          </label>
        </div>

        <footer className="flex justify-end gap-2 px-5 pb-5">
          <Button variant="secondary" onClick={onClose} disabled={busy}>
            Batal
          </Button>
          <button
            type="button"
            onClick={() => void submit()}
            disabled={!canSubmit}
            className={clsx(
              'inline-flex h-10 items-center justify-center gap-2 rounded-control px-4 text-sm font-medium',
              'bg-wa-accent text-white transition-opacity disabled:opacity-40',
            )}
          >
            {busy ? <Loader2 className="size-4 animate-spin" /> : null}
            Kirim polling
          </button>
        </footer>
      </div>
    </div>
  );
}
