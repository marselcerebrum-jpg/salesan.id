package wa

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Media retention sweep timings.
const (
	// retentionInterval is how often the sweep runs. Hourly is frequent enough
	// that a file never outlives its window by much, and rare enough to be
	// invisible in load.
	retentionInterval = time.Hour
	// retentionFirstDelay lets the process finish connecting its accounts
	// before the first sweep, so a boot is not competing with a delete storm.
	retentionFirstDelay = 2 * time.Minute
	// retentionBatch bounds one account's work per sweep. A backlog is cleared
	// over several passes rather than in one long transaction.
	retentionBatch = 200
	// retentionMaxPasses stops a sweep that is not getting anywhere.
	//
	// A paged loop whose pages stop advancing is an infinite loop wearing a
	// sensible shape, and this one was exactly that for weeks. The bug is fixed;
	// the bound stays, because the next version of that mistake should end as a
	// short log line rather than as a sweep that silently never finishes.
	retentionMaxPasses = 2000
)

// startMediaJanitor deletes stored media once it falls outside the retention
// window.
//
// The window is MEDIA_RETENTION_DAYS, or the account's own sync window when that
// is shorter — media is never kept past the point the inbox stops showing its
// message. Keeping files for chats that have already scrolled out of range would
// grow the bucket without bound and keep customer photos on disk long after
// there is any reason to hold them.
func (m *Manager) startMediaJanitor() {
	if m.store == nil {
		return // nothing is being stored, so nothing needs sweeping
	}

	m.wg.Add(1)
	go func() {
		defer m.wg.Done()

		timer := time.NewTimer(retentionFirstDelay)
		defer timer.Stop()

		for {
			select {
			case <-m.rootCtx.Done():
				return
			case <-timer.C:
			}

			m.sweepExpiredMedia()
			timer.Reset(retentionInterval)
		}
	}()
}

// sweepExpiredMedia runs one pass over every account holding stored media.
//
// Driven by the database rather than by the live sessions: a disconnected
// account still has files in the bucket, and it is precisely the account nobody
// is watching that must not keep customer photos indefinitely.
func (m *Manager) sweepExpiredMedia() {
	// A budget each. Sharing one meant the sweep that ran first could spend all
	// of it and leave the other with nothing, which is what happened: chat media
	// was never once swept, because the Status sweep in front of it never
	// finished. Neither can starve the other now.
	statusCtx, cancelStatus := context.WithTimeout(m.rootCtx, 5*time.Minute)
	m.sweepExpiredStatuses(statusCtx)
	cancelStatus()

	ctx, cancel := context.WithTimeout(m.rootCtx, 10*time.Minute)
	defer cancel()

	targets, err := m.repo.AccountsWithStoredMedia(ctx)
	if err != nil {
		m.log.Warn("list accounts for media retention", "err", err)
		return
	}

	for _, t := range targets {
		if ctx.Err() != nil {
			return
		}
		days := mediaWindowDays(t.WindowDays, m.cfg.MediaRetentionDays)
		cutoff := time.Now().AddDate(0, 0, -days)
		n, err := m.expireMediaFor(ctx, t.AccountID, cutoff)
		if err != nil {
			m.log.Warn("expire media", "account_id", t.AccountID, "err", err)
			continue
		}
		if n > 0 {
			m.log.Info("expired media removed",
				"account_id", t.AccountID, "files", n, "window_days", days)
		}
	}
}

// mediaWindowDays is how long this account's files are kept.
//
// The shorter of the two windows wins. Media is never kept past the point the
// inbox stops showing its message, and it is not kept for the whole window
// either: the bytes are what fills the disk, and the file is the one part of a
// message that still exists on the phone afterwards.
//
// A limit of zero or less means the setting is not in use, and the account's own
// window applies — a misread environment variable must not silently start
// deleting everything.
func mediaWindowDays(windowDays, limit int) int {
	if windowDays < 1 {
		windowDays = 1
	}
	if limit > 0 && limit < windowDays {
		return limit
	}
	return windowDays
}

// sweepExpiredStatuses removes Status posts past their 24 hours.
//
// Deleted outright, unlike chat media, which is marked expired and keeps its
// row. A Status is gone from WhatsApp itself after a day: there is no thread to
// look back at, nothing refers to it, and no report needs to be able to say it
// existed. Keeping the row would be a permanent record of something the platform
// treats as temporary.
//
// Files leave the bucket before the rows leave the database. The other order
// would drop the only pointer to an object and leave it in the bucket for good.
func (m *Manager) sweepExpiredStatuses(ctx context.Context) {
	files := 0
	for pass := 0; pass < retentionMaxPasses; pass++ {
		batch, err := m.repo.ExpiredStatusAttachments(ctx, retentionBatch)
		if err != nil {
			m.log.Warn("list expired status media", "err", err)
			break
		}
		if len(batch) == 0 {
			break
		}
		keys := make([]string, 0, len(batch))
		ids := make([]uuid.UUID, 0, len(batch))
		for _, a := range batch {
			keys = append(keys, a.StoragePath)
			ids = append(ids, a.ID)
		}
		// These rows still point at their files here, so the batch names itself
		// as the thing to disregard when asking what is still in use.
		m.releaseAndRemove(ctx, keys, ids)

		// The rows must stop pointing at the files before the next page is
		// asked for. Without this the same two hundred rows come back every
		// time — the listing is "status media that still has a storage path",
		// and removing the object does not change that. The loop then spun
		// until its ten minutes ran out, the row deletion below never ran, and
		// the media retention sweep after it never got a turn. Ninety-two
		// gigabytes of day-old Status video sat there for that reason alone.
		if _, err := m.repo.MarkAttachmentsExpired(ctx, ids); err != nil {
			m.log.Warn("settle expired status media", "err", err)
			break
		}
		files += len(batch)

		if len(batch) < retentionBatch || ctx.Err() != nil {
			break
		}
	}
	if files > 0 {
		m.log.Info("expired status media removed", "files", files)
	}

	n, err := m.repo.DeleteExpiredStatuses(ctx)
	if err != nil {
		m.log.Warn("delete expired statuses", "err", err)
		return
	}
	if n > 0 {
		m.log.Info("expired statuses removed", "count", n)
	}
}

// expireMediaFor removes one account's out-of-window files from the bucket and
// marks their rows expired.
//
// Storage is emptied before the rows are updated. Doing it the other way round
// would risk marking a file gone that is still sitting in the bucket with
// nothing left pointing at it; this order can at worst leave a row that says
// `stored` for a file already deleted, which the next sweep retries and which
// the reader reports honestly as unavailable in the meantime.
func (m *Manager) expireMediaFor(ctx context.Context, accountID uuid.UUID, cutoff time.Time) (int, error) {
	total := 0

	// Bounded like the Status sweep. This loop does advance — marking the batch
	// expired clears its storage paths, so the next page is a different page —
	// but a paged loop with no ceiling is one query change away from spinning.
	for pass := 0; pass < retentionMaxPasses; pass++ {
		batch, err := m.repo.AttachmentsPastRetention(ctx, accountID, cutoff, retentionBatch)
		if err != nil {
			return total, err
		}
		if len(batch) == 0 {
			return total, nil
		}

		keys := make([]string, 0, len(batch))
		ids := make([]uuid.UUID, 0, len(batch))
		for _, a := range batch {
			keys = append(keys, a.StoragePath)
			ids = append(ids, a.ID)
		}

		// The batch's own rows still point at these keys at this moment, so they
		// are named as the ones to disregard. Any other attachment referencing
		// the same file — a broadcast sibling, or a copy in another account of
		// this workspace — keeps it.
		m.releaseAndRemove(ctx, keys, ids)
		if _, err := m.repo.MarkAttachmentsExpired(ctx, ids); err != nil {
			return total, err
		}
		total += len(batch)

		// A short batch means the backlog is cleared.
		if len(batch) < retentionBatch {
			return total, nil
		}
		if ctx.Err() != nil {
			return total, ctx.Err()
		}
	}
	// The ceiling was reached with work still to do. Not an error: the next
	// hourly sweep carries on from where this one stopped.
	return total, nil
}
