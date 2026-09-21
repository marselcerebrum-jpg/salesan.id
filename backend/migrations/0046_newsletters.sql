-- -----------------------------------------------------------------------------
-- Saluran (newsletter), dan indeks untuk percakapan berjenis status
--
-- Saluran bukan percakapan: tidak ada balasan, isinya diambil dari server
-- WhatsApp saat dibuka, dan yang perlu disimpan hanyalah saluran mana yang
-- diikuti akun ini beserta keterangannya. Pesannya sengaja tidak disimpan;
-- menyalin isi saluran orang lain ke basis data kita menambah penyimpanan dan
-- kewajiban tanpa menjawab satu pun pertanyaan yang diajukan layarnya.
-- -----------------------------------------------------------------------------

-- Status hidup 24 jam, seperti di WhatsApp. Dibaca penyapu yang menghapusnya
-- setelah lewat, bersama lampirannya.
create index if not exists idx_conversations_status
  on public.conversations (account_id)
  where type = 'status';

create table if not exists public.newsletters (
  id               uuid primary key default gen_random_uuid(),
  workspace_id     uuid not null references public.workspaces (id) on delete cascade,
  account_id       uuid not null references public.whatsapp_accounts (id) on delete cascade,
  -- Alamat saluran, "<id>@newsletter".
  jid              text not null,
  name             text not null default '',
  description      text,
  -- Jumlah pengikut menurut WhatsApp saat terakhir disinkronkan. Bukan angka
  -- yang kita hitung sendiri, jadi disimpan apa adanya beserta waktunya.
  subscriber_count int,
  picture_url      text,
  -- owner, admin, subscriber, atau guest menurut WhatsApp. Ini yang menentukan
  -- apakah layarnya boleh menawarkan pengelolaan atau hanya membaca.
  viewer_role      text not null default 'subscriber',
  muted            boolean not null default false,
  verified         boolean not null default false,
  -- Kapan saluran ini dibuat menurut WhatsApp, bukan kapan barisnya dibuat.
  created_on       timestamptz,
  synced_at        timestamptz not null default now(),
  created_at       timestamptz not null default now(),
  updated_at       timestamptz not null default now(),
  constraint uq_newsletters_account_jid unique (account_id, jid)
);

create index if not exists idx_newsletters_account
  on public.newsletters (account_id, name);

drop trigger if exists trg_newsletters_updated_at on public.newsletters;
create trigger trg_newsletters_updated_at
  before update on public.newsletters
  for each row execute function public.touch_updated_at();

alter table public.newsletters enable row level security;

drop policy if exists newsletters_select on public.newsletters;
create policy newsletters_select on public.newsletters
  for select to authenticated
  using (workspace_id = public.current_workspace_id());

drop policy if exists newsletters_write on public.newsletters;
create policy newsletters_write on public.newsletters
  for all to authenticated
  using (workspace_id = public.current_workspace_id())
  with check (workspace_id = public.current_workspace_id());
