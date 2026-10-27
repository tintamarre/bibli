package main

import (
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The log's ResponseWriter wrapper must forward Flush and Unwrap, or the
// cataloguing stream is never flushed.
func TestStatusRecorderStaysFlushableAndUnwrappable(t *testing.T) {
	rr := httptest.NewRecorder()
	rec := &statusRecorder{ResponseWriter: rr}

	if _, ok := any(rec).(http.Flusher); !ok {
		t.Error("the wrapper is not an http.Flusher: SSE cataloguing would never flush")
	}
	unwrapper, ok := any(rec).(interface{ Unwrap() http.ResponseWriter })
	if !ok {
		t.Fatal("the wrapper does not implement Unwrap: ResponseController cannot reach the real writer")
	}
	if unwrapper.Unwrap() != http.ResponseWriter(rr) {
		t.Error("Unwrap did not return the wrapped writer")
	}

	// Flushing must reach the writer underneath rather than be swallowed.
	rec.Flush()
	if !rr.Flushed {
		t.Error("Flush did not reach the underlying writer")
	}
}

func TestStatusRecorderRemembersTheStatus(t *testing.T) {
	rec := &statusRecorder{ResponseWriter: httptest.NewRecorder()}
	// A body without a header leaves this at zero, logged as 200.
	if rec.status != 0 {
		t.Errorf("status before WriteHeader = %d, want 0", rec.status)
	}
	rec.WriteHeader(http.StatusNotFound)
	if rec.status != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.status)
	}
}

func TestStatusColour(t *testing.T) {
	cases := map[int]string{
		200: ansiGreen,
		201: ansiGreen,
		303: ansiDim, // the redirect after every POST: ordinary, not worth the eye
		400: ansiYellow,
		404: ansiYellow,
		500: ansiRed,
	}
	for status, want := range cases {
		if got := statusColour(status); got != want {
			t.Errorf("statusColour(%d) = %q, want %q", status, got, want)
		}
	}
}

// Colour only for a terminal; anywhere else the line comes out bare.
func TestColourIsOffByDefault(t *testing.T) {
	if logColour {
		t.Fatal("colour enabled without BIBLI_LOG_COLOR: a school's journal would fill with escape codes")
	}
	if got := colour(ansiRed, "500"); got != "500" {
		t.Errorf("colour(%q) = %q, want the text untouched", "500", got)
	}

	saved := logColour
	logColour = true
	defer func() { logColour = saved }()
	got := colour(ansiRed, "500")
	if !strings.HasPrefix(got, ansiRed) || !strings.HasSuffix(got, ansiReset) {
		t.Errorf("colour(%q) = %q, want it wrapped and reset", "500", got)
	}
}

// Only BIBLI_LOG_COLOR=1 means colour. `go test` captures stderr, so
// wantsLogColour is false here whatever the variable.
func TestWantsLogColourNeedsTheVariable(t *testing.T) {
	t.Setenv("BIBLI_LOG_COLOR", "")
	if wantsLogColour() {
		t.Error("colour wanted with the variable unset")
	}
	t.Setenv("BIBLI_LOG_COLOR", "yes")
	if wantsLogColour() {
		t.Error("colour wanted for a value other than 1")
	}
}

// The middleware end to end with colour forced on, checked on the log bytes.
func TestLoggingColoursTheStatus(t *testing.T) {
	saved := logColour
	logColour = true
	defer func() { logColour = saved }()

	var out strings.Builder
	savedOutput := log.Writer()
	savedFlags := log.Flags()
	log.SetOutput(&out)
	log.SetFlags(0)
	defer func() {
		log.SetOutput(savedOutput)
		log.SetFlags(savedFlags)
	}()

	handler := logging(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/boom" {
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))

	cases := []struct {
		path, colour string
	}{
		{"/settings", ansiGreen}, // the handler wrote nothing: 200
		{"/boom", ansiRed},
	}
	for _, c := range cases {
		out.Reset()
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, c.path, nil))
		line := out.String()
		if !strings.Contains(line, c.colour) {
			t.Errorf("%s: line %q carries no %q", c.path, line, c.colour)
		}
		if !strings.Contains(line, ansiDim) {
			t.Errorf("%s: the duration is not dimmed: %q", c.path, line)
		}
	}

	// An asset line is dimmed whole rather than coloured by status.
	out.Reset()
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/static/app.js", nil))
	line := strings.TrimRight(out.String(), "\n")
	if !strings.HasPrefix(line, ansiDim) || !strings.HasSuffix(line, ansiReset) {
		t.Errorf("asset line not dimmed whole: %q", line)
	}
	if strings.Contains(line, ansiGreen) {
		t.Errorf("asset line coloured by status: %q", line)
	}
}

// Without colour, the line carries no escape codes.
func TestLoggingIsPlainWithoutColour(t *testing.T) {
	var out strings.Builder
	savedOutput := log.Writer()
	savedFlags := log.Flags()
	log.SetOutput(&out)
	log.SetFlags(0)
	defer func() {
		log.SetOutput(savedOutput)
		log.SetFlags(savedFlags)
	}()

	handler := logging(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/loans", nil))

	line := out.String()
	if strings.Contains(line, "\033") {
		t.Errorf("escape code in a plain log: %q", line)
	}
	if !strings.HasPrefix(line, "200 GET /loans (") {
		t.Errorf("line = %q, want it to start with \"200 GET /loans (\"", line)
	}
}

// The family token is never logged.
func TestLoggingHidesTheTrackingToken(t *testing.T) {
	var out strings.Builder
	savedOutput := log.Writer()
	savedFlags := log.Flags()
	log.SetOutput(&out)
	log.SetFlags(0)
	defer func() {
		log.SetOutput(savedOutput)
		log.SetFlags(savedFlags)
	}()

	handler := logging(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	handler.ServeHTTP(httptest.NewRecorder(),
		httptest.NewRequest(http.MethodGet, "/track/s3cr3t-token-value", nil))

	if line := out.String(); strings.Contains(line, "s3cr3t-token-value") {
		t.Errorf("the family token was logged: %q", line)
	}
}

// The ring keeps the newest lines, without the colours of the development log.
func TestLogRingKeepsTheLastLines(t *testing.T) {
	l := &logRing{max: 3}
	for _, s := range []string{"one\n", "two\n", "\x1b[31mthree\x1b[0m\n", "2026/10/27 05:46:47 200 GET /static/app.js (125µs)\n", "four\n"} {
		l.Write([]byte(s))
	}
	if got := strings.Join(l.last(10), ","); got != "two,three,four" {
		t.Errorf("last = %q, want two,three,four", got)
	}
	if got := strings.Join(l.last(1), ","); got != "four" {
		t.Errorf("last(1) = %q, want four", got)
	}
}

// The log is handed out to a signed-in librarian only: the memory without a
// log file, the files (previous one first) with one.
func TestLogDownload(t *testing.T) {
	a, h := testHandler(t)
	recentLog.Write([]byte("catalogue/bnf: a line to find\n"))

	if w := get(h, "/settings/log.txt", nil); w.Code == http.StatusOK {
		t.Error("the log was handed out without signing in")
	}
	w := get(h, "/settings/log.txt", signedIn(t, a))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "a line to find") {
		t.Errorf("GET /settings/log.txt = %d %q, want the line kept in memory", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Header().Get("Content-Disposition"), "attachment") {
		t.Error("the log is not offered as a download")
	}

	a.logPath = filepath.Join(t.TempDir(), "bibli.log")
	os.WriteFile(a.logPath+".1", []byte("older\n"), 0o644)
	os.WriteFile(a.logPath, []byte("newer\n"), 0o644)
	w = get(h, "/settings/log.txt", signedIn(t, a))
	if got := w.Body.String(); got != "older\nnewer\n" {
		t.Errorf("with a log file, body = %q, want the two files in order", got)
	}
}

// Clearing empties the memory and both files, and leaves one line saying so.
func TestLogClear(t *testing.T) {
	a, h := testHandler(t)
	recentLog.Write([]byte("before the purge\n"))
	path := filepath.Join(t.TempDir(), "bibli.log")
	lf, err := openLogFile(path, maxLogSize)
	if err != nil {
		t.Fatal(err)
	}
	lf.Write([]byte("in the file\n"))
	os.WriteFile(path+".1", []byte("rotated\n"), 0o644)
	a.logPath, a.logFile = path, lf

	saved := log.Writer()
	log.SetOutput(io.MultiWriter(lf, recentLog))
	t.Cleanup(func() { log.SetOutput(saved) })

	if w := postForm(h, signedIn(t, a), "/settings/log/clear", nil); w.Code != http.StatusSeeOther {
		t.Fatalf("POST /settings/log/clear = %d, want 303", w.Code)
	}
	if got := strings.Join(recentLog.last(10), "|"); strings.Contains(got, "before the purge") || !strings.Contains(got, "log cleared") {
		t.Errorf("memory after clear = %q", got)
	}
	if _, err := os.Stat(path + ".1"); !os.IsNotExist(err) {
		t.Error("the rotated file is still there")
	}
	if b, _ := os.ReadFile(path); strings.Contains(string(b), "in the file") || !strings.Contains(string(b), "log cleared") {
		t.Errorf("file after clear = %q", b)
	}
}
