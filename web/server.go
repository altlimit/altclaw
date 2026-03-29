package web

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"altclaw.ai/internal/agent"
	"altclaw.ai/internal/bridge"
	"altclaw.ai/internal/config"
	"altclaw.ai/internal/cron"
	"altclaw.ai/internal/executor"
	"altclaw.ai/internal/mcp"
	"altclaw.ai/internal/serverjs"
	"github.com/altlimit/restruct"
	"github.com/fsnotify/fsnotify"
	"github.com/go-webauthn/webauthn/webauthn"
)

//go:embed all:views
var viewsFS embed.FS

// chatEvent is a single SSE event produced by the agent.
type chatEvent struct {
	Type      string `json:"type"`
	Content   string `json:"content,omitempty"`
	ChatID    int64  `json:"chat_id,omitempty"`
	MessageID string `json:"message_id,omitempty"`
}

// chatSession holds the state for a single chat session.
type chatSession struct {
	agent           *agent.Agent
	ctx             context.Context
	cancel          context.CancelFunc
	mu              sync.Mutex  // per-session lock for serialized sends
	running         bool        // true while agent.Send is in progress
	pendingEvents   []chatEvent // buffered events during current execution
	askChan         chan string // channel for receiving ui.ask answers
	pendingUserMsgs []string    // user messages queued while agent is working
}

// bufferAndBroadcast records the event for replay and broadcasts it via the hub.
func (s *chatSession) bufferAndBroadcast(hub *EventHub, evt chatEvent) {
	s.mu.Lock()
	s.pendingEvents = append(s.pendingEvents, evt)
	s.mu.Unlock()
	hub.Broadcast(evt)
}

// AskUser sends an ask event to the UI and blocks until answered or execution cancels.
// If no SSE clients are connected, a web push is sent so the user knows action is needed.
func (s *Server) AskUser(chatID int64, question string) string {
	s.mu.Lock()
	sess, ok := s.chats[chatID]
	s.mu.Unlock()
	if !ok {
		return ""
	}

	sess.bufferAndBroadcast(s.hub, chatEvent{
		Type:    "ask",
		Content: question,
		ChatID:  chatID,
	})

	// Push if nobody is watching via SSE
	s.maybePush("Action Required", question)

	select {
	case answer := <-sess.askChan:
		return answer
	case <-sess.ctx.Done():
		return ""
	}
}

// ConfirmUser sends a structured confirm event to the UI and blocks until the user
// approves or rejects. Uses the same askChan as AskUser — the frontend sends "yes" or "no".
func (s *Server) ConfirmUser(chatID int64, action, label, summary string, params map[string]any) string {
	s.mu.Lock()
	sess, ok := s.chats[chatID]
	s.mu.Unlock()
	if !ok {
		return "no"
	}

	// Build confirm payload as JSON in Content
	payload := map[string]any{
		"action":  action,
		"label":   label,
		"summary": summary,
		"params":  params,
	}
	payloadJSON, _ := json.Marshal(payload)

	sess.bufferAndBroadcast(s.hub, chatEvent{
		Type:    "confirm",
		Content: string(payloadJSON),
		ChatID:  chatID,
	})

	// Push if nobody is watching via SSE
	s.maybePush("Action Approval Required", label+": "+summary)

	select {
	case answer := <-sess.askChan:
		return answer
	case <-sess.ctx.Done():
		return "no"
	}
}

// App serves the IDE web UI at /app/.
type App struct {
	server *Server
}

func (a *App) Writer() restruct.ResponseWriter {
	sub, _ := fs.Sub(viewsFS, "views")
	return &restruct.View{FS: sub}
}

func (a *App) Index() *restruct.Render {
	return &restruct.Render{Path: "index.html"}
}

func (a *App) Any() *restruct.Render {
	return &restruct.Render{Path: "index.html"}
}

// Server is the web server for Altclaw.
type Server struct {
	Api Api
	App App `route:"app"`

	chats          map[int64]*chatSession // chatID → session
	hub            *EventHub
	store          *config.Store
	logBuf         *bridge.LogBuffer
	cronMgr        *cron.Manager
	password       string
	sessions       map[string]time.Time
	rateLimits     map[string][]time.Time // IP -> failed login timestamps
	passkeySession *webauthn.SessionData  // transient WebAuthn session
	tunnel         *tunnelState
	serverJS       *serverjs.Handler
	mcpServer      *mcp.Server
	GUIMode        bool // When true, skip auth for localhost (Wails webview)
	mu             sync.Mutex

	// Exec is the shared executor instance (Docker/Podman/Local) used by
	// RunScript to avoid creating a new container per invocation.
	Exec     executor.Executor
	ExecType string // resolved type: "docker", "podman", "local", "none"

	// NewAgent rebuilds the agent when config changes
	NewAgent func(providerName string) (*agent.Agent, error)
}

// NewServer creates a web server.
func NewServer(ag *agent.Agent, store *config.Store, ws *config.Workspace, cronMgr *cron.Manager, logBuf *bridge.LogBuffer, newAgent func(providerName string) (*agent.Agent, error)) *Server {
	password := generatePassword()
	s := &Server{
		chats:      make(map[int64]*chatSession),
		hub:        NewEventHub(),
		store:      store,
		logBuf:     logBuf,
		cronMgr:    cronMgr,
		password:   password,
		sessions:   make(map[string]time.Time),
		rateLimits: make(map[string][]time.Time),
		NewAgent:   newAgent,
	}
	s.Api.server = s
	s.App.server = s
	return s
}

// BroadcastLog sends a cron log event to a specific chat via the SSE hub.
// Uses "cron" event type so the frontend displays it without entering thinking state.
func (s *Server) BroadcastLog(chatID int64, msg string) {
	s.hub.Broadcast(chatEvent{Type: "cron", Content: msg, ChatID: chatID})
}

// BroadcastPanel sends pre-serialized JSON event data to all SSE subscribers.
// Used by bridge callbacks to notify the frontend when cron/module/memory changes occur.
func (s *Server) BroadcastPanel(data []byte) {
	s.hub.BroadcastRaw(data)
}

// maybePush sends a web push notification only when no SSE clients are connected.
// This avoids spamming the user who already has the app open and is watching via SSE.
func (s *Server) maybePush(title, body string) {
	if !s.hub.HasListeners() {
		SendPush(s.store, title, body)
	}
}

// SetServerJS sets the server-side JS handler for .server.js execution in public dirs.
func (s *Server) SetServerJS(h *serverjs.Handler) {
	s.serverJS = h
}

// SetMCP sets the MCP server for handling JSON-RPC 2.0 MCP requests.
func (s *Server) SetMCP(m *mcp.Server) {
	s.mcpServer = m
}

// Password returns the generated password.
func (s *Server) Password() string {
	return s.password
}

// rotatePassword generates a new password so the previous one can't be reused.
func (s *Server) rotatePassword() {
	s.mu.Lock()
	s.password = generatePassword()
	s.mu.Unlock()
	slog.Info("password rotated after login", "password", s.password)
}

// isLocalRequest returns true when the request originates from localhost
// without proxy headers and the Host header targets a loopback address.
// Guards against DNS rebinding, tunnel proxies, and spoofed headers.
func isLocalRequest(r *http.Request) bool {
	// Proxy headers indicate the request was forwarded through the relay/tunnel,
	// even if RemoteAddr happens to be localhost.
	if r.Header.Get("X-Forwarded-For") != "" || r.Header.Get("X-Client-IP") != "" {
		return false
	}
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	if host != "127.0.0.1" && host != "::1" && host != "localhost" {
		return false
	}
	// Defend against DNS rebinding: verify the Host header targets localhost,
	// not a malicious domain that re-resolved to 127.0.0.1.
	reqHost := r.Host
	if h, _, err := net.SplitHostPort(reqHost); err == nil {
		reqHost = h
	}
	return reqHost == "localhost" || reqHost == "127.0.0.1" || reqHost == "::1"
}

// isGUILocal returns true when running in Wails GUI mode and the request
// is a genuine localhost request. Session auth is skipped because the
// Wails webview is a trusted local context.
func (s *Server) isGUILocal(r *http.Request) bool {
	return s.GUIMode && isLocalRequest(r)
}

func (s *Server) Init(h *restruct.Handler) {
	h.Use(restruct.Recovery)
}

func (s *Server) servePublic(w http.ResponseWriter, r *http.Request) {
	ws := s.store.Workspace()
	if ws.PublicDir != "" {
		publicPath := ws.PublicDir
		if !filepath.IsAbs(publicPath) {
			publicPath = filepath.Join(ws.Path, publicPath)
		}
		publicPath = filepath.Clean(publicPath)

		reqPath := strings.TrimPrefix(r.URL.Path, "/")
		if reqPath == "" {
			reqPath = "index.html"
		}

		// Block direct access to .server.js source files
		if strings.HasSuffix(reqPath, ".server.js") {
			http.Error(w, "File not found", http.StatusNotFound)
			return
		}

		// Check for .server.js handler — supports exact, dynamic [param], and catch-all [...param]
		// Only try dynamic routes if no static file exists at the requested path.
		if s.serverJS != nil {
			filePath := filepath.Join(publicPath, filepath.FromSlash(reqPath))
			_, staticErr := os.Stat(filePath)

			if staticErr != nil {
				scriptPath, params, ok := serverjs.ResolveRoute(publicPath, reqPath)
				if ok {
					// Security: ensure server file is inside public dir
					realPublic, err := filepath.EvalSymlinks(publicPath)
					if err != nil {
						realPublic = publicPath
					}
					realServer, err := filepath.EvalSymlinks(scriptPath)
					if err != nil {
						realServer = scriptPath
					}
					rel, err := filepath.Rel(realPublic, realServer)
					if err == nil && !strings.HasPrefix(rel, "..") {
						s.serverJS.ServeHTTP(w, r, realServer, r.URL.Path, params)
						return
					}
				}
			}
		}

		filePath := filepath.Join(publicPath, filepath.FromSlash(reqPath))

		// Resolve symlinks for both paths
		realPublic, err := filepath.EvalSymlinks(publicPath)
		if err != nil {
			realPublic = publicPath
		}
		realFile, err := filepath.EvalSymlinks(filePath)
		if err != nil {
			// File might not exist yet — evaluate parent
			realFile = filePath
		}

		// Security: ensure resolved path is inside the public dir
		rel, err := filepath.Rel(realPublic, realFile)
		if err != nil || strings.HasPrefix(rel, "..") {
			http.Error(w, "File not found", http.StatusNotFound)
			return
		}
		http.ServeFile(w, r, realFile)
		return
	}
	http.Redirect(w, r, "/app/", http.StatusFound)
}

func (s *Server) Index(w http.ResponseWriter, r *http.Request) {
	s.servePublic(w, r)
}

func (s *Server) Any(w http.ResponseWriter, r *http.Request) {
	s.servePublic(w, r)
}

// Start starts the web server on the given address.
// If onReady is non-nil, it is called with the actual bound address (e.g. ":8080")
// after the listener is ready but before serving begins.
func (s *Server) Start(addr string, onReady func(actualAddr string)) error {
	s.tunnel = &tunnelState{status: "disconnected", notify: make(chan struct{}, 1)}

	// Auto-reconnect tunnel if it was active before restart
	s.autoReconnectTunnel()

	// Start single file watcher + tunnel listener that broadcast via hub
	s.startFileWatcher()
	s.startTunnelWatcher()

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	actualPort := ln.Addr().(*net.TCPAddr).Port
	actualAddr := fmt.Sprintf(":%d", actualPort)

	slog.Info("web UI started", "addr", "http://localhost"+actualAddr, "password", s.password)

	restruct.Handle("/", s)

	if onReady != nil {
		onReady(actualAddr)
	}

	return http.Serve(ln, nil)
}

// handleAutoLogin validates the password via query string and creates a session.
// GET /auth/auto-login?p=<password>
// Used when the app is double-clicked and auto-opens a browser.
// Only allowed from localhost to prevent leaking the password over tunnels/proxies.
func (s *Server) Auth_AutoLogin(w http.ResponseWriter, r *http.Request) {
	if !isLocalRequest(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	pw := r.URL.Query().Get("p")
	if pw == "" || subtle.ConstantTimeCompare([]byte(pw), []byte(s.password)) != 1 {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	s.rotatePassword()

	s.createSession(w, r, 86400*30)
	http.Redirect(w, r, "/app/", http.StatusFound)
}

// startFileWatcher creates a single fsnotify watcher and broadcasts file events via the hub.
func (s *Server) startFileWatcher() {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		slog.Warn("failed to create file watcher", "error", err)
		return
	}

	ws := s.store.Workspace()
	_ = filepath.WalkDir(ws.Path, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && !strings.HasPrefix(d.Name(), ".") {
			watcher.Add(path)
		}
		return nil
	})
	go func() {
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				rel, err := filepath.Rel(ws.Path, event.Name)
				if err != nil || strings.HasPrefix(rel, "..") {
					continue
				}
				switch {
				case event.Has(fsnotify.Create):
					if info, err := os.Stat(event.Name); err == nil && info.IsDir() && !strings.HasPrefix(filepath.Base(event.Name), ".") {
						watcher.Add(event.Name)
					}
					data, _ := json.Marshal(map[string]string{"type": "file_created", "path": rel})
					s.hub.BroadcastRaw(data)
				case event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename):
					data, _ := json.Marshal(map[string]string{"type": "file_deleted", "path": rel})
					s.hub.BroadcastRaw(data)
				case event.Has(fsnotify.Write):
					data, _ := json.Marshal(map[string]string{"type": "file_changed", "path": rel})
					s.hub.BroadcastRaw(data)
				}
			case <-watcher.Errors:
				// ignore
			}
		}
	}()
}

// startTunnelWatcher listens for tunnel status changes and broadcasts via the hub.
func (s *Server) startTunnelWatcher() {
	go func() {
		for {
			<-s.tunnel.notify
			payload := s.tunnel.statusPayload(s.store.Workspace())
			payload["type"] = "tunnel_status"
			data, _ := json.Marshal(payload)
			s.hub.BroadcastRaw(data)
		}
	}()
}

// autoReconnectTunnel checks persisted TunnelMode and reconnects if needed.
func (s *Server) autoReconnectTunnel() {
	ws := s.store.Workspace()
	if ws.TunnelMode != "active" {
		return
	}
	slog.Info("auto-reconnecting tunnel", "token_present", ws.TunnelToken != "")
	if err := s.startTunnel(); err != nil {
		slog.Error("auto-reconnect: failed to start tunnel", "error", err)
	}
}

// tunnelHandler returns the HTTP handler used for serving requests through the tunnel.
// This is the same handler used for local requests, so tunnel users get the full altclaw UI.
func (s *Server) tunnelHandler() http.Handler {
	return http.DefaultServeMux
}

func generatePassword() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// createSession generates a new session ID, stores it, and sets the session cookie.
func (s *Server) createSession(w http.ResponseWriter, r *http.Request, maxAge int) string {
	sessionID := generatePassword()
	s.mu.Lock()
	s.sessions[sessionID] = time.Now()
	s.mu.Unlock()
	setSessionCookie(w, r, sessionID, maxAge)
	return sessionID
}

// setSessionCookie sets the altclaw_session cookie with the given value and max age.
func setSessionCookie(w http.ResponseWriter, r *http.Request, value string, maxAge int) {
	isSecure := r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
	http.SetCookie(w, &http.Cookie{
		Name:     "altclaw_session",
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   isSecure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   maxAge,
	})
}

// clearSessionCookie removes the altclaw_session cookie.
func clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	setSessionCookie(w, r, "", -1)
}

// handleHubLogin validates a signed hub token and auto-creates a session.
// GET /auth/hub-login?t=signature:timestamp
func (s *Server) Auth_HubLogin(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("t")
	if token == "" {
		http.Error(w, "Missing token", http.StatusBadRequest)
		return
	}

	// Parse "signature:timestamp"
	parts := strings.SplitN(token, ":", 2)
	if len(parts) != 2 {
		http.Error(w, "Invalid token format", http.StatusBadRequest)
		return
	}
	sig, tsStr := parts[0], parts[1]

	// Check timestamp (60 second window)
	ts, err := strconv.ParseInt(tsStr, 10, 64)
	if err != nil || time.Now().Unix()-ts > 60 {
		http.Error(w, "Token expired", http.StatusForbidden)
		return
	}

	ws := s.store.Workspace()
	// Validate HMAC signature using the stored TunnelToken
	if ws.TunnelToken == "" {
		http.Error(w, "Not paired", http.StatusForbidden)
		return
	}

	mac := hmac.New(sha256.New, []byte(ws.TunnelToken))
	mac.Write([]byte(tsStr))
	expectedSig := hex.EncodeToString(mac.Sum(nil))

	if subtle.ConstantTimeCompare([]byte(sig), []byte(expectedSig)) != 1 {
		http.Error(w, "Invalid token", http.StatusForbidden)
		return
	}

	s.createSession(w, r, 86400*30)
	http.Redirect(w, r, "/app/", http.StatusFound)
}
