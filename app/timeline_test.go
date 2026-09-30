package main

import (
	"math"
	"testing"
)

// Date arithmetic behind the timeline bars.

func TestNewTimelineCoversEveryDateItIsGiven(t *testing.T) {
	tl := newTimeline("2026-09-14", "2026-09-01", "2026-09-22", "2026-08-20", "2026-09-10")
	if tl.Start != "2026-08-20" {
		t.Errorf("Start = %s, want the earliest date given", tl.Start)
	}
	if tl.End != "2026-09-22" {
		t.Errorf("End = %s, want the latest date given", tl.End)
	}
	if tl.Span != 33 {
		t.Errorf("Span = %v days, want 33", tl.Span)
	}
}

// Today is always on the axis, because that is where every bar ends. A screen
// showing only loans that are long overdue must still reach it.
func TestNewTimelineAlwaysReachesToday(t *testing.T) {
	tl := newTimeline("2026-09-14", "2026-07-01", "2026-07-22")
	if tl.End != "2026-09-14" {
		t.Errorf("End = %s, want today", tl.End)
	}
	// And a list of loans all due in the future still starts no later.
	tl = newTimeline("2026-09-14", "2026-09-20", "2026-10-05")
	if tl.Start != "2026-09-14" {
		t.Errorf("Start = %s, want today", tl.Start)
	}
}

// One very old loan must not squash the rest of the axis.
func TestNewTimelineCapsHowFarBackItReaches(t *testing.T) {
	tl := newTimeline("2026-09-14", "2024-11-03", "2026-09-10")
	want, _ := addDays("2026-09-14", -timelineWindow)
	if tl.Start != want {
		t.Errorf("Start = %s, want the window floor %s", tl.Start, want)
	}
	if bar := loanBar(tl, "2024-11-03", "2024-11-24"); !bar.Clipped {
		t.Error("a loan older than the window is not marked as running off the edge")
	}
}

// A day with nothing in it is still an axis: nothing may divide by its width.
func TestNewTimelineNeverHasZeroWidth(t *testing.T) {
	tl := newTimeline("2026-09-14")
	if tl.Span < 1 {
		t.Fatalf("Span = %v", tl.Span)
	}
	if bar := loanBar(tl, "2026-09-14", "2026-09-14"); !bar.Valid {
		t.Error("a loan taken and due today draws nothing")
	}
}

func TestLoanBarOnTimeAndOverdue(t *testing.T) {
	// 40 days wide: the 1st to the 10th of the next month, today the 14th.
	tl := newTimeline("2026-09-14", "2026-09-01", "2026-10-10")

	// Borrowed on the 1st, due on the 22nd: a week and a half still to run, so
	// the capsule is longer than what has been spent of it.
	onTime := loanBar(tl, "2026-09-01", "2026-09-22")
	if onTime.Overdue || onTime.Late != 0 {
		t.Errorf("a loan due next week is drawn as late: %+v", onTime)
	}
	if onTime.Elapsed >= onTime.Period {
		t.Errorf("the bar reaches the end of the capsule: %+v", onTime)
	}
	if onTime.Left+onTime.Period != onTime.Due {
		t.Errorf("the capsule does not end on the due date: %+v", onTime)
	}
	// Nothing of it is overrun, so it is one colour all the way.
	if onTime.Stop != 100 {
		t.Errorf("a loan that is not late changes colour at %v%%", onTime.Stop)
	}

	// Borrowed on the 1st, due on the 8th: six days over. The capsule is full,
	// and the overrun is drawn past its end.
	late := loanBar(tl, "2026-09-01", "2026-09-08")
	if !late.Overdue || late.Late <= 0 {
		t.Errorf("a loan due last week is not drawn as late: %+v", late)
	}
	if late.Elapsed != late.Period {
		t.Errorf("a late loan does not fill its capsule: %+v", late)
	}
	// Six days of 40 is 15% of the axis.
	if math.Abs(late.Late-15) > 0.5 {
		t.Errorf("the overrun is %v%% of the axis, want about 15", late.Late)
	}
	// Borrowed 13 days ago and due 6 days ago: the colour changes seven
	// thirteenths of the way along the bar, where the due date falls.
	if math.Abs(late.Stop-100*7/13.0) > 0.5 {
		t.Errorf("the bar changes colour at %v%%, want about %v", late.Stop, 100*7/13.0)
	}
	if late.Bar != late.Elapsed+late.Late {
		t.Errorf("the bar is not the loan end to end: %+v", late)
	}
}

// Every bar ends at today, so the left edges alone rank the rows by age.
func TestEveryBarEndsAtToday(t *testing.T) {
	const now = "2026-09-14"
	loans := [][2]string{
		{"2026-09-01", "2026-09-22"}, // running
		{"2026-08-10", "2026-08-31"}, // late
		{"2026-09-13", "2026-10-04"}, // yesterday
		{"2026-06-02", "2026-06-23"}, // older than the window
	}
	var dates []string
	for _, l := range loans {
		dates = append(dates, l[0], l[1])
	}
	tl := newTimeline(now, dates...)

	var want float64
	for i, l := range loans {
		bar := loanBar(tl, l[0], l[1])
		end := bar.Left + bar.Bar
		if i == 0 {
			want = end
			continue
		}
		if math.Abs(end-want) > 1.01 { // the one-percent floor a same-day loan gets
			t.Errorf("loan %d ends at %v%% of the axis, the first at %v%%", i, end, want)
		}
	}
}

// An unreadable date draws nothing rather than a bar starting in 1970. The row
// still carries both dates in full beside it.
func TestLoanBarRefusesADateItCannotRead(t *testing.T) {
	tl := newTimeline("2026-09-14", "2026-09-01")
	for _, c := range [][2]string{{"", "2026-09-22"}, {"2026-09-01", "later"}, {"nope", ""}} {
		if bar := loanBar(tl, c[0], c[1]); bar.Valid {
			t.Errorf("loanBar(%q, %q) drew a bar", c[0], c[1])
		}
	}
}
