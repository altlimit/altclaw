package executor

import (
	"context"
	"runtime"
	"testing"
	"time"
)

func TestWithSession_SessionFrom(t *testing.T) {
	ctx := context.Background()
	if id := SessionFrom(ctx); id != "" {
		t.Errorf("expected empty, got %q", id)
	}

	ctx = WithSession(ctx, "sub1_123")
	if id := SessionFrom(ctx); id != "sub1_123" {
		t.Errorf("expected 'sub1_123', got %q", id)
	}
}

func TestLocal_Run(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on windows")
	}

	local := NewLocal(t.TempDir(), nil)
	ctx := context.Background()

	result, err := local.Run(ctx, "echo", []string{"hello"})
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if result.ExitCode != 0 {
		t.Errorf("expected exit 0, got %d", result.ExitCode)
	}
	if result.Stdout != "hello\n" {
		t.Errorf("expected 'hello\\n', got %q", result.Stdout)
	}
}

func TestLocal_RunFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on windows")
	}

	local := NewLocal(t.TempDir(), nil)
	ctx := context.Background()

	result, err := local.Run(ctx, "false", nil)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if result.ExitCode == 0 {
		t.Error("expected non-zero exit code")
	}
}

func TestLocal_SpawnGetOutputTerminate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on windows")
	}

	local := NewLocal(t.TempDir(), nil)
	ctx := context.Background()

	// Spawn a long-running process
	id, err := local.Spawn(ctx, "sleep", []string{"10"})
	if err != nil {
		t.Fatalf("Spawn error: %v", err)
	}
	if id == "" {
		t.Fatal("expected non-empty handle ID")
	}

	// GetOutput should not error
	_, err = local.GetOutput(id)
	if err != nil {
		t.Fatalf("GetOutput error: %v", err)
	}

	// Terminate
	err = local.Terminate(id)
	if err != nil {
		t.Fatalf("Terminate error: %v", err)
	}
}

func TestLocal_GetOutputUnknown(t *testing.T) {
	local := NewLocal(t.TempDir(), nil)
	_, err := local.GetOutput("nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown handle")
	}
}

func TestLocal_SetImageNoOp(t *testing.T) {
	local := NewLocal(t.TempDir(), nil)
	// Should not panic
	local.SetImage("node:20", ImageOpts{}, "")
	local.SetImage("python:3", ImageOpts{}, "sub1")
}

func TestLocal_CleanupSessionNoOp(t *testing.T) {
	local := NewLocal(t.TempDir(), nil)
	// Should not panic
	local.CleanupSession("sub1")
}

func TestLocal_Whitelist(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on windows")
	}

	local := NewLocal(t.TempDir(), []string{"echo"})
	ctx := context.Background()

	// Allowed
	_, err := local.Run(ctx, "echo", []string{"ok"})
	if err != nil {
		t.Fatalf("expected echo to be allowed: %v", err)
	}

	// Blocked
	_, err = local.Run(ctx, "ls", nil)
	if err == nil {
		t.Fatal("expected ls to be blocked by whitelist")
	}
}

func TestLocal_SpawnCapture(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on windows")
	}

	local := NewLocal(t.TempDir(), nil)
	ctx := context.Background()

	id, err := local.Spawn(ctx, "echo", []string{"captured"})
	if err != nil {
		t.Fatalf("Spawn error: %v", err)
	}

	// Wait a bit for process to complete
	time.Sleep(100 * time.Millisecond)

	output, err := local.GetOutput(id)
	if err != nil {
		t.Fatalf("GetOutput error: %v", err)
	}
	if output != "captured\n" {
		t.Errorf("expected 'captured\\n', got %q", output)
	}
}

func TestLocal_WhitelistWildcard(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on windows")
	}

	local := NewLocal(t.TempDir(), []string{"*"})
	ctx := context.Background()

	// Wildcard should allow any command
	result, err := local.Run(ctx, "echo", []string{"allowed"})
	if err != nil {
		t.Fatalf("expected wildcard to allow echo: %v", err)
	}
	if result.ExitCode != 0 {
		t.Errorf("expected exit 0, got %d", result.ExitCode)
	}

	result, err = local.Run(ctx, "ls", nil)
	if err != nil {
		t.Fatalf("expected wildcard to allow ls: %v", err)
	}
}

func TestLocal_WhitelistWildcardMixed(t *testing.T) {
	// * mixed with other entries should still allow all
	local := NewLocal(t.TempDir(), []string{"echo", "*", "ls"})

	if !local.IsAllowed("anything") {
		t.Error("expected * to allow any command even with other entries")
	}
}

func TestLocal_EmptyWhitelist(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on windows")
	}

	// Empty whitelist = executor allows all (bridge layer handles the confirm gate)
	local := NewLocal(t.TempDir(), nil)
	ctx := context.Background()

	result, err := local.Run(ctx, "echo", []string{"allowed"})
	if err != nil {
		t.Fatalf("empty whitelist should allow: %v", err)
	}
	if result.Stdout != "allowed\n" {
		t.Errorf("expected 'allowed\\n', got %q", result.Stdout)
	}
}

func TestLocal_EmptyWhitelistSlice(t *testing.T) {
	// Explicitly empty slice (not nil) should also allow all at executor level
	local := NewLocal(t.TempDir(), []string{})

	if !local.IsAllowed("anything") {
		t.Error("expected empty slice whitelist to allow all commands")
	}
}

func TestLocal_IsAllowed(t *testing.T) {
	tests := []struct {
		name      string
		whitelist []string
		cmd       string
		want      bool
	}{
		{"nil whitelist allows all", nil, "rm", true},
		{"empty whitelist allows all", []string{}, "rm", true},
		{"wildcard allows all", []string{"*"}, "rm", true},
		{"explicit match", []string{"echo", "ls"}, "echo", true},
		{"no match", []string{"echo", "ls"}, "rm", false},
		{"wildcard with others", []string{"echo", "*"}, "anything", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			local := NewLocal(t.TempDir(), tt.whitelist)
			if got := local.IsAllowed(tt.cmd); got != tt.want {
				t.Errorf("IsAllowed(%q) = %v, want %v (whitelist=%v)", tt.cmd, got, tt.want, tt.whitelist)
			}
		})
	}
}

func TestLocal_Info(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on windows")
	}

	local := NewLocal(t.TempDir(), nil)
	ctx := context.Background()

	info, err := local.Info(ctx)
	if err != nil {
		t.Fatalf("Info error: %v", err)
	}

	// Check top-level keys
	for _, key := range []string{"os", "resources", "runtimes", "capabilities", "paths"} {
		if _, ok := info[key]; !ok {
			t.Errorf("missing top-level key %q", key)
		}
	}

	// Check os.type
	osInfo, ok := info["os"].(map[string]any)
	if !ok {
		t.Fatal("os is not a map")
	}
	if osType, ok := osInfo["type"].(string); !ok || osType == "" {
		t.Error("os.type should be a non-empty string")
	}
	if arch, ok := osInfo["arch"].(string); !ok || arch == "" {
		t.Error("os.arch should be a non-empty string")
	}

	// Check resources.cpus > 0
	resources, ok := info["resources"].(map[string]any)
	if !ok {
		t.Fatal("resources is not a map")
	}
	if cpus, ok := resources["cpus"].(int); !ok || cpus < 1 {
		t.Errorf("resources.cpus should be >= 1, got %v", resources["cpus"])
	}

	// Check capabilities
	capabilities, ok := info["capabilities"].(map[string]any)
	if !ok {
		t.Fatal("capabilities is not a map")
	}
	if executor, ok := capabilities["executor"].(string); !ok || executor != "local" {
		t.Errorf("capabilities.executor should be 'local', got %v", capabilities["executor"])
	}

	// Check paths.workspace
	paths, ok := info["paths"].(map[string]any)
	if !ok {
		t.Fatal("paths is not a map")
	}
	if ws, ok := paths["workspace"].(string); !ok || ws == "" {
		t.Error("paths.workspace should be a non-empty string")
	}
}

