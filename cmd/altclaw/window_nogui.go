//go:build !gui

package main

import (
	"fmt"

	"altclaw.ai/internal/config"
)

func startGUI(store *config.Store, workspace, addr string) error {
	return fmt.Errorf("GUI mode not available — rebuild with: go build -tags gui ./cmd/altclaw/")
}
