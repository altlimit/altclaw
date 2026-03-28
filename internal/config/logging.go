package config

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/natefinch/lumberjack.v2"
)

// SetupLogging configures slog with an optional rotating file handler and log level.
// If logPath is empty, logs go to stdout only (slog default behavior).
// logLevel can be "debug", "info", "warn", or "error" (default "info").
// maxSizeMB is the max size per log file before rotation (default 10).
// maxFiles is how many rotated files to keep (default 3).
// extra handlers are appended to the handler chain (e.g. an in-memory log buffer).
func SetupLogging(logPath, logLevel string, maxSizeMB, maxFiles int, extraHandlers ...slog.Handler) {
	if maxSizeMB <= 0 {
		maxSizeMB = 10
	}
	if maxFiles <= 0 {
		maxFiles = 3
	}

	level := parseLogLevel(logLevel)
	var w io.Writer = os.Stdout

	if logPath != "" {
		dir := filepath.Dir(logPath)
		if err := os.MkdirAll(dir, 0755); err == nil {
			lj := &lumberjack.Logger{
				Filename:   logPath,
				MaxSize:    maxSizeMB,
				MaxBackups: maxFiles,
				LocalTime:  true,
			}
			// Write to both stdout and file
			w = io.MultiWriter(os.Stdout, lj)
		}
	}

	primary := slog.NewTextHandler(w, &slog.HandlerOptions{
		Level: level,
	})

	// If extra handlers supplied, wrap in a multi-handler
	var handler slog.Handler = primary
	if len(extraHandlers) > 0 {
		all := make([]slog.Handler, 0, 1+len(extraHandlers))
		all = append(all, primary)
		all = append(all, extraHandlers...)
		handler = &multiHandler{handlers: all}
	}

	slog.SetDefault(slog.New(handler))
}

// NewMultiHandler creates a slog.Handler that fans out each record to all
// the provided handlers. This is useful for combining e.g. a text handler
// with an in-memory log buffer.
func NewMultiHandler(handlers ...slog.Handler) slog.Handler {
	return &multiHandler{handlers: handlers}
}

// multiHandler fans out each log record to multiple slog.Handlers.
type multiHandler struct {
	handlers []slog.Handler
}

func (m *multiHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, h := range m.handlers {
		if h.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

func (m *multiHandler) Handle(ctx context.Context, r slog.Record) error {
	for _, h := range m.handlers {
		if h.Enabled(ctx, r.Level) {
			_ = h.Handle(ctx, r)
		}
	}
	return nil
}

func (m *multiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	handlers := make([]slog.Handler, len(m.handlers))
	for i, h := range m.handlers {
		handlers[i] = h.WithAttrs(attrs)
	}
	return &multiHandler{handlers: handlers}
}

func (m *multiHandler) WithGroup(name string) slog.Handler {
	handlers := make([]slog.Handler, len(m.handlers))
	for i, h := range m.handlers {
		handlers[i] = h.WithGroup(name)
	}
	return &multiHandler{handlers: handlers}
}

// parseLogLevel converts a string log level to slog.Level.
func parseLogLevel(level string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

