# Altclaw — AI Agent Orchestrator

**Altclaw** is an open-source, single-binary AI agent orchestrator that lets AI models *do things* — read and write files, run shell commands, query databases, automate browsers, send emails, manage git repos, deploy web apps, and much more — all inside a secure, workspace-scoped sandbox.

Built in Go with an embedded JavaScript engine ([Goja](https://github.com/dop251/goja)), Altclaw bridges the gap between AI reasoning and real-world system execution. Ship it anywhere — desktop, server, or edge — with zero external dependencies.

---

## ✨ Why Altclaw?

- **One binary, everything included** — No Python, no Node.js, no Docker required (though Docker is supported for sandboxing).
- **30+ built-in bridge APIs** — Filesystem, HTTP, databases, git, email, SSH, cloud storage, browser automation, and more — all accessible to the AI out of the box.
- **Provider-agnostic** — Bring your own API key for OpenAI, Gemini, Claude, Ollama, or any OpenAI-compatible endpoint.
- **Secure by default** — Workspace-jailed filesystem, SSRF-protected HTTP client, optional Docker sandboxing, and approval workflows for sensitive operations.
- **Module marketplace** — Discover, install, and publish reusable modules that extend the agent's capabilities.
- **Multiple interfaces** — Web UI, terminal UI, native desktop GUI, or MCP server for integration with Claude Desktop and Cursor.

---

## Installation

### GUI (Desktop App)

Download pre-built GUI binaries for **macOS**, **Windows**, or **Linux** from the [GitHub Releases](https://github.com/altlimit/altclaw/releases) page.

### CLI

Install via [alt](https://github.com/altlimit/alt) — a stateless, zero-config CLI distribution proxy:

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

Or download pre-built CLI binaries directly from [GitHub Releases](https://github.com/altlimit/altclaw/releases).

---

## Quick Start

```bash
# Start with Web UI — uses current directory as workspace
altclaw .

# Terminal UI mode
altclaw --tui .

# Run a script directly
altclaw run script.js

# Run as MCP server (for Claude Desktop, Cursor, etc.)
altclaw --mcp .
```

Navigate to any project folder, launch Altclaw, and the AI gets sandboxed access to that directory. Configuration (providers, settings, secrets) is managed through the Web UI settings panel and stored locally in `~/.altclaw/`.

---

## Features

### 🤖 AI Agent Runtime

**The Agent Loop** — Altclaw sends your message to an AI provider, scans the response for executable code blocks, runs them in a sandboxed JavaScript engine, feeds results back to the AI, and repeats until a final answer is produced. This iterative loop — up to a configurable max iterations (default 20) — allows the AI to reason, execute, observe, and adapt.

**Smart Context Management** — Prior conversation turns are automatically compacted: intermediate execution logs are stripped, keeping only user questions and final answers. Last successful execution patterns are injected as context so the AI reuses what worked before.

**Sub-Agent Spawning** — Delegate specialized tasks to sub-agents running on different AI providers with `agent.run(task, "providerName")`. Each sub-agent gets its own isolated VM and optional per-provider Docker container.

**Task Parallelism** — `task.run()` spawns parallel child VMs for CPU-bound JS workloads without AI provider involvement. Queue support with concurrency limits keeps resource usage under control.

---

### 🔌 Bridge APIs (30+ Modules)

The AI interacts with the system through synchronous JavaScript bridges — no async/await needed:

| Module | What It Does |
|--------|-------------|
| **`fs`** | Read, write, list, grep, patch, append, search — all path-jailed to the workspace |
| **`fetch`** | SSRF-protected HTTP client with streaming download support |
| **`sys`** | Shell commands via Docker/Podman or local executor |
| **`browser`** | Headless Chrome automation — scrape, screenshot, PDF, form fill, network monitoring. Auto-pierces Shadow DOM. Supports persistent sessions and `{{secrets.NAME}}` for secure credential entry |
| **`db`** | Multi-driver database access — SQLite, PostgreSQL, MySQL, MSSQL with connection pooling |
| **`git`** | Automatic snapshots per chat session, plus full git operations (log, diff, commit, branch, restore) |
| **`blob`** | Cloud storage — AWS S3, Azure Blob, GCP Cloud Storage via gocloud.dev |
| **`mail`** | IMAP + SMTP — list, read, flag, move, send with attachments |
| **`ssh`** | Remote command execution, file upload/download over SSH |
| **`mem`** | Persistent memory with categories (core, learned, note) and auto-expiry — workspace-scoped or global |
| **`secret`** | Secure credential storage with `{{secrets.NAME}}` template expansion in fetch, sys, browser, and blob |
| **`cron`** | Schedule recurring tasks with cron expressions — run scripts or AI conversations on autopilot |
| **`agent`** | Spawn sub-agents with optional provider routing and per-provider Docker images |
| **`task`** | Parallel JS execution with queue support and concurrency limits |
| **`ui`** | Interactive prompts — log messages, ask questions, request file uploads, approval workflows |
| **`mod`** | Search, install, remove, and publish modules from the marketplace |
| **`doc`** | Built-in manuals — `doc.read("fs")`, `doc.find("keyword")`, `doc.all()` |
| **`cache`** | In-process key-value cache with TTL and rate limiting |
| **`csv`** | CSV read/write with query capabilities |
| **`crypto`** | Hashing (SHA-256, MD5, etc.), HMAC, AES encrypt/decrypt, random bytes, key generation |
| **`dns`** | DNS lookups and reverse resolution |
| **`zip`** | Archive creation and extraction (ZIP and tar.gz) |
| **`img`** | Image manipulation — resize, crop, convert, rotate |
| **`log`** | In-memory ring buffer — search and inspect application logs |
| **`chat`** | Cross-conversation access — read messages from other chats |

> All bridge docs are available at runtime via `doc.read("module")`, `doc.find("keyword")`, or `doc.all()`.

---

### 📦 Module Marketplace

Altclaw has a built-in module system that lets you extend the agent's capabilities with reusable JavaScript packages.

**Discover & Install** — Search the public marketplace from the Web UI's Modules panel or programmatically via `mod.search("query")`. Install with one click or via `mod.install("module-name")`.

**Create Your Own** — Any folder with a `package.json` and `index.js` can be a module. Right-click a folder in the file explorer → "Install as Module" to register it instantly.

**Publish to the Marketplace** — Submit your module for public listing with ed25519 cryptographic signing for tamper-proof ownership verification. Modules are reviewed by admins before appearing in search results.

**Scoped Installation** — Install modules at the workspace level (project-specific) or user level (shared across all workspaces).

```js
// Using an installed module
var greet = require("my-module");
greet.hello("World");

// Searching and installing from code
var results = mod.search("github");
mod.install("github", "workspace");
```

**Module Structure:**
```
my-module/
  package.json    ← name, version, description, keywords
  index.js        ← entry point (module.exports)
  README.md       ← docs shown in UI and marketplace
```

---

### 🌐 Interfaces

**Web UI** — A Vue.js 3 SPA with:
- Real-time SSE chat streaming with markdown rendering
- Monaco-powered code editor with syntax highlighting and diff view
- File browser with drag-and-drop upload
- Git integration panel (status, diff, commit, restore)
- Memory inspector and management
- Module manager with marketplace browsing
- Cron job scheduler
- Provider configuration with multi-provider support
- Token usage dashboard with daily tracking
- Secrets management panel
- Execution history viewer
- Search across workspace files
- Push notifications via Web Push
- Passkey (WebAuthn/FIDO2) authentication

**Terminal UI** — A Bubble Tea v2 TUI with chat, status display, and provider switching — for when you prefer the terminal.

**GUI Mode** — Native desktop window via Wails v3 (optional build tag) with system browser integration.

**MCP Server** — Expose workspace tools via Model Context Protocol (JSON-RPC 2.0) for Claude Desktop, Cursor, and other MCP clients. Works over both HTTP and stdio transports.

---

### 🔧 Server-Side JavaScript

The AI can create full-stack web applications right inside the workspace:

- **Dynamic Endpoints** — Drop a `.server.js` file into the public directory and it becomes a live HTTP endpoint with fetch-style `function(req) → Response` API.
- **Static File Serving** — Configure a public directory and Altclaw serves static files alongside dynamic endpoints.
- **Tunnel Access** — Expose local workspaces to the internet via the built-in tunnel with custom subdomains and zero-trust authentication.
- **Push Notifications** — Web push support via VAPID protocol for real-time notifications.

```js
// public/api/hello.server.js
module.exports = function(req) {
  return { message: "Hello from Altclaw!" };
};
```

---

### 🤝 MCP (Model Context Protocol)

#### Expose Custom Tools (MCP Server)

Create JS files in `.agent/mcp/` to expose them as tools for Claude Desktop, Cursor, and other MCP clients:

```js
// .agent/mcp/read_file.js
/** @name read_file @description Read a workspace file */
// inputSchema: {"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}
module.exports = function(params) {
  return fs.read(params.path);
};
```

**Connect via stdio (Claude Desktop / Cursor):**
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

#### Connect to External MCP Servers (MCP Client)

```js
var mcp = require("mcp");
var client = mcp.connect({ url: "http://example.com/mcp" });
var tools = client.tools();
var result = client.call("read_file", { path: "/workspace/README.md" });
client.close();
```

---

### 🔒 Security & Execution

**Dual Executors:**
- **Docker/Podman** (default, auto-detected) — Each agent session gets its own isolated container. Bounded networks prevent container escape. Per-provider Docker images for specialized environments.
- **Local** — Direct `os/exec` with optional command whitelist for trusted environments.

**Workspace Sandbox:**
- All filesystem operations jailed via symlink-aware path resolution
- SSRF protection on HTTP client — private IPs blocked by default, configurable whitelist
- Secrets never exposed to JS code — expanded server-side via `{{secrets.NAME}}` in trusted bridges only
- Approval workflows via `ui.confirm()` for sensitive operations (module installs, system commands)

**Pausable Deadlines** — Execution timeouts pause automatically when the agent blocks on sub-agent results, so child execution time doesn't penalize the parent.

---

### 🔑 AI Providers

| Provider | Type |
|----------|------|
| **OpenAI** | GPT-4, GPT-4o, etc. |
| **Google Gemini** | Gemini Pro, Flash, etc. |
| **Anthropic** | Claude Sonnet, Opus, etc. |
| **Ollama** | Any local model |
| **OpenAI-Compatible** | Grok (xAI), DeepSeek, Mistral, OpenRouter, Perplexity, Hugging Face, MiniMax, GLM — or any custom endpoint |

**Multi-Provider Routing** — Configure named providers with descriptions. The default provider's system prompt includes a specialist directory, enabling the AI to delegate tasks via `agent.run(task, "providerName")`.

**Rate Limiting & Token Caps** — Sliding-window RPM limiter per endpoint with per-provider overrides. Daily token caps (prompt + completion) with per-workspace and per-provider tracking.

**Profile Provisioning** — Connect to an Altclaw Hub to receive provider configurations, secrets, and settings remotely — useful for team deployments where admins manage API keys centrally.

---

### 🧠 Persistent Memory

The agent remembers across conversations:

- **Core** — Permanent identity and preference entries
- **Learned** — Lessons and patterns (auto-expire after 30 days)
- **Notes** — Short-term observations (auto-expire after 7 days)

Memory is scoped to the workspace or shared globally across all workspaces. The AI can save, search, promote, and manage memory entries programmatically.

---

### 📊 Observability

- **Token Tracking** — Per-workspace and per-provider daily usage with configurable caps
- **Execution History** — Every code block execution persisted with code, AI response, result, iteration number, and provider info
- **Log Buffer** — In-memory ring buffer (200 entries) accessible via the `log` bridge, web API, and UI panel
- **Rate Monitoring** — Per-endpoint sliding-window RPM with configurable limits

---

## Architecture

```
cmd/altclaw/          CLI entrypoint (Cobra) — web, TUI, GUI, run, and --mcp modes
internal/
  agent/              Core orchestrator loop — send → extract → execute → feed back
  bridge/             30+ Goja-to-Go bridge APIs (fs, fetch, sys, db, git, mail, etc.)
  engine/             Goja VM wrapper — CommonJS require(), pausable deadlines, lifecycle
  provider/           AI model interface — OpenAI, Gemini, Anthropic, Ollama + compatible
  executor/           Execution backends — Docker/Podman (session-isolated) and local
  config/             SQLite store (dsorm) — workspaces, chats, providers, memory, secrets
  serverjs/           Server-side JS handler for .server.js endpoints
  mcp/                MCP server — JSON-RPC 2.0 handler, .agent/mcp/ tool scanner
  cron/               Cron scheduler — script mode (Goja VM) and AI task mode
  tunnel/             Tunnel client for remote access (yamux multiplexing)
  netx/               Loopback port management and SSRF IP filtering
  util/               Shared utilities — rate limiting, patch helpers, IP utils
  search/             Full-text search utilities
stdlib/               Embedded JS modules (web, mcp, pkg, servertest)
web/                  HTTP API server — SSE streaming, auth, file browser, passkeys
vue/                  Vue.js 3 SPA frontend (Vite, TypeScript, PrimeVue, Monaco Editor)
tui/                  Bubble Tea v2 terminal interface
build/                Cross-compilation targets (linux, darwin, windows × amd64, arm64)
```

---

## Build

```bash
# Standard build
go build -o altclaw ./cmd/altclaw/

# With native GUI (Wails v3)
go build -tags gui -o altclaw ./cmd/altclaw/

# Run tests
go test ./...

# Cross-compile
GOOS=linux GOARCH=arm64 go build -o altclaw-linux-arm64 ./cmd/altclaw/
```

---

## Use Cases

- **Code & Project Assistance** — The agent reads your codebase, writes code, runs tests, and iterates until things work — all within the workspace sandbox.
- **Data Processing & Integration** — Connect to SQL databases, cloud storage, and REST APIs to build automated data pipelines.
- **Browser Automation** — Scrape websites, fill forms, take screenshots, monitor network requests — with persistent sessions and Shadow DOM support.
- **Task Automation** — Schedule recurring tasks with the built-in cron system. Run AI conversations or scripts on autopilot.
- **Dynamic Web Apps** — The agent writes both frontend and backend code, spins up a web server, and (optionally) exposes it via tunnel.
- **DevOps & Infrastructure** — SSH into servers, manage git repos, orchestrate deployments, and monitor systems.
- **Email Processing** — Read, filter, and respond to IMAP mailboxes. Send rich emails with attachments.
- **Team Deployment** — Use Hub profiles to centrally manage providers and secrets across multiple Altclaw instances.

---

## License

GNU Affero General Public License v3 (AGPLv3)
