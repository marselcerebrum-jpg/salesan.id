-- =============================================================================
-- 0052 — antrean di luar jam kerja, dipisahkan dari SLA
--
-- Sampai sekarang pesan yang masuk jam 02.00 dan dibalas jam 09.00 tetap dinilai
-- sebagai SLA, hanya jamnya dihitung mulai dari shift dibuka. Akibatnya antrean
-- semalam langsung menyeret angka SLA hari itu, padahal tidak ada yang lalai:
-- tidak ada yang bertugas saat pesan itu datang.
--
-- Aturannya sekarang: SLA hanya untuk pesan yang MASUK di jam kerja dan DIBALAS
-- di jam kerja. Selain itu bukan pelanggaran dan bukan pencapaian — itu antrean,
-- dan antrean punya pertanyaannya sendiri: berapa lama tumpukan sebelum jam
-- kerja habis setelah shift dibuka.
--
-- 'queued' adalah status untuk siklus semacam itu. Bukan tabel baru: siklusnya
-- tetap satu putaran tunggu-balas yang sama, hanya dijawab dengan pertanyaan
-- yang berbeda. business_duration_seconds pada baris itu sudah berarti "berapa
-- detik jam kerja berlalu antara pesan masuk dan dibalas", yang untuk pesan
-- pra-shift sama persis dengan "berapa lama sejak shift dibuka" — jadi tidak ada
-- kolom baru yang perlu ditambahkan untuk mengukurnya.
--
-- Aman dijalankan ulang.
-- =============================================================================

alter type public.sla_status add value if not exists 'queued';

comment on type public.sla_status is
  'waiting: masih menunggu balasan. achieved/breached: dinilai terhadap target, hanya untuk pesan yang masuk dan dibalas di jam kerja. queued: masuk di luar jam kerja, dihitung sebagai antrean bukan SLA. excluded: dikecualikan dengan alasan tertulis.';
