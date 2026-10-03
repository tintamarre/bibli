package main

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// Icons are template definitions, resolved at render time: an unknown name
// compiles and cuts the page off mid-way. These checks catch it.

var (
	reIconDefine = regexp.MustCompile(`\{\{define "(icon-[a-z-]+|logo)"\}\}`)
	reIconCall   = regexp.MustCompile(`\{\{template "(icon-[a-z-]+|logo)"`)
)

func iconsFile(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("templates/icons.html")
	if err != nil {
		t.Fatalf("reading the icon set: %v", err)
	}
	return string(b)
}

func definedIcons(t *testing.T) map[string]bool {
	t.Helper()
	defined := make(map[string]bool)
	for _, m := range reIconDefine.FindAllStringSubmatch(iconsFile(t), -1) {
		if defined[m[1]] {
			t.Errorf("%s is defined twice: html/template keeps the last one silently", m[1])
		}
		defined[m[1]] = true
	}
	if len(defined) == 0 {
		t.Fatal("no icon defined")
	}
	return defined
}

func TestEveryIconCalledIsDefined(t *testing.T) {
	defined := definedIcons(t)

	files, err := filepath.Glob("templates/*.html")
	if err != nil {
		t.Fatal(err)
	}
	used := make(map[string]bool)
	for _, path := range files {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range reIconCall.FindAllStringSubmatch(string(content), -1) {
			used[m[1]] = true
			if !defined[m[1]] {
				t.Errorf("%s calls %q, which templates/icons.html does not define",
					filepath.Base(path), m[1])
			}
		}
	}

	// The other direction: an icon nobody draws is dead weight.
	var unused []string
	for name := range defined {
		if !used[name] {
			unused = append(unused, name)
		}
	}
	sort.Strings(unused)
	if len(unused) > 0 {
		t.Errorf("icons defined and never used: %s", strings.Join(unused, ", "))
	}
}

// An icon repeats the text beside it, so it is hidden from screen readers and
// carries no label of its own.
func TestIconsAreDecorative(t *testing.T) {
	for _, svg := range strings.Split(iconsFile(t), "{{define ")[1:] {
		name := svg[1:strings.Index(svg, `"}}`)]
		if !strings.Contains(svg, `aria-hidden="true"`) {
			t.Errorf("%s: no aria-hidden — a screen reader would read the label twice", name)
		}
		for _, attr := range []string{"aria-label=", "<title>", "role="} {
			if strings.Contains(svg, attr) {
				t.Errorf("%s: carries %s — a label belongs in locales/, not in an icon", name, attr)
			}
		}
	}
}

// One grid, one stroke weight, one join, no fill, for every icon.
func TestIconsShareOneGrid(t *testing.T) {
	for _, svg := range strings.Split(iconsFile(t), "{{define ")[1:] {
		name := svg[1:strings.Index(svg, `"}}`)]
		for _, want := range []string{
			`viewBox="0 0 24 24"`,
			`fill="none"`,
			`stroke="currentColor"`,
			`stroke-linecap="round"`,
			`stroke-linejoin="round"`,
		} {
			if !strings.Contains(svg, want) {
				t.Errorf("%s: %s missing — the set no longer draws to one grid", name, want)
			}
		}
		// The group a caller passes is what sizes the icon.
		if !strings.Contains(svg, `class="{{.}}"`) {
			t.Errorf("%s: does not take its group from the caller", name)
		}
		// A hard-coded width or height would defeat the CSS slot.
		if strings.Contains(svg, "<svg") && strings.Contains(svg[:strings.Index(svg, ">")], " width=") {
			t.Errorf("%s: hard-coded width — the slot's CSS should size it", name)
		}
	}
}

// Every slot an icon is handed must exist in the stylesheet, otherwise the icon
// renders at the SVG default of 300x150 and blows the layout apart.
func TestEveryIconGroupIsStyled(t *testing.T) {
	css, err := os.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob("templates/*.html")
	if err != nil {
		t.Fatal(err)
	}
	call := regexp.MustCompile(`\{\{template "(?:icon-[a-z-]+|logo)" "([a-z-]+)"\}\}`)
	seen := make(map[string]bool)
	for _, path := range files {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range call.FindAllStringSubmatch(string(content), -1) {
			if seen[m[1]] {
				continue
			}
			seen[m[1]] = true
			if !strings.Contains(string(css), "."+m[1]) {
				t.Errorf("%s: group %q has no rule in app.css — the icon would render at 300x150",
					filepath.Base(path), m[1])
			}
		}
	}
	if len(seen) == 0 {
		t.Fatal("no icon call found")
	}
}

// Every screen in the shared layout puts an icon beside its title; a missing
// one falls back to nothing silently.
func TestEveryPageNamesItsTitleIcon(t *testing.T) {
	files, err := filepath.Glob("templates/*.html")
	if err != nil {
		t.Fatal(err)
	}
	var pages int
	for _, path := range files {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		s := string(content)
		// Only pages inside the shared layout have a title to put an icon beside;
		// a "document" (login, the printouts, the tracking page) draws its own head.
		if !strings.Contains(s, `{{define "content"}}`) {
			continue
		}
		pages++
		if !strings.Contains(s, `{{define "title_icon"}}`) {
			t.Errorf("%s: no \"title_icon\" — its title would come out bare while every other page has one",
				filepath.Base(path))
		}
	}
	if pages == 0 {
		t.Fatal("no page found inside the shared layout")
	}
}

// The width group a page asks for must exist in app.css, or the page silently
// renders at full width.
func TestEveryPageWidthIsStyled(t *testing.T) {
	css, err := os.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob("templates/*.html")
	if err != nil {
		t.Fatal(err)
	}
	width := regexp.MustCompile(`\{\{define "width"\}\}\s*([a-z0-9-]+)\s*\{\{end\}\}`)
	var found int
	for _, path := range files {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range width.FindAllStringSubmatch(string(content), -1) {
			found++
			if !strings.Contains(string(css), "."+m[1]+" ") {
				t.Errorf("%s: asks for width %q, which app.css does not define — the page would render full width",
					filepath.Base(path), m[1])
			}
		}
	}
	if found == 0 {
		t.Fatal("no page redefines \"width\"")
	}
}

// builtAtRuntime are the groups no template spells out: htmx's, the status
// groups ("status-{{.Status}}") and the heatmap shades ("hm-l{{.Level}}").
var builtAtRuntime = map[string]bool{
	"htmx-request":     true,
	"status-available": true,
	"status-damaged":   true,
	"status-lost":      true,
	"status-withdrawn": true,
	"hm-l1":            true,
	"hm-l2":            true,
	"hm-l3":            true,
	"hm-l4":            true,
}

// The other direction of TestEveryIconGroupIsStyled: no CSS rule for markup that has gone.
func TestEveryStyleRuleIsUsed(t *testing.T) {
	css, err := os.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	// Group names inside comments are prose, not rules.
	stripped := regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(string(css), "")
	// Quoted strings hold file names ("atkinson-400.woff2") and attribute
	// values, never a group.
	stripped = regexp.MustCompile(`"[^"]*"`).ReplaceAllString(stripped, `""`)

	var used strings.Builder
	for _, pattern := range []string{"templates/*.html", "static/*.js", "*.go"} {
		files, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatal(err)
		}
		for _, path := range files {
			if strings.HasSuffix(path, "_test.go") || strings.HasSuffix(path, ".css") {
				continue
			}
			content, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			used.Write(content)
		}
	}
	haystack := used.String()

	seen := make(map[string]bool)
	for _, m := range regexp.MustCompile(`\.([A-Za-z][A-Za-z0-9_-]*)`).FindAllStringSubmatch(stripped, -1) {
		name := m[1]
		if seen[name] || builtAtRuntime[name] {
			continue
		}
		seen[name] = true
		if !strings.Contains(haystack, name) {
			t.Errorf("app.css: .%s is styled but nothing names it — a dead rule, "+
				"or a group built at runtime that belongs in builtAtRuntime", name)
		}
	}
	if len(seen) == 0 {
		t.Fatal("no group found in app.css")
	}
}
