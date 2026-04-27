// Package config handles Altclaw configuration backed by dsorm (local SQLite).
// All API keys are encrypted at rest using an auto-generated key.
package config

import (
	"context"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"altclaw.ai/internal/netx"
	"altclaw.ai/internal/util"
	"cloud.google.com/go/datastore"
	"github.com/altlimit/dsorm"
)

// ── Models ──────────────────────────────────────────────────────────

// Chat represents a persistent chat session.
// Namespaced by workspace via model:"ns".
type Chat struct {
	dsorm.Base
	ID        int64     `model:"id" json:"id"`
	Workspace string    `model:"ns" json:"-"`
	Title     string    `datastore:"title,noindex,omitempty" json:"title,omitempty"`
	Provider  string    `datastore:"provider,noindex,omitempty" json:"provider,omitempty"`
	CreatedAt time.Time `model:"created" datastore:"created,omitempty" json:"created"`
	UpdatedAt time.Time `model:"modified" datastore:"modified,omitempty" json:"modified"`
}

func (c *Chat) AfterSave(ctx context.Context, old dsorm.Model) error {
	if old == nil || old.IsNew() {
		broadcast(ctx, []byte(fmt.Sprintf(`{"type":"chat_list_updated","action":"created","chat_id":%d}`, c.ID)))
	}
	return nil
}

func (c *Chat) AfterDelete(ctx context.Context) error {
	broadcast(ctx, []byte(fmt.Sprintf(`{"type":"chat_list_updated","action":"deleted","chat_id":%d}`, c.ID)))
	return nil
}

// ChatMessage is a single message in a chat, child of Chat via model:"parent".
type ChatMessage struct {
	dsorm.Base
	ID               string         `model:"id" json:"id"`
	Chat             *datastore.Key `model:"parent" datastore:"-" json:"-"`
	Role             string         `datastore:"role,noindex,omitempty" json:"role"`
	Content          string         `datastore:"content,noindex,omitempty" json:"content,omitempty"`
	PromptTokens     int64          `datastore:"prompt_tokens,noindex,omitempty" json:"prompt_tokens,omitempty"`
	CompletionTokens int64          `datastore:"completion_tokens,noindex,omitempty" json:"completion_tokens,omitempty"`
	CreatedAt        time.Time      `model:"created" datastore:"created,omitempty" json:"created"`
}

// History records a single code block execution for debugging.
// Namespaced by workspace via model:"ns".
type History struct {
	dsorm.Base
	ID               int64          `model:"id" json:"id"`
	Chat             *datastore.Key `model:"parent" json:"chat_id,omitempty"`
	ChatMessageID    string         `datastore:"message_id,omitempty" json:"message_id,omitempty"` // links to ChatMessage.ID
	Code             string         `datastore:"code,noindex,omitempty" json:"code,omitempty"`
	Result           string         `datastore:"result,noindex,omitempty" json:"result,omitempty"`
	Response         string         `datastore:"response,noindex,omitempty" json:"response,omitempty"`
	AgentType        string         `datastore:"agent_type,omitempty" json:"agent_type,omitempty"`
	Provider         string         `datastore:"provider,noindex,omitempty" json:"provider,omitempty"`
	Iteration        int            `datastore:"iteration,noindex,omitempty" json:"iteration,omitempty"`
	Block            int            `datastore:"block,noindex,omitempty" json:"block,omitempty"`
	PromptTokens     int64          `datastore:"prompt_tokens,noindex,omitempty" json:"prompt_tokens,omitempty"`
	CompletionTokens int64          `datastore:"completion_tokens,noindex,omitempty" json:"completion_tokens,omitempty"`
	CreatedAt        time.Time      `model:"created" datastore:"created,omitempty" json:"created"`
}

// CommonSettings holds settings shared by both AppConfig (user-level defaults)
// and Workspace (workspace-level overrides). When a workspace field is zero-valued,
// the SettingsResolver falls back to the AppConfig value.
type CommonSettings struct {
	RateLimit          int64    `datastore:"rate_limit,noindex,omitempty" json:"rate_limit,omitempty" help:"Max requests per minute (default: 10)"`
	DailyPromptCap     int64    `datastore:"daily_prompt_cap,noindex,omitempty" json:"daily_prompt_cap,omitempty" help:"Max input tokens per day (default: 1M)"`
	DailyCompletionCap int64    `datastore:"daily_completion_cap,noindex,omitempty" json:"daily_completion_cap,omitempty" help:"Max output tokens per day (default: 100k)"`
	ShowThinking       bool     `datastore:"show_thinking,noindex,omitempty" json:"show_thinking,omitempty" help:"Stream AI thinking/reasoning to UI"`
	MessageWindow      int64    `datastore:"message_window,noindex,omitempty" json:"message_window,omitempty" help:"Number of messages to include as context (default: 10)"`
	LogLevel           string   `datastore:"log_level,noindex,omitempty" json:"log_level,omitempty" help:"Log verbosity: debug, info, warn, error"`
	ConfirmModInstall  bool     `datastore:"confirm_mod_install,noindex,omitempty" json:"confirm_mod_install,omitempty" help:"Prompt user before installing modules"`
	IgnoreRestricted   bool     `datastore:"ignore_restricted,noindex,omitempty" json:"ignore_restricted,omitempty" help:"Skip hidden/gitignored file security gate"`
	IPWhitelist        []string `datastore:"ip_whitelist,noindex,omitempty" json:"ip_whitelist,omitempty" help:"Allowed client IPs (empty = allow all)"`
	AllowedHosts       []string `datastore:"allowed_hosts,noindex,omitempty" json:"allowed_hosts,omitempty" help:"Hosts the agent can make HTTP requests to (empty = allow all)"`
	MaxIterations      int64    `datastore:"max_iterations,noindex,omitempty" json:"max_iterations,omitempty" help:"Max code execution rounds per prompt (default: 20)"`
	// Per-context engine execution timeouts (seconds; 0 = use built-in default)
	ServerJSTimeout int64 `datastore:"serverjs_timeout,noindex,omitempty" json:"serverjs_timeout,omitempty" help:"HTTP handler timeout in seconds (default: 60)"` // default 60 s
	CronTimeout     int64 `datastore:"cron_timeout,noindex,omitempty"     json:"cron_timeout,omitempty"     help:"Cron job timeout in seconds (default: 1800)"`     // default 30 min
	AgentTimeout    int64 `datastore:"agent_timeout,noindex,omitempty"    json:"agent_timeout,omitempty"    help:"Agent iteration timeout in seconds (default: 300)"`    // default 5 min
	RunTimeout      int64 `datastore:"run_timeout,noindex,omitempty"      json:"run_timeout,omitempty"      help:"Script run timeout in seconds (default: 600)"`      // default 10 min
}

// AppConfig is the single-row application configuration (ID = "main").
type AppConfig struct {
	dsorm.Base
	ID                  string   `model:"id" json:"id,omitempty"`
	SubAgentProvider    string   `datastore:"sub_agent_provider,noindex,omitempty" json:"sub_agent_provider,omitempty" help:"Default provider name for sub-agents"`
	Executor            string   `datastore:"executor,noindex,omitempty" json:"executor,omitempty" help:"Command executor: local or docker"`
	DockerImage         string   `datastore:"docker_image,noindex,omitempty" json:"docker_image,omitempty" help:"Docker image for sandboxed execution"`
	LocalWhitelist      []string `datastore:"local_whitelist,noindex,omitempty" json:"local_whitelist,omitempty" help:"Commands allowed in local executor mode"`
	ProviderConcurrency int      `datastore:"provider_concurrency,noindex,omitempty" json:"provider_concurrency,omitempty" help:"Max concurrent provider requests (default: 1)"`
	CommonSettings               // user-level defaults
	VAPIDPublicKey      string   `datastore:"vapid_public_key,noindex,omitempty" json:"vapid_public_key,omitempty"`
	VAPIDPrivateKey     string   `model:"vapid_private,encrypt" json:"-"`
	// ed25519 key pair for module marketplace ownership proof
	ModulePublicKey  string `datastore:"module_public_key,noindex,omitempty" json:"module_public_key,omitempty"`
	ModulePrivateKey string `model:"module_private_key,encrypt" json:"-"`
	// P-256 ECDH key pair for receiving encrypted profile secrets from hub.
	// The public key is sent during /api/discover; the hub encrypts secrets
	// so only this instance can decrypt them.
	SecretPublicKey  string `datastore:"secret_public_key,noindex,omitempty" json:"secret_public_key,omitempty"`
	SecretPrivateKey string `model:"secret_private_key,encrypt" json:"-"`

	Locked bool `datastore:"-" json:"-"`
}

func (c *AppConfig) AfterSave(ctx context.Context, old dsorm.Model) error {
	broadcast(ctx, []byte(`{"type":"config_updated"}`))
	return nil
}

// Provider is a named AI provider with an encrypted API key.
type Provider struct {
	dsorm.Base
	ID                 int64     `model:"id" json:"id,omitempty"`
	Name               string    `datastore:"name,omitempty" json:"name,omitempty"`
	ProviderType       string    `datastore:"provider,omitempty" json:"provider,omitempty"`
	Model              string    `datastore:"model,noindex,omitempty" json:"model,omitempty"`
	APIKey             string    `model:"api_key,encrypt" json:"api_key,omitempty"`
	BaseURL            string    `datastore:"base_url,noindex,omitempty" json:"base_url,omitempty"`
	Host               string    `datastore:"host,noindex,omitempty" json:"host,omitempty"`
	Docs               []string  `datastore:"docs,noindex,omitempty" json:"docs,omitempty"`
	Description        string    `datastore:"description,noindex,omitempty" json:"description,omitempty"`
	DockerImage        string    `datastore:"docker_image,noindex,omitempty" json:"docker_image,omitempty"`
	RateLimit          int64     `datastore:"rate_limit,noindex,omitempty" json:"rate_limit,omitempty"`                     // req/min override (0 = use workspace default)
	DailyPromptCap     int64     `datastore:"daily_prompt_cap,noindex,omitempty" json:"daily_prompt_cap,omitempty"`         // max input tokens/day for this provider (0 = use workspace default)
	DailyCompletionCap int64     `datastore:"daily_completion_cap,noindex,omitempty" json:"daily_completion_cap,omitempty"` // max output tokens/day for this provider (0 = use workspace default)
	CreatedAt          time.Time `model:"created" datastore:"created,omitempty" json:"created,omitempty"`
	UpdatedAt          time.Time `model:"modified" datastore:"modified,omitempty" json:"updated_at,omitempty"`
	// InMemory marks this provider as profile-provisioned (from hub profile).
	// It is never persisted — BeforeSave will reject it.
	InMemory bool `datastore:"-" json:"in_memory,omitempty"`
}

func (p *Provider) BeforeSave(ctx context.Context, old dsorm.Model) error {
	if p.InMemory {
		return fmt.Errorf("profile-provisioned provider cannot be saved locally")
	}
	return nil
}

func (p *Provider) AfterSave(ctx context.Context, old dsorm.Model) error {
	broadcast(ctx, []byte(fmt.Sprintf(`{"type":"provider_updated","action":"saved","id":%d}`, p.ID)))
	return nil
}

func (p *Provider) AfterDelete(ctx context.Context) error {
	broadcast(ctx, []byte(fmt.Sprintf(`{"type":"provider_updated","action":"deleted","id":%d}`, p.ID)))
	return nil
}

// Workspace tracks a known workspace directory.
type Workspace struct {
	dsorm.Base
	ID             string    `model:"id" json:"id,omitempty"`
	Name           string    `datastore:"name,omitempty" json:"name,omitempty"`
	Path           string    `datastore:"path,omitempty" json:"path,omitempty"`
	LogPath        string    `datastore:"log_path,noindex,omitempty" json:"log_path,omitempty" help:"Path to log file"`
	LogMaxSize     int       `datastore:"log_max_size,noindex,omitempty" json:"log_max_size,omitempty" help:"Max log file size in MB before rotation"`
	LogMaxFiles    int       `datastore:"log_max_files,noindex,omitempty" json:"log_max_files,omitempty" help:"Max rotated log files to keep"`
	TunnelToken    string    `model:"tunnel_token,encrypt" json:"tunnel_token,omitempty"`
	TunnelHub      string    `datastore:"tunnel_hub,noindex,omitempty" json:"tunnel_hub,omitempty"`
	TunnelHost     string    `datastore:"tunnel_host,noindex,omitempty" json:"tunnel_host,omitempty"`
	TunnelAddr     string    `datastore:"tunnel_addr,noindex,omitempty" json:"tunnel_addr,omitempty"`
	TunnelMode     string    `datastore:"tunnel_mode,noindex,omitempty" json:"tunnel_mode,omitempty"` // "anonymous", "paired", or ""
	PublicDir      string    `datastore:"public_dir,noindex,omitempty" json:"public_dir,omitempty" help:"Subdirectory to serve as static files"`
	LastProvider   string    `datastore:"last_provider,noindex,omitempty" json:"last_provider,omitempty"`
	Port           int       `datastore:"port,noindex,omitempty" json:"port,omitempty"` // running web server port
	CommonSettings           // workspace-level overrides
	LastActive     time.Time `datastore:"last_active,omitempty" json:"last_active,omitempty"`
	CreatedAt      time.Time `model:"created" datastore:"created,omitempty" json:"created,omitempty"`

	Locked bool `datastore:"-" json:"-"`
}

// TimeoutFor returns the configured execution timeout for the given engine context.
// Resolution order: workspace field → AppConfig field → built-in default.
//
//	"serverjs" → 60 s   (HTTP request handler)
//	"cron"     → 30 min (cron script runner)
//	"agent"    → 5 min  (code-block execution per iteration)
//	"run"      → 10 min (RunScript SSE handler)
func (ws *Workspace) TimeoutFor(ctx string, cfg ...*AppConfig) time.Duration {
	fallback := func(wsField int64, cfgField int64, def time.Duration) time.Duration {
		if wsField > 0 {
			return time.Duration(wsField) * time.Second
		}
		if cfgField > 0 {
			return time.Duration(cfgField) * time.Second
		}
		return def
	}
	var c *AppConfig
	if len(cfg) > 0 {
		c = cfg[0]
	}
	cfgVal := func(f func(*AppConfig) int64) int64 {
		if c != nil {
			return f(c)
		}
		return 0
	}
	switch ctx {
	case "serverjs":
		return fallback(ws.ServerJSTimeout, cfgVal(func(a *AppConfig) int64 { return a.ServerJSTimeout }), 60*time.Second)
	case "cron":
		return fallback(ws.CronTimeout, cfgVal(func(a *AppConfig) int64 { return a.CronTimeout }), 30*time.Minute)
	case "agent":
		return fallback(ws.AgentTimeout, cfgVal(func(a *AppConfig) int64 { return a.AgentTimeout }), 5*time.Minute)
	case "run":
		return fallback(ws.RunTimeout, cfgVal(func(a *AppConfig) int64 { return a.RunTimeout }), 10*time.Minute)
	}
	return 120 * time.Second
}

func (ws *Workspace) BeforeSave(ctx context.Context, old dsorm.Model) error {
	if ws.Path == "" {
		return fmt.Errorf("workspace path must not be empty")
	}
	ws.PublicDir = strings.Trim(ws.PublicDir, "/")
	if o, ok := old.(*Workspace); ok && !slices.Equal(o.AllowedHosts, ws.AllowedHosts) {
		netx.SetAllowedHosts(ws.AllowedHosts)
	}
	return nil
}

func (ws *Workspace) AfterSave(ctx context.Context, old dsorm.Model) error {
	broadcast(ctx, []byte(`{"type":"workspace_updated"}`))
	return nil
}

func (ws *Workspace) Patch(ctx context.Context, store *Store, params map[string]any) error {
	return store.SaveWorkspace(ctx, func(w *Workspace) error {
		return util.Patch(params, w)
	})
}

// AfterDelete removes the workspace namespace DB file when using local store.
func (ws *Workspace) AfterDelete(ctx context.Context) error {
	dbDir, _ := ctx.Value(ctxKeyDBDir).(string)
	if dbDir == "" || ws.ID == "" {
		return nil
	}
	dbFile := filepath.Join(dbDir, ws.ID+".db")
	if _, err := os.Stat(dbFile); err == nil {
		_ = os.Remove(dbFile)
	}
	return nil
}

// CronJob is a scheduled task, namespaced by workspace.
type CronJob struct {
	dsorm.Base
	ID           int64     `model:"id" json:"id"`
	Workspace    string    `model:"ns" json:"ns"`
	ChatID       int64     `datastore:"chat_id,omitempty" json:"chat_id,omitempty"`
	Schedule     string    `datastore:"schedule,noindex,omitempty" json:"schedule,omitempty"`
	Instructions string    `datastore:"instructions,noindex,omitempty" json:"instructions,omitempty"`
	OneShot      bool      `datastore:"one_shot,noindex,omitempty" json:"one_shot,omitempty"`
	Script       bool      `datastore:"script,noindex,omitempty" json:"script,omitempty"`
	FireAt       string    `datastore:"fire_at,omitempty" json:"fire_at,omitempty"`
	CreatedAt    time.Time `model:"created" datastore:"created,omitempty" json:"created"`
}

func (j *CronJob) AfterSave(ctx context.Context, old dsorm.Model) error {
	action := "updated"
	if old == nil || old.IsNew() {
		action = "added"
	}
	broadcast(ctx, []byte(fmt.Sprintf(`{"type":"cron_updated","action":"%s","id":"%d"}`, action, j.ID)))
	return nil
}

func (j *CronJob) AfterDelete(ctx context.Context) error {
	broadcast(ctx, []byte(fmt.Sprintf(`{"type":"cron_updated","action":"deleted","id":"%d"}`, j.ID)))
	return nil
}

// ConnEntry is a persistent connection (WebSocket, future: TCP, MQTT), namespaced by workspace.
type ConnEntry struct {
	dsorm.Base
	ID        int64     `model:"id" json:"id"`
	Workspace string    `model:"ns" json:"ns"`
	ChatID    int64     `datastore:"chat_id,omitempty" json:"chat_id,omitempty"`
	Type      string    `datastore:"type,noindex,omitempty" json:"type,omitempty"`       // "ws", "tcp", etc.
	URL       string    `datastore:"url,noindex,omitempty" json:"url,omitempty"`
	Handler   string    `datastore:"handler,noindex,omitempty" json:"handler,omitempty"` // script filepath (require()-resolvable)
	Headers   string    `datastore:"headers,noindex,omitempty" json:"headers,omitempty"` // JSON object
	Reconnect bool      `datastore:"reconnect,noindex,omitempty" json:"reconnect,omitempty"`
	CreatedAt time.Time `model:"created" datastore:"created,omitempty" json:"created"`
}

func (c *ConnEntry) AfterSave(ctx context.Context, old dsorm.Model) error {
	action := "updated"
	if old == nil || old.IsNew() {
		action = "added"
	}
	broadcast(ctx, []byte(fmt.Sprintf(`{"type":"conn_updated","action":"%s","id":"%d"}`, action, c.ID)))
	return nil
}

func (c *ConnEntry) AfterDelete(ctx context.Context) error {
	broadcast(ctx, []byte(fmt.Sprintf(`{"type":"conn_updated","action":"deleted","id":"%d"}`, c.ID)))
	return nil
}

// Memory is a single structured memory record.
// Kind: "core" (permanent), "learned" (auto-expire ~30d), "note" (auto-expire ~7d).
type Memory struct {
	dsorm.Base
	ID        int64     `model:"id" json:"id"`
	Workspace string    `model:"ns" json:"ns"`
	Kind      string    `datastore:"kind,omitempty" json:"kind"`
	Content   string    `datastore:"content,noindex,omitempty" json:"content"`
	CreatedAt time.Time `model:"created" datastore:"created,omitempty" json:"created"`
	UpdatedAt time.Time `model:"modified" datastore:"modified,omitempty" json:"modified"`
}

func (m *Memory) AfterSave(ctx context.Context, old dsorm.Model) error {
	action := "updated"
	if old == nil || old.IsNew() {
		action = "added"
	}
	broadcast(ctx, []byte(fmt.Sprintf(`{"type":"memory_updated","action":"%s","id":%d}`, action, m.ID)))
	return nil
}

func (m *Memory) AfterDelete(ctx context.Context) error {
	broadcast(ctx, []byte(fmt.Sprintf(`{"type":"memory_updated","action":"deleted","id":%d}`, m.ID)))
	return nil
}

// TokenUsage records daily token consumption for a workspace.
// ID is "YYYY-MM-DD" for workspace totals, or "YYYY-MM-DD:providerID" for per-provider rows.
type TokenUsage struct {
	dsorm.Base
	ID               string    `model:"id" json:"id"`
	Workspace        string    `model:"ns" json:"-"`
	ProviderID       int64     `datastore:"provider_id,omitempty" json:"provider_id,omitempty"`
	PromptTokens     int64     `datastore:"prompt_tokens,noindex,omitempty" json:"prompt_tokens"`
	CompletionTokens int64     `datastore:"completion_tokens,noindex,omitempty" json:"completion_tokens"`
	TotalTokens      int64     `datastore:"total_tokens,noindex,omitempty" json:"total_tokens"`
	RequestCount     int64     `datastore:"request_count,noindex,omitempty" json:"request_count"`
	UpdatedAt        time.Time `model:"modified" datastore:"modified,omitempty" json:"date"`
}

// PasskeyEntry is a stored WebAuthn credential with encrypted data.
type PasskeyEntry struct {
	dsorm.Base
	ID             int64     `model:"id" json:"id"`
	Name           string    `datastore:"name,noindex,omitempty" json:"name,omitempty"`
	RPID           string    `datastore:"rpid,noindex,omitempty" json:"rpid,omitempty"`
	CredentialData string    `model:"credential_data,encrypt" json:"credential_data,omitempty"`
	CreatedAt      time.Time `model:"created" datastore:"created,omitempty" json:"created"`
}

// PushSubscription stores a Web Push subscription for browser notifications.
// Namespaced by workspace via model:"ns".
type PushSubscription struct {
	dsorm.Base
	ID        int64     `model:"id" json:"id"`
	Workspace string    `model:"ns" json:"-"`
	Endpoint  string    `datastore:"endpoint,omitempty" json:"endpoint"`
	P256dh    string    `datastore:"p256dh,noindex,omitempty" json:"p256dh"`
	Auth      string    `datastore:"auth,noindex,omitempty" json:"auth"`
	CreatedAt time.Time `model:"created" datastore:"created,omitempty" json:"created"`
}

// ── Broadcast ───────────────────────────────────────────────────────

// contextKey is an unexported type used for context value keys in this package.
type contextKey string

const ctxKeyDBDir contextKey = "dbDir"

// ctxKeyBroadcast is the context key for injecting an SSE broadcast function.
const ctxKeyBroadcast contextKey = "broadcast"

// BroadcastFunc is a function that sends raw JSON event data to SSE subscribers.
type BroadcastFunc func(eventJSON []byte)

// WithBroadcast returns a context with the given broadcast function attached.
// Used by the web server to inject hub.BroadcastRaw into store operations.
func WithBroadcast(ctx context.Context, fn BroadcastFunc) context.Context {
	return context.WithValue(ctx, ctxKeyBroadcast, fn)
}

// broadcast extracts the broadcast function from context and sends an event.
// No-op if no broadcast function is set (e.g. TUI mode, CLI scripts).
func broadcast(ctx context.Context, eventJSON []byte) {
	if fn, ok := ctx.Value(ctxKeyBroadcast).(BroadcastFunc); ok && fn != nil {
		fn(eventJSON)
	}
}

// ── Helpers ─────────────────────────────────────────────────────────

// WorkspaceID returns a deterministic, short ID for a workspace path.
// Uses FNV-32a hash of the cleaned path encoded as 8 hex characters.
func WorkspaceID(cleanPath string) string {
	h := fnv.New32a()
	h.Write([]byte(cleanPath))
	return fmt.Sprintf("%08x", h.Sum32())
}

// FormatAge returns a human-readable age string.
func FormatAge(d time.Duration) string {
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
