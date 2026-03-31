package web

import (
	"crypto/subtle"
	"net/http"
	"time"

	"altclaw.ai/internal/config"
	"altclaw.ai/internal/util"
	"github.com/altlimit/restruct"
)

// Api handles JSON API endpoints.
type Api struct {
	server *Server
}

func (a *Api) Middlewares() []restruct.Middleware {
	return []restruct.Middleware{
		a.authMiddleware,
	}
}

func (a *Api) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Inject SSE broadcast into all request contexts so model hooks fire
		ctx := config.WithBroadcast(r.Context(), config.BroadcastFunc(a.server.hub.BroadcastRaw))
		r = r.WithContext(ctx)

		// Allow unauthenticated access to auth and passkey login endpoints
		switch r.URL.Path {
		case "/api/auth", "/api/passkey-login-begin", "/api/passkey-login-finish", "/api/has-passkeys", "/api/stats", "/api/vapid-public-key":
			next.ServeHTTP(w, r)
			return
		}
		// In GUI mode, localhost requests bypass session auth entirely.
		// The Wails webview is a trusted local context — no session needed.
		if a.server.isGUILocal(r) {
			next.ServeHTTP(w, r)
			return
		}
		cookie, err := r.Cookie("altclaw_session")
		if err != nil {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		a.server.mu.Lock()
		created, valid := a.server.sessions[cookie.Value]
		if valid && time.Since(created) > 30*24*time.Hour {
			delete(a.server.sessions, cookie.Value)
			valid = false
		}
		a.server.mu.Unlock()
		if !valid {
			clearSessionCookie(w, r)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *Api) Auth(w http.ResponseWriter, r *http.Request) any {
	if r.Method == http.MethodDelete {
		cookie, err := r.Cookie("altclaw_session")
		if err == nil {
			a.server.mu.Lock()
			delete(a.server.sessions, cookie.Value)
			a.server.mu.Unlock()
		}
		clearSessionCookie(w, r)
		return map[string]string{"status": "ok"}
	}

	// Block password login through relay when passkeys are available for this origin.
	if r.Header.Get("X-Client-IP") != "" && hasPasskeysForOrigin(r.Context(), a.server.store, rpIDFromRequest(r)) {
		return restruct.Error{Status: http.StatusForbidden, Message: "password login disabled through relay — use passkey"}
	}
	ctx := r.Context()
	ip := util.ClientIP(r)
	cache := a.server.store.Client.Cache()
	if result, err := cache.RateLimit(ctx, ip, 5, time.Minute); err != nil {
		return err
	} else if !result.Allowed {
		return restruct.Error{Status: http.StatusTooManyRequests}
	}

	var req struct {
		Password string `json:"password"`
	}
	if err := restruct.Bind(r, &req); err != nil {
		return err
	}

	if subtle.ConstantTimeCompare([]byte(req.Password), []byte(a.server.password)) != 1 {
		return restruct.Error{Status: http.StatusUnauthorized, Message: "invalid password"}
	}

	a.server.rotatePassword()

	// Generate session
	a.server.createSession(w, r, 86400*7)
	return map[string]string{"status": "ok"}
}

// rebuildAgent clears all cached chat sessions so they pick up new config.
func (a *Api) rebuildAgent() {
	a.server.mu.Lock()
	a.server.chats = make(map[int64]*chatSession)
	a.server.mu.Unlock()
}
