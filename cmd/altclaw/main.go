package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	osexec "os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"altclaw.ai/internal/agent"
	"altclaw.ai/internal/bridge"
	"altclaw.ai/internal/buildinfo"
	"altclaw.ai/internal/config"
	"altclaw.ai/internal/cron"
	"altclaw.ai/internal/engine"
	"altclaw.ai/internal/executor"
	"altclaw.ai/internal/mcp"
	"altclaw.ai/internal/netx"
	"altclaw.ai/internal/provider"
	"altclaw.ai/internal/serverjs"
	"altclaw.ai/tui"
	"altclaw.ai/web"
	tea "charm.land/bubbletea/v2"
	"cloud.google.com/go/datastore"
	"github.com/spf13/cobra"
)

var (
	flagConfig  string
	flagAddr    string
	flagTUI     bool
	flagVerbose bool
	flagMCP     bool
)

// guiReadyFn is set by startGUI (in window_gui.go) before calling startWeb.
// When non-nil, startWeb calls it with the login URL once the server is ready.
var guiReadyFn func(loginURL string)

// guiShutdownFn is set by startWeb when running in GUI mode.
// startGUI calls it after the Wails window closes to trigger graceful cleanup
// (executor cleanup, store close, etc.).
var guiShutdownFn func()

// logBuf captures recent slog records in-memory for agent inspection via the log bridge.
var logBuf = bridge.NewLogBuffer(200)

func main() {
	rootCmd := &cobra.Command{
		Use:   "altclaw [workspace]",
		Short: "🐾 Altclaw — AI Agent Orchestrator",
		Long: `Altclaw is an open-source AI agent orchestrator.
It uses Goja as an embedded JavaScript engine with bridge APIs
for file system, system execution, HTTP, and user interaction.

Usage:
  altclaw                Start workspace picker (interactive)
  altclaw .              Use current directory as workspace (web UI)
  altclaw /path/to/dir   Use specified directory as workspace (web UI)
  altclaw --tui .        Use current directory in terminal UI mode`,
		Args: cobra.MaximumNArgs(1),
		RunE: run,
	}

	rootCmd.PersistentFlags().StringVarP(&flagConfig, "config", "c", "", "Config directory (default: ~/.altclaw)")
	rootCmd.PersistentFlags().StringVarP(&flagAddr, "addr", "a", "", "Address for the web UI (e.g. :8080). Auto-selects if not specified.")
	rootCmd.PersistentFlags().BoolVarP(&flagTUI, "tui", "t", false, "Start terminal UI instead of web UI")
	rootCmd.PersistentFlags().BoolVarP(&flagVerbose, "verbose", "v", false, "Plain stdout logging (no status TUI)")
	rootCmd.PersistentFlags().BoolVar(&flagMCP, "mcp", false, "Run as MCP server (stdio transport)")

	// Run subcommand
	runCmd := &cobra.Command{
		Use:          "run [script.js] [workspace]",
		Short:        "Run a JavaScript file through the Altclaw engine",
		Args:         cobra.RangeArgs(1, 2),
		SilenceUsage: true,
		RunE:         runScript,
	}

	rootCmd.AddCommand(runCmd)
	rootCmd.Version = buildinfo.Version

	// Allow double-click on Windows — disable cobra's mousetrap
	cobra.MousetrapHelpText = ""

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

// initStore sets up the config dir and opens the dsorm store.
func initStore() (*config.Store, error) {
	if flagConfig != "" {
		absDir, err := filepath.Abs(flagConfig)
		if err == nil {
			config.SetConfigDir(absDir)
		} else {
			config.SetConfigDir(flagConfig)
		}
	}

	store, err := config.NewStore(config.ConfigDir())
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}

	if _, err := store.GetConfig(); err != nil {
		return nil, fmt.Errorf("get config: %w", err)
	}

	return store, nil
}

// resolveWorkspace turns a raw path into an absolute, symlink-resolved workspace path.
func resolveWorkspace(raw string) string {
	ws := raw
	if ws == "" {
		ws, _ = os.Getwd()
	}
	if ws != "" && !filepath.IsAbs(ws) {
		if abs, err := filepath.Abs(ws); err == nil {
			ws = abs
		}
	}
	// Resolve symlinks to get a canonical path
	if ws != "" {
		if resolved, err := filepath.EvalSymlinks(ws); err == nil {
			ws = resolved
		}
	}
	return ws
}

// buildProviderFromConfig creates a provider from a dsorm Provider model.
func buildProviderFromConfig(p *config.Provider) (provider.Provider, error) {
	return provider.Build(p.ProviderType, p.APIKey, p.Model, p.BaseURL, p.Host)
}

// executorEnv returns process.env entries for the active executor type.
// execType should be the resolved type ("docker", "podman", "local"), not the raw config value ("auto").
func executorEnv(execType, image string) map[string]string {
	env := map[string]string{"EXECUTOR": execType}
	if (execType == "docker" || execType == "podman") && image != "" {
		env["EXECUTOR_IMAGE"] = image
	}
	return env
}

// buildExecutor creates the executor and returns (executor, resolvedType, error).
// resolvedType is the actual type used (e.g. "docker" when config says "auto").
func buildExecutor(appCfg *config.AppConfig, workspace string) (executor.Executor, string, error) {
	execType := appCfg.Executor

	// Auto-detect: prefer Docker, then Podman, otherwise no executor
	if execType == "" || execType == "auto" {
		if _, err := osexec.LookPath("docker"); err == nil {
			execType = "docker"
		} else if _, err := osexec.LookPath("podman"); err == nil {
			execType = "podman"
		} else {
			execType = "none"
		}
	}

	if execType == "none" {
		slog.Warn("no executor configured — sys.call and related APIs will fail")
		return nil, execType, nil
	}

	if execType == "local" {
		slog.Warn("SECURITY WARNING: local executor enabled")
		if len(appCfg.LocalWhitelist) == 0 {
			slog.Warn("local executor has NO command whitelist — full RCE access")
		} else {
			slog.Warn("local executor with restricted whitelist", "commands", appCfg.LocalWhitelist)
		}
		return executor.NewLocal(workspace, appCfg.LocalWhitelist), execType, nil
	}

	if execType == "docker" || execType == "podman" {
		e, err := executor.NewDocker(execType, appCfg.DockerImage, workspace, "/workspace")
		return e, execType, err
	}

	return nil, execType, fmt.Errorf("unknown executor %q (supported: local, docker, podman)", execType)
}

// allowExecutorPort whitelists a port in the Docker proxy relay so that
// app containers can reach the altclaw server on the host at that port.
// No-op for non-Docker executors.
func allowExecutorPort(exec executor.Executor, port string) {
	if d, ok := exec.(*executor.Docker); ok {
		d.AllowPort(port)
	}
}

// buildAgent creates an agent from the store (used by both TUI and web mode).
// cronMgr is optional — if non-nil, the cron bridge is registered on the engine.
func buildAgent(store *config.Store, ws *config.Workspace, providerName string, uiHandler bridge.UIHandler, exec executor.Executor, execType string, cronMgr *cron.Manager) (*agent.Agent, *engine.Engine, error) {
	return buildAgentWithLogBuf(store, ws, providerName, uiHandler, exec, execType, cronMgr, logBuf)
}

func buildAgentWithLogBuf(store *config.Store, ws *config.Workspace, providerName string, uiHandler bridge.UIHandler, exec executor.Executor, execType string, cronMgr *cron.Manager, lb *bridge.LogBuffer) (*agent.Agent, *engine.Engine, error) {
	appCfg := store.Config()
	// Apply provider-level concurrency limit (0 = unlimited)
	provider.SetConcurrency(appCfg.ProviderConcurrency)

	var provCfg *config.Provider
	var err error
	if providerName != "" {
		provCfg, err = store.GetProvider(providerName)
	}
	if provCfg == nil || err != nil {
		provCfg, err = store.GetFirstProvider()
	}
	if err != nil {
		return nil, nil, fmt.Errorf("no providers configured. Run with --web to configure")
	}

	prov, err := buildProviderFromConfig(provCfg)
	if err != nil {
		return nil, nil, fmt.Errorf("build provider: %w", err)
	}

	// Rate limit per endpoint: provider override → workspace default → user default → built-in default (10 RPM)
	settings := store.Settings()
	effectiveRPM := settings.RateLimit()
	if provCfg.RateLimit > 0 {
		effectiveRPM = provCfg.RateLimit
	}
	providerEndpoint := provCfg.BaseURL
	if providerEndpoint == "" {
		providerEndpoint = provCfg.Host
	}
	if providerEndpoint == "" {
		// Gemini or any provider without explicit base/host — use provider type as key
		providerEndpoint = provCfg.ProviderType
	}
	provider.SetProviderRPM(providerEndpoint, effectiveRPM)

	wsModDir, userModDir := store.ModuleDirs(ws.ID)
	eng := engine.New(ws, exec, uiHandler, "", store, lb).
		WithModuleDirs(wsModDir, userModDir)
	envMap := executorEnv(execType, appCfg.DockerImage)
	eng.SetProcess("agent", "", "", envMap)
	timeout := ws.TimeoutFor("agent", appCfg)
	ag := agent.New(prov, eng, ws.Path, timeout)
	if mi := settings.MaxIterations(); mi > 0 {
		ag.MaxIter = int(mi)
	}
	ag.Store = store
	ag.Ws = ws
	ag.ProviderCfg = provCfg
	ag.SetProviderName(provCfg.Name)

	// Executor info
	if execType == "local" {
		ag.ExecutorInfo = "local (direct system access)"
	} else if execType == "docker" || execType == "podman" {
		ag.ExecutorInfo = execType + ":" + appCfg.DockerImage
	} else {
		ag.ExecutorInfo = execType
	}
	ag.Exec = exec
	// Default OnLog just prints to stdout
	ag.OnLog = func(msg string) {
		fmt.Println(msg)
	}
	// Wire headlessUI.LogFunc to ag.OnLog
	if h, ok := uiHandler.(*headlessUI); ok {
		h.LogFunc = func(msg string) {
			if ag.OnLog != nil {
				ag.OnLog(msg)
			}
		}
	}
	ag.NewEngine = func(sessionID string) *engine.Engine {
		e := engine.New(ws, exec, uiHandler, sessionID, store, lb).
			WithModuleDirs(wsModDir, userModDir)
		e.SetProcess("agent", "", "", envMap)
		// Inherit the broadcast callback (set in web mode)
		e.OnBroadcast = eng.OnBroadcast
		return e
	}

	// Sub-agent provider factory
	ag.NewProvider = func(providerName, model string) provider.Provider {
		name := providerName
		if name == "" {
			name = appCfg.SubAgentProvider
		}
		if name == "" {
			name = provCfg.Name // fallback to current provider
		}
		pc, err := store.GetProvider(name)
		if err != nil {
			return prov // fallback to current
		}
		if model != "" {
			pc.Model = model
		}
		p, err := buildProviderFromConfig(pc)
		if err != nil {
			return prov // fallback
		}
		return p
	}

	// Inject specialist provider summary into system prompt
	ag.ProvidersSummary = store.ProviderSummary()

	// Provider image resolver for per-provider Docker images
	ag.ProviderImage = func(providerName string) string {
		pc, err := store.GetProvider(providerName)
		if err != nil {
			return ""
		}
		return pc.DockerImage
	}

	eng.SetAgentRunner(ag)

	// Register cron bridge if a manager was provided
	eng.WithCronManager(cronMgr, func() int64 {
		return ag.ChatID
	})

	return ag, eng, nil
}

func run(cmd *cobra.Command, args []string) error {
	store, err := initStore()
	if err != nil {
		return err
	}
	defer store.Close()

	var workspace string
	tuiMode := flagTUI

	if len(args) > 0 {
		// Positional arg: use as workspace path directly
		workspace = resolveWorkspace(args[0])
	}

	// Default: web mode address
	addr := flagAddr
	if addr == "" {
		addr = "127.0.0.1:0" // auto-select available port, loopback only
	}

	// Try GUI mode first (auto-starts if built with -tags gui).
	// startGUI handles its own folder picker when workspace is empty.
	if err := startGUI(store, workspace, addr); err != nil {
		slog.Info("GUI not available, starting web server", "reason", err)
	} else {
		return nil
	}

	// No workspace and no GUI: show the TUI picker
	if workspace == "" {
		var picked string
		picked, tuiMode, err = pickWorkspaceFolder(store)
		if err != nil {
			return fmt.Errorf("no workspace selected: %w", err)
		}
		workspace = resolveWorkspace(picked)
	}

	// MCP mode: stdio proxy
	if flagMCP {
		return startMCP(store, workspace)
	}

	// If TUI mode (from --tui flag or picker checkbox)
	if tuiMode {
		if !store.IsConfigured() {
			return fmt.Errorf("not configured. Run altclaw first to configure providers")
		}
		return startTUI(store, workspace)
	}

	return startWeb(store, workspace, addr)
}

type webUIHandler struct {
	agFunc func() *agent.Agent
	server *web.Server
}

func (w *webUIHandler) Log(msg string) {
	if w.agFunc != nil {
		if ag := w.agFunc(); ag != nil && ag.OnLog != nil {
			ag.OnLog(msg)
			return
		}
	}
	fmt.Println(msg)
}

func (w *webUIHandler) Ask(question string) string {
	if w.agFunc != nil && w.server != nil {
		if ag := w.agFunc(); ag != nil {
			return w.server.AskUser(ag.ChatID, question)
		}
	}
	return ""
}

func (w *webUIHandler) Confirm(action, label, summary string, params map[string]any) string {
	if w.agFunc != nil && w.server != nil {
		if ag := w.agFunc(); ag != nil {
			return w.server.ConfirmUser(ag.ChatID, action, label, summary, params)
		}
	}
	return "no"
}

func startWeb(store *config.Store, workspace, addr string) error {
	var exec executor.Executor
	var ag *agent.Agent
	var activeExecType, activeDockerImage string // resolved executor info for closures

	ws, err := store.GetWorkspace(workspace)
	if err != nil {
		return err
	}

	// Apply resolved settings (logging, SSRF whitelist, etc.)
	settings := store.Settings()
	settings.Init(logBuf)

	// Broadcast function for cron output — wired to web server hub after creation
	var cronBroadcast func(chatID int64, msg string)

	// Broadcast function for SSE panel events — wired to web server hub after creation
	var broadcastPanel func([]byte)

	// Create a single cron manager for this workspace (never duplicated per chat session)
	var cronMgr *cron.Manager
	var cronErr error
	cronMgr, cronErr = cron.New(store, ws.ID, func(jobID, chatID int64, instructions string, isScript bool) {
		if isScript {
			slog.Info("cron script executing", "job_id", jobID, "chat_id", chatID, "instructions", instructions)
			go func() {
				// Mutable chatID — may be updated by lazy chat creation
				activeChatID := chatID

				scriptEng := engine.New(ws, exec, &headlessUI{
					LogFunc: func(msg string) {
						slog.Info("cron", "output", msg)
						if activeChatID > 0 && cronBroadcast != nil {
							cronBroadcast(activeChatID, "⏰ "+msg)
						}
					},
				}, "", store, logBuf).WithModuleDirs(store.ModuleDirs(ws.ID))
				scriptEng.SetProcess("cron", "", "", executorEnv(activeExecType, activeDockerImage))
				scriptEng.WithCronManager(cronMgr, func() int64 { return activeChatID })
				// Wire broadcast for bridge mutations in cron scripts
				if broadcastPanel != nil {
					scriptEng.OnBroadcast = broadcastPanel
				}
				// Wire agent bridge with lazy chat creation wrapper
				if ag != nil {
					scriptEng.SetAgentRunner(&cronSubAgentRunner{
						agent:   ag,
						store:   store,
						wsID:    ws.ID,
						cronMgr: cronMgr,
						jobID:   jobID,
						chatID:  &activeChatID,
						broadcast: func(msg string) {
							if cronBroadcast != nil {
								cronBroadcast(activeChatID, msg)
							}
						},
					})
				}
				ctx, cancel := context.WithTimeout(context.Background(), ws.TimeoutFor("cron"))
				result := scriptEng.RunModule(ctx, instructions)
				cancel()
				scriptEng.Cleanup()
				if result.Error != nil {
					slog.Error("cron script error", "err", result.Error)
					if activeChatID > 0 && cronBroadcast != nil {
						cronBroadcast(activeChatID, "⏰ Error: "+result.Error.Error())
					}
				} else if result.Value != "" {
					slog.Info("cron script result", "value", result.Value)
					if activeChatID > 0 && cronBroadcast != nil {
						cronBroadcast(activeChatID, "⏰ "+result.Value)
					}
				}
			}()
		} else {
			slog.Info("cron task", "chat_id", chatID, "instructions", instructions)
			go func() {
				if ag != nil {
					_, err := ag.Send(context.Background(), "[Scheduled Task] "+instructions)
					if err != nil {
						slog.Error("cron task error", "err", err)
					}
				}
			}()
		}
	})
	if cronErr != nil {
		slog.Warn("cron setup failed", "error", cronErr)
		cronMgr = nil
	}

	// Only build agent if configured
	var eng *engine.Engine
	var srv *web.Server
	var initialHandler *webUIHandler

	if store.IsConfigured() {
		appCfg := store.Config()
		var resolvedExecType string
		exec, resolvedExecType, err = buildExecutor(appCfg, workspace)
		if err != nil {
			return err
		}
		activeExecType = resolvedExecType
		activeDockerImage = appCfg.DockerImage

		initialHandler = &webUIHandler{}
		ag, eng, err = buildAgent(store, ws, "", initialHandler, exec, resolvedExecType, cronMgr)
		initialHandler.agFunc = func() *agent.Agent { return ag }
		if err != nil {
			exec.Cleanup()
			return err
		}
		// Use late-bound broadcastPanel so it's wired after srv creation
		eng.OnBroadcast = func(data []byte) {
			if broadcastPanel != nil {
				broadcastPanel(data)
			}
		}
	}

	// Set workspace on the agent for system prompt hints and namespacing
	if ag != nil {
		ag.Ws = ws
	}

	// Track current executor for cleanup
	currentExec := exec

	srv = web.NewServer(ag, store, ws, cronMgr, logBuf, func(providerName string) (*agent.Agent, error) {
		if !store.IsConfigured() {
			return nil, fmt.Errorf("no providers configured")
		}

		// Reuse the shared executor — creating a new one per chat would
		// destroy the executor that cron and serverjs are still using.
		// Only bootstrap a new executor when starting from unconfigured state.
		if currentExec == nil {
			appCfg := store.Config()
			newExec, newExecType, execErr := buildExecutor(appCfg, workspace)
			if execErr != nil {
				return nil, execErr
			}
			currentExec = newExec
			exec = newExec
			activeExecType = newExecType
			activeDockerImage = appCfg.DockerImage
			srv.Exec = newExec
			srv.ExecType = newExecType
		}

		handler := &webUIHandler{}
		newAg, newEng, err := buildAgent(store, ws, providerName, handler, currentExec, activeExecType, cronMgr)
		handler.agFunc = func() *agent.Agent { return newAg }
		handler.server = srv
		if err != nil {
			return nil, err
		}
		// Wire SSE broadcast for bridge mutations
		if broadcastPanel != nil {
			newEng.OnBroadcast = broadcastPanel
		}
		// Wire confirm context for ui.confirm
		newEng.SetConfirmContext(srv.NewConfirmContext())
		newAg.Ws = ws
		return newAg, nil
	})

	if initialHandler != nil {
		initialHandler.server = srv
	}

	// Share the initial executor with the server for RunScript
	srv.Exec = exec
	srv.ExecType = activeExecType

	// In GUI (Wails) mode, skip session auth for localhost requests.
	// The webview is a trusted local context and should never be kicked out.
	if guiReadyFn != nil {
		srv.GUIMode = true
	}

	// Wire confirm context on the initial engine
	if eng != nil {
		eng.SetConfirmContext(srv.NewConfirmContext())
	}

	// Wire cron output to the web server's SSE hub
	cronBroadcast = srv.BroadcastLog

	// Wire SSE broadcast for bridge-level events
	broadcastPanel = srv.BroadcastPanel

	// Set up server-side JS handler for .server.js files in public dirs
	if ws.PublicDir != "" {
		pubDir := ws.PublicDir
		if !filepath.IsAbs(pubDir) {
			pubDir = filepath.Join(workspace, pubDir)
		}
		pubDir = filepath.Clean(pubDir)

		// Guard: ensure PublicDir cannot escape the workspace
		if filepath.IsAbs(pubDir) {
			rel, relErr := filepath.Rel(workspace, pubDir)
			if relErr != nil || strings.HasPrefix(rel, "..") {
				slog.Error("PublicDir escapes workspace, ignoring", "pubDir", pubDir)
				pubDir = ""
			}
		}

		if pubDir != "" {
			sjsHandler := serverjs.NewHandler(store, workspace, pubDir, exec, cronMgr, logBuf, buildinfo.Version, ws)
			sjsHandler.ExecType = activeExecType
			sjsHandler.OnBroadcast = srv.BroadcastPanel
			if ag != nil {
				sjsHandler.AgentRunner = ag
			}
			srv.SetServerJS(sjsHandler)
		}
	}

	// Wire MCP server
	mcpSrv := mcp.NewServer(ws, store, exec, logBuf)
	srv.SetMCP(mcpSrv)

	// Verbose mode: plain stdout logging, no TUI
	if flagVerbose {
		slog.Info("starting web server", "addr", addr, "workspace", workspace)

		// Graceful cleanup: executor + store + cron
		cleanup := func() {
			if cronMgr != nil {
				cronMgr.Stop()
			}
			if currentExec != nil {
				currentExec.Cleanup()
			}
			store.Close()
		}

		// Expose cleanup for GUI shutdown (window close)
		guiShutdownFn = func() {
			slog.Info("GUI shutdown: cleaning up")
			cleanup()
		}

		// Signal handling for graceful shutdown
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
		go func() {
			<-sigCh
			slog.Info("shutting down")
			cleanup()
			os.Exit(0)
		}()

		return srv.Start(addr, func(actualAddr string) {
			_, port, _ := net.SplitHostPort(actualAddr)
			os.Setenv("PORT", port)
			netx.AllowLoopbackPort(port)
			allowExecutorPort(currentExec, port)
			savePort(store, ws, actualAddr)
			loginURL := fmt.Sprintf("http://localhost%s/auth/auto-login?p=%s", actualAddr, srv.Password())
			slog.Info("server ready", "url", loginURL, "password", srv.Password())
			if guiReadyFn != nil {
				guiReadyFn(loginURL)
			} else {
				openBrowser(loginURL)
			}
		})
	}

	// Default: TUI status screen
	sm := &statusModel{}
	p := tea.NewProgram(sm)
	sm.program = p

	// Redirect slog to the TUI log panel while keeping logBuf fed
	tuiHandler := slog.NewTextHandler(
		&tuiLogWriter{model: sm}, &slog.HandlerOptions{Level: slog.LevelInfo},
	)
	slog.SetDefault(slog.New(config.NewMultiHandler(tuiHandler, logBuf)))

	// Graceful cleanup for non-verbose mode
	cleanupOnce := sync.OnceFunc(func() {
		if cronMgr != nil {
			cronMgr.Stop()
		}
		if currentExec != nil {
			currentExec.Cleanup()
		}
		store.Close()
	})

	// Expose cleanup for GUI shutdown (window close)
	guiShutdownFn = func() {
		slog.Info("GUI shutdown: cleaning up")
		cleanupOnce()
	}

	// Signal handling for graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		slog.Info("shutting down")
		cleanupOnce()
		p.Kill()
	}()

	// Start server in background
	go func() {
		err := srv.Start(addr, func(actualAddr string) {
			_, port, _ := net.SplitHostPort(actualAddr)
			os.Setenv("PORT", port)
			netx.AllowLoopbackPort(port)
			allowExecutorPort(currentExec, port)
			savePort(store, ws, actualAddr)
			loginURL := fmt.Sprintf("http://localhost%s/auth/auto-login?p=%s", actualAddr, srv.Password())
			if guiReadyFn != nil {
				guiReadyFn(loginURL)
			} else {
				openBrowser(loginURL)
			}
			p.Send(serverReady{
				addr:      actualAddr,
				password:  srv.Password(),
				workspace: workspace,
			})
		})
		if err != nil {
			sm.sendLog("Server error: " + err.Error())
		}
	}()

	_, err = p.Run()
	// Clean up when TUI exits (idempotent via sync.Once)
	cleanupOnce()
	return err
}

func startTUI(store *config.Store, workspace string) error {
	if err := os.MkdirAll(workspace, 0700); err != nil {
		return fmt.Errorf("create workspace: %w", err)
	}

	ws, err := store.GetWorkspace(workspace)
	if err != nil {
		return err
	}

	// Apply resolved settings (logging, SSRF whitelist, etc.)
	settings := store.Settings()
	settings.Init(logBuf)

	appCfg := store.Config()
	exec, resolvedExecType, err := buildExecutor(appCfg, workspace)
	if err != nil {
		return err
	}
	defer exec.Cleanup()

	// Use last-used provider if remembered, otherwise fall back to first
	initialProv := ws.LastProvider
	provCfg, provErr := store.GetProvider(initialProv)
	if provErr != nil || provCfg == nil {
		provCfg, _ = store.GetFirstProvider()
		initialProv = ""
	}

	model := tui.NewModel(nil, provCfg.Name, provCfg.ProviderType, provCfg.Model, workspace, store, ws)

	var ag *agent.Agent

	// Create a single cron manager for this workspace
	var cronMgr *cron.Manager
	var cronErr error
	cronMgr, cronErr = cron.New(store, ws.ID, func(jobID, chatID int64, instructions string, isScript bool) {
		if isScript {
			slog.Info("cron script executing", "job_id", jobID, "chat_id", chatID, "instructions", instructions)
			go func() {
				scriptEng := engine.New(ws, exec, &headlessUI{
					LogFunc: func(msg string) { slog.Info("cron", "output", msg) },
				}, "", store, logBuf).WithModuleDirs(store.ModuleDirs(ws.ID))
				scriptEng.SetProcess("cron", "", "", executorEnv(resolvedExecType, appCfg.DockerImage))
				scriptEng.WithCronManager(cronMgr, func() int64 { return chatID })
				// Wire agent bridge so cron scripts can call agent.run()/agent.result()
				if ag != nil {
					scriptEng.SetAgentRunner(ag)
				}
				ctx, cancel := context.WithTimeout(context.Background(), ws.TimeoutFor("cron"))
				result := scriptEng.RunModule(ctx, instructions)
				cancel()
				scriptEng.Cleanup()
				if result.Error != nil {
					slog.Error("cron script error", "err", result.Error)
				}
			}()
		} else {
			slog.Info("cron task", "chat_id", chatID, "instructions", instructions)
		}
	})
	if cronErr != nil {
		slog.Warn("cron setup failed", "error", cronErr)
		cronMgr = nil
	}

	var eng *engine.Engine
	ag, eng, err = buildAgent(store, ws, initialProv, model, exec, resolvedExecType, cronMgr)
	if err != nil {
		return err
	}
	defer eng.Cleanup()

	// Wire ui.confirm for TUI mode
	eng.SetConfirmContext(tui.NewConfirmContext(store, ws, func() {
		if model.RebuildAgent != nil {
			if newAg, err := model.RebuildAgent(initialProv); err == nil {
				model.SetAgent(newAg)
			}
		}
	}))

	// Track current engine for cleanup on provider switch
	currentEng := eng

	// Wire up provider switching — reuse the shared executor across rebuilds
	model.RebuildAgent = func(providerName string) (*agent.Agent, error) {
		newAg, newEng, err := buildAgent(store, ws, providerName, model, exec, resolvedExecType, cronMgr)
		if err != nil {
			return nil, err
		}
		// Wire ui.confirm on the new engine
		newEng.SetConfirmContext(tui.NewConfirmContext(store, ws, nil))
		// Clean up old engine (but not the shared executor)
		currentEng.Cleanup()
		currentEng = newEng
		return newAg, nil
	}

	_ = os.MkdirAll(filepath.Join(workspace, "scripts"), 0700)
	model.SetAgent(ag)
	model.ResumeLatestChat()

	p := tea.NewProgram(model)
	model.SetProgram(p)

	// Wire streaming chunks to the TUI
	ag.OnChunk = func(chunk string) {
		p.Send(tui.StreamChunk(chunk))
	}

	// Wire mid-execution message injection
	ag.PendingMsgs = model.DrainPendingMsgs

	_, err = p.Run()
	return err
}

func runScript(cmd *cobra.Command, args []string) error {
	store, err := initStore()
	if err != nil {
		return err
	}
	defer store.Close()

	var workspace string
	if len(args) > 1 {
		workspace = resolveWorkspace(args[1])
	} else {
		workspace = resolveWorkspace("")
	}

	ws, err := store.GetWorkspace(workspace)
	if err != nil {
		if err == datastore.ErrNoSuchEntity {
			return fmt.Errorf("workspace %s not initialized", workspace)
		}
		return err
	}

	// If a web server is already running, whitelist its port for loopback fetch
	if ws.Port > 0 {
		port := strconv.Itoa(ws.Port)
		netx.AllowLoopbackPort(port)
		os.Setenv("PORT", port)
	}

	appCfg := store.Config()
	var exec executor.Executor
	var resolvedExecType string
	if appCfg != nil {
		exec, resolvedExecType, err = buildExecutor(appCfg, workspace)
		if err != nil {
			return err
		}
		defer exec.Cleanup()
	}
	// Whitelist the altclaw port in the Docker proxy relay for self-testing
	if ws.Port > 0 && exec != nil {
		allowExecutorPort(exec, strconv.Itoa(ws.Port))
	}

	handler := &headlessUI{LogFunc: func(msg string) { fmt.Println(msg) }}
	eng := engine.New(ws, exec, handler, "", store, logBuf).WithModuleDirs(store.ModuleDirs(ws.ID))
	var scriptEnvVars map[string]string
	if appCfg != nil {
		scriptEnvVars = executorEnv(resolvedExecType, appCfg.DockerImage)
	}
	eng.SetProcess("run", "", args[0], scriptEnvVars)
	defer eng.Cleanup()

	timeout := ws.TimeoutFor("run", appCfg)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Read the script content and pass it as inline code — same pattern as cron.
	// This keeps require() in the non-absolute-path loader path so stdlib
	// modules (e.g. require("web")) resolve correctly.
	scriptPath := args[0]
	absScriptPath, err := bridge.SanitizePath(ws.Path, scriptPath)
	if err != nil {
		return fmt.Errorf("invalid script path: %w", err)
	}
	scriptContent, err := os.ReadFile(absScriptPath)
	if err != nil {
		return fmt.Errorf("cannot read script %s: %w", scriptPath, err)
	}

	result := eng.RunModule(ctx, string(scriptContent))
	if result.Error != nil {
		return result.Error
	}
	if result.Value != "" {
		fmt.Println(result.Value)
	}
	return nil
}
