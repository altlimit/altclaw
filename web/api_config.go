package web

import (
	"context"
	"encoding/json"

	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"altclaw.ai/internal/buildinfo"
	"altclaw.ai/internal/config"
	"altclaw.ai/internal/provider"
	"altclaw.ai/internal/util"
	"altclaw.ai/stdlib"
	"github.com/altlimit/restruct"
)

// Docs returns the list of available built-in modules for the UI.
func (a *Api) Docs() any {
	return stdlib.List()
}

// Config returns the current app configuration.
func (a *Api) Config() any {
	return a.server.store.Config()
}

// SaveConfig saves general app config and rebuilds agent.
func (a *Api) SaveConfig(ctx context.Context, req struct {
	Params map[string]any `json:"params"`
}) any {
	appCfg := a.server.store.Config()
	if appCfg.Locked {
		return restruct.Error{Status: http.StatusForbidden, Message: "app config is locked by profile"}
	}
	if err := a.server.store.SaveConfig(ctx, func(c *config.AppConfig) error {
		return util.Patch(req.Params, c)
	}); err != nil {
		return restruct.Error{Status: http.StatusInternalServerError, Err: err}
	}
	a.rebuildAgent()
	return map[string]string{"status": "saved"}
}

func (a *Api) Providers() any {
	providers, err := a.server.store.ListProviders()
	if err != nil {
		return restruct.Error{Status: http.StatusInternalServerError, Err: err}
	}
	return providers
}

// Provider creates or updates a single provider.
func (a *Api) Provider(ctx context.Context, p *config.Provider) any {
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" {
		p.Name = "default"
	}
	if err := a.server.store.SaveProvider(ctx, p); err != nil {
		return restruct.Error{Status: http.StatusInternalServerError, Err: err}
	}
	a.rebuildAgent()
	return map[string]any{"status": "saved", "id": p.ID}
}

// Provider_0 handles GET/DELETE for a single provider.
func (a *Api) Provider_0(r *http.Request) any {
	id, _ := strconv.ParseInt(restruct.Params(r)["0"], 10, 64)
	p := &config.Provider{ID: id}
	if r.Method == http.MethodDelete {
		if err := a.server.store.DeleteProvider(r.Context(), p); err != nil {
			return restruct.Error{Status: http.StatusInternalServerError, Err: err}
		}
		a.rebuildAgent()
		return map[string]string{"status": "deleted"}
	}
	if err := a.server.store.Client.Get(r.Context(), p); err != nil {
		return restruct.Error{Status: http.StatusInternalServerError, Err: err}
	}
	return p
}

// Models lists available models for a provider type + credentials.
func (a *Api) Models(ctx context.Context, req struct {
	Provider string `json:"provider"`
	APIKey   string `json:"api_key"`
	BaseURL  string `json:"base_url"`
	Host     string `json:"host"`
}) any {
	provType := req.Provider
	if provType == "" {
		return restruct.Error{Status: http.StatusBadRequest, Message: "provider is required"}
	}

	prov, err := provider.Build(provType, req.APIKey, "", req.BaseURL, req.Host)
	if err != nil {
		return restruct.Error{Status: http.StatusBadRequest, Message: err.Error()}
	}

	models, err := prov.ListModels(ctx)
	if err != nil {
		return restruct.Error{Status: http.StatusInternalServerError, Message: err.Error()}
	}
	return map[string]any{"models": models}
}

// WorkspaceSettings returns the current workspace settings.
func (a *Api) WorkspaceSettings() any {
	return map[string]any{
		"workspace": a.server.store.Workspace(),
		"hub_url":   buildinfo.HubURL,
		"version":   buildinfo.Version,
	}
}

// SaveWorkspaceSettings saves workspace settings.
func (a *Api) SaveWorkspaceSettings(ctx context.Context, req struct {
	Params map[string]any `json:"params"`
}) any {
	ws := a.server.store.Workspace()
	if ws.Locked {
		return restruct.Error{Status: http.StatusForbidden, Message: "workspace is locked by profile"}
	}
	if err := ws.Patch(ctx, a.server.store, req.Params); err != nil {
		return restruct.Error{Status: http.StatusInternalServerError, Err: err}
	}
	// Re-apply resolved settings
	a.server.store.Settings().Init()
	return map[string]string{"status": "saved"}
}

// OpenTabs retrieves the open tabs JSON securely mapped off local configs.
func (a *Api) OpenTabs(ctx context.Context) any {
	ws := a.server.store.Workspace()
	tabsFile := filepath.Join(a.server.store.Dir(), ws.ID, "tabs.json")
	b, err := os.ReadFile(tabsFile)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{"tabs": []any{}, "active": nil}
		}
		return restruct.Error{Status: http.StatusInternalServerError, Err: err}
	}
	return json.RawMessage(b)
}

// PatchOpenTabs updates the tabs.json metadata dropping away from local SQLite payloads.
func (a *Api) PatchOpenTabs(ctx context.Context, tabs struct {
	OpenTabs json.RawMessage `json:"open_tabs"`
}) any {
	ws := a.server.store.Workspace()

	wsDir := filepath.Join(a.server.store.Dir(), ws.ID)
	if err := os.MkdirAll(wsDir, 0755); err != nil {
		return restruct.Error{Status: http.StatusInternalServerError, Err: err}
	}
	tabsFile := filepath.Join(wsDir, "tabs.json")
	if err := os.WriteFile(tabsFile, tabs.OpenTabs, 0644); err != nil {
		return restruct.Error{Status: http.StatusInternalServerError, Err: err}
	}
	return map[string]string{"status": "saved"}
}

// PatchLastProvider partially updates only the last_provider field.
func (a *Api) PatchLastProvider(ctx context.Context, req struct {
	LastProvider string `json:"last_provider"`
}) any {
	if err := a.server.store.SaveWorkspace(ctx, func(w *config.Workspace) error {
		w.LastProvider = req.LastProvider
		return nil
	}); err != nil {
		return restruct.Error{Status: http.StatusInternalServerError, Err: err}
	}
	return map[string]string{"status": "saved"}
}

// TokenUsage returns the last 30 days of token usage for this workspace.
// Accepts optional ?provider_id=N to filter to per-provider rows.
func (a *Api) TokenUsage(ctx context.Context, req struct {
	ProviderID int64 `query:"provider_id"`
}) any {
	ws := a.server.store.Workspace()
	wsID := ws.ID
	rows, err := a.server.store.GetTokenUsage(ctx, wsID, 30, req.ProviderID)
	if err != nil {
		return restruct.Error{Status: http.StatusInternalServerError, Err: err}
	}
	var today *config.TokenUsage
	if req.ProviderID > 0 {
		today, _ = a.server.store.TodayTokenUsage(ctx, wsID, req.ProviderID)
	} else {
		today, _ = a.server.store.TodayTokenUsage(ctx, wsID)
	}
	return map[string]any{
		"rows":  rows,
		"today": today,
		"limits": map[string]any{
			"rate_limit":           ws.RateLimit,
			"daily_prompt_cap":     ws.DailyPromptCap,
			"daily_completion_cap": ws.DailyCompletionCap,
		},
	}
}

// VapidPublicKey returns the VAPID public key for push subscription.
func (a *Api) VapidPublicKey() any {
	return map[string]string{"public_key": a.server.store.Config().VAPIDPublicKey}
}

// PushSubscribe saves a browser push subscription.
func (a *Api) PushSubscribe(ctx context.Context, req struct {
	Endpoint string `json:"endpoint"`
	P256dh   string `json:"p256dh"`
	Auth     string `json:"auth"`
}) any {
	if req.Endpoint == "" || req.P256dh == "" || req.Auth == "" {
		return restruct.Error{Status: http.StatusBadRequest, Message: "endpoint, p256dh, and auth are required"}
	}
	ws := a.server.store.Workspace()
	// Remove existing subscription with the same endpoint (upsert)
	_ = a.server.store.DeletePushSubscriptionByEndpoint(ctx, ws.ID, req.Endpoint)
	sub := &config.PushSubscription{
		Workspace: ws.ID,
		Endpoint:  req.Endpoint,
		P256dh:    req.P256dh,
		Auth:      req.Auth,
	}
	if err := a.server.store.SavePushSubscription(ctx, sub); err != nil {
		return restruct.Error{Status: http.StatusInternalServerError, Err: err}
	}
	return map[string]string{"status": "subscribed"}
}

// PushUnsubscribe removes a push subscription by endpoint.
func (a *Api) PushUnsubscribe(ctx context.Context, req struct {
	Endpoint string `json:"endpoint"`
}) any {
	if req.Endpoint == "" {
		return restruct.Error{Status: http.StatusBadRequest, Message: "endpoint is required"}
	}
	ws := a.server.store.Workspace()
	if err := a.server.store.DeletePushSubscriptionByEndpoint(ctx, ws.ID, req.Endpoint); err != nil {
		return restruct.Error{Status: http.StatusInternalServerError, Err: err}
	}
	return map[string]string{"status": "unsubscribed"}
}
