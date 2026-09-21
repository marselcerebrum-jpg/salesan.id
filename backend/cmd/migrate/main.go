// Command migrate applies the SQL files in backend/migrations to the database
// named by DATABASE_URL.
//
//	go run ./cmd/migrate           # apply every pending schema migration
//	go run ./cmd/migrate -status   # show what is applied and what is pending
//	go run ./cmd/migrate -seed     # also load the demo data (0002_seed.sql)
//	go run ./cmd/migrate -file migrations/0004_clean_display_names.sql
//
// Applied versions are recorded in public.schema_migrations, so running it
// again is cheap and safe. The checksum of each applied file is stored too: if
// a migration is edited after it ran, that is reported rather than silently
// ignored, because the database and the file no longer agree.
//
// Files ending in _seed.sql are data, not schema. They are never applied
// automatically and never recorded, since they are meant to be re-runnable.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/salesan/omnichannel/backend/internal/config"
)

type migration struct {
	Version  string // "0001_init"
	Path     string
	SQL      string
	Checksum string
}

type appliedRow struct {
	Version   string
	Checksum  string
	AppliedAt time.Time
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "\nmigrate: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	seed := flag.Bool("seed", false, "also apply the demo data in 0002_seed.sql")
	status := flag.Bool("status", false, "list applied and pending migrations, then exit")
	single := flag.String("file", "", "apply exactly one SQL file and nothing else")
	dir := flag.String("dir", "migrations", "directory holding the migration files")
	flag.Parse()

	if err := config.LoadEnvFiles(".env"); err != nil {
		return fmt.Errorf("read .env: %w", err)
	}
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return fmt.Errorf("DATABASE_URL is not set (copy .env.example to .env and fill it in)")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	// Multi-statement files and DO blocks require the simple protocol.
	cfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol

	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer func() { _ = conn.Close(context.Background()) }()

	var serverVersion string
	if err := conn.QueryRow(ctx, "select version()").Scan(&serverVersion); err != nil {
		return fmt.Errorf("ping: %w", err)
	}
	fmt.Printf("connected: %.60s…\n\n", serverVersion)

	// One-off file: apply and get out, without touching the ledger.
	if *single != "" {
		return applyFile(ctx, conn, *single)
	}

	if err := ensureLedger(ctx, conn); err != nil {
		return err
	}

	all, err := loadMigrations(*dir)
	if err != nil {
		return err
	}
	applied, err := loadApplied(ctx, conn)
	if err != nil {
		return err
	}

	if *status {
		printStatus(all, applied)
		return nil
	}

	pending := 0
	for _, m := range all {
		prev, done := applied[m.Version]
		if done {
			if prev.Checksum != m.Checksum {
				fmt.Printf("!  %s changed since it was applied on %s\n",
					m.Version, prev.AppliedAt.Format(time.RFC3339))
				fmt.Printf("   The database already has the old version. If the edit matters,\n")
				fmt.Printf("   add a new migration rather than re-running this one.\n\n")
			}
			continue
		}

		fmt.Printf("→ %s (%d bytes)\n", m.Version, len(m.SQL))
		start := time.Now()
		if _, err := conn.Exec(ctx, m.SQL); err != nil {
			return fmt.Errorf("apply %s: %w", m.Version, err)
		}
		if _, err := conn.Exec(ctx,
			`insert into public.schema_migrations (version, checksum) values ($1, $2)
			 on conflict (version) do update set checksum = excluded.checksum, applied_at = now()`,
			m.Version, m.Checksum); err != nil {
			return fmt.Errorf("record %s: %w", m.Version, err)
		}
		fmt.Printf("  ok in %s\n\n", time.Since(start).Round(time.Millisecond))
		pending++
	}

	if pending == 0 {
		fmt.Println("Nothing to do — every migration is already applied.")
	}

	if *seed {
		seedPath := filepath.Join(*dir, "0002_seed.sql")
		fmt.Printf("\n→ demo data: %s\n", seedPath)
		if err := applyFile(ctx, conn, seedPath); err != nil {
			return err
		}
	}

	return report(ctx, conn)
}

// ensureLedger creates the table that records which migrations have run.
func ensureLedger(ctx context.Context, conn *pgx.Conn) error {
	_, err := conn.Exec(ctx, `
		create table if not exists public.schema_migrations (
			version    text primary key,
			checksum   text not null,
			applied_at timestamptz not null default now()
		)`)
	if err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}
	return nil
}

// loadMigrations reads every schema migration, in filename order. Seed files
// are data rather than schema and are excluded.
func loadMigrations(dir string) ([]migration, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", dir, err)
	}

	var out []migration
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".sql") || strings.HasSuffix(name, "_seed.sql") {
			continue
		}
		path := filepath.Join(dir, name)
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		sum := sha256.Sum256(raw)
		out = append(out, migration{
			Version:  strings.TrimSuffix(name, ".sql"),
			Path:     path,
			SQL:      string(raw),
			Checksum: hex.EncodeToString(sum[:]),
		})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	if len(out) == 0 {
		return nil, fmt.Errorf("no migrations found in %s", dir)
	}
	return out, nil
}

func loadApplied(ctx context.Context, conn *pgx.Conn) (map[string]appliedRow, error) {
	rows, err := conn.Query(ctx, `select version, checksum, applied_at from public.schema_migrations`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]appliedRow{}
	for rows.Next() {
		var r appliedRow
		if err := rows.Scan(&r.Version, &r.Checksum, &r.AppliedAt); err != nil {
			return nil, err
		}
		out[r.Version] = r
	}
	return out, rows.Err()
}

func printStatus(all []migration, applied map[string]appliedRow) {
	fmt.Printf("%-34s %-9s %s\n", "MIGRATION", "STATE", "APPLIED AT")
	fmt.Println(strings.Repeat("-", 72))
	for _, m := range all {
		prev, done := applied[m.Version]
		switch {
		case !done:
			fmt.Printf("%-34s %-9s %s\n", m.Version, "pending", "-")
		case prev.Checksum != m.Checksum:
			fmt.Printf("%-34s %-9s %s\n", m.Version, "CHANGED", prev.AppliedAt.Format(time.RFC3339))
		default:
			fmt.Printf("%-34s %-9s %s\n", m.Version, "applied", prev.AppliedAt.Format(time.RFC3339))
		}
	}
	fmt.Println("\n(seed files are listed nowhere: they are data, applied on demand with -seed)")
}

// applyFile runs one SQL file without recording it in the ledger.
func applyFile(ctx context.Context, conn *pgx.Conn, path string) error {
	sql, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	fmt.Printf("→ applying %s (%d bytes)\n", path, len(sql))
	start := time.Now()
	if _, err := conn.Exec(ctx, string(sql)); err != nil {
		return fmt.Errorf("apply %s: %w", path, err)
	}
	fmt.Printf("  ok in %s\n", time.Since(start).Round(time.Millisecond))
	return nil
}

// report prints a short summary so it is obvious the schema really landed.
func report(ctx context.Context, conn *pgx.Conn) error {
	const q = `
		select c.relname,
		       c.relrowsecurity,
		       coalesce(s.n_live_tup, 0)
		  from pg_class c
		  join pg_namespace n on n.oid = c.relnamespace
		  left join pg_stat_user_tables s on s.relid = c.oid
		 where n.nspname = 'public'
		   and c.relkind = 'r'
		   and c.relname in (
		     'workspaces','users','applications','whatsapp_accounts','whatsapp_sessions',
		     'contacts','conversations','conversation_members','messages',
		     'conversation_labels','conversation_label_assignments','whatsapp_label_events',
		     'whatsapp_receipt_events','message_attachments','whatsapp_media_refs',
		     'message_polls','message_poll_options','message_poll_votes','message_poll_voters')
		 order by c.relname`

	rows, err := conn.Query(ctx, q)
	if err != nil {
		return fmt.Errorf("verify: %w", err)
	}
	defer rows.Close()

	fmt.Printf("\n%-32s %-5s %s\n", "TABLE", "RLS", "ROWS")
	fmt.Println(strings.Repeat("-", 50))
	count := 0
	for rows.Next() {
		var name string
		var rls bool
		var live int64
		if err := rows.Scan(&name, &rls, &live); err != nil {
			return err
		}
		flag := "off"
		if rls {
			flag = "on"
		}
		fmt.Printf("%-32s %-5s %d\n", name, flag, live)
		count++
	}
	if err := rows.Err(); err != nil {
		return err
	}

	const want = 19
	fmt.Printf("\n%d/%d tables present.\n", count, want)
	if count < want {
		return fmt.Errorf("expected %d tables, found %d", want, count)
	}
	fmt.Println("Schema is ready.")
	return nil
}
