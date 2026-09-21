-- =============================================================================
-- salesan.id — Migration 0020: jadwal kerja admin
-- =============================================================================
-- Jadwal menjawab dua pertanyaan yang berbeda, dan keduanya perlu:
--
--   1. Berapa lama orang ini bekerja hari itu? — pembagi setiap metrik "per
--      jam". Tanpa itu, membandingkan dua Freelance berarti membandingkan
--      seseorang yang bertugas dua jam dengan seseorang yang bertugas delapan.
--   2. Kapan jam kerjanya? — SLA dengan mode business hours hanya menghitung
--      waktu yang jatuh di dalam jendela ini. Pesan yang masuk jam 2 pagi tidak
--      boleh membuat SLA seseorang terlihat terlewat karena ia sedang tidur.
--
-- Satu baris = satu blok tugas pada satu tanggal. Seorang Freelance boleh punya
-- beberapa baris pada tanggal yang sama untuk aplikasi yang berbeda, dan blok
-- itu boleh bertumpuk waktunya — dua aplikasi bisa dijaga bersamaan. Karena itu
-- perhitungan total jam kerja MENGGABUNGKAN interval yang bertumpuk alih-alih
-- menjumlahkannya (lihat internal/analytics/schedule.go).
--
-- Zona waktu disimpan per baris, dengan default Asia/Jakarta. Menyimpannya
-- membuat jadwal tetap terbaca benar seandainya suatu hari ada admin di zona
-- lain; tidak menyimpannya membuat seluruh riwayat salah pada hari itu.
--
-- Aman dijalankan ulang.
-- =============================================================================

create table if not exists public.work_schedules (
  id              uuid primary key default gen_random_uuid(),
  workspace_id    uuid not null references public.workspaces (id) on delete cascade,
  -- Siapa yang bertugas. Biasanya Freelance, tetapi PIC yang ikut memegang
  -- inbox juga punya jadwal, jadi kolomnya adalah user, bukan freelancer.
  user_id         uuid not null references public.users (id) on delete cascade,
  -- PIC penanggung jawab pada shift ini. Disimpan eksplisit, bukan diambil dari
  -- freelancer_pic_assignments saat dibaca, supaya laporan bulan lalu tetap
  -- menunjukkan PIC yang benar setelah seseorang berpindah atasan.
  pic_user_id     uuid references public.users (id) on delete set null,
  application_id  uuid references public.applications (id) on delete cascade,
  account_id      uuid references public.whatsapp_accounts (id) on delete cascade,
  work_date       date not null,
  starts_at       time not null,
  ends_at         time not null,
  timezone        text not null default 'Asia/Jakarta',
  -- False berarti libur: barisnya tetap ada supaya "hari ini kosong karena
  -- libur" bisa dibedakan dari "hari ini belum dijadwalkan".
  is_active       boolean not null default true,
  note            text,
  created_by      uuid references public.users (id) on delete set null,
  created_at      timestamptz not null default now(),
  updated_at      timestamptz not null default now(),
  constraint work_schedules_range_check check (ends_at > starts_at)
);

-- Keunikan atas seluruh dimensi penugasan. COALESCE dipakai karena
-- application_id dan account_id boleh kosong (jadwal umum), dan NULL di indeks
-- unik biasa tidak pernah dianggap bertabrakan — sehingga tanpa ini satu shift
-- yang sama bisa dimasukkan berkali-kali.
create unique index if not exists uq_work_schedules_slot
  on public.work_schedules (
    user_id,
    work_date,
    coalesce(application_id, '00000000-0000-0000-0000-000000000000'::uuid),
    coalesce(account_id,     '00000000-0000-0000-0000-000000000000'::uuid),
    starts_at
  );

create index if not exists idx_work_schedules_lookup
  on public.work_schedules (workspace_id, work_date, user_id) where is_active;
create index if not exists idx_work_schedules_app
  on public.work_schedules (application_id, work_date) where is_active;
create index if not exists idx_work_schedules_account
  on public.work_schedules (account_id, work_date) where is_active;
create index if not exists idx_work_schedules_pic
  on public.work_schedules (pic_user_id, work_date) where is_active;

drop trigger if exists trg_work_schedules_updated_at on public.work_schedules;
create trigger trg_work_schedules_updated_at
  before update on public.work_schedules
  for each row execute function public.touch_updated_at();

comment on table public.work_schedules is
  'Blok tugas seorang admin pada satu tanggal. Dipakai sebagai pembagi metrik per jam dan sebagai jendela business hours untuk SLA.';

-- -----------------------------------------------------------------------------
-- RLS
--
-- Membaca: sesuai lingkup — Leader semua, PIC jadwal timnya dan aplikasinya,
-- Freelance jadwalnya sendiri.
-- Menulis: Leader dan PIC. Seorang Freelance tidak menyusun jadwalnya sendiri.
-- -----------------------------------------------------------------------------
alter table public.work_schedules enable row level security;

drop policy if exists work_schedules_select on public.work_schedules;
create policy work_schedules_select on public.work_schedules
  for select to authenticated
  using (
    workspace_id = public.current_workspace_id()
    and (
      user_id in (select public.visible_admin_ids())
      or (application_id is not null
          and application_id in (select public.visible_application_ids()))
    )
  );

drop policy if exists work_schedules_write on public.work_schedules;
create policy work_schedules_write on public.work_schedules
  for all to authenticated
  using (
    workspace_id = public.current_workspace_id()
    and coalesce(public.current_operational_role() in ('leader', 'pic'), false)
    and (
      application_id is null
      or application_id in (select public.visible_application_ids())
    )
  )
  with check (
    workspace_id = public.current_workspace_id()
    and coalesce(public.current_operational_role() in ('leader', 'pic'), false)
    and (
      application_id is null
      or application_id in (select public.visible_application_ids())
    )
  );

-- -----------------------------------------------------------------------------
-- Realtime
-- -----------------------------------------------------------------------------
alter table public.work_schedules replica identity full;

do $$
begin
  if exists (select 1 from pg_publication where pubname = 'supabase_realtime') then
    begin
      alter publication supabase_realtime add table public.work_schedules;
    exception when duplicate_object then null; end;
  end if;
end $$;
