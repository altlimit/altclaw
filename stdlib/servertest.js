/**
 * @name servertest
 * @description Server endpoint tester. Tests .server.js modules directly without HTTP.
 * @example var st = require("servertest"); var r = st.post("public/contact.server.js", {name: "John"}); ui.log(r.status + " " + JSON.stringify(r.body));
 */

function buildReq(method, path, body, headers) {
  var req = {
    method: method,
    url: path,
    path: path,
    params: {},
    query: {},
    headers: headers || {},
    body: body || null
  };
  // Parse query string from path
  var qIdx = path.indexOf("?");
  if (qIdx >= 0) {
    req.path = path.substring(0, qIdx);
    var qs = path.substring(qIdx + 1);
    var pairs = qs.split("&");
    for (var i = 0; i < pairs.length; i++) {
      var kv = pairs[i].split("=");
      req.query[decodeURIComponent(kv[0])] = kv[1] ? decodeURIComponent(kv[1]) : "";
    }
  }
  return req;
}

function parseResponse(val) {
  // Response object (has __type === "Response")
  if (val && typeof val === "object" && val.__type === "Response") {
    var hdrs = {};
    if (val.headers) {
      var keys = Object.keys(val.headers);
      for (var i = 0; i < keys.length; i++) {
        hdrs[keys[i]] = val.headers[keys[i]];
      }
    }
    var body = val.body || "";
    // Try to parse JSON body
    if (hdrs["Content-Type"] && hdrs["Content-Type"].indexOf("application/json") >= 0) {
      try { body = JSON.parse(body); } catch(e) {}
    }
    var status = val.status || 200;
    return { status: status, headers: hdrs, body: body, ok: status >= 200 && status < 300 };
  }

  // Auto-detect bare return values (mirrors Go extractResponse logic)
  if (val === undefined || val === null) {
    return { status: 204, headers: {}, body: "", ok: true };
  }
  if (typeof val === "string") {
    var trimmed = val.trim();
    var ct = trimmed.charAt(0) === "<" ? "text/html; charset=utf-8" : "text/plain; charset=utf-8";
    return { status: 200, headers: {"Content-Type": ct}, body: val, ok: true };
  }
  // Object/array — auto JSON
  return { status: 200, headers: {"Content-Type": "application/json; charset=utf-8"}, body: val, ok: true };
}

function run(method, filePath, body, headers) {
  var handler = require("./" + filePath);
  var req = buildReq(method, filePath.replace(/\.server\.js$/, "").replace(/^public\//, "/"), body, headers);
  var result = handler(req);
  return parseResponse(result);
}

module.exports = {
  // * servertest.get(file: string, headers?: object) → {status, headers, body, ok}
  // Simulates a GET request to a .server.js file directly.
  get: function (file, headers) { return run("GET", file, null, headers); },

  // * servertest.post(file: string, body: object|string, headers?: object) → {status, headers, body, ok}
  // Simulates a POST request to a .server.js file directly.
  post: function (file, body, headers) { return run("POST", file, body, headers); },

  // * servertest.put(file: string, body: object|string, headers?: object) → {status, headers, body, ok}
  // Simulates a PUT request to a .server.js file directly.
  put: function (file, body, headers) { return run("PUT", file, body, headers); },

  // * servertest.patch(file: string, body: object|string, headers?: object) → {status, headers, body, ok}
  // Simulates a PATCH request to a .server.js file directly.
  patch: function (file, body, headers) { return run("PATCH", file, body, headers); },

  // * servertest.del(file: string, headers?: object) → {status, headers, body, ok}
  // Simulates a DELETE request to a .server.js file directly.
  del: function (file, headers) { return run("DELETE", file, null, headers); },

  // * servertest.run(method: string, file: string, body?: any, headers?: object) → {status, headers, body, ok}
  // Runs a custom HTTP method against a .server.js file directly.
  run: function (method, file, body, headers) { return run(method, file, body, headers); }
};
