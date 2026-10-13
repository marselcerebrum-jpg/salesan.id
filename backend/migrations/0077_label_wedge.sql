-- Mengingat patch mana yang ditolak WhatsApp, supaya pengulangan bisa dikenali.
--
-- CEREBRUM UTAMA dipasangkan ulang pada 13 Oktober 2026 pukul 09:21 dan gagal
-- sejak detik itu, selalu dengan kalimat yang sama:
--
--   failed to verify snapshot: failed to verify patch v1176: mismatching LTHash
--
-- Delapan belas kali, versi patch-nya tidak pernah bergeser. Semua kegagalannya
-- bertanda `full_sync: true`, artinya kami memang memulai dari nol dan membaca
-- snapshot milik WhatsApp sendiri: versi yang kami simpan tidak ikut serta, jadi
-- membersihkannya tidak mungkin menjadi obat. Memasangkan ulang perangkat juga
-- tidak, dan itu bukan dugaan — hari itu perangkatnya memang baru, kuncinya baru,
-- dan patch yang ditolak tetap v1176.
--
-- Tanpa ingatan ini, tidak ada satu pun cara membedakan dua keadaan yang
-- tindakannya berlawanan:
--
--   * versi patch-nya sama seperti tadi -> tidak ada yang berubah di sisi
--     WhatsApp, dan mencoba lagi dengan cara yang sama hanya mengunduh snapshot
--     yang sama untuk ditolak dengan alasan yang sama;
--   * versi patch-nya bergeser -> WhatsApp sudah menyusun ulang snapshot-nya,
--     dan inilah satu-satunya saat percobaan baru benar-benar punya peluang.
--
-- Yang kedua itu yang membuat nomornya bisa pulih sendiri tanpa ada yang
-- menungguinya. Yang pertama membuatnya berhenti menyiksa WhatsApp dan berhenti
-- menyuruh operator memindai QR untuk sesuatu yang tidak akan berubah.

alter table public.whatsapp_accounts
  add column if not exists labels_wedged_patch    bigint,
  add column if not exists labels_wedged_since    timestamptz,
  add column if not exists labels_wedge_last_try  timestamptz,
  add column if not exists labels_wedge_attempts  integer not null default 0;

comment on column public.whatsapp_accounts.labels_wedged_patch is
  'Versi patch yang ditolak LTHash-nya oleh data WhatsApp sendiri. NULL berarti tidak tersangkut. Pergeseran nilainya adalah satu-satunya tanda bahwa mencoba lagi ada gunanya.';
comment on column public.whatsapp_accounts.labels_wedged_since is
  'Kapan versi patch itu pertama kali terlihat gagal. Tidak direset oleh restart backend, sebab tersangkutnya juga tidak.';
comment on column public.whatsapp_accounts.labels_wedge_last_try is
  'Percobaan terakhir, dicatat terpisah dari `labels_wedged_since`. Keduanya tidak bisa dijadikan satu: yang pertama menentukan kapan percobaan berikutnya jatuh tempo, yang kedua bisa berumur tiga minggu, dan memakai yang kedua untuk keduanya membuat jeda mundurnya hangus setiap kali backend restart.';
comment on column public.whatsapp_accounts.labels_wedge_attempts is
  'Berapa kali sudah dicoba sejak versi patch itu terlihat. Dasar jeda mundurnya, dan dikosongkan begitu versinya bergeser.';
