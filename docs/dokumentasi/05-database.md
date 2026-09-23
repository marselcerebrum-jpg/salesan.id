# 5. Database Documentation

Dibaca dari basis data produksi pada 23 September 2026 dan dari 67 berkas
migration di `backend/migrations/`.

## 5.1 Database architecture

| Aspek | Nilai |
|---|---|
| Mesin | PostgreSQL, dijalankan sebagai bagian Supabase self-hosted |
| Lokasi | VPS yang sama dengan aplikasi, kontainer `supabase-db` |
| Schema | `public` (aplikasi dan whatsmeow), `auth` dan `storage` (Supabase) |
| `max_connections` | 100 |
| Jatah koneksi backend | 16 untuk aplikasi, 16 untuk whatsmeow |
| Jumlah tabel aplikasi | 48 |
| Jumlah tabel whatsmeow | 17 |
| Migrasi | Bernomor urut, dijalankan `cmd/migrate`, dicatat di `schema_migrations` |

Dua kelompok tabel hidup berdampingan di schema yang sama:

```mermaid
flowchart LR
    subgraph public
        direction TB
        A["Tabel aplikasi — 48<br/>dimiliki dan dimigrasikan proyek ini"]
        B["Tabel whatsmeow_* — 17<br/>dibuat dan dikelola pustaka whatsmeow"]
    end
    C[Supabase Auth<br/>schema auth] -.->|user id| A
    D[Supabase Storage<br/>schema storage] -.->|berkas media| A
```

Tabel `whatsmeow_*` **bukan milik proyek ini**. Pustaka whatsmeow membuat dan
memutakhirkannya sendiri saat perangkat pertama tersambung. Migrasi proyek
hanya boleh menambahkan indeks di atasnya, tidak mengubah strukturnya. Migration
`0064` melakukan tepat itu, dan keberadaan tabelnya diperiksa dulu karena pada
basis data baru tabel-tabel itu belum ada.

## 5.2 ERD

### Inti: workspace, aplikasi, nomor, percakapan

```mermaid
erDiagram
    workspaces ||--o{ applications : memiliki
    workspaces ||--o{ users : memiliki
    workspaces ||--o{ whatsapp_accounts : memiliki
    applications ||--o{ whatsapp_accounts : "dikelompokkan ke"
    whatsapp_accounts ||--o{ conversations : memiliki
    whatsapp_accounts ||--o{ contacts : memiliki
    whatsapp_accounts ||--o{ conversation_labels : memiliki
    contacts ||--o{ conversations : "dikenali sebagai"
    conversations ||--o{ messages : memuat
    conversations ||--o{ conversation_members : "beranggotakan"
    conversations ||--o{ conversation_label_assignments : diberi
    conversation_labels ||--o{ conversation_label_assignments : dipakai
    messages ||--o{ message_attachments : membawa
    messages ||--o{ message_reactions : menerima
    messages ||--o{ message_polls : "berupa"
    contacts ||--o{ conversation_members : "adalah"

    workspaces {
        uuid id PK
        text name
    }
    applications {
        uuid id PK
        uuid workspace_id FK
        text code
        text name
    }
    whatsapp_accounts {
        uuid id PK
        uuid workspace_id FK
        uuid application_id FK
        text phone_number
        text jid
        text lid
        enum status
    }
    conversations {
        uuid id PK
        uuid account_id FK
        uuid contact_id FK
        text chat_jid
        text pn_jid
        enum type
        bool is_archived
        bool group_is_member
        bool group_is_community
        bool group_announce
        bool self_is_admin
    }
    contacts {
        uuid id PK
        uuid account_id FK
        text jid
        text lid_jid
        text phone_number
    }
    messages {
        uuid id PK
        uuid conversation_id FK
        text wa_message_id
        bool from_me
        enum type
        enum status
        enum sender_source
        uuid quoted_message_id
    }
```

### Organisasi dan peran

```mermaid
erDiagram
    users ||--o{ role_assignments : "diberi peran"
    users ||--o{ pic_application_assignments : "sebagai PIC"
    users ||--o{ freelancer_application_assignments : "sebagai Freelance"
    users ||--o{ freelancer_pic_assignments : "berada di bawah"
    applications ||--o{ pic_application_assignments : ditugaskan
    applications ||--o{ freelancer_application_assignments : ditugaskan
    users ||--o{ work_schedules : memiliki

    role_assignments {
        uuid user_id FK
        enum role "leader, pic, freelance"
        bool is_active
    }
    pic_application_assignments {
        uuid user_id FK
        uuid application_id FK
    }
    freelancer_pic_assignments {
        uuid freelancer_id FK
        uuid pic_id FK
    }
```

### Kampanye

```mermaid
erDiagram
    content_campaigns ||--o{ campaign_targets : "menyasar"
    content_campaigns ||--o{ broadcast_sender_devices : "dikirim dari"
    content_campaigns ||--o{ story_publications : "diterbitkan sebagai"
    content_campaigns ||--o{ campaign_label_assignments : "diberi label"
    campaign_targets ||--o{ broadcast_target_attempts : "dicoba lewat"
    story_publications ||--o{ story_views : "dilihat pada"
    story_publications ||--o{ story_view_snapshots : "diarsipkan sebagai"
    campaign_labels ||--o{ campaign_label_assignments : dipakai
    whatsapp_accounts ||--o{ broadcast_sender_devices : "berperan sebagai"
    whatsapp_accounts ||--o{ story_publications : menerbitkan

    content_campaigns {
        uuid id PK
        enum campaign_type "story, broadcast"
        enum status
        timestamptz scheduled_at
        timestamptz lease_expires_at
        bool cancel_requested
        timestamptz stalled_since
        bool surface_on_phone
    }
    campaign_targets {
        uuid id PK
        text chat_jid
        enum status
        int attempt
        timestamptz lease_expires_at
        text failure_reason
    }
    story_publications {
        uuid id PK
        uuid account_id FK
        text wa_message_id
        timestamptz expires_at
    }
```

### Pengukuran kinerja

```mermaid
erDiagram
    conversations ||--o{ sla_cycles : mengukur
    conversations ||--o{ follow_up_events : mencatat
    contacts ||--|| contact_first_seen : "pertama dikenal"
    contacts ||--|| lead_classifications : dinilai
    contacts ||--|| contact_label_state : "kondisi label"
    contacts ||--o{ contact_label_events : "riwayat label"
    applications ||--o{ sla_targets : "punya target"

    sla_cycles {
        uuid id PK
        uuid inbound_message_id FK
        enum status "waiting, achieved, breached, excluded, queued"
    }
    lead_classifications {
        uuid contact_id PK
        enum lead_status "verified_new, historical, unknown"
    }
```

## 5.3 Table explanation

### Tabel terbesar di produksi

| Tabel | Baris | Peran |
|---|---|---|
| `conversation_members` | 499.013 | Anggota tiap grup, per percakapan |
| `contacts` | 396.823 | Kontak per nomor pengirim |
| `conversations` | 98.600 | Percakapan pribadi, grup, dan Status |
| `messages` | 97.979 | Seluruh pesan masuk dan keluar |
| `whatsapp_receipt_events` | 260.397 | Penjaga agar satu tanda terima tidak diproses dua kali |
| `whatsapp_label_events` | 52.850 | Riwayat label dari WhatsApp |
| `whatsapp_media_refs` | 40.429 | Rujukan media untuk mengunduh ulang |
| `message_attachments` | 39.807 | Lampiran, isinya tidak pernah disimpan di basis data |
| `story_views` | 34.307 | Tanda baca Status yang teramati |
| `contact_label_events` | 17.273 | Riwayat label kontak |
| `conversation_label_assignments` | 14.968 | Label yang menempel di percakapan |

### Penjelasan tabel inti

| Tabel | Penjelasan |
|---|---|
| `workspaces` | Batas terluar semua data. Setiap tabel besar membawa `workspace_id`. |
| `applications` | Brand atau produk. Nomor WhatsApp dikelompokkan ke sini, dan penugasan peran memakai aplikasi sebagai satuan. |
| `whatsapp_accounts` | Satu nomor WhatsApp. Menyimpan `jid` dan `lid`, dua alamat yang dipakai WhatsApp untuk satu nomor. |
| `conversations` | Satu utas. `chat_jid` adalah alamat yang dipakai WhatsApp pertama kali; `pn_jid` menyimpan bentuk nomor telepon bila utasnya lahir dari LID. Tanpa kolom kedua ini, satu orang bisa muncul dua kali di inbox. |
| `contacts` | Kontak per nomor pengirim, bukan per workspace. Buku alamat tiap perangkat berbeda. `lid_jid` menyimpan alamat kedua orang yang sama. |
| `messages` | Pesan. `sender_source` memisahkan asal: `web_admin`, `whatsapp_device`, `broadcast`, `story`, `bot`, `system`. Pemisahan inilah yang menjaga lalu lintas kampanye tidak terhitung sebagai pelayanan pribadi. |
| `message_attachments` | Metadata lampiran. Isinya disimpan di bucket privat, bukan di kolom. |
| `content_campaigns` | Induk Broadcast dan WA Story. Satu tabel untuk keduanya karena keduanya persoalan yang sama: menjadi siap, diklaim, dikerjakan bertahap, lalu dilaporkan. |
| `campaign_targets` | Satu baris per penerima Broadcast, beserta status dan alasan gagalnya. |
| `story_publications` | Satu baris per nomor yang menerbitkan Story. |
| `sla_cycles` | Satu siklus dari pesan masuk sampai dibalas. Unik terhadap `inbound_message_id`. |
| `whatsapp_receipt_events` | Idempotensi. Kuncinya kini berupa sidik tetap; sebelumnya gabungan seluruh id pesan dan bisa melampaui batas indeks. |

## 5.4 Relationship antar tabel

Aturan yang berlaku menyeluruh:

| Aturan | Wujudnya |
|---|---|
| Semua data terikat workspace | Kolom `workspace_id` pada tabel besar |
| Percakapan dan kontak terikat nomor, bukan workspace | `account_id` pada `conversations` dan `contacts` |
| Menghapus nomor menghapus turunannya | `on delete cascade` dari `whatsapp_accounts` |
| Satu orang satu baris kontak per nomor | Indeks unik `uq_contacts_account_phone` dan `uq_contacts_account_jid` |
| Satu percakapan per alamat per nomor | `uq_conversations_account_chat`, `uq_conversations_pn` |
| Satu pesan per id WhatsApp per nomor | `uq_messages_account_wamid` |
| Satu penerima per alamat per kampanye | `uq_campaign_targets` |

> **Catatan operasional untuk auditor:** menghapus satu `whatsapp_accounts`
> memicu rangkaian cascade yang panjang. Pada data produksi, satu penghapusan
> berjalan sekitar 90 detik dan membuat basis data sibuk penuh. Selama itu
> sebagian pesan masuk gagal disimpan karena kehabisan waktu tunggu.

## 5.5 Data flow

### Pesan masuk

```mermaid
flowchart LR
    A[WhatsApp] --> B[whatsmeow]
    B --> C[Event handler]
    C --> D[Upsert kontak]
    C --> E[Upsert percakapan]
    C --> F[Insert pesan]
    F --> G{Ada media?}
    G -->|Ya| H[Unduh, simpan ke bucket privat]
    H --> I[Insert message_attachments]
    F --> J[Perbarui kepala percakapan]
    F --> K[Buka siklus SLA]
    F --> L[Siarkan lewat WebSocket]
```

### Pesan keluar

```mermaid
flowchart LR
    A[Peramban] --> B[POST messages / media]
    B --> C[Insert pesan status pending]
    C --> D{Gambar bukan JPEG?}
    D -->|Ya| E[Ubah ke JPEG]
    D -->|Tidak| F[Lanjut]
    E --> F
    F --> G[Unggah ke WhatsApp]
    G --> H[Kirim]
    H --> I[Status sent]
    I --> J[Tanda terima masuk]
    J --> K[Status delivered lalu read]
    K --> L[Tutup siklus SLA]
```

### Kampanye

```mermaid
flowchart LR
    A[Simpan kampanye dan penerima] --> B[Scheduler tiap 5 detik]
    B --> C[Klaim dengan lease]
    C --> D[Ambil media sekali]
    D --> E[Unggah sekali per nomor]
    E --> F[Klaim penerima satu per satu]
    F --> G[Kirim, catat hasil]
    G --> H[Selesaikan kampanye]
```

## 5.6 Indeks penting

Ditambahkan setelah pengukuran, bukan diperkirakan.

| Indeks | Tabel | Untuk apa | Dampak terukur |
|---|---|---|---|
| `idx_whatsmeow_sessions_prefix` | `whatsmeow_sessions` | Pencocokan awalan saat perangkat berpindah alamat | 14,2 ms menjadi 0,16 ms; 71.062 blok menjadi 3 |
| `idx_whatsmeow_identity_keys_prefix` | `whatsmeow_identity_keys` | Sama | Ikut turun |
| `idx_messages_account_count` | `messages` | Hitungan pesan per nomor di daftar akun | Bagian dari 48.193 blok menjadi 1.106 |
| `idx_conversations_account_active` | `conversations` | Hitungan percakapan aktif per nomor | Sama |
| `idx_conversations_recency` | `conversations` | Sapuan rekonsiliasi metrik | Berhenti memindai 98.162 baris tiap 15 menit |
| `idx_conversations_account_contact` | `conversations` | Pemilih penerima broadcast | Pemindaian penuh hilang |
| `idx_conversations_inbox` | `conversations` | Membuka inbox satu nomor | 0,26 ms |

---

## Ringkasan yang belum tersedia

| Butir | Siapa yang memegang jawabannya |
|---|---|
| Kebijakan penyimpanan data lama | Pemilik produk / hukum |
| Rencana pemisahan data per pelanggan | Arsitek |
| Kebijakan anonimisasi kontak | Hukum |
