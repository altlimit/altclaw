// Package engine wraps the Goja JavaScript runtime, registers all bridges,
// and handles VM lifecycle including timeout enforcement and process cleanup.
package engine

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"altclaw.ai/internal/bridge"
	"altclaw.ai/internal/config"
	"altclaw.ai/internal/executor"
	"altclaw.ai/internal/provider"
	"altclaw.ai/stdlib"
	"github.com/dop251/goja"
	"github.com/dop251/goja_nodejs/require"
)

// doneSignal is a sentinel type used by the done() function to cleanly
// stop execution and return a value to the caller.
type doneSignal struct {
	value string
}

// Engine manages a Goja JavaScript runtime with all bridge APIs registered.
type Engine struct {
	vm             *goja.Runtime
	req            *require.RequireModule // Go-side require() for loading modules
	exec           executor.Executor
	ws             *config.Workspace
	mu             sync.Mutex
	browserCleanup bridge.BrowserCleanup
	dbPool         *bridge.DBPool
	blobPool       *bridge.BlobPool
	filesMu        sync.Mutex // separate from mu to avoid deadlock with Run()
	pendingFiles   []provider.FileData

	// Execution context — set during Run(), used by browser bridge to cancel go-rod ops
	execCtx    context.Context
	execCancel context.CancelFunc
	ctxMu      sync.RWMutex

	// Lifecycle context — tied to the Engine's lifespan
	engineCtx    context.Context
	engineCancel context.CancelFunc

	// Pausable deadline: tracks remaining execution budget.
	// When paused (e.g. waiting for agent.result), the timer stops
	// and remaining time is preserved. On resume, the timer restarts
	// with the remaining budget.
	dlMu      sync.Mutex
	dlTimer   *time.Timer
	dlStarted time.Time     // when the current timer segment started
	dlBudget  time.Duration // remaining budget when paused (0 = not paused)

	// moduleDirs is the ordered set of directories to search for user modules.
	// Set via WithModuleDirs (workspace dir first, user dir second).
	moduleDirs []string

	// OnBroadcast is an optional callback for broadcasting SSE events.
	// Set by the web server; bridge functions use it for real-time UI updates.
	OnBroadcast func(eventJSON []byte)

	// dynamicDoc provides runtime-generated docs (e.g. settings, config).
	// Set via SetConfirmContext; referenced by the RegisterDoc closure.
	dynamicDoc bridge.DynamicDocFunc

	// moduleCtx provides module install/remove operations.
	// Set via SetConfirmContext; referenced by the RegisterModule closure.
	moduleCtx bridge.ModuleContext

	// task child tracking
	taskMu       sync.Mutex
	taskChildren []*Engine // child engines spawned by task.run
	taskStore    *config.Store
	taskUI       bridge.UIHandler
	taskSession  string // session ID inherited by child VMs for Docker routing
}

// WithModuleDirs adds module search directories to the engine.
// Call after New() to configure where require("module-name") looks for files.
// Entries are searched in order (first match wins).
func (e *Engine) WithModuleDirs(dirs ...string) *Engine {
	e.moduleDirs = append(e.moduleDirs, dirs...)
	return e
}

// SetGlobal sets a named global variable on the VM.
// Used by RunTask to register the __taskResult sentinel and disable output().
func (e *Engine) SetGlobal(name string, value interface{}) {
	e.vm.Set(name, value)
}

// BroadcastCtx returns a context enriched with the broadcast function.
// Used by bridge registrations that need to pass broadcast-enabled contexts to store calls.
// Returns context.Background() if no broadcast function is set.
func (e *Engine) BroadcastCtx() context.Context {
	if e.OnBroadcast != nil {
		return config.WithBroadcast(context.Background(), config.BroadcastFunc(e.OnBroadcast))
	}
	return context.Background()
}

// ExecContext returns the current execution context, or context.Background() if not in execution.
func (e *Engine) ExecContext() context.Context {
	e.ctxMu.RLock()
	defer e.ctxMu.RUnlock()
	if e.execCtx != nil {
		return e.execCtx
	}
	return context.Background()
}

// AddFile queues a file for attachment to the next AI message.
func (e *Engine) AddFile(f provider.FileData) {
	e.filesMu.Lock()
	defer e.filesMu.Unlock()
	e.pendingFiles = append(e.pendingFiles, f)
}

// DrainFiles returns and clears all pending file attachments.
func (e *Engine) DrainFiles() []provider.FileData {
	e.filesMu.Lock()
	defer e.filesMu.Unlock()
	files := e.pendingFiles
	e.pendingFiles = nil
	return files
}

// New creates a new Engine with all bridge APIs registered.
// sessionID is used for per-session Docker container routing (empty for main agent).
// store is the config store for mem bridge (nil to skip).
// builtinModules is the set of bridge global names that require() should short-circuit
// (e.g. require("fs") → `module.exports = fs;`). Derived from the canonical registry.
var builtinModules = bridge.BuiltinNames()

func New(ws *config.Workspace, exec executor.Executor, uiHandler bridge.UIHandler, sessionID string, store *config.Store, logBuf ...*bridge.LogBuffer) *Engine {
	vm := goja.New()

	nsForDB := ""
	if store != nil && ws != nil {
		nsForDB = ws.ID
	}

	// Pre-allocate eng so the loader closure can capture eng.moduleDirs,
	// which is set later via WithModuleDirs().
	eng := &Engine{
		vm:          vm,
		exec:        exec,
		ws:          ws,
		taskStore:   store,
		taskUI:      uiHandler,
		taskSession: sessionID,
	}
	_ = nsForDB // retained for mem/secret bridges that still use it

	// Set up require() with a custom SourceLoader:
	//   require("web")             → stdlib embedded module
	//   require("/abs/path.js")    → workspace-absolute file
	//   require("./rel.js")        → workspace-relative file (jailed)
	//   require("fs") etc.         → bridge global shim
	registry := require.NewRegistryWithLoader(func(path string) ([]byte, error) {
		// Normalize to forward slashes for consistent handling across platforms (Windows passes \ )
		cleanPath := filepath.ToSlash(path)

		// Strip node_modules/ prefix — goja_nodejs adds this for bare module names.
		// It may come with a leading slash (/node_modules/...) when the require
		// originates from inside an already-loaded module.
		// When the caller was loaded via an absolute path (serverjs), goja_nodejs
		// walks up directories producing paths like /workspace/public/node_modules/db
		// so we must also extract the module name from node_modules/ anywhere in the path.
		cleanPath = strings.TrimPrefix(cleanPath, "/node_modules/")
		cleanPath = strings.TrimPrefix(cleanPath, "node_modules/")
		if idx := strings.LastIndex(cleanPath, "/node_modules/"); idx >= 0 {
			cleanPath = cleanPath[idx+len("/node_modules/"):]
		}

		// Bridge globals: require("fs"), require("db") etc. returns the global bridge object.
		// Must check BEFORE the absolute-path branch because goja_nodejs may resolve
		// a bare require("db") inside an already-loaded module to an absolute path like
		// /workspace/public/node_modules/db — which would incorrectly fail as "not found".
		if builtinModules[cleanPath] {
			return []byte("module.exports = " + cleanPath + ";"), nil
		}

		// 1. Host-absolute path (e.g. C:\workspace\test.js on Windows or /workspace/test.js on Linux)
		// This happens when RunModule passes an absolute path so goja_nodejs recognizes it natively.
		if filepath.IsAbs(path) {
			// Jail check
			rel, err := filepath.Rel(ws.Path, path)
			if err != nil || strings.HasPrefix(rel, "..") {
				// Path is outside workspace — remap to workspace-relative.
				// This handles require("/math.js") where goja-nodejs passes OS-absolute "/math.js"
				// but the user means workspace-absolute.
				safePath := filepath.Join(ws.Path, filepath.Clean(strings.TrimPrefix(cleanPath, "/")))
				rel2, err2 := filepath.Rel(ws.Path, safePath)
				if err2 != nil || strings.HasPrefix(rel2, "..") {
					return nil, fmt.Errorf("module not found (jail escape): %s", rel)
				}
				data, err := os.ReadFile(safePath)
				if err != nil {
					if displayPath, relErr := filepath.Rel(ws.Path, safePath); relErr == nil {
						return nil, fmt.Errorf("module not found: %q", displayPath)
					}
					return nil, fmt.Errorf("module not found")
				}
				return data, nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				// Don't show host paths in errors
				if displayPath, relErr := filepath.Rel(ws.Path, path); relErr == nil {
					return nil, fmt.Errorf("module not found: %q", displayPath)
				}
				return nil, fmt.Errorf("module not found")
			}
			return data, nil
		}

		notFound := func(inner error) error {
			if inner != nil {
				slog.Debug("require: module load error", "module", cleanPath, "err", inner)
			}
			// Show only a workspace-relative path when the full host path leaked in
			displayPath := cleanPath
			if rel, err := filepath.Rel(ws.Path, cleanPath); err == nil && !strings.HasPrefix(rel, "..") {
				displayPath = rel
			}
			return fmt.Errorf("module not found: %q", displayPath)
		}

		// (builtin bridge check already handled above, before the absolute-path branch)

		// Workspace-absolute path: require("/foo/bar.js") → workspace/foo/bar.js
		// Must strip leading "/" before filepath.Join — on Linux, filepath.Join(workspace, "/abs")
		// returns "/abs", completely ignoring the workspace root.
		if strings.HasPrefix(cleanPath, "/") {
			safePath := filepath.Join(ws.Path, filepath.Clean(strings.TrimPrefix(cleanPath, "/")))
			// Jail check
			rel, err := filepath.Rel(ws.Path, safePath)
			if err != nil || strings.HasPrefix(rel, "..") {
				return nil, notFound(fmt.Errorf("jail escape: rel=%s", rel))
			}
			data, err := os.ReadFile(safePath)
			if err != nil {
				return nil, notFound(err)
			}
			return data, nil
		}

		// Bare name (no slashes) → search filesystem module dirs first (slug-style), then stdlib.
		// This allows require("my-module") from a flat modules/<slug>/ directory.
		if !strings.Contains(cleanPath, "/") {
			for _, baseDir := range eng.moduleDirs {
				if data, ok := readModuleFile(baseDir, cleanPath); ok {
					return data, nil
				}
			}
			if src, ok := stdlib.Load(cleanPath); ok {
				return []byte(src), nil
			}
			return nil, notFound(nil)
		}

		// module-name or module-name.js → search filesystem module dirs, then stdlib.
		// Relative requires from WITHIN a module resolve automatically via goja_nodejs:
		//   node_modules/module-name/index.js + require("./helper.js")
		//     → loader called with node_modules/module-name/helper.js → stripped → module-name/helper.js
		// so the module-name/ jailing is automatic.
		if !strings.HasPrefix(cleanPath, ".") {
			for _, baseDir := range eng.moduleDirs {
				if data, ok := readModuleFile(baseDir, cleanPath); ok {
					return data, nil
				}
			}
			if src, ok := stdlib.Load(cleanPath); ok {
				return []byte(src), nil
			}

			// Fallback: This might be a workspace-absolute require (e.g. require("/api/foo.js"))
			// that lost its leading slash on Windows when goja_nodejs prepended node_modules\.
			// We fallback to treating it as a path relative to the workspace root.
			full, err := bridge.SanitizePath(ws.Path, cleanPath)
			if err == nil {
				if data, fileErr := os.ReadFile(full); fileErr == nil {
					return data, nil
				}
			}

			return nil, notFound(nil)
		}

		// Relative workspace-relative file: jailed to workspace
		full, err := bridge.SanitizePath(ws.Path, cleanPath)
		if err != nil {
			return nil, notFound(err)
		}
		data, err := os.ReadFile(full)
		if err != nil {
			return nil, notFound(err)
		}
		return data, nil
	})
	reqModule := registry.Enable(vm)
	eng.req = reqModule

	eng.engineCtx, eng.engineCancel = context.WithCancel(context.Background())

	// Enable console.log / console.error for general logging
	console := vm.NewObject()
	console.Set("log", func(call goja.FunctionCall) goja.Value {
		msg := eng.ConsoleFormat(call)
		if uiHandler != nil {
			uiHandler.Log(msg)
		}
		slog.Info("console.log", "msg", msg)
		return goja.Undefined()
	})
	console.Set("error", func(call goja.FunctionCall) goja.Value {
		msg := eng.ConsoleFormat(call)
		if uiHandler != nil {
			uiHandler.Log("ERROR: " + msg)
		}
		slog.Error("console.error", "msg", msg)
		return goja.Undefined()
	})
	vm.Set("console", console)

	// Create a context factory that injects the broadcast callback.
	// Bridges use this when calling store methods so AfterSave/AfterDelete hooks fire.
	broadcastCtx := func() context.Context {
		if eng.OnBroadcast != nil {
			return config.WithBroadcast(context.Background(), config.BroadcastFunc(eng.OnBroadcast))
		}
		return context.Background()
	}
	bridge.RegisterFetch(vm, store, ws.Path, broadcastCtx)
	bridge.RegisterFS(vm, ws.Path, ws, uiHandler, store)
	bridge.RegisterCSV(vm, ws.Path)
	bridge.RegisterDoc(vm, eng.moduleDirs, func(name string) string {
		if eng.dynamicDoc != nil {
			return eng.dynamicDoc(name)
		}
		return ""
	})
	bridge.RegisterCrypto(vm)
	if store != nil {
		bridge.RegisterMem(vm, store, nsForDB, broadcastCtx)
	}
	if exec != nil {
		ctx := executor.WithSession(context.Background(), sessionID)
		_, isLocal := exec.(*executor.Local)
		bridge.RegisterSys(vm, exec, store, ws.Path, ws, ctx, uiHandler, isLocal)
	}
	if store != nil {
		bridge.RegisterSecret(vm, store, nsForDB, broadcastCtx)
	}
	if uiHandler != nil {
		bridge.RegisterUI(vm, uiHandler, ws.Path, eng)
	}
	browserCleanup := bridge.RegisterBrowser(vm, ws.Path)

	// Git history bridge — bare repo in configDir for workspace versioning
	bridge.RegisterGit(vm, ws.Path, config.ConfigDir(), ws.ID)

	// Module management bridge
	bridge.RegisterModule(vm, func() []string { return eng.moduleDirs }, uiHandler, func() bridge.ModuleContext {
		return eng.moduleCtx
	}, func() bool {
		if store != nil {
			return store.Settings().ConfirmModInstall()
		}
		return ws.ConfirmModInstall
	})

	// Database bridge — auto-managed connection pool
	dbPool := bridge.NewDBPool(ws.Path)
	bridge.RegisterDB(vm, dbPool, store, ws.Path, broadcastCtx)
	eng.dbPool = dbPool

	// Blob storage bridge — auto-managed bucket pool
	blobPool := bridge.NewBlobPool()
	bridge.RegisterBlob(vm, blobPool, store, ws.Path, broadcastCtx)
	eng.blobPool = blobPool

	// Register task bridge — parallel child VMs via goroutines
	bridge.RegisterTask(vm, eng, eng.engineCtx)

	// Register log bridge — in-memory slog ring buffer (optional)
	if len(logBuf) > 0 && logBuf[0] != nil {
		bridge.RegisterLog(vm, logBuf[0])
	}

	// Simple utility bridges (no external dependencies)
	bridge.RegisterDNS(vm)
	bridge.RegisterZip(vm, ws.Path)
	bridge.RegisterImage(vm, ws.Path)
	bridge.RegisterSSH(vm, ws.Path)

	// Chat bridge — cross-conversation access (store-dependent)
	if store != nil && ws != nil {
		bridge.RegisterChat(vm, store, ws.ID)
		bridge.RegisterMail(vm, store, broadcastCtx)
	}

	// Cache bridge — uses dsorm cache from store
	if store != nil {
		bridge.RegisterCache(vm, store.Client.Cache(), ws.ID)
	}

	// Register sleep(ms) — synchronous pause
	vm.Set("sleep", func(call goja.FunctionCall) goja.Value {
		ms := int64(1000)
		if len(call.Arguments) > 0 {
			ms = call.Arguments[0].ToInteger()
		}
		time.Sleep(time.Duration(ms) * time.Millisecond)
		return goja.Undefined()
	})

	// Register output(value) — sets the result and stops execution immediately.
	// This is the proper way for scripts to pass a value to the next conversation turn.
	vm.Set("output", func(call goja.FunctionCall) goja.Value {
		val := ""
		if len(call.Arguments) > 0 {
			val = bridge.Stringify(vm, call.Arguments[0])
		}
		panic(&doneSignal{value: val})
	})

	// Persistent store for sharing state across iterations
	vm.Set("store", vm.NewObject())

	eng.browserCleanup = browserCleanup
	return eng
}

func (e *Engine) ConsoleFormat(call goja.FunctionCall) string {
	parts := make([]string, len(call.Arguments))
	for i, arg := range call.Arguments {
		parts[i] = bridge.Stringify(e.vm, arg)
	}
	return strings.Join(parts, " ")
}

// SetAgentRunner registers the agent bridge (agent.run, agent.result) on the VM.
// Called after construction to break the circular dependency between Agent and Engine.
// The engine itself is passed as the DeadlinePauser so agent.result() can
// pause/resume the execution deadline while waiting for sub-agents.
func (e *Engine) SetAgentRunner(runner bridge.SubAgentRunner) {
	bridge.RegisterAgent(e.vm, runner, e.engineCtx, e)
}

// SetConfirmContext registers ui.confirm on the VM with a server-side execution context.
// Called after construction to break the circular dependency between engine and web server.
// Also sets up the dynamic doc provider for doc.read("settings"), doc.read("config"), etc.
func (e *Engine) SetConfirmContext(confirmCtx bridge.ConfirmContext) {
	if e.taskUI != nil {
		bridge.RegisterConfirm(e.vm, e.taskUI, confirmCtx)
	}
	e.dynamicDoc = bridge.BuildDynamicDocFunc(confirmCtx)
	e.moduleCtx = confirmCtx
}

// SetProcess registers the process global on the VM, providing execution context info.
// mode: "agent", "cron", or "server"
// version: altclaw version string
// script: current script identifier (e.g. "api/users.server.js" or "cron:42")
// envExtra: optional extra env vars to set (e.g. PUBLIC_DIR, HOSTNAME)
func (e *Engine) SetProcess(mode, version, script string, envExtra map[string]string) {
	p := e.vm.NewObject()

	// process.env — Node-like object with OS env vars + CTX
	envObj := e.vm.NewObject()
	envObj.Set("CTX", mode)
	for k, v := range envExtra {
		if v != "" {
			envObj.Set(k, v)
		}
	}

	// Auto-inject PORT so scripts can reach the web server via process.env.PORT
	if port := os.Getenv("PORT"); port != "" {
		envObj.Set("PORT", port)
	}

	// Auto-inject PUSH_NOTIFICATIONS if workspace has active subscriptions
	if e.taskStore != nil && e.ws != nil {
		if subs, err := e.taskStore.ListPushSubscriptions(context.Background(), e.ws.ID); err == nil && len(subs) > 0 {
			envObj.Set("PUSH_NOTIFICATIONS", "enabled")
		}
	}

	p.Set("env", envObj)

	p.Set("version", version)
	p.Set("script", script)
	e.vm.Set("process", p)
}

// VM returns the underlying Goja runtime, for registering additional bridges.
func (e *Engine) VM() *goja.Runtime {
	return e.vm
}

// Require loads a module using the Go-side require system.
// path should be relative to workspace (e.g. "./public/api/hello.server.js").
// Returns the module.exports value.
func (e *Engine) Require(path string) (goja.Value, error) {
	return e.req.Require(path)
}

// RunModule executes a CommonJS module and calls its exported function (if any).
// Like serverjs: uses Go-side Require() for file paths, wraps inline code in
// a CommonJS module context. Handles timeout via context-based VM interruption.
//
// If instructions is a file path (single line, .js extension):
//
//	loads via Require("./path") → if module.exports is a function, calls it.
//
// If instructions is inline code:
//
//	wraps in CommonJS context → if module.exports is a function, calls it.
//
// runInVM executes fn in a goroutine with panic recovery for doneSignal,
// and blocks until either the goroutine completes or ctx is cancelled.
// If clearInterrupt is true, ClearInterrupt is deferred (needed by RunModule).
func (e *Engine) runInVM(ctx context.Context, clearInterrupt bool, fn func(ch chan<- *RunResult)) *RunResult {
	ch := make(chan *RunResult, 1)

	go func() {
		if clearInterrupt {
			defer e.vm.ClearInterrupt()
		}
		defer func() {
			if r := recover(); r != nil {
				if d, ok := r.(*doneSignal); ok {
					ch <- &RunResult{Value: d.value}
					return
				}
				ch <- &RunResult{Error: fmt.Errorf("js panic: %v", r)}
			}
		}()
		fn(ch)
	}()

	select {
	case <-ctx.Done():
		e.vm.Interrupt("execution cancelled")
		if e.browserCleanup != nil {
			e.browserCleanup()
		}
		// Wait for the VM goroutine to finish unwinding before releasing e.mu
		<-ch
		return &RunResult{Error: ctx.Err()}
	case result := <-ch:
		return result
	}
}

func (e *Engine) RunModule(ctx context.Context, instructions string) *RunResult {
	e.mu.Lock()
	defer e.mu.Unlock()

	return e.runInVM(ctx, true, func(ch chan<- *RunResult) {
		var exports goja.Value
		var err error

		trimmed := strings.TrimSpace(instructions)
		isPath := !strings.Contains(trimmed, "\n") && strings.HasSuffix(trimmed, ".js")

		if isPath {
			var absPath string
			cleanPath := strings.TrimPrefix(filepath.ToSlash(trimmed), "/")
			cleanPath = strings.TrimPrefix(cleanPath, "./")
			absPath, err = bridge.SanitizePath(e.ws.Path, cleanPath)
			if err != nil {
				ch <- &RunResult{Error: err}
				return
			}
			exports, err = e.req.Require(absPath)
		} else {
			wrapped := "(function(){var module={exports:{}};var exports=module.exports;\n" +
				instructions +
				"\nreturn module.exports;})()"
			exports, err = e.vm.RunString(wrapped)
		}

		if err != nil {
			ch <- &RunResult{Error: fmt.Errorf("js error: %s", cleanJSError(err.Error()))}
			return
		}

		fn, ok := goja.AssertFunction(exports)
		if !ok {
			ch <- &RunResult{}
			return
		}

		val, callErr := fn(goja.Undefined())
		if callErr != nil {
			ch <- &RunResult{Error: fmt.Errorf("js error: %s", cleanJSError(callErr.Error()))}
			return
		}

		result := ""
		if val != nil && !goja.IsUndefined(val) && !goja.IsNull(val) {
			result = val.String()
		}
		ch <- &RunResult{Value: result}
	})
}

// PauseDeadline pauses the execution deadline timer.
// Called by agent.result() before blocking on a sub-agent.
// Safe to call when no deadline is active (no-op).
func (e *Engine) PauseDeadline() {
	e.dlMu.Lock()
	defer e.dlMu.Unlock()
	if e.dlTimer != nil {
		if e.dlTimer.Stop() {
			// Timer was still running — compute remaining budget
			elapsed := time.Since(e.dlStarted)
			remaining := e.dlBudget - elapsed
			if remaining < 0 {
				remaining = 0
			}
			e.dlBudget = remaining
		}
		e.dlTimer = nil
	}
}

// ResumeDeadline resumes the execution deadline timer with the remaining budget.
// Called by agent.result() after the sub-agent completes.
// Safe to call when no deadline is active (no-op).
func (e *Engine) ResumeDeadline() {
	e.dlMu.Lock()
	defer e.dlMu.Unlock()
	if e.dlBudget <= 0 {
		return
	}
	e.dlStarted = time.Now()
	e.dlTimer = time.AfterFunc(e.dlBudget, func() {
		e.vm.Interrupt("execution timeout")
	})
}

// RunResult holds the output and any error from a JS execution.
type RunResult struct {
	Value string
	Error error
}

// Run executes JavaScript code within the engine with a pausable deadline.
// The timeout comes from the context; while agent.result() blocks, the deadline
// is paused so sub-agent execution time doesn't count against the parent.
func (e *Engine) Run(ctx context.Context, code string) *RunResult {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Determine timeout budget from context deadline
	budget := time.Duration(0)
	if deadline, ok := ctx.Deadline(); ok {
		budget = time.Until(deadline)
		if budget <= 0 {
			return &RunResult{Error: context.DeadlineExceeded}
		}
	}

	// Set up pausable deadline timer — interrupts the VM when budget expires
	e.dlMu.Lock()
	e.dlBudget = budget
	e.dlStarted = time.Now()
	if budget > 0 {
		e.dlTimer = time.AfterFunc(budget, func() {
			e.vm.Interrupt("execution timeout")
			if e.browserCleanup != nil {
				e.browserCleanup()
			}
		})
	}
	e.dlMu.Unlock()

	// Store execution context so browser bridge can use it
	e.ctxMu.Lock()
	e.execCtx = ctx
	e.ctxMu.Unlock()
	defer func() {
		e.ctxMu.Lock()
		e.execCtx = nil
		e.ctxMu.Unlock()
	}()

	// Clean up deadline state when done
	defer func() {
		e.dlMu.Lock()
		if e.dlTimer != nil {
			e.dlTimer.Stop()
			e.dlTimer = nil
		}
		e.dlBudget = 0
		e.dlMu.Unlock()
	}()

	return e.runInVM(ctx, false, func(ch chan<- *RunResult) {
		val, err := e.vm.RunString("(function(){" + code + "\n})()")
		if err != nil {
			ch <- &RunResult{Error: fmt.Errorf("js error: %s", cleanJSError(err.Error()))}
			return
		}

		result := ""
		if val != nil && !goja.IsUndefined(val) && !goja.IsNull(val) {
			result = val.String()
		}
		ch <- &RunResult{Value: result}
	})
}

// Cleanup releases engine-level resources.
// NOTE: Does NOT cleanup the executor — that is owned by main and cleaned up separately.
func (e *Engine) Cleanup() error {
	// Cancel all running task.run() child VMs
	e.taskMu.Lock()
	children := make([]*Engine, len(e.taskChildren))
	copy(children, e.taskChildren)
	e.taskMu.Unlock()
	for _, c := range children {
		c.Cleanup() //nolint:errcheck
	}

	e.engineCancel()
	if e.browserCleanup != nil {
		e.browserCleanup()
	}
	if e.dbPool != nil {
		e.dbPool.CloseAll()
	}
	if e.blobPool != nil {
		e.blobPool.CloseAll()
	}
	return nil
}

// readModuleFile looks for a module in a base directory with the following priority:
//  1. baseDir/module-name.js         (explicit .js)
//  2. baseDir/module-name            (exact match, no ext)
//  3. baseDir/module-name/index.js   (folder module)
//
// If modID already ends in .js, only option 1 is tried.
func readModuleFile(baseDir, modID string) ([]byte, bool) {
	tryPaths := []string{
		filepath.Join(baseDir, modID),             // exact or already has .js
		filepath.Join(baseDir, modID+".js"),       // append .js
		filepath.Join(baseDir, modID, "index.js"), // folder/index.js
	}
	// Deduplicate: if modID already ends in .js avoid trying modID.js.js
	if strings.HasSuffix(modID, ".js") {
		tryPaths = tryPaths[:1] // only exact
	}
	for _, p := range tryPaths {
		info, err := os.Stat(p)
		if err != nil || info.IsDir() {
			continue
		}
		data, err := os.ReadFile(p)
		if err == nil {
			return data, true
		}
	}
	return nil, false
}

// cleanJSError strips the goja JS call stack suffix from an error string.
// goja appends " at <location> (native)" or multiline "\n    at <frame>" to errors
// thrown from Go, which leaks internal Go package paths into user-visible messages.
// Also strips the "GoError: " prefix that goja wraps around Go errors.
func cleanJSError(msg string) string {
	// Multiline goja stack: "Error message\n    at ..."
	if idx := strings.Index(msg, "\n    at "); idx >= 0 {
		msg = strings.TrimSpace(msg[:idx])
	}
	// Single-line native frames — strip everything from " at X/" onward.
	for _, marker := range []string{" at github.com/", " at altclaw.ai/", " at native"} {
		if idx := strings.Index(msg, marker); idx >= 0 {
			msg = strings.TrimSpace(msg[:idx])
		}
	}
	// Strip "GoError: " prefix added by goja when wrapping Go errors into JS.
	msg = strings.TrimPrefix(msg, "GoError: ")
	return msg
}
