### [ globals ] - Global Execution Context & Module Reference

Use `doc.read("name")` for the full API of any individual module.

## Globals

* require(path) → module
  Load modules in priority order:
  - require("name")          → built-in stdlib module or any installed module (e.g. require("web"))
  - require("./relative.js") → workspace file, relative to the current file's directory
  - require("/abs/path.js")  → workspace file by absolute path within workspace jail
  - require("fs"), require("mem"), etc. → bridge global shims
* sleep(ms: number) → void
  Pause execution synchronously.
* output(value?: any) → void
  Passes a value back to your context for the next turn. It does NOT end the conversation—it just gives you data to look at before you construct your final answer in a markdown block.
* store → object
  In-memory object that persists across JS code blocks within a single conversation turn. Perfect for temporary state between iterations.
* console.log(args...)
  Alias for ui.log.
* process.env.CTX
  Execution context: "agent", "cron", or "server".
* process.env.PORT
  The port the Altclaw web server is listening on (e.g., "9090"). Use for self-testing MCP tools via HTTP.
* process.env.HOSTNAME
  Current hostname (available in server context, e.g., "mysite.altclaw.ai").
* process.env.EXECUTOR
  Active executor type: "docker", "podman", "local", or "" (auto).
* process.env.EXECUTOR_IMAGE
  Container image in use (e.g., "ubuntu:latest"). Only set when EXECUTOR is "docker" or "podman".
* process.version
  Altclaw version string.

## Built-in Modules

- **doc** — Read manuals: `doc.read("fs")`, `doc.all()`, `doc.find("search")`
- **fs** — File I/O: `fs.read`, `fs.write`, `fs.patch`, `fs.grep`, `fs.list`, `fs.find`, `fs.stat`, `fs.copy`, `fs.move`, `fs.rm`, `fs.append`, `fs.readLines`, `fs.lineCount`, `fs.mkdir`, `fs.exists`, `fs.search`
- **mem** — Memory: `mem.add(text, category)`, `mem.list()`, `mem.rm(id)`. Categories: core, learned, notes.
- **fetch** — HTTP: `fetch(url)`, `fetch(url, {method, body, headers})`, `fetch(url, {download: "path"})`
- **ui** — User interaction: `ui.log(msg)`, `ui.ask(question)`, `ui.file(path)`

## Advanced Modules

- **sys** — System commands: `sys.call(cmd, args)`, `sys.spawn(cmd, args)`, `sys.getOutput(pid)`, `sys.terminate(pid)`, `sys.setImage(img)`
- **cron** — Scheduled tasks: `cron.add(schedule, code)`, `cron.rm(id)`, `cron.list()`
- **agent** — Sub-agents: `agent.run(task)`, `agent.run(task, "provider")`, `agent.result(id)`
- **task** — Parallel execution: `task.run(code)`, `task.join(id)`, `task.all(ids)`. Queue support: `task.run(fn, {queue: "name", limit: 3})`
- **secret** — Credentials: `secret.list()`, `secret.exists(name)`, `secret.set(name, val)`, `secret.rm(name)`. Use `{{secrets.NAME}}` in fetch/sys/blob.
- **crypto** — Key generation: `crypto.generateKeyPairSync(type, opts)`
- **web** — Headless Chrome: `require("web")`. Methods: go, text, snap, fill, links, listen, close.
- **db** — Databases: `db.connect(driver, connStr)` → handle with `.query()`, `.exec()`, `.close()`. Drivers: sqlite, postgres, mysql, mssql.
- **blob** — Cloud storage: `blob.open(driver, bucket, opts)` → handle with `.read()`, `.write()`, `.list()`, `.rm()`, `.download()`, `.upload()`, `.stat()`. Drivers: s3, gs, azblob.
- **log** — Application logs: `log.recent(n?)`, `log.search(query)`, `log.info(msg, ...)`, `log.warn(msg, ...)`, `log.error(msg, ...)`, `log.debug(msg, ...)`
- **dns** — DNS lookups: `dns.lookup(hostname, type?)`, `dns.reverse(ip)`
- **cache** — TTL cache: `cache.set(key, val, ttl?)`, `cache.get(key)`, `cache.del(key)`, `cache.has(key)`, `cache.rate(key, limit, windowSec)`
- **zip** — Archives: `zip.create(files, out)`, `zip.extract(archive, dest)`, `zip.list(archive)`. Supports .zip and .tar.gz.
- **img** — Images: `img.info(path)`, `img.resize(src, dst, opts)`, `img.crop(src, dst, opts)`, `img.convert(src, dst)`, `img.rotate(src, dst, deg)`
- **ssh** — Remote exec: `ssh.connect({host, user, key?, password?})` → `handle.exec(cmd)`, `handle.upload()`, `handle.download()`, `handle.close()`
- **mail** — Email: `mail.send({host, from, to, subject, body})`, `mail.connect({host, user, pass})` → `handle.list()`, `handle.read()`, `handle.download()`, `handle.close()`
- **chat** — Conversations: `chat.list()`, `chat.read(chatID)` — read-only access to other workspace chats

## Loadable Modules (require)

- **pkg** — Auto-install system packages: `var pkg = require("pkg"); pkg("curl", "git");`
- **servertest** — Server endpoint tester: `var st = require("servertest"); st.post("public/api.server.js", data);`
- **mcp** — MCP client for external tool servers. See `doc.read("mcp")`.
- **mcpserver** — Expose tools as MCP server via .agent/mcp/ files. See `doc.read("mcpserver")`.
- **server** — Dynamic JS endpoints from `public/*.server.js`. See `doc.read("server")`.
