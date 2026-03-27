// Package config handles Altclaw configuration backed by dsorm (local SQLite).
// Default config dir: ~/.altclaw
package config

import (
	"os"
	"path/filepath"
)

// configDirOverride is set by SetConfigDir to override the default config directory.
var configDirOverride string

// SetConfigDir overrides the default config directory.
// Call this early (e.g. from CLI flag parsing) so all derived paths
// (memory, history, browser-data) use the custom directory.
func SetConfigDir(dir string) {
	configDirOverride = dir
}

// ConfigDir returns the altclaw config directory path.
// Returns the override if set, otherwise defaults to ~/.altclaw.
func ConfigDir() string {
	if configDirOverride != "" {
		return configDirOverride
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".altclaw"
	}
	return filepath.Join(home, ".altclaw")
}

// MemoryPath returns the path to the user-level memory file.
func MemoryPath() string {
	return filepath.Join(ConfigDir(), "memory.md")
}
