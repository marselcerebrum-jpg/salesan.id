-- =============================================================================
-- salesan.id — Migration 0070: satu berkas unik = satu objek di bucket
-- =============================================================================
-- Satu broadcast ke enam puluh kontak menghasilkan enam puluh baris pesan dan
-- enam puluh baris lampiran, masing-masing dengan id sendiri. Kunci objek di
-- bucket dibangun dari id pesan, sehingga berkas yang persis sama diunduh ulang
-- dari WhatsApp dan diunggah ulang ke bucket enam puluh kali, dengan enam puluh
-- kunci berbeda. Disk VPS terisi 120 GB dalam beberapa hari karena ini.
--
-- Tabel ini memindahkan identitas objek dari "pesan mana" ke "isi apa". Kuncinya
-- adalah hash konten yang sudah dihitung WhatsApp dan sudah kita simpan di
-- whatsapp_media_refs.file_sha256, jadi tidak ada hash baru yang perlu dihitung.
--
-- Di-scope per workspace, dan itu disengaja meski hash yang sama di dua
-- workspace berarti dua objek. Berbagi objek antar penyewa berarti berkas satu
-- pelanggan hidup di jalur yang bisa dijangkau pelanggan lain, dan menghemat
-- ruang bukan alasan yang cukup untuk itu.
--
-- Server-only: RLS aktif tanpa policy untuk `authenticated`, jadi peramban
-- mendapat nol baris. Jalur penyimpanan hanya boleh sampai ke browser sebagai
-- signed URL sementara, bukan sebagai jalur mentah.
--
-- Aman dijalankan ulang.
-- =============================================================================

create table if not exists public.media_objects (
  workspace_id  uuid   not null references public.workspaces (id) on delete cascade,
  file_sha256   bytea  not null,
  storage_path  text   not null,
  size_bytes    bigint not null default 0,
  created_at    timestamptz not null default now(),

  -- Kunci utama inilah yang menyelesaikan perlombaan. Dua goroutine broadcast
  -- yang mengunggah berkas sama secara bersamaan sama-sama mencoba menyisipkan
  -- baris ini; satu menang, yang kalah membaca jalur pemenang dan memakainya.
  primary key (workspace_id, file_sha256)
);

-- Dipakai saat menghapus: sebelum satu objek benar-benar dibuang dari bucket,
-- barisnya di sini harus ikut hilang, kalau tidak lampiran berikutnya dengan
-- hash yang sama akan ditandai "tersimpan" sambil menunjuk objek yang sudah
-- tidak ada.
create index if not exists idx_media_objects_path
  on public.media_objects (storage_path);

-- Pertanyaan yang ditanyakan sebelum menghapus objek apa pun: masih ada lampiran
-- lain yang menunjuk jalur ini? Tanpa indeks ini pertanyaan itu memindai seluruh
-- tabel lampiran, sekali per batch pembersihan.
create index if not exists idx_message_attachments_storage_path
  on public.message_attachments (storage_path)
  where storage_path is not null;

alter table public.media_objects enable row level security;

comment on table public.media_objects is
  'Server-only. RLS aktif tanpa policy authenticated. Memetakan hash konten berkas ke satu objek di bucket, supaya broadcast ke banyak penerima tidak menyimpan salinan berulang.';
comment on column public.media_objects.file_sha256 is
  'Hash konten berkas apa adanya, nilai yang sama dengan whatsapp_media_refs.file_sha256.';
comment on column public.media_objects.storage_path is
  'Kunci objek di bucket. Berbentuk {workspace_id}/by-hash/{sha256}, jadi dapat dihitung ulang dari hash dan tidak bergantung pada pesan mana pun.';
