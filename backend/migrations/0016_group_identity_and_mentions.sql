-- =============================================================================
-- salesan.id — Migration 0016: identitas anggota grup + mention
-- =============================================================================
-- Dua hal yang saling berkaitan, karena keduanya bertumpu pada satu pertanyaan
-- yang selama ini tidak pernah dijawab dengan benar: siapa pengirim pesan ini,
-- dan apakah pesan ini menyebut kita.
--
-- IDENTITAS ANGGOTA
--   participant_jid dan sender_phone disimpan eksplisit pada setiap pesan.
--   Nama pengirim TIDAK dibekukan di baris pesan — nama disusun saat dibaca,
--   dengan urutan: nama kontak tersimpan → push name → nama saat pesan tiba →
--   nomor telepon. Itulah yang membuat penggantian nama kontak di HP langsung
--   terlihat pada pesan-pesan lama, bukan hanya pada yang baru.
--
-- MENTION
--   mentioned_jids berisi daftar yang disebut menurut metadata WhatsApp, bukan
--   hasil menebak dari teks. mentions_me adalah kesimpulannya untuk akun ini —
--   dihitung sekali saat pesan masuk, karena satu grup bisa berisi beberapa
--   nomor yang sama-sama terhubung ke web app, dan jawabannya berbeda untuk
--   masing-masing.
--
--   mention_seen_at memisahkan "sudah dibuka" dari "sudah dibaca": sebuah grup
--   bisa saja seluruh pesannya terbaca sementara mention-nya belum ditengok.
--
-- Aman dijalankan ulang.
-- =============================================================================

alter table public.messages
  add column if not exists participant_jid  text,
  add column if not exists sender_phone     text,
  add column if not exists mentioned_jids   text[],
  add column if not exists mentions_me      boolean not null default false,
  add column if not exists mention_seen_at  timestamptz;

comment on column public.messages.participant_jid is
  'Alamat anggota yang menulis pesan ini. Sama dengan sender_jid, disimpan terpisah supaya makna "anggota grup" tidak bergantung pada kolom yang juga dipakai chat pribadi.';
comment on column public.messages.mentions_me is
  'Benar bila salah satu mentioned_jids adalah akun penerima pesan ini. Dihitung per akun, karena satu grup bisa berisi beberapa nomor kita.';
comment on column public.messages.mention_seen_at is
  'Kapan mention-nya ditengok. Berbeda dari read_at: satu grup bisa terbaca seluruhnya tanpa mention-nya pernah dibuka.';

-- Isi participant_jid dari data yang sudah ada, supaya riwayat ikut benar.
update public.messages
   set participant_jid = sender_jid
 where participant_jid is null and sender_jid is not null;

-- -----------------------------------------------------------------------------
-- Penghitung mention pada percakapan
--
-- Terpisah dari unread_count. Sebuah grup ramai bisa punya 200 pesan belum
-- dibaca dan nol mention, atau nol belum dibaca dan satu mention yang justru
-- paling perlu dilihat.
-- -----------------------------------------------------------------------------
alter table public.conversations
  add column if not exists mention_count int not null default 0;

-- -----------------------------------------------------------------------------
-- Indeks
-- -----------------------------------------------------------------------------

-- Menyusun nama pengirim saat membaca berarti menggabungkan pesan dengan
-- kontak pada setiap halaman; ini yang membuatnya murah.
create index if not exists idx_contacts_account_jid
  on public.contacts (account_id, jid);

-- Mencari mention yang belum ditengok, per percakapan.
create index if not exists idx_messages_unseen_mentions
  on public.messages (conversation_id, timestamp)
  where mentions_me and mention_seen_at is null;

-- Filter "Mention" pada daftar chat.
create index if not exists idx_conversations_with_mentions
  on public.conversations (account_id)
  where mention_count > 0;

-- -----------------------------------------------------------------------------
-- Hitung ulang mention_count dari pesan
--
-- Satu fungsi, dipanggil di mana pun mention berubah — pesan masuk, mention
-- ditengok, pesan dihapus. Menaikkan penghitung dari beberapa tempat berbeda
-- adalah cara paling cepat membuatnya salah.
-- -----------------------------------------------------------------------------
create or replace function public.refresh_mention_count(p_conversation_id uuid)
returns void
language sql
as $$
  update public.conversations c
     set mention_count = sub.n,
         updated_at    = now()
    from (
      select count(*) as n
        from public.messages m
       where m.conversation_id = p_conversation_id
         and m.mentions_me
         and m.mention_seen_at is null
         and m.hidden_at is null
         and m.revoked_at is null
    ) sub
   where c.id = p_conversation_id
     and c.mention_count is distinct from sub.n;
$$;

comment on function public.refresh_mention_count(uuid) is
  'Recomputes a conversation''s unseen-mention count from its messages; safe to call repeatedly.';

-- -----------------------------------------------------------------------------
-- Realtime
-- -----------------------------------------------------------------------------
do $$
begin
  if exists (select 1 from pg_publication where pubname = 'supabase_realtime') then
    begin
      alter publication supabase_realtime add table public.conversation_members;
    exception when duplicate_object then null; end;
  end if;
end $$;
