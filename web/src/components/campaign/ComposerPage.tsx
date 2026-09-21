'use client';

import { ArrowLeft, Loader2, Send, Users } from 'lucide-react';
import Link from 'next/link';
import { useRouter } from 'next/navigation';
import { useEffect, useMemo, useRef, useState } from 'react';
import useSWR from 'swr';

import {
  DeliveryStep,
  defaultDelivery,
  scheduledAtOf,
  type DeliveryValue,
} from '@/components/campaign/DeliveryStep';
import { MessagePreview } from '@/components/campaign/MessagePreview';
import { MAX_BODY, MessageStep, type MessageValue } from '@/components/campaign/MessageStep';
import {
  RecipientStep,
  emptyRecipients,
  recipientCount,
  type RecipientValue,
} from '@/components/campaign/RecipientStep';
import { SenderStep, type SenderMode } from '@/components/campaign/SenderStep';
import { StoryPreview } from '@/components/campaign/StoryPreview';
import { Field, Notice, StepCard, StepTally, inputClass } from '@/components/campaign/shared';
import {
  ApiError,
  campaignSource,
  createCampaign,
  customVariablesPath,
  fetcher,
  updateCampaign,
  type CampaignDraft,
} from '@/lib/api';
import type {
  Account,
  CampaignSource,
  CampaignType,
  CustomVariable,
  TargetSource,
} from '@/lib/types';

/**
 * Composing a Broadcast or a WA Story, as a page rather than a dialog.
 *
 * A dialog was the wrong container: choosing recipients means reading a list of
 * six hundred group members, and a modal that scrolls inside a page that also
 * scrolls makes that hard to do and impossible to link to. A page has a URL, a
 * back button, and as much room as the list needs.
 *
 * One press sends. There was a review step in front of it that had to be
 * approved before anything was written, and it is gone at the operator's
 * request. The validation it displayed is untouched, because it never lived
 * here: the server resolves the recipients, removes duplicates, checks the
 * template and refuses what it cannot send, all inside the one call this page
 * makes. What is no longer shown is the result of that work before it commits.
 */

const emptyMessage: MessageValue = {
  name: '',
  body: '',
  mediaKind: null,
  mediaURL: '',
  document: null,
};

/**
 * What this page is doing with the campaign it was opened from.
 *
 *   new   nothing to load; an empty form.
 *   copy  filled from another campaign, saved as a new one.
 *   edit  filled from a campaign, saved back onto the same row.
 *
 * Copy and edit load through the same read and fill the same fields. They differ
 * in one place only — which call the button makes — because that is the only
 * place they actually differ.
 */
export type ComposerMode = 'new' | 'copy' | 'edit';

export function ComposerPage({
  type,
  sourceID,
  mode: composerMode = 'new',
}: {
  type: CampaignType;
  sourceID?: string;
  mode?: ComposerMode;
}) {
  const isStory = type === 'story';
  const router = useRouter();
  const home = isStory ? '/story' : '/broadcast';
  const editing = composerMode === 'edit';

  const [mode, setMode] = useState<SenderMode>('qr');
  const [applicationID, setApplicationID] = useState('');
  const [accountIDs, setAccountIDs] = useState<string[]>([]);
  const [recipients, setRecipients] = useState<RecipientValue>(emptyRecipients);
  const [message, setMessage] = useState<MessageValue>(emptyMessage);
  const [delivery, setDelivery] = useState<DeliveryValue>(defaultDelivery);
  const [customValues, setCustomValues] = useState<Record<string, string>>({});

  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const accounts = useSWR<{ accounts: Account[] }>('/accounts', fetcher);
  const vars = useSWR<{ variables: CustomVariable[]; built_in: CustomVariable[] }>(
    customVariablesPath,
    fetcher,
  );

  // The campaign being copied or edited. Read once and poured into the form
  // below; nothing here writes.
  const source = useSWR(
    sourceID ? ['campaign-source', sourceID] : null,
    () => campaignSource(sourceID as string),
    { revalidateOnFocus: false },
  );

  // Filled exactly once. Without the guard a revalidation would throw away
  // whatever had been typed since the page opened.
  const filled = useRef(false);
  useEffect(() => {
    const s = source.data?.source;
    if (!s || filled.current) return;
    filled.current = true;
    fillComposer(s, composerMode, {
      setApplicationID,
      setAccountIDs,
      setMessage,
      setRecipients,
      setDelivery,
      setCustomValues,
    });
  }, [source.data, composerMode]);

  /** Clears a stale error as soon as the thing that caused it is edited. */
  function touched<T>(set: (v: T) => void) {
    return (v: T) => {
      set(v);
      setError(null);
    };
  }

  const draft: CampaignDraft = useMemo(
    () => ({
      campaign_type: type,
      name: message.name.trim(),
      body: message.body,
      compose_mode: modeOf(message.body),
      application_id: applicationID || null,
      account_ids: accountIDs,
      // A document and a link are alternatives, never both: the server would
      // have to pick one, and picking silently is how the wrong file goes out.
      media_url: message.document ? null : message.mediaURL.trim() || null,
      media_storage_path: message.document?.storage_path ?? null,
      media_file_name: message.document?.file_name ?? null,
      delay_profile: delivery.profile,
      // Sent as an explicit range rather than only a profile name, because the
      // two boxes on screen are what the operator actually set; the preset is
      // just a quick way to fill them.
      delay_min_seconds: isStory ? undefined : delivery.minSeconds,
      delay_max_seconds: isStory ? undefined : delivery.maxSeconds,
      auto_retry_on_disconnect: isStory ? undefined : delivery.autoRetry,
      recurrence: isStory || !delivery.recurring ? '' : delivery.frequency,
      recurrence_time: delivery.recurTime,
      recurrence_weekday: delivery.frequency === 'weekly' ? delivery.recurWeekday : undefined,
      recurrence_day: delivery.frequency === 'monthly' ? delivery.recurDay : undefined,
      target_source: isStory ? undefined : recipients.source,
      numbers: isStory ? undefined : recipients.numbers.filter((n) => n.trim() !== ''),
      group_ids: isStory ? undefined : recipients.groupIDs,
      contact_ids: isStory ? undefined : recipients.contactIDs,
      custom_values: customValues,
      scheduled_at: scheduledAtOf(delivery),
      run_now: delivery.when === 'now',
    }),
    [type, message, applicationID, accountIDs, delivery, isStory, recipients, customValues],
  );

  const count = recipientCount(recipients);

  /**
   * What is still missing, in the order the page asks for it.
   *
   * Computed rather than folded into a single boolean, because a disabled
   * button is a dead end: it refuses and says nothing, and the reader is left
   * hunting through four cards for whichever field it disliked. Each entry
   * carries the step it belongs to, so a failed press can take them there.
   */
  const missing = useMemo(() => {
    const out: { anchor: string; text: string }[] = [];
    const messageStep = isStory ? 'langkah-2' : 'langkah-3';

    if (accountIDs.length === 0) {
      out.push({ anchor: 'langkah-1', text: 'Pilih minimal satu nomor pengirim.' });
    }
    if (!isStory && count === 0) {
      out.push({ anchor: 'langkah-2', text: 'Tambahkan minimal satu penerima.' });
    }
    if (message.name.trim() === '') {
      out.push({ anchor: messageStep, text: 'Isi Label Broadcast.' });
    }
    if (
      message.body.trim() === '' &&
      message.mediaURL.trim() === '' &&
      message.document === null
    ) {
      out.push({ anchor: messageStep, text: 'Isi pesan, atau lampirkan media.' });
    }
    if (message.body.length > MAX_BODY) {
      out.push({
        anchor: messageStep,
        text: `Isi pesan melebihi ${MAX_BODY.toLocaleString('id-ID')} karakter.`,
      });
    }
    if (!isStory && (delivery.minSeconds < 1 || delivery.maxSeconds < delivery.minSeconds)) {
      out.push({
        anchor: isStory ? 'langkah-3' : 'langkah-4',
        text: 'Jeda antar pesan belum masuk akal: minimum 1 detik, dan maksimum tidak boleh di bawahnya.',
      });
    }
    if (delivery.when === 'custom' && delivery.customAt === '') {
      out.push({
        anchor: isStory ? 'langkah-3' : 'langkah-4',
        text: 'Pilih waktu mulainya.',
      });
    }
    return out;
  }, [isStory, accountIDs, count, message, delivery]);

  // The step the last failed press stopped on, so that card can say so.
  const [stuckAt, setStuckAt] = useState<string | null>(null);

  /**
   * One press sends.
   *
   * There was a mandatory review step in front of this, and it is gone at the
   * operator's request. Nothing about validation went with it: the server still
   * resolves the recipients, removes duplicates, checks the template and refuses
   * anything it cannot send, and it does all of that inside this one call. What
   * changed is that the result is no longer shown for approval first, so a
   * rejected number now surfaces as an error here instead of a list up there.
   */
  async function send() {
    // Checked on the press, not by greying the button out. The reader gets told
    // what is missing and is taken to it, which is the thing a disabled button
    // cannot do.
    if (missing.length > 0) {
      const first = missing[0].anchor;
      setStuckAt(first);
      setError(null);
      const el = document.getElementById(first);
      el?.scrollIntoView({ behavior: 'smooth', block: 'start' });
      // Focus after the scroll so a keyboard reader lands on the step too, not
      // only a sighted one.
      window.setTimeout(() => el?.focus({ preventScroll: true }), 350);
      return;
    }

    setStuckAt(null);
    setBusy(true);
    setError(null);
    try {
      // The same row when editing, a new one otherwise. This is the whole
      // difference between the two modes.
      if (editing && sourceID) {
        await updateCampaign(sourceID, draft);
      } else {
        await createCampaign(draft);
      }
      router.push(home);
    } catch (e) {
      setError(messageOf(e));
      setBusy(false);
    }
  }

  // The applications this reader can send from, derived from the numbers they
  // may see rather than fetched separately: the two must agree.
  const applications = useMemo(() => {
    const byID = new Map<string, { id: string; name: string }>();
    for (const a of accounts.data?.accounts ?? []) {
      if (!a.application_id || byID.has(a.application_id)) continue;
      byID.set(a.application_id, {
        id: a.application_id,
        name: a.application_name ?? a.application_code ?? 'Aplikasi',
      });
    }
    return [...byID.values()].sort((x, y) => x.name.localeCompare(y.name));
  }, [accounts.data]);

  // Built-ins and the workspace's own, which is what the chips offer.
  const variableList = useMemo(
    () =>
      [...(vars.data?.built_in ?? []), ...(vars.data?.variables ?? [])].filter(
        (v) => v.is_active !== false,
      ),
    [vars.data],
  );

  const senderName = useMemo(() => {
    const chosen = (accounts.data?.accounts ?? []).filter((a) => accountIDs.includes(a.id));
    if (chosen.length === 0) return null;
    if (chosen.length === 1) return chosen[0].name;
    return `${chosen.length} nomor`;
  }, [accounts.data, accountIDs]);

  return (
    <div className="px-5 py-6 lg:px-8 lg:py-8">
      <Link
        href={home}
        className="inline-flex items-center gap-1.5 text-sm text-ink-muted transition-colors hover:text-ink-soft"
      >
        <ArrowLeft className="size-4" />
        Kembali
      </Link>

      <header className="mt-3 max-w-[1176px]">
        <h1 className="text-2xl font-semibold tracking-[-0.02em] text-ink">
          {`${
            composerMode === 'edit' ? 'Edit' : composerMode === 'copy' ? 'Duplikat' : 'Buat'
          } ${isStory ? 'WA Story' : 'Broadcast'}`}
        </h1>
        <p className="mt-1 text-sm text-ink-muted">
          {isStory ? 'Susun pengirim dan isi story.' : 'Susun pengirim, penerima, dan pesan.'}
        </p>
      </header>

      {/* Said once, at the top, because it is the one thing that is different
          about this form: everything is filled in except the departure time,
          which has to be chosen again on purpose. An edit that kept the old
          schedule could fire halfway through being edited. */}
      {sourceID ? (
        <div className="mt-3 max-w-[1256px]">
          {source.isLoading ? (
            <Notice>Memuat isi campaign…</Notice>
          ) : source.error ? (
            <Notice tone="danger">Isi campaign gagal dimuat. Muat ulang halaman ini.</Notice>
          ) : editing ? (
            <Notice tone="warn">
              Mengubah campaign yang sudah ada. Semua isian sudah terisi, kecuali jadwalnya — pilih
              waktu mulainya lagi sebelum menyimpan. Tidak ada campaign baru yang dibuat.
            </Notice>
          ) : (
            <Notice>
              Disalin dari campaign lain. Semua isian sudah terisi, kecuali jadwalnya. Yang asli
              tidak berubah.
            </Notice>
          )}
        </div>
      ) : null}

      {/* Form on the left, the recipient's view on the right. Side by side
          rather than stacked: the thing somebody is trying to judge while they
          type is how the sentence will land in a narrow bubble, and a preview
          below the fold is a preview nobody scrolls to. Below 1280px there is
          not room for two columns, so it stacks and the preview leads. */}
      {/*
       * Capped, not stretched. A form is read one line at a time, and left to
       * fill a 1920px screen its labels end up a hand's width from their own
       * inputs.
       *
       * The cap is the sum of its parts rather than a round number that happens
       * to look right: 760 form + 16 gap + 480 preview = 1256. The gap between
       * the columns is the same 16px that separates one card from the next, so
       * the whole page runs on one spacing value instead of three.
       */}
      <div className="mt-5 flex max-w-[1256px] flex-col-reverse gap-4 xl:flex-row xl:items-start">
        <div className="min-w-0 flex-1 space-y-4 xl:max-w-[760px]">
        <StepCard step={1} title="Pengirim" id="langkah-1" invalid={stuckAt === 'langkah-1'}>
          {(accounts.data?.accounts.length ?? 0) === 0 && !accounts.isLoading ? (
            <Notice tone="warn">
              Belum ada nomor WhatsApp dalam lingkup akun Anda. Hubungkan satu di menu Akun WhatsApp
              lebih dulu.
            </Notice>
          ) : (
            <SenderStep
              accounts={accounts.data?.accounts ?? []}
              mode={mode}
              onMode={setMode}
              selected={accountIDs}
              onSelected={touched(setAccountIDs)}
              applicationID={applicationID}
              onApplication={(next) => {
                setApplicationID(next);
                // Recipients belonged to the previous application's numbers, and
                // keeping them would build exactly the cross-application campaign
                // the boundary exists to prevent.
                if (next !== applicationID) setRecipients(emptyRecipients);
                setError(null);
              }}
            />
          )}
        </StepCard>

        {!isStory ? (
          <StepCard
            step={2}
            title="Penerima"
            id="langkah-2"
            invalid={stuckAt === 'langkah-2'}
            aside={<StepTally icon={Users} value={count} />}
          >
            <RecipientStep
              value={recipients}
              onChange={touched(setRecipients)}
              accountIDs={accountIDs}
              applicationID={applicationID}
            />
          </StepCard>
        ) : null}

        <StepCard
          step={isStory ? 2 : 3}
          title="Pesan"
          id={isStory ? 'langkah-2' : 'langkah-3'}
          invalid={stuckAt === (isStory ? 'langkah-2' : 'langkah-3')}
        >
          <MessageStep
            value={message}
            onChange={touched(setMessage)}
            variables={variableList}
            applications={applications}
            applicationID={applicationID}
            onVariablesChanged={() => void vars.mutate()}
            isStory={isStory}
          />

          {(vars.data?.variables ?? []).length > 0 ? (
            <div className="mt-4">
              <Field
                label="Nilai variabel campaign"
                hint="Berlaku sama untuk seluruh penerima campaign ini."
              >
                <div className="grid gap-2 sm:grid-cols-2">
                  {(vars.data?.variables ?? []).map((v) => (
                    <label key={v.id} className="block">
                      <span className="mb-1 block text-2xs text-ink-muted">
                        {v.label}: <code>{`<<${v.key}>>`}</code>
                      </span>
                      <input
                        className={inputClass}
                        value={customValues[v.key] ?? v.default_value ?? ''}
                        onChange={(e) =>
                          touched(setCustomValues)({ ...customValues, [v.key]: e.target.value })
                        }
                      />
                    </label>
                  ))}
                </div>
              </Field>
            </div>
          ) : null}
        </StepCard>

        <StepCard
          step={isStory ? 3 : 4}
          title="Pengaturan Pengiriman"
          id={isStory ? 'langkah-3' : 'langkah-4'}
          invalid={stuckAt === (isStory ? 'langkah-3' : 'langkah-4')}
        >
          <DeliveryStep value={delivery} onChange={touched(setDelivery)} isStory={isStory} />
        </StepCard>

          {error ? <Notice tone="danger">{error}</Notice> : null}
        </div>

        {/* Sticky, so it stays in view while the form below it is scrolled,
            which is the whole point of putting it beside rather than under. */}
        <aside className="w-full shrink-0 xl:sticky xl:top-6 xl:w-[480px]">
          {isStory ? (
            <StoryPreview
              body={message.body}
              mediaKind={message.mediaKind}
              mediaURL={message.mediaURL}
              senderName={senderName}
              variables={variableList}
              values={customValues}
            />
          ) : (
            <MessagePreview
              body={message.body}
              mediaKind={message.mediaKind}
              mediaURL={message.mediaURL}
              documentName={message.document?.file_name ?? null}
              senderName={senderName}
              variables={variableList}
              values={customValues}
            />
          )}
        </aside>
      </div>

      {/*
       * One button, one press.
       *
       * The review that used to sit in front of this is gone, at the operator's
       * request: it meant clicking twice to do one thing. The validation it ran
       * has not gone anywhere, because it never lived here in the first place;
       * the server does the same resolving, deduplicating and checking inside
       * the call this button makes, and refuses anything it cannot send.
       */}
      {/* What is still missing, listed before the button rather than behind a
          greyed-out one. Only shown after a press: telling somebody the form is
          incomplete while they are still filling it in is nagging, not help. */}
      {stuckAt && missing.length > 0 ? (
        <div className="mt-4 max-w-[1256px] rounded-card border border-danger/30 bg-danger-soft px-4 py-3">
          <p className="text-sm font-medium text-danger">
            {missing.length === 1
              ? 'Satu bagian wajib masih kosong:'
              : `${missing.length} bagian wajib masih kosong:`}
          </p>
          <ul className="mt-1.5 space-y-1">
            {missing.map((m) => (
              <li key={m.text}>
                <button
                  type="button"
                  onClick={() => {
                    setStuckAt(m.anchor);
                    const el = document.getElementById(m.anchor);
                    el?.scrollIntoView({ behavior: 'smooth', block: 'start' });
                    window.setTimeout(() => el?.focus({ preventScroll: true }), 350);
                  }}
                  className="text-left text-xs text-danger underline-offset-2 hover:underline"
                >
                  {m.text}
                </button>
              </li>
            ))}
          </ul>
        </div>
      ) : null}

      <div className="mt-5 flex max-w-[1256px] justify-center">
        <button
          type="button"
          onClick={() => void send()}
          // Disabled only while the request is in flight. An incomplete form is
          // not a reason to refuse the press: the press is how somebody finds
          // out what is incomplete.
          disabled={busy}
          className="inline-flex h-11 items-center gap-2 rounded-control bg-brand-800 px-6 text-sm font-medium text-white transition-colors hover:bg-brand-900 disabled:cursor-not-allowed disabled:opacity-50"
        >
          {busy ? <Loader2 className="size-4 animate-spin" /> : <Send className="size-4" />}
          {editing
            ? delivery.when === 'now'
              ? 'Simpan & Kirim Sekarang'
              : 'Simpan & Jadwalkan'
            : delivery.when === 'now'
              ? 'Kirim Sekarang'
              : 'Jadwalkan Broadcast'}
        </button>
      </div>

      {/* What one press will do, said before it is pressed. There is no second
          press to correct course on any more. */}
      <p className="mt-2 max-w-[1256px] text-center text-2xs text-ink-muted">
        {isStory
          ? 'Story diterbitkan di nomor yang dipilih.'
          : delivery.when === 'now'
            ? `Pesan langsung dikirim ke ${count.toLocaleString('id-ID')} penerima begitu tombol ditekan.`
            : `${count.toLocaleString('id-ID')} penerima, dikirim sesuai jadwal di langkah ${isStory ? 3 : 4}.`}
      </p>
    </div>
  );
}

/* --- helpers --------------------------------------------------------------- */

/**
 * Pours a saved campaign back into the form.
 *
 * One thing is deliberately not carried over: the departure time. It comes back
 * as "Pilih waktu" with nothing chosen, so the form cannot be saved until
 * somebody sets one. Copying a campaign that said "kirim sekarang" and keeping
 * that setting would mean a press of the save button sends immediately to a list
 * the reader has not looked at yet; keeping an edited campaign's old schedule
 * would let it fire halfway through the edit.
 */
function fillComposer(
  s: CampaignSource,
  composerMode: ComposerMode,
  set: {
    setApplicationID: (v: string) => void;
    setAccountIDs: (v: string[]) => void;
    setMessage: (v: MessageValue) => void;
    setRecipients: (v: RecipientValue) => void;
    setDelivery: (v: DeliveryValue) => void;
    setCustomValues: (v: Record<string, string>) => void;
  },
) {
  set.setApplicationID(s.application_id ?? '');
  set.setAccountIDs(s.account_ids);

  set.setMessage({
    // A copy says so in its name. Without it the list shows two entries with
    // one name and no way to tell which is which.
    name: composerMode === 'copy' ? `${s.name} (salinan)`.slice(0, 120) : s.name,
    body: s.body,
    mediaKind: (s.media_kind as MessageValue['mediaKind']) ?? null,
    mediaURL: s.media_url ?? '',
    document: s.media_storage_path
      ? {
          storage_path: s.media_storage_path,
          file_name: s.media_file_name ?? 'dokumen',
          mime: s.media_mime ?? 'application/octet-stream',
          size: s.media_size_bytes ?? 0,
        }
      : null,
  });

  set.setRecipients({
    source: (s.target_source || 'manual') as TargetSource,
    // The textarea needs one empty line to type into when there is nothing.
    numbers: s.numbers.length > 0 ? s.numbers : [''],
    contactIDs: s.contact_ids,
    groupIDs: s.group_ids,
  });

  set.setDelivery({
    ...defaultDelivery,
    profile: s.delay_profile,
    minSeconds: s.delay_min_seconds ?? defaultDelivery.minSeconds,
    maxSeconds: s.delay_max_seconds ?? defaultDelivery.maxSeconds,
    autoRetry: s.auto_retry_on_disconnect,
    recurring: s.recurrence !== '',
    frequency: s.recurrence || defaultDelivery.frequency,
    recurTime: s.recurrence_time || defaultDelivery.recurTime,
    recurWeekday: s.recurrence_weekday ?? defaultDelivery.recurWeekday,
    recurDay: s.recurrence_day ?? defaultDelivery.recurDay,
    when: 'custom',
    customAt: '',
  });

  set.setCustomValues(s.custom_values);
}

/** What the template uses, so the campaign records how it was written. */
function modeOf(body: string): CampaignDraft['compose_mode'] {
  // Both spellings count. The composer writes <<nama>>; campaigns saved before
  // it changed carry {{nama}}, and the server still renders either.
  const hasVars = /<<\s*[a-z0-9_]+/i.test(body) || /\{\{\s*[a-z0-9_]+/i.test(body);
  const hasSpin = /\{[^{}]*\|[^{}]*\}/.test(
    body.replace(/\{\{[^}]*\}\}/g, '').replace(/<<[^<>]*>>/g, ''),
  );
  if (hasVars && hasSpin) return 'spintax_variables';
  if (hasVars) return 'variables';
  if (hasSpin) return 'spintax';
  return 'plain';
}


function messageOf(e: unknown): string {
  if (e instanceof ApiError) return e.message;
  if (e instanceof Error) return e.message;
  return 'Terjadi kesalahan.';
}
