package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"altclaw.ai/internal/config"
)

type testUI struct {
	logs []string
}

func (u *testUI) Log(msg string) {
	u.logs = append(u.logs, msg)
}
func (u *testUI) Ask(question string) string { return "" }
func (u *testUI) Confirm(action, label, summary string, params map[string]any) string {
	return "no"
}

func TestEngine_RunBasicJS(t *testing.T) {
	eng := New(&config.Workspace{Path: t.TempDir()}, nil, &testUI{}, "", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result := eng.Run(ctx, `output(1 + 2);`)
	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if result.Value != "3" {
		t.Errorf("expected '3', got %q", result.Value)
	}
}

func TestEngine_VarIsolation(t *testing.T) {
	eng := New(&config.Workspace{Path: t.TempDir()}, nil, &testUI{}, "", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// First run sets a var
	result := eng.Run(ctx, `var x = 42; output(x);`)
	if result.Error != nil {
		t.Fatalf("first run error: %v", result.Error)
	}

	// Second run can access the same var (global scope, no IIFE)
	result = eng.Run(ctx, `var y = 99; output(y);`)
	if result.Error != nil {
		t.Fatalf("second run error: %v", result.Error)
	}
	if result.Value != "99" {
		t.Errorf("expected '99', got %q", result.Value)
	}
}

func TestEngine_StorePersistsAcrossRuns(t *testing.T) {
	eng := New(&config.Workspace{Path: t.TempDir()}, nil, &testUI{}, "", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Set value in store
	result := eng.Run(ctx, `store.name = "test"; output("ok");`)
	if result.Error != nil {
		t.Fatalf("set store error: %v", result.Error)
	}

	// Read value from store in separate run
	result = eng.Run(ctx, `output(store.name);`)
	if result.Error != nil {
		t.Fatalf("read store error: %v", result.Error)
	}
	if result.Value != "test" {
		t.Errorf("expected 'test', got %q", result.Value)
	}
}

func TestEngine_Sleep(t *testing.T) {
	eng := New(&config.Workspace{Path: t.TempDir()}, nil, &testUI{}, "", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	start := time.Now()
	result := eng.Run(ctx, `sleep(100); "output";`)
	elapsed := time.Since(start)

	if result.Error != nil {
		t.Fatalf("sleep error: %v", result.Error)
	}
	if elapsed < 80*time.Millisecond {
		t.Errorf("sleep too short: %v", elapsed)
	}
}



func TestEngine_Timeout(t *testing.T) {
	eng := New(&config.Workspace{Path: t.TempDir()}, nil, &testUI{}, "", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	result := eng.Run(ctx, `while(true){}`)
	if result.Error == nil {
		t.Fatal("expected timeout error")
	}
}

func TestEngine_ConsoleLog(t *testing.T) {
	ui := &testUI{}
	eng := New(&config.Workspace{Path: t.TempDir()}, nil, ui, "", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	eng.Run(ctx, `console.log("hello", "world");`)
	if len(ui.logs) != 1 || ui.logs[0] != "hello world" {
		t.Errorf("expected log 'hello world', got %v", ui.logs)
	}
}

// --- Pausable Deadline Tests ---

// TestDeadline_PausePreventsTimeout verifies that pausing the deadline
// prevents a timeout even when elapsed wall time exceeds the budget.
func TestDeadline_PausePreventsTimeout(t *testing.T) {
	eng := New(&config.Workspace{Path: t.TempDir()}, nil, &testUI{}, "", nil)
	// Budget of 200ms
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	// Run code that:
	// 1. Does a tiny bit of work (uses some budget)
	// 2. Gets paused externally (simulating agent.result wait)
	// 3. Resumes and finishes
	done := make(chan *RunResult, 1)
	go func() {
		result := eng.Run(ctx, `
			sleep(50);
			output("survived");
		`)
		done <- result
	}()

	// Let the code start executing
	time.Sleep(30 * time.Millisecond)

	// Pause the deadline (simulating agent.result blocking)
	eng.PauseDeadline()

	// Wait longer than the original budget while paused
	time.Sleep(300 * time.Millisecond)

	// Resume — remaining budget should be ~150ms (200ms - 50ms used)
	eng.ResumeDeadline()

	result := <-done
	if result.Error != nil {
		t.Fatalf("should not have timed out while paused, got: %v", result.Error)
	}
	if result.Value != "survived" {
		t.Errorf("expected 'survived', got %q", result.Value)
	}
}

// TestDeadline_ResumeExhaustedBudget verifies that after resuming,
// the remaining budget is correctly enforced.
func TestDeadline_ResumeExhaustedBudget(t *testing.T) {
	eng := New(&config.Workspace{Path: t.TempDir()}, nil, &testUI{}, "", nil)
	// Budget of 200ms
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	done := make(chan *RunResult, 1)
	go func() {
		// This code sleeps 100ms, then enters an infinite loop
		// After pause/resume, it should timeout with remaining ~100ms
		result := eng.Run(ctx, `
			sleep(100);
			while(true){}
		`)
		done <- result
	}()

	// Let the code run and consume ~100ms of budget
	time.Sleep(80 * time.Millisecond)

	// Pause
	eng.PauseDeadline()
	time.Sleep(50 * time.Millisecond)

	// Resume — should have ~100ms left and then timeout on the while(true)
	eng.ResumeDeadline()

	result := <-done
	if result.Error == nil {
		t.Fatal("expected timeout error after budget exhausted")
	}
}

// TestDeadline_MultiplePauseResumeCycles verifies that multiple
// pause/resume cycles correctly track the remaining budget.
func TestDeadline_MultiplePauseResumeCycles(t *testing.T) {
	eng := New(&config.Workspace{Path: t.TempDir()}, nil, &testUI{}, "", nil)
	// Budget of 500ms
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	done := make(chan *RunResult, 1)
	go func() {
		// Sleeps 3x50ms = 150ms of actual execution
		result := eng.Run(ctx, `
			sleep(50);
			sleep(50);
			sleep(50);
			output("output");
		`)
		done <- result
	}()

	// Cycle 1: let it run 80ms, pause, wait 200ms, resume
	time.Sleep(60 * time.Millisecond)
	eng.PauseDeadline()
	time.Sleep(200 * time.Millisecond)
	eng.ResumeDeadline()

	// Cycle 2: let it run 80ms more, pause, wait 200ms, resume
	time.Sleep(60 * time.Millisecond)
	eng.PauseDeadline()
	time.Sleep(200 * time.Millisecond)
	eng.ResumeDeadline()

	// Total wall time ~800ms but only ~150ms of execution budget used
	result := <-done
	if result.Error != nil {
		t.Fatalf("should not timeout — only ~150ms of 500ms budget used, got: %v", result.Error)
	}
	if result.Value != "output" {
		t.Errorf("expected 'done', got %q", result.Value)
	}
}

// TestDeadline_PauseNoopWhenNoDeadline verifies that Pause/Resume
// are safe no-ops when there's no active deadline.
func TestDeadline_PauseNoopWhenNoDeadline(t *testing.T) {
	eng := New(&config.Workspace{Path: t.TempDir()}, nil, &testUI{}, "", nil)

	// Should not panic
	eng.PauseDeadline()
	eng.ResumeDeadline()
	eng.PauseDeadline()
	eng.ResumeDeadline()
}

// TestDeadline_ConcurrentPauseResume verifies no data races when
// Pause/Resume are called concurrently.
func TestDeadline_ConcurrentPauseResume(t *testing.T) {
	eng := New(&config.Workspace{Path: t.TempDir()}, nil, &testUI{}, "", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan *RunResult, 1)
	go func() {
		result := eng.Run(ctx, `sleep(500); output("ok");`)
		done <- result
	}()

	// Rapidly pause/resume from multiple goroutines
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 5; j++ {
				eng.PauseDeadline()
				time.Sleep(5 * time.Millisecond)
				eng.ResumeDeadline()
				time.Sleep(5 * time.Millisecond)
			}
		}()
	}
	wg.Wait()

	// Should still complete
	select {
	case result := <-done:
		if result.Error != nil {
			t.Logf("got error (acceptable in race test): %v", result.Error)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("test hung — possible deadlock in pause/resume")
	}
}

// TestDeadline_TimeoutWithoutPause verifies the basic timeout still
// works correctly when no pause/resume is used.
func TestDeadline_TimeoutWithoutPause(t *testing.T) {
	eng := New(&config.Workspace{Path: t.TempDir()}, nil, &testUI{}, "", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	result := eng.Run(ctx, `while(true){}`)
	elapsed := time.Since(start)

	if result.Error == nil {
		t.Fatal("expected timeout error")
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("timeout took too long: %v", elapsed)
	}
}

// TestDeadline_ContextCancelDuringPause verifies that user cancellation
// (e.g. stop button) works even while the deadline is paused.
func TestDeadline_ContextCancelDuringPause(t *testing.T) {
	eng := New(&config.Workspace{Path: t.TempDir()}, nil, &testUI{}, "", nil)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan *RunResult, 1)
	go func() {
		result := eng.Run(ctx, `while(true){ sleep(10); }`)
		done <- result
	}()

	// Let it start, then pause
	time.Sleep(50 * time.Millisecond)
	eng.PauseDeadline()

	// Cancel the context (simulating user stop)
	cancel()

	select {
	case result := <-done:
		if result.Error == nil {
			t.Fatal("expected cancellation error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("context cancel during pause should still interrupt execution")
	}
}

// --- require() Module System Tests ---

func TestEngine_RequireWorkspaceModule(t *testing.T) {
	workspace := t.TempDir()
	// Write a CommonJS module to workspace
	modPath := filepath.Join(workspace, "math.js")
	if err := os.WriteFile(modPath, []byte(`module.exports = { add: function(a, b) { return a + b; } };`), 0644); err != nil {
		t.Fatal(err)
	}

	eng := New(&config.Workspace{Path: workspace}, nil, &testUI{}, "", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Use OS-absolute path so it works cross-platform (the SourceLoader's
	// filepath.IsAbs check works with native paths on both Windows and Linux).
	escapedPath := strings.ReplaceAll(modPath, `\`, `\\`)
	result := eng.Run(ctx, `var math = require("`+escapedPath+`"); output(math.add(1, 2));`)
	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if result.Value != "3" {
		t.Errorf("expected '3', got %q", result.Value)
	}
}

func TestEngine_RequireBuiltinModule(t *testing.T) {
	eng := New(&config.Workspace{Path: t.TempDir()}, nil, &testUI{}, "", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Bare module name (no prefix) loads from stdlib embedded modules.
	result := eng.Run(ctx, `var m = require("mcp"); output(typeof m.connect);`)
	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if result.Value != "function" {
		t.Errorf("expected 'function', got %q", result.Value)
	}
}

func TestEngine_RequirePathJail(t *testing.T) {
	eng := New(&config.Workspace{Path: t.TempDir()}, nil, &testUI{}, "", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result := eng.Run(ctx, `require("../../etc/passwd");`)
	if result.Error == nil {
		t.Fatal("expected error for path traversal")
	}
}
