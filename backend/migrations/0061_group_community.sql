-- =============================================================================
-- salesan.id — Migration 0061: komunitas bukan grup chat
-- =============================================================================
-- Komunitas WhatsApp (induk dari beberapa grup) ikut terdaftar di daftar grup
-- yang diikuti nomor, dengan alamat @g.us seperti grup biasa. Ia bukan ruang
-- obrolan: pesan hanya bisa dikirim ke grup-grup di dalamnya, dan mengirim ke
-- induknya dijawab server dengan error 420. "TIM FIRYAL" adalah contohnya:
-- dua belas nomor tergabung, semuanya menawarkannya sebagai penerima
-- broadcast, dan setiap kiriman ke sana gagal tiga kali.
--
-- Ditandai dari info grup yang sama dengan self_is_admin dan group_announce,
-- supaya pemilih penerima bisa menolaknya dengan alasan yang jelas.
--
-- Aman dijalankan ulang.
-- =============================================================================

alter table public.conversations
  add column if not exists group_is_community boolean not null default false;

comment on column public.conversations.group_is_community is
  'Baris ini adalah komunitas (induk grup), bukan grup chat. Tidak bisa dikirimi pesan.';
