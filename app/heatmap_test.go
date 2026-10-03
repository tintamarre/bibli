package main

import (
	"testing"
	"time"
)

// Every case here fixes a day rather than reading the clock.

func TestActivityYearRunsFromAugustToJuly(t *testing.T) {
	for _, c := range []struct{ day, start, end string }{
		{"2026-09-22", "2026-08-01", "2027-07-31"}, // inside the autumn term
		{"2026-08-01", "2026-08-01", "2027-07-31"}, // the first day of the year
		{"2026-07-31", "2025-08-01", "2026-07-31"}, // the last day of the one before
		{"2026-01-15", "2025-08-01", "2026-07-31"}, // after the turn of the calendar
	} {
		start, end := activityYearBounds(c.day)
		if start != c.start || end != c.end {
			t.Errorf("%s: want %s..%s, got %s..%s", c.day, c.start, c.end, start, end)
		}
	}
}

func TestActivityYearFollowsTheMonthChosen(t *testing.T) {
	for _, c := range []struct {
		from       time.Month
		day        string
		start, end string
	}{
		{time.January, "2026-09-22", "2026-01-01", "2026-12-31"}, // a calendar year
		{time.January, "2026-01-01", "2026-01-01", "2026-12-31"},
		{time.September, "2026-08-31", "2025-09-01", "2026-08-31"},
		{time.September, "2026-09-01", "2026-09-01", "2027-08-31"},
		{time.December, "2026-02-10", "2025-12-01", "2026-11-30"},
	} {
		start, end := activityYearBoundsFrom(c.day, c.from)
		if start != c.start || end != c.end {
			t.Errorf("%v, %s: want %s..%s, got %s..%s", c.from, c.day, c.start, c.end, start, end)
		}
	}
}

// The activity-year boundary, where an off-by-one shifts a whole year.
func TestActivityYearTurnsOverOnTheFirstOfAugust(t *testing.T) {
	_, julyEnd := activityYearBounds("2026-07-31")
	augStart, _ := activityYearBounds("2026-08-01")
	if julyEnd != "2026-07-31" || augStart != "2026-08-01" {
		t.Fatalf("the turn is not on 1 August: %s then %s", julyEnd, augStart)
	}
}

func TestEveryDayOfTheYearIsDrawnExactlyOnce(t *testing.T) {
	loadForTest(t)
	h := newHeatmap("fr", "2026-09-22", nil)
	seen := map[string]int{}
	for _, row := range h.Rows {
		if len(row) != h.Weeks {
			t.Fatalf("a row holds %d cells, the grid is %d weeks wide", len(row), h.Weeks)
		}
		for _, c := range row {
			if c.Date != "" {
				seen[c.Date]++
			}
		}
	}
	start, _ := parseDate(h.Start)
	end, _ := parseDate(h.End)
	want := int(end.Sub(start).Hours()/24) + 1
	if len(seen) != want {
		t.Errorf("days drawn: want %d, got %d", want, len(seen))
	}
	for day, n := range seen {
		if n != 1 {
			t.Errorf("%s drawn %d times", day, n)
		}
		if day < h.Start || day > h.End {
			t.Errorf("%s is outside %s..%s", day, h.Start, h.End)
		}
	}
}

// Seven rows: a Saturday loan lands in its own row, and the grid's total
// matches the sentence under it.
func TestWeekendLoansAreDrawnAndCounted(t *testing.T) {
	loadForTest(t)
	saturday, sunday := "2026-09-19", "2026-09-20"
	h := newHeatmap("fr", "2026-09-22", map[string]int{saturday: 4, sunday: 2, "2026-09-18": 7})

	if h.Total != 13 {
		t.Errorf("total: want 13, got %d", h.Total)
	}
	var summed int
	found := map[string]int{}
	for _, row := range h.Rows {
		for _, c := range row {
			summed += c.Count
			if c.Count > 0 {
				found[c.Date] = c.Count
			}
		}
	}
	// The sentence under the grid says exactly what the grid draws.
	if summed != h.Total {
		t.Errorf("the cells sum to %d, the caption says %d", summed, h.Total)
	}
	if found[saturday] != 4 || found[sunday] != 2 {
		t.Errorf("the weekend is missing from the grid: %v", found)
	}

	// And each lands in its own row, which is what a five-row grid cannot do.
	at := func(iso string) int {
		for row, cells := range h.Rows {
			for _, c := range cells {
				if c.Date == iso {
					return row
				}
			}
		}
		return -1
	}
	if at(saturday) != 5 || at(sunday) != 6 {
		t.Errorf("the weekend sits in rows %d and %d, want 5 and 6", at(saturday), at(sunday))
	}
}

// Row 0 is Monday, which is where a week starts here.
func TestRowsStartOnMonday(t *testing.T) {
	loadForTest(t)
	h := newHeatmap("fr", "2026-09-22", map[string]int{"2026-09-14": 3}) // a Monday
	for _, c := range h.Rows[0] {
		if c.Date == "" {
			continue
		}
		day, _ := parseDate(c.Date)
		if day.Weekday() != time.Monday {
			t.Fatalf("row 0 holds a %s (%s)", day.Weekday(), c.Date)
		}
	}
	for _, c := range h.Rows[6] {
		if c.Date == "" {
			continue
		}
		day, _ := parseDate(c.Date)
		if day.Weekday() != time.Sunday {
			t.Fatalf("row 6 holds a %s (%s)", day.Weekday(), c.Date)
		}
	}
}

// Every day the year has reached carries its date and its count under the
// pointer, nought included; days still to come carry nothing.
func TestReachedDaysAreLabelledAndDaysToComeAreNot(t *testing.T) {
	loadForTest(t)
	h := newHeatmap("fr", "2026-09-22", map[string]int{"2026-09-21": 5})
	var quiet, busy, ahead heatCell
	for _, row := range h.Rows {
		for _, c := range row {
			switch c.Date {
			case "2026-09-21":
				busy = c
			case "2026-09-17":
				quiet = c
			case "2026-10-05":
				ahead = c
			}
		}
	}
	if busy.Label == "" || quiet.Label == "" {
		t.Errorf("a day the year has reached has no label: busy %q, quiet %q", busy.Label, quiet.Label)
	}
	if ahead.Label != "" {
		t.Errorf("a day still to come is labelled %q", ahead.Label)
	}
	if !ahead.Ahead || busy.Ahead {
		t.Errorf("Ahead is wrong: %s %v, %s %v", ahead.Date, ahead.Ahead, busy.Date, busy.Ahead)
	}
}

// Shades are quartiles, not a ramp: one very busy day must not wash the rest
// of the year pale.
func TestOneExceptionalDayDoesNotFlattenTheYear(t *testing.T) {
	loadForTest(t)
	counts := map[string]int{"2026-09-15": 200}
	day, _ := parseDate("2026-10-01")
	for i := 0; i < 60; i++ {
		counts[day.AddDate(0, 0, i).Format("2006-01-02")] = 1 + i%4
	}
	h := newHeatmap("fr", "2027-01-15", counts)

	levels := map[int]bool{}
	for _, row := range h.Rows {
		for _, c := range row {
			if c.Count > 0 && c.Count < 100 {
				levels[c.Level] = true
			}
		}
	}
	if len(levels) < 3 {
		t.Errorf("the ordinary days occupy %d shades, want at least 3: %v", len(levels), levels)
	}
}

// A year whose days are all alike draws flat.
func TestAFlatYearDrawsFlat(t *testing.T) {
	loadForTest(t)
	counts := map[string]int{}
	day, _ := parseDate("2026-09-01")
	for i := 0; i < 40; i++ {
		counts[day.AddDate(0, 0, i).Format("2006-01-02")] = 5
	}
	h := newHeatmap("fr", "2026-11-01", counts)
	for _, row := range h.Rows {
		for _, c := range row {
			if c.Count > 0 && c.Level != 1 {
				t.Fatalf("%s holds %d loans like every other day but draws at level %d", c.Date, c.Count, c.Level)
			}
		}
	}
}

// More loans never draws paler.
func TestShadesNeverGoBackwards(t *testing.T) {
	loadForTest(t)
	counts := map[string]int{}
	day, _ := parseDate("2026-09-01")
	for i := 0; i < 80; i++ {
		counts[day.AddDate(0, 0, i).Format("2006-01-02")] = i%11 + 1
	}
	h := newHeatmap("fr", "2027-01-15", counts)
	byCount := map[int]int{}
	for _, row := range h.Rows {
		for _, c := range row {
			if c.Count == 0 {
				continue
			}
			if prev, ok := byCount[c.Count]; ok && prev != c.Level {
				t.Fatalf("%d loans draws at level %d here and %d there", c.Count, c.Level, prev)
			}
			byCount[c.Count] = c.Level
		}
	}
	for a := 1; a <= 11; a++ {
		for b := a + 1; b <= 11; b++ {
			if byCount[b] < byCount[a] {
				t.Errorf("%d loans (level %d) draws paler than %d (level %d)", b, byCount[b], a, byCount[a])
			}
		}
	}
}

// The months label the columns their first day falls in, and cover the grid
// without a gap.
func TestMonthLabelsCoverTheGrid(t *testing.T) {
	loadForTest(t)
	h := newHeatmap("fr", "2026-09-22", nil)
	if len(h.Months) != 12 {
		t.Fatalf("months labelled: want 12, got %d", len(h.Months))
	}
	if h.Months[0].Label != "août" || h.Months[11].Label != "juil." {
		t.Errorf("the year runs %q..%q", h.Months[0].Label, h.Months[11].Label)
	}
	for i, m := range h.Months {
		if m.Span < 1 {
			t.Errorf("%s spans %d columns", m.Label, m.Span)
		}
		if i > 0 && m.Start != h.Months[i-1].Start+h.Months[i-1].Span {
			t.Errorf("a gap before %s: it starts at %d, %s ended at %d",
				m.Label, m.Start, h.Months[i-1].Label, h.Months[i-1].Start+h.Months[i-1].Span)
		}
	}
	if last := h.Months[11]; last.Start+last.Span != h.Weeks+1 {
		t.Errorf("the labels stop at column %d, the grid is %d wide", last.Start+last.Span-1, h.Weeks)
	}
}

// A year with nothing in it says so rather than naming a busiest day of nought.
func TestAnEmptyYearNamesNoBusiestDay(t *testing.T) {
	loadForTest(t)
	h := newHeatmap("fr", "2026-09-22", nil)
	if h.Total != 0 || h.Busiest != "" {
		t.Errorf("empty year: total %d, busiest %q", h.Total, h.Busiest)
	}
	if h.Summary == "" {
		t.Error("an empty year still has to say so")
	}
}

func TestMondayIndex(t *testing.T) {
	want := map[time.Weekday]int{
		time.Monday: 0, time.Tuesday: 1, time.Wednesday: 2, time.Thursday: 3,
		time.Friday: 4, time.Saturday: 5, time.Sunday: 6,
	}
	for day, idx := range want {
		if got := mondayIndex(day); got != idx {
			t.Errorf("%v: want row %d, got %d", day, idx, got)
		}
	}
}

func TestHeatLevelBoundaries(t *testing.T) {
	th := [3]int{2, 5, 9}
	for _, c := range []struct{ n, want int }{
		{0, 0}, {1, 1}, {2, 1}, {3, 2}, {5, 2}, {6, 3}, {9, 3}, {10, 4}, {99, 4},
	} {
		if got := heatLevel(c.n, th); got != c.want {
			t.Errorf("%d loans: want level %d, got %d", c.n, c.want, got)
		}
	}
}
