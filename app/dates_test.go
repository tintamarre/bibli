package main

import (
	"testing"
	"time"
)

func TestRelativeFrom(t *testing.T) {
	loadForTest(t)
	ref := time.Date(2026, 9, 12, 15, 30, 0, 0, time.UTC)
	cases := []struct{ date, want string }{
		{"2026-09-12", "aujourd'hui"},
		{"2026-09-12 14:03:00", "aujourd'hui"}, // SQLite datetime
		// A date already formatted is refused: handlers pass ISO.
		{"12-09-2026", ""},
		{"2026-09-11", "hier"},
		{"2026-09-13", "demain"},
		{"2026-09-10", "il y a 2 jours"},
		{"2026-09-17", "dans 5 jours"},
		{"2026-09-25", "dans 13 jours"},
		{"2026-09-26", "dans 2 sem."},
		{"2026-10-03", "dans 3 sem."}, // the default loan period, abbreviated in French
		{"2026-08-01", "il y a 6 sem."},
		{"2026-07-01", "il y a 2 mois"},
		{"2025-09-12", "il y a 1 an"},
		{"2023-01-01", "il y a 4 ans"},
		{"anything at all", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := relativeFrom("fr", c.date, ref); got != c.want {
			t.Errorf("relativeFrom(fr, %q) = %q, want %q", c.date, got, c.want)
		}
	}
}

func TestHumanDuration(t *testing.T) {
	loadForTest(t)
	cases := map[int]string{
		0: "0 jour", 1: "1 jour", 2: "2 jours", 13: "13 jours",
		14: "2 sem.", 21: "3 sem.", 59: "8 sem.",
		60: "2 mois", 300: "10 mois", 330: "1 an", 365: "1 an", 800: "2 ans",
	}
	for days, want := range cases {
		if got := humanDuration("fr", days); got != want {
			t.Errorf("humanDuration(fr, %d) = %q, want %q", days, got, want)
		}
	}
}

// The same dates in English, which does not keep the singular at zero.
func TestRelativeFromEnglish(t *testing.T) {
	loadForTest(t)
	ref := time.Date(2026, 9, 12, 15, 30, 0, 0, time.UTC)
	cases := []struct{ date, want string }{
		{"2026-09-12", "today"},
		{"2026-09-11", "yesterday"},
		{"2026-09-13", "tomorrow"},
		{"2026-09-10", "2 days ago"},
		{"2026-09-17", "in 5 days"},
		{"2026-10-03", "in 3 weeks"},
		{"2026-07-01", "2 months ago"},
		{"2025-09-12", "1 year ago"},
		{"anything at all", ""},
	}
	for _, c := range cases {
		if got := relativeFrom("en", c.date, ref); got != c.want {
			t.Errorf("relativeFrom(en, %q) = %q, want %q", c.date, got, c.want)
		}
	}
}

func TestHumanDurationEnglish(t *testing.T) {
	loadForTest(t)
	cases := map[int]string{
		0: "0 days", 1: "1 day", 2: "2 days", 13: "13 days",
		14: "2 weeks", 21: "3 weeks", 60: "2 months", 365: "1 year", 800: "2 years",
	}
	for days, want := range cases {
		if got := humanDuration("en", days); got != want {
			t.Errorf("humanDuration(en, %d) = %q, want %q", days, got, want)
		}
	}
}

// relativeDate reads against UTC, the clock of date('now').
func TestRelativeDateFollowsTheDatabaseClock(t *testing.T) {
	loadForTest(t)
	iso := today().Format("2006-01-02")
	if got, want := relativeDate("fr", iso), T("fr", "date.today"); got != want {
		t.Errorf("relativeDate(today) = %q, want %q", got, want)
	}
	if got := relativeDate("fr", "not a date"); got != "" {
		t.Errorf("relativeDate on an unreadable value = %q, want empty", got)
	}
}
