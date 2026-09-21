// Command server runs the salesan.id omnichannel backend: REST API, WebSocket
// hub and every whatsmeow session, in one process.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/salesan/omnichannel/backend/internal/auth"
	"github.com/salesan/omnichannel/backend/internal/campaign"
	"github.com/salesan/omnichannel/backend/internal/config"
	"github.com/salesan/omnichannel/backend/internal/db"
	"github.com/salesan/omnichannel/backend/internal/httpapi"
	"github.com/salesan/omnichannel/backend/internal/realtime"
	"github.com/salesan/omnichannel/backend/internal/repository"
	"github.com/salesan/omnichannel/backend/internal/wa"
)

func main() {
	if err := run(); err != nil {
		slog.Error("server exited with error", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	log := newLogger(cfg)
	slog.SetDefault(log)
	log.Info("starting salesan backend", "env", cfg.Env, "port", cfg.Port)

	startupCtx, cancelStartup := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancelStartup()

	pool, err := db.Connect(startupCtx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	log.Info("database connected")

	repo := repository.New(pool)
	hub := realtime.NewHub(log)
	verifier := auth.NewVerifier(cfg.SupabaseJWTSecret, cfg.SupabaseJWKSURL, cfg.SupabaseAudience)

	manager, err := wa.NewManager(startupCtx, cfg, repo, hub, log)
	if err != nil {
		return err
	}

	if err := manager.Bootstrap(startupCtx); err != nil {
		// A failure here means we could not even list the accounts; that is
		// worth failing on, unlike a single account failing to reconnect.
		manager.Shutdown()
		return err
	}

	// The Broadcast/Story scheduler. One instance for both, and the only one in
	// the process: its queue lives in the database, so a second scheduler here
	// would compete for the same leases rather than share the work.
	runner := campaign.New(cfg, repo, manager, hub, log)
	runner.Start()

	api := httpapi.NewServer(cfg, repo, manager, hub, verifier, runner, log)
	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           api.Handler(),
		ReadHeaderTimeout: 15 * time.Second,
		// No WriteTimeout: it would cut long-lived WebSocket connections.
		IdleTimeout: 120 * time.Second,
	}

	serverErr := make(chan error, 1)
	go func() {
		log.Info("http listening", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	// --- graceful shutdown ---------------------------------------------------
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-serverErr:
		runner.Stop()
		manager.Shutdown()
		hub.Close()
		return err
	case sig := <-stop:
		log.Info("shutdown signal received", "signal", sig.String())
	}

	// Stop accepting requests first, then let campaigns finish the recipient
	// they are on and release their leases, then close WhatsApp sockets, then
	// the hub. Stopping the runner before the sockets matters: a worker that
	// loses its connection mid-send produces an outcome nobody can determine,
	// which is exactly the case this design goes out of its way to avoid.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Warn("http shutdown", "err", err)
	}
	runner.Stop()
	manager.Shutdown()
	hub.Close()

	log.Info("shutdown complete")
	return nil
}

func newLogger(cfg *config.Config) *slog.Logger {
	level := slog.LevelInfo
	switch cfg.LogLevel {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}

	opts := &slog.HandlerOptions{Level: level}
	if cfg.IsProduction() {
		return slog.New(slog.NewJSONHandler(os.Stdout, opts))
	}
	return slog.New(slog.NewTextHandler(os.Stdout, opts))
}
