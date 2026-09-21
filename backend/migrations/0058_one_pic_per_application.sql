-- =============================================================================
-- 0058 — satu aplikasi hanya boleh dipegang satu PIC
--
-- Aturannya sudah berlaku di operasional: sebuah aplikasi tidak pernah dipegang
-- dua PIC sekaligus. Yang belum ada adalah tempat aturan itu ditegakkan. Kunci
-- tabelnya (pic_user_id, application_id) hanya mencegah orang yang sama
-- ditugaskan dua kali ke aplikasi yang sama — bukan dua orang berbeda ke satu
-- aplikasi.
--
-- Kenapa itu berbahaya. Angka seorang PIC dihitung dari aplikasi yang
-- dipegangnya. Kalau satu aplikasi tercatat pada dua PIC, keduanya memuat
-- aplikasi itu di angkanya, dan jumlah seluruh PIC menjadi lebih besar daripada
-- operasional yang sebenarnya. Tidak ada error, tidak ada tanda apa pun. Jenis
-- kesalahan yang baru ketahuan berbulan-bulan kemudian, saat seseorang
-- menjumlahkan dan hasilnya tidak masuk akal.
--
-- Migration ini SENGAJA TIDAK GAGAL kalau datanya sudah terlanjur dobel. Ia
-- memeriksa dulu, dan kalau ada yang bertabrakan ia melewatkan pembuatan indeks
-- sambil menyebutkan aplikasi mana yang bermasalah. Menggagalkan proses deploy
-- karena data lama tidak akan memperbaiki data itu, dan memilih sendiri PIC
-- mana yang dibuang adalah keputusan yang bukan milik migration.
--
-- Setelah penugasan yang dobel dirapikan lewat halaman Pengaturan, jalankan
-- ulang migration ini dan indeksnya akan terpasang.
--
-- Aman dijalankan ulang.
-- =============================================================================

do $$
declare
  bentrok integer;
  daftar  text;
begin
  select count(*), string_agg(nama, ', ')
    into bentrok, daftar
    from (
      select coalesce(nullif(app.name, ''), app.code, p.application_id::text) as nama
        from public.pic_application_assignments p
        left join public.applications app on app.id = p.application_id
       group by p.application_id, app.name, app.code
      having count(*) > 1
    ) d;

  if bentrok > 0 then
    raise warning
      'Indeks dilewati: % aplikasi tercatat dipegang lebih dari satu PIC (%). Rapikan di halaman Pengaturan, lalu jalankan migration ini lagi.',
      bentrok, daftar;
  else
    create unique index if not exists uq_pic_application_one_pic
      on public.pic_application_assignments (application_id);
  end if;
end $$;

comment on table public.pic_application_assignments is
  'Aplikasi yang dipegang seorang PIC. Satu aplikasi hanya boleh punya satu PIC; ditegakkan oleh uq_pic_application_one_pic.';
