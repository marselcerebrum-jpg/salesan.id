-- =============================================================================
-- salesan.id — Migration 0009: teks pratinjau untuk pesan media
-- =============================================================================
-- Sebelum ini, pesan tanpa teks memakai nama tipenya mentah-mentah, sehingga
-- daftar percakapan menampilkan "[image]" untuk foto tanpa keterangan.
--
-- Perbaikannya diletakkan di fungsi head percakapan, bukan di frontend, supaya
-- semua yang membaca last_message_text — daftar chat, pencarian, dan apa pun
-- nanti — melihat kalimat yang sama.
--
-- Aman dijalankan ulang.
-- =============================================================================

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
      else ''
    end
  );
$$;

comment on function public.message_preview_text(public.message_type, text, text) is
  'One-line preview for a message; names the media kind when there is no text.';

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
             public.message_preview_text(type, body, caption) as preview
        from public.messages
       where conversation_id = p_conversation_id
       order by timestamp desc, created_at desc
       limit 1
    ) m
   where c.id = p_conversation_id
     and (c.last_message_id is distinct from m.id
       or c.last_message_at is distinct from m.timestamp
       or c.last_message_text is distinct from m.preview);
end;
$$;

-- Perbaiki baris yang terlanjur tersimpan dengan bentuk lama "[image]".
update public.conversations c
   set last_message_text = public.message_preview_text(m.type, m.body, m.caption)
  from public.messages m
 where m.id = c.last_message_id
   and c.last_message_text like '[%]';
