'use client';

import { useMemo } from 'react';
import useSWR from 'swr';

import { Modal } from '@/components/ui/Modal';
import { EmptyState, ErrorNote, Spinner } from '@/components/ui/Primitives';
import { fetcher, groupMemberHistoryPath } from '@/lib/api';
import type { GroupMemberHistory } from '@/lib/api';

const DAYS = 30;

/**
 * A group's head count, day by day.
 *
 * Opened from the member count itself, because the count is the question this
 * answers: the number on the row says where the group is now, and this says how
 * it got there.
 *
 * Two things are drawn, not one. The line is the size of the group, which is
 * what anyone means by "is it growing". The bars underneath are the churn that
 * produced it, and they are the part a single number hides: a group that gained
 * thirty and lost thirty is not the same group as one where nobody moved, and
 * only one of those is worth asking about.
 */
export function MemberHistoryPanel({
  open,
  onClose,
  chatJid,
  groupName,
}: {
  open: boolean;
  onClose: () => void;
  chatJid: string;
  groupName: string;
}) {
  const { data, isLoading, error } = useSWR<GroupMemberHistory>(
    open ? groupMemberHistoryPath(chatJid, DAYS) : null,
    fetcher,
    { revalidateOnFocus: false },
  );

  const days = useMemo(() => data?.days ?? [], [data]);

  const scale = useMemo(() => {
    if (days.length === 0) return null;
    const counts = days.map((d) => d.member_count);
    const top = Math.max(...counts);
    const floor = Math.min(...counts);
    // The band is deliberately not anchored at zero. A group of four thousand
    // that gained sixty is a flat line from zero and a visible climb from its
    // own floor, and the climb is the thing being looked at. The axis says
    // where the band starts so the shape cannot be mistaken for the whole.
    const pad = Math.max(1, Math.round((top - floor) * 0.15));
    const lo = Math.max(0, floor - pad);
    const hi = top + pad;
    const churn = Math.max(1, ...days.map((d) => Math.max(d.joined, d.left)));
    return { lo, hi, churn, span: Math.max(1, hi - lo) };
  }, [days]);

  const net = days.reduce((sum, d) => sum + d.joined - d.left, 0);

  return (
    <Modal open={open} onClose={onClose} title={`Anggota per hari · ${groupName}`} size="lg">
      {isLoading ? (
        <div className="grid h-40 place-items-center">
          <Spinner />
        </div>
      ) : error ? (
        <ErrorNote message="Riwayat anggota tidak dapat dimuat." />
      ) : days.length === 0 ? (
        /* Reached only by a group whose member list has never been pulled.
           Everything else has at least today, because the server hands back
           today's head count as a starting line even before anything moves. */
        <EmptyState
          title="Anggota grup ini belum pernah diambil"
          description={
            'Riwayat harian dihitung dari jumlah anggota, jadi grup ini perlu di-fetch ' +
            'dulu. Setelah itu hari pertamanya tercatat sebagai titik awal, dan perubahan ' +
            'dihitung dari sana.'
          }
        />
      ) : (
        <div className="space-y-4">
          {/* The first day is a starting line, not a measurement, and saying so
              is the difference between "this group is flat" and "we only began
              counting today". Nothing before it was ever recorded, so there is
              nothing to compare it against and the panel does not pretend
              otherwise. */}
          {days.length === 1 && net === 0 ? (
            <p className="text-sm text-ink-soft">
              Mulai dihitung hari ini dari{' '}
              <span className="font-medium text-ink">
                {days[0].member_count.toLocaleString('id-ID')} anggota
              </span>
              . Perubahan berikutnya dihitung dari angka ini.
            </p>
          ) : (
            <p className="text-sm text-ink-soft">
              {days.length} hari tercatat ·{' '}
              <span
                className={
                  net === 0 ? 'text-ink-soft' : net > 0 ? 'text-brand-800' : 'text-danger'
                }
              >
                {net > 0 ? `+${net}` : net} anggota
              </span>{' '}
              selama periode ini
            </p>
          )}

          {/* Two points make a shape; one makes a bar whose height is an
              artefact of the padding around it. The table below says the same
              number without pretending to show a trend. */}
          {scale && days.length > 1 ? (
            <div className="rounded-control border border-hairline bg-surface-sunken/40 p-3">
              <div className="flex items-end gap-[3px]" style={{ height: 120 }}>
                {days.map((d) => {
                  const h = ((d.member_count - scale.lo) / scale.span) * 100;
                  return (
                    <div
                      key={d.day}
                      title={`${d.day} · ${d.member_count.toLocaleString('id-ID')} anggota`}
                      className="flex-1 rounded-t-[2px] bg-brand-700/80"
                      style={{ height: `${Math.max(2, h)}%` }}
                    />
                  );
                })}
              </div>
              <p className="mt-1.5 text-2xs text-ink-muted">
                Sumbu mulai dari {scale.lo.toLocaleString('id-ID')}, bukan dari nol, supaya
                perubahan kecil pada grup besar tetap terlihat.
              </p>
            </div>
          ) : null}

          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-hairline text-2xs text-ink-muted uppercase">
                <th className="py-1.5 text-left font-medium">Tanggal</th>
                <th className="py-1.5 text-right font-medium">Masuk</th>
                <th className="py-1.5 text-right font-medium">Keluar</th>
                <th className="py-1.5 text-right font-medium">Total anggota</th>
              </tr>
            </thead>
            <tbody>
              {[...days].reverse().map((d) => (
                <tr key={d.day} className="border-b border-hairline last:border-0">
                  <td className="py-1.5 text-ink-soft">{d.day}</td>
                  <td className="nums py-1.5 text-right text-brand-800">
                    {d.joined > 0 ? `+${d.joined}` : '–'}
                  </td>
                  <td className="nums py-1.5 text-right text-danger">
                    {d.left > 0 ? `−${d.left}` : '–'}
                  </td>
                  <td className="nums py-1.5 text-right font-medium text-ink">
                    {d.member_count.toLocaleString('id-ID')}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </Modal>
  );
}
