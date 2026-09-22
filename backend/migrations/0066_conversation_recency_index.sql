-- =============================================================================
-- salesan.id — Migration 0066: sapuan rekonsiliasi berhenti memindai semuanya
-- =============================================================================
-- Sapuan rekonsiliasi metrik berjalan tiap METRICS_RECONCILE_INTERVAL dan
-- bertanya: percakapan mana yang punya pesan lebih baru daripada angka
-- turunannya. Saringan pertamanya `last_message_at >= $1`, dan tidak ada indeks
-- yang melayani itu. Indeks inbox memang memuat waktu pesan terakhir, tetapi di
-- belakang account_id, jadi tidak bisa dipakai untuk pertanyaan yang tidak
-- menyebut nomor.
--
-- Akibatnya seluruh 98.162 percakapan dibaca setiap kali, sekitar 284 MB, empat
-- kali sejam, hanya untuk menemukan segelintir baris yang ketinggalan.
--
-- Indeks ini menggantinya dengan pembacaan rentang: yang dibaca hanya
-- percakapan yang memang bergerak dalam jendela yang ditanyakan.
--
-- Biayanya satu pembaruan indeks setiap kali percakapan menerima pesan, dan itu
-- jauh lebih murah daripada memindai tabelnya empat kali sejam.
--
-- Aman dijalankan ulang.
-- =============================================================================

create index if not exists idx_conversations_recency
  on public.conversations (last_message_at)
  where last_message_at is not null;

comment on index public.idx_conversations_recency is
  'Percakapan menurut waktu pesan terakhir, lintas nomor. Dipakai sapuan rekonsiliasi metrik supaya ia membaca rentang waktu, bukan seluruh tabel.';
