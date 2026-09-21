-- =============================================================================
-- 0051 — jam kerja sebagai pola mingguan
--
-- work_schedules sejauh ini hanya bisa menyatakan satu shift pada satu tanggal.
-- Untuk tim yang jam kerjanya tetap, itu berarti mengisi baris baru setiap hari
-- selamanya — dan kalau tidak diisi, tidak ada jam kerja sama sekali, sehingga
-- SLA jatuh kembali ke jam dinding dan pesan jam 2 pagi dinilai seolah ada yang
-- jaga.
--
-- Yang ditambahkan adalah cara kedua untuk menyatakan hal yang sama: baris
-- dengan weekday, bukan work_date. "Senin 08:00-17:00" berlaku setiap Senin
-- sampai diubah.
--
-- Tabel baru sengaja TIDAK dibuat. Keduanya menjawab satu pertanyaan — kapan
-- orang ini bekerja — dan memisahkannya ke dua tabel berarti dua tempat yang
-- harus dibaca, dua kunci asing, dan dua peluang untuk berbeda. Satu baris
-- punya work_date ATAU weekday, tidak pernah keduanya.
--
-- Baris bertanggal menang atas pola. Itulah yang membuat "libur nasional" dan
-- "tukar shift" bisa ditulis tanpa membongkar polanya: satu baris bertanggal
-- untuk hari itu, dan pola minggu itu diabaikan khusus untuk orang dan tanggal
-- tersebut.
--
-- Aman dijalankan ulang.
-- =============================================================================

alter table public.work_schedules
  alter column work_date drop not null;

alter table public.work_schedules
  add column if not exists weekday smallint;

-- 0 = Minggu, mengikuti extract(dow) Postgres dan urutan kalender Indonesia.
do $$ begin
  alter table public.work_schedules
    add constraint work_schedules_weekday_check
    check (weekday is null or weekday between 0 and 6);
exception when duplicate_object then null; end $$;

-- Satu baris menyatakan satu tanggal, atau satu hari dalam pekan. Tidak pernah
-- keduanya, dan tidak pernah bukan-duanya: baris tanpa keduanya adalah shift
-- yang tidak bisa ditempatkan di waktu mana pun.
do $$ begin
  alter table public.work_schedules
    add constraint work_schedules_when_check
    check ((work_date is not null) <> (weekday is not null));
exception when duplicate_object then null; end $$;

-- Indeks unik lama memakai work_date, yang kini boleh kosong. Dibatasi ke baris
-- bertanggal supaya artinya tetap sama.
drop index if exists public.uq_work_schedules_slot;

create unique index if not exists uq_work_schedules_slot
  on public.work_schedules (
    user_id,
    work_date,
    coalesce(application_id, '00000000-0000-0000-0000-000000000000'::uuid),
    coalesce(account_id,     '00000000-0000-0000-0000-000000000000'::uuid),
    starts_at
  ) where work_date is not null;

-- Pola mingguan punya kuncinya sendiri. starts_at ikut di dalamnya supaya shift
-- terbelah (08:00-12:00 dan 13:00-17:00 pada hari yang sama) tetap mungkin.
create unique index if not exists uq_work_schedules_weekly
  on public.work_schedules (
    user_id,
    weekday,
    coalesce(application_id, '00000000-0000-0000-0000-000000000000'::uuid),
    coalesce(account_id,     '00000000-0000-0000-0000-000000000000'::uuid),
    starts_at
  ) where weekday is not null;

create index if not exists idx_work_schedules_weekly
  on public.work_schedules (workspace_id, user_id, weekday)
  where weekday is not null and is_active;

comment on column public.work_schedules.weekday is
  'Hari dalam pekan (0 = Minggu) untuk pola mingguan. Null pada baris bertanggal.';
comment on column public.work_schedules.work_date is
  'Tanggal untuk shift sekali jalan. Null pada baris pola mingguan. Baris bertanggal menimpa pola untuk orang dan tanggal itu.';
