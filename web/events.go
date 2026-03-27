package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
)

// EventHub manages SSE subscribers and broadcasts events.
type EventHub struct {
	mu          sync.RWMutex
	subscribers map[*subscriber]struct{}
}

type subscriber struct {
	ch   chan []byte // serialized SSE data lines
	done chan struct{}
}

// NewEventHub creates a new event hub.
func NewEventHub() *EventHub {
	return &EventHub{
		subscribers: make(map[*subscriber]struct{}),
	}
}

// Subscribe creates a new subscriber.
func (h *EventHub) Subscribe() *subscriber {
	sub := &subscriber{
		ch:   make(chan []byte, 1024),
		done: make(chan struct{}),
	}
	h.mu.Lock()
	h.subscribers[sub] = struct{}{}
	h.mu.Unlock()
	return sub
}

// Unsubscribe removes a subscriber.
func (h *EventHub) Unsubscribe(sub *subscriber) {
	h.mu.Lock()
	delete(h.subscribers, sub)
	h.mu.Unlock()
	close(sub.done)
}

// Broadcast sends a chatEvent to all subscribers.
func (h *EventHub) Broadcast(evt chatEvent) {
	// Fast path for high-frequency events
	if evt.Type == "chunk" || evt.Type == "log" || evt.Type == "cron" {
		contentBytes, err := json.Marshal(evt.Content)
		if err == nil {
			var data []byte
			if evt.ChatID > 0 {
				data = append(data, `{"type":"`...)
				data = append(data, evt.Type...)
				data = append(data, `","content":`...)
				data = append(data, contentBytes...)
				data = append(data, `,"chat_id":`...)
				data = fmt.Appendf(data, "%d}", evt.ChatID)
			} else {
				data = append(data, `{"type":"`...)
				data = append(data, evt.Type...)
				data = append(data, `","content":`...)
				data = append(data, contentBytes...)
				data = append(data, `}`...)
			}
			h.BroadcastRaw(data)
			return
		}
	}

	data, err := json.Marshal(evt)
	if err != nil {
		return
	}
	h.BroadcastRaw(data)
}

// BroadcastRaw sends pre-serialized JSON data to all subscribers.
func (h *EventHub) BroadcastRaw(data []byte) {
	line := make([]byte, 0, len(data)+8)
	line = append(line, "data: "...)
	line = append(line, data...)
	line = append(line, "\n\n"...)

	h.mu.RLock()
	defer h.mu.RUnlock()
	for sub := range h.subscribers {
		select {
		case sub.ch <- line:
		default:
			// slow subscriber, drop event
		}
	}
}

// HasListeners reports whether any SSE clients are currently connected.
func (h *EventHub) HasListeners() bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.subscribers) > 0
}

// Events is the single SSE endpoint. All events flow through the hub.
func (a *Api) Events(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}

	hub := a.server.hub
	sub := hub.Subscribe()
	defer hub.Unsubscribe(sub)

	// Send initial connected event
	w.Write([]byte("data: {\"type\":\"connected\"}\n\n"))
	flusher.Flush()

	// Send initial tunnel status
	payload := a.server.tunnel.statusPayload(a.server.store.Workspace())
	payload["type"] = "tunnel_status"
	tsData, _ := json.Marshal(payload)
	fmt.Fprintf(w, "data: %s\n\n", tsData)
	flusher.Flush()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-sub.done:
			return
		case data := <-sub.ch:
			w.Write(data)
			flusher.Flush()
		}
	}
}
