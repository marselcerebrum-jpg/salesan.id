-- -----------------------------------------------------------------------------
-- Waktu pengulangan, dan dokumen yang diunggah
--
-- 0041 sudah menyimpan frekuensinya. Yang belum: kapan tepatnya. "Mingguan"
-- tanpa hari dan jam bukan jadwal, hanya niat — penjadwal tidak bisa menghitung
-- kemunculan berikutnya darinya.
--
-- Jam disimpan sebagai jam dinding WIB, bukan timestamptz. Broadcast pukul 09.00
-- harus tetap pukul 09.00 bagi penerimanya, dan satu-satunya cara jam dinding
-- bertahan adalah menyimpan jam dindingnya lalu menerjemahkannya ke Asia/Jakarta
-- setiap kali kemunculan berikutnya dihitung.
--
-- Tanggal 29, 30, dan 31 sengaja diizinkan. Bulan yang tidak memilikinya
-- ditangani saat penghitungan dengan mundur ke hari terakhir bulan itu, bukan
-- dengan melarang orang memilihnya: "tiap akhir bulan" adalah jadwal yang wajar,
-- dan melewatkan Februari diam-diam jauh lebih buruk daripada mengirim tanggal
-- 28.
-- -----------------------------------------------------------------------------

alter table public.content_campaigns
  -- Jam dinding WIB, dipakai semua frekuensi.
  add column if not exists recurrence_time    time,
  -- 0 = Minggu … 6 = Sabtu. Hanya untuk mingguan.
  add column if not exists recurrence_weekday smallint,
  -- 1–31. Hanya untuk bulanan.
  add column if not exists recurrence_day     smallint,
  -- Kemunculan berikutnya yang sudah dibuat, supaya satu rangkaian tidak
  -- melahirkan dua broadcast bila dua proses menghitungnya bersamaan.
  add column if not exists recurrence_spawned_at timestamptz;

comment on column public.content_campaigns.recurrence_time is
  'Jam kirim sebagai jam dinding WIB (Asia/Jakarta), bukan UTC.';
comment on column public.content_campaigns.recurrence_day is
  'Tanggal kirim bulanan, 1-31. Bulan yang lebih pendek mundur ke hari terakhirnya.';

alter table public.content_campaigns
  drop constraint if exists content_campaigns_recurrence_weekday_check;
alter table public.content_campaigns
  add constraint content_campaigns_recurrence_weekday_check
  check (recurrence_weekday is null or recurrence_weekday between 0 and 6);

alter table public.content_campaigns
  drop constraint if exists content_campaigns_recurrence_day_check;
alter table public.content_campaigns
  add constraint content_campaigns_recurrence_day_check
  check (recurrence_day is null or recurrence_day between 1 and 31);

-- Sebuah rangkaian harus punya jamnya. Tanpa itu penjadwal tidak punya apa pun
-- untuk dihitung, dan campaign akan diam selamanya sambil terlihat aktif.
alter table public.content_campaigns
  drop constraint if exists content_campaigns_recurrence_time_check;
alter table public.content_campaigns
  add constraint content_campaigns_recurrence_time_check
  check (recurrence is null or recurrence_time is not null);

-- -----------------------------------------------------------------------------
-- Dokumen broadcast
--
-- Gambar dan video cukup tautan: WhatsApp menampilkannya dari URL yang diunduh
-- server sesaat sebelum mengirim. Dokumen tidak bisa begitu — namanya tampil di
-- layar penerima, jadi berkasnya harus benar-benar ada dan bernama benar.
--
-- Berkasnya tetap tidak masuk basis data. Kolom ini menyimpan letaknya di bucket
-- privat Supabase Storage, dan runner menghapusnya setelah campaign selesai.
-- -----------------------------------------------------------------------------

alter table public.content_campaigns
  add column if not exists media_storage_path text,
  add column if not exists media_file_name    text,
  -- Diisi saat berkas sudah dihapus dari bucket, supaya penghapusan tidak
  -- diulang dan laporan tetap bisa menyebut nama berkas yang pernah dikirim.
  add column if not exists media_purged_at    timestamptz;

comment on column public.content_campaigns.media_storage_path is
  'Letak dokumen di bucket privat. Berkasnya tidak pernah disimpan di basis data, dan dihapus setelah campaign selesai.';

create index if not exists idx_campaigns_media_to_purge
  on public.content_campaigns (finished_at)
  where media_storage_path is not null and media_purged_at is null;
