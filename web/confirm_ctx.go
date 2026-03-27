package web

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"altclaw.ai/internal/bridge"
	"altclaw.ai/internal/config"
)

// serverConfirmCtx implements bridge.ConfirmContext using the web server.
// It gives the ui.confirm action handlers access to the store, workspace,
// and tunnel operations without coupling the bridge package to web.
type serverConfirmCtx struct {
	server *Server
}

// Verify interface compliance at compile time.
var _ bridge.ConfirmContext = (*serverConfirmCtx)(nil)

func (c *serverConfirmCtx) Store() *config.Store {
	return c.server.store
}

func (c *serverConfirmCtx) BroadcastCtx() context.Context {
	return config.WithBroadcast(context.Background(), config.BroadcastFunc(c.server.hub.BroadcastRaw))
}

func (c *serverConfirmCtx) Workspace() *config.Workspace {
	return c.server.store.Workspace()
}

func (c *serverConfirmCtx) RebuildAgent() {
	c.server.Api.rebuildAgent()
}

func (c *serverConfirmCtx) TunnelConnect() (map[string]any, error) {
	ts := c.server.tunnel
	ts.mu.Lock()
	if ts.status == "connected" || ts.status == "connecting" {
		s, u := ts.status, ts.url
		ts.mu.Unlock()
		return map[string]any{"status": s, "url": u}, nil
	}
	ts.mu.Unlock()

	if err := c.server.startTunnel(); err != nil {
		return nil, fmt.Errorf("start tunnel: %v", err)
	}

	// Persist mode
	_ = c.server.store.SaveWorkspace(context.Background(), func(w *config.Workspace) error {
		w.TunnelMode = "active"
		return nil
	})

	return map[string]any{"status": "connecting"}, nil
}

func (c *serverConfirmCtx) TunnelDisconnect() error {
	ts := c.server.tunnel
	ts.mu.Lock()
	defer ts.mu.Unlock()

	if ts.cancel != nil {
		ts.cancel()
	}
	if ts.client != nil {
		ts.client.Close()
	}
	ts.status = "disconnected"
	ts.url = ""
	ts.client = nil
	ts.cancel = nil

	ts.notifyChange()

	_ = c.server.store.SaveWorkspace(context.Background(), func(w *config.Workspace) error {
		w.TunnelMode = ""
		w.TunnelAddr = ""
		return nil
	})

	return nil
}

func (c *serverConfirmCtx) TunnelPair(code string) (map[string]any, error) {
	ws := c.server.store.Workspace()
	pairBody, _ := json.Marshal(map[string]string{
		"code": code,
		"name": ws.Path,
	})

	httpResp, err := http.Post(hubHTTPURL()+"/api/pair-complete", "application/json",
		bytes.NewReader(pairBody))
	if err != nil {
		return nil, fmt.Errorf("failed to reach hub: %v", err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode != 200 {
		var errResp struct {
			Message string `json:"message"`
		}
		if err := json.NewDecoder(httpResp.Body).Decode(&errResp); err != nil {
			body, _ := io.ReadAll(httpResp.Body)
			slog.Error("failed to decode hub response", "error", err, "body", string(body))
			return nil, fmt.Errorf("invalid hub response")
		}
		msg := errResp.Message
		if msg == "" {
			msg = "pairing failed"
		}
		return nil, fmt.Errorf("%s", msg)
	}

	var result struct {
		Token    string `json:"token"`
		Hostname string `json:"hostname"`
		Domain   string `json:"domain"`
		TCPAddr  string `json:"tcp_addr"`
	}
	if err := json.NewDecoder(httpResp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("invalid hub response")
	}

	if err := ws.Patch(context.Background(), c.server.store, map[string]any{
		"tunnel_token": result.Token,
		"tunnel_hub":   hubHTTPURL(),
		"tunnel_host":  result.Hostname,
		"tunnel_addr":  result.TCPAddr,
	}); err != nil {
		return nil, err
	}

	return map[string]any{
		"status":   "paired",
		"hostname": result.Hostname,
		"domain":   result.Domain,
	}, nil
}

func (c *serverConfirmCtx) TunnelUnpair() error {
	ts := c.server.tunnel
	ts.mu.Lock()
	if ts.cancel != nil {
		ts.cancel()
	}
	if ts.client != nil {
		ts.client.Close()
	}
	ts.status = "disconnected"
	ts.url = ""
	ts.client = nil
	ts.cancel = nil
	ts.mu.Unlock()
	ts.notifyChange()

	ws := c.server.store.Workspace()
	return ws.Patch(context.Background(), c.server.store, map[string]any{
		"tunnel_token": "",
		"tunnel_hub":   "",
		"tunnel_host":  "",
		"tunnel_addr":  "",
		"tunnel_mode":  "",
	})
}

func (c *serverConfirmCtx) ModuleInstall(id, scope string) (map[string]any, error) {
	wsDir, userDir := c.server.store.ModuleDirs(c.server.store.Workspace().ID)
	baseDir := userDir
	if scope == "workspace" {
		baseDir = wsDir
	}

	// Query the hub search API to get the version ID for this module.
	// We can't construct the zip URL directly because the hub parses
	// slug-version from the last dash, which fails for slugs that contain dashes.
	hubBase := strings.TrimRight(hubHTTPURL(), "/")
	infoResp, err := http.Get(fmt.Sprintf("%s/api/modules?q=%s&limit=5", hubBase, id))
	if err != nil {
		return nil, fmt.Errorf("failed to query marketplace for %q: %v", id, err)
	}
	defer infoResp.Body.Close()
	if infoResp.StatusCode != 200 {
		return nil, fmt.Errorf("marketplace returned status %d for %q", infoResp.StatusCode, id)
	}
	var mods []struct {
		Slug      string `json:"slug"`
		VersionID string `json:"id"`
	}
	if err := json.NewDecoder(infoResp.Body).Decode(&mods); err != nil {
		return nil, fmt.Errorf("invalid marketplace response: %v", err)
	}
	// Find exact slug match
	var versionID string
	for _, m := range mods {
		if m.Slug == id {
			versionID = m.VersionID
			break
		}
	}
	if versionID == "" {
		return nil, fmt.Errorf("module %q not found on marketplace", id)
	}

	// Download using slug-versionID.zip format against local hub
	zipURL := fmt.Sprintf("%s/api/modules/%s-%s.zip", hubBase, id, versionID)
	resp, err := http.Get(zipURL)
	if err != nil || resp.StatusCode != 200 {
		if resp != nil {
			resp.Body.Close()
		}
		return nil, fmt.Errorf("failed to download module %q from marketplace (status %d)", id, resp.StatusCode)
	}
	defer resp.Body.Close()

	buf, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	zr, err := zip.NewReader(bytes.NewReader(buf), int64(len(buf)))
	if err != nil {
		return nil, fmt.Errorf("invalid zip: %w", err)
	}

	modName := strings.ReplaceAll(id, "/", "-")
	destDir := filepath.Join(baseDir, modName)
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return nil, err
	}

	for _, f := range zr.File {
		rel := stripTopDir(f.Name)
		if rel == "" || strings.HasPrefix(rel, "..") {
			continue
		}
		target := filepath.Join(destDir, rel)
		if !strings.HasPrefix(target, destDir+string(filepath.Separator)) && target != destDir {
			continue
		}
		if f.FileInfo().IsDir() {
			_ = os.MkdirAll(target, 0755)
			continue
		}
		if err := extractZipFile(f, target); err != nil {
			return nil, err
		}
	}

	go (&Api{server: c.server}).sendTelemetry(id, "install")

	c.server.BroadcastPanel([]byte(fmt.Sprintf(`{"type":"module_updated","action":"installed","id":%q}`, modName)))
	return map[string]any{"status": "installed", "id": modName}, nil
}

func (c *serverConfirmCtx) ModuleRemove(id, scope string) (map[string]any, error) {
	if id == "" || strings.Contains(id, "..") {
		return nil, fmt.Errorf("invalid module id")
	}
	wsDir, userDir := c.server.store.ModuleDirs(c.server.store.Workspace().ID)
	baseDir := userDir
	if scope == "workspace" {
		baseDir = wsDir
	}
	target := filepath.Join(baseDir, id)
	if !strings.HasPrefix(target, baseDir+string(filepath.Separator)) {
		return nil, fmt.Errorf("invalid module id")
	}
	if err := os.RemoveAll(target); err != nil {
		return nil, err
	}

	go (&Api{server: c.server}).sendTelemetry(id, "uninstall")

	c.server.BroadcastPanel([]byte(fmt.Sprintf(`{"type":"module_updated","action":"deleted","id":%q}`, id)))
	return map[string]any{"status": "deleted"}, nil
}

// NewConfirmContext creates a ConfirmContext backed by this server.
func (s *Server) NewConfirmContext() bridge.ConfirmContext {
	return &serverConfirmCtx{server: s}
}
