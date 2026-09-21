-- -----------------------------------------------------------------------------
-- Masa simpan media di sisi kami
--
-- Berkas media disalin ke bucket privat supaya bisa dibuka dari web tanpa
-- meminta ulang ke WhatsApp setiap kali. Salinan itu tumbuh selamanya: satu
-- nomor yang ramai bisa menambah beberapa GB per bulan, dan hampir semuanya
-- tidak pernah dibuka lagi setelah minggu pertama.
--
-- Setelah lewat masa simpan, salinan di sisi kami dihapus: berkasnya dari
-- bucket, thumbnail-nya dari baris lampiran, dan materi unduhannya dari
-- whatsapp_media_refs. Statusnya menjadi 'expired', yang dibedakan dari
-- 'failed' karena artinya berlawanan: 'failed' berarti sesuatu tidak berjalan
-- dan mungkin bisa dicoba lagi, 'expired' berarti kami sengaja menghapusnya dan
-- tidak akan ada yang bisa diambil lagi dari sini.
--
-- Yang dihapus HANYA salinan di sisi kami. Pesannya tetap ada di HP, dan
-- tampilan chat menyebutkan itu apa adanya alih-alih berpura-pura berkasnya
-- hilang.
--
-- Baris lampirannya sendiri tidak dihapus. Riwayat pengiriman harus tetap bisa
-- menyebutkan bahwa pesan itu membawa sebuah foto, berapa ukurannya, dan
-- namanya apa; yang hilang adalah isinya, bukan catatannya.
-- -----------------------------------------------------------------------------

do $$
begin
  alter type public.attachment_storage_status add value if not exists 'expired';
exception when duplicate_object then null;
end $$;

-- Dibaca penyapu tiap jam: lampiran yang masih memegang sesuatu dan sudah lewat
-- masa simpannya. Sebagian, karena yang sudah 'expired' tidak perlu dilihat lagi.
create index if not exists idx_attachments_retention
  on public.message_attachments (created_at)
  where storage_status <> 'expired';

comment on column public.message_attachments.thumbnail_b64 is
  'Thumbnail kecil dari WhatsApp (~1-2 KB), agar bubble punya isi sebelum berkas penuhnya diunduh. Dikosongkan setelah lampiran melewati masa simpan.';
