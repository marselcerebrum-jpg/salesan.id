-- 0032 — variabel pesan dan balas cepat, dilingkupi per aplikasi.
--
-- Dua hal terjadi di sini.
--
-- Pertama, custom_variables mendapat application_id. Sebelumnya sebuah
-- variabel berlaku untuk seluruh workspace, yang keliru begitu satu workspace
-- memegang tiga belas aplikasi: "nama_toko" milik satu brand tidak ada artinya
-- di brand lain, dan composer menawarkannya ke semua orang.
--
-- NULL tetap sah dan berarti "berlaku di semua aplikasi". Itu bukan kelalaian
-- melainkan pilihan: sebagian variabel memang milik perusahaan, bukan milik
-- satu brand, dan memaksa setiap variabel punya aplikasi akan memaksa orang
-- menduplikasinya tiga belas kali.
--
-- Kedua, quick_replies: potongan pesan siap pakai yang dipanggil di ruang chat
-- dengan mengetik garis miring. Bentuknya sengaja mengikuti custom_variables,
-- termasuk soal NULL di atas, supaya keduanya bisa disaring dan dikelompokkan
-- dengan cara yang sama.

begin;

-- --------------------------------------------------------------------------
-- custom_variables: lingkup aplikasi
-- --------------------------------------------------------------------------

alter table public.custom_variables
  add column if not exists application_id uuid
    references public.applications (id) on delete cascade;

create index if not exists idx_custom_variables_application
  on public.custom_variables (workspace_id, application_id);

-- Keunikan kunci sekarang per aplikasi, bukan per workspace.
--
-- Dua indeks, bukan satu: di Postgres NULL tidak sama dengan NULL, jadi indeks
-- unik biasa akan mengizinkan dua variabel workspace-wide dengan kunci sama.
-- Yang pertama menjaga variabel milik aplikasi, yang kedua menjaga variabel
-- yang tidak punya aplikasi.
drop index if exists uq_custom_variables_key;

create unique index if not exists uq_custom_variables_key_per_app
  on public.custom_variables (workspace_id, application_id, lower(key))
  where application_id is not null;

create unique index if not exists uq_custom_variables_key_global
  on public.custom_variables (workspace_id, lower(key))
  where application_id is null;

-- --------------------------------------------------------------------------
-- quick_replies
-- --------------------------------------------------------------------------

create table if not exists public.quick_replies (
  id             uuid primary key default gen_random_uuid(),
  workspace_id   uuid not null references public.workspaces (id) on delete cascade,
  -- NULL berarti tersedia di semua aplikasi. Lihat catatan di atas.
  application_id uuid references public.applications (id) on delete cascade,

  -- Yang diketik setelah garis miring. Disimpan tanpa garis miringnya: tanda
  -- itu milik antarmuka, bukan milik datanya.
  shortcut       text not null,
  title          text not null,
  body           text not null,

  is_active      boolean not null default true,
  created_by     uuid references public.users (id) on delete set null,
  created_at     timestamptz not null default now(),
  updated_at     timestamptz not null default now(),

  -- Aturan yang sama dengan kunci variabel, supaya keduanya terasa satu sistem.
  constraint quick_replies_shortcut_check check (shortcut ~ '^[a-z0-9_]{2,40}$'),
  constraint quick_replies_body_check check (length(body) between 1 and 4096),
  constraint quick_replies_title_check check (length(title) between 1 and 120)
);

create index if not exists idx_quick_replies_application
  on public.quick_replies (workspace_id, application_id)
  where is_active;

create unique index if not exists uq_quick_replies_shortcut_per_app
  on public.quick_replies (workspace_id, application_id, lower(shortcut))
  where application_id is not null;

create unique index if not exists uq_quick_replies_shortcut_global
  on public.quick_replies (workspace_id, lower(shortcut))
  where application_id is null;

drop trigger if exists trg_quick_replies_updated_at on public.quick_replies;
create trigger trg_quick_replies_updated_at
  before update on public.quick_replies
  for each row execute function public.touch_updated_at();

-- --------------------------------------------------------------------------
-- RLS
-- --------------------------------------------------------------------------

alter table public.quick_replies enable row level security;

-- Membaca: siapa pun di workspace, tetapi hanya yang berada dalam jangkauan
-- aplikasinya. Sebuah balas cepat milik satu brand tidak berguna bagi orang
-- yang tidak memegang brand itu, dan menawarkannya hanya mengundang salah
-- kirim. Yang tanpa aplikasi terlihat oleh semua.
drop policy if exists quick_replies_select on public.quick_replies;
create policy quick_replies_select on public.quick_replies
  for select to authenticated
  using (
    workspace_id = public.current_workspace_id()
    and (
      application_id is null
      or public.current_operational_role() is null
      or public.current_operational_role() = 'leader'
      or application_id in (select public.visible_application_ids())
    )
  );

-- Menulis: Leader dan PIC. Freelance memakainya, tidak mendefinisikannya, sama
-- seperti label campaign dan variabel.
drop policy if exists quick_replies_write on public.quick_replies;
create policy quick_replies_write on public.quick_replies
  for all to authenticated
  using (
    workspace_id = public.current_workspace_id()
    and coalesce(public.current_operational_role() in ('leader', 'pic'), true)
    and (
      application_id is null
      or coalesce(public.current_operational_role(), 'leader') = 'leader'
      or application_id in (select public.visible_application_ids())
    )
  )
  with check (
    workspace_id = public.current_workspace_id()
    and coalesce(public.current_operational_role() in ('leader', 'pic'), true)
    and (
      application_id is null
      or coalesce(public.current_operational_role(), 'leader') = 'leader'
      or application_id in (select public.visible_application_ids())
    )
  );

-- Variabel mengikuti aturan baca yang sama sekarang bahwa ia punya aplikasi.
drop policy if exists custom_variables_select on public.custom_variables;
create policy custom_variables_select on public.custom_variables
  for select to authenticated
  using (
    workspace_id = public.current_workspace_id()
    and (
      application_id is null
      or public.current_operational_role() is null
      or public.current_operational_role() = 'leader'
      or application_id in (select public.visible_application_ids())
    )
  );

commit;
