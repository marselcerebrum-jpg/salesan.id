-- Pesan yang disematkan di dalam satu percakapan.
--
-- Disimpan pada pesannya, bukan pada percakapannya, karena itulah bentuk yang
-- dipakai WhatsApp sendiri: perintahnya menyebut kunci sebuah pesan, dan
-- pemberitahuan yang datang dari HP juga menyebut kunci sebuah pesan. Menyimpan
-- id pesan pada baris percakapan akan memaksa setiap peristiwa masuk
-- diterjemahkan dulu ke bentuk yang berbeda dari yang dikirimkan WhatsApp, dan
-- terjemahan itu tidak membeli apa pun.
--
-- `pinned_until` sekaligus menjawab dua hal dengan satu kolom: apakah pesan ini
-- tersemat, dan sampai kapan. WhatsApp menyematkan untuk jangka waktu tertentu,
-- bukan selamanya, jadi sebuah penanda boolean akan mulai berbohong begitu
-- waktunya lewat tanpa ada yang menuliskannya kembali.
--
-- `pinned_at` disimpan terpisah supaya urutan "yang paling baru disematkan"
-- tetap terjawab ketika dua pesan tersemat sekaligus.

alter table public.messages
  add column if not exists pinned_at    timestamptz,
  add column if not exists pinned_until timestamptz;

-- Dibaca sekali tiap kali sebuah percakapan dibuka, untuk satu baris spanduk di
-- atasnya. Tanpa index itu berarti memindai pesan satu percakapan setiap kali,
-- dan percakapan tersibuk di sini memuat puluhan ribu.
create index if not exists idx_messages_pinned
  on public.messages (conversation_id, pinned_until desc)
  where pinned_until is not null;

comment on column public.messages.pinned_until is
  'Sampai kapan pesan ini tersemat. Null berarti tidak tersemat.';
comment on column public.messages.pinned_at is
  'Kapan penyematannya dilakukan, untuk mengurutkan beberapa pesan tersemat.';
