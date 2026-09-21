-- =============================================================================
-- salesan.id — Migration 0013: satu kontak, satu percakapan
-- =============================================================================
-- WhatsApp mengalamati orang yang sama dengan dua cara: nomor telepon
-- (628…@s.whatsapp.net) dan LID (1680…@lid). Selama ini keduanya menjadi baris
-- percakapan yang berbeda, sehingga "Thaariq" muncul dua kali — satu berisi 21
-- pesan, satu kosong.
--
-- whatsmeow sudah menyimpan peta antara keduanya di whatsmeow_lid_map. Yang
-- kurang hanyalah memakainya.
--
-- Solusinya bukan mengganti alamat percakapan. Alamat yang dipakai WhatsApp
-- untuk mengirim dan menerima harus tetap apa adanya, kalau tidak pesan
-- berikutnya justru membuat baris ketiga. Yang ditambahkan adalah pn_jid:
-- bentuk nomor telepon dari chat yang sama, dipakai sebagai identitas
-- pemersatu. Dua baris dengan pn_jid sama adalah orang yang sama.
--
-- Aman dijalankan ulang.
-- =============================================================================

alter table public.conversations
  add column if not exists pn_jid text;

comment on column public.conversations.pn_jid is
  'Bentuk nomor telepon dari chat ini. Sama untuk baris @lid dan @s.whatsapp.net milik orang yang sama, sehingga keduanya tidak lagi terpisah.';

-- -----------------------------------------------------------------------------
-- Isi pn_jid
-- -----------------------------------------------------------------------------

-- Chat yang memang sudah beralamat nomor telepon: dirinya sendiri.
update public.conversations
   set pn_jid = chat_jid
 where pn_jid is null
   and chat_jid like '%@s.whatsapp.net';

-- Chat ber-LID: ambil dari peta milik whatsmeow.
update public.conversations c
   set pn_jid = lm.pn || '@s.whatsapp.net'
  from whatsmeow_lid_map lm
 where c.pn_jid is null
   and c.chat_jid = lm.lid || '@lid';

-- -----------------------------------------------------------------------------
-- Gabungkan yang kembar
--
-- Pemenangnya adalah baris dengan pesan terbanyak — itulah percakapan yang
-- sungguh dipakai. Bila seri, yang ber-LID menang, karena itu alamat yang
-- sedang dipakai WhatsApp untuk thread tersebut.
-- -----------------------------------------------------------------------------
do $$
declare
  grp    record;
  winner uuid;
  loser  uuid;
begin
  for grp in
    select account_id, pn_jid
      from public.conversations
     where pn_jid is not null
     group by account_id, pn_jid
    having count(*) > 1
  loop
    select c.id into winner
      from public.conversations c
     where c.account_id = grp.account_id and c.pn_jid = grp.pn_jid
     order by (select count(*) from public.messages m where m.conversation_id = c.id) desc,
              (c.chat_jid like '%@lid') desc,
              c.created_at asc
     limit 1;

    for loser in
      select c.id from public.conversations c
       where c.account_id = grp.account_id and c.pn_jid = grp.pn_jid and c.id <> winner
    loop
      -- Nama dan kontak sering hanya ada di baris buku alamat, sementara
      -- pesannya ada di baris satunya. Ambil yang tidak dimiliki pemenang.
      update public.conversations w
         set name       = coalesce(w.name, l.name),
             contact_id = coalesce(w.contact_id, l.contact_id),
             avatar_url = coalesce(w.avatar_url, l.avatar_url),
             wa_conversation_at = greatest(
               coalesce(w.wa_conversation_at, '-infinity'::timestamptz),
               coalesce(l.wa_conversation_at, '-infinity'::timestamptz)),
             is_pinned  = w.is_pinned or l.is_pinned,
             unread_count = greatest(w.unread_count, l.unread_count),
             marked_unread = w.marked_unread or l.marked_unread
        from public.conversations l
       where w.id = winner and l.id = loser;

      -- Lepas tautan kepala percakapan dulu, supaya foreign key-nya tidak
      -- menahan penghapusan baris yang kalah.
      update public.conversations set last_message_id = null where id = loser;

      update public.messages set conversation_id = winner where conversation_id = loser;

      -- Label dan anggota grup bisa bentrok bila keduanya sudah punya baris
      -- yang sama; yang bentrok cukup dibuang.
      update public.conversation_label_assignments a
         set conversation_id = winner
       where a.conversation_id = loser
         and not exists (
           select 1 from public.conversation_label_assignments b
            where b.conversation_id = winner and b.label_id = a.label_id);
      delete from public.conversation_label_assignments where conversation_id = loser;

      update public.conversation_members m
         set conversation_id = winner
       where m.conversation_id = loser
         and not exists (
           select 1 from public.conversation_members n
            where n.conversation_id = winner and n.jid = m.jid);
      delete from public.conversation_members where conversation_id = loser;

      delete from public.conversations where id = loser;
    end loop;

    perform public.refresh_conversation_head(winner);
  end loop;
end $$;

-- -----------------------------------------------------------------------------
-- Satu orang, satu baris — dijaga oleh basis data, bukan oleh niat baik
-- -----------------------------------------------------------------------------
create unique index if not exists uq_conversations_pn
  on public.conversations (account_id, pn_jid)
  where pn_jid is not null;

create index if not exists idx_conversations_pn_lookup
  on public.conversations (account_id, pn_jid);

-- -----------------------------------------------------------------------------
-- Isi nama yang kosong pada chat ber-LID
--
-- Sebelum ini, chat ber-LID tampil tanpa nama karena kontaknya tersimpan di
-- bawah nomor telepon. Sekarang keduanya bisa dipertemukan.
-- -----------------------------------------------------------------------------
update public.conversations c
   set name       = coalesce(c.name, nullif(ct.name, ''), nullif(ct.push_name, '')),
       contact_id = coalesce(c.contact_id, ct.id)
  from public.contacts ct
 where c.pn_jid is not null
   and ct.account_id = c.account_id
   and ct.jid = c.pn_jid
   and (c.name is null or c.contact_id is null);
