-- =============================================================================
-- salesan.id — Migration 0069: warna latar dan font untuk Story teks
-- =============================================================================
-- Story teks adalah tulisan di atas satu bidang warna. WhatsApp membiarkan
-- pengirimnya memilih warna latar dan bentuk hurufnya, dan keduanya ikut dalam
-- pesan yang dikirim, bukan sekadar tampilan di aplikasi pengirim.
--
-- Sisi pengiriman sudah menerima warna latar sejak awal, tetapi penyusunnya
-- tidak pernah menawarkannya, jadi setiap Story teks keluar dengan warna
-- bawaan yang sama. Font belum ada sama sekali.
--
-- Warna disimpan sebagai ARGB, bilangan bulat delapan digit heksadesimal yang
-- dipakai WhatsApp itu sendiri, supaya tidak ada penerjemahan yang bisa
-- meleset. bigint karena nilai dengan alfa penuh melewati batas int empat byte.
-- Kosong berarti "pakai bawaan", sehingga Story lama tetap terkirim persis
-- seperti sebelumnya.
--
-- Font disimpan sebagai namanya, bukan angkanya. Angka enum protobuf milik
-- WhatsApp dan bisa berubah arti; nama yang disimpan di sini diterjemahkan ke
-- enum saat mengirim, di satu tempat yang bisa diperiksa.
--
-- Aman dijalankan ulang.
-- =============================================================================

alter table public.content_campaigns
  add column if not exists story_background_argb bigint,
  add column if not exists story_font text;

comment on column public.content_campaigns.story_background_argb is
  'Warna latar Story teks sebagai ARGB, angka yang sama dengan yang dikirim ke WhatsApp. Kosong berarti warna bawaan.';
comment on column public.content_campaigns.story_font is
  'Nama font Story teks, diterjemahkan ke enum WhatsApp saat mengirim. Kosong berarti font bawaan.';
