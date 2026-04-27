package connmgr

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDetectType(t *testing.T) {
	tests := []struct {
		url      string
		explicit string
		want     string
	}{
		{"wss://ws.example.com/feed", "", "ws"},
		{"ws://localhost:8080/ws", "", "ws"},
		{"tcp://host:1234", "", "tcp"},
		{"mqtt://broker:1883", "", "mqtt"},
		{"mqtts://broker:8883", "", "mqtt"},
		{"https://api.example.com/stream", "", "ws"}, // default
		{"https://api.example.com/stream", "sse", "sse"},
		{"wss://ws.example.com", "ws", "ws"},
		{"https://any.url", "ws", "ws"}, // explicit wins
	}

	for _, tt := range tests {
		got := detectType(tt.url, tt.explicit)
		if got != tt.want {
			t.Errorf("detectType(%q, %q) = %q, want %q", tt.url, tt.explicit, got, tt.want)
		}
	}
}

func TestNewProtocol_Supported(t *testing.T) {
	for _, typ := range []string{"ws", "sse"} {
		p, err := newProtocol(typ)
		if err != nil {
			t.Errorf("newProtocol(%q) returned error: %v", typ, err)
		}
		if p == nil {
			t.Errorf("newProtocol(%q) returned nil", typ)
		}
	}
}

func TestNewProtocol_Unsupported(t *testing.T) {
	_, err := newProtocol("mqtt")
	if err == nil {
		t.Fatal("expected error for unsupported protocol")
	}
	if !strings.Contains(err.Error(), "unsupported") {
		t.Errorf("error should mention 'unsupported', got: %v", err)
	}
}

func TestSSEProtocol_Read(t *testing.T) {
	// Simulate an SSE stream
	stream := "data: hello\n\ndata: world\n\n"
	scanner := bufio.NewScanner(strings.NewReader(stream))
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)

	proto := &sseProtocol{scanner: scanner}

	// First event
	data, err := proto.Read(context.Background())
	if err != nil {
		t.Fatalf("first read: %v", err)
	}
	if string(data) != "hello" {
		t.Errorf("first event = %q, want %q", data, "hello")
	}

	// Second event
	data, err = proto.Read(context.Background())
	if err != nil {
		t.Fatalf("second read: %v", err)
	}
	if string(data) != "world" {
		t.Errorf("second event = %q, want %q", data, "world")
	}
}

func TestSSEProtocol_MultiLineData(t *testing.T) {
	stream := "data: line1\ndata: line2\ndata: line3\n\n"
	scanner := bufio.NewScanner(strings.NewReader(stream))
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)

	proto := &sseProtocol{scanner: scanner}

	data, err := proto.Read(context.Background())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != "line1\nline2\nline3" {
		t.Errorf("multi-line data = %q, want %q", data, "line1\nline2\nline3")
	}
}

func TestSSEProtocol_JSONData(t *testing.T) {
	stream := "data: {\"price\":42.5,\"symbol\":\"BTC\"}\n\n"
	scanner := bufio.NewScanner(strings.NewReader(stream))
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)

	proto := &sseProtocol{scanner: scanner}

	data, err := proto.Read(context.Background())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != `{"price":42.5,"symbol":"BTC"}` {
		t.Errorf("json data = %q", data)
	}
}

func TestSSEProtocol_IgnoresNonDataFields(t *testing.T) {
	stream := "event: update\nid: 42\nretry: 5000\n: this is a comment\ndata: payload\n\n"
	scanner := bufio.NewScanner(strings.NewReader(stream))
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)

	proto := &sseProtocol{scanner: scanner}

	data, err := proto.Read(context.Background())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != "payload" {
		t.Errorf("data = %q, want %q", data, "payload")
	}
}

func TestSSEProtocol_SkipsEmptyEvents(t *testing.T) {
	// Multiple blank lines between events should not yield empty results
	stream := "\n\n\ndata: actual\n\n"
	scanner := bufio.NewScanner(strings.NewReader(stream))
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)

	proto := &sseProtocol{scanner: scanner}

	data, err := proto.Read(context.Background())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != "actual" {
		t.Errorf("data = %q, want %q", data, "actual")
	}
}

func TestSSEProtocol_WriteReturnsError(t *testing.T) {
	proto := &sseProtocol{}
	err := proto.Write(context.Background(), []byte("test"))
	if err == nil {
		t.Fatal("expected error on SSE write")
	}
	if !strings.Contains(err.Error(), "read-only") {
		t.Errorf("error should mention 'read-only', got: %v", err)
	}
}

func TestSSEProtocol_DialAndRead(t *testing.T) {
	// Spin up a real SSE server for integration
	var mu sync.Mutex
	sent := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "no flusher", 500)
			return
		}
		for i := 0; i < 3; i++ {
			fmt.Fprintf(w, "data: msg-%d\n\n", i)
			flusher.Flush()
			mu.Lock()
			sent++
			mu.Unlock()
		}
	}))
	defer srv.Close()

	proto := &sseProtocol{}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := proto.Dial(ctx, srv.URL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer proto.Close()

	for i := 0; i < 3; i++ {
		data, err := proto.Read(ctx)
		if err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
		want := fmt.Sprintf("msg-%d", i)
		if string(data) != want {
			t.Errorf("read %d = %q, want %q", i, data, want)
		}
	}
}

func TestSSEProtocol_CustomHeaders(t *testing.T) {
	var receivedAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: ok\n\n")
	}))
	defer srv.Close()

	proto := &sseProtocol{}
	headers := http.Header{}
	headers.Set("Authorization", "Bearer test-token")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := proto.Dial(ctx, srv.URL, headers)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer proto.Close()

	if receivedAuth != "Bearer test-token" {
		t.Errorf("auth header = %q, want %q", receivedAuth, "Bearer test-token")
	}

	data, err := proto.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != "ok" {
		t.Errorf("data = %q, want %q", data, "ok")
	}
}

func TestIsRetryableError(t *testing.T) {
	tests := []struct {
		err       string
		retryable bool
	}{
		// Permanent — 4xx
		{"SSE: HTTP 400 400 Bad Request", false},
		{"SSE: HTTP 401 401 Unauthorized", false},
		{"SSE: HTTP 403 403 Forbidden", false},
		{"SSE: HTTP 404 404 Not Found", false},
		{"failed to WebSocket dial: expected handshake response status = 403", false},
		{"failed to WebSocket dial: expected handshake response status = 401", false},
		// coder/websocket handshake format
		{"failed to WebSocket dial: expected handshake response status code 101 but got 403", false},
		{"failed to WebSocket dial: expected handshake response status code 101 but got 451", false},
		{"failed to WebSocket dial: expected handshake response status code 101 but got 503", true},
		{"failed to WebSocket dial: expected handshake response status code 101 but got 429", true},
		// Retryable — 429
		{"SSE: HTTP 429 429 Too Many Requests", true},
		{"expected handshake response HTTP 429", true},
		// Permanent — SSRF / DNS
		{"connmgr: SSRF protection: blocked", false},
		{"dial tcp: lookup badhost.invalid: no such host", false},
		// Retryable — transient
		{"read tcp: connection reset by peer", true},
		{"context deadline exceeded", true},
		{"EOF", true},
		{"SSE: HTTP 502 502 Bad Gateway", true},
		{"SSE: HTTP 503 503 Service Unavailable", true},
		{"websocket: close 1006 (abnormal closure)", true},
		// Nil
		{"", true},
	}

	for _, tt := range tests {
		var err error
		if tt.err != "" {
			err = fmt.Errorf("%s", tt.err)
		}
		got := isRetryableError(err)
		if got != tt.retryable {
			t.Errorf("isRetryableError(%q) = %v, want %v", tt.err, got, tt.retryable)
		}
	}
}

func TestConnInfo_Defaults(t *testing.T) {
	info := ConnInfo{}
	if info.Status != "" {
		t.Errorf("default status should be empty, got %q", info.Status)
	}
	if info.MessagesIn != 0 {
		t.Errorf("default messages_in should be 0, got %d", info.MessagesIn)
	}
}
