-- =============================================================================
-- salesan.id — Migration 0024: Story & Broadcast sebagai aktivitas admin
-- =============================================================================
-- Menu Story dan Broadcast belum ada implementasinya di aplikasi ini (sidebar
-- masih menandainya COMING SOON). Yang dibangun di sini adalah fondasinya:
-- schema, kunci idempotensi, jejak audit, dan kontrak izin — supaya ketika
-- eksekutornya menyusul, angka-angka di Dashboard sudah benar sejak hari
-- pertama dan tidak perlu dihitung mundur.
--
-- Tidak ada satu pun baris contoh yang dimasukkan. Tabel yang kosong adalah
-- jawaban yang jujur untuk fitur yang belum dipakai; data karangan akan terlihat
-- seperti aktivitas nyata di laporan performa seseorang.
--
-- Aturan hitung yang ditegakkan di sini:
--
--   - Satu campaign = satu aktivitas utama. Jumlah penerima adalah metrik
--     TERPISAH, bukan pengali aktivitasnya.
--   - Mengubah jadwal menambah baris audit, TIDAK membuat campaign baru.
--   - Pesan keluar Broadcast bertanda sender_source = 'broadcast' (0021) dan
--     karena itu tidak pernah masuk ke pesan keluar chat normal, SLA,
--     follow-up, maupun performa balasan admin. Balasan pelanggan atasnya
--     adalah pesan masuk biasa dan boleh memulai SLA.
--
-- Aman dijalankan ulang.
-- =============================================================================

do $$ begin
  create type public.campaign_type as enum ('story', 'broadcast');
exception when duplicate_object then null; end $$;

do $$ begin
  create type public.campaign_status as enum (
    'draft', 'scheduled', 'running', 'completed', 'failed', 'cancelled'
  );
exception when duplicate_object then null; end $$;

do $$ begin
  create type public.campaign_activity_type as enum (
    'draft_created', 'schedule_created', 'schedule_updated', 'schedule_cancelled',
    'execution_started', 'published', 'failed', 'retried'
  );
exception when duplicate_object then null; end $$;

-- -----------------------------------------------------------------------------
-- content_campaigns — satu Story atau satu Broadcast
-- -----------------------------------------------------------------------------
create table if not exists public.content_campaigns (
  id              uuid primary key default gen_random_uuid(),
  workspace_id    uuid not null references public.workspaces (id) on delete cascade,
  application_id  uuid references public.applications (id) on delete set null,
  account_id      uuid references public.whatsapp_accounts (id) on delete set null,
  campaign_type   public.campaign_type not null,
  name            text not null,
  body            text,
  -- Lampiran dipakai ulang dari mekanisme media yang sudah ada: id-nya menunjuk
  -- message_attachments, sehingga validasi MIME, batas ukuran, bucket privat,
  -- dan signed URL yang sudah berjalan tetap berlaku tanpa jalur kedua.
  attachment_ids  uuid[] not null default '{}',
  status          public.campaign_status not null default 'draft',
  scheduled_at    timestamptz,
  executed_at     timestamptz,
  target_count    int not null default 0,
  success_count   int not null default 0,
  failed_count    int not null default 0,
  failure_reason  text,
  -- Pembuatnya. Snapshot peran dan PIC ikut disimpan supaya laporan lama tetap
  -- benar setelah perannya berganti.
  created_by      uuid references public.users (id) on delete set null,
  creator_role    public.operational_role,
  creator_pic_id  uuid references public.users (id) on delete set null,
  created_at      timestamptz not null default now(),
  updated_at      timestamptz not null default now(),
  constraint content_campaigns_counts_check
    check (success_count >= 0 and failed_count >= 0 and target_count >= 0)
);

create index if not exists idx_campaigns_scope
  on public.content_campaigns (workspace_id, campaign_type, created_at desc);
create index if not exists idx_campaigns_app
  on public.content_campaigns (application_id, created_at desc);
create index if not exists idx_campaigns_creator
  on public.content_campaigns (created_by, created_at desc) where created_by is not null;
-- Jadwal terdekat di Dashboard.
create index if not exists idx_campaigns_upcoming
  on public.content_campaigns (workspace_id, scheduled_at)
  where status = 'scheduled';

drop trigger if exists trg_content_campaigns_updated_at on public.content_campaigns;
create trigger trg_content_campaigns_updated_at
  before update on public.content_campaigns
  for each row execute function public.touch_updated_at();

-- -----------------------------------------------------------------------------
-- scheduled_publications — riwayat penjadwalan sebuah campaign
--
-- Satu baris per jadwal yang pernah berlaku. Menjadwalkan ulang menutup baris
-- lama (superseded_at) dan membuka yang baru; campaign-nya tetap satu. Itulah
-- yang membuat "Riwayat perubahan" pada layar detail bisa ditampilkan tanpa
-- mengarang.
-- -----------------------------------------------------------------------------
create table if not exists public.scheduled_publications (
  id             uuid primary key default gen_random_uuid(),
  workspace_id   uuid not null references public.workspaces (id) on delete cascade,
  campaign_id    uuid not null references public.content_campaigns (id) on delete cascade,
  scheduled_at   timestamptz not null,
  timezone       text not null default 'Asia/Jakarta',
  status         public.campaign_status not null default 'scheduled',
  attempt        int not null default 1,
  started_at     timestamptz,
  finished_at    timestamptz,
  failure_reason text,
  superseded_at  timestamptz,
  created_by     uuid references public.users (id) on delete set null,
  created_at     timestamptz not null default now(),
  updated_at     timestamptz not null default now()
);

create index if not exists idx_scheduled_publications_campaign
  on public.scheduled_publications (campaign_id, created_at desc);
create index if not exists idx_scheduled_publications_due
  on public.scheduled_publications (scheduled_at)
  where status = 'scheduled' and superseded_at is null;

drop trigger if exists trg_scheduled_publications_updated_at on public.scheduled_publications;
create trigger trg_scheduled_publications_updated_at
  before update on public.scheduled_publications
  for each row execute function public.touch_updated_at();

-- -----------------------------------------------------------------------------
-- campaign_targets — penerima, satu baris per tujuan
--
-- Kunci uniknya (campaign_id, chat_jid): menjalankan ulang sebuah campaign
-- tidak boleh menggandakan penerimanya, dan retry hanya menyentuh baris yang
-- statusnya gagal.
-- -----------------------------------------------------------------------------
create table if not exists public.campaign_targets (
  id              uuid primary key default gen_random_uuid(),
  workspace_id    uuid not null references public.workspaces (id) on delete cascade,
  campaign_id     uuid not null references public.content_campaigns (id) on delete cascade,
  contact_id      uuid references public.contacts (id) on delete set null,
  conversation_id uuid references public.conversations (id) on delete set null,
  chat_jid        text not null,
  status          text not null default 'pending',
  -- Pesan yang benar-benar terkirim, kalau ada. Inilah tautan antara campaign
  -- dan baris pesannya — dan pesan itu bertanda sender_source = 'broadcast',
  -- sehingga tetap keluar dari metrik chat normal.
  message_id      uuid references public.messages (id) on delete set null,
  attempt         int not null default 0,
  sent_at         timestamptz,
  failure_reason  text,
  created_at      timestamptz not null default now(),
  updated_at      timestamptz not null default now(),
  constraint campaign_targets_status_check
    check (status in ('pending', 'sent', 'failed', 'skipped')),
  constraint uq_campaign_targets unique (campaign_id, chat_jid)
);

create index if not exists idx_campaign_targets_campaign
  on public.campaign_targets (campaign_id, status);

drop trigger if exists trg_campaign_targets_updated_at on public.campaign_targets;
create trigger trg_campaign_targets_updated_at
  before update on public.campaign_targets
  for each row execute function public.touch_updated_at();

-- -----------------------------------------------------------------------------
-- campaign_delivery_stats — rekap per percobaan eksekusi
-- -----------------------------------------------------------------------------
create table if not exists public.campaign_delivery_stats (
  id             uuid primary key default gen_random_uuid(),
  workspace_id   uuid not null references public.workspaces (id) on delete cascade,
  campaign_id    uuid not null references public.content_campaigns (id) on delete cascade,
  publication_id uuid references public.scheduled_publications (id) on delete set null,
  attempt        int not null default 1,
  target_count   int not null default 0,
  success_count  int not null default 0,
  failed_count   int not null default 0,
  started_at     timestamptz,
  finished_at    timestamptz,
  created_at     timestamptz not null default now(),
  constraint uq_campaign_delivery_attempt unique (campaign_id, attempt)
);

create index if not exists idx_campaign_delivery_campaign
  on public.campaign_delivery_stats (campaign_id, attempt);

-- -----------------------------------------------------------------------------
-- admin_activity_logs — jejak aktivitas admin lintas fitur
--
-- Bukan hanya untuk campaign: ia juga menampung tindakan operasional lain yang
-- perlu diaudit (mengubah jadwal, mengubah penugasan peran). event_key membuat
-- retry tidak menggandakan baris.
-- -----------------------------------------------------------------------------
create table if not exists public.admin_activity_logs (
  id              uuid primary key default gen_random_uuid(),
  workspace_id    uuid not null references public.workspaces (id) on delete cascade,
  application_id  uuid references public.applications (id) on delete set null,
  account_id      uuid references public.whatsapp_accounts (id) on delete set null,
  admin_id        uuid references public.users (id) on delete set null,
  admin_role      public.operational_role,
  pic_id          uuid references public.users (id) on delete set null,
  -- Objek yang dikenai tindakan: 'campaign', 'schedule', 'role', ...
  entity_type     text not null,
  entity_id       uuid,
  activity_type   public.campaign_activity_type,
  -- Untuk entitas non-campaign; campaign memakai activity_type di atas.
  action          text,
  -- Snapshot nama objeknya pada saat kejadian.
  entity_name     text,
  detail          jsonb not null default '{}'::jsonb,
  status          text,
  failure_reason  text,
  occurred_at     timestamptz not null default now(),
  created_at      timestamptz not null default now(),
  event_key       text not null,
  constraint uq_admin_activity_event unique (workspace_id, event_key)
);

create index if not exists idx_admin_activity_scope
  on public.admin_activity_logs (workspace_id, occurred_at desc);
create index if not exists idx_admin_activity_app
  on public.admin_activity_logs (application_id, occurred_at desc);
create index if not exists idx_admin_activity_admin
  on public.admin_activity_logs (admin_id, occurred_at desc) where admin_id is not null;
create index if not exists idx_admin_activity_entity
  on public.admin_activity_logs (entity_type, entity_id, occurred_at desc);

comment on table public.admin_activity_logs is
  'Append-only. Nama entitas dibekukan sebagai snapshot; event_key adalah kunci idempotensi supaya retry tidak menggandakan aktivitas.';

-- =============================================================================
-- RLS
--
-- Membaca: sesuai lingkup aplikasi.
-- Menulis campaign: Leader dan PIC pada aplikasinya. Seorang Freelance boleh
-- membuat draft untuk aplikasi yang ditugaskan kepadanya — tetapi hanya
-- miliknya sendiri, dan itu ditegakkan lewat created_by.
-- Audit log tidak punya kebijakan tulis untuk browser sama sekali.
-- =============================================================================
alter table public.content_campaigns       enable row level security;
alter table public.scheduled_publications  enable row level security;
alter table public.campaign_targets        enable row level security;
alter table public.campaign_delivery_stats enable row level security;
alter table public.admin_activity_logs     enable row level security;

drop policy if exists content_campaigns_select on public.content_campaigns;
create policy content_campaigns_select on public.content_campaigns
  for select to authenticated
  using (
    workspace_id = public.current_workspace_id()
    and (
      application_id in (select public.visible_application_ids())
      or (application_id is null
          and coalesce(public.current_operational_role() = 'leader', true))
      or created_by = auth.uid()
    )
  );

drop policy if exists content_campaigns_write on public.content_campaigns;
create policy content_campaigns_write on public.content_campaigns
  for all to authenticated
  using (
    workspace_id = public.current_workspace_id()
    and (
      coalesce(public.current_operational_role() in ('leader', 'pic'), false)
      or created_by = auth.uid()
    )
    and (application_id is null
         or application_id in (select public.visible_application_ids()))
  )
  with check (
    workspace_id = public.current_workspace_id()
    and (
      coalesce(public.current_operational_role() in ('leader', 'pic'), false)
      or created_by = auth.uid()
    )
    and (application_id is null
         or application_id in (select public.visible_application_ids()))
  );

-- Tabel anak menumpang izin campaign induknya: satu tempat yang memutuskan,
-- bukan tiga aturan yang harus terus dijaga agar tetap sama.
do $$
declare
  t text;
begin
  foreach t in array array[
    'scheduled_publications', 'campaign_targets', 'campaign_delivery_stats'
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

drop policy if exists admin_activity_logs_select on public.admin_activity_logs;
create policy admin_activity_logs_select on public.admin_activity_logs
  for select to authenticated
  using (
    workspace_id = public.current_workspace_id()
    and (
      application_id in (select public.visible_application_ids())
      or (application_id is null
          and coalesce(public.current_operational_role() = 'leader', true))
      or admin_id in (select public.visible_admin_ids())
    )
  );

-- =============================================================================
-- Realtime
-- =============================================================================
alter table public.content_campaigns      replica identity full;
alter table public.scheduled_publications replica identity full;
alter table public.admin_activity_logs    replica identity full;

do $$
declare
  t text;
begin
  if exists (select 1 from pg_publication where pubname = 'supabase_realtime') then
    foreach t in array array[
      'content_campaigns', 'scheduled_publications', 'admin_activity_logs'
    ] loop
      begin
        execute format('alter publication supabase_realtime add table public.%I', t);
      exception when duplicate_object then null; end;
    end loop;
  end if;
end $$;
