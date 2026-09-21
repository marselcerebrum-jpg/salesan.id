# Deploy ke VPS

Satu VPS Ubuntu/Debian menjalankan tiga container lewat Docker Compose:

| Container | Isi                              | Port                                  |
| --------- | -------------------------------- | ------------------------------------- |
| `backend` | API Go + sesi WhatsApp           | 8080, hanya di jaringan internal      |
| `web`     | Next.js (build `standalone`)     | 3000, hanya di jaringan internal      |
| `caddy`   | Reverse proxy + HTTPS otomatis   | 80 dan 443, satu-satunya yang terbuka |

Database dan media tetap di Supabase. Tidak ada data pelanggan yang disimpan di
disk VPS; yang ada di VPS hanya kode, dua berkas `.env`, dan sertifikat TLS.

Berkasnya ada di folder [`deploy/`](../deploy):

- `docker-compose.prod.yml` — definisi ketiga container
- `Caddyfile` — dua hostname → dua container
- `.env.example` — domain dan kunci publik Supabase (salin ke `deploy/.env`)
- `install-vps.sh` — sekali jalan: pasang Docker, firewall, auto-update
- `deploy.sh` — setiap deploy: `git pull` → build → migrasi → restart

## 1. DNS

Buat dua record **A** di pengelola domain, keduanya menunjuk ke IP VPS:

| Nama  | Tipe | Nilai   | Fungsi                        |
| ----- | ---- | ------- | ----------------------------- |
| `app` | A    | IP VPS  | halaman aplikasi (frontend)   |
| `api` | A    | IP VPS  | backend (API dan WebSocket)   |

Tunggu sampai `nslookup app.domain-anda.id` dari laptop menjawab IP VPS.
Caddy meminta sertifikat ke Let's Encrypt saat pertama kali dipanggil, dan itu
gagal kalau DNS belum mengarah.

## 2. Siapkan VPS (sekali saja)

Masuk lewat SSH, lalu:

```bash
sudo apt-get install -y git
git clone https://github.com/<akun>/<repo>.git salesan
cd salesan
sudo bash deploy/install-vps.sh
```

Skrip itu memasang Docker, membuka hanya port 22/80/443 di firewall, dan
menyalakan pembaruan keamanan otomatis. **Keluar lalu masuk SSH lagi** supaya
user Anda bisa memanggil `docker` tanpa `sudo`.

## 3. Isi konfigurasi

```bash
cp deploy/.env.example deploy/.env
cp backend/.env.example backend/.env
nano deploy/.env      # APP_DOMAIN, API_DOMAIN, NEXT_PUBLIC_SUPABASE_URL, NEXT_PUBLIC_SUPABASE_ANON_KEY
nano backend/.env     # DATABASE_URL, SUPABASE_URL, SUPABASE_SERVICE_ROLE_KEY, dst.
```

Cara paling mudah untuk `backend/.env`: salin isi `backend/.env` dari laptop
yang sudah berjalan (lewat `scp`, bukan lewat chat atau email), karena semua
kunci Supabase-nya sama. Dua nilai berikut **tidak perlu** diubah di
`backend/.env` karena ditimpa oleh compose dari `deploy/.env`:

- `PUBLIC_API_URL` → otomatis `https://<API_DOMAIN>`
- `ALLOWED_ORIGINS` → otomatis `https://<APP_DOMAIN>`

`APP_ENV` juga otomatis `production` (log berbentuk JSON).

Jangan pernah menaruh `SUPABASE_SERVICE_ROLE_KEY` di `deploy/.env`: berkas itu
dipakai untuk build frontend, dan frontend tidak boleh punya kunci tersebut.

## 4. Deploy pertama

```bash
bash deploy/deploy.sh --no-pull
```

Build pertama memakan 3–6 menit (mengunduh image Go dan Node, `npm ci`,
`next build`). Setelah selesai skrip mencetak status ketiga container.
Lalu buka `https://app.domain-anda.id`. Sertifikat muncul otomatis dalam
beberapa detik pada kunjungan pertama.

Cek backend hidup:

```bash
curl -s https://api.domain-anda.id/health
```

## 5. Supabase: dua pengaturan

1. **Authentication → URL Configuration → Site URL** isi `https://app.domain-anda.id`
   dan tambahkan URL yang sama ke **Redirect URLs**.
2. Bucket `wa-media` sudah dibuat otomatis oleh backend; tidak ada yang perlu diubah.

## 6. Deploy berikutnya (setiap ada perubahan kode)

Dari laptop: commit dan push. Di VPS:

```bash
cd salesan
bash deploy/deploy.sh
```

Skrip melakukan `git pull`, membangun image baru, **menjalankan migrasi
database sebelum server baru start**, lalu restart. Sesi WhatsApp tersimpan di
database, jadi restart tidak memutus tautan perangkat; backend menyambung ulang
sendiri dalam beberapa detik.

## Varian: VPS yang sudah punya nginx di port 80/443

Inilah yang dipakai di `187.53.133.30` (`salesan.marseltech.cloud`), karena
nginx di host sudah melayani `app.marseltech.cloud`. Caddy tidak dijalankan;
container diekspos hanya ke `127.0.0.1` (`3180` web, `8180` backend) dan nginx
yang mem-proxy. Sekali saja di VPS:

```bash
touch deploy/USE_NGINX                       # deploy.sh lalu memakai docker-compose.nginx.yml
cp deploy/nginx-site.conf /etc/nginx/sites-available/salesan-id
ln -s /etc/nginx/sites-available/salesan-id /etc/nginx/sites-enabled/
nginx -t && systemctl reload nginx
certbot --nginx -d salesan.marseltech.cloud -d api.salesan.marseltech.cloud --redirect
```

Sertifikat diperpanjang otomatis oleh timer certbot yang sudah ada. Deploy
berikutnya tetap `bash deploy/deploy.sh`; site nginx tidak perlu disentuh lagi.
Lokasi kode di VPS: `/opt/salesan/app`.

## Perintah harian

```bash
C="docker compose -f deploy/docker-compose.prod.yml --env-file deploy/.env"
$C ps                       # status
$C logs -f backend          # log backend (Ctrl+C untuk keluar)
$C logs -f web              # log frontend
$C restart backend          # restart satu container
$C run --rm --no-deps --entrypoint /app/migrate backend -status   # migrasi mana yang sudah jalan
$C down                     # matikan semuanya (sertifikat tetap tersimpan)
```

## Kalau ada masalah

- **Browser bilang sertifikat tidak valid / Caddy error `obtaining certificate`**:
  DNS belum mengarah ke VPS, atau port 80/443 tertutup di panel firewall
  penyedia VPS (selain `ufw`, Hostinger/DigitalOcean punya firewall sendiri).
- **Halaman terbuka tapi data tidak muncul / login gagal**: cek
  `curl https://api.domain-anda.id/health`. Kalau gagal, lihat `logs -f backend`.
  Biasanya `DATABASE_URL` salah atau IP VPS belum diizinkan di Supabase
  (Project Settings → Database → Network restrictions, kalau diaktifkan).
- **Chat tidak update realtime**: buka DevTools → Network → WS; koneksi ke
  `wss://api.domain-anda.id/api/v1/ws` harus status 101. Kalau 403, berarti
  `APP_DOMAIN` di `deploy/.env` tidak sama persis dengan domain yang dibuka.
- **`permission denied` saat memanggil docker**: belum logout/login setelah
  `install-vps.sh`.

## Yang sengaja tidak ada

- **Tidak ada database di VPS.** Kalau VPS hilang, cukup pasang ulang dan
  jalankan langkah 2–4; tidak ada data yang ikut hilang.
- **Port 8080 dan 3000 tidak dibuka ke internet.** Semua lalu lintas lewat
  Caddy dengan HTTPS.
- **Tidak ada auto-deploy dari Git.** Deploy terjadi ketika Anda menjalankan
  `deploy.sh`, sehingga selalu jelas kapan versi baru naik.
