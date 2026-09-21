-- =============================================================================
-- salesan.id — Migration 0025: menambahkan anggota ke workspace yang sudah ada
-- =============================================================================
-- Sampai sekarang setiap orang yang mendaftar SELALU mendapat workspace baru.
-- Itu benar untuk pendaftaran mandiri, dan salah untuk satu-satunya cara sebuah
-- tim bisa tumbuh: seorang Leader membuatkan akun untuk PIC dan Freelance-nya.
-- Tanpa perubahan ini, PIC yang baru dibuatkan akan berdiri sendiri di workspace
-- kosong miliknya — tidak melihat satu pun aplikasi, akun WhatsApp, atau chat
-- milik timnya.
--
-- Perubahannya minimal dan aditif: trigger yang sama sekarang membaca dua kunci
-- pada metadata pengguna baru.
--
--   salesan_workspace_id   — workspace yang harus dimasuki
--   salesan_workspace_role — peran kepemilikan di workspace itu
--
-- Bila keduanya tidak ada, perilakunya persis seperti sebelumnya: workspace baru
-- beserta daftar aplikasi awalnya. Pendaftaran mandiri tidak berubah sama
-- sekali.
--
-- Metadata ini hanya bisa ditulis lewat Admin API Supabase, yang memerlukan
-- service-role key dan karena itu tidak pernah ada di browser. Seorang pengguna
-- tidak bisa memasukkan dirinya sendiri ke workspace orang lain dengan
-- mendaftar sambil menebak sebuah UUID: id-nya diverifikasi ada, tetapi yang
-- benar-benar menjaganya adalah bahwa hanya backend yang dapat menulis metadata
-- itu.
--
-- Aman dijalankan ulang.
-- =============================================================================

create or replace function public.handle_new_auth_user()
returns trigger
language plpgsql
security definer
set search_path = public
as $$
declare
  v_workspace_id uuid;
  v_slug         text;
  v_name         text;
  v_invited      uuid;
  v_role         public.workspace_role;
begin
  v_name := coalesce(new.raw_user_meta_data ->> 'full_name', split_part(new.email, '@', 1));

  -- --- diundang ke workspace yang sudah ada ---------------------------------
  begin
    v_invited := nullif(new.raw_user_meta_data ->> 'salesan_workspace_id', '')::uuid;
  exception when invalid_text_representation then
    v_invited := null;
  end;

  if v_invited is not null
     and exists (select 1 from public.workspaces w where w.id = v_invited) then

    begin
      v_role := coalesce(
        nullif(new.raw_user_meta_data ->> 'salesan_workspace_role', ''),
        'agent'
      )::public.workspace_role;
    exception when invalid_text_representation then
      v_role := 'agent';
    end;

    -- Anggota yang diundang tidak pernah menjadi owner. Kepemilikan workspace
    -- berpindah lewat tindakan yang disengaja, bukan sebagai efek samping dari
    -- membuatkan seseorang akun.
    if v_role = 'owner' then
      v_role := 'admin';
    end if;

    insert into public.users (id, workspace_id, email, full_name, role)
    values (new.id, v_invited, new.email, v_name, v_role)
    on conflict (id) do nothing;

    -- Tidak ada aplikasi yang di-seed: workspace-nya sudah punya miliknya.
    return new;
  end if;

  -- --- pendaftaran mandiri: perilaku lama, tidak berubah ---------------------
  v_slug := lower(regexp_replace(split_part(new.email, '@', 1), '[^a-zA-Z0-9]+', '-', 'g'))
            || '-' || substr(replace(new.id::text, '-', ''), 1, 8);

  insert into public.workspaces (name, slug)
  values (coalesce(v_name, 'Workspace') || '''s Workspace', v_slug)
  returning id into v_workspace_id;

  insert into public.users (id, workspace_id, email, full_name, role)
  values (new.id, v_workspace_id, new.email, v_name, 'owner');

  insert into public.applications (workspace_id, code, name, color, sort_order) values
    (v_workspace_id, 'JADIASN',      'JADIASN',      '#1B7F5A',  1),
    (v_workspace_id, 'JADIBEASISWA', 'JADIBEASISWA', '#C2853A',  2),
    (v_workspace_id, 'JADIBUMN',     'JADIBUMN',     '#166534',  3),
    (v_workspace_id, 'JADIOJK',      'JADIOJK',      '#2563EB',  4),
    (v_workspace_id, 'JADIPCPM',     'JADIPCPM',     '#7C3AED',  5),
    (v_workspace_id, 'JADIPOLISI',   'JADIPOLISI',   '#DC2626',  6),
    (v_workspace_id, 'JADISEKDIN',   'JADISEKDIN',   '#D97706',  7),
    (v_workspace_id, 'TOEFLACADEMY', 'TOEFLACADEMY', '#0891B2',  8)
  on conflict do nothing;

  -- Label tetap tidak di-seed; lihat catatan pada migrasi 0001 dan 0005.
  return new;
end;
$$;

drop trigger if exists on_auth_user_created on auth.users;
create trigger on_auth_user_created
  after insert on auth.users
  for each row execute function public.handle_new_auth_user();

comment on function public.handle_new_auth_user() is
  'Membuat profil untuk pengguna auth baru. Dengan metadata salesan_workspace_id ia bergabung ke workspace yang sudah ada; tanpa itu ia mendapat workspace baru seperti sebelumnya.';

-- -----------------------------------------------------------------------------
-- Menonaktifkan anggota
--
-- Sebuah kolom, bukan penghapusan baris. Pesan yang pernah mereka kirim tetap
-- menunjuk ke profilnya lewat messages.sent_by, dan laporan performa bulan lalu
-- harus tetap bisa menyebut namanya. Menghapus orangnya akan mengosongkan
-- atribusi pada riwayat yang sudah terjadi.
-- -----------------------------------------------------------------------------
alter table public.users
  add column if not exists is_active boolean not null default true,
  add column if not exists deactivated_at timestamptz;

comment on column public.users.is_active is
  'False berarti anggota tidak lagi bertugas. Barisnya dipertahankan supaya atribusi pesan dan laporan lama tetap utuh.';
