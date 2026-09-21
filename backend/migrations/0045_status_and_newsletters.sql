-- -----------------------------------------------------------------------------
-- Jenis percakapan ketiga: status
--
-- Status orang lain tiba sebagai pesan biasa dengan chat = status@broadcast.
-- conversationType() hanya mengenal 'group' dan selebihnya 'personal', jadi
-- alamat itu diperlakukan sebagai chat pribadi: satu percakapan bernama
-- "status@broadcast" berisi status semua orang, bercampur di inbox seperti
-- pelanggan biasa. Jenis ketiga menutup lubang itu.
--
-- Dimodelkan sebagai percakapan, bukan tabel tersendiri, karena bentuknya
-- memang percakapan: WhatsApp sendiri menaruh status semua orang di satu
-- alamat, dan pengirimnya dibedakan oleh participant_jid persis seperti di
-- grup. Memilih tabel baru berarti menulis ulang jalur lampiran, unduhan media,
-- bucket privat, signed URL, dan masa simpan — semuanya sudah ada dan sudah
-- benar untuk pesan.
--
-- Sendirian di berkasnya sendiri karena Postgres menolak memakai nilai enum
-- baru di dalam transaksi yang menambahkannya. Yang memakainya ada di 0046.
-- -----------------------------------------------------------------------------

do $$
begin
  alter type public.conversation_type add value if not exists 'status';
exception when duplicate_object then null;
end $$;
