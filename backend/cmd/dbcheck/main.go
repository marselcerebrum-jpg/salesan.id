// Command dbcheck diagnoses database connectivity before you try to boot the
// server, and can find the right Supabase pooler endpoint for you.
//
//	go run ./cmd/dbcheck           # test DATABASE_URL as configured
//	go run ./cmd/dbcheck -probe    # if that fails, try the regional poolers
//	go run ./cmd/dbcheck -probe -write   # ...and rewrite .env with what works
//
// Why this exists: Supabase's "Direct connection" host (db.<ref>.supabase.co)
// resolves to IPv6 only. On an IPv4-only network it times out with no useful
// error, and the fix — switching to the Session pooler — is not obvious.
//
// Passwords are never printed.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/salesan/omnichannel/backend/internal/config"
)

// Regions Supabase runs poolers in. ap-southeast-1 leads because that is where
// most Indonesian projects land.
var regions = []string{
	"ap-southeast-1", "ap-southeast-2", "ap-south-1", "ap-northeast-1", "ap-northeast-2",
	"us-east-1", "us-east-2", "us-west-1", "us-west-2",
	"eu-central-1", "eu-west-1", "eu-west-2", "eu-west-3", "eu-north-1",
	"ca-central-1", "sa-east-1",
}

// Supabase has used both prefixes for pooler hostnames.
var poolerPrefixes = []string{"aws-0", "aws-1"}

var directHostRe = regexp.MustCompile(`^db\.([a-z0-9]+)\.supabase\.co$`)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "\ndbcheck: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	probe := flag.Bool("probe", false, "if the configured DSN fails, try the Supabase poolers")
	write := flag.Bool("write", false, "rewrite backend/.env with the endpoint that worked")
	envPath := flag.String("env", ".env", "path to the .env file to read and (with -write) update")
	flag.Parse()

	if err := config.LoadEnvFiles(*envPath); err != nil {
		return fmt.Errorf("read %s: %w", *envPath, err)
	}

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return errors.New("DATABASE_URL is not set")
	}

	u, err := url.Parse(dsn)
	if err != nil {
		return fmt.Errorf("DATABASE_URL is not a valid URI: %w", err)
	}

	fmt.Printf("host     : %s\n", u.Hostname())
	fmt.Printf("port     : %s\n", u.Port())
	fmt.Printf("user     : %s\n", u.User.Username())
	fmt.Printf("database : %s\n\n", strings.TrimPrefix(u.Path, "/"))

	if u.Port() == "6543" {
		fmt.Println("WARNING: port 6543 is the transaction pooler. It cannot hold prepared")
		fmt.Println("         statements, which this application relies on. Use port 5432.")
		fmt.Println()
	}

	describeDNS(u.Hostname())

	fmt.Print("\nconnecting… ")
	if err := tryConnect(dsn, 12*time.Second); err == nil {
		fmt.Println("OK")
		fmt.Println("\nDATABASE_URL works. Next: go run ./cmd/migrate")
		return nil
	} else {
		fmt.Printf("FAILED\n  %v\n", condense(err))
	}

	if !*probe {
		fmt.Println("\nRe-run with -probe to search for a working Supabase pooler endpoint.")
		return errors.New("could not connect with the configured DATABASE_URL")
	}

	working, err := probePoolers(u)
	if err != nil {
		return err
	}

	fmt.Printf("\nFound a working endpoint: %s\n", hideCreds(working))
	if !*write {
		fmt.Println("Re-run with -write to save it into", *envPath)
		return nil
	}
	if err := rewriteEnv(*envPath, working); err != nil {
		return err
	}
	fmt.Printf("Updated %s. Next: go run ./cmd/migrate\n", *envPath)
	return nil
}

// describeDNS reports which address families a host offers, which is the whole
// story behind the IPv6-only direct-connection trap.
func describeDNS(host string) {
	ips, err := net.LookupIP(host)
	if err != nil {
		fmt.Printf("dns      : lookup failed: %v\n", err)
		return
	}
	var v4, v6 int
	for _, ip := range ips {
		if ip.To4() != nil {
			v4++
		} else {
			v6++
		}
	}
	fmt.Printf("dns      : %d IPv4, %d IPv6\n", v4, v6)
	if v4 == 0 && v6 > 0 {
		fmt.Println("           ^ IPv6 only. This host is unreachable from an IPv4-only network.")
	}
}

func tryConnect(dsn string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return err
	}
	cfg.ConnectTimeout = timeout

	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close(context.Background()) }()

	var one int
	return conn.QueryRow(ctx, "select 1").Scan(&one)
}

// probePoolers walks the regional pooler hostnames looking for the one that
// hosts this project. Credentials come from the DSN already configured, so
// nothing new is asked of the user.
func probePoolers(u *url.URL) (string, error) {
	m := directHostRe.FindStringSubmatch(u.Hostname())
	ref := ""
	if len(m) == 2 {
		ref = m[1]
	} else if user := u.User.Username(); strings.HasPrefix(user, "postgres.") {
		ref = strings.TrimPrefix(user, "postgres.")
	}
	if ref == "" {
		return "", errors.New("cannot work out the project ref from DATABASE_URL; copy the Session pooler URI from the dashboard instead")
	}

	password, _ := u.User.Password()
	if password == "" {
		return "", errors.New("DATABASE_URL has no password")
	}

	fmt.Printf("\nprobing poolers for project %s\n", ref)

	for _, prefix := range poolerPrefixes {
		for _, region := range regions {
			host := fmt.Sprintf("%s-%s.pooler.supabase.com", prefix, region)

			// Skip regions whose pooler does not exist at all.
			if _, err := net.LookupIP(host); err != nil {
				continue
			}

			candidate := (&url.URL{
				Scheme: "postgresql",
				User:   url.UserPassword("postgres."+ref, password),
				Host:   net.JoinHostPort(host, "5432"),
				Path:   "/postgres",
			}).String()

			fmt.Printf("  %-40s ", host)
			if err := tryConnect(candidate, 10*time.Second); err != nil {
				fmt.Printf("no (%s)\n", condense(err))
				continue
			}
			fmt.Println("YES")
			return candidate, nil
		}
	}

	return "", errors.New("no pooler accepted the connection; copy the Session pooler URI from the Supabase dashboard")
}

// condense trims multi-line driver errors down to something readable in a list.
func condense(err error) string {
	s := err.Error()
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 90 {
		s = s[:90] + "…"
	}
	return s
}

// hideCreds replaces the password with asterisks for display.
func hideCreds(dsn string) string {
	u, err := url.Parse(dsn)
	if err != nil {
		return "(unprintable)"
	}
	if u.User != nil {
		u.User = url.UserPassword(u.User.Username(), "********")
	}
	return u.String()
}

func rewriteEnv(path, dsn string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := strings.Split(string(raw), "\n")
	replaced := false
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "DATABASE_URL=") {
			lines[i] = "DATABASE_URL=" + dsn
			replaced = true
			break
		}
	}
	if !replaced {
		lines = append(lines, "DATABASE_URL="+dsn)
	}
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o600)
}
