// Command appicons sets application logos from a folder of image files.
//
//	go run ./cmd/appicons -dir logos
//	go run ./cmd/appicons -dir logos -dry-run
//
// Each file is matched to an application by name: "jadiasn.png" goes to the
// application whose code is JADIASN. Case, spaces, dashes and underscores are
// ignored, so "Jadi-ASN.png" and "JADIASN.PNG" both match. A file that matches
// nothing is reported and skipped; an application with no file is left alone.
//
// The same checks and the same storage as the upload button: the type is
// decided from the file's own bytes, only PNG, JPEG and WebP up to 2 MB are
// accepted, the file lands in the private bucket under a key the server builds,
// and the logo it replaces is deleted. The database keeps the key, never a link.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/salesan/omnichannel/backend/internal/config"
	"github.com/salesan/omnichannel/backend/internal/db"
	"github.com/salesan/omnichannel/backend/internal/media"
	"github.com/salesan/omnichannel/backend/internal/repository"
	"github.com/salesan/omnichannel/backend/internal/storage"
	"github.com/salesan/omnichannel/backend/internal/wa"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "\nappicons: %v\n", err)
		os.Exit(1)
	}
}

type app struct {
	ID          uuid.UUID
	WorkspaceID uuid.UUID
	Code        string
	IconURL     *string
}

func run() error {
	dir := flag.String("dir", "logos", "folder berisi berkas logo, satu per aplikasi, dinamai sesuai kode aplikasi")
	dryRun := flag.Bool("dry-run", false, "hanya tampilkan pasangan berkas dan aplikasi, tanpa mengunggah")
	flag.Parse()

	if err := config.LoadEnvFiles(".env"); err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	entries, err := os.ReadDir(*dir)
	if err != nil {
		return fmt.Errorf("baca folder %s: %w", *dir, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	repo := repository.New(pool)

	apps, err := listApplications(ctx, pool)
	if err != nil {
		return err
	}
	byKey := map[string][]app{}
	for _, a := range apps {
		byKey[normalise(a.Code)] = append(byKey[normalise(a.Code)], a)
	}

	var store storage.Backend
	if cfg.UseSupabaseStorage() {
		store = storage.New(cfg.SupabaseURL, cfg.SupabaseServiceRoleKey, cfg.StorageBucket)
	} else {
		store = storage.NewLocal(cfg.MediaDir, cfg.PublicAPIURL,
			storage.DeriveSecret(cfg.MediaSigningSecret, cfg.DatabaseURL))
	}
	if !*dryRun {
		if err := store.EnsureReady(ctx); err != nil {
			return fmt.Errorf("penyimpanan %s belum siap: %w", store.Name(), err)
		}
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	done, skipped := 0, 0
	for _, name := range names {
		stem := strings.TrimSuffix(name, filepath.Ext(name))
		targets := byKey[normalise(stem)]
		if len(targets) == 0 {
			fmt.Printf("  lewati  %-28s tidak ada aplikasi berkode %q\n", name, strings.ToUpper(stem))
			skipped++
			continue
		}

		full := filepath.Join(*dir, name)
		file, size, err := classify(full)
		if err != nil {
			fmt.Printf("  tolak   %-28s %v\n", name, err)
			skipped++
			continue
		}

		for _, a := range targets {
			if *dryRun {
				fmt.Printf("  cocok   %-28s -> %s (%s, %d KB)\n", name, a.Code, file.MIME, size>>10)
				continue
			}
			if err := apply(ctx, store, repo, a, file, full, size); err != nil {
				fmt.Printf("  gagal   %-28s -> %s: %v\n", name, a.Code, err)
				skipped++
				continue
			}
			fmt.Printf("  selesai %-28s -> %s\n", name, a.Code)
			done++
		}
	}

	fmt.Printf("\n%d logo dipasang, %d berkas dilewati.\n", done, skipped)
	if !*dryRun && done > 0 {
		fmt.Println("Muat ulang halaman di browser untuk melihatnya.")
	}
	return nil
}

// listApplications reads every application in every workspace. The tool runs
// on the server with the database's own credentials, so there is no caller to
// scope by; the file names decide what is touched.
func listApplications(ctx context.Context, pool *pgxpool.Pool) ([]app, error) {
	r, err := pool.Query(ctx, `select id, workspace_id, code, icon_url from public.applications order by code`)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	var out []app
	for r.Next() {
		var a app
		if err := r.Scan(&a.ID, &a.WorkspaceID, &a.Code, &a.IconURL); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, r.Err()
}

// classify validates one file exactly as the upload endpoint does.
func classify(path string) (media.File, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return media.File{}, 0, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return media.File{}, 0, err
	}
	if info.Size() > wa.MaxAppIconBytes {
		return media.File{}, 0, fmt.Errorf("melebihi batas 2 MB")
	}
	head := make([]byte, 512)
	n, _ := f.ReadAt(head, 0)
	file, err := media.Classify(filepath.Base(path), "", head[:n], info.Size(), false)
	if err != nil {
		return media.File{}, 0, err
	}
	switch file.MIME {
	case "image/png", "image/jpeg", "image/webp":
	default:
		return media.File{}, 0, fmt.Errorf("harus PNG, JPG, atau WebP (terbaca %s)", file.MIME)
	}
	return file, info.Size(), nil
}

func apply(
	ctx context.Context, store storage.Backend, repo *repository.Repo,
	a app, file media.File, path string, size int64,
) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	key := wa.AppIconKey(a.WorkspaceID, a.ID, file.MIME)
	if err := store.Upload(ctx, key, file.MIME, io.Reader(f), size); err != nil {
		return fmt.Errorf("unggah: %w", err)
	}
	previous, err := repo.SetApplicationIcon(ctx, a.WorkspaceID, a.ID, &key)
	if err != nil {
		_ = store.Remove(ctx, key)
		return err
	}
	if previous != nil && wa.IsAppIconKey(*previous) {
		_ = store.Remove(ctx, *previous)
	}
	return nil
}

// normalise reduces a code or a file stem to letters and digits, lower case,
// so the match survives the ways people actually name files.
func normalise(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}
