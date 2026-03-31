package bridge

import (
	"fmt"
	"os/exec"
	"strings"
	"syscall"
)

// killChromeForProfile kills any Chrome processes using the given user-data-dir.
// On Windows, uses wmic to find chrome.exe processes whose command line contains
// the data dir path, then taskkill to terminate them.
// All commands use HideWindow to avoid console flashes in GUI mode.
// Best-effort — all errors are silenced since this is a recovery heuristic.
func killChromeForProfile(dataDir string) {
	// Backslashes in the path need to be escaped for WMI LIKE queries.
	escaped := strings.ReplaceAll(dataDir, `\`, `\\`)
	query := fmt.Sprintf("name='chrome.exe' AND commandline like '%%%s%%'", escaped)
	cmd := exec.Command("wmic", "process", "where", query, "get", "processid", "/format:list")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.Output()
	if err != nil {
		return
	}
	// Parse PIDs from "ProcessId=12345" lines
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "ProcessId=") {
			pid := strings.TrimPrefix(line, "ProcessId=")
			pid = strings.TrimSpace(pid)
			if pid != "" {
				kill := exec.Command("taskkill", "/F", "/PID", pid)
				kill.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
				_ = kill.Run()
			}
		}
	}
}
