//go:build !windows

package main

// listDrives is a no-op on non-Windows platforms.
func listDrives() []string {
	return nil
}
