package executor

import (
	"context"
	"os/exec"
	"syscall"
)

// hideWindow sets SysProcAttr on a command to prevent a visible console
// window from flashing on screen. This is essential when running as a
// GUI application (Wails) on Windows — without it every exec.Command
// briefly shows a cmd.exe window.
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}

// command wraps exec.Command and hides the console window on Windows.
func command(name string, arg ...string) *exec.Cmd {
	cmd := exec.Command(name, arg...)
	hideWindow(cmd)
	return cmd
}

// commandContext wraps exec.CommandContext and hides the console window on Windows.
func commandContext(ctx context.Context, name string, arg ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, arg...)
	hideWindow(cmd)
	return cmd
}
