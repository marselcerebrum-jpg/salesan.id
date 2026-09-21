'use client';

import clsx from 'clsx';
import {
  Activity,
  ArrowLeft,
  CalendarClock,
  Copy,
  Eye,
  FileText,
  Image as ImageIcon,
  Loader2,
  Pencil,
  Plus,
  Send,
  Smartphone,
  Trash2,
  Users,
} from 'lucide-react';
import Link from 'next/link';
import { useRouter } from 'next/navigation';
import { useState } from 'react';
import useSWR from 'swr';

import { EmptyState, ErrorState, InfoTip, StateBadge } from '@/components/analytics/Primitives';
import {
  Button,
  CampaignStatusBadge,
  Notice,
  PUBLICATION_STATUS,
} from '@/components/campaign/shared';
import { StoryThumbnail } from '@/components/campaign/StoryGrid';
import {
  ConfirmDialog,
  useConfirm,
  type ConfirmRequest,
} from '@/components/ui/ConfirmDialog';
import {
  ApiError,
  campaignReportPath,
  cancelCampaign,
  deleteCampaign,
  fetcher,
  revokeStoryPublication,
  runCampaign,
  scheduleCampaign,
} from '@/lib/api';
import { useRealtimeEvent } from '@/lib/realtime';
import type { StoryReport } from '@/lib/types';

/**
 * One Story's page.
 *
 * Its own page rather than the Broadcast report with columns hidden. The two
 * answer different questions: a broadcast asks "who received it", a Story asks
 * "which of my numbers is it live on, and who watched". There is no recipient
 * list here and no delivery log, because neither exists — WhatsApp decides a
 * Story's audience from each account's own privacy settings.
 */
export function StoryDetailPage({ id }: { id: string }) {
  const router = useRouter();
  const confirm = useConfirm();

  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [rescheduling, setRescheduling] = useState(false);
  const [newAt, setNewAt] = useState('');

  const report = useSWR<{ story?: StoryReport; views_notice?: string }>(
    campaignReportPath(id),
    fetcher,
    {
      /*
       * Fast while it is still going somewhere, slow once it has settled.
       *
       * Publishing is per number: the first phone can be showing the status
       * while the last one has not started, and a fifteen-second poll on a
       * settled report was also the delay on a live one. The realtime event
       * still does the real work; this is the floor under it.
       */
      refreshInterval: (latest) =>
        latest?.story && inFlight(latest.story.campaign.status) ? 3_000 : 30_000,
    },
  );

  useRealtimeEvent('campaign.updated', () => void report.mutate());

  const s = report.data?.story;
  const c = s?.campaign;

  if (report.error) {
    return (
      <div className="px-5 py-6 lg:px-8 lg:py-8">
        <ErrorState
          message={report.error instanceof Error ? report.error.message : 'Gagal memuat.'}
          onRetry={() => void report.mutate()}
        />
      </div>
    );
  }
  if (!s || !c) {
    return (
      <div className="px-5 py-6 lg:px-8 lg:py-8">
        <p className="flex items-center gap-2 text-sm text-ink-muted">
          <Loader2 className="size-4 animate-spin" />
          Memuat story…
        </p>
      </div>
    );
  }

  const caption = (c.caption ?? c.message_template ?? c.body ?? '').trim();
  const scheduled = c.status === 'scheduled';
  const runnable = ['draft', 'cancelled', 'failed', 'partial'].includes(c.status);
  const cancellable = ['draft', 'scheduled', 'running'].includes(c.status);
  // Editable until it starts. Once a number has posted it, what it said is on
  // somebody's phone, and the answer to "I want it different" is Duplikat.
  const editable =
    ['draft', 'scheduled'].includes(c.status) && !c.executed_at && !c.started_at;
  /*
   * Published, counted from the publications rather than from the campaign's
   * own success_count.
   *
   * success_count is only written when the whole campaign settles, so a Story
   * going out across seven numbers read "0/7" on the web while the first phones
   * were already showing it. The publication rows are stamped one at a time, as
   * each number actually publishes, which is the thing being asked about.
   */
  // 'deleted' counts too: it did go out, and was taken down afterwards.
  const published = s.publications.filter((p) =>
    ['published', 'expired', 'deleted'].includes(p.status),
  ).length;

  async function act(kind: string, run: () => Promise<unknown>) {
    setBusy(kind);
    setError(null);
    try {
      await run();
      await report.mutate();
    } catch (e) {
      setError(e instanceof ApiError || e instanceof Error ? e.message : 'Terjadi kesalahan.');
    } finally {
      setBusy(null);
    }
  }

  /**
   * Delete, and mean it.
   *
   * There is no archive for a Story. A Story lives 24 hours and then stops
   * existing on WhatsApp too, so there is no long-lived record to preserve the
   * way a broadcast's recipient list is preserved — hiding one from the list
   * would only leave a row nobody can see and nobody can remove.
   *
   * The dialog says exactly what goes, including the one thing that does not:
   * a Story already published cannot be pulled back off anybody's phone.
   */
  const askDelete = () =>
    confirm.ask({
      title: `Hapus ${c.name}?`,
      description: (
        <>
          Story ini dihapus permanen beserta rincian per nomor dan angka penontonnya. Tidak bisa
          dikembalikan.
          {published ? (
            <>
              {' '}
              <span className="font-medium text-ink">
                Yang sudah tayang tetap ada di HP sampai 24 jamnya habis
              </span>{' '}
              — WhatsApp tidak bisa menariknya kembali dari sini.
            </>
          ) : null}
        </>
      ),
      confirmLabel: 'Hapus',
      tone: 'danger',
      icon: Trash2,
      onConfirm: async () => {
        setBusy('delete');
        try {
          await deleteCampaign(id);
          router.push('/story');
        } catch (e) {
          setError(e instanceof ApiError || e instanceof Error ? e.message : 'Terjadi kesalahan.');
          setBusy(null);
        }
      },
    });

  return (
    <div className="px-5 py-6 lg:px-8 lg:py-8">
      <Link
        href="/story"
        className="inline-flex items-center gap-1.5 text-xs text-ink-muted transition-colors hover:text-ink"
      >
        <ArrowLeft className="size-4" />
        Kembali
      </Link>

      <div className="mt-3 flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          <h1 className="text-2xl leading-snug font-semibold tracking-[-0.02em] text-ink">
            {c.name}
          </h1>
          <p className="mt-1.5">
            <CampaignStatusBadge status={c.status} isStory />
          </p>
        </div>

        <div className="flex flex-wrap items-center gap-2">
          {runnable ? (
            <Button variant="primary" onClick={() => void act('run', () => runCampaign(id))} disabled={busy !== null}>
              {busy === 'run' ? <Loader2 className="size-3.5 animate-spin" /> : <Send className="size-3.5" />}
              Posting sekarang
            </Button>
          ) : null}
          {/*
           * Edit re-opens this story in the composer with everything filled in,
           * and saves back onto the same row. It used to say "Edit Jadwal" and
           * only move the clock, because there was no way to rewrite the words
           * or swap the picture; there is now.
           *
           * "Edit Jadwal" stays beside it for the one job it does better: moving
           * a scheduled story without opening a form.
           */}
          {editable ? (
            <Button onClick={() => router.push(`/story/baru?edit=${id}`)} disabled={busy !== null}>
              <Pencil className="size-3.5" />
              Edit
            </Button>
          ) : null}
          {scheduled ? (
            <Button onClick={() => setRescheduling((v) => !v)} disabled={busy !== null}>
              <CalendarClock className="size-3.5" />
              Edit Jadwal
            </Button>
          ) : null}
          {cancellable ? (
            <Button variant="danger" onClick={() => void act('cancel', () => cancelCampaign(id))} disabled={busy !== null}>
              {busy === 'cancel' ? <Loader2 className="size-3.5 animate-spin" /> : null}
              Batalkan
            </Button>
          ) : null}
          <Button onClick={() => router.push(`/story/baru?dari=${id}`)} disabled={busy !== null}>
            <Copy className="size-3.5" />
            Duplikat
          </Button>
          <Button variant="danger" onClick={askDelete} disabled={busy !== null}>
            {busy === 'delete' ? (
              <Loader2 className="size-3.5 animate-spin" />
            ) : (
              <Trash2 className="size-3.5" />
            )}
            Hapus
          </Button>
        </div>
      </div>

      {rescheduling ? (
        <div className="mt-3 flex flex-wrap items-end gap-2 rounded-card border border-hairline bg-surface-raised px-4 py-3 shadow-e1">
          <label className="block">
            <span className="mb-1 block text-2xs text-ink-muted">Jadwal baru</span>
            <input
              type="datetime-local"
              value={newAt}
              onChange={(e) => setNewAt(e.target.value)}
              className="h-9 rounded-control border border-hairline bg-surface-raised px-3 text-sm text-ink outline-none"
            />
          </label>
          <Button
            variant="primary"
            disabled={busy !== null || newAt === ''}
            onClick={() =>
              void act('schedule', async () => {
                await scheduleCampaign(id, new Date(newAt).toISOString());
                setRescheduling(false);
              })
            }
          >
            {busy === 'schedule' ? <Loader2 className="size-3.5 animate-spin" /> : null}
            Simpan jadwal
          </Button>
        </div>
      ) : null}

      {error ? (
        <div className="mt-3">
          <Notice tone="danger">{error}</Notice>
        </div>
      ) : null}

      {/*
       * A grid rather than a flex row, so the two cards are exactly as tall as
       * each other.
       *
       * The picture is absolutely positioned inside its card, which is the part
       * that matters: an image in normal flow sets the card's height from its
       * own aspect ratio, and a portrait Story beside eight short rows of text
       * left one card overshooting the other by a couple of hundred pixels. Out
       * of flow, the picture contributes no height at all — the row is measured
       * by the details beside it, and the image fits itself into whatever that
       * turns out to be. object-contain, so fitting never means cropping.
       */}
      <div className="mt-4 grid gap-4 lg:grid-cols-[300px_minmax(0,1fr)]">
        <div className="relative min-h-[320px] overflow-hidden rounded-card border border-hairline bg-surface-raised p-3 shadow-e1">
          <div className="absolute inset-3">
            <StoryThumbnail
              url={(c.media_url ?? '').trim()}
              kind={c.media_kind}
              caption={caption}
              className="size-full rounded-lg"
              contain
            />
          </div>
        </div>

        <section className="min-w-0 rounded-card border border-hairline bg-surface-raised px-5 py-4 shadow-e1">
          <h2 className="mb-1 text-sm font-semibold text-ink">Informasi Story</h2>
          <dl>
            <Row label="Status" icon={Activity}>
              <CampaignStatusBadge status={c.status} isStory />
            </Row>
            <Row label="Perangkat" icon={Smartphone}>
              {c.device_names.length > 0 ? c.device_names.join(', ') : '—'}
            </Row>
            <Row label="Tipe" icon={ImageIcon}>
              {typeLabel(c.media_kind)}
            </Row>
            <Row label="Caption" icon={FileText}>
              {caption ? (
                <span className="whitespace-pre-wrap">{caption}</span>
              ) : (
                <span className="text-ink-muted italic">(tanpa caption)</span>
              )}
            </Row>
            <Row label="Dijadwalkan" icon={CalendarClock}>
              {c.scheduled_at ? formatWhen(c.scheduled_at) : '—'}
            </Row>
            <Row label="Terposting" icon={Send}>
              <span className="nums">
                {published}/{c.device_count}
              </span>
            </Row>
            <Row label="Dibuat" icon={Plus}>
              {formatWhen(c.created_at)}
            </Row>
            <Row label="Diupdate" icon={Pencil} last>
              {formatWhen(c.updated_at)}
            </Row>
          </dl>
        </section>
      </div>

      <StoryOutcome
        report={s}
        notice={report.data?.views_notice}
        ask={confirm.ask}
        busy={busy}
        onRevoke={(publicationId) =>
          void act(`revoke:${publicationId}`, () => revokeStoryPublication(id, publicationId))
        }
      />

      <ConfirmDialog request={confirm.request} onClose={confirm.close} />
    </div>
  );
}

function Row({
  label,
  icon: Icon,
  children,
  last,
}: {
  label: string;
  /** A mark for the eye to find the line by, not decoration. */
  icon: typeof Smartphone;
  children: React.ReactNode;
  last?: boolean;
}) {
  return (
    <div
      className={clsx(
        'grid gap-0.5 py-2.5 sm:grid-cols-[170px_1fr] sm:gap-3',
        !last && 'border-b border-hairline',
      )}
    >
      <dt className="flex items-center gap-2 text-xs text-ink-muted">
        <Icon className="size-3.5 shrink-0" aria-hidden />
        {label}
      </dt>
      <dd className="min-w-0 text-sm break-words text-ink">{children}</dd>
    </div>
  );
}

/** Still going somewhere, so the report is worth re-reading often. */
function inFlight(status: string): boolean {
  return status === 'scheduled' || status === 'running' || status === 'draft';
}

function typeLabel(kind: string | null): string {
  if (kind === 'image') return 'Gambar';
  if (kind === 'video') return 'Video';
  return 'Teks';
}

/* --- outcome ---------------------------------------------------------------- */

/** How one number's publication is doing, in the words the report uses. */
/**
 * One row per number it was published from, and who watched.
 *
 * Per number rather than as one total because that is the unit that exists: a
 * Story is published on each device separately, each gets its own WhatsApp
 * message id, and its viewers are counted against that id alone. Collapsing them
 * would hide the case this is most often opened for — one number published and
 * another did not.
 *
 * The two view figures are deliberately two figures with two names. "Per nomor"
 * sums the publications, so somebody who watched on two of our numbers counts
 * twice, and both of those are real events. "Penonton unik" deduplicates across
 * the campaign. A single number labelled "views" would be read as whichever of
 * the two the reader assumed.
 *
 * Every figure here is a lower bound, and the notice from the server says so
 * next to them. WhatsApp gives a linked device no viewer list: the only evidence
 * is a read receipt, and a viewer with read receipts switched off never sends
 * one. Nothing here estimates the gap.
 */
function StoryOutcome({
  report,
  notice,
  ask,
  busy,
  onRevoke,
}: {
  report: StoryReport;
  notice?: string;
  ask: (request: ConfirmRequest) => void;
  /** Which action the page is currently running, so one row's spinner is its own. */
  busy: string | null;
  onRevoke: (publicationId: string) => void;
}) {
  const pubs = report.publications;

  /**
   * Ask before pulling a Story off the Status display.
   *
   * Worth a dialog because it is not undoable and not partial: the Story goes
   * from everybody's status list at once, and reposting it would be a new Story
   * with a new 24 hours and a viewer count starting at zero. The dialog also
   * says what stays, because the number in the "Penonton" column does not
   * disappear with it.
   */
  const askRevoke = (p: StoryReport['publications'][number]) =>
    ask({
      title: `Hapus story dari ${p.account_name}?`,
      description: (
        <>
          Story ini langsung hilang dari tampilan status WhatsApp nomor tersebut dan tidak bisa
          dikembalikan. Nomor lain tidak terpengaruh, dan angka penonton yang sudah terkumpul tetap
          tersimpan di laporan ini.
        </>
      ),
      confirmLabel: 'Hapus dari status',
      tone: 'danger',
      icon: Trash2,
      onConfirm: () => onRevoke(p.id),
    });

  return (
    <>
      <section className="mt-4 rounded-card border border-hairline bg-surface-raised px-5 py-4 shadow-e1">
        <p className="flex items-center gap-1.5 text-sm font-semibold text-ink">
          Penonton Terdeteksi
          <InfoTip text="Dihitung dari receipt yang benar-benar diterima sistem. Ini batas bawah, bukan jumlah pasti penonton: WhatsApp tidak memberi daftar penonton." />
        </p>

        {report.views_available ? (
          <div className="mt-3 grid gap-3 sm:grid-cols-2">
            <Figure
              icon={Eye}
              label="Views per nomor"
              hint="Total penonton yang terdeteksi"
              value={report.views_per_device}
            />
            {report.unique_viewers_known ? (
              <Figure
                icon={Users}
                label="Penonton unik"
                hint="Orang berbeda yang menonton"
                value={report.unique_viewers}
              />
            ) : (
              <div className="flex items-start gap-3 rounded-card border border-hairline bg-surface-sunken/40 px-4 py-3">
                <span className="grid size-9 shrink-0 place-items-center rounded-control bg-surface-raised text-ink-muted">
                  <Users className="size-4" />
                </span>
                <div className="min-w-0">
                  <p className="text-2xs text-ink-muted">Penonton unik</p>
                  <p className="nums text-2xl leading-tight font-semibold text-ink-muted">–</p>
                  <p className="mt-0.5 text-2xs leading-relaxed text-ink-muted">
                    Tidak tersedia lagi. Setelah 24 jam hanya angka per nomor yang disimpan, jadi
                    jumlah orang yang berbeda tidak bisa dihitung ulang.
                  </p>
                </div>
              </div>
            )}
          </div>
        ) : (
          <p className="mt-2 text-xs text-ink-muted">
            Data penonton belum tersedia. Story ini belum tayang di nomor mana pun, jadi belum ada
            yang bisa dihitung.
          </p>
        )}

        {notice ? (
          <div className="mt-3">
            <Notice tone="warn">{notice}</Notice>
          </div>
        ) : null}
      </section>

      <section className="mt-4 overflow-hidden rounded-card border border-hairline bg-surface-raised shadow-e1">
        <p className="px-5 py-3.5 text-sm font-semibold text-ink">Rincian Per Nomor</p>
        {pubs.length === 0 ? (
          <div className="p-6">
            <EmptyState title="Belum ada nomor yang memproses story ini." />
          </div>
        ) : (
          <div className="max-h-[70vh] overflow-auto">
            <table className="w-full min-w-[900px] border-collapse text-sm">
              <thead>
                <tr className="border-b border-hairline text-ink-muted">
                  <Th>Nomor</Th>
                  <Th className="w-32">Status</Th>
                  <Th className="w-40">Tayang</Th>
                  <Th className="w-40">Kedaluwarsa</Th>
                  <Th className="w-32">Penonton</Th>
                  <Th>Catatan</Th>
                  <Th className="w-[150px]">Aksi</Th>
                </tr>
              </thead>
              <tbody>
                {pubs.map((p) => {
                  const st = PUBLICATION_STATUS[p.status] ?? {
                    label: p.status,
                    tone: 'neutral' as const,
                  };
                  // Once a Story has expired its tally is frozen: a late receipt
                  // must not keep moving a number for something nobody can watch
                  // any more.
                  const frozen = p.final_views !== null;
                  return (
                    <tr key={p.id} className="border-b border-hairline last:border-0">
                      <Td>
                        <span className="block font-medium text-ink">{p.account_name}</span>
                        <span className="nums block text-xs text-ink-muted">
                          {p.phone_number ?? '—'}
                        </span>
                      </Td>
                      <Td>
                        <StateBadge label={st.label} tone={st.tone} />
                      </Td>
                      <Td className="text-xs text-ink-soft">
                        {p.published_at ? formatWhen(p.published_at) : '—'}
                      </Td>
                      <Td className="text-xs text-ink-soft">
                        {p.expires_at ? formatWhen(p.expires_at) : '—'}
                      </Td>
                      <Td>
                        <span className="nums font-semibold text-ink">
                          {(p.final_views ?? p.detected_views).toLocaleString('id-ID')}
                        </span>
                        {frozen ? <span className="ml-1 text-2xs text-ink-muted">final</span> : null}
                      </Td>
                      <Td className="text-xs text-ink-muted">
                        {p.failure_reason ? (
                          <span className="text-danger">{p.failure_reason}</span>
                        ) : (
                          <span className="nums break-all">{p.wa_message_id ?? '—'}</span>
                        )}
                        {p.attempt > 1 ? <span className="ml-1">· percobaan {p.attempt}</span> : null}
                      </Td>
                      {/*
                       * Only while it is actually on somebody's status. A row
                       * that failed, expired or was already taken down has
                       * nothing left to remove, and WhatsApp would refuse the
                       * request — so the button is absent rather than present
                       * and broken.
                       */}
                      <Td>
                        {p.status === 'published' ? (
                          <button
                            type="button"
                            onClick={() => askRevoke(p)}
                            disabled={busy !== null}
                            className="inline-flex h-8 items-center gap-1.5 rounded-control border border-hairline px-2.5 text-xs font-medium text-danger transition-colors hover:bg-danger-soft disabled:opacity-60"
                          >
                            {busy === `revoke:${p.id}` ? (
                              <Loader2 className="size-3.5 animate-spin" />
                            ) : (
                              <Trash2 className="size-3.5" />
                            )}
                            Hapus status
                          </button>
                        ) : (
                          <span className="text-2xs text-ink-muted">—</span>
                        )}
                      </Td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}
      </section>
    </>
  );
}

function Figure({
  icon: Icon,
  label,
  hint,
  value,
}: {
  icon: typeof Eye;
  label: string;
  hint: string;
  value: number;
}) {
  return (
    <div className="flex items-start gap-3 rounded-card border border-hairline bg-surface-sunken/40 px-4 py-3">
      <span className="grid size-9 shrink-0 place-items-center rounded-control bg-surface-raised text-info">
        <Icon className="size-4" />
      </span>
      <div className="min-w-0">
        <p className="text-2xs text-ink-muted">{label}</p>
        <p className="nums text-2xl leading-tight font-semibold text-ink">
          {value.toLocaleString('id-ID')}
        </p>
        <p className="mt-0.5 text-2xs text-ink-muted">{hint}</p>
      </div>
    </div>
  );
}

function Th({ children, className }: { children?: React.ReactNode; className?: string }) {
  return (
    <th
      scope="col"
      // Pinned to the top of the scroll box. The border lives on the cell
      // because a border on the row does not travel with a sticky cell.
      className={clsx(
        'sticky top-0 z-10 h-[40px] border-b border-hairline bg-surface-raised px-4 text-left text-2xs font-semibold tracking-wide uppercase',
        className,
      )}
    >
      {children}
    </th>
  );
}

function Td({ children, className }: { children?: React.ReactNode; className?: string }) {
  return <td className={clsx('px-4 py-2.5 align-middle', className)}>{children}</td>;
}

function formatWhen(iso: string): string {
  return new Date(iso).toLocaleString('id-ID', {
    day: 'numeric',
    month: 'short',
    year: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
    timeZone: 'Asia/Jakarta',
  });
}
