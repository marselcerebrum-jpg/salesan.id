-- =============================================================================
-- salesan.id — Migration 0014: edit pesan, hapus untuk semua, hapus untuk saya
-- =============================================================================
-- Tiga tindakan, tiga arti yang berbeda, dan itulah sebabnya masing-masing
-- punya kolomnya sendiri alih-alih satu kolom "status":
--
--   edited_at  — isinya diganti. Pesannya masih ada, dan WhatsApp menandainya
--                "diedit" di kedua sisi.
--   revoked_at — dihapus untuk semua orang. Barisnya sengaja disimpan supaya
--                tempatnya di percakapan tetap ada dengan tulisan "Pesan ini
--                dihapus", persis seperti di HP. Isinya dikosongkan.
--   hidden_at  — dihapus untuk saya. Hilang dari web ini saja; di HP dan di
--                lawan bicara tetap utuh.
--
-- Aman dijalankan ulang.
-- =============================================================================

alter table public.messages
  add column if not exists edited_at  timestamptz,
  add column if not exists revoked_at timestamptz,
  add column if not exists hidden_at  timestamptz;

comment on column public.messages.revoked_at is
  'Dihapus untuk semua orang. Baris tetap ada sebagai penanda tempat; body dan caption dikosongkan.';
comment on column public.messages.hidden_at is
  'Dihapus untuk saya. Disembunyikan dari inbox web ini, tetap utuh di HP.';

-- Pesan yang disembunyikan tidak boleh ikut terhitung maupun tampil, jadi
-- indeksnya sekalian menyaring.
create index if not exists idx_messages_visible
  on public.messages (conversation_id, timestamp desc)
  where hidden_at is null;

-- -----------------------------------------------------------------------------
-- Pratinjau untuk pesan yang dihapus
-- -----------------------------------------------------------------------------
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

-- -----------------------------------------------------------------------------
-- Kepala percakapan mengabaikan pesan yang disembunyikan, dan menuliskan
-- "Pesan ini dihapus" untuk yang ditarik kembali.
-- -----------------------------------------------------------------------------
create or replace function public.refresh_conversation_head(p_conversation_id uuid)
returns void
language plpgsql
as $$
begin
  update public.conversations c
     set last_message_id        = m.id,
         last_message_text      = m.preview,
         last_message_at        = m.timestamp,
         last_message_direction = case when m.from_me then 'out' else 'in' end,
         updated_at             = now()
    from (
      select id,
             timestamp,
             from_me,
             case when revoked_at is not null then 'Pesan ini dihapus'
                  else public.message_preview_text(type, body, caption) end as preview
        from public.messages
       where conversation_id = p_conversation_id
         and hidden_at is null
       order by timestamp desc, created_at desc
       limit 1
    ) m
   where c.id = p_conversation_id
     and (c.last_message_id is distinct from m.id
       or c.last_message_at is distinct from m.timestamp
       or c.last_message_text is distinct from m.preview);

  -- Percakapan yang kehilangan pesan terakhirnya — semuanya disembunyikan atau
  -- dihapus — harus kembali kosong, bukan menahan pratinjau yang sudah tiada.
  update public.conversations c
     set last_message_id = null, last_message_text = null,
         last_message_at = null, last_message_direction = null, updated_at = now()
   where c.id = p_conversation_id
     and not exists (
       select 1 from public.messages m
        where m.conversation_id = p_conversation_id and m.hidden_at is null);
end;
$$;
