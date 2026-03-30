package web

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"

	"altclaw.ai/internal/bridge"
	"altclaw.ai/internal/engine"
	"altclaw.ai/internal/executor"
	"github.com/dop251/goja"
)

// RunScript streams ui.log output of a workspace JS file over SSE.
//
//	GET /api/run-script?path=relative/path.js
//
// Events: {"type":"log","content":"..."} | {"type":"error","content":"..."} | {"type":"done"}
func (a *Api) RunScript(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		http.Error(w, `{"error":"path required"}`, http.StatusBadRequest)
		return
	}

	ws := a.server.store.Workspace()
	absPath, err := bridge.SanitizePath(ws.Path, path)
	if err != nil {
		http.Error(w, `{"error":"invalid path"}`, http.StatusBadRequest)
		return
	}

	// Set up SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher, canFlush := w.(http.Flusher)

	send := func(typ, content string) {
		data, _ := json.Marshal(map[string]string{"type": typ, "content": content})
		fmt.Fprintf(w, "data: %s\n\n", data)
		if canFlush {
			flusher.Flush()
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), ws.TimeoutFor("run"))
	defer cancel()

	// Use the shared executor from the server — avoids creating a new Docker
	// container for every RunScript invocation.
	exec := a.server.Exec
	execType := a.server.ExecType

	// Fallback: build a local executor if no shared executor is available
	// (e.g. first run before any providers are configured).
	if exec == nil {
		exec = executor.NewLocal(ws.Path, nil)
		execType = "local"
	}

	ui := &scriptRunnerUI{send: send}
	wsModDir, userModDir := a.server.store.ModuleDirs(ws.ID)
	eng := engine.New(ws, exec, ui, "", a.server.store, a.server.logBuf).
		WithModuleDirs(wsModDir, userModDir)
	envMap := map[string]string{}
	if execType != "" {
		envMap["EXECUTOR"] = execType
	}
	eng.SetProcess("run", "", path, envMap)
	eng.SetGlobal("output", func(call goja.FunctionCall) goja.Value {
		// In task context output() acts as ui.log — return value comes from `return`.
		if ui != nil {
			ui.Log(eng.ConsoleFormat(call))
		}
		return goja.Undefined()
	})

	// Register cron bridge so require("cron") works in run scripts
	eng.WithCronManager(a.server.cronMgr, func() int64 { return 0 })

	defer eng.Cleanup()

	// Read the script content upfront and pass it as inline code — the same
	// pattern used by cron scripts. This keeps require() resolution in the
	// non-absolute-path loader path so stdlib modules (e.g. "browser") work.
	scriptContent, readErr := os.ReadFile(absPath)
	if readErr != nil {
		send("error", "cannot read script: "+readErr.Error())
		return
	}

	result := eng.RunModule(ctx, string(scriptContent))
	if result.Error != nil {
		slog.Debug("run-script error", "path", absPath, "err", result.Error)
		send("error", result.Error.Error())
	} else {
		send("done", "")
	}
}

// scriptRunnerUI routes ui.log() from the engine to the SSE stream.
type scriptRunnerUI struct {
	send func(typ, content string)
}

func (u *scriptRunnerUI) Log(msg string)      { u.send("log", msg) }
func (u *scriptRunnerUI) Ask(_ string) string { return "" }
func (u *scriptRunnerUI) Confirm(action, label, summary string, params map[string]any) string {
	return "no"
}
