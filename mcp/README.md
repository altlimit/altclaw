# altclaw/mcp Docker Image

Docker image for running MCP (Model Context Protocol) servers inside Altclaw's Docker executor.

Includes:
- **Node.js 22** — for `npx` based MCP servers
- **Python 3** — for Python-based MCP servers
- **uv** — fast Python package installer for `uvx` based MCP servers

## Build

```bash
docker build -t altclaw/mcp ./build/mcp/
```

## Usage

The AI agent uses this image via the `mcp` module:

```js
var mcp = require("mcp");
var client = mcp.connect({
  command: "npx",
  args: ["-y", "@modelcontextprotocol/server-filesystem", "/workspace"],
  image: "altclaw/mcp"  // switches Docker to this image
});

var tools = client.tools();
var result = client.call("read_file", {path: "/workspace/README.md"});
client.close();
```
