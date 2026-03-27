package tui

import (
	"context"
	"fmt"

	"altclaw.ai/internal/bridge"
	"altclaw.ai/internal/config"
)

// tuiConfirmCtx implements bridge.ConfirmContext for TUI mode.
// Supports store-based operations (provider CRUD, settings, config).
// Server-only operations (tunnel, module marketplace) return errors.
type tuiConfirmCtx struct {
	store        *config.Store
	ws           *config.Workspace
	rebuildAgent func()
}

var _ bridge.ConfirmContext = (*tuiConfirmCtx)(nil)

func NewConfirmContext(store *config.Store, ws *config.Workspace, rebuildAgent func()) bridge.ConfirmContext {
	return &tuiConfirmCtx{store: store, ws: ws, rebuildAgent: rebuildAgent}
}

func (c *tuiConfirmCtx) Store() *config.Store               { return c.store }
func (c *tuiConfirmCtx) Workspace() *config.Workspace        { return c.ws }
func (c *tuiConfirmCtx) BroadcastCtx() context.Context       { return context.Background() }
func (c *tuiConfirmCtx) RebuildAgent()                       { if c.rebuildAgent != nil { c.rebuildAgent() } }

func (c *tuiConfirmCtx) TunnelConnect() (map[string]any, error) {
	return nil, fmt.Errorf("tunnel operations not available in TUI mode")
}
func (c *tuiConfirmCtx) TunnelDisconnect() error {
	return fmt.Errorf("tunnel operations not available in TUI mode")
}
func (c *tuiConfirmCtx) TunnelPair(code string) (map[string]any, error) {
	return nil, fmt.Errorf("tunnel operations not available in TUI mode")
}
func (c *tuiConfirmCtx) TunnelUnpair() error {
	return fmt.Errorf("tunnel operations not available in TUI mode")
}
func (c *tuiConfirmCtx) ModuleInstall(id, scope string) (map[string]any, error) {
	return nil, fmt.Errorf("module marketplace not available in TUI mode")
}
func (c *tuiConfirmCtx) ModuleRemove(id, scope string) (map[string]any, error) {
	return nil, fmt.Errorf("module marketplace not available in TUI mode")
}
