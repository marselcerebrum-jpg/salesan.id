-- =============================================================================
-- salesan.id — Migration 0027: PIC boleh merekrut Freelance-nya sendiri
-- =============================================================================
-- Migrasi 0019 menjadikan seluruh perubahan struktur organisasi milik Leader.
-- Itu benar untuk peran, dan terlalu ketat untuk penempatan: seorang PIC yang
-- menambah Freelance ke timnya harus menunggu Leader melakukannya, padahal PIC
-- itulah yang tahu timnya butuh siapa.
--
-- Yang diizinkan di sini persis sebatas itu:
--
--   PIC boleh — menempatkan Freelance di bawah DIRINYA SENDIRI, memberi
--               Freelance itu aplikasi yang DIA SENDIRI pegang, dan
--               menonaktifkannya.
--   PIC tidak boleh — mengubah peran siapa pun (promosi tetap keputusan
--               Leader), menyentuh tim PIC lain, memberi aplikasi yang bukan
--               tanggung jawabnya, atau mengubah dirinya sendiri.
--
-- Batas terakhir itu disengaja: mempersempit lingkup diri sendiri adalah satu-
-- satunya kesalahan yang tidak bisa diperbaiki sendiri.
--
-- Backend menegakkan aturan yang sama pada jalur API. Ini salinannya untuk
-- jalur Supabase langsung.
--
-- Aman dijalankan ulang.
-- =============================================================================

-- -----------------------------------------------------------------------------
-- role_assignments
--
-- Menambahkan Freelance baru boleh; mengubah peran yang sudah ada tidak.
-- Kebijakan INSERT dan UPDATE karena itu dipisah, bukan satu FOR ALL.
-- -----------------------------------------------------------------------------
drop policy if exists role_assignments_write on public.role_assignments;

drop policy if exists role_assignments_insert on public.role_assignments;
create policy role_assignments_insert on public.role_assignments
  for insert to authenticated
  with check (
    workspace_id = public.current_workspace_id()
    and (
      coalesce(public.current_operational_role() = 'leader', false)
      or (
        public.current_operational_role() = 'pic'
        and role = 'freelance'
        and user_id <> auth.uid()
      )
    )
  );

drop policy if exists role_assignments_update on public.role_assignments;
create policy role_assignments_update on public.role_assignments
  for update to authenticated
  using (
    workspace_id = public.current_workspace_id()
    and (
      coalesce(public.current_operational_role() = 'leader', false)
      or (
        -- A PIC may switch their own Freelance off, but not re-title them.
        public.current_operational_role() = 'pic'
        and role = 'freelance'
        and user_id in (
          select freelancer_user_id from public.freelancer_pic_assignments
           where pic_user_id = auth.uid())
      )
    )
  )
  with check (
    workspace_id = public.current_workspace_id()
    and (
      coalesce(public.current_operational_role() = 'leader', false)
      or (public.current_operational_role() = 'pic' and role = 'freelance')
    )
  );

drop policy if exists role_assignments_delete on public.role_assignments;
create policy role_assignments_delete on public.role_assignments
  for delete to authenticated
  using (
    workspace_id = public.current_workspace_id()
    and coalesce(public.current_operational_role() = 'leader', false)
  );

-- -----------------------------------------------------------------------------
-- freelancer_pic_assignments
--
-- Seorang PIC hanya bisa menulis baris yang menunjuk dirinya sendiri sebagai
-- atasan. Itulah yang membuat "merekrut ke tim saya" tidak bisa berubah menjadi
-- "memindahkan orang dari tim orang lain".
-- -----------------------------------------------------------------------------
drop policy if exists freelancer_pic_assignments_write on public.freelancer_pic_assignments;

drop policy if exists freelancer_pic_assignments_rw on public.freelancer_pic_assignments;
create policy freelancer_pic_assignments_rw on public.freelancer_pic_assignments
  for all to authenticated
  using (
    workspace_id = public.current_workspace_id()
    and (
      coalesce(public.current_operational_role() = 'leader', false)
      or (public.current_operational_role() = 'pic' and pic_user_id = auth.uid())
    )
  )
  with check (
    workspace_id = public.current_workspace_id()
    and (
      coalesce(public.current_operational_role() = 'leader', false)
      or (public.current_operational_role() = 'pic' and pic_user_id = auth.uid())
    )
  );

-- -----------------------------------------------------------------------------
-- freelancer_application_assignments
--
-- Aplikasi yang diberikan harus aplikasi yang PIC itu sendiri pegang, dan
-- orangnya harus ada di timnya. Dua syarat, karena melonggarkan salah satunya
-- sudah cukup untuk memberi akses yang tidak dimiliki si pemberi.
-- -----------------------------------------------------------------------------
drop policy if exists freelancer_application_assignments_write
  on public.freelancer_application_assignments;

drop policy if exists freelancer_application_assignments_rw
  on public.freelancer_application_assignments;
create policy freelancer_application_assignments_rw
  on public.freelancer_application_assignments
  for all to authenticated
  using (
    workspace_id = public.current_workspace_id()
    and (
      coalesce(public.current_operational_role() = 'leader', false)
      or (
        public.current_operational_role() = 'pic'
        and freelancer_user_id in (
          select freelancer_user_id from public.freelancer_pic_assignments
           where pic_user_id = auth.uid())
        and application_id in (
          select application_id from public.pic_application_assignments
           where pic_user_id = auth.uid())
      )
    )
  )
  with check (
    workspace_id = public.current_workspace_id()
    and (
      coalesce(public.current_operational_role() = 'leader', false)
      or (
        public.current_operational_role() = 'pic'
        and freelancer_user_id in (
          select freelancer_user_id from public.freelancer_pic_assignments
           where pic_user_id = auth.uid())
        and application_id in (
          select application_id from public.pic_application_assignments
           where pic_user_id = auth.uid())
      )
    )
  );

-- -----------------------------------------------------------------------------
-- pic_application_assignments tetap milik Leader sepenuhnya: menentukan
-- aplikasi mana yang dipegang seorang PIC adalah keputusan di atas PIC itu.
-- Kebijakannya dari 0019 dibiarkan apa adanya.
-- -----------------------------------------------------------------------------

-- -----------------------------------------------------------------------------
-- users.is_active
--
-- Menonaktifkan anggota menulis ke public.users, yang kebijakannya sejak 0001
-- hanya mengizinkan seseorang mengubah dirinya sendiri. Ditambah satu jalur:
-- Leader untuk siapa pun, PIC untuk Freelance di bawahnya.
-- -----------------------------------------------------------------------------
drop policy if exists users_update_members on public.users;
create policy users_update_members on public.users
  for update to authenticated
  using (
    workspace_id = public.current_workspace_id()
    and id <> auth.uid()
    and (
      coalesce(public.current_operational_role() = 'leader', false)
      or (
        public.current_operational_role() = 'pic'
        and id in (
          select freelancer_user_id from public.freelancer_pic_assignments
           where pic_user_id = auth.uid())
      )
    )
  )
  with check (workspace_id = public.current_workspace_id());
