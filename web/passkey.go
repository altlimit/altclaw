package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"strconv"
	"time"

	"altclaw.ai/internal/config"
	"github.com/altlimit/restruct"
	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

// passkeyUser implements webauthn.User for the single altclaw user.
type passkeyUser struct {
	Credentials []webauthn.Credential `json:"credentials"`
}

func (u *passkeyUser) WebAuthnID() []byte                         { return []byte("altclaw-user") }
func (u *passkeyUser) WebAuthnName() string                       { return "altclaw" }
func (u *passkeyUser) WebAuthnDisplayName() string                { return "Altclaw User" }
func (u *passkeyUser) WebAuthnCredentials() []webauthn.Credential { return u.Credentials }

// loadPasskeyCredentials loads passkey credentials from dsorm, optionally filtered by rpID.
func loadPasskeyCredentials(ctx context.Context, store *config.Store, rpID string) []webauthn.Credential {
	entries, err := store.ListPasskeys(ctx)
	if err != nil {
		return nil
	}
	var creds []webauthn.Credential
	for _, e := range entries {
		if e.RPID != rpID {
			continue
		}
		var cred webauthn.Credential
		if err := json.Unmarshal([]byte(e.CredentialData), &cred); err == nil {
			creds = append(creds, cred)
		}
	}
	return creds
}

func passkeyUserFromStore(ctx context.Context, store *config.Store, rpID string) *passkeyUser {
	return &passkeyUser{Credentials: loadPasskeyCredentials(ctx, store, rpID)}
}

// rpIDFromRequest extracts the rpID for the current request origin.
func rpIDFromRequest(r *http.Request) string {
	host := r.Host
	if fwdHost := r.Header.Get("X-Forwarded-Host"); fwdHost != "" {
		host = fwdHost
	}
	if idx := strings.LastIndex(host, ":"); idx > 0 {
		host = host[:idx]
	}
	if strings.HasSuffix(host, ".altclaw.ai") {
		return "altclaw.ai"
	}
	return host
}

func hasPasskeysForOrigin(ctx context.Context, store *config.Store, rpID string) bool {
	return len(loadPasskeyCredentials(ctx, store, rpID)) > 0
}

func hasPasskeys(ctx context.Context, store *config.Store) bool {
	entries, err := store.ListPasskeys(ctx)
	return err == nil && len(entries) > 0
}

// initWebAuthn creates a WebAuthn instance with the given origin/RPID.
func initWebAuthn(r *http.Request) (*webauthn.WebAuthn, error) {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if fwd := r.Header.Get("X-Forwarded-Proto"); fwd != "" {
		scheme = fwd
	}
	host := r.Host
	// Use forwarded host when request comes through CF Worker proxy
	if fwdHost := r.Header.Get("X-Forwarded-Host"); fwdHost != "" {
		host = fwdHost
	}
	// Extract hostname without port for RPID
	rpID := host
	if idx := strings.LastIndex(rpID, ":"); idx > 0 {
		rpID = rpID[:idx]
	}
	// Use parent domain for relay subdomains so passkeys work across all relays.
	if strings.HasSuffix(rpID, ".altclaw.ai") {
		rpID = "altclaw.ai"
	}

	// Build allowed origins list. Use the browser's Origin header (sent with
	// every POST) so we accept the actual browser origin, not the relay's
	// internal host. The rpId check already ensures domain-level security.
	origin := scheme + "://" + host
	origins := []string{origin}
	if browserOrigin := r.Header.Get("Origin"); browserOrigin != "" && browserOrigin != origin {
		origins = append(origins, browserOrigin)
	}

	return webauthn.New(&webauthn.Config{
		RPDisplayName: "Altclaw",
		RPID:          rpID,
		RPOrigins:     origins,
	})
}

// --- API Handlers ---

// PasskeyRegisterBegin starts passkey registration (requires auth).
func (a *Api) PasskeyRegisterBegin(w http.ResponseWriter, r *http.Request) any {
	wa, err := initWebAuthn(r)
	if err != nil {
		return restruct.Error{Status: http.StatusInternalServerError, Err: err}
	}

	user := passkeyUserFromStore(r.Context(), a.server.store, wa.Config.RPID)

	creation, session, err := wa.BeginRegistration(user,
		webauthn.WithResidentKeyRequirement(protocol.ResidentKeyRequirementRequired),
		webauthn.WithExclusions(webauthn.Credentials(user.WebAuthnCredentials()).CredentialDescriptors()),
	)
	if err != nil {
		return restruct.Error{Status: http.StatusInternalServerError, Err: err}
	}

	a.server.mu.Lock()
	a.server.passkeySession = session
	a.server.mu.Unlock()

	return creation
}

// PasskeyRegisterFinish completes passkey registration (requires auth).
func (a *Api) PasskeyRegisterFinish(w http.ResponseWriter, r *http.Request) any {
	wa, err := initWebAuthn(r)
	if err != nil {
		return restruct.Error{Status: http.StatusInternalServerError, Err: err}
	}

	a.server.mu.Lock()
	session := a.server.passkeySession
	a.server.passkeySession = nil
	a.server.mu.Unlock()

	if session == nil {
		return restruct.Error{Status: http.StatusBadRequest, Message: "no registration in progress"}
	}

	ctx := r.Context()
	user := passkeyUserFromStore(ctx, a.server.store, wa.Config.RPID)
	cred, err := wa.FinishRegistration(user, *session, r)
	if err != nil {
		return restruct.Error{Status: http.StatusInternalServerError, Err: err}
	}

	// Use name from query or default
	name := r.URL.Query().Get("name")
	if name == "" {
		name = "Passkey"
	}

	credJSON, _ := json.Marshal(cred)
	entry := &config.PasskeyEntry{
		Name:           name,
		RPID:           wa.Config.RPID,
		CredentialData: string(credJSON),
	}
	if err := a.server.store.AddPasskey(ctx, entry); err != nil {
		return restruct.Error{Status: http.StatusInternalServerError, Err: err}
	}

	return map[string]string{"status": "ok"}
}

// PasskeyLoginBegin starts passkey login (no auth required).
func (a *Api) PasskeyLoginBegin(w http.ResponseWriter, r *http.Request) any {
	if !hasPasskeys(r.Context(), a.server.store) {
		return restruct.Error{Status: http.StatusNotFound, Message: "no passkeys registered"}
	}

	wa, err := initWebAuthn(r)
	if err != nil {
		return restruct.Error{Status: http.StatusInternalServerError, Err: err}
	}

	assertion, session, err := wa.BeginDiscoverableLogin()
	if err != nil {
		return restruct.Error{Status: http.StatusInternalServerError, Err: err}
	}

	a.server.mu.Lock()
	a.server.passkeySession = session
	a.server.mu.Unlock()

	return assertion
}

// PasskeyLoginFinish completes passkey login and creates a session (no auth required).
func (a *Api) PasskeyLoginFinish(w http.ResponseWriter, r *http.Request) any {
	wa, err := initWebAuthn(r)
	if err != nil {
		return restruct.Error{Status: http.StatusInternalServerError, Err: err}
	}

	a.server.mu.Lock()
	session := a.server.passkeySession
	a.server.passkeySession = nil
	a.server.mu.Unlock()

	if session == nil {
		return restruct.Error{Status: http.StatusBadRequest, Message: "no login in progress"}
	}

	store := a.server.store
	ctx := r.Context()
	handler := func(rawID, userHandle []byte) (webauthn.User, error) {
		return passkeyUserFromStore(ctx, store, wa.Config.RPID), nil
	}

	_, validatedCred, err := wa.FinishPasskeyLogin(handler, *session, r)
	if err != nil {
		return restruct.Error{Status: http.StatusUnauthorized, Message: "passkey authentication failed"}
	}

	// Update credential metadata in dsorm
	updatePasskeyCredential(ctx, store, validatedCred)

	// Create session
	a.server.createSession(w, r, 86400*7)

	return map[string]string{"status": "ok"}
}

// Passkeys lists registered passkeys (requires auth).
func (a *Api) Passkeys(ctx context.Context) any {
	entries, err := a.server.store.ListPasskeys(ctx)
	if err != nil {
		return restruct.Error{Status: http.StatusInternalServerError, Err: err}
	}

	type entry struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		CreatedAt string `json:"created_at"`
	}
	var result []entry
	for _, e := range entries {
		result = append(result, entry{
			ID:        fmt.Sprintf("%d", e.ID),
			Name:      e.Name,
			CreatedAt: e.CreatedAt.Format(time.RFC3339),
		})
	}
	return map[string]any{"passkeys": result, "has_passkeys": len(entries) > 0}
}

// DeletePasskey removes a passkey by ID (requires auth).
func (a *Api) DeletePasskey(r *http.Request) any {
	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return restruct.Error{Status: http.StatusBadRequest}
	}
	id, err := strconv.ParseInt(req.ID, 10, 64)
	if err != nil {
		return restruct.Error{Status: http.StatusBadRequest, Message: "invalid ID"}
	}
	if err := a.server.store.DeletePasskey(r.Context(), id); err != nil {
		return restruct.Error{Status: http.StatusInternalServerError, Err: err}
	}
	return map[string]string{"status": "ok"}
}

// HasPasskeys returns whether passkeys are registered for the current origin (no auth required, used by login page).
func (a *Api) HasPasskeys(r *http.Request) any {
	rpID := rpIDFromRequest(r)
	return map[string]bool{"has_passkeys": hasPasskeysForOrigin(r.Context(), a.server.store, rpID)}
}

// updatePasskeyCredential updates a stored passkey's credential data after login.
func updatePasskeyCredential(ctx context.Context, store *config.Store, cred *webauthn.Credential) {
	entries, err := store.ListPasskeys(ctx)
	if err != nil {
		return
	}
	for _, e := range entries {
		var storedCred webauthn.Credential
		if err := json.Unmarshal([]byte(e.CredentialData), &storedCred); err != nil {
			continue
		}
		if bytes.Equal(storedCred.ID, cred.ID) {
			credJSON, _ := json.Marshal(cred)
			e.CredentialData = string(credJSON)
			_ = store.AddPasskey(ctx, e) // upsert
			return
		}
	}
}
