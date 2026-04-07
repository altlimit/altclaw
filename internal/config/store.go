package config

import (
	"bufio"
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"strings"
	"sync"
	"time"

	"cloud.google.com/go/datastore"
	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/altlimit/dsorm"
	"github.com/altlimit/dsorm/ds/local"
	"github.com/zalando/go-keyring"
)

// ── Store ───────────────────────────────────────────────────────────

// Store wraps a dsorm client for all Altclaw persistence.
// tokenCountKey is the in-memory cache key for daily token counts.
type tokenCountKey struct {
	ws     string
	provID int64
}

type Store struct {
	Client      *dsorm.Client
	dir         string
	tokenMu     sync.Mutex
	tokenToday  string                        // current date "YYYY-MM-DD" (UTC)
	tokenCounts map[tokenCountKey]*TokenUsage // in-memory daily totals
	// mu guards ws + cfg — the in-memory source of truth.
	mu  sync.Mutex
	ws  *Workspace // active workspace (set by GetWorkspace, mutated by SaveWorkspace)
	cfg *AppConfig // app config  (set by GetConfig, mutated by SaveConfig)
	// profileData holds profile-provisioned providers and secrets from hub (InMemory=true).
	// They are prepended to DB records and can never be saved to SQLite.
	profileData *ProfileData
}

// tokenUsageMu protects concurrent DB upserts to the same day's row.
var tokenUsageMu sync.Map // key: wsNS+":"date → *sync.Mutex

func tokenUsageKey(wsNS, date string) string { return wsNS + ":" + date }

// NewStore creates a new dsorm-backed store in the given config directory.
// Loads .env from configDir and reads ALTCLAW_ENC_KEY env var (hex-encoded).
// Auto-generates the key and writes it to .env if not set.
func NewStore(configDir string) (*Store, error) {
	if err := os.MkdirAll(configDir, 0700); err != nil {
		return nil, fmt.Errorf("config: mkdir: %w", err)
	}

	// Load .env from config dir (sets env vars if not already set)
	loadEnvFile(filepath.Join(configDir, ".env"))

	// Get or generate encryption key from ALTCLAW_ENC_KEY
	key, err := getOrCreateEncKey(configDir)
	if err != nil {
		return nil, fmt.Errorf("config: encryption key: %w", err)
	}

	dbDir := filepath.Join(configDir, "db")
	if err := os.MkdirAll(dbDir, 0700); err != nil {
		return nil, fmt.Errorf("config: mkdir db: %w", err)
	}
	store := local.NewStore(dbDir)

	ctx := context.Background()
	client, err := dsorm.New(ctx,
		dsorm.WithStore(store),
		dsorm.WithEncryptionKey(key),
	)
	if err != nil {
		return nil, fmt.Errorf("config: dsorm init: %w", err)
	}

	return &Store{Client: client, dir: configDir}, nil
}

// Close shuts down the store.
func (s *Store) Close() error {
	return s.Client.Close()
}

// ── AppConfig CRUD ──────────────────────────────────────────────────

// GetConfig loads the app config from the DB (creates defaults if missing),
// caches it in memory, and returns it. Subsequent calls return the cached copy.
func (s *Store) GetConfig() (*AppConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cfg != nil {
		return s.cfg, nil
	}

	ctx := context.Background()
	cfg := &AppConfig{ID: "main"}
	if err := s.Client.Get(ctx, cfg); err != nil {
		if err == datastore.ErrNoSuchEntity {
			// Not found — return defaults
			cfg.MaxIterations = 20
			cfg.Executor = "auto"
			cfg.DockerImage = "alpine:latest"
		} else {
			return nil, fmt.Errorf("config: get: %w", err)
		}
	}
	s.cfg = cfg

	// Auto-generate keys (mutate in-memory, persist once)
	dirty := false
	if cfg.VAPIDPublicKey == "" || cfg.VAPIDPrivateKey == "" {
		if priv, pub, err := webpush.GenerateVAPIDKeys(); err == nil {
			cfg.VAPIDPublicKey = pub
			cfg.VAPIDPrivateKey = priv
			dirty = true
		} else {
			slog.Warn("failed to generate VAPID keys", "error", err)
		}
	}
	if cfg.ModulePublicKey == "" || cfg.ModulePrivateKey == "" {
		if pubKey, privKey, err := ed25519.GenerateKey(nil); err == nil {
			cfg.ModulePublicKey = hex.EncodeToString(pubKey)
			cfg.ModulePrivateKey = hex.EncodeToString(privKey)
			dirty = true
		} else {
			slog.Warn("failed to generate module keys", "error", err)
		}
	}
	if cfg.SecretPublicKey == "" || cfg.SecretPrivateKey == "" {
		if privKey, err := ecdh.P256().GenerateKey(rand.Reader); err == nil {
			cfg.SecretPublicKey = hex.EncodeToString(privKey.PublicKey().Bytes())
			cfg.SecretPrivateKey = hex.EncodeToString(privKey.Bytes())
			dirty = true
		} else {
			slog.Warn("failed to generate secret ECDH keys", "error", err)
		}
	}
	if dirty {
		if err := s.Client.Put(ctx, cfg); err != nil {
			slog.Warn("failed to persist auto-generated config keys", "error", err)
		}
	}

	return cfg, nil
}

// Config returns the cached AppConfig. Panics if GetConfig was never called.
func (s *Store) Config() *AppConfig {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg
}

// SaveConfig mutates the in-memory AppConfig under a lock, then persists to DB.
// The callback receives the live config pointer — no DB read is needed.
func (s *Store) SaveConfig(ctx context.Context, fn func(*AppConfig) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cfg == nil {
		return errors.New("config not loaded")
	}
	if err := fn(s.cfg); err != nil {
		return err
	}
	return s.Client.Put(ctx, s.cfg)
}

// ── Provider CRUD ───────────────────────────────────────────────────

// ProfileData holds in-memory profile-provisioned providers and secrets.
type ProfileData struct {
	Providers []*Provider
	Secrets   []*Secret
}

// SetProfile replaces the in-memory profile data (providers + secrets).
// Called when a hub profile is applied on tunnel connect. Pass nil to clear.
func (s *Store) SetProfile(data *ProfileData) {
	s.profileData = data
	if data == nil {
		// reload config and workspace
		wsPath := s.Workspace().Path
		s.mu.Lock()
		s.cfg = nil
		s.ws = nil
		s.mu.Unlock()

		s.GetConfig()
		s.GetWorkspace(wsPath)
	}
}

// GetProfile returns profile-provisioned data (in-memory only).
func (s *Store) GetProfile() *ProfileData {
	return s.profileData
}

// ListProviders returns all configured providers, with profile providers first.
func (s *Store) ListProviders() ([]*Provider, error) {
	ctx := context.Background()
	q := dsorm.NewQuery("Provider")
	providers, _, err := dsorm.Query[*Provider](ctx, s.Client, q, "")
	if err != nil {
		return nil, err
	}
	if s.profileData != nil && len(s.profileData.Providers) > 0 {
		return append(s.profileData.Providers, providers...), nil
	}
	return providers, nil
}

// GetProvider returns a provider by name. Checks in-memory profile providers first.
func (s *Store) GetProvider(name string) (*Provider, error) {
	if s.profileData != nil {
		for _, p := range s.profileData.Providers {
			if p.Name == name {
				return p, nil
			}
		}
	}
	ctx := context.Background()
	q := dsorm.NewQuery("Provider").FilterField("name", "=", name).Limit(1)
	providers, _, err := dsorm.Query[*Provider](ctx, s.Client, q, "")
	if err != nil {
		return nil, err
	}
	if len(providers) == 0 {
		return nil, fmt.Errorf("provider %q not found", name)
	}
	return providers[0], nil
}

// SaveProvider creates or updates a provider.
func (s *Store) SaveProvider(ctx context.Context, p *Provider) error {
	return s.Client.Put(ctx, p)
}

// DeleteProvider removes a provider.
func (s *Store) DeleteProvider(ctx context.Context, p *Provider) error {
	return s.Client.Delete(ctx, p)
}

// GetFirstProvider returns the first available provider (by creation order).
func (s *Store) GetFirstProvider() (*Provider, error) {
	providers, err := s.ListProviders()
	if err != nil {
		return nil, err
	}
	if len(providers) == 0 {
		return nil, fmt.Errorf("no providers configured")
	}
	return providers[0], nil
}

// IsConfigured returns true if at least one provider exists.
func (s *Store) IsConfigured() bool {
	providers, err := s.ListProviders()
	return err == nil && len(providers) > 0
}

// ProviderSummary returns a system-prompt-friendly description of all providers.
func (s *Store) ProviderSummary() string {
	providers, err := s.ListProviders()
	if err != nil {
		return ""
	}
	var lines []string
	for _, p := range providers {
		if len(p.Docs) == 0 && p.Description == "" {
			continue
		}
		line := fmt.Sprintf("- %q (%s/%s)", p.Name, p.ProviderType, p.Model)
		if p.Description != "" {
			line += " — " + p.Description
		}
		if len(p.Docs) > 0 {
			line += " [docs: " + strings.Join(p.Docs, ", ") + "]"
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n")
}

// ── Workspace CRUD ──────────────────────────────────────────────────

// GetWorkspace finds or creates a workspace by path, caching it in memory.
// Subsequent calls with the same path return the cached pointer.
// This replaces the old GetOrCreateWorkspace.
func (s *Store) GetWorkspace(absPath string) (*Workspace, error) {
	if absPath == "" {
		return nil, fmt.Errorf("workspace path must not be empty")
	}
	absPath = filepath.Clean(absPath)

	s.mu.Lock()
	defer s.mu.Unlock()

	// If already loaded and path matches, return cached
	if s.ws != nil && s.ws.Path == absPath {
		return s.ws, nil
	}

	id := WorkspaceID(absPath)

	// Try direct Get by ID (fast path)
	ws := &Workspace{ID: id}
	if err := s.Client.Get(context.Background(), ws); err == nil {
		ws.LastActive = time.Now()
		_ = s.Client.Put(context.Background(), ws)
		s.ws = ws
		return ws, nil
	} else if err != datastore.ErrNoSuchEntity {
		return nil, fmt.Errorf("workspace: get: %w", err)
	}

	// Create new workspace
	ws = &Workspace{
		ID:         id,
		Path:       absPath,
		Name:       filepath.Base(absPath),
		LastActive: time.Now(),
	}
	if err := s.Client.Put(context.Background(), ws); err != nil {
		return nil, err
	}
	s.ws = ws
	return ws, nil
}

// Workspace returns the cached workspace pointer. Returns nil if not yet loaded.
func (s *Store) Workspace() *Workspace {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ws
}

func (s *Store) GetWorkspaceByID(id string) (*Workspace, error) {
	ctx := context.Background()
	ws := &Workspace{ID: id}
	if err := s.Client.Get(ctx, ws); err != nil {
		return nil, err
	}
	return ws, nil
}

// ListWorkspaces returns all known workspaces.
func (s *Store) ListWorkspaces(ctx context.Context) ([]*Workspace, error) {
	q := dsorm.NewQuery("Workspace").Order("-last_active")
	ws, _, err := dsorm.Query[*Workspace](ctx, s.Client, q, "")
	return ws, err
}

// SetWorkspace replaces the cached workspace pointer (e.g. after profile application).
func (s *Store) SetWorkspace(ws *Workspace) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ws = ws
}

// SaveWorkspace mutates the in-memory workspace under a lock, then persists to DB.
// No DB read is needed — memory is the source of truth.
func (s *Store) SaveWorkspace(ctx context.Context, fn func(*Workspace) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ws == nil {
		return fmt.Errorf("workspace not loaded")
	}
	if err := fn(s.ws); err != nil {
		return err
	}
	return s.Client.Put(ctx, s.ws)
}

// DeleteWorkspace removes a workspace by ID and its namespace DB file.
func (s *Store) DeleteWorkspace(ctx context.Context, id string) error {
	ws := &Workspace{ID: id}
	// Inject db directory into context so AfterDelete hook can clean up the DB file
	ctx = context.WithValue(ctx, ctxKeyDBDir, filepath.Join(s.dir, "db"))
	return s.Client.Delete(ctx, ws)
}

// ── Cron CRUD ───────────────────────────────────────────────────────

// ListCronJobs returns all cron jobs for a workspace.
func (s *Store) ListCronJobs(ctx context.Context, workspaceID string) ([]*CronJob, error) {
	q := dsorm.NewQuery("CronJob").Namespace(workspaceID)
	jobs, _, err := dsorm.Query[*CronJob](ctx, s.Client, q, "")
	return jobs, err
}

// SaveCronJob persists a cron job.
func (s *Store) SaveCronJob(ctx context.Context, job *CronJob) error {
	return s.Client.Put(ctx, job)
}

// DeleteCronJob removes a cron job.
func (s *Store) DeleteCronJob(ctx context.Context, job *CronJob) error {
	return s.Client.Delete(ctx, job)
}

// ClearCronJobChatID clears the ChatID on cron jobs when their chat is deleted.
// Jobs become workspace-level (ChatID=0) rather than being deleted.
func (s *Store) ClearCronJobChatID(ctx context.Context, workspaceID string, chatID int64) {
	jobs, err := s.ListCronJobs(ctx, workspaceID)
	if err != nil {
		return
	}
	for _, j := range jobs {
		if j.ChatID == chatID {
			j.ChatID = 0
			_ = s.SaveCronJob(ctx, j)
		}
	}
}

// ── Passkey CRUD ────────────────────────────────────────────────────

// ListPasskeys returns all stored passkeys.
func (s *Store) ListPasskeys(ctx context.Context) ([]*PasskeyEntry, error) {
	q := dsorm.NewQuery("PasskeyEntry")
	entries, _, err := dsorm.Query[*PasskeyEntry](ctx, s.Client, q, "")
	return entries, err
}

// AddPasskey stores a new passkey.
func (s *Store) AddPasskey(ctx context.Context, entry *PasskeyEntry) error {
	return s.Client.Put(ctx, entry)
}

// DeletePasskey removes a passkey by ID.
func (s *Store) DeletePasskey(ctx context.Context, id int64) error {
	entry := &PasskeyEntry{ID: id}
	return s.Client.Delete(ctx, entry)
}

// ── Push Subscription CRUD ─────────────────────────────────────────

// SavePushSubscription creates or updates a push subscription.
func (s *Store) SavePushSubscription(ctx context.Context, sub *PushSubscription) error {
	return s.Client.Put(ctx, sub)
}

// ListPushSubscriptions returns all push subscriptions for a workspace.
func (s *Store) ListPushSubscriptions(ctx context.Context, workspaceID string) ([]*PushSubscription, error) {
	q := dsorm.NewQuery("PushSubscription").Namespace(workspaceID)
	subs, _, err := dsorm.Query[*PushSubscription](ctx, s.Client, q, "")
	return subs, err
}

// DeletePushSubscriptionByEndpoint removes a subscription by its endpoint URL.
func (s *Store) DeletePushSubscriptionByEndpoint(ctx context.Context, workspaceID string, endpoint string) error {
	q := dsorm.NewQuery("PushSubscription").Namespace(workspaceID).
		FilterField("endpoint", "=", endpoint)
	subs, _, err := dsorm.Query[*PushSubscription](ctx, s.Client, q, "")
	if err != nil {
		return err
	}
	for _, sub := range subs {
		_ = s.Client.Delete(ctx, sub)
	}
	return nil
}

// ── Module Filesystem Helpers ────────────────────────────────────────

// Dir returns the config directory this store is rooted at.
func (s *Store) Dir() string { return s.dir }

// ModuleDirs returns (workspaceScopedDir, userDir) for module file lookups.
// workspaceScopedDir = configDir/{wsID}/modules
// userDir           = configDir/modules
func (s *Store) ModuleDirs(wsID string) (wsDir, userDir string) {
	return filepath.Join(s.dir, wsID, "modules"),
		filepath.Join(s.dir, "modules")
}

// ── Memory CRUD ────────────────────────────────────────────────

// AddMemory creates a new memory entry.
func (s *Store) AddMemory(ctx context.Context, entry *Memory) error {
	return s.Client.Put(ctx, entry)
}

// GetMemory returns a single memory entry by ID.
func (s *Store) GetMemory(ctx context.Context, workspace string, id int64) (*Memory, error) {
	entry := &Memory{ID: id, Workspace: workspace}
	if err := s.Client.Get(ctx, entry); err != nil {
		return nil, err
	}
	return entry, nil
}

// UpdateMemory saves changes to an existing memory entry.
func (s *Store) UpdateMemory(ctx context.Context, entry *Memory) error {
	return s.Client.Put(ctx, entry)
}

// DeleteMemory removes a memory entry by workspace and ID.
func (s *Store) DeleteMemory(ctx context.Context, workspace string, id int64) error {
	entry := &Memory{ID: id, Workspace: workspace}
	return s.Client.Delete(ctx, entry)
}

// ListMemoryEntries returns all entries for a workspace, newest first.
func (s *Store) ListMemoryEntries(ctx context.Context, workspace string) ([]*Memory, error) {
	q := dsorm.NewQuery("Memory").Namespace(workspace).Order("-created")
	entries, _, err := dsorm.Query[*Memory](ctx, s.Client, q, "")
	return entries, err
}

// ListMemoryEntriesPaged returns paginated memory entries with cursor support.
func (s *Store) ListMemoryEntriesPaged(ctx context.Context, workspace string, limit int, cursor string) ([]*Memory, string, error) {
	q := dsorm.NewQuery("Memory").Namespace(workspace).Order("-created").Limit(limit)
	return dsorm.Query[*Memory](ctx, s.Client, q, cursor)
}

// ListMemoryEntriesByKind returns entries of a specific kind, newest first.
func (s *Store) ListMemoryEntriesByKind(ctx context.Context, workspace, kind string) ([]*Memory, error) {
	q := dsorm.NewQuery("Memory").Namespace(workspace).
		FilterField("kind", "=", kind).Order("-created")
	entries, _, err := dsorm.Query[*Memory](ctx, s.Client, q, "")
	return entries, err
}

// ListRecentMemoryEntries returns entries created since the given time, newest first.
func (s *Store) ListRecentMemoryEntries(ctx context.Context, workspace string, since time.Time) ([]*Memory, error) {
	q := dsorm.NewQuery("Memory").Namespace(workspace).
		FilterField("created", ">=", since).Order("-created")
	entries, _, err := dsorm.Query[*Memory](ctx, s.Client, q, "")
	return entries, err
}

// ExpireMemoryEntries deletes expired learned/note entries.
// Returns the number of entries removed.
func (s *Store) ExpireMemoryEntries(ctx context.Context, workspace string, learnedDays, noteDays int) (int, error) {
	now := time.Now()
	learnedCutoff := now.AddDate(0, 0, -learnedDays)
	noteCutoff := now.AddDate(0, 0, -noteDays)

	entries, err := s.ListMemoryEntries(ctx, workspace)
	if err != nil {
		return 0, err
	}

	removed := 0
	for _, e := range entries {
		expire := false
		switch e.Kind {
		case "learned":
			expire = e.CreatedAt.Before(learnedCutoff)
		case "note":
			expire = e.CreatedAt.Before(noteCutoff)
		}
		if expire {
			if err := s.Client.Delete(ctx, e); err == nil {
				removed++
			}
		}
	}
	return removed, nil
}

// PromoteMemory changes a learned/note entry to core.
func (s *Store) PromoteMemory(ctx context.Context, workspace string, id int64) error {
	entry, err := s.GetMemory(ctx, workspace, id)
	if err != nil {
		return err
	}
	entry.Kind = "core"
	return s.Client.Put(ctx, entry)
}

// BuildMemoryPrompt generates a curated memory section for the system prompt.
// Shows all core entries + recent entries (last 7 days), with a count of older ones.
func (s *Store) BuildMemoryPrompt(ctx context.Context, workspace string) string {
	// Gather entries from both scopes
	var userEntries, wsEntries []*Memory
	userEntries, _ = s.ListMemoryEntries(ctx, "")
	if workspace != "" {
		wsEntries, _ = s.ListMemoryEntries(ctx, workspace)
	}

	if len(userEntries) == 0 && len(wsEntries) == 0 {
		return "\n\n## Memory\nEmpty — you don't know anything yet. Save lessons learned through trial and error so you remember them."
	}

	now := time.Now()
	recentCutoff := now.AddDate(0, 0, -7)
	const maxPromptChars = 2000

	var coreLines, recentLines []string
	olderCount := 0
	promptLen := 0

	formatEntry := func(e *Memory, scope string) {
		// Truncate to first line for preview
		preview := strings.TrimSpace(e.Content)
		if idx := strings.IndexByte(preview, '\n'); idx > 0 {
			preview = preview[:idx] + "…"
		}
		if len(preview) > 120 {
			preview = preview[:117] + "…"
		}
		tag := fmt.Sprintf("[#%d", e.ID)
		if scope != "" {
			tag += ", " + scope
		}
		tag += "]"

		if e.Kind == "core" {
			line := fmt.Sprintf("- %s %s", preview, tag)
			coreLines = append(coreLines, line)
			promptLen += len(line) + 1
		} else if e.CreatedAt.After(recentCutoff) {
			age := FormatAge(now.Sub(e.CreatedAt))
			line := fmt.Sprintf("- %s [#%d, %s, %s]", preview, e.ID, e.Kind, age)
			if promptLen+len(line) < maxPromptChars {
				recentLines = append(recentLines, line)
				promptLen += len(line) + 1
			} else {
				olderCount++
			}
		} else {
			olderCount++
		}
	}

	// Process workspace entries first (higher priority)
	for _, e := range wsEntries {
		formatEntry(e, "")
	}
	for _, e := range userEntries {
		formatEntry(e, "user")
	}

	var sb strings.Builder
	sb.WriteString("\n\n## Memory")
	if len(coreLines) > 0 {
		sb.WriteString("\n\n### Core\n")
		sb.WriteString(strings.Join(coreLines, "\n"))
	}
	if len(recentLines) > 0 {
		sb.WriteString("\n\n### Recent\n")
		sb.WriteString(strings.Join(recentLines, "\n"))
	}
	if olderCount > 0 {
		sb.WriteString(fmt.Sprintf("\n\n(%d older entries not shown — if this task involves APIs, modules, or patterns you may have tried before, run mem.search(\"keyword\") first to recall past lessons before guessing.)", olderCount))
	}

	return sb.String()
}

// ── TokenUsage ──────────────────────────────────────────────────────

// tokenUsageRowID returns the DB row ID for a given date and optional provider.
func tokenUsageRowID(date string, providerID int64) string {
	if providerID > 0 {
		return fmt.Sprintf("%s:%d", date, providerID)
	}
	return date
}

// IncrementTokenUsage atomically adds prompt/completion counts to today's usage row.
// Pass providerID > 0 to also write a per-provider row.
func (s *Store) IncrementTokenUsage(ctx context.Context, wsNS string, prompt, completion int64, providerID ...int64) error {
	today := time.Now().UTC().Format("2006-01-02")
	pid := int64(0)
	if len(providerID) > 0 {
		pid = providerID[0]
	}

	// Upsert workspace-level row
	if err := s.upsertTokenRow(ctx, wsNS, today, 0, prompt, completion); err != nil {
		return err
	}

	// Upsert per-provider row if requested
	if pid > 0 {
		if err := s.upsertTokenRow(ctx, wsNS, today, pid, prompt, completion); err != nil {
			return err
		}
	}
	return nil
}

// upsertTokenRow atomically increments a single token usage row (Get → add → Put)
// and updates the in-memory cache. provID=0 means workspace-level totals.
func (s *Store) upsertTokenRow(ctx context.Context, wsNS, today string, provID, prompt, completion int64) error {
	rowID := tokenUsageRowID(today, provID)
	dbKey := tokenUsageKey(wsNS, rowID)
	mu, _ := tokenUsageMu.LoadOrStore(dbKey, &sync.Mutex{})
	mu.(*sync.Mutex).Lock()

	row := &TokenUsage{ID: rowID, Workspace: wsNS, ProviderID: provID}
	_ = s.Client.Get(ctx, row)
	row.PromptTokens += prompt
	row.CompletionTokens += completion
	row.TotalTokens += prompt + completion
	row.RequestCount++
	err := s.Client.Put(ctx, row)
	mu.(*sync.Mutex).Unlock()

	if err != nil {
		return err
	}

	// Update in-memory cache
	s.tokenMu.Lock()
	if s.tokenToday != today {
		s.tokenCounts = make(map[tokenCountKey]*TokenUsage)
		s.tokenToday = today
	}
	s.tokenCounts[tokenCountKey{ws: wsNS, provID: provID}] = &TokenUsage{
		ID:               row.ID,
		Workspace:        row.Workspace,
		ProviderID:       row.ProviderID,
		PromptTokens:     row.PromptTokens,
		CompletionTokens: row.CompletionTokens,
		TotalTokens:      row.TotalTokens,
		RequestCount:     row.RequestCount,
	}
	s.tokenMu.Unlock()
	return nil
}

// TodayTokenUsage returns today's usage, reading from the in-memory cache when possible.
// Pass providerID > 0 to get per-provider usage.
func (s *Store) TodayTokenUsage(ctx context.Context, wsNS string, providerID ...int64) (*TokenUsage, error) {
	today := time.Now().UTC().Format("2006-01-02")
	pid := int64(0)
	if len(providerID) > 0 {
		pid = providerID[0]
	}

	// Try in-memory cache first
	s.tokenMu.Lock()
	if s.tokenToday == today {
		key := tokenCountKey{ws: wsNS, provID: pid}
		if row, ok := s.tokenCounts[key]; ok {
			copy := *row
			s.tokenMu.Unlock()
			return &copy, nil
		}
	}
	s.tokenMu.Unlock()

	// Cache miss — load from DB
	rowID := tokenUsageRowID(today, pid)
	row := &TokenUsage{ID: rowID, Workspace: wsNS}
	if err := s.Client.Get(ctx, row); err != nil {
		return &TokenUsage{ID: rowID}, nil
	}
	// Warm cache
	s.tokenMu.Lock()
	if s.tokenToday == today {
		key := tokenCountKey{ws: wsNS, provID: pid}
		s.tokenCounts[key] = row
	}
	s.tokenMu.Unlock()
	return row, nil
}

// GetTokenUsage returns up to `days` days of usage rows, newest first.
func (s *Store) GetTokenUsage(ctx context.Context, wsNS string, days int, providerID ...int64) ([]*TokenUsage, error) {
	pid := int64(0)
	if len(providerID) > 0 {
		pid = providerID[0]
	}

	var keys []*TokenUsage
	now := time.Now().UTC()
	for i := 0; i < days; i++ {
		dateStr := now.AddDate(0, 0, -i).Format("2006-01-02")
		keys = append(keys, &TokenUsage{
			ID:        tokenUsageRowID(dateStr, pid),
			Workspace: wsNS,
		})
	}

	rows, err := dsorm.GetMulti[*TokenUsage](ctx, s.Client, keys)
	if err != nil {
		return nil, err
	}

	var ws []*TokenUsage
	for _, r := range rows {
		// GetMulti returns a slice where missing entities are nil
		if r != nil {
			ws = append(ws, r)
		}
	}
	return ws, nil
}

// ── History CRUD ────────────────────────────────────────────────────

// AddHistory stores a code execution history entry.
func (s *Store) AddHistory(ctx context.Context, h *History) error {
	return s.Client.Put(ctx, h)
}

// GetHistory returns a history entry by ID and workspace.
func (s *Store) GetHistory(ctx context.Context, chat *Chat, id int64) (*History, error) {
	h := &History{ID: id, Chat: s.Client.Key(chat)}
	if err := s.Client.Get(ctx, h); err != nil {
		return nil, err
	}
	return h, nil
}

// ListHistory returns all history entries for a workspace, newest first.
func (s *Store) ListHistory(ctx context.Context, workspaceID string) ([]*History, error) {
	q := dsorm.NewQuery("History").Namespace(workspaceID).Order("-created")
	entries, _, err := dsorm.Query[*History](ctx, s.Client, q, "")
	return entries, err
}

// ListHistoryByChat returns history entries for a specific chat.
func (s *Store) ListHistoryByChat(ctx context.Context, chat *Chat) ([]*History, error) {
	q := dsorm.NewQuery("History").Ancestor(s.Client.Key(chat)).Order("-created")
	entries, _, err := dsorm.Query[*History](ctx, s.Client, q, "")
	return entries, err
}

// ClearHistoryByChat removes all history entries for a specific chat.
func (s *Store) ClearHistoryByChat(ctx context.Context, chat *Chat) error {
	entries, err := s.ListHistoryByChat(ctx, chat)
	if err != nil {
		return err
	}
	for _, h := range entries {
		_ = s.Client.Delete(ctx, h)
	}
	return nil
}

// ListHistoryByTurn returns history entries for a specific turn within a chat.
func (s *Store) ListHistoryByTurn(ctx context.Context, chat *Chat, turnID string) ([]*History, error) {
	q := dsorm.NewQuery("History").Ancestor(s.Client.Key(chat)).
		FilterField("message_id", "=", turnID).Order("-created")
	entries, _, err := dsorm.Query[*History](ctx, s.Client, q, "")
	return entries, err
}

// ListSubAgentHistory returns paginated sub-agent history entries for a chat.
func (s *Store) ListSubAgentHistory(ctx context.Context, chat *Chat, limit int, cursor string) ([]*History, string, error) {
	q := dsorm.NewQuery("History").Ancestor(s.Client.Key(chat)).
		FilterField("agent_type", "=", "sub-agent").Order("-created").Limit(limit)
	return dsorm.Query[*History](ctx, s.Client, q, cursor)
}

// ── Chat CRUD ───────────────────────────────────────────────────────

// CreateChat creates a new chat session.
func (s *Store) CreateChat(ctx context.Context, workspaceID string, title, provider string) (*Chat, error) {
	c := &Chat{
		Workspace: workspaceID,
		Title:     title,
		Provider:  provider,
	}
	if err := s.Client.Put(ctx, c); err != nil {
		return nil, err
	}
	return c, nil
}

// GetChat returns a chat by ID.
func (s *Store) GetChat(ctx context.Context, workspaceID string, chatID int64) (*Chat, error) {
	c := &Chat{ID: chatID, Workspace: workspaceID}
	if err := s.Client.Get(ctx, c); err != nil {
		return nil, err
	}
	return c, nil
}

// ListChats returns all chats for a workspace, newest first.
func (s *Store) ListChats(ctx context.Context, workspaceID string) ([]*Chat, error) {
	q := dsorm.NewQuery("Chat").Namespace(workspaceID).Order("-modified")
	chats, _, err := dsorm.Query[*Chat](ctx, s.Client, q, "")
	return chats, err
}

// ListChatsPaged returns paginated chats with cursor support, newest first.
func (s *Store) ListChatsPaged(ctx context.Context, workspaceID string, limit int, cursor string) ([]*Chat, string, error) {
	q := dsorm.NewQuery("Chat").Namespace(workspaceID).Order("-modified").Limit(limit)
	return dsorm.Query[*Chat](ctx, s.Client, q, cursor)
}

// UpdateChat updates a chat record.
func (s *Store) UpdateChat(ctx context.Context, c *Chat) error {
	return s.Client.Put(ctx, c)
}

// DeleteChat removes a chat and all its messages and history.
// Cron jobs scoped to this chat have their ChatID cleared (become workspace-level).
func (s *Store) DeleteChat(ctx context.Context, chat *Chat) error {
	// Clear ChatID on cron jobs scoped to this chat
	if chat.Workspace != "" {
		s.ClearCronJobChatID(ctx, chat.Workspace, chat.ID)
	}
	// Delete child messages
	_ = s.ClearChatMessages(ctx, chat)
	// Delete child history
	_ = s.ClearHistoryByChat(ctx, chat)
	// Delete the chat itself
	return s.Client.Delete(ctx, chat)
}

// ── ChatMessage CRUD ────────────────────────────────────────────────

// AddChatMessage stores a message in a chat.
// promptTokens and completionTokens are optional (assistant messages only).
func (s *Store) AddChatMessage(ctx context.Context, chat *Chat, role, content, id string, tokenCounts ...int64) error {
	if id == "" {
		id = fmt.Sprintf("%x", time.Now().UnixNano())
	}
	msg := &ChatMessage{
		ID:      id,
		Chat:    s.Client.Key(chat),
		Role:    role,
		Content: content,
	}
	if len(tokenCounts) >= 2 {
		msg.PromptTokens = tokenCounts[0]
		msg.CompletionTokens = tokenCounts[1]
	}
	return s.Client.Put(ctx, msg)
}

// ListChatMessages returns all messages in a chat, oldest first.
func (s *Store) ListChatMessages(ctx context.Context, chat *Chat) ([]*ChatMessage, error) {
	q := dsorm.NewQuery("ChatMessage").Namespace(chat.Workspace).
		Ancestor(s.Client.Key(chat)).Order("created")
	msgs, _, err := dsorm.Query[*ChatMessage](ctx, s.Client, q, "")
	return msgs, err
}

// ListChatMessagesPaged returns paginated messages with cursor support.
// Messages are returned newest-first; the caller should reverse them for display.
func (s *Store) ListChatMessagesPaged(ctx context.Context, chat *Chat, limit int, cursor string) ([]*ChatMessage, string, error) {
	q := dsorm.NewQuery("ChatMessage").Namespace(chat.Workspace).
		Ancestor(s.Client.Key(chat)).Order("-created").Limit(limit)
	return dsorm.Query[*ChatMessage](ctx, s.Client, q, cursor)
}

// ClearChatMessages removes all messages for a chat.
func (s *Store) ClearChatMessages(ctx context.Context, chat *Chat) error {
	msgs, err := s.ListChatMessages(ctx, chat)
	if err != nil {
		return err
	}
	for _, m := range msgs {
		_ = s.Client.Delete(ctx, m)
	}
	return nil
}

// ── Helpers ─────────────────────────────────────────────────────────

// loadEnvFile reads a .env file and sets env vars that aren't already set.
// Each line should be KEY=VALUE (leading/trailing whitespace trimmed).
// Lines starting with # are comments. Missing file is silently ignored.
func loadEnvFile(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		// Don't override existing env vars
		if _, exists := os.LookupEnv(key); !exists {
			os.Setenv(key, val)
		}
	}
}

// getOrCreateEncKey reads the encryption key from the OS keyring (if supported)
// or from ALTCLAW_ENC_KEY env var (hex-encoded, 64 hex chars = 32 bytes).
// If not found, generates a new key and attempts to save it to the keyring,
// falling back to appending it to the .env file in configDir on headless setups.
func getOrCreateEncKey(configDir string) ([]byte, error) {
	var hexKey string
	if k, err := keyring.Get("altclaw", "encryption_key"); err == nil && k != "" {
		hexKey = k
	} else if k := os.Getenv("ALTCLAW_ENC_KEY"); k != "" {
		hexKey = k
	}

	if hexKey != "" {
		key, err := hex.DecodeString(hexKey)
		if err != nil {
			return nil, fmt.Errorf("ALTCLAW_ENC_KEY invalid hex: %w", err)
		}
		if len(key) != 32 {
			return nil, fmt.Errorf("ALTCLAW_ENC_KEY must be 32 bytes (64 hex chars), got %d bytes", len(key))
		}
		return key, nil
	}

	// Generate new key
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}

	hexKey = hex.EncodeToString(key)

	// Attempt to save to OS keyring first
	if err := keyring.Set("altclaw", "encryption_key", hexKey); err != nil {
		slog.Warn("Failed to save encryption key to OS keyring, falling back to .env", "error", err)

		// Fallback: Append to .env file
		envPath := filepath.Join(configDir, ".env")
		f, err := os.OpenFile(envPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
		if err != nil {
			return nil, fmt.Errorf("write .env: %w", err)
		}
		defer f.Close()

		if _, err := fmt.Fprintf(f, "ALTCLAW_ENC_KEY=%s\n", hexKey); err != nil {
			return nil, fmt.Errorf("write .env: %w", err)
		}
	}
	return key, nil
}
