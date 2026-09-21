-- =============================================================================
-- salesan.id — Migration 0012: identitas akun (LID) + jejak pemilih polling
-- =============================================================================
-- Dua perbaikan yang ditemukan saat menelusuri logika polling:
--
--   (1) whatsapp_accounts.jid menyimpan alamat LENGKAP dengan nomor perangkat,
--       misalnya "628xxx:20@s.whatsapp.net". Padahal pesan menyimpan pengirim
--       tanpa nomor perangkat. Membandingkan keduanya tidak pernah cocok, jadi
--       suara kita sendiri tidak pernah tampil tercentang.
--
--       Lebih dari itu: WhatsApp mengalamati akun yang sama sebagai LID pada
--       sebagian chat (27 dari 46 percakapan di sini). Jadi "diri sendiri"
--       punya DUA alamat, dan keduanya harus disimpan.
--
--   (2) Penjaga suara basi memakai max(voted_at) dari baris suara. Begitu
--       seseorang menghapus suaranya, barisnya hilang dan penjaga itu ikut
--       hilang — sehingga suara lama yang diputar ulang saat reconnect bisa
--       hidup kembali. Waktu pembaruan terakhir per pemilih kini disimpan
--       terpisah, sehingga tetap ada walaupun pilihannya kosong.
--
-- Aman dijalankan ulang.
-- =============================================================================

-- -----------------------------------------------------------------------------
-- (1) Alamat kedua milik akun
-- -----------------------------------------------------------------------------
alter table public.whatsapp_accounts
  add column if not exists lid text;

comment on column public.whatsapp_accounts.lid is
  'Alamat LID akun ini. WhatsApp memakai LID alih-alih nomor telepon pada sebagian chat, jadi satu akun bisa muncul sebagai dua alamat.';

-- -----------------------------------------------------------------------------
-- (2) Waktu pembaruan suara terakhir per pemilih
-- -----------------------------------------------------------------------------
create table if not exists public.message_poll_voters (
  message_id uuid not null references public.message_polls (message_id) on delete cascade,
  voter_jid  text not null,
  voted_at   timestamptz not null,
  primary key (message_id, voter_jid)
);

alter table public.message_poll_voters enable row level security;

drop policy if exists message_poll_voters_rw on public.message_poll_voters;
create policy message_poll_voters_rw on public.message_poll_voters
  for all to authenticated
  using (exists (
    select 1 from public.message_polls p
     where p.message_id = message_poll_voters.message_id
       and p.workspace_id = public.current_workspace_id()))
  with check (exists (
    select 1 from public.message_polls p
     where p.message_id = message_poll_voters.message_id
       and p.workspace_id = public.current_workspace_id()));

-- Isi dari suara yang sudah ada, supaya penjaga langsung berlaku.
insert into public.message_poll_voters (message_id, voter_jid, voted_at)
select message_id, voter_jid, max(voted_at)
  from public.message_poll_votes
 group by message_id, voter_jid
on conflict (message_id, voter_jid) do nothing;
