# Modules

A module is a reusable JavaScript package stored on the filesystem that any agent script can load with `require()`.

## Directory layout

```
ConfigDir/
  modules/                    ← user-level (shared across all workspaces)
    {slug}/                   ← module folder (slug = unique name, no slashes)
      package.json            ← required: declares name, version, description
      index.js                ← entry point
      utils.js                ← internal helpers (optional)
      README.md               ← documentation (shown in UI and by doc.read)

  {wsID}/modules/             ← workspace-scoped (overrides user-level when slug matches)
    {slug}/
```

**Workspace modules take priority** over user-level modules with the same slug.

---

## package.json

Every module **must** have a `package.json` at its root. This is used for version display, marketplace publishing, and search indexing.

```json
{
  "name": "my-module",
  "version": "1.0.0",
  "description": "A short description shown in the marketplace",
  "keywords": ["ai", "utility", "browser"],
  "main": "index.js"
}
```

| Field | Required | Purpose |
|-------|----------|---------|
| `name` | ✓ | Must match the folder (slug). Used as the `require()` name. |
| `version` | ✓ | Semver string. Required to publish. |
| `description` | recommended | Shown in the marketplace listing. |
| `keywords` | recommended | Used for search indexing on the marketplace. |
| `main` | optional | Defaults to `index.js`. |

---

## index.js — entry point

```js
// my-module/index.js
var utils = require("./utils.js");  // relative require, jailed to this folder

module.exports = {
  greet: function(name) {
    return "Hello, " + name + "!";
  },
  helper: utils.helper,
};
```

- Use `require("./file.js")` for internal files (relative path, jailed to the module folder).
- Use `require("other-slug")` to depend on another installed module.
- `module.exports` is what callers get when they `require("my-module")`.

---

## README.md

Include a `README.md` at the module root. It is displayed in the Modules panel when a user clicks on the module and is also shown in the marketplace.

```markdown
# my-module

A short description of what this module does.

## Usage

\`\`\`js
var greet = require("my-module");
sys.log(greet.greet("World"));  // Hello, World!
\`\`\`

## API

### greet(name)
Returns a greeting string for `name`.
```

---

## require() resolution

```js
require("my-module")           // → my-module/index.js  (via package.json "main")
require("my-module/utils.js")  // → my-module/utils.js  (specific file, jailed)
require("web")                 // → stdlib built-in (if no installed module matches)
```

Installed modules take priority over stdlib for bare names. Extension is optional when requiring the entry point.

---

## Creating a new module (step by step)

1. **Create a folder** in your workspace — the folder name becomes the slug, e.g. `my-module/`
2. **Add `package.json`** with at minimum `name` and `version`
3. **Add `index.js`** with your exports
4. **Add `README.md`** (optional but recommended)
5. **Right-click the folder** in the file explorer → **Install as Module** (workspace or user scope)

After installing, test with:
```js
var m = require("my-module");
```

---

## Publishing to the Marketplace

Publishing makes your module publicly available to other Altclaw users. Publishing requires admin approval.

### Requirements before publishing
- `package.json` with `name`, `version`, and ideally `description` + `keywords`
- `README.md` is strongly recommended
- The module must be installed locally (workspace or user scope)

### Publish flow
1. Open the **Modules panel** in the sidebar
2. Click your installed module
3. If the hub recognises the slug as yours (by your instance's ed25519 public key), you'll see **"You own this"** and a **Publish** or **Publish Update** button
4. Click **Publish** — your module is zipped, signed, and submitted for admin review
5. Once an admin approves, it appears in marketplace search results

### Version bumping
Update `version` in `package.json` before publishing an update. The UI compares your local version against the current hub version and only shows **Publish Update** if your local version is higher.

### Ownership
Ownership is tied to your Altclaw instance's **module public key** (auto-generated at first startup, stored encrypted in config). The first submission of a slug registers that key as the owner — subsequent submissions from a different key are rejected.

---

## Scope

| Scope | Location | Visibility |
|-------|----------|------------|
| `workspace` | `ConfigDir/{wsID}/modules/` | Only this workspace |
| `user` | `ConfigDir/modules/` | All workspaces on this instance |
| `marketplace` | Hub + public CDN | All Altclaw users (after admin approval) |

---

## Documentation helpers

```js
doc.read("my-module")    // returns README.md content (or JSDoc block if no README)
doc.list()               // lists all available modules (installed + stdlib)
doc.find("keyword")      // fuzzy search by name/description
```
