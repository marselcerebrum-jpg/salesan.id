-- =============================================================================
-- salesan.id — Migration 0031: satu campaign, satu aplikasi
-- =============================================================================
-- Sampai sekarang sebuah campaign boleh memakai perangkat dari aplikasi mana
-- pun yang bisa dilihat pembuatnya, dan application_id-nya boleh kosong. Itu
-- merusak tiga hal sekaligus:
--
--   - Laporan. "Broadcast aplikasi TOEFL" tidak bisa dijawab kalau separuh
--     penerimanya dikirim dari nomor aplikasi lain.
--   - Retry. Target yang gagal harus kembali ke perangkat yang sama; kalau
--     campaign-nya lintas aplikasi, "perangkat yang sama" jadi ambigu.
--   - Akses. Seorang PIC hanya berwenang atas aplikasinya. Campaign lintas
--     aplikasi berarti satu objek yang sebagian di dalam wewenangnya dan
--     sebagian di luar — dan tidak ada jawaban benar untuk "boleh dibuka atau
--     tidak".
--
-- Berkas ini menutupnya di level basis data, bukan hanya di backend, karena
-- aturan yang hanya hidup di kode akan dilanggar oleh kode berikutnya.
--
-- Seluruhnya aditif. Tidak ada kolom yang dihapus, tidak ada baris yang
-- dibuang, dan campaign lama yang terlanjur tanpa aplikasi dibiarkan apa adanya
-- setelah diusahakan diisi — memaksakan NOT NULL akan menghapus riwayat yang
-- tidak bisa direkonstruksi.
-- =============================================================================

-- -----------------------------------------------------------------------------
-- Backfill: simpulkan aplikasi campaign lama dari perangkatnya sendiri
--
-- Dua sumber, dari yang paling dapat dipercaya. Perangkat pengirim adalah bukti
-- terkuat; account_id pada campaign adalah peninggalan sebelum multi-perangkat
-- dan dipakai sebagai cadangan.
-- -----------------------------------------------------------------------------
update public.content_campaigns cc
   set application_id = sub.application_id
  from (
    select d.campaign_id, min(a.application_id::text)::uuid as application_id
      from public.broadcast_sender_devices d
      join public.whatsapp_accounts a on a.id = d.account_id
     where a.application_id is not null
     group by d.campaign_id
    having count(distinct a.application_id) = 1
  ) sub
 where sub.campaign_id = cc.id
   and cc.application_id is null;

update public.content_campaigns cc
   set application_id = a.application_id
  from public.whatsapp_accounts a
 where a.id = cc.account_id
   and cc.application_id is null
   and a.application_id is not null;

-- -----------------------------------------------------------------------------
-- Aplikasi wajib untuk campaign baru
--
-- Ditegakkan lewat trigger, bukan NOT NULL. Alasannya: campaign lama yang
-- perangkatnya sudah dilepas dari aplikasi tidak bisa diisi, dan menolak
-- seluruh tabel karena beberapa baris sejarah akan mengorbankan riwayat demi
-- kerapian. Trigger ini menolak baris BARU tanpa aplikasi, dan menolak
-- pengosongan aplikasi pada baris yang sudah punya — dua hal yang benar-benar
-- perlu dicegah.
-- -----------------------------------------------------------------------------
create or replace function public.campaign_requires_application()
returns trigger
language plpgsql
as $$
begin
  if tg_op = 'INSERT' and new.application_id is null then
    raise exception 'campaign harus punya application_id'
      using errcode = 'check_violation';
  end if;

  if tg_op = 'UPDATE'
     and new.application_id is null
     and old.application_id is not null then
    raise exception 'application_id campaign tidak boleh dikosongkan'
      using errcode = 'check_violation';
  end if;

  -- Memindahkan campaign yang sudah punya perangkat ke aplikasi lain akan
  -- membuat perangkatnya asing terhadap aplikasinya sendiri.
  if tg_op = 'UPDATE'
     and new.application_id is distinct from old.application_id
     and exists (select 1 from public.broadcast_sender_devices d where d.campaign_id = new.id) then
    raise exception 'aplikasi campaign tidak dapat diubah setelah perangkat pengirim ditetapkan'
      using errcode = 'check_violation';
  end if;

  return new;
end;
$$;

drop trigger if exists trg_campaign_requires_application on public.content_campaigns;
create trigger trg_campaign_requires_application
  before insert or update on public.content_campaigns
  for each row execute function public.campaign_requires_application();

-- -----------------------------------------------------------------------------
-- Perangkat campaign harus berasal dari aplikasi campaign itu
--
-- Ini inti dari migration ini. Berlaku untuk perangkat pengirim Broadcast dan
-- untuk publikasi Story, karena keduanya adalah "nomor yang dipakai campaign
-- ini" dengan nama berbeda.
-- -----------------------------------------------------------------------------
create or replace function public.campaign_device_matches_application()
returns trigger
language plpgsql
as $$
declare
  campaign_app uuid;
  device_app   uuid;
begin
  select application_id into campaign_app
    from public.content_campaigns where id = new.campaign_id;
  select application_id into device_app
    from public.whatsapp_accounts where id = new.account_id;

  if campaign_app is null then
    raise exception 'campaign % belum punya aplikasi, perangkat tidak dapat ditambahkan', new.campaign_id
      using errcode = 'check_violation';
  end if;

  if device_app is distinct from campaign_app then
    raise exception 'perangkat % bukan milik aplikasi campaign ini', new.account_id
      using errcode = 'check_violation';
  end if;

  return new;
end;
$$;

drop trigger if exists trg_broadcast_device_application on public.broadcast_sender_devices;
create trigger trg_broadcast_device_application
  before insert or update on public.broadcast_sender_devices
  for each row execute function public.campaign_device_matches_application();

drop trigger if exists trg_story_publication_application on public.story_publications;
create trigger trg_story_publication_application
  before insert or update on public.story_publications
  for each row execute function public.campaign_device_matches_application();

comment on function public.campaign_device_matches_application() is
  'Menjaga satu campaign tetap berada dalam satu aplikasi. Dilonggarkan berarti laporan, retry, dan batas akses PIC ikut kehilangan artinya.';

-- -----------------------------------------------------------------------------
-- Indeks untuk daftar Broadcast/Story yang difilter per aplikasi
-- -----------------------------------------------------------------------------
create index if not exists idx_campaigns_app_type
  on public.content_campaigns (application_id, campaign_type, created_at desc);

-- Pencarian nama campaign pada daftar.
create index if not exists idx_campaigns_name
  on public.content_campaigns (workspace_id, lower(name));

-- -----------------------------------------------------------------------------
-- Percakapan yang masih menunggu balasan, diurut dari yang paling lama
--
-- Kartu "Masih Menunggu Balasan" membuka daftar ini, dan daftarnya diurutkan
-- menurut started_at. Tanpa indeks parsial ini setiap pembukaan daftar memindai
-- seluruh siklus SLA yang pernah ada.
-- -----------------------------------------------------------------------------
create index if not exists idx_sla_waiting_oldest
  on public.sla_cycles (workspace_id, started_at)
  where status = 'waiting';
