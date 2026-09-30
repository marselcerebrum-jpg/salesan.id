-- =============================================================================
-- salesan.id — Migration 0072: Cold / Warm / Hot di atas label WhatsApp
-- =============================================================================
-- Dasbor ingin tiga angka: berapa pelanggan Cold, Warm, Hot. Label WhatsApp
-- yang sebenarnya ada bukan tiga, melainkan seratus empat puluh tujuh varian
-- nama yang mengandung ketiga kata itu — "Cold", "cold", "COLD", "DB COLD",
-- "fu cold", "FU3 COLD", "Pindahan COLD" — ditambah label yang bukan suhu sama
-- sekali seperti "Premium", "IKLAN", "minggu", dan "Rabu".
--
-- Sebabnya bukan kelalaian. Label WhatsApp itu milik per nomor, jadi tiga
-- puluh tujuh nomor masing-masing punya "Cold" sendiri, dan penamaannya bebas
-- diketik siapa pun yang memegang HP.
--
-- Pengelompokannya karena itu dilakukan lewat pola nama, bukan lewat daftar
-- yang ditulis tangan. Daftar tangan berarti setiap label baru yang dibuat
-- seseorang di HP-nya besok pagi diam-diam hilang dari laporan sampai ada yang
-- menyadarinya dan menambahkannya. Pola bekerja sejak menit pertama.
--
-- Polanya bisa salah, dan untuk itulah tabel override ada: satu baris menambat
-- satu label ke kategori tertentu, mengalahkan polanya. Yang tidak cocok dengan
-- pola apa pun tidak dipaksa masuk — ia bukan Cold, Warm, atau Hot, dan
-- mengarangnya menjadi salah satunya akan membuat ketiga angka di dasbor
-- menjumlahkan sesuatu yang bukan pelanggan.
--
-- Aman dijalankan ulang.
-- =============================================================================

do $$ begin
  create type public.label_category as enum ('cold', 'warm', 'hot');
exception when duplicate_object then null; end $$;

-- -----------------------------------------------------------------------------
-- label_category_overrides — tambatan manual, mengalahkan pola
--
-- 'none' bukan anggota enum di atas karena ia bukan kategori: ia pernyataan
-- bahwa label ini sengaja tidak masuk ketiganya, misalnya sebuah label bernama
-- "Hotel" yang kebetulan mengandung kata "hot".
-- -----------------------------------------------------------------------------
create table if not exists public.label_category_overrides (
  workspace_id uuid not null references public.workspaces (id) on delete cascade,
  label_id     uuid not null references public.conversation_labels (id) on delete cascade,
  category     text not null check (category in ('cold', 'warm', 'hot', 'none')),
  set_by       uuid references public.users (id) on delete set null,
  updated_at   timestamptz not null default now(),
  primary key (workspace_id, label_id)
);

-- -----------------------------------------------------------------------------
-- label_category_of — pola, satu tempat
--
-- IMMUTABLE supaya bisa dipakai di indeks dan supaya perencana kueri boleh
-- memanggilnya sekali per nilai, bukan sekali per baris.
--
-- Urutannya sengaja: 'hot' diperiksa lebih dulu karena tidak ada nama yang
-- masuk akal mengandung dua suhu sekaligus, dan bila suatu saat ada, yang
-- paling panas adalah yang paling mahal untuk dilewatkan.
-- -----------------------------------------------------------------------------
create or replace function public.label_category_of(p_name text)
returns text
language sql
immutable
as $$
  select case
           when p_name is null then null
           when p_name ilike '%hot%'  then 'hot'
           when p_name ilike '%warm%' then 'warm'
           when p_name ilike '%cold%' then 'cold'
           else null
         end;
$$;

comment on function public.label_category_of(text) is
  'Kategori suhu sebuah label dari namanya, atau null bila bukan salah satunya. Dapat ditambat per label lewat label_category_overrides.';

-- -----------------------------------------------------------------------------
-- v_label_category — kategori final tiap label yang masih hidup
--
-- Override yang berbunyi 'none' menghasilkan baris yang hilang dari view ini,
-- bukan baris berkategori null: pemanggilnya menanyakan "label mana yang punya
-- kategori", dan sebuah null di tengah jawaban itu hanya menunggu untuk lupa
-- disaring.
-- -----------------------------------------------------------------------------
create or replace view public.v_label_category as
  select l.id as label_id,
         l.workspace_id,
         l.account_id,
         l.name,
         coalesce(o.category, public.label_category_of(l.name)) as category
    from public.conversation_labels l
    left join public.label_category_overrides o
           on o.label_id = l.id and o.workspace_id = l.workspace_id
   where l.deleted_at is null
     and coalesce(o.category, public.label_category_of(l.name)) in ('cold', 'warm', 'hot');

-- Dipakai setiap kali dasbor menghitung ketiga angkanya: dari penugasan yang
-- berlaku sekarang, ambil yang paling baru per kontak.
create index if not exists idx_label_assignments_recent
  on public.conversation_label_assignments (label_id, assigned_at desc);

alter table public.label_category_overrides enable row level security;

drop policy if exists label_category_overrides_rw on public.label_category_overrides;
create policy label_category_overrides_rw on public.label_category_overrides
  for all to authenticated
  using (workspace_id = public.current_workspace_id())
  with check (workspace_id = public.current_workspace_id());

comment on table public.label_category_overrides is
  'Menambat satu label WhatsApp ke Cold/Warm/Hot, mengalahkan pencocokan pola. category = ''none'' berarti label ini sengaja di luar ketiganya.';
