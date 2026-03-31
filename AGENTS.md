# Altclaw — AI Agent Orchestrator (Agent Context)

Single-binary Go application (`altclaw.ai` module, Go 1.25) that orchestrates AI conversations with JavaScript code execution via an embedded Goja engine. All JS bridge APIs are **synchronous** — no async/await/Promises.

## Architecture Overview

```
cmd/altclaw/main.go        → CLI entrypoint (Cobra). Web, TUI, GUI, `run`, and `--mcp` modes.
internal/
  agent/agent.go           → Core orchestrator loop. Sends messages → extracts ```exec blocks →
                              executes in Goja → feeds results back. Loops up to MaxIter (default 20).
  provider/provider.go     → AI model interface: Chat, ChatStream, ListModels, Name.
                              Implementations: openai.go, gemini.go, anthropic.go, ollama.go.
                              OpenAI-compatible presets: grok, deepseek, mistral, openrouter,
                              perplexity, hugging_face, minimax, glm.
  engine/engine.go         → Goja VM wrapper. Registers ALL bridges at construction. Manages:
                              - CommonJS require() with custom SourceLoader
                              - Pausable deadline timer (pauses during agent.result() blocks)
                              - Module resolution: builtin bridges → filesystem → stdlib
                              - Lifecycle: Run(), RunModule(), Cleanup()
  bridge/                  → 30+ Goja-to-Go bridge APIs (one file per module):
    fs.go                  → fs.read/write/list/grep/patch/append — path-jailed to workspace
    fetch.go               → fetch(url, opts) — SSRF-protected HTTP client, {download: "path"} for streaming
    sys.go                 → sys.call/spawn/getOutput/terminate/setImage — via executor
    ui.go                  → ui.log/ask/file — user interaction (Log, Ask, Confirm handlers)
    agent.go               → agent.run(task, providerName?)/agent.result(id) — sub-agent spawning
    task.go                → task.run(code)/task.result(id) — parallel child VM execution
    browser.go             → Headless Chrome via go-rod (scrape, snap, print, fill, links, listen)
    db.go                  → db.connect/query/exec/close — multi-driver (SQLite, Postgres, MySQL, MSSQL)
    blob.go                → blob.connect/read/write/list/delete — cloud storage (S3, Azure, GCP)
    git.go                 → git.clone/commit/push/diff/log/branch — full git operations
    mail.go                → mail.connect/list/read/flag/move/send — IMAP + SMTP
    ssh.go                 → ssh.connect/exec — remote command execution
    mem.go                 → mem.add/recent/core/all/search/rm/promote — persistent memory with scope
    secret.go              → secret.set/get/delete — OS-native keyring storage (Keychain, wincred, libsecret)
    cache.go               → cache.set/get/delete — in-process key-value cache with TTL
    cron.go                → cron.add/rm/list — scheduled tasks/scripts
    doc.go                 → doc.read/list/find/all — module manuals and discovery
    dynamic_doc.go         → Runtime-generated docs (settings, config) via ConfirmContext
    module.go              → mod.install/remove/list — module management with confirmation
    confirm.go             → ui.confirm(action, params) — approval workflow bridge
    csv.go                 → csv.read/write/query — CSV operations
    crypto.go              → crypto.hash/encode/decode — hashing and encoding
    dns.go                 → dns.lookup — DNS resolution
    zip.go                 → zip.create/extract — archive operations
    img.go                 → img.resize/crop/convert — image manipulation
    log.go                 → log.search/tail — in-memory slog ring buffer
    chat.go                → chat.list/read — cross-conversation access
    proxy.go               → Internal proxy helpers for executor port forwarding
    path.go                → Workspace-relative path utilities
    pool.go                → Connection pooling for task child VMs
    registry.go            → Bridge registration metadata (BuiltinNames)
    pathcheck.go           → Symlink-aware path jailing (SanitizePath)
    util.go                → Stringify, parameter validation helpers
  executor/                → Command execution backends:
    executor.go            → Interface: Run, Spawn, GetOutput, Terminate, SetImage, Popen, Info
    docker.go              → Docker/Podman with session isolation, bounded networks, proxy forwarding
    local.go               → Direct os/exec with optional command whitelist
  config/
    store.go               → SQLite database via dsorm — workspaces, chats, messages, history,
                              providers, memory, secrets, cron jobs, token usage, settings
    models.go              → Data models: Workspace, Chat, ChatMessage, History, Provider, Memory, etc.
    profile.go             → Remote profile sync (provider/secret injection via tunnel)
    secret.go              → OS-native encryption key management
    store_settings.go      → Typed settings accessors (rate limits, token caps, message window, etc.)
  serverjs/serverjs.go     → Server-side JS handler for .server.js endpoints in public dirs
  mcp/                     → MCP server: JSON-RPC 2.0 handler, .agent/mcp/ tool scanner
  cron/                    → Cron scheduler: script mode (Goja) and AI task mode (agent.Send)
  tunnel/                  → Tunnel client using yamux multiplexing
  netx/                    → Loopback port management and SSRF IP filtering
  util/                    → Shared utilities: patch helpers, rate limiting, IP utils
  search/                  → Full-text search utilities
  buildinfo/               → Build version injection
stdlib/
  stdlib.go                → Embedded module loader (go:embed). Load("web") → web.js source
  web.js                   → Server-side JS module: Response class, routing, static files
  mcp.js                   → MCP client module: connect (HTTP/stdio), tools, call
  pkg.js                   → Package manager bridge for Docker containers
  servertest.js            → Endpoint testing utility
  docs/                    → 30 markdown files: one per bridge module + globals + manual
web/
  server.go                → HTTP server: SSE EventHub, auth (passkey + password), static files
  api_chat.go              → Chat API: create, send (SSE stream), list, delete, rename
  api_config.go            → Config API: providers CRUD, executor settings, workspace settings
  api_files.go             → File browser API: list, read, write, delete, upload
  api_git.go               → Git API: status, diff, commit, log, branches
  api_modules.go           → Module manager API: install, remove, list, create, edit
  api_tunnel.go            → Tunnel API: connect/disconnect, pairing, domain management
  api_memory.go            → Memory API: list, add, delete
  api_cron.go              → Cron API: list, add, delete
  api_history.go           → Execution history API
  api_logs.go              → Log buffer API
  api_run.go               → Script execution API
  api_secrets.go           → Secrets API
  api_stats.go             → Token usage statistics API
  api_render.go            → Markdown rendering API
  api_mcp.go               → MCP server endpoint
  passkey.go               → WebAuthn/FIDO2 registration and authentication
  confirm_ctx.go           → Confirm context: approval workflows via SSE
  events.go                → SSE EventHub for real-time streaming
  discover.go              → mDNS service discovery
  push.go                  → Web push notification support
vue/                       → Vue.js 3 SPA (Vite + TypeScript + PrimeVue)
tui/                       → Bubble Tea v2 terminal interface
build/                     → Cross-compilation targets: linux, darwin, windows × amd64, arm64
```

## Key Patterns

### Agent Loop (`internal/agent/agent.go`)

The agent sends user messages to an AI provider, scans responses for ` ```exec` blocks, executes them in the Goja engine, and feeds execution results back into the conversation. This loops up to `MaxIter` (default 20, configurable via settings) until the AI responds with a ` ```md` block (final answer) or plain text (no code).

**Smart context management:**
- Prior conversation turns are compacted: intermediate exec messages (code blocks + result feedback) are stripped, keeping only user questions and final assistant responses.
- A configurable message window controls how many prior messages are sent (setting: `MessageWindow`).
- Last successful execution patterns from DB history are injected as context so the AI reuses working patterns.
- An iteration ledger tracks what was executed and deduplicates identical results via FNV-1a hashing.

**Error handling:**
- Provider API calls have a 120-second timeout with up to 3 retries and exponential backoff.
- Consecutive errors and small results are tracked to trigger early termination hints.
- User messages sent mid-execution are drained and injected at the start of the next iteration.

### Provider Interface (`internal/provider/provider.go`)

All AI backends implement `Chat`, `ChatStream`, `ListModels`, and `Name`. Providers are instantiated via `provider.Build(type, apiKey, model, baseURL, host)`.

**Rate limiting:** Per-provider sliding-window RPM limiter + per-endpoint concurrency semaphore. Both are configurable at global and provider level.

**Token tracking:** Every provider call returns `TokenCounts{Prompt, Completion}`. Usage is persisted per-workspace and per-provider to SQLite for daily cap enforcement.

**Remote profiles:** Profile-based providers (injected via tunnel pairing) can use a RelayTransport for secure proxying through the tunnel connection.

### Engine & VM (`internal/engine/engine.go`)

Bridges are registered on the Goja VM at `engine.New()`. The agent bridge (`SetAgentRunner`) and confirm context (`SetConfirmContext`) are set separately to break circular dependencies.

**Module resolution order:**
1. Bridge globals — `require("fs")` → `module.exports = fs;` (short-circuit)
2. Filesystem module directories — workspace modules dir, then user modules dir
3. Stdlib — embedded Go modules (`web`, `mcp`, `pkg`, `servertest`)
4. Workspace-relative files — `require("./helper.js")`, `require("/abs/path.js")`

**Execution model:**
- `Run(ctx, code)` — wraps code in an IIFE, executes with pausable deadline timer
- `RunModule(ctx, instructions)` — loads via CommonJS require or wraps inline code, calls exported function
- `output(value)` — panics with `doneSignal` to cleanly stop execution and return a value
- `store` object persists across iterations within a single conversation turn

### Executor (`internal/executor/`)

Two backends:
- **Docker/Podman** (`docker.go`): Session-isolated containers. Default container for main agent, separate containers per sub-agent session. Bounded networks, proxy forwarding for host port access. Image switching via `sys.setImage()`.
- **Local** (`local.go`): Direct `os/exec`. Optional command whitelist for security. Used for development or trusted environments.

Auto-detection: tries Docker first, then Podman, falls back to "none" (sys.call disabled).

### Sub-Agent Spawning (`internal/bridge/agent.go`)

`agent.run(task, providerName?)` spawns a goroutine with a fresh Goja VM and a new Agent. Optional second argument selects a named provider (with optional per-provider Docker image). `agent.result(id)` blocks until completion — the parent engine's deadline is paused during the wait.

Sub-agents have separate `store` memory (not shared with parent). They cannot recursively spawn sub-agents. Their final ` ```md` block response is returned to the parent.

### Server-Side JS (`internal/serverjs/`)

Files named `*.server.js` in the workspace's public directory are served as dynamic endpoints. Each request creates a fresh Goja VM with all bridges registered plus `Response` global. Endpoints use the fetch-style API: `function(req) → Response`.

### Cron (`internal/cron/`)

Two modes:
- **Script mode** — Executes JS via `engine.RunModule()` in a fresh VM
- **AI task mode** — Sends the instruction to `agent.Send()` as a user message

Both modes support broadcast output to the web UI's SSE hub.

### Configuration (`internal/config/`)

SQLite database via [dsorm](https://github.com/altlimit/dsorm) in `~/.altclaw/`. Key models:

| Model | Purpose |
|-------|---------|
| `Workspace` | Path, public dir, tunnel config, executor overrides, settings |
| `Provider` | Name, type, API key, model, base URL, rate limit, daily caps |
| `Chat` / `ChatMessage` | Conversation persistence and message history |
| `History` | Code block execution log with results (debugging) |
| `Memory` | Persistent key-value entries with categories and auto-expiry |
| `Secret` | OS-native keyring secrets (workspace-scoped) |
| `CronJob` | Scheduled tasks with cron expressions |
| `TokenUsage` | Daily token counters per workspace and per provider |

Settings are typed accessors in `store_settings.go`: rate limits, token caps, message window, max iterations, confirm-on-install, SSRF whitelist, etc.

## Build & Test

```bash
# Standard build
go build -o altclaw ./cmd/altclaw/

# With GUI (Wails v3 desktop window)
go build -tags gui -o altclaw ./cmd/altclaw/

# Run tests
go test ./...

# Cross-compile (see build/ directory for targets)
GOOS=linux GOARCH=arm64 go build -o altclaw-linux-arm64 ./cmd/altclaw/
```

## Conventions & Rules

### JavaScript Runtime
- All JS bridge APIs are **synchronous** — no async/await/Promises in Goja (ECMAScript 5.1).
- `output(value)` stops execution immediately (via panic/recover with `doneSignal`) and passes the value to the next conversation turn.
- `store` object is an in-memory JS object that persists across code execution iterations within a single conversation turn. It is NOT shared between agents or conversation turns.
- System prompts are rebuilt fresh each `Send()` call to pick up memory changes.

### Filesystem & Security
- Path operations are jailed to workspace directory via `bridge.SanitizePath()` (symlink-aware).
- `fetch()` has SSRF protection — blocks private IPs unless whitelisted. Response size capped at 32MB in-memory; use `{download: "path"}` for larger files.
- Docker executor uses bounded networks to prevent container escape. Host port access is explicitly whitelisted via `AllowPort()`.

### History & Debugging
- Every executed code block is saved to the `History` table with: code, AI response, execution result, iteration/block number, provider name, and agent type (main/sub-agent).
- History entries are identified by a per-turn `TurnID` (hex nanosecond timestamp).

### Module System
- User modules: `{configDir}/modules/{slug}/index.js` (stored in user config directory)
- Workspace modules: `{configDir}/{workspaceID}/modules/{slug}/index.js` (stored in user config directory)
- Stdlib modules: embedded in binary via `go:embed` in `stdlib/stdlib.go`
- Bridge shims: `require("fs")` returns the global `fs` object (no file I/O, just `module.exports = fs;`)

### Provider Routing
- Providers are configured as named entries in the store's `Provider` table.
- Each provider can have: `description` (shown to AI), `rate_limit` (RPM override), `daily_prompt_cap`, `daily_completion_cap`, `docker_image` (per-provider container).
- The default provider's system prompt includes a "Specialist Providers" section built from `store.ProviderSummary()`.
- Sub-agents inherit the parent's `NewProvider` factory, which resolves providers by name from the store.

### Web Server
- SSE streaming via `EventHub` for real-time chat responses and panel updates.
- Authentication: session-based with auto-login URLs (password generated at startup), WebAuthn passkeys, and GUI mode bypass.
- API follows REST conventions: `/api/chat`, `/api/config`, `/api/files`, `/api/git`, etc.
- Static files served from the embedded Vue.js SPA.

### Cron System
- Jobs stored in `CronJob` table, keyed by workspace ID.
- Script jobs get a fresh Goja VM with all bridges; AI jobs call `agent.Send()`.
- Output broadcasts to the web UI's SSE hub with `⏰` prefix for visual distinction.
- Cron scripts can call `agent.run()` / `agent.result()` for AI-powered scheduled tasks.

### Testing
- Unit tests co-located with source: `*_test.go` files in each package.
- `serverjs_test.go` has comprehensive endpoint testing.
- `bridge/*_test.go` tests individual bridge functions.
- `stdlib_test.go` tests module loading and resolution.
- Integration tests use the `servertest.js` stdlib module.
