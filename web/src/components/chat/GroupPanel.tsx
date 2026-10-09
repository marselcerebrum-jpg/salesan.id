'use client';

import clsx from 'clsx';
import {
  Check,
  Crown,
  Loader2,
  Pencil,
  RefreshCw,
  Users,
  X,
} from 'lucide-react';
import { useCallback, useEffect, useRef, useState } from 'react';

import { ErrorNote } from '@/components/ui/Primitives';
import { deleteConversation, leaveGroup, listGroupMembers, refreshGroup, updateGroup } from '@/lib/api';
import { conversationTitle, initials } from '@/lib/format';
import type { Conversation, GroupMember } from '@/lib/types';

const MAX_DESCRIPTION = 2048;

/**
 * Side panel for a group: who is in it, and the description.
 *
 * Reading, not managing. Promoting and removing people live on the message
 * bubble now, because that is where the operator is standing when they need
 * them — finding one name in a list of several hundred was the slow way round.
 * The only thing this says about power is whether this number is an admin at
 * all, which is why the "Anda admin" badge stayed.
 */
export function GroupPanel({
  conversation,
  open,
  onClose,
  onConversationChange,
  onConversationRemoved,
}: {
  conversation: Conversation | null;
  open: boolean;
  onClose: () => void;
  onConversationChange: (conversation: Conversation) => void;
  /** Called after the thread is removed, so the list can move on. */
  onConversationRemoved: () => void;
}) {
  const [members, setMembers] = useState<GroupMember[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [editingDesc, setEditingDesc] = useState(false);
  const [busy, setBusy] = useState(false);
  const [confirm, setConfirm] = useState<'leave' | 'leave-remove' | null>(null);
  const [desc, setDesc] = useState('');
  const [savingDesc, setSavingDesc] = useState(false);

  const conversationId = conversation?.id ?? null;
  const isAdmin = conversation?.self_is_admin ?? false;

  const load = useCallback(async () => {
    if (!conversationId) return;
    setLoading(true);
    setError(null);
    try {
      const { members } = await listGroupMembers(conversationId);
      setMembers(members);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Gagal memuat anggota.');
    } finally {
      setLoading(false);
    }
  }, [conversationId]);

  useEffect(() => {
    if (!open) return;
    setEditingDesc(false);
    setDesc(conversation?.group_description ?? '');
    void load();
  }, [open, load, conversation?.group_description]);

  const closeRef = useRef(onClose);
  closeRef.current = onClose;
  useEffect(() => {
    if (!open) return;
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') closeRef.current();
    };
    document.addEventListener('keydown', onKeyDown);
    return () => document.removeEventListener('keydown', onKeyDown);
  }, [open]);

  if (!open || !conversation) return null;

  async function saveDescription() {
    if (!conversationId || savingDesc) return;
    setSavingDesc(true);
    setError(null);
    try {
      const updated = await updateGroup(conversationId, { description: desc });
      onConversationChange(updated);
      setEditingDesc(false);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Deskripsi gagal disimpan.');
    } finally {
      setSavingDesc(false);
    }
  }

  async function reload() {
    if (!conversationId) return;
    setLoading(true);
    setError(null);
    try {
      const { conversation: updated, members } = await refreshGroup(conversationId);
      setMembers(members);
      onConversationChange(updated);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Gagal menyegarkan dari WhatsApp.');
    } finally {
      setLoading(false);
    }
  }

  const admins = members.filter((m) => m.is_admin).length;
  // `group_is_member` is false once this number has left. Null on a group that
  // predates the flag, which is read as "still in it" — the safer guess, since
  // offering Leave on a group already left does nothing worse than fail.
  const stillMember = conversation?.group_is_member !== false;

  async function leave(alsoRemove: boolean) {
    if (!conversationId) return;
    setBusy(true);
    setError(null);
    try {
      if (stillMember) {
        const { conversation: updated } = await leaveGroup(conversationId);
        onConversationChange(updated);
      }
      if (alsoRemove) {
        await deleteConversation(conversationId);
        onConversationRemoved();
        onClose();
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Gagal keluar dari grup.');
    } finally {
      setBusy(false);
      setConfirm(null);
    }
  }

  return (
    <>
      {/* A drawer rather than a dialog: the operator is inspecting the group
          they are reading, and the conversation should stay visible behind it. */}
      <div className="fixed inset-0 z-40 bg-black/30 lg:hidden" onClick={onClose} aria-hidden />
      <aside
        role="dialog"
        aria-label="Info grup"
        className="fixed inset-y-0 right-0 z-50 flex w-full max-w-[380px] flex-col border-l border-wa-border bg-wa-panel shadow-e4"
      >
        <header className="flex items-center gap-3 border-b border-wa-border px-4 py-3">
          <button
            type="button"
            onClick={onClose}
            aria-label="Tutup"
            className="grid size-9 place-items-center rounded-full text-wa-text-2 hover:bg-wa-active"
          >
            <X className="size-5" />
          </button>
          <p className="min-w-0 flex-1 truncate text-base font-medium text-wa-text">Info grup</p>
          <button
            type="button"
            onClick={() => void reload()}
            disabled={loading}
            aria-label="Segarkan dari WhatsApp"
            title="Segarkan dari WhatsApp"
            className="grid size-9 place-items-center rounded-full text-wa-text-2 hover:bg-wa-active disabled:opacity-40"
          >
            <RefreshCw className={clsx('size-4', loading && 'animate-spin')} />
          </button>
        </header>

        <div className="scrollbar-slim min-h-0 flex-1 overflow-y-auto">
          <div className="flex flex-col items-center gap-2 border-b border-wa-border px-6 py-6 text-center">
            <span className="grid size-20 place-items-center rounded-full bg-wa-active text-wa-text-2">
              <Users className="size-9" />
            </span>
            <p className="text-base text-wa-text">{conversationTitle(conversation)}</p>
            <p className="text-sm text-wa-text-2">
              Grup · {members.length} anggota{admins > 0 ? ` · ${admins} admin` : ''}
            </p>
            {isAdmin ? (
              <span className="mt-1 inline-flex items-center gap-1 rounded-full bg-wa-accent/15 px-2.5 py-1 text-2xs font-medium text-wa-accent">
                <Crown className="size-3" />
                Anda admin
              </span>
            ) : null}
          </div>

          <section className="border-b border-wa-border px-4 py-4">
            <div className="mb-2 flex items-center justify-between">
              <h3 className="text-2xs font-medium tracking-wide text-wa-text-2 uppercase">
                Deskripsi
              </h3>
              {isAdmin && !editingDesc ? (
                <button
                  type="button"
                  onClick={() => setEditingDesc(true)}
                  className="inline-flex items-center gap-1 text-xs text-wa-accent"
                >
                  <Pencil className="size-3.5" />
                  Ubah
                </button>
              ) : null}
            </div>

            {editingDesc ? (
              <>
                <textarea
                  autoFocus
                  value={desc}
                  onChange={(event) => setDesc(event.target.value)}
                  maxLength={MAX_DESCRIPTION}
                  rows={5}
                  placeholder="Tulis deskripsi grup"
                  className="w-full resize-y rounded-lg bg-wa-panel-2 px-3 py-2 text-sm text-wa-text outline-none focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-wa-accent placeholder:text-wa-text-2"
                />
                <div className="mt-2 flex items-center justify-end gap-2 text-sm">
                  <button
                    type="button"
                    onClick={() => {
                      setDesc(conversation.group_description ?? '');
                      setEditingDesc(false);
                    }}
                    disabled={savingDesc}
                    className="text-wa-text-2"
                  >
                    Batal
                  </button>
                  <button
                    type="button"
                    onClick={() => void saveDescription()}
                    disabled={savingDesc}
                    className="inline-flex items-center gap-1.5 font-medium text-wa-accent disabled:opacity-50"
                  >
                    {savingDesc ? <Loader2 className="size-3.5 animate-spin" /> : <Check className="size-3.5" />}
                    Simpan
                  </button>
                </div>
              </>
            ) : (
              <p className="text-sm leading-relaxed whitespace-pre-wrap text-wa-text">
                {conversation.group_description || (
                  <span className="text-wa-text-2 italic">Belum ada deskripsi.</span>
                )}
              </p>
            )}
          </section>

          <section className="px-2 py-3">
            <h3 className="px-2 pb-2 text-2xs font-medium tracking-wide text-wa-text-2 uppercase">
              {members.length} anggota
            </h3>

            {error ? (
              <div className="px-2 pb-2">
                <ErrorNote message={error} />
              </div>
            ) : null}

            {loading && members.length === 0 ? (
              <p className="px-3 py-6 text-center text-sm text-wa-text-2">Memuat anggota…</p>
            ) : members.length === 0 ? (
              <p className="px-3 py-6 text-center text-sm text-wa-text-2">
                Daftar anggota belum tersinkron. Tekan segarkan di atas.
              </p>
            ) : (
              members.map((member) => (
                <MemberRow key={member.jid} member={member} />
              ))
            )}
          </section>

          {/* Last in the panel, and the only destructive thing in it.
              Two buttons rather than one, because they are different
              decisions: leaving is about WhatsApp, removing the thread is
              about this workspace, and a month of analytics is built on those
              rows. WhatsApp has no "delete group for everyone", so neither
              button claims to. */}
          <section className="border-t border-wa-border px-4 py-4">
            <p className="text-xs font-medium tracking-wide text-wa-text-2 uppercase">
              Keluar grup
            </p>

            {stillMember ? (
              <p className="mt-1 text-xs text-wa-text-2">
                Nomor ini keluar dari grup. Anggota lain melihatnya, dan untuk masuk
                lagi perlu diundang.
              </p>
            ) : (
              <p className="mt-1 text-xs text-wa-text-2">
                Nomor ini sudah tidak ada di grup. Chatnya masih tersimpan di sini.
              </p>
            )}

            <div className="mt-3 flex flex-wrap gap-2">
              {stillMember ? (
                <button
                  type="button"
                  disabled={busy}
                  onClick={() => setConfirm('leave')}
                  className="rounded-lg border border-danger/40 px-3 py-1.5 text-sm text-danger transition-colors hover:bg-danger-soft/40 disabled:opacity-50"
                >
                  Keluar grup
                </button>
              ) : null}
              <button
                type="button"
                disabled={busy}
                onClick={() => setConfirm('leave-remove')}
                className="rounded-lg border border-danger/40 px-3 py-1.5 text-sm text-danger transition-colors hover:bg-danger-soft/40 disabled:opacity-50"
              >
                {stillMember ? 'Keluar & hapus chat' : 'Hapus chat'}
              </button>
            </div>

            {confirm ? (
              <div className="mt-3 rounded-lg border border-danger/30 bg-danger-soft/40 p-3">
                <p className="text-xs text-ink">
                  {confirm === 'leave'
                    ? 'Keluar dari grup ini? Untuk kembali, nomor ini harus diundang lagi.'
                    : stillMember
                      ? 'Keluar dari grup dan hapus chatnya? Seluruh pesan di percakapan ini ikut hilang, dan angka performanya tidak bisa dikembalikan.'
                      : 'Hapus chat grup ini? Seluruh pesannya ikut hilang, dan angka performanya tidak bisa dikembalikan.'}
                </p>
                <div className="mt-2 flex gap-2">
                  <button
                    type="button"
                    disabled={busy}
                    onClick={() => setConfirm(null)}
                    className="rounded-md px-2.5 py-1 text-xs text-wa-text-2 hover:bg-wa-active disabled:opacity-50"
                  >
                    Batal
                  </button>
                  <button
                    type="button"
                    disabled={busy}
                    onClick={() => void leave(confirm === 'leave-remove')}
                    className="rounded-md bg-danger px-2.5 py-1 text-xs font-medium text-white disabled:opacity-50"
                  >
                    {busy ? 'Memproses…' : 'Ya, lanjutkan'}
                  </button>
                </div>
              </div>
            ) : null}
          </section>
        </div>
      </aside>

    </>
  );
}

/**
 * One participant, read only.
 *
 * Promoting and removing people used to live here behind a menu on every row.
 * It moved to the message bubble: an operator reaches for it the moment
 * somebody misbehaves, and by then they are looking at that person's message,
 * not hunting for their name among several hundred.
 */
function MemberRow({ member }: { member: GroupMember }) {
  return (
    <div className="relative flex items-center gap-3 rounded-xl px-2 py-2 hover:bg-wa-active">
      <span className="grid size-10 shrink-0 place-items-center rounded-full bg-wa-accent text-sm font-medium text-white">
        {initials(member.display_name)}
      </span>

      <span className="min-w-0 flex-1">
        <span className="flex items-center gap-1.5">
          {/* Empty when nobody has saved them. The LID WhatsApp addresses
              participants by is not a name and does not belong here. */}
          <span className="truncate text-sm text-wa-text">
            {member.display_name || <span className="text-wa-text-2 italic">Belum tersimpan</span>}
          </span>
          {member.is_admin ? (
            <span className="shrink-0 rounded-sm bg-wa-accent/15 px-1.5 py-px text-2xs font-medium text-wa-accent">
              Admin
            </span>
          ) : null}
        </span>
        {member.phone_number ? (
          <span className="block truncate text-xs text-wa-text-2">+{member.phone_number}</span>
        ) : null}
      </span>
    </div>
  );
}
