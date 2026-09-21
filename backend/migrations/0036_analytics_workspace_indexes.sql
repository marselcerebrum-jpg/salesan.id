-- =============================================================================
-- salesan.id — Migration 0036: indeks untuk cara analitik benar-benar bertanya
-- =============================================================================
-- Setiap tabel metrik sudah punya indeks per akun dan per aplikasi:
--
--   idx_messages_analytics_account      (account_id, timestamp desc)
--   idx_sla_cycles_scope                (account_id, started_at desc)
--   idx_follow_up_scope                 (account_id, local_date desc)
--   idx_contact_label_events_scope      (account_id, occurred_at desc)
--   idx_lead_classifications_scope      (account_id, lead_status, qualified_date)
--
-- Tetapi query-nya tidak memimpin dengan account_id. scopeWhere() memimpin
-- dengan workspace_id, lalu rentang waktu, dan baru menambahkan penyempitan
-- aplikasi kalau pemanggilnya bukan Leader. Untuk Leader, sc.All bernilai true
-- dan penyempitan itu tidak pernah ditambahkan sama sekali.
--
-- Jadi untuk Leader tidak ada satu pun indeks yang cocok: yang tersedia hanya
-- idx_messages_workspace (workspace_id saja, tanpa waktu), dan sisanya tidak
-- punya apa pun yang dimulai dari workspace_id. Postgres membaca seluruh
-- riwayat workspace lalu membuang yang di luar rentang.
--
-- Selama datanya kecil itu tidak terasa. Halaman Performa sekarang menjalankan
-- agregat itu berkali-kali dalam satu pemuatan (satu per aplikasi, satu per
-- orang), jadi biayanya dikalikan, bukan dijumlahkan.
--
-- Satu ketidakcocokan lain ikut diperbaiki: follow_up_events disaring dengan
-- started_at tetapi satu-satunya indeks waktunya ada di local_date, kolom yang
-- berbeda. Indeks itu tidak pernah bisa dipakai oleh penyaringan rentangnya.
--
-- CONCURRENTLY tidak dipakai: ia tidak boleh berada di dalam transaksi, dan
-- runner migrasi ini membungkus tiap berkas dalam satu transaksi. Pada ukuran
-- data sekarang pembuatannya hitungan detik.
--
-- Aman dijalankan ulang.
-- =============================================================================

begin;

-- Chat: jantung hampir setiap angka di Performa.
create index if not exists idx_messages_workspace_time
  on public.messages (workspace_id, timestamp desc);

-- SLA: disaring lewat started_at, sama seperti yang dipakai collectSLA dan
-- fillRangeResponse.
create index if not exists idx_sla_cycles_workspace_time
  on public.sla_cycles (workspace_id, started_at desc);

-- Follow-up: started_at, bukan local_date. Lihat catatan di atas.
create index if not exists idx_follow_up_workspace_time
  on public.follow_up_events (workspace_id, started_at desc);

-- Label: occurred_at, dipakai collectLabels dan drill-down label.
create index if not exists idx_contact_label_events_workspace_time
  on public.contact_label_events (workspace_id, occurred_at desc);

-- Lead: qualified_date adalah tanggal WIB, bukan timestamp, dan hampir selalu
-- dibaca bersama lead_status.
create index if not exists idx_lead_classifications_workspace_date
  on public.lead_classifications (workspace_id, lead_status, qualified_date);

-- Campaign: tanggal hasil sudah punya indeksnya sendiri sejak 0034. Yang belum
-- adalah dua tanggal lain yang dibaca pass "dibuat" dan "dijadwalkan".
create index if not exists idx_campaigns_workspace_created
  on public.content_campaigns (workspace_id, created_at desc);

create index if not exists idx_campaigns_workspace_scheduled
  on public.content_campaigns (workspace_id, scheduled_at desc)
  where scheduled_at is not null;

commit;
