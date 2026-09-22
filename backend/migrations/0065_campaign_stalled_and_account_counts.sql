-- =============================================================================
-- salesan.id — Migration 0065: campaign yang mandek, dan hitungan per nomor
-- =============================================================================
--
-- BAGIAN 1 — campaign yang tidak pernah bisa mulai
--
-- Kalau semua nomor pengirim sebuah Broadcast sedang tidak terhubung, worker
-- melepas klaimnya dan membiarkan campaign berstatus "berjalan", supaya tik
-- berikutnya mencoba lagi. Itu jawaban yang benar untuk HP yang sebentar lagi
-- kembali, dan jawaban yang salah untuk HP yang tidak akan kembali: campaign
-- berputar selamanya, tidak mengirim apa pun, dan di layar tetap terbaca
-- "Berjalan".
--
-- Hari ini ada dua Broadcast terjadwal yang nomor pengirimnya sudah keluar
-- dari WhatsApp. Keduanya akan masuk keadaan itu begitu jamnya tiba.
--
-- stalled_since mencatat kapan pertama kali campaign mendapati tidak ada satu
-- pun nomornya terhubung. Nomor yang kembali menghapusnya, jadi gangguan
-- sebentar tidak berakibat apa-apa. Kalau masih kosong setelah tenggang
-- CAMPAIGN_OFFLINE_GRACE, campaign dinyatakan gagal dengan alasan yang menyebut
-- nomor mana yang hilang, bukan dibiarkan menggantung.
--
-- BAGIAN 2 — daftar nomor tidak lagi menghitung ulang seluruh tabel
--
-- Layar daftar nomor menampilkan jumlah percakapan, belum dibaca, belum
-- dijawab, dan jumlah pesan untuk setiap nomor. Keempatnya dihitung ulang
-- setiap kali layar dibuka, dan tidak ada indeks yang memuat angka-angka itu:
-- hitungan pesan saja membaca 36.531 blok, dan seluruh kuerinya 48.193 blok,
-- sekitar 376 MB, untuk menampilkan 35 baris.
--
-- Dua indeks ramping di bawah ini membuat keempat angka itu dibaca dari indeks
-- per nomor, bukan dari tabel bersama. Terukur pada data produksi: 48.193 blok
-- menjadi 15.391, dan waktu kueri menjadi 32 ms. Angkanya akan turun lagi
-- setelah autovacuum menyegarkan peta visibilitas, karena saat itu indeks bisa
-- dibaca tanpa menyentuh tabel sama sekali.
--
-- INCLUDE dipakai, bukan kolom kunci tambahan, karena unread_count dan
-- awaiting_reply hanya perlu dibaca, tidak perlu diurutkan atau dicari.
--
-- Aman dijalankan ulang.
-- =============================================================================

alter table public.content_campaigns
  add column if not exists stalled_since timestamptz;

comment on column public.content_campaigns.stalled_since is
  'Sejak kapan campaign ini tidak punya satu pun nomor pengirim yang terhubung. Dikosongkan begitu ada yang kembali; kalau tetap terisi melebihi tenggang, campaign dinyatakan gagal.';

create index if not exists idx_messages_account_count
  on public.messages (account_id);

comment on index public.idx_messages_account_count is
  'Jumlah pesan per nomor untuk layar daftar nomor. Sempit dengan sengaja: hanya untuk menghitung, sehingga tidak ikut membaca tabel pesan.';

create index if not exists idx_conversations_account_active
  on public.conversations (account_id)
  include (unread_count, awaiting_reply)
  where is_archived = false;

comment on index public.idx_conversations_account_active is
  'Jumlah percakapan aktif, belum dibaca, dan belum dijawab per nomor. INCLUDE membawa kedua angkanya, jadi hitungannya selesai di dalam indeks.';
