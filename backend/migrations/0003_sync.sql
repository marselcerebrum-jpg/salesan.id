-- =============================================================================
-- salesan.id — Migration 0003: sinkronisasi (status baca, label, jendela 7 hari)
-- =============================================================================
-- Aman dijalankan ulang.
--
--   go run ./cmd/migrate -file migrations/0003_sync.sql
-- =============================================================================

-- -----------------------------------------------------------------------------
-- conversations
--
-- marked_unread: WhatsApp membedakan "ada pesan belum dibaca" dari "ditandai
-- belum dibaca secara manual". Keduanya perlu disimpan terpisah supaya ikon di
-- inbox jujur.
--
-- wa_conversation_at: stempel waktu aktivitas terakhir menurut WhatsApp. Ini
-- yang menentukan urutan daftar chat. Dibutuhkan karena kita hanya menyimpan
-- riwayat 7 hari: chat yang pesan terakhirnya 10 hari lalu tetap harus duduk di
-- posisi yang sama seperti di HP, meski tidak ada satu pun pesannya tersimpan.
-- -----------------------------------------------------------------------------
alter table public.conversations
  add column if not exists marked_unread     boolean not null default false,
  add column if not exists wa_conversation_at timestamptz;

-- Urutan inbox: pinned dulu, lalu aktivitas terbaru menurut WhatsApp, baru
-- pesan tersimpan sebagai cadangan.
drop index if exists idx_conversations_inbox;
create index if not exists idx_conversations_inbox
  on public.conversations (
    account_id,
    is_archived,
    is_pinned desc,
    coalesce(wa_conversation_at, last_message_at) desc nulls last
  );

-- -----------------------------------------------------------------------------
-- conversation_labels — bedakan label buatan sendiri dari label WhatsApp
-- -----------------------------------------------------------------------------
alter table public.conversation_labels
  add column if not exists source text not null default 'manual';

do $$ begin
  alter table public.conversation_labels
    add constraint conversation_labels_source_check
    check (source in ('manual', 'whatsapp'));
exception when duplicate_object then null; end $$;

-- -----------------------------------------------------------------------------
-- whatsapp_label_map
--
-- Label milik WhatsApp Business bersifat per-perangkat dan ber-ID angka, sedang
-- label kita milik workspace. Tabel ini memetakan keduanya, sehingga dua nomor
-- yang sama-sama punya label "Premium" menunjuk ke satu label yang sama di UI.
-- -----------------------------------------------------------------------------
create table if not exists public.whatsapp_label_map (
  account_id   uuid not null references public.whatsapp_accounts (id) on delete cascade,
  wa_label_id  text not null,
  label_id     uuid not null references public.conversation_labels (id) on delete cascade,
  created_at   timestamptz not null default now(),
  primary key (account_id, wa_label_id)
);

create index if not exists idx_whatsapp_label_map_label
  on public.whatsapp_label_map (label_id);

-- -----------------------------------------------------------------------------
-- whatsapp_accounts — pembukuan sinkronisasi
-- -----------------------------------------------------------------------------
alter table public.whatsapp_accounts
  add column if not exists last_synced_at   timestamptz,
  add column if not exists sync_window_days int not null default 7;

do $$ begin
  alter table public.whatsapp_accounts
    add constraint whatsapp_accounts_sync_window_check
    check (sync_window_days between 1 and 90);
exception when duplicate_object then null; end $$;

-- -----------------------------------------------------------------------------
-- RLS untuk tabel baru
-- -----------------------------------------------------------------------------
alter table public.whatsapp_label_map enable row level security;

drop policy if exists whatsapp_label_map_rw on public.whatsapp_label_map;
create policy whatsapp_label_map_rw on public.whatsapp_label_map
  for all to authenticated
  using (exists (
    select 1 from public.whatsapp_accounts a
     where a.id = whatsapp_label_map.account_id
       and a.workspace_id = public.current_workspace_id()))
  with check (exists (
    select 1 from public.whatsapp_accounts a
     where a.id = whatsapp_label_map.account_id
       and a.workspace_id = public.current_workspace_id()));

-- -----------------------------------------------------------------------------
-- Trigger pesan: jangan naikkan unread untuk pesan hasil sinkronisasi riwayat.
--
-- Saat menarik riwayat, jumlah belum-dibaca yang benar datang dari WhatsApp
-- sendiri (Conversation.unreadCount), bukan dari menghitung baris yang baru
-- masuk. Menghitung sendiri akan menggelembungkan angka setiap kali sinkron.
-- Penanda dipasang lewat GUC transaksi oleh lapisan repository.
-- -----------------------------------------------------------------------------
create or replace function public.bump_conversation_on_message()
returns trigger
language plpgsql
as $$
declare
  v_backfill boolean := coalesce(
    nullif(current_setting('salesan.backfill', true), '')::boolean, false);
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
         unread_count = case
           when v_backfill then c.unread_count
           when new.from_me then c.unread_count
           else c.unread_count + 1
         end,
         updated_at   = now()
   where c.id = new.conversation_id;
  return null;
end;
$$;
