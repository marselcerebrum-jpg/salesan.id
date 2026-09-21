-- =============================================================================
-- salesan.id — Omnichannel MVP (WhatsApp only)
-- Migration 0001: schema, indexes, triggers, RLS
-- Target: Supabase PostgreSQL 15+
-- =============================================================================
-- Run this in the Supabase SQL Editor, or via `psql "$SUPABASE_DB_URL" -f 0001_init.sql`.
-- It is idempotent enough to re-run on a fresh project; it is NOT a down-migration.
-- =============================================================================

create extension if not exists "pgcrypto";

-- -----------------------------------------------------------------------------
-- Enums
-- -----------------------------------------------------------------------------
do $$ begin
  create type public.connection_method as enum ('qr', 'waba');
exception when duplicate_object then null; end $$;

do $$ begin
  -- Lifecycle of a WhatsApp account's socket.
  create type public.account_status as enum (
    'disconnected',  -- no session / intentionally offline
    'connecting',    -- dialing, no QR needed (session exists)
    'qr_pending',    -- waiting for the user to scan a QR
    'connected',     -- online
    'logged_out',    -- device was unlinked from the phone
    'error'          -- last connect attempt failed, see status_detail
  );
exception when duplicate_object then null; end $$;

do $$ begin
  create type public.conversation_type as enum ('personal', 'group');
exception when duplicate_object then null; end $$;

do $$ begin
  create type public.conversation_status as enum ('new', 'in_progress', 'done');
exception when duplicate_object then null; end $$;

do $$ begin
  create type public.message_status as enum ('pending', 'sent', 'delivered', 'read', 'failed');
exception when duplicate_object then null; end $$;

do $$ begin
  create type public.message_type as enum (
    'text', 'image', 'video', 'audio', 'document', 'sticker',
    'location', 'contact', 'reaction', 'system', 'unsupported'
  );
exception when duplicate_object then null; end $$;

do $$ begin
  create type public.workspace_role as enum ('owner', 'admin', 'agent');
exception when duplicate_object then null; end $$;

-- -----------------------------------------------------------------------------
-- Shared trigger: keep updated_at honest
-- -----------------------------------------------------------------------------
create or replace function public.touch_updated_at()
returns trigger
language plpgsql
as $$
begin
  new.updated_at = now();
  return new;
end;
$$;

-- -----------------------------------------------------------------------------
-- workspaces
-- -----------------------------------------------------------------------------
create table if not exists public.workspaces (
  id          uuid primary key default gen_random_uuid(),
  name        text not null,
  slug        text not null unique,
  created_at  timestamptz not null default now(),
  updated_at  timestamptz not null default now()
);

drop trigger if exists trg_workspaces_updated_at on public.workspaces;
create trigger trg_workspaces_updated_at
  before update on public.workspaces
  for each row execute function public.touch_updated_at();

-- -----------------------------------------------------------------------------
-- users — application profile mirroring auth.users
-- -----------------------------------------------------------------------------
create table if not exists public.users (
  id            uuid primary key references auth.users (id) on delete cascade,
  workspace_id  uuid not null references public.workspaces (id) on delete cascade,
  email         text not null,
  full_name     text,
  avatar_url    text,
  role          public.workspace_role not null default 'owner',
  created_at    timestamptz not null default now(),
  updated_at    timestamptz not null default now()
);

create index if not exists idx_users_workspace on public.users (workspace_id);

drop trigger if exists trg_users_updated_at on public.users;
create trigger trg_users_updated_at
  before update on public.users
  for each row execute function public.touch_updated_at();

-- Resolve the caller's workspace. SECURITY DEFINER so the policies that use it
-- are not themselves subject to RLS on public.users (which would recurse).
create or replace function public.current_workspace_id()
returns uuid
language sql
stable
security definer
set search_path = public
as $$
  select workspace_id from public.users where id = auth.uid()
$$;

revoke all on function public.current_workspace_id() from public;
grant execute on function public.current_workspace_id() to authenticated, service_role;

-- -----------------------------------------------------------------------------
-- applications — the "JADIASN / JADIBUMN / ..." classification
-- -----------------------------------------------------------------------------
create table if not exists public.applications (
  id            uuid primary key default gen_random_uuid(),
  workspace_id  uuid not null references public.workspaces (id) on delete cascade,
  code          text not null,
  name          text not null,
  color         text not null default '#0F3D2E',
  icon_url      text,
  sort_order    int  not null default 0,
  is_active     boolean not null default true,
  created_at    timestamptz not null default now(),
  updated_at    timestamptz not null default now(),
  constraint uq_applications_workspace_code unique (workspace_id, code)
);

create index if not exists idx_applications_workspace on public.applications (workspace_id, sort_order);

drop trigger if exists trg_applications_updated_at on public.applications;
create trigger trg_applications_updated_at
  before update on public.applications
  for each row execute function public.touch_updated_at();

-- -----------------------------------------------------------------------------
-- whatsapp_accounts — one linked device / WABA number
-- -----------------------------------------------------------------------------
create table if not exists public.whatsapp_accounts (
  id                 uuid primary key default gen_random_uuid(),
  workspace_id       uuid not null references public.workspaces (id) on delete cascade,
  application_id     uuid references public.applications (id) on delete set null,
  name               text not null,
  label              text,                       -- "HP UTAMA", "HP GRUP", "ADMIN KONSULTASI"
  phone_number       text,                       -- 62xxxx, filled after pairing
  device_id          text not null,              -- short human code shown on the card, e.g. D-GSRG3S
  jid                text,                       -- full whatsmeow JID, e.g. 628xxx:12@s.whatsapp.net
  connection_method  public.connection_method not null default 'qr',
  status             public.account_status not null default 'disconnected',
  status_detail      text,
  daily_send_limit   int not null default 20,
  last_connected_at  timestamptz,
  created_at         timestamptz not null default now(),
  updated_at         timestamptz not null default now(),
  constraint uq_whatsapp_accounts_device_id unique (workspace_id, device_id)
);

create index if not exists idx_wa_accounts_workspace   on public.whatsapp_accounts (workspace_id);
create index if not exists idx_wa_accounts_application on public.whatsapp_accounts (application_id);
create unique index if not exists uq_wa_accounts_jid   on public.whatsapp_accounts (workspace_id, jid) where jid is not null;

drop trigger if exists trg_wa_accounts_updated_at on public.whatsapp_accounts;
create trigger trg_wa_accounts_updated_at
  before update on public.whatsapp_accounts
  for each row execute function public.touch_updated_at();

-- -----------------------------------------------------------------------------
-- whatsapp_sessions — server-side session metadata.
--
-- IMPORTANT: the actual Signal/Noise credentials live in the `whatsmeow_*`
-- tables created by whatsmeow's own sqlstore (see migration 0002). This table
-- only records *which* device is bound to an account plus connection bookkeeping,
-- so nothing secret is ever exposed. RLS below denies every browser role.
-- -----------------------------------------------------------------------------
create table if not exists public.whatsapp_sessions (
  id               uuid primary key default gen_random_uuid(),
  account_id       uuid not null references public.whatsapp_accounts (id) on delete cascade,
  device_jid       text not null,
  push_name        text,
  platform         text,
  business_name    text,
  is_active        boolean not null default true,
  connected_at     timestamptz,
  disconnected_at  timestamptz,
  created_at       timestamptz not null default now(),
  updated_at       timestamptz not null default now(),
  constraint uq_whatsapp_sessions_device unique (account_id, device_jid)
);

create index if not exists idx_wa_sessions_account on public.whatsapp_sessions (account_id) where is_active;

drop trigger if exists trg_wa_sessions_updated_at on public.whatsapp_sessions;
create trigger trg_wa_sessions_updated_at
  before update on public.whatsapp_sessions
  for each row execute function public.touch_updated_at();

-- -----------------------------------------------------------------------------
-- contacts
-- -----------------------------------------------------------------------------
create table if not exists public.contacts (
  id            uuid primary key default gen_random_uuid(),
  workspace_id  uuid not null references public.workspaces (id) on delete cascade,
  account_id    uuid not null references public.whatsapp_accounts (id) on delete cascade,
  jid           text not null,
  phone_number  text,
  name          text,
  push_name     text,
  business_name text,
  avatar_url    text,
  is_business   boolean not null default false,
  is_blocked    boolean not null default false,
  created_at    timestamptz not null default now(),
  updated_at    timestamptz not null default now(),
  constraint uq_contacts_account_jid unique (account_id, jid)
);

create index if not exists idx_contacts_workspace on public.contacts (workspace_id);
create index if not exists idx_contacts_phone     on public.contacts (workspace_id, phone_number);

drop trigger if exists trg_contacts_updated_at on public.contacts;
create trigger trg_contacts_updated_at
  before update on public.contacts
  for each row execute function public.touch_updated_at();

-- -----------------------------------------------------------------------------
-- conversations
-- -----------------------------------------------------------------------------
create table if not exists public.conversations (
  id                    uuid primary key default gen_random_uuid(),
  workspace_id          uuid not null references public.workspaces (id) on delete cascade,
  account_id            uuid not null references public.whatsapp_accounts (id) on delete cascade,
  contact_id            uuid references public.contacts (id) on delete set null,
  chat_jid              text not null,
  type                  public.conversation_type not null default 'personal',
  name                  text,
  avatar_url            text,
  status                public.conversation_status not null default 'new',
  unread_count          int not null default 0,
  last_message_at       timestamptz,
  last_message_preview  text,
  last_message_from_me  boolean not null default false,
  is_archived           boolean not null default false,
  is_pinned             boolean not null default false,
  assigned_to           uuid references public.users (id) on delete set null,
  created_at            timestamptz not null default now(),
  updated_at            timestamptz not null default now(),
  constraint uq_conversations_account_chat unique (account_id, chat_jid)
);

create index if not exists idx_conversations_inbox
  on public.conversations (account_id, is_archived, last_message_at desc nulls last);
create index if not exists idx_conversations_workspace on public.conversations (workspace_id);
create index if not exists idx_conversations_status    on public.conversations (account_id, status);
create index if not exists idx_conversations_unread    on public.conversations (account_id) where unread_count > 0;

drop trigger if exists trg_conversations_updated_at on public.conversations;
create trigger trg_conversations_updated_at
  before update on public.conversations
  for each row execute function public.touch_updated_at();

-- -----------------------------------------------------------------------------
-- conversation_members — participants of a group chat
-- -----------------------------------------------------------------------------
create table if not exists public.conversation_members (
  id               uuid primary key default gen_random_uuid(),
  conversation_id  uuid not null references public.conversations (id) on delete cascade,
  contact_id       uuid references public.contacts (id) on delete set null,
  jid              text not null,
  display_name     text,
  is_admin         boolean not null default false,
  joined_at        timestamptz not null default now(),
  constraint uq_conversation_members unique (conversation_id, jid)
);

create index if not exists idx_conversation_members_conv on public.conversation_members (conversation_id);

-- -----------------------------------------------------------------------------
-- messages
--
-- (account_id, wa_message_id) is the idempotency key: whatsmeow can replay the
-- same message on reconnect/history-sync, and every write path uses
-- ON CONFLICT DO NOTHING/UPDATE against this constraint.
-- -----------------------------------------------------------------------------
create table if not exists public.messages (
  id                 uuid primary key default gen_random_uuid(),
  workspace_id       uuid not null references public.workspaces (id) on delete cascade,
  account_id         uuid not null references public.whatsapp_accounts (id) on delete cascade,
  conversation_id    uuid not null references public.conversations (id) on delete cascade,
  wa_message_id      text not null,
  sender_jid         text,
  sender_name        text,
  from_me            boolean not null default false,
  type               public.message_type not null default 'text',
  body               text,
  caption            text,
  media_url          text,
  media_mime         text,
  media_size         bigint,
  quoted_message_id  text,
  status             public.message_status not null default 'sent',
  error_message      text,
  sent_by            uuid references public.users (id) on delete set null,
  timestamp          timestamptz not null default now(),
  created_at         timestamptz not null default now(),
  updated_at         timestamptz not null default now(),
  constraint uq_messages_account_wamid unique (account_id, wa_message_id)
);

create index if not exists idx_messages_conversation
  on public.messages (conversation_id, timestamp desc);
create index if not exists idx_messages_workspace on public.messages (workspace_id);

drop trigger if exists trg_messages_updated_at on public.messages;
create trigger trg_messages_updated_at
  before update on public.messages
  for each row execute function public.touch_updated_at();

-- Keep the conversation preview + unread counter in sync. Because inserts use
-- ON CONFLICT on the idempotency key, this only fires for genuinely new rows,
-- so unread counts can never be double-incremented by a replayed message.
create or replace function public.bump_conversation_on_message()
returns trigger
language plpgsql
as $$
begin
  update public.conversations c
     set last_message_at      = greatest(coalesce(c.last_message_at, new.timestamp), new.timestamp),
         last_message_preview = case
           when new.timestamp >= coalesce(c.last_message_at, new.timestamp)
             then coalesce(nullif(new.body, ''), nullif(new.caption, ''), '[' || new.type::text || ']')
           else c.last_message_preview
         end,
         last_message_from_me = case
           when new.timestamp >= coalesce(c.last_message_at, new.timestamp) then new.from_me
           else c.last_message_from_me
         end,
         unread_count = case when new.from_me then c.unread_count else c.unread_count + 1 end,
         updated_at   = now()
   where c.id = new.conversation_id;
  return null;
end;
$$;

drop trigger if exists trg_messages_bump_conversation on public.messages;
create trigger trg_messages_bump_conversation
  after insert on public.messages
  for each row execute function public.bump_conversation_on_message();

-- -----------------------------------------------------------------------------
-- conversation_labels + assignments
-- -----------------------------------------------------------------------------
create table if not exists public.conversation_labels (
  id            uuid primary key default gen_random_uuid(),
  workspace_id  uuid not null references public.workspaces (id) on delete cascade,
  name          text not null,
  color         text not null default '#B45309',
  sort_order    int not null default 0,
  created_at    timestamptz not null default now(),
  updated_at    timestamptz not null default now(),
  constraint uq_conversation_labels_name unique (workspace_id, name)
);

create index if not exists idx_conversation_labels_workspace
  on public.conversation_labels (workspace_id, sort_order);

drop trigger if exists trg_conversation_labels_updated_at on public.conversation_labels;
create trigger trg_conversation_labels_updated_at
  before update on public.conversation_labels
  for each row execute function public.touch_updated_at();

create table if not exists public.conversation_label_assignments (
  conversation_id  uuid not null references public.conversations (id) on delete cascade,
  label_id         uuid not null references public.conversation_labels (id) on delete cascade,
  assigned_by      uuid references public.users (id) on delete set null,
  assigned_at      timestamptz not null default now(),
  primary key (conversation_id, label_id)
);

create index if not exists idx_label_assignments_label on public.conversation_label_assignments (label_id);

-- =============================================================================
-- Signup bootstrap: every new auth user gets a workspace, a profile and a
-- starter set of applications/labels so the UI is never empty on first login.
-- =============================================================================
create or replace function public.handle_new_auth_user()
returns trigger
language plpgsql
security definer
set search_path = public
as $$
declare
  v_workspace_id uuid;
  v_slug         text;
  v_name         text;
begin
  v_name := coalesce(new.raw_user_meta_data ->> 'full_name', split_part(new.email, '@', 1));
  v_slug := lower(regexp_replace(split_part(new.email, '@', 1), '[^a-zA-Z0-9]+', '-', 'g'))
            || '-' || substr(replace(new.id::text, '-', ''), 1, 8);

  insert into public.workspaces (name, slug)
  values (coalesce(v_name, 'Workspace') || '''s Workspace', v_slug)
  returning id into v_workspace_id;

  insert into public.users (id, workspace_id, email, full_name, role)
  values (new.id, v_workspace_id, new.email, v_name, 'owner');

  insert into public.applications (workspace_id, code, name, color, sort_order) values
    (v_workspace_id, 'JADIASN',      'JADIASN',      '#1B7F5A',  1),
    (v_workspace_id, 'JADIBEASISWA', 'JADIBEASISWA', '#C2853A',  2),
    (v_workspace_id, 'JADIBUMN',     'JADIBUMN',     '#166534',  3),
    (v_workspace_id, 'JADIOJK',      'JADIOJK',      '#2563EB',  4),
    (v_workspace_id, 'JADIPCPM',     'JADIPCPM',     '#7C3AED',  5),
    (v_workspace_id, 'JADIPOLISI',   'JADIPOLISI',   '#DC2626',  6),
    (v_workspace_id, 'JADISEKDIN',   'JADISEKDIN',   '#D97706',  7),
    (v_workspace_id, 'TOEFLACADEMY', 'TOEFLACADEMY', '#0891B2',  8)
  on conflict do nothing;

  -- Labels are deliberately NOT seeded. They must mirror the WhatsApp Business
  -- label set pulled during a sync, or be created by hand in the app; inventing
  -- them here would put tags in the inbox filter row that exist nowhere on the
  -- phone. See migration 0005, which removed the seed that used to live here.

  return new;
end;
$$;

drop trigger if exists on_auth_user_created on auth.users;
create trigger on_auth_user_created
  after insert on auth.users
  for each row execute function public.handle_new_auth_user();

-- =============================================================================
-- Row Level Security
--
-- Model: everything is scoped to a workspace; a user may only touch rows whose
-- workspace_id matches their own. The Go backend connects with the service role
-- (which bypasses RLS) and re-applies the same scoping in the repository layer,
-- so both paths are protected.
-- =============================================================================
alter table public.workspaces                     enable row level security;
alter table public.users                          enable row level security;
alter table public.applications                   enable row level security;
alter table public.whatsapp_accounts              enable row level security;
alter table public.whatsapp_sessions              enable row level security;
alter table public.contacts                       enable row level security;
alter table public.conversations                  enable row level security;
alter table public.conversation_members           enable row level security;
alter table public.messages                       enable row level security;
alter table public.conversation_labels            enable row level security;
alter table public.conversation_label_assignments enable row level security;

-- workspaces: read your own only.
drop policy if exists workspaces_select on public.workspaces;
create policy workspaces_select on public.workspaces
  for select to authenticated
  using (id = public.current_workspace_id());

-- users: read every member of your workspace, update only yourself.
drop policy if exists users_select on public.users;
create policy users_select on public.users
  for select to authenticated
  using (workspace_id = public.current_workspace_id());

drop policy if exists users_update_self on public.users;
create policy users_update_self on public.users
  for update to authenticated
  using (id = auth.uid())
  with check (id = auth.uid() and workspace_id = public.current_workspace_id());

-- Workspace-scoped tables that carry workspace_id directly.
do $$
declare
  t text;
begin
  foreach t in array array[
    'applications', 'whatsapp_accounts', 'contacts',
    'conversations', 'messages', 'conversation_labels'
  ] loop
    execute format('drop policy if exists %I on public.%I', t || '_rw', t);
    execute format($f$
      create policy %I on public.%I
        for all to authenticated
        using (workspace_id = public.current_workspace_id())
        with check (workspace_id = public.current_workspace_id())
    $f$, t || '_rw', t);
  end loop;
end $$;

-- Child tables reach the workspace through their parent conversation.
drop policy if exists conversation_members_rw on public.conversation_members;
create policy conversation_members_rw on public.conversation_members
  for all to authenticated
  using (exists (
    select 1 from public.conversations c
     where c.id = conversation_members.conversation_id
       and c.workspace_id = public.current_workspace_id()))
  with check (exists (
    select 1 from public.conversations c
     where c.id = conversation_members.conversation_id
       and c.workspace_id = public.current_workspace_id()));

drop policy if exists conversation_label_assignments_rw on public.conversation_label_assignments;
create policy conversation_label_assignments_rw on public.conversation_label_assignments
  for all to authenticated
  using (exists (
    select 1 from public.conversations c
     where c.id = conversation_label_assignments.conversation_id
       and c.workspace_id = public.current_workspace_id()))
  with check (exists (
    select 1 from public.conversations c
     where c.id = conversation_label_assignments.conversation_id
       and c.workspace_id = public.current_workspace_id()));

-- whatsapp_sessions: deliberately NO policy for `authenticated`.
-- RLS is on and no policy grants access, so browsers get zero rows. Only the
-- service role (backend) can read or write session bookkeeping.
comment on table public.whatsapp_sessions is
  'Server-only. RLS enabled with no authenticated policy: never readable from the browser.';

-- =============================================================================
-- Supabase Realtime — publish the tables the inbox listens to.
-- (The Go backend also pushes over its own WebSocket; this is the DB-level path.)
-- =============================================================================
alter table public.conversations     replica identity full;
alter table public.messages          replica identity full;
alter table public.whatsapp_accounts replica identity full;

do $$
begin
  if exists (select 1 from pg_publication where pubname = 'supabase_realtime') then
    begin
      alter publication supabase_realtime add table public.conversations;
    exception when duplicate_object then null; end;
    begin
      alter publication supabase_realtime add table public.messages;
    exception when duplicate_object then null; end;
    begin
      alter publication supabase_realtime add table public.whatsapp_accounts;
    exception when duplicate_object then null; end;
  end if;
end $$;
