package main

import (
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// A theme changes what colour things are, never what a colour means: every
// theme with a palette of its own has all of :root's tokens, leaves the fixed ones alone, and passes contrast.

type cssBlock struct {
	selector string
	decls    map[string]string
}

var (
	reCSSComment = regexp.MustCompile(`(?s)/\*.*?\*/`)
	reCSSBlock   = regexp.MustCompile(`([^{}]+)\{([^{}]*)\}`)
	reCSSDecl    = regexp.MustCompile(`(--[a-z0-9-]+)\s*:\s*([^;]+);`)
	reThemeSel   = regexp.MustCompile(`^:root\[data-theme="([a-z]+)"\]$`)
	reAreaSel    = regexp.MustCompile(`^:root\[data-theme="([a-z]+)"\] :is\(\[data-area="([a-z]+)"\]`)
	reThemeAny   = regexp.MustCompile(`data-theme="([a-z]+)"`)
	reHexColour  = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
)

func cssBlocks(t *testing.T) []cssBlock {
	t.Helper()
	raw, err := os.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	src := reCSSComment.ReplaceAllString(string(raw), "")
	var out []cssBlock
	for _, m := range reCSSBlock.FindAllStringSubmatch(src, -1) {
		b := cssBlock{selector: strings.Join(strings.Fields(m[1]), " "), decls: map[string]string{}}
		for _, d := range reCSSDecl.FindAllStringSubmatch(m[2], -1) {
			b.decls[d[1]] = strings.TrimSpace(d[2])
		}
		out = append(out, b)
	}
	return out
}

// The tokens whose colour carries a fixed meaning; no theme may redefine
// them.
func fixedToken(name string) bool {
	for _, p := range []string{"--err", "--ok", "--alert", "--tl-late"} {
		if name == p || strings.HasPrefix(name, p+"-") {
			return true
		}
	}
	return false
}

// The tokens an area of the Crayons theme gives its own value.
var areaTokens = []string{"--accent", "--accent-dark", "--accent-light", "--accent-border", "--accent-hover", "--sunken"}

type themeSheet struct {
	root   map[string]string            // :root, the base theme's palette
	themes map[string]map[string]string // theme -> its block
	areas  map[string]map[string]map[string]string
	styled map[string]bool // every theme some rule names
}

func readThemes(t *testing.T) themeSheet {
	t.Helper()
	s := themeSheet{themes: map[string]map[string]string{}, areas: map[string]map[string]map[string]string{}, styled: map[string]bool{}}
	for _, b := range cssBlocks(t) {
		for _, m := range reThemeAny.FindAllStringSubmatch(b.selector, -1) {
			s.styled[m[1]] = true
		}
		switch {
		case b.selector == ":root":
			s.root = b.decls
		case reThemeSel.MatchString(b.selector):
			s.themes[reThemeSel.FindStringSubmatch(b.selector)[1]] = b.decls
		case reAreaSel.MatchString(b.selector):
			m := reAreaSel.FindStringSubmatch(b.selector)
			if s.areas[m[1]] == nil {
				s.areas[m[1]] = map[string]map[string]string{}
			}
			s.areas[m[1]][m[2]] = b.decls
		}
	}
	if s.root == nil {
		t.Fatal("app.css has no :root block")
	}
	return s
}

// themeColours are the colour tokens of :root a theme must choose itself.
func (s themeSheet) themeColours() []string {
	var out []string
	for name, v := range s.root {
		if reHexColour.MatchString(v) && !fixedToken(name) {
			out = append(out, name)
		}
	}
	return out
}

func TestEveryThemeHasItsBlock(t *testing.T) {
	s := readThemes(t)
	// A theme without a palette of its own (Ink) still restyles something.
	for _, th := range themes {
		if !s.styled[th] && th != baseTheme {
			t.Errorf("theme %q is offered (themes.go) but app.css never styles it", th)
		}
	}
	for th := range s.styled {
		if !knownTheme(th) {
			t.Errorf("app.css styles a theme %q that themes.go does not offer", th)
		}
	}
	if _, ok := s.themes[baseTheme]; ok {
		t.Errorf("%s is :root itself; a block of its own would be a second copy of it", baseTheme)
	}
	if !knownTheme(defaultTheme) || !knownTheme(baseTheme) {
		t.Errorf("defaultTheme %q or baseTheme %q is not offered", defaultTheme, baseTheme)
	}
}

// A token a theme forgets falls back to the base theme's.
func TestEveryThemeDefinesEveryColour(t *testing.T) {
	s := readThemes(t)
	want := s.themeColours()
	for th, decls := range s.themes {
		for _, name := range want {
			if _, ok := decls[name]; !ok {
				t.Errorf("theme %q does not define %s", th, name)
			}
		}
		for name := range decls {
			if fixedToken(name) {
				t.Errorf("theme %q redefines %s, whose colour means the same in every theme", th, name)
			} else if _, ok := s.root[name]; !ok {
				t.Errorf("theme %q defines %s, which :root does not", th, name)
			}
		}
	}
	for th, areas := range s.areas {
		for area, decls := range areas {
			for _, name := range areaTokens {
				if _, ok := decls[name]; !ok {
					t.Errorf("theme %q, area %q does not define %s", th, area, name)
				}
			}
			for name := range decls {
				if !slicesContains(areaTokens, name) {
					t.Errorf("theme %q, area %q defines %s; an area only recolours its accent and its bands", th, area, name)
				}
			}
		}
	}
}

func slicesContains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// Every area pageArea can name has its colours in each theme that colours
// areas, and no theme colours an area no page is ever in.
func TestAreasMatchThePages(t *testing.T) {
	s := readThemes(t)
	named := map[string]bool{}
	for _, p := range []string{"/borrow", "/return", "/loans", "/borrowers", "/inventory", "/book/1", "/catalogue"} {
		named[pageArea(p)] = true
	}
	for th, areas := range s.areas {
		for area := range named {
			if _, ok := areas[area]; !ok {
				t.Errorf("theme %q gives no colour to area %q", th, area)
			}
		}
		for area := range areas {
			if !named[area] {
				t.Errorf("theme %q colours area %q, which no page is in", th, area)
			}
		}
	}
}

func TestPageArea(t *testing.T) {
	cases := map[string]string{
		"/borrow":         "borrow",
		"/borrow/add":     "borrow",
		"/borrowers":      "borrowers",
		"/borrowers/12":   "borrowers",
		"/return":         "return",
		"/loans":          "loans",
		"/inventory":      "inventory",
		"/book/3":         "inventory",
		"/catalogue":      "inventory",
		"/":               "",
		"/settings":       "",
		"/stats":          "",
		"/borrowerscards": "",
	}
	for path, want := range cases {
		if got := pageArea(path); got != want {
			t.Errorf("pageArea(%q) = %q, want %q", path, got, want)
		}
	}
}

// The browser chrome colour is each theme's accent.
func TestThemeColourIsTheAccent(t *testing.T) {
	s := readThemes(t)
	for _, th := range themes {
		accent := s.root["--accent"]
		if d, ok := s.themes[th]; ok {
			accent = d["--accent"]
		}
		if !strings.EqualFold(themeColor[th], accent) {
			t.Errorf("themeColor[%q] = %s, the theme's accent is %s", th, themeColor[th], accent)
		}
	}
}

// relLuminance is WCAG 2's relative luminance of a #rrggbb colour.
func relLuminance(t *testing.T, hex string) float64 {
	t.Helper()
	if !reHexColour.MatchString(hex) {
		t.Fatalf("%q is not a #rrggbb colour", hex)
	}
	var c [3]float64
	for i := range c {
		v, _ := strconv.ParseUint(hex[1+2*i:3+2*i], 16, 8)
		f := float64(v) / 255
		if f <= 0.04045 {
			c[i] = f / 12.92
		} else {
			c[i] = math.Pow((f+0.055)/1.055, 2.4)
		}
	}
	return 0.2126*c[0] + 0.7152*c[1] + 0.0722*c[2]
}

func contrast(t *testing.T, a, b string) float64 {
	la, lb := relLuminance(t, a), relLuminance(t, b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// Measured on every background of every theme and area. Secondary text is held
// above AA on purpose.
func TestThemeContrast(t *testing.T) {
	s := readThemes(t)
	type palette struct {
		name   string
		tokens map[string]string
	}
	merge := func(layers ...map[string]string) map[string]string {
		out := map[string]string{}
		for _, l := range layers {
			for k, v := range l {
				out[k] = v
			}
		}
		return out
	}
	palettes := []palette{{baseTheme, s.root}}
	for th, decls := range s.themes {
		palettes = append(palettes, palette{th, merge(s.root, decls)})
		for area, a := range s.areas[th] {
			palettes = append(palettes, palette{th + "/" + area, merge(s.root, decls, a)})
		}
	}
	checks := []struct {
		fg, bg string
		min    float64
	}{
		{"--text", "--bg", 7}, {"--text", "--surface", 7}, {"--text", "--sunken", 7},
		{"--muted", "--bg", 6}, {"--muted", "--surface", 6}, {"--muted", "--sunken", 6},
		{"#ffffff", "--accent", 4.5},
		{"--accent", "--surface", 4.5},
		{"--accent-dark", "--surface", 4.5}, {"--accent-dark", "--accent-light", 4.5},
		{"--err", "--surface", 4.5},
	}
	for _, p := range palettes {
		get := func(name string) string {
			if strings.HasPrefix(name, "#") {
				return name
			}
			return p.tokens[name]
		}
		for _, c := range checks {
			if r := contrast(t, get(c.fg), get(c.bg)); r < c.min {
				t.Errorf("%s: %s on %s is %.2f:1, want at least %.1f:1", p.name, c.fg, c.bg, r, c.min)
			}
		}
	}
}

// Every screen page carries the theme; the printouts do not.
func TestPagesCarryTheTheme(t *testing.T) {
	printouts := map[string]bool{"cards.html": true, "inventory_print.html": true, "labels_print.html": true, "loans_print.html": true}
	files, err := filepath.Glob("templates/*.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src := string(raw)
		if !strings.Contains(src, "<html") {
			continue
		}
		has := strings.Contains(src, `data-theme="{{theme}}"`)
		switch name := filepath.Base(f); {
		case printouts[name] && has:
			t.Errorf("%s is a printout and carries the screen theme", name)
		case !printouts[name] && !has:
			t.Errorf("%s opens a page without data-theme=\"{{theme}}\" on <html>", name)
		}
	}
}

func TestEveryThemeIsNamed(t *testing.T) {
	loadForTest(t)
	for _, lang := range langs {
		for _, th := range themes {
			if _, ok := catalogues[lang]["theme."+th]; !ok {
				t.Errorf("%s.json has no name for theme %q", lang, th)
			}
		}
	}
}

// An unknown value in the database keeps the theme in place.
func TestSetThemeIgnoresUnknown(t *testing.T) {
	defer setTheme(instanceTheme())
	setTheme("stamp")
	setTheme("neon")
	if got := instanceTheme(); got != "stamp" {
		t.Errorf("after setTheme(neon), theme = %q, want stamp", got)
	}
}

// A retired theme in the database becomes the one that replaced it.
func TestSetThemeMapsRetired(t *testing.T) {
	defer setTheme(instanceTheme())
	for old, now := range retiredThemes {
		if !knownTheme(now) {
			t.Errorf("retired theme %q maps to %q, which is not offered", old, now)
		}
		setTheme("crayons")
		setTheme(old)
		if got := instanceTheme(); got != now {
			t.Errorf("after setTheme(%s), theme = %q, want %s", old, got, now)
		}
	}
}
