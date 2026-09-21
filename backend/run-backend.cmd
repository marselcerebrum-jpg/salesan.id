@echo off
rem Jalankan backend salesan.id di jendela terminal sendiri.
rem
rem Klik dua kali file ini, atau jalankan dari PowerShell:
rem     K:\SALESANv2\backend\run-backend.cmd
rem
rem Biarkan jendelanya terbuka selama aplikasi dipakai. Menutup jendela ini
rem mematikan backend, dan seluruh halaman akan gagal memuat data.
title salesan backend
cd /d "%~dp0"
echo Menjalankan server.exe dari %CD%
echo Tutup jendela ini untuk menghentikan backend.
echo.
server.exe
echo.
echo Backend berhenti dengan kode %ERRORLEVEL%.
pause
