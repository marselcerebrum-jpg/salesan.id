-- =============================================================================
-- salesan.id — Migration 0067: broadcast yang terkirim ikut terlihat di HP
-- =============================================================================
-- Broadcast sudah benar-benar terkirim. Penerimanya membuka dan membacanya, dan
-- tanda dibaca itu datang dari server WhatsApp, bukan dari kita. Tetapi di HP
-- pengirim sendiri pesannya tidak terlihat, dan sebabnya bukan pengiriman
-- melainkan tempat.
--
-- Dari 1.335 grup yang diikuti nomor-nomor ini, 851 ada di folder Diarsipkan,
-- sekitar dua pertiga. Chat yang diarsipkan tidak naik ke daftar utama meskipun
-- ada pesan baru, selama setelan "Tetap arsipkan chat" menyala. Jadi operator
-- melihat "Terkirim" di salesan, membuka WhatsApp di HP, dan tidak menemukan
-- apa pun di layar depan.
--
-- Mengarsipkan tidak pernah menghalangi pengiriman, dan itu sudah dipastikan
-- sejak migration 0057. Yang belum ada adalah cara membuat chat yang baru saja
-- dikirimi muncul kembali.
--
-- surface_on_phone menyalakan itu: setelah sebuah pesan broadcast benar-benar
-- terkirim ke satu chat, kalau chat itu terarsip, arsipnya dibuka. WhatsApp
-- menyebarkan perubahan itu ke semua perangkat, jadi chatnya muncul di HP
-- bersama pesannya.
--
-- Hanya chat yang memang jadi tujuan broadcast ini yang tersentuh, dan hanya
-- yang benar-benar berhasil dikirimi. Operator tetap bisa mengarsipkannya lagi
-- dari HP kapan saja, dan campaign berikutnya menghormati pilihan itu kecuali
-- dinyalakan lagi.
--
-- Bawaannya menyala, karena broadcast yang tidak bisa dilihat pengirimnya
-- sendiri adalah keluhan yang membawa kita ke sini.
--
-- Aman dijalankan ulang.
-- =============================================================================

alter table public.content_campaigns
  add column if not exists surface_on_phone boolean not null default true;

comment on column public.content_campaigns.surface_on_phone is
  'Keluarkan chat tujuan dari arsip setelah pesan broadcast berhasil terkirim, supaya chatnya muncul di HP. Hanya menyentuh chat yang jadi tujuan campaign ini.';
