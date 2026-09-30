package main

import "math"

// Open loans drawn on one time axis per screen: a capsule for what was
// granted, with the loan over it as one shape that changes colour at the due
// date. Every bar ends at today. Nothing is stored.

// timelineWindow caps how far back the axis reaches, in days, so one forgotten
// book does not squash the rest. Past it a bar is Clipped at the left edge.
const timelineWindow = 120

// timeline is the axis one screen's bars are measured against.
type timeline struct {
	Start string  // ISO, the left edge
	End   string  // ISO, the right edge
	Today string  // ISO
	Span  float64 // days from Start to End, never below 1
}

// newTimeline reads the axis off the borrow and due dates a screen is about to
// draw. Today is always inside it.
func newTimeline(todayISO string, dates ...string) timeline {
	start, end := todayISO, todayISO
	for _, d := range dates {
		if len(d) < 10 {
			continue
		}
		if d = d[:10]; d < start {
			start = d
		} else if d > end {
			end = d
		}
	}
	if floor, ok := addDays(todayISO, -timelineWindow); ok && start < floor {
		start = floor
	}
	span, ok := daysBetween(start, end)
	if !ok || span < 1 {
		span = 1
	}
	return timeline{Start: start, End: end, Today: todayISO, Span: span}
}

// loanSpan is one row, in percentages of the axis, with the dates for its label.
type loanSpan struct {
	Valid   bool
	Overdue bool
	Clipped bool    // it started before the axis does
	Left    float64 // where the loan begins
	Period  float64 // the capsule: from there to the day it is due
	Due     float64 // where the capsule ends
	Elapsed float64 // the part of the capsule already spent
	Late    float64 // past the capsule to today; 0 when the book is not late
	// The loan from the day it went out to today, and how far along it the due
	// date falls; past that point the bar is the overrun colour.
	Bar      float64
	Stop     float64
	LoanedOn string
	DueOn    string
}

// loanBar places one loan on the axis. An unreadable date draws nothing rather
// than a bar starting in 1970: the row still carries the dates in full.
func loanBar(t timeline, loanedOn, dueOn string) loanSpan {
	from, ok1 := daysBetween(t.Start, loanedOn)
	due, ok2 := daysBetween(t.Start, dueOn)
	now, ok3 := daysBetween(t.Start, t.Today)
	if !ok1 || !ok2 || !ok3 {
		return loanSpan{LoanedOn: loanedOn, DueOn: dueOn}
	}

	// Two decimals, so the style attribute stays readable: a difference of two
	// rounded percentages is not itself round.
	round := func(v float64) float64 { return math.Round(v*100) / 100 }
	pct := func(days float64) float64 {
		return round(math.Min(math.Max(days/t.Span*100, 0), 100))
	}
	s := loanSpan{
		Valid:    true,
		Clipped:  from < 0,
		Overdue:  due < now,
		Left:     pct(from),
		Due:      pct(due),
		LoanedOn: loanedOn,
		DueOn:    dueOn,
	}
	// A zero-length capsule or loan is still drawn as a stub: the book is out.
	s.Period = round(math.Max(s.Due-s.Left, 1))
	s.Elapsed = round(math.Max(math.Min(pct(now), s.Due)-s.Left, 1))
	if s.Overdue {
		s.Late = round(math.Max(pct(now)-s.Due, 1))
	}
	s.Bar = round(s.Elapsed + s.Late)
	s.Stop = 100
	if s.Late > 0 {
		s.Stop = round(s.Elapsed / s.Bar * 100)
	}
	return s
}

// daysBetween counts whole days from one ISO date to another. Both are stored
// at midnight UTC, the clock the due dates are computed on (dates.go).
func daysBetween(from, to string) (float64, bool) {
	a, ok1 := parseDate(from)
	b, ok2 := parseDate(to)
	if !ok1 || !ok2 {
		return 0, false
	}
	return b.Sub(a).Hours() / 24, true
}

func addDays(iso string, days int) (string, bool) {
	d, ok := parseDate(iso)
	if !ok {
		return "", false
	}
	return d.AddDate(0, 0, days).Format("2006-01-02"), true
}

// todayISO is the day the bars are drawn against, on the same clock as the
// database's date('now').
func todayISO() string { return today().Format("2006-01-02") }
