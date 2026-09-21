-- =============================================================================
-- salesan.id — Migration 0015: buang batas kirim harian
-- =============================================================================
-- Kolom ini menjanjikan sesuatu yang tidak pernah ada: nilainya ditampilkan
-- sebagai "batas aman ~20/hari", bisa diubah dari layar detail akun, dan tidak
-- pernah diperiksa oleh apa pun saat mengirim.
--
-- Angka yang terlihat seperti pagar tetapi tidak menahan apa-apa lebih buruk
-- daripada tidak ada angka sama sekali — operator mengira dirinya terlindungi.
-- Pemilik memutuskan tidak ingin ada batas pesan, jadi kolomnya dibuang
-- sekalian, bukan dibiarkan menganggur.
--
-- Yang menjaga akun dari blokir sekarang adalah perilaku pengiriman di
-- internal/wa/humanize.go — jeda, indikator mengetik, dan tanda baca — bukan
-- hitungan harian.
--
-- Aman dijalankan ulang.
-- =============================================================================

alter table public.whatsapp_accounts
  drop column if exists daily_send_limit;
