# 3. User Flow Documentation

Alur di dokumen ini diturunkan dari rute aplikasi, middleware, dan perilaku
worker yang benar-benar berjalan.

## 3.1 Main user journey

### Masuk sampai bisa bekerja

```mermaid
flowchart TD
    A[Buka salesan.marseltech.cloud] --> B{Punya sesi?}
    B -->|Tidak| C[Halaman /login]
    C --> D[Supabase Auth memeriksa email dan sandi]
    D -->|Gagal| C
    D -->|Berhasil| E[JWT disimpan di peramban]
    B -->|Ya| E
    E --> F[Middleware Next.js meneruskan ke /dashboard]
    F --> G[Backend memanggil ResolveScope]
    G --> H{Peran operasional}
    H -->|Leader| I[Seluruh workspace terlihat]
    H -->|PIC| J[Hanya aplikasi yang ditugaskan]
    H -->|Freelance| K[Aplikasi yang ditugaskan, performa diri sendiri]
    I --> L[Sidebar dan data mengikuti scope]
    J --> L
    K --> L
```

Scope dihitung ulang di backend pada setiap permintaan. Tidak ada peran yang
disimpan di peramban dan dipercaya begitu saja.

### Membalas pelanggan

```mermaid
sequenceDiagram
    participant A as Admin
    participant W as Web (Next.js)
    participant B as Backend (Go)
    participant M as whatsmeow
    participant WA as WhatsApp

    A->>W: Pilih aplikasi lalu nomor
    W->>B: GET /accounts/{id}/conversations
    B-->>W: Daftar percakapan
    A->>W: Buka satu percakapan
    W->>B: WebSocket presence.view
    B-->>W: presence.viewers ke semua yang membuka
    W->>B: GET /conversations/{id}/messages
    A->>W: Tulis balasan, pilih pesan yang dikutip
    W->>B: POST /conversations/{id}/messages
    B->>B: Tulis baris pesan status pending
    B->>M: SendMessage dengan ContextInfo kutipan
    M->>WA: Kirim terenkripsi
    WA-->>M: Tanda terima
    B->>B: Status jadi sent, lalu delivered, lalu read
    B-->>W: Siarkan lewat WebSocket
    W-->>A: Gelembung pesan berubah tanda
```

## 3.2 Feature flow

### Broadcast dari pembuatan sampai laporan

```mermaid
stateDiagram-v2
    [*] --> draft: Simpan tanpa jadwal
    [*] --> scheduled: Jadwalkan
    draft --> scheduled: Jadwalkan
    scheduled --> running: Scheduler mengklaim saat waktunya tiba
    running --> completed: Semua penerima berhasil
    running --> partial: Sebagian gagal
    running --> failed: Tidak ada yang berhasil
    running --> cancelled: Operator membatalkan
    scheduled --> cancelled: Operator membatalkan
    running --> running: Nomor putus, dicoba lagi tiap 5 detik
    running --> failed: Semua nomor putus melebihi tenggang 30 menit
    completed --> [*]
    partial --> [*]
    failed --> [*]
    cancelled --> [*]
```

Langkah worker untuk satu penerima:

```mermaid
flowchart TD
    A[Klaim penerima dari basis data] --> B[Bangun pesan dari template]
    B --> C{Variabel lengkap?}
    C -->|Tidak| D[Gagal: variabel_kosong]
    C -->|Ya| E[Tulis baris percobaan sebelum kirim]
    E --> F[Tulis pesan status pending di thread]
    F --> G[Kirim ke WhatsApp, batas 2 menit]
    G -->|Lewat batas| H[Catat hasil tidak diketahui, tidak diulang]
    G -->|Error 420| I[Gagal final: komunitas atau grup khusus admin]
    G -->|Error lain| J{Percobaan kurang dari batas?}
    J -->|Ya| K[Jadwalkan ulang setelah jeda]
    J -->|Tidak| L[Gagal final]
    G -->|Berhasil| M[Status sent, catat waktu]
    M --> N{Chat tujuan terarsip?}
    N -->|Ya| O[Keluarkan dari arsip lewat app state]
    N -->|Tidak| P[Selesai]
    O --> P
```

### WA Story

```mermaid
flowchart TD
    A[Buat Story, pilih nomor] --> B[Seed satu publikasi per nomor]
    B --> C[Worker klaim maksimal 4 publikasi]
    C --> D[Ambil slot, maksimal 3 serentak se-proses]
    D --> E[Perpanjang lease selama proses berjalan]
    E --> F[whatsmeow enkripsi kunci untuk tiap kontak]
    F --> G[Terbit, catat id pesan WhatsApp]
    G --> H[Tulis salinan ke thread Status sendiri]
    H --> I[Tanda baca masuk dicatat sebagai penonton]
    I --> J[Setelah 24 jam, ditutup otomatis]
```

> Satu Story bukan satu kiriman. WhatsApp mengalamatkannya ke seluruh kontak
> tersimpan nomor itu, sehingga nomor dengan puluhan ribu kontak butuh waktu
> nyata. Detail pengukurannya ada di [`docs/broadcast-dan-story.md`](../broadcast-dan-story.md).

### Menyambungkan nomor baru

```mermaid
sequenceDiagram
    participant A as Admin
    participant B as Backend
    participant WA as WhatsApp

    A->>B: POST /accounts (nama, aplikasi)
    A->>B: POST /accounts/{id}/pair
    B->>WA: Minta kode QR
    WA-->>B: Kode QR
    B-->>A: QR ditampilkan, status qr_pending
    A->>A: Pindai dari aplikasi WhatsApp di HP
    WA-->>B: Perangkat tertaut
    B->>B: Status jadi connected
    B->>WA: Tarik kontak, grup, riwayat
    B-->>A: Progres sinkron lewat WebSocket
```

## 3.3 Exception flow

| Kejadian | Yang dilakukan sistem | Yang dilihat operator |
|---|---|---|
| Nomor keluar dari WhatsApp | Status jadi `logged_out`, pengiriman dari nomor itu gagal | Nomor ditandai perlu pindai ulang |
| Semua nomor kampanye putus | Menunggu, dicoba tiap 5 detik, gagal setelah 30 menit | Kampanye gagal dengan nama nomor yang hilang |
| Pengiriman menggantung | Dibatalkan setelah 2 menit, dicatat tidak diketahui | "Hasil pengiriman tidak diketahui, periksa percakapan" |
| Backend restart saat kampanye jalan | Lease habis, kampanye diklaim ulang, yang menggantung direkonsiliasi | Kampanye lanjut sendiri |
| Pembatalan saat worker sedang mengirim | Penanda dipasang, worker menutup kampanye di kesempatan berikutnya | Kampanye jadi dibatalkan, sisa penerima ditandai dibatalkan |
| Kampanye selesai meninggalkan penerima menggantung | Sapuan tiap 10 menit menutupnya | Laporan tidak lagi menyisakan "sedang diproses" |
| Gambar bukan JPEG | Diubah ke JPEG sebelum diunggah | Tidak terlihat, gambar sampai seperti biasa |
| Gambar tidak bisa dibaca | Dikirim apa adanya | Tidak terlihat |
| Media di URL gagal diambil | Kampanye ditolak sebelum tersimpan | "Media tidak dapat diambil" beserta sebabnya |
| Grup tujuan ternyata komunitas | Ditolak di pratinjau | "Ini komunitas, bukan grup chat" |
| Dua alamat untuk satu orang | Baris kontak digabung | Kontak tidak lagi dobel |

### Pemulihan setelah restart

```mermaid
flowchart TD
    A[Backend mulai] --> B[Scheduler jalan tiap 5 detik]
    B --> C[Klaim kampanye yang lease-nya habis]
    C --> D{Ada pembatalan tertunda?}
    D -->|Ya| E[Tutup kampanye, batalkan sisa penerima]
    D -->|Tidak| F[Rekonsiliasi penerima yang menggantung]
    F --> G{Pesannya benar-benar terkirim?}
    G -->|Ada buktinya| H[Tandai terkirim]
    G -->|Tidak ada| I[Tandai hasil tidak diketahui, jangan ulang]
    H --> J[Lanjutkan sisa penerima]
    I --> J
```

## 3.4 Customer journey map

`[DATA BELUM TERSEDIA]` — Peta perjalanan pelanggan akhir, yaitu orang yang
menerima pesan WhatsApp, tidak bisa diturunkan dari sistem ini. Sistem hanya
merekam sisi operator. Untuk menyusunnya diperlukan riset terhadap penerima.

Yang tersedia dari sistem adalah jejak yang bisa diukur pada sisi pelanggan:

| Tahap | Yang tercatat | Tabel |
|---|---|---|
| Pertama kali dikenal | Waktu pertama terlihat, dari mana | `contact_first_seen` |
| Masuk percakapan | Pesan pertama masuk, waktu balas pertama | `sla_cycles` |
| Diberi label | Perubahan label beserta pelakunya | `contact_label_events` |
| Dinilai sebagai lead | `verified_new`, `historical`, `unknown` | `lead_classifications` |
| Ditindaklanjuti | Satu catatan per percakapan per hari | `follow_up_events` |
| Menerima kampanye | Status per penerima | `campaign_targets` |

Enam tabel itu cukup untuk menyusun peta perjalanan pelanggan berbasis data,
tetapi penafsirannya menjadi tahapan bisnis belum ditetapkan.

---

## Ringkasan yang belum tersedia

| Butir | Siapa yang memegang jawabannya |
|---|---|
| Customer journey map sisi penerima | Product manager / riset pengguna |
| Tahapan bisnis dari data lead | Pemilik produk |
| Harapan operator saat nomor keluar mendadak | Operasional |
