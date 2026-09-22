# Broadcast & WA Story

Dokumentasi teknis modul pengiriman massal dan status WhatsApp pada salesan.id.

---

## 1. Bentuk sistemnya

Satu campaign adalah satu pesan yang ditujukan ke satu audiens, dijadwalkan,
dikerjakan worker, lalu dilaporkan. Broadcast dan Story adalah objek yang sama
dengan cara pengantaran berbeda, jadi keduanya memakai satu tabel, satu
scheduler, satu set endpoint, dan satu halaman.

Perbedaannya hanya pada unit kerjanya:

| | Broadcast | WA Story |
|---|---|---|
| Unit kerja | penerima (`campaign_targets`) | perangkat (`story_publications`) |
| Audiens ditentukan oleh | kita | pengaturan privasi status di HP |
| Kunci idempotensi | `campaign_id + account_id + target_id` | `campaign_id + account_id` |
| Metrik hasil | terkirim / delivered / dibaca | terbit / views terdeteksi |

### Antrean hidup di basis data, bukan di memori

Tidak ada satu pun bagian dari antrean yang disimpan di proses. Scheduler
bertanya ke basis data apa yang jatuh tempo, mengklaim baris dengan
`for update skip locked`, dan memegangnya lewat *lease* yang kedaluwarsa
sendiri. Konsekuensinya:

- Backend restart di tengah campaign bukan insiden. Tick berikutnya mengambil
  campaign itu kembali, merekonsiliasi yang sempat menggantung, lalu lanjut.
- Dua worker yang berebut baris yang sama menghasilkan satu pemenang dan satu
  worker yang lewat begitu saja. Tidak perlu leader election.
- Browser boleh ditutup. Jadwal berjalan di server.

### Tidak boleh terkirim dua kali

Dijamin oleh basis data, bukan oleh kode:

```sql
create unique index uq_broadcast_attempt_success
  on public.broadcast_target_attempts (campaign_id, account_id, target_id)
  where status = 'sent';
```

Baris percobaan ditulis **sebelum** panggilan jaringan. Kalaupun worker mati,
retry salah, atau dua worker menabrak, pengiriman kedua ke orang yang sama
ditolak oleh constraint.

### Hasil yang tidak diketahui tidak dikirim ulang otomatis

Ini aturan yang paling mudah dilanggar dan paling mahal akibatnya. Setiap
pengiriman menulis baris pesan berstatus `pending` sebelum dikirim, lalu
menaikkannya ke `sent`. Saat merekonsiliasi campaign yang terputus:

- status pesan sudah lewat `pending` → terbukti terkirim, target ditandai
  **terkirim**;
- status masih `pending` → **tidak ada yang tahu**. Target ditandai gagal dengan
  `error_code = unknown_outcome` dan **tidak** dijadwalkan ulang. Laporannya
  menyebut ini apa adanya supaya orang memeriksa percakapannya dulu.

---

## 2. Migration

| Berkas | Isi |
|---|---|
| `0029_campaign_status_values.sql` | Nilai enum baru: `partial`, `expired`, plus tipe aktivitas `cancelled`, `expired`, `partially_published`. Berdiri sendiri karena PostgreSQL tidak mengizinkan nilai enum dipakai di transaksi yang menambahkannya. |
| `0030_broadcast_and_story_execution.sql` | Kolom eksekusi pada `content_campaigns` dan `campaign_targets`; tabel `broadcast_sender_devices`, `broadcast_target_attempts`, `campaign_labels`, `campaign_label_assignments`, `custom_variables`, `gpt_generation_logs`, `story_publications`, `story_views`, `story_view_snapshots`; kolom `messages.campaign_id`; RLS dan Realtime. |

Keduanya aditif: tidak ada kolom yang dihapus atau diganti nama, dan seluruhnya
memakai `if not exists` sehingga aman dijalankan ulang.

Menjalankan:

```bash
cd backend
go run ./cmd/migrate -status   # lihat yang tertunda
go run ./cmd/migrate           # terapkan
```

---

## 3. Endpoint

Semua di bawah `/api/v1`, semua memerlukan token Supabase, dan semua memeriksa
lingkup akses pemanggil di sisi server — bukan hanya menyembunyikan di frontend.

| Metode | Rute | Fungsi |
|---|---|---|
| GET | `/campaigns?type=&status=` | Daftar campaign dalam lingkup pemanggil |
| POST | `/campaigns` | Simpan campaign; opsional langsung dijadwalkan |
| POST | `/campaigns/preview` | Tinjauan: validasi, dedup, pembagian perangkat, preview pesan. Tidak menulis apa pun |
| GET | `/campaigns/audiences?account_ids=&kind=` | Kandidat penerima untuk perangkat terpilih |
| POST | `/campaigns/draft` | Draft GPT (hanya draft) |
| GET | `/campaigns/{id}` | Campaign, riwayat jadwal, jejak audit |
| GET | `/campaigns/{id}/source` | Isi campaign dalam bentuk yang dibaca composer. Dipakai tombol Duplikat dan Edit. Tidak menulis apa pun |
| PUT | `/campaigns/{id}` | Tulis ulang campaign yang **belum berjalan**, di baris yang sama. 409 kalau sudah berjalan |
| POST | `/campaigns/{id}/schedule` | Jadwalkan ulang |
| POST | `/campaigns/{id}/run` | Jalankan sekarang |
| POST | `/campaigns/{id}/cancel` | Batalkan (kooperatif) |
| POST | `/campaigns/{id}/retry` | Coba ulang **hanya** target gagal |
| GET | `/campaigns/{id}/report` | Laporan Broadcast atau Story |
| GET | `/campaigns/{id}/targets?status=` | Daftar penerima, berhalaman |
| PUT | `/campaigns/{id}/labels` | Ganti label internal campaign |
| DELETE | `/campaigns/{id}` | Hapus draft yang belum pernah dijadwalkan |
| GET/POST/PATCH | `/campaign-labels` | Label internal campaign |
| GET/POST/DELETE | `/custom-variables` | Variabel composer |

Berubah perilakunya: `POST /campaigns` kini menerima `account_ids`,
`target_source`, `numbers`, `group_ids`, `contact_ids`, `media_url`,
`delay_profile` dan `custom_values`. Bentuk lama (`account_id`, `target_jids`)
tidak dipakai frontend mana pun sebelum ini.

Endpoint analitik yang kembali dipakai: `/analytics/sla`,
`/analytics/follow-ups`.

---

## 4. Halaman dan komponen

| Berkas | Isi |
|---|---|
| `web/src/app/(app)/broadcast/page.tsx` | Halaman Broadcast |
| `web/src/app/(app)/story/page.tsx` | Halaman WA Story |
| `components/campaign/CampaignPage.tsx` | Daftar, filter status, aksi buat |
| `components/campaign/CampaignComposer.tsx` | Penyusun: perangkat, target, pesan, media, jadwal, profil jeda, tinjauan wajib, GPT |
| `components/campaign/CampaignDetail.tsx` | Laporan: rekap, hasil per perangkat, daftar penerima, publikasi Story, views |
| `components/campaign/CampaignSettings.tsx` | Label campaign dan variabel, dipasang di halaman Pengaturan |
| `components/campaign/shared.tsx` | Kosakata status, profil jeda, komponen form |

Menyusun campaign tidak bisa dilanjutkan sebelum **Tinjau** ditekan dan
berhasil. Tinjauan itu dihitung server dengan kode yang sama yang nanti menulis
barisnya, jadi angka yang disetujui adalah angka yang benar-benar dipakai.

---

## 5. Worker dan scheduler

Satu `campaign.Runner` per proses, dimulai di `cmd/server/main.go`.

```
scheduleLoop  ── tiap CAMPAIGN_POLL_INTERVAL (default 5 dtk)
   └─ ClaimDueCampaigns(owner, lease, limit)      -- for update skip locked
        └─ run(job)
             ├─ RecordActivity "execution_started"
             ├─ ReconcileStuckTargets              -- lihat §1
             ├─ mediafetch.Fetch (sekali per campaign, dihapus setelah selesai)
             ├─ Broadcast: per perangkat, paralel
             │    └─ ClaimTargets(limit 1) → render → BeginAttempt
             │       → SendCampaignMessage → FinishAttempt*  → jeda profil
             └─ Story: ClaimStoryPublications → PublishStory → MarkStory*
expiryLoop    ── tiap 10 menit: ExpireStories (snapshot final + status expired)
```

Catatan penting:

- **Perangkat mengirim paralel, satu perangkat mengirim berurutan.** Jeda profil
  baru bermakna kalau satu nomor mengirim satu per satu.
- **Jeda diundi ulang untuk tiap penerima** dalam rentang profil.
- **Tidak ada simulasi.** Tidak ada indikator mengetik, tidak ada read receipt
  palsu, tidak ada presence karangan. Satu-satunya jeda adalah profil antrean,
  dan antarmuka menyebutnya begitu — bukan sebagai jaminan keamanan akun.
- **Perangkat offline = jeda, bukan gagal.** Baris penerimanya tetap `pending`
  dan campaign diambil lagi pada tick berikutnya.
- **Pembatalan bersifat kooperatif**, dibaca di antara dua penerima. Yang sudah
  terkirim tetap terkirim — pesan WhatsApp tidak bisa ditarik kembali.
- Lease diperpanjang selama campaign berjalan, sehingga campaign berhari-hari
  pada profil Santai tidak kehilangan klaimnya.

### Keamanan unduhan media (`internal/mediafetch`)

Media dimasukkan sebagai URL, dan backend memang akan menghubungi alamat yang
dipilih pengguna. Pagarnya dipasang di level socket:

- hanya `https`, kredensial di URL ditolak;
- `net.Dialer.Control` memeriksa **alamat IP hasil resolusi** tepat sebelum
  connect, pada setiap hop — sehingga DNS rebinding tidak lolos;
- ditolak: loopback, `0.0.0.0`, link-local (termasuk `169.254.169.254`), RFC
  1918, CGNAT `100.64/10`, multicast, `240/4`, `198.18/15`, IPv6 unique-local,
  dan bentuk IPv4-mapped seperti `::ffff:127.0.0.1`;
- redirect dibatasi 3 hop, tiap hop divalidasi ulang;
- proxy dimatikan (proxy akan menyembunyikan alamat sebenarnya);
- ukuran dibatasi, isi disniff dan diklasifikasi ulang oleh `internal/media`
  (executable berkedok `.jpg` ditolak);
- berkas sementara dihapus di setiap jalur keluar.

Basis data hanya menyimpan URL, tipe, ukuran, dan hash. Tidak ada base64 dan
tidak ada biner.

---

## 6. Environment variable

Yang baru, semuanya opsional:

| Nama | Default | Arti |
|---|---|---|
| `CAMPAIGN_POLL_INTERVAL` | `5s` | Jeda scheduler menanyakan pekerjaan |
| `CAMPAIGN_LEASE` | `10m` | Lama klaim sebelum worker lain boleh mengambil |
| `CAMPAIGN_CONCURRENCY` | `4` | Campaign paralel per proses |
| `CAMPAIGN_STORY_CONCURRENCY` | `3` | Nomor yang menerbitkan Story bersamaan, seluruh proses |
| `CAMPAIGN_MEDIA_MAX_BYTES` | `67108864` | Batas unduhan media |
| `CAMPAIGN_MEDIA_TIMEOUT` | `2m` | Batas waktu unduhan |
| `CAMPAIGN_TEMP_DIR` | sistem | Lokasi berkas sementara |
| `OPENAI_API_KEY` | kosong | Tanpa ini, mode GPT melaporkan dirinya tidak tersedia; mode lain tetap jalan |
| `OPENAI_BASE_URL` | `https://api.openai.com` | |
| `OPENAI_MODEL` | `gpt-4o-mini` | |

`SUPABASE_SERVICE_ROLE_KEY` tetap khusus backend: tidak pernah masuk ke
frontend, log, atau variabel berprefiks `NEXT_PUBLIC_`.

---

## 7. Menjalankan test

```bash
cd backend
go vet ./...
go test ./...
go run ./cmd/analyticscheck    # menjalankan seluruh query terhadap DB nyata
```

```bash
cd web
npx tsc --noEmit
npx next lint
npx next build
```

`analyticscheck` bersifat read-only kecuali dua langkah yang sengaja menulis
lalu membersihkan sendiri (satu shift, dan satu draft campaign yang tidak pernah
dijadwalkan sehingga scheduler tidak bisa mengambilnya).

---

## 8. Keterbatasan whatsmeow yang diverifikasi langsung

Diperiksa pada modul yang benar-benar terpasang
(`go.mau.fi/whatsmeow@v0.0.0-20260904121843-28bfe537ea6a`), bukan diasumsikan
dari dokumentasi.

**Story bisa diterbitkan.** `SendMessage(ctx, types.StatusBroadcastJID, msg)`
bekerja; `broadcast.go` menunjukkan whatsmeow sendiri yang menyusun daftar
penerimanya lewat `getStatusBroadcastRecipients()` dari `GetStatusPrivacy()`.
Artinya audiens Story ditentukan oleh pengaturan privasi di HP, bukan oleh
aplikasi ini.

**Satu Story memakan menit, bukan detik.** Dengan privasi "Kontak saya",
audiensnya adalah semua kontak tersimpan nomor itu, dan whatsmeow mengenkripsi
kunci Story untuk setiap perangkat mereka satu per satu, setiap kali terbit.
Diukur di produksi: 10.000 kontak sekitar 6 menit, 22.000 kontak sekitar 12
menit, satu inti CPU penuh selama itu. Worker menerbitkan nomor-nomor dalam satu
campaign secara paralel (dibatasi `CAMPAIGN_STORY_CONCURRENCY`) dan memperpanjang
lease publikasi selama proses berjalan, sehingga Story ke sepuluh nomor selesai
dalam belasan menit, bukan dua jam. Restart backend di tengah proses membatalkan
kiriman yang sedang berjalan; ia diulang setelah lease habis. Cara memperkecil
biayanya ada di HP: daftar privasi Status "Hanya bagikan dengan…" mempersempit
audiens, dan whatsmeow mengikutinya.

**Komunitas dan grup khusus admin ditolak sebelum kirim.** Komunitas WhatsApp
(induk grup) ikut muncul di `GetJoinedGroups` dengan alamat `@g.us`, dan grup
bermode "hanya admin" tetap menganggap nomor kita anggota. Keduanya dijawab
server dengan error 420 saat kirim. Keduanya disimpan dari info grup
(`group_is_community`, `group_announce` bersama `self_is_admin`), pratinjau
Broadcast melaporkannya sebagai masalah dengan alasan tertulis, dan error 420
saat kirim dicatat final pada percobaan pertama.

**Tidak ada API untuk membaca penonton Story.** Tidak ada padanan daftar penonton
yang ditampilkan aplikasi HP. Satu-satunya sinyal nyata adalah `events.Receipt`
bertipe `read` atau `played` dengan `Chat = status@broadcast`. Karena itu:

- angka views adalah **batas bawah**, bukan jumlah penonton;
- penonton yang mematikan read receipt tidak akan pernah muncul;
- receipt yang datang saat backend mati tidak diputar ulang;
- satu viewer per publikasi dijamin oleh `unique (publication_id, viewer_jid)`,
  sehingga receipt berulang tidak menambah angka;
- setelah 24 jam disimpan snapshot final, supaya laporan Story yang sudah habis
  tidak terus bergerak.

Antarmuka menampilkan kalimat ini di bawah setiap angka views:

> Angka berdasarkan receipt yang berhasil diterima sistem. Sebagian penonton
> mungkin tidak terhitung.

Sebelum ada publikasi sama sekali, yang ditampilkan adalah **Data views belum
tersedia** — bukan angka nol yang akan dibaca sebagai hasil pengukuran.

**Dua angka yang berbeda.** `Total views per perangkat` adalah jumlah lintas
publikasi (satu orang yang menonton di dua nomor kita memang menonton dua
Story). `Viewer unik campaign` dideduplikasi berdasarkan identitas penonton.
Keduanya diberi nama berbeda supaya tidak tertukar.

**Polling.** Tidak ada. Views hanya bertambah dari receipt yang benar-benar
datang.

---

## 9. Pemetaan istilah

Nama internal dipertahankan; istilah UI dipetakan di atasnya.

| Internal | UI |
|---|---|
| `content_campaigns` | Broadcast / WA Story |
| `campaign_targets` | Penerima |
| `broadcast_sender_devices` | Nomor pengirim |
| `story_publications` | Rincian per nomor |
| `story_views` | Penonton terdeteksi |
| `story_view_snapshots` | Angka penonton yang sudah final |
| `status_view_counts` | satu-satunya sumber angka penonton untuk semua layar |
| `campaign_labels` | Label Broadcast |
| `custom_variables` | Variabel pesan |
| status `partial` | Selesai Sebagian (Broadcast) / Sebagian (Story) |
| status `expired` | Kedaluwarsa |

Kata dan warnanya ada di satu tempat, `web/src/components/campaign/shared.tsx`:
`CAMPAIGN_STATUS` (status keseluruhan), `TARGET_STATUS` (per penerima Broadcast),
`PUBLICATION_STATUS` (per nomor Story). Warnanya membawa arti yang sama di mana
pun: biru menunggu, kuning sedang berjalan, hijau berhasil, merah gagal, abu
draf/batal/lewat masanya.
| `sender_source = 'broadcast'` | tidak masuk Pesan Terkirim, Kontak Terlayani, SLA, follow-up |

---

## 10. Yang masih perlu dikerjakan

- **Halaman pengaturan SLA belum ada.** Target respons masih berasal dari
  `SLA_TARGET_SECONDS` di environment, dan belum bisa di-override per aplikasi
  atau dibedakan jam kerja/luar jam kerja lewat antarmuka. Snapshot target sudah
  disimpan per siklus, jadi menambahkan layarnya nanti tidak akan mengubah
  angka historis.
- **Polling belum ada untuk Broadcast berjenis polling.** Belum diverifikasi
  apakah versi whatsmeow ini mendukung poll pada broadcast, jadi belum dibuat.
- **`internal/wa/humanize.go` mensimulasikan mengetik dan membaca pada jalur
  chat manual.** Broadcast dan Story tidak memakainya sama sekali. Untuk chat,
  perilaku itu sudah ada sebelum dokumen ini dan tidak dicabut sepihak — lihat
  catatan risiko di laporan implementasi.
- **Uji end-to-end dengan pengiriman nyata belum dijalankan**, karena akan
  mengirim pesan ke nomor sungguhan.
- **Hierarki belum diuji dengan orang sungguhan** karena
  `SUPABASE_SERVICE_ROLE_KEY` masih kosong sehingga akun PIC/Freelance belum
  bisa dibuat.
