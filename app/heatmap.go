package main

import (
	"sort"
	"time"
)

// An activity year drawn one cell per day: weeks down the columns, the days of a
// week across the seven rows. Nothing is stored.

// The year starts on the 1st of a month the library chooses in /settings,
// August until it does: the one a school's year turns on.
const defaultYearStart = time.August

// Seven rows and not five: a loan recorded on a Saturday must have somewhere
// to go.
const heatRows = 7

// heatCell is one day. Date is empty on the cells that pad the first and last
// columns out to whole weeks — a grid is rectangular and a year is not.
type heatCell struct {
	Date  string // ISO; empty when the cell is padding
	Count int
	Level int    // 0 for a day with no loans, 1..4 for the shades
	Ahead bool   // the year has not reached it yet
	Label string // "04-11-2025 — 23 prêts"; empty on padding and on days to come
}

// heatMonth labels a stretch of columns. Start is a 1-based grid column.
type heatMonth struct {
	Label string
	Start int
	Span  int
}

type heatmap struct {
	Start, End string // ISO bounds of the activity year
	Weeks      int    // columns
	Rows       [][]heatCell
	Months     []heatMonth
	Total      int
	Summary    string // said in words: nothing is kept behind a hover
	Busiest    string // empty when the year holds no loans
}

// activityYearBounds gives the first and last day of the activity year containing
// the given day, for the month the library set.
func activityYearBounds(todayISO string) (string, string) {
	return activityYearBoundsFrom(todayISO, yearStart())
}

// activityYearBoundsFrom is activityYearBounds for a given starting month.
func activityYearBoundsFrom(todayISO string, from time.Month) (string, string) {
	t, ok := parseDate(todayISO)
	if !ok {
		t = today()
	}
	year := t.Year()
	if t.Month() < from {
		year--
	}
	start := time.Date(year, from, 1, 0, 0, 0, 0, time.UTC)
	return start.Format("2006-01-02"), start.AddDate(1, 0, -1).Format("2006-01-02")
}

// monthKeys indexes the catalogue by month number. The month names are in the
// catalogues and not in a `time` layout, whose abbreviations are English only.
var monthKeys = [...]string{
	"month.jan", "month.feb", "month.mar", "month.apr", "month.may", "month.jun",
	"month.jul", "month.aug", "month.sep", "month.oct", "month.nov", "month.dec",
}

func monthLabel(lang string, m time.Month) string { return T(lang, monthKeys[int(m)-1]) }

// mondayIndex numbers the days of the week from Monday, which is the row a
// week starts on.
func mondayIndex(w time.Weekday) int { return (int(w) + 6) % 7 }

// newHeatmap lays the counts out on the grid. counts is keyed by ISO day; days
// it does not mention held no loan.
func newHeatmap(lang, todayISO string, counts map[string]int) heatmap {
	startISO, endISO := activityYearBounds(todayISO)
	start, _ := parseDate(startISO)
	end, _ := parseDate(endISO)
	now, ok := parseDate(todayISO)
	if !ok {
		now = today()
	}

	// The grid runs from the Monday on or before the first day to the Sunday on
	// or after the last, so every column is a whole week.
	first := start.AddDate(0, 0, -mondayIndex(start.Weekday()))
	last := end.AddDate(0, 0, 6-mondayIndex(end.Weekday()))
	weeks := int(last.Sub(first).Hours()/24+1) / heatRows

	h := heatmap{Start: startISO, End: endISO, Weeks: weeks}
	thresholds := heatThresholds(counts, startISO, endISO)

	busiest := heatCell{}
	h.Rows = make([][]heatCell, heatRows)
	for row := 0; row < heatRows; row++ {
		h.Rows[row] = make([]heatCell, weeks)
		for col := 0; col < weeks; col++ {
			day := first.AddDate(0, 0, col*heatRows+row)
			iso := day.Format("2006-01-02")
			if iso < startISO || iso > endISO {
				continue // padding: outside the activity year
			}
			c := heatCell{Date: iso, Count: counts[iso], Ahead: day.After(now)}
			c.Level = heatLevel(c.Count, thresholds)
			if !c.Ahead {
				// Every day reached carries its date and count, nought
				// included: an idle day is a reading, not a missing cell.
				c.Label = Tn(lang, "stats.day_label", c.Count, shortDate(lang, iso))
			}
			if c.Count > 0 {
				h.Total += c.Count
				if c.Count > busiest.Count {
					busiest = c
				}
			}
			h.Rows[row][col] = c
		}
	}

	h.Months = monthSpans(lang, first, weeks, startISO, endISO)
	h.Summary = Tn(lang, "stats.year_summary", h.Total, shortDate(lang, startISO))
	if busiest.Count > 0 {
		h.Busiest = Tn(lang, "stats.year_busiest", busiest.Count, shortDate(lang, busiest.Date))
	}
	return h
}

// monthSpans places a label over the columns a month occupies, counting a month
// in whichever column holds its first day inside the year.
func monthSpans(lang string, first time.Time, weeks int, startISO, endISO string) []heatMonth {
	var out []heatMonth
	for col := 0; col < weeks; col++ {
		for row := 0; row < heatRows; row++ {
			day := first.AddDate(0, 0, col*heatRows+row)
			iso := day.Format("2006-01-02")
			if iso < startISO || iso > endISO || day.Day() != 1 {
				continue
			}
			if n := len(out); n > 0 {
				out[n-1].Span = col + 1 - out[n-1].Start
			}
			out = append(out, heatMonth{Label: monthLabel(lang, day.Month()), Start: col + 1, Span: 1})
		}
	}
	if n := len(out); n > 0 {
		out[n-1].Span = weeks + 1 - out[n-1].Start
	}
	return out
}

// heatThresholds splits the days that saw a loan into quartiles, not a ramp
// against the busiest day, which would wash the rest of the year pale.
func heatThresholds(counts map[string]int, startISO, endISO string) [3]int {
	var v []int
	for iso, n := range counts {
		if n > 0 && iso >= startISO && iso <= endISO {
			v = append(v, n)
		}
	}
	if len(v) == 0 {
		return [3]int{}
	}
	sort.Ints(v)
	return [3]int{v[len(v)/4], v[len(v)/2], v[len(v)*3/4]}
}

// heatLevel is the shade a count falls in. A tie at a boundary goes to the
// lower level, so the palest shade is never empty.
func heatLevel(n int, t [3]int) int {
	switch {
	case n <= 0:
		return 0
	case n <= t[0]:
		return 1
	case n <= t[1]:
		return 2
	case n <= t[2]:
		return 3
	default:
		return 4
	}
}
