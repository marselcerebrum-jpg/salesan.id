// Command delmember removes a workspace member permanently.
//
//	go run ./cmd/delmember -email arik@marsel.id            # periksa saja
//	go run ./cmd/delmember -email arik@marsel.id -confirm   # benar-benar hapus
//
// Dry run by default. Deleting an account cannot be undone, and the damage is
// not where people look for it: `public.users.id` cascades from `auth.users`,
// so one delete also removes the profile, the operational role, the application
// assignments and the work schedules.
//
// What it does NOT remove is the work. Every history column (messages.sent_by,
// content_campaigns.created_by, contact_label_events.admin_id, and the rest) is
// declared `on delete set null`, so the rows survive with their attribution
// blanked. That is worse than it sounds for a report: a month of somebody's
// replies does not disappear, it silently becomes activity nobody did.
//
// So an account with history is refused. Deactivating it keeps the numbers
// attributable and takes the access away just the same, which is why the
// product offers that and not this.
//
// SUPABASE_SERVICE_ROLE_KEY is read from backend/.env and never printed.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/salesan/omnichannel/backend/internal/config"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "\ndelmember: %v\n", err)
		os.Exit(1)
	}
}

type history struct {
	Messages  int
	Campaigns int
	Labels    int
	FollowUps int
	SLACycles int
	Schedules int
}

func (h history) total() int {
	return h.Messages + h.Campaigns + h.Labels + h.FollowUps + h.SLACycles + h.Schedules
}

func run() error {
	email := flag.String("email", "", "alamat email akun yang dihapus")
	confirm := flag.Bool("confirm", false, "benar-benar hapus; tanpa ini hanya diperiksa")
	force := flag.Bool("force", false, "hapus walau akunnya punya riwayat kerja")
	flag.Parse()

	addr := strings.ToLower(strings.TrimSpace(*email))
	if addr == "" {
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

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	var id, workspace uuid.UUID
	var name, opRole string
	if err := pool.QueryRow(ctx, `
		select au.id, pu.workspace_id,
		       coalesce(nullif(pu.full_name, ''), au.email),
		       coalesce(ra.role::text, '-')
		  from auth.users au
		  join public.users pu on pu.id = au.id
		  left join public.role_assignments ra
		         on ra.user_id = pu.id and ra.workspace_id = pu.workspace_id
		 where lower(btrim(au.email)) = lower(btrim($1))`,
		addr).Scan(&id, &workspace, &name, &opRole); err != nil {
		if err == pgx.ErrNoRows {
			return fmt.Errorf("tidak ada akun dengan email %s", addr)
		}
		return err
	}

	var h history
	if err := pool.QueryRow(ctx, `
		select (select count(*) from public.messages             where sent_by = $1),
		       (select count(*) from public.content_campaigns    where created_by = $1),
		       (select count(*) from public.contact_label_events where admin_id = $1),
		       (select count(*) from public.follow_up_events     where admin_id = $1),
		       (select count(*) from public.sla_cycles           where responder_admin_id = $1),
		       (select count(*) from public.work_schedules       where user_id = $1)`,
		id).Scan(&h.Messages, &h.Campaigns, &h.Labels, &h.FollowUps, &h.SLACycles, &h.Schedules); err != nil {
		return fmt.Errorf("hitung riwayat: %w", err)
	}

	// How many Leaders the workspace would have left. A workspace with none has
	// nobody who can manage accounts or roles from inside the product.
	var leadersLeft int
	if err := pool.QueryRow(ctx, `
		select count(*) from public.role_assignments
		 where workspace_id = $1 and role = 'leader' and is_active and user_id <> $2`,
		workspace, id).Scan(&leadersLeft); err != nil {
		return err
	}

	fmt.Printf("akun      : %s (%s)\n", addr, name)
	fmt.Printf("peran     : %s\n", opRole)
	fmt.Printf("workspace : %s\n", workspace)
	fmt.Printf("riwayat   : %d pesan, %d campaign, %d label, %d follow-up, %d siklus SLA, %d jadwal\n",
		h.Messages, h.Campaigns, h.Labels, h.FollowUps, h.SLACycles, h.Schedules)
	fmt.Printf("leader tersisa setelah dihapus: %d\n\n", leadersLeft)

	if h.total() > 0 && !*force {
		return fmt.Errorf(
			"akun ini punya riwayat kerja.\n"+
				"Menghapusnya membiarkan barisnya tetap ada tetapi tanpa pelaku, sehingga\n"+
				"%d catatan berubah menjadi aktivitas yang tidak dimiliki siapa pun.\n"+
				"Nonaktifkan saja lewat Pengaturan (akses hilang, angka tetap utuh),\n"+
				"atau ulangi dengan -force kalau memang itu yang Anda mau", h.total())
	}

	if !*confirm {
		fmt.Println("Pemeriksaan saja. Tambahkan -confirm untuk benar-benar menghapus.")
		return nil
	}

	if err := deleteUser(ctx, cfg.SupabaseURL, cfg.SupabaseServiceRoleKey, id); err != nil {
		return err
	}

	// The cascade from auth.users does the rest; this only reports whether it
	// actually ran, because an orphan profile is invisible until somebody opens
	// the member list and finds a name that cannot be used.
	var left int
	if err := pool.QueryRow(ctx,
		`select count(*) from public.users where id = $1`, id).Scan(&left); err != nil {
		return err
	}
	fmt.Printf("dihapus. profil tersisa di public.users: %d (harusnya 0)\n", left)
	if leadersLeft == 0 {
		fmt.Println("\nPeringatan: workspace ini sekarang tidak punya Leader.")
		fmt.Println("Buat satu lagi dengan: go run ./cmd/newmember -role leader ...")
	}
	return nil
}

func deleteUser(ctx context.Context, baseURL, key string, id uuid.UUID) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete,
		strings.TrimRight(baseURL, "/")+"/auth/v1/admin/users/"+id.String(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("apikey", key)
	req.Header.Set("Authorization", "Bearer "+key)

	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode >= 300 {
		var detail struct {
			Msg     string `json:"msg"`
			Message string `json:"message"`
			Error   string `json:"error"`
		}
		_ = json.Unmarshal(raw, &detail)
		for _, text := range []string{detail.Msg, detail.Message, detail.Error} {
			if strings.TrimSpace(text) != "" {
				return fmt.Errorf("supabase (%d): %s", resp.StatusCode, text)
			}
		}
		return fmt.Errorf("supabase (%d): permintaan ditolak", resp.StatusCode)
	}
	return nil
}
