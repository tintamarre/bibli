package main

import (
	"math"
	"time"
)

// today is in UTC, the clock of SQLite's date('now'), so "today" and "0 days
// overdue" mean the same day.
func today() time.Time {
	return time.Now().UTC()
}

// dateLayouts holds the short date per language: `time` layouts, kept in Go
// rather than the catalogue. English spells the month, since "08-09-2026"
// reads differently in London and New York.
var dateLayouts = map[string]string{
	"fr": "02-01-2006",
	"en": "2 Jan 2006",
	"nl": "02-01-2006",
}

func dateLayout(lang string) string {
	if f, ok := dateLayouts[lang]; ok {
		return f
	}
	return dateLayouts[defaultLang]
}

// parseDate accepts the stored ISO form, with or without a time after it.
func parseDate(s string) (time.Time, bool) {
	if len(s) >= 10 {
		if t, err := time.Parse("2006-01-02", s[:10]); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// shortDate renders "11-09-2026" or "11 Sep 2026", depending on the language.
func shortDate(lang, iso string) string {
	t, ok := parseDate(iso)
	if !ok {
		return iso // unexpected value: show it whole rather than truncated
	}
	return t.Format(dateLayout(lang))
}

// relativeDate renders "today", "in 5 days", "3 weeks ago"..., empty when the
// date is unreadable.
func relativeDate(lang, s string) string {
	return relativeFrom(lang, s, today())
}

func relativeFrom(lang, s string, now time.Time) string {
	t, ok := parseDate(s)
	if !ok {
		return ""
	}
	a := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	days := int(math.Round(t.Sub(a).Hours() / 24))

	switch days {
	case 0:
		return T(lang, "date.today")
	case -1:
		return T(lang, "date.yesterday")
	case 1:
		return T(lang, "date.tomorrow")
	}
	if days < 0 {
		return T(lang, "date.ago", humanDuration(lang, -days))
	}
	return T(lang, "date.in", humanDuration(lang, days))
}

// humanDuration renders a number of days in words — days under a fortnight,
// then weeks, months, years — coarse on purpose, abbreviated for table cells.
func humanDuration(lang string, days int) string {
	switch {
	case days < 14:
		return Tn(lang, "duration.days", days)
	case days < 60:
		return Tn(lang, "duration.weeks", int(math.Round(float64(days)/7)))
	case days < 330:
		return Tn(lang, "duration.months", int(math.Round(float64(days)/30.44)))
	default:
		return Tn(lang, "duration.years", int(math.Round(float64(days)/365.25)))
	}
}
