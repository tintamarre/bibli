package main

import (
	"encoding/json"
	"net/http"
)

// manifest is the web app manifest a phone reads when someone adds Bibli to
// the home screen: it gives the shortcut its name, its icon and a window of its
// own (display standalone), so it opens like an app rather than a browser tab.
// Served fresh (the library name and theme may change) and without a session,
// since the browser fetches it before anyone signs in.
func (a *app) manifest(w http.ResponseWriter, r *http.Request) {
	name := "Bibli"
	if s := libraryName(); s != "" {
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
		// "maskable": the icon has a full-bleed background and the logo well
		// inside the safe zone, so Android shapes it into its adaptive icon
		// (rounded square, circle) instead of dropping the logo on a plain
		// white tile; "any" lets a browser that ignores maskable use it too.
		"icons": []map[string]string{
			{"src": asset("/static/icon-192.png"), "sizes": "192x192", "type": "image/png", "purpose": "any maskable"},
			{"src": asset("/static/icon-512.png"), "sizes": "512x512", "type": "image/png", "purpose": "any maskable"},
		},
	}
	w.Header().Set("Content-Type", "application/manifest+json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_ = json.NewEncoder(w).Encode(m)
}
