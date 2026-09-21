-- =============================================================================
-- salesan.id — Migration 0018: reaksi pesan
-- =============================================================================
-- Reaksi menempel pada pesan, bukan berdiri sendiri di percakapan.
--
-- Sebelum ini reaksi masuk sebagai baris pesan bertipe 'reaction' — sehingga
-- setiap jempol dari anggota grup muncul sebagai gelembung tersendiri di dalam
-- thread. Itu bukan yang dilakukan WhatsApp dan bukan yang diharapkan siapa
-- pun yang membacanya.
--
-- Kuncinya (message_id, reactor_jid): satu orang punya paling banyak satu
-- reaksi per pesan. Mengganti jempol dengan hati menimpa, tidak menumpuk —
-- persis seperti di HP.
--
-- Aman dijalankan ulang.
-- =============================================================================

create table if not exists public.message_reactions (
  message_id  uuid not null references public.messages (id) on delete cascade,
  -- Alamat pemberi reaksi, tanpa nomor perangkat.
  reactor_jid text not null,
  emoji       text not null,
  -- Apakah reaksi ini milik akun kita. Disimpan, bukan dihitung ulang saat
  -- dibaca, karena satu grup bisa berisi beberapa nomor kita dan jawabannya
  -- berbeda untuk masing-masing.
  from_me     boolean not null default false,
  reacted_at  timestamptz not null default now(),
  primary key (message_id, reactor_jid)
);

create index if not exists idx_message_reactions_message
  on public.message_reactions (message_id);

comment on table public.message_reactions is
  'Satu baris per (pesan, pemberi reaksi). Reaksi baru dari orang yang sama menimpa yang lama, seperti di WhatsApp.';

-- -----------------------------------------------------------------------------
-- Bersihkan reaksi lama yang terlanjur tersimpan sebagai pesan
--
-- Baris bertipe 'reaction' tidak pernah dimaksudkan tampil sebagai gelembung.
-- Yang punya sasaran dipindahkan ke tabel baru; sisanya dibuang.
-- -----------------------------------------------------------------------------
insert into public.message_reactions (message_id, reactor_jid, emoji, from_me, reacted_at)
select target.id,
       coalesce(r.participant_jid, r.sender_jid, ''),
       r.body,
       r.from_me,
       r.timestamp
  from public.messages r
  join public.messages target
    on target.account_id = r.account_id
   and target.wa_message_id = r.quoted_message_id
 where r.type = 'reaction'
   and r.quoted_message_id is not null
   and coalesce(nullif(r.body, ''), '') <> ''
   and coalesce(r.participant_jid, r.sender_jid, '') <> ''
on conflict (message_id, reactor_jid) do nothing;

delete from public.messages where type = 'reaction';

-- -----------------------------------------------------------------------------
-- Realtime
-- -----------------------------------------------------------------------------
alter table public.message_reactions replica identity full;

do $$
begin
  if exists (select 1 from pg_publication where pubname = 'supabase_realtime') then
    begin
      alter publication supabase_realtime add table public.message_reactions;
    exception when duplicate_object then null; end;
  end if;
end $$;
