### [ mcp ] - MCP Client Module

Altclaw can connect to external MCP servers as a client. Use require("mcp").
Read doc.read("mcpserver") for how to also expose tools as an MCP server.

* mcp.connect(opts) → Client
  HTTP transport:
    `var client = mcp.connect({url: "http://example.com/mcp"});`

  Stdio transport (spawns process in Docker):
    `var client = mcp.connect({command: "npx", args: ["-y", "@modelcontextprotocol/server-filesystem", "/workspace"], image: "altclaw/mcp"});`

* client.tools() → [{name, description, inputSchema}]
  List all tools available on the connected server.

* client.call(name, args) → string
  Call a tool by name with arguments. Returns the text content from the result.

* client.close() → void
  Disconnect and clean up (terminates stdio process if applicable).

[ Self-test — connect to own MCP server ]
```js
var client = mcp.connect({url: "http://localhost:" + process.env.PORT + "/mcp"});
var tools = client.tools();
ui.log("Tools: " + JSON.stringify(tools));
client.close();
```
