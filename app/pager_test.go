package main

import (
	"net/http/httptest"
	"testing"
)

// The arithmetic under the two buttons, on a list of two and a half pages.
func TestPage(t *testing.T) {
	total := 2*pageSize + pageSize/2
	cases := []struct {
		offset                 int
		from, to               int
		hasPrev, hasNext       bool
		prevOffset, nextOffset int
	}{
		{0, 1, pageSize, false, true, 0, pageSize},
		{pageSize, pageSize + 1, 2 * pageSize, true, true, 0, 2 * pageSize},
		{2 * pageSize, 2*pageSize + 1, total, true, false, pageSize, 3 * pageSize},
	}
	for _, c := range cases {
		p := page{Offset: c.offset, Total: total}
		if !p.Paged() {
			t.Errorf("offset %d: a list of %d rows is not paged", c.offset, total)
		}
		if p.From() != c.from || p.To() != c.to {
			t.Errorf("offset %d: rows %d–%d, want %d–%d", c.offset, p.From(), p.To(), c.from, c.to)
		}
		if p.HasPrev() != c.hasPrev || p.HasNext() != c.hasNext {
			t.Errorf("offset %d: prev %v next %v, want %v %v", c.offset, p.HasPrev(), p.HasNext(), c.hasPrev, c.hasNext)
		}
		if p.PrevOffset() != c.prevOffset || p.NextOffset() != c.nextOffset {
			t.Errorf("offset %d: turns to %d / %d, want %d / %d", c.offset, p.PrevOffset(), p.NextOffset(), c.prevOffset, c.nextOffset)
		}
	}

	// One page, or none: nothing to turn, and an empty list numbers no rows.
	if p := (page{Total: pageSize}); p.Paged() || p.From() != 1 || p.To() != pageSize {
		t.Errorf("a full single page: paged %v, rows %d–%d", p.Paged(), p.From(), p.To())
	}
	if p := (page{}); p.Paged() || p.From() != 0 || p.To() != 0 || p.HasNext() {
		t.Errorf("an empty list: paged %v, rows %d–%d, next %v", p.Paged(), p.From(), p.To(), p.HasNext())
	}
}

// An offset is fitted to the list: never past the end, on a page boundary.
func TestClampOffset(t *testing.T) {
	total := 2*pageSize + pageSize/2 // two pages and a half
	cases := []struct{ offset, total, want int }{
		{0, 0, 0},
		{pageSize, 0, 0},                      // an emptied list
		{0, total, 0},                         //
		{pageSize, total, pageSize},           //
		{2*pageSize + 3, total, 2 * pageSize}, // snapped to the page it falls in
		{50 * pageSize, total, 2 * pageSize},  // past the end: the last page
		{pageSize, pageSize, 0},               // exactly one page: no second one
		{-5, total, 0},                        //
	}
	for _, c := range cases {
		if got := clampOffset(c.offset, c.total); got != c.want {
			t.Errorf("clampOffset(%d, %d) = %d, want %d", c.offset, c.total, got, c.want)
		}
	}
}

func TestReadOffset(t *testing.T) {
	for raw, want := range map[string]int{"": 0, "100": 100, "-1": 0, "abc": 0, "12.5": 0} {
		r := httptest.NewRequest("GET", "/inventory?offset="+raw, nil)
		if got := readOffset(r); got != want {
			t.Errorf("offset=%q read as %d, want %d", raw, got, want)
		}
	}
}

// The screen's page is a slice of what the label sheet prints.
func TestPageInventory(t *testing.T) {
	loadForTest(t)
	restoreSettings(t)
	a := testApp(t)

	f := invFilter{Sort: "title", Dir: "asc"}
	all, err := a.listInventory(f)
	if err != nil {
		t.Fatalf("listInventory: %v", err)
	}
	rows, pg, err := a.pageInventory(f, 0)
	if err != nil {
		t.Fatalf("pageInventory: %v", err)
	}
	if pg.Total != len(all) {
		t.Errorf("total %d, the list has %d rows", pg.Total, len(all))
	}
	if want := min(len(all), pageSize); len(rows) != want {
		t.Errorf("%d rows on the first page, want %d", len(rows), want)
	}
	for i, r := range rows {
		if r.ID != all[i].ID {
			t.Fatalf("row %d is copy %s, the list has %s", i, r.Code, all[i].Code)
		}
	}
	if pg.Form != f.SortForm() || pg.Post == "" || pg.Target == "" {
		t.Errorf("the page does not know how to turn: %+v", pg)
	}

	// The filter narrows the count as it narrows the rows.
	rows, pg, err = a.pageInventory(invFilter{Q: "VOL204572", Sort: "title", Dir: "asc"}, 0)
	if err != nil {
		t.Fatalf("pageInventory (search): %v", err)
	}
	if len(rows) != 1 || pg.Total != 1 || pg.Paged() {
		t.Errorf("one copy searched for: %d rows, total %d, paged %v", len(rows), pg.Total, pg.Paged())
	}
}
