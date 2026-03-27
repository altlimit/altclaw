package bridge

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/dop251/goja"
)

func makeRecord(msg string, level slog.Level) slog.Record {
	return slog.NewRecord(time.Now(), level, msg, 0)
}

func TestLogBuffer_RingOverflow(t *testing.T) {
	buf := NewLogBuffer(3)

	for i := 0; i < 5; i++ {
		r := makeRecord("msg", slog.LevelInfo)
		_ = buf.Handle(context.Background(), r)
	}

	entries := buf.Recent(0)
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}
}

func TestLogBuffer_RecentOrder(t *testing.T) {
	buf := NewLogBuffer(10)

	for _, msg := range []string{"first", "second", "third"} {
		r := makeRecord(msg, slog.LevelInfo)
		_ = buf.Handle(context.Background(), r)
	}

	entries := buf.Recent(3)
	if len(entries) != 3 {
		t.Fatalf("expected 3, got %d", len(entries))
	}
	if entries[0].Msg != "third" {
		t.Errorf("expected newest first, got %q", entries[0].Msg)
	}
	if entries[2].Msg != "first" {
		t.Errorf("expected oldest last, got %q", entries[2].Msg)
	}
}

func TestLogBuffer_Search(t *testing.T) {
	buf := NewLogBuffer(10)

	messages := []string{
		"server starting on port 8080",
		"database connection established",
		"server request handled",
	}
	for _, msg := range messages {
		r := makeRecord(msg, slog.LevelInfo)
		_ = buf.Handle(context.Background(), r)
	}

	results := buf.Search("server")
	if len(results) != 2 {
		t.Fatalf("expected 2 matches for 'server', got %d", len(results))
	}

	results = buf.Search("database")
	if len(results) != 1 {
		t.Fatalf("expected 1 match for 'database', got %d", len(results))
	}
}

func TestLogBridge_Recent(t *testing.T) {
	vm := goja.New()
	buf := NewLogBuffer(10)

	for _, msg := range []string{"alpha", "beta", "gamma"} {
		r := makeRecord(msg, slog.LevelInfo)
		_ = buf.Handle(context.Background(), r)
	}

	RegisterLog(vm, buf)

	val, err := vm.RunString(`log.recent(2).length`)
	if err != nil {
		t.Fatalf("log.recent failed: %v", err)
	}
	if val.ToInteger() != 2 {
		t.Errorf("expected 2, got %d", val.ToInteger())
	}

	val, err = vm.RunString(`log.recent(1)[0].msg`)
	if err != nil {
		t.Fatalf("log.recent msg failed: %v", err)
	}
	if val.String() != "gamma" {
		t.Errorf("expected 'gamma', got %q", val.String())
	}
}

func TestLogBridge_Search(t *testing.T) {
	vm := goja.New()
	buf := NewLogBuffer(10)

	messages := []string{"hello world", "goodbye world", "hello again"}
	for _, msg := range messages {
		r := makeRecord(msg, slog.LevelInfo)
		_ = buf.Handle(context.Background(), r)
	}

	RegisterLog(vm, buf)

	val, err := vm.RunString(`log.search("hello").length`)
	if err != nil {
		t.Fatalf("log.search failed: %v", err)
	}
	if val.ToInteger() != 2 {
		t.Errorf("expected 2 matches, got %d", val.ToInteger())
	}
}
