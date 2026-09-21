-- 0033 — jejak audit kepemilikan campaign.
--
-- Kepemilikan performa sudah benar sebelum migrasi ini: laporan memakai
-- content_campaigns.created_by, dan tidak ada satu pun query yang melihat
-- siapa yang sedang bertugas saat worker menjalankan jadwal. Pesan yang
-- dikirim worker pun ditulis dengan sender_source = 'broadcast' dan sent_by
-- kosong, jadi tidak pernah masuk ke metrik chat siapa pun.
--
-- Yang belum ada adalah jejak siapa yang menyentuh campaign setelah dibuat.
-- Tiga kolom di bawah ini murni audit trail: tidak satu pun dipakai untuk
-- menghitung performa, dan itu memang inti aturannya. Seseorang yang mengedit
-- atau membatalkan campaign orang lain tercatat di sini, tetapi angkanya tetap
-- menjadi milik pembuatnya.

begin;

alter table public.content_campaigns
  -- Siapa yang menekan "jadwalkan". Terpisah dari created_by karena sebuah
  -- draft bisa dibuat satu orang lalu dijadwalkan orang lain; performanya
  -- tetap milik created_by, dan kolom ini yang menjelaskan bedanya.
  add column if not exists scheduled_by uuid references public.users (id) on delete set null,
  -- Penyunting terakhir. Audit saja.
  add column if not exists updated_by   uuid references public.users (id) on delete set null,
  -- Siapa yang membatalkan. Audit saja; cancelled_at sudah ada sejak 0030.
  add column if not exists cancelled_by uuid references public.users (id) on delete set null;

-- Backfill jujur: untuk campaign lama yang punya jadwal, satu-satunya bukti
-- yang kita punya adalah pembuatnya. Tidak ada riwayat yang menunjukkan orang
-- lain pernah menjadwalkannya, jadi menebak orang lain justru mengarang.
update public.content_campaigns
   set scheduled_by = created_by
 where scheduled_at is not null
   and scheduled_by is null;

-- Dibaca saat menyusun daftar campaign dan audit; tidak dipakai agregasi.
create index if not exists idx_campaigns_scheduled_by
  on public.content_campaigns (workspace_id, scheduled_by)
  where scheduled_by is not null;

-- --------------------------------------------------------------------------
-- Indeks untuk pembukuan per tanggal kejadian
-- --------------------------------------------------------------------------
--
-- Laporan harian tidak lagi membukukan semuanya pada created_at. "Dibuat"
-- tetap di created_at, "dijadwalkan" pindah ke scheduled_at, dan seluruh hasil
-- (selesai, gagal, dibatalkan, kedaluwarsa, target terkirim, views) dibukukan
-- pada tanggal kejadiannya. Tiga tanggal berarti tiga jalur pemindaian.

create index if not exists idx_campaigns_scheduled_at
  on public.content_campaigns (workspace_id, scheduled_at)
  where scheduled_at is not null;

-- Tanggal hasil: executed_at kalau ada, kalau tidak cancelled_at, kalau tidak
-- baru created_at. Urutan itu sama persis dengan yang dipakai query.
create index if not exists idx_campaigns_outcome_at
  on public.content_campaigns
     (workspace_id, (coalesce(executed_at, cancelled_at, created_at)));

commit;
