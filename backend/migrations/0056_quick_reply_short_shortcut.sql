-- =============================================================================
-- 0056 — pintasan balas cepat boleh satu karakter
--
-- Aturan "minimal 2 karakter" diwarisi dari kunci variabel pesan, dan untuk
-- balas cepat tidak pernah ada alasannya. Sebuah variabel muncul di dalam
-- kalimat dan butuh nama yang bisa dibaca; sebuah pintasan diketik secepat
-- mungkin di tengah percakapan, dan /1, /k, /p justru persis yang diinginkan
-- orang yang mengetik cepat.
--
-- Kumpulan balas cepat yang sudah dipakai sehari-hari memang penuh pintasan
-- satu huruf, dan aturan lama menolak semuanya.
--
-- Batas atas 40 karakter tidak berubah.
--
-- Aman dijalankan ulang.
-- =============================================================================

alter table public.quick_replies
  drop constraint if exists quick_replies_shortcut_check;

alter table public.quick_replies
  add constraint quick_replies_shortcut_check
  check (shortcut ~ '^[a-z0-9_]{1,40}$');

comment on column public.quick_replies.shortcut is
  'Yang diketik setelah garis miring, tanpa garis miringnya. Huruf kecil, angka dan garis bawah, 1-40 karakter. Spasi tidak mungkin: menu garis-miring di ruang chat tertutup begitu spasi diketik.';
