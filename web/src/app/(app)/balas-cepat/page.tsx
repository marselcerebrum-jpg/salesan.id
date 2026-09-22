'use client';

import clsx from 'clsx';
import {
  BarChart3,
  Bold,
  CheckCheck,
  Download,
  Image as ImageIcon,
  Italic,
  LayoutGrid,
  Link as LinkIcon,
  List,
  MoreVertical,
  Pencil,
  Plus,
  Search,
  Strikethrough,
  Trash2,
  Upload,
  Zap,
} from 'lucide-react';
import { useEffect, useMemo, useRef, useState } from 'react';
import useSWR from 'swr';

import { AppMark } from '@/components/analytics/AppBadge';
import { EmptyState, ErrorState, RowSkeleton } from '@/components/analytics/Primitives';
import {
  AppFilter,
  SettingsPageShell,
  filterByApp,
  useAppFilter,
  useApplications,
} from '@/components/settings/AppScoped';
import { Button } from '@/components/ui/Button';
import { ConfirmDialog } from '@/components/ui/ConfirmDialog';
import { Modal } from '@/components/ui/Modal';
import { inputClass, labelClass, textareaClass } from '@/components/ui/control';
import {
  deleteAllQuickReplies,
  deleteQuickReply,
  exportQuickReplies,
  fetcher,
  importQuickReplies,
  quickRepliesPath,
  upsertQuickReply,
  type QuickReplyImportResult,
} from '@/lib/api';
import type { AnalyticsScope, Application, QuickReply } from '@/lib/types';

/**
 * Balas Cepat.
 *
 * Canned answers an operator drops into a chat by typing a slash. The whole
 * value is speed at the keyboard, so the shortcut is the thing that matters
 * most and is shown first on every row.
 *
 * Scoped per application for the same reason everything else here is: a reply
 * written in one brand's voice, pasted into another brand's chat, is a mistake
 * that reaches a customer. A reply with no application is the company's own and
 * available everywhere, which only a Leader can create. Replies for one
 * application are kept by whoever holds it: Leader, PIC or Freelance alike.
 */
export default function QuickRepliesPage() {
  const { data, error, isLoading, mutate } = useSWR<{ quick_replies: QuickReply[] }>(
    quickRepliesPath,
    fetcher,
  );
  const org = useSWR<{ scope: AnalyticsScope }>('/org/members', fetcher);
  const applications = useApplications();

  const [filter, setFilter] = useAppFilter();
  const [term, setTerm] = useState('');
  const [sort, setSort] = useState<SortKey>('terbaru');
  const [view, setView] = useState<'list' | 'grid'>('list');
  const [composing, setComposing] = useState(false);
  const [editing, setEditing] = useState<QuickReply | null>(null);
  const [pending, setPending] = useState<QuickReply | null>(null);
  const [failure, setFailure] = useState<string | null>(null);
  const [exporting, setExporting] = useState(false);
  const [importing, setImporting] = useState(false);
  const [report, setReport] = useState<QuickReplyImportResult | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [wiping, setWiping] = useState<{
    title: string;
    count: number;
    applicationId: string | null;
    scoped: boolean;
  } | null>(null);
  const fileRef = useRef<HTMLInputElement>(null);
  const searchRef = useRef<HTMLInputElement>(null);

  // Ctrl+K puts the caret in the box. This page is a list of a hundred and
  // fifty things whose only job is to be found quickly.
  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'k') {
        e.preventDefault();
        searchRef.current?.focus();
        searchRef.current?.select();
      }
    }
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, []);

  async function runExport() {
    setFailure(null);
    setExporting(true);
    try {
      await exportQuickReplies();
    } catch (e) {
      setFailure(e instanceof Error ? e.message : 'Gagal mengunduh.');
    } finally {
      setExporting(false);
    }
  }

  async function runImport(file: File) {
    setFailure(null);
    setReport(null);
    setImporting(true);
    try {
      const result = await importQuickReplies(file);
      setReport(result);
      void mutate();
    } catch (e) {
      setFailure(e instanceof Error ? e.message : 'Gagal membaca berkas.');
    } finally {
      setImporting(false);
    }
  }

  const role = org.data?.scope.role ?? '';
  // Every operational role keeps its own replies: a Freelance edits those of
  // the applications assigned to them, a PIC those of their brands, a Leader
  // everything. Only a workspace-wide reply ("semua aplikasi") is a Leader's.
  // Until /org/members answers, `role` is '' and the buttons show; the server
  // still decides, so a wrong guess costs one refused click, not a leak.
  const canEdit = org.data ? Boolean(org.data.scope.is_leader || role) : true;
  const isLeader = Boolean(org.data?.scope.is_leader) || role === 'leader' || role === '';

  // Memoised so the list below it is not rebuilt on every keystroke elsewhere
  // on the page: `?? []` is a fresh array each render otherwise.
  const rows = useMemo(() => data?.quick_replies ?? [], [data]);

  /*
   * Application, then words, then order.
   *
   * Searching covers the shortcut and the message because people remember one
   * or the other: "the one about the UTBK package" and "/mau_ambil_paket_utbk"
   * are the same reply reached from two different memories.
   */
  const visible = useMemo(() => {
    const q = term.trim().toLowerCase();
    const scoped = filterByApp(rows, filter);
    const found = q
      ? scoped.filter(
          (r) =>
            r.shortcut.toLowerCase().includes(q) ||
            r.body.toLowerCase().includes(q) ||
            (r.category ?? '').toLowerCase().includes(q),
        )
      : scoped;
    return [...found].sort(SORTS[sort]);
  }, [rows, filter, term, sort]);

  /*
   * What "hapus semua" will actually reach.
   *
   * Narrowed to one application only when the filter names a real one. The
   * "semua aplikasi" and "tanpa aplikasi" filters are views, not scopes — the
   * server has no way to express the second — so in those cases the wipe is
   * everything the caller may delete, and the dialog says so rather than
   * implying it follows the filter.
   */
  const pickedApp = applications.find((a) => a.id === filter) ?? null;
  const wipeCount = pickedApp ? filterByApp(rows, filter).length : rows.length;

  const askWipe = () =>
    setWiping({
      title: pickedApp ? `Hapus semua balas cepat ${pickedApp.name}?` : 'Hapus semua balas cepat?',
      count: wipeCount,
      applicationId: pickedApp?.id ?? null,
      scoped: Boolean(pickedApp),
    });

  async function wipe(applicationId: string | null) {
    setFailure(null);
    try {
      const result = await deleteAllQuickReplies(applicationId);
      setWiping(null);
      setReport(null);
      void mutate();
      setNotice(`${result.deleted} balas cepat dihapus.`);
    } catch (e) {
      setFailure(e instanceof Error ? e.message : 'Gagal menghapus balas cepat.');
    }
  }

  async function remove(q: QuickReply) {
    setFailure(null);
    try {
      await deleteQuickReply(q.id);
      setPending(null);
      void mutate();
    } catch (e) {
      setFailure(e instanceof Error ? e.message : 'Gagal menghapus balas cepat.');
    }
  }

  /** Same reply, new shortcut. The fastest way to write the next one. */
  async function duplicate(q: QuickReply) {
    setFailure(null);
    try {
      const copy = `${q.shortcut}_salinan`.slice(0, 40);
      await upsertQuickReply({
        shortcut: copy,
        application_id: q.application_id,
        title: copy,
        body: q.body,
        category: q.category,
        media_url: q.media_url,
      });
      void mutate();
      setNotice(`Disalin sebagai /${copy}.`);
    } catch (e) {
      setFailure(e instanceof Error ? e.message : 'Gagal menyalin.');
    }
  }

  async function setActive(q: QuickReply, active: boolean) {
    setFailure(null);
    try {
      await upsertQuickReply({
        id: q.id,
        shortcut: q.shortcut,
        application_id: q.application_id,
        title: q.title,
        body: q.body,
        category: q.category,
        media_url: q.media_url,
        is_active: active,
      });
      void mutate();
    } catch (e) {
      setFailure(e instanceof Error ? e.message : 'Gagal mengubah status.');
    }
  }

  return (
    <SettingsPageShell
      title="Balas Cepat"
      description="Jawaban siap pakai untuk mempercepat respons di ruang chat. Ketik garis miring di kolom pesan, lalu pilih."
      actions={
        <div className="flex flex-wrap items-center gap-2">
          <label className="relative block w-full sm:w-[260px]">
            <Search className="pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-ink-muted" />
            <input
              ref={searchRef}
              value={term}
              onChange={(e) => setTerm(e.target.value)}
              placeholder="Cari kode atau isi pesan…"
              aria-label="Cari balas cepat"
              className={clsx(inputClass, 'w-full pr-14 pl-8')}
            />
            <kbd className="pointer-events-none absolute top-1/2 right-2 -translate-y-1/2 rounded border border-hairline bg-surface-sunken px-1 text-[10px] text-ink-muted">
              Ctrl K
            </kbd>
          </label>

          {/* Export is open to anyone who can read the list: it contains
              nothing they cannot already see on this screen. */}
          <Button onClick={() => void runExport()} loading={exporting}>
            <Download className="size-4" />
            Ekspor
          </Button>
          {canEdit ? (
            <>
              <Button onClick={() => fileRef.current?.click()} loading={importing}>
                <Upload className="size-4" />
                Impor
              </Button>
              <input
                ref={fileRef}
                type="file"
                accept=".csv,text/csv"
                hidden
                onChange={(e) => {
                  const file = e.target.files?.[0];
                  // Reset first, so picking the same file twice fires again.
                  e.target.value = '';
                  if (file) void runImport(file);
                }}
              />
              {wipeCount > 0 ? (
                <Button variant="danger" onClick={askWipe}>
                  <Trash2 className="size-4" />
                  Hapus semua
                </Button>
              ) : null}
              <Button variant="primary" onClick={() => setComposing(true)}>
                <Plus className="size-4" />
                Balasan baru
              </Button>
            </>
          ) : null}
        </div>
      }
    >
      {failure ? (
        <div className="mt-4">
          <ErrorState message={failure} onRetry={() => setFailure(null)} />
        </div>
      ) : null}

      {notice ? (
        <div className="mt-4 flex items-center justify-between gap-3 rounded-card border border-hairline bg-surface-raised px-4 py-3 shadow-e1">
          <p className="text-sm text-ink-soft">{notice}</p>
          <button
            type="button"
            onClick={() => setNotice(null)}
            className="shrink-0 text-xs font-medium text-ink-muted hover:text-ink-soft"
          >
            Tutup
          </button>
        </div>
      ) : null}

      {/*
       * What the file actually did, line by line.
       *
       * A count alone ("183 disimpan, 17 dilewati") tells somebody there is a
       * problem without telling them where. The refused lines are listed with
       * their line number and the reason, so the file can be fixed and handed
       * back rather than re-typed.
       */}
      {report ? (
        <div className="mt-4 rounded-card border border-hairline bg-surface-raised px-4 py-3.5 shadow-e1">
          <div className="flex flex-wrap items-baseline justify-between gap-2">
            <p className="text-sm font-medium text-ink">
              <span className="text-brand-700">{report.saved} disimpan</span>
              {report.skipped > 0 ? (
                <>
                  {' · '}
                  <span className="text-danger">{report.skipped} dilewati</span>
                </>
              ) : null}
            </p>
            <button
              type="button"
              onClick={() => setReport(null)}
              className="text-xs font-medium text-ink-muted hover:text-ink-soft"
            >
              Tutup
            </button>
          </div>

          {/*
           * Both kinds of note, in one list.
           *
           * A repaired shortcut is reported even though its row succeeded: the
           * file said "FOLLOW UP" and what now summons it is /follow_up, and
           * somebody who is not told that will type the wrong thing in a chat
           * and conclude the import failed.
           */}
          {report.rows.some((row) => row.status !== 'disimpan' || row.reason) ? (
            <ul className="mt-2 max-h-72 space-y-1 overflow-y-auto border-t border-hairline pt-2">
              {report.rows
                .filter((row) => row.status !== 'disimpan' || row.reason)
                .map((row) => (
                  <li key={row.line} className="flex gap-2 text-xs">
                    <span className="nums shrink-0 text-ink-muted">Baris {row.line}</span>
                    <span className="shrink-0 font-mono text-ink-soft">
                      {row.shortcut ? `/${row.shortcut}` : '—'}
                    </span>
                    <span
                      className={clsx(
                        'min-w-0',
                        row.status === 'disimpan' ? 'text-ink-muted' : 'text-danger',
                      )}
                    >
                      {row.reason}
                    </span>
                  </li>
                ))}
            </ul>
          ) : (
            <p className="mt-1 text-xs text-ink-muted">
              Seluruh baris berhasil dibaca apa adanya. Pintasan yang sudah ada diperbarui, bukan
              digandakan.
            </p>
          )}
        </div>
      ) : null}

      <div className="mt-5">
        <AppFilter rows={rows} value={filter} onChange={setFilter} applications={applications} />
      </div>

      {/* How many are on screen, in what order, and drawn which way. */}
      <div className="mt-4 flex flex-wrap items-center justify-between gap-3">
        <p className="text-xs text-ink-muted">
          <span className="nums font-medium text-ink-soft">{visible.length}</span>
          {term ? ' cocok' : ' balas cepat'}
        </p>

        <div className="flex items-center gap-2">
          <label className="flex items-center gap-1.5 text-xs text-ink-muted">
            Urutkan
            <select
              value={sort}
              onChange={(e) => setSort(e.target.value as SortKey)}
              className="h-9 rounded-control border border-hairline bg-surface-raised px-2 text-xs text-ink-soft outline-none focus:border-brand-600"
            >
              <option value="terbaru">Terbaru</option>
              <option value="sering">Paling sering dipakai</option>
              <option value="abjad">Abjad</option>
            </select>
          </label>

          <div className="flex rounded-control border border-hairline p-0.5">
            {([
              { id: 'list' as const, icon: List, label: 'Daftar' },
              { id: 'grid' as const, icon: LayoutGrid, label: 'Kartu' },
            ]).map((v) => {
              const Icon = v.icon;
              return (
                <button
                  key={v.id}
                  type="button"
                  onClick={() => setView(v.id)}
                  aria-pressed={view === v.id}
                  aria-label={v.label}
                  className={clsx(
                    'rounded-[6px] p-1.5 transition-colors',
                    view === v.id
                      ? 'bg-brand-800 text-white'
                      : 'text-ink-muted hover:bg-surface-sunken',
                  )}
                >
                  <Icon className="size-4" />
                </button>
              );
            })}
          </div>
        </div>
      </div>

      {error ? (
        <div className="mt-4">
          <ErrorState
            message={error instanceof Error ? error.message : 'Gagal memuat.'}
            onRetry={() => void mutate()}
          />
        </div>
      ) : isLoading ? (
        <div className="mt-4">
          <RowSkeleton count={4} />
        </div>
      ) : visible.length === 0 ? (
        <div className="mt-4">
          <EmptyState
            title={
              rows.length === 0
                ? 'Belum ada balas cepat'
                : term
                  ? 'Tidak ada yang cocok'
                  : 'Tidak ada di aplikasi ini'
            }
            hint={
              rows.length === 0
                ? 'Buat jawaban yang sering diketik ulang, lalu panggil dengan garis miring di ruang chat.'
                : term
                  ? 'Coba kata lain, atau hapus kata kuncinya.'
                  : 'Pilih aplikasi lain di baris filter, atau buat balasan untuk aplikasi ini.'
            }
          />
        </div>
      ) : (
        <ul
          className={clsx(
            'mt-4',
            view === 'grid' ? 'grid gap-3 md:grid-cols-2 xl:grid-cols-3' : 'space-y-2',
          )}
        >
          {visible.map((q) => (
            <QuickReplyRow
              key={q.id}
              reply={q}
              view={view}
              canEdit={canEdit}
              onEdit={() => setEditing(q)}
              onDelete={() => setPending(q)}
              onDuplicate={() => void duplicate(q)}
              onToggleActive={() => void setActive(q, !q.is_active)}
            />
          ))}
        </ul>
      )}

      {/*
       * One form for both jobs.
       *
       * Editing is the same fields as creating; a second component would be two
       * places for the validation to drift apart. `key` forces a fresh one when
       * the subject changes, so the boxes never keep the previous reply's words.
       */}
      {composing || editing ? (
        <QuickReplyDialog
          key={editing?.id ?? 'baru'}
          existing={editing}
          applications={applications}
          canPickAll={isLeader}
          onClose={() => {
            setComposing(false);
            setEditing(null);
          }}
          onSaved={() => {
            setComposing(false);
            setEditing(null);
            void mutate();
          }}
        />
      ) : null}

      {/*
       * The bulk wipe asks in its own dialog rather than sharing the one below.
       *
       * It states the count and the exact scope, because "semua" means
       * something different depending on which filter is on screen, and it
       * points at Ekspor first: the file it writes is these very rows, and
       * importing it back puts them all returned. That is the closest thing to
       * an undo this action has, so it is offered before rather than regretted
       * after.
       */}
      <ConfirmDialog
        request={
          wiping
            ? {
                title: wiping.title,
                description: (
                  <>
                    <span className="font-medium text-ink">
                      {wiping.count.toLocaleString('id-ID')} balas cepat
                    </span>{' '}
                    akan dihapus permanen
                    {wiping.scoped
                      ? '. Balas cepat di aplikasi lain tidak tersentuh.'
                      : ' — seluruh yang ada dalam wewenang Anda, bukan hanya yang sedang tampil di layar.'}
                    <br />
                    <br />
                    Tekan <span className="font-medium text-ink">Ekspor</span> dulu kalau belum:
                    berkasnya berisi baris-baris ini, dan mengimpornya kembali akan memulihkannya.
                    Pesan yang sudah terkirim memakainya tidak berubah.
                  </>
                ),
                confirmLabel: 'Hapus semua',
                tone: 'danger',
                icon: Trash2,
                onConfirm: () => wipe(wiping.applicationId),
              }
            : null
        }
        onClose={() => setWiping(null)}
      />

      <ConfirmDialog
        request={
          pending
            ? {
                title: `Hapus /${pending.shortcut}?`,
                description:
                  'Pesan yang sudah terkirim memakainya tidak berubah. Yang hilang hanya templatnya.',
                confirmLabel: 'Hapus balas cepat',
                tone: 'danger',
                onConfirm: () => remove(pending),
              }
            : null
        }
        onClose={() => setPending(null)}
      />
    </SettingsPageShell>
  );
}

/* --- list ---------------------------------------------------------------- */

type SortKey = 'terbaru' | 'sering' | 'abjad';

const SORTS: Record<SortKey, (a: QuickReply, b: QuickReply) => number> = {
  terbaru: (a, b) => b.created_at.localeCompare(a.created_at),
  // Most used first, and a tie broken by the shortcut so the order is stable
  // rather than shuffling on every fetch.
  sering: (a, b) => b.usage_count - a.usage_count || a.shortcut.localeCompare(b.shortcut),
  abjad: (a, b) => a.shortcut.localeCompare(b.shortcut),
};

function QuickReplyRow({
  reply: q,
  view,
  canEdit,
  onEdit,
  onDelete,
  onDuplicate,
  onToggleActive,
}: {
  reply: QuickReply;
  view: 'list' | 'grid';
  canEdit: boolean;
  onEdit: () => void;
  onDelete: () => void;
  onDuplicate: () => void;
  onToggleActive: () => void;
}) {
  return (
    <li
      className={clsx(
        'rounded-card border border-hairline bg-surface-raised px-4 py-3.5 shadow-e1 transition-shadow hover:shadow-e2',
        view === 'list' ? 'flex items-start gap-3' : 'flex flex-col gap-2',
        !q.is_active && 'opacity-60',
      )}
    >
      <div className={clsx('flex min-w-0 flex-1 gap-3', view === 'grid' && 'w-full')}>
        <span
          aria-hidden
          className="grid size-9 shrink-0 place-items-center rounded-xl bg-brand-600/10 text-brand-700"
        >
          <Zap className="size-4" />
        </span>

        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <span className="font-mono text-sm font-medium text-ink">/{q.shortcut}</span>

            {q.application_code ? (
              <span
                className="rounded-md px-1.5 py-0.5 text-2xs font-medium"
                style={{
                  backgroundColor: `${q.application_color ?? '#3a604b'}1F`,
                  color: q.application_color ?? '#3a604b',
                }}
              >
                {q.application_code}
              </span>
            ) : (
              <span className="rounded-md bg-surface-sunken px-1.5 py-0.5 text-2xs text-ink-muted">
                Semua aplikasi
              </span>
            )}

            {q.category ? (
              <span className="rounded-full border border-hairline px-2 py-0.5 text-2xs text-ink-muted">
                {q.category}
              </span>
            ) : null}

            {!q.is_active ? (
              <span className="rounded-full bg-surface-sunken px-2 py-0.5 text-2xs text-ink-muted">
                Nonaktif
              </span>
            ) : null}
          </div>

          <p className="mt-1.5 line-clamp-2 text-sm leading-relaxed text-ink-soft">{q.body}</p>

          {/* The picture, as a chip with the picture in it. "Gambar" as a word
              says there is one; the thumbnail says which one, which is what
              somebody scanning for the right promo needs — and at this size it
              costs one line rather than four. */}
          {q.media_url ? (
            <span className="mt-2 inline-flex items-center gap-1.5 rounded-full border border-hairline bg-surface-sunken py-0.5 pr-2.5 pl-0.5 text-2xs text-ink-soft">
              {/* eslint-disable-next-line @next/next/no-img-element */}
              <img
                src={q.media_url}
                alt=""
                loading="lazy"
                className="size-5 rounded-full object-cover"
              />
              <ImageIcon className="size-3" aria-hidden />
              Gambar terlampir
            </span>
          ) : null}
        </div>
      </div>

      <div
        className={clsx(
          'flex shrink-0 items-center gap-1',
          view === 'grid' && 'w-full justify-between border-t border-hairline pt-2',
        )}
      >
        {/*
         * How often it is actually sent.
         *
         * The list only grows, and without this there is no way to tell a reply
         * used two hundred times from one made once and forgotten — and both
         * crowd the slash menu while somebody is in a hurry.
         */}
        <span
          title={
            q.last_used_at
              ? `Terakhir dipakai ${new Date(q.last_used_at).toLocaleString('id-ID', { dateStyle: 'medium', timeStyle: 'short' })}`
              : 'Belum pernah dipakai'
          }
          className={clsx(
            'nums inline-flex items-center gap-1 text-2xs',
            q.usage_count > 0 ? 'text-ink-soft' : 'text-ink-muted',
          )}
        >
          <BarChart3 className="size-3.5" aria-hidden />
          {q.usage_count.toLocaleString('id-ID')}
        </span>

        {canEdit ? (
          <span className="flex items-center gap-0.5">
            <button
              type="button"
              onClick={onEdit}
              aria-label={`Ubah /${q.shortcut}`}
              className="rounded-lg p-1.5 text-ink-muted transition-colors hover:bg-surface-sunken hover:text-ink-soft"
            >
              <Pencil className="size-4" />
            </button>
            <button
              type="button"
              onClick={onDelete}
              aria-label={`Hapus /${q.shortcut}`}
              className="rounded-lg p-1.5 text-danger/70 transition-colors hover:bg-danger-soft hover:text-danger"
            >
              <Trash2 className="size-4" />
            </button>
            <RowMenu
              label={`Aksi untuk /${q.shortcut}`}
              items={[
                { label: 'Salin isi pesan', onClick: () => void navigator.clipboard?.writeText(q.body) },
                { label: 'Duplikat', onClick: onDuplicate },
                {
                  label: q.is_active ? 'Nonaktifkan' : 'Aktifkan',
                  onClick: onToggleActive,
                },
              ]}
            />
          </span>
        ) : null}
      </div>
    </li>
  );
}

/** The row's own menu: one button, and what it can do behind it. */
function RowMenu({
  label,
  items,
}: {
  label: string;
  items: { label: string; onClick: () => void }[];
}) {
  const [open, setOpen] = useState(false);
  const box = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    function onDown(e: MouseEvent) {
      if (!box.current?.contains(e.target as Node)) setOpen(false);
    }
    function onKey(e: KeyboardEvent) {
      if (e.key === 'Escape') setOpen(false);
    }
    document.addEventListener('mousedown', onDown);
    document.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('mousedown', onDown);
      document.removeEventListener('keydown', onKey);
    };
  }, [open]);

  return (
    <div ref={box} className="relative">
      <button
        type="button"
        aria-label={label}
        aria-expanded={open}
        onClick={() => setOpen((v) => !v)}
        className="rounded-lg p-1.5 text-ink-muted transition-colors hover:bg-surface-sunken hover:text-ink-soft"
      >
        <MoreVertical className="size-4" />
      </button>

      {open ? (
        <div className="absolute right-0 z-30 mt-1 w-44 rounded-card border border-hairline bg-surface-raised p-1 shadow-e2">
          {items.map((item) => (
            <button
              key={item.label}
              type="button"
              onClick={() => {
                setOpen(false);
                item.onClick();
              }}
              className="block w-full rounded-control px-2.5 py-2 text-left text-xs text-ink-soft transition-colors hover:bg-surface-sunken"
            >
              {item.label}
            </button>
          ))}
        </div>
      ) : null}
    </div>
  );
}

/* --- the form ------------------------------------------------------------ */

const BODY_LIMIT = 2000;

/**
 * Writing one reply: what summons it, what it says, and where it may be used.
 *
 * A dialog rather than a panel above the list. The list is the context somebody
 * writes against — "do I already have one of these" — and a form that pushed it
 * down the screen took that away at the moment it was needed.
 *
 * The preview is not decoration. A reply is written here and read in a chat
 * bubble, and the two look nothing alike: line breaks, length and where the
 * emphasis falls all read differently at bubble width.
 */
function QuickReplyDialog({
  existing,
  applications,
  canPickAll,
  onClose,
  onSaved,
}: {
  /** The reply being changed, or null when creating a new one. */
  existing: QuickReply | null;
  applications: Application[];
  canPickAll: boolean;
  onClose: () => void;
  onSaved: () => void;
}) {
  const [applicationID, setApplicationID] = useState(existing?.application_id ?? '');
  const [shortcut, setShortcut] = useState(existing?.shortcut ?? '');
  const [body, setBody] = useState(existing?.body ?? '');
  const [category, setCategory] = useState(existing?.category ?? '');
  const [mediaURL, setMediaURL] = useState(existing?.media_url ?? '');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const bodyRef = useRef<HTMLTextAreaElement>(null);

  /*
   * The same repair the server does, mirrored here so the hint under the field
   * shows the shortcut that will actually exist rather than what was typed.
   *
   * The slash is dropped because it belongs to the interface that summons the
   * reply, not to the record. Spaces and punctuation become underscores because
   * the slash menu in the chat composer closes the moment a space is typed —
   * "/follow up" can never be summoned, "/follow_up" can.
   */
  const clean = shortcut
    .trim()
    .toLowerCase()
    .replace(/^\/+/, '')
    .replace(/[^a-z0-9_]+/g, '_')
    .replace(/^_+|_+$/g, '')
    .slice(0, 40);
  const canSave = clean !== '' && body.trim() !== '' && !busy;

  /** Wraps the selected words in WhatsApp's own emphasis marks. */
  function wrap(mark: string) {
    const el = bodyRef.current;
    if (!el) return;
    const { selectionStart: from, selectionEnd: to } = el;
    const picked = body.slice(from, to);
    if (!picked) {
      el.focus();
      return;
    }
    setBody(body.slice(0, from) + mark + picked + mark + body.slice(to));
    // Put the caret back around the same words, so a second press undoes
    // nothing and a third does not nest.
    requestAnimationFrame(() => {
      el.focus();
      el.setSelectionRange(from + mark.length, to + mark.length);
    });
  }

  async function save() {
    setBusy(true);
    setError(null);
    try {
      await upsertQuickReply({
        // Present when editing, which is what lets the shortcut and the
        // application be changed instead of leaving the old row behind under
        // its old name.
        id: existing?.id,
        shortcut: clean,
        application_id: applicationID || null,
        // No separate title field: the shortcut is the name. One less thing to
        // type for a feature whose whole value is speed.
        title: clean,
        body: body.trim(),
        category: category.trim() || null,
        media_url: mediaURL.trim() || null,
      });
      onSaved();
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Gagal menyimpan.');
    } finally {
      setBusy(false);
    }
  }

  return (
    <Modal
      open
      onClose={onClose}
      size="xl"
      title={existing ? `Ubah /${existing.shortcut}` : 'Buat Balasan Cepat Baru'}
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            Batal
          </Button>
          <Button variant="primary" onClick={save} disabled={!canSave} loading={busy}>
            {existing ? 'Simpan perubahan' : 'Simpan balasan'}
          </Button>
        </>
      }
    >
      <p className="-mt-2 mb-4 text-xs text-ink-muted">
        Tambahkan template balasan untuk mempercepat respons di chat.
      </p>

      <div className="grid gap-5 lg:grid-cols-2">
        <div className="min-w-0">
          <label className="block">
            <span className={labelClass}>
              Shortcut code<span className="text-danger"> *</span>
            </span>
            <span className="relative block">
              <span className="absolute top-1/2 left-3 -translate-y-1/2 font-mono text-sm text-ink-muted">
                /
              </span>
              <input
                value={shortcut}
                onChange={(e) => setShortcut(e.target.value)}
                placeholder="promo_utbk"
                className={clsx(inputClass, 'w-full pl-6 font-mono')}
              />
            </span>
            <span className="mt-1 block text-2xs text-ink-muted">
              Ketik <span className="font-mono">/{clean || 'kode'}</span> di chat untuk
              menggunakan.
              {/* Said only when it actually happened, so the line is a
                  correction rather than a rule nobody needed to read. */}
              {clean && clean !== shortcut.trim().toLowerCase().replace(/^\/+/, '')
                ? ' Spasi dan tanda baca diubah jadi garis bawah, karena menu garis-miring di chat tertutup begitu spasi diketik.'
                : ''}
            </span>
          </label>

          {/* The writing box and the things that act on it, in one frame.
              The toolbar belongs to the text, and floating it underneath as a
              separate row made it read as a toolbar for the whole form. */}
          <div className="mt-4">
            <span className={labelClass}>
              Isi pesan<span className="text-danger"> *</span>
            </span>
            <div className="rounded-control border border-hairline bg-surface-raised focus-within:border-brand-600">
              <textarea
                ref={bodyRef}
                value={body}
                onChange={(e) => setBody(e.target.value.slice(0, BODY_LIMIT))}
                rows={6}
                placeholder="Halo kak, terima kasih sudah menghubungi kami. Ada yang bisa dibantu?"
                className={clsx(
                  textareaClass,
                  'w-full resize-y border-0 bg-transparent focus:ring-0 focus:outline-none',
                )}
              />
              <div className="flex items-center justify-between gap-2 border-t border-hairline px-1.5 py-1">
                <span className="flex items-center gap-0.5">
                  {/* WhatsApp's own marks, not rich text. What is typed here is
                      what is sent, so the buttons insert the characters rather
                      than styling anything. */}
                  <FormatButton label="Tebal (*teks*)" onClick={() => wrap('*')}>
                    <Bold className="size-3.5" />
                  </FormatButton>
                  <FormatButton label="Miring (_teks_)" onClick={() => wrap('_')}>
                    <Italic className="size-3.5" />
                  </FormatButton>
                  <FormatButton label="Coret (~teks~)" onClick={() => wrap('~')}>
                    <Strikethrough className="size-3.5" />
                  </FormatButton>
                </span>
                <span
                  className={clsx(
                    'nums pr-1 text-2xs',
                    body.length >= BODY_LIMIT ? 'text-danger' : 'text-ink-muted',
                  )}
                >
                  {body.length}/{BODY_LIMIT}
                </span>
              </div>
            </div>
          </div>

          <label className="mt-4 block">
            <span className={labelClass}>Link gambar (opsional)</span>
            <span className="relative block">
              <LinkIcon className="pointer-events-none absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-ink-muted" />
              <input
                value={mediaURL}
                onChange={(e) => setMediaURL(e.target.value)}
                placeholder="https://…"
                inputMode="url"
                className={clsx(inputClass, 'w-full pl-8')}
              />
            </span>
            <span className="mt-1 block text-2xs leading-relaxed text-ink-muted">
              Alamat gambar. Bila diisi, pesan dikirim sebagai gambar dengan isi pesan di atas
              sebagai keterangan, dan alamatnya diperiksa saat disimpan. Untuk tautan biasa, tulis
              saja di dalam isi pesan — WhatsApp yang membuat previewnya.
            </span>
          </label>

          <label className="mt-4 block">
            <span className={labelClass}>Kategori (opsional)</span>
            <input
              value={category}
              onChange={(e) => setCategory(e.target.value)}
              placeholder="promo"
              className={clsx(inputClass, 'w-full')}
            />
          </label>
        </div>

        <div className="min-w-0">
          <div className="flex flex-wrap items-start justify-between gap-2">
            <div className="min-w-0">
              <span className={labelClass}>Akses Aplikasi</span>
              <p className="text-2xs text-ink-muted">
                Pilih satu aplikasi yang dapat menggunakan template ini.
              </p>
            </div>
            {canPickAll ? (
              // "Semua aplikasi" is the stored state of having no application,
              // so the checkbox is the same choice as the option, said the way
              // somebody looks for it.
              <label className="flex shrink-0 items-center gap-1.5 text-2xs text-ink-soft">
                <input
                  type="checkbox"
                  checked={applicationID === ''}
                  onChange={() => setApplicationID('')}
                  className="size-3.5 accent-brand-700"
                />
                Pilih semua
              </label>
            ) : null}
          </div>

          <div className="mt-2 grid max-h-[212px] gap-1.5 overflow-y-auto pr-1 sm:grid-cols-2">
            {applications.map((a) => (
              <AppChoice
                key={a.id}
                selected={applicationID === a.id}
                onClick={() => setApplicationID(a.id)}
                label={a.code}
                mark={<AppMark code={a.code} color={a.color} size={20} />}
              />
            ))}
          </div>

          <p className="mt-2 text-2xs text-ink-muted">
            {applicationID
              ? 'Hanya muncul di ruang chat aplikasi ini.'
              : 'Muncul di seluruh aplikasi.'}
          </p>

          <div className="mt-4">
            <span className={labelClass}>Preview (tampilan saat dikirim)</span>
            {/* The bubble as the customer receives it: the operator writes in a
                box the width of a form and it lands in one the width of a
                phone, and length, line breaks and emphasis all read
                differently there. */}
            <div className="mt-1 rounded-card border border-hairline bg-surface-sunken p-3">
              <div className="ml-auto max-w-[280px] rounded-xl rounded-tr-sm bg-brand-600/15 px-3 py-2">
                {mediaURL ? (
                  // eslint-disable-next-line @next/next/no-img-element
                  <img
                    src={mediaURL}
                    alt=""
                    className="mb-1.5 max-h-28 w-full rounded-lg object-cover"
                  />
                ) : null}
                <p className="text-xs leading-relaxed whitespace-pre-wrap text-ink">
                  {body.trim() || 'Isi pesan akan tampil di sini.'}
                </p>
                <p className="mt-1 flex items-center justify-end gap-1 text-[10px] text-ink-muted">
                  <span className="nums">
                    {new Date().toLocaleTimeString('id-ID', {
                      hour: '2-digit',
                      minute: '2-digit',
                    })}
                  </span>
                  <CheckCheck className="size-3 text-info" aria-label="Terkirim" />
                </p>
              </div>
            </div>
          </div>
        </div>
      </div>

      {error ? <p className="mt-4 text-sm text-danger">{error}</p> : null}
    </Modal>
  );
}

function FormatButton({
  label,
  onClick,
  children,
}: {
  label: string;
  onClick: () => void;
  children: React.ReactNode;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      title={label}
      aria-label={label}
      className="rounded-control p-1.5 text-ink-muted transition-colors hover:bg-surface-sunken hover:text-ink-soft"
    >
      {children}
    </button>
  );
}

/** One application in the picker, or the workspace-wide option. */
function AppChoice({
  selected,
  onClick,
  label,
  mark,
}: {
  selected: boolean;
  onClick: () => void;
  label: string;
  mark?: React.ReactNode;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-pressed={selected}
      className={clsx(
        'flex items-center gap-2 rounded-control border px-2.5 py-1.5 text-left transition-colors',
        selected
          ? 'border-brand-600/40 bg-brand-600/[0.07]'
          : 'border-hairline hover:bg-surface-sunken/60',
      )}
    >
      <span
        aria-hidden
        className={clsx(
          'grid size-4 shrink-0 place-items-center rounded-[5px] border text-[9px] font-bold',
          selected ? 'border-brand-700 bg-brand-700 text-white' : 'border-hairline-strong',
        )}
      >
        {selected ? '✓' : ''}
      </span>
      {mark}
      <span className="min-w-0 flex-1 truncate text-xs text-ink-soft">{label}</span>
    </button>
  );
}
