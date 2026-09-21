-- =============================================================================
-- salesan.id — Migration 0023: riwayat label (append-only) + keadaan sekarang
-- =============================================================================
-- Sinkronisasi label dua arah sudah berjalan sejak 0006: perubahan dari HP tiba
-- lewat app-state (handleLabelEdit / handleLabelAssociation), perubahan dari web
-- didorong ke HP lewat whatsmeow, dan sebuah reconciler berkala memperbaiki
-- event yang terlewat. Yang belum ada adalah INGATAN atas semua itu.
--
-- conversation_label_assignments hanya menyimpan keadaan sekarang. Ia tidak bisa
-- menjawab "berapa kontak yang pindah dari Cold ke Warm bulan ini", karena
-- begitu labelnya berpindah, jejak label sebelumnya sudah tidak ada.
--
-- Dua tabel, dua tugas:
--
--   contact_label_events — append-only. Tidak pernah di-UPDATE, tidak pernah
--     di-DELETE. Nama label dibekukan sebagai snapshot, sehingga mengganti nama
--     label di HP tidak menulis ulang sejarah.
--   contact_label_state  — keadaan sekarang per kontak, diturunkan dari event.
--     Ada supaya "kontak yang tadinya belum punya label lalu diberi label"
--     dapat dijawab tanpa memindai seluruh riwayat.
--
-- Aman dijalankan ulang.
-- =============================================================================

do $$ begin
  create type public.label_event_type as enum (
    'label_created', 'label_updated', 'label_deleted',
    'label_assigned', 'label_removed', 'label_moved'
  );
exception when duplicate_object then null; end $$;

do $$ begin
  -- Dari mana perubahannya datang. 'whatsapp' berarti HP; kita tidak tahu siapa
  -- yang memegangnya, jadi admin_id-nya null dan tetap begitu.
  create type public.change_source as enum ('web', 'whatsapp', 'system');
exception when duplicate_object then null; end $$;

-- -----------------------------------------------------------------------------
-- contact_label_events
-- -----------------------------------------------------------------------------
create table if not exists public.contact_label_events (
  id               uuid primary key default gen_random_uuid(),
  workspace_id     uuid not null references public.workspaces (id) on delete cascade,
  account_id       uuid not null references public.whatsapp_accounts (id) on delete cascade,
  application_id   uuid references public.applications (id) on delete set null,
  -- Kontak yang terkena. Null untuk event yang menyangkut definisi labelnya
  -- saja (dibuat, diganti nama, dihapus) dan bukan pemasangannya.
  contact_id       uuid references public.contacts (id) on delete set null,
  conversation_id  uuid references public.conversations (id) on delete set null,
  event_type       public.label_event_type not null,

  -- Label sebelum dan sesudah. Untuk assigned hanya `to`, untuk removed hanya
  -- `from`, untuk moved keduanya.
  from_label_id    uuid references public.conversation_labels (id) on delete set null,
  to_label_id      uuid references public.conversation_labels (id) on delete set null,
  -- Snapshot nama, dibekukan pada saat kejadian. Inilah yang membuat riwayat
  -- tetap terbaca setelah labelnya diganti nama atau dihapus.
  from_label_name  text,
  to_label_name    text,

  source           public.change_source not null,
  -- Admin yang melakukannya. Null bila datang dari HP: identitas pemegang HP
  -- tidak dapat diverifikasi, dan menebaknya dilarang.
  admin_id         uuid references public.users (id) on delete set null,
  admin_role       public.operational_role,
  occurred_at      timestamptz not null default now(),
  created_at       timestamptz not null default now(),

  -- Kunci idempotensi. Event yang sama terkirim ulang — hal biasa saat
  -- reconnect, pemulihan app-state, atau retry — hanya tersimpan sekali.
  event_key        text not null,
  constraint uq_contact_label_events_key unique (account_id, event_key)
);

create index if not exists idx_contact_label_events_scope
  on public.contact_label_events (account_id, occurred_at desc);
create index if not exists idx_contact_label_events_app
  on public.contact_label_events (application_id, occurred_at desc);
create index if not exists idx_contact_label_events_contact
  on public.contact_label_events (contact_id, occurred_at desc)
  where contact_id is not null;
create index if not exists idx_contact_label_events_admin
  on public.contact_label_events (admin_id, occurred_at desc)
  where admin_id is not null;

comment on table public.contact_label_events is
  'Append-only. Tidak pernah di-update atau dihapus; nama label dibekukan sebagai snapshot supaya riwayat tidak berubah ketika data master berubah.';

-- -----------------------------------------------------------------------------
-- contact_label_state
-- -----------------------------------------------------------------------------
create table if not exists public.contact_label_state (
  contact_id        uuid primary key references public.contacts (id) on delete cascade,
  workspace_id      uuid not null references public.workspaces (id) on delete cascade,
  account_id        uuid not null references public.whatsapp_accounts (id) on delete cascade,
  application_id    uuid references public.applications (id) on delete set null,
  label_ids         uuid[] not null default '{}',
  label_names       text[] not null default '{}',
  -- Kapan kontak ini PERTAMA kali punya label. Kolom inilah yang menjawab
  -- "berapa kontak yang tadinya belum berlabel lalu diberi label", dan ia tidak
  -- bisa diturunkan dari keadaan sekarang.
  first_labeled_at  timestamptz,
  last_changed_at   timestamptz not null default now(),
  change_count      int not null default 0,
  updated_at        timestamptz not null default now()
);

create index if not exists idx_contact_label_state_scope
  on public.contact_label_state (account_id, last_changed_at desc);
create index if not exists idx_contact_label_state_first
  on public.contact_label_state (account_id, first_labeled_at)
  where first_labeled_at is not null;

drop trigger if exists trg_contact_label_state_updated_at on public.contact_label_state;
create trigger trg_contact_label_state_updated_at
  before update on public.contact_label_state
  for each row execute function public.touch_updated_at();

-- -----------------------------------------------------------------------------
-- Backfill keadaan sekarang dari penugasan yang sudah ada
--
-- Hanya STATE yang diisi, bukan event. Membuat baris riwayat untuk perubahan
-- yang waktunya tidak kita ketahui berarti mengarang audit log — persis hal
-- yang tabel ini ada untuk mencegahnya. Riwayat dimulai dari sekarang.
-- -----------------------------------------------------------------------------
insert into public.contact_label_state
  (contact_id, workspace_id, account_id, application_id,
   label_ids, label_names, first_labeled_at, last_changed_at, change_count)
select conv.contact_id,
       conv.workspace_id,
       conv.account_id,
       acc.application_id,
       array_agg(distinct l.id),
       array_agg(distinct l.name),
       null,
       now(),
       0
  from public.conversation_label_assignments a
  join public.conversations conv on conv.id = a.conversation_id
  join public.whatsapp_accounts acc on acc.id = conv.account_id
  join public.conversation_labels l on l.id = a.label_id
 where conv.contact_id is not null
 group by conv.contact_id, conv.workspace_id, conv.account_id, acc.application_id
on conflict (contact_id) do nothing;

-- =============================================================================
-- RLS
--
-- Baca sesuai lingkup aplikasi. Tidak ada kebijakan tulis untuk `authenticated`:
-- riwayat hanya ditulis backend, dan sebuah audit log yang bisa diubah dari
-- browser bukan audit log.
-- =============================================================================
alter table public.contact_label_events enable row level security;
alter table public.contact_label_state  enable row level security;

drop policy if exists contact_label_events_select on public.contact_label_events;
create policy contact_label_events_select on public.contact_label_events
  for select to authenticated
  using (
    workspace_id = public.current_workspace_id()
    and (
      application_id in (select public.visible_application_ids())
      or (application_id is null
          and coalesce(public.current_operational_role() = 'leader', true))
    )
  );

drop policy if exists contact_label_state_select on public.contact_label_state;
create policy contact_label_state_select on public.contact_label_state
  for select to authenticated
  using (
    workspace_id = public.current_workspace_id()
    and (
      application_id in (select public.visible_application_ids())
      or (application_id is null
          and coalesce(public.current_operational_role() = 'leader', true))
    )
  );

-- =============================================================================
-- Realtime
-- =============================================================================
alter table public.contact_label_events replica identity full;

do $$
begin
  if exists (select 1 from pg_publication where pubname = 'supabase_realtime') then
    begin
      alter publication supabase_realtime add table public.contact_label_events;
    exception when duplicate_object then null; end;
  end if;
end $$;
