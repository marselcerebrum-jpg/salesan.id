-- =============================================================================
-- salesan.id — Migration 0002: seed data (preview only)
-- =============================================================================
-- Run this AFTER you have signed up at least one user through Supabase Auth.
-- It attaches demo applications, accounts, contacts, conversations and messages
-- to the FIRST workspace it finds so the UI has something to render before any
-- real WhatsApp device is linked.
--
-- Seeded accounts are created with status 'disconnected' and no JID, so the
-- backend will never try to open a whatsmeow socket for them. Deleting them is
-- safe at any time:
--
--   delete from public.whatsapp_accounts where device_id like 'D-SEED%';
--
-- Optional: pin the target workspace by email instead of "first found" —
--   select id from public.users where email = 'you@example.com';
-- =============================================================================

do $$
declare
  v_ws        uuid;
  v_user      uuid;
  v_app_asn   uuid;
  v_app_sek   uuid;
  v_app_bumn  uuid;
  v_acc_grup  uuid;
  v_acc_utama uuid;
  v_acc_asn   uuid;
  v_contact   uuid;
  v_conv      uuid;
  v_label_prem uuid;
  v_label_hot  uuid;
  r           record;
  i           int;
begin
  select u.workspace_id, u.id into v_ws, v_user
    from public.users u
   order by u.created_at asc
   limit 1;

  if v_ws is null then
    raise notice 'No user found in public.users — sign up first, then re-run 0002_seed.sql.';
    return;
  end if;

  raise notice 'Seeding workspace %', v_ws;

  -- --- applications (idempotent; 0001 already creates these on signup) -------
  insert into public.applications (workspace_id, code, name, color, sort_order) values
    (v_ws, 'JADIASN',      'JADIASN',      '#1B7F5A', 1),
    (v_ws, 'JADIBEASISWA', 'JADIBEASISWA', '#C2853A', 2),
    (v_ws, 'JADIBUMN',     'JADIBUMN',     '#166534', 3),
    (v_ws, 'JADIOJK',      'JADIOJK',      '#2563EB', 4),
    (v_ws, 'JADIPCPM',     'JADIPCPM',     '#7C3AED', 5),
    (v_ws, 'JADIPOLISI',   'JADIPOLISI',   '#DC2626', 6),
    (v_ws, 'JADIPPPK',     'JADIPPPK',     '#CA8A04', 7),
    (v_ws, 'JADIPRAJURIT', 'JADIPRAJURIT', '#15803D', 8),
    (v_ws, 'JADISEKDIN',   'JADISEKDIN',   '#D97706', 9),
    (v_ws, 'JAGOTPA',      'JAGOTPA',      '#EA580C', 10),
    (v_ws, 'PSIKOTESKERJA','PSIKOTESKERJA','#0891B2', 11),
    (v_ws, 'TOEFLACADEMY', 'TOEFLACADEMY', '#B91C1C', 12),
    (v_ws, 'CEREBRUM',     'CEREBRUM',     '#9F1239', 13)
  on conflict (workspace_id, code) do nothing;

  select id into v_app_asn  from public.applications where workspace_id = v_ws and code = 'JADIASN';
  select id into v_app_sek  from public.applications where workspace_id = v_ws and code = 'JADISEKDIN';
  select id into v_app_bumn from public.applications where workspace_id = v_ws and code = 'JADIBUMN';

  -- --- labels ---------------------------------------------------------------
  -- Tidak ada label contoh di sini, disengaja. Label harus datang dari
  -- sinkronisasi WhatsApp Business atau dibuat sendiri lewat aplikasi; menyemai
  -- nama karangan hanya mengisi baris filter inbox dengan tag yang tidak ada
  -- padanannya di HP mana pun.
  v_label_prem := null;
  v_label_hot  := null;

  -- --- accounts -------------------------------------------------------------
  insert into public.whatsapp_accounts
    (workspace_id, application_id, name, label, phone_number, device_id, connection_method, status)
  values
    (v_ws, v_app_sek,  'Jadisekdinofficial', 'HP GRUP',   '6289612715604', 'D-SEED01', 'qr', 'disconnected'),
    (v_ws, v_app_sek,  'Admin Jadisekdin',   'HP UTAMA',  '6289612715622', 'D-SEED02', 'qr', 'disconnected'),
    (v_ws, v_app_asn,  'Tim Pembimbing JadiAsn', 'HP UTAMA', '62895401388409', 'D-SEED03', 'qr', 'disconnected'),
    (v_ws, v_app_bumn, 'Admin BUMN',         'HP SAVE KONTAK 1', '628998814208', 'D-SEED04', 'qr', 'disconnected')
  on conflict (workspace_id, device_id) do nothing;

  select id into v_acc_grup  from public.whatsapp_accounts where workspace_id = v_ws and device_id = 'D-SEED01';
  select id into v_acc_utama from public.whatsapp_accounts where workspace_id = v_ws and device_id = 'D-SEED02';
  select id into v_acc_asn   from public.whatsapp_accounts where workspace_id = v_ws and device_id = 'D-SEED03';

  -- --- contacts + conversations for the "HP GRUP" account -------------------
  for r in
    select * from (values
      ('6281234500001', 'ROZEE',            'personal', 'Selamat pagi kak stis habis tes skd tes apalagi', 1, 'new'),
      ('6281234500002', 'ya',               'personal', 'TO GRATIS SKD KEDINASAN #4 Tryout Online',        1, 'new'),
      ('6281234500003', 'Sakala',           'personal', 'Bagaimana cara ganti kata sandi akun jadisekdin', 2, 'in_progress'),
      ('6281234500004', 'Bu Uripah',        'personal', 'Halo kak, saya mau Paket Lolos SKD 2026, bisa dibantu?', 1, 'new'),
      ('6281234500005', 'Titi Sumargiyati', 'personal', 'Halo kak, saya mau Paket Lolos SKD 2026, bisa dibantu?', 1, 'new'),
      ('6281234500006', 'elma aulia',       'personal', 'Halo kak, saya mau Paket Lolos SKD 2026, bisa dibantu?', 1, 'done'),
      ('6281234500007', 'PA,LI',            'personal', 'Halo kak, saya mau Paket Lolos SKD 2026, bisa dibantu?', 1, 'new')
    ) as t(phone, nama, tipe, preview, unread, status)
  loop
    insert into public.contacts (workspace_id, account_id, jid, phone_number, name, push_name)
    values (v_ws, v_acc_grup, r.phone || '@s.whatsapp.net', r.phone, r.nama, r.nama)
    on conflict (account_id, jid) do update set name = excluded.name
    returning id into v_contact;

    insert into public.conversations
      (workspace_id, account_id, contact_id, chat_jid, type, name, status,
       unread_count, last_message_at, last_message_preview, last_message_from_me)
    values
      (v_ws, v_acc_grup, v_contact, r.phone || '@s.whatsapp.net', r.tipe::public.conversation_type,
       r.nama, r.status::public.conversation_status, r.unread,
       now() - (random() * interval '20 hours'), r.preview, false)
    on conflict (account_id, chat_jid) do nothing;
  end loop;

  -- two group chats
  for r in
    select * from (values
      ('120363000000000018@g.us', '18 JADISEKDIN - SEKOLAH KEDINASAN', 'Timpa teks bang, timpa menimpa', 243),
      ('120363000000000005@g.us', '05 JADISEKDIN - SEKOLAH KEDINASAN', 'Bisa diulang berapa kali kak tryoutnya', 4)
    ) as t(jid, nama, preview, unread)
  loop
    insert into public.conversations
      (workspace_id, account_id, chat_jid, type, name, status,
       unread_count, last_message_at, last_message_preview, last_message_from_me)
    values
      (v_ws, v_acc_grup, r.jid, 'group', r.nama, 'new', r.unread,
       now() - (random() * interval '3 hours'), r.preview, false)
    on conflict (account_id, chat_jid) do nothing;
  end loop;

  -- a handful of conversations on the other two accounts so the app/number
  -- pickers (reference screens 4 and 5) show non-zero counts
  for i in 1..6 loop
    insert into public.contacts (workspace_id, account_id, jid, phone_number, name)
    values (v_ws, v_acc_utama, '62812346000' || i || '@s.whatsapp.net', '62812346000' || i, 'Peserta Sekdin ' || i)
    on conflict (account_id, jid) do nothing;

    insert into public.conversations
      (workspace_id, account_id, chat_jid, type, name, status, unread_count,
       last_message_at, last_message_preview, last_message_from_me)
    values
      (v_ws, v_acc_utama, '62812346000' || i || '@s.whatsapp.net', 'personal',
       'Peserta Sekdin ' || i, 'new', (i % 3), now() - (i * interval '35 minutes'),
       'Kak, jadwal tryout berikutnya kapan ya?', false)
    on conflict (account_id, chat_jid) do nothing;

    insert into public.contacts (workspace_id, account_id, jid, phone_number, name)
    values (v_ws, v_acc_asn, '62812347000' || i || '@s.whatsapp.net', '62812347000' || i, 'Peserta ASN ' || i)
    on conflict (account_id, jid) do nothing;

    insert into public.conversations
      (workspace_id, account_id, chat_jid, type, name, status, unread_count,
       last_message_at, last_message_preview, last_message_from_me)
    values
      (v_ws, v_acc_asn, '62812347000' || i || '@s.whatsapp.net', 'personal',
       'Peserta ASN ' || i, 'in_progress', (i % 2), now() - (i * interval '50 minutes'),
       'Terima kasih kak infonya', true)
    on conflict (account_id, chat_jid) do nothing;
  end loop;

  -- --- a full message thread on one conversation ----------------------------
  select id into v_conv from public.conversations
   where account_id = v_acc_grup and chat_jid = '6281234500003@s.whatsapp.net';

  if v_conv is not null then
    insert into public.messages
      (workspace_id, account_id, conversation_id, wa_message_id, sender_jid, sender_name,
       from_me, type, body, status, timestamp)
    values
      (v_ws, v_acc_grup, v_conv, 'SEED-MSG-0001', '6281234500003@s.whatsapp.net', 'Sakala',
       false, 'text', 'Halo kak, selamat pagi', 'read',        now() - interval '3 hours'),
      (v_ws, v_acc_grup, v_conv, 'SEED-MSG-0002', null, 'Admin',
       true,  'text', 'Halo kak Sakala, ada yang bisa dibantu?', 'read', now() - interval '2 hours 55 minutes'),
      (v_ws, v_acc_grup, v_conv, 'SEED-MSG-0003', '6281234500003@s.whatsapp.net', 'Sakala',
       false, 'text', 'Bagaimana cara ganti kata sandi akun jadisekdin', 'delivered', now() - interval '2 hours'),
      (v_ws, v_acc_grup, v_conv, 'SEED-MSG-0004', '6281234500003@s.whatsapp.net', 'Sakala',
       false, 'text', 'Sudah saya coba lupa password tapi email tidak masuk', 'delivered', now() - interval '1 hour 58 minutes')
    on conflict (account_id, wa_message_id) do nothing;

    -- the insert trigger recomputed unread_count; pin it back to the demo value
    update public.conversations set unread_count = 2, status = 'in_progress' where id = v_conv;
  end if;

  raise notice 'Seed complete (tanpa label — label hanya dari sinkronisasi WhatsApp).';
end $$;
