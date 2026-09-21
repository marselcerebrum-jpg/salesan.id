# salesan.id — Omnichannel MVP (WhatsApp)

Web app untuk mengelola banyak akun WhatsApp dan membalas percakapan pelanggan
dari satu inbox. Tahap ini **hanya WhatsApp**; Instagram, TikTok, Broadcast,
Chatbot, dan Auto Follow Up sengaja belum dikembangkan (tampil sebagai
`COMING SOON` di sidebar agar layout sesuai referensi).

| Lapisan | Teknologi |
| --- | --- |
| Frontend | Next.js 15 (App Router) · TypeScript · Tailwind CSS v4 · SWR |
| Backend | Go 1.25+ · chi · pgx v5 |
| WhatsApp | [whatsmeow](https://github.com/tulir/whatsmeow) (WhatsApp Web multi-device) |
| Database | Supabase PostgreSQL (RLS aktif) |
| Realtime | WebSocket dari backend Go + Supabase Realtime di level tabel |
| Auth | Supabase Auth (JWT diverifikasi backend, HS256 & JWKS) |

---

## Daftar isi

1. [Arsitektur](#arsitektur)
2. [Struktur folder](#struktur-folder)
3. [Prasyarat](#prasyarat)
4. [Setup 1 — Supabase](#setup-1--supabase)
5. [Setup 2 — Backend](#setup-2--backend)
6. [Setup 3 — Frontend](#setup-3--frontend)
7. [Menjalankan dengan Docker](#menjalankan-dengan-docker)
8. [Alur uji end-to-end](#alur-uji-end-to-end)
9. [Referensi API](#referensi-api)
10. [Event realtime](#event-realtime)
11. [Catatan keamanan](#catatan-keamanan)
12. [Verifikasi (lint, type-check, test, build)](#verifikasi)
13. [Batasan tahap MVP](#batasan-tahap-mvp)

---

## Arsitektur

```
┌─────────────────┐        REST + WebSocket        ┌──────────────────────────┐
│  Next.js (web)  │ ──────────────────────────────▶│      Go API (backend)    │
│                 │◀────────── event push ─────────│                          │
│  Supabase Auth  │                                │  httpapi  → handler HTTP │
└────────┬────────┘                                │  wa       → whatsmeow    │
         │ access token (JWT)                      │  realtime → hub WebSocket│
         │                                         │  repository → SQL        │
         ▼                                         └────────┬─────────────────┘
   Supabase Auth                                            │ pgx
                                                            ▼
                                              ┌───────────────────────────────┐
                                              │   Supabase PostgreSQL         │
                                              │   • tabel aplikasi (RLS)      │
                                              │   • tabel whatsmeow_* (sesi)  │
                                              └───────────────────────────────┘
                                                            ▲
                                                            │ soket per akun
                                              ┌─────────────┴─────────────────┐
                                              │  WhatsApp Web multi-device    │
                                              └───────────────────────────────┘
```

Prinsip pemisahan:

- **UI** (`web/src/app`, `web/src/components`) tidak pernah menyentuh database
  langsung. Supabase di browser dipakai **hanya untuk auth**.
- **API** (`backend/internal/httpapi`) tipis: parse input → delegasi → serialisasi.
- **WhatsApp client manager** (`backend/internal/wa`) memegang semua sesi
  whatsmeow, satu `Session` per `account_id`.
- **Event handler** (`backend/internal/wa/events.go`) satu handler per sesi,
  menangani pesan masuk, receipt, status koneksi, QR, dan history sync.
- **Repository** (`backend/internal/repository`) satu-satunya tempat SQL ditulis.
- **Realtime service** (`backend/internal/realtime`) hub WebSocket, fan-out
  per `workspace_id`.

### Kenapa QR lewat WebSocket sendiri, bukan Supabase Realtime

Kode QR berputar setiap ~30 detik dan **tidak pernah masuk database** — jadi
tidak ada perubahan baris untuk di-*subscribe*. Hub WebSocket Go dipakai sebagai
kanal realtime utama (QR, status koneksi, pesan baru, receipt). Tabel
`conversations`, `messages`, dan `whatsapp_accounts` tetap didaftarkan ke
publication `supabase_realtime` (migrasi 0001) bila nanti ingin dipakai.

---

## Struktur folder

```
SALESANv2/
├── docker-compose.yml            # backend saja (DB memakai Supabase terkelola)
├── backend/
│   ├── Dockerfile
│   ├── .env.example
│   ├── cmd/server/main.go        # bootstrap + graceful shutdown
│   ├── migrations/
│   │   ├── 0001_init.sql         # schema, index, trigger, RLS, realtime
│   │   └── 0002_seed.sql         # data dummy untuk preview
│   └── internal/
│       ├── auth/                 # verifikasi JWT Supabase (HS256 + JWKS)
│       ├── config/               # env loader
│       ├── db/                   # pool pgx
│       ├── httpapi/              # router, middleware, handler
│       ├── models/               # tipe yang dikirim ke frontend
│       ├── realtime/             # hub WebSocket
│       ├── repository/           # semua SQL, selalu scoped workspace
│       └── wa/                   # manager, session, events, sync, send
└── web/
    ├── .env.example
    └── src/
        ├── app/
        │   ├── login/            # sign in / sign up
        │   └── (app)/
        │       ├── accounts/     # gambar 1
        │       ├── chat/         # gambar 4
        │       ├── chat/[applicationId]/            # gambar 5
        │       ├── chat/[applicationId]/[accountId]/# gambar 6
        │       ├── contacts/
        │       └── dashboard/
        ├── components/{accounts,chat,layout,ui}
        ├── lib/{api,types,format,realtime,supabase}
        └── middleware.ts         # refresh sesi + proteksi rute
```

---

## Prasyarat

- **Go** 1.25 atau lebih baru
- **Node.js** 20 atau lebih baru
- **Project Supabase** (gratis pun cukup)
- Docker hanya bila memakai `docker compose`

---

## Setup 1 — Supabase

### a. Jalankan migrasi

Isi `backend/.env` lebih dulu (Setup 2), lalu satu perintah:

```bash
cd backend
go run ./cmd/migrate            # terapkan semua migrasi yang belum jalan
go run ./cmd/migrate -status    # lihat mana yang sudah/belum
```

Runner menerapkan berkas `migrations/*.sql` berurutan dan mencatatnya di
`public.schema_migrations`, jadi menjalankannya ulang aman dan murah. Checksum
tiap berkas ikut disimpan: kalau sebuah migrasi diedit setelah terlanjur jalan,
runner memberitahu alih-alih diam saja.

| Berkas | Isi |
| --- | --- |
| `0001_init.sql` | schema, index, trigger, RLS, realtime |
| `0002_seed.sql` | **data dummy, opsional** — lihat di bawah |
| `0003_sync.sql` | kolom sinkronisasi: status baca, jendela 7 hari, peta label |
| `0004_clean_display_names.sql` | buang karakter Unicode tak terlihat dari nama |
| `0005_no_seeded_labels.sql` | hentikan penyemaian label bawaan |

Berkas berakhiran `_seed.sql` adalah **data**, bukan schema: tidak pernah
diterapkan otomatis dan tidak dicatat, karena memang dirancang untuk diulang.
Jalankan hanya bila ingin isi contoh, dan **setelah** mendaftar user pertama:

```bash
go run ./cmd/migrate -seed
```

Lebih suka lewat dashboard? Buka **SQL Editor** Supabase dan tempel isi tiap
berkas berurutan (`0001` → `0003` → `0004` → `0005`). Hasil akhirnya sama,
hanya tanpa pencatatan `schema_migrations`.

`0001_init.sql` memasang trigger pada `auth.users`, sehingga **setiap user baru
otomatis mendapat workspace, profil, dan 8 aplikasi awal**. Label sengaja tidak
disemai — lihat catatan di bawah.

> **Label tidak pernah dikarang.** Label hanya lahir dari dua sumber: hasil
> sinkronisasi WhatsApp Business, atau dibuat sendiri lewat aplikasi. Menyemai
> nama contoh akan mengisi baris filter inbox dengan tag yang tidak ada
> padanannya di HP mana pun.

### b. Yang dibuat migrasi

| Tabel | Isi |
| --- | --- |
| `workspaces` | batas tenancy |
| `users` | profil aplikasi, cermin `auth.users`, memegang `workspace_id` |
| `applications` | klasifikasi JADIASN / JADIBUMN / … |
| `whatsapp_accounts` | satu baris per perangkat tertaut |
| `whatsapp_sessions` | metadata sesi (device JID, push name, platform) |
| `contacts` | buku alamat per akun |
| `conversations` | thread chat, status Baru/Diproses/Selesai, unread |
| `conversation_members` | peserta grup |
| `messages` | pesan, unik pada `(account_id, wa_message_id)` |
| `conversation_labels` | tag percakapan, dari WhatsApp Business atau dibuat sendiri |
| `conversation_label_assignments` | relasi tag ↔ percakapan |
| `whatsapp_label_map` | peta id label WhatsApp (per akun) ↔ tag kita |
| `conversations.pn_jid` | bentuk nomor telepon dari sebuah chat — penyatu baris `@lid` dan `@s.whatsapp.net` milik orang yang sama |
| `message_attachments` | metadata berkas: nama, tipe, ukuran, dimensi, letak di Storage |
| `whatsapp_media_refs` | bahan unduh media (kunci + direct path) — server-only, RLS tanpa policy |
| `message_polls` | pertanyaan polling |
| `message_poll_options` | pilihan + SHA-256 teksnya, yang dipakai WhatsApp untuk menamai suara |
| `message_poll_votes` | satu baris per (pemilih, pilihan) |
| `schema_migrations` | catatan migrasi yang sudah diterapkan |

Berkas media **tidak pernah** disimpan di database. Yang tersimpan hanya
metadata dan letaknya di bucket privat Supabase Storage.

Tabel `whatsmeow_*` dibuat otomatis oleh whatsmeow saat backend pertama kali
jalan; di situlah kredensial WhatsApp disimpan.

### c. Ambil kredensial

- **Project Settings → Database → Connection string → URI** → `DATABASE_URL`
- **Project Settings → API** → `Project URL` dan `anon public key`
- **Project Settings → API keys → `service_role`** → `SUPABASE_SERVICE_ROLE_KEY`

`service_role` melewati RLS. Ia hanya boleh ada di `backend/.env` — jangan
pernah masuk ke `web/.env.local` atau variabel apa pun berawalan
`NEXT_PUBLIC_`, karena semua yang berawalan itu ikut terkirim ke browser.

### d. Penyimpanan media

Media **selalu berjalan**. Yang berubah hanya tempat berkasnya:

| Kondisi | Tempat | Catatan |
| --- | --- | --- |
| `SUPABASE_SERVICE_ROLE_KEY` diisi | Bucket privat `wa-media` | Dianjurkan. Bucket dibuat otomatis saat backend start. |
| Kunci itu kosong | Folder `MEDIA_DIR` (default `backend/.media`) | Jalan penuh, tanpa setup. Tidak tahan jika mesinnya hilang. |

Keduanya memakai model keamanan yang sama, dan itulah yang penting: berkas tidak
pernah publik, browser tidak pernah menerima URL permanen, dan setiap tautan
punya masa berlaku (default 1 jam). Pada mode lokal tautannya ditandatangani
HMAC dan dilayani oleh backend sendiri di `/api/v1/media/{token}` — rute itu
sengaja di luar autentikasi header, karena tag `<img>` tidak bisa mengirim
header `Authorization`; tanda tangan di URL itulah kredensialnya, persis seperti
signed URL Supabase.

Ganti ke Supabase kapan saja cukup dengan mengisi kuncinya lalu restart. Berkas
lama tetap di folder lokal; yang baru masuk ke bucket.

---

## Setup 2 — Backend

```bash
cd backend
cp .env.example .env      # lalu isi DATABASE_URL dan SUPABASE_URL
go mod download
go run ./cmd/server
```

Server siap di `http://localhost:8080`. Cek `curl http://localhost:8080/health`.

### Catatan `DATABASE_URL`

Pakai **connection string langsung** atau pooler *session mode* (port `5432`).
Bila terpaksa memakai pooler *transaction mode* (port `6543`), tambahkan:

```
?default_query_exec_mode=simple_protocol
```

karena PgBouncer mode transaksi tidak bisa menyimpan prepared statement. Backend
otomatis membuang parameter khusus pgx tersebut sebelum meneruskan DSN ke
whatsmeow (yang memakai `lib/pq`).

### Catatan verifikasi JWT

Cukup isi `SUPABASE_URL`; backend menurunkan endpoint JWKS sendiri, sehingga
project dengan kunci asimetris (ES256/RS256) langsung jalan. Untuk project lama
yang masih memakai *shared secret*, isi `SUPABASE_JWT_SECRET`. Mengisi keduanya
aman — algoritma di dalam token yang menentukan kunci mana yang dipakai.

---

## Setup 3 — Frontend

```bash
cd web
cp .env.example .env.local   # isi URL + anon key Supabase, dan NEXT_PUBLIC_API_URL
npm install
npm run dev
```

Buka `http://localhost:3000`. Halaman akan mengarahkan ke `/login`; buat akun
lewat tab **Daftar**. Bila konfirmasi email aktif di Supabase, cek inbox lebih
dulu, atau matikan sementara di **Authentication → Providers → Email →
Confirm email**.

---

## Menjalankan dengan Docker

```bash
cp backend/.env.example backend/.env   # isi dulu
docker compose up --build
```

Compose hanya menjalankan backend — database memakai Supabase terkelola.
Container menangani `SIGTERM`: berhenti menerima request, menutup seluruh soket
whatsmeow, lalu mengosongkan hub realtime (`stop_grace_period: 45s`).

---

## Alur uji end-to-end

1. **Tambah akun** — buka `/accounts` → **+ Tambah Akun** → isi nama (opsional)
   dan pilih aplikasi → **Tambah Akun**.
2. **Scan QR** — modal QR terbuka otomatis. QR berputar sendiri saat kedaluwarsa;
   tombol **Refresh QR** memaksa sesi pairing baru.
   Pindai lewat WhatsApp → **Titik tiga (⋮) → Perangkat Tertaut → Tautkan Perangkat**.
3. **Status berubah tanpa reload** — begitu perangkat tertaut, status kartu
   menjadi **Terhubung** melalui event `account.status`; nomor WA dan JID terisi.
4. **Terima pesan** — kirim pesan dari HP lain ke nomor tersebut. Pesan tersimpan
   dan langsung muncul di inbox lewat event `message.new`.
5. **Buka chat** — `/chat` → pilih aplikasi → pilih nomor → **Masuk**. Pilih
   percakapan di panel kiri; unread langsung ter-reset.
6. **Balas** — ketik di composer, tekan Enter. Status pesan bergerak
   `pending → sent → delivered → read` mengikuti receipt dari WhatsApp.
7. **Multi akun** — ulangi langkah 1–2 untuk nomor kedua; keduanya aktif
   bersamaan karena setiap sesi terisolasi per `account_id`.
8. **Sinkron** — tombol **Sinkron** di header inbox menarik kontak dan grup,
   lalu meminta HP mengirim ulang riwayat chat (datang asinkron sebagai
   `HistorySync`).

---

## Referensi API

Semua endpoint di bawah `/api/v1` dan butuh header
`Authorization: Bearer <supabase_access_token>`.

**Kirim media** memakai satu berkas per permintaan — itulah yang membuat progress
dan “coba lagi” berlaku untuk satu berkas, bukan satu tumpukan. Field-nya:
`file`, `caption`, `as_document`, dan `client_token` (kunci idempotensi: mengulang
dengan token yang sama memakai baris pesan yang sudah ada, bukan membuat yang
kedua). Batas ukuran: foto 16 MB, video 64 MB, audio 16 MB, dokumen 100 MB.

| Metode | Path | Fungsi |
| --- | --- | --- |
| `GET` | `/me` | profil + workspace |
| `GET` | `/applications` | daftar aplikasi + agregat |
| `POST` | `/applications` | tambah aplikasi |
| `PATCH` | `/applications/{id}` | ubah aplikasi |
| `DELETE` | `/applications/{id}` | hapus aplikasi |
| `GET` | `/applications/{id}/accounts` | nomor pada satu aplikasi (gambar 5) |
| `GET` | `/accounts` | grid akun; filter `application_id`, `unassigned`, `method`, `search` |
| `GET` | `/accounts/stats` | counter header |
| `POST` | `/accounts` | tambah akun |
| `GET·PATCH·DELETE` | `/accounts/{id}` | detail / ubah / hapus |
| `POST` | `/accounts/{id}/pair` | mulai pairing, balas QR pertama |
| `GET` | `/accounts/{id}/qr` | QR terbaru yang tersimpan |
| `POST` | `/accounts/{id}/connect` | sambung ulang (atau pairing bila belum pernah) |
| `POST` | `/accounts/{id}/disconnect` | putuskan, perangkat tetap tertaut |
| `POST` | `/accounts/{id}/logout` | lepas perangkat dari HP |
| `POST` | `/accounts/{id}/sync` | sinkron kontak, grup, minta riwayat |
| `GET` | `/accounts/{id}/conversations` | inbox; filter `type`, `status`, `unread`, `mentions`, `label_id`, `search` |
| `GET` | `/accounts/{id}/conversations/counts` | angka pada chip filter |
| `GET·PATCH` | `/conversations/{id}` | detail / ubah status |
| `GET` | `/conversations/{id}/members` | anggota grup dengan nama yang sudah disusun |
| `POST` | `/conversations/{id}/members` | `action`: `promote` / `demote` / `remove` — hanya admin |
| `PATCH` | `/conversations/{id}/group` | ubah nama atau deskripsi grup |
| `POST` | `/conversations/{id}/group/refresh` | baca ulang info grup dari WhatsApp |
| `GET` | `/conversations/{id}/first-mention` | pesan mention terawal yang belum ditengok, untuk di-scroll |
| `DELETE` | `/conversations/{id}` | hapus obrolan dari web; `?clear=true` hanya mengosongkan isinya |
| `PATCH` | `/messages/{id}` | edit teks pesan, atau caption foto/video/dokumen (batas 15 menit) |
| `POST` | `/messages/{id}/forward` | teruskan ke maksimal 20 percakapan; balasan menyebut mana yang gagal |
| `DELETE` | `/messages/{id}` | `?scope=everyone` hapus untuk semua, `?scope=me` hapus dari web ini saja |
| `POST` | `/conversations/{id}/read` | tandai sudah dibaca |
| `GET·POST` | `/conversations/{id}/messages` | riwayat / kirim teks; `reply_to` mengutip pesan di thread yang sama |
| `POST` | `/conversations/{id}/media` | kirim satu berkas (`multipart/form-data`) |
| `POST` | `/conversations/{id}/polls` | buat polling |
| `POST` | `/messages/{id}/vote` | beri suara; body `options` adalah pilihan lengkap, bukan perubahan |
| `GET` | `/attachments/{id}/url` | signed URL berumur pendek; `?download=true` untuk unduh |
| `POST` | `/conversations/{id}/labels` | pasang label |
| `DELETE` | `/conversations/{id}/labels/{labelID}` | lepas label |
| `GET·POST` | `/labels` | daftar / tambah label |
| `DELETE` | `/labels/{id}` | hapus label |
| `GET` | `/contacts` | buku alamat |
| `GET` | `/ws?token=…` | WebSocket realtime |
| `GET` | `/health` | health check (tanpa auth) |

---

## Event realtime

Amplop tetap: `{ "type": …, "payload": …, "timestamp": … }`.

| `type` | Kapan | Isi `payload` |
| --- | --- | --- |
| `account.qr` | whatsmeow mengeluarkan kode QR baru | `account_id`, `qr`, `expires_in`, `expired` |
| `account.status` | koneksi berubah | `account_id`, `status`, `status_detail`, `account?` |
| `account.deleted` | akun dihapus | `account_id` |
| `message.new` | pesan masuk / keluar tersimpan | `account_id`, `message` |
| `message.status` | receipt diterima | `account_id`, `changes[]` |
| `conversation.updated` | preview, unread, status, atau label berubah | objek `Conversation` |
| `sync.progress` | sinkron kontak/grup/riwayat selesai | `account_id`, `phase`, `result` |

Browser tidak bisa memasang header pada handshake WebSocket, jadi token dikirim
lewat query `?token=`. Origin handshake dicek terhadap `ALLOWED_ORIGINS` karena
WebSocket tidak tunduk pada CORS.

---

## Catatan keamanan

- **Kredensial WhatsApp tidak pernah sampai ke browser.** Kunci Noise/Signal
  hanya ada di tabel `whatsmeow_*`. Tabel `whatsapp_sessions` mengaktifkan RLS
  **tanpa policy untuk role `authenticated`**, jadi browser mendapat nol baris;
  hanya service role (backend) yang bisa membacanya. API hanya memaparkan
  status, JID, dan nomor telepon.
- **RLS aktif di semua tabel.** Fungsi `public.current_workspace_id()`
  (`SECURITY DEFINER`) memetakan `auth.uid()` ke workspace, dan setiap policy
  menyaring `workspace_id = current_workspace_id()`.
- **Scoping ganda.** Backend terhubung sebagai service role (melewati RLS),
  karena itu setiap method di `repository` selalu menerima `workspace_id` dan
  memasukkannya ke `WHERE`. Jalur browser dilindungi RLS, jalur API dilindungi
  kode — keduanya tertutup.
- **Idempotensi pesan.** `UNIQUE (account_id, wa_message_id)` adalah kunci
  idempotensi. Pesan yang diputar ulang saat reconnect atau history sync
  ditolak `ON CONFLICT DO NOTHING`, sehingga unread tidak pernah dihitung dobel.
- **Receipt tidak bisa mundur.** `AdvanceMessageStatus` hanya menaikkan
  peringkat status, jadi `delivered` yang datang terlambat tidak menimpa `read`.
- **Nama pengirim disusun saat dibaca, bukan dibekukan.** Urutannya: nama
  kontak tersimpan → push name → nama yang ikut saat pesan tiba → nomor telepon
  → alamatnya. Karena disusun saat dibaca, mengganti nama kontak di HP langsung
  terlihat pada pesan-pesan lama juga; membekukannya akan meninggalkan nama
  lama selamanya.
- **Mention dibaca dari metadata WhatsApp, bukan dari teks.** Teks hanya apa
  yang dipilih klien pengirim untuk ditampilkan; `mentioned_jids` adalah
  faktanya. `mentions_me` dihitung per akun terhadap **kedua** alamat akun
  (nomor telepon dan LID) — satu grup bisa berisi beberapa nomor kita, dan
  mencocokkan satu bentuk saja akan melewatkan sebagian besar grup di sini.
- **Mention punya penghitung sendiri.** `mention_count` terpisah dari
  `unread_count`, dan selalu dihitung ulang lewat `refresh_mention_count()`,
  tidak pernah dinaikkan dari beberapa tempat — penghitung yang disenggol dari
  tiga tempat berbeda adalah penghitung yang melenceng.
- **Pengiriman dibuat menyerupai orang.** Setiap pesan keluar melewati urutan
  baca → online → mengetik → berhenti → kirim, dengan lama mengetik menyesuaikan
  panjang pesan dan diberi variasi acak, ditambah jeda minimum antar pesan yang
  diberlakukan per akun (kunci ditahan melintasi pengiriman, sehingga dua
  operator sekaligus tetap antre). Ini bukan hiasan: akun yang membalas seketika
  tanpa indikator mengetik akan dibatasi lalu diblokir WhatsApp, dan yang kena
  adalah nomor bisnis sungguhan. Lihat `internal/wa/humanize.go` dan setelan
  `WA_HUMANIZE` di `.env.example`.
- **Media disimpan di bucket privat.** Tidak ada base64 atau binary di database.
  Browser tidak pernah menerima URL permanen maupun kunci Storage — hanya signed
  URL berumur pendek yang diterbitkan per permintaan, setelah backend memeriksa
  bahwa berkas itu milik workspace pemanggil (`workspace_id` masuk ke `WHERE`,
  sehingga berkas tenant lain terbaca sebagai *tidak ditemukan*).
- **Kunci media setara kredensial.** `media_key` bisa mendekripsi berkas dari
  server WhatsApp, jadi ia tinggal di `whatsapp_media_refs` — RLS aktif tanpa
  policy untuk `authenticated`, pola yang sama dengan `whatsapp_sessions`.
- **Berkas unggahan dianggap bermusuhan.** Tipe berkas ditentukan dari byte
  awalnya sendiri, bukan dari nama atau `Content-Type` yang dikirim browser.
  Berkas program ditolak berdasarkan tanda tangannya (MZ, ELF, Mach-O, `#!`),
  di luar daftar ekstensi terlarang. Nama berkas disanitasi menjadi satu segmen
  (`../`, `..\`, path absolut, karakter kontrol, dan *bidi override* dibuang),
  dan letak berkas di bucket sepenuhnya diturunkan dari id yang dikendalikan
  server — nama unggahan tidak pernah ikut menentukan path.
- **Berkas sementara tidak berumur panjang.** Unggahan ditulis ke temp file dan
  dihapus saat permintaan selesai, termasuk pada jalur error.

---

## Verifikasi

```bash
# Backend
cd backend
gofmt -l .        # harus kosong
go vet ./...
go test ./...
go build ./...

# Frontend
cd web
npm run lint
npm run typecheck
npm run build:check   # bukan `npm run build` — lihat catatan di bawah
```

> **Jangan jalankan `npm run build` selagi `npm run dev` hidup.** Keduanya
> memakai `.next`, dan build produksi menimpa manifest yang sedang dipegang dev
> server. Dev server tidak menyadarinya — ia hanya menjawab **500 Internal
> Server Error** di setiap halaman sampai `.next` dihapus dan dev dijalankan
> ulang. `npm run build:check` melakukan build yang sama ke `.next-check`,
> sehingga aman dipakai untuk verifikasi kapan pun.
>
> Kalau terlanjur: hentikan dev server, `Remove-Item -Recurse -Force .next`,
> lalu `npm run dev` lagi.

Status pada commit ini — semuanya lolos:

```
backend  gofmt: bersih · vet: bersih · test: ok (auth, config, repository, wa) · build: ok
web      eslint: 0 error · tsc --noEmit: 0 error · next build: 9 route ter-generate
```

---

## Batasan tahap MVP

Disengaja, sesuai ruang lingkup:

- **Media dihapus setelah 7 hari.** Penyapu berjalan tiap jam dan membuang
  berkas yang pesannya sudah lewat jendela sinkron — dari bucket, berikut
  thumbnail dan bahan unduhnya, sehingga benar-benar tidak bisa ditarik lagi.
  Barisnya tetap ada supaya percakapan tidak berlubang; bubble-nya berbunyi
  “sudah kedaluwarsa”.
- **Media riwayat diunduh saat dibuka, bukan saat sinkron.** Bubble langsung
  tampil dari thumbnail bawaan WhatsApp; berkas penuhnya baru ditarik ketika
  ada yang membukanya. Ini disengaja: menarik seluruh media tujuh hari akan
  menukar sinkron yang cepat dengan yang lambat tanpa diminta.
- **Media yang terlalu tua bisa hilang lebih dulu.** WhatsApp menghapus berkas
  dari servernya setelah sekitar dua minggu bila belum pernah diunduh; yang
  seperti itu dilaporkan sebagai “tidak tersedia lagi”, bukan sebagai kegagalan.
- **Suara polling hanya bisa dibaca untuk polling yang tersimpan.** Suara
  dienkripsi dengan kunci yang diturunkan dari pesan pollingnya, jadi polling
  yang dibuat sebelum jendela sinkron tidak bisa dihitung — itu dilaporkan di
  log, bukan ditebak.
- **Belum ada** Instagram, TikTok, Broadcast, WA Story, Chatbot, Auto Follow Up,
  dan Fetch Grup — tampil `COMING SOON` di sidebar.
- **WABA** baru ada sebagai klasifikasi dan penghitung tab; jalur koneksinya
  belum diimplementasikan (`connection_method = 'waba'` menolak pairing QR).
- **Batas paket** (10 perangkat, 5 WABA) masih konstanta di
  `repository.AccountStats`; pindahkan ke baris workspace saat billing masuk.
- **Satu proses** memegang semua sesi. Untuk menjalankan beberapa replika,
  perlu penugasan akun per instance agar dua proses tidak membuka soket untuk
  akun yang sama.
- Riwayat chat bergantung pada **HistorySync** dari HP; WhatsApp membatasi
  seberapa jauh ke belakang riwayat dikirim.
