package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// maxLogSize is where -log-file rotates: the current file becomes <name>.1, so
// a server left running for years keeps two files at most.
const maxLogSize = 5 << 20

// logFile is the -log-file destination. The log package serialises writes, so
// it needs no lock of its own.
type logFile struct {
	path string
	max  int64
	f    *os.File
	size int64
}

func openLogFile(path string, max int64) (*logFile, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	return &logFile{path: path, max: max, f: f, size: st.Size()}, nil
}

func (l *logFile) Write(p []byte) (int, error) {
	if l.size > 0 && l.size+int64(len(p)) > l.max {
		l.f.Close()
		if err := os.Rename(l.path, l.path+".1"); err != nil {
			fmt.Fprintf(os.Stderr, "log rotation: %v\n", err)
		}
		f, err := os.OpenFile(l.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return 0, err
		}
		l.f, l.size = f, 0
	}
	n, err := l.f.Write(p)
	l.size += int64(n)
	return n, err
}

// readPasswordFile reads -password-file: the whole file is the password, minus
// a UTF-8 BOM (Windows editors add one) and the final line break.
func readPasswordFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(strings.TrimPrefix(string(b), "\ufeff"), "\r\n"), nil
}
