package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"altclaw.ai/internal/config"
	"altclaw.ai/internal/netx"
	"github.com/dop251/goja"
	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
	"github.com/go-rod/stealth"
)

// ── Shadow DOM piercing helpers ──────────────────────────────────────

// jsDeepQuerySelector is browser-side JS that finds the first element
// matching a CSS selector, recursively traversing open shadow roots.
const jsDeepQuerySelector = `(sel) => {
	function deep(root) {
		const el = root.querySelector(sel);
		if (el) return el;
		const hosts = root.querySelectorAll('*');
		for (const h of hosts) {
			if (h.shadowRoot) {
				const found = deep(h.shadowRoot);
				if (found) return found;
			}
		}
		return null;
	}
	return deep(document);
}`

// jsDeepQuerySelectorAll is browser-side JS that finds all elements
// matching a CSS selector, recursively traversing open shadow roots.
// Returns an array so rod can iterate the remote object.
const jsDeepQuerySelectorAll = `(sel) => {
	const results = [];
	function deep(root) {
		root.querySelectorAll(sel).forEach(el => results.push(el));
		root.querySelectorAll('*').forEach(h => {
			if (h.shadowRoot) deep(h.shadowRoot);
		});
	}
	deep(document);
	return results;
}`

// jsDeepText extracts visible text from the page including shadow DOM content.
const jsDeepText = `() => {
	function deep(node) {
		let text = '';
		if (node.shadowRoot) {
			text += deep(node.shadowRoot);
		} else if (node.childNodes.length === 0) {
			if (node.nodeType === 3) text += node.textContent;
		} else {
			for (const child of node.childNodes) {
				if (child.nodeType === 1) {
					const style = window.getComputedStyle(child);
					if (style.display === 'none' || style.visibility === 'hidden') continue;
					text += deep(child);
				} else if (child.nodeType === 3) {
					text += child.textContent;
				}
			}
		}
		return text;
	}
	return deep(document.body);
}`

// jsDeepHTML serializes the full DOM including shadow DOM content.
const jsDeepHTML = `() => {
	function deep(node) {
		if (node.nodeType === 3) return node.textContent;
		if (node.nodeType !== 1) return '';
		const tag = node.tagName.toLowerCase();
		let attrs = '';
		for (const a of node.attributes || []) {
			attrs += ' ' + a.name + '="' + a.value.replace(/"/g, '&quot;') + '"';
		}
		let inner = '';
		if (node.shadowRoot) {
			inner += '<!--shadow-root-->';
			for (const child of node.shadowRoot.childNodes) inner += deep(child);
			inner += '<!--/shadow-root-->';
		}
		for (const child of node.childNodes) inner += deep(child);
		return '<' + tag + attrs + '>' + inner + '</' + tag + '>';
	}
	return deep(document.documentElement);
}`

// deepElement finds a single element by CSS selector, piercing shadow DOM.
// Falls back to standard page.Element (with short timeout) only if the JS eval fails.
func deepElement(page *rod.Page, selector string) (*rod.Element, error) {
	result, err := page.Evaluate(rod.Eval(jsDeepQuerySelector, selector).ByObject().ByPromise())
	if err != nil {
		// JS eval failed (e.g. context not ready) — try standard query with a short timeout
		return page.Timeout(3 * time.Second).Element(selector)
	}
	if result.ObjectID == "" {
		// Deep search traversed the entire DOM (including shadow roots) and found nothing.
		// Don't fall back to page.Element which would block indefinitely.
		return nil, fmt.Errorf("element %q not found (searched light DOM and shadow roots)", selector)
	}
	return page.ElementFromObject(result)
}

// deepElements finds all elements by CSS selector, piercing shadow DOM.
func deepElements(page *rod.Page, selector string) ([]*rod.Element, error) {
	result, err := page.Evaluate(rod.Eval(jsDeepQuerySelectorAll, selector).ByObject().ByPromise())
	if err != nil {
		// JS eval failed — try standard query with short timeout
		return page.Timeout(3 * time.Second).Elements(selector)
	}
	if result.ObjectID == "" {
		// Deep search returned empty array or null — return empty slice
		return nil, nil
	}
	// The result is a JS array of elements. Get the array properties via CDP.
	props, err := proto.RuntimeGetProperties{
		ObjectID:      result.ObjectID,
		OwnProperties: true,
	}.Call(page)
	if err != nil {
		return nil, nil
	}
	var elements []*rod.Element
	for _, prop := range props.Result {
		if prop.Value == nil || prop.Value.ObjectID == "" {
			continue
		}
		// Skip non-numeric property names (like "length")
		if prop.Name == "length" || prop.Name == "__proto__" {
			continue
		}
		el, err := page.ElementFromObject(&proto.RuntimeRemoteObject{
			ObjectID: prop.Value.ObjectID,
		})
		if err != nil {
			continue
		}
		elements = append(elements, el)
	}
	return elements, nil
}

// looksLikeFunction checks if JS code starts with a function expression
// or arrow function that rod can directly evaluate.
func looksLikeFunction(code string) bool {
	s := strings.TrimSpace(code)
	if strings.HasPrefix(s, "function") || strings.HasPrefix(s, "async ") {
		return true
	}
	// Arrow functions: () =>, (a) =>, (a, b) =>, etc.
	if strings.HasPrefix(s, "(") {
		// Look for => within the first paren group
		parenEnd := strings.Index(s, ")")
		if parenEnd > 0 && parenEnd+1 < len(s) {
			after := strings.TrimSpace(s[parenEnd+1:])
			if strings.HasPrefix(after, "=>") {
				return true
			}
		}
	}
	return false
}

// BrowserCleanup is a function that cleans up browser resources.
type BrowserCleanup func()

// RegisterBrowser adds the browser namespace to the runtime.
// workspace is the jailed directory for saving screenshots/PDFs.
// Each call creates a unique temp directory for the Chrome user-data profile
// so concurrent agents never collide on the same profile lock.
// handler is optional — if provided, enables page.pause() for user interaction.
// pauser is optional — if provided, page.pause() will pause the execution deadline.
// Returns a cleanup function that must be called when the engine shuts down.
func RegisterBrowser(vm *goja.Runtime, workspace string, handler UIHandler, pauser DeadlinePauser, store *config.Store, ctxFn ...func() context.Context) BrowserCleanup {
	// Track all active browsers for cleanup (thread-safe)
	var mu sync.Mutex
	var activeBrowsers []*rod.Browser
	var activeTempLaunchers []*launcher.Launcher // ephemeral — Cleanup() deletes data dir
	var activePersistLaunchers []*launcher.Launcher // persistent — Kill() preserves data dir
	var activeTempDirs []string

	// Start an SSRF-safe proxy for Chrome to use.
	// This ensures ALL Chrome traffic (including in-page fetch/XHR from eval)
	// goes through SafeDialer, blocking access to private/loopback/metadata IPs.
	proxyServer, proxyErr := netx.NewProxyServer()
	var proxyURL string
	if proxyErr == nil {
		proxyURL = fmt.Sprintf("http://127.0.0.1:%d", proxyServer.Port())
	}

	cleanup := func() {
		mu.Lock()
		defer mu.Unlock()
		for _, b := range activeBrowsers {
			_ = b.Close()
		}
		// Ephemeral launchers: kill process AND delete data dir
		for _, l := range activeTempLaunchers {
			l.Cleanup()
		}
		// Persistent launchers: kill process only, preserve user data dir
		for _, l := range activePersistLaunchers {
			l.Kill()
		}
		activeBrowsers = nil
		activeTempLaunchers = nil
		activePersistLaunchers = nil
		for _, d := range activeTempDirs {
			_ = os.RemoveAll(d)
		}
		activeTempDirs = nil
		if proxyServer != nil {
			proxyServer.Close()
		}
	}

	browserObj := vm.NewObject()

	// browser.open(url, opts?) → page object
	// opts: { headless: bool (default true), visible: bool (default false), wait: "load"|"idle"|"selector:...", timeout: ms (default 30000), viewport: {width, height}, dataPath: string }
	const maxBrowsers = 3
	browserObj.Set("open", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			Throw(vm, "browser.open requires a URL argument")
		}
		url := call.Arguments[0].String()

		// SSRF protection: block navigation to private/loopback/metadata IPs
		if err := netx.ValidateURL(url); err != nil {
			Throwf(vm, "browser.open: %v", err)
		}

		// Enforce max concurrent browser limit
		mu.Lock()
		if len(activeBrowsers) >= maxBrowsers {
			mu.Unlock()
			Throwf(vm, "browser.open: max concurrent browsers (%d) reached — close existing pages first", maxBrowsers)
		}
		mu.Unlock()

		// Defaults
		headless := true
		waitStrategy := "load"
		timeoutMs := 30000
		viewportW, viewportH := 0, 0
		dataPath := ""

		// Parse options
		if len(call.Arguments) > 1 && !goja.IsUndefined(call.Arguments[1]) && !goja.IsNull(call.Arguments[1]) {
			opts := call.Arguments[1].ToObject(vm)
			CheckOpts(vm, "browser.open", opts, "headless", "visible", "wait", "timeout", "viewport", "dataPath")
			if v := opts.Get("headless"); v != nil && !goja.IsUndefined(v) {
				headless = v.ToBoolean()
			}
			if v := opts.Get("visible"); v != nil && !goja.IsUndefined(v) {
				if v.ToBoolean() {
					headless = false
				}
			}
			if v := opts.Get("wait"); v != nil && !goja.IsUndefined(v) {
				waitStrategy = v.String()
			}
			if v := opts.Get("timeout"); v != nil && !goja.IsUndefined(v) {
				timeoutMs = int(v.ToInteger())
			}
			if v := opts.Get("viewport"); v != nil && !goja.IsUndefined(v) && !goja.IsNull(v) {
				vpObj := v.ToObject(vm)
				if w := vpObj.Get("width"); w != nil && !goja.IsUndefined(w) {
					viewportW = int(w.ToInteger())
				}
				if h := vpObj.Get("height"); h != nil && !goja.IsUndefined(h) {
					viewportH = int(h.ToInteger())
				}
			}
			if v := opts.Get("dataPath"); v != nil && !goja.IsUndefined(v) {
				dataPath = v.String()
			}
		}

		// Determine user-data directory: persistent (dataPath) or ephemeral (temp)
		var userDataDir string
		persistProfile := false
		if dataPath != "" {
			safe := sanitizeBrowserPath(workspace, dataPath)
			if safe == "" {
				Throw(vm, "browser.open: dataPath escapes workspace")
			}
			if err := os.MkdirAll(safe, 0755); err != nil {
				Throwf(vm, "browser.open: failed to create dataPath dir: %v", sanitizeError(workspace, "", err))
			}
			userDataDir = safe
			persistProfile = true

			// Clear Chrome singleton locks so we can reclaim a profile left
			// behind by a crashed or orphaned browser instance.
			for _, lock := range []string{"SingletonLock", "SingletonCookie", "SingletonSocket"} {
				_ = os.Remove(filepath.Join(safe, lock))
			}
		} else {
			var err error
			userDataDir, err = os.MkdirTemp("", "altclaw-browser-*")
			if err != nil {
				Throwf(vm, "browser.open: failed to create temp profile dir: %v", err)
			}
		}

		// Launch browser — with retry for locked persistent profiles.
		// Leakless is disabled because Windows Defender flags leakless.exe as
		// a false positive. We have our own cleanup (activeBrowsers + cleanup func).
		launchBrowser := func() (string, *launcher.Launcher, error) {
			ll := launcher.New().
				Leakless(false).
				UserDataDir(userDataDir).
				Headless(headless).
				Set("disable-gpu").
				Set("no-sandbox").
				Set("disable-dev-shm-usage")

			// Route all Chrome traffic through our SSRF-safe proxy
			if proxyURL != "" {
				ll = ll.Set("proxy-server", proxyURL)
			}

			url, err := ll.Launch()
			return url, ll, err
		}

		controlURL, l, err2 := launchBrowser()
		if err2 != nil && persistProfile && strings.Contains(err2.Error(), "existing browser session") {
			// Chrome is still running with this profile — kill it and retry
			killChromeForProfile(userDataDir)
			time.Sleep(500 * time.Millisecond) // give OS time to release locks
			for _, lock := range []string{"SingletonLock", "SingletonCookie", "SingletonSocket", "lockfile"} {
				_ = os.Remove(filepath.Join(userDataDir, lock))
			}
			controlURL, l, err2 = launchBrowser()
		}
		if err2 != nil {
			if !persistProfile {
				_ = os.RemoveAll(userDataDir)
			}
			Throwf(vm, "browser.open: failed to launch browser: %v", sanitizeError(workspace, userDataDir, err2))
		}

		browser := rod.New().ControlURL(controlURL)
		if err := browser.Connect(); err != nil {
			if persistProfile {
				l.Kill() // Kill process but preserve user data
			} else {
				l.Cleanup()
				_ = os.RemoveAll(userDataDir)
			}
			Throwf(vm, "browser.open: failed to connect to browser: %v", sanitizeError(workspace, userDataDir, err))
		}

		mu.Lock()
		activeBrowsers = append(activeBrowsers, browser)
		if persistProfile {
			activePersistLaunchers = append(activePersistLaunchers, l)
		} else {
			activeTempLaunchers = append(activeTempLaunchers, l)
			activeTempDirs = append(activeTempDirs, userDataDir)
		}
		mu.Unlock()

		// Create page and navigate
		page, err := stealth.Page(browser)
		if err != nil {
			Throwf(vm, "browser.open: failed to create stealth page: %v", err)
		}
		if err := page.Navigate(url); err != nil {
			Throwf(vm, "browser.open: navigate failed: %v", err)
		}

		// Set custom viewport if specified
		if viewportW > 0 && viewportH > 0 {
			page.MustSetViewport(viewportW, viewportH, 0, false)
		}

		timeout := time.Duration(timeoutMs) * time.Millisecond
		page = page.Timeout(timeout)

		// Wait strategy
		if err := applyWaitStrategy(page, waitStrategy); err != nil {
			Throwf(vm, "browser.open: wait failed: %v", err)
		}

		// Reset timeout after wait
		page = page.CancelTimeout()

		return buildPageObject(vm, page, browser, workspace, l, userDataDir, persistProfile, handler, pauser, store, ctxFn...)
	})

	vm.Set("browser", browserObj)
	return cleanup
}

// applyWaitStrategy applies the specified wait strategy to the page.
func applyWaitStrategy(page *rod.Page, strategy string) error {
	switch {
	case strategy == "load":
		return page.WaitLoad()
	case strategy == "idle":
		wait := page.WaitRequestIdle(300*time.Millisecond, nil, nil, nil)
		wait()
		return nil
	case strings.HasPrefix(strategy, "selector:"):
		sel := strings.TrimPrefix(strategy, "selector:")
		_, err := page.Element(sel)
		return err
	default:
		return page.WaitLoad()
	}
}

// buildPageObject creates the JS page object with all methods.
func buildPageObject(vm *goja.Runtime, page *rod.Page, browser *rod.Browser, workspace string, l *launcher.Launcher, userDataDir string, persistProfile bool, handler UIHandler, pauser DeadlinePauser, store *config.Store, ctxFn ...func() context.Context) goja.Value {
	getCtx := defaultCtxFn(ctxFn)
	obj := vm.NewObject()

	// page.html() → string — full page HTML including shadow DOM content
	obj.Set("html", func(call goja.FunctionCall) goja.Value {
		result, err := page.Eval(jsDeepHTML)
		if err != nil {
			// Fallback to standard HTML if deep traversal fails
			html, err2 := page.HTML()
			if err2 != nil {
				Throwf(vm, "page.html error: %v", err)
			}
			return vm.ToValue(html)
		}
		return vm.ToValue(result.Value.Str())
	})

	// page.text() → string — visible text content including shadow DOM
	obj.Set("text", func(call goja.FunctionCall) goja.Value {
		result, err := page.Eval(jsDeepText)
		if err != nil {
			// Fallback to standard text if deep traversal fails
			el, err2 := page.Element("body")
			if err2 != nil {
				Throwf(vm, "page.text error: %v", err)
			}
			text, err2 := el.Text()
			if err2 != nil {
				Throwf(vm, "page.text error: %v", err)
			}
			return vm.ToValue(text)
		}
		return vm.ToValue(result.Value.Str())
	})

	// page.title() → string
	obj.Set("title", func(call goja.FunctionCall) goja.Value {
		info, err := page.Info()
		if err != nil {
			Throwf(vm, "page.title error: %v", err)
		}
		return vm.ToValue(info.Title)
	})

	// page.url() → string
	obj.Set("url", func(call goja.FunctionCall) goja.Value {
		info, err := page.Info()
		if err != nil {
			Throwf(vm, "page.url error: %v", err)
		}
		return vm.ToValue(info.URL)
	})

	// page.screenshot(path?, opts?) — save screenshot to workspace
	// path: string, opts: {fullPage: bool, quality: number}
	obj.Set("screenshot", func(call goja.FunctionCall) goja.Value {
		filename := fmt.Sprintf("screenshot_%s.jpg", time.Now().Format("20060102_150405"))
		fullPage := false
		quality := 80

		if len(call.Arguments) > 0 && !goja.IsUndefined(call.Arguments[0]) && !goja.IsNull(call.Arguments[0]) {
			arg := call.Arguments[0]
			if obj := arg.ToObject(vm); obj != nil && obj.ClassName() == "Object" {
				Throw(vm, "page.screenshot: first argument must be a string path, not an options object. Usage: page.screenshot(path?, {fullPage, quality}?)")
			}
			filename = arg.String()
		}

		// Parse optional second argument as options object
		if len(call.Arguments) > 1 && !goja.IsUndefined(call.Arguments[1]) && !goja.IsNull(call.Arguments[1]) {
			arg := call.Arguments[1]
			if obj := arg.ToObject(vm); obj != nil && obj.ClassName() == "Object" {
				if v := obj.Get("fullPage"); v != nil && !goja.IsUndefined(v) {
					fullPage = v.ToBoolean()
				}
				if v := obj.Get("quality"); v != nil && !goja.IsUndefined(v) {
					quality = int(v.ToInteger())
				}
			} else {
				// Allow page.screenshot(path, true) as shorthand for fullPage
				fullPage = arg.ToBoolean()
			}
		}

		savePath := sanitizeBrowserPath(workspace, filename)
		if savePath == "" {
			Throw(vm, "page.screenshot: path escapes workspace")
		}

		if err := os.MkdirAll(filepath.Dir(savePath), 0755); err != nil {
			Throwf(vm, "page.screenshot: mkdir error: %v", sanitizeError(workspace, "", err))
		}

		format := proto.PageCaptureScreenshotFormatJpeg
		data, err := page.Screenshot(fullPage, &proto.PageCaptureScreenshot{
			Format:  format,
			Quality: &quality,
		})
		if err != nil {
			Throwf(vm, "page.screenshot error: %v", err)
		}
		if err := os.WriteFile(savePath, data, 0644); err != nil {
			Throwf(vm, "page.screenshot write error: %v", sanitizeError(workspace, "", err))
		}
		return goja.Undefined()
	})

	// page.pdf(path?) — save PDF to workspace
	obj.Set("pdf", func(call goja.FunctionCall) goja.Value {
		filename := fmt.Sprintf("page_%s.pdf", time.Now().Format("20060102_150405"))
		if len(call.Arguments) > 0 && !goja.IsUndefined(call.Arguments[0]) && !goja.IsNull(call.Arguments[0]) {
			arg := call.Arguments[0]
			if obj := arg.ToObject(vm); obj != nil && obj.ClassName() == "Object" {
				Throw(vm, "page.pdf: expects a string path, not an options object. Usage: page.pdf(path?)")
			}
			filename = arg.String()
		}

		savePath := sanitizeBrowserPath(workspace, filename)
		if savePath == "" {
			Throw(vm, "page.pdf: path escapes workspace")
		}

		if err := os.MkdirAll(filepath.Dir(savePath), 0755); err != nil {
			Throwf(vm, "page.pdf: mkdir error: %v", sanitizeError(workspace, "", err))
		}

		reader, err := page.PDF(&proto.PagePrintToPDF{
			PrintBackground: true,
		})
		if err != nil {
			Throwf(vm, "page.pdf error: %v", err)
		}
		data, err := io.ReadAll(reader)
		if err != nil {
			Throwf(vm, "page.pdf read error: %v", err)
		}
		if err := os.WriteFile(savePath, data, 0644); err != nil {
			Throwf(vm, "page.pdf write error: %v", sanitizeError(workspace, "", err))
		}
		return goja.Undefined()
	})

	// page.click(selector) — pierces shadow DOM
	obj.Set("click", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			Throw(vm, "page.click requires a selector argument")
		}
		sel := call.Arguments[0].String()
		el, err := deepElement(page, sel)
		if err != nil {
			Throwf(vm, "page.click: element %q not found: %v", sel, err)
		}
		if err := el.Click(proto.InputMouseButtonLeft, 1); err != nil {
			Throwf(vm, "page.click error: %v", err)
		}
		return goja.Undefined()
	})

	// page.type(selector, text) — pierces shadow DOM
	obj.Set("type", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 2 {
			Throw(vm, "page.type requires selector and text arguments")
		}
		sel := call.Arguments[0].String()
		text := call.Arguments[1].String()
		// Expand {{secrets.NAME}} so agents can type passwords without seeing raw values
		if store != nil {
			text = ExpandSecrets(getCtx(), store, text)
		}
		el, err := deepElement(page, sel)
		if err != nil {
			Throwf(vm, "page.type: element %q not found: %v", sel, err)
		}
		if err := el.Input(text); err != nil {
			Throwf(vm, "page.type error: %v", err)
		}
		return goja.Undefined()
	})

	// page.eval(jsCode) → result (auto-wraps non-function code, returns parsed objects)
	obj.Set("eval", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			Throw(vm, "page.eval requires a JavaScript code argument")
		}
		code := call.Arguments[0].String()

		// Auto-wrap non-function code so rod can evaluate it.
		// Rod wraps code as `.apply(this, args)` which fails on IIFEs and statements.
		if !looksLikeFunction(code) {
			trimmed := strings.TrimSpace(code)
			// Single expression: use expression-body arrow so it's implicitly returned
			// Multi-statement: wrap in block with return on the last expression
			if !strings.Contains(trimmed, "\n") && !strings.HasSuffix(trimmed, ";") {
				code = "() => (" + trimmed + ")"
			} else {
				code = "() => {\n" + code + "\n}"
			}
		}

		result, err := page.Eval(code)
		if err != nil {
			Throwf(vm, "page.eval error: %v", err)
		}

		// Safely convert the rod result to a goja value via JSON.
		val := result.Value
		if val.Nil() {
			return goja.Undefined()
		}
		return jsonToGoja(vm, val.JSON("", ""))
	})

	// page.waitFor(selector, timeout?) — pierces shadow DOM via polling
	obj.Set("waitFor", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			Throw(vm, "page.waitFor requires a selector argument")
		}
		sel := call.Arguments[0].String()

		timeoutMs := int64(10000)
		if len(call.Arguments) > 1 && !goja.IsUndefined(call.Arguments[1]) {
			timeoutMs = call.Arguments[1].ToInteger()
		}

		// Poll with deepElement to find elements inside shadow DOM
		deadline := time.Now().Add(time.Duration(timeoutMs) * time.Millisecond)
		for {
			_, err := deepElement(page, sel)
			if err == nil {
				return goja.Undefined()
			}
			if time.Now().After(deadline) {
				Throwf(vm, "page.waitFor: %q not found after %dms", sel, timeoutMs)
			}
			time.Sleep(200 * time.Millisecond)
		}
	})

	// page.select(selector) → {text, html, attr(name)} — pierces shadow DOM
	obj.Set("select", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			Throw(vm, "page.select requires a selector argument")
		}
		sel := call.Arguments[0].String()
		el, err := deepElement(page, sel)
		if err != nil {
			Throwf(vm, "page.select: %q not found: %v", sel, err)
		}
		return buildElementObject(vm, el)
	})

	// page.selectAll(selector) → [{text, html, attr(name)}] — pierces shadow DOM
	obj.Set("selectAll", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			Throw(vm, "page.selectAll requires a selector argument")
		}
		sel := call.Arguments[0].String()
		elements, err := deepElements(page, sel)
		if err != nil {
			Throwf(vm, "page.selectAll: %q error: %v", sel, err)
		}
		arr := make([]interface{}, 0, len(elements))
		for _, el := range elements {
			arr = append(arr, buildElementObject(vm, el))
		}
		return vm.ToValue(arr)
	})

	// page.navigate(url) — navigate to new URL
	obj.Set("navigate", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			Throw(vm, "page.navigate requires a URL argument")
		}
		url := call.Arguments[0].String()

		// SSRF protection: block navigation to private/loopback/metadata IPs
		if err := netx.ValidateURL(url); err != nil {
			Throwf(vm, "page.navigate: %v", err)
		}

		if err := page.Navigate(url); err != nil {
			Throwf(vm, "page.navigate error: %v", err)
		}
		if err := page.WaitLoad(); err != nil {
			Throwf(vm, "page.navigate wait error: %v", err)
		}
		return goja.Undefined()
	})

	// page.scroll(selector_or_pixels) — scroll to element or by pixel count
	obj.Set("scroll", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			Throw(vm, "page.scroll requires a selector or pixel count argument")
		}
		arg := call.Arguments[0]
		// Check if it's a number (pixels)
		if n := arg.ToInteger(); n != 0 {
			_, err := page.Eval(fmt.Sprintf(`() => window.scrollBy(0, %d)`, n))
			if err != nil {
				Throwf(vm, "page.scroll error: %v", err)
			}
		} else {
			// Treat as selector — pierces shadow DOM
			sel := arg.String()
			el, err := deepElement(page, sel)
			if err != nil {
				Throwf(vm, "page.scroll: element %q not found: %v", sel, err)
			}
			if err := el.ScrollIntoView(); err != nil {
				Throwf(vm, "page.scroll error: %v", err)
			}
		}
		return goja.Undefined()
	})

	// page.listen(opts?) → {requests(), stop()}
	// Enables the Chrome DevTools Network domain and captures request/response events.
	// opts: { filter: string|regex pattern to match URLs, types: ["XHR","Fetch","Document",...] }
	obj.Set("listen", func(call goja.FunctionCall) goja.Value {
		var urlFilter string
		var typeFilter map[string]bool

		// Parse options
		if len(call.Arguments) > 0 && !goja.IsUndefined(call.Arguments[0]) && !goja.IsNull(call.Arguments[0]) {
			opts := call.Arguments[0].ToObject(vm)
			CheckOpts(vm, "page.listen", opts, "filter", "types")
			if v := opts.Get("filter"); v != nil && !goja.IsUndefined(v) {
				urlFilter = v.String()
			}
			if v := opts.Get("types"); v != nil && !goja.IsUndefined(v) {
				typeFilter = make(map[string]bool)
				// Parse as JSON array string or goja array
				arr := v.ToObject(vm)
				for i := 0; ; i++ {
					item := arr.Get(fmt.Sprintf("%d", i))
					if item == nil || goja.IsUndefined(item) {
						break
					}
					typeFilter[item.String()] = true
				}
			}
		}

		// Enable network domain
		err := proto.NetworkEnable{}.Call(page)
		if err != nil {
			Throwf(vm, "page.listen: failed to enable network domain: %v", err)
		}

		nl := newNetworkListener(urlFilter, typeFilter)

		// Start event listener in background goroutine
		go page.EachEvent(
			func(e *proto.NetworkRequestWillBeSent) bool {
				nl.onRequest(e)
				return nl.stopped()
			},
			func(e *proto.NetworkResponseReceived) bool {
				nl.onResponse(e)
				return nl.stopped()
			},
		)()

		// Build the returned JS handle
		handle := vm.NewObject()

		// handle.requests() → [{url, method, status, type, headers, postData, responseHeaders}]
		handle.Set("requests", func(call goja.FunctionCall) goja.Value {
			return vm.ToValue(nl.toJS(vm))
		})

		// handle.stop() → [{...}] — stops listening and returns final snapshot
		handle.Set("stop", func(call goja.FunctionCall) goja.Value {
			nl.stop()
			return vm.ToValue(nl.toJS(vm))
		})

		return handle
	})

	// page.back() — navigate back in browser history
	obj.Set("back", func(call goja.FunctionCall) goja.Value {
		if err := page.NavigateBack(); err != nil {
			Throwf(vm, "page.back error: %v", err)
		}
		if err := page.WaitLoad(); err != nil {
			Throwf(vm, "page.back wait error: %v", err)
		}
		return goja.Undefined()
	})

	// page.forward() — navigate forward in browser history
	obj.Set("forward", func(call goja.FunctionCall) goja.Value {
		if err := page.NavigateForward(); err != nil {
			Throwf(vm, "page.forward error: %v", err)
		}
		if err := page.WaitLoad(); err != nil {
			Throwf(vm, "page.forward wait error: %v", err)
		}
		return goja.Undefined()
	})

	// page.pause(message?) — block execution until user responds.
	// Used to let the user manually interact with the browser (e.g. login).
	obj.Set("pause", func(call goja.FunctionCall) goja.Value {
		if handler == nil {
			Throw(vm, "page.pause: not available (no UI handler)")
		}
		msg := "Browser paused. Please complete your actions in the browser, then type anything here to continue."
		if len(call.Arguments) > 0 && !goja.IsUndefined(call.Arguments[0]) && !goja.IsNull(call.Arguments[0]) {
			msg = call.Arguments[0].String()
		}

		handler.Log("⏸️ " + msg)

		// Pause the execution deadline so the user has unlimited time
		if pauser != nil {
			pauser.PauseDeadline()
		}
		_ = handler.Ask(msg)
		if pauser != nil {
			pauser.ResumeDeadline()
		}

		handler.Log("▶️ Resuming browser automation")
		return goja.Undefined()
	})

	// page.cookies() — return all cookies for the current page as an array of objects.
	obj.Set("cookies", func(call goja.FunctionCall) goja.Value {
		cookies, err := proto.NetworkGetAllCookies{}.Call(page)
		if err != nil {
			Throwf(vm, "page.cookies error: %v", err)
		}
		arr := make([]interface{}, 0, len(cookies.Cookies))
		for _, c := range cookies.Cookies {
			co := vm.NewObject()
			co.Set("name", c.Name)
			co.Set("value", c.Value)
			co.Set("domain", c.Domain)
			co.Set("path", c.Path)
			co.Set("secure", c.Secure)
			co.Set("httpOnly", c.HTTPOnly)
			if c.Expires != 0 {
				co.Set("expires", c.Expires.Time().Unix())
			}
			arr = append(arr, co)
		}
		return vm.ToValue(arr)
	})

	// page.close() — close page and browser
	obj.Set("close", func(call goja.FunctionCall) goja.Value {
		_ = page.Close()
		_ = browser.Close()
		if persistProfile {
			// Kill browser process but preserve user data directory for session persistence
			l.Kill()
		} else {
			// Ephemeral profile: kill process AND delete data dir
			l.Cleanup()
			_ = os.RemoveAll(userDataDir)
		}
		return goja.Undefined()
	})

	return obj
}

// buildElementObject creates a JS object for an element with text, html, attr methods.
func buildElementObject(vm *goja.Runtime, el *rod.Element) goja.Value {
	obj := vm.NewObject()

	text, _ := el.Text()
	obj.Set("text", text)

	html, _ := el.HTML()
	obj.Set("html", html)

	obj.Set("attr", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			return goja.Null()
		}
		name := call.Arguments[0].String()
		val, err := el.Attribute(name)
		if err != nil || val == nil {
			return goja.Null()
		}
		return vm.ToValue(*val)
	})

	return obj
}

// sanitizeBrowserPath resolves a filename to a safe path within the workspace.
func sanitizeBrowserPath(workspace, filename string) string {
	if strings.Contains(filename, "..") {
		return ""
	}
	var abs string
	if filepath.IsAbs(filename) {
		abs = filepath.Clean(filename)
	} else {
		abs = filepath.Clean(filepath.Join(workspace, filename))
	}
	rel, err := filepath.Rel(workspace, abs)
	if err != nil || strings.HasPrefix(rel, "..") {
		return ""
	}
	return abs
}

// sanitizeError strips the workspace and userDataDir absolute paths from error
// messages so the AI only sees relative paths and never learns the host layout.
func sanitizeError(workspace, userDataDir string, err error) string {
	msg := err.Error()
	// Strip longer paths first to avoid partial matches
	for _, p := range []string{userDataDir, workspace} {
		if p != "" {
			msg = strings.ReplaceAll(msg, p+"/", "")
			msg = strings.ReplaceAll(msg, p, "")
		}
	}
	return msg
}

// jsonToGoja safely converts a JSON string to a goja.Value using json.Unmarshal.
// This avoids vm.RunString which would be a code-injection vector if the JSON
// contained crafted payloads from browser-evaluated expressions.
func jsonToGoja(vm *goja.Runtime, jsonStr string) goja.Value {
	if jsonStr == "" || jsonStr == "null" || jsonStr == "undefined" {
		return goja.Undefined()
	}
	var parsed interface{}
	if err := json.Unmarshal([]byte(jsonStr), &parsed); err != nil {
		// Not valid JSON — return as plain string
		return vm.ToValue(jsonStr)
	}
	return goToGoja(vm, parsed)
}

// goToGoja recursively converts a Go value (from json.Unmarshal) to a goja.Value.
func goToGoja(vm *goja.Runtime, v interface{}) goja.Value {
	if v == nil {
		return goja.Null()
	}
	switch val := v.(type) {
	case bool:
		return vm.ToValue(val)
	case float64:
		// json.Unmarshal decodes all numbers as float64
		if val == float64(int64(val)) {
			return vm.ToValue(int64(val))
		}
		return vm.ToValue(val)
	case string:
		return vm.ToValue(val)
	case []interface{}:
		arr := make([]interface{}, len(val))
		for i, item := range val {
			arr[i] = goToGoja(vm, item).Export()
		}
		return vm.ToValue(arr)
	case map[string]interface{}:
		obj := vm.NewObject()
		for k, item := range val {
			obj.Set(k, goToGoja(vm, item))
		}
		return obj
	default:
		return vm.ToValue(fmt.Sprintf("%v", val))
	}
}

// ── Network listener ─────────────────────────────────────────────────

// capturedRequest holds data for a single network request + response pair.
type capturedRequest struct {
	RequestID string
	URL       string
	Method    string
	Type      string
	Headers   map[string]string
	PostData  string

	// Filled when response arrives
	Status          int
	StatusText      string
	ResponseHeaders map[string]string
	MimeType        string
}

// networkListener collects CDP network events with optional filtering.
type networkListener struct {
	mu         sync.Mutex
	requests   map[string]*capturedRequest // keyed by NetworkRequestID
	order      []string                   // insertion order of request IDs
	urlFilter  string
	urlRe      *regexp.Regexp
	typeFilter map[string]bool // nil = accept all
	done       chan struct{}
}

func newNetworkListener(urlFilter string, typeFilter map[string]bool) *networkListener {
	nl := &networkListener{
		requests:   make(map[string]*capturedRequest),
		typeFilter: typeFilter,
		urlFilter:  urlFilter,
		done:       make(chan struct{}),
	}
	// Try to compile as regex; if it fails, we fall back to substring match
	if urlFilter != "" {
		if re, err := regexp.Compile(urlFilter); err == nil {
			nl.urlRe = re
		}
	}
	return nl
}

func (nl *networkListener) matchesURL(url string) bool {
	if nl.urlFilter == "" {
		return true
	}
	if nl.urlRe != nil {
		return nl.urlRe.MatchString(url)
	}
	return strings.Contains(url, nl.urlFilter)
}

func (nl *networkListener) matchesType(resType string) bool {
	if len(nl.typeFilter) == 0 {
		return true
	}
	return nl.typeFilter[resType]
}

func (nl *networkListener) stopped() bool {
	select {
	case <-nl.done:
		return true
	default:
		return false
	}
}

func (nl *networkListener) stop() {
	nl.mu.Lock()
	defer nl.mu.Unlock()
	select {
	case <-nl.done:
	default:
		close(nl.done)
	}
}

func (nl *networkListener) onRequest(e *proto.NetworkRequestWillBeSent) {
	url := e.Request.URL
	resType := string(e.Type)

	if !nl.matchesURL(url) || !nl.matchesType(resType) {
		return
	}

	headers := make(map[string]string)
	if e.Request.Headers != nil {
		raw, _ := json.Marshal(e.Request.Headers)
		_ = json.Unmarshal(raw, &headers)
	}

	nl.mu.Lock()
	defer nl.mu.Unlock()
	id := string(e.RequestID)
	nl.requests[id] = &capturedRequest{
		RequestID: id,
		URL:       url,
		Method:    e.Request.Method,
		Type:      resType,
		Headers:   headers,
		PostData:  e.Request.PostData,
	}
	nl.order = append(nl.order, id)
}

func (nl *networkListener) onResponse(e *proto.NetworkResponseReceived) {
	nl.mu.Lock()
	defer nl.mu.Unlock()
	id := string(e.RequestID)
	req, ok := nl.requests[id]
	if !ok {
		return
	}
	req.Status = e.Response.Status
	req.StatusText = e.Response.StatusText
	req.MimeType = e.Response.MIMEType

	respHeaders := make(map[string]string)
	if e.Response.Headers != nil {
		raw, _ := json.Marshal(e.Response.Headers)
		_ = json.Unmarshal(raw, &respHeaders)
	}
	req.ResponseHeaders = respHeaders
}

// toJS converts captured requests to a slice of goja objects in insertion order.
func (nl *networkListener) toJS(vm *goja.Runtime) []interface{} {
	nl.mu.Lock()
	defer nl.mu.Unlock()

	result := make([]interface{}, 0, len(nl.order))
	for _, id := range nl.order {
		req, ok := nl.requests[id]
		if !ok {
			continue
		}
		obj := vm.NewObject()
		obj.Set("url", req.URL)
		obj.Set("method", req.Method)
		obj.Set("type", req.Type)
		obj.Set("status", req.Status)
		obj.Set("statusText", req.StatusText)
		obj.Set("mimeType", req.MimeType)
		obj.Set("headers", req.Headers)
		obj.Set("postData", req.PostData)
		obj.Set("responseHeaders", req.ResponseHeaders)
		result = append(result, obj)
	}
	return result
}
