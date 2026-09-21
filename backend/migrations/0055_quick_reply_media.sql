-- =============================================================================
-- 0055 — balas cepat: kategori dan gambar
--
-- Dua tambahan pada quick_replies:
--
--   category   — pengelompokan bebas ("umum", "harga", "pendaftaran"), opsional.
--   media_url  — kalau diisi, balasan dikirim sebagai GAMBAR + teks, bukan teks
--                saja. Isi pesannya menjadi caption gambar itu.
--
-- Yang disimpan hanya ALAMATNYA, bukan berkasnya. Sama seperti Broadcast dan
-- Story: gambar diambil ke berkas sementara saat dikirim lalu dihapus, dan
-- database tidak pernah memegang binary. Kolom media_kind/mime/size/sha256
-- adalah hasil pengukuran saat alamat itu disimpan — bukan tebakan dari
-- ekstensi — supaya "ini benar-benar gambar" sudah dipastikan di saat menyimpan,
-- bukan saat seorang operator menekan kirim di depan pelanggan.
--
-- Aman dijalankan ulang.
-- =============================================================================

alter table public.quick_replies
  add column if not exists category         text,
  add column if not exists media_url        text,
  add column if not exists media_kind       text,
  add column if not exists media_mime       text,
  add column if not exists media_size_bytes bigint,
  add column if not exists media_sha256     text;

-- Hanya gambar. WhatsApp memang bisa mengirim video dan dokumen, tetapi balas
-- cepat adalah sesuatu yang ditekan cepat-cepat di tengah percakapan; video
-- puluhan megabita yang terkirim karena salah pintasan bukan kesalahan yang
-- bisa ditarik kembali. Dibatasi di sini, bukan hanya di formulir, supaya
-- klien lama atau permintaan yang dirakit tangan tidak bisa melewatinya.
do $$ begin
  alter table public.quick_replies
    add constraint quick_replies_media_kind_check
    check (media_kind is null or media_kind = 'image');
exception when duplicate_object then null; end $$;

-- Alamat tanpa hasil pengukuran berarti belum pernah divalidasi, dan itu tidak
-- boleh ada: yang memutuskan "ini gambar" adalah byte-nya, bukan tautannya.
do $$ begin
  alter table public.quick_replies
    add constraint quick_replies_media_measured_check
    check ((media_url is null) = (media_kind is null));
exception when duplicate_object then null; end $$;

create index if not exists idx_quick_replies_category
  on public.quick_replies (workspace_id, category) where category is not null;

comment on column public.quick_replies.category is
  'Pengelompokan bebas untuk daftar balas cepat. Opsional.';
comment on column public.quick_replies.media_url is
  'Alamat gambar. Bila diisi, balasan dikirim sebagai gambar dengan body sebagai caption. Berkasnya tidak pernah disimpan di database.';
