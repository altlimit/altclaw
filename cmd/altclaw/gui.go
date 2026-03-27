package main

import (
	osexec "os/exec"
	"runtime"
)

// openBrowser opens the given URL in the user's default browser using
// OS-specific commands.
func openBrowser(url string) {
	var cmd *osexec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = osexec.Command("open", url)
	case "windows":
		cmd = osexec.Command("cmd", "/c", "start", "", url)
	default: // linux, freebsd
		cmd = osexec.Command("xdg-open", url)
	}
	// Fire-and-forget — don't block the server
	_ = cmd.Start()
}
