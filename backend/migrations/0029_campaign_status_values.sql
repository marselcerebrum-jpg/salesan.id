-- =============================================================================
-- salesan.id — Migration 0029: nilai status baru untuk campaign
-- =============================================================================
-- Berdiri sendiri, dan hanya berisi ALTER TYPE.
--
-- PostgreSQL mengizinkan penambahan nilai enum di dalam sebuah transaksi, tetapi
-- TIDAK mengizinkan nilai itu dipakai sampai transaksinya commit. Migration
-- runner menjalankan satu berkas sebagai satu transaksi implisit, sehingga
-- menaruh penambahan enum dan pemakaiannya di berkas yang sama akan gagal
-- dengan "unsafe use of new value of enum type". Karena itu berkas ini sengaja
-- dipisah dari 0030 yang memakainya.
--
-- Dua status yang ditambahkan, keduanya menjawab kenyataan yang sudah ada:
--
--   partial — sebagian penerima berhasil dan sebagian gagal. Tanpa status ini
--             satu-satunya pilihan adalah menyebut campaign "selesai" (menutupi
--             kegagalan) atau "gagal" (menutupi yang sudah terkirim). Keduanya
--             salah, dan yang kedua mengundang orang mengirim ulang pesan yang
--             sebenarnya sudah sampai.
--   expired — Story lewat 24 jam. Bukan kegagalan: ia memang terbit lalu habis
--             masa berlakunya.
--
-- Aman dijalankan ulang.
-- =============================================================================

alter type public.campaign_status add value if not exists 'partial';
alter type public.campaign_status add value if not exists 'expired';

-- Jejak aktivitas untuk pembatalan dan kedaluwarsa, supaya laporan aktivitas
-- bisa menyebut keduanya tanpa memakai kolom bebas.
alter type public.campaign_activity_type add value if not exists 'cancelled';
alter type public.campaign_activity_type add value if not exists 'expired';
alter type public.campaign_activity_type add value if not exists 'partially_published';
