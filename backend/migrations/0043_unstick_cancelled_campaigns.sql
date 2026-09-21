-- -----------------------------------------------------------------------------
-- Bebaskan campaign yang terkunci di status "running"
--
-- Pembatalan bersifat kooperatif: benderanya dinaikkan, worker membacanya di
-- antara dua penerima. Itu hanya berjalan selama ada worker yang sedang
-- memegang campaign-nya. Bila tidak ada, tidak ada yang akan membaca bendera itu
-- — dan penjadwal justru menolak mengambilnya kembali KARENA benderanya sudah
-- naik (lihat ClaimDueCampaigns: `and cancel_requested = false`).
--
-- Akibatnya campaign duduk selamanya di "Sedang Dikirim": penerimanya sudah
-- dibatalkan, pekerjaannya sudah selesai, statusnya salah permanen, dan tidak
-- bisa dihapus karena hanya draft yang boleh dihapus.
--
-- RequestCancel sekarang menyelesaikannya langsung bila tidak ada lease yang
-- hidup. Yang di bawah ini membereskan baris yang terlanjur terjebak sebelum
-- perbaikan itu ada.
--
-- Hanya menyentuh yang benar-benar tidak mungkin berjalan lagi: pembatalan
-- diminta, tidak ada worker yang memegangnya, dan tidak ada satu pun penerima
-- yang masih menunggu. Campaign yang masih punya penerima tertunda dibiarkan —
-- statusnya masih bisa benar.
-- -----------------------------------------------------------------------------

update public.content_campaigns cc
   set status       = 'cancelled',
       finished_at  = coalesce(cc.finished_at, cc.cancelled_at, now()),
       cancelled_at = coalesce(cc.cancelled_at, now()),
       lease_owner      = null,
       lease_expires_at = null
 where cc.status = 'running'
   and cc.cancel_requested = true
   and (cc.lease_owner is null or cc.lease_expires_at is null
        or cc.lease_expires_at < now())
   and not exists (
     select 1 from public.campaign_targets t
      where t.campaign_id = cc.id
        and t.status in ('pending', 'retry_wait', 'processing')
   );

-- Perangkat pengirimnya ikut dibereskan, supaya laporan "hasil per nomor" tidak
-- menyebut sebuah nomor masih berjalan padahal campaign-nya sudah berakhir.
update public.broadcast_sender_devices d
   set status      = 'cancelled',
       finished_at = coalesce(d.finished_at, now())
  from public.content_campaigns cc
 where cc.id = d.campaign_id
   and cc.status = 'cancelled'
   and d.status in ('pending', 'running');
