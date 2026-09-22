-- =============================================================================
-- salesan.id — Migration 0062: satu orang, satu baris kontak per nomor
-- =============================================================================
-- WhatsApp memanggil orang yang sama dengan dua alamat: nomor telepon
-- (@s.whatsapp.net) dan LID (@lid). Sinkron bisa berkenalan lewat LID dulu,
-- dari sebuah grup, saat nomornya belum diketahui. Baris kontak lahir tanpa
-- phone_number. Belakangan orang yang sama muncul lewat nomornya, baris kedua
-- lahir, dan satu orang jadi dua kontak di satu nomor HP.
--
-- Ketika sinkron akhirnya tahu nomor si baris LID, ia mencoba mengisinya dan
-- ditolak `uq_contacts_account_phone` karena nomor itu sudah dipakai baris
-- kedua. Kontaknya lalu dilewati begitu saja — terlihat di log sebagai
-- "duplicate key value violates unique constraint" berulang-ulang setiap
-- sinkron. Jadi bukan hanya ganda: kontak yang seharusnya diperbarui malah
-- hilang.
--
-- Migration ini memberi satu cara resmi untuk menggabungkan dua baris kontak
-- yang ternyata orang yang sama, lalu memakainya untuk membereskan yang sudah
-- terlanjur ada. Backend memanggil fungsi yang sama saat sinkron menemukan
-- tabrakan, sehingga ganda tidak lahir lagi.
--
-- Penggabungan tidak membuang riwayat: setiap baris yang menunjuk ke kontak
-- duplikat dipindahkan ke kontak yang dipertahankan. Yang dihapus hanya baris
-- kontak duplikatnya sendiri, setelah kosong.
--
-- Aman dijalankan ulang.
-- =============================================================================

create or replace function public.merge_contact_rows(keep_id uuid, drop_id uuid)
returns void
language plpgsql
as $fn$
begin
  if keep_id is null or drop_id is null or keep_id = drop_id then
    return;
  end if;

  -- --- tabel dengan satu baris per kontak -----------------------------------
  --
  -- contact_id adalah primary key di sini, jadi baris tidak bisa sekadar
  -- dipindahkan kalau yang dipertahankan sudah punya. Yang dipertahankan
  -- menang, tetapi angka yang hanya bisa mengecil diambil yang paling awal:
  -- "pertama kali terlihat" yang mundur karena penggabungan akan memalsukan
  -- umur kontak dan ikut menggeser laporan yang menghitungnya.
  update public.contact_first_seen k
     set first_seen_at   = least(k.first_seen_at, d.first_seen_at),
         first_inbound_at = least(k.first_inbound_at, d.first_inbound_at),
         updated_at      = now()
    from public.contact_first_seen d
   where k.contact_id = keep_id and d.contact_id = drop_id;
  delete from public.contact_first_seen
   where contact_id = drop_id
     and exists (select 1 from public.contact_first_seen k where k.contact_id = keep_id);
  update public.contact_first_seen set contact_id = keep_id where contact_id = drop_id;

  update public.contact_label_state k
     set first_labeled_at = least(k.first_labeled_at, d.first_labeled_at),
         last_changed_at  = greatest(k.last_changed_at, d.last_changed_at),
         change_count     = k.change_count + d.change_count,
         updated_at       = now()
    from public.contact_label_state d
   where k.contact_id = keep_id and d.contact_id = drop_id;
  delete from public.contact_label_state
   where contact_id = drop_id
     and exists (select 1 from public.contact_label_state k where k.contact_id = keep_id);
  update public.contact_label_state set contact_id = keep_id where contact_id = drop_id;

  delete from public.lead_classifications
   where contact_id = drop_id
     and exists (select 1 from public.lead_classifications k where k.contact_id = keep_id);
  update public.lead_classifications set contact_id = keep_id where contact_id = drop_id;

  -- --- tabel dengan banyak baris per kontak ---------------------------------
  --
  -- Tidak ada keunikan atas contact_id di sini, jadi cukup dipindahkan.
  update public.contact_label_events set contact_id = keep_id where contact_id = drop_id;
  update public.conversations         set contact_id = keep_id where contact_id = drop_id;
  update public.conversation_members  set contact_id = keep_id where contact_id = drop_id;
  update public.campaign_targets      set contact_id = keep_id where contact_id = drop_id;
  update public.sla_cycles            set contact_id = keep_id where contact_id = drop_id;
  update public.follow_up_events      set contact_id = keep_id where contact_id = drop_id;

  -- --- kontak yang dipertahankan mewarisi yang belum dimilikinya ------------
  update public.contacts k
     set lid_jid       = coalesce(k.lid_jid,
                                  case when d.jid like '%@lid' then d.jid else d.lid_jid end),
         phone_number  = coalesce(nullif(k.phone_number, ''), nullif(d.phone_number, '')),
         name          = coalesce(nullif(k.name, ''), nullif(d.name, '')),
         push_name     = coalesce(nullif(k.push_name, ''), nullif(d.push_name, '')),
         business_name = coalesce(nullif(k.business_name, ''), nullif(d.business_name, '')),
         is_business   = k.is_business or d.is_business,
         updated_at    = now()
    from public.contacts d
   where k.id = keep_id and d.id = drop_id;

  delete from public.contacts where id = drop_id;
end;
$fn$;

comment on function public.merge_contact_rows(uuid, uuid) is
  'Menggabungkan dua baris kontak yang ternyata orang yang sama pada satu nomor. Riwayat dipindahkan, baris duplikat dihapus setelah kosong.';

-- --- membereskan yang sudah terlanjur ada ------------------------------------
--
-- Pasangannya dibuktikan lewat whatsmeow_lid_map, peta resmi WhatsApp dari LID
-- ke nomor telepon, bukan ditebak dari kemiripan nama. Tabel itu milik
-- whatsmeow dan baru ada setelah perangkat pertama tersambung, jadi keberadaan
-- tabelnya diperiksa dulu: pada basis data baru tidak ada yang perlu
-- digabungkan.
do $backfill$
declare
  pair record;
  merged int := 0;
begin
  if to_regclass('public.whatsmeow_lid_map') is null then
    raise notice 'whatsmeow_lid_map belum ada; tidak ada kontak lama untuk digabungkan';
    return;
  end if;

  for pair in
    select lidc.id as drop_id, pnc.id as keep_id
      from public.contacts lidc
      join public.whatsmeow_lid_map m
        on m.lid = split_part(lidc.jid, '@', 1)
      join public.contacts pnc
        on pnc.account_id = lidc.account_id
       and pnc.phone_number = split_part(m.pn, '@', 1)
       and pnc.id <> lidc.id
     where lidc.jid like '%@lid'
       and coalesce(lidc.phone_number, '') = ''
  loop
    perform public.merge_contact_rows(pair.keep_id, pair.drop_id);
    merged := merged + 1;
  end loop;

  raise notice 'kontak ganda digabungkan: %', merged;
end;
$backfill$;
