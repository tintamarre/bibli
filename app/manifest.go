package main

import (
	"encoding/json"
	"net/http"
)

// manifest is the web app manifest a phone reads when a teacher adds Bibli to
// the home screen: it gives the shortcut its name, its icon and a window of its
// own (display standalone), so it opens like an app rather than a browser tab.
// Served fresh (the school name and theme may change) and without a session,
// since the browser fetches it before anyone signs in.
func (a *app) manifest(w http.ResponseWriter, r *http.Request) {
	name := "Bibli"
	if s := school(); s != "" {
		name = "Bibli — " + s
	}
	m := map[string]any{
		"name":             name,
		"short_name":       "Bibli",
		"start_url":        "/",
		"scope":            "/",
		"display":          "standalone",
		"background_color": "#ffffff",
		"theme_color":      themeColor[instanceTheme()],
		"icons": []map[string]string{
			{"src": asset("/static/icon-192.png"), "sizes": "192x192", "type": "image/png", "purpose": "any"},
			{"src": asset("/static/icon-512.png"), "sizes": "512x512", "type": "image/png", "purpose": "any"},
		},
	}
	w.Header().Set("Content-Type", "application/manifest+json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_ = json.NewEncoder(w).Encode(m)
}
