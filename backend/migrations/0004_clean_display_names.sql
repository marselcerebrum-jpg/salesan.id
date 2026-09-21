-- =============================================================================
-- salesan.id — Migration 0004: bersihkan karakter Unicode tak terlihat
-- =============================================================================
-- WhatsApp mengawali nama label bawaannya dengan U+200E (LEFT-TO-RIGHT MARK),
-- sehingga "Menunggu pembayaran" tersimpan sebagai "<U+200E>Menunggu pembayaran".
-- Nama seperti itu tampil dengan spasi hantu, mengurutkan diri secara aneh, dan
-- membuat pencocokan nama persis meleset. Push name dan nama grup bisa membawa
-- tanda yang sama.
--
-- Sejak versi ini backend membersihkannya saat data masuk; migrasi ini
-- membereskan baris yang terlanjur tersimpan.
--
-- Aman dijalankan ulang.
--   go run ./cmd/migrate -file migrations/0004_clean_display_names.sql
-- =============================================================================

-- Karakter yang dibuang: zero-width space/non-joiner/joiner, LTR/RTL mark,
-- bidi embedding & override, bidi isolate, dan BOM.
create or replace function public.strip_invisible(input text)
returns text
language sql
immutable
as $$
  select nullif(btrim(translate(
    coalesce(input, ''),
    chr(8203) || chr(8204) || chr(8205) ||          -- U+200B..U+200D
    chr(8206) || chr(8207) ||                        -- U+200E, U+200F
    chr(8234) || chr(8235) || chr(8236) ||           -- U+202A..U+202C
    chr(8237) || chr(8238) ||                        -- U+202D, U+202E
    chr(8294) || chr(8295) || chr(8296) || chr(8297) -- U+2066..U+2069
    || chr(65279),                                   -- U+FEFF
    ''
  )), '')
$$;

comment on function public.strip_invisible(text) is
  'Removes invisible Unicode formatting marks; mirrors sanitizeDisplayName in internal/wa.';

-- --- conversation_labels ------------------------------------------------------
-- Dilakukan satu per satu dan melewati baris yang namanya akan bentrok dengan
-- label lain, karena (workspace_id, name) unik.
do $$
declare
  r      record;
  target text;
begin
  for r in
    select id, workspace_id, name
      from public.conversation_labels
     where name is distinct from public.strip_invisible(name)
  loop
    target := public.strip_invisible(r.name);
    if target is null then
      continue;
    end if;

    if exists (
      select 1 from public.conversation_labels
       where workspace_id = r.workspace_id and name = target and id <> r.id
    ) then
      raise notice 'Melewati label % — nama "%" sudah dipakai label lain', r.id, target;
      continue;
    end if;

    update public.conversation_labels set name = target where id = r.id;
  end loop;
end $$;

-- --- contacts -----------------------------------------------------------------
update public.contacts
   set name          = public.strip_invisible(name),
       push_name     = public.strip_invisible(push_name),
       business_name = public.strip_invisible(business_name)
 where name          is distinct from public.strip_invisible(name)
    or push_name     is distinct from public.strip_invisible(push_name)
    or business_name is distinct from public.strip_invisible(business_name);

-- --- conversations ------------------------------------------------------------
update public.conversations
   set name = public.strip_invisible(name)
 where name is distinct from public.strip_invisible(name);

-- --- conversation_members -----------------------------------------------------
update public.conversation_members
   set display_name = public.strip_invisible(display_name)
 where display_name is distinct from public.strip_invisible(display_name);

-- --- messages -----------------------------------------------------------------
update public.messages
   set sender_name = public.strip_invisible(sender_name)
 where sender_name is distinct from public.strip_invisible(sender_name);
