package util

import (
	"net"
	"net/http"
	"strings"
)

// ClientIP extracts the real client IP from an HTTP request.
// Priority: X-Client-IP (set by CF Worker proxy) → X-Forwarded-For (first hop) → RemoteAddr.
func ClientIP(r *http.Request) string {
	// Preferred: set explicitly by our CF Worker proxy
	if ip := r.Header.Get("X-Client-IP"); ip != "" {
		return strings.TrimSpace(ip)
	}

	// Fallback: standard proxy header (first IP in chain)
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		if idx := strings.IndexByte(fwd, ','); idx > 0 {
			return strings.TrimSpace(fwd[:idx])
		}
		return strings.TrimSpace(fwd)
	}

	// Direct connection
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// MatchIPWhitelist checks if the given IP matches any entry in the whitelist.
// Entries can be exact IPs (e.g. "1.2.3.4") or CIDR ranges (e.g. "192.168.1.0/24").
func MatchIPWhitelist(clientIP string, whitelist []string) bool {
	if len(whitelist) == 0 {
		return true // no whitelist = allow all
	}

	ip := net.ParseIP(clientIP)

	for _, entry := range whitelist {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}

		// Try CIDR first
		if strings.Contains(entry, "/") {
			_, cidr, err := net.ParseCIDR(entry)
			if err == nil && ip != nil && cidr.Contains(ip) {
				return true
			}
			continue
		}

		// Exact IP match
		if entry == clientIP {
			return true
		}
	}
	return false
}
