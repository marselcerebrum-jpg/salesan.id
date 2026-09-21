-- =============================================================================
-- salesan.id — Migration 0011: pemakaian nilai enum dari 0010
-- =============================================================================
-- Dipisah dari 0010 karena PostgreSQL menolak nilai enum baru dipakai pada
-- transaksi yang sama dengan yang menambahkannya. Setelah 0010 commit, nilainya
-- boleh dipakai — itu isi berkas ini.
--
-- Aman dijalankan ulang.
-- =============================================================================

-- Pratinjau untuk polling di daftar percakapan.
create or replace function public.message_preview_text(
  p_type    public.message_type,
  p_body    text,
  p_caption text
)
returns text
language sql
immutable
as $$
  select coalesce(
    nullif(p_body, ''),
    nullif(p_caption, ''),
    case p_type
      when 'image'    then 'Foto'
      when 'video'    then 'Video'
      when 'audio'    then 'Pesan suara'
      when 'document' then 'Dokumen'
      when 'sticker'  then 'Stiker'
      when 'location' then 'Lokasi'
      when 'contact'  then 'Kontak'
      when 'reaction' then 'Reaksi'
      when 'poll'     then 'Polling'
      else ''
    end
  );
$$;

-- Sapuan kedaluwarsa memindai lampiran yang berkasnya masih ada. Indeks parsial
-- ini membuatnya murah walaupun tabelnya sudah panjang.
create index if not exists idx_message_attachments_stored
  on public.message_attachments (account_id)
  where storage_status = 'stored';

comment on column public.message_attachments.storage_status is
  'pending/uploading/stored/failed, atau expired setelah berkasnya dihapus dari bucket karena lewat jendela sinkron.';
