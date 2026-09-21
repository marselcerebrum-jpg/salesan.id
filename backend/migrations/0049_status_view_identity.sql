-- =============================================================================
-- 0049 — satu identitas untuk satu status, satu tempat untuk angkanya
--
-- story_views menyimpan satu fakta ("seseorang menonton status kita") dengan dua
-- induk yang berbeda: publication_id untuk status yang terbit lewat penjadwal WA
-- Story, message_id untuk status yang diposting langsung dari HP. Fakta yang
-- sama, dua tempat, dan dua akibat yang sudah terlihat:
--
--   1. Angkanya berbeda antar layar. Laporan Story menghitung lewat kolom
--      pertama, panel Status di menu Chat lewat kolom kedua, jadi status yang
--      sama bisa terbaca 1 di satu layar dan 0 di layar lain.
--   2. Umurnya berbeda. Baris yang menempel pada pesan ikut terhapus bersama
--      pesannya di jam ke-24 (ON DELETE CASCADE), yang menempel pada publikasi
--      tidak. Fakta yang sama, dua nasib.
--
-- Identitas sebenarnya sebuah status di WhatsApp adalah (account_id,
-- wa_message_id), dan kedua induk itu sama-sama sudah memilikinya. Jadi kolom
-- itulah yang dipakai sebagai kunci, dan kedua kolom induk dibuang. Satu baris
-- per (nomor, status, penonton). Tidak ada lagi dua jalur hitung yang bisa
-- berbeda.
--
-- Setelah 24 jam, untuk status kita sendiri, yang disimpan hanya ANGKANYA, di
-- story_view_snapshots. Daftar penontonnya dihapus setelah angkanya dibekukan:
-- angkanya tetap bisa dilihat di rincian per nomor selamanya, sementara JID
-- orang lain tidak disimpan tanpa batas waktu di server ini.
--
-- Semua tampilan membaca dari satu view, public.status_view_counts, sehingga
-- tidak mungkin ada dua definisi "jumlah penonton" yang berbeda lagi.
--
-- Aman dijalankan ulang.
-- =============================================================================

-- -----------------------------------------------------------------------------
-- status 'deleted'
--
-- RevokeStoryPublication menulis status ini ketika pemiliknya menghapus story
-- sebelum 24 jam. CHECK aslinya tidak mengenalnya, jadi setiap penghapusan dini
-- ditolak dengan 23514 dan publikasinya tetap terbaca "Tayang" padahal sudah
-- tidak ada.
-- -----------------------------------------------------------------------------
alter table public.story_publications
  drop constraint if exists story_publications_status_check;

alter table public.story_publications
  add constraint story_publications_status_check
  check (status in ('pending', 'processing', 'published', 'failed',
                    'cancelled', 'expired', 'deleted'));

-- -----------------------------------------------------------------------------
-- Kebijakan lama dibuang lebih dulu: keduanya menyebut campaign_id, dan kolom
-- itu tidak bisa dihapus selama masih ada yang bergantung padanya.
-- -----------------------------------------------------------------------------
drop policy if exists story_views_select          on public.story_views;
drop policy if exists story_view_snapshots_select on public.story_view_snapshots;

-- -----------------------------------------------------------------------------
-- story_views — kunci tunggal
-- -----------------------------------------------------------------------------
alter table public.story_views
  add column if not exists wa_message_id text;

do $$
begin
  if exists (
    select 1 from information_schema.columns
     where table_schema = 'public' and table_name = 'story_views'
       and column_name = 'publication_id'
  ) then
    execute $q$
      update public.story_views v
         set wa_message_id = p.wa_message_id
        from public.story_publications p
       where v.wa_message_id is null
         and v.publication_id = p.id
         and p.wa_message_id is not null
    $q$;
  end if;

  if exists (
    select 1 from information_schema.columns
     where table_schema = 'public' and table_name = 'story_views'
       and column_name = 'message_id'
  ) then
    execute $q$
      update public.story_views v
         set wa_message_id = m.wa_message_id
        from public.messages m
       where v.wa_message_id is null
         and v.message_id = m.id
         and m.wa_message_id is not null
    $q$;
  end if;
end $$;

-- Sebuah baris yang tidak bisa disebutkan statusnya tidak bisa dihitung oleh
-- siapa pun: induknya terbit tanpa pernah mendapat id pesan.
delete from public.story_views where wa_message_id is null;

-- Sebelum 0048 hanya ada satu jalur, jadi ini nyaris selalu nol baris. Tetap
-- dijalankan supaya kunci baru di bawah tidak pernah gagal dibuat.
delete from public.story_views v
 using public.story_views keep
 where v.account_id    = keep.account_id
   and v.wa_message_id = keep.wa_message_id
   and v.viewer_jid    = keep.viewer_jid
   and v.id > keep.id;

alter table public.story_views
  drop constraint if exists uq_story_view,
  drop constraint if exists story_views_target_present;

drop index if exists public.uq_status_view;
drop index if exists public.idx_story_views_message;
drop index if exists public.idx_story_views_campaign;

alter table public.story_views
  drop column if exists publication_id,
  drop column if exists campaign_id,
  drop column if exists message_id;

alter table public.story_views
  alter column wa_message_id set not null;

create unique index if not exists uq_story_view
  on public.story_views (account_id, wa_message_id, viewer_jid);

create index if not exists idx_story_views_status
  on public.story_views (account_id, wa_message_id);

comment on table public.story_views is
  'Batas bawah, bukan jumlah penonton sebenarnya: hanya receipt yang benar-benar diterima. Satu baris per (nomor, status, penonton). Dihapus setelah angkanya dibekukan di story_view_snapshots.';

-- -----------------------------------------------------------------------------
-- story_view_snapshots — angka yang bertahan
--
-- Dikunci pada status yang sama, bukan pada publikasi, supaya status yang
-- diposting langsung dari HP juga bisa membekukan angkanya. Tanpa ini angka
-- penonton status dari HP ikut hilang bersama pesannya di jam ke-24, sementara
-- angka penonton story terjadwal bertahan — dua nasib untuk fakta yang sama.
-- -----------------------------------------------------------------------------
alter table public.story_view_snapshots
  add column if not exists account_id uuid
    references public.whatsapp_accounts (id) on delete cascade,
  add column if not exists wa_message_id text;

do $$
begin
  if exists (
    select 1 from information_schema.columns
     where table_schema = 'public' and table_name = 'story_view_snapshots'
       and column_name = 'publication_id'
  ) then
    execute $q$
      update public.story_view_snapshots s
         set account_id    = p.account_id,
             wa_message_id = p.wa_message_id
        from public.story_publications p
       where s.wa_message_id is null
         and s.publication_id = p.id
         and p.wa_message_id is not null
    $q$;
  end if;
end $$;

delete from public.story_view_snapshots
 where account_id is null or wa_message_id is null;

drop index if exists public.uq_story_snapshot_final;
drop index if exists public.idx_story_snapshots_campaign;

alter table public.story_view_snapshots
  drop column if exists publication_id,
  drop column if exists campaign_id;

alter table public.story_view_snapshots
  alter column account_id    set not null,
  alter column wa_message_id set not null;

create unique index if not exists uq_story_snapshot_final
  on public.story_view_snapshots (account_id, wa_message_id) where is_final;

create index if not exists idx_story_snapshots_status
  on public.story_view_snapshots (account_id, wa_message_id);

comment on table public.story_view_snapshots is
  'Angka penonton yang dibekukan setelah status habis. Hanya angkanya: daftar penontonnya sengaja tidak disimpan setelah 24 jam.';

-- -----------------------------------------------------------------------------
-- status_view_counts — satu-satunya definisi "jumlah penonton"
--
-- Selama status masih hidup angkanya dihitung dari baris penonton; setelah
-- dibekukan angkanya diambil dari snapshot dan baris penontonnya sudah tidak
-- ada. Keduanya dijawab oleh satu view, jadi laporan Story dan panel Status di
-- menu Chat membaca angka yang sama persis, bukan dua hitungan yang kebetulan
-- mirip.
-- -----------------------------------------------------------------------------
create or replace view public.status_view_counts as
select s.account_id, s.wa_message_id, s.viewers, true as frozen
  from public.story_view_snapshots s
 where s.is_final
union all
select v.account_id, v.wa_message_id, count(*)::int as viewers, false as frozen
  from public.story_views v
 where not exists (
         select 1 from public.story_view_snapshots s
          where s.is_final
            and s.account_id    = v.account_id
            and s.wa_message_id = v.wa_message_id)
 group by v.account_id, v.wa_message_id;

-- Hak akses pembaca, bukan hak akses pemilik view: tanpa ini view melewati RLS
-- tabel di bawahnya dan jadi jalan pintas ke data workspace lain.
do $$ begin
  execute 'alter view public.status_view_counts set (security_invoker = on)';
exception when others then null; end $$;

comment on view public.status_view_counts is
  'Jumlah penonton per (nomor, status). Frozen = angka final setelah status habis. Satu-satunya sumber angka penonton untuk semua tampilan.';

-- -----------------------------------------------------------------------------
-- RLS
--
-- Cakupan workspace, lalu batasan aplikasi tetap dihormati lewat publikasinya
-- kalau status itu memang berasal dari sebuah campaign. Status yang diposting
-- langsung dari HP tidak punya campaign, jadi ia mengikuti aturan percakapan
-- Status biasa: terlihat dalam workspace-nya sendiri.
-- -----------------------------------------------------------------------------
do $$
declare
  t text;
begin
  foreach t in array array['story_views', 'story_view_snapshots'] loop
    execute format($f$
      create policy %I on public.%I
        for select to authenticated
        using (
          workspace_id = public.current_workspace_id()
          and not exists (
            select 1
              from public.story_publications p
              join public.content_campaigns c on c.id = p.campaign_id
             where p.account_id    = %I.account_id
               and p.wa_message_id = %I.wa_message_id
               and not (
                 c.application_id in (select public.visible_application_ids())
                 or (c.application_id is null
                     and coalesce(public.current_operational_role() = 'leader', true))
                 or c.created_by = auth.uid())
          )
        )
    $f$, t || '_select', t, t, t);
  end loop;
end $$;
