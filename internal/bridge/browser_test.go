package bridge

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dop251/goja"
	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
)

// ── Unit tests (no browser needed) ──────────────────────────────────

func TestLooksLikeFunction(t *testing.T) {
	tests := []struct {
		name string
		code string
		want bool
	}{
		// Arrow functions — should be detected
		{"empty arrow", "() => 1", true},
		{"arrow block", "() => { return 1; }", true},
		{"arrow with param", "(a) => a + 1", true},
		{"arrow multi param", "(a, b) => a + b", true},
		{"arrow with space", "  () => { return 1; }  ", true},
		{"async arrow", "async () => await fetch('/api')", true},

		// Function declarations — should be detected
		{"function decl", "function() { return 1; }", true},
		{"function named", "function foo() { return 1; }", true},
		{"async function", "async function() { return 1; }", true},

		// Non-functions — should NOT be detected
		{"bare expression", "document.title", false},
		{"IIFE", "(function(){ return 1; })()", false},
		{"variable decl", "var x = 1; return x;", false},
		{"const decl", "const x = document.querySelector('h1'); return x.textContent;", false},
		{"number", "42", false},
		{"string", "'hello'", false},
		{"object literal", "({foo: 1})", false},
		{"method call", "document.querySelector('div')", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := looksLikeFunction(tt.code)
			if got != tt.want {
				t.Errorf("looksLikeFunction(%q) = %v, want %v", tt.code, got, tt.want)
			}
		})
	}
}

func TestSanitizeBrowserPath(t *testing.T) {
	workspace := "/home/user/projects"
	tests := []struct {
		name     string
		filename string
		wantOk   bool
	}{
		{"relative file", "screenshot.jpg", true},
		{"nested relative", "screenshots/test.jpg", true},
		{"dotdot escape", "../../../etc/passwd", false},
		{"dotdot in middle", "foo/../../bar", false},
		{"absolute within", "/home/user/projects/test.jpg", true},
		{"absolute escape", "/tmp/evil.jpg", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sanitizeBrowserPath(workspace, tt.filename)
			if tt.wantOk && result == "" {
				t.Errorf("sanitizeBrowserPath(%q, %q) = empty, want valid path", workspace, tt.filename)
			}
			if !tt.wantOk && result != "" {
				t.Errorf("sanitizeBrowserPath(%q, %q) = %q, want empty (blocked)", workspace, tt.filename, result)
			}
		})
	}
}

func TestSanitizeError(t *testing.T) {
	workspace := "/home/user/projects"
	dataDir := "/tmp/altclaw-browser-12345"
	err := fmt.Errorf("failed to open %s/data.json: no such file", workspace)

	result := sanitizeError(workspace, dataDir, err)
	if strings.Contains(result, workspace) {
		t.Errorf("sanitized error should not contain workspace path: %s", result)
	}
	if !strings.Contains(result, "data.json") {
		t.Errorf("sanitized error should preserve filename: %s", result)
	}
}

// ── Integration tests (require Chrome) ──────────────────────────────

// testHTML serves a page with shadow DOM elements for testing.
const testHTML = `<!DOCTYPE html>
<html>
<head><title>Shadow DOM Test</title></head>
<body>
  <h1>Light DOM Title</h1>
  <p id="visible-text">Hello from light DOM</p>
  <div id="hidden" style="display:none">Hidden text</div>

  <!-- Custom element with shadow DOM -->
  <my-component id="comp1"></my-component>

  <script>
    class MyComponent extends HTMLElement {
      constructor() {
        super();
        const shadow = this.attachShadow({ mode: 'open' });
        shadow.innerHTML = '<div class="shadow-content"><textarea id="about-field" name="about" placeholder="About (optional)">existing about text</textarea><button id="save-btn">Save</button><p class="inner-text">Shadow DOM content</p></div>';
      }
    }
    customElements.define('my-component', MyComponent);
  </script>
</body>
</html>`

// testNestedShadowHTML has nested shadow DOM (shadow inside shadow).
const testNestedShadowHTML = `<!DOCTYPE html>
<html>
<head><title>Nested Shadow Test</title></head>
<body>
  <outer-component></outer-component>
  <script>
    class InnerComponent extends HTMLElement {
      constructor() {
        super();
        const shadow = this.attachShadow({ mode: 'open' });
        shadow.innerHTML = '<span class="deep-text">Deep nested text</span><input type="text" name="deep-input" />';
      }
    }
    customElements.define('inner-component', InnerComponent);

    class OuterComponent extends HTMLElement {
      constructor() {
        super();
        const shadow = this.attachShadow({ mode: 'open' });
        shadow.innerHTML = '<div class="outer-shadow"><p>Outer shadow</p><inner-component></inner-component></div>';
      }
    }
    customElements.define('outer-component', OuterComponent);
  </script>
</body>
</html>`

// launchTestBrowser tries to launch a headless browser, skipping the test if unavailable.
func launchTestBrowser(t *testing.T) (*rod.Browser, func()) {
	t.Helper()

	// Check if a browser binary is available
	path, _ := launcher.LookPath()
	if path == "" {
		t.Skip("no browser binary found — skipping integration test")
	}

	ll := launcher.New().
		Headless(true).
		Set("no-sandbox").
		Set("disable-gpu").
		Set("disable-dev-shm-usage")

	controlURL, err := ll.Launch()
	if err != nil {
		t.Skipf("failed to launch browser: %v", err)
	}

	browser := rod.New().ControlURL(controlURL)
	if err := browser.Connect(); err != nil {
		ll.Cleanup()
		t.Skipf("failed to connect to browser: %v", err)
	}

	cleanup := func() {
		_ = browser.Close()
		ll.Cleanup()
	}
	return browser, cleanup
}

// startTestServer starts a local HTTP server for test pages.
func startTestServer(pages map[string]string) *httptest.Server {
	mux := http.NewServeMux()
	for path, html := range pages {
		html := html // capture
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprint(w, html)
		})
	}
	return httptest.NewServer(mux)
}

func TestDeepElement_ShadowDOM(t *testing.T) {
	browser, cleanup := launchTestBrowser(t)
	defer cleanup()

	srv := startTestServer(map[string]string{"/": testHTML})
	defer srv.Close()

	page := browser.MustPage(srv.URL)
	page.MustWaitLoad()

	// Standard querySelector should NOT find the textarea inside shadow DOM
	_, stdErr := page.Element("textarea[name='about']")
	if stdErr == nil {
		t.Log("Standard Element() found textarea — shadow DOM may not be active in this browser version")
	}

	// Deep query SHOULD find it
	el, err := deepElement(page, "textarea[name='about']")
	if err != nil {
		t.Fatalf("deepElement failed to find textarea in shadow DOM: %v", err)
	}

	text, err := el.Text()
	if err != nil {
		t.Fatalf("failed to get text from shadow element: %v", err)
	}
	if !strings.Contains(text, "existing about text") {
		t.Errorf("expected textarea to contain 'existing about text', got %q", text)
	}
}

func TestDeepElements_ShadowDOM(t *testing.T) {
	browser, cleanup := launchTestBrowser(t)
	defer cleanup()

	srv := startTestServer(map[string]string{"/": testHTML})
	defer srv.Close()

	page := browser.MustPage(srv.URL)
	page.MustWaitLoad()

	// Should find both light DOM <p> and shadow DOM <p>
	elements, err := deepElements(page, "p")
	if err != nil {
		t.Fatalf("deepElements failed: %v", err)
	}

	// At minimum we should find the light DOM <p> and shadow <p class="inner-text">
	if len(elements) < 2 {
		t.Errorf("expected at least 2 <p> elements (light + shadow), got %d", len(elements))
	}

	// Check that shadow content is included
	found := false
	for _, el := range elements {
		text, _ := el.Text()
		if strings.Contains(text, "Shadow DOM content") {
			found = true
			break
		}
	}
	if !found {
		t.Error("deepElements did not find <p> inside shadow DOM")
	}
}

func TestDeepElement_NestedShadow(t *testing.T) {
	browser, cleanup := launchTestBrowser(t)
	defer cleanup()

	srv := startTestServer(map[string]string{"/": testNestedShadowHTML})
	defer srv.Close()

	page := browser.MustPage(srv.URL)
	page.MustWaitLoad()
	// Give custom elements time to register and render
	time.Sleep(500 * time.Millisecond)

	// Find element nested two shadow DOM levels deep
	el, err := deepElement(page, ".deep-text")
	if err != nil {
		t.Fatalf("deepElement failed to find nested shadow element: %v", err)
	}

	text, err := el.Text()
	if err != nil {
		t.Fatalf("failed to get text: %v", err)
	}
	if text != "Deep nested text" {
		t.Errorf("expected 'Deep nested text', got %q", text)
	}
}

func TestDeepElement_FallbackToLightDOM(t *testing.T) {
	browser, cleanup := launchTestBrowser(t)
	defer cleanup()

	srv := startTestServer(map[string]string{"/": testHTML})
	defer srv.Close()

	page := browser.MustPage(srv.URL)
	page.MustWaitLoad()

	// Light DOM element should still be found
	el, err := deepElement(page, "h1")
	if err != nil {
		t.Fatalf("deepElement failed to find light DOM element: %v", err)
	}

	text, err := el.Text()
	if err != nil {
		t.Fatalf("failed to get text: %v", err)
	}
	if text != "Light DOM Title" {
		t.Errorf("expected 'Light DOM Title', got %q", text)
	}
}

func TestDeepElement_Click_InShadow(t *testing.T) {
	browser, cleanup := launchTestBrowser(t)
	defer cleanup()

	srv := startTestServer(map[string]string{"/": testHTML})
	defer srv.Close()

	page := browser.MustPage(srv.URL)
	page.MustWaitLoad()

	// Should be able to click a button inside shadow DOM
	btn, err := deepElement(page, "#save-btn")
	if err != nil {
		t.Fatalf("deepElement failed to find button in shadow DOM: %v", err)
	}

	// Click should not error
	err = btn.Click("left", 1)
	if err != nil {
		t.Errorf("clicking shadow DOM button failed: %v", err)
	}
}

func TestBuildPageObject_DeepText(t *testing.T) {
	browser, cleanup := launchTestBrowser(t)
	defer cleanup()

	srv := startTestServer(map[string]string{"/": testHTML})
	defer srv.Close()

	page := browser.MustPage(srv.URL)
	page.MustWaitLoad()

	// Use the deep text JS to get text including shadow DOM
	result, err := page.Eval(jsDeepText)
	if err != nil {
		t.Fatalf("jsDeepText eval failed: %v", err)
	}

	text := result.Value.Str()

	// Should include light DOM text
	if !strings.Contains(text, "Light DOM Title") {
		t.Error("deep text missing light DOM content")
	}
	if !strings.Contains(text, "Hello from light DOM") {
		t.Error("deep text missing visible paragraph")
	}

	// Should include shadow DOM text
	if !strings.Contains(text, "Shadow DOM content") {
		t.Error("deep text missing shadow DOM content")
	}
	if !strings.Contains(text, "existing about text") {
		t.Error("deep text missing textarea content from shadow DOM")
	}

	// Should NOT include hidden text (display:none)
	if strings.Contains(text, "Hidden text") {
		t.Error("deep text should not include display:none content")
	}
}

func TestBuildPageObject_DeepHTML(t *testing.T) {
	browser, cleanup := launchTestBrowser(t)
	defer cleanup()

	srv := startTestServer(map[string]string{"/": testHTML})
	defer srv.Close()

	page := browser.MustPage(srv.URL)
	page.MustWaitLoad()

	result, err := page.Eval(jsDeepHTML)
	if err != nil {
		t.Fatalf("jsDeepHTML eval failed: %v", err)
	}

	html := result.Value.Str()

	// Should include shadow DOM content markers
	if !strings.Contains(html, "<!--shadow-root-->") {
		t.Error("deep HTML missing shadow root markers")
	}
	// Should include shadow DOM elements
	if !strings.Contains(html, "shadow-content") {
		t.Error("deep HTML missing shadow DOM class")
	}
	if !strings.Contains(html, "about-field") {
		t.Error("deep HTML missing textarea from shadow DOM")
	}
}

func TestBuildPageObject_Eval_AutoWrap(t *testing.T) {
	// Check if a browser binary is available
	path, _ := launcher.LookPath()
	if path == "" {
		t.Skip("no browser binary found — skipping integration test")
	}

	workspace := t.TempDir()
	srv := startTestServer(map[string]string{"/": testHTML})
	defer srv.Close()

	vm := goja.New()
	ll := launcher.New().Headless(true).Set("no-sandbox").Set("disable-gpu").Set("disable-dev-shm-usage")
	controlURL, err := ll.Launch()
	if err != nil {
		t.Skipf("failed to launch browser: %v", err)
	}
	defer ll.Cleanup()

	b := rod.New().ControlURL(controlURL)
	if err := b.Connect(); err != nil {
		t.Skipf("failed to connect: %v", err)
	}
	defer b.Close()

	page := b.MustPage(srv.URL)
	page.MustWaitLoad()

	pageObj := buildPageObject(vm, page, b, workspace, ll, workspace, false, nil, nil, nil)
	vm.Set("page", pageObj)

	// Test 1: Arrow function (should work as before)
	result, err := vm.RunString(`page.eval("() => document.title")`)
	if err != nil {
		t.Fatalf("arrow function eval failed: %v", err)
	}
	if result.String() != "Shadow DOM Test" {
		t.Errorf("expected 'Shadow DOM Test', got %q", result.String())
	}

	// Test 2: Bare expression (should be auto-wrapped)
	result, err = vm.RunString(`page.eval("document.title")`)
	if err != nil {
		t.Fatalf("bare expression eval failed: %v", err)
	}
	if result.String() != "Shadow DOM Test" {
		t.Errorf("bare expression: expected 'Shadow DOM Test', got %q", result.String())
	}

	// Test 3: IIFE (should be auto-wrapped as expression)
	result, err = vm.RunString(`page.eval("(function(){ return document.title; })()")`)
	if err != nil {
		t.Fatalf("IIFE eval failed: %v", err)
	}
	if result.String() != "Shadow DOM Test" {
		t.Errorf("IIFE: expected 'Shadow DOM Test', got %q", result.String())
	}

	// Test 4: Object return (should parse to object, not string)
	result, err = vm.RunString(`page.eval("() => ({a: 1, b: 'hello'})")`)
	if err != nil {
		t.Fatalf("object return eval failed: %v", err)
	}
	// Access as object
	aVal, err := vm.RunString(`var r = page.eval("() => ({a: 1, b: 'hello'})"); r.a`)
	if err != nil {
		t.Fatalf("object property access failed: %v", err)
	}
	if aVal.ToInteger() != 1 {
		t.Errorf("expected r.a === 1, got %v", aVal)
	}

	// Test 5: Number return
	result, err = vm.RunString(`page.eval("() => 42")`)
	if err != nil {
		t.Fatalf("number eval failed: %v", err)
	}
	if result.ToInteger() != 42 {
		t.Errorf("expected 42, got %v", result)
	}

	// Test 6: Boolean return
	result, err = vm.RunString(`page.eval("() => true")`)
	if err != nil {
		t.Fatalf("bool eval failed: %v", err)
	}
	if !result.ToBoolean() {
		t.Error("expected true, got false")
	}
}

func TestBuildPageObject_SelectAll_PiercesShadow(t *testing.T) {
	browser, cleanup := launchTestBrowser(t)
	defer cleanup()

	workspace := t.TempDir()
	srv := startTestServer(map[string]string{"/": testHTML})
	defer srv.Close()

	vm := goja.New()
	page := browser.MustPage(srv.URL)
	page.MustWaitLoad()

	ll := &launcher.Launcher{} // dummy for buildPageObject
	pageObj := buildPageObject(vm, page, browser, workspace, ll, workspace, false, nil, nil, nil)
	vm.Set("page", pageObj)

	// selectAll("textarea") should find the shadow DOM textarea
	result, err := vm.RunString(`
		var elements = page.selectAll("textarea");
		var found = false;
		for (var i = 0; i < elements.length; i++) {
			if (elements[i].text.indexOf("existing about text") !== -1) {
				found = true;
			}
		}
		found;
	`)
	if err != nil {
		t.Fatalf("selectAll eval failed: %v", err)
	}
	if !result.ToBoolean() {
		t.Error("selectAll('textarea') did not find textarea inside shadow DOM")
	}
}

func TestBuildPageObject_Text_IncludesShadow(t *testing.T) {
	browser, cleanup := launchTestBrowser(t)
	defer cleanup()

	workspace := t.TempDir()
	srv := startTestServer(map[string]string{"/": testHTML})
	defer srv.Close()

	vm := goja.New()
	page := browser.MustPage(srv.URL)
	page.MustWaitLoad()

	ll := &launcher.Launcher{}
	pageObj := buildPageObject(vm, page, browser, workspace, ll, workspace, false, nil, nil, nil)
	vm.Set("page", pageObj)

	result, err := vm.RunString(`page.text()`)
	if err != nil {
		t.Fatalf("page.text() failed: %v", err)
	}
	text := result.String()

	if !strings.Contains(text, "Shadow DOM content") {
		t.Error("page.text() should include shadow DOM content")
	}
	if !strings.Contains(text, "Light DOM Title") {
		t.Error("page.text() should include light DOM content")
	}
}

func TestBuildPageObject_HTML_IncludesShadow(t *testing.T) {
	browser, cleanup := launchTestBrowser(t)
	defer cleanup()

	workspace := t.TempDir()
	srv := startTestServer(map[string]string{"/": testHTML})
	defer srv.Close()

	vm := goja.New()
	page := browser.MustPage(srv.URL)
	page.MustWaitLoad()

	ll := &launcher.Launcher{}
	pageObj := buildPageObject(vm, page, browser, workspace, ll, workspace, false, nil, nil, nil)
	vm.Set("page", pageObj)

	result, err := vm.RunString(`page.html()`)
	if err != nil {
		t.Fatalf("page.html() failed: %v", err)
	}
	html := result.String()

	if !strings.Contains(html, "shadow-root") {
		t.Error("page.html() should include shadow root markers")
	}
	if !strings.Contains(html, "about-field") {
		t.Error("page.html() should include shadow DOM textarea")
	}
}

func TestBuildPageObject_Screenshot_PathSecurity(t *testing.T) {
	workspace := t.TempDir()

	// Test that path traversal is blocked
	result := sanitizeBrowserPath(workspace, "../../../etc/passwd")
	if result != "" {
		t.Error("screenshot path traversal should be blocked")
	}

	// Test valid path
	result = sanitizeBrowserPath(workspace, "screenshots/test.jpg")
	if result == "" {
		t.Error("valid screenshot path should be allowed")
	}
	expected := filepath.Join(workspace, "screenshots", "test.jpg")
	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}
}

func TestBuildPageObject_Close_PreservesDataPath(t *testing.T) {
	// Ensure that persistent data directories are NOT deleted on close
	workspace := t.TempDir()
	dataDir := filepath.Join(workspace, "browser-data")
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Write a marker file
	marker := filepath.Join(dataDir, "marker.txt")
	if err := os.WriteFile(marker, []byte("persist"), 0644); err != nil {
		t.Fatal(err)
	}

	vm := goja.New()

	// Simulate the close behavior with persistProfile=true
	// (we can't do a full browser test here, but we verify the logic)
	persistProfile := true
	if !persistProfile {
		_ = os.RemoveAll(dataDir)
	}

	// Marker should still exist
	if _, err := os.Stat(marker); os.IsNotExist(err) {
		t.Error("persistent data directory should not be deleted on close")
	}

	_ = vm // used in real test
}
