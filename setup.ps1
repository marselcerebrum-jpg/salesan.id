# =============================================================================
# salesan.id — setup interaktif
#
# Menanyakan 3-4 nilai dari dashboard Supabase, lalu menulis:
#   backend/.env
#   web/.env.local
#
# Jalankan dari folder project:
#   cd K:\SALESANv2
#   powershell -ExecutionPolicy Bypass -File .\setup.ps1
#
# Aman diulang: file ditulis ulang penuh setiap kali dijalankan.
# Password tidak pernah ditampilkan di layar.
# =============================================================================

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $MyInvocation.MyCommand.Path

function Write-Head($text) {
    Write-Host ""
    Write-Host $text -ForegroundColor Cyan
    Write-Host ("-" * $text.Length) -ForegroundColor DarkGray
}

function Ask($prompt, $hint) {
    Write-Host ""
    Write-Host $prompt -ForegroundColor White
    if ($hint) { Write-Host "  $hint" -ForegroundColor DarkGray }
    do {
        $value = (Read-Host "  >").Trim()
        if ($value -eq '') { Write-Host "  Tidak boleh kosong." -ForegroundColor Yellow }
    } while ($value -eq '')
    return $value
}

Write-Host ""
Write-Host "  salesan.id - setup" -ForegroundColor Green
Write-Host "  Buka: https://supabase.com/dashboard -> pilih project kamu" -ForegroundColor DarkGray

# --- 1. Project URL ----------------------------------------------------------
Write-Head "1/3  Project URL"
$projectUrl = Ask "Tempel Project URL:" "Project Settings -> API -> Project URL. Contoh: https://abcdefgh.supabase.co"
$projectUrl = $projectUrl.TrimEnd('/')
if ($projectUrl -notmatch '^https://[a-z0-9-]+\.supabase\.(co|in)$') {
    Write-Host "  Peringatan: bentuknya tidak seperti Project URL biasanya. Lanjut saja." -ForegroundColor Yellow
}

# --- 2. anon key -------------------------------------------------------------
Write-Head "2/3  anon / public key"
$anonKey = Ask "Tempel anon key:" "Project Settings -> API Keys -> anon / public. JANGAN service_role."
if ($anonKey -like '*service_role*') {
    Write-Host "  BERHENTI: itu service_role key, jangan dipakai di browser." -ForegroundColor Red
    exit 1
}

# --- 3. Connection string ----------------------------------------------------
Write-Head "3/3  Connection string (Session pooler)"
Write-Host "  Project Settings -> Database -> Connection string -> tab 'Session pooler'" -ForegroundColor DarkGray
Write-Host "  Mesin ini tanpa IPv6, jadi 'Direct connection' tidak akan nyambung." -ForegroundColor DarkGray
$dbUri = Ask "Tempel connection string (URI):" "Contoh: postgresql://postgres.abc:[YOUR-PASSWORD]@aws-0-ap-southeast-1.pooler.supabase.com:5432/postgres"

if ($dbUri -match ':6543/') {
    Write-Host ""
    Write-Host "  BERHENTI: itu Transaction pooler (port 6543)." -ForegroundColor Red
    Write-Host "  Mode itu tidak bisa menyimpan prepared statement dan akan menggagalkan query." -ForegroundColor Red
    Write-Host "  Pilih tab 'Session pooler' (port 5432), lalu jalankan skrip ini lagi." -ForegroundColor Red
    exit 1
}
if ($dbUri -match '@db\.[a-z0-9-]+\.supabase\.co') {
    Write-Host ""
    Write-Host "  Peringatan: itu Direct connection (IPv6-only)." -ForegroundColor Yellow
    Write-Host "  Mesin ini tidak punya IPv6, jadi hampir pasti timeout." -ForegroundColor Yellow
    $go = Read-Host "  Tetap lanjut? (y/N)"
    if ($go -ne 'y') { exit 1 }
}

# Ganti placeholder password bila masih ada.
if ($dbUri -match '\[YOUR-PASSWORD\]|\[YOUR_PASSWORD\]|\[password\]') {
    Write-Host ""
    Write-Host "  String masih berisi placeholder password." -ForegroundColor White
    Write-Host "  Ketik password database kamu (tidak akan tampil di layar):" -ForegroundColor DarkGray
    $secure = Read-Host "  >" -AsSecureString
    $bstr = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($secure)
    try {
        $plain = [Runtime.InteropServices.Marshal]::PtrToStringAuto($bstr)
    } finally {
        [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($bstr)
    }
    if ([string]::IsNullOrWhiteSpace($plain)) {
        Write-Host "  Password kosong. Berhenti." -ForegroundColor Red
        exit 1
    }
    # Password harus di-encode; karakter seperti @ : / ? # akan merusak URI.
    $encoded = [uri]::EscapeDataString($plain)
    $dbUri = $dbUri -replace '\[YOUR-PASSWORD\]|\[YOUR_PASSWORD\]|\[password\]', $encoded
    $plain = $null
}

# Sanity check akhir.
try {
    $parsed = [uri]$dbUri
    $dbHost = $parsed.Host
    $dbPort = $parsed.Port
} catch {
    Write-Host "  BERHENTI: connection string tidak bisa dibaca sebagai URI." -ForegroundColor Red
    exit 1
}

# --- tulis file --------------------------------------------------------------
$backendEnv = @"
APP_ENV=development
PORT=8080
LOG_LEVEL=info

DATABASE_URL=$dbUri
SUPABASE_URL=$projectUrl

WHATSMEOW_DATABASE_URL=

SUPABASE_JWT_SECRET=
SUPABASE_JWKS_URL=
SUPABASE_JWT_AUDIENCE=authenticated

ALLOWED_ORIGINS=http://localhost:3000

QR_TIMEOUT=3m
RECONNECT_BASE_DELAY=3s
RECONNECT_MAX_DELAY=5m
WA_AUTO_MARK_READ=false
"@

$webEnv = @"
NEXT_PUBLIC_SUPABASE_URL=$projectUrl
NEXT_PUBLIC_SUPABASE_ANON_KEY=$anonKey
NEXT_PUBLIC_API_URL=http://localhost:8080
"@

$backendPath = Join-Path $root 'backend\.env'
$webPath = Join-Path $root 'web\.env.local'

# PowerShell 5.1's `-Encoding utf8` writes a BOM, which .env parsers choke on.
# Write the bytes ourselves so the files are plain UTF-8.
$utf8NoBom = New-Object System.Text.UTF8Encoding($false)
[System.IO.File]::WriteAllText($backendPath, $backendEnv, $utf8NoBom)
[System.IO.File]::WriteAllText($webPath, $webEnv, $utf8NoBom)

Write-Head "Selesai ditulis"
Write-Host "  $backendPath" -ForegroundColor Green
Write-Host "  $webPath" -ForegroundColor Green

# --- tes koneksi TCP ---------------------------------------------------------
Write-Head "Tes koneksi ke $dbHost port $dbPort"
try {
    $test = Test-NetConnection -ComputerName $dbHost -Port $dbPort -WarningAction SilentlyContinue
    if ($test.TcpTestSucceeded) {
        Write-Host "  BERHASIL - host database bisa dijangkau." -ForegroundColor Green
    } else {
        Write-Host "  GAGAL - host tidak bisa dijangkau di port $dbPort." -ForegroundColor Red
        Write-Host "  Kalau kamu memilih Direct connection, ganti ke Session pooler." -ForegroundColor Yellow
    }
} catch {
    Write-Host "  Tes tidak bisa dijalankan: $($_.Exception.Message)" -ForegroundColor Yellow
}

Write-Host ""
Write-Host "  Langkah berikutnya: kembali ke Claude Code dan ketik: sudah" -ForegroundColor Cyan
Write-Host ""
