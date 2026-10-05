-- Setiap foreign key diberi index pada kolom yang menunjuk.
--
-- Postgres meng-index sisi yang ditunjuk, bukan sisi yang menunjuk. Jadi
-- menghapus satu baris induk memaksa pemindaian penuh setiap tabel anak yang
-- kolomnya tidak ter-index -- sekali untuk tiap baris yang dihapus.
--
-- Itu bukan teori. Menghapus satu akun WhatsApp dengan 5.039 kontak merembet
-- ke empat tabel tanpa index dan melewati dua menit tanpa menyelesaikan tabel
-- pertama; perkiraannya sekitar 3,3 miliar pembacaan baris. Yang terlihat oleh
-- admin hanyalah tombol Hapus yang berputar selamanya. Lebih buruk lagi,
-- perangkatnya sudah terlepas dari WhatsApp sebelum barisnya dihapus, jadi
-- kegagalan itu meninggalkan akun yang tidak bisa dihubungkan dan tidak bisa
-- dibuang.
--
-- Index ditulis tanpa CONCURRENTLY karena berkas migrasi dijalankan sebagai
-- satu transaksi. Di produksi index yang sama dipasang lebih dulu secara
-- concurrent, sehingga pernyataan di sini melewatinya tanpa mengunci apa pun.

create index if not exists idx_admin_activity_logs_account_id
  on public.admin_activity_logs (account_id);
create index if not exists idx_admin_activity_logs_pic_id
  on public.admin_activity_logs (pic_id);
create index if not exists idx_broadcast_sender_devices_account_id
  on public.broadcast_sender_devices (account_id);
create index if not exists idx_broadcast_sender_devices_workspace_id
  on public.broadcast_sender_devices (workspace_id);
create index if not exists idx_broadcast_target_attempts_account_id
  on public.broadcast_target_attempts (account_id);
create index if not exists idx_broadcast_target_attempts_message_id
  on public.broadcast_target_attempts (message_id);
create index if not exists idx_broadcast_target_attempts_workspace_id
  on public.broadcast_target_attempts (workspace_id);
create index if not exists idx_campaign_delivery_stats_publication_id
  on public.campaign_delivery_stats (publication_id);
create index if not exists idx_campaign_delivery_stats_workspace_id
  on public.campaign_delivery_stats (workspace_id);
create index if not exists idx_campaign_label_assignments_assigned_by
  on public.campaign_label_assignments (assigned_by);
create index if not exists idx_campaign_labels_created_by
  on public.campaign_labels (created_by);
create index if not exists idx_campaign_targets_conversation_id
  on public.campaign_targets (conversation_id);
create index if not exists idx_campaign_targets_message_id
  on public.campaign_targets (message_id);
create index if not exists idx_campaign_targets_workspace_id
  on public.campaign_targets (workspace_id);
create index if not exists idx_contact_first_seen_application_id
  on public.contact_first_seen (application_id);
create index if not exists idx_contact_first_seen_conversation_id
  on public.contact_first_seen (conversation_id);
create index if not exists idx_contact_first_seen_first_message_id
  on public.contact_first_seen (first_message_id);
create index if not exists idx_contact_first_seen_import_batch_id
  on public.contact_first_seen (import_batch_id);
create index if not exists idx_contact_first_seen_workspace_id
  on public.contact_first_seen (workspace_id);
create index if not exists idx_contact_label_events_conversation_id
  on public.contact_label_events (conversation_id);
create index if not exists idx_contact_label_events_from_label_id
  on public.contact_label_events (from_label_id);
create index if not exists idx_contact_label_events_to_label_id
  on public.contact_label_events (to_label_id);
create index if not exists idx_contact_label_state_application_id
  on public.contact_label_state (application_id);
create index if not exists idx_contact_label_state_workspace_id
  on public.contact_label_state (workspace_id);
create index if not exists idx_content_campaigns_account_id
  on public.content_campaigns (account_id);
create index if not exists idx_content_campaigns_cancelled_by
  on public.content_campaigns (cancelled_by);
create index if not exists idx_content_campaigns_creator_pic_id
  on public.content_campaigns (creator_pic_id);
create index if not exists idx_content_campaigns_scheduled_by
  on public.content_campaigns (scheduled_by);
create index if not exists idx_content_campaigns_updated_by
  on public.content_campaigns (updated_by);
create index if not exists idx_conversation_label_assignments_assigned_by
  on public.conversation_label_assignments (assigned_by);
create index if not exists idx_conversations_assigned_to
  on public.conversations (assigned_to);
create index if not exists idx_conversations_last_message_id
  on public.conversations (last_message_id);
create index if not exists idx_custom_variables_application_id
  on public.custom_variables (application_id);
create index if not exists idx_custom_variables_created_by
  on public.custom_variables (created_by);
create index if not exists idx_follow_up_events_response_message_id
  on public.follow_up_events (response_message_id);
create index if not exists idx_follow_up_events_trigger_message_id
  on public.follow_up_events (trigger_message_id);
create index if not exists idx_freelancer_application_assignments_assigned_by
  on public.freelancer_application_assignments (assigned_by);
create index if not exists idx_freelancer_pic_assignments_assigned_by
  on public.freelancer_pic_assignments (assigned_by);
create index if not exists idx_gpt_generation_logs_admin_id
  on public.gpt_generation_logs (admin_id);
create index if not exists idx_gpt_generation_logs_campaign_id
  on public.gpt_generation_logs (campaign_id);
create index if not exists idx_group_member_events_heard_by
  on public.group_member_events (heard_by);
create index if not exists idx_group_mentions_responder_admin_id
  on public.group_mentions (responder_admin_id);
create index if not exists idx_group_mentions_response_message_id
  on public.group_mentions (response_message_id);
create index if not exists idx_group_mentions_workspace_id
  on public.group_mentions (workspace_id);
create index if not exists idx_import_batches_workspace_id
  on public.import_batches (workspace_id);
create index if not exists idx_label_category_overrides_label_id
  on public.label_category_overrides (label_id);
create index if not exists idx_label_category_overrides_set_by
  on public.label_category_overrides (set_by);
create index if not exists idx_message_attachments_workspace_id
  on public.message_attachments (workspace_id);
create index if not exists idx_message_polls_account_id
  on public.message_polls (account_id);
create index if not exists idx_message_polls_workspace_id
  on public.message_polls (workspace_id);
create index if not exists idx_messages_sender_pic_id
  on public.messages (sender_pic_id);
create index if not exists idx_newsletters_workspace_id
  on public.newsletters (workspace_id);
create index if not exists idx_pic_application_assignments_assigned_by
  on public.pic_application_assignments (assigned_by);
create index if not exists idx_quick_replies_application_id
  on public.quick_replies (application_id);
create index if not exists idx_quick_replies_created_by
  on public.quick_replies (created_by);
create index if not exists idx_role_assignments_assigned_by
  on public.role_assignments (assigned_by);
create index if not exists idx_scheduled_publications_created_by
  on public.scheduled_publications (created_by);
create index if not exists idx_scheduled_publications_workspace_id
  on public.scheduled_publications (workspace_id);
create index if not exists idx_sla_cycles_first_response_message_id
  on public.sla_cycles (first_response_message_id);
create index if not exists idx_sla_cycles_schedule_id
  on public.sla_cycles (schedule_id);
create index if not exists idx_sla_targets_application_id
  on public.sla_targets (application_id);
create index if not exists idx_sla_targets_updated_by
  on public.sla_targets (updated_by);
create index if not exists idx_story_publications_account_id
  on public.story_publications (account_id);
create index if not exists idx_story_publications_workspace_id
  on public.story_publications (workspace_id);
create index if not exists idx_story_view_snapshots_workspace_id
  on public.story_view_snapshots (workspace_id);
create index if not exists idx_story_views_workspace_id
  on public.story_views (workspace_id);
create index if not exists idx_work_schedules_created_by
  on public.work_schedules (created_by);
