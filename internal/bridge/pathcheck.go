package bridge

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"altclaw.ai/internal/config"
	"altclaw.ai/internal/util"
	"github.com/bmatcuk/doublestar/v4"
	"github.com/dop251/goja"
)

// IsHidden reports whether absPath contains any hidden (dot-prefixed) component
// after the workspace root, excluding the agent's own working directory (.agent/).
func IsHidden(workspace, absPath string) bool {
	rel, err := filepath.Rel(workspace, absPath)
	if err != nil {
		return false
	}
	// Normalize to forward slashes for consistent handling
	rel = filepath.ToSlash(rel)
	parts := strings.Split(rel, "/")
	for _, part := range parts {
		if strings.HasPrefix(part, ".") && part != "." && part != ".agent" {
			return true
		}
	}
	return false
}

// IsIgnored reports whether absPath is matched by any rule in the workspace-root
// .gitignore or .agentignore files. Best-effort: missing files are silently skipped.
func IsIgnored(workspace, absPath string) bool {
	rel, err := filepath.Rel(workspace, absPath)
	if err != nil {
		return false
	}
	rel = filepath.ToSlash(rel)

	for _, ignoreFile := range []string{".gitignore", ".agentignore"} {
		if matchesIgnoreFile(filepath.Join(workspace, ignoreFile), rel) {
			return true
		}
	}
	return false
}

// matchesIgnoreFile checks whether relPath matches any non-comment, non-empty
// pattern in ignoreFilePath. Patterns use doublestar glob semantics.
func matchesIgnoreFile(ignoreFilePath, relPath string) bool {
	f, err := os.Open(ignoreFilePath)
	if err != nil {
		return false // file missing or unreadable — skip silently
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Support both "*.log" and "**/*.log" style patterns.
		// If pattern has no slash, match against the basename component; otherwise match full rel path.
		pattern := line
		if !strings.Contains(pattern, "/") {
			// bare name: match against any path segment
			if ok, _ := doublestar.Match(pattern, filepath.Base(relPath)); ok {
				return true
			}
			if ok, _ := doublestar.Match("**/"+pattern, relPath); ok {
				return true
			}
		} else {
			pattern = strings.TrimPrefix(pattern, "/")
			if ok, _ := doublestar.Match(pattern, relPath); ok {
				return true
			}
		}
	}
	return false
}

// CheckRestricted enforces the workspace file security gate.
// When ws.EffectiveIgnoreRestricted is false (the secure default), and the given
// absPath is hidden or matched by .gitignore/.agentignore, the user is prompted
// via handler.Ask. If the user rejects, a JS exception is thrown.
//
// ws and handler may be nil (e.g. in tests) — the gate is simply skipped.
func CheckRestricted(vm *goja.Runtime, workspace, absPath, op string, ws *config.Workspace, handler UIHandler, store ...*config.Store) {
	if ws == nil || handler == nil {
		return // gate disabled or no handler
	}
	if len(store) > 0 && store[0] != nil {
		if store[0].Settings().IgnoreRestricted() {
			return // gate disabled
		}
	} else if ws.IgnoreRestricted {
		return // gate disabled (no store fallback)
	}

	reason := ""
	if IsHidden(workspace, absPath) {
		reason = "hidden file"
	} else if IsIgnored(workspace, absPath) {
		reason = "gitignored/agentignored file"
	}
	if reason == "" {
		return
	}

	rel, err := filepath.Rel(workspace, absPath)
	if err != nil {
		rel = absPath
	}

	answer := handler.Ask(fmt.Sprintf(
		"%s on %q (%s) — allow access? (yes/no)", op, rel, reason,
	))
	if !util.IsApproved(answer) {
		Throwf(vm, "user rejected: %s on %s", op, rel)
	}
}
