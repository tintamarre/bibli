//go:build !windows

package main

import "os"

// runAsService only does something on Windows (service_windows.go).
func runAsService(stop chan<- os.Signal) func() { return func() {} }
