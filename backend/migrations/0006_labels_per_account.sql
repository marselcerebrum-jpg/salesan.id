-- =============================================================================
-- salesan.id — Migration 0006: label berbasis identitas WhatsApp, per akun
-- =============================================================================
-- Sebelum ini label bersifat per-workspace dan dicocokkan berdasarkan NAMA,
-- dengan tabel pemetaan terpisah (whatsapp_label_map) yang menautkan id label
-- WhatsApp ke baris label kita. Desain itu salah untuk dua alasan:
--
--   1. Nama bukan identitas. WhatsApp mengidentifikasi label dengan angka;
--      mengganti nama label di HP akan terlihat seperti label baru, dan dua
--      label berbeda yang kebetulan senama akan bertabrakan.
--   2. Dua nomor WhatsApp yang sama-sama punya label "Premium" berbagi satu
--      baris, sehingga mengubahnya di satu akun ikut mengubah akun lain.
--
-- Sesudah migrasi ini identitasnya adalah (account_id, wa_label_id), dan setiap
-- akun memiliki himpunan labelnya sendiri.
--
-- Label buatan sendiri (source = 'manual') tetap boleh tanpa akun: ia milik
-- workspace sampai dipasang ke chat, saat itu barulah didorong ke WhatsApp dan
-- memperoleh wa_label_id.
--
-- Aman dijalankan ulang.
-- =============================================================================

-- -----------------------------------------------------------------------------
-- Kolom identitas dan metadata konflik
-- -----------------------------------------------------------------------------
alter table public.conversation_labels
  add column if not exists account_id    uuid references public.whatsapp_accounts (id) on delete cascade,
  add column if not exists wa_label_id   text,
  -- Indeks palet WhatsApp. Warna hex kita diturunkan dari sini; menyimpan
  -- angkanya membuat perjalanan bolak-balik ke HP tidak kehilangan informasi.
  add column if not exists color_index   int,
  -- Stempel waktu mutasi app-state terakhir yang membentuk baris ini.
  -- Dipakai sebagai penentu konflik: mutasi WhatsApp yang lebih baru menang.
  add column if not exists wa_updated_at timestamptz,
  -- Nisan. Label yang dihapus di HP tidak langsung dibuang supaya event lama
  -- yang datang terlambat tidak menghidupkannya kembali.
  add column if not exists deleted_at    timestamptz;

-- -----------------------------------------------------------------------------
-- Pindahkan data dari whatsapp_label_map
--
-- Satu baris label bisa terpetakan ke beberapa akun. Baris pertama mewarisi
-- label yang ada; sisanya digandakan supaya tiap akun punya salinannya sendiri,
-- lalu penugasan label pada percakapan diarahkan ke salinan yang benar.
-- -----------------------------------------------------------------------------
do $$
declare
  m         record;
  claimed   boolean;
  new_id    uuid;
begin
  if not exists (
    select 1 from information_schema.tables
     where table_schema = 'public' and table_name = 'whatsapp_label_map'
  ) then
    raise notice 'whatsapp_label_map sudah tidak ada — lewati pemindahan.';
    return;
  end if;

  for m in
    select map.account_id, map.wa_label_id, map.label_id,
           l.workspace_id, l.name, l.color, l.sort_order, l.source
      from public.whatsapp_label_map map
      join public.conversation_labels l on l.id = map.label_id
     order by map.label_id, map.account_id
  loop
    select (account_id is null) into claimed
      from public.conversation_labels
     where id = m.label_id;

    if claimed then
      -- Baris aslinya belum dimiliki akun mana pun: jadikan milik akun ini.
      update public.conversation_labels
         set account_id  = m.account_id,
             wa_label_id = m.wa_label_id,
             source      = 'whatsapp'
       where id = m.label_id;
    else
      -- Sudah dimiliki akun lain: gandakan untuk akun ini.
      insert into public.conversation_labels
        (workspace_id, account_id, wa_label_id, name, color, sort_order, source)
      values
        (m.workspace_id, m.account_id, m.wa_label_id, m.name, m.color, m.sort_order, 'whatsapp')
      on conflict do nothing
      returning id into new_id;

      if new_id is not null then
        -- Arahkan penugasan pada percakapan milik akun ini ke salinan barunya.
        update public.conversation_label_assignments a
           set label_id = new_id
          from public.conversations c
         where a.conversation_id = c.id
           and c.account_id = m.account_id
           and a.label_id = m.label_id;
      end if;
    end if;
  end loop;
end $$;

drop table if exists public.whatsapp_label_map;

-- -----------------------------------------------------------------------------
-- Batasan keunikan
--
-- Identitas label WhatsApp adalah (account_id, wa_label_id). Label manual tanpa
-- akun tetap unik per nama di dalam workspace, supaya UI tidak menampilkan dua
-- tag yang tak bisa dibedakan.
-- -----------------------------------------------------------------------------
alter table public.conversation_labels
  drop constraint if exists uq_conversation_labels_name;

drop index if exists uq_conversation_labels_wa;
create unique index if not exists uq_conversation_labels_wa
  on public.conversation_labels (account_id, wa_label_id)
  where wa_label_id is not null;

drop index if exists uq_conversation_labels_manual_name;
create unique index if not exists uq_conversation_labels_manual_name
  on public.conversation_labels (workspace_id, lower(name))
  where account_id is null and deleted_at is null;

create index if not exists idx_conversation_labels_account
  on public.conversation_labels (account_id)
  where deleted_at is null;

-- -----------------------------------------------------------------------------
-- Pembukuan sinkronisasi label per akun
-- -----------------------------------------------------------------------------
alter table public.whatsapp_accounts
  add column if not exists labels_synced_at timestamptz,
  -- 'idle' | 'syncing' | 'synced' | 'failed' — dipakai indikator status di UI.
  add column if not exists label_sync_state text not null default 'idle',
  add column if not exists label_sync_error text;

do $$ begin
  alter table public.whatsapp_accounts
    add constraint whatsapp_accounts_label_sync_state_check
    check (label_sync_state in ('idle', 'syncing', 'synced', 'failed'));
exception when duplicate_object then null; end $$;

-- -----------------------------------------------------------------------------
-- Jejak mutasi label, untuk idempotensi dan pencegahan loop
--
-- Setiap mutasi app-state yang kita terapkan dicatat di sini. Event yang sama
-- terkirim ulang — hal biasa saat reconnect atau pemulihan snapshot — akan
-- ditolak, dan gema dari perubahan yang kita kirim sendiri tidak diproses dua
-- kali.
-- -----------------------------------------------------------------------------
create table if not exists public.whatsapp_label_events (
  account_id    uuid not null references public.whatsapp_accounts (id) on delete cascade,
  -- Kunci idempotensi: jenis mutasi + sasarannya + stempel waktunya.
  event_key     text not null,
  applied_at    timestamptz not null default now(),
  primary key (account_id, event_key)
);

create index if not exists idx_whatsapp_label_events_applied
  on public.whatsapp_label_events (applied_at);

alter table public.whatsapp_label_events enable row level security;

drop policy if exists whatsapp_label_events_rw on public.whatsapp_label_events;
create policy whatsapp_label_events_rw on public.whatsapp_label_events
  for all to authenticated
  using (exists (
    select 1 from public.whatsapp_accounts a
     where a.id = whatsapp_label_events.account_id
       and a.workspace_id = public.current_workspace_id()))
  with check (exists (
    select 1 from public.whatsapp_accounts a
     where a.id = whatsapp_label_events.account_id
       and a.workspace_id = public.current_workspace_id()));
