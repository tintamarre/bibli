package main

import (
	"database/sql"
	"log"
)

// The colour themes a school can pick in /settings: only colour, one block of
// custom properties in app.css. Keep the list short: every colour role has
// to be chosen in each.
var themes = []string{"ink", "stamp", "crayons"}

// defaultTheme is what a new instance starts on; baseTheme is the :root
// palette in app.css, which Ink restyles only the top bar of and Crayons
// overrides.
const (
	defaultTheme = "ink"
	baseTheme    = "stamp"
)

// retiredThemes maps a theme no longer offered to the one closest to it, so a
// school that chose it keeps its look.
var retiredThemes = map[string]string{"classic": "stamp"}

// themeColor is the browser chrome colour of each theme (<meta name=
// "theme-color">): its accent, which themes_test.go holds to the stylesheet.
var themeColor = map[string]string{
	"stamp":   "#3a43a3",
	"ink":     "#3a43a3",
	"crayons": "#3d4a5c",
}

var cachedTheme = defaultTheme

func knownTheme(t string) bool {
	for _, k := range themes {
		if k == t {
			return true
		}
	}
	return false
}

// setTheme records the instance theme, ignoring an unknown value.
func setTheme(t string) {
	if r, ok := retiredThemes[t]; ok {
		t = r
	}
	if !knownTheme(t) {
		log.Printf("unknown theme in the database: %q — keeping %s", t, instanceTheme())
		return
	}
	settingsMu.Lock()
	cachedTheme = t
	settingsMu.Unlock()
}

// instanceTheme is the theme chosen in /settings.
func instanceTheme() string {
	settingsMu.RLock()
	defer settingsMu.RUnlock()
	return cachedTheme
}

func loadTheme(db *sql.DB) {
	var v string
	switch err := db.QueryRow(`SELECT value FROM setting WHERE key = 'theme'`).Scan(&v); err {
	case nil:
		setTheme(v)
	case sql.ErrNoRows:
	default:
		log.Printf("settings cache (theme): %v", err)
	}
}

// offeredTheme is a theme as the settings screen lists it.
type offeredTheme struct {
	Code     string
	Name     string
	Selected bool
}

func offeredThemes(lang, selected string) []offeredTheme {
	out := make([]offeredTheme, 0, len(themes))
	for _, t := range themes {
		out = append(out, offeredTheme{Code: t, Name: T(lang, "theme."+t), Selected: t == selected})
	}
	return out
}
