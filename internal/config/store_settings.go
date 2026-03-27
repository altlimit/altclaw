package config

import (
	"altclaw.ai/internal/netx"
	"log/slog"
	"time"
)

// SettingsResolver provides cascading access to workspace settings with
// automatic fallback to AppConfig user-level defaults and built-in defaults.
// Both Workspace and AppConfig embed CommonSettings, so field names are identical.
// Use store.Settings() to obtain an instance.
type SettingsResolver struct {
	ws  *Workspace
	cfg *AppConfig
}

// Settings returns a SettingsResolver that cascades:
// workspace field → AppConfig user default → built-in default.
// Both workspace and config must already be loaded (GetWorkspace/GetConfig).
func (s *Store) Settings() SettingsResolver {
	return SettingsResolver{ws: s.Workspace(), cfg: s.Config()}
}

// Init applies the resolved settings to the running process.
// Call this once at startup and whenever workspace or user settings are saved.
// Extra slog handlers (e.g. an in-memory log buffer) are forwarded to SetupLogging.
func (r SettingsResolver) Init(extraHandlers ...slog.Handler) {
	if r.ws != nil {
		SetupLogging(r.ws.LogPath, r.LogLevel(), r.ws.LogMaxSize, r.ws.LogMaxFiles, extraHandlers...)
	}
	netx.SetAllowedHosts(r.AllowedHosts())
}

// RateLimit returns the effective per-workspace rate limit (RPM).
// Cascade: workspace → user default → 10.
func (r SettingsResolver) RateLimit() int64 {
	if r.ws != nil && r.ws.RateLimit > 0 {
		return r.ws.RateLimit
	}
	if r.cfg != nil && r.cfg.RateLimit > 0 {
		return r.cfg.RateLimit
	}
	return 10
}

// DailyPromptCap returns the effective daily input-token cap.
// Cascade: workspace → user default → 1M.
func (r SettingsResolver) DailyPromptCap() int64 {
	if r.ws != nil && r.ws.DailyPromptCap > 0 {
		return r.ws.DailyPromptCap
	}
	if r.cfg != nil && r.cfg.DailyPromptCap > 0 {
		return r.cfg.DailyPromptCap
	}
	return 1_000_000
}

// DailyCompletionCap returns the effective daily output-token cap.
// Cascade: workspace → user default → 100k.
func (r SettingsResolver) DailyCompletionCap() int64 {
	if r.ws != nil && r.ws.DailyCompletionCap > 0 {
		return r.ws.DailyCompletionCap
	}
	if r.cfg != nil && r.cfg.DailyCompletionCap > 0 {
		return r.cfg.DailyCompletionCap
	}
	return 100_000
}

// MessageWindow returns the effective message context window size.
// Cascade: workspace → user default → 10.
func (r SettingsResolver) MessageWindow() int64 {
	if r.ws != nil && r.ws.MessageWindow > 0 {
		return r.ws.MessageWindow
	}
	if r.cfg != nil && r.cfg.MessageWindow > 0 {
		return r.cfg.MessageWindow
	}
	return 10
}

// ShowThinking returns whether to stream AI thinking to the UI.
// Cascade: workspace → user default → false.
func (r SettingsResolver) ShowThinking() bool {
	if r.ws != nil && r.ws.ShowThinking {
		return true
	}
	if r.cfg != nil {
		return r.cfg.ShowThinking
	}
	return false
}

// ConfirmModInstall returns whether to prompt before module installs.
// Cascade: workspace → user default → false.
func (r SettingsResolver) ConfirmModInstall() bool {
	if r.ws != nil && r.ws.ConfirmModInstall {
		return true
	}
	if r.cfg != nil {
		return r.cfg.ConfirmModInstall
	}
	return false
}

// IgnoreRestricted returns whether to skip the hidden/gitignored file security gate.
// Cascade: workspace → user default → false.
func (r SettingsResolver) IgnoreRestricted() bool {
	if r.ws != nil && r.ws.IgnoreRestricted {
		return true
	}
	if r.cfg != nil {
		return r.cfg.IgnoreRestricted
	}
	return false
}

// LogLevel returns the effective log level.
// Cascade: workspace → user default → "".
func (r SettingsResolver) LogLevel() string {
	if r.ws != nil && r.ws.LogLevel != "" {
		return r.ws.LogLevel
	}
	if r.cfg != nil && r.cfg.LogLevel != "" {
		return r.cfg.LogLevel
	}
	return ""
}

// IPWhitelist returns the combined IP whitelist from both workspace and user defaults.
// Unlike other settings, both lists are joined (not cascaded) so user defaults
// always apply and workspaces can add additional IPs.
func (r SettingsResolver) IPWhitelist() []string {
	seen := make(map[string]struct{})
	var result []string
	add := func(list []string) {
		for _, ip := range list {
			if _, ok := seen[ip]; !ok {
				seen[ip] = struct{}{}
				result = append(result, ip)
			}
		}
	}
	if r.cfg != nil {
		add(r.cfg.IPWhitelist)
	}
	if r.ws != nil {
		add(r.ws.IPWhitelist)
	}
	return result
}

// AllowedHosts returns the combined allowed-hosts list from both workspace and user defaults.
// Both lists are joined (not cascaded) so user defaults always apply and workspaces
// can add additional hosts.
func (r SettingsResolver) AllowedHosts() []string {
	seen := make(map[string]struct{})
	var result []string
	add := func(list []string) {
		for _, h := range list {
			if _, ok := seen[h]; !ok {
				seen[h] = struct{}{}
				result = append(result, h)
			}
		}
	}
	if r.cfg != nil {
		add(r.cfg.AllowedHosts)
	}
	if r.ws != nil {
		add(r.ws.AllowedHosts)
	}
	return result
}

// MaxIterations returns the effective max code execution rounds per prompt.
// Cascade: workspace → user default → 20.
func (r SettingsResolver) MaxIterations() int64 {
	if r.ws != nil && r.ws.MaxIterations > 0 {
		return r.ws.MaxIterations
	}
	if r.cfg != nil && r.cfg.MaxIterations > 0 {
		return r.cfg.MaxIterations
	}
	return 20
}

// Timeout returns the effective execution timeout for the given context.
// Delegates to the existing Workspace.TimeoutFor with AppConfig fallback.
func (r SettingsResolver) Timeout(context string) time.Duration {
	if r.ws != nil {
		return r.ws.TimeoutFor(context, r.cfg)
	}
	return 120 * time.Second
}
