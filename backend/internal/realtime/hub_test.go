package realtime

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

func quietHub() *Hub {
	return NewHub(slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// A server that upgrades and then does nothing, so tests can hold real
// *websocket.Conn values. Real connections matter here: shutdown closes the
// socket as well as the channel, and a stub would not exercise that half.
func socketPair(t *testing.T) func() *websocket.Conn {
	t.Helper()
	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				_ = conn.Close()
				return
			}
		}
	}))
	t.Cleanup(srv.Close)

	url := "ws" + strings.TrimPrefix(srv.URL, "http")
	return func() *websocket.Conn {
		conn, _, err := websocket.DefaultDialer.Dial(url, nil)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		t.Cleanup(func() { _ = conn.Close() })
		return conn
	}
}

// A registered client with no pumps running, so the test drives trySend and
// shutdown directly instead of racing the read and write loops.
func detachedClient(h *Hub, ws uuid.UUID, conn *websocket.Conn) *Client {
	c := &Client{hub: h, conn: conn, workspaceID: ws, send: make(chan []byte, sendBuffer)}
	h.mu.Lock()
	if h.clients[ws] == nil {
		h.clients[ws] = map[*Client]struct{}{}
	}
	h.clients[ws][c] = struct{}{}
	h.mu.Unlock()
	return c
}

// shutdown must be safe to call twice, from anywhere. It used to be guarded by
// a sync.Once; the guard is now a flag under the same mutex trySend takes, and
// this is what proves the channel is still closed exactly once.
func TestShutdownIsIdempotent(t *testing.T) {
	dial := socketPair(t)
	c := &Client{conn: dial(), send: make(chan []byte, 1)}

	c.shutdown()
	c.shutdown()

	if got := c.trySend([]byte("x")); got != sendClosed {
		t.Fatalf("trySend after shutdown = %v, want sendClosed", got)
	}
}

func TestTrySendReportsFullBuffer(t *testing.T) {
	dial := socketPair(t)
	c := &Client{conn: dial(), send: make(chan []byte, 1)}

	if got := c.trySend([]byte("first")); got != sendOK {
		t.Fatalf("first trySend = %v, want sendOK", got)
	}
	if got := c.trySend([]byte("second")); got != sendFull {
		t.Fatalf("second trySend = %v, want sendFull", got)
	}
}

// The bug this guards against: Broadcast took a snapshot of the clients, then
// sent on each one's channel. A client disconnecting in that window closed the
// channel underneath the send, and sending on a closed channel panics, which
// takes the whole process down rather than one connection.
//
// Broadcast is called from the whatsmeow event goroutines, so the window opens
// on every inbound message. Without the fix this test panics; with it, the
// disconnected clients report sendClosed and are skipped.
func TestBroadcastDoesNotPanicWhenClientsDisconnectConcurrently(t *testing.T) {
	h := quietHub()
	dial := socketPair(t)
	ws := uuid.New()

	const clients = 40
	all := make([]*Client, 0, clients)
	for range clients {
		all = append(all, detachedClient(h, ws, dial()))
	}

	var wg sync.WaitGroup

	// Half the goroutines broadcast while the other half disconnect.
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 50 {
				h.Broadcast(ws, EventMessageNew, map[string]any{"id": "x"})
			}
		}()
	}
	for _, c := range all {
		wg.Add(1)
		go func(c *Client) {
			defer wg.Done()
			h.unregister(c)
		}(c)
	}

	wg.Wait()

	h.mu.RLock()
	left := len(h.clients[ws])
	h.mu.RUnlock()
	if left != 0 {
		t.Fatalf("clients left registered after unregister: %d", left)
	}
}

// Close during traffic is the same race by a different door: shutdown runs
// from the shutdown path while events are still being fanned out.
func TestCloseDuringBroadcastDoesNotPanic(t *testing.T) {
	h := quietHub()
	dial := socketPair(t)
	ws := uuid.New()
	for range 20 {
		detachedClient(h, ws, dial())
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for range 200 {
			h.Broadcast(ws, EventMetricsUpdated, map[string]any{"conversation_id": "x"})
		}
	}()
	go func() {
		defer wg.Done()
		h.Close()
	}()
	wg.Wait()

	// A hub that has been closed refuses to hand out new registrations rather
	// than accepting a client nothing will ever read from.
	h.mu.RLock()
	closed := h.closed
	count := len(h.clients)
	h.mu.RUnlock()
	if !closed {
		t.Fatal("hub not marked closed")
	}
	if count != 0 {
		t.Fatalf("clients map not cleared: %d workspaces left", count)
	}
}

// Events for one workspace must never reach another. This is the tenant
// boundary, and it is cheap enough to assert that there is no reason not to.
func TestBroadcastIsScopedToOneWorkspace(t *testing.T) {
	h := quietHub()
	dial := socketPair(t)
	mine, theirs := uuid.New(), uuid.New()
	a := detachedClient(h, mine, dial())
	b := detachedClient(h, theirs, dial())

	h.Broadcast(mine, EventAccountStatus, map[string]any{"status": "connected"})

	if len(a.send) != 1 {
		t.Fatalf("own workspace received %d events, want 1", len(a.send))
	}
	if len(b.send) != 0 {
		t.Fatalf("other workspace received %d events, want 0", len(b.send))
	}
}
