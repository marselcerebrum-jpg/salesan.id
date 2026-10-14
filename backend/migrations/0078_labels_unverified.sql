-- Membaca label tanpa bisa membuktikan keutuhannya, dan mengatakan apa adanya.
--
-- CEREBRUM UTAMA tersangkut di patch v1176 sejak 13 Oktober dan tidak akan
-- lepas sendiri: snapshot yang disajikan WhatsApp gagal MAC-nya sendiri, dan
-- memasangkan ulang perangkat sudah dibuktikan tidak menolong. Selama itu
-- nomornya memegang nol label dari 2.463 percakapan.
--
-- Ada satu jalan yang tersisa. whatsmeow memverifikasi MAC karena diminta
-- memverifikasi — `DecodePatches` menerima parameter `validateMACs`, dan jalur
-- normalnya selalu mengisinya `true`. Dimatikan, snapshot yang sama terbaca:
-- isinya tetap data WhatsApp, yang hilang hanyalah bukti bahwa isinya tidak
-- berubah di tengah jalan.
--
-- Itu bukan pertukaran yang boleh disembunyikan, jadi dicatat:
--
--   * `labels_unverified_at` menandai kapan terakhir label dibaca dengan cara
--     ini. Sengaja BUKAN `labels_read_at`, yang artinya "terbaca dan
--     terverifikasi" dan menjadi dasar pemeriksaan kesehatan. Menumpangkan
--     keduanya akan membuat nomor ini terlihat sehat, padahal tidak.
--   * `labels_unverified_count` adalah jumlah mutasi yang terbaca, supaya
--     "berhasil" bisa dibedakan dari "berhasil membaca nol".
--
-- Dan satu keadaan baru untuk indikatornya: `partial`. Nomor seperti ini tidak
-- `synced` — perubahan label dari salesan tidak sampai ke HP selama versinya
-- nol — tetapi juga tidak `failed`, sebab labelnya ada di layar dan terpakai.
-- Dua kata yang sudah ada tidak bisa menampung keadaan ketiga ini, dan memaksa
-- salah satunya berarti berbohong ke arah yang berlawanan.

alter table public.whatsapp_accounts
  add column if not exists labels_unverified_at    timestamptz,
  add column if not exists labels_unverified_count integer not null default 0;

comment on column public.whatsapp_accounts.labels_unverified_at is
  'Kapan label terakhir dibaca dengan MAC tidak diverifikasi. Bukan `labels_read_at`: yang itu berarti terbaca DAN terbukti utuh.';
comment on column public.whatsapp_accounts.labels_unverified_count is
  'Jumlah mutasi yang terbaca pada pembacaan tanpa verifikasi terakhir. Membedakan "berhasil" dari "berhasil membaca nol".';

alter table public.whatsapp_accounts
  drop constraint if exists whatsapp_accounts_label_sync_state_check;

alter table public.whatsapp_accounts
  add constraint whatsapp_accounts_label_sync_state_check
  check (label_sync_state in ('idle', 'syncing', 'synced', 'failed', 'partial'));
