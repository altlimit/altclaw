### [ server ] - Server-Side JS Endpoints
**CRITICAL RULE: THIS IS NOT NODE.JS.** Do not use require('http') or require('express'). ONLY the custom globals listed below exist.

[ Routing Rules ]
* Files MUST be placed in the 'public/' directory.
* Static files (.html, .css, .js) in public/ are served as-is and take priority over dynamic routes.
* Server Rendered JS MUST be named: public/{name}.server.js (Routes to /{name})
* Dynamic segments: public/users/[id].server.js → matches /users/123 (req.params.id = "123")
* Dynamic directories: public/users/[id]/posts.server.js → matches /users/42/posts
* Catch-all: public/api/[...path].server.js → matches /api/foo/bar/baz (req.params.path = "foo/bar/baz")
* Index: public/api/index.server.js → matches /api
* Priority: exact match > static directory > dynamic [param] > catch-all [...param]

[ Expected Export Format ]
```js
module.exports = function(req) {
  if (req.method === "POST") {
    fs.append("data.json", JSON.stringify(req.body) + "\n");
    return Response.json({ok: true});
  }
  return Response.sendFile("public/form.html");
}
```

[ Request Object (req) ]
* req.method: string (e.g., "GET", "POST")
* req.url / req.path: string
* req.params: object — dynamic route parameters (e.g., {id: "123"} from [id].server.js)
* req.query / req.headers: object
* req.body: object (auto-parsed for JSON & urlencoded) | string otherwise

[ Response — Global Constructor ]
Follows Web Fetch API pattern. Return a Response from your handler function.

* Response.json(data, init?) → Response — JSON response (auto Content-Type)
* Response.redirect(url, status?) → Response — Redirect (default 302)
* Response.sendFile(path) → Response — Serve a file from workspace
* new Response(body?, init?) → Response — body: string, init: {status, headers}

[ Auto-Detection (bare return values) ]
* return {key: "val"} → auto JSON (application/json)
* return "<h1>Hi</h1>" → auto HTML (text/html)
* return "hello" → auto plain text (text/plain)
* return (nothing) → 204 No Content

[ Server Environment Context ]
* Globals available: fs, fetch, mem, cron, agent (use doc.read("fs") etc. for full API)
* process.env.CTX === "server"
* process.env.PUBLIC_DIR is available
* process.env.HOSTNAME — current request hostname (e.g., "123123-relay.altclaw.ai")
