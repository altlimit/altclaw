package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
)

// ── Context key for relay transport ─────────────────────────────────────────

type relayCtxKey struct{}

// RelayConfig holds the relay connection details injected via context.
type RelayConfig struct {
	ForwardURL  string // e.g. "http://relay1.altclaw.ai/forward"
	TunnelHost  string // raw relay key (result.Hostname, e.g. 'abc123') — must match r.tunnels key
	TunnelToken string // workspace token, matched against relay tunnel session
	AuthEnc     string // AES-GCM encrypted API key blob (provider.APIKey for InMemory providers)
}

// WithRelay returns a context that causes relay-aware http.Clients to route
// requests through the hub relay /forward endpoint instead of directly.
func WithRelay(ctx context.Context, cfg RelayConfig) context.Context {
	return context.WithValue(ctx, relayCtxKey{}, cfg)
}

// relayFromContext extracts the RelayConfig from ctx, if set.
func relayFromContext(ctx context.Context) (RelayConfig, bool) {
	v, ok := ctx.Value(relayCtxKey{}).(RelayConfig)
	return v, ok && v.ForwardURL != ""
}

// ── Relay-aware RoundTripper ─────────────────────────────────────────────────

// RelayTransport is an http.RoundTripper that, when the request context carries
// a RelayConfig, forwards the request through the hub relay /forward endpoint.
//
// The relay decrypts the API key by:
//   - Scanning all header values for the literal auth_enc ciphertext
//   - Scanning all query-string parameter values for the same
//   - Replacing each occurrence with the decrypted plaintext key
//
// This approach works for every provider regardless of how they place their key
// (Authorization: Bearer, x-api-key, ?key=..., etc.) and requires no
// provider-specific knowledge in the relay.
//
// When no RelayConfig is in the context, requests pass through to the inner
// transport unchanged (standard outbound HTTP).
type RelayTransport struct {
	Inner http.RoundTripper // fallback transport; defaults to http.DefaultTransport
}

// RoundTrip implements http.RoundTripper.
func (rt *RelayTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	cfg, ok := relayFromContext(req.Context())
	if !ok {
		return rt.inner().RoundTrip(req)
	}
	return rt.forwardViaRelay(req, cfg)
}

func (rt *RelayTransport) inner() http.RoundTripper {
	if rt.Inner != nil {
		return rt.Inner
	}
	return http.DefaultTransport
}

func (rt *RelayTransport) forwardViaRelay(req *http.Request, cfg RelayConfig) (*http.Response, error) {
	// Capture the request body.
	var bodyBytes []byte
	if req.Body != nil {
		var err error
		bodyBytes, err = io.ReadAll(req.Body)
		if err != nil {
			return nil, fmt.Errorf("relay: read body: %w", err)
		}
		req.Body.Close()
	}

	// Copy headers and query params. The relay will find any value that equals
	// the TunnelToken (which for InMemory providers IS the auth_enc ciphertext)
	// and replace it with the decrypted key.
	headers := make(map[string]string, len(req.Header))
	for k, vv := range req.Header {
		if len(vv) > 0 {
			headers[k] = vv[0]
		}
	}

	// Include query params as synthetic headers so the relay can replace them.
	// The relay's entry point is a single URL (our ForwardURL), not the target
	// URL, so we embed the target URL and query string in the forward payload.
	targetURL := req.URL.String()

	fwd := forwardPayload{
		Endpoint: targetURL,
		Method:   req.Method,
		AuthEnc:  cfg.AuthEnc, // encrypted API key blob; relay decrypts
		Headers:  headers,
		Body:     bodyBytes,
	}

	fwdData, err := json.Marshal(fwd)
	if err != nil {
		return nil, fmt.Errorf("relay: marshal forward request: %w", err)
	}

	relayReq, err := http.NewRequestWithContext(req.Context(), http.MethodPost, cfg.ForwardURL, bytes.NewReader(fwdData))
	if err != nil {
		return nil, fmt.Errorf("relay: build relay request: %w", err)
	}
	relayReq.Header.Set("Content-Type", "application/json")
	relayReq.Header.Set("X-Tunnel-Host", cfg.TunnelHost)
	relayReq.Header.Set("X-Tunnel-Token", cfg.TunnelToken)
	slog.Debug("relay forward", "url", cfg.ForwardURL, "host", cfg.TunnelHost, "has_token", cfg.TunnelToken != "", "has_auth", cfg.AuthEnc != "")

	return rt.inner().RoundTrip(relayReq)
}

// forwardPayload matches the forwardRequest struct in hub/hub.go.
type forwardPayload struct {
	Endpoint string            `json:"endpoint"`
	Method   string            `json:"method"`
	AuthEnc  string            `json:"auth_enc"`
	Headers  map[string]string `json:"headers,omitempty"`
	Body     json.RawMessage   `json:"body,omitempty"`
}

// ── Shared relay-aware client ────────────────────────────────────────────────

// NewRelayClient returns an *http.Client whose transport transparently routes
// requests through the hub relay when the context carries a RelayConfig.
// Providers should use this instead of &http.Client{}.
func NewRelayClient() *http.Client {
	return &http.Client{
		Transport: &RelayTransport{},
	}
}
