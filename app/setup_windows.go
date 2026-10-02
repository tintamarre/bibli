package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"

	winsetup "bibli/packaging/windows"
)

// runSetup runs the installer when it was asked for: "bibli.exe install", or
// a double-click from Explorer. Anything else (flags, the service manager, a
// terminal) starts the server as usual.
func runSetup() (code int, ran bool) {
	var extra []string
	switch {
	case len(os.Args) > 1 && os.Args[1] == "install":
		extra = os.Args[2:]
	case len(os.Args) == 1 && doubleClicked():
		// A double-click asks before installing anything (install.ps1).
	default:
		return 0, false
	}
	if err := setup(extra); err != nil {
		var exit *osexec.ExitError
		if errors.As(err, &exit) {
			return exit.ExitCode(), true
		}
		fmt.Fprintln(os.Stderr, err)
		return 1, true
	}
	return 0, true
}

// doubleClicked reports whether Windows opened a console for this process
// alone, which is what launching it from Explorer does. Started from a
// terminal, the console is shared with the shell; as a service, there is none.
func doubleClicked() bool {
	proc := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetConsoleProcessList")
	// It returns how many processes share the console, 0 without one.
	var pids [2]uint32
	n, _, _ := proc.Call(uintptr(unsafe.Pointer(&pids[0])), uintptr(len(pids)))
	return n == 1
}

// setup writes the embedded scripts (packaging/windows): bibli.exe is the whole
// download. It writes the scripts to a temporary folder and runs install.ps1 there,
// in this console, until it is done.
func setup(extra []string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "bibli-setup-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	if err := writeSetupFiles(dir); err != nil {
		return err
	}
	args := append([]string{"-NoProfile", "-ExecutionPolicy", "Bypass",
		"-File", filepath.Join(dir, "install.ps1"), "-Exe", exe}, extra...)
	cmd := osexec.Command("powershell.exe", args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

// writeSetupFiles copies the embedded files into dir. Windows PowerShell 5
// reads a script without a BOM in the ANSI code page, which garbles the
// accents, and Notepad wants CRLF: the scripts get both, whatever line endings
// the checkout gave them.
func writeSetupFiles(dir string) error {
	entries, err := fs.ReadDir(winsetup.Files, ".")
	if err != nil {
		return err
	}
	for _, e := range entries {
		b, err := winsetup.Files.ReadFile(e.Name())
		if err != nil {
			return err
		}
		if strings.HasSuffix(e.Name(), ".ps1") {
			lf := strings.ReplaceAll(string(b), "\r\n", "\n")
			b = []byte("\xef\xbb\xbf" + strings.ReplaceAll(lf, "\n", "\r\n"))
		}
		if err := os.WriteFile(filepath.Join(dir, e.Name()), b, 0o644); err != nil {
			return err
		}
	}
	return nil
}
