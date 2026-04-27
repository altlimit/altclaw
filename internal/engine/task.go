package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/dop251/goja"
)

// RunTask implements bridge.TaskRunner.
// It spawns a child Engine with the same workspace/executor/store settings,
// executes the given code (inline JS or module path), and returns the result
// as a JSON-encoded string.
//
// code may be:
//   - A workspace-relative module path ending in .js (no newlines, no parens) → RunModule
//   - Any other string → Run (inline JS)
//
// Results are JSON-encoded so they survive the VM boundary cleanly.
func (e *Engine) RunTask(ctx context.Context, code string, _ string) (string, error) {
	child := New(e.ws, e.exec, e.taskUI, e.taskSession, e.taskStore).
		WithModuleDirs(e.moduleDirs...)

	// Track child so parent Cleanup() cascades
	e.taskMu.Lock()
	e.taskChildren = append(e.taskChildren, child)
	e.taskMu.Unlock()

	defer func() {
		child.Cleanup() //nolint:errcheck
		e.taskMu.Lock()
		for i, c := range e.taskChildren {
			if c == child {
				e.taskChildren = append(e.taskChildren[:i], e.taskChildren[i+1:]...)
				break
			}
		}
		e.taskMu.Unlock()
	}()

	// __taskResult captures the return value from inline task functions.
	// output() is overridden to a no-op so user calls don't interrupt execution.
	var taskJSON string
	child.SetGlobal("__taskResult", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) > 0 {
			exported := call.Arguments[0].Export()
			if b, err := json.Marshal(exported); err == nil {
				taskJSON = string(b)
			}
		}
		return goja.Undefined()
	})
	// Inherit agent bridge from parent so agent.run()/agent.result() works in tasks
	if e.agentRunner != nil {
		child.SetAgentRunner(e.agentRunner)
	}
	child.SetGlobal("output", func(call goja.FunctionCall) goja.Value {
		// In task context output() acts as ui.log — return value comes from `return`.
		if e.taskUI != nil {
			e.taskUI.Log(e.ConsoleFormat(call))
		}
		return goja.Undefined()
	})

	trimmed := strings.TrimSpace(code)
	isPath := !strings.Contains(trimmed, "\n") &&
		strings.HasSuffix(trimmed, ".js") &&
		!strings.Contains(trimmed, "(") // function bodies always contain parens

	var result *RunResult
	if isPath {
		result = child.RunModule(ctx, trimmed)
	} else {
		result = child.Run(ctx, code)
	}

	if result.Error != nil {
		// Clean the child's error before surfacing it to the parent bridge.
		// This prevents double-prefixes like:
		// "js error: GoError: task.join: js error: GoError: module not found"
		return "", fmt.Errorf("%s", cleanJSError(result.Error.Error()))
	}

	// Prefer the __taskResult value (captured via return), fall back to output()
	if taskJSON != "" {
		return taskJSON, nil
	}
	// JSON-encode the result.Value (set if output() was somehow called)
	if result.Value == "" {
		return "null", nil
	}
	var raw interface{}
	if err := json.Unmarshal([]byte(result.Value), &raw); err == nil {
		return result.Value, nil
	}
	b, _ := json.Marshal(result.Value)
	return string(b), nil
}
