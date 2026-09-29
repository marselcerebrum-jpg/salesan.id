-- =============================================================================
-- salesan.id — Migration 0071: riwayat keluar-masuk anggota grup
-- =============================================================================
-- Sampai sekarang jumlah anggota grup hanya ada dalam bentuk "sekarang berapa":
-- dihitung langsung dari conversation_members, dan saat sebuah grup di-fetch
-- ulang, anggota yang sudah keluar dihapus permanen. Tidak ada satu pun jejak
-- bahwa mereka pernah ada, jadi pertanyaan "grup ini tumbuh atau menyusut"
-- tidak bisa dijawab sama sekali.
--
-- Dua tabel, dan keduanya punya alasan yang berbeda.
--
-- group_member_events adalah faktanya: siapa masuk atau keluar, kapan. WhatsApp
-- sendiri yang memberi tahu lewat event GroupInfo, jadi tidak ada yang perlu
-- ditebak dan tidak ada 904 grup yang perlu ditanyai tiap hari.
--
-- group_member_daily adalah jangkarnya: berapa anggota grup ini pada akhir satu
-- hari. Tanpa itu, riwayat harian hanya bisa dihitung mundur dari jumlah hari
-- ini, sehingga satu event yang terlewat — dan event pasti ada yang terlewat,
-- setiap kali proses ini menyambung ulang — menggeser seluruh sejarahnya.
-- Dengan jangkar, hari yang terlewat salah sendirian, bukan menyeret yang lain.
--
-- Harinya hari Jakarta, bukan hari UTC. Badge "hari ini" harus berganti saat
-- tengah malam di tempat orang yang membacanya, bukan pukul tujuh pagi.
--
-- Aman dijalankan ulang.
-- =============================================================================

create table if not exists public.group_member_events (
  id               uuid primary key default gen_random_uuid(),
  workspace_id     uuid not null references public.workspaces (id) on delete cascade,
  chat_jid         text not null,
  participant_jid  text not null,
  direction        text not null check (direction in ('join', 'leave')),

  -- Waktu dari WhatsApp, bukan waktu kita menerimanya. Event yang datang
  -- terlambat karena proses ini sempat mati tetap jatuh pada harinya sendiri.
  occurred_at      timestamptz not null,

  -- Siapa yang melakukannya, kalau WhatsApp menyebutkannya. Kosong untuk orang
  -- yang masuk lewat tautan undangan.
  actor_jid        text,

  -- Nomor kita yang mendengar kabarnya. Hanya untuk penelusuran; ia sengaja
  -- tidak ikut dalam kunci unik di bawah.
  heard_by         uuid references public.whatsapp_accounts (id) on delete set null,

  created_at       timestamptz not null default now()
);

-- Satu orang masuk ke satu grup adalah satu kejadian, meski lima nomor kita ada
-- di dalam grup itu dan kelimanya menerima kabar yang sama. Tanpa kunci ini,
-- grup yang dijaga lima nomor akan melaporkan pertumbuhan lima kali lipat.
--
-- Detiknya dipotong karena itulah ketelitian yang sebenarnya dimiliki: dua nomor
-- yang menerima notifikasi yang sama bisa mencatat milidetik yang berbeda, dan
-- orang yang masuk lalu keluar dalam detik yang sama bukan hal yang terjadi.
--
-- Dipaksa ke UTC dulu sebelum dipotong. date_trunc pada timestamptz hasilnya
-- bergantung pada zona waktu sesi yang sedang berjalan, sehingga Postgres
-- menolaknya di dalam indeks: nilainya bisa berubah tanpa barisnya berubah.
-- Dikonversi ke waktu polos lebih dulu, ekspresinya menjadi tetap. Zonanya UTC
-- dan bukan Jakarta karena di sini yang dibutuhkan hanya satu titik waktu yang
-- konsisten, bukan tanggal yang dibaca orang.
create unique index if not exists uq_group_member_events
  on public.group_member_events
     (workspace_id, chat_jid, participant_jid, direction,
      date_trunc('second', occurred_at at time zone 'UTC'));

-- Pertanyaan yang ditanyakan daftar grup: apa saja yang terjadi sejak tengah
-- malam. Dijawab per grup, jadi grup adalah kolom pertamanya.
create index if not exists idx_group_member_events_recent
  on public.group_member_events (workspace_id, chat_jid, occurred_at desc);

create table if not exists public.group_member_daily (
  workspace_id   uuid not null references public.workspaces (id) on delete cascade,
  chat_jid       text not null,
  day            date not null,

  -- Jumlah anggota pada akhir hari itu.
  member_count   int  not null,
  joined         int  not null default 0,
  left_count     int  not null default 0,

  updated_at     timestamptz not null default now(),
  primary key (workspace_id, chat_jid, day)
);

-- Grafik satu grup dibaca mundur dari hari ini, jadi urutannya ikut disimpan
-- terbalik supaya tidak perlu diurutkan ulang tiap kali dibuka.
create index if not exists idx_group_member_daily_day
  on public.group_member_daily (workspace_id, chat_jid, day desc);

alter table public.group_member_events enable row level security;
alter table public.group_member_daily  enable row level security;

drop policy if exists group_member_events_ro on public.group_member_events;
create policy group_member_events_ro on public.group_member_events
  for select to authenticated
  using (workspace_id = public.current_workspace_id());

drop policy if exists group_member_daily_ro on public.group_member_daily;
create policy group_member_daily_ro on public.group_member_daily
  for select to authenticated
  using (workspace_id = public.current_workspace_id());

comment on table public.group_member_events is
  'Satu baris per orang yang masuk atau keluar satu grup. Sumbernya event GroupInfo dari WhatsApp. Kunci uniknya membuang duplikat dari beberapa nomor kita yang ada di grup yang sama.';
comment on table public.group_member_daily is
  'Jumlah anggota tiap grup pada akhir tiap hari Jakarta, sebagai jangkar supaya event yang terlewat tidak menggeser seluruh riwayat.';
comment on column public.group_member_daily.left_count is
  'Jumlah yang keluar hari itu. Dinamai left_count karena "left" adalah kata kunci SQL.';
