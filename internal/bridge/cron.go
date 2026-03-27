package bridge

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"altclaw.ai/internal/cron"
	"github.com/dop251/goja"
)

// RegisterCron adds the cron namespace (cron.add, cron.rm, cron.list) to the runtime.
// workspace is the filesystem path for resolving script file paths.
// chatIDFn returns the current chat ID (captured at registration time for context).
// ctxFn is an optional context factory for broadcast-enriched contexts.
func RegisterCron(vm *goja.Runtime, mgr *cron.Manager, workspace string, chatIDFn func() int64, ctxFn ...func() context.Context) {
	getCtx := defaultCtxFn(ctxFn)
	cronObj := vm.NewObject()

	// cron.add(schedule, content, opts?) → string (job ID)
	// schedule: cron expression, duration, or datetime
	// opts: { script: true } — content is a .js file path or inline CommonJS module
	cronObj.Set("add", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 2 {
			Throw(vm, "cron.add requires (schedule, content) arguments")
		}
		schedule := call.Arguments[0].String()
		instructions := call.Arguments[1].String()

		isScript := false
		if len(call.Arguments) > 2 && !goja.IsUndefined(call.Arguments[2]) && !goja.IsNull(call.Arguments[2]) {
			opts := call.Arguments[2].ToObject(vm)
			if v := opts.Get("script"); v != nil && !goja.IsUndefined(v) {
				isScript = v.ToBoolean()
			}
		}

		if isScript {
			trimmed := strings.TrimSpace(instructions)
			// Check if it's a workspace file path
			absPath := filepath.Clean(filepath.Join(workspace, trimmed))
			if _, err := os.Stat(absPath); err == nil {
				// File exists — store the workspace-relative path
				// Normalize: strip leading ./ for consistency
				instructions = strings.TrimPrefix(trimmed, "./")
			} else {
				// Not a file — validate as inline CommonJS module
				if !strings.Contains(instructions, "module.exports") {
					Throw(vm, "cron.add: script must use module.exports = function() { ... } pattern")
				}
				_, err := goja.Compile("cron-script", "(function(module,exports){\n"+instructions+"\n})", false)
				if err != nil {
					Throwf(vm, "cron.add: invalid JS — %v", err)
				}
			}
		}

		// Capture the chat context at the time the job is created
		chatID := int64(0)
		if chatIDFn != nil {
			chatID = chatIDFn()
		}

		id, err := mgr.Add(getCtx(), chatID, schedule, instructions, isScript)
		if err != nil {
			logErr(vm, "cron.add", err)
		}
		return vm.ToValue(cron.IDStr(id))
	})

	// cron.rm(id) — remove a scheduled job
	cronObj.Set("rm", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			Throw(vm, "cron.rm requires a job ID argument")
		}
		idStr := call.Arguments[0].String()
		id, err := cron.ParseID(idStr)
		if err != nil {
			Throw(vm, "cron.rm: invalid ID: " + idStr)
		}
		if err := mgr.Remove(getCtx(), id); err != nil {
			logErr(vm, "cron.rm", err)
		}
		return goja.Undefined()
	})

	// cron.list() → [{id, chat_id, schedule, instructions, one_shot, script, created_at, next_run}]
	cronObj.Set("list", func(call goja.FunctionCall) goja.Value {
		jobs := mgr.List()
		result := make([]map[string]interface{}, len(jobs))
		for i, j := range jobs {
			result[i] = map[string]interface{}{
				"id":           cron.IDStr(j.ID),
				"chat_id":      j.ChatID,
				"schedule":     j.Schedule,
				"instructions": j.Instructions,
				"one_shot":     j.OneShot,
				"script":       j.Script,
				"created_at":   j.CreatedAt,
				"next_run":     j.NextRun,
			}
		}
		return vm.ToValue(result)
	})

	vm.Set(NameCron, cronObj)
}
