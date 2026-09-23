# 9. Testing Documentation

## 9.1 Keadaan sekarang

| Aspek | Keadaan |
|---|---|
| Uji otomatis backend | 209 fungsi uji, 34 berkas, 4.989 baris |
| Uji otomatis frontend | Tidak ada |
| Uji menyeluruh (end-to-end) | Tidak ada |
| Integrasi berkelanjutan | Tidak ada |
| Alat pelacak bug | `[DATA BELUM TERSEDIA]` |
| Rencana uji formal | `[DATA BELUM TERSEDIA]` |

Uji yang ada berjalan cepat dan tidak membutuhkan basis data, kecuali berkas
integrasi di `repository`.

### Sebaran uji per paket

| Paket | Berkas uji | Yang diuji |
|---|---|---|
| `wa` | 10 | Penanganan peristiwa WhatsApp, normalisasi gambar, kunci tanda terima |
| `repository` | 7 | Kueri, scope, penggabungan kontak |
| `analytics` | 5 | SLA, follow up, klasifikasi lead |
| `campaign` | 3 | Penjadwal, penyelesaian target, klasifikasi galat kirim |
| `auth` | 2 | Verifikasi JWT |
| `httpapi` | 2 | Izin peran |
| `compose`, `config`, `media`, `mediafetch`, `realtime` | 1 masing-masing | Spintax, konfigurasi, klasifikasi berkas, penjagaan SSRF, kehadiran |

## 9.2 Test scenario

Skenario di bawah diturunkan dari nama fungsi uji yang benar-benar ada, jadi
setiap barisnya bisa dijalankan.

### Analitik dan SLA

| Skenario | Fungsi uji |
|---|---|
| Follow up dihitung setelah percakapan sepi | `TestFollowUpAfterQuietPeriod` |
| Follow up tidak dihitung saat pelanggan yang menunggu | `TestFollowUpNotCountedWhenCustomerIsWaiting` |
| Follow up tidak dihitung pada kontak pertama | `TestFollowUpNotCountedOnFirstContact` |
| Beberapa pesan sehari dihitung satu aktivitas | `TestFollowUpSeveralMessagesSameDayIsOneActivity` |
| Hari berbeda dihitung terpisah | `TestFollowUpOnSeparateDaysAreSeparateActivities` |
| Broadcast dan otomatisasi tidak dihitung follow up | `TestFollowUpExcludesBroadcastAndAutomation` |
| Urutan masukan tidak mengubah hasil | `TestFollowUpIsOrderIndependent` |
| Tanggal lokal memakai zona Jakarta | `TestLocalDateUsesJakarta` |

### Klasifikasi lead

| Skenario | Fungsi uji |
|---|---|
| Kontak baru terverifikasi | `TestLeadVerifiedNew` |
| Kontak dari impor dinilai historis | `TestLeadFromImportIsHistorical` |
| Kontak dengan riwayat lebih awal dinilai historis | `TestLeadWithEarlierHistoryIsHistorical` |
| Kontak yang sudah berlabel dinilai historis | `TestLeadWithPriorLabelIsHistorical` |
| Kontak tanpa pesan masuk dinilai tidak diketahui | `TestLeadWithoutInboundIsUnknown` |
| Setiap klasifikasi membawa alasannya | `TestEveryClassificationCarriesAReason` |

### Pengiriman

| Skenario | Fungsi uji |
|---|---|
| Gambar bukan JPEG diubah sebelum dikirim | `TestImageThatIsNotJPEGIsConvertedBeforeSending` |
| Gambar JPEG dibiarkan apa adanya | `TestJPEGIsLeftAlone` |
| Video dan dokumen tidak disentuh | `TestNonImagesArePassedThrough` |
| Gambar rusak tetap dikirim | `TestUndecodableImageIsStillSent` |
| Kunci tanda terima tetap pendek dan unik | `TestReceiptKeyStaysShortAndStaysDistinct` |
| Pengiriman lewat batas waktu dibedakan dari penolakan | `TestSendTimeoutIsDistinguishedFromRefusal` |

### Izin dan kehadiran

| Skenario | Fungsi uji |
|---|---|
| Semua peran boleh mengelola balas cepat | `TestQuickRepliesAreManagedByEveryRole` |
| Kehadiran menyatukan beberapa tab dan bersih saat keluar | `TestPresenceCollapsesTabsAndClearsOnUnregister` |

## 9.3 Test case

Contoh format kasus uji, memakai satu uji yang benar-benar ada sebagai model.

| Bidang | Isi |
|---|---|
| **ID** | TC-WA-001 |
| **Judul** | Gambar bukan JPEG diubah sebelum dikirim ke WhatsApp |
| **Prasyarat** | Ada berkas gambar PNG atau WebP yang sah |
| **Langkah** | 1. Panggil `normaliseImage` dengan berkas PNG<br/>2. Periksa hasil kembaliannya |
| **Hasil yang diharapkan** | MIME menjadi `image/jpeg`, ekstensi `.jpg`, ukuran terisi, berkas bisa didekode sebagai JPEG |
| **Alasan kasus ini ada** | Foto WebP diterima server WhatsApp tetapi tidak bisa digambar HP; di peramban terlihat, di HP kosong |
| **Otomatis** | Ya, `internal/wa/imagenormalise_test.go` |

`[DATA BELUM TERSEDIA]` — Kasus uji manual untuk alur antarmuka, misalnya
menyusun broadcast dari awal sampai laporan, belum ditulis.

## 9.4 Acceptance criteria

Kriteria berikut diturunkan dari perilaku yang sudah ditegakkan kode dan bisa
langsung dipakai menguji ulang.

### Broadcast

| Kriteria | Cara memeriksa |
|---|---|
| Kampanye tanpa penerima ditolak saat dibuat | Buat kampanye dengan grup yang tidak terjangkau; harus 400 `no_recipients` |
| Komunitas ditolak dengan alasan terbaca | Pratinjau dengan komunitas sebagai tujuan; `problems` menyebut "ini komunitas" |
| Pengiriman gagal memindahkan status kampanye | Kampanye dengan semua nomor putus harus gagal setelah 30 menit, bukan menggantung |
| Chat terarsip dikeluarkan dari arsip | Kirim ke chat terarsip; `is_archived` menjadi `false` setelah terkirim |
| Laporan tidak menyisakan penerima menggantung | Batalkan kampanye saat berjalan; setelah sapuan, tidak ada penerima berstatus `processing` |

### Inbox

| Kriteria | Cara memeriksa |
|---|---|
| Kutipan muncul seketika pada balasan bergambar | Balas dengan gambar; tanggapan API memuat `quoted` |
| Balas cepat membawa kutipan | Kirim balas cepat bergambar dengan `reply_to`; tanggapan memuat `quoted` |
| Foto sampai dalam keadaan bisa digambar | Lampiran tercatat `image/jpeg` dengan dimensi dan thumbnail |
| Status milik sendiri tampil satu baris | Panel Status hanya menampilkan satu "Status Saya" per nomor |

### Akses

| Kriteria | Cara memeriksa |
|---|---|
| Freelance tidak bisa melihat performa orang lain | Panggil `/performance/members` sebagai Freelance; hanya dirinya yang muncul |
| PIC tidak bisa membuka nomor di luar penugasannya | Panggil `/accounts/{id}/conversations` untuk nomor lain; harus 403 |
| Pembatasan bekerja walau dipanggil langsung ke API | Ulangi dua pemeriksaan di atas tanpa lewat antarmuka |

## 9.5 Bug tracking format

`[DATA BELUM TERSEDIA]` — Alat pelacak bug yang dipakai tim belum ditetapkan.

Format berikut diusulkan karena cocok dengan cara masalah pada sistem ini
biasanya muncul, yaitu terlihat benar di satu sisi dan salah di sisi lain.

```markdown
## [ID] Judul singkat

**Dilaporkan:** tanggal, oleh siapa
**Tingkat:** kritis / tinggi / sedang / rendah
**Modul:** Inbox / Broadcast / Story / Performa / Akun

### Yang terjadi
Apa yang dilihat, di layar mana, dengan nomor mana, jam berapa.

### Yang diharapkan
Satu kalimat.

### Langkah mengulang
1.
2.

### Bukti
Tangkapan layar, id pesan WhatsApp, id kampanye, potongan log.

### Perbedaan web dan HP
Terlihat di salesan? Terlihat di HP? Keduanya perlu dijawab terpisah,
karena banyak masalah pada sistem ini hanya muncul di salah satunya.

### Penyebab yang ditemukan
Diisi saat ditelusuri.

### Perbaikan
Commit, migration, dan cara memverifikasinya.
```

Bidang "Perbedaan web dan HP" bukan tambahan biasa. Beberapa cacat paling mahal
pada sistem ini, misalnya foto WebP yang tidak bisa digambar HP, hanya bisa
dikenali bila kedua sisi ditanyakan terpisah.

## 9.6 Menjalankan uji

```bash
cd backend
go test ./internal/...                      # seluruhnya
go test ./internal/campaign/ -v             # satu paket, rinci
go test ./internal/wa/ -run Image -v        # satu kelompok
```

---

## Ringkasan yang belum tersedia

| Butir | Siapa yang memegang jawabannya |
|---|---|
| Alat pelacak bug | Tim |
| Kasus uji manual antarmuka | QA |
| Uji otomatis frontend | Tim developer |
| Uji menyeluruh | Tim developer |
| Target cakupan uji | Tim developer |
| Integrasi berkelanjutan | Tim developer |
