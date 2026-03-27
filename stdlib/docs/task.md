# task — Parallel Script Execution

The `task` bridge lets you run JavaScript functions or workspace modules concurrently in isolated environments. Each task runs independently, so there are no shared-state conflicts. Values cross the task boundary as JSON.

## API

### `task.run(fn | path, ...args?)` → handleId

Starts a parallel task and immediately returns an opaque handle string you use with `task.join`, `task.done`, `task.all`, and `task.cancel`.

- **`fn`** — a JavaScript function. It is serialized and executed in a new isolated VM. Any extra arguments are passed as the function's arguments.
- **`path`** — a workspace-relative `.js` module path (e.g. `"./worker.js"`).

> **Closures don't carry parent state.** Child VMs are blank — any data the child needs must be passed as arguments or read from shared resources (`fs`, `mem`, etc.).

```javascript
// Pass a function
const h1 = task.run((a, b) => {
  const r = fetch('https://api.example.com/sum?a=' + a + '&b=' + b)
  return r.json().result
}, 3, 4)

// Pass a module path
const h2 = task.run('./worker.js')
```

#### Queue Concurrency Control

Pass `{queue: "name", limit: N}` as the **last** argument to throttle tasks on the same named queue. Tasks exceeding the limit wait in FIFO order. Default limit is 1 (serial).

```javascript
// At most 3 downloads run concurrently
const ids = urls.map(url =>
  task.run(u => fetch(u, {download: ".agent/tmp/" + u.split("/").pop()}), url, {queue: "downloads", limit: 3})
)
task.all.apply(null, ids)

// Serial queue (default limit: 1)
task.run(fn1, {queue: "db-writes"})
task.run(fn2, {queue: "db-writes"})
```

### `task.join(id)` → value

Blocks until the task completes and returns its return value. Throws if the task errored.

```javascript
const result = task.join(h1)
ui.log('sum:', result)
```

### `task.done(id)` → `null | {value, error}`

Non-blocking poll. Returns `null` while still running, or a settled object when done.

```javascript
const status = task.done(h1)
if (status === null) {
  ui.log('still running...')
} else if (status.error) {
  ui.log('failed:', status.error)
} else {
  ui.log('result:', status.value)
}
```

### `task.all(...ids)` → array

Waits for **all** handles to finish (in parallel wall time) and returns an array of their values in the same order as the IDs. Throws if any task errored.

```javascript
const h1 = task.run(() => fetch('https://api.github.com/users/altlimit').json().followers)
const h2 = task.run(() => fetch('https://api.github.com/users/dop251').json().followers)

const [f1, f2] = task.all(h1, h2)
ui.log('altlimit followers:', f1, '| dop251 followers:', f2)
```

### `task.cancel(id)`

Interrupts a running task by cancelling its context. The child VM is stopped and any `fs`/`fetch`/`sys` calls will be aborted.

```javascript
const h = task.run('./long-job.js')
sleep(2000)
task.cancel(h)
```

## Patterns

### Fan-out / fan-in
```javascript
const urls = ['https://api1.com/data', 'https://api2.com/data', 'https://api3.com/data']
const handles = urls.map(url => task.run(u => fetch(u).json(), url))
const results = task.all.apply(null, handles)
ui.log(JSON.stringify(results))
```

### Poll loop
```javascript
const h = task.run('./heavy-compute.js')
while (true) {
  const s = task.done(h)
  if (s !== null) { ui.log('done:', s.value); break }
  sleep(500)
}
```

### Worker module pattern

`worker.js` in workspace:
```javascript
// this module is loaded by task.run('./worker.js')
const data = fs.read('input.json')
const parsed = JSON.parse(data)
output(JSON.stringify(parsed.items.map(i => i.value * 2)))
```

Then from the main script:
```javascript
const h = task.run('./worker.js')
const doubled = task.join(h)
```

## Constraints

- **No shared state** — each task runs in complete isolation. You cannot share variables or closures across the boundary.  
- **JSON boundary** — return values must be JSON-serializable. Functions, `Map`, `Set`, or `undefined` returned from a task become `null` or a string.  
- **All bridges available** — tasks have access to `fs`, `fetch`, `mem`, `sys`, `ui`, `cron`, `crypto`, and `doc` with the same workspace and permissions as the parent.  
- **No nested `task.run`** — spawning tasks from within a task is not recommended and may cause resource exhaustion.  
- **`task.cancel`** interrupts long-running I/O but cannot stop a CPU-bound JS loop mid-instruction.
