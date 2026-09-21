-- =============================================================================
-- 0057 — berapa kali sebuah balas cepat dipakai
--
-- Sebuah daftar balas cepat tumbuh terus dan tidak pernah menyusut sendiri.
-- Tanpa angka pemakaian, tidak ada cara membedakan pintasan yang dipakai dua
-- ratus kali sebulan dari yang dibuat sekali lalu dilupakan — dan keduanya
-- sama-sama memenuhi menu garis-miring saat operator sedang buru-buru.
--
-- Dihitung saat DIKIRIM, bukan saat dipilih. Memilih lalu menghapus lagi
-- isinya di kolom pesan bukan pemakaian, dan angka yang menghitungnya akan
-- melebih-lebihkan justru balasan yang paling sering salah pilih.
--
-- last_used_at disimpan terpisah dari angkanya karena menjawab pertanyaan yang
-- berbeda: "sering dipakai" dan "terakhir dipakai kapan" bisa sangat berbeda
-- untuk balasan musiman seperti promo.
--
-- Aman dijalankan ulang.
-- =============================================================================

alter table public.quick_replies
  add column if not exists usage_count  bigint not null default 0,
  add column if not exists last_used_at timestamptz;

comment on column public.quick_replies.usage_count is
  'Berapa kali balasan ini benar-benar terkirim. Bukan berapa kali dipilih.';
comment on column public.quick_replies.last_used_at is
  'Terakhir kali terkirim. Terpisah dari usage_count karena menjawab pertanyaan berbeda.';
