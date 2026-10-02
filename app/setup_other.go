//go:build !windows

package main

// runSetup only does something on Windows (setup_windows.go).
func runSetup() (code int, ran bool) { return 0, false }
