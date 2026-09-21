-- =============================================================================
-- 0047 — indeks pencarian kontak per workspace
--
-- Resolusi anggota grup (memberidentity.go) mencari kontak dengan:
--
--   where x.workspace_id = c.workspace_id
--     and (x.jid = mem.jid or x.lid_jid = mem.jid)
--
-- Indeks kontak yang ada semuanya diawali account_id, bukan workspace_id, jadi
-- satu-satunya yang bisa dipakai predikat ini adalah idx_contacts_workspace
-- (workspace_id saja). Artinya SETIAP baris anggota memicu pemindaian seluruh
-- kontak workspace itu. Dengan 70 anggota dan 163 kontak biayanya tidak terasa;
-- dengan 70 anggota dan 100.000 kontak menjadi 7 juta baris dibaca untuk satu
-- kali membuka halaman detail grup.
--
-- Dua indeks di bawah membuat kedua sisi OR itu terindeks, sehingga Postgres
-- dapat memakai BitmapOr dan hanya menyentuh baris yang benar-benar cocok.
--
-- Aman dijalankan ulang. Tidak mengubah data apa pun.
-- =============================================================================

create index if not exists idx_contacts_workspace_jid
  on public.contacts (workspace_id, jid);

-- Parsial: sebagian besar kontak tidak punya lid_jid, dan baris null tidak
-- pernah dicari lewat kolom ini.
create index if not exists idx_contacts_workspace_lid
  on public.contacts (workspace_id, lid_jid)
  where lid_jid is not null;

comment on index public.idx_contacts_workspace_jid is
  'Dipakai resolusi anggota grup: contacts(workspace_id, jid). Tanpa ini setiap anggota memindai seluruh kontak workspace.';
