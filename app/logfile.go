package main

import (
	"bytes"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// maxLogSize is where -log-file rotates: the current file becomes <name>.1, so
// a server left running for years keeps two files at most.
const maxLogSize = 5 << 20

// logFile is the -log-file destination. Its lock is for clear, which runs
// beside the log package's own serialised writes.
type logFile struct {
	mu   sync.Mutex
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
	l.mu.Lock()
	defer l.mu.Unlock()
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

// recentLog keeps the last lines of the log in memory, so the settings screen
// can show them and hand them out wherever the log goes: a terminal, Docker,
// a file. Lost on restart, like Docker's own log on an update.
var recentLog = &logRing{max: 2000}

// recentLogShown is how many of them the settings screen shows.
const recentLogShown = 200

var ansiCode = regexp.MustCompile(`\x1b\[[0-9;]*m`)

type logRing struct {
	mu    sync.Mutex
	max   int
	lines []string
}

// Write takes one log entry per call, as the log package writes them. The
// fetches of static files are left to the file: here they would bury the rest.
func (l *logRing) Write(p []byte) (int, error) {
	line := ansiCode.ReplaceAllString(strings.TrimRight(string(p), "\n"), "")
	if strings.Contains(line, " GET /static/") {
		return len(p), nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.lines) >= l.max {
		l.lines = append(l.lines[:0], l.lines[len(l.lines)-l.max+1:]...)
	}
	l.lines = append(l.lines, line)
	return len(p), nil
}

func (l *logRing) clear() {
	l.mu.Lock()
	l.lines = nil
	l.mu.Unlock()
}

// last returns the n most recent entries, oldest first.
func (l *logRing) last(n int) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if n > len(l.lines) {
		n = len(l.lines)
	}
	return append([]string(nil), l.lines[len(l.lines)-n:]...)
}

// logClear empties the log, in memory and in the file, so what follows can be
// handed out on its own. The line it leaves says so.
func (a *app) logClear(w http.ResponseWriter, r *http.Request) {
	recentLog.clear()
	if a.logFile != nil {
		if err := a.logFile.clear(); err != nil {
			log.Printf("log clear: %v", err)
		}
	}
	log.Print("log cleared from the settings")
	http.Redirect(w, r, "/settings?tab=installation", http.StatusSeeOther)
}

// logDownload hands out the log: the -log-file files when there are some, as
// they reach further back, else the lines kept in memory.
func (a *app) logDownload(w http.ResponseWriter, r *http.Request) {
	var b bytes.Buffer
	if a.logPath != "" {
		for _, p := range []string{a.logPath + ".1", a.logPath} {
			if data, err := os.ReadFile(p); err == nil {
				b.Write(ansiCode.ReplaceAll(data, nil))
			}
		}
	} else {
		for _, line := range recentLog.last(recentLog.max) {
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}
	name := "bibli-log-" + time.Now().Format("2006-01-02") + ".txt"
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Cache-Control", "no-store")
	w.Write(b.Bytes())
}

// clear empties the file and drops the rotated one.
func (l *logFile) clear() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := os.Remove(l.path + ".1"); err != nil && !os.IsNotExist(err) {
		return err
	}
	l.size = 0
	return l.f.Truncate(0)
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
