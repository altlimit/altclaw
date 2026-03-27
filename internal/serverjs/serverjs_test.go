package serverjs

import (
	"io"
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

	h := NewHandler(nil, workspace, publicDir, nil, nil, "test", &config.Workspace{Path: workspace})

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
	return Response.json({received: req.body.name, method: req.method})
}
`), 0644)

	h := NewHandler(nil, workspace, publicDir, nil, nil, "test", &config.Workspace{Path: workspace})

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
	return Response.json({name: req.body.name, email: req.body.email})
}
`), 0644)

	h := NewHandler(nil, workspace, publicDir, nil, nil, "test", &config.Workspace{Path: workspace})

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

	h := NewHandler(nil, workspace, publicDir, nil, nil, "test", &config.Workspace{Path: workspace})

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

	h := NewHandler(nil, workspace, publicDir, nil, nil, "test", &config.Workspace{Path: workspace})

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

	h := NewHandler(nil, workspace, publicDir, nil, nil, "test", &config.Workspace{Path: workspace})

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

	h := NewHandler(nil, workspace, publicDir, nil, nil, "test", &config.Workspace{Path: workspace})

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

	h := NewHandler(nil, workspace, publicDir, nil, nil, "1.0.0", &config.Workspace{Path: workspace})

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

	h := NewHandler(nil, workspace, publicDir, nil, nil, "test", &config.Workspace{Path: workspace})

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

	h := NewHandler(nil, workspace, publicDir, nil, nil, "test", &config.Workspace{Path: workspace})

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

	h := NewHandler(nil, workspace, publicDir, nil, nil, "test", &config.Workspace{Path: workspace})

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
	return {message: "created", name: req.body.name}
}
`), 0644)

	h := NewHandler(nil, workspace, publicDir, nil, nil, "test", &config.Workspace{Path: workspace})

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

	h := NewHandler(nil, workspace, publicDir, nil, nil, "test", &config.Workspace{Path: workspace})

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

	h := NewHandler(nil, workspace, publicDir, nil, nil, "test", &config.Workspace{Path: workspace})

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

	h := NewHandler(nil, workspace, publicDir, nil, nil, "test", &config.Workspace{Path: workspace})

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

	h := NewHandler(nil, workspace, publicDir, nil, nil, "test", &config.Workspace{Path: workspace})

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

	h := NewHandler(nil, workspace, publicDir, nil, nil, "test", &config.Workspace{Path: workspace})

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
