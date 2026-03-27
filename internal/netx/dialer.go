package netx

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"
)

// allowedLoopbackPorts contains ports that are exempt from SSRF protection.
// Used to allow the agent to call its own server (e.g., /mcp endpoint).
var allowedLoopbackPorts = make(map[string]bool)

// AllowLoopbackPort whitelists a port for loopback connections (SSRF bypass).
// Call this when the web server starts so the agent can fetch from itself.
func AllowLoopbackPort(port string) {
	allowedLoopbackPorts[port] = true
}

// allowedHosts is the set of hosts that bypass SSRF protection.
// When non-empty, ONLY these hosts are allowed (strict whitelist).
// When empty, default behavior allows everything except loopback/private/metadata.
var (
	allowedHosts   map[string]bool
	allowedHostsMu sync.RWMutex
)

// SetAllowedHosts configures the host whitelist for SSRF protection.
// When hosts is non-empty, only those hosts are allowed for outbound requests.
// When hosts is empty/nil, default protection applies (block loopback/private/metadata).
func SetAllowedHosts(hosts []string) {
	allowedHostsMu.Lock()
	defer allowedHostsMu.Unlock()
	if len(hosts) == 0 {
		allowedHosts = nil
		return
	}
	m := make(map[string]bool, len(hosts))
	for _, h := range hosts {
		h = strings.TrimSpace(strings.ToLower(h))
		if h != "" {
			m[h] = true
		}
	}
	allowedHosts = m
}

// isHostAllowed checks whether a host is permitted by the current whitelist.
// Returns true if allowed, false if blocked.
func isHostAllowed(host string) bool {
	allowedHostsMu.RLock()
	wl := allowedHosts
	allowedHostsMu.RUnlock()

	if wl == nil {
		return true // no whitelist → default SSRF rules apply
	}
	return wl[strings.ToLower(host)]
}

// SafeDialer creates a network connection but explicitly rejects connections
// to local subnets, loopback, or cloud metadata IP addresses (SSRF protection).
// When an allowed-hosts whitelist is set, only those hosts are permitted.
func SafeDialer(ctx context.Context, network, addr string) (net.Conn, error) {
	// Extract host from addr (might be host:port)
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}

	// Check strict whitelist first (if configured)
	if !isHostAllowed(host) {
		return nil, fmt.Errorf("access to %s is restricted (host not in allowed list)", host)
	}

	// Resolve IP
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, fmt.Errorf("DNS resolution failed: %v", err)
	}

	if len(ips) == 0 {
		return nil, fmt.Errorf("no IP addresses found for host")
	}

	// Take first IP and validate it
	ip := ips[0]

	// Block loopback, private networks, and link-local (metadata services)
	// Allow whitelisted loopback ports (e.g., the app's own /mcp endpoint)
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() {
		if !ip.IsLoopback() || !allowedLoopbackPorts[port] {
			return nil, fmt.Errorf("access to local/private network is restricted (SSRF protection)")
		}
	}

	// Explicitly block AWS metadata IP just in case
	if ip.String() == "169.254.169.254" {
		return nil, fmt.Errorf("access to cloud metadata service is restricted")
	}

	// Reconstruct the address using the actual checked IP
	var safeAddr string
	if port != "" { // we split it successfully earlier
		safeAddr = net.JoinHostPort(ip.String(), port)
	} else {
		safeAddr = net.JoinHostPort(ip.String(), "80")
	}

	dialer := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	return dialer.DialContext(ctx, network, safeAddr)
}

// ValidateURL checks whether a URL's host resolves to a safe (non-private) IP.
// Returns an error if the host resolves to loopback, private, link-local,
// or cloud metadata addresses. Used to protect browser navigation from SSRF.
// Also enforces the allowed-hosts whitelist when configured.
func ValidateURL(rawURL string) error {
	host := rawURL

	// Strip scheme
	if i := strings.Index(host, "://"); i >= 0 {
		host = host[i+3:]
	}
	// Strip path/query
	if i := strings.IndexAny(host, "/?#"); i >= 0 {
		host = host[:i]
	}
	// Strip port
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}

	if host == "" {
		return fmt.Errorf("empty host in URL")
	}

	// Check strict whitelist first (if configured)
	if !isHostAllowed(host) {
		return fmt.Errorf("access to %s is restricted (host not in allowed list)", host)
	}

	ips, err := net.DefaultResolver.LookupNetIP(context.Background(), "ip", host)
	if err != nil {
		return fmt.Errorf("DNS resolution failed: %v", err)
	}
	if len(ips) == 0 {
		return fmt.Errorf("no IP addresses found for host %q", host)
	}

	ip := ips[0]
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() {
		return fmt.Errorf("access to %s is restricted (SSRF protection)", host)
	}
	if ip.String() == "169.254.169.254" {
		return fmt.Errorf("access to cloud metadata service is restricted")
	}

	return nil
}
