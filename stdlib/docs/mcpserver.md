### [ mcpserver ] - MCP Server — Exposing Tools

Altclaw is an MCP dual-citizen: it can act as both an MCP server (exposing tools) and an MCP client (connecting to external servers).

To expose tools, create JS files in {workspace}/.altclaw/mcp/. Each file becomes an MCP tool automatically.

[ File Format ]
```js
/** @name tool_name @description What the tool does */
// inputSchema: {"type":"object","properties":{"param":{"type":"string"}},"required":["param"]}
module.exports = function(params) {
  // Full bridge access: fs, fetch, sys, mem, etc.
  return "result string or JSON.stringify(object)";
};
```

[ Metadata ]
* @name — tool name (defaults to filename without .js)
* @description — human-readable description
* // inputSchema: — JSON Schema for the tool's input parameters

[ Examples ]

```js
// .altclaw/mcp/read_file.js
/** @name read_file @description Read a file from workspace */
// inputSchema: {"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}
module.exports = function(params) { return fs.read(params.path); };
```

```js
// .altclaw/mcp/search_code.js
/** @name search_code @description Search for text across workspace files */
// inputSchema: {"type":"object","properties":{"query":{"type":"string"},"glob":{"type":"string"}},"required":["query"]}
module.exports = function(params) {
  var results = fs.search(params.glob || "**/*");
  var matches = [];
  results.forEach(function(f) {
    var hits = fs.grep(f, params.query);
    if (hits.length > 0) matches.push({file: f, hits: hits});
  });
  return JSON.stringify(matches);
};
```

[ Connecting ]
External clients connect via HTTP (POST /mcp) or stdio (altclaw --mcp /workspace).

Self-test from agent code:
```js
var mcp = require("mcp");
var c = mcp.connect({url: "http://localhost:" + process.env.PORT + "/mcp"});
var tools = c.tools();
var result = c.call("tool_name", {param: "value"});
c.close();
```

Claude Desktop config:
```json
{"mcpServers":{"altclaw":{"command":"altclaw","args":["--mcp","/path/to/workspace"]}}}
```
