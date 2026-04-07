package bridge

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"

	"altclaw.ai/internal/config"
	"github.com/dop251/goja"
)

var secretRegex = regexp.MustCompile(`\{\{secrets\.([a-zA-Z0-9_-]+)\}\}`)

// ExpandSecrets replaces {{secrets.NAME}} placeholders with actual secret values.
// Resolution order: workspace-scoped secret first, then user-level (empty namespace).
// The workspace ID is resolved from store.Workspace() automatically.
func ExpandSecrets(ctx context.Context, store *config.Store, input string) string {
	if store == nil || input == "" {
		return input
	}
	return secretRegex.ReplaceAllStringFunc(input, func(match string) string {
		name := match[10 : len(match)-2] // strip {{secrets. and }}

		// Try workspace-scoped first
		if ws := store.Workspace(); ws != nil {
			sec, err := store.GetSecret(ctx, ws.ID, name)
			if err == nil && sec != nil {
				slog.Debug("ExpandSecrets: resolved", "name", name, "ns", ws.ID, "scope", "workspace")
				return sec.Value
			}
			slog.Debug("ExpandSecrets: miss", "name", name, "ns", ws.ID, "err", err)
		} else {
			slog.Debug("ExpandSecrets: no workspace on store")
		}
		// Fall back to user-level (empty namespace)
		sec, err := store.GetSecret(ctx, "", name)
		if err == nil && sec != nil {
			slog.Debug("ExpandSecrets: resolved", "name", name, "scope", "user-level")
			return sec.Value
		}
		slog.Debug("ExpandSecrets: not found", "name", name, "err", err)
		return match // Leave unexpanded if not found
	})
}

// RegisterSecret adds the secret namespace to the runtime.
func RegisterSecret(vm *goja.Runtime, store *config.Store, workspace string, ctxFn ...func() context.Context) {
	if store == nil {
		return
	}
	secObj := vm.NewObject()
	getCtx := defaultCtxFn(ctxFn)

	// secret.list() — returns array of available secret names
	secObj.Set("list", func(call goja.FunctionCall) goja.Value {
		if workspace == "" {
			return vm.ToValue([]string{})
		}
		ctx := getCtx()
		var names []string
		if secs, err := store.ListSecrets(ctx, workspace); err == nil {
			for _, s := range secs {
				names = append(names, s.ID)
			}
		}
		return vm.ToValue(names)
	})

	// secret.exists(name) — returns true if secret exists
	secObj.Set("exists", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 || workspace == "" {
			return vm.ToValue(false)
		}
		name := call.Arguments[0].String()
		ctx := getCtx()
		_, err := store.GetSecret(ctx, workspace, name)
		return vm.ToValue(err == nil)
	})

	// secret.get(name) — returns the {{secrets.NAME}} placeholder for safe usage.
	// The actual value is never exposed to JS; bridges like git auth and fetch
	// expand the placeholder at the Go level right before use.
	secObj.Set("get", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			Throw(vm, "secret.get requires a name argument")
		}
		name := call.Arguments[0].String()
		return vm.ToValue(fmt.Sprintf("{{secrets.%s}}", name))
	})

	// secret.set(name, value)
	secObj.Set("set", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 2 {
			return vm.ToValue(false)
		}
		name := call.Arguments[0].String()
		value := call.Arguments[1].String()
		ctx := getCtx()

		if workspace == "" {
			return vm.ToValue(false)
		}

		sec := &config.Secret{
			ID:        name,
			Workspace: workspace,
			Value:     value,
		}
		err := store.SaveSecret(ctx, sec)
		if err == nil {
			// Immediately scrub plain-text value from recent DB history
			_ = store.MaskRecentSecrets(ctx, workspace, value, fmt.Sprintf("[REDACTED: {{secrets.%s}}]", name))
		}
		return vm.ToValue(err == nil)
	})

	// secret.rm(name)
	secObj.Set("rm", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			return vm.ToValue(false)
		}
		name := call.Arguments[0].String()
		ctx := getCtx()

		if workspace == "" {
			return vm.ToValue(false)
		}

		err := store.DeleteSecret(ctx, workspace, name)
		return vm.ToValue(err == nil)
	})

	vm.Set(NameSecret, secObj)
}
