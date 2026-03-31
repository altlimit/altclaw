### [ browser ] - Headless Chrome
**TIP:** For general automation, prefer using `var b = require("web")` which provides auto-waiting, session management, and auto-recovery if the browser crashes. Always fetch raw page text/data and use AI reasoning to extract information instead of writing brittle DOM selectors or complex regex. Stick to the 'KEEP SCRIPTS DUMB' strategy.

**IMPORTANT:** Session locking, process cleanup, and crash recovery are handled automatically by `require("web")`. Do NOT manually kill Chrome processes or delete lock files — the bridge handles all of this.

**Shadow DOM:** All DOM queries (select, selectAll, click, type, waitFor, scroll) automatically pierce Shadow DOM boundaries. Elements inside Web Components (e.g. Reddit's `<faceplate-textarea>`, GitHub's custom elements) are found transparently. `page.text()` and `page.html()` also include shadow DOM content.

* browser.open(url: string, opts?: BrowserOpts) → Page
  - opts.headless: boolean (default: true)
  - opts.visible: boolean (default: false) — alias for headless:false, opens a visible browser window
  - opts.wait: "load" | "idle" | "selector:<css>" (default: "load")
  - opts.timeout: number (ms, default: 30000)
  - opts.viewport: {width: number, height: number}
  - opts.dataPath: string — workspace-relative path for persistent browser profile (cookies, localStorage survive across runs). Use ".agent/tmp/browser/<name>" to keep it out of version control.

[ Page Operations ] (**Always call page.close() when done**)
* page.html() → string (Full page HTML)
* page.text() → string (Visible text content / body)
* page.title() → string (Document title)
* page.url() → string (Current URL)
* page.screenshot(path?, opts?: {fullPage, quality}) → Saves JPEG to workspace
* page.pdf(path?) → Saves PDF to workspace
* page.navigate(url) → void (Navigate and wait for load)
* page.back() → void (Navigate back in browser history)
* page.forward() → void (Navigate forward in browser history)
* page.close() → void (**CRITICAL**: Always close page and browser)

[ User Interaction ]
* page.pause(message?) → void (Pause execution for manual user actions — e.g. login, captcha. Execution deadline is paused. User types anything to resume.)
* page.cookies() → [{name, value, domain, path, secure, httpOnly, expires}] (All cookies in session)

[ DOM Interaction ]
* page.click(selector) → void
* page.type(selector, text) → void — Supports `{{secrets.NAME}}` placeholders for secure password entry (values are expanded server-side, never visible in code)
* page.eval(js) → any (Evaluate JS in browser context. Accepts arrow functions, function expressions, or bare expressions. Returns parsed objects/arrays, not strings.)
* page.waitFor(selector, timeout?) → void (default timeout: 10s)
* page.select(selector) → Element {text, html, attr(name)}
* page.selectAll(selector) → Element[]
* page.scroll(selectorOrPixels) → void (Scroll to element or by pixel count)

[ Element Properties ]
Elements returned by select/selectAll/scrape have:
* el.text — visible text content
* el.html — raw HTML
* el.href — resolved absolute URL (via Proxy, calls attr("href"))
* el.src — resolved absolute URL (via Proxy, calls attr("src"))
* el.<any> — any HTML attribute (via Proxy, calls attr(name))
* el.attr(name) → string|null (explicit attribute getter)
**NOTE:** Use `el.href` not `el.attributes.href`. There is no `.attributes` property.

[ Network Monitoring ]
* page.listen(opts?) → Handle {requests(), stop()}
  - opts.filter: string (URL substring or regex pattern to match)
  - opts.types: string[] (resource types to capture, e.g. ["XHR", "Fetch", "Document"])
  - handle.requests() → [{url, method, type, status, statusText, mimeType, headers, postData, responseHeaders}]
  - handle.stop() → [{...}] (stops listening and returns final snapshot)
