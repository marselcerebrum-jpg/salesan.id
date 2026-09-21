'use client';

import clsx from 'clsx';
import {
  Braces,
  Check,
  FileText,
  Image as ImageIcon,
  Loader2,
  Paperclip,
  Shuffle,
  Sparkles,
  Video,
  Wand2,
} from 'lucide-react';
import { useRef, useState } from 'react';

import { Notice, Toggle, inputClass, textareaClass } from '@/components/campaign/shared';
import {
  ApiError,
  MAX_DOCUMENT_BYTES,
  generateDraft,
  uploadCampaignDocument,
  type CampaignDocument,
  type DraftMode,
} from '@/lib/api';
import type { CustomVariable } from '@/lib/types';

/**
 * Step 3: what the message says, and what goes with it.
 *
 * Variables are written <<nama>> rather than {{nama}} because spintax already
 * owns single braces on this same screen, and asking somebody to read two
 * meanings out of one character is how a customer's name ends up as a spintax
 * branch. The server accepts both spellings, so campaigns written before this
 * still render.
 */

export const MAX_BODY = 5000;

export type MediaKind = 'image' | 'video' | 'document';

const MEDIA_TYPES: { id: MediaKind; label: string; icon: typeof ImageIcon }[] = [
  { id: 'image', label: 'Gambar', icon: ImageIcon },
  { id: 'video', label: 'Video', icon: Video },
  { id: 'document', label: 'Dokumen', icon: FileText },
];

const AI_MODES: { id: DraftMode; title: string; hint: string; icon: typeof Shuffle }[] = [
  { id: 'spintax', title: 'Spintax', hint: 'Variasi {a|b} pada frasa', icon: Shuffle },
  { id: 'variable', title: 'Variable', hint: 'Data jadi <<variabel>>', icon: Braces },
  { id: 'both', title: 'Keduanya', hint: 'Spintax + variabel', icon: Wand2 },
];

export interface MessageValue {
  name: string;
  body: string;
  mediaKind: MediaKind | null;
  /** Image and video only: the link the server fetches at send time. */
  mediaURL: string;
  /** Document only: the object in the private bucket, once uploaded. */
  document: CampaignDocument | null;
}

export function MessageStep({
  value,
  onChange,
  variables,
  applications,
  applicationID,
  onVariablesChanged,
  isStory = false,
}: {
  value: MessageValue;
  onChange: (next: MessageValue) => void;
  variables: CustomVariable[];
  applications: { id: string; name: string }[];
  applicationID: string;
  onVariablesChanged: () => void;
  /** A Story takes only text, a photo or a video: WhatsApp has no file status. */
  isStory?: boolean;
}) {
  const [aiOn, setAiOn] = useState(false);
  const body = useRef<HTMLTextAreaElement>(null);

  function set(patch: Partial<MessageValue>) {
    onChange({ ...value, ...patch });
  }

  /** Inserts at the caret, because appending puts it at the end of the wrong line. */
  function insert(token: string) {
    const el = body.current;
    if (!el) {
      set({ body: value.body + token });
      return;
    }
    const start = el.selectionStart ?? value.body.length;
    const end = el.selectionEnd ?? start;
    const next = value.body.slice(0, start) + token + value.body.slice(end);
    set({ body: next.slice(0, MAX_BODY) });
    requestAnimationFrame(() => {
      el.focus();
      el.setSelectionRange(start + token.length, start + token.length);
    });
  }

  const over = value.body.length > MAX_BODY;

  return (
    <>
      <p className="text-xs font-medium text-ink-soft">Label Broadcast</p>
      <input
        className={clsx(inputClass, 'mt-1.5')}
        value={value.name}
        onChange={(e) => set({ name: e.target.value })}
        placeholder="Contoh: Promo Ramadan 2026"
        aria-label="Label broadcast"
      />

      <div className="mt-4 rounded-lg border border-hairline bg-surface-sunken/60 px-3.5 py-3">
        <div className="flex items-start justify-between gap-3">
          <div className="min-w-0">
            <p className="text-sm font-semibold text-ink">
              Buat pesan dengan AI (variabel + spintax otomatis)
            </p>
            <p className="mt-0.5 text-2xs text-ink-muted">
              ChatGPT menyusun pesan dari caption &amp; aplikasi, lengkap dengan variabel dan/atau
              spintax anti-spam.
            </p>
          </div>
          <Toggle
            checked={aiOn}
            onChange={setAiOn}
            label="Buat pesan dengan AI"
          />
        </div>
      </div>

      {aiOn ? (
        <AIPanel
          applications={applications}
          applicationID={applicationID}
          knownVariables={variables.map((v) => v.key)}
          onDraft={(text) => set({ body: text.slice(0, MAX_BODY) })}
          onVariablesChanged={onVariablesChanged}
        />
      ) : null}

      <p className="mt-4 text-xs font-medium text-ink-soft">Isi Pesan</p>
      <p className="mt-0.5 text-2xs text-ink-muted">
        Variabel tersedia (diatur di menu Set Variabel) — klik untuk menyisipkan di posisi kursor
      </p>
      <div className="mt-1.5 flex flex-wrap items-center gap-1.5">
        {variables.map((v) => (
          <button
            key={v.key}
            type="button"
            onClick={() => insert(`<<${v.key}>>`)}
            title={v.label}
            className="rounded-full border border-brand-700/30 bg-brand-600/8 px-2.5 py-0.5 font-mono text-2xs text-brand-800 transition-colors hover:bg-brand-600/15"
          >
            {`<<${v.key}>>`}
          </button>
        ))}
        <button
          type="button"
          onClick={() => insert('{opsi1|opsi2}')}
          className="rounded-full border border-hairline bg-surface-raised px-2.5 py-0.5 font-mono text-2xs text-ink-soft transition-colors hover:bg-surface-sunken"
        >
          {'{opsi1|opsi2}'}
        </button>
      </div>

      <textarea
        ref={body}
        className={clsx(textareaClass, 'mt-2 h-44 font-mono text-xs')}
        value={value.body}
        onChange={(e) => set({ body: e.target.value })}
        placeholder={
          'Halo <<nama>>,\n\nKami punya penawaran spesial!\nProduk: <<produk>>\n\n{Hubungi kami|Chat sekarang} untuk info lebih lanjut.'
        }
        aria-label="Isi pesan"
      />

      <div className="mt-1 flex flex-wrap items-center justify-between gap-2">
        <p className="font-mono text-2xs text-ink-muted">
          Variabel: {'<<nama>>'} · Spintax: {'{opsi1|opsi2}'}
        </p>
        <p className={clsx('text-2xs tabular-nums', over ? 'text-danger' : 'text-ink-muted')}>
          {value.body.length}/{MAX_BODY}
        </p>
      </div>

      <p className="mt-4 text-xs font-medium text-ink-soft">Media (opsional)</p>
      <div className="mt-1.5 flex flex-wrap items-center gap-1.5">
        {/* WhatsApp has no file status, so a Story is never offered Dokumen.
            Offering it and refusing later would mean composing a whole story
            before being told it cannot exist. */}
        {MEDIA_TYPES.filter((m) => !(isStory && m.id === 'document')).map((m) => {
          const on = value.mediaKind === m.id;
          const Icon = m.icon;
          return (
            <button
              key={m.id}
              type="button"
              onClick={() => set({ mediaKind: m.id, mediaURL: '' })}
              aria-pressed={on}
              className={clsx(
                'inline-flex items-center gap-1.5 rounded-lg border px-3 py-1.5 text-xs transition-colors',
                on
                  ? 'border-brand-700 bg-brand-600/10 font-medium text-brand-800'
                  : 'border-hairline bg-surface-raised text-ink-soft hover:bg-surface-sunken',
              )}
            >
              <Icon className="size-3.5" />
              {m.label}
            </button>
          );
        })}
        {value.mediaKind ? (
          <button
            type="button"
            onClick={() => set({ mediaKind: null, mediaURL: '' })}
            className="text-xs text-ink-muted transition-colors hover:text-ink-soft"
          >
            hapus tipe
          </button>
        ) : null}
      </div>

      {!value.mediaKind ? (
        <p className="mt-1.5 text-2xs text-ink-muted">Pilih tipe media dulu (opsional).</p>
      ) : value.mediaKind === 'document' ? (
        <DocumentUpload
          document={value.document}
          onChange={(document) => set({ document })}
        />
      ) : (
        <input
          className={clsx(inputClass, 'mt-2')}
          value={value.mediaURL}
          onChange={(e) => set({ mediaURL: e.target.value })}
          placeholder={
            value.mediaKind === 'image'
              ? 'Tempel URL gambar (https://…jpg/png)'
              : 'Tempel URL video (https://…mp4)'
          }
          aria-label={value.mediaKind === 'image' ? 'URL gambar' : 'URL video'}
        />
      )}

      <p className="mt-1.5 text-2xs text-ink-muted">
        Untuk <span className="font-medium text-ink-soft">Gambar/Video</span> cukup tempel URL.
        Untuk <span className="font-medium text-ink-soft">Dokumen (PDF)</span> beri nama jelas (mis.
        Katalog-Produk.pdf) karena nama tampil di WhatsApp penerima.
      </p>
    </>
  );
}

/**
 * The document attachment.
 *
 * Uploaded rather than linked because the filename is what the recipient reads
 * on their phone, and because a catalogue usually does not live anywhere public.
 * The file goes straight into the private bucket and is deleted once the
 * campaign has finished.
 */
function DocumentUpload({
  document: doc,
  onChange,
}: {
  document: CampaignDocument | null;
  onChange: (next: CampaignDocument | null) => void;
}) {
  const [busy, setBusy] = useState(false);
  const [over, setOver] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const picker = useRef<HTMLInputElement>(null);

  async function take(file: File | undefined) {
    if (!file) return;
    setError(null);
    if (file.size > MAX_DOCUMENT_BYTES) {
      setError(`Berkas ${(file.size / 1024 / 1024).toFixed(1)} MB melebihi batas 16 MB.`);
      return;
    }
    setBusy(true);
    try {
      onChange(await uploadCampaignDocument(file));
    } catch (e) {
      setError(e instanceof ApiError || e instanceof Error ? e.message : 'Dokumen gagal diunggah.');
    } finally {
      setBusy(false);
    }
  }

  if (doc) {
    return (
      <div className="mt-2 flex items-center gap-3 rounded-lg border border-hairline bg-surface-raised px-3.5 py-2.5">
        <FileText className="size-4 shrink-0 text-ink-muted" />
        <span className="min-w-0 flex-1">
          <span className="block truncate text-sm text-ink">{doc.file_name}</span>
          <span className="block text-2xs text-ink-muted tabular-nums">
            {(doc.size / 1024 / 1024).toFixed(2)} MB
          </span>
        </span>
        <button
          type="button"
          onClick={() => onChange(null)}
          className="shrink-0 text-xs text-ink-muted transition-colors hover:text-danger"
        >
          ganti
        </button>
      </div>
    );
  }

  return (
    <>
      <div
        onDragOver={(e) => {
          e.preventDefault();
          setOver(true);
        }}
        onDragLeave={() => setOver(false)}
        onDrop={(e) => {
          e.preventDefault();
          setOver(false);
          void take(e.dataTransfer.files[0]);
        }}
        className={clsx(
          'mt-2 rounded-lg border border-dashed transition-colors',
          over ? 'border-brand-700 bg-brand-600/8' : 'border-hairline-strong bg-surface',
        )}
      >
        <button
          type="button"
          disabled={busy}
          onClick={() => picker.current?.click()}
          className="flex w-full items-center justify-center gap-2 px-4 py-4 text-sm text-ink-soft disabled:opacity-60"
        >
          {busy ? (
            <Loader2 className="size-4 animate-spin" />
          ) : (
            <Paperclip className="size-4 text-ink-muted" />
          )}
          {busy ? 'Mengunggah…' : 'Unggah dokumen (PDF, DOCX, XLSX… maks 16MB)'}
        </button>
        <input
          ref={picker}
          type="file"
          className="hidden"
          onChange={(e) => void take(e.target.files?.[0])}
        />
      </div>
      {error ? (
        <div className="mt-2">
          <Notice tone="danger">{error}</Notice>
        </div>
      ) : null}
    </>
  );
}

/** The AI panel: a caption in, a message with variables and spintax out. */
function AIPanel({
  applications,
  applicationID,
  knownVariables,
  onDraft,
  onVariablesChanged,
}: {
  applications: { id: string; name: string }[];
  applicationID: string;
  knownVariables: string[];
  onDraft: (text: string) => void;
  onVariablesChanged: () => void;
}) {
  const [caption, setCaption] = useState('');
  // Defaults to the campaign's own application when there is one: a variable
  // invented for this brand's promo is almost never wanted workspace-wide.
  const [target, setTarget] = useState(applicationID);
  const [mode, setMode] = useState<DraftMode>('both');
  const [busy, setBusy] = useState(false);
  const [note, setNote] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  async function run() {
    setBusy(true);
    setError(null);
    setNote(null);
    try {
      const res = await generateDraft({
        brief: caption,
        variables: knownVariables,
        mode,
        application_id: target || null,
      });
      onDraft(res.draft.text);

      const saved = res.saved_variables ?? [];
      if (saved.length > 0) {
        // Registered server-side, so the chips below update and the review can
        // find a value for them instead of refusing to send.
        onVariablesChanged();
        setNote(
          `${res.notice} Variabel baru tersimpan: ${saved.map((v) => v.key).join(', ')}.`,
        );
      } else {
        setNote(res.notice);
      }
    } catch (e) {
      setError(e instanceof ApiError || e instanceof Error ? e.message : 'Terjadi kesalahan.');
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="mt-2 rounded-lg border border-hairline bg-surface px-3.5 py-3">
      <p className="text-xs font-medium text-ink-soft">Caption asli</p>
      <textarea
        className={clsx(textareaClass, 'mt-1.5 h-24')}
        value={caption}
        onChange={(e) => setCaption(e.target.value)}
        placeholder={
          'Tempel caption asli kamu di sini. Contoh:\nHalo Kak, kesempatan terakhir gabung Kelas Intensif TWK 2026! Pakai kode HEMAT25 diskon 25rb, berlaku sampai besok pukul 20.00 WIB.'
        }
        aria-label="Caption asli"
      />

      <p className="mt-3 text-xs font-medium text-ink-soft">
        Aplikasi (simpan variabel ke sini)
      </p>
      <select
        className={clsx(inputClass, 'mt-1.5')}
        value={target}
        onChange={(e) => setTarget(e.target.value)}
        aria-label="Aplikasi tempat variabel disimpan"
      >
        <option value="">Global (semua aplikasi)</option>
        {applications.map((a) => (
          <option key={a.id} value={a.id}>
            {a.name}
          </option>
        ))}
      </select>

      <p className="mt-3 text-xs font-medium text-ink-soft">Mode</p>
      <div className="mt-1.5 grid gap-2 sm:grid-cols-3">
        {AI_MODES.map((m) => {
          const on = mode === m.id;
          const Icon = m.icon;
          return (
            <button
              key={m.id}
              type="button"
              onClick={() => setMode(m.id)}
              aria-pressed={on}
              className={clsx(
                'relative rounded-lg border px-3 py-2.5 text-left transition-colors',
                on
                  ? 'border-brand-700 bg-surface-raised'
                  : 'border-hairline bg-surface-raised hover:bg-surface-sunken',
              )}
            >
              <span
                className={clsx(
                  'grid size-7 place-items-center rounded-lg',
                  on ? 'bg-brand-800 text-white' : 'bg-surface-sunken text-ink-muted',
                )}
              >
                <Icon className="size-3.5" />
              </span>
              <span className="mt-1.5 block text-sm font-medium text-ink">{m.title}</span>
              <span className="block text-2xs text-ink-muted">{m.hint}</span>
              {on ? (
                <Check
                  className="absolute top-2 right-2 size-4 rounded-full bg-brand-800 p-0.5 text-white"
                  strokeWidth={3}
                />
              ) : null}
            </button>
          );
        })}
      </div>

      <button
        type="button"
        onClick={() => void run()}
        disabled={busy || caption.trim() === ''}
        className="mt-3 inline-flex h-10 w-full items-center justify-center gap-2 rounded-lg bg-brand-800 text-sm font-medium text-white transition-colors hover:bg-brand-900 disabled:opacity-50"
      >
        {busy ? <Loader2 className="size-4 animate-spin" /> : <Sparkles className="size-4" />}
        Generate dengan AI
      </button>
      <p className="mt-1.5 text-2xs text-ink-muted">
        Hasil mengisi kolom pesan di bawah — masih bisa diedit.
      </p>

      {note ? (
        <div className="mt-2">
          <Notice>{note}</Notice>
        </div>
      ) : null}
      {error ? (
        <div className="mt-2">
          <Notice tone="danger">{error}</Notice>
        </div>
      ) : null}
    </div>
  );
}
