package wa

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/salesan/omnichannel/backend/internal/realtime"
	"github.com/salesan/omnichannel/backend/internal/repository"
)

// Reconciliation sweep timings.
const (
	// metricsFirstDelay lets accounts finish connecting before the first sweep,
	// so a boot is not competing with a reconnect storm.
	metricsFirstDelay = 90 * time.Second
	// metricsLookback bounds how far back a sweep repairs. Messages are pruned
	// to the account's sync window anyway, so anything older has no timeline
	// left to recompute from.
	metricsLookback = 14 * 24 * time.Hour
	// metricsBatch bounds one sweep's work. A backlog is cleared over several
	// passes rather than in one long transaction.
	metricsBatch = 100
)

// metricsConfig turns the process configuration into the shape the rules read.
func (m *Manager) metricsConfig() repository.MetricsConfig {
	return repository.MetricsConfig{
		TargetSeconds:    m.cfg.SLATargetSeconds,
		UseBusinessHours: m.cfg.SLABusinessHours,
		FollowUpGap:      m.cfg.FollowUpGap,
	}
}

// RefreshConversationMetrics rebuilds the derived rows for one conversation and
// tells the browser they changed.
//
// Called from every path that changes what a conversation contains: a message
// arriving, one being sent, edited, revoked or hidden, and a history sync. It
// recomputes rather than patches, so calling it twice, out of order, or after a
// replay all reach the same answer.
func (m *Manager) RefreshConversationMetrics(ctx context.Context, workspaceID, conversationID uuid.UUID) {
	if err := m.repo.RecomputeConversation(ctx, conversationID, m.metricsConfig()); err != nil {
		m.log.Warn("recompute conversation metrics", "conversation_id", conversationID, "err", err)
		return
	}
	m.hub.Broadcast(workspaceID, realtime.EventMetricsUpdated, map[string]any{
		"conversation_id": conversationID,
	})
}

// refreshAfterMessage is the session-side entry point, which also keeps the
// contact's lead classification current.
//
// Best effort by design: the message itself is already stored and already on
// the operator's screen. A reporting row that failed to write is repaired by
// the next sweep, and failing the whole ingestion over it would cost the chat.
func (s *Session) refreshAfterMessage(ctx context.Context, conversationID uuid.UUID, contactID *uuid.UUID) {
	s.mgr.RefreshConversationMetrics(ctx, s.WorkspaceID, conversationID)
	if contactID != nil {
		if err := s.mgr.repo.RefreshLead(ctx, *contactID); err != nil {
			s.log.Warn("refresh lead classification", "contact_id", *contactID, "err", err)
		}
	}
}

// startMetricsReconciler repairs derived rows that a lost event left behind.
//
// Events do go missing: a dropped WebSocket, a process restart in the middle of
// a batch, a WhatsApp reconnect that replays half a day. The chat itself
// recovers from those on its own because messages are keyed by their WhatsApp
// id; the reporting rows are computed once and would simply stay wrong. This is
// what makes "tidak ada duplikasi akibat webhook, reconnect, atau retry" hold
// without any of the write paths having to be perfect.
func (m *Manager) startMetricsReconciler() {
	interval := m.cfg.MetricsReconcileInterval
	if interval <= 0 {
		return
	}

	m.wg.Add(1)
	go func() {
		defer m.wg.Done()

		timer := time.NewTimer(metricsFirstDelay)
		defer timer.Stop()

		for {
			select {
			case <-m.rootCtx.Done():
				return
			case <-timer.C:
			}

			m.reconcileMetrics()
			timer.Reset(interval)
		}
	}()
}

func (m *Manager) reconcileMetrics() {
	ctx, cancel := context.WithTimeout(m.rootCtx, 5*time.Minute)
	defer cancel()

	since := time.Now().Add(-metricsLookback)
	stale, err := m.repo.StaleMetricConversations(ctx, since, metricsBatch)
	if err != nil {
		m.log.Warn("find stale metrics", "err", err)
		return
	}
	for _, id := range stale {
		if ctx.Err() != nil {
			return
		}
		if err := m.repo.RecomputeConversation(ctx, id, m.metricsConfig()); err != nil {
			m.log.Warn("reconcile conversation metrics", "conversation_id", id, "err", err)
		}
	}

	pending, err := m.repo.UnclassifiedContacts(ctx, metricsBatch)
	if err != nil {
		m.log.Warn("find unclassified contacts", "err", err)
		return
	}
	for _, id := range pending {
		if ctx.Err() != nil {
			return
		}
		if err := m.repo.RefreshLead(ctx, id); err != nil {
			m.log.Warn("classify contact", "contact_id", id, "err", err)
		}
	}

	if len(stale) > 0 || len(pending) > 0 {
		m.log.Info("metrics reconciled",
			"conversations", len(stale), "contacts", len(pending))
	}
}
