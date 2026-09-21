-- -----------------------------------------------------------------------------
-- Tandai grup yang memang kita ikuti
--
-- History sync membuat percakapan untuk SETIAP obrolan di riwayat ponsel,
-- termasuk grup yang sudah kita tinggalkan atau yang pernah menambahkan kita
-- lalu mengeluarkan kita. Grup seperti itu tetap muncul di daftar Fetch Grup
-- padahal kita bukan anggotanya: nama grupnya ada di riwayat, tetapi
-- GetJoinedGroups tidak pernah menyebutnya, sehingga daftar anggotanya selamanya
-- kosong dan setiap percobaan ambil metadatanya ditolak WhatsApp.
--
-- Tiga keadaan, karena memang ada tiga:
--   true  — GetJoinedGroups menyebutnya, kita anggota
--   false — GetJoinedGroups tidak menyebutnya padahal daftar itu lengkap
--   null  — belum pernah diperiksa
--
-- NULL sengaja dipertahankan dan tetap ditampilkan. Grup yang baru masuk lewat
-- pesan, sebelum penyapuan berikutnya, belum tentu bukan milik kita, dan
-- menyembunyikannya lebih buruk daripada menampilkannya sebentar.
-- -----------------------------------------------------------------------------

alter table public.conversations
  add column if not exists group_is_member boolean;

comment on column public.conversations.group_is_member is
  'Apakah akun ini benar-benar anggota grup tersebut menurut GetJoinedGroups. NULL bila belum pernah diperiksa.';

create index if not exists idx_conversations_group_member
  on public.conversations (workspace_id, type)
  where type = 'group' and group_is_member is not false;
