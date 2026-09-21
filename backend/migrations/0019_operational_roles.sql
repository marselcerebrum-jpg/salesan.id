-- =============================================================================
-- salesan.id — Migration 0019: hierarki operasional (Leader / PIC / Freelance)
-- =============================================================================
-- Workspace sudah punya `public.users.role` dengan enum owner/admin/agent. Itu
-- adalah peran KEPEMILIKAN workspace — siapa yang boleh mengubah pengaturan —
-- dan tidak dibongkar di sini, karena seluruh RLS lama bergantung padanya.
--
-- Yang ditambahkan adalah lapisan kedua: peran OPERASIONAL. Seseorang bisa saja
-- owner workspace sekaligus Leader, atau agent yang bertugas sebagai Freelance.
-- Dua pertanyaan yang berbeda ("boleh mengubah apa" vs "bertanggung jawab atas
-- apa") dijawab oleh dua kolom yang berbeda; menggabungkannya adalah cara
-- tercepat membuat salah satunya salah.
--
-- Bentuk hierarkinya:
--
--   Leader     — melihat seluruh workspace.
--   PIC        — memegang satu atau beberapa aplikasi, membawahi Freelance.
--   Freelance  — berada di bawah tepat satu PIC, bertugas pada satu atau
--                beberapa aplikasi.
--
-- Aman dijalankan ulang.
-- =============================================================================

do $$ begin
  create type public.operational_role as enum ('leader', 'pic', 'freelance');
exception when duplicate_object then null; end $$;

-- -----------------------------------------------------------------------------
-- role_assignments — satu peran operasional per orang per workspace
-- -----------------------------------------------------------------------------
create table if not exists public.role_assignments (
  id            uuid primary key default gen_random_uuid(),
  workspace_id  uuid not null references public.workspaces (id) on delete cascade,
  user_id       uuid not null references public.users (id) on delete cascade,
  role          public.operational_role not null,
  -- Nama saat penugasan dibuat. Dipakai audit log supaya riwayat lama tidak
  -- ikut berubah ketika seseorang mengganti namanya.
  display_name  text,
  is_active     boolean not null default true,
  assigned_by   uuid references public.users (id) on delete set null,
  created_at    timestamptz not null default now(),
  updated_at    timestamptz not null default now(),
  constraint uq_role_assignments_user unique (workspace_id, user_id)
);

create index if not exists idx_role_assignments_workspace
  on public.role_assignments (workspace_id, role) where is_active;

-- Dicari per user pada jalur tulis pesan (lihat trigger atribusi di 0021).
-- Batasan uniknya berawalan workspace_id, jadi tidak melayani pencarian ini.
create index if not exists idx_role_assignments_user
  on public.role_assignments (user_id) where is_active;

drop trigger if exists trg_role_assignments_updated_at on public.role_assignments;
create trigger trg_role_assignments_updated_at
  before update on public.role_assignments
  for each row execute function public.touch_updated_at();

-- -----------------------------------------------------------------------------
-- pic_application_assignments — aplikasi yang dipegang seorang PIC
-- -----------------------------------------------------------------------------
create table if not exists public.pic_application_assignments (
  workspace_id    uuid not null references public.workspaces (id) on delete cascade,
  pic_user_id     uuid not null references public.users (id) on delete cascade,
  application_id  uuid not null references public.applications (id) on delete cascade,
  assigned_by     uuid references public.users (id) on delete set null,
  created_at      timestamptz not null default now(),
  primary key (pic_user_id, application_id)
);

create index if not exists idx_pic_app_assignments_app
  on public.pic_application_assignments (application_id);
create index if not exists idx_pic_app_assignments_ws
  on public.pic_application_assignments (workspace_id);

-- -----------------------------------------------------------------------------
-- freelancer_pic_assignments — setiap Freelance di bawah TEPAT SATU PIC
--
-- Keunikan pada freelancer_user_id, bukan pada pasangannya: "berada di bawah
-- satu PIC" adalah aturan bisnis, dan tempat menegakkannya adalah di sini,
-- bukan di layar yang membuat penugasannya.
-- -----------------------------------------------------------------------------
create table if not exists public.freelancer_pic_assignments (
  workspace_id        uuid not null references public.workspaces (id) on delete cascade,
  freelancer_user_id  uuid not null references public.users (id) on delete cascade,
  pic_user_id         uuid not null references public.users (id) on delete cascade,
  assigned_by         uuid references public.users (id) on delete set null,
  created_at          timestamptz not null default now(),
  updated_at          timestamptz not null default now(),
  primary key (freelancer_user_id)
);

create index if not exists idx_freelancer_pic_pic
  on public.freelancer_pic_assignments (pic_user_id);
create index if not exists idx_freelancer_pic_ws
  on public.freelancer_pic_assignments (workspace_id);

drop trigger if exists trg_freelancer_pic_updated_at on public.freelancer_pic_assignments;
create trigger trg_freelancer_pic_updated_at
  before update on public.freelancer_pic_assignments
  for each row execute function public.touch_updated_at();

-- -----------------------------------------------------------------------------
-- freelancer_application_assignments — aplikasi yang ditugaskan ke Freelance
-- -----------------------------------------------------------------------------
create table if not exists public.freelancer_application_assignments (
  workspace_id        uuid not null references public.workspaces (id) on delete cascade,
  freelancer_user_id  uuid not null references public.users (id) on delete cascade,
  application_id      uuid not null references public.applications (id) on delete cascade,
  assigned_by         uuid references public.users (id) on delete set null,
  created_at          timestamptz not null default now(),
  primary key (freelancer_user_id, application_id)
);

create index if not exists idx_freelancer_app_app
  on public.freelancer_application_assignments (application_id);
create index if not exists idx_freelancer_app_ws
  on public.freelancer_application_assignments (workspace_id);

-- =============================================================================
-- Fungsi lingkup akses
--
-- SECURITY DEFINER supaya kebijakan yang memakainya tidak ikut tunduk pada RLS
-- tabel yang dibacanya — pola yang sama dengan current_workspace_id() di 0001,
-- dan alasan yang sama: tanpa itu kebijakannya akan memanggil dirinya sendiri.
-- =============================================================================

-- Peran operasional pemanggil. NULL bila belum pernah ditugaskan.
create or replace function public.current_operational_role()
returns public.operational_role
language sql
stable
security definer
set search_path = public
as $$
  select ra.role
    from public.role_assignments ra
   where ra.user_id = auth.uid()
     and ra.is_active
   limit 1
$$;

revoke all on function public.current_operational_role() from public;
grant execute on function public.current_operational_role() to authenticated, service_role;

-- Aplikasi yang boleh dilihat pemanggil.
--
-- Leader: seluruh aplikasi workspace. PIC: yang dipegangnya. Freelance: yang
-- ditugaskan kepadanya. Seseorang tanpa peran operasional diperlakukan sebagai
-- Leader HANYA bila ia owner/admin workspace — itu menjaga workspace lama yang
-- belum menetapkan peran apa pun tetap bisa dipakai pemiliknya.
create or replace function public.visible_application_ids()
returns setof uuid
language sql
stable
security definer
set search_path = public
as $$
  with me as (
    select u.id, u.workspace_id, u.role::text as ws_role,
           public.current_operational_role() as op_role
      from public.users u
     where u.id = auth.uid()
  )
  select a.id
    from public.applications a, me
   where a.workspace_id = me.workspace_id
     and (
       me.op_role = 'leader'
       or (me.op_role is null and me.ws_role in ('owner', 'admin'))
     )
  union
  select p.application_id
    from public.pic_application_assignments p, me
   where p.pic_user_id = me.id and me.op_role = 'pic'
  union
  select f.application_id
    from public.freelancer_application_assignments f, me
   where f.freelancer_user_id = me.id and me.op_role = 'freelance'
$$;

revoke all on function public.visible_application_ids() from public;
grant execute on function public.visible_application_ids() to authenticated, service_role;

-- Admin (user) yang performanya boleh dilihat pemanggil.
--
-- Selalu memuat dirinya sendiri: seorang Freelance melihat dirinya dan tidak
-- ada orang lain, dan itu adalah kasus yang paling mudah salah bila daftarnya
-- dibangun dari sisi "siapa yang di bawah saya" saja.
create or replace function public.visible_admin_ids()
returns setof uuid
language sql
stable
security definer
set search_path = public
as $$
  with me as (
    select u.id, u.workspace_id, u.role::text as ws_role,
           public.current_operational_role() as op_role
      from public.users u
     where u.id = auth.uid()
  )
  select me.id from me
  union
  select u.id
    from public.users u, me
   where u.workspace_id = me.workspace_id
     and (
       me.op_role = 'leader'
       or (me.op_role is null and me.ws_role in ('owner', 'admin'))
     )
  union
  -- PIC melihat Freelance yang ada di bawahnya.
  select fp.freelancer_user_id
    from public.freelancer_pic_assignments fp, me
   where fp.pic_user_id = me.id and me.op_role = 'pic'
$$;

revoke all on function public.visible_admin_ids() from public;
grant execute on function public.visible_admin_ids() to authenticated, service_role;

-- =============================================================================
-- Backfill: pemilik workspace menjadi Leader
--
-- Tanpa ini setiap workspace yang sudah ada akan kehilangan akses ke halaman
-- baru sampai seseorang menetapkan peran — padahal orang yang seharusnya
-- menetapkannya adalah pemilik itu sendiri.
-- =============================================================================
insert into public.role_assignments (workspace_id, user_id, role, display_name)
select u.workspace_id, u.id, 'leader', u.full_name
  from public.users u
 where u.role = 'owner'
on conflict (workspace_id, user_id) do nothing;

-- =============================================================================
-- RLS
-- =============================================================================
alter table public.role_assignments                   enable row level security;
alter table public.pic_application_assignments        enable row level security;
alter table public.freelancer_pic_assignments         enable row level security;
alter table public.freelancer_application_assignments enable row level security;

-- Semua anggota workspace boleh MEMBACA struktur organisasi — sebuah Freelance
-- perlu tahu siapa PIC-nya. Yang dibatasi adalah menulisnya.
drop policy if exists role_assignments_select on public.role_assignments;
create policy role_assignments_select on public.role_assignments
  for select to authenticated
  using (workspace_id = public.current_workspace_id());

drop policy if exists role_assignments_write on public.role_assignments;
create policy role_assignments_write on public.role_assignments
  for all to authenticated
  using (workspace_id = public.current_workspace_id()
         and coalesce(public.current_operational_role() = 'leader', false))
  with check (workspace_id = public.current_workspace_id()
              and coalesce(public.current_operational_role() = 'leader', false));

do $$
declare
  t text;
begin
  foreach t in array array[
    'pic_application_assignments',
    'freelancer_pic_assignments',
    'freelancer_application_assignments'
  ] loop
    execute format('drop policy if exists %I on public.%I', t || '_select', t);
    execute format($f$
      create policy %I on public.%I
        for select to authenticated
        using (workspace_id = public.current_workspace_id())
    $f$, t || '_select', t);

    execute format('drop policy if exists %I on public.%I', t || '_write', t);
    execute format($f$
      create policy %I on public.%I
        for all to authenticated
        using (workspace_id = public.current_workspace_id()
               and coalesce(public.current_operational_role() = 'leader', false))
        with check (workspace_id = public.current_workspace_id()
                    and coalesce(public.current_operational_role() = 'leader', false))
    $f$, t || '_write', t);
  end loop;
end $$;
