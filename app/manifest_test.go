package main

import (
	"encoding/json"
	"net/http"
	"testing"
)

// The manifest must be reachable without a session (the browser fetches it
// before anyone signs in) and be a valid manifest naming the icons.
func TestManifest(t *testing.T) {
	_, h := testHandler(t)
	w := get(h, "/manifest.webmanifest", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /manifest.webmanifest without a session = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/manifest+json; charset=utf-8" {
		t.Errorf("content-type = %q", ct)
	}
	var m struct {
		Name    string
		Display string
		Icons   []struct{ Src, Sizes, Type string }
	}
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatalf("manifest is not valid JSON: %v", err)
	}
	if m.Display != "standalone" {
		t.Errorf("display = %q, want standalone", m.Display)
	}
	if len(m.Icons) != 2 || m.Icons[0].Type != "image/png" {
		t.Errorf("icons = %+v, want two PNG icons", m.Icons)
	}
}

// The home-screen icons and the manifest link are served, so a shortcut has an
// icon rather than a screenshot.
func TestHomeScreenIconsExist(t *testing.T) {
	_, h := testHandler(t)
	for _, p := range []string{"/static/icon-192.png", "/static/icon-512.png", "/static/apple-touch-icon.png"} {
		if w := get(h, p, nil); w.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", p, w.Code)
		}
	}
}
