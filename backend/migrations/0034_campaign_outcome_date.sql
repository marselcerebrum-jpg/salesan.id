-- 0034 — tanggal hasil campaign yang benar untuk status berjalan.
--
-- Migrasi 0033 memindahkan pembukuan hasil campaign ke tanggal kejadiannya,
-- tetapi rantai tanggalnya melewatkan started_at. Akibatnya status "sedang
-- dikirim" jatuh kembali ke created_at: broadcast yang ditulis tanggal 8 dan
-- masih berjalan tanggal 11 terbukukan di tanggal 8, dan hilang sama sekali
-- dari filter satu hari.
--
-- started_at diisi tepat saat worker mengambil campaign, jadi itulah tanggal
-- yang benar untuk status berjalan. Indeksnya harus mengikuti ekspresi yang
-- dipakai query persis, kalau tidak ia tidak akan pernah terpakai.
--
-- Kolom audit scheduled_by, updated_by dan cancelled_by dari 0033 tidak
-- diisi lewat trigger database. auth.uid() kosong pada jalur service role,
-- dan seluruh tulisan aplikasi ini lewat jalur itu, jadi trigger semacam itu
-- akan selalu menulis NULL. Pengisiannya dilakukan di Go, di tempat identitas
-- penggunanya memang diketahui.

begin;

drop index if exists idx_campaigns_outcome_at;

create index if not exists idx_campaigns_outcome_at
  on public.content_campaigns
     (workspace_id, (coalesce(executed_at, started_at, cancelled_at, created_at)));

commit;
