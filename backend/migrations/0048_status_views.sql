-- =============================================================================
-- 0048 — penonton status yang diposting langsung dari HP
--
-- story_views selama ini hanya bisa menempel pada story_publications, yaitu
-- baris yang dibuat menu penjadwalan Story. Status yang diposting langsung dari
-- HP tidak punya baris itu, jadi receipt penontonnya sampai ke server lalu
-- dibuang tanpa jejak — padahal itu tetap status kita dan receipt-nya tetap
-- bukti seseorang menontonnya.
--
-- Bukan tabel baru: satu tabel, satu fungsi pencatat, satu pengertian tentang
-- apa itu "view". Yang ditambahkan hanya sasaran kedua. Sebuah baris menempel
-- pada publikasi ATAU pada pesan status, tidak pernah keduanya, sehingga satu
-- tontonan tidak mungkin terhitung dua kali.
--
-- message_id memakai ON DELETE CASCADE dengan sengaja. Pesan status dihapus
-- permanen setelah 24 jam (lihat DeleteExpiredStatuses), dan angka penontonnya
-- ikut hilang bersamanya — sama seperti di WhatsApp, di mana tidak ada tempat
-- untuk melihat kembali siapa yang menonton status kemarin.
--
-- Aman dijalankan ulang.
-- =============================================================================

alter table public.story_views
  alter column publication_id drop not null,
  alter column campaign_id    drop not null;

alter table public.story_views
  add column if not exists message_id uuid
    references public.messages (id) on delete cascade;

-- Parsial: baris milik campaign tidak punya message_id, dan NULL tidak pernah
-- dicari lewat kolom ini.
create unique index if not exists uq_status_view
  on public.story_views (message_id, viewer_jid)
  where message_id is not null;

create index if not exists idx_story_views_message
  on public.story_views (message_id)
  where message_id is not null;

-- Satu sasaran harus ada. Tanpa ini sebuah baris bisa menggantung tanpa
-- menunjuk apa pun, dan angka mana pun yang menghitungnya jadi tidak bisa
-- dipertanggungjawabkan.
do $$ begin
  alter table public.story_views
    add constraint story_views_target_present
    check (publication_id is not null or message_id is not null);
exception when duplicate_object then null; end $$;

-- -----------------------------------------------------------------------------
-- RLS
--
-- Kebijakan lama hanya menumpang izin campaign induknya. Baris yang menempel
-- pada pesan status tidak punya campaign, jadi kebijakannya diperluas lewat
-- percakapan tempat pesan itu berada.
-- -----------------------------------------------------------------------------
drop policy if exists story_views_select on public.story_views;
create policy story_views_select on public.story_views
  for select to authenticated
  using (
    exists (
      select 1 from public.content_campaigns c
       where c.id = story_views.campaign_id
         and c.workspace_id = public.current_workspace_id()
         and (
           c.application_id in (select public.visible_application_ids())
           or (c.application_id is null
               and coalesce(public.current_operational_role() = 'leader', true))
           or c.created_by = auth.uid()
         )
    )
    or exists (
      select 1 from public.messages m
        join public.conversations cv on cv.id = m.conversation_id
       where m.id = story_views.message_id
         and cv.workspace_id = public.current_workspace_id()
    )
  );

comment on column public.story_views.message_id is
  'Pesan status yang ditonton, untuk status yang diposting langsung dari HP. Null untuk baris milik campaign Story.';
