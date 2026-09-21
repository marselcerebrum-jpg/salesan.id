// Command analyticscheck exercises every read query behind the Dashboard and
// the Performa page against the real database.
//
//	go run ./cmd/analyticscheck
//
// Read-only: it runs the same repository methods the HTTP handlers call and
// prints what comes back. It exists because a SQL string that compiles in Go
// tells you nothing about whether Postgres will accept it, and these queries
// are assembled from clauses rather than written out whole.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/salesan/omnichannel/backend/internal/analytics"
	"github.com/salesan/omnichannel/backend/internal/campaign"
	"github.com/salesan/omnichannel/backend/internal/config"
	"github.com/salesan/omnichannel/backend/internal/models"
	"github.com/salesan/omnichannel/backend/internal/repository"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "\nanalyticscheck: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	if err := config.LoadEnvFiles(".env"); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	pool, err := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		return err
	}
	defer pool.Close()
	repo := repository.New(pool)

	var workspaceID, userID string
	if err := pool.QueryRow(ctx,
		`select workspace_id::text, id::text from public.users limit 1`,
	).Scan(&workspaceID, &userID); err != nil {
		return fmt.Errorf("no user profile to resolve a scope from: %w", err)
	}

	user, err := repo.GetUser(ctx, mustUUID(userID))
	if err != nil {
		return err
	}
	scope, err := repo.ResolveScope(ctx, user)
	if err != nil {
		return fmt.Errorf("resolve scope: %w", err)
	}
	fmt.Printf("scope: role=%q all=%v applications=%d admins=%d\n\n",
		scope.Role, scope.All, len(scope.ApplicationIDs), len(scope.AdminIDs))

	now := time.Now().In(analytics.Jakarta)
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, analytics.Jakarta)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, analytics.Jakarta)

	day := models.AnalyticsFilter{From: today, To: today.AddDate(0, 0, 1)}
	month := models.AnalyticsFilter{From: monthStart, To: monthStart.AddDate(0, 1, 0)}
	// A window wide enough to actually contain this workspace's messages, so
	// the queries are exercised against rows rather than against emptiness.
	wide := models.AnalyticsFilter{From: now.AddDate(0, 0, -60), To: now.AddDate(0, 0, 1)}

	steps := []struct {
		name string
		fn   func() error
	}{
		{"dashboard (hari ini)", func() error {
			s, err := repo.Dashboard(ctx, scope, day)
			if err != nil {
				return err
			}
			printSummary("hari ini", s)
			return nil
		}},
		{"dashboard (60 hari)", func() error {
			s, err := repo.Dashboard(ctx, scope, wide)
			if err != nil {
				return err
			}
			printSummary("60 hari terakhir", s)
			return nil
		}},
		{"performance (bulan berjalan)", func() error {
			rep, err := repo.Performance(ctx, scope, month)
			if err != nil {
				return err
			}
			fmt.Printf("  %s..%s, %d baris harian, %.1f jam terjadwal\n",
				rep.From, rep.To, len(rep.Days), rep.PerHour.WorkHours)
			return nil
		}},
		{"performance (filter grup)", func() error {
			f := wide
			f.ChatType = models.ConversationTypeGroup
			rep, err := repo.Performance(ctx, scope, f)
			if err != nil {
				return err
			}
			fmt.Printf("  grup: masuk=%d balasan=%d aktif=%d ditangani=%d\n",
				rep.Summary.GroupInbound, rep.Summary.GroupReplies,
				rep.Summary.GroupsActive, rep.Summary.GroupsHandled)
			return nil
		}},
		{"performance (filter pribadi)", func() error {
			f := wide
			f.ChatType = models.ConversationTypePersonal
			rep, err := repo.Performance(ctx, scope, f)
			if err != nil {
				return err
			}
			fmt.Printf("  pribadi: masuk=%d terkirim=%d kontak=%d terlayani=%d belum=%d\n",
				rep.Summary.InboundPersonal, rep.Summary.OutboundManualPersonal,
				rep.Summary.ContactsInbound, rep.Summary.ContactsServed,
				rep.Summary.ContactsUnserved)
			return nil
		}},
		{"filter options", func() error {
			o, err := repo.FilterOptions(ctx, scope, nil)
			if err != nil {
				return err
			}
			fmt.Printf("  aplikasi=%d akun=%d pic=%d freelance=%d shift=%d\n",
				len(o.Applications), len(o.Accounts), len(o.PICs), len(o.Freelancers), len(o.Shifts))
			return nil
		}},
		{"drilldown SLA", func() error {
			rows, err := repo.SLACycles(ctx, scope, wide, "", 20)
			if err != nil {
				return err
			}
			fmt.Printf("  %d siklus\n", len(rows))
			for _, r := range rows[:min(3, len(rows))] {
				fmt.Printf("    %-22s %-10s raw=%s\n",
					deref(r.ConversationName), r.Status, durationOf(r.RawDurationSeconds))
			}
			return nil
		}},
		{"drilldown follow-up", func() error {
			rows, err := repo.FollowUps(ctx, scope, wide, "", 20)
			if err != nil {
				return err
			}
			fmt.Printf("  %d aktivitas\n", len(rows))
			return nil
		}},
		{"drilldown mention grup", func() error {
			rows, err := repo.GroupMentions(ctx, scope, wide, "", 20)
			if err != nil {
				return err
			}
			fmt.Printf("  %d mention\n", len(rows))
			for _, r := range rows[:min(3, len(rows))] {
				answered := "belum"
				if r.RespondedAt != nil {
					answered = "sudah"
				}
				fmt.Printf("    %-22s oleh %-18s ditanggapi=%s\n",
					deref(r.GroupName), deref(r.SenderName), answered)
			}
			return nil
		}},
		{"drilldown label", func() error {
			events, err := repo.LabelEvents(ctx, scope, wide, "", 20)
			if err != nil {
				return err
			}
			transitions, err := repo.LabelTransitions(ctx, scope, wide, 20)
			if err != nil {
				return err
			}
			usage, err := repo.LabelUsage(ctx, scope, wide, 20)
			if err != nil {
				return fmt.Errorf("rincian per label: %w", err)
			}
			fmt.Printf("  %d event, %d perpindahan, %d label terpakai\n",
				len(events), len(transitions), len(usage))
			for _, u := range usage[:min(3, len(usage))] {
				fmt.Printf("    %-18s dipasang=%d dilepas=%d kontak=%d aktif=%d\n",
					u.Name, u.Assigned, u.Removed, u.Contacts, u.ActiveContacts)
			}
			return nil
		}},
		{"drilldown leads", func() error {
			for _, status := range []string{"verified_new", "historical", "unknown"} {
				rows, err := repo.Leads(ctx, scope, wide, status, 20)
				if err != nil {
					return fmt.Errorf("%s: %w", status, err)
				}
				fmt.Printf("  %-13s %d kontak\n", status, len(rows))
			}
			return nil
		}},
		{"campaign terjadwal", func() error {
			rows, err := repo.UpcomingCampaigns(ctx, scope, 5)
			if err != nil {
				return err
			}
			fmt.Printf("  %d campaign\n", len(rows))
			return nil
		}},
		{"jadwal kerja", func() error {
			rows, err := repo.ListSchedules(ctx, scope, repository.ScheduleFilter{
				From: monthStart, To: monthStart.AddDate(0, 1, 0),
			})
			if err != nil {
				return err
			}
			fmt.Printf("  %d shift\n", len(rows))
			return nil
		}},
		{"performa per anggota (tim)", func() error {
			rep, err := repo.TeamPerformance(ctx, scope, wide)
			if err != nil {
				return err
			}
			fmt.Printf("  %d anggota terlihat, peran pemanggil=%q\n", len(rep.Members), rep.Role)
			for _, m := range rep.Members {
				team := "-"
				if m.Team != nil {
					team = fmt.Sprintf("tim: terkirim=%d kontak=%d grup=%d",
						m.Team.OutboundManual, m.Team.ContactsServed, m.Team.GroupReplies)
				}
				fmt.Printf("    %-24s %-10s terkirim=%d kontak=%d grup=%d jadwal=%d/%d · %s\n",
					m.Name, orDash(m.Role),
					m.Personal.OutboundManual, m.Personal.ContactsServed, m.Personal.GroupReplies,
					m.Personal.ActivitiesInSchedule, m.Personal.ActivitiesOutOfSchedule, team)
			}
			u := rep.Unattributed
			fmt.Printf("    %-24s %-10s terkirim=%d kontak=%d grup=%d\n",
				"(tanpa atribusi / HP)", "-", u.OutboundManual, u.ContactsServed, u.GroupReplies)
			return nil
		}},
		{"performa tim dengan filter PIC", func() error {
			// Exercises the PIC narrowing even when no PIC exists yet: the query
			// must run and return nothing rather than fail.
			f := wide
			id := scope.UserID
			f.PICUserID = &id
			rep, err := repo.TeamPerformance(ctx, scope, f)
			if err != nil {
				return err
			}
			fmt.Printf("  %d anggota di bawah PIC terpilih\n", len(rep.Members))
			return nil
		}},
		{"filter options mengikuti PIC", func() error {
			id := scope.UserID
			o, err := repo.FilterOptions(ctx, scope, &id)
			if err != nil {
				return err
			}
			fmt.Printf("  aplikasi=%d akun=%d freelance=%d shift=%d\n",
				len(o.Applications), len(o.Accounts), len(o.Freelancers), len(o.Shifts))
			return nil
		}},
		{"jalur tulis jadwal (dibuat lalu dihapus)", func() error {
			// The upsert's ON CONFLICT clause has to match the unique index in
			// migration 0020 exactly, including its COALESCE expressions. That
			// is not something reading the SQL can confirm, so one row is
			// written, saved again to prove the conflict resolves rather than
			// duplicates, and then removed.
			target := scope.UserID
			s1, err := repo.UpsertSchedule(ctx, mustUUID(workspaceID), scope.UserID,
				repository.ScheduleInput{
					UserID:   target,
					WorkDate: today,
					StartsAt: "09:00",
					EndsAt:   "17:00",
					IsActive: true,
					Note:     strPtr("verifikasi analyticscheck"),
				})
			if err != nil {
				return fmt.Errorf("insert: %w", err)
			}
			s2, err := repo.UpsertSchedule(ctx, mustUUID(workspaceID), scope.UserID,
				repository.ScheduleInput{
					UserID:   target,
					WorkDate: today,
					StartsAt: "09:00",
					EndsAt:   "18:00",
					IsActive: true,
					Note:     strPtr("verifikasi analyticscheck"),
				})
			if err != nil {
				return fmt.Errorf("upsert: %w", err)
			}
			if s1.ID != s2.ID {
				return fmt.Errorf("upsert created a second row (%s then %s); ON CONFLICT does not match the index",
					s1.ID, s2.ID)
			}
			if s2.EndsAt != "18:00" {
				return fmt.Errorf("upsert did not apply the new end time, got %s", s2.EndsAt)
			}

			windows, err := repo.ScheduleWindows(ctx, mustUUID(workspaceID),
				repository.WindowQuery{ScheduleID: &s2.ID})
			if err != nil {
				return fmt.Errorf("resolve window: %w", err)
			}
			if len(windows) != 1 {
				return fmt.Errorf("got %d windows, want 1", len(windows))
			}
			fmt.Printf("  1 shift, ON CONFLICT cocok, jendela UTC %s..%s\n",
				windows[0].Start.Format(time.RFC3339), windows[0].End.Format(time.RFC3339))

			return repo.DeleteSchedule(ctx, mustUUID(workspaceID), s2.ID)
		}},
		{"riwayat aktivitas terpadu", func() error {
			rows, err := repo.ActivityFeed(ctx, scope, wide, repository.ActivityCursor{}, 20)
			if err != nil {
				return err
			}
			fmt.Printf("  %d aktivitas\n", len(rows))
			for _, a := range rows[:min(4, len(rows))] {
				fmt.Printf("    %-19s %-17s %-22s %s\n",
					a.OccurredAt.In(analytics.Jakarta).Format("2006-01-02 15:04"),
					a.Kind, orDash(deref(a.ActorName)), orDash(deref(a.Subject)))
			}
			// The keyset cursor has to actually move; an offset that repeats the
			// same page is the failure this pagination exists to avoid.
			if len(rows) > 0 {
				next, err := repo.ActivityFeed(ctx, scope, wide, repository.ActivityCursor{
					Before: rows[len(rows)-1].OccurredAt, BeforeID: rows[len(rows)-1].ID,
				}, 5)
				if err != nil {
					return fmt.Errorf("halaman kedua: %w", err)
				}
				for _, n := range next {
					if n.ID == rows[0].ID {
						return fmt.Errorf("kursor tidak bergerak: halaman kedua mengulang baris pertama")
					}
				}
				fmt.Printf("  halaman kedua: %d aktivitas\n", len(next))
			}
			// Narrowing to one person must not leak anybody else's rows.
			self := wide
			id := scope.UserID
			self.AdminID = &id
			mine, err := repo.ActivityFeed(ctx, scope, self, repository.ActivityCursor{}, 20)
			if err != nil {
				return fmt.Errorf("aktivitas satu orang: %w", err)
			}
			for _, a := range mine {
				if a.ActorID == nil || *a.ActorID != id {
					return fmt.Errorf("aktivitas orang lain bocor ke Performa Saya: %s", a.ID)
				}
			}
			fmt.Printf("  aktivitas akun sendiri: %d, semuanya milik pemanggil\n", len(mine))
			return nil
		}},
		{"menunggu balasan (urutan & tautan room)", func() error {
			rows, err := repo.SLACycles(ctx, scope, wide, "waiting", 50)
			if err != nil {
				return err
			}
			fmt.Printf("  %d percakapan menunggu\n", len(rows))

			// Longest wait first: that is the order somebody acts on.
			for i := 1; i < len(rows); i++ {
				if rows[i].StartedAt.Before(rows[i-1].StartedAt) {
					return fmt.Errorf("urutan salah: baris %d lebih tua dari baris %d", i, i-1)
				}
			}
			for _, r := range rows[:min(3, len(rows))] {
				// Every waiting row must carry the three ids a deep link needs.
				if r.ApplicationID == nil || r.AccountID == nil {
					return fmt.Errorf("baris menunggu tanpa aplikasi/perangkat: %s", r.ID)
				}
				loc, err := repo.LocateConversation(ctx, scope, r.ConversationID)
				if err != nil {
					return fmt.Errorf("lokasi percakapan: %w", err)
				}
				if loc.AccountID != *r.AccountID {
					return fmt.Errorf("perangkat pada siklus dan pada percakapan berbeda")
				}
				wait := "-"
				if r.WaitingSeconds != nil {
					wait = (time.Duration(*r.WaitingSeconds) * time.Second).Round(time.Second).String()
				}
				fmt.Printf("    %-22s menunggu %-12s target %s\n",
					deref(r.ConversationName), wait,
					(time.Duration(r.TargetSeconds)*time.Second).String())
			}
			return nil
		}},
		{"kosakata campaign (label & variabel)", func() error {
			labels, err := repo.ListCampaignLabels(ctx, mustUUID(workspaceID), true)
			if err != nil {
				return fmt.Errorf("label: %w", err)
			}
			// all=true: this tool reads as the whole workspace, the way a
			// Leader would.
			vars, err := repo.ListCustomVariables(ctx, mustUUID(workspaceID), nil, true)
			if err != nil {
				return fmt.Errorf("variabel: %w", err)
			}
			replies, err := repo.ListQuickReplies(ctx, mustUUID(workspaceID), nil, true, false)
			if err != nil {
				return fmt.Errorf("balas cepat: %w", err)
			}
			fmt.Printf("  %d label, %d variabel custom, %d balas cepat\n",
				len(labels), len(vars), len(replies))
			return nil
		}},
		{"audiens broadcast", func() error {
			accounts, err := repo.AccountsByIDs(ctx, scope, allAccountIDs(ctx, pool, workspaceID))
			if err != nil {
				return fmt.Errorf("perangkat: %w", err)
			}
			ids := make([]uuid.UUID, 0, len(accounts))
			for _, a := range accounts {
				ids = append(ids, a.ID)
			}
			if len(ids) == 0 {
				fmt.Println("  (belum ada perangkat WhatsApp; query dilewati)")
				return nil
			}
			contacts, err := repo.ContactTargets(ctx, mustUUID(workspaceID), ids, 50)
			if err != nil {
				return fmt.Errorf("kontak: %w", err)
			}
			groups, err := repo.GroupTargets(ctx, mustUUID(workspaceID), ids)
			if err != nil {
				return fmt.Errorf("grup: %w", err)
			}
			var members []repository.TargetCandidate
			if len(groups) > 0 && groups[0].ConversationID != nil {
				members, err = repo.GroupMemberTargets(ctx, mustUUID(workspaceID),
					[]uuid.UUID{*groups[0].ConversationID})
				if err != nil {
					return fmt.Errorf("anggota grup: %w", err)
				}
			}
			fmt.Printf("  %d perangkat, %d kontak, %d grup, %d anggota grup pertama\n",
				len(ids), len(contacts), len(groups), len(members))
			return nil
		}},
		{"broadcast: simpan draft, hitung, lalu hapus", func() error {
			// A draft and nothing more. It is never scheduled, so the live
			// scheduler cannot pick it up, and it is deleted at the end — the
			// point is to prove the write path, the queue queries and the report
			// queries all run against the real schema, not to send anything.
			accountIDs := allAccountIDs(ctx, pool, workspaceID)
			if len(accountIDs) == 0 {
				fmt.Println("  (belum ada perangkat WhatsApp; pengecekan dilewati)")
				return nil
			}

			resolver := campaign.NewResolver(repo, func(uuid.UUID) bool { return true })
			plan, targets, err := resolver.Resolve(ctx, campaign.TargetRequest{
				WorkspaceID: mustUUID(workspaceID),
				Scope:       scope,
				AccountIDs:  accountIDs,
				Source:      models.TargetSourceManual,
				// Reserved test numbers, so nothing here could reach a real
				// person even if it were somehow sent.
				Numbers:      []string{"6281200000001", "081200000002", "0812-0000-0002", "bukan-nomor"},
				Template:     "{Halo|Hai} {{nama|Kak}}, ini uji coba dari {{aplikasi|salesan}}.",
				ComposeMode:  models.ComposeSpintaxVariables,
				DelayProfile: models.DelayCepat,
			})
			if err != nil {
				return fmt.Errorf("resolve: %w", err)
			}
			fmt.Printf("  rencana: valid=%d duplikat=%d invalid=%d estimasi=%ds\n",
				plan.Valid, plan.Duplicates, plan.Invalid, plan.EstimatedSeconds)
			if plan.Duplicates != 1 {
				return fmt.Errorf("dedup gagal: 0812-0000-0002 dan 081200000002 harus dianggap satu, dapat %d duplikat", plan.Duplicates)
			}
			if plan.Invalid != 1 {
				return fmt.Errorf("validasi gagal: 'bukan-nomor' harus ditolak, dapat %d masalah", plan.Invalid)
			}
			if len(plan.Preview) == 0 {
				return fmt.Errorf("preview kosong")
			}
			fmt.Printf("  contoh pesan: %q\n", plan.Preview[0])

			// One campaign belongs to exactly one application, enforced by the
			// database since migration 0031. The diagnostic has to name it.
			appID, err := repo.ApplicationOfAccount(ctx, mustUUID(workspaceID), accountIDs[0])
			if err != nil {
				return fmt.Errorf("aplikasi perangkat: %w", err)
			}
			if appID == nil {
				fmt.Println("  (perangkat belum ditugaskan ke aplikasi; pengecekan dilewati)")
				return nil
			}

			c, err := repo.SaveBroadcast(ctx, mustUUID(workspaceID), scope.UserID,
				repository.SaveBroadcastInput{
					CampaignType:  models.CampaignBroadcast,
					Name:          "verifikasi analyticscheck",
					Template:      "uji coba",
					ComposeMode:   models.ComposePlain,
					ApplicationID: appID,
					AccountIDs:    accountIDs,
					DelayProfile:  models.DelayCepat,
					TargetSource:  models.TargetSourceManual,
					Targets:       targets,
				})
			if err != nil {
				return fmt.Errorf("simpan: %w", err)
			}

			// The database must refuse a number from another application, even
			// when the Go layer is bypassed entirely. This is the guarantee that
			// survives a future code path forgetting to check.
			var foreign *uuid.UUID
			if err := pool.QueryRow(ctx, `
				select a.id from public.whatsapp_accounts a
				 where a.workspace_id = $1 and a.application_id is distinct from $2
				 limit 1`, mustUUID(workspaceID), *appID).Scan(&foreign); err != nil && !isNoRows(err) {
				return fmt.Errorf("cari perangkat aplikasi lain: %w", err)
			}
			if foreign != nil {
				_, err := pool.Exec(ctx, `
					insert into public.broadcast_sender_devices
						(workspace_id, campaign_id, account_id, position)
					values ($1, $2, $3, 99)`, mustUUID(workspaceID), c.ID, *foreign)
				if err == nil {
					return fmt.Errorf("BOCOR: perangkat dari aplikasi lain diterima ke campaign ini")
				}
				fmt.Printf("  lintas aplikasi ditolak basis data: %s\n", shortErr(err))
			} else {
				fmt.Println("  (tidak ada perangkat di aplikasi lain untuk diuji)")
			}
			if c.TargetCount != len(targets) {
				return fmt.Errorf("target_count=%d, seharusnya %d", c.TargetCount, len(targets))
			}

			devices, err := repo.CampaignDevices(ctx, c.ID)
			if err != nil {
				return fmt.Errorf("perangkat: %w", err)
			}
			totals, err := repo.TargetTotals(ctx, c.ID)
			if err != nil {
				return fmt.Errorf("rekap: %w", err)
			}
			pending, err := repo.PendingTargetCount(ctx, c.ID)
			if err != nil {
				return fmt.Errorf("antrean: %w", err)
			}
			sent, unknown, err := repo.ReconcileStuckTargets(ctx, c.ID)
			if err != nil {
				return fmt.Errorf("rekonsiliasi: %w", err)
			}
			list, err := repo.ListTargets(ctx, c.ID, "", 5, 0)
			if err != nil {
				return fmt.Errorf("daftar target: %w", err)
			}
			replies, err := repo.CampaignReplies(ctx, c.ID)
			if err != nil {
				return fmt.Errorf("balasan: %w", err)
			}
			if _, err := repo.CampaignDuration(ctx, c.ID); err != nil {
				return fmt.Errorf("durasi: %w", err)
			}
			// Claiming a recipient must work; the draft is not scheduled, so the
			// campaign-level claim would (correctly) skip it.
			claimed, err := repo.ClaimTargets(ctx, c.ID, accountIDs[0], time.Minute, 1)
			if err != nil {
				return fmt.Errorf("klaim target: %w", err)
			}
			fmt.Printf("  tersimpan: %d perangkat, total=%d menunggu=%d, daftar=%d, klaim=%d\n",
				len(devices), totals.Total, pending, len(list), len(claimed))
			fmt.Printf("  rekonsiliasi: terkirim=%d tidak-diketahui=%d, balasan=%d\n",
				sent, unknown, replies)

			// Story-side queries, on the same reversible draft.
			if err := repo.SeedStoryPublications(ctx, mustUUID(workspaceID), c.ID, accountIDs); err != nil {
				return fmt.Errorf("seed publikasi story: %w", err)
			}
			pubs, err := repo.StoryPublications(ctx, c.ID)
			if err != nil {
				return fmt.Errorf("publikasi story: %w", err)
			}
			perDevice, unique, uniqueKnown, err := repo.StoryViewTotals(ctx, c.ID)
			if err != nil {
				return fmt.Errorf("views story: %w", err)
			}
			if _, err := repo.ClaimStoryPublications(ctx, c.ID, time.Minute, 4); err != nil {
				return fmt.Errorf("klaim publikasi: %w", err)
			}
			if _, err := repo.ExpireStories(ctx); err != nil {
				return fmt.Errorf("kedaluwarsa story: %w", err)
			}
			fmt.Printf("  story: %d publikasi, views per perangkat=%d viewer unik=%d (tersedia=%t)\n",
				len(pubs), perDevice, unique, uniqueKnown)

			// Cancel first so nothing is left claimable, then remove the draft
			// entirely. Deleting cascades to devices, targets and publications.
			// The creator stands in as the actor: this tool has no signed-in
			// user, and the row is deleted on the next line anyway.
			if _, err := repo.RequestCancel(ctx, mustUUID(workspaceID), c.ID, mustUUID(workspaceID)); err != nil {
				return fmt.Errorf("batalkan: %w", err)
			}
			if _, err := pool.Exec(ctx,
				`delete from public.content_campaigns where id = $1`, c.ID); err != nil {
				return fmt.Errorf("hapus: %w", err)
			}
			fmt.Println("  draft uji coba dihapus")
			return nil
		}},
		{"anggota organisasi", func() error {
			members, err := repo.ListOrgMembers(ctx, mustUUID(workspaceID))
			if err != nil {
				return err
			}
			for _, m := range members {
				fmt.Printf("  %-28s ws=%-6s op=%-10s aplikasi=%d\n",
					m.Email, m.WorkspaceRole, orDash(m.Role), len(m.Applications))
			}
			return nil
		}},
	}

	failed := 0
	for _, step := range steps {
		fmt.Printf("→ %s\n", step.name)
		if err := step.fn(); err != nil {
			failed++
			fmt.Printf("  FAILED: %v\n", err)
		}
		fmt.Println()
	}

	if failed > 0 {
		return fmt.Errorf("%d of %d checks failed", failed, len(steps))
	}
	fmt.Printf("All %d analytics queries executed successfully.\n", len(steps))
	return nil
}

func printSummary(label string, s *models.DashboardSummary) {
	fmt.Printf("  %s\n", label)
	fmt.Printf("    pribadi   masuk=%d terkirim=%d dariHP=%d\n",
		s.InboundPersonal, s.OutboundManualPersonal, s.OutboundDevicePersonal)
	fmt.Printf("    kontak    menghubungi=%d terlayani=%d belum=%d leads=%d\n",
		s.ContactsInbound, s.ContactsServed, s.ContactsUnserved, s.VerifiedNewLeads)
	fmt.Printf("    grup      masuk=%d balasan=%d aktif=%d ditangani=%d\n",
		s.GroupInbound, s.GroupReplies, s.GroupsActive, s.GroupsHandled)
	fmt.Printf("    label     total=%d dipasang=%d dilepas=%d pindah=%d kontak=%d pertama=%d\n",
		s.LabelChangesTotal, s.LabelsAssigned, s.LabelsRemoved, s.LabelsMoved,
		s.LabelContactsChanged, s.ContactsFirstLabeled)
	fmt.Printf("    campaign  broadcast=%d/%d/%d story=%d/%d/%d\n",
		s.BroadcastsCreated, s.BroadcastsSent, s.BroadcastsFailed,
		s.StoriesCreated, s.StoriesPublished, s.StoriesFailed)
	fmt.Printf("    respons   rata=%s median=%s sla=%d/%d menunggu=%d dikonfigurasi=%v\n",
		durationOf(s.AvgFirstResponseSeconds), durationOf(s.MedianFirstResponseSeconds),
		s.SLAAchieved, s.SLABreached, s.SLAWaiting, s.SLAConfigured)
	fmt.Printf("    followup  aktivitas=%d kontak=%d dibalas=%d belum=%d\n",
		s.FollowUps, s.FollowUpContacts, s.FollowUpsAnswered, s.FollowUpsUnanswered)
	fmt.Printf("    jadwal    jam=%ds dalam=%d luar=%d hariAktif=%d\n",
		s.WorkSeconds, s.ActivitiesInSchedule, s.ActivitiesOutOfSchedule, s.ActiveDays)
}

func durationOf(v *int) string {
	if v == nil {
		return "-"
	}
	return (time.Duration(*v) * time.Second).String()
}

func deref(v *string) string {
	if v == nil || *v == "" {
		return "-"
	}
	return *v
}

func orDash(v string) string {
	if v == "" {
		return "-"
	}
	return v
}

func strPtr(s string) *string { return &s }

// isNoRows keeps "matched nothing" out of the failure path, for the lookups
// where matching nothing is an ordinary answer.
func isNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

// shortErr trims a database error to its first line, which is the part worth
// printing in a check's output.
func shortErr(err error) string {
	line := err.Error()
	if idx := strings.IndexByte(line, '\n'); idx >= 0 {
		line = line[:idx]
	}
	if idx := strings.Index(line, "ERROR: "); idx >= 0 {
		line = line[idx+len("ERROR: "):]
	}
	return line
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// allAccountIDs lists every WhatsApp account in the workspace, so the campaign
// checks run against whatever devices actually exist rather than a fixture.
func allAccountIDs(ctx context.Context, pool *pgxpool.Pool, workspaceID string) []uuid.UUID {
	rows, err := pool.Query(ctx,
		`select id from public.whatsapp_accounts where workspace_id = $1 order by name`,
		mustUUID(workspaceID))
	if err != nil {
		return nil
	}
	defer rows.Close()

	out := []uuid.UUID{}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return out
		}
		out = append(out, id)
	}
	return out
}

func mustUUID(s string) uuid.UUID {
	id, err := uuid.Parse(s)
	if err != nil {
		panic(err)
	}
	return id
}
