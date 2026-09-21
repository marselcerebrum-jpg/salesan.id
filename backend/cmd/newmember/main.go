// Command newmember creates a workspace member from the command line.
//
//	go run ./cmd/newmember -email arik@marsel.id -password "..." \
//	    -role leader -name Arik -workspace-of arik@salesan.id
//
// It does exactly what the Akun & Peran screen does: create a confirmed
// Supabase account through the Admin API, then write the profile, the
// operational role and the placement in one transaction. Same code path for the
// second half, so an account made here cannot end up shaped differently from
// one made in the product.
//
// It exists for the case the screen cannot cover: nobody can sign in yet, so
// there is no Leader logged in to press the button.
//
// SUPABASE_SERVICE_ROLE_KEY is read from backend/.env and never printed.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/salesan/omnichannel/backend/internal/auth"
	"github.com/salesan/omnichannel/backend/internal/config"
	"github.com/salesan/omnichannel/backend/internal/models"
	"github.com/salesan/omnichannel/backend/internal/repository"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "\nnewmember: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	email := flag.String("email", "", "alamat email akun baru")
	password := flag.String("password", "", "kata sandi awal, minimal 8 karakter")
	role := flag.String("role", "", "leader, pic, atau freelance")
	name := flag.String("name", "", "nama lengkap; kosong berarti diambil dari email")
	ref := flag.String("workspace-of", "",
		"email anggota yang sudah ada, untuk menentukan workspace mana yang dipakai")
	flag.Parse()

	addr := strings.ToLower(strings.TrimSpace(*email))
	if !strings.Contains(addr, "@") || len(addr) < 5 {
		return fmt.Errorf("-email tidak valid")
	}
	// The same floor the Akun & Peran screen enforces, repeated rather than
	// assumed: this path does not go through that handler.
	if len([]rune(*password)) < 8 {
		return fmt.Errorf("-password minimal 8 karakter")
	}
	switch *role {
	case models.RoleLeader, models.RolePIC, models.RoleFreelance:
	default:
		return fmt.Errorf("-role harus leader, pic, atau freelance")
	}
	if strings.TrimSpace(*ref) == "" {
		return fmt.Errorf("-workspace-of wajib diisi: email anggota yang sudah ada di workspace tujuan")
	}

	full := strings.TrimSpace(*name)
	if full == "" {
		full = strings.Split(addr, "@")[0]
	}

	if err := config.LoadEnvFiles(".env"); err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	admin := auth.NewAdmin(cfg.SupabaseURL, cfg.SupabaseServiceRoleKey)
	if !admin.Enabled() {
		return fmt.Errorf("SUPABASE_SERVICE_ROLE_KEY belum diisi di backend/.env")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	// The workspace is named by an existing member rather than by a raw uuid:
	// a uuid typed by hand is how an account lands in the wrong workspace, and
	// the wrong workspace looks exactly like a working account with no data.
	//
	// Matched through auth.users rather than on public.users.email. The profile
	// row is keyed by id and its email column is a copy that can be stale, so
	// the address the person actually signs in with is the one to search.
	var workspaceID, assignedBy uuid.UUID
	var refName string
	if err := pool.QueryRow(ctx,
		`select pu.workspace_id, pu.id, coalesce(nullif(pu.full_name, ''), au.email)
		   from auth.users au
		   join public.users pu on pu.id = au.id
		  where lower(btrim(au.email)) = lower(btrim($1))`,
		strings.TrimSpace(*ref)).Scan(&workspaceID, &assignedBy, &refName); err != nil {
		if err == pgx.ErrNoRows {
			// Listing what is there beats repeating what is not: the usual cause
			// is a near-miss spelling, and the near-miss is invisible until the
			// real ones are on screen beside it.
			return fmt.Errorf("tidak ada anggota dengan email %s.\nyang ada: %s",
				*ref, candidates(ctx, pool))
		}
		return err
	}

	// A Leader operates the whole workspace, so they get the workspace-level
	// admin role too. PIC and Freelance stay agents: their reach comes from the
	// operational role and the assignments, not from workspace ownership.
	workspaceRole := "agent"
	if *role == models.RoleLeader {
		workspaceRole = "admin"
	}

	fmt.Printf("workspace  : %s (mengikuti %s)\n", workspaceID, refName)
	fmt.Printf("email      : %s\n", addr)
	fmt.Printf("nama       : %s\n", full)
	fmt.Printf("peran      : %s (workspace_role %s)\n\n", *role, workspaceRole)

	userID, err := admin.CreateUser(ctx, auth.CreateUserInput{
		Email:         addr,
		Password:      *password,
		FullName:      full,
		WorkspaceID:   workspaceID,
		WorkspaceRole: workspaceRole,
	})
	if err != nil {
		return fmt.Errorf("buat akun di Supabase: %w", err)
	}
	fmt.Println("akun Supabase dibuat:", userID)

	repo := repository.New(pool)
	if err := repo.AttachMemberFull(ctx, workspaceID, assignedBy, repository.AttachMemberInput{
		UserID:          userID,
		Email:           addr,
		FullName:        full,
		WorkspaceRole:   workspaceRole,
		OperationalRole: *role,
	}); err != nil {
		// Said plainly rather than swallowed: the auth account exists now, and
		// whoever runs this needs to know the halves came apart.
		return fmt.Errorf(
			"akun dibuat di Supabase (%s) tetapi gagal ditautkan ke workspace: %w", userID, err)
	}

	fmt.Println("profil dan peran ditulis.")
	fmt.Printf("\nSelesai. %s dapat masuk sekarang.\n", addr)
	return nil
}

// candidates lists the addresses that could have been meant.
func candidates(ctx context.Context, pool *pgxpool.Pool) string {
	rows, err := pool.Query(ctx, `
		select au.email, (pu.id is not null), coalesce(left(pu.workspace_id::text, 8), '-')
		  from auth.users au
		  left join public.users pu on pu.id = au.id
		 order by au.email`)
	if err != nil {
		return "(gagal dibaca: " + err.Error() + ")"
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var email, ws string
		var hasProfile bool
		if err := rows.Scan(&email, &hasProfile, &ws); err != nil {
			return "(gagal dibaca)"
		}
		mark := "TANPA PROFIL"
		if hasProfile {
			mark = "workspace " + ws
		}
		out = append(out, fmt.Sprintf("\n  %-24s %s", email, mark))
	}
	if len(out) == 0 {
		return "(tidak ada satu pun akun)"
	}
	return strings.Join(out, "")
}
