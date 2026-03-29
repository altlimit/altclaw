# Altclaw — AI Agent Orchestrator

**Altclaw** is an open-source AI agent orchestrator that bridges the gap between AI reasoning and system execution. It embeds a sandboxed JavaScript engine ([Goja](https://github.com/dop251/goja)) alongside a full suite of bridge APIs, letting AI models read files, run commands, query databases, send emails, manage git repos, and more — all within a controlled, workspace-scoped environment.

Ship it as a single Go binary. Run it anywhere — desktop, server, or edge.

## Installation

### GUI Download

For a native desktop experience, download the pre-built GUI binaries for **macOS**, **Windows**, or **Linux** directly from the [GitHub Releases](https://github.com/altlimit/altclaw/releases) page.

### CLI Install

Install via [alt](https://github.com/altlimit/alt) — a stateless, zero-config CLI distribution proxy, or optionally download the pre-built CLI binaries from the [GitHub Releases](https://github.com/altlimit/altclaw/releases) page.

**Linux & macOS**
```bash
curl -fsSL https://raw.githubusercontent.com/altlimit/alt/main/scripts/install.sh | sh
alt install altlimit/altclaw
```

**Windows (PowerShell)**
```powershell
powershell -Command "iwr https://raw.githubusercontent.com/altlimit/alt/main/scripts/install.ps1 -useb | iex"
alt install altlimit/altclaw
```

---

## Features

### Core Runtime
- **Embedded JavaScript VM** — AI-generated code executes synchronously in [Goja](https://github.com/dop251/goja).
- **Agent Loop** — The orchestrator sends user messages to an AI provider, extracts executable code blocks from responses, runs them, feeds results back, and repeats until a final text answer is produced (up to a configurable max iterations).
- **CommonJS Modules** — Full `require()` support: load embedded stdlib modules, workspace-relative files, or user-installed modules from configurable directories.

### AI Providers
- **Provider-Agnostic** — First-class support for OpenAI, Google Gemini, Anthropic (Claude), and Ollama (local models).
- **OpenAI-Compatible Endpoints** — Built-in presets for Grok (xAI), DeepSeek, Mistral, OpenRouter, Perplexity, Hugging Face, MiniMax, and GLM — or point at any OpenAI-compatible API.
- **Multi-Provider Routing** — Configure named providers with descriptions. The default provider's system prompt includes a specialist provider directory, enabling the AI to delegate tasks via `agent.run(task, "providerName")`.
- **Streaming** — Real-time SSE streaming of AI responses to the Web UI, with ````md` fence filtering for clean output.

### Execution & Security
- **Dual Executors** — Run AI-generated system commands in isolated Docker/Podman containers (default, auto-detected) or directly on the host (local mode with optional command whitelist).
- **Workspace Sandbox** — All filesystem operations are jailed to the workspace directory with symlink-aware path resolution. SSRF protection on the HTTP client.
- **Pausable Deadlines** — Execution timeouts pause automatically when the agent blocks on sub-agent results, so child execution time doesn't penalize the parent.
- **Session Isolation** — Each sub-agent gets its own Docker container session, preventing state leaks between parallel tasks.

### Sub-Agents & Task Parallelism
- **Sub-Agent Spawning** — `agent.run(task, providerName?)` spawns a goroutine with a fresh Goja VM and optional per-provider Docker image. `agent.result(id)` blocks until completion.
- **Task Bridge** — `task.run()` spawns parallel child VMs for CPU-bound JS workloads (no AI provider involved). Lifecycle-managed with automatic cleanup.

### Bridge APIs (30+ modules)

The AI interacts with the system through synchronous JavaScript bridges:

| Module | Description |
|--------|-------------|
| `fs` | Workspace filesystem — read, write, list, grep, patch, append (path-jailed) |
| `fetch` | SSRF-protected HTTP client with streaming download support (`{download: "path"}`) |
| `sys` | Shell commands via executor — call, spawn, getOutput, terminate, setImage |
| `ui` | User interaction — log, ask, file attachments, confirm (approval workflows) |
| `agent` | Sub-agent spawning with provider routing |
| `task` | Parallel child VM execution |
| `mem` | Persistent memory — workspace-scoped and global user-scoped |
| `secret` | Secret storage with bridge expansion support |
| `cron` | Scheduled tasks and scripts with cron expressions |
| `db` | Multi-driver database — SQLite, PostgreSQL, MySQL, MSSQL (connection pooling) |
| `blob` | Cloud storage — AWS S3, Azure Blob, GCP Cloud Storage (via gocloud.dev) |
| `git` | Automatic snapshots per chat session, with full git operations support |
| `browser` | Headless Chrome automation — scrape, snap, print, fill, links, listen |
| `mail` | IMAP + SMTP — connect, list, read, flag, move, send (with attachments) |
| `ssh` | Remote command execution over SSH |
| `dns` | DNS lookups |
| `csv` | CSV read/write with query support |
| `cache` | In-process key-value cache with TTL |
| `zip` | Archive creation and extraction |
| `img` | Image manipulation (resize, crop, convert) |
| `crypto` | Hashing and encoding utilities |
| `log` | In-memory slog ring buffer inspection |
| `chat` | Cross-conversation message access |
| `doc` | Module manuals, discovery, and dynamic docs |

Full API docs available at runtime via `doc.read("module")`, `doc.find("keyword")`, `doc.all()`.

### Loadable Modules (stdlib)
- **`web`** — Server-side JS endpoints (`.server.js`) with fetch-style `Response` API, auto-type detection, redirects, and file serving.
- **`mcp`** — MCP client for connecting to external MCP servers (HTTP and stdio transports).
- **`pkg`** — Package manager bridge for installing system packages inside Docker containers.
- **`servertest`** — Endpoint testing utility for `.server.js` files.

### Interfaces
- **Web UI** — Vue.js 3 SPA with real-time SSE chat streaming, file browser, settings panel, module manager, git integration, and memory inspector.
- **Terminal UI** — Bubble Tea v2 TUI with chat, status display, and provider switching.
- **GUI Mode** — Native desktop window via Wails v3 (optional build tag), with system browser link interception.
- **MCP Server** — Expose workspace tools via Model Context Protocol (JSON-RPC 2.0 over HTTP or stdio) for Claude Desktop, Cursor, and other MCP clients.

### Server-Side JavaScript
- **Dynamic Web Hosting** — The AI can create `.server.js` endpoints in a public directory, spin up a web server with automatic routing, and expose it via the Relay tunnel.
- **Fetch-Style API** — Endpoints use `function(req) → Response` with `Response.json()`, `Response.redirect()`, and `Response.file()` helpers.
- **Push Notifications** — Web push support via the VAPID protocol.
- **Passkey Authentication** — WebAuthn/FIDO2 login for the web server.

### Control Plane
- **Hub** — Central dashboard for managing and monitoring remote Altclaw instances, with billing, user management, and workspace profiles.
- **Relay** — Secure tunnel that exposes local workspaces to the internet with custom subdomains, zero-trust authentication, and yamux-based multiplexing.
- **Worker** — Edge proxy handling custom domains, reserved hostnames, and relay routing.

### Observability
- **Token Tracking** — Per-workspace and per-provider daily token usage with configurable caps (prompt + completion).
- **Rate Limiting** — Sliding-window RPM limiter per endpoint, with per-provider overrides.
- **Execution History** — Every code block execution is persisted to SQLite with code, AI response, result, iteration number, and provider info.
- **Log Buffer** — In-memory ring buffer (200 entries) accessible via the `log` bridge and the web API.

---

## Quick Start

```bash
# Start with Web UI (default) — uses current directory as workspace
altclaw .

# Start with Terminal UI
altclaw --tui .

# Run a JS file directly (uses CWD as workspace)
altclaw run script.js

# Run with explicit workspace
altclaw run script.js /path/to/workspace

# Run as MCP server (for Claude Desktop, Cursor, etc.)
altclaw --mcp .

# Verbose mode (plain stdout logging, no TUI status screen)
altclaw --verbose .
```

## Workspaces

The workspace is the root boundary for the AI. Navigate to any project folder and launch Altclaw — the AI gets sandboxed access to that directory.

```bash
cd ~/projects/myapp
altclaw .    # myapp is now the workspace
```

Configuration is stored in `~/.altclaw/` (SQLite via [dsorm](https://github.com/altlimit/dsorm)). Providers, executor type, timeout, Docker image, and all settings are managed through the Web UI settings panel.

## MCP (Model Context Protocol)

### MCP Server — Expose Custom Tools

Create JS files in `{workspace}/.altclaw/mcp/` to expose them as MCP tools:

```js
// .altclaw/mcp/read_file.js
/** @name read_file @description Read a workspace file */
// inputSchema: {"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}
module.exports = function(params) {
  return fs.read(params.path);
};
```

**Connect via HTTP:**
```bash
curl -X POST http://localhost:9090/mcp \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'
```

**Connect via stdio (Claude Desktop, Cursor, etc.):**
```json
{
  "mcpServers": {
    "altclaw": {
      "command": "altclaw",
      "args": ["--mcp", "/path/to/workspace"]
    }
  }
}
```

### MCP Client — Connect to External Servers

```js
var mcp = require("mcp");

// HTTP transport
var client = mcp.connect({ url: "http://example.com/mcp" });

// Stdio transport (runs in Docker via sys.spawn)
var client = mcp.connect({
  command: "npx",
  args: ["-y", "@modelcontextprotocol/server-filesystem", "/workspace"],
  image: "altclaw/mcp"
});

var tools = client.tools();
var result = client.call("read_file", { path: "/workspace/README.md" });
client.close();
```

## Architecture

```
cmd/altclaw/          CLI entrypoint (Cobra) — web, TUI, GUI, run, and --mcp modes
internal/
  agent/              Core orchestrator loop — send → extract → execute → feed back
  bridge/             30+ Goja-to-Go bridge APIs (fs, fetch, sys, db, git, mail, etc.)
  engine/             Goja VM wrapper — CommonJS require(), pausable deadlines, lifecycle
  provider/           AI model interface — OpenAI, Gemini, Anthropic, Ollama + 8 compatible
  executor/           Execution backends — Docker/Podman (session-isolated) and local
  config/             SQLite store (dsorm), workspace/chat/history models, settings
  serverjs/           Server-side JS handler for .server.js endpoints
  mcp/                MCP server (JSON-RPC 2.0, .mcp/ tool scanner)
  cron/               Cron scheduler with script and AI task modes
  tunnel/             Relay tunnel client (yamux multiplexing)
  netx/               Loopback port management and SSRF protection
  search/             Full-text search utilities
stdlib/               Embedded JS modules (web, mcp, pkg, servertest)
web/                  HTTP API server with SSE streaming, file browser, passkeys
vue/                  Vue.js 3 SPA frontend (Vite, TypeScript, PrimeVue)
tui/                  Bubble Tea v2 terminal interface
build/                Cross-compilation targets (linux, darwin, windows × amd64, arm64)
```

## Build

```bash
go build -o altclaw ./cmd/altclaw/
go test ./...

# With GUI support (Wails v3)
go build -tags gui -o altclaw ./cmd/altclaw/
```

## Use Cases

- **Data Migration & Integration** — Connect to APIs, cloud storage, or SQL databases to automate data flows.
- **Task Automation** — Replace repetitive manual tasks using the built-in cron scheduler.
- **Dynamic Web Hosting** — The agent writes frontend code, spins up its own web server, and exposes it to the internet via the Relay tunnel.
- **DevOps & Infrastructure** — SSH into remote servers, manage git repos, and orchestrate deployments.
- **Email Processing** — Read, filter, and respond to IMAP mailboxes programmatically.

## License

GNU Affero General Public License v3 (AGPLv3)
