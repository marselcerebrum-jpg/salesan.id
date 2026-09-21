-- =============================================================================
-- salesan.id — Migration 0028: penanda di dalam / di luar jadwal
-- =============================================================================
-- Jadwal kerja BUKAN penentu siapa yang melakukan sebuah aktivitas. Pelakunya
-- selalu akun yang login; jadwal hanya dipakai untuk satu pertanyaan lain:
-- apakah pekerjaan itu terjadi pada jam yang memang dijadwalkan.
--
-- Karena itu penandanya disimpan di baris aktivitas, bukan dipakai untuk
-- menebak pelakunya. Aktivitas di luar jadwal tetap tercatat penuh pada akun
-- pelaksananya, hanya diberi tanda.
--
-- Nilainya dihitung saat baris ditulis, bukan saat dibaca. Dua alasan:
--
--   1. Jadwal bisa diubah kemudian. "Apakah saat itu ia sedang dijadwalkan"
--      adalah fakta tentang saat itu, dan mengubah rota bulan depan tidak boleh
--      menulis ulang catatan bulan lalu.
--   2. Menghitungnya saat dibaca berarti menggabungkan tabel pesan dengan tabel
--      jadwal pada setiap laporan, untuk jawaban yang tidak pernah berubah.
--
-- Aman dijalankan ulang.
-- =============================================================================

-- -----------------------------------------------------------------------------
-- Apakah seorang admin dijadwalkan pada satu momen?
--
-- NULL untuk aktivitas tanpa pelaku (dikirim dari HP): tidak ada orang yang
-- jadwalnya bisa dibandingkan, dan memaksakan true/false di situ akan
-- memasukkan aktivitas tak teratribusi ke dalam angka kepatuhan seseorang.
-- -----------------------------------------------------------------------------
create or replace function public.is_within_schedule(p_user uuid, p_at timestamptz)
returns boolean
language sql
stable
as $$
  select case
    when p_user is null then null
    else exists (
      select 1
        from public.work_schedules s
       where s.user_id = p_user
         and s.is_active
         and p_at >= ((s.work_date + s.starts_at) at time zone s.timezone)
         and p_at <  ((s.work_date + s.ends_at)   at time zone s.timezone)
    )
  end
$$;

comment on function public.is_within_schedule(uuid, timestamptz) is
  'Apakah admin ini punya blok jadwal aktif yang mencakup momen tersebut. NULL bila tidak ada pelaku yang bisa diverifikasi.';

-- -----------------------------------------------------------------------------
-- messages.in_schedule
-- -----------------------------------------------------------------------------
alter table public.messages
  add column if not exists in_schedule boolean;

comment on column public.messages.in_schedule is
  'True bila pengirimnya sedang dalam jadwal saat pesan dikirim, false bila di luar jadwal, NULL bila pesan masuk atau tidak teratribusi. Jadwal hanya pembanding kepatuhan, tidak pernah dipakai menebak pelaku.';

-- Dihitung sekali pada saat menulis, ikut menumpang trigger atribusi yang sudah
-- ada supaya jalur tulis pesan tidak bertambah satu trigger lagi.
create or replace function public.default_message_attribution()
returns trigger
language plpgsql
as $$
begin
  if new.from_me and new.sender_source is null then
    if new.sent_by is not null then
      new.sender_source := 'web_admin';
    else
      -- Dikirim dari HP. Identitasnya tidak dapat diverifikasi, jadi ia tetap
      -- tidak teratribusi; menebaknya dari jadwal dilarang secara eksplisit.
      new.sender_source := 'whatsapp_device';
    end if;
  end if;

  if new.from_me and new.sender_role is null and new.sent_by is not null then
    select ra.role into new.sender_role
      from public.role_assignments ra
     where ra.user_id = new.sent_by and ra.is_active
     limit 1;
  end if;

  if new.from_me and new.sender_pic_id is null and new.sent_by is not null then
    select fp.pic_user_id into new.sender_pic_id
      from public.freelancer_pic_assignments fp
     where fp.freelancer_user_id = new.sent_by;
  end if;

  if new.from_me and new.in_schedule is null and new.sent_by is not null then
    new.in_schedule := public.is_within_schedule(new.sent_by, new.timestamp);
  end if;

  return new;
end;
$$;

drop trigger if exists trg_messages_attribution on public.messages;
create trigger trg_messages_attribution
  before insert on public.messages
  for each row execute function public.default_message_attribution();

-- Riwayat yang sudah ada dinilai dengan aturan yang sama. Jadwal baru diisi
-- belakangan, jadi sebagian besar baris lama akan menjadi false — itu jawaban
-- yang benar: pada saat itu memang tidak ada jadwal yang mencakupnya.
update public.messages m
   set in_schedule = public.is_within_schedule(m.sent_by, m.timestamp)
 where m.from_me and m.sent_by is not null and m.in_schedule is null;

create index if not exists idx_messages_out_of_schedule
  on public.messages (sent_by, timestamp desc)
  where from_me and in_schedule = false;

-- -----------------------------------------------------------------------------
-- admin_activity_logs.in_schedule
--
-- Aktivitas non-chat — membuat campaign, mengubah jadwal, memasang label —
-- dinilai dengan aturan yang sama, supaya "di luar jadwal" berarti satu hal di
-- seluruh laporan.
-- -----------------------------------------------------------------------------
alter table public.admin_activity_logs
  add column if not exists in_schedule boolean;

create or replace function public.default_activity_schedule()
returns trigger
language plpgsql
as $$
begin
  if new.in_schedule is null and new.admin_id is not null then
    new.in_schedule := public.is_within_schedule(new.admin_id, new.occurred_at);
  end if;
  return new;
end;
$$;

drop trigger if exists trg_activity_schedule on public.admin_activity_logs;
create trigger trg_activity_schedule
  before insert on public.admin_activity_logs
  for each row execute function public.default_activity_schedule();

update public.admin_activity_logs
   set in_schedule = public.is_within_schedule(admin_id, occurred_at)
 where admin_id is not null and in_schedule is null;

create index if not exists idx_activity_out_of_schedule
  on public.admin_activity_logs (admin_id, occurred_at desc)
  where in_schedule = false;

-- -----------------------------------------------------------------------------
-- contact_label_events.in_schedule
-- -----------------------------------------------------------------------------
alter table public.contact_label_events
  add column if not exists in_schedule boolean;

create or replace function public.default_label_event_schedule()
returns trigger
language plpgsql
as $$
begin
  if new.in_schedule is null and new.admin_id is not null then
    new.in_schedule := public.is_within_schedule(new.admin_id, new.occurred_at);
  end if;
  return new;
end;
$$;

drop trigger if exists trg_label_event_schedule on public.contact_label_events;
create trigger trg_label_event_schedule
  before insert on public.contact_label_events
  for each row execute function public.default_label_event_schedule();

update public.contact_label_events
   set in_schedule = public.is_within_schedule(admin_id, occurred_at)
 where admin_id is not null and in_schedule is null;

-- -----------------------------------------------------------------------------
-- Indeks untuk metrik kontak unik
--
-- "Kontak Menghubungi" dan "Kontak Terlayani" adalah hitungan kontak unik atas
-- rentang waktu, bukan penjumlahan angka harian. Keduanya memindai pesan per
-- percakapan dalam rentang; indeks ini yang membuatnya tidak menjadi seq scan.
-- -----------------------------------------------------------------------------
create index if not exists idx_messages_inbound_window
  on public.messages (account_id, timestamp desc)
  where not from_me and hidden_at is null;

create index if not exists idx_messages_web_outbound_window
  on public.messages (account_id, timestamp desc)
  where from_me and sender_source = 'web_admin' and hidden_at is null;
