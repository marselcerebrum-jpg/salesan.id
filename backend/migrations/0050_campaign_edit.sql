-- =============================================================================
-- 0050 — jejak audit untuk campaign yang diubah
--
-- Sampai sekarang sebuah campaign hanya bisa dibuat, dijadwalkan, dibatalkan,
-- atau diulang. Isinya tidak pernah bisa diubah: satu-satunya jalan adalah
-- membuat salinan baru, dan salinan itu pun tidak bisa dibuka di composer.
--
-- Dengan tombol Edit, isi campaign yang belum berjalan bisa ditulis ulang di
-- baris yang sama. Perubahan itu harus punya namanya sendiri di riwayat: tanpa
-- ini perubahan isi akan tercatat sebagai 'schedule_updated', yang menyesatkan —
-- yang berubah pesannya, bukan jamnya.
--
-- Aman dijalankan ulang.
-- =============================================================================

alter type public.campaign_activity_type add value if not exists 'draft_updated';
