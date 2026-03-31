//go:build !windows

package bridge

import (
	"fmt"
	"os/exec"
)

// killChromeForProfile kills any Chrome processes using the given user-data-dir.
// On Linux/macOS, uses pkill to find and terminate matching processes.
// Best-effort — all errors are silenced since this is a recovery heuristic.
func killChromeForProfile(dataDir string) {
	_ = exec.Command("pkill", "-f", fmt.Sprintf("--user-data-dir=%s", dataDir)).Run()
}
