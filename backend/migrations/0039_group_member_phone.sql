-- -----------------------------------------------------------------------------
-- Simpan nomor asli peserta grup
--
-- WhatsApp kini mengalamati peserta grup dengan LID ("<lid>@lid"), bukan nomor.
-- LID bukan nomor: tidak bisa dihubungi, tidak bisa diekspor, dan tidak berarti
-- apa-apa bagi manusia. Selama ini kolom nomor di layar terisi potongan LID
-- karena itulah satu-satunya yang tersimpan.
--
-- Padahal WhatsApp sudah memberi tahu nomornya: setiap GroupParticipant membawa
-- PhoneNumber di samping JID-nya, dan selama ini dibuang begitu saja. Kolom ini
-- menampungnya, sehingga peserta yang belum pernah jadi kontak pun tetap punya
-- nomor yang benar.
--
-- Nullable dengan sengaja: untuk peserta anonim di announcement group, WhatsApp
-- memang tidak mengungkapkan nomornya. Kosong adalah jawaban yang jujur.
-- -----------------------------------------------------------------------------

alter table public.conversation_members
  add column if not exists phone_number text;

comment on column public.conversation_members.phone_number is
  'Nomor asli peserta menurut WhatsApp (GroupParticipant.PhoneNumber), tanpa sufiks server. NULL bila WhatsApp tidak mengungkapkannya.';

-- Isi yang sudah bisa diisi sekarang dari peta LID milik whatsmeow, supaya baris
-- lama tidak perlu menunggu refresh grup berikutnya.
update public.conversation_members mem
   set phone_number = lm.pn
  from whatsmeow_lid_map lm
 where mem.phone_number is null
   and mem.jid = lm.lid || '@lid';

-- Peserta yang memang beralamat nomor: nomornya adalah dirinya sendiri.
update public.conversation_members
   set phone_number = split_part(jid, '@', 1)
 where phone_number is null
   and jid like '%@s.whatsapp.net';

create index if not exists idx_conversation_members_phone
  on public.conversation_members (phone_number)
  where phone_number is not null;
