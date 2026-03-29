//go:build !windows

package executor

import (
	"context"
	"os/exec"
)

// hideWindow is a no-op on non-Windows platforms.
func hideWindow(_ *exec.Cmd) {}

// command wraps exec.Command (no-op on non-Windows).
func command(name string, arg ...string) *exec.Cmd {
	return exec.Command(name, arg...)
}

// commandContext wraps exec.CommandContext (no-op on non-Windows).
func commandContext(ctx context.Context, name string, arg ...string) *exec.Cmd {
	return exec.CommandContext(ctx, name, arg...)
}
