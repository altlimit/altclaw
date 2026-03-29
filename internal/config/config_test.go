package config

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
)

func TestNewStore(t *testing.T) {
	dir := t.TempDir()
	// Clear any existing env var to force generation
	os.Unsetenv("ALTCLAW_ENC_KEY")
	defer os.Unsetenv("ALTCLAW_ENC_KEY")

	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	// Key may be stored in OS keyring (wincred/Keychain/libsecret) or in .env fallback.
	// Check .env first — if it exists, verify its contents.
	envPath := filepath.Join(dir, ".env")
	data, envErr := os.ReadFile(envPath)
	if envErr == nil {
		content := string(data)
		if !strings.Contains(content, "ALTCLAW_ENC_KEY=") {
			t.Error(".env should contain ALTCLAW_ENC_KEY")
		}
		// Key should be 64 hex chars
		for _, line := range strings.Split(content, "\n") {
			if strings.HasPrefix(line, "ALTCLAW_ENC_KEY=") {
				hexKey := strings.TrimSpace(strings.TrimPrefix(line, "ALTCLAW_ENC_KEY="))
				if len(hexKey) != 64 {
					t.Errorf("ALTCLAW_ENC_KEY should be 64 hex chars, got %d", len(hexKey))
				}
			}
		}
	} else {
		// No .env — key should be in OS keyring
		k, keyErr := keyring.Get("altclaw", "encryption_key")
		if keyErr != nil || k == "" {
			t.Fatalf("encryption key not found in .env or OS keyring: envErr=%v, keyringErr=%v", envErr, keyErr)
		}
		if len(k) != 64 {
			t.Errorf("keyring key should be 64 hex chars, got %d", len(k))
		}
	}
}

func TestGetConfig_Defaults(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	cfg, err := store.GetConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AgentTimeout != 0 {
		t.Errorf("expected agent_timeout 0 (unset), got %d", cfg.AgentTimeout)
	}
	if cfg.Executor != "auto" {
		t.Errorf("expected executor 'auto', got %q", cfg.Executor)
	}
	if cfg.DockerImage != "alpine:latest" {
		t.Errorf("expected docker_image 'alpine:latest', got %q", cfg.DockerImage)
	}
}

func TestProviderCRUD(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if store.IsConfigured() {
		t.Error("should not be configured with no providers")
	}

	// Create
	p := &Provider{
		Name:         "default",
		ProviderType: "gemini",
		Model:        "flash",
		APIKey:       "secret-key-123",
	}
	if err := store.SaveProvider(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if p.ID == 0 {
		t.Error("expected auto-generated ID")
	}

	if !store.IsConfigured() {
		t.Error("should be configured with a provider")
	}

	// Read
	got, err := store.GetProvider("default")
	if err != nil {
		t.Fatal(err)
	}
	if got.ProviderType != "gemini" {
		t.Errorf("expected 'gemini', got %q", got.ProviderType)
	}
	if got.APIKey != "secret-key-123" {
		t.Errorf("expected decrypted key, got %q", got.APIKey)
	}

	// Delete
	if err := store.DeleteProvider(context.Background(), got); err != nil {
		t.Fatal(err)
	}
	if store.IsConfigured() {
		t.Error("should not be configured after delete")
	}
}

func TestWorkspaceCRUD(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	// Create
	ws, err := store.GetWorkspace("/tmp/myproject")
	if err != nil {
		t.Fatal(err)
	}
	if ws.ID == "" {
		t.Error("expected auto-generated ID")
	}
	if ws.Name != "myproject" {
		t.Errorf("expected name 'myproject', got %q", ws.Name)
	}

	// Retrieve same
	ws2, err := store.GetWorkspace("/tmp/myproject")
	if err != nil {
		t.Fatal(err)
	}
	if ws2.ID != ws.ID {
		t.Errorf("expected same ID %s, got %s", ws.ID, ws2.ID)
	}

	// List
	all, err := store.ListWorkspaces(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Errorf("expected 1 workspace, got %d", len(all))
	}
}

func TestEmptyWorkspaceBlocked(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	_, err = store.GetWorkspace("")
	if err == nil {
		t.Error("expected error for empty workspace path")
	}
}

func TestWorkspacePathNormalization(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	// Create with a clean path
	ws1, err := store.GetWorkspace("/tmp/myproject")
	if err != nil {
		t.Fatal(err)
	}

	// Same path with trailing slash should return same workspace
	ws2, err := store.GetWorkspace("/tmp/myproject/")
	if err != nil {
		t.Fatal(err)
	}
	if ws2.ID != ws1.ID {
		t.Errorf("trailing slash produced different ID: %s vs %s", ws1.ID, ws2.ID)
	}

	// Same path with extra components should return same workspace
	ws3, err := store.GetWorkspace("/tmp/myproject/./")
	if err != nil {
		t.Fatal(err)
	}
	if ws3.ID != ws1.ID {
		t.Errorf("dot component produced different ID: %s vs %s", ws1.ID, ws3.ID)
	}

	// Verify only 1 workspace exists
	all, err := store.ListWorkspaces(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Errorf("expected 1 workspace, got %d", len(all))
	}
}

func TestProviderSummary(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	// Default only — no summary
	store.SaveProvider(context.Background(), &Provider{Name: "default", ProviderType: "gemini", Model: "flash"})
	if s := store.ProviderSummary(); s != "" {
		t.Errorf("expected empty summary, got %q", s)
	}

	// Add specialist
	store.SaveProvider(context.Background(), &Provider{
		Name:         "coding",
		ProviderType: "anthropic",
		Model:        "claude-sonnet",
		Docs:         []string{"coding", "debugging"},
		Description:  "Best for code",
	})

	s := store.ProviderSummary()
	if s == "" {
		t.Fatal("expected non-empty summary")
	}
	for _, want := range []string{"coding", "debugging", "Best for code"} {
		found := false
		for i := 0; i <= len(s)-len(want); i++ {
			if s[i:i+len(want)] == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("summary missing %q: %s", want, s)
		}
	}
}

func TestSaveConfig_Callback(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()
	store.GetConfig()

	// Save with callback
	if err := store.SaveConfig(ctx, func(c *AppConfig) error {
		c.AgentTimeout = 999
		c.Executor = "local"
		c.LocalWhitelist = []string{"echo", "ls"}
		return nil
	}); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	// Verify changes persisted
	cfg, err := store.GetConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AgentTimeout != 999 {
		t.Errorf("expected agent_timeout 999, got %d", cfg.AgentTimeout)
	}
	if cfg.Executor != "local" {
		t.Errorf("expected executor 'local', got %q", cfg.Executor)
	}
	if len(cfg.LocalWhitelist) != 2 || cfg.LocalWhitelist[0] != "echo" {
		t.Errorf("expected whitelist [echo, ls], got %v", cfg.LocalWhitelist)
	}
}

func TestSaveConfig_PartialUpdate(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()
	store.GetConfig()

	// First save
	if err := store.SaveConfig(ctx, func(c *AppConfig) error {
		c.AgentTimeout = 300
		c.MaxIterations = 50
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// Second save — only change MaxIterations
	if err := store.SaveConfig(ctx, func(c *AppConfig) error {
		c.MaxIterations = 100
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	cfg, err := store.GetConfig()
	if err != nil {
		t.Fatal(err)
	}
	// AgentTimeout should still be 300 from first save
	if cfg.AgentTimeout != 300 {
		t.Errorf("expected agent_timeout 300 (unchanged), got %d", cfg.AgentTimeout)
	}
	if cfg.MaxIterations != 100 {
		t.Errorf("expected max_iterations 100, got %d", cfg.MaxIterations)
	}
}

func TestSaveConfig_NoRecursion(t *testing.T) {
	// Regression: SaveConfig used to call GetConfig() which calls SaveConfig() for VAPID keys
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	store.GetConfig()

	// This must complete without deadlock/stack overflow
	if err := store.SaveConfig(context.Background(), func(c *AppConfig) error {
		c.AgentTimeout = 42
		return nil
	}); err != nil {
		t.Fatalf("SaveConfig should not deadlock: %v", err)
	}
}

func TestSaveConfig_ClearsWhitelist(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()
	store.GetConfig()

	// Set whitelist
	store.SaveConfig(ctx, func(c *AppConfig) error {
		c.LocalWhitelist = []string{"npm", "go"}
		return nil
	})

	// Clear it
	store.SaveConfig(ctx, func(c *AppConfig) error {
		c.LocalWhitelist = []string{}
		return nil
	})

	cfg, _ := store.GetConfig()
	if len(cfg.LocalWhitelist) != 0 {
		t.Errorf("expected empty whitelist, got %v", cfg.LocalWhitelist)
	}
}

func TestSaveWorkspace_Callback(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	_, err = store.GetWorkspace("/tmp/test-ws")
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	if err := store.SaveWorkspace(ctx, func(w *Workspace) error {
		w.LastProvider = "coding"
		w.LogLevel = "debug"
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// Verify changes persisted
	got := store.Workspace()
	if got.LastProvider != "coding" {
		t.Errorf("expected last_provider 'coding', got %q", got.LastProvider)
	}
	if got.LogLevel != "debug" {
		t.Errorf("expected log_level 'debug', got %q", got.LogLevel)
	}
}

func TestGetWorkspaceByID(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ws, err := store.GetWorkspace("/tmp/test-byid")
	if err != nil {
		t.Fatal(err)
	}

	got, err := store.GetWorkspaceByID(ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Path != ws.Path {
		t.Errorf("expected path %q, got %q", ws.Path, got.Path)
	}
}

func TestWorkspace_Patch(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ws, err := store.GetWorkspace("/tmp/test-patch")
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	if err := ws.Patch(ctx, store, map[string]any{
		"last_provider": "fast",
		"log_level":     "warn",
	}); err != nil {
		t.Fatal(err)
	}

	got := store.Workspace()
	if got.LastProvider != "fast" {
		t.Errorf("expected last_provider 'fast', got %q", got.LastProvider)
	}
	if got.LogLevel != "warn" {
		t.Errorf("expected log_level 'warn', got %q", got.LogLevel)
	}
	// Original fields should be preserved
	if got.Name != ws.Name {
		t.Errorf("expected name %q preserved, got %q", ws.Name, got.Name)
	}
}
