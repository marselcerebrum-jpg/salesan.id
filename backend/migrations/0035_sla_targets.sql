-- 0035 — target SLA yang bisa diatur, bukan lagi variabel lingkungan.
--
-- Sebelum ini satu-satunya sumber target adalah SLA_TARGET_SECONDS di .env:
-- satu angka untuk seluruh workspace, hanya bisa diubah dengan menyentuh
-- server, dan tidak bisa berbeda antar brand. Sebuah toko yang menjanjikan
-- balasan lima menit dan sebuah layanan B2B yang wajar dibalas satu jam
-- terpaksa memakai angka yang sama.
--
-- Bentuknya mengikuti pola yang sudah dipakai variabel pesan dan balas cepat:
-- application_id NULL berarti berlaku untuk seluruh workspace, baris dengan
-- aplikasi menimpanya. Pencarian selalu mencoba yang spesifik dulu, lalu
-- jatuh ke bawaan workspace, lalu ke nilai .env sebagai lapis terakhir supaya
-- workspace yang belum mengatur apa pun tetap terukur.
--
-- Yang TIDAK berubah karena migrasi ini: angka historis. Target disalin ke
-- setiap sla_cycles saat siklus itu dibuat, jadi mengubah pengaturan hari ini
-- tidak menilai ulang bulan lalu. Itu memang inti aturannya.

begin;

create table if not exists public.sla_targets (
  id             uuid primary key default gen_random_uuid(),
  workspace_id   uuid not null references public.workspaces (id) on delete cascade,
  -- NULL = bawaan workspace. Lihat catatan di atas.
  application_id uuid references public.applications (id) on delete cascade,

  target_seconds int not null,
  -- Apakah waktu tunggu hanya dihitung di dalam jam kerja. Menghitung penuh
  -- akan membuat pesan yang masuk pukul dua pagi selalu melewati target,
  -- sekalipun dibalas menit pertama esok paginya.
  business_hours boolean not null default true,

  updated_by     uuid references public.users (id) on delete set null,
  created_at     timestamptz not null default now(),
  updated_at     timestamptz not null default now(),

  -- Satu menit sampai satu hari. Batas bawah mencegah target yang mustahil
  -- dipenuhi manusia; batas atas mencegah salah ketik yang membuat SLA selalu
  -- tercapai dan angkanya berhenti berarti.
  constraint sla_targets_seconds_check check (target_seconds between 60 and 86400)
);

create unique index if not exists uq_sla_targets_per_app
  on public.sla_targets (workspace_id, application_id)
  where application_id is not null;

create unique index if not exists uq_sla_targets_default
  on public.sla_targets (workspace_id)
  where application_id is null;

drop trigger if exists trg_sla_targets_updated_at on public.sla_targets;
create trigger trg_sla_targets_updated_at
  before update on public.sla_targets
  for each row execute function public.touch_updated_at();

-- --------------------------------------------------------------------------
-- RLS
-- --------------------------------------------------------------------------

alter table public.sla_targets enable row level security;

-- Membaca: siapa pun di workspace. Seorang Freelance diukur dengan target ini,
-- jadi menyembunyikannya dari mereka berarti menilai orang dengan aturan yang
-- tidak boleh mereka lihat.
drop policy if exists sla_targets_select on public.sla_targets;
create policy sla_targets_select on public.sla_targets
  for select to authenticated
  using (workspace_id = public.current_workspace_id());

-- Menulis: Leader untuk bawaan workspace, PIC hanya untuk aplikasinya sendiri.
drop policy if exists sla_targets_write on public.sla_targets;
create policy sla_targets_write on public.sla_targets
  for all to authenticated
  using (
    workspace_id = public.current_workspace_id()
    and coalesce(public.current_operational_role() in ('leader', 'pic'), true)
    and (
      application_id is null
        and coalesce(public.current_operational_role(), 'leader') = 'leader'
      or application_id in (select public.visible_application_ids())
    )
  )
  with check (
    workspace_id = public.current_workspace_id()
    and coalesce(public.current_operational_role() in ('leader', 'pic'), true)
    and (
      application_id is null
        and coalesce(public.current_operational_role(), 'leader') = 'leader'
      or application_id in (select public.visible_application_ids())
    )
  );

commit;
