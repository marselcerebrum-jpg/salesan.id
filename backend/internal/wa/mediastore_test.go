package wa

import (
	"context"
	"encoding/hex"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/salesan/omnichannel/backend/internal/repository"
)

// These exercise the store-once rules, not the SQL that enforces them.
//
// The fake index below mimics what the real queries promise — a locked read, and
// an insert that yields to whoever got there first — so a bug in this file's
// algorithm is caught here. A bug in the queries themselves is not, and cannot
// be: see TestIntegrationMediaObject* in internal/repository, which runs the
// real statements against a real database.

type fakeIndex struct {
	mu      sync.Mutex
	objects map[string]repository.StoredObject
	marked  map[uuid.UUID]string
}

func newFakeIndex() *fakeIndex {
	return &fakeIndex{
		objects: map[string]repository.StoredObject{},
		marked:  map[uuid.UUID]string{},
	}
}

func indexKey(workspaceID uuid.UUID, sha []byte) string {
	return workspaceID.String() + ":" + hex.EncodeToString(sha)
}

func (f *fakeIndex) ClaimStoredObject(
	_ context.Context, workspaceID uuid.UUID, sha []byte, attachmentID uuid.UUID,
) (*repository.StoredObject, error) {
	if len(sha) == 0 {
		return nil, nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	obj, ok := f.objects[indexKey(workspaceID, sha)]
	if !ok {
		return nil, nil
	}
	f.marked[attachmentID] = obj.StoragePath
	return &obj, nil
}

func (f *fakeIndex) RegisterStoredObject(
	_ context.Context, workspaceID uuid.UUID, sha []byte, path string, size int64,
) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	k := indexKey(workspaceID, sha)
	if _, taken := f.objects[k]; taken {
		return nil // whoever was first keeps it
	}
	f.objects[k] = repository.StoredObject{StoragePath: path, SizeBytes: size}
	return nil
}

func (f *fakeIndex) MarkAttachmentStored(
	_ context.Context, id uuid.UUID, path string, _ int64,
) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.marked[id] = path
	return nil
}

func (f *fakeIndex) pathFor(id uuid.UUID) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.marked[id]
}

// fakeStore records what reached the bucket. Only UploadBytes is used by this
// layer; the rest satisfies the interface and fails loudly if something starts
// calling it.
type fakeStore struct {
	mu      sync.Mutex
	written map[string]int // key -> how many times it was written
}

func newFakeStore() *fakeStore { return &fakeStore{written: map[string]int{}} }

func (s *fakeStore) Name() string                            { return "fake" }
func (s *fakeStore) EnsureReady(context.Context) error       { return nil }
func (s *fakeStore) Remove(context.Context, ...string) error { return nil }

func (s *fakeStore) UploadBytes(_ context.Context, key, _ string, _ []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.written[key]++
	return nil
}

func (s *fakeStore) Upload(context.Context, string, string, io.Reader, int64) error {
	panic("Upload is not part of the store-once path")
}

func (s *fakeStore) SignedURL(context.Context, string, time.Duration, string) (string, error) {
	panic("SignedURL is not part of the store-once path")
}

func (s *fakeStore) Download(context.Context, string) ([]byte, string, error) {
	panic("Download is not part of the store-once path")
}

// objects is how many distinct files ended up in the bucket, which is the number
// that decides whether the disk fills up.
func (s *fakeStore) objects() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.written))
	for k := range s.written {
		out = append(out, k)
	}
	return out
}

func (s *fakeStore) writes() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, c := range s.written {
		n += c
	}
	return n
}

// counted returns a fetch function that reports how often it was asked for the
// bytes. Fetching is the expensive half — it is a download from WhatsApp — so a
// fetch that never happens is the saving this whole change is about.
func counted(n *int, mu *sync.Mutex) func(context.Context) ([]byte, error) {
	return func(context.Context) ([]byte, error) {
		mu.Lock()
		*n++
		mu.Unlock()
		return []byte("berkas-uji"), nil
	}
}

// The case that filled the disk: one broadcast, many recipients, one file.
func TestStoreOnceReusesTheObjectHoldingTheSameFile(t *testing.T) {
	ctx := context.Background()
	idx, store := newFakeIndex(), newFakeStore()
	workspace := uuid.New()
	sha := []byte("hash-yang-sama-32-byte----------")

	var mu sync.Mutex
	fetches := 0

	first, second := uuid.New(), uuid.New()
	req := func(attachment uuid.UUID) storeRequest {
		return storeRequest{
			WorkspaceID:  workspace,
			AttachmentID: attachment,
			SHA256:       sha,
			FallbackKey:  "tidak/dipakai",
			ContentType:  "image/jpeg",
			Fetch:        counted(&fetches, &mu),
		}
	}

	a, err := storeOnce(ctx, idx, store, req(first))
	if err != nil {
		t.Fatal(err)
	}
	if fetches != 1 || store.writes() != 1 {
		t.Fatalf("first copy: fetches=%d writes=%d, want 1 and 1", fetches, store.writes())
	}

	b, err := storeOnce(ctx, idx, store, req(second))
	if err != nil {
		t.Fatal(err)
	}

	if fetches != 1 {
		t.Errorf("the second recipient downloaded the file again: fetches=%d, want 1", fetches)
	}
	if store.writes() != 1 {
		t.Errorf("the second recipient uploaded the file again: writes=%d, want 1", store.writes())
	}
	if a.StoragePath != b.StoragePath {
		t.Errorf("paths differ: %q and %q", a.StoragePath, b.StoragePath)
	}
	if got := idx.pathFor(second); got != a.StoragePath {
		t.Errorf("second attachment points at %q, want %q", got, a.StoragePath)
	}
}

// Saving disk is not a reason to let one tenant's file live where another
// tenant's rows can reach it.
func TestStoreOnceKeepsWorkspacesApart(t *testing.T) {
	ctx := context.Background()
	idx, store := newFakeIndex(), newFakeStore()
	sha := []byte("isi-identik-di-dua-workspace---")
	one, two := uuid.New(), uuid.New()

	var mu sync.Mutex
	fetches := 0

	a, err := storeOnce(ctx, idx, store, storeRequest{
		WorkspaceID: one, AttachmentID: uuid.New(), SHA256: sha,
		ContentType: "image/jpeg", Fetch: counted(&fetches, &mu),
	})
	if err != nil {
		t.Fatal(err)
	}
	b, err := storeOnce(ctx, idx, store, storeRequest{
		WorkspaceID: two, AttachmentID: uuid.New(), SHA256: sha,
		ContentType: "image/jpeg", Fetch: counted(&fetches, &mu),
	})
	if err != nil {
		t.Fatal(err)
	}

	if a.StoragePath == b.StoragePath {
		t.Fatalf("both workspaces share the object %q", a.StoragePath)
	}
	if !strings.HasPrefix(a.StoragePath, one.String()+"/") {
		t.Errorf("%q is not inside workspace %s", a.StoragePath, one)
	}
	if !strings.HasPrefix(b.StoragePath, two.String()+"/") {
		t.Errorf("%q is not inside workspace %s", b.StoragePath, two)
	}
	if len(store.objects()) != 2 {
		t.Errorf("objects in bucket = %d, want one per workspace", len(store.objects()))
	}
}

// Parallel work is the normal case, not an edge one: a thread list can ask for
// many previews in the same moment. However many callers arrive together, the
// bucket must end with one file, because the bucket is what ran out of room.
func TestStoreOnceLeavesOneObjectUnderConcurrency(t *testing.T) {
	ctx := context.Background()
	idx, store := newFakeIndex(), newFakeStore()
	workspace := uuid.New()
	sha := []byte("satu-berkas-banyak-penerima----")

	const callers = 24
	var mu sync.Mutex
	fetches := 0

	paths := make([]string, callers)
	errs := make([]error, callers)

	var start sync.WaitGroup
	var done sync.WaitGroup
	start.Add(1)
	for i := 0; i < callers; i++ {
		done.Add(1)
		go func(i int) {
			defer done.Done()
			start.Wait() // all of them past the gate at once
			obj, err := storeOnce(ctx, idx, store, storeRequest{
				WorkspaceID:  workspace,
				AttachmentID: uuid.New(),
				SHA256:       sha,
				ContentType:  "image/jpeg",
				Fetch:        counted(&fetches, &mu),
			})
			errs[i] = err
			if obj != nil {
				paths[i] = obj.StoragePath
			}
		}(i)
	}
	start.Done()
	done.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("caller %d: %v", i, err)
		}
	}

	if got := len(store.objects()); got != 1 {
		t.Errorf("objects in bucket = %d, want 1: %v", got, store.objects())
	}
	for i, p := range paths {
		if p != paths[0] {
			t.Fatalf("caller %d got %q, caller 0 got %q", i, p, paths[0])
		}
	}
	if fetches > callers {
		t.Errorf("fetches = %d, more than the %d callers", fetches, callers)
	}
	// Not asserted as exactly one. Callers that all miss the lookup together do
	// each fetch and upload, and the key is computed from the hash so they write
	// the same bytes to the same object rather than creating several. Wasted work
	// in a burst, yes; wasted disk, no, and disk was the problem.
	t.Logf("%d callers caused %d fetches and %d writes of 1 object",
		callers, fetches, store.writes())
}

// A ref with no content hash cannot be deduplicated. It still has to be stored,
// under the key the caller supplies, rather than failing.
func TestStoreOnceFallsBackWhenThereIsNoHash(t *testing.T) {
	ctx := context.Background()
	idx, store := newFakeIndex(), newFakeStore()
	attachment := uuid.New()

	var mu sync.Mutex
	fetches := 0

	obj, err := storeOnce(ctx, idx, store, storeRequest{
		WorkspaceID:  uuid.New(),
		AttachmentID: attachment,
		FallbackKey:  "ws/akun/pesan/0.jpg",
		ContentType:  "image/jpeg",
		Fetch:        counted(&fetches, &mu),
	})
	if err != nil {
		t.Fatal(err)
	}
	if obj.StoragePath != "ws/akun/pesan/0.jpg" {
		t.Errorf("path = %q, want the fallback key", obj.StoragePath)
	}
	if got := idx.pathFor(attachment); got != obj.StoragePath {
		t.Errorf("attachment points at %q, want %q", got, obj.StoragePath)
	}
}

// The key is the hash, so the same bytes always name the same object. That is
// what makes a lost race harmless instead of leaving a second copy nothing
// points at.
func TestHashStorageKeyIsTheSameForTheSameContent(t *testing.T) {
	workspace := uuid.New()
	sha := []byte{0xde, 0xad, 0xbe, 0xef}

	a := hashStorageKey(workspace, sha)
	b := hashStorageKey(workspace, []byte{0xde, 0xad, 0xbe, 0xef})
	if a != b {
		t.Fatalf("same content gave %q and %q", a, b)
	}
	if want := workspace.String() + "/by-hash/deadbeef"; a != want {
		t.Errorf("key = %q, want %q", a, want)
	}
}
