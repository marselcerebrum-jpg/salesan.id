-- =============================================================================
-- salesan.id — Migration 0021: atribusi pesan + batch impor
-- =============================================================================
-- Sebuah pesan keluar bisa berasal dari lima tempat yang sangat berbeda, dan
-- selama ini semuanya terlihat sama di database: from_me = true. Padahal:
--
--   web_admin        — dikirim seorang admin dari web app. Ini yang dihitung
--                      sebagai balasan manual, menutup SLA, dan menjadi
--                      follow-up.
--   whatsapp_device  — dikirim langsung dari HP. Tetap balasan manusia, tetapi
--                      TIDAK bisa diklaim milik admin tertentu: WhatsApp tidak
--                      memberi tahu siapa yang memegang HP-nya. admin_id-nya
--                      null, dan menebaknya dari jadwal atau dari PIC akan
--                      menghasilkan laporan performa yang meyakinkan sekaligus
--                      salah.
--   bot / system     — otomatis. Bukan aktivitas admin, tidak menutup SLA.
--   broadcast        — pesan kampanye. Tidak boleh mencemari metrik chat normal
--                      sama sekali; balasan pelanggan atasnya tetap inbound
--                      biasa.
--   story            — publikasi status, tidak masuk chat mana pun.
--
-- sent_by yang sudah ada tetap menjadi sumber admin_id. Kolom baru menjawab
-- pertanyaan yang berbeda: bukan "siapa", melainkan "lewat jalan mana".
--
-- Aman dijalankan ulang.
-- =============================================================================

do $$ begin
  create type public.sender_source as enum (
    'web_admin', 'whatsapp_device', 'bot', 'system', 'broadcast', 'story'
  );
exception when duplicate_object then null; end $$;

-- -----------------------------------------------------------------------------
-- import_batches — satu baris per proses sinkronisasi riwayat
--
-- Ini yang memberi bukti bahwa sebuah pesan datang dari impor tujuh hari, bukan
-- dari percakapan yang benar-benar berlangsung setelah sistem aktif. Tanpa
-- bukti itu, klasifikasi leads hanya bisa menebak.
-- -----------------------------------------------------------------------------
create table if not exists public.import_batches (
  id             uuid primary key default gen_random_uuid(),
  workspace_id   uuid not null references public.workspaces (id) on delete cascade,
  account_id     uuid not null references public.whatsapp_accounts (id) on delete cascade,
  source         text not null default 'history_sync',
  window_days    int  not null default 7,
  window_start   timestamptz,
  started_at     timestamptz not null default now(),
  finished_at    timestamptz,
  conversations  int not null default 0,
  messages       int not null default 0,
  skipped        int not null default 0,
  status         text not null default 'running',
  error_message  text,
  constraint import_batches_status_check
    check (status in ('running', 'completed', 'failed'))
);

create index if not exists idx_import_batches_account
  on public.import_batches (account_id, started_at desc);

-- -----------------------------------------------------------------------------
-- Kolom atribusi pada pesan
-- -----------------------------------------------------------------------------
alter table public.messages
  add column if not exists sender_source   public.sender_source,
  -- Snapshot peran saat pesan dikirim. Riwayat tidak boleh ikut berubah ketika
  -- seorang Freelance dipromosikan menjadi PIC bulan berikutnya.
  add column if not exists sender_role     public.operational_role,
  add column if not exists sender_pic_id   uuid references public.users (id) on delete set null,
  add column if not exists import_batch_id uuid references public.import_batches (id) on delete set null;

comment on column public.messages.sender_source is
  'Jalur asal pesan keluar. Null untuk pesan masuk. broadcast/story/bot/system tidak pernah dihitung sebagai balasan admin manual.';
comment on column public.messages.sender_role is
  'Snapshot peran operasional pengirim saat pesan dikirim, supaya laporan lama tidak berubah ketika perannya berganti.';

-- -----------------------------------------------------------------------------
-- Default atribusi, ditegakkan di database
--
-- Diletakkan sebagai trigger, bukan di kode Go, karena pesan masuk lewat empat
-- jalur berbeda (kirim manual, media, poll, forward, history sync) dan satu
-- jalur yang lupa mengisinya sudah cukup untuk membuat seluruh metrik miring.
-- Trigger tidak pernah menimpa nilai yang sudah diisi pemanggil.
-- -----------------------------------------------------------------------------
create or replace function public.default_message_attribution()
returns trigger
language plpgsql
as $$
begin
  if new.from_me and new.sender_source is null then
    if new.sent_by is not null then
      new.sender_source := 'web_admin';
    else
      -- Dikirim dari HP. Identitas pengirimnya tidak dapat diverifikasi, jadi
      -- ia tetap tidak teratribusi; menebaknya dilarang secara eksplisit.
      new.sender_source := 'whatsapp_device';
    end if;
  end if;

  if new.from_me and new.sender_role is null and new.sent_by is not null then
    select ra.role into new.sender_role
      from public.role_assignments ra
     where ra.user_id = new.sent_by and ra.is_active
     limit 1;
  end if;

  if new.from_me and new.sender_pic_id is null and new.sent_by is not null then
    select fp.pic_user_id into new.sender_pic_id
      from public.freelancer_pic_assignments fp
     where fp.freelancer_user_id = new.sent_by;
  end if;

  return new;
end;
$$;

drop trigger if exists trg_messages_attribution on public.messages;
create trigger trg_messages_attribution
  before insert on public.messages
  for each row execute function public.default_message_attribution();

-- -----------------------------------------------------------------------------
-- Backfill data lama
--
-- Pesan yang sudah tersimpan mengikuti aturan yang sama persis: ada sent_by
-- berarti dikirim dari web, tidak ada berarti dari HP dan tetap tanpa admin.
-- -----------------------------------------------------------------------------
update public.messages
   set sender_source = case when sent_by is not null then 'web_admin'::public.sender_source
                                                     else 'whatsapp_device'::public.sender_source end
 where from_me and sender_source is null;

-- -----------------------------------------------------------------------------
-- Kapan sistem mulai memantau akun ini
--
-- Batas antara "leads yang benar-benar baru" dan "riwayat hasil impor". Untuk
-- akun yang sudah ada saat migrasi ini dijalankan, batasnya adalah sekarang —
-- sehingga seluruh kontak yang sudah terlanjur tersimpan jatuh ke sisi
-- historical. Itu jawaban yang benar: sistem memang belum memantau mereka.
-- -----------------------------------------------------------------------------
alter table public.whatsapp_accounts
  add column if not exists tracking_started_at timestamptz;

update public.whatsapp_accounts
   set tracking_started_at = coalesce(tracking_started_at, now())
 where tracking_started_at is null;

alter table public.whatsapp_accounts
  alter column tracking_started_at set default now();

-- -----------------------------------------------------------------------------
-- Indeks untuk agregasi harian/bulanan
--
-- Setiap kartu Dashboard adalah hitungan atas irisan (akun, arah, jalur asal,
-- rentang waktu). Indeks-indeks ini yang membuatnya tidak menjadi seq scan
-- atas seluruh tabel pesan.
-- -----------------------------------------------------------------------------
create index if not exists idx_messages_analytics_account
  on public.messages (account_id, timestamp desc)
  where hidden_at is null;

create index if not exists idx_messages_analytics_outbound
  on public.messages (account_id, sender_source, timestamp desc)
  where from_me and hidden_at is null;

create index if not exists idx_messages_analytics_sent_by
  on public.messages (sent_by, timestamp desc)
  where sent_by is not null;

create index if not exists idx_messages_import_batch
  on public.messages (import_batch_id)
  where import_batch_id is not null;

-- -----------------------------------------------------------------------------
-- RLS
-- -----------------------------------------------------------------------------
alter table public.import_batches enable row level security;

drop policy if exists import_batches_select on public.import_batches;
create policy import_batches_select on public.import_batches
  for select to authenticated
  using (workspace_id = public.current_workspace_id());
