package wa

import (
	"net/url"
	"strings"
	"testing"
)

func TestSanitizeDSN(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		wantHas []string
		wantNot []string
	}{
		{
			name:    "pgx-only options are dropped for lib/pq",
			in:      "postgres://u:p@db.example.co:5432/postgres?sslmode=require&default_query_exec_mode=simple_protocol&pool_max_conns=10",
			wantHas: []string{"sslmode=require"},
			wantNot: []string{"default_query_exec_mode", "pool_max_conns"},
		},
		{
			name:    "plain dsn is untouched",
			in:      "postgres://u:p@db.example.co:5432/postgres?sslmode=require",
			wantHas: []string{"sslmode=require"},
		},
		{
			name: "dsn without query survives",
			in:   "postgres://u:p@db.example.co:5432/postgres",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := sanitizeDSN(tt.in)
			if err != nil {
				t.Fatalf("sanitizeDSN() error = %v", err)
			}

			parsed, err := url.Parse(got)
			if err != nil {
				t.Fatalf("result is not a valid url: %v", err)
			}
			if parsed.Host != "db.example.co:5432" {
				t.Errorf("host mangled: %q", parsed.Host)
			}
			if parsed.Path != "/postgres" {
				t.Errorf("database mangled: %q", parsed.Path)
			}

			for _, want := range tt.wantHas {
				if !strings.Contains(got, want) {
					t.Errorf("result %q is missing %q", got, want)
				}
			}
			for _, unwanted := range tt.wantNot {
				if strings.Contains(got, unwanted) {
					t.Errorf("result %q still carries %q", got, unwanted)
				}
			}
		})
	}
}

func TestSanitizeDSNRejectsGarbage(t *testing.T) {
	if _, err := sanitizeDSN("://not a url"); err == nil {
		t.Error("expected an error for an unparseable DSN")
	}
}
