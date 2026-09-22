-- =============================================================================
-- salesan.id — Migration 0060: grup yang hanya boleh dikirimi admin
-- =============================================================================
-- WhatsApp punya mode "hanya admin yang dapat mengirim pesan", dan grup
-- pengumuman komunitas selalu memakainya. Nomor kita ada di dalamnya, jadi
-- pemilih penerima broadcast menawarkannya; tetapi ketika pesan dikirim,
-- servernya menjawab error 420, dan setiap grup semacam itu dicoba tiga kali
-- sebelum dinyatakan gagal tanpa alasan yang terbaca.
--
-- Disimpan bersama self_is_admin, dari sumber yang sama (GetJoinedGroups dan
-- info grup), supaya pratinjau bisa menolaknya lebih dulu dengan alasan yang
-- jelas. Nilainya diperbarui setiap kali grup disinkronkan; sebelum sinkron
-- berikutnya, baris lama bernilai false dan penolakan tetap datang dari
-- WhatsApp saat kirim, kini tanpa percobaan ulang.
--
-- Aman dijalankan ulang.
-- =============================================================================

alter table public.conversations
  add column if not exists group_announce boolean not null default false;

comment on column public.conversations.group_announce is
  'Grup hanya mengizinkan admin mengirim pesan. Bersama self_is_admin menentukan apakah nomor ini bisa dipakai broadcast ke grup tersebut.';
