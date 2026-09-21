-- =============================================================================
-- 0054 — dibaca berarti tidak lagi menunggu dibalas
--
-- Aturan yang diminta: kalau pesannya sudah dibaca oleh yang memegang akun,
-- chat itu keluar dari "menunggu dibalas". Lencana di inbox adalah daftar
-- kerja, dan chat yang sudah dibuka sudah diambil orang.
--
-- CATATAN PENTING, karena ini bertentangan dengan aturan SLA di aplikasi yang
-- sama: penilaian SLA TIDAK ikut berubah. Di sana membaca tetap tidak
-- menghentikan hitungan, karena kalau membaca menghentikannya, siapa pun bisa
-- menuntaskan kewajiban SLA-nya hanya dengan membuka chat. Dua angka ini
-- sekarang sengaja menjawab dua pertanyaan yang berbeda:
--
--   awaiting_reply  — "apa yang belum saya sentuh", untuk lencana inbox
--   sla waiting     — "siapa yang masih menunggu jawaban", untuk performa
--
-- Definisinya ditulis SEKALI, sebagai fungsi, lalu dipakai oleh trigger kepala
-- percakapan dan trigger pembacaan. Menyalinnya ke dua tempat berarti dua
-- tempat yang bisa berbeda pendapat tentang satu pertanyaan.
--
-- Trigger pembacaan ada karena read_at dicap di tiga jalur berbeda: operator
-- membuka chat di web, status percakapan datang dari HP, dan read receipt
-- WhatsApp. Menambal ketiganya di Go berarti tiga kesempatan untuk lupa; di
-- database, jalur mana pun yang mengubah read_at akan memperbarui angkanya.
--
-- Aman dijalankan ulang.
-- =============================================================================

-- -----------------------------------------------------------------------------
-- Satu-satunya definisi "menunggu dibalas".
--
-- Pesan terbaru yang BERARTI di chat itu: masuk dari pelanggan, atau keluar
-- yang ditulis manusia. Broadcast, story, bot dan sistem dilewati — tidak satu
-- pun darinya menjawab pertanyaan pelanggan.
--
-- Menunggu dibalas jika pesan itu masuk DAN belum dibaca.
-- -----------------------------------------------------------------------------
create or replace function public.conversation_awaiting_reply(p_conversation_id uuid)
returns boolean
language sql
stable
as $$
  select coalesce(
    (select not h.from_me and h.read_at is null
       from public.messages h
      where h.conversation_id = p_conversation_id
        and h.hidden_at is null
        and (h.from_me = false
             or h.sender_source in ('web_admin', 'whatsapp_device'))
      order by h.timestamp desc, h.created_at desc
      limit 1),
    false);
$$;

comment on function public.conversation_awaiting_reply(uuid) is
  'Pelanggan bicara terakhir dan pesannya belum dibaca. Dipakai lencana inbox. Bukan aturan SLA: di sana membaca tidak menghentikan hitungan.';

-- -----------------------------------------------------------------------------
-- Kepala percakapan, memakai definisi di atas.
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

  update public.conversations c
     set awaiting_reply = public.conversation_awaiting_reply(p_conversation_id)
   where c.id = p_conversation_id
     and c.type = 'personal'
     and c.awaiting_reply
         is distinct from public.conversation_awaiting_reply(p_conversation_id);
end;
$$;

-- -----------------------------------------------------------------------------
-- Pembacaan.
--
-- Trigger tingkat STATEMENT, bukan per baris. Membuka satu chat bisa mencap
-- dua ratus pesan sekaligus; trigger per baris akan menghitung ulang dua ratus
-- kali untuk satu jawaban yang sama.
--
-- Kolom yang diawasi disaring di dalam fungsi, bukan lewat "after update of
-- (...)": Postgres menolak transition table pada trigger yang menyebut daftar
-- kolom. Jadi setiap UPDATE pada messages memanggil fungsi ini, dan fungsi ini
-- yang memutuskan apakah ada yang perlu dihitung ulang — perbandingan dua
-- tuplestore, bukan pembacaan tabel.
-- -----------------------------------------------------------------------------
create or replace function public.messages_refresh_awaiting()
returns trigger
language plpgsql
as $$
begin
  update public.conversations c
     set awaiting_reply = w.v,
         updated_at     = now()
    from (
      select distinct n.conversation_id as id
        from changed n
        join before o on o.id = n.id
       where o.read_at is distinct from n.read_at
          or o.hidden_at is distinct from n.hidden_at
    ) ch
    join lateral (select public.conversation_awaiting_reply(ch.id) as v) w on true
   where c.id = ch.id
     and c.type = 'personal'
     and c.awaiting_reply is distinct from w.v;
  return null;
end;
$$;

drop trigger if exists trg_messages_awaiting on public.messages;
create trigger trg_messages_awaiting
  after update on public.messages
  referencing old table as before new table as changed
  for each statement execute function public.messages_refresh_awaiting();

-- -----------------------------------------------------------------------------
-- Isi ulang dengan aturan baru.
-- -----------------------------------------------------------------------------
update public.conversations c
   set awaiting_reply = public.conversation_awaiting_reply(c.id)
 where c.type = 'personal'
   and c.awaiting_reply is distinct from public.conversation_awaiting_reply(c.id);

comment on column public.conversations.awaiting_reply is
  'Pelanggan bicara terakhir dan pesannya belum dibaca. Angka untuk lencana inbox: chat pribadi saja, dan broadcast/story/bot/sistem tidak mematikannya.';
