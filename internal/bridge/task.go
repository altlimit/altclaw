package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/dop251/goja"
	"github.com/google/uuid"
)

// TaskRunner is implemented by the Engine to let the task bridge spawn
// child engines without creating a circular import.
type TaskRunner interface {
	// RunTask executes a JS function body or module path in a new isolated
	// child VM. The jsonArgs string is a JSON-encoded array that will be
	// available as `arguments` inside an inline function body.
	// Returns a JSON-encoded result or an error.
	RunTask(ctx context.Context, code string, jsonArgs string) (string, error)
}

// taskHandle tracks one running goroutine.
type taskHandle struct {
	result string
	err    error
	done   chan struct{}
	cancel context.CancelFunc
}

// RegisterTask adds the task namespace to the runtime.
//
//	task.run(fn | path, ...args?)  → handleId  (non-blocking)
//	task.join(id)                  → value      (blocks, throws on error)
//	task.done(id)                  → null | {value, error}  (non-blocking poll)
//	task.all(...ids)               → [values]   (blocks until all done)
//	task.cancel(id)                → undefined  (interrupt child VM)
func RegisterTask(vm *goja.Runtime, runner TaskRunner, ctx context.Context) {
	taskObj := vm.NewObject()

	var mu sync.Mutex
	handles := make(map[string]*taskHandle)

	// Named queue semaphores for concurrency limiting.
	// key = queue name, value = buffered channel (capacity = limit).
	queueMu := &sync.Mutex{}
	queues := make(map[string]chan struct{})

	getQueue := func(name string, limit int) chan struct{} {
		queueMu.Lock()
		defer queueMu.Unlock()
		ch, ok := queues[name]
		if !ok || cap(ch) != limit {
			ch = make(chan struct{}, limit)
			queues[name] = ch
		}
		return ch
	}

	// spawn starts a goroutine for the given code/path and returns a handle ID.
	// If queueCh is non-nil, the goroutine acquires a slot before running.
	spawnHandle := func(code, jsonArgs string, ctx context.Context, queueCh chan struct{}) string {
		subCtx, cancel := context.WithCancel(ctx)
		h := &taskHandle{
			done:   make(chan struct{}),
			cancel: cancel,
		}
		id := uuid.New().String()[:8]
		mu.Lock()
		handles[id] = h
		mu.Unlock()

		go func() {
			defer close(h.done)
			// Queue concurrency: acquire slot before running, release after
			if queueCh != nil {
				select {
				case queueCh <- struct{}{}:
					defer func() { <-queueCh }()
				case <-subCtx.Done():
					h.err = subCtx.Err()
					return
				}
			}
			result, err := runner.RunTask(subCtx, code, jsonArgs)
			h.result = result
			h.err = err
		}()
		return id
	}

	// task.run(fn | path, ...args?, opts?) — spawn a parallel child VM.
	//
	// If the first argument is a function, it is serialized via .toString()
	// and its body executed in a new VM. Any extra arguments are JSON-encoded
	// and available as the `args` array inside the function body.
	// If the first argument is a string and ends with .js, it is treated
	// as a workspace-relative module path.
	//
	// Optional last argument: {queue: "name", limit: N} for concurrency control.
	// Tasks on the same queue run at most `limit` concurrently (default 1).
	taskObj.Set("run", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			Throw(vm, "task.run requires a function or path argument")
		}
		arg0 := call.Arguments[0]

		// Check if last argument is a queue opts object
		var queueCh chan struct{}
		args := call.Arguments[1:]
		if len(args) > 0 {
			lastArg := args[len(args)-1]
			if lastObj := lastArg.ToObject(vm); lastObj != nil {
				qVal := lastObj.Get("queue")
				if qVal != nil && !goja.IsUndefined(qVal) && !goja.IsNull(qVal) {
					queueName := qVal.String()
					limit := 1
					if lVal := lastObj.Get("limit"); lVal != nil && !goja.IsUndefined(lVal) {
						limit = int(lVal.ToInteger())
						if limit < 1 {
							limit = 1
						}
					}
					queueCh = getQueue(queueName, limit)
					args = args[:len(args)-1] // strip opts from args
				}
			}
		}

		// Serialize extra arguments as JSON
		var argSlice []interface{}
		for _, a := range args {
			argSlice = append(argSlice, a.Export())
		}
		jsonArgs := "[]"
		if len(argSlice) > 0 {
			if b, err := json.Marshal(argSlice); err == nil {
				jsonArgs = string(b)
			}
		}

		var code string
		if fn, ok := goja.AssertFunction(arg0); ok {
			_ = fn // we use .String() instead to get source
			src := arg0.String()
			// Use a private __taskResult sentinel (not output()) to capture the return value.
			// This means output() calls inside the function are no-ops in a task context,
			// and only the actual return value is returned to the caller.
			code = fmt.Sprintf(
				`(function(){var args=JSON.parse(%s);var __r=(%s).apply(null,args);__taskResult(__r===undefined?null:__r);})()`,
				jsonQuote(jsonArgs), src)
		} else {
			path := strings.TrimSpace(arg0.String())
			if strings.HasSuffix(path, ".js") {
				code = path // treated as module path by RunTask
			} else {
				code = path // inline code
			}
		}

		id := spawnHandle(code, jsonArgs, ctx, queueCh)
		return vm.ToValue(id)
	})

	// task.join(id) — block until task completes, return its value. Throws on error.
	taskObj.Set("join", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			Throw(vm, "task.join requires a handleId argument")
		}
		id := call.Arguments[0].String()

		mu.Lock()
		h, ok := handles[id]
		mu.Unlock()
		if !ok {
			Throwf(vm, "task.join: unknown handle %q", id)
		}

		select {
		case <-ctx.Done():
			Throw(vm, "task.join: execution cancelled")
		case <-h.done:
		}

		mu.Lock()
		delete(handles, id)
		mu.Unlock()

		if h.err != nil {
			logErr(vm, "task.join", h.err)
		}
		// Parse JSON result back into a JS value
		return jsonToValue(vm, h.result)
	})

	// task.done(id) — non-blocking poll.
	// Returns null if still running, or {value, error} when settled.
	taskObj.Set("done", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			Throw(vm, "task.done requires a handleId argument")
		}
		id := call.Arguments[0].String()

		mu.Lock()
		h, ok := handles[id]
		mu.Unlock()
		if !ok {
			Throwf(vm, "task.done: unknown handle %q", id)
		}

		select {
		case <-h.done:
			// settled — remove and return result
			mu.Lock()
			delete(handles, id)
			mu.Unlock()

			obj := vm.NewObject()
			if h.err != nil {
				obj.Set("value", goja.Null())
				obj.Set("error", h.err.Error())
			} else {
				obj.Set("value", jsonToValue(vm, h.result))
				obj.Set("error", goja.Null())
			}
			return obj
		default:
			// still running
			return goja.Null()
		}
	})

	// task.all(...ids) — block until ALL tasks finish, return array of values.
	// Throws if any task errored (after waiting for all).
	taskObj.Set("all", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			Throw(vm, "task.all requires at least one handleId")
		}

		type item struct {
			id string
			h  *taskHandle
		}
		items := make([]item, 0, len(call.Arguments))
		for _, arg := range call.Arguments {
			id := arg.String()
			mu.Lock()
			h, ok := handles[id]
			mu.Unlock()
			if !ok {
				Throwf(vm, "task.all: unknown handle %q", id)
			}
			items = append(items, item{id, h})
		}

		// Wait for each in order (total wall time = max of all tasks)
		results := make([]goja.Value, len(items))
		var firstErr error
		for i, it := range items {
			select {
			case <-ctx.Done():
				Throw(vm, "task.all: execution cancelled")
			case <-it.h.done:
			}
			mu.Lock()
			delete(handles, it.id)
			mu.Unlock()

			if it.h.err != nil && firstErr == nil {
				firstErr = it.h.err
			}
			results[i] = jsonToValue(vm, it.h.result)
		}
		if firstErr != nil {
			logErr(vm, "task.all", firstErr)
		}

		arr := make([]interface{}, len(results))
		for i, v := range results {
			arr[i] = v
		}
		return vm.ToValue(arr)
	})

	// task.cancel(id) — interrupt a running task.
	taskObj.Set("cancel", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			Throw(vm, "task.cancel requires a handleId argument")
		}
		id := call.Arguments[0].String()

		mu.Lock()
		h, ok := handles[id]
		mu.Unlock()
		if !ok {
			Throwf(vm, "task.cancel: unknown handle %q", id)
		}
		h.cancel()
		return goja.Undefined()
	})

	vm.Set(NameTask, taskObj)
}

// jsonToValue parses a JSON string back into a goja.Value.
// Returns goja.Undefined() if the string is empty or not valid JSON.
func jsonToValue(vm *goja.Runtime, s string) goja.Value {
	if s == "" {
		return goja.Undefined()
	}
	v, err := vm.RunString("(" + s + ")")
	if err != nil {
		// Fall back to the raw string
		return vm.ToValue(s)
	}
	return v
}

// jsonQuote returns s as a JSON string literal (with double quotes).
func jsonQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
