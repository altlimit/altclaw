//go:build gui

package main

import (
	"embed"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"altclaw.ai/internal/buildinfo"
	"altclaw.ai/internal/config"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

//go:embed assets/*
var guiAssets embed.FS

func startGUI(store *config.Store, workspace, addr string) error {
	// Force verbose mode (no Bubble Tea TUI).
	flagVerbose = true

	iconData, _ := guiAssets.ReadFile("assets/altclaw.png")

	// Create the Wails application.
	app := application.New(application.Options{
		Name: "Altclaw",
		Icon: iconData,
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(guiAssets),
		},
	})

	// Create the main window with the splash screen.
	window := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:                      "Altclaw",
		Width:                      1280,
		Height:                     860,
		URL:                        "/",
		DefaultContextMenuDisabled: buildinfo.Version != "dev",
	})

	// Channel signals when the webview has completed its first navigation.
	webviewReady := make(chan struct{}, 1)
	window.OnWindowEvent(events.Windows.WebViewNavigationCompleted, func(e *application.WindowEvent) {
		select {
		case webviewReady <- struct{}{}:
		default:
		}
	})

	// Helper to update the splash screen status text via JS.
	setStatus := func(text string) {
		window.ExecJS(fmt.Sprintf(`document.getElementById('status').innerHTML = %q`, text))
	}

	// After the event loop starts, show folder dialog (if needed) and start the server.
	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(event *application.ApplicationEvent) {
		go func() {
			ws := workspace

			// Wait for the splash screen to be fully loaded.
			<-webviewReady

			// If no workspace, show folder picker dialog.
			if ws == "" {
				setStatus("Select a workspace folder<span class='dots'></span>")

				selected, err := app.Dialog.OpenFile().
					SetTitle("Select Workspace Folder").
					CanChooseDirectories(true).
					CanChooseFiles(false).
					PromptForSingleSelection()
				if err != nil || selected == "" {
					slog.Error("no workspace selected, closing")
					app.Quit()
					return
				}
				ws = selected
				if abs, err := filepath.Abs(ws); err == nil {
					ws = abs
				}
				if resolved, err := filepath.EvalSymlinks(ws); err == nil {
					ws = resolved
				}
			}

			setStatus("Starting server<span class='dots'></span>")

			// Channel to receive the login URL once the server is ready.
			readyCh := make(chan string, 1)
			guiReadyFn = func(loginURL string) {
				readyCh <- loginURL
			}

			// Start the web server.
			go func() {
				if err := startWeb(store, ws, addr); err != nil {
					slog.Error("web server error", "err", err)
					app.Quit()
				}
			}()

			// Wait for server ready.
			loginURL := <-readyCh
			slog.Info("GUI: server ready", "url", loginURL)

			// In dev mode, redirect to Vite dev server on :5173.
			targetURL := loginURL
			if buildinfo.Version == "dev" {
				if u, err := url.Parse(loginURL); err == nil {
					u.Host = u.Hostname() + ":5173"
					targetURL = u.String()
					slog.Info("GUI: dev mode, using Vite", "url", targetURL)
				}
			}
			setStatus("Connecting<span class='dots'></span>")

			// Probe a neutral URL to check reachability without consuming the
			// one-time auto-login password. The /auth/auto-login endpoint rotates
			// the password on success, so hitting it here would invalidate the
			// URL before the webview navigates to it.
			probeURL := loginURL
			if u, err := url.Parse(targetURL); err == nil {
				u.Path = "/"
				u.RawQuery = ""
				probeURL = u.String()
			}

			// Retry: Windows firewall dialogs can block the first connection.
			for i := 0; i < 10; i++ {
				resp, err := http.Get(probeURL)
				if err == nil {
					resp.Body.Close()
					break
				}
				slog.Info("GUI: waiting for server to be reachable", "attempt", i+1)
				time.Sleep(time.Second)
			}

			window.SetURL(targetURL)
		}()
	})

	// Graceful shutdown on signals.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		slog.Info("shutting down GUI")
		app.Quit()
	}()

	// Run the Wails event loop — blocks until the window is closed.
	if err := app.Run(); err != nil {
		return fmt.Errorf("GUI error: %w", err)
	}

	// Graceful shutdown: clean up server, executor (Docker containers), store.
	if guiShutdownFn != nil {
		guiShutdownFn()
	}
	return nil
}
