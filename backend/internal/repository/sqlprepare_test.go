package repository

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Every SQL literal in the codebase, handed to Postgres to plan.
//
// This exists because the same defect shipped twice in one day, in two
// unrelated features, and neither was visible from the outside:
//
//   - refreshLabelState: a bare $5 inside a CASE whose only other branch was
//     NULL. Deduced as text, rejected with 42804, and the enclosing transaction
//     rolled back — so the label history stayed empty while label changes were
//     happening all day.
//   - MarkStoryPublished: a bare $3 used as both `published_at = $3` and
//     `$3 + interval`. Deduced as timestamptz in one place and timestamp in the
//     other, rejected with 42P08 — so a Story that was live on the phone read
//     "Sedang Dipublikasikan" on the web, and would have been published twice
//     when its lease expired.
//
// Both failed in paths that only logged. A unit test cannot catch either,
// because the statement is valid Go and valid-looking SQL; only Postgres knows.
// PREPARE plans a statement without running it, so this is read-only even for
// the INSERTs and UPDATEs it checks.
func TestEverySQLStatementPlans(t *testing.T) {
	// Read-only by construction, so unlike the integration tests above this may
	// use the ordinary DSN: PREPARE cannot create, change or delete a row.
	dsn := os.Getenv("SALESAN_TEST_DATABASE_URL")
	if dsn == "" {
		dsn = os.Getenv("DATABASE_URL")
	}
	if dsn == "" {
		t.Skip("no DATABASE_URL or SALESAN_TEST_DATABASE_URL; skipping SQL plan check")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Skipf("cannot reach the database: %v", err)
	}
	defer func() { _ = conn.Close(context.Background()) }()

	var (
		literal = regexp.MustCompile("(?s)`([^`]*)`")
		starts  = regexp.MustCompile(`(?is)^\s*(with|select|insert|update|delete)\b`)
	)

	strict := os.Getenv("SALESAN_SQL_STRICT") == "1"

	checked := 0
	err = filepath.Walk("..", func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") ||
			strings.HasSuffix(path, "_test.go") {
			return err
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range literal.FindAllStringSubmatchIndex(string(src), -1) {
			body := string(src)[m[2]:m[3]]
			// A fragment, or a statement assembled with fmt: it cannot stand
			// alone, so Postgres has nothing to plan.
			if !starts.MatchString(body) || strings.Contains(body, "%") {
				continue
			}
			checked++
			line := 1 + strings.Count(string(src)[:m[2]], "\n")
			name := fmt.Sprintf("plan_%d", checked)

			if _, err := conn.Prepare(ctx, name, body); err != nil {
				var pg *pgconn.PgError
				if !errors.As(err, &pg) {
					continue
				}
				// Only the type-deduction failures are reported. Any other error
				// here usually means this scan could not reassemble the statement,
				// which is a limitation of the scan and not a defect in the code.
				//
				// SALESAN_SQL_STRICT=1 reports everything instead, including a
				// column that no longer exists. Noisy by design, so it is not the
				// default: run it after a schema change, read past the fragments
				// the scan could not rebuild, and act on the rest.
				if !strict && pg.Code != "42P08" && pg.Code != "42804" {
					continue
				}
				t.Errorf("%s:%d cannot be planned [%s]: %s\n    %s",
					path, line, pg.Code, pg.Message, firstSQLLine(body))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if checked == 0 {
		t.Fatal("no SQL statements found; the scan is not looking where it thinks it is")
	}
	t.Logf("%d SQL statements planned", checked)
}

func firstSQLLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			if len(l) > 110 {
				l = l[:110] + "…"
			}
			return l
		}
	}
	return ""
}
