package bridge

import (
	"context"
	"fmt"
	"strings"

	"altclaw.ai/internal/config"
	"altclaw.ai/internal/util"
	"github.com/dop251/goja"
)

// ConfirmContext provides server-side access for executing confirmed actions.
// Implemented by the web server to keep bridge code decoupled from web package.
type ConfirmContext interface {
	Store() *config.Store
	Workspace() *config.Workspace
	BroadcastCtx() context.Context
	RebuildAgent()
	TunnelConnect() (map[string]any, error)
	TunnelDisconnect() error
	TunnelPair(code string) (map[string]any, error)
	TunnelUnpair() error
	ModuleInstall(id, scope string) (map[string]any, error)
	ModuleRemove(id, scope string) (map[string]any, error)
}

// confirmAction describes a single gated action the LLM can propose.
type confirmAction struct {
	Label    string // human-readable label shown in the UI card
	Handler  func(ctx ConfirmContext, params map[string]any) (any, error)
	Validate func(params map[string]any) error // optional pre-validation before showing UI
}

// confirmRegistry is the fixed set of actions the LLM can propose.
var confirmRegistry = map[string]confirmAction{
	"tunnel.connect":    {Label: "Enable Tunnel", Handler: handleTunnelConnect},
	"tunnel.disconnect": {Label: "Disable Tunnel", Handler: handleTunnelDisconnect},
	"tunnel.pair":       {Label: "Pair with Hub", Handler: handleTunnelPair},
	"tunnel.unpair":     {Label: "Unpair from Hub", Handler: handleTunnelUnpair},
	"provider.add":      {Label: "Add Provider", Handler: handleProviderAdd},
	"provider.update":   {Label: "Update Provider", Handler: handleProviderUpdate},
	"provider.delete":   {Label: "Delete Provider", Handler: handleProviderDelete},
	"settings.update":   {Label: "Update Settings", Handler: handleSettingsUpdate, Validate: validateSettingsUpdate},
	"sys.exec":          {Label: "Execute Command", Handler: handleSysExec},
	"module.install":    {Label: "Install Module", Handler: handleModuleInstall},
	"module.remove":     {Label: "Remove Module", Handler: handleModuleRemove},
}

// RegisterConfirm adds ui.confirm(action, params) to the runtime.
func RegisterConfirm(vm *goja.Runtime, handler UIHandler, confirmCtx ConfirmContext) {
	// Get the existing "ui" object
	uiVal := vm.Get(NameUI)
	if uiVal == nil || goja.IsUndefined(uiVal) {
		return
	}
	ui := uiVal.ToObject(vm)

	ui.Set("confirm", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			Throw(vm, "ui.confirm requires an action argument")
		}

		actionName := call.Arguments[0].String()

		// Look up action in registry
		action, ok := confirmRegistry[actionName]
		if !ok {
			// List available actions
			var names []string
			for k := range confirmRegistry {
				names = append(names, k)
			}
			Throwf(vm, "ui.confirm: unknown action %q. Available: %s", actionName, strings.Join(names, ", "))
		}

		// Extract params
		params := make(map[string]any)
		if len(call.Arguments) > 1 && !goja.IsUndefined(call.Arguments[1]) && !goja.IsNull(call.Arguments[1]) {
			obj := call.Arguments[1].ToObject(vm)
			for _, key := range obj.Keys() {
				params[key] = obj.Get(key).Export()
			}
		}

		// Pre-validate params before showing UI (catches bad field names early)
		if action.Validate != nil {
			if err := action.Validate(params); err != nil {
				Throwf(vm, "ui.confirm(%q): %s", actionName, err.Error())
			}
		}

		// Build summary from action + params
		summary := buildSummary(action.Label, params)

		// Ask user for confirmation (blocks like ui.ask)
		answer := handler.Confirm(actionName, action.Label, summary, params)

		result := vm.NewObject()

		if !util.IsApproved(answer) {
			Throwf(vm, "user rejected: %s", actionName)
		}

		// No confirm context (headless/TUI without server) — can't execute
		if confirmCtx == nil {
			result.Set("approved", true)
			result.Set("error", "action execution not available in this mode")
			return result
		}

		// Execute the action handler server-side
		actionResult, err := action.Handler(confirmCtx, params)
		result.Set("approved", true)
		if err != nil {
			result.Set("error", err.Error())
		} else {
			result.Set("result", vm.ToValue(actionResult))
		}

		return result
	})
}

// buildSummary creates a human-readable summary of an action + its params.
func buildSummary(label string, params map[string]any) string {
	if len(params) == 0 {
		return label
	}
	var parts []string
	for k, v := range params {
		// Mask sensitive fields
		s := fmt.Sprintf("%v", v)
		if strings.Contains(strings.ToLower(k), "key") || strings.Contains(strings.ToLower(k), "secret") || strings.Contains(strings.ToLower(k), "password") || strings.Contains(strings.ToLower(k), "token") {
			if len(s) > 8 {
				s = s[:4] + "..." + s[len(s)-4:]
			} else {
				s = "****"
			}
		}
		parts = append(parts, fmt.Sprintf("%s: %s", k, s))
	}
	return label + " — " + strings.Join(parts, ", ")
}

// --- Action handlers ---

func handleTunnelConnect(ctx ConfirmContext, params map[string]any) (any, error) {
	return ctx.TunnelConnect()
}

func handleTunnelDisconnect(ctx ConfirmContext, params map[string]any) (any, error) {
	return map[string]string{"status": "disconnected"}, ctx.TunnelDisconnect()
}

func handleTunnelPair(ctx ConfirmContext, params map[string]any) (any, error) {
	code, _ := params["code"].(string)
	if code == "" {
		return nil, fmt.Errorf("tunnel.pair requires a 'code' parameter")
	}
	return ctx.TunnelPair(code)
}

func handleTunnelUnpair(ctx ConfirmContext, params map[string]any) (any, error) {
	return map[string]string{"status": "unpaired"}, ctx.TunnelUnpair()
}

func handleProviderAdd(ctx ConfirmContext, params map[string]any) (any, error) {
	store := ctx.Store()
	p := &config.Provider{}
	if v, ok := params["name"].(string); ok {
		p.Name = strings.TrimSpace(v)
	}
	if p.Name == "" {
		p.Name = "default"
	}
	if v, ok := params["provider"].(string); ok {
		p.ProviderType = v
	}
	if v, ok := params["model"].(string); ok {
		p.Model = v
	}
	if v, ok := params["api_key"].(string); ok {
		p.APIKey = v
	}
	if v, ok := params["base_url"].(string); ok {
		p.BaseURL = v
	}
	if v, ok := params["host"].(string); ok {
		p.Host = v
	}
	if v, ok := params["description"].(string); ok {
		p.Description = v
	}

	if p.ProviderType == "" {
		return nil, fmt.Errorf("provider.add requires a 'provider' type parameter")
	}

	if err := store.SaveProvider(ctx.BroadcastCtx(), p); err != nil {
		return nil, err
	}
	ctx.RebuildAgent()
	return map[string]any{"status": "saved", "id": p.ID, "name": p.Name}, nil
}

func handleProviderUpdate(ctx ConfirmContext, params map[string]any) (any, error) {
	store := ctx.Store()

	// Find provider by id or name
	var p *config.Provider
	var err error
	if id, ok := params["id"].(int64); ok && id > 0 {
		p = &config.Provider{ID: id}
		if err = store.Client.Get(context.Background(), p); err != nil {
			return nil, fmt.Errorf("provider id %d not found", id)
		}
	} else if idF, ok := params["id"].(float64); ok && idF > 0 {
		p = &config.Provider{ID: int64(idF)}
		if err = store.Client.Get(context.Background(), p); err != nil {
			return nil, fmt.Errorf("provider id %d not found", int64(idF))
		}
	} else if name, ok := params["name"].(string); ok && name != "" {
		p, err = store.GetProvider(name)
		if err != nil {
			return nil, fmt.Errorf("provider %q not found", name)
		}
	} else {
		return nil, fmt.Errorf("provider.update requires 'id' or 'name' to identify the provider")
	}

	// Apply updates
	if v, ok := params["provider"].(string); ok && v != "" {
		p.ProviderType = v
	}
	if v, ok := params["model"].(string); ok && v != "" {
		p.Model = v
	}
	if v, ok := params["api_key"].(string); ok && v != "" {
		p.APIKey = v
	}
	if v, ok := params["base_url"].(string); ok {
		p.BaseURL = v
	}
	if v, ok := params["host"].(string); ok {
		p.Host = v
	}
	if v, ok := params["description"].(string); ok {
		p.Description = v
	}
	if v, ok := params["new_name"].(string); ok && v != "" {
		p.Name = strings.TrimSpace(v)
	}

	if err := store.SaveProvider(ctx.BroadcastCtx(), p); err != nil {
		return nil, err
	}
	ctx.RebuildAgent()
	return map[string]any{"status": "saved", "id": p.ID, "name": p.Name}, nil
}

func handleProviderDelete(ctx ConfirmContext, params map[string]any) (any, error) {
	store := ctx.Store()

	var p *config.Provider
	var err error
	if id, ok := params["id"].(int64); ok && id > 0 {
		p = &config.Provider{ID: id}
	} else if idF, ok := params["id"].(float64); ok && idF > 0 {
		p = &config.Provider{ID: int64(idF)}
	} else if name, ok := params["name"].(string); ok && name != "" {
		p, err = store.GetProvider(name)
		if err != nil {
			return nil, fmt.Errorf("provider %q not found", name)
		}
	} else {
		return nil, fmt.Errorf("provider.delete requires 'id' or 'name'")
	}

	if err := store.DeleteProvider(ctx.BroadcastCtx(), p); err != nil {
		return nil, err
	}
	ctx.RebuildAgent()
	return map[string]string{"status": "deleted"}, nil
}

// validateSettingsUpdate does a dry-run patch against an empty struct to catch
// invalid field names before showing the confirmation card to the user.
func validateSettingsUpdate(params map[string]any) error {
	scope, _ := params["scope"].(string)
	if scope == "" {
		scope = "workspace"
	}

	// Build fields without "scope"
	fields := make(map[string]any, len(params))
	for k, v := range params {
		if k != "scope" {
			fields[k] = v
		}
	}
	if len(fields) == 0 {
		return fmt.Errorf("no fields to update")
	}

	// Dry-run patch against an empty struct to validate field names
	switch scope {
	case "workspace":
		var dummy config.Workspace
		return util.Patch(fields, &dummy)
	case "user":
		var dummy config.AppConfig
		return util.Patch(fields, &dummy)
	default:
		return fmt.Errorf("scope must be 'workspace' or 'user', got %q", scope)
	}
}

func handleSettingsUpdate(ctx ConfirmContext, params map[string]any) (any, error) {
	scope, _ := params["scope"].(string)
	if scope == "" {
		scope = "workspace"
	}

	// Strip scope from the fields to patch
	fields := make(map[string]any, len(params))
	for k, v := range params {
		if k != "scope" {
			fields[k] = v
		}
	}

	switch scope {
	case "workspace":
		ws := ctx.Workspace()
		store := ctx.Store()
		if err := ws.Patch(ctx.BroadcastCtx(), store, fields); err != nil {
			return nil, err
		}
		store.Settings().Init()
		return map[string]string{"status": "saved", "scope": "workspace"}, nil

	case "user":
		store := ctx.Store()
		appCfg := store.Config()
		if err := util.Patch(fields, appCfg); err != nil {
			return nil, err
		}
		if err := store.Client.Put(ctx.BroadcastCtx(), appCfg); err != nil {
			return nil, err
		}
		ctx.RebuildAgent()
		return map[string]string{"status": "saved", "scope": "user"}, nil

	default:
		return nil, fmt.Errorf("settings.update: scope must be 'workspace' or 'user', got %q", scope)
	}
}

// handleSysExec is a pass-through handler for sys.exec confirmations.
// The confirmation itself is the gate — actual command execution happens
// in the sys bridge after the user approves.
func handleSysExec(_ ConfirmContext, params map[string]any) (any, error) {
	cmd, _ := params["command"].(string)
	return map[string]string{"status": "approved", "command": cmd}, nil
}

func handleModuleInstall(ctx ConfirmContext, params map[string]any) (any, error) {
	id, _ := params["id"].(string)
	scope, _ := params["scope"].(string)
	if id == "" {
		return nil, fmt.Errorf("module.install requires an 'id' parameter")
	}
	if scope == "" {
		scope = "user"
	}
	return ctx.ModuleInstall(id, scope)
}

func handleModuleRemove(ctx ConfirmContext, params map[string]any) (any, error) {
	id, _ := params["id"].(string)
	scope, _ := params["scope"].(string)
	if id == "" {
		return nil, fmt.Errorf("module.remove requires an 'id' parameter")
	}
	if scope == "" {
		scope = "user"
	}
	return ctx.ModuleRemove(id, scope)
}
