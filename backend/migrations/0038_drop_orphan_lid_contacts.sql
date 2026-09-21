-- =============================================================================
-- salesan.id — Migration 0038: buang kontak LID yang tidak menunjuk apa pun
-- =============================================================================
-- Migrasi 0037 menggabungkan kontak kembar yang nomornya diketahui. Yang
-- tersisa adalah baris ber-LID yang nomornya tidak diketahui siapa pun: tidak
-- ada di peta whatsmeow, tidak punya percakapan, tidak punya pesan.
--
-- Baris-baris itu masuk dari sinkronisasi buku alamat. Selama WhatsApp
-- memindahkan penggunanya ke LID, ia menyebut satu kontak tersimpan dua kali:
-- sekali dengan nomor, sekali dengan LID. Yang ber-LID tidak bisa dipakai untuk
-- apa pun di sini. Tidak bisa di-broadcast, karena tidak punya nomor. Tidak
-- bisa dibuka, karena tidak punya percakapan. Yang dilakukannya hanya muncul di
-- daftar kontak sebagai kembaran dari baris yang benar, dengan angka LID-nya
-- terbaca seperti nomor telepon.
--
-- Penyaring di bawah yang membuat penghapusan ini aman, bukan keyakinan:
-- sebuah baris hanya dibuang kalau ia tidak dirujuk oleh satu pun dari sembilan
-- tabel yang menyimpan contact_id, dan tidak ada pesan yang datang dari
-- alamatnya. Tidak ada riwayat yang bisa ikut hilang, karena baris yang punya
-- riwayat tidak akan lolos penyaring.
--
-- Pencegahannya ada di sinkronisasi: entri buku alamat tanpa nomor yang bisa
-- diselesaikan sekarang dilewati. Orang yang memang hanya ada sebagai LID tetap
-- masuk lewat jalur pesan dan anggota grup, dan di sana ia memang dibutuhkan.
--
-- Aman dijalankan ulang.
-- =============================================================================

begin;

delete from public.contacts c
 where c.jid like '%@lid'
   and (c.phone_number is null or c.phone_number = '')
   and not exists (select 1 from public.conversations        t where t.contact_id = c.id)
   and not exists (select 1 from public.conversation_members t where t.contact_id = c.id)
   and not exists (select 1 from public.sla_cycles           t where t.contact_id = c.id)
   and not exists (select 1 from public.follow_up_events     t where t.contact_id = c.id)
   and not exists (select 1 from public.contact_label_events t where t.contact_id = c.id)
   and not exists (select 1 from public.campaign_targets     t where t.contact_id = c.id)
   and not exists (select 1 from public.contact_first_seen   t where t.contact_id = c.id)
   and not exists (select 1 from public.lead_classifications t where t.contact_id = c.id)
   and not exists (select 1 from public.contact_label_state  t where t.contact_id = c.id)
   -- Alamatnya sendiri juga harus belum pernah terlihat mengirim apa pun.
   and not exists (
     select 1 from public.messages m
      where m.account_id = c.account_id
        and (m.sender_jid = c.jid or m.participant_jid = c.jid));

commit;
