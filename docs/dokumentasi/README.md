# Dokumentasi salesan.id

Dokumentasi ini disusun dengan **reverse engineering** terhadap aplikasi yang
sudah berjalan di produksi, bukan dari rencana atau spesifikasi awal. Setiap
angka, nama tabel, rute API, dan nilai konfigurasi di dalamnya dibaca langsung
dari kode sumber, basis data produksi, dan server pada **23 September 2026**.

## Cara membaca

| No | Dokumen | Untuk siapa |
|---|---|---|
| 1 | [Product Brief](01-product-brief.md) | Stakeholder, product manager |
| 2 | [Product Requirement Document](02-prd.md) | Product manager, QA, developer |
| 3 | [User Flow](03-user-flow.md) | Product manager, UX, QA |
| 4 | [UI/UX](04-ui-ux.md) | Frontend developer, UX |
| 5 | [Database](05-database.md) | Backend developer, DBA, auditor |
| 6 | [Arsitektur Teknis](06-arsitektur.md) | Developer, arsitek, auditor |
| 7 | [API](07-api.md) | Developer frontend, integrator |
| 8 | [Development](08-development.md) | Developer baru |
| 9 | [Testing](09-testing.md) | QA, developer |
| 10 | [Deployment](10-deployment.md) | DevOps, developer |
| 11 | [Future Development](11-future.md) | Product manager, stakeholder |

**Onboarding developer baru** sebaiknya dibaca urut: 1 → 6 → 8 → 5 → 7.
**Audit sistem** fokus pada: 5, 6, 10.
**Presentasi stakeholder** cukup: 1, 2, 11.

## Tanda [DATA BELUM TERSEDIA]

Dokumentasi ini tidak menebak. Informasi yang tidak bisa diturunkan dari sistem
yang berjalan ditandai `[DATA BELUM TERSEDIA]` beserta keterangan siapa yang
memegang jawabannya. Sebagian besar berada di ranah keputusan bisnis: latar
belakang, target pasar, dan roadmap komersial tidak tertulis di dalam kode.

Daftar lengkap yang masih kosong ada di bagian akhir setiap dokumen.

## Sumber kebenaran

| Yang didokumentasikan | Dibaca dari |
|---|---|
| Rute API | `backend/internal/httpapi/router.go` |
| Skema basis data | Basis data produksi + `backend/migrations/` |
| Peran dan izin | `backend/internal/repository/org.go`, `scope.go` |
| Halaman dan komponen | `web/src/app/`, `web/src/components/` |
| Konfigurasi | `backend/.env.example`, `web/.env.example` |
| Infrastruktur | Server produksi, `deploy/` |
| Arahan desain | `DESIGN.md` (ditulis pemilik produk) |

Dokumen lama yang tetap berlaku dan tidak digantikan dokumentasi ini:

- [`docs/broadcast-dan-story.md`](../broadcast-dan-story.md) — detail mendalam
  mesin Broadcast dan WA Story, termasuk keterbatasan whatsmeow yang sudah
  diverifikasi langsung.
- [`docs/deploy-vps.md`](../deploy-vps.md) — prosedur deploy ke VPS.
- [`README.md`](../../README.md) — panduan setup lokal.

> **Catatan akurasi:** `README.md` di akar repositori menyebut Broadcast "sengaja
> belum dikembangkan". Pernyataan itu sudah usang; Broadcast dan WA Story
> berjalan penuh di produksi. Dokumentasi ini mencerminkan keadaan sebenarnya.

## Cara menjaga dokumentasi ini tetap benar

Angka pemakaian di dokumen ini adalah potret satu hari, bukan nilai tetap.
Struktur, rute, tabel, dan peran adalah fakta yang berubah hanya lewat commit.
Saat mengubah salah satu dari berikut, perbarui dokumen yang disebut:

| Perubahan kode | Perbarui |
|---|---|
| Menambah rute di `router.go` | [07-api.md](07-api.md) |
| Menambah migration | [05-database.md](05-database.md) |
| Menambah halaman di `web/src/app/` | [04-ui-ux.md](04-ui-ux.md) |
| Mengubah peran atau izin | [02-prd.md](02-prd.md) |
| Menambah environment variable | [10-deployment.md](10-deployment.md) |
