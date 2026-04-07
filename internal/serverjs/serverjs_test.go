package serverjs

import (
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"altclaw.ai/internal/config"
)

func TestServeHTTP_JSON(t *testing.T) {
	workspace := t.TempDir()
	publicDir := filepath.Join(workspace, "public")
	os.MkdirAll(publicDir, 0755)

	// Write a .server.js that responds with JSON
	scriptPath := filepath.Join(publicDir, "hello.server.js")
	os.WriteFile(scriptPath, []byte(`
module.exports = function(req) {
	return Response.json({hello: "world", method: req.method})
}
`), 0644)

	h := NewHandler(nil, workspace, publicDir, nil, nil, nil, "test", &config.Workspace{Path: workspace})

	req := httptest.NewRequest("GET", "/hello", nil)
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req, scriptPath, "/hello", nil)

	resp := w.Result()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("expected JSON content type, got %q", ct)
	}
	bodyStr := string(body)
	if !strings.Contains(bodyStr, `"hello":"world"`) {
		t.Errorf("expected JSON with hello:world, got %q", bodyStr)
	}
	if !strings.Contains(bodyStr, `"method":"GET"`) {
		t.Errorf("expected method GET in response, got %q", bodyStr)
	}
}

func TestServeHTTP_PostBody(t *testing.T) {
	workspace := t.TempDir()
	publicDir := filepath.Join(workspace, "public")
	os.MkdirAll(publicDir, 0755)

	scriptPath := filepath.Join(publicDir, "echo.server.js")
	os.WriteFile(scriptPath, []byte(`
module.exports = function(req) {
	var body = req.json();
	return Response.json({received: body.name, method: req.method})
}
`), 0644)

	h := NewHandler(nil, workspace, publicDir, nil, nil, nil, "test", &config.Workspace{Path: workspace})

	req := httptest.NewRequest("POST", "/echo", strings.NewReader(`{"name":"altclaw"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req, scriptPath, "/echo", nil)

	resp := w.Result()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	bodyStr := string(body)
	if !strings.Contains(bodyStr, `"received":"altclaw"`) {
		t.Errorf("expected received:altclaw, got %q", bodyStr)
	}
}

func TestServeHTTP_FormBody(t *testing.T) {
	workspace := t.TempDir()
	publicDir := filepath.Join(workspace, "public")
	os.MkdirAll(publicDir, 0755)

	scriptPath := filepath.Join(publicDir, "form.server.js")
	os.WriteFile(scriptPath, []byte(`
module.exports = function(req) {
	var body = req.form();
	return Response.json({name: body.name, email: body.email})
}
`), 0644)

	h := NewHandler(nil, workspace, publicDir, nil, nil, nil, "test", &config.Workspace{Path: workspace})

	req := httptest.NewRequest("POST", "/form", strings.NewReader("name=John+Doe&email=john%40example.com"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req, scriptPath, "/form", nil)

	resp := w.Result()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	bodyStr := string(body)
	if !strings.Contains(bodyStr, `"name":"John Doe"`) {
		t.Errorf("expected name:John Doe, got %q", bodyStr)
	}
	if !strings.Contains(bodyStr, `"email":"john@example.com"`) {
		t.Errorf("expected decoded email, got %q", bodyStr)
	}
}

func TestServeHTTP_StatusAndHeaders(t *testing.T) {
	workspace := t.TempDir()
	publicDir := filepath.Join(workspace, "public")
	os.MkdirAll(publicDir, 0755)

	scriptPath := filepath.Join(publicDir, "custom.server.js")
	os.WriteFile(scriptPath, []byte(`
module.exports = function(req) {
	return Response.json({created: true}, {status: 201, headers: {"X-Custom": "test-value"}})
}
`), 0644)

	h := NewHandler(nil, workspace, publicDir, nil, nil, nil, "test", &config.Workspace{Path: workspace})

	req := httptest.NewRequest("POST", "/custom", nil)
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req, scriptPath, "/custom", nil)

	resp := w.Result()
	if resp.StatusCode != 201 {
		t.Errorf("expected 201, got %d", resp.StatusCode)
	}
	if v := resp.Header.Get("X-Custom"); v != "test-value" {
		t.Errorf("expected X-Custom: test-value, got %q", v)
	}
}

func TestServeHTTP_HTML(t *testing.T) {
	workspace := t.TempDir()
	publicDir := filepath.Join(workspace, "public")
	os.MkdirAll(publicDir, 0755)

	scriptPath := filepath.Join(publicDir, "page.server.js")
	os.WriteFile(scriptPath, []byte(`
module.exports = function(req) {
	return "<h1>Hello</h1>"
}
`), 0644)

	h := NewHandler(nil, workspace, publicDir, nil, nil, nil, "test", &config.Workspace{Path: workspace})

	req := httptest.NewRequest("GET", "/page", nil)
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req, scriptPath, "/page", nil)

	resp := w.Result()
	body, _ := io.ReadAll(resp.Body)

	if !strings.Contains(resp.Header.Get("Content-Type"), "text/html") {
		t.Errorf("expected HTML content type, got %q", resp.Header.Get("Content-Type"))
	}
	if string(body) != "<h1>Hello</h1>" {
		t.Errorf("expected <h1>Hello</h1>, got %q", string(body))
	}
}

func TestServeHTTP_QueryParams(t *testing.T) {
	workspace := t.TempDir()
	publicDir := filepath.Join(workspace, "public")
	os.MkdirAll(publicDir, 0755)

	scriptPath := filepath.Join(publicDir, "search.server.js")
	os.WriteFile(scriptPath, []byte(`
module.exports = function(req) {
	return Response.json({q: req.query.q, page: req.query.page})
}
`), 0644)

	h := NewHandler(nil, workspace, publicDir, nil, nil, nil, "test", &config.Workspace{Path: workspace})

	req := httptest.NewRequest("GET", "/search?q=test&page=2", nil)
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req, scriptPath, "/search", nil)

	resp := w.Result()
	body, _ := io.ReadAll(resp.Body)

	bodyStr := string(body)
	if !strings.Contains(bodyStr, `"q":"test"`) {
		t.Errorf("expected q:test, got %q", bodyStr)
	}
	if !strings.Contains(bodyStr, `"page":"2"`) {
		t.Errorf("expected page:2, got %q", bodyStr)
	}
}

func TestServeHTTP_FSBridge(t *testing.T) {
	workspace := t.TempDir()
	publicDir := filepath.Join(workspace, "public")
	os.MkdirAll(publicDir, 0755)

	// Write data file in workspace
	os.WriteFile(filepath.Join(workspace, "data.txt"), []byte("hello from file"), 0644)

	scriptPath := filepath.Join(publicDir, "readfile.server.js")
	os.WriteFile(scriptPath, []byte(`
module.exports = function(req) {
	var content = fs.read("data.txt")
	return content
}
`), 0644)

	h := NewHandler(nil, workspace, publicDir, nil, nil, nil, "test", &config.Workspace{Path: workspace})

	req := httptest.NewRequest("GET", "/readfile", nil)
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req, scriptPath, "/readfile", nil)

	resp := w.Result()
	body, _ := io.ReadAll(resp.Body)

	if string(body) != "hello from file" {
		t.Errorf("expected 'hello from file', got %q", string(body))
	}
}

func TestServeHTTP_ProcessGlobal(t *testing.T) {
	workspace := t.TempDir()
	publicDir := filepath.Join(workspace, "public")
	os.MkdirAll(publicDir, 0755)

	scriptPath := filepath.Join(publicDir, "env.server.js")
	os.WriteFile(scriptPath, []byte(`
module.exports = function(req) {
	return Response.json({mode: process.env.CTX, version: process.version, script: process.script})
}
`), 0644)

	h := NewHandler(nil, workspace, publicDir, nil, nil, nil, "1.0.0", &config.Workspace{Path: workspace})

	req := httptest.NewRequest("GET", "/env", nil)
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req, scriptPath, "/env", nil)

	resp := w.Result()
	body, _ := io.ReadAll(resp.Body)

	bodyStr := string(body)
	if !strings.Contains(bodyStr, `"mode":"server"`) {
		t.Errorf("expected mode:server, got %q", bodyStr)
	}
	if !strings.Contains(bodyStr, `"version":"1.0.0"`) {
		t.Errorf("expected version:1.0.0, got %q", bodyStr)
	}
	if !strings.Contains(bodyStr, `"script":"/env"`) {
		t.Errorf("expected script:/env, got %q", bodyStr)
	}
}

func TestServeHTTP_Redirect(t *testing.T) {
	workspace := t.TempDir()
	publicDir := filepath.Join(workspace, "public")
	os.MkdirAll(publicDir, 0755)

	scriptPath := filepath.Join(publicDir, "redir.server.js")
	os.WriteFile(scriptPath, []byte(`
module.exports = function(req) {
	return Response.redirect("/new-location", 301)
}
`), 0644)

	h := NewHandler(nil, workspace, publicDir, nil, nil, nil, "test", &config.Workspace{Path: workspace})

	req := httptest.NewRequest("GET", "/redir", nil)
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req, scriptPath, "/redir", nil)

	resp := w.Result()
	if resp.StatusCode != 301 {
		t.Errorf("expected 301, got %d", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/new-location" {
		t.Errorf("expected Location: /new-location, got %q", loc)
	}
}

func TestServeHTTP_NotAFunction(t *testing.T) {
	workspace := t.TempDir()
	publicDir := filepath.Join(workspace, "public")
	os.MkdirAll(publicDir, 0755)

	scriptPath := filepath.Join(publicDir, "bad.server.js")
	os.WriteFile(scriptPath, []byte(`
module.exports = {not: "a function"}
`), 0644)

	h := NewHandler(nil, workspace, publicDir, nil, nil, nil, "test", &config.Workspace{Path: workspace})

	req := httptest.NewRequest("GET", "/bad", nil)
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req, scriptPath, "/bad", nil)

	resp := w.Result()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("expected 500 for non-function export, got %d", resp.StatusCode)
	}
}

func TestServeHTTP_SendFile(t *testing.T) {
	workspace := t.TempDir()
	publicDir := filepath.Join(workspace, "public")
	os.MkdirAll(publicDir, 0755)

	// Create a file to serve
	os.MkdirAll(filepath.Join(workspace, "assets"), 0755)
	os.WriteFile(filepath.Join(workspace, "assets", "test.txt"), []byte("file content"), 0644)

	scriptPath := filepath.Join(publicDir, "download.server.js")
	os.WriteFile(scriptPath, []byte(`
module.exports = function(req) {
	return Response.sendFile("assets/test.txt")
}
`), 0644)

	h := NewHandler(nil, workspace, publicDir, nil, nil, nil, "test", &config.Workspace{Path: workspace})

	req := httptest.NewRequest("GET", "/download", nil)
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req, scriptPath, "/download", nil)

	resp := w.Result()
	body, _ := io.ReadAll(resp.Body)

	if string(body) != "file content" {
		t.Errorf("expected 'file content', got %q", string(body))
	}
}

func TestServeHTTP_ReturnObject(t *testing.T) {
	workspace := t.TempDir()
	publicDir := filepath.Join(workspace, "public")
	os.MkdirAll(publicDir, 0755)

	scriptPath := filepath.Join(publicDir, "api.server.js")
	os.WriteFile(scriptPath, []byte(`
module.exports = function(req) {
	var body = req.json();
	return {message: "created", name: body.name}
}
`), 0644)

	h := NewHandler(nil, workspace, publicDir, nil, nil, nil, "test", &config.Workspace{Path: workspace})

	req := httptest.NewRequest("POST", "/api", strings.NewReader(`{"name":"test"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req, scriptPath, "/api", nil)

	resp := w.Result()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("expected JSON content type for auto-detected object, got %q", ct)
	}
	bodyStr := string(body)
	if !strings.Contains(bodyStr, `"message":"created"`) {
		t.Errorf("expected message:created, got %q", bodyStr)
	}
	if !strings.Contains(bodyStr, `"name":"test"`) {
		t.Errorf("expected name:test, got %q", bodyStr)
	}
}

// --- Auto-detection Tests ---

func TestServeHTTP_AutoDetect_PlainString(t *testing.T) {
	workspace := t.TempDir()
	publicDir := filepath.Join(workspace, "public")
	os.MkdirAll(publicDir, 0755)

	scriptPath := filepath.Join(publicDir, "plain.server.js")
	os.WriteFile(scriptPath, []byte(`
module.exports = function(req) {
	return "just plain text"
}
`), 0644)

	h := NewHandler(nil, workspace, publicDir, nil, nil, nil, "test", &config.Workspace{Path: workspace})

	req := httptest.NewRequest("GET", "/plain", nil)
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req, scriptPath, "/plain", nil)

	resp := w.Result()
	body, _ := io.ReadAll(resp.Body)

	if !strings.Contains(resp.Header.Get("Content-Type"), "text/plain") {
		t.Errorf("expected text/plain content type, got %q", resp.Header.Get("Content-Type"))
	}
	if string(body) != "just plain text" {
		t.Errorf("expected 'just plain text', got %q", string(body))
	}
}

func TestServeHTTP_AutoDetect_HTMLString(t *testing.T) {
	workspace := t.TempDir()
	publicDir := filepath.Join(workspace, "public")
	os.MkdirAll(publicDir, 0755)

	scriptPath := filepath.Join(publicDir, "autohtml.server.js")
	os.WriteFile(scriptPath, []byte(`
module.exports = function(req) {
	return "<div>Auto-detected HTML</div>"
}
`), 0644)

	h := NewHandler(nil, workspace, publicDir, nil, nil, nil, "test", &config.Workspace{Path: workspace})

	req := httptest.NewRequest("GET", "/autohtml", nil)
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req, scriptPath, "/autohtml", nil)

	resp := w.Result()
	body, _ := io.ReadAll(resp.Body)

	if !strings.Contains(resp.Header.Get("Content-Type"), "text/html") {
		t.Errorf("expected text/html content type, got %q", resp.Header.Get("Content-Type"))
	}
	if string(body) != "<div>Auto-detected HTML</div>" {
		t.Errorf("expected auto-detected HTML body, got %q", string(body))
	}
}

func TestServeHTTP_NoReturn_204(t *testing.T) {
	workspace := t.TempDir()
	publicDir := filepath.Join(workspace, "public")
	os.MkdirAll(publicDir, 0755)

	scriptPath := filepath.Join(publicDir, "noop.server.js")
	os.WriteFile(scriptPath, []byte(`
module.exports = function(req) {
	// does work but returns nothing
}
`), 0644)

	h := NewHandler(nil, workspace, publicDir, nil, nil, nil, "test", &config.Workspace{Path: workspace})

	req := httptest.NewRequest("POST", "/noop", nil)
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req, scriptPath, "/noop", nil)

	resp := w.Result()
	if resp.StatusCode != 204 {
		t.Errorf("expected 204 for no return value, got %d", resp.StatusCode)
	}
}

func TestServeHTTP_ResponseConstructor(t *testing.T) {
	workspace := t.TempDir()
	publicDir := filepath.Join(workspace, "public")
	os.MkdirAll(publicDir, 0755)

	scriptPath := filepath.Join(publicDir, "newresp.server.js")
	os.WriteFile(scriptPath, []byte(`
module.exports = function(req) {
	return new Response("custom body", {status: 202, headers: {"X-Foo": "bar"}})
}
`), 0644)

	h := NewHandler(nil, workspace, publicDir, nil, nil, nil, "test", &config.Workspace{Path: workspace})

	req := httptest.NewRequest("GET", "/newresp", nil)
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req, scriptPath, "/newresp", nil)

	resp := w.Result()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 202 {
		t.Errorf("expected 202, got %d", resp.StatusCode)
	}
	if v := resp.Header.Get("X-Foo"); v != "bar" {
		t.Errorf("expected X-Foo: bar, got %q", v)
	}
	if string(body) != "custom body" {
		t.Errorf("expected 'custom body', got %q", string(body))
	}
}

// --- ResolveRoute Tests ---

func TestResolveRoute_ExactMatch(t *testing.T) {
	pub := t.TempDir()
	os.MkdirAll(filepath.Join(pub, "api"), 0755)
	os.WriteFile(filepath.Join(pub, "api", "users.server.js"), []byte(""), 0644)

	script, params, ok := ResolveRoute(pub, "api/users")
	if !ok {
		t.Fatal("expected match")
	}
	if filepath.Base(script) != "users.server.js" {
		t.Errorf("expected users.server.js, got %s", filepath.Base(script))
	}
	if len(params) != 0 {
		t.Errorf("expected no params, got %v", params)
	}
}

func TestResolveRoute_IndexServerJS(t *testing.T) {
	pub := t.TempDir()
	os.MkdirAll(filepath.Join(pub, "api"), 0755)
	os.WriteFile(filepath.Join(pub, "api", "index.server.js"), []byte(""), 0644)

	script, _, ok := ResolveRoute(pub, "api")
	if !ok {
		t.Fatal("expected match")
	}
	if filepath.Base(script) != "index.server.js" {
		t.Errorf("expected index.server.js, got %s", filepath.Base(script))
	}
}

func TestResolveRoute_DynamicSegmentFile(t *testing.T) {
	pub := t.TempDir()
	os.MkdirAll(filepath.Join(pub, "api", "users"), 0755)
	os.WriteFile(filepath.Join(pub, "api", "users", "[id].server.js"), []byte(""), 0644)

	script, params, ok := ResolveRoute(pub, "api/users/123")
	if !ok {
		t.Fatal("expected match")
	}
	if filepath.Base(script) != "[id].server.js" {
		t.Errorf("expected [id].server.js, got %s", filepath.Base(script))
	}
	if params["id"] != "123" {
		t.Errorf("expected id=123, got %v", params)
	}
}

func TestResolveRoute_DynamicSegmentDir(t *testing.T) {
	pub := t.TempDir()
	os.MkdirAll(filepath.Join(pub, "api", "users", "[id]"), 0755)
	os.WriteFile(filepath.Join(pub, "api", "users", "[id]", "posts.server.js"), []byte(""), 0644)

	script, params, ok := ResolveRoute(pub, "api/users/42/posts")
	if !ok {
		t.Fatal("expected match")
	}
	if filepath.Base(script) != "posts.server.js" {
		t.Errorf("expected posts.server.js, got %s", filepath.Base(script))
	}
	if params["id"] != "42" {
		t.Errorf("expected id=42, got %v", params)
	}
}

func TestResolveRoute_CatchAll(t *testing.T) {
	pub := t.TempDir()
	os.MkdirAll(filepath.Join(pub, "api"), 0755)
	os.WriteFile(filepath.Join(pub, "api", "[...path].server.js"), []byte(""), 0644)

	script, params, ok := ResolveRoute(pub, "api/foo/bar/baz")
	if !ok {
		t.Fatal("expected match")
	}
	if filepath.Base(script) != "[...path].server.js" {
		t.Errorf("expected [...path].server.js, got %s", filepath.Base(script))
	}
	if params["path"] != "foo/bar/baz" {
		t.Errorf("expected path=foo/bar/baz, got %v", params)
	}
}

func TestResolveRoute_Priority_ExactOverDynamic(t *testing.T) {
	pub := t.TempDir()
	os.MkdirAll(filepath.Join(pub, "api", "users"), 0755)
	os.WriteFile(filepath.Join(pub, "api", "users", "settings.server.js"), []byte(""), 0644)
	os.WriteFile(filepath.Join(pub, "api", "users", "[id].server.js"), []byte(""), 0644)

	script, params, ok := ResolveRoute(pub, "api/users/settings")
	if !ok {
		t.Fatal("expected match")
	}
	if filepath.Base(script) != "settings.server.js" {
		t.Errorf("expected exact match settings.server.js, got %s", filepath.Base(script))
	}
	if len(params) != 0 {
		t.Errorf("expected no params for exact match, got %v", params)
	}
}

func TestResolveRoute_Priority_DynamicOverCatchAll(t *testing.T) {
	pub := t.TempDir()
	os.MkdirAll(filepath.Join(pub, "api", "users"), 0755)
	os.WriteFile(filepath.Join(pub, "api", "users", "[id].server.js"), []byte(""), 0644)
	os.WriteFile(filepath.Join(pub, "api", "users", "[...path].server.js"), []byte(""), 0644)

	script, params, ok := ResolveRoute(pub, "api/users/456")
	if !ok {
		t.Fatal("expected match")
	}
	if filepath.Base(script) != "[id].server.js" {
		t.Errorf("expected dynamic [id] over catch-all, got %s", filepath.Base(script))
	}
	if params["id"] != "456" {
		t.Errorf("expected id=456, got %v", params)
	}
}

func TestResolveRoute_NestedDynamic(t *testing.T) {
	pub := t.TempDir()
	os.MkdirAll(filepath.Join(pub, "api", "users", "[id]", "posts"), 0755)
	os.WriteFile(filepath.Join(pub, "api", "users", "[id]", "posts", "[postId].server.js"), []byte(""), 0644)

	script, params, ok := ResolveRoute(pub, "api/users/7/posts/99")
	if !ok {
		t.Fatal("expected match")
	}
	if filepath.Base(script) != "[postId].server.js" {
		t.Errorf("expected [postId].server.js, got %s", filepath.Base(script))
	}
	if params["id"] != "7" || params["postId"] != "99" {
		t.Errorf("expected id=7, postId=99, got %v", params)
	}
}

func TestResolveRoute_NoMatch(t *testing.T) {
	pub := t.TempDir()
	os.MkdirAll(filepath.Join(pub, "api"), 0755)

	_, _, ok := ResolveRoute(pub, "api/nonexistent")
	if ok {
		t.Error("expected no match")
	}
}

func TestResolveRoute_CatchAllWithDynamicPrefix(t *testing.T) {
	pub := t.TempDir()
	os.MkdirAll(filepath.Join(pub, "api", "users", "[id]"), 0755)
	os.WriteFile(filepath.Join(pub, "api", "users", "[id]", "[...rest].server.js"), []byte(""), 0644)

	script, params, ok := ResolveRoute(pub, "api/users/5/foo/bar")
	if !ok {
		t.Fatal("expected match")
	}
	if filepath.Base(script) != "[...rest].server.js" {
		t.Errorf("expected [...rest].server.js, got %s", filepath.Base(script))
	}
	if params["id"] != "5" {
		t.Errorf("expected id=5, got %v", params)
	}
	if params["rest"] != "foo/bar" {
		t.Errorf("expected rest=foo/bar, got %v", params)
	}
}

// E2E: test that req.params is populated in the JS handler
func TestServeHTTP_DynamicParams(t *testing.T) {
	workspace := t.TempDir()
	publicDir := filepath.Join(workspace, "public")
	os.MkdirAll(filepath.Join(publicDir, "api", "users"), 0755)

	scriptPath := filepath.Join(publicDir, "api", "users", "[id].server.js")
	os.WriteFile(scriptPath, []byte(`
module.exports = function(req) {
	return Response.json({id: req.params.id, method: req.method})
}
`), 0644)

	h := NewHandler(nil, workspace, publicDir, nil, nil, nil, "test", &config.Workspace{Path: workspace})

	req := httptest.NewRequest("GET", "/api/users/42", nil)
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req, scriptPath, "/api/users/42", map[string]string{"id": "42"})

	resp := w.Result()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	bodyStr := string(body)
	if !strings.Contains(bodyStr, `"id":"42"`) {
		t.Errorf("expected id:42 in response, got %q", bodyStr)
	}
}

// --- Body Method Tests ---

func TestServeHTTP_ReqText(t *testing.T) {
	workspace := t.TempDir()
	publicDir := filepath.Join(workspace, "public")
	os.MkdirAll(publicDir, 0755)

	scriptPath := filepath.Join(publicDir, "rawtext.server.js")
	os.WriteFile(scriptPath, []byte(`
module.exports = function(req) {
	return Response.json({raw: req.text()})
}
`), 0644)

	h := NewHandler(nil, workspace, publicDir, nil, nil, nil, "test", &config.Workspace{Path: workspace})

	req := httptest.NewRequest("POST", "/rawtext", strings.NewReader("hello raw world"))
	req.Header.Set("Content-Type", "text/plain")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req, scriptPath, "/rawtext", nil)

	resp := w.Result()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	if !strings.Contains(string(body), `"raw":"hello raw world"`) {
		t.Errorf("expected raw text in response, got %q", string(body))
	}
}

func TestServeHTTP_ReqBytes(t *testing.T) {
	workspace := t.TempDir()
	publicDir := filepath.Join(workspace, "public")
	os.MkdirAll(publicDir, 0755)

	scriptPath := filepath.Join(publicDir, "rawbytes.server.js")
	os.WriteFile(scriptPath, []byte(`
module.exports = function(req) {
	var buf = req.bytes();
	var view = new Uint8Array(buf);
	return Response.json({length: view.length, first: view[0]})
}
`), 0644)

	h := NewHandler(nil, workspace, publicDir, nil, nil, nil, "test", &config.Workspace{Path: workspace})

	req := httptest.NewRequest("POST", "/rawbytes", strings.NewReader("AB"))
	req.Header.Set("Content-Type", "application/octet-stream")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req, scriptPath, "/rawbytes", nil)

	resp := w.Result()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	bodyStr := string(body)
	if !strings.Contains(bodyStr, `"length":2`) {
		t.Errorf("expected length:2, got %q", bodyStr)
	}
	if !strings.Contains(bodyStr, `"first":65`) {
		t.Errorf("expected first:65 (ASCII 'A'), got %q", bodyStr)
	}
}

func TestServeHTTP_FormDuplicateKeys(t *testing.T) {
	workspace := t.TempDir()
	publicDir := filepath.Join(workspace, "public")
	os.MkdirAll(publicDir, 0755)

	scriptPath := filepath.Join(publicDir, "dupes.server.js")
	os.WriteFile(scriptPath, []byte(`
module.exports = function(req) {
	var f = req.form();
	return Response.json({color: f.color})
}
`), 0644)

	h := NewHandler(nil, workspace, publicDir, nil, nil, nil, "test", &config.Workspace{Path: workspace})

	req := httptest.NewRequest("POST", "/dupes", strings.NewReader("color=red&color=blue"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req, scriptPath, "/dupes", nil)

	resp := w.Result()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	bodyStr := string(body)
	// Should be an array ["red","blue"]
	if !strings.Contains(bodyStr, `"red"`) || !strings.Contains(bodyStr, `"blue"`) {
		t.Errorf("expected array with red and blue, got %q", bodyStr)
	}
}

func TestServeHTTP_FormBracketConvention(t *testing.T) {
	workspace := t.TempDir()
	publicDir := filepath.Join(workspace, "public")
	os.MkdirAll(publicDir, 0755)

	scriptPath := filepath.Join(publicDir, "brackets.server.js")
	os.WriteFile(scriptPath, []byte(`
module.exports = function(req) {
	var f = req.form();
	return Response.json({tags: f.tags, name: f.name})
}
`), 0644)

	h := NewHandler(nil, workspace, publicDir, nil, nil, nil, "test", &config.Workspace{Path: workspace})

	// tags[] should be stripped to "tags" and always array, even with one value
	req := httptest.NewRequest("POST", "/brackets", strings.NewReader("tags[]=go&tags[]=js&name=test"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req, scriptPath, "/brackets", nil)

	resp := w.Result()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	bodyStr := string(body)
	if !strings.Contains(bodyStr, `"go"`) || !strings.Contains(bodyStr, `"js"`) {
		t.Errorf("expected tags array with go and js, got %q", bodyStr)
	}
	if !strings.Contains(bodyStr, `"name":"test"`) {
		t.Errorf("expected name:test, got %q", bodyStr)
	}
}

func TestServeHTTP_FormBracketSingleValue(t *testing.T) {
	workspace := t.TempDir()
	publicDir := filepath.Join(workspace, "public")
	os.MkdirAll(publicDir, 0755)

	scriptPath := filepath.Join(publicDir, "bracket1.server.js")
	os.WriteFile(scriptPath, []byte(`
module.exports = function(req) {
	var f = req.form();
	// tags[] with one value should still be an array
	var isArray = Array.isArray(f.tags);
	return Response.json({isArray: isArray, tags: f.tags})
}
`), 0644)

	h := NewHandler(nil, workspace, publicDir, nil, nil, nil, "test", &config.Workspace{Path: workspace})

	req := httptest.NewRequest("POST", "/bracket1", strings.NewReader("tags[]=only"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req, scriptPath, "/bracket1", nil)

	resp := w.Result()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	bodyStr := string(body)
	if !strings.Contains(bodyStr, `"isArray":true`) {
		t.Errorf("expected tags[] with one value to be array, got %q", bodyStr)
	}
}

func TestServeHTTP_MultipartFileSave(t *testing.T) {
	workspace := t.TempDir()
	publicDir := filepath.Join(workspace, "public")
	os.MkdirAll(publicDir, 0755)
	os.MkdirAll(filepath.Join(workspace, "uploads"), 0755)

	scriptPath := filepath.Join(publicDir, "upload.server.js")
	os.WriteFile(scriptPath, []byte(`
module.exports = function(req) {
	var f = req.form();
	var result = f.doc.save("uploads/" + f.doc.filename);
	return Response.json({
		filename: f.doc.filename,
		size: f.doc.size,
		description: f.description,
		saved: result.bytes
	})
}
`), 0644)

	h := NewHandler(nil, workspace, publicDir, nil, nil, nil, "test", &config.Workspace{Path: workspace})

	// Build multipart body
	var buf bytes.Buffer
	mpw := multipart.NewWriter(&buf)
	mpw.WriteField("description", "Test upload")
	part, _ := mpw.CreateFormFile("doc", "test.txt")
	part.Write([]byte("file content here"))
	mpw.Close()

	req := httptest.NewRequest("POST", "/upload", &buf)
	req.Header.Set("Content-Type", mpw.FormDataContentType())
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req, scriptPath, "/upload", nil)

	resp := w.Result()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	bodyStr := string(body)
	if !strings.Contains(bodyStr, `"filename":"test.txt"`) {
		t.Errorf("expected filename:test.txt, got %q", bodyStr)
	}
	if !strings.Contains(bodyStr, `"description":"Test upload"`) {
		t.Errorf("expected description, got %q", bodyStr)
	}
	if !strings.Contains(bodyStr, `"saved":17`) {
		t.Errorf("expected saved:17 bytes, got %q", bodyStr)
	}

	// Verify file was actually saved
	saved, err := os.ReadFile(filepath.Join(workspace, "uploads", "test.txt"))
	if err != nil {
		t.Fatalf("saved file not found: %v", err)
	}
	if string(saved) != "file content here" {
		t.Errorf("expected 'file content here', got %q", string(saved))
	}
}

func TestServeHTTP_MultipartFileText(t *testing.T) {
	workspace := t.TempDir()
	publicDir := filepath.Join(workspace, "public")
	os.MkdirAll(publicDir, 0755)

	scriptPath := filepath.Join(publicDir, "filetext.server.js")
	os.WriteFile(scriptPath, []byte(`
module.exports = function(req) {
	var f = req.form();
	var content = f.doc.text();
	return Response.json({content: content, type: f.doc.type})
}
`), 0644)

	h := NewHandler(nil, workspace, publicDir, nil, nil, nil, "test", &config.Workspace{Path: workspace})

	var buf bytes.Buffer
	mpw := multipart.NewWriter(&buf)
	part, _ := mpw.CreateFormFile("doc", "readme.md")
	part.Write([]byte("# Hello"))
	mpw.Close()

	req := httptest.NewRequest("POST", "/filetext", &buf)
	req.Header.Set("Content-Type", mpw.FormDataContentType())
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req, scriptPath, "/filetext", nil)

	resp := w.Result()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	if !strings.Contains(string(body), `"content":"# Hello"`) {
		t.Errorf("expected file text content, got %q", string(body))
	}
}

func TestServeHTTP_MultipartFileBytes(t *testing.T) {
	workspace := t.TempDir()
	publicDir := filepath.Join(workspace, "public")
	os.MkdirAll(publicDir, 0755)

	scriptPath := filepath.Join(publicDir, "filebytes.server.js")
	os.WriteFile(scriptPath, []byte(`
module.exports = function(req) {
	var f = req.form();
	var buf = f.bin.bytes();
	var view = new Uint8Array(buf);
	return Response.json({len: view.length, first: view[0], last: view[view.length-1]})
}
`), 0644)

	h := NewHandler(nil, workspace, publicDir, nil, nil, nil, "test", &config.Workspace{Path: workspace})

	var buf bytes.Buffer
	mpw := multipart.NewWriter(&buf)
	part, _ := mpw.CreateFormFile("bin", "data.bin")
	part.Write([]byte{0xDE, 0xAD, 0xBE, 0xEF})
	mpw.Close()

	req := httptest.NewRequest("POST", "/filebytes", &buf)
	req.Header.Set("Content-Type", mpw.FormDataContentType())
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req, scriptPath, "/filebytes", nil)

	resp := w.Result()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	bodyStr := string(body)
	if !strings.Contains(bodyStr, `"len":4`) {
		t.Errorf("expected len:4, got %q", bodyStr)
	}
	if !strings.Contains(bodyStr, fmt.Sprintf(`"first":%d`, 0xDE)) {
		t.Errorf("expected first:222 (0xDE), got %q", bodyStr)
	}
	if !strings.Contains(bodyStr, fmt.Sprintf(`"last":%d`, 0xEF)) {
		t.Errorf("expected last:239 (0xEF), got %q", bodyStr)
	}
}

func TestServeHTTP_MultipartMultipleFiles(t *testing.T) {
	workspace := t.TempDir()
	publicDir := filepath.Join(workspace, "public")
	os.MkdirAll(publicDir, 0755)

	scriptPath := filepath.Join(publicDir, "multifile.server.js")
	os.WriteFile(scriptPath, []byte(`
module.exports = function(req) {
	var f = req.form();
	var isArray = Array.isArray(f.files);
	var names = [];
	for (var i = 0; i < f.files.length; i++) {
		names.push(f.files[i].filename);
	}
	return Response.json({isArray: isArray, count: f.files.length, names: names})
}
`), 0644)

	h := NewHandler(nil, workspace, publicDir, nil, nil, nil, "test", &config.Workspace{Path: workspace})

	var buf bytes.Buffer
	mpw := multipart.NewWriter(&buf)
	p1, _ := mpw.CreateFormFile("files", "a.txt")
	p1.Write([]byte("aaa"))
	p2, _ := mpw.CreateFormFile("files", "b.txt")
	p2.Write([]byte("bbb"))
	mpw.Close()

	req := httptest.NewRequest("POST", "/multifile", &buf)
	req.Header.Set("Content-Type", mpw.FormDataContentType())
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req, scriptPath, "/multifile", nil)

	resp := w.Result()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	bodyStr := string(body)
	if !strings.Contains(bodyStr, `"isArray":true`) {
		t.Errorf("expected files to be array, got %q", bodyStr)
	}
	if !strings.Contains(bodyStr, `"count":2`) {
		t.Errorf("expected count:2, got %q", bodyStr)
	}
	if !strings.Contains(bodyStr, `"a.txt"`) || !strings.Contains(bodyStr, `"b.txt"`) {
		t.Errorf("expected both filenames, got %q", bodyStr)
	}
}
