package main

import "math"

// Each open loan drawn as a gauge of its own period: the track is the days
// granted, filled as they pass; a late loan's overrun sticks out past it. No
// axis is shared between rows, so one forgotten book squashes nothing. Nothing
// is stored.

// overrunFull is the delay, in days, at which the overrun reaches its full
// length; below it the length grows on a log scale, so 1, 7 and 30 days late
// look different while 150 does not push the column wider.
const overrunFull = 30

// Overrun length, in percent of the track: at one day late, and from
// overrunFull on.
const (
	overrunMin = 12.0
	overrunMax = 40.0
)

// loanGauge is one row's gauge, with the dates for its label.
type loanGauge struct {
	Valid   bool
	Overdue bool
	Used    int     // days since the book went out
	Period  int     // days granted
	Late    int     // days past the due date; 0 when on time
	Fill    float64 // percent of the track spent
	Over    float64 // overrun length, percent of the track; 0 when on time
	// The label names both dates.
	LoanedOn string
	DueOn    string
}

func loanGaugeToday(loanedOn, dueOn string) loanGauge {
	return gaugeOn(todayISO(), loanedOn, dueOn)
}

// gaugeOn measures one loan on a given day. An unreadable date draws nothing:
// the row still carries the dates in full.
func gaugeOn(today, loanedOn, dueOn string) loanGauge {
	period, ok1 := daysBetween(loanedOn, dueOn)
	used, ok2 := daysBetween(loanedOn, today)
	if !ok1 || !ok2 {
		return loanGauge{LoanedOn: loanedOn, DueOn: dueOn}
	}
	g := loanGauge{
		Valid:    true,
		Period:   int(math.Max(period, 0)),
		Used:     int(math.Max(used, 0)),
		LoanedOn: loanedOn,
		DueOn:    dueOn,
	}
	g.Late = g.Used - g.Period
	if g.Late > 0 {
		g.Overdue = true
		g.Fill = 100
		grow := math.Min(1, math.Log(1+float64(g.Late))/math.Log(1+overrunFull))
		g.Over = math.Round((overrunMin+(overrunMax-overrunMin)*grow)*100) / 100
		return g
	}
	g.Late = 0
	g.Fill = 100
	if g.Period > 0 {
		g.Fill = math.Round(float64(g.Used)/float64(g.Period)*10000) / 100
	}
	return g
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

// todayISO is the day the gauges are drawn against, on the same clock as the
// database's date('now').
func todayISO() string { return today().Format("2006-01-02") }
