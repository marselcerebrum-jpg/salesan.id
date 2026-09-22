// Package realtime broadcasts server events to connected browsers.
//
// The frontend keeps one WebSocket per session. Events are fanned out per
// workspace, so a client only ever sees its own tenant's traffic. Supabase
// Realtime is enabled on the same tables (see migration 0001) as an alternative
// transport, but QR codes never touch the database and so must travel here.
//
// The socket also carries one thing the other way: which conversation a browser
// has open. That is presence, not data, so it lives in memory here and nowhere
// else; a restart forgets it and the browsers announce it again on reconnect.
package realtime

import (
	"encoding/json"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

// Event types pushed to the browser.
const (
	EventAccountStatus      = "account.status"
	EventAccountQR          = "account.qr"
	EventAccountDeleted     = "account.deleted"
	EventMessageNew         = "message.new"
	EventMessageStatus      = "message.status"
	EventConversationUpdate = "conversation.updated"
	// EventConversationDeleted fires when a thread is removed or emptied from
	// this inbox, so other open tabs drop it too instead of showing a chat
	// that no longer exists.
	EventConversationDeleted = "conversation.deleted"
	// EventMessageHidden fires when a message is deleted for this side only,
	// so other open tabs drop it rather than keep showing it.
	EventMessageHidden = "message.hidden"
	EventSyncProgress  = "sync.progress"
	// EventMetricsUpdated fires when a conversation's derived reporting rows
	// were rebuilt, so an open Dashboard or Performa page refreshes without a
	// reload. It carries only the conversation id: the figures themselves are
	// computed server-side, and pushing partial numbers would invite the
	// browser to start adding them up.
	EventMetricsUpdated = "metrics.updated"
	// EventCampaignUpdated fires when a Story or Broadcast is created,
	// rescheduled, cancelled or run.
	EventCampaignUpdated = "campaign.updated"
	// EventScheduleUpdated fires when a work schedule changes, because every
	// per-hour figure on the Performa page is divided by it.
	EventScheduleUpdated = "schedule.updated"
	// EventLabelsUpdated carries an account's whole label set after any change,
	// so the browser never has to reconcile a partial diff.
	EventLabelsUpdated = "labels.updated"
	// EventLabelSyncState drives the Menyinkronkan / Tersinkron / Gagal badge.
	EventLabelSyncState = "labels.sync_state"
	// EventPresenceViewers carries who has one conversation open right now. It
	// fires for a conversation whenever somebody opens or leaves it, and once
	// per open conversation to a browser that has just connected.
	EventPresenceViewers = "presence.viewers"
)

// Inbound frame types, browser -> server.
const (
	// frameView announces the conversation this browser is looking at. A null
	// or empty conversation id means "none".
	frameView = "presence.view"
)

// Event is the envelope every push shares.
type Event struct {
	Type      string    `json:"type"`
	Payload   any       `json:"payload"`
	Timestamp time.Time `json:"timestamp"`
}

// Viewer is one person with a conversation open. Two tabs of the same person
// collapse into one entry: the question is who, not how many windows.
type Viewer struct {
	UserID uuid.UUID `json:"user_id"`
	Name   string    `json:"name"`
}

// ViewersPayload is what EventPresenceViewers carries.
type ViewersPayload struct {
	ConversationID uuid.UUID `json:"conversation_id"`
	Viewers        []Viewer  `json:"viewers"`
}

const (
	writeWait      = 10 * time.Second
	pongWait       = 60 * time.Second
	pingPeriod     = (pongWait * 9) / 10
	maxMessageSize = 4096
	sendBuffer     = 64
)

// Client is one browser connection.
//
// `send` is guarded rather than closed wherever convenient, and the reason is
// the only way this type can crash the process: sending on a closed channel
// panics, and a panic in one of these goroutines is not recoverable from
// anywhere else. The previous shape closed the channel from `unregister` while
// `Broadcast` was selecting on it, which is a live race every time a browser
// disconnects during traffic. So both operations take `mu`, and `closed` is
// the single fact they agree on.
type Client struct {
	hub         *Hub
	conn        *websocket.Conn
	workspaceID uuid.UUID
	userID      uuid.UUID
	name        string
	// viewing is the conversation this browser has open; zero when none. Guarded
	// by hub.mu, because it is read while building the viewer list for others.
	viewing uuid.UUID

	mu     sync.Mutex
	send   chan []byte
	closed bool
}

// What happened to one queued frame.
type sendResult int

const (
	sendOK sendResult = iota
	sendClosed
	sendFull
)

// trySend queues one frame without blocking.
//
// Taking the mutex around the channel send is what makes this safe: shutdown
// cannot close the channel between the `closed` check and the send, because
// shutdown needs the same lock to do it.
func (c *Client) trySend(raw []byte) sendResult {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return sendClosed
	}
	select {
	case c.send <- raw:
		return sendOK
	default:
		return sendFull
	}
}

// shutdown closes the connection and the send channel, exactly once.
func (c *Client) shutdown() {
	c.mu.Lock()
	first := !c.closed
	if first {
		c.closed = true
		close(c.send)
	}
	c.mu.Unlock()

	if first {
		_ = c.conn.Close()
	}
}

// Hub fans events out to clients, grouped by workspace.
type Hub struct {
	log *slog.Logger

	mu      sync.RWMutex
	clients map[uuid.UUID]map[*Client]struct{}
	// viewing indexes clients by the conversation they have open:
	// workspace -> conversation -> clients. Kept alongside `clients` under the
	// same lock so the two can never disagree about a connection.
	viewing map[uuid.UUID]map[uuid.UUID]map[*Client]struct{}
	closed  bool
}

// NewHub creates an empty hub.
func NewHub(log *slog.Logger) *Hub {
	return &Hub{
		log:     log,
		clients: map[uuid.UUID]map[*Client]struct{}{},
		viewing: map[uuid.UUID]map[uuid.UUID]map[*Client]struct{}{},
	}
}

// Register attaches a websocket connection to a workspace and starts its pumps.
//
// userID and name identify the person for presence; name is what the other
// browsers will show, so the caller passes whatever the sidebar shows for them.
func (h *Hub) Register(conn *websocket.Conn, workspaceID, userID uuid.UUID, name string) *Client {
	c := &Client{
		hub:         h,
		conn:        conn,
		workspaceID: workspaceID,
		userID:      userID,
		name:        name,
		send:        make(chan []byte, sendBuffer),
	}

	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		_ = conn.Close()
		return c
	}
	if h.clients[workspaceID] == nil {
		h.clients[workspaceID] = map[*Client]struct{}{}
	}
	h.clients[workspaceID][c] = struct{}{}
	count := len(h.clients[workspaceID])
	h.mu.Unlock()

	h.log.Debug("realtime client connected", "workspace_id", workspaceID, "clients", count)

	go c.writePump()
	go c.readPump()

	// A browser that has just connected has missed every earlier presence
	// change, so it gets the current picture straight away. Only conversations
	// somebody actually has open are sent, which is a handful at most.
	for _, p := range h.openConversations(workspaceID) {
		if raw, err := marshalEvent(EventPresenceViewers, p); err == nil {
			_ = c.trySend(raw)
		}
	}
	return c
}

func (h *Hub) unregister(c *Client) {
	h.mu.Lock()
	if set, ok := h.clients[c.workspaceID]; ok {
		if _, present := set[c]; present {
			delete(set, c)
			if len(set) == 0 {
				delete(h.clients, c.workspaceID)
			}
		}
	}
	prev := c.viewing
	h.detachViewingLocked(c)
	h.mu.Unlock()

	c.shutdown()

	if prev != uuid.Nil {
		h.broadcastViewers(c.workspaceID, prev)
	}
}

// detachViewingLocked removes the client from the conversation it was viewing.
// Caller holds h.mu.
func (h *Hub) detachViewingLocked(c *Client) {
	if c.viewing == uuid.Nil {
		return
	}
	if byConv, ok := h.viewing[c.workspaceID]; ok {
		if set, ok := byConv[c.viewing]; ok {
			delete(set, c)
			if len(set) == 0 {
				delete(byConv, c.viewing)
			}
		}
		if len(byConv) == 0 {
			delete(h.viewing, c.workspaceID)
		}
	}
	c.viewing = uuid.Nil
}

// setViewing records which conversation a browser has open and tells the rest
// of the workspace about both the one it left and the one it entered.
func (h *Hub) setViewing(c *Client, conv uuid.UUID) {
	h.mu.Lock()
	if h.closed || c.viewing == conv {
		h.mu.Unlock()
		return
	}
	// A client that is no longer registered (dropped as too slow, say) must
	// not be re-added through this side door.
	if _, registered := h.clients[c.workspaceID][c]; !registered {
		h.mu.Unlock()
		return
	}
	prev := c.viewing
	h.detachViewingLocked(c)
	if conv != uuid.Nil {
		if h.viewing[c.workspaceID] == nil {
			h.viewing[c.workspaceID] = map[uuid.UUID]map[*Client]struct{}{}
		}
		if h.viewing[c.workspaceID][conv] == nil {
			h.viewing[c.workspaceID][conv] = map[*Client]struct{}{}
		}
		h.viewing[c.workspaceID][conv][c] = struct{}{}
		c.viewing = conv
	}
	h.mu.Unlock()

	if prev != uuid.Nil {
		h.broadcastViewers(c.workspaceID, prev)
	}
	if conv != uuid.Nil {
		h.broadcastViewers(c.workspaceID, conv)
	}
}

// viewersOf lists who has a conversation open, one entry per person.
func (h *Hub) viewersOf(workspaceID, conv uuid.UUID) []Viewer {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.viewersOfLocked(workspaceID, conv)
}

func (h *Hub) viewersOfLocked(workspaceID, conv uuid.UUID) []Viewer {
	seen := map[uuid.UUID]bool{}
	out := []Viewer{}
	for c := range h.viewing[workspaceID][conv] {
		if seen[c.userID] {
			continue
		}
		seen[c.userID] = true
		out = append(out, Viewer{UserID: c.userID, Name: c.name})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// openConversations is the presence picture for one workspace: every
// conversation somebody has open, with who.
func (h *Hub) openConversations(workspaceID uuid.UUID) []ViewersPayload {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := []ViewersPayload{}
	for conv := range h.viewing[workspaceID] {
		out = append(out, ViewersPayload{ConversationID: conv, Viewers: h.viewersOfLocked(workspaceID, conv)})
	}
	return out
}

func (h *Hub) broadcastViewers(workspaceID, conv uuid.UUID) {
	h.Broadcast(workspaceID, EventPresenceViewers, ViewersPayload{
		ConversationID: conv,
		Viewers:        h.viewersOf(workspaceID, conv),
	})
}

func marshalEvent(eventType string, payload any) ([]byte, error) {
	return json.Marshal(Event{Type: eventType, Payload: payload, Timestamp: time.Now().UTC()})
}

// Broadcast delivers an event to every client in a workspace. Slow clients are
// dropped rather than allowed to block the WhatsApp event pipeline.
func (h *Hub) Broadcast(workspaceID uuid.UUID, eventType string, payload any) {
	raw, err := marshalEvent(eventType, payload)
	if err != nil {
		h.log.Error("realtime marshal failed", "type", eventType, "err", err)
		return
	}

	h.mu.RLock()
	targets := make([]*Client, 0, len(h.clients[workspaceID]))
	for c := range h.clients[workspaceID] {
		targets = append(targets, c)
	}
	h.mu.RUnlock()

	for _, c := range targets {
		switch c.trySend(raw) {
		case sendOK:
		case sendFull:
			// The buffer is full, so this browser is not keeping up. Dropping
			// it is deliberate: the alternative is blocking the WhatsApp event
			// pipeline behind one slow tab.
			h.log.Warn("realtime client too slow, dropping", "workspace_id", workspaceID)
			h.unregister(c)
		case sendClosed:
			// It disconnected between the snapshot above and here. Nothing to
			// do, and nothing to report: this is the ordinary case.
		}
	}
}

// Close tears down every connection; called during graceful shutdown.
func (h *Hub) Close() {
	h.mu.Lock()
	h.closed = true
	all := make([]*Client, 0)
	for _, set := range h.clients {
		for c := range set {
			all = append(all, c)
		}
	}
	h.clients = map[uuid.UUID]map[*Client]struct{}{}
	h.viewing = map[uuid.UUID]map[uuid.UUID]map[*Client]struct{}{}
	h.mu.Unlock()

	for _, c := range all {
		c.shutdown()
	}
}

// inboundFrame is the only shape a browser is allowed to send.
type inboundFrame struct {
	Type           string  `json:"type"`
	ConversationID *string `json:"conversation_id"`
}

// readPump processes the few frames a browser may send (presence) and, as
// before, is what notices pongs and a closed connection.
func (c *Client) readPump() {
	defer c.hub.unregister(c)

	c.conn.SetReadLimit(maxMessageSize)
	_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(pongWait))
	})

	for {
		_, raw, err := c.conn.ReadMessage()
		if err != nil {
			return
		}
		var f inboundFrame
		if json.Unmarshal(raw, &f) != nil {
			// Not ours to interpret. Ignoring it keeps a confused client
			// connected rather than punishing it for one bad frame.
			continue
		}
		if f.Type != frameView {
			continue
		}
		conv := uuid.Nil
		if f.ConversationID != nil && *f.ConversationID != "" {
			parsed, err := uuid.Parse(*f.ConversationID)
			if err != nil {
				continue
			}
			conv = parsed
		}
		c.hub.setViewing(c, conv)
	}
}

func (c *Client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		_ = c.conn.Close()
	}()

	for {
		select {
		case raw, ok := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				_ = c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, raw); err != nil {
				return
			}
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
