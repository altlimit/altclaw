### [ browser ] - Headless Chrome
**TIP:** For general automation, prefer using `var b = require("web")` which provides auto-waiting and session management. Always fetch raw page text/data and use AI reasoning to extract information instead of writing brittle DOM selectors or complex regex. Stick to the 'KEEP SCRIPTS DUMB' strategy.

* browser.open(url: string, opts?: BrowserOpts) → Page
  - opts.headless: boolean (default: true)
  - opts.wait: "load" | "idle" | "selector:<css>" (default: "load")
  - opts.timeout: number (ms, default: 30000)
  - opts.viewport: {width: number, height: number}

[ Page Operations ] (**Always call page.close() when done**)
* page.html() → string (Full page HTML)
* page.text() → string (Visible text content / body)
* page.title() → string (Document title)
* page.url() → string (Current URL)
* page.screenshot(path?, opts?: {fullPage, quality}) → Saves JPEG to workspace
* page.pdf(path?) → Saves PDF to workspace
* page.navigate(url) → void (Navigate and wait for load)
* page.close() → void (**CRITICAL**: Always close page and browser)

[ DOM Interaction ]
* page.click(selector) → void
* page.type(selector, text) → void
* page.eval(js) → string (Evaluate JS in browser context)
* page.waitFor(selector, timeout?) → void
* page.select(selector) → Element {text, html, attr(name)}
* page.selectAll(selector) → Element[]
* page.scroll(selectorOrPixels) → void

[ Network Monitoring ]
* page.listen(opts?) → Handle {requests(), stop()}
  - opts.filter: string (URL substring or regex pattern to match)
  - opts.types: string[] (resource types to capture, e.g. ["XHR", "Fetch", "Document"])
  - handle.requests() → [{url, method, type, status, statusText, mimeType, headers, postData, responseHeaders}]
  - handle.stop() → [{...}] (stops listening and returns final snapshot)
