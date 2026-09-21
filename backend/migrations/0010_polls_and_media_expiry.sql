-- =============================================================================
-- salesan.id — Migration 0010: polling + masa berlaku media
-- =============================================================================
-- Dua penambahan:
--
--   (1) Polling. WhatsApp mengirim pilihan sebagai hash SHA-256 dari teks
--       pilihannya, bukan sebagai nomor urut — jadi hash itu ikut disimpan,
--       karena itulah satu-satunya cara memetakan suara kembali ke pilihan.
--
--   (2) Status 'expired' untuk lampiran. Berkas media dihapus dari bucket
--       setelah lewat jendela sinkron (default 7 hari); barisnya tetap ada
--       supaya percakapan tidak berlubang, tetapi berkasnya benar-benar hilang.
--
-- CATATAN: berkas ini hanya MENAMBAH nilai enum, tidak memakainya. PostgreSQL
-- melarang nilai enum baru dipakai pada transaksi yang sama dengan yang
-- menambahkannya, jadi yang memakai ada di 0011.
--
-- Aman dijalankan ulang.
-- =============================================================================

alter type public.message_type add value if not exists 'poll';
alter type public.attachment_storage_status add value if not exists 'expired';

-- -----------------------------------------------------------------------------
-- message_polls — pertanyaan
-- -----------------------------------------------------------------------------
create table if not exists public.message_polls (
  message_id       uuid primary key references public.messages (id) on delete cascade,
  workspace_id     uuid not null references public.workspaces (id) on delete cascade,
  account_id       uuid not null references public.whatsapp_accounts (id) on delete cascade,
  name             text not null,
  -- 0 berarti "boleh pilih semua"; 1 berarti pilihan tunggal.
  selectable_count int  not null default 1,
  created_at       timestamptz not null default now()
);

-- -----------------------------------------------------------------------------
-- message_poll_options — pilihan
--
-- option_hash adalah SHA-256 dari teks pilihan, disimpan hex. Itulah yang
-- dikirim WhatsApp di dalam suara, sehingga pemetaan suara → pilihan tidak
-- bergantung pada urutan maupun pada teks yang sama persis di kedua sisi.
-- -----------------------------------------------------------------------------
create table if not exists public.message_poll_options (
  id          uuid primary key default gen_random_uuid(),
  message_id  uuid not null references public.message_polls (message_id) on delete cascade,
  idx         int  not null,
  name        text not null,
  option_hash text not null,
  constraint uq_poll_option_slot unique (message_id, idx)
);

create index if not exists idx_poll_options_hash
  on public.message_poll_options (message_id, option_hash);

-- -----------------------------------------------------------------------------
-- message_poll_votes — suara
--
-- Satu baris per (pemilih, pilihan). Sebuah pembaruan suara membawa seluruh
-- pilihan pemilih saat itu, jadi penerapannya adalah: hapus semua baris milik
-- pemilih itu, lalu masukkan yang baru. Itu sebabnya voted_at ada di sini —
-- suara yang datang terlambat tidak boleh menimpa yang lebih baru.
-- -----------------------------------------------------------------------------
create table if not exists public.message_poll_votes (
  message_id uuid not null references public.message_polls (message_id) on delete cascade,
  voter_jid  text not null,
  option_idx int  not null,
  voted_at   timestamptz not null default now(),
  primary key (message_id, voter_jid, option_idx)
);

create index if not exists idx_poll_votes_message
  on public.message_poll_votes (message_id);

-- -----------------------------------------------------------------------------
-- RLS
-- -----------------------------------------------------------------------------
alter table public.message_polls        enable row level security;
alter table public.message_poll_options enable row level security;
alter table public.message_poll_votes   enable row level security;

drop policy if exists message_polls_rw on public.message_polls;
create policy message_polls_rw on public.message_polls
  for all to authenticated
  using (workspace_id = public.current_workspace_id())
  with check (workspace_id = public.current_workspace_id());

-- Pilihan dan suara mewarisi cakupan dari pollnya; tidak ada workspace_id
-- terpisah supaya tidak mungkin melenceng dari induknya.
drop policy if exists message_poll_options_rw on public.message_poll_options;
create policy message_poll_options_rw on public.message_poll_options
  for all to authenticated
  using (exists (
    select 1 from public.message_polls p
     where p.message_id = message_poll_options.message_id
       and p.workspace_id = public.current_workspace_id()))
  with check (exists (
    select 1 from public.message_polls p
     where p.message_id = message_poll_options.message_id
       and p.workspace_id = public.current_workspace_id()));

drop policy if exists message_poll_votes_rw on public.message_poll_votes;
create policy message_poll_votes_rw on public.message_poll_votes
  for all to authenticated
  using (exists (
    select 1 from public.message_polls p
     where p.message_id = message_poll_votes.message_id
       and p.workspace_id = public.current_workspace_id()))
  with check (exists (
    select 1 from public.message_polls p
     where p.message_id = message_poll_votes.message_id
       and p.workspace_id = public.current_workspace_id()));

-- -----------------------------------------------------------------------------
-- Realtime
-- -----------------------------------------------------------------------------
alter table public.message_poll_votes replica identity full;

do $$
begin
  if exists (select 1 from pg_publication where pubname = 'supabase_realtime') then
    begin
      alter publication supabase_realtime add table public.message_poll_votes;
    exception when duplicate_object then null; end;
  end if;
end $$;
