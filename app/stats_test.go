package main

import (
	"database/sql"
	"testing"
)

// Every fixture below is written relative to date('now'), so the numbers
// asserted are the same whatever day the test runs.

func statsApp(t *testing.T) *app {
	t.Helper()
	return &app{db: loadInto(t, "testdata/fixture.sql")}
}

func exec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

func TestBorrowerStatsCountsOnlyTheWindow(t *testing.T) {
	a := statsApp(t)
	exec(t, a.db, `DELETE FROM loan`)
	exec(t, a.db, `INSERT INTO loan (copy_id, borrower_id, loaned_on, due_on, returned_on) VALUES
	  -- Inside the window: 10 days.
	  (1, 101, date('now','-30 days'), date('now','-9 days'),  date('now','-20 days')),
	  -- Entirely before it: counts for nothing at all.
	  (2, 101, date('now','-3 years'), date('now','-3 years','+21 days'), date('now','-3 years','+7 days'))`)

	s := a.borrowerStats(101)
	if s.Loans != 1 {
		t.Errorf("loans in the window: want 1, got %d", s.Loans)
	}
	if s.BookDays != 10 {
		t.Errorf("book-days: want 10, got %d", s.BookDays)
	}
}

// A loan that started before the window and ended inside it contributes only
// the days that fall inside: the figure must never exceed the year it names.
func TestBorrowerStatsClipsALoanToTheWindow(t *testing.T) {
	a := statsApp(t)
	exec(t, a.db, `DELETE FROM loan`)
	exec(t, a.db, `INSERT INTO loan (copy_id, borrower_id, loaned_on, due_on, returned_on) VALUES
	  (1, 101, date('now','-14 months'), date('now','-13 months'), date('now','-11 months'))`)

	s := a.borrowerStats(101)
	if s.Loans != 0 {
		t.Errorf("a loan started before the window is not one of the year's loans, got %d", s.Loans)
	}
	// From the start of the window to the return: one month, 28 to 31 days.
	if s.BookDays < 28 || s.BookDays > 31 {
		t.Errorf("book-days clipped to the window: want a month, got %d", s.BookDays)
	}
}

// An open loan is counted up to today and no further.
func TestBorrowerStatsCountsAnOpenLoanUpToToday(t *testing.T) {
	a := statsApp(t)
	exec(t, a.db, `DELETE FROM loan`)
	exec(t, a.db, `INSERT INTO loan (copy_id, borrower_id, loaned_on, due_on) VALUES
	  (1, 101, date('now','-5 days'), date('now','+16 days'))`)

	s := a.borrowerStats(101)
	if s.Loans != 1 || s.BookDays != 5 {
		t.Errorf("open loan: want 1 loan and 5 book-days, got %d and %d", s.Loans, s.BookDays)
	}
}

func TestBorrowerStatsWithoutLoans(t *testing.T) {
	a := statsApp(t)
	exec(t, a.db, `DELETE FROM loan`)

	s := a.borrowerStats(101)
	if s.Loans != 0 || s.BookDays != 0 {
		t.Errorf("no loans: want zeroes, got %d and %d", s.Loans, s.BookDays)
	}
}

// Two copies owned for 100 days each is 200 copy-days; 50 of them spent out is
// a quarter of the time, not a half.
func TestBookStatsCountsCopyDaysOnBothSides(t *testing.T) {
	a := statsApp(t)
	exec(t, a.db, `DELETE FROM loan`)
	exec(t, a.db, `DELETE FROM copy`)
	exec(t, a.db, `INSERT INTO copy (id, book_id, code, acquired_on) VALUES
	  (901, 1, 'VOL900016', date('now','-100 days')),
	  (902, 1, 'VOL900027', date('now','-100 days'))`)
	exec(t, a.db, `INSERT INTO loan (copy_id, borrower_id, loaned_on, due_on, returned_on) VALUES
	  (901, 101, date('now','-90 days'), date('now','-69 days'), date('now','-70 days')),
	  (902, 102, date('now','-60 days'), date('now','-39 days'), date('now','-30 days'))`)

	s := a.bookStats(1)
	if !s.HasShare {
		t.Fatal("a book owned for 100 days has a share to state")
	}
	if s.Readers != 2 {
		t.Errorf("distinct readers: want 2, got %d", s.Readers)
	}
	if s.OutShare != 25 { // (20 + 30) days out of 200 copy-days
		t.Errorf("share of time out: want 25, got %d", s.OutShare)
	}
}

// The same child twice is one reader, and two loans.
func TestBookStatsCountsAReaderOnce(t *testing.T) {
	a := statsApp(t)
	exec(t, a.db, `DELETE FROM loan`)
	exec(t, a.db, `DELETE FROM copy`)
	exec(t, a.db, `INSERT INTO copy (id, book_id, code, acquired_on) VALUES
	  (901, 1, 'VOL900016', date('now','-100 days'))`)
	exec(t, a.db, `INSERT INTO loan (copy_id, borrower_id, loaned_on, due_on, returned_on) VALUES
	  (901, 101, date('now','-90 days'), date('now','-69 days'), date('now','-80 days')),
	  (901, 101, date('now','-40 days'), date('now','-19 days'), date('now','-30 days'))`)

	if s := a.bookStats(1); s.Readers != 1 {
		t.Errorf("one child borrowing twice is one reader, got %d", s.Readers)
	}
}

// Catalogued today: it owns no whole day, so it has no share to state.
func TestBookStatsSaysNothingOnItsFirstDay(t *testing.T) {
	a := statsApp(t)
	exec(t, a.db, `DELETE FROM loan`)
	exec(t, a.db, `DELETE FROM copy`)
	exec(t, a.db, `INSERT INTO copy (id, book_id, code, acquired_on) VALUES
	  (901, 1, 'VOL900016', date('now'))`)

	s := a.bookStats(1)
	if s.HasShare {
		t.Error("a book catalogued today states no share")
	}
	if s.OutShare != 0 {
		t.Errorf("share: want 0, got %d", s.OutShare)
	}
}

// A copy acquired after it was first lent must not report more than 100 %.
func TestBookStatsCapsTheShareAtAllOfTheTime(t *testing.T) {
	a := statsApp(t)
	exec(t, a.db, `DELETE FROM loan`)
	exec(t, a.db, `DELETE FROM copy`)
	exec(t, a.db, `INSERT INTO copy (id, book_id, code, acquired_on) VALUES
	  (901, 1, 'VOL900016', date('now','-10 days'))`)
	exec(t, a.db, `INSERT INTO loan (copy_id, borrower_id, loaned_on, due_on) VALUES
	  (901, 101, date('now','-200 days'), date('now','-179 days'))`)

	if s := a.bookStats(1); s.OutShare != 100 {
		t.Errorf("share capped at 100, got %d", s.OutShare)
	}
}

// acquired_on is optional; created_at never is, and is what the day count
// falls back to.
func TestBookStatsFallsBackToTheDayACopyWasEntered(t *testing.T) {
	a := statsApp(t)
	exec(t, a.db, `DELETE FROM loan`)
	exec(t, a.db, `DELETE FROM copy`)
	exec(t, a.db, `INSERT INTO copy (id, book_id, code, acquired_on, created_at) VALUES
	  (901, 1, 'VOL900016', NULL, datetime('now','-50 days'))`)
	exec(t, a.db, `INSERT INTO loan (copy_id, borrower_id, loaned_on, due_on, returned_on) VALUES
	  (901, 101, date('now','-30 days'), date('now','-9 days'), date('now','-5 days'))`)

	s := a.bookStats(1)
	if !s.HasShare {
		t.Fatal("a copy entered 50 days ago has a share to state")
	}
	if s.OutShare != 50 { // 25 days out of 50 owned
		t.Errorf("share: want 50, got %d", s.OutShare)
	}
}

// The /stats readings, each over loans the test writes from scratch.

func homeApp(t *testing.T) *app {
	t.Helper()
	loadForTest(t)
	a := statsApp(t)
	exec(t, a.db, `DELETE FROM loan`)
	return a
}

// A work is one row however many copies carry it, and a copy lent twice is two
// loans of one title.
func TestTopTitlesGatherEveryCopyOfAWork(t *testing.T) {
	a := homeApp(t)
	// Copies 1 and 2 are both Le Petit Prince; copy 3 is Le loup est revenu.
	exec(t, a.db, `INSERT INTO loan (copy_id, borrower_id, loaned_on, due_on, returned_on) VALUES
	  (1, 101, date('now','-20 days'), date('now','-6 days'), date('now','-10 days')),
	  (2, 102, date('now','-18 days'), date('now','-4 days'), date('now','-9 days')),
	  (1, 103, date('now','-8 days'),  date('now','+6 days'), NULL),
	  (3, 104, date('now','-7 days'),  date('now','+7 days'), NULL)`)

	start, _ := schoolYearBounds(todayISO())
	top := a.topTitles(start)
	if len(top) != 2 {
		t.Fatalf("titles: want 2, got %d (%+v)", len(top), top)
	}
	if top[0].Title != "Le Petit Prince" || top[0].Count != 3 {
		t.Errorf("first: want Le Petit Prince ×3, got %s ×%d", top[0].Title, top[0].Count)
	}
	if top[1].Count != 1 {
		t.Errorf("second: want ×1, got ×%d", top[1].Count)
	}
}

// An anonymised loan still counts towards the work.
func TestTopTitlesCountAnAnonymisedLoan(t *testing.T) {
	a := homeApp(t)
	exec(t, a.db, `INSERT INTO loan (copy_id, borrower_id, loaned_on, due_on, returned_on) VALUES
	  (1, 1, date('now','-30 days'), date('now','-16 days'), date('now','-20 days'))`)

	start, _ := schoolYearBounds(todayISO())
	top := a.topTitles(start)
	if len(top) != 1 || top[0].Count != 1 {
		t.Fatalf("the sentinel's loan was dropped: %+v", top)
	}
}

// The list is this school year's, so what moved three years ago is not what the
// screen calls most borrowed today.
func TestTopTitlesReadOnlyThisSchoolYear(t *testing.T) {
	a := homeApp(t)
	exec(t, a.db, `INSERT INTO loan (copy_id, borrower_id, loaned_on, due_on, returned_on) VALUES
	  (1, 101, date('now','-3 years'), date('now','-3 years','+14 days'), date('now','-3 years','+7 days')),
	  (2, 102, date('now','-3 years'), date('now','-3 years','+14 days'), date('now','-3 years','+7 days')),
	  (3, 103, date('now','-2 days'),  date('now','+12 days'), NULL)`)

	start, _ := schoolYearBounds(todayISO())
	top := a.topTitles(start)
	if len(top) != 1 || top[0].Title != "Le loup est revenu" {
		t.Fatalf("the window was not applied: %+v", top)
	}
}

// Dead stock counts works, not copies.
func TestIdleStockCountsWorksNotCopies(t *testing.T) {
	a := homeApp(t)
	// Le Petit Prince has two copies; lending one makes the work not idle.
	exec(t, a.db, `INSERT INTO loan (copy_id, borrower_id, loaned_on, due_on, returned_on) VALUES
	  (1, 101, date('now','-9 days'), date('now','+5 days'), NULL)`)

	s := a.idleStock()
	if s.Total != 3 {
		t.Fatalf("works on the shelf: want 3, got %d", s.Total)
	}
	if s.Never != 2 {
		t.Errorf("never borrowed: want 2, got %d", s.Never)
	}
	if s.Share != 67 {
		t.Errorf("share: want 67 %%, got %d %%", s.Share)
	}
	for _, x := range s.Titles {
		if x.Title == "Le Petit Prince" {
			t.Error("a work with a copy that has gone out is listed as never borrowed")
		}
	}
}

// A work nobody can pick up leaves both halves of the share.
func TestIdleStockIgnoresLostAndWithdrawnCopies(t *testing.T) {
	a := homeApp(t)
	exec(t, a.db, `UPDATE copy SET status = 'withdrawn' WHERE book_id = 3`)

	s := a.idleStock()
	if s.Total != 2 {
		t.Errorf("works on the shelf: want 2, got %d", s.Total)
	}
	for _, x := range s.Titles {
		if x.ID == 3 {
			t.Error("a withdrawn work is listed as stock to do something about")
		}
	}
}

// Idle titles are listed oldest first.
func TestIdleStockListsTheOldestFirst(t *testing.T) {
	a := homeApp(t)
	exec(t, a.db, `UPDATE copy SET acquired_on = date('now','-30 days') WHERE book_id = 1`)
	exec(t, a.db, `UPDATE copy SET acquired_on = date('now','-900 days') WHERE book_id = 2`)
	// Book 3 keeps a NULL acquired_on and falls back on created_at, which the
	// fixture writes as today.
	exec(t, a.db, `UPDATE copy SET acquired_on = NULL WHERE book_id = 3`)

	s := a.idleStock()
	if len(s.Titles) != 3 {
		t.Fatalf("titles: want 3, got %d", len(s.Titles))
	}
	if s.Titles[0].Title != "Le loup est revenu" {
		t.Errorf("oldest first: got %s", s.Titles[0].Title)
	}
	if s.Titles[0].Since >= s.Titles[1].Since || s.Titles[1].Since >= s.Titles[2].Since {
		t.Errorf("not ordered by the day they arrived: %v", s.Titles)
	}
}

// A collection everything has gone out of says so rather than listing nothing
// under a heading.
func TestIdleStockIsEmptyWhenEverythingHasGoneOut(t *testing.T) {
	a := homeApp(t)
	exec(t, a.db, `INSERT INTO loan (copy_id, borrower_id, loaned_on, due_on, returned_on) VALUES
	  (1, 101, date('now','-9 days'), date('now','+5 days'), NULL),
	  (3, 102, date('now','-9 days'), date('now','+5 days'), NULL),
	  (5, 103, date('now','-9 days'), date('now','+5 days'), NULL)`)

	s := a.idleStock()
	if s.Never != 0 || len(s.Titles) != 0 || s.Share != 0 {
		t.Errorf("want nothing idle, got %d/%d (%d %%) %v", s.Never, s.Total, s.Share, s.Titles)
	}
}

// The grid reads the school year, so a loan from before it is not drawn on it.
func TestLoansByDayReadsTheSchoolYearOnly(t *testing.T) {
	a := homeApp(t)
	exec(t, a.db, `INSERT INTO loan (copy_id, borrower_id, loaned_on, due_on, returned_on) VALUES
	  (1, 101, date('now','-2 days'),  date('now','+12 days'), NULL),
	  (2, 102, date('now','-3 years'), date('now','-3 years','+14 days'), NULL)`)

	start, end := schoolYearBounds(todayISO())
	days := a.loansByDay(todayISO())
	total := 0
	for day, n := range days {
		if day < start || day > end {
			t.Errorf("%s is outside %s..%s", day, start, end)
		}
		total += n
	}
	if total != 1 {
		t.Errorf("loans in the year: want 1, got %d", total)
	}
}

// The bar row is a rolling twelve months, not the grid's school year.
func TestMonthBarsHoldTwelveMonthsEndingThisOne(t *testing.T) {
	a := homeApp(t)
	exec(t, a.db, `INSERT INTO loan (copy_id, borrower_id, loaned_on, due_on, returned_on) VALUES
	  (1, 101, date('now','-2 days'),   date('now','+19 days'), NULL),
	  (2, 102, date('now','-2 days'),   date('now','+19 days'), NULL),
	  (3, 103, date('now','-300 days'), date('now','-279 days'), date('now','-290 days')),
	  (4, 104, date('now','-800 days'), date('now','-779 days'), date('now','-790 days'))`)

	bars := a.monthBars("fr", todayISO())
	if len(bars) != monthsShown {
		t.Fatalf("bars: want %d, got %d", monthsShown, len(bars))
	}
	var total int
	for _, b := range bars {
		total += b.Count
	}
	// The two from this month and the one from ten months ago; not the one from
	// two years back.
	if total != 3 {
		t.Errorf("loans in the rolling year: want 3, got %d", total)
	}
	if bars[11].Count != 2 {
		t.Errorf("the last column is this month: want 2, got %d", bars[11].Count)
	}
	if bars[11].Height != 100 {
		t.Errorf("the tallest column draws %.2f%%", bars[11].Height)
	}
	// A month nobody borrowed in is still a column, and draws nothing.
	var empty int
	for _, b := range bars {
		if b.Count == 0 {
			if b.Height != 0 {
				t.Errorf("%s: %d loans drawn at %.2f%%", b.Label, b.Count, b.Height)
			}
			empty++
		}
	}
	if empty != 10 {
		t.Errorf("empty columns: want 10, got %d", empty)
	}
}

// One loan against a busy month still draws a stub.
func TestAMonthWithOneLoanIsStillVisible(t *testing.T) {
	a := homeApp(t)
	exec(t, a.db, `INSERT INTO loan (copy_id, borrower_id, loaned_on, due_on, returned_on) VALUES
	  (3, 103, date('now','-100 days'), date('now','-79 days'), date('now','-90 days'))`)
	for i := 0; i < 60; i++ {
		exec(t, a.db, `INSERT INTO loan (copy_id, borrower_id, loaned_on, due_on, returned_on)
		  VALUES (1, 101, date('now','-2 days'), date('now','+19 days'), date('now','-1 days'))`)
	}

	bars := a.monthBars("fr", todayISO())
	for _, b := range bars {
		if b.Count == 1 && b.Height < 4 {
			t.Errorf("one loan draws %.2f%%, which is nothing at all", b.Height)
		}
	}
}
