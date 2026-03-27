# mod — Module Management

The `mod` bridge lets agent scripts search, install, remove, and inspect modules from the marketplace and local filesystem.

## Functions

### mod.search(query)

Search the marketplace for modules matching a keyword.

```js
var results = mod.search("browser");
// → [{slug: "my-browser-tool", name: "Browser Tool", description: "...", installs: 42}, ...]
```

Returns an array of `{slug, name, description, installs}` objects.

---

### mod.install(id, scope?)

Install a module from the marketplace. **Requires user approval** via `ui.confirm`.

```js
var result = mod.install("my-module", "workspace");
// result.approved  → true/false
// result.result    → {status: "installed", id: "my-module"}
// result.error     → string if something went wrong
```

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `id` | string | required | Module slug on the marketplace |
| `scope` | string | `"user"` | `"workspace"` or `"user"` |

---

### mod.remove(id, scope?)

Remove an installed module. **Requires user approval** via `ui.confirm`.

```js
var result = mod.remove("my-module", "workspace");
// result.approved  → true/false
// result.result    → {status: "deleted"}
```

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `id` | string | required | Installed module folder name |
| `scope` | string | `"user"` | `"workspace"` or `"user"` |

---

### mod.info(id)

Inspect a locally installed module. Returns `null` if not found.

```js
var info = mod.info("my-module");
// → {id: "my-module", scope: "workspace", version: "1.0.0", description: "...", readme: "..."}
```

---

### mod.list()

List all installed modules (workspace + user scoped).

```js
var mods = mod.list();
// → [{id: "greet", scope: "user", version: "1.0.0"}, ...]
```

---

## Scopes

| Scope | Location | Visibility |
|-------|----------|------------|
| `workspace` | `ConfigDir/{wsID}/modules/` | Only this workspace |
| `user` | `ConfigDir/modules/` | All workspaces on this instance |

## Notes

- `mod.install` and `mod.remove` are gated by `ui.confirm` — the user must approve the action.
- `mod.search` requires a configured hub URL (configured at build time).
- After installing, modules are available via `require("module-name")` and `doc.read("module-name")`.
