-- =============================================================================
-- salesan.id — Migration 0064: indeks awalan untuk tabel sesi whatsmeow
-- =============================================================================
-- Inilah sebab Broadcast dan Story terasa lambat.
--
-- Setiap kali WhatsApp memperkenalkan perangkat lawan bicara dengan alamat
-- barunya, whatsmeow memindahkan sesi dan kunci identitas dari alamat lama ke
-- alamat baru. Empat perintah yang dipakainya semua mencocokkan awalan:
--
--   DELETE FROM whatsmeow_sessions       WHERE our_jid=$1 AND their_id LIKE $2
--   INSERT INTO whatsmeow_sessions ...   WHERE our_jid=$1 AND their_id LIKE $2||$4
--   DELETE FROM whatsmeow_identity_keys  WHERE our_jid=$1 AND their_id LIKE $2
--   INSERT INTO whatsmeow_identity_keys  WHERE our_jid=$1 AND their_id LIKE $2||$4
--
-- Satu-satunya indeks pada tabel itu adalah primary key (our_jid, their_id).
-- Basis data ini memakai collation en_US.UTF-8, dan pada collation itu btree
-- biasa tidak bisa dipakai untuk pencocokan awalan LIKE. Kolom pertama pun
-- tidak menolong: satu nomor besar memegang 68.806 dari 302.637 sesi, jadi
-- membaca lewat our_jid saja berarti membaca seperempat tabel. Postgres
-- menyerah dan memindai seluruhnya.
--
-- Terukur di produksi pada 22 September 2026, satu perintah DELETE:
--
--   Seq Scan, 302.633 baris dibuang oleh filter, 71.062 blok dibaca, 14,2 ms
--
-- Dikalikan 71.440 panggilan menjadi 17 menit; empat perintah itu bersama-sama
-- memakan 51 menit waktu basis data. Waktu itu dibayar justru saat mengirim,
-- karena perkenalan perangkat terjadi tepat ketika pesan hendak dienkripsi.
-- Story ke dua puluh ribu kontak membayarnya ribuan kali berturut-turut.
--
-- text_pattern_ops membuat btree membandingkan karakter demi karakter, tanpa
-- aturan urutan bahasa, yang justru bentuk perbandingan yang dibutuhkan LIKE
-- 'awalan%'. Perintah yang sama, diukur ulang dengan indeks ini:
--
--   Index Scan, 3 blok dibaca, 0,16 ms
--
-- Delapan puluh sembilan kali lebih cepat, dan blok yang dibaca turun dari
-- 71.062 menjadi 3.
--
-- Tabel-tabel ini milik whatsmeow, bukan milik skema kita: ia membuatnya
-- sendiri saat perangkat pertama tersambung, yaitu setelah migration berjalan.
-- Karena itu keberadaannya diperiksa dulu. Pada basis data baru indeks ini
-- belum terpasang, dan migration berikutnya yang jalan setelah ada perangkat
-- akan memasangnya. Menambah indeks tidak mengubah tabel milik whatsmeow dan
-- tidak mengganggu upgrade-nya sendiri.
--
-- Aman dijalankan ulang.
-- =============================================================================

do $$
begin
  if to_regclass('public.whatsmeow_sessions') is not null then
    create index if not exists idx_whatsmeow_sessions_prefix
      on public.whatsmeow_sessions (our_jid, their_id text_pattern_ops);
  else
    raise notice 'whatsmeow_sessions belum ada; indeks dilewati';
  end if;

  if to_regclass('public.whatsmeow_identity_keys') is not null then
    create index if not exists idx_whatsmeow_identity_keys_prefix
      on public.whatsmeow_identity_keys (our_jid, their_id text_pattern_ops);
  else
    raise notice 'whatsmeow_identity_keys belum ada; indeks dilewati';
  end if;

  -- Tabel ini masih kecil, 6.377 baris, dan perintahnya sudah berjalan di
  -- bawah satu milidetik. Diindeks juga karena bentuk perintahnya sama persis
  -- dan akan menemui dinding yang sama begitu tabelnya tumbuh.
  if to_regclass('public.whatsmeow_sender_keys') is not null then
    create index if not exists idx_whatsmeow_sender_keys_prefix
      on public.whatsmeow_sender_keys (our_jid, sender_id text_pattern_ops);
  else
    raise notice 'whatsmeow_sender_keys belum ada; indeks dilewati';
  end if;
end;
$$;
