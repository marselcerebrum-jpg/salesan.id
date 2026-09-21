-- -----------------------------------------------------------------------------
-- Pengaturan pengiriman broadcast
--
-- Tiga hal yang selama ini hanya ada sebagai profil tetap:
--
--   1. Jeda kustom. Lima profil sudah cukup untuk hampir semua campaign, tetapi
--      yang menentukan aman atau tidaknya sebuah nomor adalah ritme nyatanya,
--      bukan nama profilnya. Min/max eksplisit membuat itu bisa diatur tanpa
--      menambah profil keenam yang artinya harus dihafal orang lain.
--
--   2. Auto-retry saat device terputus. Perilaku ini sudah ada di runner —
--      bagian milik nomor yang terputus berhenti dan dilanjutkan saat tersambung
--      lagi — tetapi tidak pernah bisa dimatikan. Untuk campaign yang terikat
--      waktu (promo yang berakhir pukul 20.00), melanjutkan enam jam kemudian
--      justru salah.
--
--   3. Broadcast berulang. Harian, mingguan, atau bulanan.
--
-- Untuk yang berulang, tiap kemunculan adalah baris campaign-nya sendiri, bukan
-- satu baris yang dijalankan ulang. Menjalankan ulang baris yang sama akan
-- menimpa laporannya: jumlah terkirim, gagal, dan daftar penerima minggu lalu
-- hilang begitu minggu ini berjalan. recurrence_parent_id yang menghubungkan
-- mereka, sehingga tiap kemunculan punya laporannya sendiri dan rangkaiannya
-- tetap bisa ditelusuri.
-- -----------------------------------------------------------------------------

alter table public.content_campaigns
  -- NULL berarti ikut profil. Diisi hanya bila operator menyetel sendiri.
  add column if not exists delay_min_seconds int,
  add column if not exists delay_max_seconds int,
  add column if not exists auto_retry_on_disconnect boolean not null default true,
  add column if not exists recurrence          text,
  -- Kapan rangkaian berhenti. NULL berarti berjalan sampai dimatikan orang.
  add column if not exists recurrence_until    timestamptz,
  -- Kemunculan pertama menjadi induk; turunannya menunjuk ke sana.
  add column if not exists recurrence_parent_id uuid
    references public.content_campaigns (id) on delete set null;

comment on column public.content_campaigns.delay_min_seconds is
  'Batas bawah jeda antar pesan, dalam detik. NULL berarti memakai delay_profile.';
comment on column public.content_campaigns.auto_retry_on_disconnect is
  'Lanjutkan sisa pengiriman saat nomor tersambung kembali. Dimatikan untuk campaign yang terikat waktu.';
comment on column public.content_campaigns.recurrence is
  'daily, weekly, atau monthly. NULL berarti sekali jalan.';

alter table public.content_campaigns
  drop constraint if exists content_campaigns_recurrence_check;
alter table public.content_campaigns
  add constraint content_campaigns_recurrence_check
  check (recurrence is null or recurrence in ('daily', 'weekly', 'monthly'));

-- Jeda harus masuk akal sebagai rentang, dan keduanya ada atau keduanya kosong.
-- Batas atas 3600 detik: apa pun di atas satu jam per pesan bukan lagi jeda,
-- melainkan penjadwalan, dan itu urusan kolom scheduled_at.
alter table public.content_campaigns
  drop constraint if exists content_campaigns_delay_range_check;
alter table public.content_campaigns
  add constraint content_campaigns_delay_range_check
  check (
    (delay_min_seconds is null and delay_max_seconds is null)
    or (delay_min_seconds is not null and delay_max_seconds is not null
        and delay_min_seconds >= 1
        and delay_max_seconds >= delay_min_seconds
        and delay_max_seconds <= 3600)
  );

-- Sebuah campaign tidak boleh menjadi induknya sendiri.
alter table public.content_campaigns
  drop constraint if exists content_campaigns_recurrence_parent_check;
alter table public.content_campaigns
  add constraint content_campaigns_recurrence_parent_check
  check (recurrence_parent_id is null or recurrence_parent_id <> id);

-- Dibaca penjadwal saat mencari rangkaian yang perlu kemunculan berikutnya.
create index if not exists idx_campaigns_recurring
  on public.content_campaigns (workspace_id, recurrence)
  where recurrence is not null;

create index if not exists idx_campaigns_recurrence_parent
  on public.content_campaigns (recurrence_parent_id)
  where recurrence_parent_id is not null;
