package repository

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/salesan/omnichannel/backend/internal/models"
)

// Integration tests for the store-once layer.
//
// These run the real statements, which is the point: the rules that keep one
// file to one object live in the SQL — a locked read, an insert that yields to
// whoever was first, and a question asked of the attachment rows before anything
// is deleted. A fake cannot vouch for any of that.
//
// Same guard as the rest of this file's neighbours: SALESAN_TEST_DATABASE_URL,
// never DATABASE_URL.
//
//	$env:SALESAN_TEST_DATABASE_URL = "postgres://..."   # a scratch database
//	go test ./internal/repository -run IntegrationMedia -v

// attach writes one attachment row for a fresh message and returns its id.
func (f *fixture) attach(t *testing.T) uuid.UUID {
	t.Helper()
	msg := f.insertMessage(t, InsertMessageInput{
		FromMe: true, Type: "image", Timestamp: time.Now(),
	})
	att, _, err := f.repo.InsertAttachment(context.Background(), InsertAttachmentInput{
		WorkspaceID: f.workspaceID,
		AccountID:   f.accountID,
		MessageID:   msg.ID,
		Index:       0,
		Kind:        "image",
	})
	if err != nil {
		t.Fatalf("insert attachment: %v", err)
	}
	return att.ID
}

// storedPath is what an attachment row says about where its bytes are.
func (f *fixture) storedPath(t *testing.T, id uuid.UUID) (string, string) {
	t.Helper()
	var path *string
	var status string
	err := f.repo.pool.QueryRow(context.Background(), `
		select storage_path, storage_status::text
		  from public.message_attachments where id = $1`, id).Scan(&path, &status)
	if err != nil {
		t.Fatalf("read attachment %s: %v", id, err)
	}
	if path == nil {
		return "", status
	}
	return *path, status
}

func (f *fixture) objectRows(t *testing.T, path string) int {
	t.Helper()
	var n int
	if err := f.repo.pool.QueryRow(context.Background(),
		`select count(*) from public.media_objects where storage_path = $1`, path,
	).Scan(&n); err != nil {
		t.Fatalf("count media objects: %v", err)
	}
	return n
}

// The broadcast case. Sixty recipients, sixty attachment rows, one file: the
// second and every later one must find the object the first registered.
func TestIntegrationMediaObjectIsClaimedByLaterAttachments(t *testing.T) {
	f := newFixture(t, models.ConversationTypePersonal)
	ctx := context.Background()
	sha := []byte("isi-berkas-yang-sama-untuk-semua")
	const path = "by-hash/berkas-satu"

	first, second := f.attach(t), f.attach(t)

	// Nothing holds this content yet.
	obj, err := f.repo.ClaimStoredObject(ctx, f.workspaceID, sha, first)
	if err != nil {
		t.Fatal(err)
	}
	if obj != nil {
		t.Fatalf("claimed %q before anything was registered", obj.StoragePath)
	}

	if err := f.repo.RegisterStoredObject(ctx, f.workspaceID, sha, path, 1234); err != nil {
		t.Fatal(err)
	}

	for _, id := range []uuid.UUID{first, second} {
		obj, err := f.repo.ClaimStoredObject(ctx, f.workspaceID, sha, id)
		if err != nil {
			t.Fatal(err)
		}
		if obj == nil {
			t.Fatalf("attachment %s could not claim the registered object", id)
		}
		if obj.StoragePath != path || obj.SizeBytes != 1234 {
			t.Errorf("claimed %q/%d, want %q/1234", obj.StoragePath, obj.SizeBytes, path)
		}

		gotPath, status := f.storedPath(t, id)
		if gotPath != path {
			t.Errorf("attachment %s points at %q, want %q", id, gotPath, path)
		}
		if status != models.AttachmentStored {
			t.Errorf("attachment %s status = %q, want stored", id, status)
		}
	}
}

// Two tenants with byte-identical files get two objects. Sharing one would put a
// customer's file on a path reachable through another customer's rows, and disk
// is not worth that.
func TestIntegrationMediaObjectKeepsWorkspacesApart(t *testing.T) {
	one := newFixture(t, models.ConversationTypePersonal)
	two := newFixture(t, models.ConversationTypePersonal)
	ctx := context.Background()
	sha := []byte("isi-identik-di-dua-workspace----")

	if err := one.repo.RegisterStoredObject(ctx, one.workspaceID, sha, "ws1/by-hash/x", 10); err != nil {
		t.Fatal(err)
	}

	other := two.attach(t)
	obj, err := two.repo.ClaimStoredObject(ctx, two.workspaceID, sha, other)
	if err != nil {
		t.Fatal(err)
	}
	if obj != nil {
		t.Fatalf("workspace two claimed workspace one's object %q", obj.StoragePath)
	}
	if path, _ := two.storedPath(t, other); path != "" {
		t.Errorf("attachment in workspace two points at %q, want nothing", path)
	}
}

// Parallel callers registering the same content: one row wins, and everybody
// ends up pointing at the winner rather than at whatever they each proposed.
func TestIntegrationMediaObjectRegisterYieldsToTheFirstWriter(t *testing.T) {
	f := newFixture(t, models.ConversationTypePersonal)
	ctx := context.Background()
	sha := []byte("perlombaan-pendaftaran-objek----")

	const callers = 8
	ids := make([]uuid.UUID, callers)
	for i := range ids {
		ids[i] = f.attach(t)
	}

	var start, done sync.WaitGroup
	start.Add(1)
	errs := make([]error, callers)
	for i := 0; i < callers; i++ {
		done.Add(1)
		go func(i int) {
			defer done.Done()
			start.Wait()
			// Each proposes a different path on purpose. In production the path
			// is computed from the hash so they would all agree; proposing
			// different ones is what proves the winner is the one that sticks.
			errs[i] = f.repo.RegisterStoredObject(
				ctx, f.workspaceID, sha, "by-hash/kandidat-"+uuid.NewString()[:8], int64(i+1))
		}(i)
	}
	start.Done()
	done.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("caller %d: %v", i, err)
		}
	}

	var rows int
	if err := f.repo.pool.QueryRow(ctx,
		`select count(*) from public.media_objects where workspace_id = $1 and file_sha256 = $2`,
		f.workspaceID, sha).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("media_objects rows = %d, want exactly 1", rows)
	}

	winner := ""
	for _, id := range ids {
		obj, err := f.repo.ClaimStoredObject(ctx, f.workspaceID, sha, id)
		if err != nil {
			t.Fatal(err)
		}
		if obj == nil {
			t.Fatalf("attachment %s claimed nothing", id)
		}
		if winner == "" {
			winner = obj.StoragePath
		}
		if obj.StoragePath != winner {
			t.Fatalf("attachment %s got %q, another got %q", id, obj.StoragePath, winner)
		}
	}
}

// The deletion rule. One file now serves many messages, so clearing one of them
// must leave the file alone while any other still points at it.
func TestIntegrationReleaseKeepsObjectsStillInUse(t *testing.T) {
	f := newFixture(t, models.ConversationTypePersonal)
	ctx := context.Background()
	sha := []byte("berkas-dipakai-dua-lampiran-----")
	const path = "by-hash/dipakai-bersama"

	keep, drop := f.attach(t), f.attach(t)
	if err := f.repo.RegisterStoredObject(ctx, f.workspaceID, sha, path, 99); err != nil {
		t.Fatal(err)
	}
	for _, id := range []uuid.UUID{keep, drop} {
		if _, err := f.repo.ClaimStoredObject(ctx, f.workspaceID, sha, id); err != nil {
			t.Fatal(err)
		}
	}

	// One of the two goes, the way revoking or hiding a message removes its row.
	if _, err := f.repo.pool.Exec(ctx,
		`delete from public.message_attachments where id = $1`, drop); err != nil {
		t.Fatal(err)
	}

	free, err := f.repo.ReleaseStorageObjects(ctx, []string{path}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(free) != 0 {
		t.Fatalf("released %v while an attachment still points at it", free)
	}
	if n := f.objectRows(t, path); n != 1 {
		t.Errorf("media_objects rows for a file still in use = %d, want 1", n)
	}

	// Now the last one goes too, and only then is the file free.
	if _, err := f.repo.pool.Exec(ctx,
		`delete from public.message_attachments where id = $1`, keep); err != nil {
		t.Fatal(err)
	}

	free, err = f.repo.ReleaseStorageObjects(ctx, []string{path}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(free) != 1 || free[0] != path {
		t.Fatalf("released %v, want just %q", free, path)
	}
	if n := f.objectRows(t, path); n != 0 {
		t.Errorf("media_objects rows after release = %d, want 0: a later attachment "+
			"would be told the file is stored at a path that no longer exists", n)
	}
}

// The retention janitor empties the bucket before it marks rows expired, so the
// rows it is about to settle still point at the files. Naming them is what lets
// it delete anything at all, and it must not make it delete anything more.
func TestIntegrationReleaseDisregardsTheBatchBeingExpired(t *testing.T) {
	f := newFixture(t, models.ConversationTypePersonal)
	ctx := context.Background()
	sha := []byte("berkas-yang-akan-kedaluwarsa----")
	const path = "by-hash/akan-kedaluwarsa"

	expiring, survivor := f.attach(t), f.attach(t)
	if err := f.repo.RegisterStoredObject(ctx, f.workspaceID, sha, path, 7); err != nil {
		t.Fatal(err)
	}
	for _, id := range []uuid.UUID{expiring, survivor} {
		if _, err := f.repo.ClaimStoredObject(ctx, f.workspaceID, sha, id); err != nil {
			t.Fatal(err)
		}
	}

	// Only one of them is in the batch: the other is a broadcast sibling whose
	// message is newer, and it keeps the file.
	free, err := f.repo.ReleaseStorageObjects(ctx, []string{path}, []uuid.UUID{expiring})
	if err != nil {
		t.Fatal(err)
	}
	if len(free) != 0 {
		t.Fatalf("released %v while a sibling attachment still points at it", free)
	}

	// With both in the batch, nothing outside it is left and the file goes.
	free, err = f.repo.ReleaseStorageObjects(ctx, []string{path}, []uuid.UUID{expiring, survivor})
	if err != nil {
		t.Fatal(err)
	}
	if len(free) != 1 || free[0] != path {
		t.Fatalf("released %v, want just %q", free, path)
	}
}

// A key nothing ever registered — an old per-message path, or a file from before
// this layer existed — is still safe to delete once its rows are gone.
func TestIntegrationReleaseHandlesKeysOutsideTheLookup(t *testing.T) {
	f := newFixture(t, models.ConversationTypePersonal)
	ctx := context.Background()

	free, err := f.repo.ReleaseStorageObjects(ctx,
		[]string{"ws/akun/pesan-lama/0.jpg"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(free) != 1 {
		t.Fatalf("released %v, want the unreferenced legacy key", free)
	}
}
