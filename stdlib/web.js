/**
 * @name web
 * @description Headless Chrome session. b.go(url); b.scrape(cssSel); b.click(sel); b.snap(); b.close();
 * @example var b = require("web"); b.go("https://example.com"); var items = b.scrape("h1"); b.close();
 */

// Session-first browser wrapper — all methods auto-wait for selectors.
// The session persists in store._bpage across JS execution iterations.
var page = store._bpage || null;
var opts = {};

// Internal: ensure browser is open, panic if not
function ensurePage(method) {
  if (!page) {
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

var session = {
  // * web.go(url: string, opts?: object) → session
  // Navigate to URL (or open browser on first call). Relative paths map to current origin.
  // opts: {wait: "load"|"idle"|"selector:...", timeout: ms, viewport: {width, height}}
  go: function (url, goOpts) {
    if (!url || typeof url !== "string") {
      throw new Error("go: url must be a non-empty string, got: " + typeof url);
    }
    var o = goOpts || {};
    if (page) {
      // Relative path support
      if (url.indexOf("/") === 0 && url.indexOf("//") !== 0) {
        var info = page.url();
        // Extract origin from current URL
        var match = info.match(/^(https?:\/\/[^\/]+)/);
        if (match) {
          url = match[1] + url;
        }
      }
      page.navigate(url);
    } else {
      // First call — launch browser
      var browserOpts = {};
      if (o.wait) browserOpts.wait = o.wait;
      if (o.timeout) browserOpts.timeout = o.timeout;
      if (o.viewport) browserOpts.viewport = o.viewport;
      page = browser.open(url, browserOpts);
      store._bpage = page;
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

  // * web.listen(opts?: {filter: string, types: string[]}) → {requests(), stop()}
  // Start capturing network requests. Call requests() to get captured so far, stop() to finish.
  // opts.filter: URL substring or regex pattern to match.
  // opts.types: Array of resource types to capture (e.g. ["XHR", "Fetch", "Document"]).
  listen: function (listenOpts) {
    ensurePage("listen");
    return page.listen(listenOpts);
  },

  // * web.close() → void
  // **CRITICAL:** Closes the browser session and cleans up memory. Always call this when finished.
  close: function () {
    if (page) {
      page.close();
      page = null;
      store._bpage = null;
    }
  }
};

// If a session already exists from a previous iteration, reconnect
if (store._bpage) {
  page = store._bpage;
}

module.exports = session;
