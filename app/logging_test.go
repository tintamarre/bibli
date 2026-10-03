package main

import (
	"log"
	"net/http"
	"net/http/httptest"
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
