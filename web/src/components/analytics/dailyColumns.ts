import { formatDuration, formatHours } from '@/components/analytics/Primitives';
import type { PerformanceDay } from '@/lib/types';

/**
 * The column model behind Rincian Per Hari.
 *
 * One declaration, three consumers: the two-level header, the body cells and
 * the CSV. Keeping them in one list is the whole point. The previous table
 * built its header in JSX and its CSV in a separate function, which is how a
 * column ends up in the export under the wrong name, or missing from it.
 *
 * A `null` value means the metric does not apply to that day, and is rendered
 * as a dash. Zero means it applies and nothing happened, and is rendered as 0.
 * The two are different facts and the table says so.
 */

export interface DailyColumn {
  key: string;
  label: string;
  /** Shown on the subheader, explaining what the figure counts. */
  info: string;
  value: (d: PerformanceDay) => number | string | null;
  /** For CSV: the raw number, unformatted. Defaults to `value`. */
  raw?: (d: PerformanceDay) => number | string | null;
}

export interface DailyGroup {
  key: string;
  label: string;
  columns: DailyColumn[];
}

const outbound = (d: PerformanceDay) => d.outbound_manual_personal + d.outbound_device_personal;

export const DAILY_GROUPS: DailyGroup[] = [
  {
    key: 'chat',
    label: 'Chat Engagement',
    columns: [
      {
        key: 'contacts_inbound',
        label: 'Kontak Masuk',
        info: 'Kontak unik yang mengirim pesan pribadi pada hari itu. Satu nomor dihitung sekali.',
        value: (d) => d.contacts_inbound,
      },
      {
        key: 'leads',
        label: 'Leads Baru',
        info: 'Kontak yang belum pernah punya percakapan dan belum berlabel, menulis pertama kali.',
        value: (d) => d.verified_new_leads,
      },
      {
        key: 'contacts_served',
        label: 'Kontak Ditangani',
        info: 'Kontak unik yang mendapat minimal satu balasan manual. Dihitung per kontak, bukan per pesan.',
        value: (d) => d.contacts_served,
      },
      {
        key: 'inbound',
        label: 'Pesan Masuk',
        info: 'Jumlah bubble dari pelanggan. Chat pribadi saja, tanpa grup, broadcast, bot, dan sistem.',
        value: (d) => d.inbound_personal,
      },
      {
        key: 'outbound',
        label: 'Pesan Keluar',
        info: 'Bubble keluar ke pelanggan, dari web maupun HP. Broadcast tidak termasuk.',
        value: outbound,
      },
    ],
  },
  {
    key: 'group',
    label: 'Group Engagement',
    columns: [
      {
        key: 'groups_active',
        label: 'Grup Aktif',
        info: 'Grup unik yang menerima minimal satu pesan dari anggota pada hari itu.',
        value: (d) => d.groups_active,
      },
      {
        key: 'groups_handled',
        label: 'Grup Ditangani',
        info: 'Grup unik yang mendapat minimal satu pesan dari admin atau freelance.',
        value: (d) => d.groups_handled,
      },
      {
        key: 'group_in',
        label: 'Pesan Masuk Grup',
        info: 'Bubble dari anggota grup, tidak termasuk pesan dari nomor internal.',
        value: (d) => d.group_inbound,
      },
      {
        key: 'group_out',
        label: 'Pesan Keluar Grup',
        info: 'Bubble yang dikirim admin ke dalam grup. Broadcast ke grup tidak dihitung.',
        value: (d) => d.group_replies,
      },
    ],
  },
  {
    key: 'sla',
    label: 'Performa SLA',
    columns: [
      {
        key: 'avg',
        label: 'Rata-rata Respons',
        info: 'Jarak dari pesan pertama pelanggan sampai balasan manual pertama. Membaca tidak menghentikan hitungan.',
        // Null, not zero: a day with no completed cycle has no average, and
        // printing 0 would read as "answered instantly".
        value: (d) => (d.avg_first_response_seconds === null ? null : formatDuration(d.avg_first_response_seconds)),
        raw: (d) => d.avg_first_response_seconds,
      },
      {
        key: 'fastest',
        label: 'Respons Tercepat',
        info: 'Dari siklus yang sudah dibalas pada hari itu.',
        value: (d) => (d.fastest_response_seconds === null ? null : formatDuration(d.fastest_response_seconds)),
        raw: (d) => d.fastest_response_seconds,
      },
      {
        key: 'slowest',
        label: 'Respons Terlama',
        info: 'Dari siklus yang sudah dibalas. Yang masih menunggu dilaporkan terpisah.',
        value: (d) => (d.slowest_response_seconds === null ? null : formatDuration(d.slowest_response_seconds)),
        raw: (d) => d.slowest_response_seconds,
      },
      {
        key: 'sla_ratio',
        label: 'SLA Tercapai',
        info: 'Siklus yang dibalas dalam batas waktu yang berlaku saat siklus itu dibuat, dibanding siklus yang sudah dibalas.',
        value: (d) => (d.sla_completed > 0 ? `${d.sla_achieved}/${d.sla_completed}` : null),
        raw: (d) => (d.sla_completed > 0 ? `${d.sla_achieved}/${d.sla_completed}` : null),
      },
      {
        key: 'sla_waiting',
        label: 'Menunggu Balasan',
        info: 'Siklus yang dimulai pada hari itu dan sampai sekarang belum mendapat balasan manual pertama. Bukan jumlah yang menunggu saat ini.',
        value: (d) => d.sla_waiting,
      },
    ],
  },
  {
    key: 'followup',
    label: 'Follow-up Status',
    columns: [
      {
        key: 'fu_contacts',
        label: 'Kontak Di-follow-up',
        info: 'Kontak unik yang dihubungi kembali pada hari itu.',
        value: (d) => d.follow_up_contacts,
      },
      {
        key: 'fu_done',
        label: 'Sudah Di-follow-up',
        info: 'Jumlah aktivitas follow-up. Satu kontak bisa di-follow-up lebih dari sekali.',
        value: (d) => d.follow_ups,
      },
      {
        key: 'fu_answered',
        label: 'Sudah Dibalas',
        info: 'Follow-up yang mendapat balasan pelanggan sebelum follow-up berikutnya.',
        value: (d) => d.follow_ups_answered,
      },
      {
        key: 'fu_unanswered',
        label: 'Belum Dibalas',
        info: 'Follow-up yang belum mendapat balasan.',
        value: (d) => d.follow_ups_unanswered,
      },
    ],
  },
  {
    key: 'label',
    label: 'Status Label',
    columns: [
      {
        key: 'first_labeled',
        label: 'Kontak Berlabel',
        info: 'Kontak yang sebelumnya tanpa label sama sekali, lalu mendapat label pertamanya.',
        value: (d) => d.contacts_first_labeled,
      },
      {
        key: 'assigned',
        label: 'Label Dipasang',
        info: 'Berapa kali sebuah label dipasang pada hari itu.',
        value: (d) => d.labels_assigned,
      },
      {
        key: 'removed',
        label: 'Label Dilepas',
        info: 'Berapa kali sebuah label dilepas.',
        value: (d) => d.labels_removed,
      },
      {
        key: 'contacts_changed',
        label: 'Kontak Unik Berubah',
        info: 'Berapa kontak yang labelnya pernah berubah. Satu kontak tetap satu, meski berubah lima kali.',
        value: (d) => d.label_contacts_changed,
      },
      {
        key: 'changes_total',
        label: 'Total Perpindahan',
        info: 'Seluruh aktivitas perpindahan label. Bisa lebih besar dari jumlah kontak.',
        value: (d) => d.label_changes_total,
      },
    ],
  },
  {
    key: 'broadcast',
    label: 'Broadcast',
    columns: [
      {
        key: 'bc_created',
        label: 'Dibuat',
        info: 'Dibukukan pada tanggal campaign dibuat, dan menjadi performa pembuatnya.',
        value: (d) => d.broadcasts_created,
      },
      {
        key: 'bc_scheduled',
        label: 'Dijadwalkan',
        info: 'Dibukukan pada tanggal jadwalnya, bukan tanggal dibuat.',
        value: (d) => d.broadcasts_scheduled,
      },
      {
        key: 'bc_running',
        label: 'Berjalan',
        info: 'Dibukukan pada tanggal eksekusi. Tetap milik pembuatnya, siapa pun yang sedang bertugas.',
        value: (d) => d.broadcasts_running,
      },
      {
        key: 'bc_sent',
        label: 'Selesai',
        info: 'Seluruh target terkirim. Dibukukan pada tanggal eksekusi.',
        value: (d) => d.broadcasts_sent,
      },
      {
        key: 'bc_partial',
        label: 'Selesai Sebagian',
        info: 'Sebagian target gagal atau dibatalkan. Sengaja dipisah dari "selesai" dan "gagal".',
        value: (d) => d.broadcasts_partial,
      },
      {
        key: 'bc_failed',
        label: 'Gagal',
        info: 'Tidak ada satu pun target yang berhasil.',
        value: (d) => d.broadcasts_failed,
      },
      {
        key: 'bc_cancelled',
        label: 'Dibatalkan',
        info: 'Dibatalkan sebelum selesai. Pembatalan oleh orang lain tidak memindahkan kepemilikan.',
        value: (d) => d.broadcasts_cancelled,
      },
      {
        key: 'bc_targets',
        label: 'Target Terkirim',
        info: 'Jumlah penerima, bukan jumlah campaign.',
        value: (d) => d.broadcast_targets_sent,
      },
    ],
  },
  {
    key: 'story',
    label: 'WA Story',
    columns: [
      {
        key: 'st_created',
        label: 'Dibuat',
        info: 'Dibukukan pada tanggal story dibuat.',
        value: (d) => d.stories_created,
      },
      {
        key: 'st_scheduled',
        label: 'Dijadwalkan',
        info: 'Dibukukan pada tanggal jadwalnya.',
        value: (d) => d.stories_scheduled,
      },
      {
        key: 'st_published',
        label: 'Terposting',
        info: 'Terposting di seluruh nomor yang dipilih.',
        value: (d) => d.stories_published,
      },
      {
        key: 'st_partial',
        label: 'Sebagian',
        info: 'Terposting hanya di sebagian nomor.',
        value: (d) => d.stories_partial,
      },
      {
        key: 'st_failed',
        label: 'Gagal',
        info: 'Tidak terposting di nomor mana pun.',
        value: (d) => d.stories_failed,
      },
      {
        key: 'st_expired',
        label: 'Kedaluwarsa',
        info: 'Sudah lewat 24 jam sejak diposting.',
        value: (d) => d.stories_expired,
      },
      {
        key: 'st_views',
        label: 'Views Terdeteksi',
        info: 'Dihitung dari receipt yang benar-benar diterima sistem. Batas bawah, bukan jumlah pasti penonton.',
        value: (d) => d.story_views_detected,
      },
    ],
  },
  {
    key: 'schedule',
    label: 'Jadwal Kerja',
    columns: [
      {
        key: 'work',
        label: 'Jam Kerja',
        info: 'Total jam terjadwal. Jadwal yang waktunya bertumpuk dihitung satu kali.',
        value: (d) => (d.work_seconds > 0 ? formatHours(d.work_seconds) : null),
        raw: (d) => d.work_seconds,
      },
      {
        key: 'in_schedule',
        label: 'Status Jadwal',
        info: 'Aktivitas yang terjadi di dalam jam jadwal.',
        value: (d) => d.activities_in_schedule,
      },
      {
        key: 'out_schedule',
        label: 'Aktivitas di Luar Jadwal',
        info: 'Tetap tercatat penuh pada akun pelaksananya. Jadwal adalah pembanding, bukan penentu.',
        value: (d) => d.activities_out_of_schedule,
      },
    ],
  },
];


/**
 * CSV over every group, whatever is hidden on screen.
 *
 * Hiding a column is a reading choice; an export is a record. Someone who
 * narrowed the view to read it more easily should not discover later that
 * their spreadsheet is missing the columns they narrowed away.
 */
export function dailyCSV(rows: PerformanceDay[]): string {
  return groupedCSV(
    'Periode',
    rows.map((d) => ({ label: d.date, summary: d })),
  );
}

/**
 * The same export for any table built on this column model.
 *
 * Only the first column differs between them: a date, a person, an application.
 * Everything after it is the same forty-one columns under the same names, which
 * is the point of having one model.
 */
export function groupedCSV(
  firstHeader: string,
  rows: { label: string; summary: PerformanceDay }[],
): string {
  const header = [firstHeader];
  for (const g of DAILY_GROUPS) {
    for (const c of g.columns) header.push(`${g.label} - ${c.label}`);
  }

  const lines = [header.join(',')];
  for (const row of rows) {
    const cells: string[] = [row.label];
    for (const g of DAILY_GROUPS) {
      for (const c of g.columns) {
        const v = (c.raw ?? c.value)(row.summary);
        cells.push(v === null ? '' : String(v));
      }
    }
    lines.push(cells.map(csvCell).join(','));
  }
  return lines.join('\n');
}

/** Quotes only what needs it, so a plain number stays a plain number. */
function csvCell(value: string): string {
  return /[",\n]/.test(value) ? `"${value.replace(/"/g, '""')}"` : value;
}
