-- =============================================================================
-- salesan.id — Migration 0017: pengelolaan grup
-- =============================================================================
-- Supaya web bisa mengelola grup — mengangkat admin, mengeluarkan anggota,
-- mengubah deskripsi — ia perlu tahu satu hal yang selama ini tidak disimpan:
-- apakah akun kita sendiri admin di grup itu.
--
-- Disimpan, bukan ditanyakan ke WhatsApp setiap kali. Menampilkan tombol
-- "keluarkan" berarti menjawab pertanyaan itu untuk setiap baris pada setiap
-- render; satu permintaan jaringan per baris bukan cara menjawabnya.
--
-- Nilainya diperbarui setiap kali info grup disinkronkan, dan WhatsApp tetap
-- menjadi penentu akhir: kalau kita mengira diri admin padahal bukan, servernya
-- yang menolak, dan penolakan itu diteruskan apa adanya ke operator.
--
-- Aman dijalankan ulang.
-- =============================================================================

alter table public.conversations
  add column if not exists group_description text,
  add column if not exists group_topic_id    text,
  add column if not exists group_owner_jid   text,
  add column if not exists self_is_admin     boolean not null default false;

comment on column public.conversations.self_is_admin is
  'Apakah akun ini admin di grup tersebut. Menentukan tombol mana yang muncul; WhatsApp tetap penentu akhirnya.';
comment on column public.conversations.group_topic_id is
  'Id topik terakhir dari WhatsApp. Perubahan deskripsi harus menyebut yang digantikannya.';
