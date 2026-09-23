# 11. Future Development

Keterbatasan di bawah ini bukan dugaan. Semuanya ditemukan lewat pengukuran
atau kejadian nyata di produksi, dan sebagian sudah diverifikasi langsung pada
pustaka whatsmeow yang terpasang.

## 11.1 Current limitation

### Keterbatasan yang berasal dari WhatsApp dan whatsmeow

| Keterbatasan | Akibatnya | Bisa diperbaiki? |
|---|---|---|
| Tidak ada API untuk membaca daftar penonton Status | Angka penonton hanya batas bawah, dihitung dari tanda baca yang kebetulan diterima | Tidak, selama whatsmeow belum menyediakannya |
| Satu Story dialamatkan ke seluruh kontak nomor itu | Nomor dengan puluhan ribu kontak butuh waktu nyata tiap kali menerbitkan | Bisa diperkecil lewat daftar privasi Status di HP |
| Daftar perangkat penerima disimpan tanpa masa kedaluwarsa | Bila WhatsApp memberi tahu daftarnya berubah, untuk Status pustakanya tidak menyegarkan cache-nya | Tidak dari luar pustaka; pulih sendiri saat backend restart |
| Pemulihan pengiriman yang hasilnya tidak diketahui | Tidak diulang otomatis, harus diperiksa manusia | Tidak, mengulang berisiko kirim dua kali |

### Keterbatasan yang berasal dari sistem ini

| Keterbatasan | Ukurannya | Dampak |
|---|---|---|
| Laporan Performa menjalankan kueri berat sekali per anggota | 1.650 eksekusi dalam 16 menit, masing-masing 42.902 blok | Basis data bekerja jauh lebih keras dari yang perlu |
| Menghapus satu nomor berjalan lama | Sekitar 90 detik, basis data sibuk penuh | Pesan masuk sempat gagal tersimpan selama itu |
| Restart membuat 31 nomor menyambung serentak | Sebagian pesan masuk gagal disimpan, beban naik sampai sekitar 8 | Pesan masuk hilang, tidak diulang |
| Penggabungan kontak ganda hanya jalan saat migration dan saat tabrakan sinkron | Satu pasangan baru terdeteksi sehari kemudian | Kontak ganda bisa bertahan sampai ada yang memeriksa |
| Menu `COMING SOON` menempati sidebar | Instagram, TikTok, Chatbot, Auto Follow Up | Pengguna melihat menu yang tidak bisa dibuka |
| Tidak ada uji otomatis frontend | 38.212 baris tanpa uji | Perubahan antarmuka hanya diuji manual |
| Tidak ada pemantauan otomatis | — | Masalah ditemukan saat ada yang melapor |
| Tidak ada lingkungan staging | — | Perubahan diuji langsung di produksi |
| Tidak ada integrasi berkelanjutan | — | Verifikasi bergantung kedisiplinan manual |
| Tabel `work_schedules` dan `custom_variables` kosong | Fitur ada, belum dipakai | SLA jam kerja belum berjalan sesuai rancangan |

### Utang teknis yang sudah diketahui

| Butir | Keterangan |
|---|---|
| `README.md` menyebut Broadcast belum dikembangkan | Sudah usang; Broadcast berjalan penuh |
| Token desain tersebar di kelas Tailwind | Belum ada daftar warna dan skala yang terpusat |
| Tidak ada spesifikasi OpenAPI | Klien lain harus membaca kode |
| Tidak ada pembatasan laju permintaan | API bisa dipanggil sesering apa pun oleh pemegang token |

## 11.2 Recommended improvement

Diurutkan menurut perbandingan dampak dan risiko, berdasarkan apa yang sudah
terbukti mahal di produksi.

### Prioritas tinggi

| Usulan | Masalah yang diselesaikan | Perkiraan risiko |
|---|---|---|
| Hitung Performa sekali dikelompokkan per anggota, bukan diulang per anggota | Beban basis data terbesar yang tersisa | Sedang; mengubah angka yang dipakai sehari-hari, perlu perbandingan hasil lama dan baru angka per angka |
| Pantau lima indikator di bagian 10.7 | Masalah ditemukan oleh pengguna, bukan oleh sistem | Rendah |
| Cicil penghapusan nomor di latar belakang | Pesan masuk hilang selama penghapusan | Sedang |
| Sapuan berkala penggabungan kontak ganda | Kontak ganda bertahan sampai ada yang memeriksa | Rendah; fungsinya sudah ada dan teruji |

### Prioritas menengah

| Usulan | Masalah yang diselesaikan | Perkiraan risiko |
|---|---|---|
| Sebar waktu sambung ulang setelah restart | Ledakan 31 nomor serentak | Sedang |
| Ulangi penyimpanan pesan masuk yang gagal | Pesan masuk hilang saat basis data sibuk | Rendah |
| Uji otomatis untuk alur antarmuka utama | Perubahan antarmuka tanpa jaring pengaman | Rendah |
| Lingkungan staging | Perubahan diuji di produksi | Rendah |
| Integrasi berkelanjutan | Verifikasi bergantung kedisiplinan | Rendah |

### Prioritas rendah

| Usulan | Masalah yang diselesaikan |
|---|---|
| Spesifikasi OpenAPI | Integrator harus membaca kode |
| Token desain terpusat | Tampilan bisa menyimpang antar layar |
| Pembatasan laju permintaan | Tidak ada perlindungan dari pemakaian berlebihan |
| Perbarui `README.md` | Pernyataan usang tentang Broadcast |
| Tombol mematikan pengeluaran chat dari arsip per broadcast | Saat ini selalu menyala, hanya bisa dimatikan lewat API |

## 11.3 Feature roadmap

`[DATA BELUM TERSEDIA]` — Urutan dan waktu pengembangan fitur adalah keputusan
pemilik produk.

Yang bisa dipastikan dari sistem: empat menu sudah disiapkan tempatnya di
sidebar dan ditandai `COMING SOON`.

| Fitur | Tempat di sidebar | Kesiapan teknis yang sudah ada |
|---|---|---|
| Akun Instagram | Kelompok Akun | Belum ada; struktur `applications` sudah netral kanal |
| Akun TikTok | Kelompok Akun | Belum ada |
| Chat Instagram | Kelompok Percakapan | Belum ada; `conversations` terikat `whatsapp_accounts` dan perlu perubahan bila kanal lain masuk |
| Chat TikTok | Kelompok Percakapan | Belum ada |
| Chatbot | Kelompok Otomatisasi | `sender_source` sudah punya nilai `bot` |
| Auto Follow Up | Kelompok Otomatisasi | `follow_up_events` sudah mencatat follow up manual |

Catatan arsitektur untuk siapa pun yang mengerjakan kanal kedua: `conversations`,
`contacts`, dan `messages` saat ini terikat langsung ke `whatsapp_accounts`.
Menambah Instagram atau TikTok berarti memutuskan apakah akan menambah tabel
akun per kanal, atau menggeneralisasi `whatsapp_accounts` menjadi tabel akun
kanal. Keputusan itu menyentuh hampir seluruh kueri di `repository` dan
sebaiknya diambil sadar, bukan lewat penambahan kolom satu per satu.

## 11.4 Yang sebaiknya tidak diubah

Beberapa keputusan terlihat seperti kekurangan tetapi sebenarnya disengaja.
Mengubahnya tanpa alasan kuat akan merusak sesuatu yang sudah benar.

| Keputusan | Mengapa dipertahankan |
|---|---|
| Angka penonton Story disebut batas bawah | Menambah perkiraan akan mengubah pengukuran jujur menjadi angka karangan |
| Hasil pengiriman tidak diketahui tidak diulang otomatis | Mengulang berisiko mengirim pesan yang sama dua kali ke orang yang sama |
| Tidak ada simulasi mengetik atau membaca palsu | Keputusan produk yang tercatat; memalsukan perilaku manusia membahayakan nomor pelanggan sendiri |
| Antrean di basis data, bukan di memori | Itulah yang membuat restart menjadi peristiwa biasa |
| Pembatasan akses di backend, bukan di tampilan | Menyembunyikan menu bukan pembatasan |
| Riwayat pengiriman dan performa tidak dihapus permanen | Angka kinerja harus bisa ditelusuri ulang |

---

## Ringkasan yang belum tersedia

| Butir | Siapa yang memegang jawabannya |
|---|---|
| Urutan dan waktu roadmap | Pemilik produk |
| Keputusan arsitektur untuk kanal kedua | Arsitek dan pemilik produk |
| Anggaran dan kapasitas tim | Pemilik produk |
| Target komersial yang menentukan prioritas | Pemilik produk |
