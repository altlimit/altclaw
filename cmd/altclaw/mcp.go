package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	"altclaw.ai/internal/config"
)

// savePort persists the actual server port to the workspace record.
// actualAddr is like ":8080".
func savePort(store *config.Store, ws *config.Workspace, actualAddr string) {
	portStr := actualAddr
	if len(portStr) > 0 && portStr[0] == ':' {
		portStr = portStr[1:]
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return
	}
	_ = store.SaveWorkspace(context.Background(), func(w *config.Workspace) error {
		w.Port = port
		return nil
	})
}

// startMCP runs the MCP stdio proxy.
// - If a web server is already running (ws.Port is set and reachable), proxy to it.
// - Otherwise, start a new web server, then proxy.
// - On exit, shut down the server if we started it.
func startMCP(store *config.Store, workspace string) error {
	ws, err := store.GetWorkspace(workspace)
	if err != nil {
		return err
	}

	port := ws.Port

	// Check if existing port is reachable
	if port > 0 {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 2*time.Second)
		if err != nil {
			port = 0 // not reachable, will start a new server
		} else {
			conn.Close()
		}
	}
	if port == 0 {
		// Start web server in background
		slog.Info("mcp: starting web server for workspace", "workspace", workspace)

		addr := flagAddr
		if addr == "" {
			addr = "127.0.0.1:0"
		}

		errCh := make(chan error, 1)

		go func() {
			err := startWeb(store, workspace, addr)
			if err != nil {
				errCh <- err
			}
		}()

		// Wait for the port to be saved — poll the workspace record
		timeout := time.After(30 * time.Second)
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()

	waitLoop:
		for {
			select {
			case err := <-errCh:
				return fmt.Errorf("mcp: web server failed: %w", err)
			case <-timeout:
				return fmt.Errorf("mcp: timed out waiting for web server to start")
			case <-ticker.C:
				// Re-read workspace to get the port
				fresh := store.Workspace()
				if fresh != nil && fresh.Port > 0 {
					port = fresh.Port
					break waitLoop
				}
			}
		}

		slog.Info("mcp: web server ready", "port", port)
	}

	// Proxy stdio to the web server's /mcp endpoint.
	// The /mcp endpoint is public (no auth required), so no session cookie needed.
	// This ensures MCP tools get the full bridge stack (executor, Docker, etc.).
	slog.Info("mcp: proxying stdio to web server", "port", port)
	return mcpProxyToHTTP(port, "")
}

// mcpProxyToHTTP sends JSON-RPC requests to the web server's /mcp endpoint.
// Used when proxying to an already-running instance.
func mcpProxyToHTTP(port int, sessionCookie string) error {
	baseURL := fmt.Sprintf("http://127.0.0.1:%d/mcp", port)
	client := &http.Client{Timeout: 60 * time.Second}

	reader := bufio.NewReader(os.Stdin)

	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return fmt.Errorf("stdin read: %w", err)
		}

		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}

		req, err := http.NewRequest("POST", baseURL, bytes.NewReader(line))
		if err != nil {
			return fmt.Errorf("create request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		if sessionCookie != "" {
			req.AddCookie(&http.Cookie{Name: "altclaw_session", Value: sessionCookie})
		}

		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("proxy request: %w", err)
		}

		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode == http.StatusNoContent {
			continue // notification acknowledged
		}

		os.Stdout.Write(body)
		os.Stdout.Write([]byte("\n"))
	}
}
