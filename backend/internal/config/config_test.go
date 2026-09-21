package config

import (
	"testing"
	"time"
)

func TestLoadRequiresDatabaseAndAuth(t *testing.T) {
	// Load() reads the process environment; t.Setenv restores it afterwards.
	t.Setenv("DATABASE_URL", "")
	t.Setenv("SUPABASE_URL", "")
	t.Setenv("SUPABASE_JWT_SECRET", "")
	t.Setenv("SUPABASE_JWKS_URL", "")

	if _, err := Load(); err == nil {
		t.Fatal("expected Load() to fail when DATABASE_URL and auth config are missing")
	}
}

func TestLoadDerivesDefaults(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://u:p@localhost:5432/postgres")
	t.Setenv("SUPABASE_URL", "https://demo.supabase.co/")
	t.Setenv("SUPABASE_JWT_SECRET", "")
	t.Setenv("SUPABASE_JWKS_URL", "")
	t.Setenv("WHATSMEOW_DATABASE_URL", "")
	t.Setenv("ALLOWED_ORIGINS", "http://localhost:3000, https://app.salesan.id ,")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	// The whatsmeow store falls back to the main database.
	if cfg.WhatsmeowDatabaseURL != cfg.DatabaseURL {
		t.Errorf("WhatsmeowDatabaseURL = %q, want it to fall back to DATABASE_URL", cfg.WhatsmeowDatabaseURL)
	}

	// The JWKS endpoint is derived so SUPABASE_URL alone is enough, and the
	// trailing slash must not produce a double slash.
	want := "https://demo.supabase.co/auth/v1/.well-known/jwks.json"
	if cfg.SupabaseJWKSURL != want {
		t.Errorf("SupabaseJWKSURL = %q, want %q", cfg.SupabaseJWKSURL, want)
	}

	if len(cfg.AllowedOrigins) != 2 ||
		cfg.AllowedOrigins[0] != "http://localhost:3000" ||
		cfg.AllowedOrigins[1] != "https://app.salesan.id" {
		t.Errorf("AllowedOrigins = %#v, want the two trimmed origins", cfg.AllowedOrigins)
	}

	if cfg.Port != "8080" {
		t.Errorf("Port = %q, want the 8080 default", cfg.Port)
	}
	if !cfg.AutoMarkRead {
		t.Error("AutoMarkRead should default to true so read state stays two-way")
	}
}

func TestLoadAutoMarkReadCanBeDisabled(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://u:p@localhost:5432/postgres")
	t.Setenv("SUPABASE_JWT_SECRET", "secret")
	t.Setenv("SUPABASE_URL", "")
	t.Setenv("SUPABASE_JWKS_URL", "")
	t.Setenv("WA_AUTO_MARK_READ", "false")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.AutoMarkRead {
		t.Error("WA_AUTO_MARK_READ=false must switch blue ticks off")
	}
}

func TestLoadParsesDurationsAndBooleans(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://u:p@localhost:5432/postgres")
	t.Setenv("SUPABASE_JWT_SECRET", "secret")
	t.Setenv("SUPABASE_URL", "")
	t.Setenv("SUPABASE_JWKS_URL", "")
	t.Setenv("RECONNECT_MAX_DELAY", "90s")
	t.Setenv("WA_AUTO_MARK_READ", "true")
	t.Setenv("QR_TIMEOUT", "not-a-duration")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.ReconnectMaxDelay != 90*time.Second {
		t.Errorf("ReconnectMaxDelay = %v, want 90s", cfg.ReconnectMaxDelay)
	}
	if !cfg.AutoMarkRead {
		t.Error("AutoMarkRead = false, want true")
	}
	// An unparseable duration must fall back rather than crash the process.
	if cfg.QRTimeout != 3*time.Minute {
		t.Errorf("QRTimeout = %v, want the 3m fallback", cfg.QRTimeout)
	}
}

func TestIsProduction(t *testing.T) {
	if (&Config{Env: "production"}).IsProduction() != true {
		t.Error("production env not detected")
	}
	if (&Config{Env: "development"}).IsProduction() != false {
		t.Error("development env reported as production")
	}
}
