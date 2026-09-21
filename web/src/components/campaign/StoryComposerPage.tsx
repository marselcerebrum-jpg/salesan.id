'use client';

import clsx from 'clsx';
import { ArrowLeft, Loader2, Paperclip, Send } from 'lucide-react';
import Link from 'next/link';
import { useRouter } from 'next/navigation';
import { useEffect, useMemo, useRef, useState } from 'react';
import useSWR from 'swr';

import {
  WHEN_OPTIONS,
  defaultDelivery,
  scheduledAtOf,
  type When,
} from '@/components/campaign/DeliveryStep';
import { Notice, inputClass } from '@/components/campaign/shared';
import { SenderStep } from '@/components/campaign/SenderStep';
import { StoryPreview } from '@/components/campaign/StoryPreview';
import { Button } from '@/components/ui/Button';
import {
  ApiError,
  campaignSource,
  createCampaign,
  fetcher,
  updateCampaign,
  type CampaignDraft,
} from '@/lib/api';
import type { ComposerMode } from '@/components/campaign/ComposerPage';
import type { Account, Recurrence } from '@/lib/types';

/**
 * Composing a WA Story.
 *
 * Its own page rather than the Broadcast wizard with pieces switched off. A
 * Story has no recipients to resolve, no pacing between sends and no per-person
 * retry, so three quarters of that form was either hidden or inert — and a
 * three-step wizard for six fields makes a small thing feel like a large one.
 *
 * Everything the Broadcast composer does that still applies is reused rather
 * than rewritten: the same departure options, the same conversion to an absolute
 * instant, the same preview renderer.
 */

/** A Story is text over a colour, or a photo or video with words on it. */
type StoryKind = 'media' | 'text';

const RECURRENCES: { id: '' | Recurrence; label: string }[] = [
  { id: '', label: 'Sekali saja' },
  { id: 'daily', label: 'Setiap hari' },
  { id: 'weekly', label: 'Setiap pekan' },
  { id: 'monthly', label: 'Setiap bulan' },
];

export function StoryComposerPage({
  sourceID,
  mode = 'new',
}: {
  /** The story being copied or edited. Read only; nothing is written until save. */
  sourceID?: string;
  mode?: ComposerMode;
}) {
  const router = useRouter();
  const editing = mode === 'edit';

  const [accountIDs, setAccountIDs] = useState<string[]>([]);
  // Held beside the numbers rather than derived from them: the picker enforces
  // one application per campaign, and it is the thing that decides which.
  const [applicationID, setApplicationID] = useState('');
  const [kind, setKind] = useState<StoryKind>('media');
  const [mediaURL, setMediaURL] = useState('');
  const [caption, setCaption] = useState('');
  const [when, setWhen] = useState<When>('now');
  const [customAt, setCustomAt] = useState('');
  const [recurrence, setRecurrence] = useState<'' | Recurrence>('');

  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [showMissing, setShowMissing] = useState(false);

  const { data } = useSWR<{ accounts: Account[] }>('/accounts', fetcher);
  const accounts = useMemo(() => data?.accounts ?? [], [data]);
  const chosen = accounts.filter((a) => accountIDs.includes(a.id));

  const source = useSWR(
    sourceID ? ['campaign-source', sourceID] : null,
    () => campaignSource(sourceID as string),
    { revalidateOnFocus: false },
  );

  // Filled exactly once, so a revalidation cannot throw away what has been typed
  // since the page opened.
  const filled = useRef(false);
  useEffect(() => {
    const s = source.data?.source;
    if (!s || filled.current) return;
    filled.current = true;
    setAccountIDs(s.account_ids);
    setApplicationID(s.application_id ?? '');
    const link = (s.media_url ?? '').trim();
    setKind(link ? 'media' : 'text');
    setMediaURL(link);
    setCaption(s.body);
    setRecurrence(s.recurrence);
    // The departure time is deliberately not carried over: "Pilih waktu" with
    // nothing chosen, so nothing can go out until somebody sets one. A copy that
    // kept "sekarang" would post the moment the button was pressed.
    setWhen('custom');
    setCustomAt('');
  }, [source.data]);

  const scheduledAt = scheduledAtOf({ ...defaultDelivery, when, customAt });
  const scheduled = when !== 'now';

  const missing = useMemo(() => {
    const out: string[] = [];
    if (accountIDs.length === 0) out.push('Pilih minimal satu perangkat pengirim.');
    if (kind === 'media' && mediaURL.trim() === '') out.push('Tempel URL gambar atau videonya.');
    if (kind === 'text' && caption.trim() === '') out.push('Isi teks story-nya.');
    if (when === 'custom' && customAt === '') out.push('Pilih waktu mulainya.');
    return out;
  }, [accountIDs, kind, mediaURL, caption, when, customAt]);

  async function post() {
    if (missing.length > 0) {
      setShowMissing(true);
      return;
    }
    setBusy(true);
    setError(null);
    try {
      // The same row when editing, a new one otherwise.
      const saved =
        editing && sourceID
          ? await updateCampaign(sourceID, buildDraft())
          : await createCampaign(buildDraft());
      router.push(`/story/${saved.campaign.id}`);
    } catch (e) {
      setError(e instanceof ApiError || e instanceof Error ? e.message : 'Terjadi kesalahan.');
      setBusy(false);
    }
  }

  function buildDraft(): CampaignDraft {
    // The recurrence anchor is the departure itself, so "setiap hari" means this
    // same clock time tomorrow rather than some default hour nobody chose.
    const anchor = scheduledAt ? new Date(scheduledAt) : new Date();
    return {
      campaign_type: 'story',
      name: storyName(caption, anchor),
      body: caption,
      compose_mode: 'plain',
      application_id: applicationID || null,
      account_ids: accountIDs,
      // Kind is decided by the server from the bytes it actually fetches, not
      // from the extension in the link, so nothing is sent about it here.
      media_url: kind === 'media' ? mediaURL.trim() || null : null,
      media_storage_path: null,
      media_file_name: null,
      delay_profile: defaultDelivery.profile,
      recurrence,
      recurrence_time: jakartaClock(anchor),
      recurrence_weekday: recurrence === 'weekly' ? anchor.getDay() : undefined,
      recurrence_day: recurrence === 'monthly' ? anchor.getDate() : undefined,
      custom_values: {},
      scheduled_at: scheduledAt,
      run_now: when === 'now',
    };
  }

  return (
    <div className="px-5 py-6 lg:px-8 lg:py-8">
      <div className="mx-auto max-w-[1100px]">
        <div className="flex items-start gap-3">
          <Link
            href="/story"
            aria-label="Kembali"
            className="mt-0.5 rounded-lg p-1 text-ink-muted transition-colors hover:bg-surface-sunken hover:text-ink-soft"
          >
            <ArrowLeft className="size-5" />
          </Link>
          <div>
            <h1 className="text-xl font-semibold tracking-[-0.01em] text-ink">
              {mode === 'edit' ? 'Edit Story' : mode === 'copy' ? 'Duplikat Story' : 'Buat Story'}
            </h1>
            <p className="mt-0.5 text-xs text-ink-muted">
              Story terbit di nomor yang dipilih dan hilang sendiri setelah 24 jam.
            </p>
          </div>
        </div>

        {/* Said once, at the top: everything is filled in except the departure
            time, which has to be chosen again on purpose. */}
        {sourceID ? (
          <div className="mt-4">
            {source.isLoading ? (
              <Notice>Memuat isi story…</Notice>
            ) : source.error ? (
              <Notice tone="danger">Isi story gagal dimuat. Muat ulang halaman ini.</Notice>
            ) : editing ? (
              <Notice tone="warn">
                Mengubah story yang sudah ada. Semua isian sudah terisi, kecuali jadwalnya — pilih
                waktu mulainya lagi sebelum menyimpan. Tidak ada story baru yang dibuat.
              </Notice>
            ) : (
              <Notice>
                Disalin dari story lain. Semua isian sudah terisi, kecuali jadwalnya. Yang asli
                tidak berubah.
              </Notice>
            )}
          </div>
        ) : null}

        <div className="mt-5 flex flex-col gap-5 xl:flex-row xl:items-start">
          <div className="min-w-0 flex-1 rounded-card border border-hairline bg-surface-raised px-5 py-5 shadow-e1">
            {/*
             * The same picker the Broadcast composer uses, not a simpler one of
             * this page's own. Picking numbers is identical work — one
             * application, as many of its numbers as you like — and the rule
             * that a campaign may not span two applications is enforced by the
             * database, so a picker that let you break it would only be refused
             * at the very end.
             */}
            <SenderStep
              accounts={accounts}
              selected={accountIDs}
              onSelected={(next) => {
                setAccountIDs(next);
                setShowMissing(false);
              }}
              applicationID={applicationID}
              onApplication={setApplicationID}
              isStory
            />

            <Field label="Tipe Story" className="mt-5">
              <div className="grid grid-cols-2 gap-2">
                {(
                  [
                    { id: 'media', label: 'Gambar / Video' },
                    { id: 'text', label: 'Teks' },
                  ] as const
                ).map((t) => (
                  <button
                    key={t.id}
                    type="button"
                    onClick={() => setKind(t.id)}
                    aria-pressed={kind === t.id}
                    className={clsx(
                      'h-10 rounded-control border text-sm font-medium transition-colors',
                      kind === t.id
                        ? 'border-brand-800 bg-brand-800 text-white'
                        : 'border-hairline bg-surface-sunken/50 text-ink-soft hover:bg-surface-sunken',
                    )}
                  >
                    {t.label}
                  </button>
                ))}
              </div>
            </Field>

            {kind === 'media' ? (
              <Field label="Media" className="mt-4">
                <span className="relative block">
                  <Paperclip className="absolute top-1/2 left-3 size-4 -translate-y-1/2 text-ink-muted" />
                  <input
                    value={mediaURL}
                    onChange={(e) => {
                      setMediaURL(e.target.value);
                      setShowMissing(false);
                    }}
                    placeholder="Tempel URL media… (https://…)"
                    className={clsx(inputClass, 'pl-9')}
                  />
                </span>
              </Field>
            ) : null}

            <Field
              label={kind === 'media' ? 'Caption (opsional)' : 'Teks Story'}
              className="mt-4"
            >
              <textarea
                value={caption}
                onChange={(e) => {
                  setCaption(e.target.value);
                  setShowMissing(false);
                }}
                rows={4}
                placeholder={kind === 'media' ? 'Caption…' : 'Tulis teks story…'}
                className={clsx(inputClass, 'h-auto resize-y py-2.5')}
              />
            </Field>

            <Field label="Waktu Mulai" className="mt-4">
              <div className="grid gap-2 sm:grid-cols-4">
                {WHEN_OPTIONS.map((o) => (
                  <button
                    key={o.id}
                    type="button"
                    onClick={() => {
                      setWhen(o.id);
                      setShowMissing(false);
                    }}
                    aria-pressed={when === o.id}
                    className={clsx(
                      'h-10 rounded-control border px-2 text-sm transition-colors',
                      when === o.id
                        ? 'border-brand-700 bg-brand-600/10 font-medium text-brand-800'
                        : 'border-hairline bg-surface-raised text-ink-soft hover:bg-surface-sunken',
                    )}
                  >
                    {o.label}
                  </button>
                ))}
              </div>
              {when === 'custom' ? (
                <input
                  type="datetime-local"
                  value={customAt}
                  onChange={(e) => {
                    setCustomAt(e.target.value);
                    setShowMissing(false);
                  }}
                  className={clsx(inputClass, 'mt-2')}
                />
              ) : null}
            </Field>

            <Field label="Pengulangan" className="mt-4">
              <select
                value={recurrence}
                onChange={(e) => setRecurrence(e.target.value as '' | Recurrence)}
                className={inputClass}
              >
                {RECURRENCES.map((r) => (
                  <option key={r.id || 'once'} value={r.id}>
                    {r.label}
                  </option>
                ))}
              </select>
              {recurrence ? (
                <p className="mt-1.5 text-2xs leading-snug text-ink-muted">
                  Tiap pengulangan jadi story tersendiri, supaya laporan penonton hari ini tidak
                  menimpa laporan kemarin.
                </p>
              ) : null}
            </Field>

            {showMissing && missing.length > 0 ? (
              <div className="mt-4">
                <Notice tone="warn">
                  <span className="block font-medium">Masih ada yang belum diisi:</span>
                  <ul className="mt-1 list-disc pl-4">
                    {missing.map((m) => (
                      <li key={m}>{m}</li>
                    ))}
                  </ul>
                </Notice>
              </div>
            ) : null}
            {error ? (
              <div className="mt-4">
                <Notice tone="danger">{error}</Notice>
              </div>
            ) : null}

            <div className="mt-5 flex items-center justify-end gap-2">
              <Button onClick={() => router.push('/story')} disabled={busy}>
                Batal
              </Button>
              {/* The label follows the departure, so the button says what is
                  about to happen rather than a generic verb. */}
              <Button variant="primary" onClick={() => void post()} disabled={busy}>
                {busy ? <Loader2 className="size-3.5 animate-spin" /> : <Send className="size-3.5" />}
                {editing
                  ? scheduled
                    ? 'Simpan & Jadwalkan'
                    : 'Simpan & Posting Sekarang'
                  : scheduled
                    ? 'Jadwalkan Story'
                    : 'Posting Sekarang'}
              </Button>
            </div>
          </div>

          <aside className="w-full shrink-0 xl:sticky xl:top-6 xl:w-[420px]">
            <StoryPreview
              body={caption}
              mediaKind={kind === 'media' ? 'image' : null}
              mediaURL={kind === 'media' ? mediaURL : ''}
              senderName={chosen[0]?.label ?? chosen[0]?.name ?? null}
              variables={[]}
              values={{}}
            />
          </aside>
        </div>
      </div>
    </div>
  );
}

function Field({
  label,
  className,
  children,
}: {
  label: string;
  className?: string;
  children: React.ReactNode;
}) {
  return (
    <label className={clsx('block', className)}>
      <span className="mb-1.5 block text-xs font-medium text-ink-soft">{label}</span>
      {children}
    </label>
  );
}

/**
 * A Story has no label field of its own, so its name is its first line.
 *
 * That is what the list and the report show as the title, and asking for a
 * separate label would mean typing the same words twice. A picture with no
 * caption falls back to when it went out, which is the only other thing that
 * distinguishes it.
 */
function storyName(caption: string, at: Date): string {
  const first = caption.trim().split('\n')[0]?.trim() ?? '';
  if (first) return first.slice(0, 120);
  return `Story ${at.toLocaleString('id-ID', {
    day: 'numeric',
    month: 'short',
    hour: '2-digit',
    minute: '2-digit',
    timeZone: 'Asia/Jakarta',
  })}`;
}

/** "HH:MM" in WIB, so a repeat lands at the same wall clock time. */
function jakartaClock(at: Date): string {
  return at
    .toLocaleTimeString('id-ID', {
      hour: '2-digit',
      minute: '2-digit',
      hour12: false,
      timeZone: 'Asia/Jakarta',
    })
    .replace('.', ':');
}
