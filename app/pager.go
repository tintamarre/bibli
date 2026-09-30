package main

import (
	"net/http"
	"strconv"
)

// One page of /inventory or /borrowers. The offset is not part of the
// filter, which printouts and exports carry: the buttons post it beside the
// form, and anything posting the form alone starts at the top.
const pageSize = 27

type page struct {
	Offset int    // the first row shown, counted from 0
	Total  int    // the rows the filter selects, every page together
	Form   string // id of the filter form the buttons post along with
	Post   string
	Target string // selector of the list they replace
}

// Paged says whether there is more than one page.
func (p page) Paged() bool { return p.Total > pageSize }

// From and To number the rows on screen from 1, for "101–200 sur 2 480".
func (p page) From() int {
	if p.Total == 0 {
		return 0
	}
	return p.Offset + 1
}

func (p page) To() int { return min(p.Offset+pageSize, p.Total) }

func (p page) HasPrev() bool   { return p.Offset > 0 }
func (p page) HasNext() bool   { return p.Offset+pageSize < p.Total }
func (p page) PrevOffset() int { return max(p.Offset-pageSize, 0) }
func (p page) NextOffset() int { return p.Offset + pageSize }

// readOffset reads where the list opens; anything but a number from 0 up is
// the first page.
func readOffset(r *http.Request) int {
	n, err := strconv.Atoi(r.FormValue("offset"))
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// clampOffset fits an offset to the list as it stands, which may have shrunk
// since the button was drawn, snapped to a page boundary.
func clampOffset(offset, total int) int {
	if offset <= 0 || total == 0 {
		return 0
	}
	offset -= offset % pageSize
	if offset >= total {
		offset = (total - 1) / pageSize * pageSize
	}
	return offset
}
