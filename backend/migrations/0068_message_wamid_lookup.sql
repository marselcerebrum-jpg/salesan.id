-- =============================================================================
-- salesan.id — Migration 0068: mencari pesan lewat id WhatsApp-nya saja
-- =============================================================================
-- Gelembung kutipan di atas sebuah balasan perlu menemukan pesan yang dikutip.
-- Selama ini pencariannya dikunci ke satu nomor, lewat indeks unik
-- (account_id, wa_message_id), padahal satu grup yang sama diikuti beberapa
-- nomor kita dan pesannya tersimpan satu baris per nomor.
--
-- Akibatnya, membaca percakapan lewat nomor A sementara pesan yang dikutip
-- hanya tersimpan di bawah nomor B membuat kutipannya dinyatakan hilang.
-- Terhitung pada data produksi: 916 kutipan gagal ditemukan dalam tujuh hari,
-- dan 144 di antaranya sebenarnya ada, hanya di bawah nomor lain.
--
-- Pencarian yang baru menyebut id WhatsApp saja, dibatasi workspace yang sama.
-- Tanpa indeks ini pencarian seperti itu akan memindai seluruh tabel pesan
-- setiap kali satu halaman percakapan dibuka, yang justru lebih buruk daripada
-- cacat yang diperbaikinya.
--
-- Aman dijalankan ulang.
-- =============================================================================

create index if not exists idx_messages_wamid
  on public.messages (wa_message_id);

comment on index public.idx_messages_wamid is
  'Mencari satu pesan dari id WhatsApp-nya tanpa menyebut nomor. Dipakai gelembung kutipan, karena pesan grup yang sama tersimpan satu baris per nomor yang mengikutinya.';
