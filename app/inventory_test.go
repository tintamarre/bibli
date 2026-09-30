package main

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// Declaring an out book lost closes its loan, or it stays overdue for good.
func TestLosingACopyClosesItsLoan(t *testing.T) {
	a := testApp(t)
	// Copy 1 is out to Tom, and overdue.
	if n := count(t, a.db, `SELECT COUNT(*) FROM loan WHERE copy_id = 1 AND returned_on IS NULL`); n != 1 {
		t.Fatalf("fixture: %d open loans on copy 1, want 1", n)
	}

	closed, err := a.updateCopy(1, "lost", nil)
	if err != nil {
		t.Fatalf("updateCopy: %v", err)
	}
	if !closed {
		t.Error("updateCopy did not report closing the loan: the screen would say nothing")
	}
	if n := count(t, a.db, `SELECT COUNT(*) FROM loan WHERE copy_id = 1 AND returned_on IS NULL`); n != 0 {
		t.Errorf("%d loans still open on a lost copy", n)
	}
	// The loan is closed, not deleted: the work keeps its loan count.
	if n := count(t, a.db, `SELECT COUNT(*) FROM loan WHERE copy_id = 1`); n != 1 {
		t.Errorf("the loan was deleted rather than closed (%d rows left)", n)
	}
}

// A withdrawn copy will not come back either.
func TestWithdrawingACopyClosesItsLoan(t *testing.T) {
	a := testApp(t)
	closed, err := a.updateCopy(1, "withdrawn", nil)
	if err != nil {
		t.Fatalf("updateCopy: %v", err)
	}
	if !closed {
		t.Error("withdrawing a copy left its loan open")
	}
}

// "Damaged" closes nothing: the book is still with the child.
func TestMarkingACopyDamagedKeepsTheLoanOpen(t *testing.T) {
	a := testApp(t)
	closed, err := a.updateCopy(1, "damaged", nil)
	if err != nil {
		t.Fatalf("updateCopy: %v", err)
	}
	if closed {
		t.Error("marking a copy damaged closed the loan: the book is still out")
	}
	if n := count(t, a.db, `SELECT COUNT(*) FROM loan WHERE copy_id = 1 AND returned_on IS NULL`); n != 1 {
		t.Errorf("%d open loans, want 1", n)
	}
}

// A copy on the shelf has no loan to close, and that is not a failure.
func TestUpdatingACopyOnTheShelfClosesNothing(t *testing.T) {
	a := testApp(t)
	closed, err := a.updateCopy(2, "lost", nil)
	if err != nil {
		t.Fatalf("updateCopy: %v", err)
	}
	if closed {
		t.Error("a loan was reported closed on a copy that was not out")
	}
	var status string
	a.db.QueryRow(`SELECT status FROM copy WHERE id = 2`).Scan(&status)
	if status != "lost" {
		t.Errorf("status = %q, want lost", status)
	}
}

// A nil location leaves it alone; an explicit empty one clears it.
func TestUpdateCopyLeavesTheLocationAloneWhenNotGiven(t *testing.T) {
	a := testApp(t)
	if _, err := a.updateCopy(2, "damaged", nil); err != nil {
		t.Fatalf("updateCopy: %v", err)
	}
	var location string
	a.db.QueryRow(`SELECT COALESCE(location, '') FROM copy WHERE id = 2`).Scan(&location)
	if location != "bac albums" {
		t.Errorf("location = %q, want it unchanged", location)
	}

	// An empty location, given explicitly, does clear it.
	empty := ""
	if _, err := a.updateCopy(2, "damaged", &empty); err != nil {
		t.Fatalf("updateCopy: %v", err)
	}
	var stored *string
	a.db.QueryRow(`SELECT location FROM copy WHERE id = 2`).Scan(&stored)
	if stored != nil {
		t.Errorf("location = %q, want NULL", *stored)
	}
}

// Every word must match the title, subtitle, an author, the code or the ISBN.
func TestListInventoryFilter(t *testing.T) {
	a := testApp(t)

	all, err := a.listInventory(invFilter{Sort: invSortDefault, Dir: "asc"})
	if err != nil {
		t.Fatalf("listInventory: %v", err)
	}
	if len(all) != 5 {
		t.Fatalf("%d rows for an empty filter, want 5", len(all))
	}

	cases := []struct {
		q    string
		want int
		why  string
	}{
		{"loup", 2, "title"},
		{"LOUP", 2, "case does not matter"},
		{"pennart", 2, "author: Le loup est revenu's two copies"},
		{"exupéry", 2, "author, accented: Le Petit Prince's two copies"},
		{"aquarelles", 2, "subtitle of Le Petit Prince, on neither title nor author"},
		{"VOL146302", 1, "internal code"},
		{"9782070408504", 2, "ISBN"},
		{"2070408507", 2, "the ISBN-10, as printed on the copyright page of an older book"},
		{"2-07-040850-7", 2, "the ISBN-10 with its hyphens"},
		{"ISBN 2-07-040850-7", 2, "copied with its label"},
		{"978-2-07-040850-4", 2, "the ISBN-13 with its hyphens"},
		{"2070408", 2, "the start of an ISBN-10, typed into the live search"},
		{"2-07-040850-8", 0, "an ISBN-10 with a wrong check digit is a typo, not a match"},
		{"loup revenu", 2, "two words, both matching"},
		{"loup prince", 0, "two words, no row matching both"},
		{"prince exupéry", 2, "two words across title and author, both on the same book"},
		{"zzz", 0, "no match"},
	}
	for _, c := range cases {
		rows, err := a.listInventory(invFilter{Q: c.q, Sort: invSortDefault, Dir: "asc"})
		if err != nil {
			t.Fatalf("listInventory(%q): %v", c.q, err)
		}
		if len(rows) != c.want {
			t.Errorf("listInventory(%q) = %d rows, want %d (%s)", c.q, len(rows), c.want, c.why)
		}
	}
}

// The list says what is out.
func TestListInventoryMarksWhatIsOut(t *testing.T) {
	a := testApp(t)
	rows, err := a.listInventory(invFilter{Q: "VOL204572", Sort: invSortDefault, Dir: "asc"})
	if err != nil {
		t.Fatalf("listInventory: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("%d rows, want 1", len(rows))
	}
	if !rows[0].Out {
		t.Error("VOL204572 is out to Tom and is not marked as such")
	}
	if rows[0].ISBN != "9782070408504" || rows[0].Title != "Le Petit Prince" {
		t.Errorf("row: %+v", rows[0])
	}

	rows, _ = a.listInventory(invFilter{Q: "VOL811045", Sort: invSortDefault, Dir: "asc"})
	if rows[0].Out {
		t.Error("VOL811045 is on the shelf and is marked as out")
	}
}

// Only these four statuses exist; anything else is a typo or a forged form.
func TestValidStatuses(t *testing.T) {
	for _, s := range []string{"available", "damaged", "lost", "withdrawn"} {
		if !validStatuses[s] {
			t.Errorf("%s should be a valid status", s)
		}
	}
	for _, s := range []string{"", "disponible", "out", "borrowed"} {
		if validStatuses[s] {
			t.Errorf("%q accepted as a status", s)
		}
	}
	// Only the two meaning "it will not come back" close a loan.
	if !statusesClosingLoan["lost"] || !statusesClosingLoan["withdrawn"] {
		t.Error("lost and withdrawn must close the loan")
	}
	if statusesClosingLoan["damaged"] || statusesClosingLoan["available"] {
		t.Error("only a book that will not come back closes its loan")
	}
}

// --- Filtering and ordering ------------------------------------------------

// The sort column is concatenated into SQL (an ORDER BY cannot be a parameter),
// so it is only ever a map key; anything unrecognised takes the default.
func TestInvSortIsAWhitelistAndNeverTheRequestsWords(t *testing.T) {
	for _, bad := range []string{
		"", "nope", "b.title; DROP TABLE copy", "c.code--", "title)", "TITLE",
	} {
		r := httptest.NewRequest("GET", "/inventory?sort="+url.QueryEscape(bad)+"&dir=sideways", nil)
		f := readInvFilter(r)
		if f.Sort != invSortDefault {
			t.Errorf("sort=%q was kept as %q, want the default", bad, f.Sort)
		}
		// An unrecognised column takes the default order whole, direction included.
		if f.Dir != invSortDefaultDir {
			t.Errorf("dir=sideways was kept as %q", f.Dir)
		}
		if got := invOrderBy(f); strings.Contains(got, bad) && bad != "" {
			t.Errorf("ORDER BY carries the request's own words: %q", got)
		}
	}
	// Every column the headings offer is in the map.
	for _, key := range []string{"mark", "title", "isbn", "code", "location", "status", "loan", "added"} {
		r := httptest.NewRequest("GET", "/inventory?sort="+key+"&dir=desc", nil)
		f := readInvFilter(r)
		if f.Sort != key {
			t.Errorf("sort=%q fell back to %q", key, f.Sort)
		}
		if !strings.Contains(invOrderBy(f), "DESC") {
			t.Errorf("%s: the descending order is not descending: %q", key, invOrderBy(f))
		}
	}
}

// A status that is not a status is dropped rather than queried with.
func TestReadInvFilterDropsAnImpossibleStatus(t *testing.T) {
	r := httptest.NewRequest("GET", "/inventory?status=melted&location=+coin+lecture+&q=+loup+", nil)
	f := readInvFilter(r)
	if f.Status != "" {
		t.Errorf("status = %q, want it dropped", f.Status)
	}
	if f.Location != "coin lecture" || f.Q != "loup" {
		t.Errorf("filter = %+v, want the stray spaces gone", f)
	}
}

// The location dropdown offers whatever has been used, with "nowhere yet" last.
func TestCopyLocations(t *testing.T) {
	a := testApp(t)
	locs, err := a.copyLocations()
	if err != nil {
		t.Fatalf("copyLocations: %v", err)
	}
	want := []LocationCount{
		{"bac albums", 2}, {"classe P3", 2}, {"coin lecture", 1},
	}
	if len(locs) != len(want) {
		t.Fatalf("locations = %+v, want %+v", locs, want)
	}
	for i, w := range want {
		if locs[i] != w {
			t.Errorf("location %d = %+v, want %+v", i, locs[i], w)
		}
	}

	// Unfile one, and it appears last.
	if _, err := a.db.Exec(`UPDATE copy SET location = NULL WHERE code = 'VOL811045'`); err != nil {
		t.Fatal(err)
	}
	locs, _ = a.copyLocations()
	if last := locs[len(locs)-1]; last.Location != "" || last.Count != 1 {
		t.Errorf("the unfiled copy is at %+v, want it last and alone", last)
	}
}

// A location selects the same copies on the inventory and the label sheet.
func TestFilteringByLocation(t *testing.T) {
	a := testApp(t)

	rows, err := a.listInventory(invFilter{Location: "coin lecture", Sort: invSortDefault, Dir: "asc"})
	if err != nil {
		t.Fatalf("listInventory: %v", err)
	}
	if len(rows) != 1 || rows[0].Code != "VOL146302" {
		t.Errorf("inventory at the reading corner = %+v, want VOL146302 alone", rows)
	}

	// The label sheet selects the same copies.
	labels := labelsForCopies(rows)
	if len(labels) != 1 || labels[0].Code != "VOL146302" || labels[0].Mark == "" {
		t.Errorf("labels at the reading corner = %+v, want VOL146302 alone with a shelf mark", labels)
	}

	// The sentinel is the copies with no location, not "every location".
	if _, err := a.db.Exec(`UPDATE copy SET location = NULL WHERE code = 'VOL811045'`); err != nil {
		t.Fatal(err)
	}
	unfiled, err := a.listInventory(invFilter{Location: locationFilterNone, Sort: invSortDefault, Dir: "asc"})
	if err != nil {
		t.Fatalf("listInventory: %v", err)
	}
	if len(unfiled) != 1 || unfiled[0].Code != "VOL811045" {
		t.Errorf("unfiled = %+v, want VOL811045 alone", unfiled)
	}
	everywhere, _ := a.listInventory(invFilter{Location: "", Sort: invSortDefault, Dir: "asc"})
	if len(everywhere) != 5 {
		t.Errorf("an empty location selected %d copies, want all 5", len(everywhere))
	}
}

// Out, on the shelf, or neither: a lost or withdrawn copy has no open loan and
// is not on the shelf either.
func TestFilteringByLoan(t *testing.T) {
	a := testApp(t)

	// Through updateCopy, not a raw UPDATE, so the loan is closed as the
	// application closes it.
	var id int64
	if err := a.db.QueryRow(`SELECT id FROM copy WHERE code = 'VOL350929'`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err := a.updateCopy(id, "lost", nil); err != nil {
		t.Fatalf("updateCopy: %v", err)
	}

	list := func(loan string) []InvRow {
		t.Helper()
		rows, err := a.listInventory(invFilter{Loan: loan, Sort: invSortDefault, Dir: "asc"})
		if err != nil {
			t.Fatalf("listInventory(%q): %v", loan, err)
		}
		return rows
	}
	all, out, shelf := list(""), list(invLoanOut), list(invLoanShelf)

	if hasCode(shelf, "VOL350929") {
		t.Error("a lost copy is offered as on the shelf: someone is sent to look for it")
	}
	if hasCode(out, "VOL350929") {
		t.Error("a lost copy is offered as out")
	}
	if !hasCode(all, "VOL350929") {
		t.Error("a lost copy vanished from the unfiltered list")
	}

	// The two halves do not add up to the whole: the difference is the copies that are gone.
	gone := 0
	for _, r := range all {
		if !r.Out && !onShelf(r.Status) {
			gone++
		}
	}
	if gone == 0 {
		t.Fatal("no copy is neither out nor on a shelf: the case is untested")
	}
	if len(out)+len(shelf)+gone != len(all) {
		t.Errorf("%d out + %d on the shelf + %d gone = %d, want the %d of the whole list",
			len(out), len(shelf), gone, len(out)+len(shelf)+gone, len(all))
	}

	// Each side agrees with what the row says, binding the filter's SQL to statusesOnShelf.
	for _, r := range out {
		if !r.Out {
			t.Errorf("%s is in the out list and is not marked out", r.Code)
		}
	}
	for _, r := range shelf {
		if r.Out || !onShelf(r.Status) {
			t.Errorf("%s is in the shelf list but is out=%v status=%q", r.Code, r.Out, r.Status)
		}
	}

	// VOL204572 is out to Tom (the demo dataset), so it is on one side only.
	if !hasCode(out, "VOL204572") || hasCode(shelf, "VOL204572") {
		t.Error("VOL204572 is out to Tom and should appear out, and only out")
	}

	// The filter travels, so a printed sheet shows the copies the screen shows.
	if q := (invFilter{Loan: invLoanShelf}).Query(); !strings.Contains(q, "loan=shelf") {
		t.Errorf("Query() = %q, want it to carry loan=shelf", q)
	}
	if !(invFilter{Loan: invLoanOut}).Filtering() {
		t.Error("a list narrowed to what is out does not say it is filtered")
	}
}

// A damaged copy is still on its shelf; a lost or withdrawn one is not.
func TestOnShelfStatuses(t *testing.T) {
	for _, s := range []string{"available", "damaged"} {
		if !onShelf(s) {
			t.Errorf("%q should be a copy someone can find on a shelf", s)
		}
	}
	for _, s := range []string{"lost", "withdrawn", "", "sorti"} {
		if onShelf(s) {
			t.Errorf("%q should not be a copy someone can find on a shelf", s)
		}
	}
}

func hasCode(rows []InvRow, code string) bool {
	for _, r := range rows {
		if r.Code == code {
			return true
		}
	}
	return false
}

func TestFilteringByStatus(t *testing.T) {
	a := testApp(t)
	if _, err := a.db.Exec(`UPDATE copy SET status = 'damaged' WHERE code = 'VOL350929'`); err != nil {
		t.Fatal(err)
	}
	rows, err := a.listInventory(invFilter{Status: "damaged", Sort: invSortDefault, Dir: "asc"})
	if err != nil {
		t.Fatalf("listInventory: %v", err)
	}
	if len(rows) != 1 || rows[0].Code != "VOL350929" {
		t.Errorf("damaged = %+v, want VOL350929 alone", rows)
	}
}

// Every column sorts, clicking again reverses it, and ties break the same way every time.
func TestListInventorySortsByEveryColumn(t *testing.T) {
	a := testApp(t)
	for _, column := range []string{"mark", "title", "isbn", "code", "location", "status", "loan"} {
		asc, err := a.listInventory(invFilter{Sort: column, Dir: "asc"})
		if err != nil {
			t.Fatalf("%s: %v", column, err)
		}
		desc, err := a.listInventory(invFilter{Sort: column, Dir: "desc"})
		if err != nil {
			t.Fatalf("%s desc: %v", column, err)
		}
		if len(asc) != 5 || len(desc) != 5 {
			t.Fatalf("%s: sorting changed how many rows there are (%d/%d)", column, len(asc), len(desc))
		}
		if asc[0].Code == desc[0].Code && column != "status" {
			t.Errorf("%s: ascending and descending start with the same copy (%s)", column, asc[0].Code)
		}
		// Same query twice, same order: no ORDER BY may leave ties loose.
		again, _ := a.listInventory(invFilter{Sort: column, Dir: "asc"})
		for i := range asc {
			if asc[i].Code != again[i].Code {
				t.Errorf("%s: the order is not stable at row %d", column, i)
				break
			}
		}
	}

	// By location, the unfiled after the shelves.
	if _, err := a.db.Exec(`UPDATE copy SET location = NULL WHERE code = 'VOL811045'`); err != nil {
		t.Fatal(err)
	}
	rows, _ := a.listInventory(invFilter{Sort: "location", Dir: "asc"})
	if rows[len(rows)-1].Code != "VOL811045" {
		t.Errorf("the copy with no shelf is not last: %+v", rows[len(rows)-1])
	}
}

// A screen nobody has sorted opens on the copies just recorded.
func TestInventoryOpensOnTheNewestCopies(t *testing.T) {
	a := testApp(t)
	if _, err := a.db.Exec(
		`UPDATE copy SET created_at = datetime('now', '-30 days') WHERE code = 'VOL146302'`,
	); err != nil {
		t.Fatalf("backdating a copy: %v", err)
	}

	f := readInvFilter(httptest.NewRequest("GET", "/inventory", nil))
	if f.Sort != "added" || f.Dir != "desc" {
		t.Fatalf("an unsorted screen opens on %s %s, want added desc", f.Sort, f.Dir)
	}
	rows, err := a.listInventory(f)
	if err != nil {
		t.Fatalf("listInventory: %v", err)
	}
	if last := rows[len(rows)-1]; last.Code != "VOL146302" {
		t.Errorf("the copy recorded last month is at %s, want it last", last.Code)
	}
}

// A row redrawn after an inline edit is the row the list would have drawn: the
// swap replaces the whole <tr>.
func TestEditedRowKeepsEveryColumn(t *testing.T) {
	a := testApp(t)

	listed, err := a.listInventory(invFilter{Q: "VOL204572", Sort: invSortDefault, Dir: invSortDefaultDir})
	if err != nil || len(listed) != 1 {
		t.Fatalf("listInventory = %v, %v", listed, err)
	}
	if _, err := a.updateCopy(listed[0].ID, "damaged", nil); err != nil {
		t.Fatalf("updateCopy: %v", err)
	}

	row, err := a.invRow(listed[0].ID)
	if err != nil {
		t.Fatalf("invRow: %v", err)
	}
	if row.AddedOn != listed[0].AddedOn || row.AddedOn == "" {
		t.Errorf("added on = %q, want the listed %q", row.AddedOn, listed[0].AddedOn)
	}
	if row.Title != listed[0].Title || row.ISBN != listed[0].ISBN ||
		row.Code != listed[0].Code || row.Location != listed[0].Location ||
		row.BookID != listed[0].BookID || row.Out != listed[0].Out {
		t.Errorf("redrawn row = %+v, want the listed one %+v", row, listed[0])
	}
	if row.Status != "damaged" {
		t.Errorf("status = %q, want the edit it was just given", row.Status)
	}
}

// A period travels as its key, so a bookmark stays relative to today.
func TestAddedPeriodResolvesAgainstToday(t *testing.T) {
	now := time.Date(2026, 9, 16, 15, 30, 0, 0, time.UTC)
	cases := []struct {
		f              invFilter
		wantFrom, want string
	}{
		{invFilter{Added: "today"}, "2026-09-16", ""},
		{invFilter{Added: "7d"}, "2026-09-09", ""},
		{invFilter{Added: "30d"}, "2026-08-17", ""},
		{invFilter{}, "", ""},
		// The exact range, and anything unrecognised, is the two dates typed.
		{invFilter{Added: addedCustom, From: "2026-03-01", To: "2026-03-17"}, "2026-03-01", "2026-03-17"},
		{invFilter{From: "2026-03-01"}, "2026-03-01", ""},
	}
	for _, c := range cases {
		from, to := c.f.boundsFrom(now)
		if from != c.wantFrom || to != c.want {
			t.Errorf("%+v bounds = (%q, %q), want (%q, %q)", c.f, from, to, c.wantFrom, c.want)
		}
	}
}

// Hidden date fields keep posting what was last typed; the period wins.
func TestReadInvFilterPeriodDropsTheDates(t *testing.T) {
	r := httptest.NewRequest("GET", "/inventory?added=7d&from=2026-03-01&to=2026-03-17", nil)
	f := readInvFilter(r)
	if f.Added != "7d" || f.From != "" || f.To != "" {
		t.Errorf("filter = %+v, want the period alone", f)
	}
	if f.RangeOpen() {
		t.Error("the date fields show although a period is chosen")
	}

	// A period nobody offers is every date, not an error page.
	f = readInvFilter(httptest.NewRequest("GET", "/inventory?added=since+1789", nil))
	if f.Added != "" {
		t.Errorf("added = %q, want it dropped", f.Added)
	}

	// An address carrying dates shows them and selects them.
	f = readInvFilter(httptest.NewRequest("GET", "/inventory?from=2026-03-01&to=2026-03-17", nil))
	if !f.RangeOpen() || !f.Filtering() {
		t.Errorf("filter = %+v, want the range open and filtering", f)
	}
	if from, to := f.Bounds(); from != "2026-03-01" || to != "2026-03-17" {
		t.Errorf("bounds = (%q, %q), want the dates from the address", from, to)
	}
}

// And the period really selects: the same question the two dates answered.
func TestFilteringByAddedPeriod(t *testing.T) {
	a := testApp(t)
	if _, err := a.db.Exec(
		`UPDATE copy SET created_at = datetime('now', '-30 days') WHERE code = 'VOL146302'`,
	); err != nil {
		t.Fatalf("backdating a copy: %v", err)
	}

	recent, err := a.listInventory(invFilter{Added: "7d"})
	if err != nil {
		t.Fatalf("listInventory: %v", err)
	}
	if len(recent) != 4 {
		t.Errorf("the last 7 days hold %d copies, want the 4 recorded today", len(recent))
	}
	for _, l := range recent {
		if l.Code == "VOL146302" {
			t.Error("the copy recorded a month ago came back in the last 7 days")
		}
	}
	all, _ := a.listInventory(invFilter{})
	if len(all) != 5 {
		t.Errorf("no period selected %d copies, want all 5", len(all))
	}
}

// By cote, the list reads as the shelf does, and the cote it shows is the one
// the label prints.
func TestListInventorySortsByCote(t *testing.T) {
	a := testApp(t)
	rows, err := a.listInventory(invFilter{Sort: "mark", Dir: "asc"})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range rows {
		if want := shelfMark(r.Authors, r.Title); r.Mark != want {
			t.Errorf("%s: cote %q, the label prints %q", r.Code, r.Mark, want)
		}
		got = append(got, r.Mark)
	}
	// Anonyme is filed by its title (ALB), Pennart at PEN, Saint-Exupéry at SAI.
	if want := []string{"ALB", "PEN", "PEN", "SAI", "SAI"}; strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("by cote: %v, want %v", got, want)
	}
}
