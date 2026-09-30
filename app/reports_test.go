package main

import (
	"strings"
	"testing"
)

func TestShortDate(t *testing.T) {
	cases := []struct{ in, fr, en string }{
		{"2026-09-11", "11-09-2026", "11 Sep 2026"},
		// SQLite datetime: only the date is kept
		{"2026-09-11 14:03:00", "11-09-2026", "11 Sep 2026"},
		{"2026-01-01", "01-01-2026", "1 Jan 2026"},
		// Returned as is, never an error on screen.
		{"not a date", "not a date", "not a date"},
		{"", "", ""},
	}
	for _, c := range cases {
		if got := shortDate("fr", c.in); got != c.fr {
			t.Errorf("shortDate(fr, %q) = %q, want %q", c.in, got, c.fr)
		}
		if got := shortDate("en", c.in); got != c.en {
			t.Errorf("shortDate(en, %q) = %q, want %q", c.in, got, c.en)
		}
	}
}

// A display date must not reparse: handlers pass ISO and the template formats.
func TestDisplayDateDoesNotReparse(t *testing.T) {
	for _, lang := range langs {
		shown := shortDate(lang, "2026-09-11")
		if _, ok := parseDate(shown); ok {
			t.Errorf("%s: %q reparses — the round trip through the display format is back", lang, shown)
		}
	}
}

// The export is opened by the same person as the screens, so its header follows
// the interface language. The column count is what an import parser counts on.
func TestCSVHeader(t *testing.T) {
	loadForTest(t)

	for _, lang := range langs {
		row := csvHeader(lang)
		if len(row) != len(csvColumns) {
			t.Fatalf("%s: %d columns, want %d", lang, len(row), len(csvColumns))
		}
		for i, cell := range row {
			if cell == "" {
				t.Errorf("%s: column %d is empty", lang, i)
			}
			// A key showing through means the string was never written.
			if cell == csvColumns[i] {
				t.Errorf("%s: column %d shows its key, %q", lang, i, cell)
			}
		}
	}
	if fr, en := csvHeader("fr"), csvHeader("en"); fr[1] == en[1] {
		t.Errorf("the header is the same in both languages: %q", fr[1])
	}
}

// The CSV is RFC 4180, and its column names do not follow the interface
// language.
func TestCSVColumnNamesAreStableAndMachineReadable(t *testing.T) {
	loadForTest(t)

	names := csvColumnNames()
	if len(names) != len(csvColumns) {
		t.Fatalf("%d names for %d columns", len(names), len(csvColumns))
	}
	seen := map[string]bool{}
	for i, n := range names {
		switch {
		case n == "":
			t.Errorf("column %d has no name", i+1)
		case seen[n]:
			t.Errorf("two columns are called %q; a query could not tell them apart", n)
		case strings.ContainsAny(n, " ,;\"\n\r"):
			t.Errorf("column %q holds a character that has to be quoted", n)
		case strings.ToLower(n) != n:
			t.Errorf("column %q is not lowercase", n)
		case strings.HasPrefix(n, "csv."):
			t.Errorf("column %q kept its locale prefix", n)
		}
		seen[n] = true
	}

	// The names are the same in every language, which is the point: the
	// spreadsheet is where the school's own wording belongs.
	for _, lang := range langs {
		if h := csvHeader(lang); len(h) != len(names) {
			t.Errorf("%s: the spreadsheet heading has %d columns, the CSV %d",
				lang, len(h), len(names))
		}
	}
	if csvHeader("fr")[1] == csvColumnNames()[1] {
		t.Error("the French heading equals the CSV column name; one of the two is not doing its job")
	}
}

// A spreadsheet reads a cell that starts with = + - @ as a formula. A title
// coming from a catalogue anyone can edit must not run when a volunteer opens
// the CSV, so those cells are pinned to text with a leading apostrophe.
func TestCSVSafeNeutralisesFormulaCells(t *testing.T) {
	cases := map[string]string{
		`=HYPERLINK("http://x","y")`: `'=HYPERLINK("http://x","y")`,
		"+1+2":                       "'+1+2",
		"-1":                         "'-1",
		"@cmd":                       "'@cmd",
		"\ttab-led":                  "'\ttab-led",
		"Le Petit Prince":            "Le Petit Prince",
		"9782070408504":              "9782070408504",
		"":                           "",
	}
	for in, want := range cases {
		if got := csvSafe(in); got != want {
			t.Errorf("csvSafe(%q) = %q, want %q", in, got, want)
		}
	}
}
