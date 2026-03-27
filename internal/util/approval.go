package util

import "strings"

// IsApproved returns true if the given answer string indicates user approval.
// Accepted values (case-insensitive, whitespace-trimmed): "yes", "y", "approve".
func IsApproved(answer string) bool {
	v := strings.TrimSpace(strings.ToLower(answer))
	return v == "yes" || v == "y" || v == "approve"
}
