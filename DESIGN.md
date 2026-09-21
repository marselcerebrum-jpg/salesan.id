# DESIGN.md — salesan.id

Arahan desain untuk dashboard omnichannel SALESAN.

Penulis arahan: pemilik produk. Berkas ini merekam keputusan mereka, bukan
usulan agen. Bagian mana pun yang belum diputuskan ditandai sebagai belum
diputuskan, bukan diisi tebakan.

---

## Basis

Minimalism dengan sentuhan Material Design.

Alasannya: layar ini data-heavy. Banyak channel, banyak status, banyak
notifikasi, dan orang membukanya berjam-jam. Minimalism menjaga fokus;
shadow dan elevation tipis ala Material membantu membedakan layer yang aktif
dari background.

## Layout

Bento grid.

Ringkasan tiap channel berdiri di kotak terpisah tapi tetap satu pandangan,
supaya user langsung melihat channel mana yang butuh perhatian tanpa scroll.

Catatan penerapan: antislop R-05 melarang bento grid sebagai layout default.
Yang membuatnya sah di sini adalah kotaknya memang tidak seukuran, dan
ukurannya mengikuti bobot isi, bukan mengisi ruang. Di Performa: SLA memegang
antrean orang yang belum dibalas, jadi ia selebar halaman; Group Engagement
membawa empat angka, jadi ia sepertiga. Kalau suatu hari semua kotaknya sama
besar, itu tandanya bento-nya sudah jadi template dan harus dibongkar.

## Dark mode

Opsi, bukan default.

Enak buat tim yang memantau dashboard berjam-jam, tapi harus lewat toggle,
jangan dipaksa. Terang tetap keadaan awal.

## Yang dihindari

- **Glassmorphism berlebihan.** Transparansi bikin teks susah dibaca di data
  padat.
- **Neo-brutalism.** Kesannya berisik untuk tool kerja harian.
- **Neumorphism.** Kontras rendah, capek dipelototin lama.

---

## Dial

    ENERGY 2 / RHYTHM 2 / MOTION 1

Diturunkan dari arahan di atas, bukan dipilih sendiri:

- **ENERGY 2.** "Minimalism + sentuhan Material" bukan 1 (datar sepenuhnya,
  GOV.UK) dan bukan 3 (agency). Elevasi dan satu warna aksen boleh terlihat,
  selebihnya diam.
- **RHYTHM 2.** Bento dengan ukuran mengikuti isi berarti komposisinya
  bervariasi, tapi dalam satu grid yang konsisten. Bukan asimetris bebas.
- **MOTION 1.** "Capek dipelototin lama" dan "tool kerja harian" menutup pintu
  untuk scroll-reveal dan parallax. Yang tersisa: hover, transisi state, dan
  bar yang tumbuh sekali saat angkanya datang.

## Palet

Sudah ada dan dipertahankan, diambil dari layar referensi salesan.id:

| Peran | Terang | Gelap |
|---|---|---|
| Merek (sidebar, aksi utama) | hijau tua `#0f3d2e` … `#22a06b` | `#17734f` … `#5fd3a3` |
| Kanvas | krem hangat `#faf7f2` | `#0f1512` |
| Permukaan kartu | `#ffffff` | `#161e19` |
| Garis rambut | pasir `#e8e1d6` | `#26312b` |
| Tinta | `#1c1917` / `#57534e` / `#756c62` | `#e8efe9` / `#b6c3ba` / `#86928a` |
| Perhatian | `#a1620a` (warn), `#c2410c` (danger) | `#dda75f`, `#f28b6b` |

Dua warna inti (hijau merek + netral hangat) plus semantik warn/danger. Batas
antislop R-29 adalah 2-3 warna inti + 1 aksen, dan palet ini di bawahnya.

Satu pengecualian yang disengaja: label yang dibuat pengguna memakai warna
pilihan pengguna sendiri. Itu satu-satunya warna di aplikasi ini yang datang
dari data, bukan dari kami.

Permukaan chat memakai palet WhatsApp sendiri dan sengaja dipisah, supaya
inbox terasa seperti WhatsApp sementara sisa aplikasi tetap jadi dirinya.

## Tipografi

Inter.

Alasannya bukan default: layar ini adalah kolom angka yang dibaca berjam-jam,
dan Inter punya `cv11` (angka satu yang tidak tertukar dengan huruf I kapital)
serta varian tabular. Keduanya sudah dinyalakan. Kalau suatu hari ada arahan
merek yang menuntut karakter lain, huruf ini boleh diganti.

Skala, sembilan langkah, tiap langkah satu tugas:

    12px  badge, header tabel, meta mikro
    13px  hint, waktu, keterangan pendukung
    15px  ISI: baris, tombol, input, label
    17px  judul kartu, nama di kepala baris
    20px  judul seksi dan dialog
    24px  subjek satu bagian halaman
    28px  judul halaman
    34px  angka utama sebuah kartu
    42px  satu angka yang dipimpin seluruh layar

Aturan yang lebih penting dari nilainya: dua elemen yang mengerjakan hal sama
memakai langkah sama, di berkas mana pun.

## Motif identitas

Tanda aplikasi.

Setiap objek di aplikasi ini milik tepat satu aplikasi, dan itu satu-satunya
hal yang selalu benar tentang objek apa pun di sini. Tandanya adalah kotak
bersudut 10px berisi dua huruf kode aplikasi dalam warna aplikasi itu sendiri,
dan ia muncul di mana pun sebuah objek perlu mengaku miliknya siapa: baris
percakapan, kartu akun, baris campaign, baris aktivitas, antrean SLA.

Ini bukan ornamen. Dengan tiga belas aplikasi berbagi satu layar, objek yang
tidak jelas aplikasinya adalah objek yang cepat atau lambat dikira milik tim
lain.

## Belum diputuskan

- Logo. Saat ini huruf "S" dalam lingkaran hijau sebagai penampung sementara,
  bukan logo final.
- Ilustrasi. Belum ada, dan tidak akan diadakan tanpa arahan.
