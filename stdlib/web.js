/**
 * @name web
 * @description Headless Chrome session with visible mode and session persistence. b.go(url); b.scrape(cssSel); b.click(sel); b.snap(); b.close();
 * @example var b = require("web"); b.go("https://example.com"); var items = b.scrape("h1"); b.close();
 */

// Session-first browser wrapper — all methods auto-wait for selectors.
// The session persists in store._bpage across JS execution iterations.
var page = store._bpage || null;
var lastOpts = store._bopts || {}; // remember open opts for auto-recovery

// Internal: safely close a dead page reference without throwing
function killPage() {
  if (page) {
    try { page.close(); } catch (e) { /* already dead */ }
  }
  page = null;
  store._bpage = null;
}

// Internal: check if current page is still alive
function isAlive() {
  if (!page) return false;
  try {
    page.url(); // lightweight probe — throws if connection is dead
    return true;
  } catch (e) {
    killPage();
    return false;
  }
}

// Internal: ensure browser is open, panic if not
function ensurePage(method) {
  if (!page || !isAlive()) {
    throw new Error(method + ": no page open. Call b.go(url) first.");
  }
}

// Internal: get the current page origin for resolving relative URLs.
function getOrigin() {
  try {
    var info = page.url();
    var m = info.match(/^(https?:\/\/[^\/]+)/);
    return m ? m[1] : "";
  } catch (e) {
    return "";
  }
}

// Internal: wrap a Go element with Proxy so .href/.src/etc. transparently call attr().
// Also resolves relative URLs to absolute for href and src.
function wrapElement(el) {
  return new Proxy(el, {
    get: function (target, prop) {
      if (prop in target) return target[prop];
      var val = target.attr(prop);
      // Resolve relative URLs for href and src
      if (val && (prop === "href" || prop === "src") && val.indexOf("//") !== 0 && val.indexOf("http") !== 0) {
        val = getOrigin() + (val.indexOf("/") === 0 ? val : "/" + val);
      }
      return val;
    }
  });
}

// Internal: wait for a selector to appear, returns the page for chaining.
// Uses page.waitFor which blocks until the element exists.
function autoWait(selector, timeoutMs) {
  page.waitFor(selector, timeoutMs || 10000);
}

// Internal: build browser.open opts from a user options object
function buildBrowserOpts(o) {
  var browserOpts = {};
  if (o.wait) browserOpts.wait = o.wait;
  if (o.timeout) browserOpts.timeout = o.timeout;
  if (o.viewport) browserOpts.viewport = o.viewport;
  if (o.visible) browserOpts.visible = true;
  if (o.headless === false) browserOpts.headless = false;
  if (o.dataPath) browserOpts.dataPath = o.dataPath;
  return browserOpts;
}

// Internal: open a fresh browser session
function openFresh(url, o) {
  killPage(); // clean up any dead refs first
  var browserOpts = buildBrowserOpts(o);
  page = browser.open(url, browserOpts);
  store._bpage = page;
  // Remember opts for auto-recovery (strip transient fields)
  store._bopts = { visible: o.visible, dataPath: o.dataPath, viewport: o.viewport };
  lastOpts = store._bopts;
}

var session = {
  // * web.go(url: string, opts?: object) → session
  // Navigate to URL (or open browser on first call). Relative paths map to current origin.
  // Auto-recovers if browser was closed or crashed.
  // opts: {wait: "load"|"idle"|"selector:...", timeout: ms, viewport: {width, height}, visible: bool, dataPath: string}
  go: function (url, goOpts) {
    if (!url || typeof url !== "string") {
      throw new Error("go: url must be a non-empty string, got: " + typeof url);
    }
    var o = goOpts || {};
    // Merge persistent opts (dataPath, visible) from previous session if not explicitly provided
    if (!goOpts && lastOpts) {
      o = lastOpts;
    } else if (goOpts && lastOpts) {
      if (!o.dataPath && lastOpts.dataPath) o.dataPath = lastOpts.dataPath;
      if (o.visible === undefined && lastOpts.visible) o.visible = lastOpts.visible;
    }

    if (page && isAlive()) {
      // Existing live session — navigate within it
      try {
        // Relative path support
        if (url.indexOf("/") === 0 && url.indexOf("//") !== 0) {
          var info = page.url();
          var match = info.match(/^(https?:\/\/[^\/]+)/);
          if (match) {
            url = match[1] + url;
          }
        }
        page.navigate(url);
      } catch (e) {
        // Navigation failed — browser probably died. Reopen fresh.
        openFresh(url, o);
      }
    } else {
      // No session or dead session — open fresh
      openFresh(url, o);
    }
    // Post-navigation wait for dynamic content
    if (o.wait && page) {
      if (o.wait.indexOf("selector:") === 0) {
        var sel = o.wait.substring(9);
        autoWait(sel, o.timeout);
      }
    }
    return session;
  },

  // * web.url() → string
  // Returns the current page URL.
  url: function () {
    ensurePage("url");
    return page.url();
  },

  // * web.title() → string
  // Returns the current page title.
  title: function () {
    ensurePage("title");
    return page.title();
  },

  // * web.text() → string
  // Returns the visible text content of the page body.
  text: function () {
    ensurePage("text");
    return page.text();
  },

  // * web.html() → string
  // Returns the full page HTML.
  html: function () {
    ensurePage("html");
    return page.html();
  },

  // * web.scrape(selector?: string) → [{text, html, href, src, ...any attr, attr(name)}] | string
  // Query for elements matching selector. Returns [] if none found (does NOT wait/throw).
  // Returns full page text if no selector given.
  scrape: function (selector) {
    ensurePage("scrape");
    if (selector && typeof selector !== "string") {
      throw new Error("scrape(selector) requires a CSS selector string, got " + typeof selector + ". Use scrape(\"#id\") not scrape({key: sel}).");
    }
    if (!selector) {
      return page.text();
    }
    try {
      var elements = page.selectAll(selector);
      var result = [];
      for (var i = 0; i < elements.length; i++) {
        result.push(wrapElement(elements[i]));
      }
      return result;
    } catch (e) {
      return [];
    }
  },

  // * web.links(selector?: string) → [{text, href, ...attrs}]
  // Query for link elements. Returns [] if none found (does NOT wait/throw). Defaults to "a[href]".
  // href values are resolved to absolute URLs.
  links: function (selector) {
    ensurePage("links");
    var sel = selector || "a[href]";
    try {
      var elements = page.selectAll(sel);
      var result = [];
      for (var i = 0; i < elements.length; i++) {
        result.push(wrapElement(elements[i]));
      }
      return result;
    } catch (e) {
      return [];
    }
  },

  // * web.table(selector?: string) → string[][]
  // Auto-waits and extracts table data as an array of row arrays. Defaults to "table".
  table: function (selector) {
    ensurePage("table");
    var sel = selector || "table";
    autoWait(sel);
    var rows = page.selectAll(sel + " tr");
    var result = [];
    for (var i = 0; i < rows.length; i++) {
      var cells = page.eval('() => { var tr = document.querySelectorAll("' + sel + ' tr")[' + i + ']; if (!tr) return "[]"; return JSON.stringify(Array.from(tr.querySelectorAll("td, th")).map(c => c.textContent.trim())); }');
      try {
        result.push(JSON.parse(cells));
      } catch (e) {
        result.push([rows[i].text]);
      }
    }
    return result;
  },

  // * web.eval(js: string) → string
  // Evaluate JavaScript natively in the browser's page context.
  eval: function (js) {
    ensurePage("eval");
    return page.eval(js);
  },

  // * web.click(selector: string) → session
  // Auto-waits for a selector to appear, then clicks it.
  click: function (selector) {
    ensurePage("click");
    autoWait(selector);
    page.click(selector);
    sleep(300); // brief settle after click
    return session;
  },

  // * web.type(selector: string, text: string) → session
  // Auto-waits for a selector, then types text into it.
  type: function (selector, text) {
    ensurePage("type");
    autoWait(selector);
    page.type(selector, text);
    return session;
  },

  // * web.scroll(selectorOrPixels: string|number) → session
  // Scroll to a specific element selector, or by a pixel count.
  scroll: function (selectorOrPixels) {
    ensurePage("scroll");
    page.scroll(selectorOrPixels);
    return session;
  },

  // * web.waitFor(selector: string, timeoutMs?: number) → session
  // Explicitly block execution until a selector appears in the DOM.
  waitFor: function (selector, timeout) {
    ensurePage("waitFor");
    autoWait(selector, timeout);
    return session;
  },

  // * web.select(selector: string) → Element {text, html, href, src, ...any attr, attr(name)}
  // Auto-waits and returns a single matching element.
  select: function (selector) {
    ensurePage("select");
    autoWait(selector);
    return wrapElement(page.select(selector));
  },

  // * web.selectAll(selector: string) → Element[]
  // Auto-waits and returns all matching elements.
  selectAll: function (selector) {
    ensurePage("selectAll");
    autoWait(selector);
    var elements = page.selectAll(selector);
    var result = [];
    for (var i = 0; i < elements.length; i++) {
      result.push(wrapElement(elements[i]));
    }
    return result;
  },

  // * web.fill(fields: object, submitSelector?: string) → session
  // Fills a form where keys are selectors and values are inputs. Optionally clicks a submit button.
  fill: function (fields, submitSelector) {
    ensurePage("fill");
    for (var sel in fields) {
      autoWait(sel);
      page.type(sel, fields[sel]);
    }
    if (submitSelector) {
      autoWait(submitSelector);
      page.click(submitSelector);
      sleep(1000);
    }
    return session;
  },

  // * web.snap(path?: string, opts?: {fullPage: boolean}) → session
  // Take a JPEG screenshot. fullPage defaults to true.
  snap: function (path, snapOpts) {
    ensurePage("snap");
    var o = snapOpts || {};
    var fullPage = o.fullPage !== false;
    page.screenshot(path, { fullPage: fullPage });
    return session;
  },

  // * web.print(path?: string) → session
  // Save the current page as a PDF file.
  print: function (path) {
    ensurePage("print");
    page.pdf(path);
    return session;
  },

  // * web.back() → session
  // Navigate back in browser history (like clicking the back button).
  back: function () {
    ensurePage("back");
    page.back();
    sleep(500);
    return session;
  },

  // * web.forward() → session
  // Navigate forward in browser history.
  forward: function () {
    ensurePage("forward");
    page.forward();
    sleep(500);
    return session;
  },

  // * web.listen(opts?: {filter: string, types: string[]}) → {requests(), stop()}
  // Start capturing network requests. Call requests() to get captured so far, stop() to finish.
  // opts.filter: URL substring or regex pattern to match.
  // opts.types: Array of resource types to capture (e.g. ["XHR", "Fetch", "Document"]).
  listen: function (listenOpts) {
    ensurePage("listen");
    return page.listen(listenOpts);
  },

  // * web.pause(message?: string) → session
  // Pauses script execution and asks the user to perform manual actions in the browser
  // (e.g. log in, solve a captcha). Execution resumes when the user responds.
  // The execution timeout is paused while waiting.
  pause: function (message) {
    ensurePage("pause");
    page.pause(message);
    return session;
  },

  // * web.cookies() → [{name, value, domain, path, secure, httpOnly, expires}]
  // Returns all cookies for the current browser session.
  cookies: function () {
    ensurePage("cookies");
    return page.cookies();
  },

  // * web.close() → void
  // Closes the browser session and cleans up memory. Always call this when finished.
  close: function () {
    killPage();
    store._bopts = null;
    lastOpts = {};
  }
};

// If a session already exists from a previous iteration, verify it's alive
if (store._bpage) {
  page = store._bpage;
  if (!isAlive()) {
    // Dead session from previous iteration — clean up silently
    page = null;
    store._bpage = null;
  }
}

module.exports = session;
