-- Satu stempel yang hanya bisa diisi oleh koleksi label yang benar-benar terbaca.
--
-- `labels_synced_at` tidak bisa dipakai untuk ini. Kolom itu ikut terisi setiap
-- kali `label_sync_state` menjadi 'synced', dan salah satu jalan menuju 'synced'
-- adalah menyerah: ketika HP tidak menjawab permintaan pemulihan, badge-nya
-- sengaja diturunkan ke 'synced' supaya tidak berputar selamanya. Jadi kolom itu
-- menjawab "kapan terakhir kita berhenti menunggu", bukan "kapan terakhir label
-- terbaca".
--
-- Perbedaan itu bukan soal rapi-rapian. Selama enam hari, tiga puluh delapan
-- nomor menulis hijau sementara tujuh di antaranya tidak bisa membaca label sama
-- sekali, dan dua tidak punya satu label pun. Tidak ada satu kolom pun di basis
-- data ini yang bisa membedakan keduanya, jadi tidak ada layar yang bisa, jadi
-- tidak ada orang yang tahu.
--
-- `labels_read_at` hanya ditulis ketika koleksi `regular` selesai didekode oleh
-- WhatsApp. `labels_stale_since` menandai kapan kegagalan beruntunnya dimulai,
-- dan dikosongkan begitu satu pembacaan berhasil. Dari dua kolom itu, "sudah
-- berapa lama nomor ini buta" bisa dijawab dengan satu pengurangan.

alter table public.whatsapp_accounts
  add column if not exists labels_read_at     timestamptz,
  add column if not exists labels_stale_since timestamptz,
  add column if not exists labels_stale_error text;

comment on column public.whatsapp_accounts.labels_read_at is
  'Kapan koleksi app-state `regular` terakhir benar-benar terbaca. Bukan kapan badge terakhir menulis synced.';
comment on column public.whatsapp_accounts.labels_stale_since is
  'Kapan kegagalan membaca label mulai beruntun. Dikosongkan oleh pembacaan yang berhasil.';
comment on column public.whatsapp_accounts.labels_stale_error is
  'Galat terakhir saat membaca koleksi label, apa adanya dari WhatsApp.';
