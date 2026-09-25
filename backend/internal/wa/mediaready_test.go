package wa

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"
)

// A backend whose bucket refuses a fixed number of times before answering, the
// way the real one does while the service behind it is still starting.
type flakyStore struct {
	fakeStore
	mu       sync.Mutex
	failures int
	calls    int
}

func (s *flakyStore) EnsureReady(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.calls <= s.failures {
		return errors.New("storage: service is not answering yet")
	}
	return nil
}

func (s *flakyStore) attempts() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func quietManager(store *flakyStore) *Manager {
	return &Manager{store: store, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

// The invariant the interface depends on: until the bucket has answered, media
// is reported as unavailable rather than attempted.
func TestMediaStaysDisabledUntilTheBucketAnswers(t *testing.T) {
	store := &flakyStore{failures: 1}
	m := quietManager(store)

	if m.MediaEnabled() {
		t.Fatal("media was enabled before the bucket was ever checked")
	}
	if m.tryStorage(context.Background(), 1) {
		t.Fatal("a refused check reported success")
	}
	if m.MediaEnabled() {
		t.Error("media is enabled although the bucket refused")
	}

	if !m.tryStorage(context.Background(), 2) {
		t.Fatal("the second check should have succeeded")
	}
	if !m.MediaEnabled() {
		t.Error("the bucket answered but media is still disabled")
	}
}

// The failure this replaces: one refusal at boot disabled photos for the life
// of the process, and only a restart brought them back.
func TestStorageIsRetriedUntilItIsReady(t *testing.T) {
	// The loop is what is under test, not the clock it waits on.
	storageRetryBase = time.Millisecond
	t.Cleanup(func() { storageRetryBase = 10 * time.Second })

	store := &flakyStore{failures: 2}
	m := quietManager(store)
	m.rootCtx, m.cancel = context.WithCancel(context.Background())
	t.Cleanup(m.cancel)

	m.ensureStorage(m.rootCtx)
	if m.MediaEnabled() {
		t.Fatal("media must not be enabled while the bucket is still refusing")
	}

	deadline := time.Now().Add(10 * time.Second)
	for !m.MediaEnabled() {
		if time.Now().After(deadline) {
			t.Fatalf("media never recovered; bucket was asked %d times", store.attempts())
		}
		time.Sleep(50 * time.Millisecond)
	}
	m.cancel()
	m.wg.Wait()

	if store.attempts() != 3 {
		t.Errorf("bucket asked %d times, want 3 (one at boot, two retries)", store.attempts())
	}
}

// Quick while the cause is probably a service a few seconds behind, then slow,
// so a genuinely broken endpoint is not hammered forever.
func TestStorageRetryBacksOffAndStops(t *testing.T) {
	first := storageRetryDelay(0)
	if first != storageRetryBase {
		t.Errorf("first retry after %v, want 10s", first)
	}
	if second := storageRetryDelay(1); second <= first {
		t.Errorf("second retry %v is not longer than the first %v", second, first)
	}
	for _, attempt := range []int{6, 10, 100} {
		if d := storageRetryDelay(attempt); d != 5*time.Minute {
			t.Errorf("retry %d waits %v, want the 5m cap", attempt, d)
		}
	}
}
