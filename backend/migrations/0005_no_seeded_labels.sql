-- =============================================================================
-- salesan.id — Migration 0005: berhenti menyemai label bawaan
-- =============================================================================
-- Label harus mencerminkan apa yang benar-benar ada di WhatsApp, bukan tebakan.
-- Migrasi 0001 menyemai empat label contoh (FU COLD, Jum'at, FU HOT, Premium)
-- pada setiap pendaftaran; itu membuat baris filter inbox menampilkan tag yang
-- tidak pernah dipakai siapa pun dan tidak ada padanannya di HP.
--
-- Setelah ini, label hanya lahir dari dua sumber:
--   1. sinkronisasi WhatsApp Business  (source = 'whatsapp')
--   2. dibuat sendiri lewat aplikasi   (source = 'manual')
--
-- Aplikasi (JADIASN, JADIBUMN, ...) tetap disemai: itu konsep salesan.id, bukan
-- sesuatu yang bisa diambil dari WhatsApp.
--
-- Aman dijalankan ulang.
--   go run ./cmd/migrate -file migrations/0005_no_seeded_labels.sql
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
begin
  v_name := coalesce(new.raw_user_meta_data ->> 'full_name', split_part(new.email, '@', 1));
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

  -- Tidak ada penyemaian label di sini, disengaja. Lihat catatan di atas.

  return new;
end;
$$;
