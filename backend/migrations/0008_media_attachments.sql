-- =============================================================================
-- salesan.id — Migration 0008: lampiran media & dokumen
-- =============================================================================
-- Berkas tidak pernah disimpan di database. Yang disimpan hanya metadata dan
-- lokasinya di Supabase Storage (bucket privat), sehingga tabel tetap ramping
-- dan berkasnya bisa dilayani lewat signed URL berumur pendek.
--
-- Bahan untuk mengunduh ulang media dari WhatsApp (media key, direct path)
-- diletakkan di tabel terpisah yang RLS-nya menutup total akses browser —
-- pola yang sama dengan whatsapp_sessions. Kunci itu setara kredensial: siapa
-- pun yang memilikinya bisa mendekripsi media dari server WhatsApp.
--
-- Aman dijalankan ulang.
-- =============================================================================

do $$ begin
  create type public.attachment_kind as enum
    ('image', 'video', 'audio', 'document', 'sticker');
exception when duplicate_object then null; end $$;

do $$ begin
  create type public.attachment_storage_status as enum
    ('pending', 'uploading', 'stored', 'failed');
exception when duplicate_object then null; end $$;

-- -----------------------------------------------------------------------------
-- message_attachments — metadata yang boleh dibaca browser
-- -----------------------------------------------------------------------------
create table if not exists public.message_attachments (
  id             uuid primary key default gen_random_uuid(),
  workspace_id   uuid not null references public.workspaces (id) on delete cascade,
  account_id     uuid not null references public.whatsapp_accounts (id) on delete cascade,
  message_id     uuid not null references public.messages (id) on delete cascade,

  -- Urutan lampiran dalam satu pesan. WhatsApp mengirim satu media per pesan,
  -- jadi nilainya selalu 0 untuk sekarang; kolomnya ada supaya album tidak
  -- memerlukan perubahan skema.
  idx            int not null default 0,

  kind           public.attachment_kind not null,
  file_name      text,
  mime_type      text,
  size_bytes     bigint,
  width          int,
  height         int,
  duration_secs  int,
  -- Thumbnail kecil dari WhatsApp, dipakai agar bubble punya isi sebelum
  -- berkas penuhnya selesai diunduh. Base64 JPEG berukuran ~1-2 KB.
  thumbnail_b64  text,

  storage_path   text,
  storage_status public.attachment_storage_status not null default 'pending',
  storage_error  text,

  created_at     timestamptz not null default now(),
  updated_at     timestamptz not null default now(),

  constraint uq_message_attachments_slot unique (message_id, idx)
);

create index if not exists idx_message_attachments_message
  on public.message_attachments (message_id);
create index if not exists idx_message_attachments_pending
  on public.message_attachments (account_id)
  where storage_status in ('pending', 'failed');

drop trigger if exists trg_message_attachments_updated_at on public.message_attachments;
create trigger trg_message_attachments_updated_at
  before update on public.message_attachments
  for each row execute function public.touch_updated_at();

-- -----------------------------------------------------------------------------
-- whatsapp_media_refs — bahan unduh, server-only
--
-- RLS aktif TANPA policy untuk role `authenticated`: browser mendapat nol
-- baris. Hanya service role (backend) yang bisa membacanya.
-- -----------------------------------------------------------------------------
create table if not exists public.whatsapp_media_refs (
  attachment_id     uuid primary key references public.message_attachments (id) on delete cascade,
  direct_path       text,
  media_key         bytea,
  file_enc_sha256   bytea,
  file_sha256       bytea,
  media_type        text,
  created_at        timestamptz not null default now()
);

-- -----------------------------------------------------------------------------
-- messages: jumlah lampiran + kunci idempotensi kiriman
-- -----------------------------------------------------------------------------
alter table public.messages
  add column if not exists attachment_count int not null default 0;

-- client_token membuat "coba lagi" aman.
--
-- Saat mengirim berkas, koneksi bisa putus setelah server menerima permintaan
-- tetapi sebelum browser menerima jawabannya. Browser tidak bisa membedakan itu
-- dari kegagalan, jadi ia akan mencoba lagi. Token yang sama pada percobaan
-- ulang membuat penyisipan kedua ditolak oleh indeks unik ini — sebelum apa pun
-- dikirim ke WhatsApp — sehingga pesan tidak pernah terkirim dua kali.
alter table public.messages
  add column if not exists client_token text;

create unique index if not exists uq_messages_client_token
  on public.messages (account_id, client_token)
  where client_token is not null;

-- Jaga hitungannya tetap benar tanpa perlu diingat lapisan aplikasi.
create or replace function public.sync_message_attachment_count()
returns trigger
language plpgsql
as $$
declare
  v_message uuid := coalesce(new.message_id, old.message_id);
begin
  update public.messages m
     set attachment_count = (
           select count(*) from public.message_attachments a where a.message_id = m.id
         )
   where m.id = v_message;
  return null;
end;
$$;

drop trigger if exists trg_message_attachments_count on public.message_attachments;
create trigger trg_message_attachments_count
  after insert or delete on public.message_attachments
  for each row execute function public.sync_message_attachment_count();

-- -----------------------------------------------------------------------------
-- RLS
-- -----------------------------------------------------------------------------
alter table public.message_attachments  enable row level security;
alter table public.whatsapp_media_refs  enable row level security;

drop policy if exists message_attachments_rw on public.message_attachments;
create policy message_attachments_rw on public.message_attachments
  for all to authenticated
  using (workspace_id = public.current_workspace_id())
  with check (workspace_id = public.current_workspace_id());

comment on table public.whatsapp_media_refs is
  'Server-only. RLS enabled with no authenticated policy: media keys never reach a browser.';

-- -----------------------------------------------------------------------------
-- Realtime
-- -----------------------------------------------------------------------------
alter table public.message_attachments replica identity full;

do $$
begin
  if exists (select 1 from pg_publication where pubname = 'supabase_realtime') then
    begin
      alter publication supabase_realtime add table public.message_attachments;
    exception when duplicate_object then null; end;
  end if;
end $$;

-- =============================================================================
-- CATATAN SETUP — bucket Storage harus dibuat sekali, manual.
--
-- Supabase Dashboard → Storage → New bucket
--   Name   : wa-media
--   Public : NO  (wajib privat — berkas hanya dilayani lewat signed URL)
--
-- Tidak ada policy yang perlu ditambahkan: backend mengaksesnya memakai
-- service role, yang melewati RLS Storage. Browser tidak pernah menyentuh
-- bucket secara langsung.
-- =============================================================================
