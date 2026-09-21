package wa

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"go.mau.fi/whatsmeow"

	"github.com/salesan/omnichannel/backend/internal/models"
	"github.com/salesan/omnichannel/backend/internal/realtime"
)

// Session is one account's whatsmeow client plus its lifecycle bookkeeping.
type Session struct {
	AccountID   uuid.UUID
	WorkspaceID uuid.UUID

	client    *whatsmeow.Client
	mgr       *Manager
	log       *slog.Logger
	handlerID uint32

	mu       sync.Mutex
	qrCode   string
	pairing  bool
	qrCancel context.CancelFunc

	// groupNames caches chat JID -> group subject so a busy group does not
	// trigger a metadata fetch on every incoming message.
	groupNames sync.Map

	// sendMu serialises outgoing messages for this account and, with
	// lastSendAt, enforces the minimum gap between them. Held across the send
	// itself so the gap is measured send-to-send — see beginSend.
	sendMu     sync.Mutex
	lastSendAt time.Time
	// presenceSet records that this connection has already announced itself as
	// available, which WhatsApp needs before it will deliver typing indicators.
	presenceSet atomic.Bool

	// syncWindowDays bounds how far back history ingest reaches. Read from the
	// HistorySync handler, which runs on whatsmeow's goroutine, so it is atomic.
	syncWindowDays atomic.Int32

	// pendingLabels holds chat JIDs whose label association arrived before the
	// label definition itself. App-state mutations come in whatever order the
	// snapshot happens to hold them, so associations routinely land first;
	// without this they would simply be dropped. Keyed by WhatsApp label id.
	pendingLabels sync.Map // map[string][]string

	// appStateRetryAt throttles automatic recovery requests per collection.
	// A wedged collection fails on every notification, and each recovery costs
	// the phone a full plaintext dump. Keyed by patch name.
	appStateRetryAt sync.Map // map[string]time.Time

	// labelRecoverySince is when this connection first asked the phone to
	// resend its label state, as a Unix second, or zero when nothing is
	// outstanding. It bounds how long the interface may keep saying
	// "Menyinkronkan label". Atomic because the reconciler goroutine and
	// whatsmeow's event goroutine both touch it.
	labelRecoverySince atomic.Int64

	// recoveryGates holds an open channel per collection currently being
	// recovered. Writes to a collection whose local version was cleared are
	// rejected by the server with `409 conflict`, so writers wait on the gate
	// instead of failing. Keyed by patch name.
	recoveryGates sync.Map // map[string]chan struct{}

	done      chan struct{}
	closeOnce sync.Once
}

// syncWindow returns the active retention window in days.
func (s *Session) syncWindow() int {
	if d := s.syncWindowDays.Load(); d > 0 {
		return int(d)
	}
	return DefaultSyncWindowDays
}

func (s *Session) setSyncWindow(days int) {
	if days > 0 {
		s.syncWindowDays.Store(int32(days))
	}
}

// Client exposes the underlying whatsmeow client (read-only use by callers).
func (s *Session) Client() *whatsmeow.Client { return s.client }

// IsConnected reports whether the socket is up and authenticated.
func (s *Session) IsConnected() bool {
	return s.client.IsConnected() && s.client.IsLoggedIn()
}

// startConnectLoop dials WhatsApp, retrying with exponential backoff.
//
// whatsmeow reconnects on its own once a connection has been established
// (EnableAutoReconnect); this loop covers the case where the very first dial
// fails — server down, no network at boot — which auto-reconnect does not.
func (s *Session) startConnectLoop() {
	s.mgr.setStatus(s.AccountID, s.WorkspaceID, models.AccountStatusConnecting, "")

	s.mgr.wg.Add(1)
	go func() {
		defer s.mgr.wg.Done()

		base := s.mgr.cfg.ReconnectBaseDelay
		maxDelay := s.mgr.cfg.ReconnectMaxDelay

		for attempt := 0; ; attempt++ {
			select {
			case <-s.done:
				return
			case <-s.mgr.rootCtx.Done():
				return
			default:
			}

			err := s.client.Connect()
			if err == nil {
				return // events.Connected takes it from here
			}
			if errors.Is(err, whatsmeow.ErrAlreadyConnected) {
				return
			}

			delay := time.Duration(float64(base) * math.Pow(1.7, float64(attempt)))
			if delay > maxDelay {
				delay = maxDelay
			}
			s.log.Warn("connect failed, retrying", "attempt", attempt+1, "in", delay, "err", err)
			s.mgr.setStatus(s.AccountID, s.WorkspaceID, models.AccountStatusError, err.Error())

			select {
			case <-time.After(delay):
			case <-s.done:
				return
			case <-s.mgr.rootCtx.Done():
				return
			}
		}
	}()
}

// startPairing opens a QR channel and connects. It blocks up to `wait` for the
// first code so the HTTP response can carry it; later codes go over the hub.
func (s *Session) startPairing(wait time.Duration) (string, error) {
	qrCtx, cancel := context.WithCancel(s.mgr.rootCtx)

	qrChan, err := s.client.GetQRChannel(qrCtx)
	if err != nil {
		cancel()
		return "", fmt.Errorf("open qr channel: %w", err)
	}

	s.mu.Lock()
	s.pairing = true
	s.qrCancel = cancel
	s.mu.Unlock()

	if err := s.client.Connect(); err != nil {
		cancel()
		return "", fmt.Errorf("connect: %w", err)
	}

	first := make(chan string, 1)
	s.mgr.wg.Add(1)
	go func() {
		defer s.mgr.wg.Done()
		defer cancel()
		s.consumeQR(qrChan, first)
	}()

	select {
	case code := <-first:
		return code, nil
	case <-time.After(wait):
		// No code yet — not an error; the frontend will pick it up over the
		// WebSocket as soon as whatsmeow emits one.
		return "", nil
	}
}

func (s *Session) consumeQR(qrChan <-chan whatsmeow.QRChannelItem, first chan<- string) {
	sentFirst := false

	for item := range qrChan {
		switch item.Event {
		case "code":
			s.mu.Lock()
			s.qrCode = item.Code
			s.mu.Unlock()

			if !sentFirst {
				sentFirst = true
				select {
				case first <- item.Code:
				default:
				}
			}

			s.mgr.setStatus(s.AccountID, s.WorkspaceID, models.AccountStatusQRPending, "")
			s.mgr.hub.Broadcast(s.WorkspaceID, realtime.EventAccountQR, map[string]any{
				"account_id": s.AccountID,
				"qr":         item.Code,
				"expires_in": int(item.Timeout.Seconds()),
			})
			s.log.Debug("qr code emitted", "expires_in", item.Timeout)

		case "success":
			// events.PairSuccess already persisted the identity.
			s.clearQR()
			s.log.Info("qr pairing succeeded")

		case "timeout":
			s.clearQR()
			s.mgr.setStatus(s.AccountID, s.WorkspaceID, models.AccountStatusDisconnected,
				"QR kedaluwarsa sebelum dipindai")
			s.mgr.hub.Broadcast(s.WorkspaceID, realtime.EventAccountQR, map[string]any{
				"account_id": s.AccountID,
				"qr":         "",
				"expired":    true,
			})

		default:
			// Every remaining event is an error variant ("err-*").
			detail := item.Event
			if item.Error != nil {
				detail = item.Error.Error()
			}
			s.clearQR()
			s.log.Warn("qr pairing failed", "event", item.Event, "err", item.Error)
			s.mgr.setStatus(s.AccountID, s.WorkspaceID, models.AccountStatusError, detail)
		}
	}

	s.clearQR()
	if !sentFirst {
		select {
		case first <- "":
		default:
		}
	}
}

func (s *Session) clearQR() {
	s.mu.Lock()
	s.qrCode = ""
	s.pairing = false
	s.mu.Unlock()
}

// stop closes the session. It is safe to call more than once.
func (s *Session) stop(graceful bool) {
	s.closeOnce.Do(func() {
		close(s.done)

		s.mu.Lock()
		cancel := s.qrCancel
		s.qrCancel = nil
		s.qrCode = ""
		s.pairing = false
		s.mu.Unlock()

		if cancel != nil {
			cancel()
		}

		s.client.RemoveEventHandler(s.handlerID)
		s.client.Disconnect()

		if graceful {
			// Give in-flight acks a moment to drain before the process exits.
			time.Sleep(200 * time.Millisecond)
		}
	})
}
