// Package repository is the only place that speaks SQL.
//
// Every method takes the caller's workspace ID and folds it into the WHERE
// clause. The backend connects with the service role (which bypasses RLS), so
// this application-level scoping is what actually isolates tenants on the API
// path; the RLS policies in migration 0001 protect the direct Supabase path
// used by the browser.
package repository

import (
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when a scoped lookup matches no row.
//
// Kept distinct from ErrForbidden (declared in org.go) on purpose: "does not
// exist" and "exists but is not yours" are different facts, and the API answers
// them differently — 404 for the first, 403 for the second.
var ErrNotFound = errors.New("repository: not found")

// Repo bundles all data access.
type Repo struct {
	pool *pgxpool.Pool
}

// New builds a Repo over an existing pool.
func New(pool *pgxpool.Pool) *Repo { return &Repo{pool: pool} }

// Pool exposes the underlying pool for health checks.
func (r *Repo) Pool() *pgxpool.Pool { return r.pool }

func mapErr(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

// isNoRows distinguishes "matched nothing" from a real failure, for the callers
// where matching nothing is an expected outcome rather than an error.
func isNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

// asPgError unwraps a driver error so a caller can branch on the SQLSTATE.
//
// Worth naming: a unique-violation is not always a fault. In the campaign queue
// it is the mechanism — the database refusing a second successful send to the
// same recipient — and telling that apart from a genuine failure needs the code,
// not the message text.
func asPgError(err error, target **pgconn.PgError) bool { return errors.As(err, target) }

// AsPgError hands the driver error to callers outside this package, so an HTTP
// handler can turn a unique-violation into a sentence about the name that is
// taken rather than a generic failure. Returns nil when the error did not come
// from Postgres.
func AsPgError(err error) *pgconn.PgError {
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		return pg
	}
	return nil
}
