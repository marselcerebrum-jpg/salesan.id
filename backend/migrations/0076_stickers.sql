-- Pustaka stiker milik ruang kerja.
--
-- Stiker tidak disimpan pada percakapan seperti lampiran, sebab ia bukan
-- riwayat melainkan perkakas: satu berkas yang dipakai berkali-kali ke banyak
-- chat. Menyimpannya sebagai lampiran berarti satu baris per pengiriman untuk
-- gambar yang itu-itu juga, dan tidak ada tempat untuk menampilkannya sebelum
-- dikirim.
--
-- Berkasnya sendiri tetap di bucket yang sama dengan media lain, dan dengan
-- dedup yang sama: dua orang mengunggah stiker yang identik hanya memakai satu
-- objek. Yang disimpan di sini jalurnya, bukan isinya -- aturan yang sudah
-- berlaku di seluruh sistem ini.
--
-- Satu hal yang membedakannya dari lampiran chat: stiker TIDAK ikut retensi
-- dua hari. Lampiran dua hari adalah salinan percakapan yang masih bisa dilihat
-- di HP; stiker yang hilang setelah dua hari adalah perkakas yang lenyap dari
-- laci. Karena itu objeknya ditandai agar penyapu melewatinya.

create table if not exists public.stickers (
  id           uuid primary key default gen_random_uuid(),
  workspace_id uuid not null references public.workspaces(id) on delete cascade,
  -- Nama yang diketik orang supaya bisa dicari. Boleh kosong: sebagian stiker
  -- hanya dikenali dari gambarnya.
  name         text,
  storage_path text not null,
  mime_type    text not null default 'image/webp',
  size_bytes   bigint not null default 0,
  file_sha256  text not null,
  created_by   uuid references public.users(id) on delete set null,
  created_at   timestamptz not null default now(),
  deleted_at   timestamptz
);

-- Satu gambar sekali saja per ruang kerja. Tanpa ini, mengunggah stiker yang
-- sama dua kali memberi dua ubin yang tidak bisa dibedakan di panel.
create unique index if not exists idx_stickers_unique
  on public.stickers (workspace_id, file_sha256)
  where deleted_at is null;

create index if not exists idx_stickers_workspace
  on public.stickers (workspace_id, created_at desc)
  where deleted_at is null;

alter table public.stickers enable row level security;

-- Dibaca dan ditulis hanya lewat backend, seperti media_objects. Jalur dari
-- peramban selalu melewati API yang sudah memeriksa ruang kerjanya.
drop policy if exists stickers_server_only on public.stickers;
create policy stickers_server_only on public.stickers
  for all to service_role using (true) with check (true);

comment on table public.stickers is
  'Pustaka stiker per ruang kerja. Berkasnya di bucket media, tidak ikut retensi dua hari.';
