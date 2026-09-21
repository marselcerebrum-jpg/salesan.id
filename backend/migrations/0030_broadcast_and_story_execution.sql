-- =============================================================================
-- salesan.id — Migration 0030: eksekusi Broadcast dan WA Story
-- =============================================================================
-- 0024 membuat kerangka campaign: satu baris per Story/Broadcast, riwayat
-- penjadwalan, daftar penerima, jejak audit. Yang belum ada adalah bagian yang
-- benar-benar mengirim: perangkat pengirim, pembagian target, percobaan per
-- target, antrean yang selamat dari restart, dan publikasi Story per perangkat.
--
-- Berkas ini menambahkannya. Seluruhnya aditif — tidak ada kolom yang dihapus
-- atau diganti nama, tidak ada data yang dibuang, dan setiap tabel serta kolom
-- memakai IF NOT EXISTS sehingga aman dijalankan ulang.
--
-- Tiga keputusan yang menentukan bentuk skema ini:
--
--   1. Idempotensi ditegakkan oleh basis data, bukan oleh kode.
--      broadcast_target_attempts punya indeks unik parsial yang hanya
--      mengizinkan SATU percobaan berstatus 'sent' untuk setiap
--      (campaign, perangkat, target). Worker boleh crash di titik mana pun;
--      pengiriman kedua ke orang yang sama akan ditolak oleh constraint, bukan
--      oleh harapan bahwa kode selalu benar.
--
--   2. Antrean hidup di tabel, bukan di memori.
--      Setiap baris yang bisa dikerjakan punya next_attempt_at dan sepasang
--      kolom sewa (lease). Worker mengklaim baris dengan UPDATE ... FOR UPDATE
--      SKIP LOCKED dan memegangnya selama sewa berlaku. Backend yang mati di
--      tengah campaign meninggalkan sewa yang kedaluwarsa, dan worker
--      berikutnya mengambilnya kembali — bukan meninggalkan pekerjaan yang
--      hilang bersama prosesnya.
--
--   3. Media disimpan sebagai URL, bukan sebagai berkas.
--      Kolomnya menyimpan alamat, tipe, ukuran, dan hash. Unduhan terjadi
--      sementara di backend saat pengiriman lalu dihapus. Tidak ada base64 dan
--      tidak ada biner di basis data.
--
-- Tidak ada satu baris contoh pun yang dimasukkan.
-- =============================================================================

-- -----------------------------------------------------------------------------
-- content_campaigns — kolom yang dibutuhkan eksekutor
-- -----------------------------------------------------------------------------
alter table public.content_campaigns
  -- Profil jeda antar pengiriman. Ini pengaturan antrean dan ritme, bukan
  -- jaminan apa pun terhadap pemblokiran akun.
  add column if not exists delay_profile      text not null default 'normal',
  -- Bagaimana pesan disusun: biasa, spintax, variabel, keduanya, atau draft GPT.
  add column if not exists compose_mode       text not null default 'plain',
  -- Template asli, sebelum spintax dipilih dan variabel diisi. Disimpan supaya
  -- laporan bisa menunjukkan apa yang ditulis orangnya, bukan hanya salah satu
  -- hasil acaknya.
  add column if not exists message_template   text,
  add column if not exists media_url          text,
  add column if not exists media_kind         text,
  add column if not exists media_mime         text,
  add column if not exists media_size_bytes   bigint,
  add column if not exists media_sha256       text,
  add column if not exists caption            text,
  add column if not exists target_source      text,
  add column if not exists started_at         timestamptz,
  add column if not exists finished_at        timestamptz,
  add column if not exists cancelled_at       timestamptz,
  -- Pembatalan bersifat kooperatif: bendera dinaikkan, worker membacanya di
  -- antara dua target. Menghentikan pengiriman yang sedang berjalan di tengah
  -- jalan akan meninggalkan target yang statusnya tidak diketahui.
  add column if not exists cancel_requested   boolean not null default false,
  add column if not exists max_attempts       int not null default 3,
  add column if not exists retry_gap_seconds  int not null default 180,
  add column if not exists lease_owner        uuid,
  add column if not exists lease_expires_at   timestamptz,
  -- Arsip, bukan hapus. Riwayat pengiriman tidak pernah dibuang.
  add column if not exists archived_at        timestamptz;

alter table public.content_campaigns
  drop constraint if exists content_campaigns_delay_profile_check;
alter table public.content_campaigns
  add constraint content_campaigns_delay_profile_check
  check (delay_profile in ('super_cepat', 'cepat', 'normal', 'aman', 'santai'));

alter table public.content_campaigns
  drop constraint if exists content_campaigns_compose_mode_check;
alter table public.content_campaigns
  add constraint content_campaigns_compose_mode_check
  check (compose_mode in ('plain', 'spintax', 'variables', 'spintax_variables', 'gpt'));

alter table public.content_campaigns
  drop constraint if exists content_campaigns_media_kind_check;
alter table public.content_campaigns
  add constraint content_campaigns_media_kind_check
  check (media_kind is null or media_kind in ('image', 'video', 'document', 'audio'));

alter table public.content_campaigns
  drop constraint if exists content_campaigns_target_source_check;
alter table public.content_campaigns
  add constraint content_campaigns_target_source_check
  check (target_source is null or target_source in
         ('manual', 'csv', 'contacts', 'group_members', 'groups'));

-- Antrean scheduler: campaign yang jatuh tempo dan belum dipegang siapa pun.
create index if not exists idx_campaigns_due
  on public.content_campaigns (scheduled_at)
  where status in ('scheduled', 'running');

comment on column public.content_campaigns.media_url is
  'Alamat media. Berkasnya tidak pernah disimpan di basis data; backend mengunduhnya sementara saat mengirim lalu menghapusnya.';

-- -----------------------------------------------------------------------------
-- broadcast_sender_devices — perangkat pengirim sebuah campaign
--
-- Satu campaign boleh memakai beberapa nomor. Baris ini yang membuat "hasil per
-- perangkat" di laporan bisa dijawab, dan yang menahan pembagian target supaya
-- retry tidak berpindah perangkat diam-diam.
-- -----------------------------------------------------------------------------
create table if not exists public.broadcast_sender_devices (
  id             uuid primary key default gen_random_uuid(),
  workspace_id   uuid not null references public.workspaces (id) on delete cascade,
  campaign_id    uuid not null references public.content_campaigns (id) on delete cascade,
  account_id     uuid not null references public.whatsapp_accounts (id) on delete cascade,
  position       int not null default 0,
  status         text not null default 'pending',
  assigned_count int not null default 0,
  sent_count     int not null default 0,
  failed_count   int not null default 0,
  started_at     timestamptz,
  finished_at    timestamptz,
  failure_reason text,
  created_at     timestamptz not null default now(),
  updated_at     timestamptz not null default now(),
  constraint uq_broadcast_sender_devices unique (campaign_id, account_id),
  constraint broadcast_sender_devices_status_check
    check (status in ('pending', 'running', 'done', 'failed', 'cancelled'))
);

create index if not exists idx_broadcast_sender_devices_campaign
  on public.broadcast_sender_devices (campaign_id, position);

drop trigger if exists trg_broadcast_sender_devices_updated_at on public.broadcast_sender_devices;
create trigger trg_broadcast_sender_devices_updated_at
  before update on public.broadcast_sender_devices
  for each row execute function public.touch_updated_at();

-- -----------------------------------------------------------------------------
-- campaign_targets — kolom eksekusi
--
-- Tabelnya sudah ada sejak 0024 dengan kunci unik (campaign_id, chat_jid); itu
-- yang menjamin satu penerima hanya muncul sekali dalam satu campaign, apa pun
-- berapa perangkat yang dipakai. Yang ditambahkan di sini adalah perangkat yang
-- ditugaskan, status antrean, dan hasil render pesannya.
-- -----------------------------------------------------------------------------
alter table public.campaign_targets
  add column if not exists account_id       uuid references public.whatsapp_accounts (id) on delete set null,
  add column if not exists target_type      text not null default 'contact',
  add column if not exists phone_number     text,
  add column if not exists display_name     text,
  -- Nilai variabel untuk penerima ini, dibekukan saat target dibuat.
  add column if not exists variables        jsonb not null default '{}'::jsonb,
  -- Pesan final yang benar-benar dikirim ke orang ini, setelah spintax dipilih
  -- dan variabel diisi. Tanpa ini audit hanya bisa menunjukkan templatenya.
  add column if not exists rendered_body    text,
  add column if not exists next_attempt_at  timestamptz,
  add column if not exists claimed_at       timestamptz,
  add column if not exists lease_expires_at timestamptz,
  add column if not exists wa_message_id    text,
  add column if not exists delivered_at     timestamptz,
  add column if not exists read_at          timestamptz,
  add column if not exists cancelled_at     timestamptz,
  add column if not exists invalid_reason   text,
  add column if not exists error_code       text;

-- Status lama ('pending','sent','failed','skipped') tetap sah; yang ditambahkan
-- adalah keadaan antrean yang sebelumnya tidak bisa dinyatakan.
alter table public.campaign_targets
  drop constraint if exists campaign_targets_status_check;
alter table public.campaign_targets
  add constraint campaign_targets_status_check
  check (status in ('pending', 'processing', 'sent', 'delivered', 'read',
                    'failed', 'cancelled', 'skipped', 'retry_wait', 'invalid'));

alter table public.campaign_targets
  drop constraint if exists campaign_targets_target_type_check;
alter table public.campaign_targets
  add constraint campaign_targets_target_type_check
  check (target_type in ('contact', 'manual', 'csv', 'group_member', 'group'));

-- Indeks klaim: baris yang siap dikerjakan, diurut menurut jatuh tempo.
create index if not exists idx_campaign_targets_queue
  on public.campaign_targets (campaign_id, status, next_attempt_at);
create index if not exists idx_campaign_targets_device
  on public.campaign_targets (account_id, status) where account_id is not null;
create index if not exists idx_campaign_targets_wa_message
  on public.campaign_targets (wa_message_id) where wa_message_id is not null;

-- -----------------------------------------------------------------------------
-- broadcast_target_attempts — satu baris per percobaan pengiriman
--
-- Inilah kunci idempotensi yang diminta: campaign + perangkat + target. Indeks
-- unik parsial di bawah hanya mengizinkan satu percobaan BERHASIL untuk
-- kombinasi itu, sehingga retry yang salah, worker ganda, atau backend yang
-- restart di tengah pengiriman tidak dapat menghasilkan pesan kedua ke orang
-- yang sama. Percobaan gagal boleh berulang sampai batas percobaan.
-- -----------------------------------------------------------------------------
create table if not exists public.broadcast_target_attempts (
  id             uuid primary key default gen_random_uuid(),
  workspace_id   uuid not null references public.workspaces (id) on delete cascade,
  campaign_id    uuid not null references public.content_campaigns (id) on delete cascade,
  target_id      uuid not null references public.campaign_targets (id) on delete cascade,
  account_id     uuid not null references public.whatsapp_accounts (id) on delete cascade,
  attempt        int not null,
  status         text not null,
  wa_message_id  text,
  message_id     uuid references public.messages (id) on delete set null,
  error_code     text,
  failure_reason text,
  started_at     timestamptz not null default now(),
  finished_at    timestamptz,
  constraint broadcast_target_attempts_status_check
    check (status in ('sending', 'sent', 'failed')),
  constraint uq_broadcast_attempt unique (campaign_id, account_id, target_id, attempt)
);

-- Satu keberhasilan saja per (campaign, perangkat, target). Ini yang benar-benar
-- mencegah pesan ganda.
create unique index if not exists uq_broadcast_attempt_success
  on public.broadcast_target_attempts (campaign_id, account_id, target_id)
  where status = 'sent';

create index if not exists idx_broadcast_attempts_target
  on public.broadcast_target_attempts (target_id, attempt);

comment on table public.broadcast_target_attempts is
  'Append-only. Indeks unik parsial uq_broadcast_attempt_success adalah jaminan tidak-terkirim-dua-kali; jangan dilonggarkan.';

-- -----------------------------------------------------------------------------
-- campaign_labels — label internal campaign
--
-- Terpisah sepenuhnya dari label kontak WhatsApp (conversation_labels). Keduanya
-- kebetulan disebut "label", tetapi yang satu menandai orang di WhatsApp dan
-- yang lain menandai pekerjaan kita sendiri; menggabungkannya akan mengirim
-- nama internal ke telepon pelanggan.
-- -----------------------------------------------------------------------------
create table if not exists public.campaign_labels (
  id           uuid primary key default gen_random_uuid(),
  workspace_id uuid not null references public.workspaces (id) on delete cascade,
  name         text not null,
  color        text not null default '#0F766E',
  archived_at  timestamptz,
  created_by   uuid references public.users (id) on delete set null,
  created_at   timestamptz not null default now(),
  updated_at   timestamptz not null default now()
);

create unique index if not exists uq_campaign_labels_name
  on public.campaign_labels (workspace_id, lower(name))
  where archived_at is null;

drop trigger if exists trg_campaign_labels_updated_at on public.campaign_labels;
create trigger trg_campaign_labels_updated_at
  before update on public.campaign_labels
  for each row execute function public.touch_updated_at();

create table if not exists public.campaign_label_assignments (
  campaign_id uuid not null references public.content_campaigns (id) on delete cascade,
  label_id    uuid not null references public.campaign_labels (id) on delete cascade,
  assigned_by uuid references public.users (id) on delete set null,
  assigned_at timestamptz not null default now(),
  primary key (campaign_id, label_id)
);

create index if not exists idx_campaign_label_assignments_label
  on public.campaign_label_assignments (label_id);

-- -----------------------------------------------------------------------------
-- custom_variables — variabel buatan pengguna untuk composer
--
-- {{nama}}, {{nomor}}, {{aplikasi}}, {{nama_grup}} berasal dari data dan tidak
-- perlu didaftarkan. Yang di sini adalah yang tidak bisa diketahui sistem —
-- {{kode_promo}} dan sejenisnya.
-- -----------------------------------------------------------------------------
create table if not exists public.custom_variables (
  id            uuid primary key default gen_random_uuid(),
  workspace_id  uuid not null references public.workspaces (id) on delete cascade,
  key           text not null,
  label         text not null,
  default_value text,
  description   text,
  is_active     boolean not null default true,
  created_by    uuid references public.users (id) on delete set null,
  created_at    timestamptz not null default now(),
  updated_at    timestamptz not null default now(),
  constraint custom_variables_key_check check (key ~ '^[a-z0-9_]{2,40}$')
);

create unique index if not exists uq_custom_variables_key
  on public.custom_variables (workspace_id, lower(key));

drop trigger if exists trg_custom_variables_updated_at on public.custom_variables;
create trigger trg_custom_variables_updated_at
  before update on public.custom_variables
  for each row execute function public.touch_updated_at();

-- -----------------------------------------------------------------------------
-- gpt_generation_logs — pemakaian GPT untuk menyusun draft
--
-- Yang dicatat adalah permintaan, hasil, dan biayanya. TIDAK ada API key, dan
-- tidak boleh ada: kolomnya memang tidak disediakan.
-- -----------------------------------------------------------------------------
create table if not exists public.gpt_generation_logs (
  id                uuid primary key default gen_random_uuid(),
  workspace_id      uuid not null references public.workspaces (id) on delete cascade,
  campaign_id       uuid references public.content_campaigns (id) on delete set null,
  admin_id          uuid references public.users (id) on delete set null,
  model             text not null,
  prompt            text not null,
  result            text,
  prompt_tokens     int,
  completion_tokens int,
  status            text not null default 'ok',
  failure_reason    text,
  created_at        timestamptz not null default now()
);

create index if not exists idx_gpt_logs_scope
  on public.gpt_generation_logs (workspace_id, created_at desc);

comment on table public.gpt_generation_logs is
  'Tidak menyimpan credential. Hasil GPT selalu berupa draft: pengguna harus mengonfirmasi sebelum dikirim.';

-- -----------------------------------------------------------------------------
-- story_publications — satu Story, satu baris per perangkat
--
-- Story tidak punya penerima seperti Broadcast: ia terbit di perangkat. Karena
-- itu unit antreannya adalah perangkat, dan kunci idempotensinya adalah
-- (campaign, perangkat) persis seperti yang diminta.
-- -----------------------------------------------------------------------------
create table if not exists public.story_publications (
  id               uuid primary key default gen_random_uuid(),
  workspace_id     uuid not null references public.workspaces (id) on delete cascade,
  campaign_id      uuid not null references public.content_campaigns (id) on delete cascade,
  account_id       uuid not null references public.whatsapp_accounts (id) on delete cascade,
  status           text not null default 'pending',
  attempt          int not null default 0,
  next_attempt_at  timestamptz,
  claimed_at       timestamptz,
  lease_expires_at timestamptz,
  wa_message_id    text,
  published_at     timestamptz,
  -- Story WhatsApp hidup 24 jam. Diisi saat terbit supaya kedaluwarsa bisa
  -- ditentukan dari kenyataan, bukan dari perkiraan saat membaca.
  expires_at       timestamptz,
  failure_reason   text,
  error_code       text,
  created_at       timestamptz not null default now(),
  updated_at       timestamptz not null default now(),
  constraint uq_story_publication unique (campaign_id, account_id),
  constraint story_publications_status_check
    check (status in ('pending', 'processing', 'published', 'failed', 'cancelled', 'expired'))
);

create index if not exists idx_story_publications_queue
  on public.story_publications (status, next_attempt_at);
create index if not exists idx_story_publications_campaign
  on public.story_publications (campaign_id);
create index if not exists idx_story_publications_wa_message
  on public.story_publications (wa_message_id) where wa_message_id is not null;

drop trigger if exists trg_story_publications_updated_at on public.story_publications;
create trigger trg_story_publications_updated_at
  before update on public.story_publications
  for each row execute function public.touch_updated_at();

-- -----------------------------------------------------------------------------
-- story_views — penonton yang benar-benar terdeteksi
--
-- Hanya diisi dari receipt nyata yang diterima whatsmeow untuk pesan status.
-- Satu baris per (publikasi, penonton), sehingga receipt berulang tidak menambah
-- angka. Angkanya adalah BATAS BAWAH: penonton yang mematikan read receipt dan
-- receipt yang datang saat backend mati tidak akan pernah muncul di sini, dan
-- tidak ada yang boleh mengarang selisihnya.
-- -----------------------------------------------------------------------------
create table if not exists public.story_views (
  id             uuid primary key default gen_random_uuid(),
  workspace_id   uuid not null references public.workspaces (id) on delete cascade,
  publication_id uuid not null references public.story_publications (id) on delete cascade,
  campaign_id    uuid not null references public.content_campaigns (id) on delete cascade,
  account_id     uuid not null references public.whatsapp_accounts (id) on delete cascade,
  viewer_jid     text not null,
  receipt_type   text not null,
  first_seen_at  timestamptz not null default now(),
  last_seen_at   timestamptz not null default now(),
  receipt_count  int not null default 1,
  constraint uq_story_view unique (publication_id, viewer_jid)
);

create index if not exists idx_story_views_campaign
  on public.story_views (campaign_id);

comment on table public.story_views is
  'Batas bawah, bukan jumlah penonton sebenarnya. Hanya receipt yang benar-benar diterima yang tercatat.';

-- story_view_snapshots — rekap angka pada satu saat, termasuk yang final
create table if not exists public.story_view_snapshots (
  id             uuid primary key default gen_random_uuid(),
  workspace_id   uuid not null references public.workspaces (id) on delete cascade,
  publication_id uuid not null references public.story_publications (id) on delete cascade,
  campaign_id    uuid not null references public.content_campaigns (id) on delete cascade,
  viewers        int not null default 0,
  is_final       boolean not null default false,
  captured_at    timestamptz not null default now()
);

create unique index if not exists uq_story_snapshot_final
  on public.story_view_snapshots (publication_id) where is_final;
create index if not exists idx_story_snapshots_campaign
  on public.story_view_snapshots (campaign_id, captured_at desc);

-- -----------------------------------------------------------------------------
-- messages.campaign_id — tautan dua arah antara pesan dan campaign
--
-- Pada pesan keluar: pesan yang dihasilkan Broadcast. Pada pesan masuk: balasan
-- pelanggan atas Broadcast. Balasan itu tetap pesan masuk biasa dan tetap
-- memulai siklus SLA — tautan ini hanya membuat laporan campaign bisa menyebut
-- berapa yang membalas, tanpa mengubah arti metrik chat mana pun.
-- -----------------------------------------------------------------------------
alter table public.messages
  add column if not exists campaign_id uuid references public.content_campaigns (id) on delete set null;

create index if not exists idx_messages_campaign
  on public.messages (campaign_id) where campaign_id is not null;

-- =============================================================================
-- RLS
--
-- Pola yang sama seperti 0024: tabel anak menumpang izin campaign induknya,
-- sehingga hanya ada satu tempat yang memutuskan siapa boleh melihat apa.
-- =============================================================================
alter table public.broadcast_sender_devices   enable row level security;
alter table public.broadcast_target_attempts  enable row level security;
alter table public.campaign_labels            enable row level security;
alter table public.campaign_label_assignments enable row level security;
alter table public.custom_variables           enable row level security;
alter table public.gpt_generation_logs        enable row level security;
alter table public.story_publications         enable row level security;
alter table public.story_views                enable row level security;
alter table public.story_view_snapshots       enable row level security;

do $$
declare
  t text;
begin
  foreach t in array array[
    'broadcast_sender_devices', 'broadcast_target_attempts', 'campaign_label_assignments',
    'story_publications', 'story_views', 'story_view_snapshots'
  ] loop
    execute format('drop policy if exists %I on public.%I', t || '_select', t);
    execute format($f$
      create policy %I on public.%I
        for select to authenticated
        using (exists (
          select 1 from public.content_campaigns c
           where c.id = %I.campaign_id
             and c.workspace_id = public.current_workspace_id()
             and (
               c.application_id in (select public.visible_application_ids())
               or (c.application_id is null
                   and coalesce(public.current_operational_role() = 'leader', true))
               or c.created_by = auth.uid()
             )))
    $f$, t || '_select', t, t);
  end loop;
end $$;

-- Label campaign dan variabel custom adalah milik workspace: setiap orang di
-- dalamnya boleh membacanya, karena keduanya dipakai untuk menyusun campaign
-- yang memang boleh mereka buat.
drop policy if exists campaign_labels_select on public.campaign_labels;
create policy campaign_labels_select on public.campaign_labels
  for select to authenticated
  using (workspace_id = public.current_workspace_id());

drop policy if exists custom_variables_select on public.custom_variables;
create policy custom_variables_select on public.custom_variables
  for select to authenticated
  using (workspace_id = public.current_workspace_id());

-- Menulis label campaign dan variabel: Leader dan PIC. Seorang Freelance
-- memakainya, tidak mendefinisikannya.
drop policy if exists campaign_labels_write on public.campaign_labels;
create policy campaign_labels_write on public.campaign_labels
  for all to authenticated
  using (workspace_id = public.current_workspace_id()
         and coalesce(public.current_operational_role() in ('leader', 'pic'), true))
  with check (workspace_id = public.current_workspace_id()
              and coalesce(public.current_operational_role() in ('leader', 'pic'), true));

drop policy if exists custom_variables_write on public.custom_variables;
create policy custom_variables_write on public.custom_variables
  for all to authenticated
  using (workspace_id = public.current_workspace_id()
         and coalesce(public.current_operational_role() in ('leader', 'pic'), true))
  with check (workspace_id = public.current_workspace_id()
              and coalesce(public.current_operational_role() in ('leader', 'pic'), true));

-- Log GPT: hanya miliknya sendiri, kecuali Leader.
drop policy if exists gpt_generation_logs_select on public.gpt_generation_logs;
create policy gpt_generation_logs_select on public.gpt_generation_logs
  for select to authenticated
  using (
    workspace_id = public.current_workspace_id()
    and (admin_id = auth.uid()
         or coalesce(public.current_operational_role() = 'leader', true))
  );

-- =============================================================================
-- Realtime
--
-- Hanya baris ringkasan yang dipublikasikan. campaign_targets sengaja TIDAK
-- ikut: sebuah campaign bisa punya ribuan baris target, dan menyalurkan setiap
-- perubahannya lewat WAL akan membebani basis data untuk sesuatu yang sudah
-- dikirim ringkas lewat hub WebSocket backend.
-- =============================================================================
alter table public.broadcast_sender_devices replica identity full;
alter table public.story_publications       replica identity full;

do $$
declare
  t text;
begin
  if exists (select 1 from pg_publication where pubname = 'supabase_realtime') then
    foreach t in array array['broadcast_sender_devices', 'story_publications'] loop
      begin
        execute format('alter publication supabase_realtime add table public.%I', t);
      exception when duplicate_object then null; end;
    end loop;
  end if;
end $$;
