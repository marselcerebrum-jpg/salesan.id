'use client';

import clsx from 'clsx';
import {
  BookUser,
  Check,
  ChevronRight,
  Download,
  MessageCircle,
  Pencil,
  Plus,
  Search,
  Trash2,
  Upload,
  Users,
} from 'lucide-react';
import { useMemo, useRef, useState } from 'react';
import useSWR from 'swr';

import { Notice } from '@/components/campaign/shared';
import { fetcher, groupMembersPath, groupsPath, listAudiences } from '@/lib/api';
import type {
  AudienceEntry,
  GroupDirectoryMember,
  GroupRow,
  TargetSource,
} from '@/lib/types';

/**
 * Step 2: who receives it.
 *
 * Five ways in, because recipients genuinely arrive five different ways and
 * flattening them into one list would lose what each one knows: a contact has a
 * name, a group member belongs to a group, and a pasted number is only a number.
 *
 * "Dari Grup" and "Ke Grup" are not the same thing and the screen says so. The
 * first messages each member in their own chat; the second posts once into the
 * group. Choosing the wrong one is the difference between six hundred private
 * messages and one public one, so they are separate tabs rather than a checkbox.
 */

const TABS: { id: TargetSource; label: string; icon: typeof Pencil }[] = [
  { id: 'manual', label: 'Manual', icon: Pencil },
  { id: 'csv', label: 'Import', icon: Upload },
  { id: 'contacts', label: 'Kontak', icon: BookUser },
  { id: 'group_members', label: 'Dari Grup', icon: Users },
  { id: 'groups', label: 'Ke Grup', icon: MessageCircle },
];

export interface RecipientValue {
  source: TargetSource;
  /** Manual rows, imported rows, or the group members that were ticked. */
  numbers: string[];
  contactIDs: string[];
  /** Conversation ids: the groups to post into, or the ones members came from. */
  groupIDs: string[];
}

export const emptyRecipients: RecipientValue = {
  source: 'manual',
  numbers: [''],
  contactIDs: [],
  groupIDs: [],
};

/** How many recipients this step currently holds. */
export function recipientCount(v: RecipientValue): number {
  switch (v.source) {
    case 'manual':
    case 'csv':
    case 'group_members':
      return v.numbers.filter((n) => n.trim() !== '').length;
    case 'contacts':
      return v.contactIDs.length;
    case 'groups':
      return v.groupIDs.length;
    default:
      return 0;
  }
}

export function RecipientStep({
  value,
  onChange,
  accountIDs,
  applicationID,
}: {
  value: RecipientValue;
  onChange: (next: RecipientValue) => void;
  accountIDs: string[];
  applicationID: string;
}) {
  function set(patch: Partial<RecipientValue>) {
    onChange({ ...value, ...patch });
  }

  return (
    <>
      <div className="flex flex-wrap gap-1.5">
        {TABS.map((t) => {
          const on = value.source === t.id;
          const Icon = t.icon;
          return (
            <button
              key={t.id}
              type="button"
              onClick={() =>
                onChange({
                  // Switching the source clears the previous one rather than
                  // keeping both. Two half-filled sources look like one list and
                  // send as another.
                  source: t.id,
                  numbers: t.id === 'manual' ? [''] : [],
                  contactIDs: [],
                  groupIDs: [],
                })
              }
              aria-pressed={on}
              className={clsx(
                'inline-flex items-center gap-1.5 rounded-full border px-3 py-1.5 text-xs transition-colors',
                on
                  ? 'border-brand-700 bg-brand-600/10 font-medium text-brand-800'
                  : 'border-hairline bg-surface-raised text-ink-soft hover:bg-surface-sunken',
              )}
            >
              <Icon className="size-3.5" />
              {t.label}
            </button>
          );
        })}
      </div>

      <div className="mt-3.5">
        {value.source === 'manual' ? (
          <ManualTable numbers={value.numbers} onChange={(numbers) => set({ numbers })} />
        ) : value.source === 'csv' ? (
          <ImportPane onNumbers={(numbers) => set({ numbers })} count={value.numbers.length} />
        ) : accountIDs.length === 0 ? (
          <Notice>Pilih nomor pengirim di langkah 1 dulu, daftarnya mengikuti nomor itu.</Notice>
        ) : value.source === 'contacts' ? (
          <ContactPane
            accountIDs={accountIDs}
            selected={value.contactIDs}
            onChange={(contactIDs) => set({ contactIDs })}
          />
        ) : value.source === 'group_members' ? (
          <FromGroupPane
            applicationID={applicationID}
            accountIDs={accountIDs}
            numbers={value.numbers}
            groupIDs={value.groupIDs}
            onChange={(numbers, groupIDs) => set({ numbers, groupIDs })}
          />
        ) : (
          <ToGroupPane
            applicationID={applicationID}
            accountIDs={accountIDs}
            selected={value.groupIDs}
            onChange={(groupIDs) => set({ groupIDs })}
          />
        )}
      </div>
    </>
  );
}

/* --- Manual ---------------------------------------------------------------- */

function ManualTable({
  numbers,
  onChange,
}: {
  numbers: string[];
  onChange: (next: string[]) => void;
}) {
  const rows = numbers.length > 0 ? numbers : [''];

  function edit(index: number, next: string) {
    const copy = [...rows];
    copy[index] = next;
    onChange(copy);
  }

  function remove(index: number) {
    const copy = rows.filter((_, i) => i !== index);
    onChange(copy.length > 0 ? copy : ['']);
  }

  /**
   * Pasting a column of numbers fills a column of rows.
   *
   * The ordinary way to get four hundred numbers in here is to copy them from a
   * spreadsheet, and the ordinary result of that is four hundred numbers jammed
   * into one cell. Splitting on newlines, commas, semicolons and tabs turns the
   * paste into what the person plainly meant.
   */
  function paste(index: number, event: React.ClipboardEvent<HTMLInputElement>) {
    const text = event.clipboardData.getData('text');
    const parts = text
      .split(/[\n\r,;\t]+/)
      .map((s) => s.trim())
      .filter(Boolean);
    if (parts.length <= 1) return;
    event.preventDefault();
    const copy = [...rows];
    copy.splice(index, 1, ...parts);
    onChange(copy);
  }

  return (
    <>
      {/* Scrolls in its own box, so a long recipient list keeps its header on
          screen instead of leaving numbered rows with no column names. */}
      <div className="max-h-[60vh] overflow-auto rounded-lg border border-hairline">
        <table className="w-full border-collapse text-sm">
          <thead>
            <tr className="text-ink-muted">
              <th
                scope="col"
                className="sticky top-0 z-10 w-12 border-b border-hairline bg-surface-sunken px-3 py-2 text-left text-2xs font-semibold uppercase"
              >
                #
              </th>
              <th
                scope="col"
                className="sticky top-0 z-10 border-b border-hairline bg-surface-sunken px-3 py-2 text-left text-2xs font-semibold uppercase"
              >
                Nomor WA <span className="text-danger">*</span>
              </th>
              <th
                scope="col"
                className="sticky top-0 z-10 w-12 border-b border-hairline bg-surface-sunken"
              />
            </tr>
          </thead>
          <tbody>
            {rows.map((n, i) => (
              <tr key={i} className="border-b border-hairline last:border-0">
                <td className="px-3 py-1.5 text-xs text-ink-muted tabular-nums">{i + 1}</td>
                <td className="px-3 py-1.5">
                  <input
                    value={n}
                    onChange={(e) => edit(i, e.target.value)}
                    onPaste={(e) => paste(i, e)}
                    inputMode="tel"
                    placeholder="628123456789"
                    aria-label={`Nomor WA baris ${i + 1}`}
                    className="w-full bg-transparent text-sm text-ink outline-none placeholder:text-ink-muted"
                  />
                </td>
                <td className="px-2 py-1.5">
                  <button
                    type="button"
                    onClick={() => remove(i)}
                    aria-label={`Hapus baris ${i + 1}`}
                    className="rounded p-1.5 text-ink-muted transition-colors hover:bg-danger-soft hover:text-danger"
                  >
                    <Trash2 className="size-3.5" />
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      <div className="mt-2 flex flex-wrap items-center justify-between gap-2">
        <button
          type="button"
          onClick={() => onChange([...rows, ''])}
          className="inline-flex items-center gap-1 text-xs font-medium text-brand-700 transition-colors hover:text-brand-800"
        >
          <Plus className="size-3.5" />
          Tambah baris
        </button>
        <p className="text-2xs text-ink-muted">
          Tempel banyak nomor sekaligus di kolom <span className="text-brand-700">Nomor WA</span>
        </p>
      </div>
    </>
  );
}

/* --- Import ---------------------------------------------------------------- */

const MAX_IMPORT_BYTES = 5 * 1024 * 1024;

function ImportPane({
  onNumbers,
  count,
}: {
  onNumbers: (next: string[]) => void;
  count: number;
}) {
  const [over, setOver] = useState(false);
  const [note, setNote] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [extraColumns, setExtraColumns] = useState<string[]>([]);
  const input = useRef<HTMLInputElement>(null);

  async function take(file: File | undefined) {
    setError(null);
    setNote(null);
    setExtraColumns([]);
    if (!file) return;

    if (file.size > MAX_IMPORT_BYTES) {
      setError(`Berkas ${(file.size / 1024 / 1024).toFixed(1)} MB melebihi batas 5 MB.`);
      return;
    }
    // Spreadsheets are a different format, not a bigger CSV: .xlsx is a zip of
    // XML. Saying so beats reading one as text and producing a list of mojibake.
    if (/\.(xlsx|xls)$/i.test(file.name)) {
      setError(
        'Berkas Excel belum bisa dibaca di sini. Simpan sebagai CSV lebih dulu (File → Save As → CSV).',
      );
      return;
    }

    const text = await file.text();
    const parsed = parseDelimited(text);
    if (parsed.numbers.length === 0) {
      setError('Tidak ada nomor yang terbaca. Pastikan kolom pertama berisi nomor WhatsApp.');
      return;
    }
    onNumbers(parsed.numbers);
    setExtraColumns(parsed.extraColumns);
    setNote(`${parsed.numbers.length.toLocaleString('id-ID')} nomor terbaca dari ${file.name}.`);
  }

  return (
    <>
      <button
        type="button"
        onClick={() => downloadTemplate()}
        className="inline-flex items-center gap-1.5 text-xs font-medium text-ink-soft transition-colors hover:text-ink"
      >
        <Download className="size-3.5" />
        Unduh template CSV
      </button>

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
          'mt-2 rounded-lg border border-dashed px-4 py-8 text-center transition-colors',
          over ? 'border-brand-700 bg-brand-600/8' : 'border-hairline-strong bg-surface',
        )}
      >
        <span className="mx-auto grid size-10 place-items-center rounded-full bg-surface-sunken">
          <Upload className="size-4 text-ink-muted" />
        </span>
        <button
          type="button"
          onClick={() => input.current?.click()}
          className="mt-2.5 block w-full text-sm font-medium text-ink"
        >
          Drag &amp; drop atau klik untuk pilih file
        </button>
        <p className="mt-0.5 text-2xs text-ink-muted">CSV · TXT — maks 5MB</p>
        <input
          ref={input}
          type="file"
          accept=".csv,.txt,text/csv,text/plain"
          className="hidden"
          onChange={(e) => void take(e.target.files?.[0])}
        />
      </div>

      <div className="mt-2.5 grid gap-2 sm:grid-cols-2">
        <div className="rounded-lg bg-surface-sunken/70 px-3.5 py-2.5">
          <p className="text-2xs font-semibold text-ink-soft">Format CSV</p>
          <p className="mt-1 text-2xs text-ink-muted">Kolom pertama: phone</p>
          <p className="text-2xs text-ink-muted">Baris pertama: header</p>
        </div>
        <div className="rounded-lg bg-surface-sunken/70 px-3.5 py-2.5">
          <p className="text-2xs font-semibold text-ink-soft">Kolom variabel</p>
          <p className="mt-1 text-2xs text-ink-muted">
            Kolom tambahan terbaca sebagai nama variabel
          </p>
          <p className="font-mono text-2xs text-ink-muted">phone, nama, produk, …</p>
        </div>
      </div>

      {note ? (
        <p className="mt-2.5 text-xs text-brand-700">{note}</p>
      ) : null}
      {error ? (
        <div className="mt-2.5">
          <Notice tone="danger">{error}</Notice>
        </div>
      ) : null}

      {/* Stated rather than silently dropped. The columns were read, and the
          person who prepared the file deserves to know they are not yet used. */}
      {extraColumns.length > 0 ? (
        <div className="mt-2.5">
          <Notice tone="warn">
            Kolom {extraColumns.map((c) => `"${c}"`).join(', ')} terbaca tetapi belum dipakai:
            variabel per penerima belum tersambung. Untuk sekarang pakai nilai variabel campaign di
            langkah berikutnya, yang berlaku sama untuk semua penerima.
          </Notice>
        </div>
      ) : null}

      {count > 0 && !note ? (
        <p className="mt-2.5 text-xs text-ink-muted">
          <span className="font-medium text-ink-soft tabular-nums">{count}</span> nomor siap dikirim
          dari import sebelumnya.
        </p>
      ) : null}
    </>
  );
}

/** Splits a CSV or TXT into its phone column and whatever else it carried. */
function parseDelimited(text: string): { numbers: string[]; extraColumns: string[] } {
  const lines = text
    .split(/\r?\n/)
    .map((l) => l.trim())
    .filter(Boolean);
  if (lines.length === 0) return { numbers: [], extraColumns: [] };

  const split = (line: string) => line.split(/[,;\t]/).map((c) => c.trim().replace(/^"|"$/g, ''));

  const first = split(lines[0]);
  // A header is a first row whose first cell holds no digits. A file that opens
  // straight into numbers has no header, and skipping its first line would
  // silently drop a recipient.
  const hasHeader = !/\d/.test(first[0] ?? '');
  const body = hasHeader ? lines.slice(1) : lines;
  const extraColumns = hasHeader ? first.slice(1).filter(Boolean) : [];

  const numbers: string[] = [];
  for (const line of body) {
    const cell = split(line)[0] ?? '';
    if (cell.replace(/\D/g, '').length >= 6) numbers.push(cell);
  }
  return { numbers, extraColumns };
}

function downloadTemplate() {
  const csv = 'phone,nama\n628123456789,Budi\n628987654321,Sari\n';
  const blob = new Blob([csv], { type: 'text/csv;charset=utf-8' });
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = 'template-broadcast.csv';
  a.click();
  URL.revokeObjectURL(url);
}

/* --- Kontak ---------------------------------------------------------------- */

function ContactPane({
  accountIDs,
  selected,
  onChange,
}: {
  accountIDs: string[];
  selected: string[];
  onChange: (next: string[]) => void;
}) {
  const { data, isLoading } = useSWR<{ contacts?: AudienceEntry[] }>(
    ['audiences', accountIDs.join(','), 'contacts'],
    () => listAudiences(accountIDs, 'contacts'),
  );
  const rows = useMemo(
    () => (data?.contacts ?? []).filter((r) => r.contact_id),
    [data],
  );

  return (
    <PickList
      rows={rows.map((r) => ({
        id: r.contact_id as string,
        title: r.name,
        subtitle: r.phone_number,
      }))}
      loading={isLoading}
      selected={selected}
      onChange={onChange}
      placeholder="Cari nama / nomor…"
      empty="Belum ada kontak untuk nomor yang dipilih."
      noun="kontak"
    />
  );
}

/* --- Dari Grup ------------------------------------------------------------- */

function FromGroupPane({
  applicationID,
  accountIDs,
  numbers,
  groupIDs,
  onChange,
}: {
  applicationID: string;
  accountIDs: string[];
  numbers: string[];
  groupIDs: string[];
  onChange: (numbers: string[], groupIDs: string[]) => void;
}) {
  const [openJID, setOpenJID] = useState<string | null>(null);
  const groups = useGroups(applicationID, accountIDs);

  const open = groups.rows.find((g) => g.chat_jid === openJID) ?? null;

  const { data: detail, isLoading: loadingMembers } = useSWR<{
    members: GroupDirectoryMember[];
  }>(openJID ? groupMembersPath(openJID) : null, fetcher, { revalidateOnFocus: false });

  // Only members with a real number can be messaged privately. The rest are
  // shown as unavailable rather than hidden, so a count that does not match the
  // group's own figure has a visible reason.
  const members = useMemo(() => detail?.members ?? [], [detail]);
  const reachable = useMemo(() => members.filter((m) => m.phone_number), [members]);

  function setMembers(next: string[]) {
    const ids = open ? uniq([...groupIDs, conversationFor(open, accountIDs)].filter(Boolean)) : groupIDs;
    onChange(next, ids as string[]);
  }

  return (
    <>
      <GroupList
        rows={groups.rows}
        loading={groups.isLoading}
        accountIDs={accountIDs}
        activeJID={openJID}
        onOpen={(g) => setOpenJID(g.chat_jid === openJID ? null : g.chat_jid)}
      />

      {open ? (
        <div className="mt-2.5 rounded-lg border border-hairline bg-surface-raised">
          <div className="flex flex-wrap items-center justify-between gap-2 border-b border-hairline px-3 py-2">
            <p className="flex items-center gap-1.5 text-xs font-medium text-ink">
              <Users className="size-3.5 shrink-0 text-ink-muted" />
              {open.name || open.chat_jid}
              <span className="font-normal text-ink-muted tabular-nums">
                — {numbers.length}/{reachable.length} dipilih
              </span>
            </p>
          </div>

          <MemberList
            members={members}
            loading={loadingMembers}
            selected={numbers}
            onChange={setMembers}
          />
        </div>
      ) : null}
    </>
  );
}

function MemberList({
  members,
  loading,
  selected,
  onChange,
}: {
  members: GroupDirectoryMember[];
  loading: boolean;
  selected: string[];
  onChange: (next: string[]) => void;
}) {
  const [search, setSearch] = useState('');
  const needle = search.trim().toLowerCase();
  const digits = needle.replace(/\D/g, '');

  const shown = members.filter((m) => {
    if (!needle) return true;
    if (m.display_name.toLowerCase().includes(needle)) return true;
    return (
      digits.length > 0 && (m.phone_number ?? '').replace(/\D/g, '').includes(digits)
    );
  });

  const unreachable = members.length - members.filter((m) => m.phone_number).length;
  // Only the visible members who can actually be reached privately.
  const shownReachable = shown
    .map((m) => m.phone_number)
    .filter((n): n is string => Boolean(n));

  return (
    <>
      <div className="flex items-center gap-2 border-b border-hairline px-3 py-2">
        <Search className="size-3.5 shrink-0 text-ink-muted" />
        <input
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          placeholder="Cari nama / nomor…"
          aria-label="Cari anggota grup"
          className="w-full bg-transparent text-xs text-ink outline-none placeholder:text-ink-muted"
        />
        {/*
          Beside the search, not above the list, because this button acts on
          exactly what the search is showing. Members without a number cannot be
          messaged privately, so they are never chosen even when they are on
          screen.
        */}
        <button
          type="button"
          onClick={() => onChange(toggleShown(selected, shownReachable))}
          className="shrink-0 text-xs font-medium text-brand-700 transition-colors hover:text-brand-800"
        >
          {allShownChosen(selected, shownReachable) ? 'Kosongkan' : 'Pilih semua'}
        </button>
      </div>

      {loading ? (
        <p className="px-3 py-6 text-center text-xs text-ink-muted">Memuat anggota…</p>
      ) : members.length === 0 ? (
        <p className="px-3 py-6 text-center text-xs text-ink-muted">
          Daftar anggota grup ini belum diambil. Buka menu Fetch Grup dan tekan Perbarui.
        </p>
      ) : (
        <ul className="max-h-64 overflow-y-auto">
          {shown.map((m) => {
            const phone = m.phone_number;
            const on = phone ? selected.includes(phone) : false;
            return (
              <li key={m.jid}>
                <button
                  type="button"
                  disabled={!phone}
                  onClick={() =>
                    phone &&
                    onChange(
                      on ? selected.filter((n) => n !== phone) : [...selected, phone],
                    )
                  }
                  className={clsx(
                    'flex w-full items-center gap-2.5 px-3 py-1.5 text-left transition-colors',
                    !phone
                      ? 'cursor-not-allowed opacity-50'
                      : on
                        ? 'bg-brand-600/8'
                        : 'hover:bg-surface-sunken',
                  )}
                >
                  <Box checked={on} />
                  <span className="min-w-0 flex-1">
                    <span className="block truncate text-xs text-ink">
                      {m.display_name || <span className="text-ink-muted">—</span>}
                    </span>
                    <span className="block truncate text-2xs text-ink-muted tabular-nums">
                      {phone ?? 'nomor tidak diketahui'}
                    </span>
                  </span>
                </button>
              </li>
            );
          })}
        </ul>
      )}

      {unreachable > 0 ? (
        <p className="border-t border-hairline px-3 py-1.5 text-2xs text-ink-muted">
          <span className="tabular-nums">{unreachable}</span> anggota tidak punya nomor yang
          diketahui, jadi tidak bisa dikirimi pesan pribadi.
        </p>
      ) : null}
    </>
  );
}

/* --- Ke Grup --------------------------------------------------------------- */

function ToGroupPane({
  applicationID,
  accountIDs,
  selected,
  onChange,
}: {
  applicationID: string;
  accountIDs: string[];
  selected: string[];
  onChange: (next: string[]) => void;
}) {
  const groups = useGroups(applicationID, accountIDs);
  const [search, setSearch] = useState('');

  const needle = search.trim().toLowerCase();
  const shown = groups.rows.filter((g) => !needle || g.name.toLowerCase().includes(needle));
  // The ids the button acts on are the ids on screen, which is what the search
  // just narrowed them to.
  const shownIDs = shown
    .map((g) => conversationFor(g, accountIDs))
    .filter((id): id is string => Boolean(id));

  return (
    <>
      <p className="text-xs text-brand-700">
        Pesan dikirim langsung ke obrolan grup (bukan ke anggota individual).
      </p>

      <div className="mt-2 flex flex-wrap items-center gap-2">
        <label className="flex min-w-[200px] flex-1 items-center gap-2 rounded-lg border border-hairline bg-surface-raised px-3 py-2">
          <Search className="size-3.5 shrink-0 text-ink-muted" />
          <input
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder="Cari grup…"
            aria-label="Cari grup"
            className="w-full bg-transparent text-xs text-ink outline-none placeholder:text-ink-muted"
          />
        </label>
        <button
          type="button"
          onClick={() => onChange(toggleShown(selected, shownIDs))}
          className="shrink-0 text-xs font-medium text-brand-700 transition-colors hover:text-brand-800"
        >
          {allShownChosen(selected, shownIDs) ? 'Kosongkan' : 'Pilih semua'}
        </button>
      </div>

      <div className="mt-2 overflow-hidden rounded-lg border border-hairline bg-surface-raised">
        {groups.isLoading ? (
          <p className="px-3 py-6 text-center text-xs text-ink-muted">Memuat grup…</p>
        ) : shown.length === 0 ? (
          <p className="px-3 py-6 text-center text-xs text-ink-muted">
            Tidak ada grup untuk nomor yang dipilih.
          </p>
        ) : (
          <ul className="max-h-72 overflow-y-auto">
            {shown.map((g) => {
              const id = conversationFor(g, accountIDs);
              if (!id) return null;
              const on = selected.includes(id);
              return (
                <li key={g.chat_jid}>
                  <button
                    type="button"
                    onClick={() =>
                      onChange(on ? selected.filter((x) => x !== id) : [...selected, id])
                    }
                    className={clsx(
                      'flex w-full items-center gap-2.5 px-3 py-2 text-left transition-colors',
                      on ? 'bg-brand-600/8' : 'hover:bg-surface-sunken',
                    )}
                  >
                    <Box checked={on} />
                    <span className="min-w-0 flex-1">
                      <span className="block truncate text-xs font-medium text-ink">
                        {g.name || g.chat_jid}
                      </span>
                      <span className="block truncate text-2xs text-ink-muted">
                        {g.fetched ? `${g.member_count.toLocaleString('id-ID')} anggota` : 'anggota belum diambil'}
                        {' · '}
                        {g.accounts.map((a) => a.account_name).join(', ')}
                      </span>
                    </span>
                  </button>
                </li>
              );
            })}
          </ul>
        )}
      </div>
    </>
  );
}

/* --- shared pieces --------------------------------------------------------- */

/**
 * The groups the chosen numbers are in.
 *
 * Read from the group directory rather than the audience endpoint because the
 * directory carries the member counts this screen shows, and because it already
 * excludes groups we have left — a group nobody is in is not somewhere to
 * broadcast to.
 */
function useGroups(applicationID: string, accountIDs: string[]) {
  const { data, isLoading } = useSWR<{ groups: GroupRow[] }>(
    applicationID ? groupsPath({ application_id: applicationID, limit: 500 }) : null,
    fetcher,
  );
  const rows = useMemo(
    () =>
      (data?.groups ?? []).filter((g) =>
        g.accounts.some((a) => accountIDs.includes(a.account_id)),
      ),
    [data, accountIDs],
  );
  return { rows, isLoading };
}

function GroupList({
  rows,
  loading,
  accountIDs,
  activeJID,
  onOpen,
}: {
  rows: GroupRow[];
  loading: boolean;
  accountIDs: string[];
  activeJID: string | null;
  onOpen: (g: GroupRow) => void;
}) {
  const [search, setSearch] = useState('');
  const needle = search.trim().toLowerCase();
  const shown = rows.filter((g) => !needle || g.name.toLowerCase().includes(needle));

  return (
    <>
      <label className="flex items-center gap-2 rounded-lg border border-hairline bg-surface-raised px-3 py-2">
        <Search className="size-3.5 shrink-0 text-ink-muted" />
        <input
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          placeholder="Cari grup…"
          aria-label="Cari grup"
          className="w-full bg-transparent text-xs text-ink outline-none placeholder:text-ink-muted"
        />
      </label>

      <div className="mt-2 overflow-hidden rounded-lg border border-hairline bg-surface-raised">
        {loading ? (
          <p className="px-3 py-6 text-center text-xs text-ink-muted">Memuat grup…</p>
        ) : shown.length === 0 ? (
          <p className="px-3 py-6 text-center text-xs text-ink-muted">
            Tidak ada grup untuk nomor yang dipilih.
          </p>
        ) : (
          <ul className="max-h-56 overflow-y-auto">
            {shown.map((g) => {
              const on = g.chat_jid === activeJID;
              const reachable = g.accounts.some((a) => accountIDs.includes(a.account_id));
              if (!reachable) return null;
              return (
                <li key={g.chat_jid}>
                  <button
                    type="button"
                    onClick={() => onOpen(g)}
                    className={clsx(
                      'flex w-full items-center gap-2.5 px-3 py-2 text-left transition-colors',
                      on ? 'bg-brand-600/8' : 'hover:bg-surface-sunken',
                    )}
                  >
                    <Users className="size-3.5 shrink-0 text-ink-muted" />
                    <span className="min-w-0 flex-1 truncate text-xs font-medium text-ink">
                      {g.name || g.chat_jid}
                    </span>
                    <span className="shrink-0 text-2xs text-ink-muted tabular-nums">
                      {g.fetched ? g.member_count.toLocaleString('id-ID') : '–'}
                    </span>
                    <ChevronRight className="size-3.5 shrink-0 text-ink-muted" />
                  </button>
                </li>
              );
            })}
          </ul>
        )}
      </div>
    </>
  );
}

function PickList({
  rows,
  loading,
  selected,
  onChange,
  placeholder,
  empty,
  noun,
}: {
  rows: { id: string; title: string; subtitle: string }[];
  loading: boolean;
  selected: string[];
  onChange: (next: string[]) => void;
  placeholder: string;
  empty: string;
  noun: string;
}) {
  const [search, setSearch] = useState('');
  const needle = search.trim().toLowerCase();
  const shown = rows.filter(
    (r) => !needle || r.title.toLowerCase().includes(needle) || r.subtitle.includes(needle),
  );

  return (
    <div className="overflow-hidden rounded-lg border border-hairline bg-surface-raised">
      <div className="flex items-center gap-2 border-b border-hairline px-3 py-2">
        <Search className="size-3.5 shrink-0 text-ink-muted" />
        <input
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          placeholder={placeholder}
          aria-label={placeholder}
          className="w-full bg-transparent text-xs text-ink outline-none placeholder:text-ink-muted"
        />
        <button
          type="button"
          onClick={() => onChange(toggleShown(selected, shown.map((r) => r.id)))}
          className="shrink-0 text-xs font-medium text-brand-700 transition-colors hover:text-brand-800"
        >
          {allShownChosen(selected, shown.map((r) => r.id)) ? 'Kosongkan' : 'Pilih semua'}
        </button>
      </div>

      {loading ? (
        <p className="px-3 py-6 text-center text-xs text-ink-muted">Memuat…</p>
      ) : rows.length === 0 ? (
        <p className="px-3 py-6 text-center text-xs text-ink-muted">{empty}</p>
      ) : (
        <ul className="max-h-64 overflow-y-auto">
          {shown.slice(0, 500).map((r) => {
            const on = selected.includes(r.id);
            return (
              <li key={r.id}>
                <button
                  type="button"
                  onClick={() =>
                    onChange(on ? selected.filter((x) => x !== r.id) : [...selected, r.id])
                  }
                  className={clsx(
                    'flex w-full items-center gap-2.5 px-3 py-1.5 text-left transition-colors',
                    on ? 'bg-brand-600/8' : 'hover:bg-surface-sunken',
                  )}
                >
                  <Box checked={on} />
                  <span className="min-w-0 flex-1 truncate text-xs text-ink">{r.title}</span>
                  <span className="shrink-0 text-2xs text-ink-muted tabular-nums">
                    {r.subtitle}
                  </span>
                </button>
              </li>
            );
          })}
        </ul>
      )}

      <p className="border-t border-hairline px-3 py-1.5 text-2xs text-ink-muted">
        <span className="tabular-nums">{selected.length}</span> dipilih dari{' '}
        <span className="tabular-nums">{rows.length}</span> {noun}
        {shown.length > 500 ? ' · menampilkan 500 pertama' : ''}
      </p>
    </div>
  );
}

function Box({ checked }: { checked: boolean }) {
  return (
    <span
      aria-hidden
      className={clsx(
        'grid size-4 shrink-0 place-items-center rounded border',
        checked ? 'border-brand-700 bg-brand-700 text-white' : 'border-hairline-strong',
      )}
    >
      {checked ? <Check className="size-3" strokeWidth={3} /> : null}
    </span>
  );
}

/** The conversation id through which one of the chosen numbers reaches a group. */
function conversationFor(g: GroupRow, accountIDs: string[]): string | null {
  const hit = g.accounts.find((a) => accountIDs.includes(a.account_id));
  return hit?.conversation_id ?? null;
}

function uniq<T>(items: T[]): T[] {
  return [...new Set(items)];
}

/**
 * Whether everything the search is currently showing is already chosen.
 *
 * Asked of the visible rows, not of the whole list, so the button can say
 * "Kosongkan" while a search is narrowing the view.
 */
function allShownChosen(selected: string[], shown: string[]): boolean {
  if (shown.length === 0) return false;
  const have = new Set(selected);
  return shown.every((id) => have.has(id));
}

/**
 * "Pilih semua" acts on what the search is showing, never on what it is hiding.
 *
 * It used to read the unfiltered list. Searching "CPNS" and pressing it put
 * every group on the phone into the broadcast — sixty-seven of them — for a
 * campaign meant to reach the six that matched. The count at the top said 67
 * and looked like a feature working.
 *
 * Choosing adds to what is already chosen rather than replacing it, so two
 * searches one after another keep both sets. Clearing removes only what is on
 * screen, leaving anything chosen under an earlier search alone. Either way the
 * operator can see exactly which rows the button is about to touch.
 */
function toggleShown(selected: string[], shown: string[]): string[] {
  if (allShownChosen(selected, shown)) {
    const drop = new Set(shown);
    return selected.filter((id) => !drop.has(id));
  }
  return uniq([...selected, ...shown]);
}
