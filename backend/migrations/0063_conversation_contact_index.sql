-- =============================================================================
-- salesan.id — Migration 0063: peta percakapan pribadi per nomor
-- =============================================================================
-- Pemilih penerima Broadcast menanyakan, untuk satu nomor pengirim, seluruh
-- kontaknya beserta percakapan pribadi yang sudah ada dengan masing-masing.
-- Sambungan itu dicari lewat (account_id, contact_id), dan tidak ada indeks
-- yang menutupinya: untuk satu nomor dengan 44.541 kontak, Postgres memindai
-- seluruh 98.181 baris percakapan — milik ke-35 nomor lain juga — lalu
-- membuang yang bukan miliknya.
--
-- Indeks ini memisahkan percakapan per nomor sejak awal, jadi yang dibaca
-- hanya milik nomor yang ditanyakan. Diukur pada nomor terbesar: pemindaian
-- penuh hilang, dan kueri turun dari 55 ms ke 44 ms. Bedanya kecil hari ini
-- karena tabelnya masih kecil; yang berubah adalah bentuk bebannya, dari
-- tumbuh mengikuti seluruh workspace menjadi tumbuh mengikuti satu nomor saja.
--
-- Sebagian, karena hanya percakapan pribadi yang punya contact_id dan hanya
-- itu yang ditanyakan. Indeks penuh akan menyimpan puluhan ribu baris grup
-- yang tidak pernah dibaca lewat jalan ini.
--
-- Aman dijalankan ulang.
-- =============================================================================

create index if not exists idx_conversations_account_contact
  on public.conversations (account_id, contact_id)
  where contact_id is not null and type = 'personal';

comment on index public.idx_conversations_account_contact is
  'Percakapan pribadi per nomor pengirim. Dipakai pemilih penerima Broadcast untuk memasangkan kontak dengan percakapan yang sudah ada.';
