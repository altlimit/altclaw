package bridge

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"altclaw.ai/stdlib"
	"github.com/dop251/goja"
)

// DynamicDocFunc returns documentation for a given name dynamically.
// Returns empty string if the name is not handled.
type DynamicDocFunc func(name string) string

// RegisterDoc adds the doc namespace (doc.read, doc.find, doc.list, doc.all) to the runtime.
// moduleDirsFn is a lazy accessor for module directories (may be populated after construction).
// dynamicDoc is optional — if non-nil, it's checked before user modules for dynamic docs.
func RegisterDoc(vm *goja.Runtime, moduleDirsFn func() []string, dynamicDoc DynamicDocFunc) {
	obj := vm.NewObject()

	// Per-turn cache: prevents redundant doc.read calls within the same
	// conversation turn. Small docs (≤3000 chars) are fully preserved in the
	// iteration ledger, so re-reading wastes tokens. Large docs may have been
	// truncated in the ledger, so re-reads are allowed.
	const ledgerFullThreshold = 3000
	readCache := make(map[string]string) // name → cached content

	// doc.read(name, ...) — returns docs for one or more bridges/built-in modules/user modules.
	// Priority per name: stdlib/docs MD → stdlib JS signatures → moduleDirs README.md → moduleDirs JSDoc
	obj.Set("read", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			Throw(vm, "doc.read requires at least one name argument")
		}

		// Collect all requested names
		var names []string
		for _, arg := range call.Arguments {
			names = append(names, arg.String())
		}

		// Log status
		if uiObj := vm.Get(NameUI); uiObj != nil {
			if logFn, ok := goja.AssertFunction(uiObj.ToObject(vm).Get("log")); ok {
				logFn(goja.Undefined(), vm.ToValue("📖 Reading '"+strings.Join(names, "', '")+"' documentation..."))
			}
		}

		var allParts []string
		for _, name := range names {
			// Cache check: if already read this turn and content was small enough
			// to be fully preserved in the iteration ledger, return a hint instead.
			if cached, seen := readCache[name]; seen {
				if len(cached) <= ledgerFullThreshold {
					allParts = append(allParts,
						"[Already read '"+name+"' this turn ("+
							strconv.Itoa(len(cached))+" chars, fully available in your iteration log). "+
							"Use the data from your previous read — do NOT re-read.]")
					continue
				}
				// Large doc — may have been truncated in the ledger; allow re-read.
			}

			var parts []string

			// 1. Embedded stdlib doc (stdlib/docs/<name>.md)
			doc := stdlib.Doc(name)
			if doc != "" {
				parts = append(parts, "=== "+name+" ===\n"+doc+"\n=== end ===")
			}

			// 2. Stdlib JS function signatures
			sigs := stdlib.Signatures(name)
			if sigs != "" {
				parts = append(parts, "=== "+name+" signatures ===\n"+sigs+"\n=== end ===")
			}

			// 3. Dynamic doc provider (settings, config, etc.)
			if len(parts) == 0 && dynamicDoc != nil {
				if content := dynamicDoc(name); content != "" {
					parts = append(parts, "=== "+name+" ===\n"+content+"\n=== end ===")
				}
			}

			// 4. User module README.md or JSDoc (searched in moduleDirs order)
			if len(parts) == 0 {
				for _, baseDir := range moduleDirsFn() {
					if content := readModuleDoc(baseDir, name); content != "" {
						parts = append(parts, "=== "+name+" ===\n"+content+"\n=== end ===")
						break
					}
				}
			}

			if len(parts) == 0 {
				msg := "No documentation found for '" + name + "'."
				if bestName, ok := stdlib.FindName(name); ok && bestName != name {
					msg += " Did you mean '" + bestName + "'? Use doc.read('" + bestName + "'), or use doc.find('" + name + "') to search all documentation."
				} else {
					msg += " Use doc.find('" + name + "') to search all documentation."
				}
				allParts = append(allParts, msg)
			} else {
				combined := strings.Join(parts, "\n\n")
				readCache[name] = combined // cache for dedup on re-reads
				allParts = append(allParts, parts...)
			}
		}

		return vm.ToValue(strings.Join(allParts, "\n\n"))
	})

	// doc.find(query) — keyword search across all docs + modules; returns best matching doc string.
	obj.Set("find", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			Throw(vm, "doc.find requires a search query argument")
		}
		query := call.Arguments[0].String()

		var parts []string
		// Check dynamic doc names first (exact match)
		if dynamicDoc != nil {
			for _, dynName := range []string{"settings", "config", "providers"} {
				if strings.EqualFold(query, dynName) {
					if content := dynamicDoc(dynName); content != "" {
						parts = append(parts, "=== "+dynName+" ===\n"+content+"\n=== end ===")
					}
				}
			}
		}

		// Search stdlib
		name, ok := stdlib.FindName(query)
		if ok {
			doc := stdlib.Doc(name)
			sigs := stdlib.Signatures(name)
			if doc != "" {
				parts = append(parts, "=== "+name+" ===\n"+doc+"\n=== end ===")
			}
			if sigs != "" {
				parts = append(parts, "=== "+name+" signatures ===\n"+sigs+"\n=== end ===")
			}
		}

		// Search user modules by ID keyword match
		queryLow := strings.ToLower(query)
		for _, baseDir := range moduleDirsFn() {
			entries := listModuleIDs(baseDir)
			for _, id := range entries {
				if strings.Contains(strings.ToLower(id), queryLow) {
					content := readModuleDoc(baseDir, id)
					if content == "" {
						content = "(no documentation available)"
					}
					parts = append(parts, "=== "+id+" ===\n"+content+"\n=== end ===")
				}
			}
		}

		return vm.ToValue(strings.Join(parts, "\n\n"))
	})

	// doc.list() — returns array of {id, description} for all available modules/bridges.
	obj.Set("list", func(call goja.FunctionCall) goja.Value {
		var names []string
		seen := make(map[string]bool)

		// 1. All embedded docs/*.md names (covers bridges, globals, manual, mcp, server, etc.)
		for _, name := range stdlib.DocList() {
			if seen[name] {
				continue
			}
			seen[name] = true
			names = append(names, name)
		}

		// 2. Embedded stdlib JS modules not already covered by a .md doc
		for _, m := range stdlib.List() {
			if seen[m.Name] {
				continue
			}
			seen[m.Name] = true
			names = append(names, m.Name)
		}

		// 3. Dynamic doc names (settings, config, providers)
		// Always include these when a confirm context is wired — they're valid docs
		// even if content generation might be temporarily empty.
		if dynamicDoc != nil {
			for _, dynName := range []string{"settings", "config", "providers"} {
				if seen[dynName] {
					continue
				}
				seen[dynName] = true
				names = append(names, dynName)
			}
		}

		// 4. User modules from filesystem (workspace first, then user)
		for _, baseDir := range moduleDirsFn() {
			for _, id := range listModuleIDs(baseDir) {
				if seen[id] {
					continue
				}
				seen[id] = true
				names = append(names, id)
			}
		}

		return vm.ToValue(names)
	})

	// doc.all() — returns all docs combined (bridges + built-in module signatures).
	obj.Set("all", func(call goja.FunctionCall) goja.Value {
		var parts []string
		for _, name := range stdlib.DocList() {
			doc := stdlib.Doc(name)
			if doc != "" {
				parts = append(parts, "=== "+name+" ===\n"+doc+"\n=== end ===")
			}
		}
		for _, m := range stdlib.List() {
			sigs := stdlib.Signatures(m.Name)
			if sigs != "" {
				parts = append(parts, "=== "+m.Name+" signatures ===\n"+sigs+"\n=== end ===")
			}
		}
		return vm.ToValue(strings.Join(parts, "\n\n"))
	})

	vm.Set(NameDoc, obj)
}

// ── filesystem helpers ────────────────────────────────────────────────────────

// listModuleIDs returns all module IDs found in a base directory.
// Supports both single-level slug layout ({slug}/index.js) and legacy two-level ({author}/{name}).
func listModuleIDs(baseDir string) []string {
	var ids []string
	dirs, err := os.ReadDir(baseDir)
	if err != nil {
		return nil
	}
	for _, d := range dirs {
		if !d.IsDir() {
			// top-level .js file (legacy single-file module without author)
			if strings.HasSuffix(d.Name(), ".js") {
				ids = append(ids, strings.TrimSuffix(d.Name(), ".js"))
			}
			continue
		}
		slug := d.Name()
		subDir := filepath.Join(baseDir, slug)
		// Check if this looks like a slug-level module (has index.js or package.json directly)
		hasIndex := fileExists(filepath.Join(subDir, "index.js")) ||
			fileExists(filepath.Join(subDir, "package.json"))
		if hasIndex {
			ids = append(ids, slug)
			continue
		}
		// Legacy: treat as author dir, enumerate one level deeper
		subs, _ := os.ReadDir(subDir)
		for _, sub := range subs {
			name := sub.Name()
			if sub.IsDir() {
				ids = append(ids, slug+"/"+name)
			} else if strings.HasSuffix(name, ".js") {
				ids = append(ids, slug+"/"+strings.TrimSuffix(name, ".js"))
			}
		}
	}
	return ids
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// readModuleDoc looks for documentation for a module ID in priority order:
//  1. {baseDir}/{id}/README.md  (or readme.md)
//  2. First JSDoc block (/** */) inside the module's entry file
func readModuleDoc(baseDir, id string) string {
	// 1. README in module folder
	for _, rname := range []string{"README.md", "readme.md", "README.txt"} {
		if data, err := os.ReadFile(filepath.Join(baseDir, id, rname)); err == nil {
			return strings.TrimSpace(string(data))
		}
	}

	// 2. JSDoc from the module entry file
	for _, suffix := range []string{"/index.js", ".js", ""} {
		src, err := os.ReadFile(filepath.Join(baseDir, id+suffix))
		if err != nil {
			continue
		}
		if doc := extractJSDoc(string(src)); doc != "" {
			return doc
		}
	}
	return ""
}

// modDescription returns a one-line description from a module's docs.
func modDescription(baseDir, id string) string {
	content := readModuleDoc(baseDir, id)
	if content == "" {
		return ""
	}
	for _, ln := range strings.Split(content, "\n") {
		ln = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(ln, "/**"), "*"))
		ln = strings.TrimSuffix(ln, "*/")
		ln = strings.TrimSpace(ln)
		if ln != "" && !strings.HasPrefix(ln, "#") {
			return ln
		}
	}
	return ""
}

// extractJSDoc returns the first /** ... */ block from JS source, or empty string.
func extractJSDoc(src string) string {
	start := strings.Index(src, "/**")
	if start < 0 {
		return ""
	}
	end := strings.Index(src[start:], "*/")
	if end < 0 {
		return ""
	}
	return strings.TrimSpace(src[start : start+end+2])
}
