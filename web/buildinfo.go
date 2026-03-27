package web

import "altclaw.ai/internal/buildinfo"

// hubHTTPURL returns the hub HTTP URL from the buildinfo package.
// This is a convenience alias so existing code in the web package
// doesn't need to change.
func hubHTTPURL() string {
	return buildinfo.HubURL
}
