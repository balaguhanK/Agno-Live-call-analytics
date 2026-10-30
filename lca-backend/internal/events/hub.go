// Package events fans out live call updates to WebSocket clients. Events go
// through Redis pub/sub so several backend instances share one stream; without
// Redis they are broadcast in-process to this instance's clients only.
package events

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"

	"github.com/redis/go-redis/v9"

	"lca-backend/internal/store"
)

const channel = "lca:call-events"

type Event struct {
	Type    string         `json:"type"` // "call", "segment" or "assist"
	CallID  string         `json:"callId"`
	Agent   string         `json:"agent"`
	Call    *store.Call    `json:"call,omitempty"`
	Segment *store.Segment `json:"segment,omitempty"`
	Assist  *Suggestion    `json:"assist,omitempty"`
}

// Suggestion is advice pushed to the agent's screen during a call, found in
// the knowledge base from what the customer just said.
type Suggestion struct {
	Trigger string `json:"trigger"` // the customer's words that prompted it
	Answer  string `json:"answer"`  // say and steps as plain text
	// What the agent can say to the customer, and the steps to take
	Say     string   `json:"say,omitempty"`
	Steps   []string `json:"steps,omitempty"`
	Model   string   `json:"model,omitempty"` // empty when a passage is quoted as is
	Sources []Source `json:"sources"`
	// The transcript line showing it; a later event with the same ID replaces it
	SegmentID int `json:"segmentId,omitempty"`
	// The model is still writing the reply; a final event follows
	Pending bool `json:"pending,omitempty"`
}

type Source struct {
	Title   string `json:"title"`
	Section string `json:"section,omitempty"`
	File    string `json:"file,omitempty"`
	Excerpt string `json:"excerpt,omitempty"` // the passage the advice is based on
}

// Client receives events. An empty Agent receives every call's events (admins).
type Client struct {
	Agent string
	Send  chan []byte
}

type Hub struct {
	rdb     *redis.Client // nil means in-process only
	mu      sync.Mutex
	clients map[*Client]struct{}
}

func NewHub(rdb *redis.Client) *Hub {
	return &Hub{rdb: rdb, clients: map[*Client]struct{}{}}
}

func (h *Hub) Publish(ctx context.Context, e Event) error {
	data, err := json.Marshal(e)
	if err != nil {
		return err
	}
	if h.rdb == nil {
		h.broadcast(e.Agent, data)
		return nil
	}
	return h.rdb.Publish(ctx, channel, data).Err()
}

func (h *Hub) Register(agent string) *Client {
	c := &Client{Agent: agent, Send: make(chan []byte, 64)}
	h.mu.Lock()
	h.clients[c] = struct{}{}
	h.mu.Unlock()
	return c
}

func (h *Hub) Unregister(c *Client) {
	h.mu.Lock()
	if _, ok := h.clients[c]; ok {
		delete(h.clients, c)
		close(c.Send)
	}
	h.mu.Unlock()
}

// Run relays Redis messages to registered clients until ctx is cancelled.
func (h *Hub) Run(ctx context.Context) {
	if h.rdb == nil {
		return
	}
	sub := h.rdb.Subscribe(ctx, channel)
	defer sub.Close()
	for msg := range sub.Channel() {
		var e Event
		if err := json.Unmarshal([]byte(msg.Payload), &e); err != nil {
			slog.Warn("dropping malformed event", "err", err)
			continue
		}
		h.broadcast(e.Agent, []byte(msg.Payload))
	}
}

func (h *Hub) broadcast(agent string, data []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients {
		if c.Agent != "" && c.Agent != agent {
			continue
		}
		select {
		case c.Send <- data:
		default:
			// Client can't keep up; drop it and let the browser reconnect
			delete(h.clients, c)
			close(c.Send)
		}
	}
}
