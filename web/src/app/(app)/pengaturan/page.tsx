'use client';

import clsx from 'clsx';
import { inputClassSm as inputClass } from '@/components/ui/control';
import {
  AlertTriangle,
  ArrowLeft,
  Briefcase,
  ChevronRight,
  Crown,
  ImageUp,
  MoreHorizontal,
  Pencil,
  Plus,
  Search,
  Trash2,
  User,
  UserPlus,
  Users,
} from 'lucide-react';
import { useEffect, useMemo, useRef, useState } from 'react';
import useSWR from 'swr';

import { AppMark } from '@/components/accounts/AccountCard';
import { ErrorState, StateBadge } from '@/components/analytics/Primitives';
import { Button } from '@/components/ui/Button';
import { ConfirmDialog } from '@/components/ui/ConfirmDialog';
import { Modal } from '@/components/ui/Modal';
import {
  APP_ICON_TYPES,
  createApplication,
  createMember,
  deleteApplication,
  deleteApplicationIcon,
  MAX_APP_ICON_BYTES,
  uploadApplicationIcon,
  fetcher,
  orgMembersPath,
  setMemberActive,
  setMemberAssignments,
  setMemberPassword,
  setOperationalRole,
  type OrgMembersResponse,
} from '@/lib/api';
import type { Application, OperationalRole, OrgMember } from '@/lib/types';

/**
 * Anggota & Peran — who is in the workspace, and what each of them may reach.
 *
 * One screen for one decision made in three parts: an account, a role, and the
 * applications behind it. Splitting them across screens is how somebody ends up
 * with a role and no reach, or reach and no role.
 *
 * Creating an account is a page rather than a row of fields on this one. It
 * carries six inputs and a list of applications, and squeezed into the table's
 * header it read as an afterthought attached to a list — which is the opposite
 * of what it is.
 *
 * Every rule here is enforced by the API and by RLS. What this page does is
 * avoid offering an action that will be refused: a Freelance is only offered
 * their PIC's applications, and an application already held by another PIC is
 * shown as taken rather than as a choice.
 */
export default function SettingsPage() {
  const [error, setError] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);

  const org = useSWR<OrgMembersResponse>(orgMembersPath, fetcher);
  const apps = useSWR<{ applications: Application[] }>('/applications', fetcher);

  const members = useMemo(() => org.data?.members ?? [], [org.data]);
  const isLeader = org.data?.scope.is_leader ?? false;
  // A PIC can hire into their own team, so "may create" is not the same
  // question as "is a Leader".
  const creatable = org.data?.creatable_roles ?? [];

  /*
   * Whose password this caller may reset: the same people they may manage.
   *
   * A Leader, anybody. A PIC, the Freelance in their own team — which is exactly
   * the Freelance whose ids are in the PIC's scope. The server checks the same
   * rule again; this only decides whether the menu offers it.
   */
  const scopeIds = useMemo(() => new Set(org.data?.scope.admin_ids ?? []), [org.data]);
  const canResetPassword = (m: OrgMember) =>
    isLeader ||
    (org.data?.scope.role === 'pic' && m.role === 'freelance' && scopeIds.has(m.user_id));

  async function refreshMembers(next: OrgMember[]) {
    if (!org.data) return;
    await org.mutate({ ...org.data, members: next }, { revalidate: false });
  }

  async function changeRole(userId: string, role: OperationalRole) {
    setError(null);
    try {
      const { members: next } = await setOperationalRole(userId, role);
      await refreshMembers(next);
      await apps.mutate();
    } catch (err) {
      setError(message(err, 'Peran gagal disimpan.'));
    }
  }

  async function changeAssignments(
    userId: string,
    applicationIds: string[],
    picUserId: string | null,
  ) {
    setError(null);
    try {
      const { members: next } = await setMemberAssignments(userId, {
        application_ids: applicationIds,
        pic_user_id: picUserId,
      });
      await refreshMembers(next);
    } catch (err) {
      setError(message(err, 'Penugasan gagal disimpan.'));
    }
  }

  async function changeActive(userId: string, active: boolean) {
    setError(null);
    try {
      const { members: next } = await setMemberActive(userId, active);
      await refreshMembers(next);
    } catch (err) {
      setError(message(err, 'Status anggota gagal diubah.'));
    }
  }

  if (creating) {
    return (
      <CreateMemberPage
        members={members}
        applications={apps.data?.applications ?? []}
        creatableRoles={creatable}
        adminAvailable={org.data?.admin_available ?? false}
        isLeader={isLeader}
        onDone={async (next) => {
          await refreshMembers(next);
          setCreating(false);
        }}
        onCancel={() => setCreating(false)}
      />
    );
  }

  return (
    <div className="px-5 py-6 lg:px-8 lg:py-8">
      <Breadcrumb trail={['Pengaturan', 'Anggota & Peran']} />

      <header className="mt-1 flex flex-wrap items-start justify-between gap-x-4 gap-y-3">
        <div className="min-w-0">
          <h1 className="text-2xl font-semibold tracking-[-0.02em] text-ink">Anggota & Peran</h1>
          <p className="mt-1 text-sm text-ink-muted">
            Kelola anggota tim, atur peran, dan akses aplikasi untuk setiap anggota workspace.
          </p>
        </div>

        {creatable.length > 0 ? (
          <Button
            variant="primary"
            icon={<UserPlus className="size-4" />}
            onClick={() => setCreating(true)}
          >
            Buat akun
          </Button>
        ) : null}
      </header>

      {error ? (
        <div className="mt-4 flex items-start gap-2 rounded-card border border-danger/25 bg-danger-soft px-4 py-2.5 text-sm text-danger">
          <AlertTriangle className="mt-0.5 size-4 shrink-0" />
          <span>{error}</span>
        </div>
      ) : null}

      <MemberTotals members={members} />

      <MemberTable
        members={members}
        applications={apps.data?.applications ?? []}
        canEdit={isLeader}
        canResetPassword={canResetPassword}
        adminAvailable={org.data?.admin_available ?? false}
        loading={org.isLoading}
        error={org.error ? message(org.error, 'Gagal memuat.') : null}
        onRetry={() => void org.mutate()}
        onRole={changeRole}
        onAssignments={changeAssignments}
        onActive={changeActive}
      />

      <section id="aplikasi" className="mt-8">
        <h2 className="text-base font-semibold text-ink">Aplikasi</h2>
        <p className="mt-0.5 mb-3 text-sm text-ink-muted">
          Aplikasi adalah puncak hierarki akses: akun WhatsApp, PIC, dan Freelance semuanya melekat
          padanya.
        </p>
        <ApplicationManager
          applications={apps.data?.applications ?? []}
          canEdit={isLeader}
          loading={apps.isLoading}
          onChanged={() => void apps.mutate()}
          onError={setError}
        />
      </section>
    </div>
  );
}

/* --- list ---------------------------------------------------------------- */

function Breadcrumb({ trail }: { trail: string[] }) {
  return (
    <nav aria-label="Jalur" className="flex flex-wrap items-center gap-1 text-2xs text-ink-muted">
      {trail.map((item, i) => (
        <span key={item} className="flex items-center gap-1">
          {i > 0 ? <ChevronRight className="size-3" aria-hidden /> : null}
          <span className={i === trail.length - 1 ? 'text-ink-soft' : undefined}>{item}</span>
        </span>
      ))}
    </nav>
  );
}

/**
 * How the workspace is made up.
 *
 * Four counts rather than a chart: the question is "do we have the people we
 * think we have", and it is answered by four small numbers or not at all.
 */
function MemberTotals({ members }: { members: OrgMember[] }) {
  const counts = {
    total: members.length,
    leader: members.filter((m) => m.role === 'leader').length,
    pic: members.filter((m) => m.role === 'pic').length,
    freelance: members.filter((m) => m.role === 'freelance').length,
  };

  const cards = [
    { icon: Users, tone: 'brand' as const, label: 'Total anggota', value: counts.total },
    { icon: Crown, tone: 'amber' as const, label: 'Leader', value: counts.leader },
    { icon: User, tone: 'info' as const, label: 'PIC', value: counts.pic },
    { icon: Briefcase, tone: 'iris' as const, label: 'Freelance', value: counts.freelance },
  ];

  return (
    <div className="mt-5 grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
      {cards.map((c) => {
        const Icon = c.icon;
        return (
          <div
            key={c.label}
            className="flex items-center gap-3 rounded-card border border-hairline bg-surface-raised px-4 py-3.5 shadow-e1"
          >
            <span
              className={clsx(
                'flex size-10 shrink-0 items-center justify-center rounded-control',
                TILES[c.tone],
              )}
            >
              <Icon className="size-5" aria-hidden />
            </span>
            <span className="min-w-0">
              <span className="nums block text-2xl leading-tight font-semibold text-ink">
                {c.value}
              </span>
              <span className="block truncate text-xs text-ink-muted">{c.label}</span>
            </span>
          </div>
        );
      })}
    </div>
  );
}

/** Icon tints. Identity only: none of them carries a status meaning. */
const TILES = {
  brand: 'bg-brand-600/12 text-brand-700',
  info: 'bg-info-soft text-info',
  amber: 'bg-warn-soft text-amber-badge',
  iris: 'bg-iris-soft text-iris',
} as const;

function MemberTable({
  members,
  applications,
  canEdit,
  canResetPassword,
  adminAvailable,
  loading,
  error,
  onRetry,
  onRole,
  onAssignments,
  onActive,
}: {
  members: OrgMember[];
  applications: Application[];
  canEdit: boolean;
  canResetPassword: (member: OrgMember) => boolean;
  adminAvailable: boolean;
  loading: boolean;
  error: string | null;
  onRetry: () => void;
  onRole: (userId: string, role: OperationalRole) => void;
  onAssignments: (userId: string, applicationIds: string[], picUserId: string | null) => void;
  onActive: (userId: string, active: boolean) => void;
}) {
  const [term, setTerm] = useState('');

  const shown = useMemo(() => {
    const q = term.trim().toLowerCase();
    if (!q) return members;
    return members.filter((m) =>
      `${m.full_name ?? ''} ${m.email}`.toLowerCase().includes(q),
    );
  }, [members, term]);

  if (error) {
    return (
      <div className="mt-4">
        <ErrorState message={error} onRetry={onRetry} />
      </div>
    );
  }

  return (
    <section className="mt-4 overflow-hidden rounded-card border border-hairline bg-surface-raised shadow-e1">
      <div className="flex flex-wrap items-center justify-between gap-3 border-b border-hairline px-4 py-3">
        <h2 className="text-sm font-semibold text-ink">
          Daftar anggota
          <span className="nums ml-2 font-normal text-ink-muted">{shown.length}</span>
        </h2>
        <label className="relative block w-full max-w-xs">
          <Search className="pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-ink-muted" />
          <input
            value={term}
            onChange={(e) => setTerm(e.target.value)}
            placeholder="Cari anggota…"
            aria-label="Cari anggota"
            className={clsx(inputClass, 'w-full pl-8')}
          />
        </label>
      </div>

      {loading ? (
        <div className="px-4 py-4">
          <RowSkeleton />
        </div>
      ) : shown.length === 0 ? (
        <p className="px-4 py-10 text-center text-sm text-ink-muted">
          {members.length === 0 ? 'Belum ada anggota.' : 'Tidak ada anggota yang cocok.'}
        </p>
      ) : (
        <div className="max-h-[70vh] overflow-auto">
          <table className="w-full min-w-[980px] border-collapse text-xs">
            <thead>
              <tr className="text-left text-2xs tracking-wide text-ink-muted uppercase">
                {['Anggota', 'Peran', 'PIC', 'Akses Aplikasi', 'Status', ''].map((h, i) => (
                  <th
                    key={h || i}
                    className={clsx(
                      'sticky top-0 z-10 border-b border-hairline bg-surface-raised px-3 py-2.5 font-semibold',
                      h === '' && 'w-12',
                    )}
                  >
                    {h || <span className="sr-only">Aksi</span>}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {shown.map((m) => (
                <MemberRow
                  key={m.user_id}
                  member={m}
                  members={members}
                  applications={applications}
                  canEdit={canEdit}
                  canResetPassword={canResetPassword(m)}
                  adminAvailable={adminAvailable}
                  onRole={onRole}
                  onAssignments={onAssignments}
                  onActive={onActive}
                />
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
}

function MemberRow({
  member,
  members,
  applications,
  canEdit,
  canResetPassword,
  adminAvailable,
  onRole,
  onAssignments,
  onActive,
}: {
  member: OrgMember;
  members: OrgMember[];
  applications: Application[];
  canEdit: boolean;
  canResetPassword: boolean;
  adminAvailable: boolean;
  onRole: (userId: string, role: OperationalRole) => void;
  onAssignments: (userId: string, applicationIds: string[], picUserId: string | null) => void;
  onActive: (userId: string, active: boolean) => void;
}) {
  const [editing, setEditing] = useState(false);
  const [changingPassword, setChangingPassword] = useState(false);
  const assigned = new Set(member.applications.map((a) => a.id));
  const pics = members.filter((m) => m.role === 'pic' && m.is_active);

  /*
   * Applications another PIC already holds.
   *
   * One application has exactly one PIC, so offering a taken one to a second
   * PIC is offering a request the server refuses. This member's own
   * applications are not in the map: they are what the dialog is editing.
   */
  const heldByOther = new Map<string, string>();
  for (const m of members) {
    if (m.role !== 'pic' || m.user_id === member.user_id) continue;
    for (const a of m.applications) heldByOther.set(a.id, m.full_name ?? m.email);
  }
  // A Leader already sees the whole workspace, so an application list for them
  // would suggest a narrowing that does not exist.
  const scoped = member.role === 'pic' || member.role === 'freelance';

  // A Freelance works inside their PIC's remit. Once a PIC is chosen, the only
  // applications on offer are that PIC's: anything else is refused by the API,
  // so offering it would be offering a button that fails.
  const pic =
    member.role === 'freelance' && member.pic_user_id
      ? members.find((m) => m.user_id === member.pic_user_id)
      : undefined;
  const picApps = pic ? new Set(pic.applications.map((a) => a.id)) : null;

  // Anything already assigned but outside the PIC's set stays visible rather
  // than being hidden while still in force: it is in effect until somebody
  // removes it, and a permission nobody can see is a permission nobody audits.
  const offered = picApps
    ? applications.filter((a) => picApps.has(a.id) || assigned.has(a.id))
    : applications;

  /** Drops whatever the new PIC does not hold, so the pair is never in conflict. */
  function changePIC(nextPICUserID: string) {
    const next = members.find((m) => m.user_id === nextPICUserID);
    const allow = next ? new Set(next.applications.map((a) => a.id)) : null;
    const kept = allow ? [...assigned].filter((id) => allow.has(id)) : [...assigned];
    onAssignments(member.user_id, kept, nextPICUserID || null);
  }

  return (
    <tr
      className={clsx(
        'border-b border-hairline align-top last:border-0',
        !member.is_active && 'opacity-60',
      )}
    >
      <td className="px-3 py-3">
        <div className="flex min-w-0 items-center gap-2.5">
          <Avatar name={member.full_name ?? member.email} />
          <div className="min-w-0">
            <p className="truncate font-medium text-ink">{member.full_name ?? member.email}</p>
            <p className="truncate text-2xs text-ink-muted">{member.email}</p>
          </div>
        </div>
      </td>

      <td className="px-3 py-3">
        {canEdit && member.is_active ? (
          <select
            value={member.role}
            onChange={(e) => onRole(member.user_id, e.target.value as OperationalRole)}
            aria-label={`Peran ${member.full_name ?? member.email}`}
            className="h-9 rounded-control border border-hairline bg-surface-raised px-2 text-xs outline-none focus:border-brand-600"
          >
            <option value="" disabled>
              Belum ditetapkan
            </option>
            <option value="leader">Leader</option>
            <option value="pic">PIC</option>
            <option value="freelance">Freelance</option>
          </select>
        ) : (
          <StateBadge
            label={member.role ? ROLE_LABEL[member.role] : 'Belum ditetapkan'}
            tone={member.role === 'leader' ? 'good' : 'info'}
          />
        )}
      </td>

      <td className="px-3 py-3">
        {member.role === 'freelance' ? (
          canEdit && member.is_active ? (
            <select
              value={member.pic_user_id ?? ''}
              onChange={(e) => changePIC(e.target.value)}
              aria-label={`PIC untuk ${member.full_name ?? member.email}`}
              className="h-9 rounded-control border border-hairline bg-surface-raised px-2 text-xs outline-none focus:border-brand-600"
            >
              <option value="">Tanpa PIC</option>
              {pics.map((p) => (
                <option key={p.user_id} value={p.user_id}>
                  {p.full_name ?? p.email}
                </option>
              ))}
            </select>
          ) : (
            <span className="text-ink-soft">{member.pic_name ?? '-'}</span>
          )
        ) : member.role === 'pic' ? (
          <span className="text-ink-muted">{member.freelance_count} Freelance</span>
        ) : (
          <span className="text-ink-muted">-</span>
        )}
      </td>

      {/* What this account actually holds — not every application there is.
          The full list belongs in the dialog, where choosing happens; here it
          buried the two applications somebody holds among the ten they do
          not. */}
      <td className="px-3 py-3">
        {!scoped ? (
          <span className="text-ink-muted">Seluruh workspace</span>
        ) : member.role === 'freelance' && !member.pic_user_id ? (
          <span className="text-ink-muted">Pilih PIC dulu</span>
        ) : (
          <div className="flex max-w-[460px] flex-wrap items-center gap-1.5">
            {member.applications.length === 0 ? (
              <span className="text-ink-muted">Belum ada aplikasi</span>
            ) : (
              member.applications.map((a) => {
                // Assigned, but the PIC does not hold it. Marked rather than
                // hidden: it is in force until somebody removes it, and a
                // permission nobody can see is a permission nobody audits.
                const stray = Boolean(picApps && !picApps.has(a.id));
                return (
                  <span
                    key={a.id}
                    title={stray ? 'Di luar aplikasi PIC-nya. Lepas penugasan ini.' : a.name}
                    className={clsx(
                      'rounded-full border px-2 py-0.5 text-2xs',
                      stray
                        ? 'border-warn/40 bg-warn-soft text-warn'
                        : 'border-brand-600/30 bg-brand-600/10 text-brand-700',
                    )}
                  >
                    {a.code}
                  </span>
                );
              })
            )}

            {canEdit && member.is_active ? (
              <button
                type="button"
                onClick={() => setEditing(true)}
                className="inline-flex items-center gap-1 rounded-full border border-hairline px-2 py-0.5 text-2xs text-ink-soft transition-colors hover:bg-surface-sunken"
              >
                <Pencil className="size-3" aria-hidden />
                Edit
              </button>
            ) : null}
          </div>
        )}

        {editing ? (
          <AppAccessDialog
            member={member}
            offered={offered}
            takenBy={member.role === 'pic' ? (id) => heldByOther.get(id) : undefined}
            onClose={() => setEditing(false)}
            onSave={(ids) => {
              setEditing(false);
              onAssignments(member.user_id, ids, member.pic_user_id);
            }}
          />
        ) : null}
      </td>

      {/* State, not an action. The button that changes it lives under Aksi,
          where the other things somebody can do to this row will live too. */}
      <td className="px-3 py-3">
        <StateBadge
          label={member.is_active ? 'Aktif' : 'Nonaktif'}
          tone={member.is_active ? 'good' : 'neutral'}
        />
      </td>

      <td className="px-3 py-3">
        {canEdit || canResetPassword ? (
          <RowMenu
            label={`Aksi untuk ${member.full_name ?? member.email}`}
            items={[
              ...(canResetPassword
                ? [{ label: 'Ganti kata sandi', onClick: () => setChangingPassword(true) }]
                : []),
              ...(canEdit
                ? [
                    {
                      label: member.is_active ? 'Nonaktifkan akun' : 'Aktifkan akun',
                      tone: member.is_active ? ('danger' as const) : ('default' as const),
                      onClick: () => onActive(member.user_id, !member.is_active),
                    },
                  ]
                : []),
            ]}
          />
        ) : null}

        {changingPassword ? (
          <PasswordDialog
            member={member}
            adminAvailable={adminAvailable}
            onClose={() => setChangingPassword(false)}
          />
        ) : null}
      </td>
    </tr>
  );
}

/** Initials on a tinted disc, so a row is recognised before it is read. */
function Avatar({ name }: { name: string }) {
  const initials = name
    .split(/\s+/)
    .slice(0, 2)
    .map((w) => w[0] ?? '')
    .join('')
    .toUpperCase();
  return (
    <span className="flex size-8 shrink-0 items-center justify-center rounded-full bg-brand-600/12 text-2xs font-semibold text-brand-700">
      {initials || '?'}
    </span>
  );
}

/** The row's own menu: one button, and what it can do behind it. */
function RowMenu({
  label,
  items,
}: {
  label: string;
  items: { label: string; tone?: 'default' | 'danger'; onClick: () => void }[];
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
        className="rounded-control p-1.5 text-ink-muted transition-colors hover:bg-surface-sunken hover:text-ink"
      >
        <MoreHorizontal className="size-4" />
      </button>

      {open ? (
        <div className="absolute right-0 z-30 mt-1 w-48 rounded-card border border-hairline bg-surface-raised p-1 shadow-e2">
          {items.map((item) => (
            <button
              key={item.label}
              type="button"
              onClick={() => {
                setOpen(false);
                item.onClick();
              }}
              className={clsx(
                'block w-full rounded-control px-2.5 py-2 text-left text-xs transition-colors',
                item.tone === 'danger'
                  ? 'text-danger hover:bg-danger-soft'
                  : 'text-ink-soft hover:bg-surface-sunken',
              )}
            >
              {item.label}
            </button>
          ))}
        </div>
      ) : null}
    </div>
  );
}

/**
 * Which applications an account may reach.
 *
 * One picker, two places: the create page and the dialog that edits an existing
 * member. They ask the same question under the same rules, and two copies of it
 * would be two places for those rules to drift.
 *
 * An application somebody else already holds is shown and disabled rather than
 * hidden. "Who has it" is an answerable question; "why is it not in the list"
 * is not.
 */
function AppAccessPicker({
  offered,
  picked,
  onChange,
  takenBy,
}: {
  offered: Application[];
  picked: string[];
  onChange: (next: string[]) => void;
  /** Names the PIC already holding an application, when that blocks it. */
  takenBy?: (applicationId: string) => string | undefined;
}) {
  const selectable = offered.filter((a) => !takenBy?.(a.id));
  const allPicked = selectable.length > 0 && selectable.every((a) => picked.includes(a.id));

  return (
    <>
      {selectable.length > 0 ? (
        <label className="flex items-center gap-1.5 text-2xs text-ink-soft">
          <input
            type="checkbox"
            checked={allPicked}
            onChange={() =>
              onChange(allPicked ? [] : selectable.map((a) => a.id))
            }
            className="size-3.5 accent-brand-700"
          />
          Pilih semua
        </label>
      ) : null}

      <div className="mt-3 grid gap-2 sm:grid-cols-2 xl:grid-cols-3">
        {offered.map((a) => {
          const on = picked.includes(a.id);
          const taken = takenBy?.(a.id);
          return (
            <label
              key={a.id}
              title={taken ? `Sedang dipegang ${taken}` : a.name}
              className={clsx(
                'flex items-center gap-2 rounded-control border px-2.5 py-2 transition-colors',
                taken
                  ? 'cursor-not-allowed border-hairline bg-surface-sunken/50 opacity-60'
                  : on
                    ? 'cursor-pointer border-brand-600/40 bg-brand-600/[0.07]'
                    : 'cursor-pointer border-hairline hover:bg-surface-sunken/50',
              )}
            >
              <input
                type="checkbox"
                checked={on}
                disabled={Boolean(taken)}
                onChange={() =>
                  onChange(on ? picked.filter((x) => x !== a.id) : [...picked, a.id])
                }
                className="size-3.5 shrink-0 accent-brand-700"
              />
              <AppMark code={a.code} color={a.color} size={22} />
              <span className="min-w-0 flex-1 truncate text-xs text-ink-soft">{a.code}</span>
              {taken ? <span className="shrink-0 text-2xs text-ink-muted">dipegang</span> : null}
            </label>
          );
        })}
      </div>
    </>
  );
}

/**
 * Editing one member's applications.
 *
 * A dialog rather than chips that toggle in place. Toggling wrote every press
 * straight to the server, so granting three applications was three requests and
 * three chances to half-finish; and the row had to list every application in
 * the workspace to offer them, which buried the two this person actually holds
 * among twelve they do not.
 */
function AppAccessDialog({
  member,
  offered,
  takenBy,
  onClose,
  onSave,
}: {
  member: OrgMember;
  offered: Application[];
  takenBy?: (applicationId: string) => string | undefined;
  onClose: () => void;
  onSave: (applicationIds: string[]) => void;
}) {
  const [picked, setPicked] = useState<string[]>(member.applications.map((a) => a.id));

  return (
    <Modal
      open
      onClose={onClose}
      size="lg"
      title={`Akses aplikasi — ${member.full_name ?? member.email}`}
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            Batal
          </Button>
          <Button variant="primary" onClick={() => onSave(picked)}>
            Simpan akses
          </Button>
        </>
      }
    >
      <p className="mb-3 text-xs text-ink-muted">
        {member.role === 'freelance'
          ? 'Hanya aplikasi yang dipegang PIC-nya yang dapat dipilih.'
          : 'Satu aplikasi hanya boleh dipegang satu PIC, jadi yang sudah dipegang PIC lain tidak dapat dipilih di sini.'}
      </p>

      {offered.length === 0 ? (
        <p className="text-sm text-ink-muted">Tidak ada aplikasi yang dapat dipilih.</p>
      ) : (
        <AppAccessPicker
          offered={offered}
          picked={picked}
          onChange={setPicked}
          takenBy={takenBy}
        />
      )}
    </Modal>
  );
}

/**
 * Setting a new password for somebody on the team.
 *
 * No current password is asked for: this is for the teammate who forgot theirs,
 * and they are the one person who cannot supply it. Shown in plain text for the
 * same reason as on the create page — whoever types it is about to hand it over.
 *
 * Nothing about the other sessions is changed here. Supabase keeps a signed-in
 * browser signed in until its token lapses, so the dialog says so rather than
 * implying the old password stops working everywhere at once.
 */
function PasswordDialog({
  member,
  adminAvailable,
  onClose,
}: {
  member: OrgMember;
  adminAvailable: boolean;
  onClose: () => void;
}) {
  const [password, setPassword] = useState('');
  const [confirm, setConfirm] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [done, setDone] = useState(false);

  const name = member.full_name ?? member.email;

  async function save() {
    setError(null);
    if ([...password].length < 8) {
      setError('Kata sandi minimal 8 karakter.');
      return;
    }
    if (password !== confirm) {
      setError('Konfirmasi kata sandi tidak sama.');
      return;
    }
    setBusy(true);
    try {
      await setMemberPassword(member.user_id, password);
      setDone(true);
    } catch (err) {
      setError(message(err, 'Kata sandi gagal diganti.'));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Modal
      open
      onClose={onClose}
      title={`Ganti kata sandi — ${name}`}
      footer={
        done ? (
          <Button variant="primary" onClick={onClose}>
            Selesai
          </Button>
        ) : (
          <>
            <Button variant="ghost" onClick={onClose}>
              Batal
            </Button>
            <Button
              variant="primary"
              loading={busy}
              disabled={!adminAvailable || password === '' || confirm === ''}
              onClick={() => void save()}
            >
              Simpan kata sandi
            </Button>
          </>
        )
      }
    >
      {done ? (
        <p className="text-sm text-ink-soft">
          Kata sandi <span className="font-medium text-ink">{name}</span> sudah diganti. Serahkan
          kata sandi baru kepada yang bersangkutan; login berikutnya memakai kata sandi ini.
        </p>
      ) : (
        <form
          onSubmit={(e) => {
            e.preventDefault();
            void save();
          }}
          className="space-y-4"
        >
          {!adminAvailable ? (
            <p className="rounded-control border border-warn/30 bg-warn-soft px-3 py-2 text-xs text-ink-soft">
              Belum bisa dipakai: isi{' '}
              <code className="rounded bg-surface-sunken px-1">SUPABASE_SERVICE_ROLE_KEY</code> di{' '}
              <code className="rounded bg-surface-sunken px-1">backend/.env</code> lalu jalankan
              ulang server.
            </p>
          ) : null}

          <p className="text-2xs text-ink-muted">{member.email}</p>

          <Field label="Kata sandi baru" required>
            <input
              type="text"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              minLength={8}
              autoComplete="new-password"
              placeholder="Minimal 8 karakter"
              className={clsx(inputClass, 'w-full')}
            />
          </Field>
          <Field label="Konfirmasi kata sandi" required>
            <input
              type="text"
              value={confirm}
              onChange={(e) => setConfirm(e.target.value)}
              minLength={8}
              autoComplete="new-password"
              placeholder="Ketik ulang kata sandi baru"
              className={clsx(inputClass, 'w-full')}
            />
          </Field>

          {error ? <p className="text-xs text-danger">{error}</p> : null}

          <p className="text-2xs leading-relaxed text-ink-muted">
            Kata sandi lama tidak diperlukan. Perangkat yang sedang login tetap masuk sampai sesinya
            berakhir.
          </p>
          {/* Lets Enter submit without a visible second button. */}
          <button type="submit" hidden />
        </form>
      )}
    </Modal>
  );
}

/* --- create -------------------------------------------------------------- */

/**
 * Creating an account: the identity, and what it may reach.
 *
 * Two cards because they are two questions. The left one is about a person; the
 * right one is about the workspace, and it is the half people forget — an
 * account with a role and no applications opens the product to an empty screen.
 *
 * What is offered is what the API will accept. A Freelance is shown their PIC's
 * applications and nothing else; a PIC is not offered an application another PIC
 * already holds, because one application has exactly one PIC and the request
 * would be refused. The rules are not restated here as text, they are the
 * options.
 */
function CreateMemberPage({
  members,
  applications,
  creatableRoles,
  adminAvailable,
  isLeader,
  onDone,
  onCancel,
}: {
  members: OrgMember[];
  applications: Application[];
  creatableRoles: OperationalRole[];
  adminAvailable: boolean;
  isLeader: boolean;
  onDone: (members: OrgMember[]) => Promise<void>;
  onCancel: () => void;
}) {
  const [fullName, setFullName] = useState('');
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [confirm, setConfirm] = useState('');
  const [role, setRole] = useState<OperationalRole>(creatableRoles[0] ?? 'freelance');
  const [picId, setPicId] = useState('');
  const [picked, setPicked] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const pics = members.filter((m) => m.role === 'pic' && m.is_active);

  /*
   * Which applications this account may be given.
   *
   * A Leader reaches everything, so there is nothing to choose. A Freelance is
   * bound to their PIC's set. A PIC may take any application that no other PIC
   * holds — the ones that are taken stay on screen, marked, because "why is it
   * not in the list" is a worse question than "who has it".
   */
  const heldBy = new Map<string, string>();
  for (const m of members) {
    if (m.role !== 'pic') continue;
    for (const a of m.applications) heldBy.set(a.id, m.full_name ?? m.email);
  }

  const picApps = picId
    ? new Set((members.find((m) => m.user_id === picId)?.applications ?? []).map((a) => a.id))
    : null;

  const offered =
    role === 'leader'
      ? []
      : role === 'freelance'
        ? applications.filter((a) => picApps?.has(a.id))
        : applications;

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);

    if (password !== confirm) {
      setError('Konfirmasi kata sandi tidak sama.');
      return;
    }
    if (role === 'freelance' && isLeader && !picId) {
      setError('Freelance harus punya PIC. Pilih PIC-nya dulu.');
      return;
    }

    setBusy(true);
    try {
      const { user_id, members: afterCreate } = await createMember({
        email: email.trim().toLowerCase(),
        full_name: fullName.trim(),
        password,
        role,
      });

      /*
       * Assignments as a second step, on purpose.
       *
       * The endpoint that grants them already enforces every rule about them —
       * a Freelance inside their PIC's remit, one PIC per application — and
       * teaching the create endpoint the same rules would be a second place for
       * them to live. If this half fails the account still exists and the
       * message says so; the assignment can be finished in the table.
       */
      let members = afterCreate;
      if (role !== 'leader' && (picked.length > 0 || picId)) {
        const { members: assigned } = await setMemberAssignments(user_id, {
          application_ids: picked,
          pic_user_id: role === 'freelance' ? picId || null : null,
        });
        members = assigned;
      }
      await onDone(members);
    } catch (err) {
      setError(message(err, 'Akun gagal dibuat.'));
    } finally {
      setBusy(false);
    }
  }

  return (
    <form onSubmit={submit} className="px-5 py-6 lg:px-8 lg:py-8">
      <Breadcrumb trail={['Pengaturan', 'Anggota & Peran']} />

      <header className="mt-1 flex items-start gap-3">
        <button
          type="button"
          onClick={onCancel}
          aria-label="Kembali"
          className="mt-1 rounded-control p-1 text-ink-muted transition-colors hover:bg-surface-sunken hover:text-ink"
        >
          <ArrowLeft className="size-5" />
        </button>
        <div className="min-w-0">
          <h1 className="text-2xl font-semibold tracking-[-0.02em] text-ink">Buat Akun Baru</h1>
          <p className="mt-1 text-sm text-ink-muted">
            Tambahkan anggota baru ke workspace. Akun akan langsung aktif setelah dibuat.
          </p>
        </div>
      </header>

      {!adminAvailable ? (
        <div className="mt-5 flex items-start gap-2.5 rounded-card border border-warn/30 bg-warn-soft px-4 py-3 text-xs text-ink-soft">
          <AlertTriangle className="mt-0.5 size-4 shrink-0 text-warn" />
          <div>
            <p className="font-medium text-warn">Pembuatan akun belum aktif</p>
            <p className="mt-0.5">
              Isi <code className="rounded bg-surface-sunken px-1">SUPABASE_SERVICE_ROLE_KEY</code>{' '}
              di <code className="rounded bg-surface-sunken px-1">backend/.env</code>, lalu jalankan
              ulang server. Membuat akun adalah operasi administratif yang memerlukan kunci itu, dan
              kunci tersebut tidak pernah dikirim ke browser.
            </p>
          </div>
        </div>
      ) : null}

      {error ? (
        <div className="mt-5 flex items-start gap-2 rounded-card border border-danger/25 bg-danger-soft px-4 py-2.5 text-sm text-danger">
          <AlertTriangle className="mt-0.5 size-4 shrink-0" />
          <span>{error}</span>
        </div>
      ) : null}

      <div className="mt-5 grid gap-4 lg:grid-cols-2">
        <section className="rounded-card border border-hairline bg-surface-raised px-4 py-4 shadow-e1">
          <h2 className="text-sm font-semibold text-ink">Informasi Akun</h2>

          <div className="mt-4 grid gap-4 sm:grid-cols-2">
            <Field label="Nama lengkap" required>
              <input
                value={fullName}
                onChange={(e) => setFullName(e.target.value)}
                required
                maxLength={80}
                placeholder="Nama lengkap anggota"
                className={clsx(inputClass, 'w-full')}
              />
            </Field>
            <Field label="Email" required>
              <input
                type="email"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                required
                placeholder="nama@domain.com"
                className={clsx(inputClass, 'w-full')}
              />
            </Field>
            <Field label="Kata sandi" required>
              <input
                type="text"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                required
                minLength={8}
                placeholder="Minimal 8 karakter"
                className={clsx(inputClass, 'w-full')}
              />
            </Field>
            <Field label="Konfirmasi kata sandi" required>
              <input
                type="text"
                value={confirm}
                onChange={(e) => setConfirm(e.target.value)}
                required
                minLength={8}
                placeholder="Minimal 8 karakter"
                className={clsx(inputClass, 'w-full')}
              />
            </Field>
            <Field label="Peran" required>
              <select
                value={role}
                onChange={(e) => {
                  setRole(e.target.value as OperationalRole);
                  setPicked([]);
                }}
                className={clsx(inputClass, 'w-full')}
              >
                {creatableRoles.map((r) => (
                  <option key={r} value={r}>
                    {ROLE_LABEL[r]}
                  </option>
                ))}
              </select>
            </Field>
            {role === 'freelance' && isLeader ? (
              <Field label="PIC" required>
                <select
                  value={picId}
                  onChange={(e) => {
                    setPicId(e.target.value);
                    setPicked([]);
                  }}
                  className={clsx(inputClass, 'w-full')}
                >
                  <option value="">Pilih PIC</option>
                  {pics.map((p) => (
                    <option key={p.user_id} value={p.user_id}>
                      {p.full_name ?? p.email}
                    </option>
                  ))}
                </select>
              </Field>
            ) : null}
          </div>

          {/* The one thing about this form that is not obvious from its fields. */}
          <p className="mt-4 text-2xs leading-relaxed text-ink-muted">
            Kata sandi ditampilkan apa adanya karena Anda yang akan menyerahkannya kepada yang
            bersangkutan. Akun langsung aktif tanpa konfirmasi email, dan masuk ke workspace ini.
          </p>
        </section>

        <section className="flex flex-col rounded-card border border-hairline bg-surface-raised px-4 py-4 shadow-e1">
          <div className="min-w-0">
            <h2 className="text-sm font-semibold text-ink">Akses Aplikasi</h2>
            <p className="mt-0.5 mb-3 text-2xs text-ink-muted">
              Pilih aplikasi yang dapat diakses oleh anggota ini. Bisa memilih lebih dari satu.
            </p>
          </div>

          {role === 'leader' ? (
            <p className="mt-4 text-xs text-ink-muted">
              Leader menjangkau seluruh workspace, jadi tidak ada aplikasi yang perlu dipilih.
            </p>
          ) : role === 'freelance' && isLeader && !picId ? (
            <p className="mt-4 text-xs text-ink-muted">
              Pilih PIC-nya dulu. Freelance hanya dapat ditugaskan ke aplikasi yang dipegang PIC-nya.
            </p>
          ) : offered.length === 0 ? (
            <p className="mt-4 text-xs text-ink-muted">
              {role === 'freelance'
                ? 'PIC ini belum memegang aplikasi mana pun.'
                : 'Belum ada aplikasi. Tambahkan lebih dulu di daftar Aplikasi.'}
            </p>
          ) : (
            <AppAccessPicker
              offered={offered}
              picked={picked}
              onChange={setPicked}
              // One application has one PIC, so a taken one is not on offer.
              takenBy={role === 'pic' ? (id) => heldBy.get(id) : undefined}
            />
          )}
        </section>
      </div>

      <div className="mt-5 flex flex-wrap items-center justify-end gap-2 rounded-card border border-hairline bg-surface-raised px-4 py-3 shadow-e1">
        <Button type="button" variant="ghost" onClick={onCancel}>
          Batal
        </Button>
        <Button type="submit" variant="primary" loading={busy} disabled={!adminAvailable}>
          Simpan akun
        </Button>
      </div>
    </form>
  );
}

/* --- applications -------------------------------------------------------- */

const PALETTE = [
  '#1B7F5A',
  '#C2853A',
  '#166534',
  '#2563EB',
  '#7C3AED',
  '#DC2626',
  '#D97706',
  '#0891B2',
];

function ApplicationManager({
  applications,
  canEdit,
  loading,
  onChanged,
  onError,
}: {
  applications: Application[];
  canEdit: boolean;
  loading: boolean;
  onChanged: () => void;
  onError: (message: string | null) => void;
}) {
  const [code, setCode] = useState('');
  const [color, setColor] = useState(PALETTE[0]);
  const [busy, setBusy] = useState<string | null>(null);
  const [pendingDelete, setPendingDelete] = useState<Application | null>(null);

  async function add(e: React.FormEvent) {
    e.preventDefault();
    const trimmed = code.trim().toUpperCase();
    if (!trimmed) return;
    onError(null);
    setBusy('add');
    try {
      await createApplication({ code: trimmed, name: trimmed, color });
      setCode('');
      onChanged();
    } catch (err) {
      onError(message(err, 'Aplikasi gagal ditambahkan.'));
    } finally {
      setBusy(null);
    }
  }

  async function remove(app: Application) {
    onError(null);
    setBusy(app.id);
    try {
      await deleteApplication(app.id);
      onChanged();
    } catch (err) {
      onError(message(err, 'Aplikasi gagal dihapus.'));
    } finally {
      setBusy(null);
      setPendingDelete(null);
    }
  }

  return (
    <>
      {canEdit ? (
        <form
          onSubmit={add}
          className="rounded-card border border-hairline bg-surface-raised px-4 py-3.5"
        >
          <div className="flex flex-wrap items-end gap-3">
            <Field label="Kode aplikasi">
              <input
                value={code}
                onChange={(e) => setCode(e.target.value)}
                placeholder="Contoh: JADIASN"
                maxLength={32}
                className={clsx(inputClass, 'min-w-[200px] uppercase placeholder:normal-case')}
              />
            </Field>
            <div className="flex flex-col gap-1">
              <span className="text-2xs font-medium tracking-wide text-ink-muted uppercase">
                Warna
              </span>
              <div className="flex h-9 flex-wrap items-center gap-1.5">
                {PALETTE.map((hex) => (
                  <button
                    key={hex}
                    type="button"
                    onClick={() => setColor(hex)}
                    aria-label={`Warna ${hex}`}
                    aria-pressed={color === hex}
                    style={{ backgroundColor: hex }}
                    className={clsx(
                      'size-6 rounded-full transition-transform',
                      color === hex ? 'ring-2 ring-ink ring-offset-2' : 'hover:scale-110',
                    )}
                  />
                ))}
              </div>
            </div>
            <Button
              type="submit"
              variant="primary"
              icon={<Plus className="size-4" />}
              loading={busy === 'add'}
            >
              Tambah
            </Button>
          </div>
        </form>
      ) : (
        <p className="rounded-card border border-hairline bg-surface-raised px-4 py-3 text-xs text-ink-muted">
          Hanya Leader yang dapat menambah atau menghapus aplikasi.
        </p>
      )}

      <div className="mt-3">
        {loading ? (
          <RowSkeleton />
        ) : applications.length === 0 ? (
          <p className="rounded-card border border-dashed border-hairline-strong bg-surface-raised px-4 py-8 text-center text-sm text-ink-muted">
            Belum ada aplikasi.
          </p>
        ) : (
          <ul className="divide-y divide-hairline overflow-hidden rounded-card border border-hairline bg-surface-raised">
            {applications.map((app) => (
              <li key={app.id} className="flex flex-wrap items-center gap-3 px-4 py-3">
                <AppMark code={app.code} color={app.color} size={36} />
                <div className="min-w-0 flex-1">
                  <p className="truncate text-base font-medium text-ink">{app.name}</p>
                  <p className="text-xs text-ink-muted">
                    {app.account_count} nomor · {app.conversation_count} percakapan
                  </p>
                </div>
                {canEdit ? (
                  <LogoControls
                    app={app}
                    onChanged={onChanged}
                    onError={onError}
                  />
                ) : null}
                {canEdit ? (
                  <Button
                    variant="ghost"
                    size="sm"
                    aria-label={`Hapus ${app.name}`}
                    loading={busy === app.id}
                    onClick={() => setPendingDelete(app)}
                    className="text-danger hover:bg-danger-soft"
                    icon={<Trash2 className="size-4" />}
                  />
                ) : null}
              </li>
            ))}
          </ul>
        )}

        <p className="mt-2 flex items-start gap-1.5 text-2xs text-ink-muted">
          <Users className="mt-0.5 size-3.5 shrink-0" />
          Menghapus aplikasi tidak menghapus akun WhatsApp-nya. Akun itu hanya menjadi &ldquo;Tanpa
          aplikasi&rdquo;. Penugasan PIC dan Freelance ke aplikasi tersebut ikut terhapus.
        </p>
      </div>

      <ConfirmDialog
        request={
          pendingDelete
            ? {
                title: `Hapus ${pendingDelete.name}?`,
                description: `${pendingDelete.account_count} akun WhatsApp menjadi "Tanpa aplikasi". Tidak ada yang terputus dari WhatsApp. Penugasan PIC dan Freelance ke aplikasi ini ikut terhapus.`,
                confirmLabel: 'Hapus aplikasi',
                tone: 'danger',
                icon: Trash2,
                onConfirm: () => remove(pendingDelete),
              }
            : null
        }
        onClose={() => setPendingDelete(null)}
      />
    </>
  );
}

/**
 * Upload or remove one application's logo.
 *
 * The logo replaces the two-letter tile everywhere the application appears —
 * the chat list, the dashboard, the accounts page — so it is set once, here.
 * The file is checked in the browser for a quick answer and again on the server
 * from its own bytes, which is the check that counts.
 */
function LogoControls({
  app,
  onChanged,
  onError,
}: {
  app: Application;
  onChanged: () => void;
  onError: (message: string | null) => void;
}) {
  const input = useRef<HTMLInputElement>(null);
  const [busy, setBusy] = useState(false);

  async function upload(file: File) {
    onError(null);
    if (!APP_ICON_TYPES.split(',').includes(file.type)) {
      onError('Logo harus berupa gambar PNG, JPG, atau WebP.');
      return;
    }
    if (file.size > MAX_APP_ICON_BYTES) {
      onError('Logo melebihi batas 2 MB.');
      return;
    }
    setBusy(true);
    try {
      await uploadApplicationIcon(app.id, file);
      onChanged();
    } catch (err) {
      onError(message(err, 'Logo gagal diunggah.'));
    } finally {
      setBusy(false);
    }
  }

  async function remove() {
    onError(null);
    setBusy(true);
    try {
      await deleteApplicationIcon(app.id);
      onChanged();
    } catch (err) {
      onError(message(err, 'Logo gagal dihapus.'));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="flex shrink-0 items-center gap-1">
      <input
        ref={input}
        type="file"
        accept={APP_ICON_TYPES}
        hidden
        onChange={(e) => {
          const file = e.target.files?.[0];
          e.target.value = ''; // the same file chosen twice still fires
          if (file) void upload(file);
        }}
      />
      <Button
        variant="ghost"
        size="sm"
        loading={busy}
        icon={<ImageUp className="size-4" />}
        onClick={() => input.current?.click()}
      >
        {app.icon_url ? 'Ganti logo' : 'Unggah logo'}
      </Button>
      {app.icon_url && !busy ? (
        <Button variant="ghost" size="sm" onClick={() => void remove()} className="text-ink-muted">
          Hapus logo
        </Button>
      ) : null}
    </div>
  );
}

/* --- shared -------------------------------------------------------------- */

const ROLE_LABEL: Record<OperationalRole, string> = {
  leader: 'Leader',
  pic: 'PIC',
  freelance: 'Freelance',
};

function Field({
  label,
  required,
  children,
}: {
  label: string;
  required?: boolean;
  children: React.ReactNode;
}) {
  return (
    <label className="flex min-w-0 flex-col gap-1">
      <span className="text-2xs font-medium tracking-wide text-ink-muted uppercase">
        {label}
        {required ? <span className="text-danger"> *</span> : null}
      </span>
      {children}
    </label>
  );
}

function RowSkeleton() {
  return (
    <div className="space-y-2">
      {Array.from({ length: 3 }).map((_, i) => (
        <div key={i} className="h-14 animate-pulse rounded-card bg-surface-sunken" />
      ))}
    </div>
  );
}

function message(err: unknown, fallback: string): string {
  return err instanceof Error ? err.message : fallback;
}
