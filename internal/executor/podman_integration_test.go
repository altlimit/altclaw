package executor

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestPodmanIntegration runs integration tests against the Docker executor
// using the "podman" CLI binary. Skipped if podman is not installed.
func TestPodmanIntegration(t *testing.T) {
	if _, err := exec.LookPath("podman"); err != nil {
		t.Skip("podman not installed, skipping integration tests")
	}

	workspace := t.TempDir()

	// Create a test file in workspace
	testFile := filepath.Join(workspace, "hello.txt")
	if err := os.WriteFile(testFile, []byte("hello from workspace"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	t.Run("NewDocker_Podman", func(t *testing.T) {
		d, err := NewDocker("podman", "alpine:latest", workspace, "/workspace")
		if err != nil {
			t.Fatalf("NewDocker with podman CLI failed: %v", err)
		}
		defer d.Cleanup()
		t.Logf("NewDocker('podman') succeeded, prefix=%s", d.prefix)
	})

	t.Run("QualifyImage", func(t *testing.T) {
		cases := []struct {
			input, want string
		}{
			{"alpine:latest", "docker.io/library/alpine:latest"},
			{"alpine", "docker.io/library/alpine"},
			{"ubuntu:22.04", "docker.io/library/ubuntu:22.04"},
			{"alpine/socat", "docker.io/alpine/socat"},
			{"myuser/myimage:v1", "docker.io/myuser/myimage:v1"},
			{"ghcr.io/owner/image:tag", "ghcr.io/owner/image:tag"},
			{"docker.io/library/alpine:latest", "docker.io/library/alpine:latest"},
			{"registry.example.com/foo/bar", "registry.example.com/foo/bar"},
		}
		for _, tc := range cases {
			got := qualifyImage(tc.input)
			if got != tc.want {
				t.Errorf("qualifyImage(%q) = %q, want %q", tc.input, got, tc.want)
			}
		}
	})

	t.Run("Run_BasicCommand", func(t *testing.T) {
		d, err := NewDocker("podman", "alpine:latest", workspace, "/workspace")
		if err != nil {
			t.Fatalf("NewDocker failed: %v", err)
		}
		defer d.Cleanup()

		result, err := d.Run(context.Background(), "echo", []string{"hello", "podman"})
		if err != nil {
			t.Fatalf("Run failed: %v", err)
		}
		if result.ExitCode != 0 {
			t.Errorf("expected exit 0, got %d (stderr: %s)", result.ExitCode, result.Stderr)
		}
		if result.Stdout != "hello podman\n" {
			t.Errorf("expected stdout %q, got %q", "hello podman\n", result.Stdout)
		}
	})

	t.Run("Run_WorkspaceMount", func(t *testing.T) {
		d, err := NewDocker("podman", "alpine:latest", workspace, "/workspace")
		if err != nil {
			t.Fatalf("NewDocker failed: %v", err)
		}
		defer d.Cleanup()

		result, err := d.Run(context.Background(), "cat", []string{"/workspace/hello.txt"})
		if err != nil {
			t.Fatalf("Run failed: %v", err)
		}
		if result.ExitCode != 0 {
			t.Errorf("expected exit 0, got %d (stderr: %s)", result.ExitCode, result.Stderr)
		}
		if result.Stdout != "hello from workspace" {
			t.Errorf("expected workspace file contents, got %q", result.Stdout)
		}
	})

	t.Run("Run_WriteToWorkspace", func(t *testing.T) {
		d, err := NewDocker("podman", "alpine:latest", workspace, "/workspace")
		if err != nil {
			t.Fatalf("NewDocker failed: %v", err)
		}
		defer d.Cleanup()

		result, err := d.Run(context.Background(), "sh", []string{"-c", "echo 'written from podman' > /workspace/output.txt"})
		if err != nil {
			t.Fatalf("Run failed: %v", err)
		}
		if result.ExitCode != 0 {
			t.Errorf("expected exit 0, got %d (stderr: %s)", result.ExitCode, result.Stderr)
		}

		content, err := os.ReadFile(filepath.Join(workspace, "output.txt"))
		if err != nil {
			t.Fatalf("failed to read output file: %v", err)
		}
		if string(content) != "written from podman\n" {
			t.Errorf("expected 'written from podman\\n', got %q", string(content))
		}
	})

	t.Run("Run_ExitCode", func(t *testing.T) {
		d, err := NewDocker("podman", "alpine:latest", workspace, "/workspace")
		if err != nil {
			t.Fatalf("NewDocker failed: %v", err)
		}
		defer d.Cleanup()

		result, err := d.Run(context.Background(), "sh", []string{"-c", "exit 42"})
		if err != nil {
			t.Fatalf("Run error: %v", err)
		}
		if result.ExitCode != 42 {
			t.Errorf("expected exit 42, got %d", result.ExitCode)
		}
	})

	t.Run("Spawn_GetOutput_Terminate", func(t *testing.T) {
		d, err := NewDocker("podman", "alpine:latest", workspace, "/workspace")
		if err != nil {
			t.Fatalf("NewDocker failed: %v", err)
		}
		defer d.Cleanup()

		id, err := d.Spawn(context.Background(), "sh", []string{"-c", "echo spawned; sleep 30"})
		if err != nil {
			t.Fatalf("Spawn failed: %v", err)
		}

		time.Sleep(500 * time.Millisecond)

		output, err := d.GetOutput(id)
		if err != nil {
			t.Fatalf("GetOutput failed: %v", err)
		}
		if !strings.Contains(output, "spawned") {
			t.Errorf("expected output to contain 'spawned', got %q", output)
		}

		if err := d.Terminate(id); err != nil {
			t.Fatalf("Terminate failed: %v", err)
		}
	})

	t.Run("Popen_ReadLineWriteStdin", func(t *testing.T) {
		d, err := NewDocker("podman", "alpine:latest", workspace, "/workspace")
		if err != nil {
			t.Fatalf("NewDocker failed: %v", err)
		}
		defer d.Cleanup()

		id, err := d.Popen(context.Background(), "cat", nil)
		if err != nil {
			t.Fatalf("Popen failed: %v", err)
		}

		if err := d.WriteStdin(id, "hello popen\n"); err != nil {
			t.Fatalf("WriteStdin failed: %v", err)
		}

		line, err := d.ReadLine(id, 5000)
		if err != nil {
			t.Fatalf("ReadLine failed: %v", err)
		}
		if line != "hello popen" {
			t.Errorf("expected 'hello popen', got %q", line)
		}

		if err := d.Terminate(id); err != nil {
			t.Fatalf("Terminate popen failed: %v", err)
		}
	})

	t.Run("SetImage_SessionContainer", func(t *testing.T) {
		d, err := NewDocker("podman", "alpine:latest", workspace, "/workspace")
		if err != nil {
			t.Fatalf("NewDocker failed: %v", err)
		}
		defer d.Cleanup()

		ctx := context.Background()
		if _, err := d.Run(ctx, "echo", []string{"init"}); err != nil {
			t.Fatalf("initial Run failed: %v", err)
		}

		sessionID := "sub1_test"
		d.SetImage("alpine:latest", ImageOpts{}, sessionID)

		sessionCtx := WithSession(ctx, sessionID)
		result, err := d.Run(sessionCtx, "echo", []string{"from session"})
		if err != nil {
			t.Fatalf("session Run failed: %v", err)
		}
		if result.Stdout != "from session\n" {
			t.Errorf("expected 'from session\\n', got %q", result.Stdout)
		}

		d.mu.Lock()
		_, hasSession := d.sessions[sessionID]
		d.mu.Unlock()
		if !hasSession {
			t.Error("expected session container to exist")
		}

		d.CleanupSession(sessionID)
		d.mu.Lock()
		_, hasSessionAfter := d.sessions[sessionID]
		d.mu.Unlock()
		if hasSessionAfter {
			t.Error("expected session container to be cleaned up")
		}
	})

	t.Run("SetImage_SwitchDefault", func(t *testing.T) {
		d, err := NewDocker("podman", "alpine:latest", workspace, "/workspace")
		if err != nil {
			t.Fatalf("NewDocker failed: %v", err)
		}
		defer d.Cleanup()

		ctx := context.Background()
		if _, err := d.Run(ctx, "echo", []string{"init"}); err != nil {
			t.Fatalf("initial Run failed: %v", err)
		}

		oldContainer := d.defaultContainer
		d.SetImage("alpine:latest", ImageOpts{}, "")

		if d.defaultContainer != "" {
			t.Error("expected default container to be cleared after SetImage")
		}

		result, err := d.Run(ctx, "echo", []string{"new image"})
		if err != nil {
			t.Fatalf("Run after SetImage failed: %v", err)
		}
		if result.Stdout != "new image\n" {
			t.Errorf("wrong stdout: %q", result.Stdout)
		}
		if d.defaultContainer == oldContainer {
			t.Error("expected new container ID after image switch")
		}
	})

	t.Run("NetworkIsolation_ProxyRelay", func(t *testing.T) {
		d, err := NewDocker("podman", "alpine:latest", workspace, "/workspace")
		if err != nil {
			t.Fatalf("NewDocker failed: %v", err)
		}
		defer d.Cleanup()

		result, err := d.Run(context.Background(), "echo", []string{"testing network"})
		if err != nil {
			t.Fatalf("Run failed: %v", err)
		}
		if result.ExitCode != 0 {
			t.Fatalf("expected exit 0, got %d", result.ExitCode)
		}

		if d.networkName == "" {
			t.Error("expected network name to be set")
		}
		if d.proxyRelay == "" {
			t.Error("expected proxy relay container ID")
		}
		if d.proxyRelayIP == "" {
			t.Error("expected proxy relay IP")
		}
	})

	t.Run("Cleanup_AllContainers", func(t *testing.T) {
		d, err := NewDocker("podman", "alpine:latest", workspace, "/workspace")
		if err != nil {
			t.Fatalf("NewDocker failed: %v", err)
		}

		if _, err := d.Run(context.Background(), "echo", []string{"init"}); err != nil {
			t.Fatalf("Run failed: %v", err)
		}
		defaultID := d.defaultContainer

		if err := d.Cleanup(); err != nil {
			t.Fatalf("Cleanup failed: %v", err)
		}

		cmd := exec.Command("podman", "inspect", defaultID)
		if err := cmd.Run(); err == nil {
			t.Error("expected default container to be removed after cleanup")
		}
	})

	t.Run("ContextCancellation", func(t *testing.T) {
		d, err := NewDocker("podman", "alpine:latest", workspace, "/workspace")
		if err != nil {
			t.Fatalf("NewDocker failed: %v", err)
		}
		defer d.Cleanup()

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		_, err = d.Run(ctx, "sleep", []string{"30"})
		if err == nil {
			t.Error("expected error from context cancellation")
		}
	})
}
