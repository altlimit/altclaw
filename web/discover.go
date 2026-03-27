package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"altclaw.ai/internal/config"
)

// discoverResult holds the relay address, assigned hostname, and optional
// hub profile returned by /api/discover.
type discoverResult struct {
	TCPAddr  string          `json:"tcp_addr"`
	Hostname string          `json:"hostname"`
	Domain   string          `json:"domain"`
	Profile  *config.Profile `json:"profile,omitempty"`
}

// discoverRelay calls the hub's /api/discover endpoint with the workspace token.
// The hub assigns a hostname (reserved or random), picks the best relay,
// pre-authorizes the hostname on the relay, and returns everything.
// secretPublicKey is the instance's P-256 ECDH public key (hex) for encrypting secrets.
func discoverRelay(token string, secretPublicKey string) (*discoverResult, error) {
	client := &http.Client{Timeout: 10 * time.Second}

	payload := map[string]string{"token": token}
	if secretPublicKey != "" {
		payload["secret_public_key"] = secretPublicKey
	}
	body, _ := json.Marshal(payload)
	resp, err := client.Post(hubHTTPURL()+"/api/discover", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("discover relay: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("discover relay: status %d", resp.StatusCode)
	}

	var result discoverResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("discover relay: %w", err)
	}
	if result.TCPAddr == "" {
		return nil, fmt.Errorf("discover relay: no relays available")
	}
	return &result, nil
}
