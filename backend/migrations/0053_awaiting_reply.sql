-- =============================================================================
-- 0053 — percakapan yang benar-benar belum terjawab
--
-- Dua lencana di antarmuka menghitung dua hal yang berbeda, dan tidak satu pun
-- menghitung yang dimaksud orang saat melihatnya:
--
--   * Angka di baris nomor adalah JUMLAH SELURUH PERCAKAPAN pada nomor itu.
--     Berwarna peringatan, jadi terbaca seperti daftar tugas, padahal ia tidak
--     berkurang walau semua chat sudah dibalas.
--   * Angka di menu Chat WhatsApp adalah unread_count WhatsApp: pesan yang
--     belum dibuka. Membaca bukan menjawab, dan pesan yang sudah dibuka tapi
--     belum dibalas hilang dari angka itu.
--
-- awaiting_reply menjawab pertanyaan yang sebenarnya: pelanggan bicara
-- terakhir, dan belum ada manusia yang membalas.
--
-- Disimpan sebagai kolom, bukan dihitung saat dibaca. Menghitungnya dengan
-- menyisir pesan tiap kali daftar nomor dibuka berarti satu pemindaian penuh
-- per percakapan, dan workspace ini dirancang untuk kontak yang sangat banyak.
-- Sebagai kolom ia dirawat oleh trigger yang sudah ada dan dihitung lewat satu
-- indeks parsial.
--
-- Kenapa tidak memakai last_message_direction yang sudah ada: kolom itu
-- mengikuti pesan terbaru apa pun, termasuk broadcast dan story. Sebuah
-- broadcast yang mendarat di chat yang menunggu akan menandainya sebagai sudah
-- terjawab, padahal tidak ada yang menjawab. Kepala percakapan memang harus
-- menampilkan broadcast itu di inbox — jadi kolomnya dipisah, bukan
-- disaring.
--
-- Chat pribadi saja, sama seperti SLA dan "Masih Menunggu Balasan" di Performa.
-- Grup tidak punya satu pelanggan yang menunggu dijawab, dan angka yang
-- mencampurkan keduanya akan berbeda dengan angka Performa untuk pertanyaan
-- yang sama.
--
-- Aman dijalankan ulang.
-- =============================================================================

alter table public.conversations
  add column if not exists awaiting_reply boolean not null default false;

comment on column public.conversations.awaiting_reply is
  'Pelanggan bicara terakhir dan belum dibalas manusia. Chat pribadi saja. Broadcast, story, bot dan notifikasi sistem tidak menjawab siapa pun, jadi tidak mematikannya.';

-- Parsial: yang dihitung selalu yang bernilai true, dan indeks penuh atas kolom
-- boolean pada tabel sebesar ini hanya menambah beban tulis tanpa mempercepat
-- pembacaan itu.
create index if not exists idx_conversations_awaiting
  on public.conversations (account_id) where awaiting_reply;

-- -----------------------------------------------------------------------------
-- Dirawat oleh fungsi yang sama yang merawat kepala percakapan, supaya keduanya
-- tidak mungkin dihitung dari dua pembacaan yang berbeda.
--
-- Pesan terbaru yang BERARTI: masuk dari pelanggan, atau keluar yang ditulis
-- manusia (web atau HP). Broadcast, story, bot dan sistem dilewati — bukan
-- karena tidak penting, tetapi karena tidak satu pun darinya menjawab
-- pertanyaan pelanggan.
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
     set awaiting_reply = coalesce(
           (select not h.from_me
              from public.messages h
             where h.conversation_id = p_conversation_id
               and h.hidden_at is null
               and (h.from_me = false
                    or h.sender_source in ('web_admin', 'whatsapp_device'))
             order by h.timestamp desc, h.created_at desc
             limit 1),
           false)
   where c.id = p_conversation_id
     and c.type = 'personal';
end;
$$;

comment on function public.refresh_conversation_head(uuid) is
  'Recomputes a conversation head and its awaiting_reply flag from its newest messages; safe to call repeatedly.';

-- -----------------------------------------------------------------------------
-- Isi awal untuk percakapan yang sudah ada.
-- -----------------------------------------------------------------------------
update public.conversations c
   set awaiting_reply = coalesce(
         (select not h.from_me
            from public.messages h
           where h.conversation_id = c.id
             and h.hidden_at is null
             and (h.from_me = false
                  or h.sender_source in ('web_admin', 'whatsapp_device'))
           order by h.timestamp desc, h.created_at desc
           limit 1),
         false)
 where c.type = 'personal';
