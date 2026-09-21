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
)

// startMediaJanitor deletes stored media once it falls outside the retention
// window.
//
// The window is the account's own sync window — seven days by default — so it
// matches what the inbox actually shows. Keeping files for chats that have
// already scrolled out of range would grow the bucket without bound and keep
// customer photos on disk long after there is any reason to hold them.
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
	ctx, cancel := context.WithTimeout(m.rootCtx, 10*time.Minute)
	defer cancel()

	m.sweepExpiredStatuses(ctx)

	targets, err := m.repo.AccountsWithStoredMedia(ctx)
	if err != nil {
		m.log.Warn("list accounts for media retention", "err", err)
		return
	}

	for _, t := range targets {
		if ctx.Err() != nil {
			return
		}
		cutoff := time.Now().AddDate(0, 0, -t.WindowDays)
		n, err := m.expireMediaFor(ctx, t.AccountID, cutoff)
		if err != nil {
			m.log.Warn("expire media", "account_id", t.AccountID, "err", err)
			continue
		}
		if n > 0 {
			m.log.Info("expired media removed",
				"account_id", t.AccountID, "files", n, "window_days", t.WindowDays)
		}
	}
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
	for {
		batch, err := m.repo.ExpiredStatusAttachments(ctx, retentionBatch)
		if err != nil {
			m.log.Warn("list expired status media", "err", err)
			break
		}
		if len(batch) == 0 {
			break
		}
		keys := make([]string, 0, len(batch))
		for _, a := range batch {
			keys = append(keys, a.StoragePath)
		}
		m.removeStoredObjects(ctx, keys)
		if len(batch) < retentionBatch || ctx.Err() != nil {
			break
		}
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

	for {
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

		m.removeStoredObjects(ctx, keys)
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
}
