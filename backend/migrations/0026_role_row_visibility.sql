-- =============================================================================
-- salesan.id — Migration 0026: batas baris per peran pada data performa
-- =============================================================================
-- Migrasi 0022–0024 membatasi data turunan berdasarkan APLIKASI. Itu benar untuk
-- Leader dan PIC, dan terlalu longgar untuk Freelance: dua Freelance pada
-- aplikasi yang sama akan saling melihat siklus SLA, follow-up, dan perubahan
-- label satu sama lain.
--
-- Aturan yang sebenarnya berlaku adalah:
--
--   Leader     — seluruh workspace.
--   PIC        — aplikasi yang dipegangnya, termasuk aktivitas yang tidak
--                teratribusi (dikirim dari HP, tanpa admin yang bisa
--                diverifikasi).
--   Freelance  — hanya baris yang menyebut dirinya sendiri.
--
-- Backend sudah menegakkannya pada jalur API. Ini salinannya untuk jalur
-- Supabase langsung, supaya keduanya menjawab pertanyaan yang sama — dan supaya
-- token seorang Freelance tidak bisa dipakai membaca angka rekan kerjanya
-- dengan melewati backend.
--
-- Aman dijalankan ulang.
-- =============================================================================

-- -----------------------------------------------------------------------------
-- Apakah pemanggil boleh melihat baris milik admin ini?
--
-- NULL berarti aktivitas tanpa atribusi. Leader dan PIC boleh melihatnya —
-- itulah bagian "Aktivitas perangkat/tidak teratribusi" — sedangkan Freelance
-- tidak, karena itu bukan pekerjaannya dan bukan pekerjaan siapa pun yang bisa
-- dipastikan.
-- -----------------------------------------------------------------------------
create or replace function public.member_row_visible(p_admin uuid)
returns boolean
language sql
stable
security definer
set search_path = public
as $$
  select case
    when public.current_operational_role() = 'freelance' then p_admin = auth.uid()
    else true
  end
$$;

revoke all on function public.member_row_visible(uuid) from public;
grant execute on function public.member_row_visible(uuid) to authenticated, service_role;

-- -----------------------------------------------------------------------------
-- Untuk tabel yang tidak menyebut admin sama sekali.
--
-- Sebuah leads, atau keadaan label sebuah kontak, tidak dimiliki siapa pun. Bagi
-- seorang Freelance, kontak itu relevan bila ia pernah membalasnya — dan itulah
-- satu-satunya kaitan yang benar-benar ada di data.
-- -----------------------------------------------------------------------------
create or replace function public.contact_touched_by_me(p_contact uuid)
returns boolean
language sql
stable
security definer
set search_path = public
as $$
  select case
    when public.current_operational_role() <> 'freelance' then true
    when p_contact is null then false
    else exists (
      select 1
        from public.messages m
        join public.conversations c on c.id = m.conversation_id
       where c.contact_id = p_contact
         and m.sent_by = auth.uid()
         and m.hidden_at is null
    )
  end
$$;

revoke all on function public.contact_touched_by_me(uuid) from public;
grant execute on function public.contact_touched_by_me(uuid) to authenticated, service_role;

-- =============================================================================
-- Kebijakan baca, ditulis ulang di atas aturan aplikasi yang sudah ada
-- =============================================================================

-- Bagian aplikasi tetap sama seperti 0022; yang ditambahkan adalah batas admin.
do $$
declare
  t     text;
  col   text;
  pairs text[][] := array[
    ['sla_cycles',          'responder_admin_id'],
    ['follow_up_events',    'admin_id'],
    ['group_mentions',      'responder_admin_id'],
    ['contact_label_events','admin_id']
  ];
  i int;
begin
  for i in 1 .. array_length(pairs, 1) loop
    t   := pairs[i][1];
    col := pairs[i][2];

    execute format('drop policy if exists %I on public.%I', t || '_select', t);
    execute format($f$
      create policy %I on public.%I
        for select to authenticated
        using (
          workspace_id = public.current_workspace_id()
          and (
            application_id in (select public.visible_application_ids())
            or (application_id is null
                and coalesce(public.current_operational_role() = 'leader', true))
          )
          and public.member_row_visible(%I)
        )
    $f$, t || '_select', t, col);
  end loop;
end $$;

-- Tabel tanpa kolom admin: dikaitkan lewat kontaknya.
drop policy if exists lead_classifications_select on public.lead_classifications;
create policy lead_classifications_select on public.lead_classifications
  for select to authenticated
  using (
    workspace_id = public.current_workspace_id()
    and (
      application_id in (select public.visible_application_ids())
      or (application_id is null
          and coalesce(public.current_operational_role() = 'leader', true))
    )
    and public.contact_touched_by_me(contact_id)
  );

drop policy if exists contact_first_seen_select on public.contact_first_seen;
create policy contact_first_seen_select on public.contact_first_seen
  for select to authenticated
  using (
    workspace_id = public.current_workspace_id()
    and (
      application_id in (select public.visible_application_ids())
      or (application_id is null
          and coalesce(public.current_operational_role() = 'leader', true))
    )
    and public.contact_touched_by_me(contact_id)
  );

drop policy if exists contact_label_state_select on public.contact_label_state;
create policy contact_label_state_select on public.contact_label_state
  for select to authenticated
  using (
    workspace_id = public.current_workspace_id()
    and (
      application_id in (select public.visible_application_ids())
      or (application_id is null
          and coalesce(public.current_operational_role() = 'leader', true))
    )
    and public.contact_touched_by_me(contact_id)
  );

-- Campaign: seorang Freelance melihat miliknya sendiri saja.
drop policy if exists content_campaigns_select on public.content_campaigns;
create policy content_campaigns_select on public.content_campaigns
  for select to authenticated
  using (
    workspace_id = public.current_workspace_id()
    and (
      created_by = auth.uid()
      or (
        public.member_row_visible(created_by)
        and (
          application_id in (select public.visible_application_ids())
          or (application_id is null
              and coalesce(public.current_operational_role() = 'leader', true))
        )
      )
    )
  );

-- Audit log: sama, dengan pengecualian bahwa seseorang selalu melihat jejaknya
-- sendiri.
drop policy if exists admin_activity_logs_select on public.admin_activity_logs;
create policy admin_activity_logs_select on public.admin_activity_logs
  for select to authenticated
  using (
    workspace_id = public.current_workspace_id()
    and (
      admin_id = auth.uid()
      or (
        public.member_row_visible(admin_id)
        and (
          application_id in (select public.visible_application_ids())
          or admin_id in (select public.visible_admin_ids())
          or (application_id is null
              and coalesce(public.current_operational_role() = 'leader', true))
        )
      )
    )
  );

-- -----------------------------------------------------------------------------
-- Jadwal kerja: seorang Freelance melihat jadwalnya sendiri.
--
-- Kebijakan 0020 memakai visible_admin_ids() ATAU aplikasi yang terlihat, dan
-- cabang kedua itulah yang membuat jadwal rekan satu aplikasi ikut terbaca.
-- -----------------------------------------------------------------------------
drop policy if exists work_schedules_select on public.work_schedules;
create policy work_schedules_select on public.work_schedules
  for select to authenticated
  using (
    workspace_id = public.current_workspace_id()
    and (
      case
        when public.current_operational_role() = 'freelance' then user_id = auth.uid()
        else user_id in (select public.visible_admin_ids())
             or (application_id is not null
                 and application_id in (select public.visible_application_ids()))
      end
    )
  );

-- -----------------------------------------------------------------------------
-- Indeks pendukung
--
-- contact_touched_by_me menelusuri pesan keluar milik satu admin; tanpa indeks
-- ini kebijakan di atas akan memindai tabel pesan untuk setiap baris leads.
-- -----------------------------------------------------------------------------
create index if not exists idx_messages_sent_by_conversation
  on public.messages (sent_by, conversation_id)
  where sent_by is not null and hidden_at is null;
