// Package buildinfo holds build-time configuration that can be overridden
// via ldflags. Kept in its own package so any internal package can import
// it without circular dependencies.
package buildinfo

// Version is the build version string.
// Override at build time:
//
//	go build -ldflags "-X 'altclaw.ai/internal/buildinfo.Version=v2026.03.25'"
var Version = "dev"

var HubURL = "https://hub.altclaw.ai"

func init() {
	if Version == "dev" {
		HubURL = "http://localhost:8787"
	}
}
