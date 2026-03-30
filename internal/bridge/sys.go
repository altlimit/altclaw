package bridge

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"altclaw.ai/internal/buildinfo"
	"altclaw.ai/internal/config"
	"altclaw.ai/internal/executor"
	"github.com/dop251/goja"
)

// RegisterSys adds the sys namespace (sys.call, sys.spawn, sys.getOutput, sys.terminate)
// to the runtime, delegating to the given Executor.
// If exec is nil, sys is still registered but every method throws "no executor configured".
// uiHandler and isLocal control the command whitelist confirmation flow for local executors.
// ws is optional — when non-nil and ws.IgnoreRestricted is false, sys.call/spawn/popen
// perform a best-effort scan for hidden-path arguments (e.g. .env, ~/.ssh) and prompt
// the user for confirmation before executing.
func RegisterSys(vm *goja.Runtime, exec executor.Executor, store *config.Store, workspace string, ws *config.Workspace, ctx context.Context, uiHandler UIHandler, isLocal bool) {
	sys := vm.NewObject()

	if exec == nil {
		noExec := func(call goja.FunctionCall) goja.Value {
			Throw(vm, "no executor configured — sys.call and related APIs are unavailable")
			return goja.Undefined()
		}
		for _, name := range []string{"call", "spawn", "getOutput", "terminate", "setImage", "popen", "write", "readLine"} {
			sys.Set(name, noExec)
		}
		// sys.info still works without an executor — returns minimal info
		sys.Set("info", func(call goja.FunctionCall) goja.Value {
			info := map[string]any{
				"os":           map[string]any{"type": "unknown"},
				"resources":    map[string]any{},
				"runtimes":     map[string]any{},
				"capabilities": map[string]any{"executor": "none"},
				"paths":        map[string]any{"workspace": workspace},
			}
			obj := vm.ToValue(info)
			return obj
		})
		vm.Set(NameSys, sys)
		return
	}

	// guardLocal checks the command whitelist for local executors.
	// Returns nil if the command is allowed, or an error to propagate.
	// For empty whitelists, prompts confirmation via ui.confirm.
	guardLocal := func(cmd string, args []string) error {
		if !isLocal {
			return nil
		}
		localExec, ok := exec.(*executor.Local)
		if !ok {
			return nil
		}
		// Empty whitelist → prompt user for confirmation before every command
		if len(localExec.Whitelist) == 0 {
			if uiHandler == nil {
				return fmt.Errorf("command execution denied: empty whitelist and no active UI")
			}
			desc := cmd
			if len(args) > 0 {
				desc = cmd + " " + strings.Join(args, " ")
			}
			answer := uiHandler.Confirm("sys.exec", "Execute Command", desc, map[string]any{
				"command": cmd,
				"args":    strings.Join(args, " "),
			})
			approved := strings.TrimSpace(strings.ToLower(answer))
			if approved == "yes" || approved == "y" || approved == "approve" {
				return nil
			}
			return fmt.Errorf("user rejected: command execution was denied")
		}
		// Non-empty whitelist: check if command is allowed (includes * wildcard)
		if localExec.IsAllowed(cmd) {
			return nil
		}
		// Not in whitelist → hard error
		return fmt.Errorf("command %q not in whitelist — add it to Settings > Command Whitelist, or use * to allow all", cmd)
	}

	parseArgsEnv := func(arg goja.Value) ([]string, context.Context) {
		var args []string
		execCtx := ctx
		// Plain string shorthand: sys.call("curl", "https://...") → args=["https://..."]
		if arg.ExportType() != nil && arg.ExportType().Kind().String() == "string" {
			return []string{arg.String()}, execCtx
		}
		if argsObj := arg.ToObject(vm); argsObj != nil {
			if argsObj.ClassName() == "Array" {
				for _, key := range argsObj.Keys() {
					args = append(args, argsObj.Get(key).String())
				}
			} else {
				if a := argsObj.Get("args"); a != nil && !goja.IsUndefined(a) {
					if aObj := a.ToObject(vm); aObj != nil {
						for _, key := range aObj.Keys() {
							args = append(args, aObj.Get(key).String())
						}
					}
				}
				if e := argsObj.Get("env"); e != nil && !goja.IsUndefined(e) {
					envMap := make(map[string]string)
					if eObj := e.ToObject(vm); eObj != nil {
						for _, key := range eObj.Keys() {
							vStr := eObj.Get(key).String()
							envMap[key] = ExpandSecrets(ctx, store, vStr)
						}
					}
					if len(envMap) > 0 {
						execCtx = executor.WithEnv(ctx, envMap)
					}
				}
			}
		}
		return args, execCtx
	}

	// guardRestricted performs a best-effort check on command + args for hidden-path tokens.
	// Scans each token's path segments for leading dots (e.g. .env, .ssh, .git).
	guardRestricted := func(cmd string, args []string) {
		if !isLocal {
			return
		}
		if ws == nil || uiHandler == nil {
			return
		}
		if store != nil {
			if store.Settings().IgnoreRestricted() {
				return
			}
		} else if ws.IgnoreRestricted {
			return
		}
		tokens := append([]string{cmd}, args...)
		hasHidden := false
		for _, token := range tokens {
			// Normalize to forward slashes for segment splitting
			normalized := filepath.ToSlash(token)
			// Handle ~ expansion: ~/. paths
			if strings.HasPrefix(normalized, "~/") {
				normalized = strings.TrimPrefix(normalized, "~/")
			}
			for _, seg := range strings.Split(normalized, "/") {
				if len(seg) > 1 && seg[0] == '.' {
					hasHidden = true
					break
				}
			}
			if hasHidden {
				break
			}
		}
		if !hasHidden {
			return
		}
		full := cmd + " " + strings.Join(args, " ")
		answer := uiHandler.Ask(fmt.Sprintf(
			"sys command may access a restricted path: %q — allow? (yes/no)", full,
		))
		approved := strings.TrimSpace(strings.ToLower(answer))
		if approved != "yes" && approved != "y" && approved != "approve" {
			Throwf(vm, "user rejected: sys command with restricted path")
		}
	}

	sys.Set("call", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			Throw(vm, "sys.call requires a command argument")
		}
		cmd := call.Arguments[0].String()
		var args []string
		execCtx := ctx

		if len(call.Arguments) > 1 {
			args, execCtx = parseArgsEnv(call.Arguments[1])
		}

		if err := guardLocal(cmd, args); err != nil {
			Throwf(vm, "sys.call: %v", err)
		}
		guardRestricted(cmd, args)

		result, err := exec.Run(execCtx, cmd, args)
		if err != nil {
			Throwf(vm, "sys.call error: %v", err)
		}

		obj := vm.NewObject()
		obj.Set("stdout", result.Stdout)
		obj.Set("stderr", result.Stderr)
		obj.Set("exitCode", result.ExitCode)
		return obj
	})

	sys.Set("spawn", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			Throw(vm, "sys.spawn requires a command argument")
		}
		cmd := call.Arguments[0].String()
		var args []string
		execCtx := ctx

		if len(call.Arguments) > 1 {
			args, execCtx = parseArgsEnv(call.Arguments[1])
		}

		if err := guardLocal(cmd, args); err != nil {
			Throwf(vm, "sys.spawn: %v", err)
		}
		guardRestricted(cmd, args)

		handleID, err := exec.Spawn(execCtx, cmd, args)
		if err != nil {
			Throwf(vm, "sys.spawn error: %v", err)
		}
		return vm.ToValue(handleID)
	})

	sys.Set("getOutput", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			Throw(vm, "sys.getOutput requires a handleID argument")
		}
		output, err := exec.GetOutput(call.Arguments[0].String())
		if err != nil {
			Throwf(vm, "sys.getOutput error: %v", err)
		}
		return vm.ToValue(output)
	})

	sys.Set("terminate", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			Throw(vm, "sys.terminate requires a handleID argument")
		}
		if err := exec.Terminate(call.Arguments[0].String()); err != nil {
			Throwf(vm, "sys.terminate error: %v", err)
		}
		return goja.Undefined()
	})

	sys.Set("setImage", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			Throw(vm, "sys.setImage requires an image name argument")
		}
		image := call.Arguments[0].String()
		sessionID := executor.SessionFrom(ctx)

		var opts executor.ImageOpts
		if len(call.Arguments) > 1 {
			arg := call.Arguments[1]
			// String shorthand: sys.setImage("name", "mcp.Dockerfile")
			if str := arg.String(); arg.ExportType().Kind().String() == "string" {
				opts.Build = str
			} else {
				// Object: sys.setImage("name", { build: "...", volumes: [...] })
				obj := arg.ToObject(vm)
				if b := obj.Get("build"); b != nil && !goja.IsUndefined(b) {
					opts.Build = b.String()
				}
				if v := obj.Get("volumes"); v != nil && !goja.IsUndefined(v) {
					vObj := v.ToObject(vm)
					for _, key := range vObj.Keys() {
						vStr := vObj.Get(key).String()
						parts := strings.SplitN(vStr, ":", 2)
						if len(parts) != 2 {
							Throwf(vm, "Invalid volume mount format (expected source:target): %s", vStr)
						}
						src := parts[0]
						if src == "" || strings.ContainsAny(src, "/.\\\x00") {
							Throwf(vm, "Invalid volume source (bind mounts to host paths are forbidden): %s", src)
						}
						opts.Volumes = append(opts.Volumes, vStr)
					}
				}
			}
		}

		exec.SetImage(image, opts, sessionID)
		return goja.Undefined()
	})

	sys.Set("popen", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			Throw(vm, "sys.popen requires a command argument")
		}
		cmd := call.Arguments[0].String()
		var args []string
		execCtx := ctx

		if len(call.Arguments) > 1 {
			args, execCtx = parseArgsEnv(call.Arguments[1])
		}

		if err := guardLocal(cmd, args); err != nil {
			Throwf(vm, "sys.popen: %v", err)
		}
		guardRestricted(cmd, args)

		handleID, err := exec.Popen(execCtx, cmd, args)
		if err != nil {
			Throwf(vm, "sys.popen error: %v", err)
		}
		return vm.ToValue(handleID)
	})

	sys.Set("write", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 2 {
			Throw(vm, "sys.write requires handleID and data arguments")
		}
		handleID := call.Arguments[0].String()
		data := call.Arguments[1].String()
		if err := exec.WriteStdin(handleID, data); err != nil {
			Throwf(vm, "sys.write error: %v", err)
		}
		return goja.Undefined()
	})

	sys.Set("readLine", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			Throw(vm, "sys.readLine requires a handleID argument")
		}
		handleID := call.Arguments[0].String()
		timeoutMs := 30000 // default 30s
		if len(call.Arguments) > 1 {
			timeoutMs = int(call.Arguments[1].ToInteger())
		}
		line, err := exec.ReadLine(handleID, timeoutMs)
		if err != nil {
			Throwf(vm, "sys.readLine error: %v", err)
		}
		return vm.ToValue(line)
	})

	sys.Set("info", func(call goja.FunctionCall) goja.Value {
		info, err := exec.Info(ctx)
		if err != nil {
			Throwf(vm, "sys.info error: %v", err)
		}

		// Inject host engine info — the agent runs inside Goja (Go JS runtime),
		// not Node.js. This prevents confusion when node isn't on PATH.
		host := map[string]any{
			"platform": "AltClaw",
			"engine":   "goja",
			"mode":     "synchronous",
			"version":  buildinfo.Version,
		}
		info["host"] = host

		return vm.ToValue(info)
	})

	vm.Set("sys", sys)
}
