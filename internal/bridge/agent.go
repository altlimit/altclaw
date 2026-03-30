package bridge

import (
	"context"
	"sync"

	"github.com/dop251/goja"
	"github.com/google/uuid"
)

// SubAgentRunner is the interface the agent must implement for sub-agent spawning.
type SubAgentRunner interface {
	// RunSubAgent processes the given task string through a fresh agent conversation
	// and returns the final response.
	RunSubAgent(ctx context.Context, task string) (string, error)

	// RunSubAgentWith processes the task using a specific named provider.
	RunSubAgentWith(ctx context.Context, task, providerName string) (string, error)
}

// DeadlinePauser allows pausing/resuming the execution deadline.
// Implemented by the engine to let agent.result() pause the parent's
// timeout while waiting for a sub-agent.
type DeadlinePauser interface {
	PauseDeadline()
	ResumeDeadline()
}

// subAgentHandle tracks a running sub-agent goroutine.
type subAgentHandle struct {
	result string
	err    error
	done   chan struct{}
	cancel context.CancelFunc
}

// ExecCtxFunc returns the current execution context. Sub-agents derive their
// context from this so they inherit the parent's cancellation (e.g. user Stop).
type ExecCtxFunc func() context.Context

// RegisterAgent adds the agent namespace (agent.run, agent.result) to the runtime.
// execCtxFn returns the current execution context; sub-agents derive from it so
// they get cancelled when the parent agent is stopped.
// pauser is optional — if provided, agent.result() will pause/resume the deadline.
func RegisterAgent(vm *goja.Runtime, runner SubAgentRunner, execCtxFn ExecCtxFunc, pauser DeadlinePauser) {
	agentObj := vm.NewObject()

	var mu sync.Mutex
	handles := make(map[string]*subAgentHandle)

	// agent.run(task, providerName?) — spawn a sub-agent goroutine, returns handleId
	// Optional second argument selects a named provider for the sub-agent.
	agentObj.Set("run", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			Throw(vm, "agent.run requires a task string argument")
		}
		task := call.Arguments[0].String()

		var providerName string
		if len(call.Arguments) >= 2 && !goja.IsUndefined(call.Arguments[1]) && !goja.IsNull(call.Arguments[1]) {
			providerName = call.Arguments[1].String()
		}

		// Derive from the current execution context so sub-agents are cancelled
		// when the parent agent is stopped by the user.
		parentCtx := execCtxFn()
		subCtx, cancel := context.WithCancel(parentCtx)
		handle := &subAgentHandle{
			done:   make(chan struct{}),
			cancel: cancel,
		}
		id := uuid.New().String()[:8]

		mu.Lock()
		handles[id] = handle
		mu.Unlock()

		go func() {
			defer close(handle.done)
			var result string
			var err error
			if providerName != "" {
				result, err = runner.RunSubAgentWith(subCtx, task, providerName)
			} else {
				result, err = runner.RunSubAgent(subCtx, task)
			}
			handle.result = result
			handle.err = err
		}()

		return vm.ToValue(id)
	})

	// agent.kill(handleId) — immediately terminates a sub-agent.
	agentObj.Set("kill", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			Throw(vm, "agent.kill requires a handleId argument")
		}
		id := call.Arguments[0].String()

		mu.Lock()
		handle, ok := handles[id]
		if ok {
			handle.cancel() // terminate subagent!
		}
		mu.Unlock()

		if !ok {
			Throwf(vm, "unknown agent handle %q", id)
		}
		return goja.Undefined()
	})

	// agent.result(handleId) — block until sub-agent completes, returns response string.
	// Pauses the parent's execution deadline while waiting.
	agentObj.Set("result", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			Throw(vm, "agent.result requires a handleId argument")
		}
		id := call.Arguments[0].String()

		mu.Lock()
		handle, ok := handles[id]
		mu.Unlock()
		if !ok {
			Throwf(vm, "unknown agent handle %q", id)
		}

		// Pause parent deadline while waiting for sub-agent
		execCtx := execCtxFn()
		if pauser != nil {
			pauser.PauseDeadline()
			if ep, ok := pauser.(interface{ ExecContext() context.Context }); ok {
				execCtx = ep.ExecContext()
			}
		}

		// Block until done or execution timeout
		select {
		case <-execCtx.Done():
			// Resume to avoid leaking pauser state if panic is caught
			if pauser != nil {
				pauser.ResumeDeadline()
			}
			Throw(vm, "execution timeout or cancelled while waiting for sub-agent")
		case <-handle.done:
		}

		// Resume parent deadline
		if pauser != nil {
			pauser.ResumeDeadline()
		}

		mu.Lock()
		delete(handles, id)
		mu.Unlock()

		if handle.err != nil {
			Throwf(vm, "sub-agent error: %v", handle.err)
		}
		return vm.ToValue(handle.result)
	})

	// agent.status(handleId) — non-blocking check on sub-agent state.
	// Returns {done: bool, result?: string, error?: string}
	agentObj.Set("status", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			Throw(vm, "agent.status requires a handleId argument")
		}
		id := call.Arguments[0].String()

		mu.Lock()
		handle, ok := handles[id]
		mu.Unlock()
		if !ok {
			Throwf(vm, "unknown agent handle %q", id)
		}

		obj := vm.NewObject()
		select {
		case <-handle.done:
			obj.Set("done", true)
			if handle.err != nil {
				obj.Set("error", handle.err.Error())
			} else {
				obj.Set("result", handle.result)
			}
		default:
			obj.Set("done", false)
		}
		return obj
	})

	vm.Set(NameAgent, agentObj)
}
