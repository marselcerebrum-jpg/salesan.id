'use client';

import clsx from 'clsx';
import {
  Check,
  ChevronDown,
  Crown,
  Loader2,
  Pencil,
  RefreshCw,
  ShieldMinus,
  ShieldPlus,
  UserMinus,
  Users,
  X,
} from 'lucide-react';
import { useCallback, useEffect, useRef, useState } from 'react';

import { ConfirmDialog, useConfirm } from '@/components/ui/ConfirmDialog';
import { ErrorNote } from '@/components/ui/Primitives';
import { listGroupMembers, refreshGroup, updateGroup, updateGroupMember } from '@/lib/api';
import { conversationTitle, initials } from '@/lib/format';
import type { Conversation, GroupMember } from '@/lib/types';

const MAX_DESCRIPTION = 2048;

/**
 * Side panel for a group: description, members, and the admin controls.
 *
 * Which controls appear is decided from `self_is_admin`, but that only governs
 * what is *offered*. WhatsApp is what authorises the change, so a stale flag
 * ends in a refusal the operator can read rather than a button that quietly
 * does nothing.
 */
export function GroupPanel({
  conversation,
  open,
  onClose,
  onConversationChange,
}: {
  conversation: Conversation | null;
  open: boolean;
  onClose: () => void;
  onConversationChange: (conversation: Conversation) => void;
}) {
  const [members, setMembers] = useState<GroupMember[]>([]);
  const [loading, setLoading] = useState(false);
  const [busyJid, setBusyJid] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [editingDesc, setEditingDesc] = useState(false);
  const [desc, setDesc] = useState('');
  const [savingDesc, setSavingDesc] = useState(false);
  const confirm = useConfirm();

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

  async function act(member: GroupMember, action: 'promote' | 'demote' | 'remove') {
    if (!conversationId) return;
    setBusyJid(member.jid);
    setError(null);
    try {
      const { members } = await updateGroupMember(conversationId, member.jid, action);
      setMembers(members);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Perubahan ditolak WhatsApp.');
    } finally {
      setBusyJid(null);
    }
  }

  function askRemove(member: GroupMember) {
    confirm.ask({
      title: `Keluarkan ${member.display_name}?`,
      description:
        'Mereka akan dikeluarkan dari grup di WhatsApp, bukan hanya dari tampilan ini. Untuk masuk lagi mereka perlu diundang.',
      confirmLabel: 'Keluarkan',
      tone: 'danger',
      icon: UserMinus,
      onConfirm: () => act(member, 'remove'),
    });
  }

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
                <MemberRow
                  key={member.jid}
                  member={member}
                  canManage={isAdmin}
                  busy={busyJid === member.jid}
                  onPromote={() => void act(member, 'promote')}
                  onDemote={() => void act(member, 'demote')}
                  onRemove={() => askRemove(member)}
                />
              ))
            )}
          </section>
        </div>
      </aside>

      <ConfirmDialog request={confirm.request} onClose={confirm.close} />
    </>
  );
}

function MemberRow({
  member,
  canManage,
  busy,
  onPromote,
  onDemote,
  onRemove,
}: {
  member: GroupMember;
  canManage: boolean;
  busy: boolean;
  onPromote: () => void;
  onDemote: () => void;
  onRemove: () => void;
}) {
  const [open, setOpen] = useState(false);
  const wrapRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    const onPointerDown = (event: MouseEvent) => {
      if (!wrapRef.current?.contains(event.target as Node)) setOpen(false);
    };
    document.addEventListener('mousedown', onPointerDown);
    return () => document.removeEventListener('mousedown', onPointerDown);
  }, [open]);

  return (
    <div ref={wrapRef} className="relative flex items-center gap-3 rounded-xl px-2 py-2 hover:bg-wa-active">
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

      {canManage ? (
        <button
          type="button"
          onClick={() => setOpen((v) => !v)}
          disabled={busy}
          aria-label={`Kelola ${member.display_name}`}
          aria-expanded={open}
          className="grid size-8 shrink-0 place-items-center rounded-full text-wa-text-2 hover:bg-wa-panel-2 disabled:opacity-40"
        >
          {busy ? <Loader2 className="size-4 animate-spin" /> : <ChevronDown className="size-4" />}
        </button>
      ) : null}

      {open ? (
        <div
          role="menu"
          className="absolute top-full right-2 z-20 w-52 overflow-hidden rounded-xl border border-wa-border bg-wa-panel-2 py-1 shadow-e3"
        >
          {member.is_admin ? (
            <MenuItem icon={ShieldMinus} label="Turunkan dari admin" onClick={() => { setOpen(false); onDemote(); }} />
          ) : (
            <MenuItem icon={ShieldPlus} label="Jadikan admin" onClick={() => { setOpen(false); onPromote(); }} />
          )}
          <MenuItem
            icon={UserMinus}
            label="Keluarkan dari grup"
            danger
            onClick={() => { setOpen(false); onRemove(); }}
          />
        </div>
      ) : null}
    </div>
  );
}

function MenuItem({
  icon: Icon,
  label,
  onClick,
  danger,
}: {
  icon: typeof Crown;
  label: string;
  onClick: () => void;
  danger?: boolean;
}) {
  return (
    <button
      type="button"
      role="menuitem"
      onClick={onClick}
      className={clsx(
        'flex w-full items-center gap-2.5 px-3 py-2.5 text-left text-sm transition-colors',
        danger ? 'text-danger hover:bg-danger-soft' : 'text-wa-text hover:bg-wa-active',
      )}
    >
      <Icon className={clsx('size-4', danger ? '' : 'text-wa-text-2')} />
      {label}
    </button>
  );
}
