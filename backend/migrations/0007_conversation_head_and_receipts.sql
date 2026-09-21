-- =============================================================================
-- salesan.id — Migration 0007: kepala percakapan & status dibaca
-- =============================================================================
-- Dua masalah diselesaikan di sini.
--
-- (1) Preview daftar chat bisa tertinggal pada pesan admin meski sudah ada
--     pesan customer yang lebih baru.
--
--     Penyebabnya bukan perbandingan timestamp, melainkan dari mana angkanya
--     berasal. Pesan keluar disisipkan dengan jam mesin (time.Now) lalu
--     timestamp-nya ditimpa waktu server WhatsApp — sedangkan trigger hanya
--     berjalan saat INSERT. Kalau jam mesin lebih maju, last_message_at ikut
--     maju, dan pesan masuk berikutnya kalah oleh penjaga ">=" lalu diabaikan.
--
--     Alih-alih menambal perbandingan itu, kepala percakapan sekarang DIHITUNG
--     ULANG dari tabel messages. Baris terbaru menurut timestamp selalu menang,
--     tidak peduli urutan kedatangan, jam siapa pun, atau berapa kali event
--     terkirim ulang. Ini juga yang membuat "event lama tidak boleh menimpa
--     data lebih baru" berlaku dengan sendirinya.
--
-- (2) Status dibaca perlu tersimpan, bukan hanya tercermin di jumlah unread.
--
-- Aman dijalankan ulang.
-- =============================================================================

-- -----------------------------------------------------------------------------
-- conversations: kepala percakapan yang eksplisit
-- -----------------------------------------------------------------------------
alter table public.conversations
  add column if not exists last_message_id        uuid,
  add column if not exists last_message_direction text;

-- last_message_preview -> last_message_text
do $$ begin
  if exists (
    select 1 from information_schema.columns
     where table_schema = 'public' and table_name = 'conversations'
       and column_name = 'last_message_preview'
  ) and not exists (
    select 1 from information_schema.columns
     where table_schema = 'public' and table_name = 'conversations'
       and column_name = 'last_message_text'
  ) then
    alter table public.conversations rename column last_message_preview to last_message_text;
  end if;
end $$;

alter table public.conversations
  add column if not exists last_message_text text;

-- FK dipasang terpisah supaya migrasi tetap idempoten.
do $$ begin
  alter table public.conversations
    add constraint conversations_last_message_fk
    foreign key (last_message_id) references public.messages (id) on delete set null;
exception when duplicate_object then null; end $$;

do $$ begin
  alter table public.conversations
    add constraint conversations_last_message_direction_check
    check (last_message_direction is null or last_message_direction in ('in', 'out'));
exception when duplicate_object then null; end $$;

-- Turunkan arah dari kolom lama sebelum kolom itu dibuang.
do $$ begin
  if exists (
    select 1 from information_schema.columns
     where table_schema = 'public' and table_name = 'conversations'
       and column_name = 'last_message_from_me'
  ) then
    update public.conversations
       set last_message_direction = case when last_message_from_me then 'out' else 'in' end
     where last_message_direction is null and last_message_at is not null;

    alter table public.conversations drop column last_message_from_me;
  end if;
end $$;

-- -----------------------------------------------------------------------------
-- messages: kapan terkirim & kapan dibaca
-- -----------------------------------------------------------------------------
alter table public.messages
  add column if not exists delivered_at timestamptz,
  add column if not exists read_at      timestamptz;

-- Isi mundur dari status yang sudah ada, supaya data lama tidak tampak seolah
-- belum pernah diterima atau dibaca.
update public.messages
   set delivered_at = coalesce(delivered_at, timestamp)
 where status in ('delivered', 'read') and delivered_at is null;

update public.messages
   set read_at = coalesce(read_at, timestamp)
 where status = 'read' and read_at is null;

-- -----------------------------------------------------------------------------
-- Idempotensi event receipt
--
-- WhatsApp mengirim ulang receipt bebas-bebas saja: saat reconnect, saat
-- offline sync, dan dari tiap perangkat tertaut. Tanpa kunci ini, satu receipt
-- yang datang dua kali bisa membuat unread count melenceng.
-- -----------------------------------------------------------------------------
create table if not exists public.whatsapp_receipt_events (
  account_id uuid not null references public.whatsapp_accounts (id) on delete cascade,
  event_key  text not null,
  applied_at timestamptz not null default now(),
  primary key (account_id, event_key)
);

create index if not exists idx_whatsapp_receipt_events_applied
  on public.whatsapp_receipt_events (applied_at);

alter table public.whatsapp_receipt_events enable row level security;

drop policy if exists whatsapp_receipt_events_rw on public.whatsapp_receipt_events;
create policy whatsapp_receipt_events_rw on public.whatsapp_receipt_events
  for all to authenticated
  using (exists (
    select 1 from public.whatsapp_accounts a
     where a.id = whatsapp_receipt_events.account_id
       and a.workspace_id = public.current_workspace_id()))
  with check (exists (
    select 1 from public.whatsapp_accounts a
     where a.id = whatsapp_receipt_events.account_id
       and a.workspace_id = public.current_workspace_id()));

-- -----------------------------------------------------------------------------
-- refresh_conversation_head
--
-- Sumber kebenaran kepala percakapan adalah baris terbaru di tabel messages,
-- bukan baris yang kebetulan sedang disisipkan. Karena selalu membaca ulang,
-- fungsi ini kebal terhadap urutan kedatangan yang acak, event yang terkirim
-- ulang, maupun timestamp yang belakangan dikoreksi.
--
-- Urutan pemilihan: timestamp terbaru, lalu created_at sebagai pemecah seri —
-- timestamp WhatsApp hanya berpresisi detik, jadi seri itu lumrah.
-- -----------------------------------------------------------------------------
create or replace function public.refresh_conversation_head(p_conversation_id uuid)
returns void
language plpgsql
as $$
begin
  update public.conversations c
     set last_message_id        = m.id,
         last_message_text      = m.preview,
         last_message_at        = m.timestamp,
         last_message_direction = case when m.from_me then 'out' else 'in' end,
         updated_at             = now()
    from (
      select id,
             timestamp,
             from_me,
             coalesce(nullif(body, ''), nullif(caption, ''), '[' || type::text || ']') as preview
        from public.messages
       where conversation_id = p_conversation_id
       order by timestamp desc, created_at desc
       limit 1
    ) m
   where c.id = p_conversation_id
     and (c.last_message_id is distinct from m.id
       or c.last_message_at is distinct from m.timestamp
       or c.last_message_text is distinct from m.preview);
end;
$$;

comment on function public.refresh_conversation_head(uuid) is
  'Recomputes a conversation head from its newest message; safe to call repeatedly.';

-- -----------------------------------------------------------------------------
-- Trigger
--
-- Berjalan pada INSERT dan pada UPDATE timestamp/body/caption. Cabang UPDATE
-- itu yang menutup celah asli: pesan keluar disisipkan dengan jam mesin, lalu
-- timestamp-nya dikoreksi ke waktu server setelah WhatsApp menjawab.
--
-- unread_count hanya naik untuk pesan masuk yang benar-benar baru, dan tidak
-- pernah saat backfill riwayat — di sana angka yang benar datang dari WhatsApp
-- sendiri dan dipasang belakangan.
-- -----------------------------------------------------------------------------
create or replace function public.bump_conversation_on_message()
returns trigger
language plpgsql
as $$
declare
  v_backfill boolean := coalesce(
    nullif(current_setting('salesan.backfill', true), '')::boolean, false);
begin
  if tg_op = 'INSERT' and not new.from_me and not v_backfill then
    update public.conversations
       set unread_count = unread_count + 1
     where id = new.conversation_id;
  end if;

  -- Selama backfill, kepala percakapan disegarkan sekali di akhir batch oleh
  -- lapisan repository; menghitung ulang per baris akan sia-sia untuk ribuan
  -- pesan sekaligus.
  if not v_backfill then
    perform public.refresh_conversation_head(new.conversation_id);
  end if;

  return null;
end;
$$;

drop trigger if exists trg_messages_bump_conversation on public.messages;
create trigger trg_messages_bump_conversation
  after insert or update of timestamp, body, caption on public.messages
  for each row execute function public.bump_conversation_on_message();

-- -----------------------------------------------------------------------------
-- Urutan inbox
--
-- Diurutkan menurut bukti aktivitas terakhir yang kita punya. last_message_at
-- adalah yang utama; wa_conversation_at menjadi cadangan untuk percakapan yang
-- pesan terbarunya berada di luar jendela sinkronisasi 7 hari, agar tetap duduk
-- di posisi yang sama seperti di HP.
-- -----------------------------------------------------------------------------
drop index if exists idx_conversations_inbox;
create index if not exists idx_conversations_inbox
  on public.conversations (
    account_id,
    is_archived,
    is_pinned desc,
    greatest(
      coalesce(last_message_at, '-infinity'::timestamptz),
      coalesce(wa_conversation_at, '-infinity'::timestamptz)
    ) desc
  );

-- -----------------------------------------------------------------------------
-- Isi ulang kepala percakapan untuk data yang sudah ada
-- -----------------------------------------------------------------------------
do $$
declare
  r record;
  n int := 0;
begin
  for r in select id from public.conversations loop
    perform public.refresh_conversation_head(r.id);
    n := n + 1;
  end loop;
  raise notice 'Kepala percakapan disegarkan untuk % percakapan.', n;
end $$;
