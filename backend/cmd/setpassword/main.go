// Command setpassword sets a team member's password through the Supabase Admin
// API, the same endpoint this server already uses to create accounts.
//
//	go run ./cmd/setpassword -email abdul@salesan.id
//	go run ./cmd/setpassword -email abdul@salesan.id -password "yang-anda-pilih"
//
// Why this exists: the Supabase dashboard's user panel has no password field,
// and its "send password recovery" path needs a mailbox that actually receives
// mail plus SMTP that is not rate limited. Neither holds for a workspace whose
// members are set up by their Leader with addresses on a domain nobody reads.
//
// Without -password a strong one is generated and printed. That print is the
// point: it is the thing being handed to the person, and there is nowhere else
// to read it from afterwards, because Supabase stores only the hash.
//
// SUPABASE_SERVICE_ROLE_KEY is read from backend/.env and never printed, never
// logged, and never sent anywhere except Supabase itself.
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/salesan/omnichannel/backend/internal/config"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "\nsetpassword: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	email := flag.String("email", "", "alamat email akun yang kata sandinya diatur ulang")
	password := flag.String("password", "", "kata sandi baru; dikosongkan berarti dibuatkan")
	flag.Parse()

	if strings.TrimSpace(*email) == "" {
		flag.Usage()
		return fmt.Errorf("-email wajib diisi")
	}

	if err := config.LoadEnvFiles(".env"); err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if strings.TrimSpace(cfg.SupabaseServiceRoleKey) == "" {
		return fmt.Errorf("SUPABASE_SERVICE_ROLE_KEY belum diisi di backend/.env")
	}
	if cfg.SupabaseURL == "" {
		return fmt.Errorf("SUPABASE_URL belum diisi di backend/.env")
	}

	secret := *password
	generated := false
	if secret == "" {
		if secret, err = generate(16); err != nil {
			return err
		}
		generated = true
	}
	// Supabase refuses anything shorter, and so does the login form.
	if len([]rune(secret)) < 6 {
		return fmt.Errorf("kata sandi minimal 6 karakter")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	id, err := lookup(ctx, cfg.DatabaseURL, strings.TrimSpace(*email))
	if err != nil {
		return err
	}

	if err := update(ctx, cfg.SupabaseURL, cfg.SupabaseServiceRoleKey, id, secret); err != nil {
		return err
	}

	fmt.Printf("\nBerhasil. %s sekarang dapat masuk dengan:\n\n", *email)
	fmt.Printf("    kata sandi: %s\n\n", secret)
	if generated {
		fmt.Println("Sampaikan langsung ke orangnya, lalu minta dia menggantinya sendiri.")
	}
	fmt.Println("Kata sandi ini tidak tersimpan di mana pun selain sebagai hash di Supabase,")
	fmt.Println("jadi tidak bisa dibaca lagi setelah jendela ini ditutup.")
	return nil
}

// lookup finds the auth user id for an address.
//
// Read from auth.users rather than from the Admin API's list endpoint: the list
// is paginated and its email filter has changed shape between GoTrue versions,
// while this table is the same one the app already reads.
func lookup(ctx context.Context, dsn, email string) (uuid.UUID, error) {
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return uuid.Nil, err
	}
	defer conn.Close(ctx)

	var id uuid.UUID
	err = conn.QueryRow(ctx,
		`select id from auth.users where lower(email) = lower($1)`, email).Scan(&id)
	if err != nil {
		if err == pgx.ErrNoRows {
			return uuid.Nil, fmt.Errorf("tidak ada akun dengan email %s", email)
		}
		return uuid.Nil, err
	}
	return id, nil
}

func update(ctx context.Context, baseURL, key string, id uuid.UUID, password string) error {
	body, err := json.Marshal(map[string]any{"password": password})
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut,
		strings.TrimRight(baseURL, "/")+"/auth/v1/admin/users/"+id.String(),
		bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("apikey", key)
	req.Header.Set("Authorization", "Bearer "+key)

	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode >= 300 {
		// The response body can quote the request back, so only the message
		// field is surfaced and never the raw payload.
		var detail struct {
			Msg     string `json:"msg"`
			Message string `json:"message"`
			Error   string `json:"error"`
		}
		_ = json.Unmarshal(raw, &detail)
		text := firstNonEmpty(detail.Msg, detail.Message, detail.Error)
		if text == "" {
			text = "permintaan ditolak"
		}
		return fmt.Errorf("supabase (%d): %s", resp.StatusCode, text)
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// generate builds a password from crypto/rand.
//
// The alphabet leaves out characters that are read wrong when a password is
// spoken aloud or copied off a screen: no O against 0, no l against 1 or I.
// This one gets dictated across a room more often than it gets pasted.
const alphabet = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"

func generate(n int) (string, error) {
	out := make([]byte, n)
	max := big.NewInt(int64(len(alphabet)))
	for i := range out {
		pick, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		out[i] = alphabet[pick.Int64()]
	}
	return string(out), nil
}
