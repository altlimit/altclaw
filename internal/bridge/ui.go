package bridge

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"altclaw.ai/internal/provider"
	"github.com/dop251/goja"
)

// UIHandler is the interface the TUI must implement to receive bridge UI calls.
type UIHandler interface {
	// Log displays a timestamped message in the UI.
	Log(msg string)
	// Ask prompts the user and blocks until they respond.
	Ask(question string) string
	// Confirm presents a privileged action for user approval.
	// Returns the user's answer string (parsed by the caller).
	Confirm(action, label, summary string, params map[string]any) string
}

// FileAttacher receives file attachments from ui.file() for AI analysis.
// Implemented by the Engine to queue files for the next AI message.
type FileAttacher interface {
	AddFile(f provider.FileData)
}

// RegisterUI adds the ui namespace (ui.log, ui.ask, ui.file) to the runtime.
// attacher is optional — if nil, ui.file() will not be registered.
func RegisterUI(vm *goja.Runtime, handler UIHandler, workspace string, attacher FileAttacher) {
	ui := vm.NewObject()

	// ui.log(args...) — log to console, visible to user
	ui.Set("log", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			return goja.Undefined()
		}
		parts := make([]string, len(call.Arguments))
		for i, arg := range call.Arguments {
			parts[i] = stringify(vm, arg)
		}
		msg := ""
		for i, p := range parts {
			if i > 0 {
				msg += " "
			}
			msg += p
		}
		ts := time.Now().Format("15:04:05")
		// Truncate large messages to avoid flooding context
		const maxLogLen = 5000
		if len(msg) > maxLogLen {
			msg = msg[:maxLogLen] + "... (truncated)"
		}
		handler.Log(fmt.Sprintf("[%s] %s", ts, msg))
		return goja.Undefined()
	})

	ui.Set("ask", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			Throw(vm, "ui.ask requires a question argument")
		}
		question := call.Arguments[0].String()
		answer := handler.Ask(question)
		return vm.ToValue(answer)
	})

	// ui.file(path) — attach workspace file for AI analysis
	if attacher != nil {
		ui.Set("file", func(call goja.FunctionCall) goja.Value {
			if len(call.Arguments) < 1 {
				Throw(vm, "ui.file requires a path argument")
			}
			relPath := call.Arguments[0].String()

			absPath, err := SanitizePath(workspace, relPath)
			if err != nil {
				logErr(vm, "ui.file", err)
			}

			data, err := os.ReadFile(absPath)
			if err != nil {
				Throwf(vm, "ui.file: %v", err)
			}

			mime := mimeFromExt(filepath.Ext(relPath))
			name := filepath.Base(relPath)

			attacher.AddFile(provider.FileData{
				Name:     name,
				MimeType: mime,
				Data:     data,
			})

			handler.Log(fmt.Sprintf("📎 Attached: %s (%s, %d bytes)", name, mime, len(data)))
			return goja.Undefined()
		})
	}

	vm.Set(NameUI, ui)
}

// mimeFromExt returns a MIME type for a file extension.
func mimeFromExt(ext string) string {
	ext = strings.ToLower(ext)
	switch ext {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".svg":
		return "image/svg+xml"
	case ".pdf":
		return "application/pdf"
	case ".mp4":
		return "video/mp4"
	case ".webm":
		return "video/webm"
	case ".mp3":
		return "audio/mpeg"
	case ".wav":
		return "audio/wav"
	case ".json":
		return "application/json"
	case ".txt", ".md", ".log", ".csv":
		return "text/plain"
	case ".html", ".htm":
		return "text/html"
	case ".xml":
		return "application/xml"
	default:
		return "application/octet-stream"
	}
}
