// Package serverjs handles server-side JavaScript execution for .server.js files
// in the public directory. It creates a Goja engine per request, loads the script
// as a CommonJS module, and calls the exported handler function with req/res objects
// built from the HTTP request/response.
package serverjs

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"altclaw.ai/internal/bridge"
	"altclaw.ai/internal/config"
	"altclaw.ai/internal/cron"
	"altclaw.ai/internal/engine"
	"altclaw.ai/internal/executor"
	"github.com/dop251/goja"
)

// Handler serves .server.js files from the public directory.
type Handler struct {
	Store     *config.Store
	Workspace string
	PublicDir string // resolved absolute path to public dir
	Exec      executor.Executor
	CronMgr   *cron.Manager
	Version   string
	Timeout   time.Duration
	ExecType  string // resolved executor type (e.g. "docker", "podman", "local")
	LogBuf    *bridge.LogBuffer

	// AgentRunner is optional — if set, the agent bridge is registered.
	AgentRunner bridge.SubAgentRunner

	// OnBroadcast is optional — for SSE event broadcasting.
	OnBroadcast func([]byte)

	// Sticky chat map: endpoint path → chatID for agent.run() calls
	chatsMu sync.Mutex
	chats   map[string]int64

	// Workspace for store operations
	Ws *config.Workspace
}

// routeCacheEntry stores a resolved route with a TTL.
type routeCacheEntry struct {
	scriptPath string
	params     map[string]string
	ok         bool
	expires    time.Time
}

const routeCacheMax = 64
const routeCacheTTL = 1 * time.Minute

// routeCache is a simple bounded LRU cache for resolved routes.
type routeCache struct {
	mu      sync.Mutex
	entries map[string]routeCacheEntry
	order   []string // access-ordered keys for LRU eviction
}

func newRouteCache() *routeCache {
	return &routeCache{entries: make(map[string]routeCacheEntry)}
}

func (c *routeCache) get(key string) (routeCacheEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok || time.Now().After(e.expires) {
		if ok {
			delete(c.entries, key)
		}
		return routeCacheEntry{}, false
	}
	// Move to end (most recently used)
	c.touch(key)
	return e, true
}

func (c *routeCache) put(key string, e routeCacheEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.entries[key]; exists {
		c.touch(key)
	} else {
		// Evict oldest if at capacity
		for len(c.entries) >= routeCacheMax && len(c.order) > 0 {
			oldest := c.order[0]
			c.order = c.order[1:]
			delete(c.entries, oldest)
		}
		c.order = append(c.order, key)
	}
	e.expires = time.Now().Add(routeCacheTTL)
	c.entries[key] = e
}

func (c *routeCache) touch(key string) {
	for i, k := range c.order {
		if k == key {
			c.order = append(c.order[:i], c.order[i+1:]...)
			c.order = append(c.order, key)
			return
		}
	}
}

// NewHandler creates a new server-side JS handler.
func NewHandler(store *config.Store, workspace, publicDir string, exec executor.Executor, cronMgr *cron.Manager, logBuf *bridge.LogBuffer, version string, ws *config.Workspace) *Handler {
	return &Handler{
		Store:     store,
		Workspace: workspace,
		PublicDir: publicDir,
		Exec:      exec,
		CronMgr:   cronMgr,
		LogBuf:    logBuf,
		Version:   version,
		Timeout:   ws.TimeoutFor("serverjs"),
		chats:     make(map[string]int64),
		Ws:        ws,
	}
}

// ResolveRoute finds a .server.js file for the given request path, supporting
// exact matches, dynamic segments ([param]), and catch-all segments ([...param]).
// Results are cached with a 1-minute TTL LRU cache.
//
// Priority: exact match > dynamic [param] > catch-all [...param]
func ResolveRoute(publicDir, reqPath string) (scriptPath string, params map[string]string, ok bool) {
	// Use package-level cache
	cacheKey := publicDir + "\x00" + reqPath
	if e, hit := globalRouteCache.get(cacheKey); hit {
		if e.ok {
			// Return a copy of params to avoid mutation across requests
			cp := make(map[string]string, len(e.params))
			for k, v := range e.params {
				cp[k] = v
			}
			return e.scriptPath, cp, true
		}
		return "", nil, false
	}

	segments := splitPath(reqPath)
	result := make(map[string]string)
	script, found := resolveSegments(publicDir, segments, 0, result)

	// Cache the result
	e := routeCacheEntry{scriptPath: script, ok: found}
	if found {
		cp := make(map[string]string, len(result))
		for k, v := range result {
			cp[k] = v
		}
		e.params = cp
	}
	globalRouteCache.put(cacheKey, e)

	if found {
		return script, result, true
	}
	return "", nil, false
}

var globalRouteCache = newRouteCache()

// splitPath splits a URL path into non-empty segments.
func splitPath(p string) []string {
	p = strings.TrimPrefix(p, "/")
	if p == "" {
		return nil
	}
	return strings.Split(p, "/")
}

// resolveSegments recursively walks the directory tree to find a matching .server.js file.
// It tries matches in priority order: exact > dynamic [param] > catch-all [...param].
func resolveSegments(dir string, segments []string, idx int, params map[string]string) (string, bool) {
	// Base case: consumed all segments — look for an index.server.js or a direct .server.js
	if idx >= len(segments) {
		// Try exact: dir/index.server.js (for paths that end at a directory)
		if script := filepath.Join(dir, "index.server.js"); fileExists(script) {
			return script, true
		}
		return "", false
	}

	seg := segments[idx]
	isLast := idx == len(segments)-1

	type candidate struct {
		priority int // lower = higher priority
		resolver func() (string, bool)
	}
	var candidates []candidate

	// Priority 1: Exact match
	if isLast {
		// Try exact file: dir/seg.server.js
		exactFile := filepath.Join(dir, seg+".server.js")
		candidates = append(candidates, candidate{0, func() (string, bool) {
			if fileExists(exactFile) {
				return exactFile, true
			}
			return "", false
		}})
	}
	// Try exact directory: dir/seg/ then recurse
	exactDir := filepath.Join(dir, seg)
	candidates = append(candidates, candidate{1, func() (string, bool) {
		if dirExists(exactDir) {
			return resolveSegments(exactDir, segments, idx+1, params)
		}
		return "", false
	}})

	// Priority 2: Dynamic segment [param]
	dynDirs, dynFiles := listDynamic(dir)
	for _, d := range dynDirs {
		paramName := d.param
		dirPath := d.path
		candidates = append(candidates, candidate{2, func() (string, bool) {
			old, existed := params[paramName]
			params[paramName] = seg
			if script, ok := resolveSegments(dirPath, segments, idx+1, params); ok {
				return script, true
			}
			// Backtrack
			if existed {
				params[paramName] = old
			} else {
				delete(params, paramName)
			}
			return "", false
		}})
	}
	if isLast {
		for _, f := range dynFiles {
			paramName := f.param
			filePath := f.path
			candidates = append(candidates, candidate{2, func() (string, bool) {
				params[paramName] = seg
				return filePath, true
			}})
		}
	}

	// Priority 3: Catch-all [...param]
	catchAlls := listCatchAll(dir)
	for _, ca := range catchAlls {
		paramName := ca.param
		scriptPath := ca.path
		remaining := strings.Join(segments[idx:], "/")
		candidates = append(candidates, candidate{3, func() (string, bool) {
			params[paramName] = remaining
			return scriptPath, true
		}})
	}

	// Sort by priority and try each
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].priority < candidates[j].priority
	})
	for _, c := range candidates {
		if script, ok := c.resolver(); ok {
			return script, true
		}
	}

	return "", false
}

// dynamicEntry represents a [param] directory or file.
type dynamicEntry struct {
	param string
	path  string
}

// listDynamic returns [param] directories and [param].server.js files in a directory.
func listDynamic(dir string) (dirs []dynamicEntry, files []dynamicEntry) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, "[") && !strings.HasPrefix(name, "[...") {
			if e.IsDir() {
				// [param] directory
				paramName := strings.TrimSuffix(strings.TrimPrefix(name, "["), "]")
				if paramName != "" {
					dirs = append(dirs, dynamicEntry{paramName, filepath.Join(dir, name)})
				}
			} else if strings.HasSuffix(name, "].server.js") {
				// [param].server.js file
				paramName := strings.TrimSuffix(strings.TrimPrefix(name, "["), "].server.js")
				if paramName != "" {
					files = append(files, dynamicEntry{paramName, filepath.Join(dir, name)})
				}
			}
		}
	}
	return
}

// listCatchAll returns [...param].server.js files in a directory.
func listCatchAll(dir string) []dynamicEntry {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var result []dynamicEntry
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() && strings.HasPrefix(name, "[...") && strings.HasSuffix(name, "].server.js") {
			paramName := strings.TrimSuffix(strings.TrimPrefix(name, "[..."), "].server.js")
			if paramName != "" {
				result = append(result, dynamicEntry{paramName, filepath.Join(dir, name)})
			}
		}
	}
	return result
}

// fileExists checks if a file exists and is not a directory.
func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// dirExists checks if a path exists and is a directory.
func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// responseData holds the parsed result of a Response object returned from JS.
type responseData struct {
	statusCode int
	headers    map[string]string
	body       []byte
	filePath   string // for sendFile — resolved absolute path
	isRedirect bool
}

// registerResponse registers the global Response constructor and static methods on the VM.
// Response follows the Web Fetch API pattern:
//
//	new Response(body?, init?)          — body is string, init is {status, headers}
//	Response.json(data, init?)          — JSON convenience
//	Response.redirect(url, status?)     — Redirect convenience
//	Response.sendFile(path)             — File serving
func registerResponse(vm *goja.Runtime, workspace string) {
	// Response constructor: new Response(body?, init?)
	vm.Set("Response", func(call goja.ConstructorCall) *goja.Object {
		obj := call.This
		obj.Set("__type", "Response")

		// Default values
		obj.Set("status", 200)
		obj.Set("body", "")

		headersObj := vm.NewObject()
		obj.Set("headers", headersObj)

		// Parse body argument
		if len(call.Arguments) > 0 && !goja.IsUndefined(call.Arguments[0]) && !goja.IsNull(call.Arguments[0]) {
			obj.Set("body", call.Arguments[0].String())
		}

		// Parse init argument {status, headers}
		if len(call.Arguments) > 1 && !goja.IsUndefined(call.Arguments[1]) && !goja.IsNull(call.Arguments[1]) {
			initObj := call.Arguments[1].ToObject(vm)
			if s := initObj.Get("status"); s != nil && !goja.IsUndefined(s) {
				obj.Set("status", s.ToInteger())
			}
			if h := initObj.Get("headers"); h != nil && !goja.IsUndefined(h) && !goja.IsNull(h) {
				hObj := h.ToObject(vm)
				for _, k := range hObj.Keys() {
					headersObj.Set(k, hObj.Get(k).String())
				}
			}
		}

		return obj
	})

	// Response.json(data, init?) — static method
	respCtor := vm.Get("Response").ToObject(vm)
	respCtor.Set("json", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			panic(vm.ToValue("Response.json requires a data argument"))
		}
		data := call.Arguments[0].Export()
		jsonBytes, err := json.Marshal(data)
		if err != nil {
			panic(vm.ToValue(fmt.Sprintf("Response.json: marshal error: %v", err)))
		}

		// Build init with JSON content-type, merge with user init if provided
		status := int64(200)
		headersObj := vm.NewObject()
		headersObj.Set("Content-Type", "application/json; charset=utf-8")
		if len(call.Arguments) > 1 && !goja.IsUndefined(call.Arguments[1]) && !goja.IsNull(call.Arguments[1]) {
			initObj := call.Arguments[1].ToObject(vm)
			if s := initObj.Get("status"); s != nil && !goja.IsUndefined(s) {
				status = s.ToInteger()
			}
			if h := initObj.Get("headers"); h != nil && !goja.IsUndefined(h) && !goja.IsNull(h) {
				hObj := h.ToObject(vm)
				for _, k := range hObj.Keys() {
					headersObj.Set(k, hObj.Get(k).String())
				}
			}
		}

		obj := vm.NewObject()
		obj.Set("__type", "Response")
		obj.Set("status", status)
		obj.Set("body", string(jsonBytes))
		obj.Set("headers", headersObj)
		return obj
	})

	// Response.redirect(url, status?) — static method
	respCtor.Set("redirect", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			panic(vm.ToValue("Response.redirect requires a URL"))
		}
		url := call.Arguments[0].String()
		code := int64(302)
		if len(call.Arguments) > 1 && !goja.IsUndefined(call.Arguments[1]) {
			code = call.Arguments[1].ToInteger()
		}

		headersObj := vm.NewObject()
		headersObj.Set("Location", url)

		obj := vm.NewObject()
		obj.Set("__type", "Response")
		obj.Set("status", code)
		obj.Set("body", "")
		obj.Set("headers", headersObj)
		obj.Set("__redirect", true)
		return obj
	})

	// Response.sendFile(path) — static method
	respCtor.Set("sendFile", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			panic(vm.ToValue("Response.sendFile requires a path argument"))
		}
		relPath := call.Arguments[0].String()
		absPath, err := bridge.SanitizePath(workspace, relPath)
		if err != nil {
			panic(vm.ToValue(fmt.Sprintf("Response.sendFile: %v", err)))
		}

		headersObj := vm.NewObject()
		headersObj.Set("Content-Type", mimeFromExt(filepath.Ext(absPath)))

		obj := vm.NewObject()
		obj.Set("__type", "Response")
		obj.Set("status", 200)
		obj.Set("body", "")
		obj.Set("headers", headersObj)
		obj.Set("__filePath", absPath)
		return obj
	})
}

// extractResponse reads a Response object (or auto-detects) from a goja return value.
func extractResponse(vm *goja.Runtime, val goja.Value) *responseData {
	if val == nil || goja.IsUndefined(val) || goja.IsNull(val) {
		return &responseData{statusCode: 204}
	}

	// Check if it's a Response object (has __type === "Response")
	if obj := val.ToObject(vm); obj != nil {
		typeVal := obj.Get("__type")
		if typeVal != nil && typeVal.String() == "Response" {
			rd := &responseData{
				statusCode: 200,
				headers:    make(map[string]string),
			}

			if s := obj.Get("status"); s != nil && !goja.IsUndefined(s) {
				rd.statusCode = int(s.ToInteger())
			}

			if h := obj.Get("headers"); h != nil && !goja.IsUndefined(h) && !goja.IsNull(h) {
				hObj := h.ToObject(vm)
				for _, k := range hObj.Keys() {
					v := hObj.Get(k)
					if v != nil {
						rd.headers[k] = v.String()
					}
				}
			}

			// Check for sendFile
			if fp := obj.Get("__filePath"); fp != nil && !goja.IsUndefined(fp) {
				rd.filePath = fp.String()
				return rd
			}

			// Check for redirect
			if redir := obj.Get("__redirect"); redir != nil && redir.ToBoolean() {
				rd.isRedirect = true
				return rd
			}

			if b := obj.Get("body"); b != nil && !goja.IsUndefined(b) {
				rd.body = []byte(b.String())
			}
			return rd
		}
	}

	// Auto-detection for bare return values
	exported := val.Export()

	switch v := exported.(type) {
	case string:
		rd := &responseData{statusCode: 200, headers: make(map[string]string)}
		rd.body = []byte(v)
		trimmed := strings.TrimSpace(v)
		if strings.HasPrefix(trimmed, "<") {
			rd.headers["Content-Type"] = "text/html; charset=utf-8"
		} else {
			rd.headers["Content-Type"] = "text/plain; charset=utf-8"
		}
		return rd
	default:
		// Object, array, number, bool → JSON
		jsonBytes, err := json.Marshal(v)
		if err != nil {
			jsonBytes = []byte(fmt.Sprintf("%v", v))
		}
		rd := &responseData{statusCode: 200, headers: make(map[string]string)}
		rd.body = jsonBytes
		rd.headers["Content-Type"] = "application/json; charset=utf-8"
		return rd
	}
}

// ServeHTTP handles a request for a .server.js file.
// scriptPath is the absolute path to the .server.js file.
// reqPath is the URL path that was matched (for process.script and sticky chat).
// params contains any dynamic route parameters extracted by ResolveRoute.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request, scriptPath, reqPath string, params map[string]string) {

	ctx, cancel := context.WithTimeout(r.Context(), h.Timeout)
	defer cancel()

	// Create engine with all bridges
	eng := h.createEngine()
	defer eng.Cleanup()

	// Set process global — include executor info for scripts
	relPubDir := h.PublicDir
	if rel, err := filepath.Rel(h.Workspace, h.PublicDir); err == nil {
		relPubDir = rel
	}
	envExtra := map[string]string{
		"PUBLIC_DIR": relPubDir,
		"HOSTNAME":   h.Ws.TunnelHost,
	}
	if h.ExecType != "" {
		envExtra["EXECUTOR"] = h.ExecType
	}
	eng.SetProcess("server", h.Version, reqPath, envExtra)

	// Wire agent bridge if available
	if h.AgentRunner != nil {
		runner := &stickyAgentRunner{
			runner:   h.AgentRunner,
			handler:  h,
			endpoint: reqPath,
		}
		eng.SetAgentRunner(runner)
	}

	vm := eng.VM()

	// Register global Response constructor
	registerResponse(vm, h.Workspace)

	// Load module via require() from Go — returns module.exports.
	exports, err := eng.Require(scriptPath)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		slog.Error("serverjs: require", "path", scriptPath, "error", err)
		return
	}

	// Assert exports is a function: module.exports = function(req) { ... }
	handlerFn, ok := goja.AssertFunction(exports)
	if !ok {
		errMsg := "server.js must export a function: module.exports = function(req) { ... } — got an object/value instead"
		http.Error(w, errMsg, http.StatusInternalServerError)
		slog.Error("serverjs: "+errMsg, "path", scriptPath)
		return
	}

	// Build req object
	reqObj := buildReq(vm, r, params)

	// Call handler(req) with timeout enforcement.
	type callResult struct {
		val goja.Value
		err error
	}
	ch := make(chan callResult, 1)

	// Set up timeout: interrupt VM when context expires
	go func() {
		<-ctx.Done()
		vm.Interrupt("execution timeout")
	}()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				ch <- callResult{err: fmt.Errorf("%v", r)}
			}
		}()
		val, err := handlerFn(goja.Undefined(), reqObj)
		ch <- callResult{val: val, err: err}
	}()

	result := <-ch

	if result.err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		slog.Error("serverjs: execution error", "path", reqPath, "error", result.err)
		return
	}

	// Extract response from return value
	rd := extractResponse(vm, result.val)

	// Write headers
	for k, v := range rd.headers {
		w.Header().Set(k, v)
	}

	// Handle redirect
	if rd.isRedirect {
		w.WriteHeader(rd.statusCode)
		return
	}

	// Handle sendFile
	if rd.filePath != "" {
		data, err := os.ReadFile(rd.filePath)
		if err != nil {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			slog.Error("serverjs: sendFile", "path", rd.filePath, "error", err)
			return
		}
		w.WriteHeader(rd.statusCode)
		w.Write(data)
		return
	}

	// Write body
	w.WriteHeader(rd.statusCode)
	if len(rd.body) > 0 {
		w.Write(rd.body)
	}
}

// createEngine creates a fresh engine with all bridges registered.
func (h *Handler) createEngine() *engine.Engine {
	ui := &serverUI{}
	eng := engine.New(h.Ws, h.Exec, ui, "", h.Store, h.LogBuf).
		WithCronManager(h.CronMgr, func() int64 { return 0 })
	if h.OnBroadcast != nil {
		eng.OnBroadcast = h.OnBroadcast
	}
	return eng
}

// buildReq creates the req object from an http.Request.
func buildReq(vm *goja.Runtime, r *http.Request, params map[string]string) *goja.Object {
	req := vm.NewObject()

	// Set route params (from dynamic segments like [id] and [...path])
	paramsObj := vm.NewObject()
	for k, v := range params {
		paramsObj.Set(k, v)
	}
	req.Set("params", paramsObj)

	req.Set("method", r.Method)
	req.Set("url", r.RequestURI)
	req.Set("path", r.URL.Path)

	// Parse query params
	query := vm.NewObject()
	for key, vals := range r.URL.Query() {
		if len(vals) == 1 {
			query.Set(key, vals[0])
		} else {
			ivals := make([]interface{}, len(vals))
			for i, v := range vals {
				ivals[i] = v
			}
			query.Set(key, ivals)
		}
	}
	req.Set("query", query)

	// Headers (lowercase keys)
	headers := vm.NewObject()
	for key, vals := range r.Header {
		headers.Set(strings.ToLower(key), strings.Join(vals, ", "))
	}
	req.Set("headers", headers)

	// Body (read for POST/PUT/PATCH/DELETE)
	// Auto-parsed based on Content-Type: JSON → object, form-urlencoded → object, else → raw string
	if r.Body != nil && (r.Method == "POST" || r.Method == "PUT" || r.Method == "PATCH" || r.Method == "DELETE") {
		bodyBytes, err := io.ReadAll(io.LimitReader(r.Body, 10*1024*1024)) // 10MB limit
		r.Body.Close()
		if err == nil && len(bodyBytes) > 0 {
			ct := r.Header.Get("Content-Type")
			if strings.Contains(ct, "application/json") {
				// Parse JSON body into object
				var parsed interface{}
				if json.Unmarshal(bodyBytes, &parsed) == nil {
					req.Set("body", parsed)
				} else {
					req.Set("body", string(bodyBytes))
				}
			} else if strings.Contains(ct, "application/x-www-form-urlencoded") {
				// Parse form-urlencoded body into object
				formObj := vm.NewObject()
				pairs := strings.Split(string(bodyBytes), "&")
				for _, pair := range pairs {
					kv := strings.SplitN(pair, "=", 2)
					if len(kv) == 2 {
						key, _ := url.QueryUnescape(kv[0])
						val, _ := url.QueryUnescape(kv[1])
						formObj.Set(key, val)
					}
				}
				req.Set("body", formObj)
			} else {
				req.Set("body", string(bodyBytes))
			}
		} else {
			req.Set("body", "")
		}
	} else {
		req.Set("body", "")
	}

	return req
}

// stickyAgentRunner wraps a SubAgentRunner to provide sticky chat per endpoint.
type stickyAgentRunner struct {
	runner   bridge.SubAgentRunner
	handler  *Handler
	endpoint string
}

func (s *stickyAgentRunner) RunSubAgent(ctx context.Context, task string) (string, error) {
	return s.runner.RunSubAgent(ctx, task)
}

func (s *stickyAgentRunner) RunSubAgentWith(ctx context.Context, task, providerName string) (string, error) {
	return s.runner.RunSubAgentWith(ctx, task, providerName)
}

// serverUI implements bridge.UIHandler for server-side scripts.
type serverUI struct{}

func (u *serverUI) Log(msg string) {
	slog.Info("serverjs", "log", msg)
}

func (u *serverUI) Ask(question string) string {
	return "" // no interactive user in server context
}

func (u *serverUI) Confirm(action, label, summary string, params map[string]any) string {
	return "no" // no interactive user in server context
}

// mimeFromExt returns a MIME type for common file extensions.
func mimeFromExt(ext string) string {
	ext = strings.ToLower(ext)
	switch ext {
	case ".html", ".htm":
		return "text/html; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".js":
		return "application/javascript; charset=utf-8"
	case ".json":
		return "application/json; charset=utf-8"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".svg":
		return "image/svg+xml"
	case ".pdf":
		return "application/pdf"
	case ".xml":
		return "application/xml"
	case ".txt", ".md", ".log", ".csv":
		return "text/plain; charset=utf-8"
	default:
		return "application/octet-stream"
	}
}
