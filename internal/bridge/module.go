package bridge

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"altclaw.ai/internal/buildinfo"
	"altclaw.ai/internal/util"
	"github.com/dop251/goja"
)

// ModuleContext provides access to module install/remove operations.
// Implemented by the web server to keep bridge code decoupled.
type ModuleContext interface {
	ModuleInstall(id, scope string) (map[string]any, error)
	ModuleRemove(id, scope string) (map[string]any, error)
}

// RegisterModule adds the mod namespace to the runtime:
//
//	mod.search(query)       — search the marketplace
//	mod.install(id, scope?) — install via ui.confirm
//	mod.remove(id, scope?)  — remove via ui.confirm
//	mod.info(id)            — inspect a local module
//	mod.list()              — list installed modules
//
// moduleDirsFn: lazy accessor for module directories (set after construction via WithModuleDirs).
// handler: UIHandler for status logging and ui.confirm prompts.
// modCtxFn: lazy accessor for ModuleContext (nil until SetConfirmContext).
// needsConfirm: returns true if the workspace has confirm_mod_install enabled.
func RegisterModule(vm *goja.Runtime, moduleDirsFn func() []string, handler UIHandler, modCtxFn func() ModuleContext, needsConfirm func() bool) {
	obj := vm.NewObject()

	// mod.search(query) — search marketplace modules via hub API.
	obj.Set("search", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			Throw(vm, "mod.search requires a query argument")
		}
		query := call.Arguments[0].String()

		if handler != nil {
			handler.Log(fmt.Sprintf("🔍 Searching marketplace for %q...", query))
		}

		results, err := hubSearchModules(buildinfo.HubURL, query)
		if err != nil {
			Throwf(vm, "mod.search: %v", err)
		}
		return vm.ToValue(results)
	})

	// mod.install(id, scope?) — install a marketplace module (gated by ui.confirm).
	obj.Set("install", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			Throw(vm, "mod.install requires a module id argument")
		}
		id := call.Arguments[0].String()
		scope := "user"
		if len(call.Arguments) > 1 && !goja.IsUndefined(call.Arguments[1]) && !goja.IsNull(call.Arguments[1]) {
			if _, ok := call.Arguments[1].Export().(string); !ok {
				Throw(vm, "mod.install: scope must be a string (\"user\" or \"workspace\"), got object")
			}
			scope = call.Arguments[1].String()
		}

		if handler == nil {
			Throw(vm, "mod.install: ui handler not available")
		}

		// Gate through ui.confirm (only when workspace setting is enabled)
		if needsConfirm() {
			summary := fmt.Sprintf("Install Module — id: %s, scope: %s", id, scope)
			answer := handler.Confirm("module.install", "Install Module", summary, map[string]any{
				"id":    id,
				"scope": scope,
			})
			if !util.IsApproved(answer) {
				Throwf(vm, "user rejected: installation of module %q was denied", id)
			}
		}

		ctx := modCtxFn()
		if ctx == nil {
			Throw(vm, "mod.install: module management not available in this mode")
		}

		installResult, err := ctx.ModuleInstall(id, scope)
		if err != nil {
			Throwf(vm, "mod.install: %v", err)
		}
		return vm.ToValue(installResult)
	})

	// mod.remove(id, scope?) — remove an installed module (gated by ui.confirm).
	obj.Set("remove", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			Throw(vm, "mod.remove requires a module id argument")
		}
		id := call.Arguments[0].String()
		scope := "user"
		if len(call.Arguments) > 1 && !goja.IsUndefined(call.Arguments[1]) && !goja.IsNull(call.Arguments[1]) {
			if _, ok := call.Arguments[1].Export().(string); !ok {
				Throw(vm, "mod.remove: scope must be a string (\"user\" or \"workspace\"), got object")
			}
			scope = call.Arguments[1].String()
		}

		if handler == nil {
			Throw(vm, "mod.remove: ui handler not available")
		}

		// Gate through ui.confirm (only when workspace setting is enabled)
		if needsConfirm() {
			summary := fmt.Sprintf("Remove Module — id: %s, scope: %s", id, scope)
			answer := handler.Confirm("module.remove", "Remove Module", summary, map[string]any{
				"id":    id,
				"scope": scope,
			})
			if !util.IsApproved(answer) {
				Throwf(vm, "user rejected: removal of module %q was denied", id)
			}
		}

		ctx := modCtxFn()
		if ctx == nil {
			Throw(vm, "mod.remove: module management not available in this mode")
		}

		removeResult, err := ctx.ModuleRemove(id, scope)
		if err != nil {
			Throwf(vm, "mod.remove: %v", err)
		}
		return vm.ToValue(removeResult)
	})

	// mod.info(id) — inspect a locally installed module.
	// Returns {id, scope, version, description, readme} or null if not found.
	obj.Set("info", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			Throw(vm, "mod.info requires a module id argument")
		}
		id := call.Arguments[0].String()
		if strings.Contains(id, "..") {
			Throw(vm, "mod.info: invalid module id")
		}

		for i, baseDir := range moduleDirsFn() {
			scope := "user"
			if i == 0 {
				scope = "workspace"
			}
			modDir := filepath.Join(baseDir, id)
			info, err := os.Stat(modDir)
			if err != nil || !info.IsDir() {
				continue
			}

			entry := map[string]any{
				"id":    id,
				"scope": scope,
			}

			// Read package.json
			if pkgData, err := os.ReadFile(filepath.Join(modDir, "package.json")); err == nil {
				var pkg struct {
					Version     string `json:"version"`
					Description string `json:"description"`
					Name        string `json:"name"`
				}
				if json.Unmarshal(pkgData, &pkg) == nil {
					entry["version"] = pkg.Version
					entry["description"] = pkg.Description
				}
			}

			// Read README.md
			for _, rname := range []string{"README.md", "readme.md", "README.txt"} {
				if data, err := os.ReadFile(filepath.Join(modDir, rname)); err == nil {
					entry["readme"] = strings.TrimSpace(string(data))
					break
				}
			}

			return vm.ToValue(entry)
		}

		return goja.Null()
	})

	// mod.list() — list all installed modules.
	// Returns [{id, scope, version}, ...].
	obj.Set("list", func(call goja.FunctionCall) goja.Value {
		var entries []map[string]any
		for i, baseDir := range moduleDirsFn() {
			scope := "user"
			if i == 0 {
				scope = "workspace"
			}
			dirs, err := os.ReadDir(baseDir)
			if err != nil {
				continue
			}
			for _, d := range dirs {
				if !d.IsDir() {
					continue
				}
				modPath := filepath.Join(baseDir, d.Name())
				pkgPath := filepath.Join(modPath, "package.json")
				pkgData, err := os.ReadFile(pkgPath)
				if err != nil {
					continue
				}
				var pkg struct {
					Version string `json:"version"`
				}
				_ = json.Unmarshal(pkgData, &pkg)
				entries = append(entries, map[string]any{
					"id":      d.Name(),
					"scope":   scope,
					"version": pkg.Version,
				})
			}
		}
		if entries == nil {
			return vm.ToValue([]map[string]any{})
		}
		return vm.ToValue(entries)
	})

	vm.Set(NameMod, obj)
}

// hubSearchModules queries the hub marketplace API for modules matching query.
func hubSearchModules(hubURL, query string) ([]map[string]any, error) {
	if hubURL == "" {
		return nil, fmt.Errorf("hub URL not configured")
	}
	url := fmt.Sprintf("%s/api/modules?q=%s&limit=20", strings.TrimRight(hubURL, "/"), query)
	resp, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to reach marketplace: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("marketplace returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %v", err)
	}

	var raw []map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("invalid marketplace response: %v", err)
	}

	// Simplify the response to only include useful fields.
	var results []map[string]any
	for _, m := range raw {
		entry := map[string]any{}
		for _, key := range []string{"slug", "name", "description", "installs"} {
			if v, ok := m[key]; ok {
				entry[key] = v
			}
		}
		results = append(results, entry)
	}
	if results == nil {
		return []map[string]any{}, nil
	}
	return results, nil
}
