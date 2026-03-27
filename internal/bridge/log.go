package bridge

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/dop251/goja"
)

// LogEntry is a single captured log record.
type LogEntry struct {
	Time  time.Time
	Level string
	Msg   string
	Attrs map[string]string
}

// LogBuffer is a thread-safe ring buffer that implements slog.Handler.
// It captures the last N log records in memory for agent inspection.
type LogBuffer struct {
	mu      sync.RWMutex
	entries []LogEntry
	size    int
	head    int // next write position
	count   int // total entries stored (≤ size)
}

// NewLogBuffer creates a ring buffer that holds up to size log entries.
func NewLogBuffer(size int) *LogBuffer {
	if size <= 0 {
		size = 200
	}
	return &LogBuffer{
		entries: make([]LogEntry, size),
		size:    size,
	}
}

// Enabled implements slog.Handler — accepts all levels.
func (b *LogBuffer) Enabled(_ context.Context, _ slog.Level) bool {
	return true
}

// Handle implements slog.Handler — captures the record into the ring buffer.
func (b *LogBuffer) Handle(_ context.Context, r slog.Record) error {
	entry := LogEntry{
		Time:  r.Time,
		Level: r.Level.String(),
		Msg:   r.Message,
		Attrs: make(map[string]string),
	}
	r.Attrs(func(a slog.Attr) bool {
		entry.Attrs[a.Key] = a.Value.String()
		return true
	})

	b.mu.Lock()
	b.entries[b.head] = entry
	b.head = (b.head + 1) % b.size
	if b.count < b.size {
		b.count++
	}
	b.mu.Unlock()
	return nil
}

// WithAttrs implements slog.Handler — returns self (attrs captured per-record).
func (b *LogBuffer) WithAttrs(_ []slog.Attr) slog.Handler { return b }

// WithGroup implements slog.Handler — returns self (groups not needed).
func (b *LogBuffer) WithGroup(_ string) slog.Handler { return b }

// Recent returns the last n entries, newest first.
func (b *LogBuffer) Recent(n int) []LogEntry {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if n <= 0 || n > b.count {
		n = b.count
	}
	result := make([]LogEntry, n)
	for i := 0; i < n; i++ {
		idx := (b.head - 1 - i + b.size) % b.size
		result[i] = b.entries[idx]
	}
	return result
}

// Search returns entries whose msg or attr values match the query keywords.
func (b *LogBuffer) Search(query string) []LogEntry {
	queryWords := tokenize(query)
	if len(queryWords) == 0 {
		return nil
	}

	all := b.Recent(0) // all entries, newest first
	var results []LogEntry
	for _, e := range all {
		// Build corpus from msg + attr values
		corpus := e.Msg
		for _, v := range e.Attrs {
			corpus += " " + v
		}
		if overlapScore(queryWords, tokenize(corpus)) >= 0.1 {
			results = append(results, e)
		}
	}
	return results
}

// EntryToMap converts a LogEntry to a map for JS/JSON consumption.
func EntryToMap(e LogEntry) map[string]interface{} {
	m := map[string]interface{}{
		"time":  e.Time.Format(time.RFC3339),
		"level": e.Level,
		"msg":   e.Msg,
	}
	if len(e.Attrs) > 0 {
		attrs := make(map[string]interface{}, len(e.Attrs))
		for k, v := range e.Attrs {
			attrs[k] = v
		}
		m["attrs"] = attrs
	}
	return m
}

// RegisterLog adds the log namespace to the JS runtime.
//
// Reading:
//
//	log.recent(n?)       — last N entries (default 50), newest first
//	log.search(query)    — keyword search across messages and attrs
//
// Writing:
//
//	log.debug(msg, ...)  — emit DEBUG log
//	log.info(msg, ...)   — emit INFO log
//	log.warn(msg, ...)   — emit WARN log
//	log.error(msg, ...)  — emit ERROR log
func RegisterLog(vm *goja.Runtime, buf *LogBuffer) {
	logObj := vm.NewObject()

	// log.recent(n?) — read recent log entries
	logObj.Set("recent", func(call goja.FunctionCall) goja.Value {
		n := 50
		if len(call.Arguments) >= 1 {
			n = int(call.Arguments[0].ToInteger())
		}
		entries := buf.Recent(n)
		result := make([]map[string]interface{}, len(entries))
		for i, e := range entries {
			result[i] = EntryToMap(e)
		}
		return vm.ToValue(result)
	})

	// log.search(query) — keyword search
	logObj.Set("search", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			Throw(vm, "log.search requires a query string")
		}
		query := call.Arguments[0].String()
		entries := buf.Search(query)
		result := make([]map[string]interface{}, len(entries))
		for i, e := range entries {
			result[i] = EntryToMap(e)
		}
		return vm.ToValue(result)
	})

	// Write helpers: log.debug, log.info, log.warn, log.error
	for _, pair := range []struct {
		name  string
		level slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"info", slog.LevelInfo},
		{"warn", slog.LevelWarn},
		{"error", slog.LevelError},
	} {
		level := pair.level
		logObj.Set(pair.name, func(call goja.FunctionCall) goja.Value {
			if len(call.Arguments) < 1 {
				return goja.Undefined()
			}
			msg := call.Arguments[0].String()

			// Remaining args are key-value attr pairs
			var attrs []any
			for i := 1; i+1 < len(call.Arguments); i += 2 {
				attrs = append(attrs, call.Arguments[i].String(), call.Arguments[i+1].String())
			}
			// If odd trailing arg, add it with empty value
			if len(call.Arguments) > 1 && (len(call.Arguments)-1)%2 != 0 {
				attrs = append(attrs, call.Arguments[len(call.Arguments)-1].String(), "")
			}

			slog.Log(context.Background(), level, fmt.Sprintf("[agent] %s", msg), attrs...)
			return goja.Undefined()
		})
	}

	vm.Set(NameLog, logObj)
}
