// Package wa owns every whatsmeow client in the process.
//
// One Session == one WhatsApp account == one whatsmeow client, keyed by
// account_id. Sessions never share state: each has its own device row in
// whatsmeow's sqlstore, its own socket, and its own event handler. That
// isolation is what lets many accounts run side by side in a single process.
//
// Credentials (Noise/Signal keys, identity, sessions) live exclusively in the
// whatsmeow_* tables managed by sqlstore. Nothing in this package ever puts
// them on the wire to a browser — the API only exposes status, JID and phone
// number.
package wa

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"sync"
	"time"

	"github.com/google/uuid"
	_ "github.com/lib/pq" // database/sql driver used by whatsmeow's sqlstore

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	waLog "go.mau.fi/whatsmeow/util/log"

	"github.com/salesan/omnichannel/backend/internal/config"
	"github.com/salesan/omnichannel/backend/internal/models"
	"github.com/salesan/omnichannel/backend/internal/realtime"
	"github.com/salesan/omnichannel/backend/internal/repository"
	"github.com/salesan/omnichannel/backend/internal/storage"
)

// Errors surfaced to the HTTP layer.
var (
	ErrSessionNotFound = errors.New("wa: no active session for this account")
	ErrNotConnected    = errors.New("wa: account is not connected")
	ErrAlreadyPaired   = errors.New("wa: account is already paired; disconnect or logout first")
)

// Manager supervises every Session.
type Manager struct {
	cfg   *config.Config
	repo  *repository.Repo
	hub   *realtime.Hub
	log   *slog.Logger
	waLog waLog.Logger

	container *sqlstore.Container

	// store is where media bytes live — Supabase or the local disk. Never nil
	// in practice; the nil checks remain so a misconfiguration degrades to
	// "media unavailable" rather than a panic.
	store storage.Backend
	// mediaSem bounds concurrent media downloads across every account, so a
	// group dumping fifty photos cannot saturate the connection.
	mediaSem chan struct{}

	mu       sync.RWMutex
	sessions map[uuid.UUID]*Session

	rootCtx context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

// NewManager opens whatsmeow's device store and runs its migrations.
func NewManager(ctx context.Context, cfg *config.Config, repo *repository.Repo, hub *realtime.Hub, log *slog.Logger) (*Manager, error) {
	level := "INFO"
	if cfg.LogLevel == "debug" {
		level = "DEBUG"
	}
	wl := waLog.Stdout("whatsmeow", level, true)

	dsn, err := sanitizeDSN(cfg.WhatsmeowDatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("whatsmeow dsn: %w", err)
	}

	// The connection pool is opened here rather than left to sqlstore.New, so
	// that it can be bounded.
	//
	// sqlstore.New opens a database/sql pool with Go's defaults, and Go's
	// default for MaxOpenConns is unlimited. This process therefore held two
	// pools against the same database, one of them with no ceiling at all.
	//
	// That ceiling is not ours to ignore. This project's Supabase session
	// pooler refuses past fifteen clients:
	//
	//   FATAL: (EMAXCONNSESSION) max clients reached in session mode -
	//   max clients are limited to pool_size: 15
	//
	// Fifteen covers everything: this pool, the repository pool, and any
	// migration or diagnostic run alongside. Four here and eight there leaves
	// three spare, which is enough to run `cmd/migrate` while the server is up.
	// Whatsmeow needs far less than four in practice; it reads keys and writes
	// app-state versions, and it does so per account rather than per request.
	sqlDB, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("open whatsmeow store: %w", err)
	}
	sqlDB.SetMaxOpenConns(4)
	sqlDB.SetMaxIdleConns(2)
	sqlDB.SetConnMaxLifetime(time.Hour)
	sqlDB.SetConnMaxIdleTime(15 * time.Minute)

	container := sqlstore.NewWithDB(sqlDB, "postgres", wl.Sub("store"))
	if err := container.Upgrade(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("upgrade whatsmeow store: %w", err)
	}

	// Supabase when it is configured, this machine's disk otherwise. Media is
	// never simply switched off: a chat app that cannot send a photo is not
	// finished, and the local backend keeps every security property that
	// matters — private directory, signed links, short expiry.
	var store storage.Backend
	if cfg.UseSupabaseStorage() {
		store = storage.New(cfg.SupabaseURL, cfg.SupabaseServiceRoleKey, cfg.StorageBucket)
	} else {
		store = storage.NewLocal(
			cfg.MediaDir,
			cfg.PublicAPIURL,
			storage.DeriveSecret(cfg.MediaSigningSecret, cfg.DatabaseURL),
		)
	}

	rootCtx, cancel := context.WithCancel(context.Background())
	return &Manager{
		cfg:       cfg,
		repo:      repo,
		hub:       hub,
		log:       log.With("component", "wa"),
		waLog:     wl,
		container: container,
		store:     store,
		mediaSem:  make(chan struct{}, 4),
		sessions:  map[uuid.UUID]*Session{},
		rootCtx:   rootCtx,
		cancel:    cancel,
	}, nil
}

// sanitizeDSN drops pgx-only query parameters so the same connection string can
// be handed to lib/pq, which rejects unknown options.
func sanitizeDSN(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	q := u.Query()
	for _, key := range []string{
		"default_query_exec_mode", "statement_cache_capacity",
		"description_cache_capacity", "pool_max_conns", "pool_min_conns",
		"pool_max_conn_lifetime", "pool_max_conn_idle_time", "pool_health_check_period",
	} {
		q.Del(key)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// Bootstrap restores a session for every account that has been paired before.
// Failures are logged and reflected on the account row; one bad account must
// never stop the server from booting.
func (m *Manager) Bootstrap(ctx context.Context) error {
	// Prepare the media bucket before any account can receive a photo. A
	// failure here is logged, not fatal: chat must still work.
	m.ensureStorage(ctx)
	m.startMediaJanitor()
	// Repairs derived reporting rows that a lost event or a restart left
	// behind. Independent of any live session: the conversations that most
	// need repairing belong to the account that was disconnected.
	m.startMetricsReconciler()

	accounts, err := m.repo.ListLinkedAccounts(ctx)
	if err != nil {
		return fmt.Errorf("list linked accounts: %w", err)
	}
	m.log.Info("restoring whatsapp sessions", "count", len(accounts))

	for _, acc := range accounts {
		acc := acc
		m.wg.Add(1)
		go func() {
			defer m.wg.Done()
			if err := m.restore(acc); err != nil {
				m.log.Error("restore session failed", "account_id", acc.ID, "err", err)
				m.setStatus(acc.ID, acc.WorkspaceID, models.AccountStatusError, err.Error())
			}
		}()
	}
	return nil
}

func (m *Manager) restore(acc repository.AccountRef) error {
	if acc.JID == nil || *acc.JID == "" {
		return errors.New("account has no jid")
	}
	jid, err := types.ParseJID(*acc.JID)
	if err != nil {
		return fmt.Errorf("parse jid %q: %w", *acc.JID, err)
	}

	ctx, cancel := context.WithTimeout(m.rootCtx, 30*time.Second)
	device, err := m.container.GetDevice(ctx, jid)
	cancel()
	if err != nil {
		return fmt.Errorf("load device: %w", err)
	}
	if device == nil {
		// The account row points at a device the store no longer has (store
		// wiped, or unlinked from the phone). Force the user to re-pair.
		m.log.Warn("device missing for account, marking logged out", "account_id", acc.ID)
		_ = m.repo.ClearAccountIdentity(m.rootCtx, acc.ID)
		m.broadcastAccount(acc.WorkspaceID, acc.ID)
		return nil
	}

	sess := m.newSession(acc.ID, acc.WorkspaceID, device)
	m.putSession(sess)
	sess.startConnectLoop()
	return nil
}

func (m *Manager) newSession(accountID, workspaceID uuid.UUID, device *store.Device) *Session {
	client := whatsmeow.NewClient(device, m.waLog.Sub(accountID.String()[:8]))
	client.EnableAutoReconnect = true
	client.AutoTrustIdentity = true

	// Without this, whatsmeow deliberately swallows every app-state mutation
	// whenever the sync is a full one — and a full sync is exactly what the
	// Sinkron button performs. Labels, label-to-chat links and per-chat read
	// state all arrive as those mutations, so leaving it at the default false
	// makes them silently unreachable. It also gates the recovery path in
	// handleAppStateRecovery, so both routes need it.
	client.EmitAppStateEventsOnFullSync = true

	s := &Session{
		AccountID:   accountID,
		WorkspaceID: workspaceID,
		client:      client,
		mgr:         m,
		log:         m.log.With("account_id", accountID),
		done:        make(chan struct{}),
	}
	s.handlerID = client.AddEventHandler(s.handleEvent)
	s.startLabelReconciler()
	return s
}

func (m *Manager) putSession(s *Session) {
	m.mu.Lock()
	if old, ok := m.sessions[s.AccountID]; ok && old != s {
		go old.stop(false)
	}
	m.sessions[s.AccountID] = s
	m.mu.Unlock()
}

// Session returns the live session for an account, if any.
func (m *Manager) Session(accountID uuid.UUID) (*Session, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.sessions[accountID]
	return s, ok
}

func (m *Manager) dropSession(accountID uuid.UUID) {
	m.mu.Lock()
	delete(m.sessions, accountID)
	m.mu.Unlock()
}

// --- public operations -------------------------------------------------------

// StartPairing creates a fresh device for the account and streams QR codes.
//
// It returns the first QR code if one arrives within `wait`; subsequent codes
// (whatsmeow rotates them roughly every 30s until the whole pairing attempt
// times out) are pushed over the realtime hub as `account.qr` events.
func (m *Manager) StartPairing(ctx context.Context, accountID uuid.UUID, wait time.Duration) (string, error) {
	acc, err := m.repo.GetAccountRef(ctx, accountID)
	if err != nil {
		return "", err
	}
	if acc.JID != nil && *acc.JID != "" {
		if s, ok := m.Session(accountID); ok && s.client.IsLoggedIn() {
			return "", ErrAlreadyPaired
		}
	}

	// Tear down anything already running for this account.
	if old, ok := m.Session(accountID); ok {
		old.stop(false)
		m.dropSession(accountID)
	}

	device := m.container.NewDevice()
	sess := m.newSession(accountID, acc.WorkspaceID, device)
	m.putSession(sess)

	firstQR, err := sess.startPairing(wait)
	if err != nil {
		sess.stop(false)
		m.dropSession(accountID)
		m.setStatus(accountID, acc.WorkspaceID, models.AccountStatusError, err.Error())
		return "", err
	}
	return firstQR, nil
}

// CurrentQR returns the most recent QR code for an account, if it is pairing.
func (m *Manager) CurrentQR(accountID uuid.UUID) (string, bool) {
	s, ok := m.Session(accountID)
	if !ok {
		return "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.qrCode, s.qrCode != ""
}

// Connect brings a previously paired account back online.
func (m *Manager) Connect(ctx context.Context, accountID uuid.UUID) error {
	if s, ok := m.Session(accountID); ok {
		if s.client.IsConnected() {
			return nil
		}
		s.startConnectLoop()
		return nil
	}

	acc, err := m.repo.GetAccountRef(ctx, accountID)
	if err != nil {
		return err
	}
	if acc.JID == nil || *acc.JID == "" {
		return ErrSessionNotFound
	}
	return m.restore(*acc)
}

// Disconnect closes the socket but keeps the device, so Connect can resume it.
func (m *Manager) Disconnect(ctx context.Context, accountID uuid.UUID) error {
	s, ok := m.Session(accountID)
	if !ok {
		return ErrSessionNotFound
	}
	s.stop(false)
	m.dropSession(accountID)

	_ = m.repo.CloseSessions(ctx, accountID)
	m.setStatus(accountID, s.WorkspaceID, models.AccountStatusDisconnected, "")
	return nil
}

// Logout unlinks the device from the phone and deletes it from the store.
func (m *Manager) Logout(ctx context.Context, accountID uuid.UUID) error {
	s, ok := m.Session(accountID)
	if !ok {
		// Nothing running; just clear the DB side.
		_ = m.repo.CloseSessions(ctx, accountID)
		return m.repo.ClearAccountIdentity(ctx, accountID)
	}

	if s.client.IsLoggedIn() {
		if err := s.client.Logout(ctx); err != nil {
			s.log.Warn("logout call failed, deleting device anyway", "err", err)
			if s.client.Store != nil {
				_ = m.container.DeleteDevice(ctx, s.client.Store)
			}
		}
	} else if s.client.Store != nil && s.client.Store.ID != nil {
		_ = m.container.DeleteDevice(ctx, s.client.Store)
	}

	s.stop(false)
	m.dropSession(accountID)

	_ = m.repo.CloseSessions(ctx, accountID)
	if err := m.repo.ClearAccountIdentity(ctx, accountID); err != nil {
		return err
	}
	m.broadcastAccount(s.WorkspaceID, accountID)
	return nil
}

// Release tears a session down without touching the database. Used right before
// an account row is deleted.
func (m *Manager) Release(ctx context.Context, accountID uuid.UUID) {
	s, ok := m.Session(accountID)
	if !ok {
		return
	}
	if s.client.IsLoggedIn() {
		logoutCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		_ = s.client.Logout(logoutCtx)
		cancel()
	} else if s.client.Store != nil && s.client.Store.ID != nil {
		_ = m.container.DeleteDevice(ctx, s.client.Store)
	}
	s.stop(false)
	m.dropSession(accountID)
}

// Shutdown closes every session and the device store.
func (m *Manager) Shutdown() {
	m.log.Info("shutting down whatsapp sessions")
	m.cancel()

	m.mu.Lock()
	sessions := make([]*Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		sessions = append(sessions, s)
	}
	m.sessions = map[uuid.UUID]*Session{}
	m.mu.Unlock()

	var wg sync.WaitGroup
	for _, s := range sessions {
		wg.Add(1)
		go func(s *Session) {
			defer wg.Done()
			s.stop(true)
		}(s)
	}
	wg.Wait()
	m.wg.Wait()

	if err := m.container.Close(); err != nil {
		m.log.Warn("closing whatsmeow store", "err", err)
	}
}

// --- shared helpers ----------------------------------------------------------

// setStatus persists a status transition and pushes it to the browser.
func (m *Manager) setStatus(accountID, workspaceID uuid.UUID, status, detail string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var detailPtr *string
	if detail != "" {
		detailPtr = &detail
	}
	if err := m.repo.SetAccountStatus(ctx, accountID, status, detailPtr); err != nil {
		m.log.Error("persist account status", "account_id", accountID, "status", status, "err", err)
	}
	m.hub.Broadcast(workspaceID, realtime.EventAccountStatus, map[string]any{
		"account_id":    accountID,
		"status":        status,
		"status_detail": detailPtr,
	})
}

// broadcastAccount pushes the full, freshly-read account row.
func (m *Manager) broadcastAccount(workspaceID, accountID uuid.UUID) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	acc, err := m.repo.GetAccount(ctx, workspaceID, accountID)
	if err != nil {
		return
	}
	m.hub.Broadcast(workspaceID, realtime.EventAccountStatus, map[string]any{
		"account_id":    acc.ID,
		"status":        acc.Status,
		"status_detail": acc.StatusDetail,
		"account":       acc,
	})
}
