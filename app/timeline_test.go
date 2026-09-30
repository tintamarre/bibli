package main

import "testing"

// Date arithmetic behind the loan gauges.

func TestGaugeOnTime(t *testing.T) {
	g := gaugeOn("2026-09-14", "2026-09-01", "2026-09-22")
	if !g.Valid || g.Overdue {
		t.Fatalf("gauge = %+v, want a valid loan on time", g)
	}
	if g.Used != 13 || g.Period != 21 || g.Late != 0 || g.Over != 0 {
		t.Errorf("used %d / %d, late %d, over %v; want 13 / 21, not late", g.Used, g.Period, g.Late, g.Over)
	}
	if g.Fill != 61.9 {
		t.Errorf("Fill = %v%%, want 61.9", g.Fill)
	}
}

func TestGaugeEnds(t *testing.T) {
	if g := gaugeOn("2026-09-14", "2026-09-14", "2026-10-05"); g.Used != 0 || g.Fill != 0 {
		t.Errorf("borrowed today: used %d, fill %v; want an empty track", g.Used, g.Fill)
	}
	if g := gaugeOn("2026-09-14", "2026-08-24", "2026-09-14"); g.Overdue || g.Fill != 100 {
		t.Errorf("due today: overdue %v, fill %v; want a full track, not late", g.Overdue, g.Fill)
	}
	// Lent and due the same day: a full track, not a division by zero.
	if g := gaugeOn("2026-09-14", "2026-09-14", "2026-09-14"); !g.Valid || g.Fill != 100 {
		t.Errorf("zero-day loan = %+v, want a full valid track", g)
	}
}

func TestGaugeOverrunGrowsThenStops(t *testing.T) {
	over := func(late int) float64 {
		t.Helper()
		due := "2026-09-01"
		d, _ := parseDate(due)
		today := d.AddDate(0, 0, late).Format("2006-01-02")
		g := gaugeOn(today, "2026-08-11", due)
		if !g.Overdue || g.Late != late || g.Fill != 100 {
			t.Fatalf("%d days late: %+v", late, g)
		}
		return g.Over
	}
	one, week, month, forgotten := over(1), over(7), over(overrunFull), over(150)
	if one < overrunMin || !(one < week && week < month) {
		t.Errorf("overrun at 1, 7, 30 days = %v, %v, %v; want it to grow from %v", one, week, month, overrunMin)
	}
	if month != overrunMax || forgotten != overrunMax {
		t.Errorf("overrun at %d and 150 days = %v, %v; want both at the %v cap", overrunFull, month, forgotten, overrunMax)
	}
}

func TestGaugeRefusesADateItCannotRead(t *testing.T) {
	for _, c := range [][2]string{{"", "2026-09-22"}, {"2026-09-01", "not a date"}} {
		if g := gaugeOn("2026-09-14", c[0], c[1]); g.Valid {
			t.Errorf("gaugeOn(%q, %q) drew a gauge", c[0], c[1])
		}
	}
}
