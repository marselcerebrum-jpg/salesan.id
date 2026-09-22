-- Balas cepat boleh dikelola Freelance juga.
--
-- Sebelumnya menulis balas cepat dibatasi Leader dan PIC, mengikuti label
-- campaign dan variabel. Operator meminta agar Freelance, yang paling sering
-- memakainya, juga bisa membuat dan mengubahnya. Jangkauannya tetap dibatasi:
-- hanya aplikasi yang ditugaskan kepadanya (visible_application_ids), dan
-- balas cepat "semua aplikasi" (application_id null) tetap hanya milik Leader.
--
-- Klausa untuk baris tanpa aplikasi juga diperbaiki: versi lama membiarkan
-- `application_id is null` lolos untuk siapa pun, padahal komentarnya sendiri
-- bilang itu wewenang Leader. Backend sudah menegakkan aturan yang benar;
-- kebijakan ini kini menyatakan hal yang sama.

drop policy if exists quick_replies_write on public.quick_replies;
create policy quick_replies_write on public.quick_replies
  for all to authenticated
  using (
    workspace_id = public.current_workspace_id()
    and coalesce(public.current_operational_role() in ('leader', 'pic', 'freelance'), true)
    and (
      coalesce(public.current_operational_role(), 'leader') = 'leader'
      or (application_id is not null
          and application_id in (select public.visible_application_ids()))
    )
  )
  with check (
    workspace_id = public.current_workspace_id()
    and coalesce(public.current_operational_role() in ('leader', 'pic', 'freelance'), true)
    and (
      coalesce(public.current_operational_role(), 'leader') = 'leader'
      or (application_id is not null
          and application_id in (select public.visible_application_ids()))
    )
  );
