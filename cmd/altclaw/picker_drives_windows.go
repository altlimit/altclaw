//go:build windows

package main

import "os"

// listDrives returns available Windows drive letters (e.g. ["C:\\", "D\\"]).
func listDrives() []string {
	var drives []string
	for c := 'A'; c <= 'Z'; c++ {
		root := string(c) + ":\\"
		if _, err := os.Stat(root); err == nil {
			drives = append(drives, root)
		}
	}
	return drives
}
