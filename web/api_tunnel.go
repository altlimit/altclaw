package web

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"altclaw.ai/internal/buildinfo"
	"altclaw.ai/internal/config"
	"altclaw.ai/internal/tunnel"
	"github.com/altlimit/restruct"
)

// tunnelState tracks the tunnel connection state.
type tunnelState struct {
	mu              sync.Mutex
	client          *tunnel.Client
	cancel          context.CancelFunc
	status          string // "disconnected", "connecting", "connected"
	url             string // e.g. "abc123.altclaw.ai" or "abc123.localclaw.dev:8082"
	notify          chan struct{}
	statsReportStop chan struct{} // signals the hourly stats ticker to stop
	lastStatsHash   string        // hash of last reported stats payload
}

// statusPayload returns the current tunnel status as a map (for SSE events).
func (ts *tunnelState) statusPayload(ws *config.Workspace) map[string]any {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	paired := ws != nil && ws.TunnelToken != ""

	return map[string]any{
		"status":  ts.status,
		"url":     ts.url,
		"hub_url": hubHTTPURL(),
		"paired":  paired,
	}
}

// notifyChange sends a non-blocking signal on the notify channel.
func (ts *tunnelState) notifyChange() {
	select {
	case ts.notify <- struct{}{}:
	default:
	}
}

// TunnelStatus returns the current tunnel state.
func (a *Api) TunnelStatus() any {
	return a.server.tunnel.statusPayload(a.server.store.Workspace())
}

// TunnelPair pairs this altclaw instance with a hub using a 6-digit code.
func (a *Api) TunnelPair(r *http.Request) any {
	var req struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return restruct.Error{Status: http.StatusBadRequest, Message: "invalid request"}
	}
	if req.Code == "" {
		return restruct.Error{Status: http.StatusBadRequest, Message: "code is required"}
	}

	// Call hub's pair-complete endpoint
	ts := a.server.tunnel
	ts.mu.Lock()
	currentHost := ts.url
	ts.mu.Unlock()

	// Extract subdomain from full URL (e.g. "abcdefgh-relay.altclaw.ai" → "abcdefgh")
	hostname := currentHost
	if idx := strings.Index(hostname, "."); idx > 0 {
		hostname = hostname[:idx]
	}
	hostname = strings.TrimSuffix(hostname, "-relay")

	pairBody, _ := json.Marshal(map[string]string{
		"code":     req.Code,
		"name":     a.server.store.Workspace().Path,
		"hostname": hostname,
	})

	httpResp, err := http.Post(hubHTTPURL()+"/api/pair-complete", "application/json",
		bytes.NewReader(pairBody))
	if err != nil {
		return restruct.Error{Status: http.StatusBadGateway, Message: "failed to reach hub: " + err.Error()}
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode != 200 {
		var errResp struct {
			Message string `json:"message"`
		}
		if err := json.NewDecoder(httpResp.Body).Decode(&errResp); err != nil {
			body, _ := io.ReadAll(httpResp.Body)
			slog.Error("failed to decode hub response", "error", err, "body", string(body))
			return restruct.Error{Status: http.StatusBadRequest, Message: "invalid hub response", Err: err}
		}
		msg := errResp.Message
		if msg == "" {
			msg = "pairing failed"
		}
		return restruct.Error{Status: http.StatusBadRequest, Message: msg}
	}

	var result struct {
		Token    string `json:"token"`
		Hostname string `json:"hostname"`
		Domain   string `json:"domain"`
		TCPAddr  string `json:"tcp_addr"`
	}
	if err := json.NewDecoder(httpResp.Body).Decode(&result); err != nil {
		return restruct.Error{Status: http.StatusInternalServerError, Message: "invalid hub response"}
	}

	fullURL := result.Hostname
	if result.Domain != "" {
		fullURL = result.Hostname + "-relay." + result.Domain
	}

	if err := a.server.store.SaveWorkspace(r.Context(), func(w *config.Workspace) error {
		w.TunnelHost = fullURL
		w.TunnelAddr = result.TCPAddr
		w.TunnelToken = result.Token
		w.TunnelHub = hubHTTPURL()
		return nil
	}); err != nil {
		return restruct.Error{Status: http.StatusInternalServerError, Err: err}
	}

	return map[string]any{
		"status":   "paired",
		"hostname": result.Hostname,
		"domain":   result.Domain,
	}
}

// TunnelUnpair clears stored pairing data without disconnecting the tunnel.
func (a *Api) TunnelUnpair(r *http.Request) any {
	ctx := r.Context()
	ws := a.server.store.Workspace()
	if err := ws.Patch(ctx, a.server.store, map[string]any{
		"tunnel_token": "",
		"tunnel_hub":   "",
		"tunnel_host":  "",
		"tunnel_addr":  "",
		"tunnel_mode":  "",
	}); err != nil {
		return restruct.Error{Status: http.StatusInternalServerError, Err: err}
	}
	a.server.tunnel.notifyChange()
	return map[string]string{"status": "unpaired"}
}

// TunnelConnect starts the tunnel connection.
// Passes the stored token if paired (for reserved subdomains), otherwise connects anonymously.
func (a *Api) TunnelConnect(r *http.Request) any {
	ts := a.server.tunnel
	ts.mu.Lock()
	if ts.status == "connected" || ts.status == "connecting" {
		s, u := ts.status, ts.url
		ts.mu.Unlock()
		return map[string]string{"status": s, "url": u}
	}
	ts.mu.Unlock()

	if err := a.server.startTunnel(); err != nil {
		slog.Error("failed to start tunnel", "error", err)
		return map[string]string{"status": "error"}
	}

	// Persist mode so it auto-reconnects on restart
	if err := a.server.store.SaveWorkspace(r.Context(), func(w *config.Workspace) error {
		w.TunnelMode = "active"
		return nil
	}); err != nil {
		slog.Error("failed to persist tunnel mode", "error", err)
	}

	return map[string]string{"status": "connecting"}
}

// startTunnel is the shared helper that discovers the relay, creates the tunnel
// client, and starts RunWithReconnect with built-in re-authorization.
func (s *Server) startTunnel() error {
	ws := s.store.Workspace()
	token := ws.TunnelToken

	secretPubKey := ""
	if cfg := s.store.Config(); cfg != nil {
		secretPubKey = cfg.SecretPublicKey
	}

	result, err := discoverRelay(token, secretPubKey)
	if err != nil {
		return fmt.Errorf("discover relay: %w", err)
	}

	hubProfile := result.Profile

	ts := s.tunnel
	handler := s.tunnelHandler()
	client := tunnel.New(result.TCPAddr, token, result.Hostname, handler)
	ctx, cancel := context.WithCancel(context.Background())

	ts.mu.Lock()
	ts.client = client
	ts.cancel = cancel
	ts.status = "connecting"
	ts.mu.Unlock()

	discoverFn := func() (string, string, error) {
		r, err := discoverRelay(token, secretPubKey)
		if err != nil {
			return "", "", err
		}
		return r.TCPAddr, r.Hostname, nil
	}

	api := &Api{server: s}
	go client.RunWithReconnect(ctx, discoverFn,
		func(url string) {
			ts.mu.Lock()
			ts.status = "connected"
			ts.url = url
			ts.mu.Unlock()
			ts.notifyChange()
			slog.Info("tunnel connected", "url", url)
			_ = s.store.SaveWorkspace(context.Background(), func(w *config.Workspace) error {
				w.TunnelHost = url
				return nil
			})
			if hubProfile != nil {
				config.ApplyProfile(s.store, hubProfile)
				s.hub.BroadcastRaw([]byte(`{"type":"config_updated"}`))
				hubProfile = nil // apply only once
			}
			ts.mu.Lock()
			if ts.statsReportStop != nil {
				close(ts.statsReportStop)
			}
			stopCh := make(chan struct{})
			ts.statsReportStop = stopCh
			ts.mu.Unlock()
			go api.runStatsReporter(stopCh)
		},
		func(err error) {
			ts.mu.Lock()
			if ts.status != "disconnected" {
				ts.status = "connecting"
			}
			ts.mu.Unlock()
			ts.notifyChange()
			if err != nil {
				slog.Error("tunnel disconnected", "error", err)
			}
		},
	)

	return nil
}

// TunnelDisconnect stops the tunnel connection.
func (a *Api) TunnelDisconnect(r *http.Request) any {
	ts := a.server.tunnel
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
	if ts.statsReportStop != nil {
		close(ts.statsReportStop)
		ts.statsReportStop = nil
	}

	ts.notifyChange()

	// Clear profile data — providers and secrets are only valid while tunneled.
	a.server.store.SetProfile(nil)
	a.server.hub.BroadcastRaw([]byte(`{"type":"config_updated"}`))

	// Clear persisted mode
	if err := a.server.store.SaveWorkspace(r.Context(), func(w *config.Workspace) error {
		w.TunnelMode = ""
		return nil
	}); err != nil {
		slog.Error("failed to persist tunnel mode", "error", err)
	}

	return map[string]string{"status": "disconnected"}
}

// runStatsReporter reports stats immediately and then hourly until stopCh is closed.
func (a *Api) runStatsReporter(stopCh chan struct{}) {
	// Report immediately on connect
	a.reportHubStats(false)

	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-stopCh:
			return
		case <-ticker.C:
			a.reportHubStats(true)
		}
	}
}

// reportHubStats gathers workspace stats and POSTs them to the hub.
// If skipIfUnchanged is true and the payload hash matches the last sent, it skips.
func (a *Api) reportHubStats(skipIfUnchanged bool) {
	ctx := context.Background()
	ws := a.server.store.Workspace()
	if ws == nil || ws.TunnelHub == "" {
		return
	}

	wsID := ws.ID

	// Gather stats (same as api_stats.go)
	chats, _ := a.server.store.ListChats(ctx, wsID)
	chatCount := len(chats)

	var cronCount int
	if a.server.cronMgr != nil {
		cronCount = len(a.server.cronMgr.List())
	}

	a.server.mu.Lock()
	var activeChats int
	for _, sess := range a.server.chats {
		sess.mu.Lock()
		if sess.running {
			activeChats++
		}
		sess.mu.Unlock()
	}
	a.server.mu.Unlock()

	tokenUsageToday, _ := a.server.store.TodayTokenUsage(ctx, wsID)
	tokenUsageHistory, _ := a.server.store.GetTokenUsage(ctx, wsID, 14)

	// Extract hostname from tunnel URL
	ts := a.server.tunnel
	ts.mu.Lock()
	hostname := ts.url
	ts.mu.Unlock()
	if idx := strings.Index(hostname, "."); idx > 0 {
		hostname = hostname[:idx]
	}
	hostname = strings.TrimSuffix(hostname, "-relay")

	// Build payload
	var todayMap map[string]int64
	if tokenUsageToday != nil {
		todayMap = map[string]int64{
			"prompt_tokens":     tokenUsageToday.PromptTokens,
			"completion_tokens": tokenUsageToday.CompletionTokens,
			"total_tokens":      tokenUsageToday.TotalTokens,
		}
	}

	var historyList []map[string]any
	for _, h := range tokenUsageHistory {
		historyList = append(historyList, map[string]any{
			"id":                h.ID,
			"prompt_tokens":     h.PromptTokens,
			"completion_tokens": h.CompletionTokens,
			"total_tokens":      h.TotalTokens,
		})
	}

	payload := map[string]any{
		"hostname":            hostname,
		"chats":               chatCount,
		"cron_jobs":           cronCount,
		"active_chats":        activeChats,
		"token_usage_today":   todayMap,
		"token_usage_history": historyList,
	}

	body, _ := json.Marshal(payload)

	// Check hash to skip if unchanged
	hash := fmt.Sprintf("%x", sha256.Sum256(body))
	ts.mu.Lock()
	if skipIfUnchanged && ts.lastStatsHash == hash {
		ts.mu.Unlock()
		return
	}
	ts.lastStatsHash = hash
	ts.mu.Unlock()

	req, _ := http.NewRequest("POST", buildinfo.HubURL+"/api/stats/report", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Tunnel-Token", ws.TunnelToken)

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		slog.Error("stats report failed", "error", err)
		return
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	if resp.StatusCode != 200 {
		slog.Warn("stats report non-200", "status", resp.StatusCode)
	}
}
