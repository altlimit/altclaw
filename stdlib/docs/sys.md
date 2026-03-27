### [ sys ] - System Execution

[ Synchronous Execution ]
* sys.call(cmd: string, options?: string[] | {args?: string[], env?: object}) → {stdout: string, stderr: string, exitCode: number}
  Run command synchronously, wait for completion. Supports passing an optional object with `args` and an `env` dictionary.
  *Note:* To securely pass API tokens, use `env: {"TOKEN": "{{secrets.YOUR_SECRET}}"}`. See `secret` namespace docs.

[ Background Processes ]
* sys.spawn(cmd: string, options?: string[] | {args?: string[], env?: object}) → string (handleID)
  Start background process. Returns handleID.
* sys.getOutput(handleID: string) → string
  Get accumulated output from a spawned process.
* sys.terminate(handleID: string) → void
  Kill a spawned or popen'd process.

[ Interactive Processes (stdin/stdout piping) ]
* sys.popen(cmd: string, options?: string[] | {args?: string[], env?: object}) → string (handleID)
  Start a process with piped stdin and line-buffered stdout. Returns handleID.
* sys.write(handleID: string, data: string) → void
  Write data to the stdin of a popen'd process.
* sys.readLine(handleID: string, timeoutMs?: number) → string
  Read the next line from stdout. Blocks until a line is available or timeout (default 30s).

[ Environment ]
* sys.setImage(name: string, opts?: string | object) → void
  Switch Docker image for this session. Options:
  - String: embedded buildpack name, e.g. sys.setImage("altclaw/mcp", "mcp.Dockerfile")
  - Object: { build: string, volumes?: string[] }
    - build: Dockerfile source. Embedded buildpack name (e.g. "mcp.Dockerfile") or workspace path (starts with "./")
    - volumes: extra volume mounts, e.g. ["altclaw-pkg-cache:/root/.npm"]
  Images are tagged with a content hash — auto-rebuilds only when Dockerfile changes.

* sys.info() → object
  Returns environment introspection data. No arguments needed. The returned object has:
  - host: { engine: "goja", mode: "synchronous", version? } — the JS runtime you are executing in (not Node.js)
  - os: { type, arch, distro?, version?, kernel? }
  - resources: { cpus, memory_total_mb?, disk_free_mb? }
  - runtimes: { node?, python?, git?, go?, ffmpeg?, ruby?, java?, php?, rust?, curl?, wget?, docker?, make?, pip?, npm? } — only tools found on PATH
  - capabilities: { internet_access: bool, executor: "local"|"docker"|"none" }
  - paths: { workspace, home? }
  Use this instead of blind-probing with sys.call to discover the environment.

[ Available Buildpacks ]
* mcp.Dockerfile — Node.js 22 + Python 3 + uv. For MCP servers and general-purpose scripting.
