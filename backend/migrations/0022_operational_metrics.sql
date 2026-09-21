-- =============================================================================
-- salesan.id — Migration 0022: SLA, follow-up, mention grup, klasifikasi leads
-- =============================================================================
-- Empat hal yang semuanya diturunkan dari lini masa pesan, dan semuanya
-- disimpan sebagai baris tersendiri alih-alih dihitung ulang saat halaman
-- dibuka. Alasannya bukan kecepatan, melainkan audit: Leader harus bisa membuka
-- satu siklus SLA dan melihat pesan mana yang memulainya, pesan mana yang
-- menutupnya, dan jadwal siapa yang dipakai menghitung durasinya. Angka yang
-- dihitung ulang setiap kali dibaca tidak menyimpan jawaban atas satu pun dari
-- pertanyaan itu.
--
-- Semua tabel di sini bersifat TURUNAN dan dapat dibangun ulang dari pesan
-- (lihat internal/analytics dan internal/repository/metrics.go). Karena itu
-- setiap barisnya memakai kunci alami yang stabil, sehingga menghitung ulang
-- percakapan yang sama tidak pernah menghasilkan baris ganda — syarat mutlak
-- ketika event WhatsApp datang tidak berurutan atau terkirim dua kali.
--
-- Aman dijalankan ulang.
-- =============================================================================

do $$ begin
  create type public.sla_status as enum ('waiting', 'achieved', 'breached', 'excluded');
exception when duplicate_object then null; end $$;

-- -----------------------------------------------------------------------------
-- sla_cycles — satu siklus tunggu-balas pada chat pribadi
--
-- Siklus dimulai pada pesan masuk pertama yang belum terbalas, dan selesai pada
-- balasan manual admin pertama. Beberapa pesan pelanggan beruntun sebelum
-- dibalas adalah SATU siklus, memakai pesan pertama sebagai waktu mulai —
-- itulah sebabnya kunci alaminya adalah inbound_message_id dan bukan
-- (conversation_id, waktu).
--
-- Membaca pesan tidak menutup siklus. Hanya balasan manual yang menutupnya.
-- -----------------------------------------------------------------------------
create table if not exists public.sla_cycles (
  id                        uuid primary key default gen_random_uuid(),
  workspace_id              uuid not null references public.workspaces (id) on delete cascade,
  account_id                uuid not null references public.whatsapp_accounts (id) on delete cascade,
  application_id            uuid references public.applications (id) on delete set null,
  conversation_id           uuid not null references public.conversations (id) on delete cascade,
  contact_id                uuid references public.contacts (id) on delete set null,
  -- Pesan masuk pertama dalam waiting cycle. Sekaligus kunci idempotensinya.
  inbound_message_id        uuid not null references public.messages (id) on delete cascade,
  started_at                timestamptz not null,
  -- Berapa pesan pelanggan menumpuk sebelum dibalas. Bukan pengganti siklus,
  -- melainkan konteks: "menunggu 40 menit setelah 5 pesan" berbeda rasanya dari
  -- "menunggu 40 menit setelah 1 pesan".
  inbound_message_count     int not null default 1,
  first_response_message_id uuid references public.messages (id) on delete set null,
  responded_at              timestamptz,
  -- Durasi mentah: selisih jam dinding, apa adanya.
  raw_duration_seconds      int,
  -- Durasi kerja: hanya bagian yang jatuh di dalam jadwal kerja. Keduanya
  -- disimpan supaya audit tetap mungkin — sebuah angka yang sudah dipotong jam
  -- kerja tidak bisa dikembalikan menjadi angka aslinya.
  business_duration_seconds int,
  target_seconds            int not null,
  status                    public.sla_status not null default 'waiting',
  -- Alasan sebuah siklus dikecualikan; kosong untuk siklus normal.
  exclusion_reason          text,
  responder_admin_id        uuid references public.users (id) on delete set null,
  responder_source          public.sender_source,
  schedule_id               uuid references public.work_schedules (id) on delete set null,
  created_at                timestamptz not null default now(),
  updated_at                timestamptz not null default now(),
  constraint uq_sla_cycles_inbound unique (inbound_message_id)
);

create index if not exists idx_sla_cycles_scope
  on public.sla_cycles (account_id, started_at desc);
create index if not exists idx_sla_cycles_app
  on public.sla_cycles (application_id, started_at desc);
create index if not exists idx_sla_cycles_responder
  on public.sla_cycles (responder_admin_id, started_at desc)
  where responder_admin_id is not null;
create index if not exists idx_sla_cycles_open
  on public.sla_cycles (conversation_id)
  where status = 'waiting';
create index if not exists idx_sla_cycles_conversation
  on public.sla_cycles (conversation_id, started_at);

drop trigger if exists trg_sla_cycles_updated_at on public.sla_cycles;
create trigger trg_sla_cycles_updated_at
  before update on public.sla_cycles
  for each row execute function public.touch_updated_at();

-- -----------------------------------------------------------------------------
-- follow_up_events — admin memulai lagi percakapan yang sudah dingin
--
-- Kunci alaminya adalah (conversation_id, local_date): beberapa pesan admin
-- beruntun kepada kontak yang sama pada hari yang sama adalah SATU aktivitas
-- follow-up, sesuai aturan bisnisnya. Menyimpan trigger_message_id membuatnya
-- dapat diaudit — pesan itulah dasar klasifikasinya.
-- -----------------------------------------------------------------------------
create table if not exists public.follow_up_events (
  id                    uuid primary key default gen_random_uuid(),
  workspace_id          uuid not null references public.workspaces (id) on delete cascade,
  account_id            uuid not null references public.whatsapp_accounts (id) on delete cascade,
  application_id        uuid references public.applications (id) on delete set null,
  conversation_id       uuid not null references public.conversations (id) on delete cascade,
  contact_id            uuid references public.contacts (id) on delete set null,
  -- Tanggal WIB. Disimpan sebagai kolom, bukan diturunkan saat dibaca, karena
  -- ia adalah bagian dari kunci keunikan.
  local_date            date not null,
  -- Pesan admin yang membuka aktivitas ini.
  trigger_message_id    uuid not null references public.messages (id) on delete cascade,
  started_at            timestamptz not null,
  message_count         int not null default 1,
  -- Sejak kapan percakapan diam sebelum admin memulainya lagi. Ini bukti bahwa
  -- pesan admin itu memang tidak dipicu pesan pelanggan.
  gap_seconds           int,
  last_inbound_at       timestamptz,
  admin_id              uuid references public.users (id) on delete set null,
  admin_source          public.sender_source,
  -- Balasan pelanggan atas follow-up ini, bila ada.
  responded_at          timestamptz,
  response_message_id   uuid references public.messages (id) on delete set null,
  created_at            timestamptz not null default now(),
  updated_at            timestamptz not null default now(),
  constraint uq_follow_up_events_day unique (conversation_id, local_date)
);

create index if not exists idx_follow_up_scope
  on public.follow_up_events (account_id, local_date desc);
create index if not exists idx_follow_up_app
  on public.follow_up_events (application_id, local_date desc);
create index if not exists idx_follow_up_admin
  on public.follow_up_events (admin_id, local_date desc) where admin_id is not null;
create index if not exists idx_follow_up_contact
  on public.follow_up_events (contact_id, local_date desc) where contact_id is not null;

drop trigger if exists trg_follow_up_events_updated_at on public.follow_up_events;
create trigger trg_follow_up_events_updated_at
  before update on public.follow_up_events
  for each row execute function public.touch_updated_at();

-- -----------------------------------------------------------------------------
-- group_mentions — setiap penyebutan nomor kita di dalam grup
--
-- messages sudah menyimpan mentioned_jids/mentions_me/mention_seen_at, dan itu
-- tetap menjadi sumber kebenarannya. Tabel ini menambahkan satu hal yang tidak
-- ada di sana dan tidak bisa diturunkan tanpa menelusuri seluruh percakapan:
-- BALASAN mana yang menanggapi mention itu.
--
-- "Ditengok" (mention_seen_at) berbeda dari "ditanggapi" (responded_at).
-- Membuka grup bukan menjawab.
-- -----------------------------------------------------------------------------
create table if not exists public.group_mentions (
  id                   uuid primary key default gen_random_uuid(),
  workspace_id         uuid not null references public.workspaces (id) on delete cascade,
  account_id           uuid not null references public.whatsapp_accounts (id) on delete cascade,
  application_id       uuid references public.applications (id) on delete set null,
  conversation_id      uuid not null references public.conversations (id) on delete cascade,
  message_id           uuid not null references public.messages (id) on delete cascade,
  -- Anggota grup yang menyebut kita, beserta identitas mentahnya. Nama TIDAK
  -- dibekukan di sini: ia disusun saat dibaca dari contacts, mengikuti aturan
  -- yang sama dengan bubble chat.
  participant_jid      text,
  sender_phone         text,
  -- JID nomor kita yang disebut. Satu grup bisa memuat beberapa nomor kita, dan
  -- jawabannya berbeda untuk masing-masing.
  mentioned_account_jid text,
  mentioned_at         timestamptz not null,
  responded_at         timestamptz,
  response_message_id  uuid references public.messages (id) on delete set null,
  responder_admin_id   uuid references public.users (id) on delete set null,
  responder_source     public.sender_source,
  created_at           timestamptz not null default now(),
  updated_at           timestamptz not null default now(),
  -- Kunci idempotensinya. Satu pesan hanya bisa menyebut kita satu kali,
  -- berapa kali pun event-nya diputar ulang oleh reconnect atau history sync.
  constraint uq_group_mentions_message unique (message_id)
);

create index if not exists idx_group_mentions_scope
  on public.group_mentions (account_id, mentioned_at desc);
create index if not exists idx_group_mentions_app
  on public.group_mentions (application_id, mentioned_at desc);
create index if not exists idx_group_mentions_open
  on public.group_mentions (conversation_id, mentioned_at desc)
  where responded_at is null;

drop trigger if exists trg_group_mentions_updated_at on public.group_mentions;
create trigger trg_group_mentions_updated_at
  before update on public.group_mentions
  for each row execute function public.touch_updated_at();

-- -----------------------------------------------------------------------------
-- contact_first_seen — kapan sebuah kontak pertama kali terlihat, dan lewat apa
--
-- Dipisahkan dari lead_classifications karena ini adalah FAKTA (kapan, pesan
-- mana, batch impor mana), sementara klasifikasi adalah KESIMPULAN atas fakta
-- itu. Aturan klasifikasinya bisa berubah; faktanya tidak boleh ikut berubah.
-- -----------------------------------------------------------------------------
create table if not exists public.contact_first_seen (
  contact_id                 uuid primary key references public.contacts (id) on delete cascade,
  workspace_id               uuid not null references public.workspaces (id) on delete cascade,
  account_id                 uuid not null references public.whatsapp_accounts (id) on delete cascade,
  application_id             uuid references public.applications (id) on delete set null,
  conversation_id            uuid references public.conversations (id) on delete set null,
  -- Kapan baris kontaknya lahir di sistem kita.
  first_seen_at              timestamptz not null,
  -- Pesan masuk pertama dari kontak ini, dan kapan.
  first_inbound_at           timestamptz,
  first_message_id           uuid references public.messages (id) on delete set null,
  -- Sejak kapan sistem memantau akun penerimanya. Disalin, bukan dibaca lewat
  -- join, supaya bukti klasifikasinya tetap utuh seandainya akun dihapus.
  system_tracking_started_at timestamptz,
  imported_at                timestamptz,
  import_batch_id            uuid references public.import_batches (id) on delete set null,
  had_label_before           boolean not null default false,
  created_at                 timestamptz not null default now(),
  updated_at                 timestamptz not null default now()
);

create index if not exists idx_contact_first_seen_scope
  on public.contact_first_seen (account_id, first_inbound_at desc);

drop trigger if exists trg_contact_first_seen_updated_at on public.contact_first_seen;
create trigger trg_contact_first_seen_updated_at
  before update on public.contact_first_seen
  for each row execute function public.touch_updated_at();

-- -----------------------------------------------------------------------------
-- lead_classifications — kesimpulan atas fakta di atas
--
-- verified_new : benar-benar masuk setelah sistem memantau, tanpa riwayat dan
--                tanpa label sebelumnya.
-- historical   : berasal dari impor tujuh hari, atau sudah punya jejak sebelum
--                sistem aktif.
-- unknown      : buktinya tidak cukup. Ini bukan kegagalan — ini jawaban yang
--                jujur, dan Dashboard sengaja hanya menghitung verified_new.
-- -----------------------------------------------------------------------------
do $$ begin
  create type public.lead_status as enum ('verified_new', 'historical', 'unknown');
exception when duplicate_object then null; end $$;

create table if not exists public.lead_classifications (
  contact_id      uuid primary key references public.contacts (id) on delete cascade,
  workspace_id    uuid not null references public.workspaces (id) on delete cascade,
  account_id      uuid not null references public.whatsapp_accounts (id) on delete cascade,
  application_id  uuid references public.applications (id) on delete set null,
  lead_status     public.lead_status not null,
  status_reason   text not null,
  -- Tanggal WIB saat kontak ini dihitung sebagai leads; null untuk historical.
  qualified_date  date,
  classified_at   timestamptz not null default now(),
  updated_at      timestamptz not null default now()
);

create index if not exists idx_lead_classifications_scope
  on public.lead_classifications (account_id, lead_status, qualified_date);
create index if not exists idx_lead_classifications_app
  on public.lead_classifications (application_id, qualified_date)
  where lead_status = 'verified_new';

drop trigger if exists trg_lead_classifications_updated_at on public.lead_classifications;
create trigger trg_lead_classifications_updated_at
  before update on public.lead_classifications
  for each row execute function public.touch_updated_at();

-- =============================================================================
-- RLS
--
-- Semua tabel di sini dibaca lewat aplikasi: Leader semua, PIC aplikasinya,
-- Freelance aplikasi yang ditugaskan kepadanya. Tidak ada kebijakan tulis untuk
-- `authenticated` — baris-baris ini turunan, dan hanya backend (service role)
-- yang boleh membuatnya. Browser yang menulis ke sini akan membuat laporan yang
-- tidak lagi cocok dengan pesannya.
-- =============================================================================
alter table public.sla_cycles           enable row level security;
alter table public.follow_up_events     enable row level security;
alter table public.group_mentions       enable row level security;
alter table public.contact_first_seen   enable row level security;
alter table public.lead_classifications enable row level security;

do $$
declare
  t text;
begin
  foreach t in array array[
    'sla_cycles', 'follow_up_events', 'group_mentions',
    'contact_first_seen', 'lead_classifications'
  ] loop
    execute format('drop policy if exists %I on public.%I', t || '_select', t);
    -- application_id boleh null (akun belum ditautkan ke aplikasi). Barisnya
    -- hanya terlihat oleh yang berhak atas seluruh workspace, karena tidak ada
    -- aplikasi yang bisa dipakai membatasinya.
    execute format($f$
      create policy %I on public.%I
        for select to authenticated
        using (
          workspace_id = public.current_workspace_id()
          and (
            application_id in (select public.visible_application_ids())
            or (application_id is null
                and coalesce(public.current_operational_role() = 'leader', true))
          )
        )
    $f$, t || '_select', t);
  end loop;
end $$;

-- =============================================================================
-- Realtime
-- =============================================================================
alter table public.sla_cycles       replica identity full;
alter table public.follow_up_events replica identity full;
alter table public.group_mentions   replica identity full;

do $$
declare
  t text;
begin
  if exists (select 1 from pg_publication where pubname = 'supabase_realtime') then
    foreach t in array array['sla_cycles', 'follow_up_events', 'group_mentions'] loop
      begin
        execute format('alter publication supabase_realtime add table public.%I', t);
      exception when duplicate_object then null; end;
    end loop;
  end if;
end $$;
