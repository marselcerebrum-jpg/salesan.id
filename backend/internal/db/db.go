// Package db owns the Postgres connection pool.
package db

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Connect opens a pgx pool and verifies it with a ping.
//
// Note on Supabase: use the *direct* connection string or the session-mode
// pooler (port 5432). If you must use the transaction-mode pooler (port 6543),
// append `?default_query_exec_mode=simple_protocol` to DATABASE_URL, because
// PgBouncer in transaction mode cannot hold server-side prepared statements.
func Connect(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse DATABASE_URL: %w", err)
	}

	// Ten was too few once Performa started fanning out.
	//
	// One load of that page can issue the summary, its comparison period, the
	// per-person table and the per-application table at the same time, and the
	// last two run several aggregates in parallel each. At ten, a single report
	// could hold most of the pool and the inbox would sit waiting behind it:
	// chat is what people notice, and it was queueing behind a page nobody was
	// even looking at yet.
	//
	// Ten, and the ceiling is not ours to choose.
	//
	// This project's Supabase session pooler refuses past fifteen clients:
	//
	//   FATAL: (EMAXCONNSESSION) max clients reached in session mode -
	//   max clients are limited to pool_size: 15
	//
	// That fifteen covers everything speaking to the database, not just this
	// server, so taking it all would lock out `cmd/migrate` and the diagnostic
	// tools exactly when they are needed. Ten leaves five, which is enough for
	// a migration to run while the server is up.
	//
	// Eight here, four for whatsmeow's own pool (see wa.NewManager), three
	// spare. This process holds two pools against the same database, and until
	// both were bounded the pair of them could reach the ceiling on their own.
	//
	// Raising this is a Supabase setting first and a line of Go second: lift
	// the pooler's pool_size, then lift this, in that order. What actually
	// protects chat from a heavy report is not this number but the analytics
	// budget, capped at three (see repository.analyticsFanout).
	cfg.MaxConns = 8
	// Two warm connections rather than one, so the first request after an idle
	// spell does not pay for a TLS handshake before it can answer.
	cfg.MinConns = 2
	cfg.MaxConnLifetime = time.Hour
	cfg.MaxConnIdleTime = 15 * time.Minute
	cfg.HealthCheckPeriod = time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("open pool: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return pool, nil
}
