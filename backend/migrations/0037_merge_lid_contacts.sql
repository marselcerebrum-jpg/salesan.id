-- =============================================================================
-- salesan.id — Migration 0037: satu orang, satu kontak
-- =============================================================================
-- Persoalan yang sama dengan migrasi 0013, tetapi di tabel yang berbeda dan
-- belum pernah disentuh.
--
-- WhatsApp mengalamati orang yang sama dengan dua cara, nomor telepon
-- (628…@s.whatsapp.net) dan LID (1680…@lid). Tabel contacts unik pada
-- (account_id, jid), sementara satu orang punya dua jid. Jadi satu orang
-- menjadi dua baris, dan buku alamat penuh kembaran.
--
-- Lebih buruk lagi, kedua baris itu tidak bisa dipertemukan sama sekali:
-- phone_number diisi oleh phoneFromJID(), yang mengembalikan string kosong
-- untuk LID. Baris ber-LID karena itu selalu punya phone_number NULL dan tidak
-- punya apa pun yang menghubungkannya ke baris nomornya.
--
-- Solusinya persis seperti 0013: jangan mengganti alamat baris yang sudah ada,
-- karena alamat itulah yang dipakai WhatsApp. Yang ditambahkan adalah alamat
-- keduanya. phone_number jadi identitas pemersatu, lid_jid menyimpan bentuk
-- satunya sehingga pencarian dari arah mana pun menemukan baris yang sama.
--
-- Aman dijalankan ulang.
-- =============================================================================

begin;

alter table public.contacts
  add column if not exists lid_jid text;

comment on column public.contacts.lid_jid is
  'Bentuk LID dari kontak ini. Disimpan berdampingan dengan jid supaya pencarian dari nomor maupun dari LID menemukan baris yang sama.';

-- -----------------------------------------------------------------------------
-- Lengkapi kedua alamat
-- -----------------------------------------------------------------------------

-- Baris yang memang beralamat nomor: nomornya adalah dirinya sendiri.
update public.contacts
   set phone_number = split_part(jid, '@', 1)
 where phone_number is null
   and jid like '%@s.whatsapp.net';

-- Baris ber-LID: ambil nomornya dari peta milik whatsmeow, dan catat LID-nya
-- di kolomnya sendiri.
update public.contacts c
   set phone_number = lm.pn,
       lid_jid      = c.jid
  from whatsmeow_lid_map lm
 where c.jid = lm.lid || '@lid'
   and (c.phone_number is null or c.lid_jid is null);

-- Baris beralamat nomor yang LID-nya diketahui: catat juga.
update public.contacts c
   set lid_jid = lm.lid || '@lid'
  from whatsmeow_lid_map lm
 where c.lid_jid is null
   and c.phone_number = lm.pn;

-- -----------------------------------------------------------------------------
-- Gabungkan yang kembar
--
-- Pemenangnya baris yang paling banyak dipakai, karena itulah kontak yang
-- sungguh dirujuk oleh data lain. Bila seri, yang beralamat nomor menang:
-- nomor adalah bentuk yang bisa dibaca manusia dan yang dipakai broadcast.
-- -----------------------------------------------------------------------------
do $$
declare
  grp    record;
  winner uuid;
  loser  uuid;
begin
  for grp in
    select account_id, phone_number
      from public.contacts
     where phone_number is not null and phone_number <> ''
     group by account_id, phone_number
    having count(*) > 1
  loop
    select c.id into winner
      from public.contacts c
     where c.account_id = grp.account_id and c.phone_number = grp.phone_number
     order by (select count(*) from public.conversations v where v.contact_id = c.id) desc,
              (c.jid like '%@s.whatsapp.net') desc,
              c.created_at asc
     limit 1;

    for loser in
      select c.id from public.contacts c
       where c.account_id = grp.account_id
         and c.phone_number = grp.phone_number
         and c.id <> winner
    loop
      -- Nama sering hanya ada di salah satu baris. Ambil apa pun yang
      -- dimiliki yang kalah dan belum dimiliki pemenang, termasuk alamat
      -- keduanya.
      update public.contacts w
         set name          = coalesce(w.name, l.name),
             push_name     = coalesce(w.push_name, l.push_name),
             business_name = coalesce(w.business_name, l.business_name),
             avatar_url    = coalesce(w.avatar_url, l.avatar_url),
             is_business   = w.is_business or l.is_business,
             is_blocked    = w.is_blocked or l.is_blocked,
             lid_jid       = coalesce(w.lid_jid,
                                      nullif(l.lid_jid, ''),
                                      case when l.jid like '%@lid' then l.jid end)
        from public.contacts l
       where w.id = winner and l.id = loser;

      -- Rujukan biasa: dipindahkan apa adanya.
      update public.conversations        set contact_id = winner where contact_id = loser;
      update public.conversation_members set contact_id = winner where contact_id = loser;
      update public.sla_cycles           set contact_id = winner where contact_id = loser;
      update public.follow_up_events     set contact_id = winner where contact_id = loser;
      update public.contact_label_events set contact_id = winner where contact_id = loser;
      update public.campaign_targets     set contact_id = winner where contact_id = loser;

      -- Tiga tabel berikut memakai contact_id sebagai primary key, jadi satu
      -- kontak hanya boleh punya satu baris. Yang kalah dipindahkan hanya bila
      -- pemenang belum punya; kalau sudah, catatan pemenang yang dipertahankan
      -- dan yang kalah dibuang.
      update public.contact_first_seen t
         set contact_id = winner
       where t.contact_id = loser
         and not exists (select 1 from public.contact_first_seen w where w.contact_id = winner);
      delete from public.contact_first_seen where contact_id = loser;

      update public.lead_classifications t
         set contact_id = winner
       where t.contact_id = loser
         and not exists (select 1 from public.lead_classifications w where w.contact_id = winner);
      delete from public.lead_classifications where contact_id = loser;

      update public.contact_label_state t
         set contact_id = winner
       where t.contact_id = loser
         and not exists (select 1 from public.contact_label_state w where w.contact_id = winner);
      delete from public.contact_label_state where contact_id = loser;

      delete from public.contacts where id = loser;
    end loop;
  end loop;
end $$;

-- -----------------------------------------------------------------------------
-- Dijaga oleh basis data, bukan oleh niat baik
-- -----------------------------------------------------------------------------
create unique index if not exists uq_contacts_account_phone
  on public.contacts (account_id, phone_number)
  where phone_number is not null and phone_number <> '';

create index if not exists idx_contacts_lid
  on public.contacts (account_id, lid_jid)
  where lid_jid is not null;

commit;
