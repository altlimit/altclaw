package bridge

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SanitizePath ensures the given path is within the workspace directory.
// Returns the cleaned absolute path, or an error if the path escapes the workspace.
// Handles symlinks, non-existent paths (evaluates the parent), and both
// relative and absolute inputs.
func SanitizePath(workspace, path string) (string, error) {
	var abs string
	if filepath.IsAbs(path) {
		abs = filepath.Clean(path)
	} else {
		abs = filepath.Clean(filepath.Join(workspace, path))
	}

	// Resolve any symlinks to get the real path.
	// EvalSymlinks returns an error if the path doesn't exist, which is fine
	// for reading, but for writing to a new file, we must evaluate the directory.
	realPath, err := filepath.EvalSymlinks(abs)
	if err != nil {
		if os.IsNotExist(err) {
			parent := filepath.Dir(abs)
			realParent, err := filepath.EvalSymlinks(parent)
			if err != nil && !os.IsNotExist(err) {
				return "", fmt.Errorf("invalid path: %v", err)
			}
			if err == nil {
				realPath = filepath.Join(realParent, filepath.Base(abs))
			} else {
				realPath = abs
			}
		} else {
			return "", fmt.Errorf("invalid path: %v", err)
		}
	}

	// Ensure the workspace root itself is fully evaluated
	realWorkspace, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		realWorkspace = workspace
	}

	// Verify the resolved path is within the workspace
	rel, err := filepath.Rel(realWorkspace, realPath)
	if err != nil {
		return "", fmt.Errorf("invalid path: %s", path)
	}
	if strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("path escapes workspace: %s", path)
	}
	return abs, nil
}
