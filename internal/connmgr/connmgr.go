// Package connmgr provides a persistent connection manager for the AI agent.
// Manages background WebSocket (and future TCP, MQTT, etc.) connections with
// warm VM instances that receive lifecycle callbacks. Persistence is handled
// by dsorm via config.Store.
package connmgr

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"altclaw.ai/internal/config"
	"altclaw.ai/internal/engine"
	"altclaw.ai/internal/netx"
	"github.com/coder/websocket"
)

// Protocol is the interface each connection type must implement.
// WebSocket is the first; TCP, MQTT, etc. can be added later.
type Protocol interface {
	Dial(ctx context.Context, url string, headers http.Header) error
	Read(ctx context.Context) ([]byte, error)
	Write(ctx context.Context, data []byte) error
	Close() error
}

// EngineFactory creates a warm Engine for a connection handler script.
// sendFn allows the handler to send data back on the connection.
// closeFn allows the handler to request a connection close.
type EngineFactory func(handler string, sendFn func(string) error, closeFn func()) *engine.Engine

// ConnInfo is the view returned by List().
type ConnInfo struct {
	ID          int64  `json:"id"`
	ChatID      int64  `json:"chat_id,omitempty"`
	Type        string `json:"type"`
	URL         string `json:"url"`
	Handler     string `json:"handler"`
	Status      string `json:"status"`
	ConnectedAt string `json:"connected_at,omitempty"`
	MessagesIn  int64  `json:"messages_in"`
	LastMessage string `json:"last_message,omitempty"`
	Errors      int    `json:"errors"`
	Reconnects  int    `json:"reconnects"`
	CreatedAt   string `json:"created_at"`
}

// Manager handles persistent connections, warm VMs, and reconnection logic.
type Manager struct {
	mu          sync.RWMutex
	store       *config.Store
	workspaceID string
	entries     map[int64]*connEntry
	engineFn    EngineFactory
	stopCh      chan struct{}
	stopOnce    sync.Once
}

type connEntry struct {
	config.ConnEntry
	proto      Protocol
	eng        *engine.Engine
	cancel     context.CancelFunc
	lastActive time.Time
	mu         sync.Mutex // protects eng access during recycle
	userClosed bool       // set when JS handler calls conn.close()

	// stats
	status      string
	connectedAt time.Time
	messagesIn  atomic.Int64
	errors      int
	reconnects  int
}

// New creates a new Manager, loading existing connections from dsorm and reconnecting them.
func New(store *config.Store, workspaceID string, engineFn EngineFactory) (*Manager, error) {
	m := &Manager{
		store:       store,
		workspaceID: workspaceID,
		entries:     make(map[int64]*connEntry),
		engineFn:    engineFn,
		stopCh:      make(chan struct{}),
	}

	ctx := context.Background()
	conns, err := store.ListConnEntries(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("connmgr: load: %w", err)
	}

	for _, c := range conns {
		entry := &connEntry{ConnEntry: *c, status: "connecting"}
		m.entries[c.ID] = entry
		go m.runConn(entry)
	}

	// Start idle VM reaper
	go m.reapLoop()

	return m, nil
}

// Add creates a new persistent connection.
func (m *Manager) Add(ctx context.Context, chatID int64, connType, url, handler string, headers map[string]string, reconnect bool) (int64, error) {
	// Detect type from URL scheme if not specified
	connType = detectType(url, connType)

	// Validate that the protocol is supported
	if _, err := newProtocol(connType); err != nil {
		return 0, err
	}

	// SSRF protection
	if err := netx.ValidateURL(url); err != nil {
		return 0, fmt.Errorf("connmgr: %w", err)
	}

	headersJSON := ""
	if len(headers) > 0 {
		b, _ := json.Marshal(headers)
		headersJSON = string(b)
	}

	// Dedup: if an existing connection matches url+type+handler+headers, return it
	m.mu.RLock()
	for _, existing := range m.entries {
		if existing.URL == url && existing.Type == connType && existing.Handler == handler && existing.Headers == headersJSON {
			id := existing.ID
			m.mu.RUnlock()
			return id, nil
		}
	}
	m.mu.RUnlock()

	entry := &config.ConnEntry{
		Workspace: m.workspaceID,
		ChatID:    chatID,
		Type:      connType,
		URL:       url,
		Handler:   handler,
		Headers:   headersJSON,
		Reconnect: reconnect,
	}

	if err := m.store.SaveConnEntry(ctx, entry); err != nil {
		return 0, fmt.Errorf("connmgr: save: %w", err)
	}

	ce := &connEntry{ConnEntry: *entry, status: "connecting"}

	m.mu.Lock()
	m.entries[entry.ID] = ce
	m.mu.Unlock()

	go m.runConn(ce)

	return entry.ID, nil
}

// cleanupEntry removes a finished connection from the map and store.
// Called when a connection permanently stops (user-close or non-retryable error).
func (m *Manager) cleanupEntry(entry *connEntry) {
	m.mu.Lock()
	delete(m.entries, entry.ID)
	m.mu.Unlock()

	entry.mu.Lock()
	if entry.eng != nil {
		entry.eng.Cleanup()
		entry.eng = nil
	}
	entry.mu.Unlock()

	if err := m.store.DeleteConnEntry(context.Background(), &entry.ConnEntry); err != nil {
		slog.Error("connmgr: cleanup delete failed", "id", entry.ID, "err", err)
	}
	slog.Info("connmgr: connection removed", "id", entry.ID, "url", entry.URL, "status", entry.status)
}

// Remove closes and removes a connection.
func (m *Manager) Remove(ctx context.Context, id int64) error {
	m.mu.Lock()
	entry, ok := m.entries[id]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("connmgr: connection %d not found", id)
	}
	delete(m.entries, id)
	m.mu.Unlock()

	// Cancel the connection goroutine
	if entry.cancel != nil {
		entry.cancel()
	}

	// Close the protocol connection
	entry.mu.Lock()
	if entry.proto != nil {
		entry.proto.Close()
	}
	if entry.eng != nil {
		entry.eng.Cleanup()
		entry.eng = nil
	}
	entry.mu.Unlock()

	return m.store.DeleteConnEntry(ctx, &entry.ConnEntry)
}

// Send sends data on an existing connection.
func (m *Manager) Send(id int64, data string) error {
	m.mu.RLock()
	entry, ok := m.entries[id]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("connmgr: connection %d not found", id)
	}

	entry.mu.Lock()
	proto := entry.proto
	entry.mu.Unlock()

	if proto == nil {
		return fmt.Errorf("connmgr: connection %d not connected", id)
	}

	return proto.Write(context.Background(), []byte(data))
}

// List returns all active connections with stats.
func (m *Manager) List() any {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]ConnInfo, 0, len(m.entries))
	for _, e := range m.entries {
		info := ConnInfo{
			ID:         e.ID,
			ChatID:     e.ChatID,
			Type:       e.Type,
			URL:        e.URL,
			Handler:    e.Handler,
			Status:     e.status,
			MessagesIn: e.messagesIn.Load(),
			Errors:     e.errors,
			Reconnects: e.reconnects,
			CreatedAt:  e.CreatedAt.Format(time.RFC3339),
		}
		if !e.connectedAt.IsZero() {
			info.ConnectedAt = e.connectedAt.Format(time.RFC3339)
		}
		if e.lastActive.After(e.connectedAt) {
			info.LastMessage = e.lastActive.Format(time.RFC3339)
		}
		result = append(result, info)
	}
	return result
}

// Stop gracefully stops all connections and cleans up VMs.
func (m *Manager) Stop() {
	m.stopOnce.Do(func() {
		close(m.stopCh)

		m.mu.Lock()
		for _, entry := range m.entries {
			if entry.cancel != nil {
				entry.cancel()
			}
			entry.mu.Lock()
			if entry.proto != nil {
				entry.proto.Close()
			}
			if entry.eng != nil {
				entry.eng.Cleanup()
				entry.eng = nil
			}
			entry.mu.Unlock()
		}
		m.mu.Unlock()
	})
}

// runConn is the main goroutine per connection. Handles connect, read loop, and reconnection.
func (m *Manager) runConn(entry *connEntry) {
	ctx, cancel := context.WithCancel(context.Background())
	entry.cancel = cancel
	defer cancel()

	backoff := time.Second
	maxBackoff := time.Minute

	for {
		select {
		case <-m.stopCh:
			return
		case <-ctx.Done():
			return
		default:
		}

		// Create protocol driver
		proto, err := newProtocol(entry.Type)
		if err != nil {
			slog.Error("connmgr: unsupported protocol", "type", entry.Type, "err", err)
			entry.status = "error"
			return
		}

		// Parse headers
		headers := make(http.Header)
		if entry.Headers != "" {
			var h map[string]string
			if json.Unmarshal([]byte(entry.Headers), &h) == nil {
				for k, v := range h {
					headers.Set(k, v)
				}
			}
		}

		// Connect
		entry.status = "connecting"
		dialCtx, dialCancel := context.WithTimeout(ctx, 30*time.Second)
		err = proto.Dial(dialCtx, entry.URL, headers)
		dialCancel()

		if err != nil {
			slog.Error("connmgr: dial failed", "url", entry.URL, "err", err)
			entry.errors++

			// Determine retry: handler override > isRetryableError > reconnect flag
			shoudRetry := entry.Reconnect && isRetryableError(err)
			if override := m.dispatchError(entry, err.Error()); override != nil {
				shoudRetry = *override
			}

			if !shoudRetry {
				entry.status = "error"
				m.cleanupEntry(entry)
				return
			}

			entry.status = "reconnecting"
			select {
			case <-time.After(backoff):
				backoff = min(backoff*2, maxBackoff)
				continue
			case <-m.stopCh:
				return
			case <-ctx.Done():
				return
			}
		}

		// Connected successfully
		entry.mu.Lock()
		entry.proto = proto
		entry.mu.Unlock()
		entry.status = "connected"
		entry.connectedAt = time.Now()
		connectedAt := time.Now()
		slog.Info("connmgr: connected", "url", entry.URL)

		// Dispatch onConnect
		m.ensureWarmVM(entry)
		m.dispatchConnect(entry)

		// Read loop
		readErr := m.readLoop(ctx, entry)

		// Connection lost
		entry.mu.Lock()
		entry.proto = nil
		proto.Close()
		entry.mu.Unlock()

		// Only reset backoff if connection was stable (30s+). Short-lived
		// connections keep growing backoff to avoid hammering the server.
		if time.Since(connectedAt) > 30*time.Second {
			backoff = time.Second
		}

		if readErr != nil {
			slog.Error("connmgr: connection lost", "url", entry.URL, "err", readErr, "uptime", time.Since(connectedAt).Round(time.Millisecond))
			m.dispatchClose(entry, readErr.Error())
		}

		shouldRetry := entry.Reconnect && (readErr == nil || isRetryableError(readErr))
		// User-initiated close (conn.close() from JS) — never reconnect
		entry.mu.Lock()
		if entry.userClosed {
			shouldRetry = false
		}
		entry.mu.Unlock()
		if readErr != nil {
			if override := m.dispatchError(entry, readErr.Error()); override != nil {
				shouldRetry = *override
			}
		}

		if !shouldRetry {
			entry.status = "closed"
			m.cleanupEntry(entry)
			return
		}

		entry.status = "reconnecting"
		entry.reconnects++
		slog.Info("connmgr: reconnecting", "url", entry.URL, "attempt", entry.reconnects, "backoff", backoff)

		select {
		case <-time.After(backoff):
			backoff = min(backoff*2, maxBackoff)
		case <-m.stopCh:
			return
		case <-ctx.Done():
			return
		}
	}
}

// readLoop reads messages from the connection and dispatches them to the warm VM.
func (m *Manager) readLoop(ctx context.Context, entry *connEntry) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-m.stopCh:
			return nil
		default:
		}

		data, err := entry.proto.Read(ctx)
		if err != nil {
			return err
		}

		entry.messagesIn.Add(1)
		entry.lastActive = time.Now()

		m.ensureWarmVM(entry)
		m.dispatchMessage(entry, data)
	}
}

// ensureWarmVM creates or recreates the warm VM if it was recycled.
func (m *Manager) ensureWarmVM(entry *connEntry) {
	entry.mu.Lock()
	defer entry.mu.Unlock()

	if entry.eng != nil {
		return
	}

	// Create send function bound to this connection's protocol
	sendFn := func(data string) error {
		entry.mu.Lock()
		proto := entry.proto
		entry.mu.Unlock()
		if proto == nil {
			return fmt.Errorf("connection not active")
		}
		return proto.Write(context.Background(), []byte(data))
	}

	// Create close function — sets userClosed flag to suppress reconnect
	closeFn := func() {
		entry.mu.Lock()
		entry.userClosed = true
		proto := entry.proto
		entry.mu.Unlock()
		if proto != nil {
			proto.Close()
		}
	}

	eng := m.engineFn(entry.Handler, sendFn, closeFn)

	// Note: Engine.Run() wraps code in (function(){ ... })(), so all declarations
	// are IIFE-scoped. We MUST use "this." to assign to the global scope so
	// subsequent Run() calls (dispatch) can access these functions.
	initCode := fmt.Sprintf(`
this.__connMod = require(%q);
if (typeof this.__connMod === "function") { this.__connMod = { onMessage: this.__connMod }; }
this.__connObj = {
	id: %q,
	url: %q,
	get ready() { return typeof __connReady !== "undefined" && __connReady; },
	send: function(data) {
		if (typeof data === "object" && data !== null) data = JSON.stringify(data);
		__connSend(String(data));
	},
	close: function() { __connClose(); }
};
this.__connOnConnect = function() { if (__connMod.onConnect) __connMod.onConnect(__connObj); };
this.__connOnMessage = function(raw) {
	if (!__connMod.onMessage) return;
	var msg = raw;
	try { msg = JSON.parse(raw); } catch(e) {}
	__connMod.onMessage(__connObj, msg);
};
this.__connOnClose = function(reason) { if (__connMod.onClose) __connMod.onClose(__connObj, String(reason)); };
this.__connOnError = function(err) {
	if (__connMod.onError) { var r = __connMod.onError(__connObj, String(err)); if (typeof r === "boolean") return r; }
};
`, entry.Handler, fmt.Sprintf("%d", entry.ID), entry.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	result := eng.Run(ctx, initCode)
	cancel()

	if result.Error != nil {
		slog.Error("connmgr: handler init failed", "handler", entry.Handler, "err", result.Error)
		eng.Cleanup()
		return
	}

	entry.eng = eng
}

// dispatchConnect calls __connOnConnect() on the warm VM.
func (m *Manager) dispatchConnect(entry *connEntry) {
	m.dispatch(entry, "__connOnConnect()")
}

// dispatchMessage calls __connOnMessage(raw) on the warm VM.
func (m *Manager) dispatchMessage(entry *connEntry, data []byte) {
	// Escape the raw message for embedding in JS
	escaped, _ := json.Marshal(string(data))
	m.dispatch(entry, fmt.Sprintf("__connOnMessage(%s)", string(escaped)))
}

// dispatchClose calls __connOnClose(reason) on the warm VM.
func (m *Manager) dispatchClose(entry *connEntry, reason string) {
	escaped, _ := json.Marshal(reason)
	m.dispatch(entry, fmt.Sprintf("__connOnClose(%s)", string(escaped)))
}

// dispatchError calls __connOnError(err) on the warm VM.
// Returns a retry override: true=force retry, false=stop, nil=use default.
func (m *Manager) dispatchError(entry *connEntry, errMsg string) *bool {
	escaped, _ := json.Marshal(errMsg)
	result := m.dispatch(entry, fmt.Sprintf("return __connOnError(%s)", string(escaped)))
	if result == "true" {
		v := true
		return &v
	}
	if result == "false" {
		v := false
		return &v
	}
	return nil // no override, use default
}

// dispatch runs JS code on the warm VM with error recovery.
// Returns the stringified result value (empty if nil/undefined/error).
func (m *Manager) dispatch(entry *connEntry, code string) string {
	entry.mu.Lock()
	eng := entry.eng
	entry.mu.Unlock()

	if eng == nil {
		return ""
	}

	// Run with a per-message timeout (30s default)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	result := eng.Run(ctx, code)
	cancel()

	if result.Error != nil {
		entry.errors++
		slog.Error("connmgr: handler error", "conn_id", entry.ID, "err", result.Error)
		return ""
	}
	return result.Value
}

// reapLoop periodically recycles idle warm VMs.
func (m *Manager) reapLoop() {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-m.stopCh:
			return
		case <-ticker.C:
			m.reapIdleVMs()
		}
	}
}

// reapIdleVMs tears down warm VMs that have been idle for more than 10 minutes.
// The connection stays alive; the VM is recreated on the next message.
func (m *Manager) reapIdleVMs() {
	m.mu.RLock()
	defer m.mu.RUnlock()

	cutoff := time.Now().Add(-10 * time.Minute)
	for _, entry := range m.entries {
		entry.mu.Lock()
		if entry.eng != nil && entry.lastActive.Before(cutoff) {
			slog.Info("connmgr: recycling idle VM", "conn_id", entry.ID)
			entry.eng.Cleanup()
			entry.eng = nil
		}
		entry.mu.Unlock()
	}
}

// isRetryableError determines if a connection error is worth retrying.
// Permanent errors (4xx HTTP, bad auth, SSRF blocks) return false.
// Transient errors (5xx, timeouts, connection resets, EOF) return true.
func isRetryableError(err error) bool {
	if err == nil {
		return true
	}
	msg := err.Error()

	// HTTP 4xx errors are permanent (bad request, unauthorized, forbidden, not found)
	// Both WebSocket (via coder/websocket) and SSE surface these in error strings.
	for _, code := range []string{
		"HTTP 400", "HTTP 401", "HTTP 402", "HTTP 403", "HTTP 404",
		"HTTP 405", "HTTP 406", "HTTP 407", "HTTP 408", "HTTP 409",
		"HTTP 410", "HTTP 411", "HTTP 412", "HTTP 413", "HTTP 414",
		"HTTP 415", "HTTP 416", "HTTP 417", "HTTP 418", "HTTP 421",
		"HTTP 422", "HTTP 423", "HTTP 424", "HTTP 425", "HTTP 426",
		"HTTP 428", "HTTP 429", "HTTP 431", "HTTP 451",
		"status = 400", "status = 401", "status = 403", "status = 404",
	} {
		if strings.Contains(msg, code) {
			// Exception: 429 Too Many Requests is retryable
			if strings.Contains(msg, "429") {
				return true
			}
			return false
		}
	}

	// WebSocket close codes 4000-4999 are application-defined permanent errors
	if strings.Contains(msg, "status = 4") && strings.Contains(msg, "StatusCode") {
		return false
	}

	// SSRF / DNS errors are permanent (misconfigured URL)
	if strings.Contains(msg, "SSRF protection") ||
		strings.Contains(msg, "host not in allowed list") ||
		strings.Contains(msg, "no such host") {
		return false
	}

	// coder/websocket: "expected handshake response status code 101 but got XXX"
	// Extract the actual status code and classify it.
	if idx := strings.Index(msg, "status code 101 but got "); idx >= 0 {
		codeStr := msg[idx+len("status code 101 but got "):]
		// Trim any trailing text after the status code
		if spIdx := strings.IndexByte(codeStr, ' '); spIdx >= 0 {
			codeStr = codeStr[:spIdx]
		}
		// 4xx → permanent (except 429)
		if len(codeStr) == 3 && codeStr[0] == '4' {
			return codeStr == "429"
		}
		// 5xx → retryable
		return true
	}

	// Everything else (timeouts, connection reset, EOF, 5xx) is retryable
	return true
}

// detectType auto-detects the connection type from the URL scheme.
func detectType(url, explicit string) string {
	if explicit != "" {
		return explicit
	}
	switch {
	case strings.HasPrefix(url, "wss://"), strings.HasPrefix(url, "ws://"):
		return "ws"
	case strings.HasPrefix(url, "tcp://"):
		return "tcp"
	case strings.HasPrefix(url, "mqtt://"), strings.HasPrefix(url, "mqtts://"):
		return "mqtt"
	}
	return "ws" // default
}

// newProtocol creates a Protocol implementation for the given type.
func newProtocol(connType string) (Protocol, error) {
	switch connType {
	case "ws":
		return &wsProtocol{}, nil
	case "sse":
		return &sseProtocol{}, nil
	default:
		return nil, fmt.Errorf("unsupported connection type %q (supported: ws, sse)", connType)
	}
}

// wsProtocol implements Protocol for WebSocket connections.
type wsProtocol struct {
	conn *websocket.Conn
}

func (w *wsProtocol) Dial(ctx context.Context, url string, headers http.Header) error {
	opts := &websocket.DialOptions{}
	if len(headers) > 0 {
		opts.HTTPHeader = headers
	}
	conn, _, err := websocket.Dial(ctx, url, opts)
	if err != nil {
		return err
	}
	// Set a large read limit for market data messages
	conn.SetReadLimit(1 << 20) // 1MB
	w.conn = conn
	return nil
}

func (w *wsProtocol) Read(ctx context.Context) ([]byte, error) {
	_, data, err := w.conn.Read(ctx)
	return data, err
}

func (w *wsProtocol) Write(ctx context.Context, data []byte) error {
	return w.conn.Write(ctx, websocket.MessageText, data)
}

func (w *wsProtocol) Close() error {
	if w.conn == nil {
		return nil
	}
	return w.conn.Close(websocket.StatusNormalClosure, "")
}

// sseProtocol implements Protocol for Server-Sent Events (read-only).
// SSE connections use standard HTTP GET with Accept: text/event-stream.
// Write() returns an error since SSE is uni-directional (server→client).
type sseProtocol struct {
	resp    *http.Response
	scanner *bufio.Scanner
	cancel  context.CancelFunc
}

func (s *sseProtocol) Dial(ctx context.Context, url string, headers http.Header) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")
	for k, vals := range headers {
		for _, v := range vals {
			req.Header.Add(k, v)
		}
	}

	// Use a separate cancellable context for the long-lived response body
	bodyCtx, cancel := context.WithCancel(context.Background())
	req = req.WithContext(bodyCtx)
	s.cancel = cancel

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		return err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		cancel()
		return fmt.Errorf("SSE: HTTP %d %s", resp.StatusCode, resp.Status)
	}

	s.resp = resp
	s.scanner = bufio.NewScanner(resp.Body)
	// Allow up to 1MB per line
	s.scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	return nil
}

func (s *sseProtocol) Read(_ context.Context) ([]byte, error) {
	// SSE format: lines of "field: value\n", events separated by blank lines.
	// We accumulate "data:" fields and yield the combined payload on blank line.
	var data strings.Builder
	hasData := false

	for s.scanner.Scan() {
		line := s.scanner.Text()

		// Blank line = end of event — deliver accumulated data
		if line == "" {
			if hasData {
				result := data.String()
				// Trim trailing newline added by multi-line concatenation
				if len(result) > 0 && result[len(result)-1] == '\n' {
					result = result[:len(result)-1]
				}
				return []byte(result), nil
			}
			continue
		}

		// Parse field
		if strings.HasPrefix(line, "data:") {
			payload := strings.TrimPrefix(line, "data:")
			payload = strings.TrimPrefix(payload, " ") // optional space after colon
			if hasData {
				data.WriteByte('\n') // multi-line data fields
			}
			data.WriteString(payload)
			hasData = true
		}
		// Ignore "event:", "id:", "retry:", and comment lines (starting with ':')
	}

	if err := s.scanner.Err(); err != nil {
		return nil, err
	}
	return nil, io.EOF
}

func (s *sseProtocol) Write(_ context.Context, _ []byte) error {
	return fmt.Errorf("SSE connections are read-only; use conn.close() and conn.open() with type 'ws' for bidirectional communication")
}

func (s *sseProtocol) Close() error {
	if s.cancel != nil {
		s.cancel()
	}
	if s.resp != nil {
		return s.resp.Body.Close()
	}
	return nil
}
