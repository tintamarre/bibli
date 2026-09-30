package main

import (
	"strings"
	"testing"
)

func TestReviewColumnsLineUp(t *testing.T) {
	cells := reviewRow{}.cells()
	if len(cells) != len(reviewColumns) || len(reviewNumeric) != len(reviewColumns) || len(reviewWidths) != len(reviewColumns) {
		t.Fatalf("cells %d, columns %d, numeric %d, widths %d: all must match",
			len(cells), len(reviewColumns), len(reviewNumeric), len(reviewWidths))
	}
	if reviewNumeric[3] {
		t.Error("the ISBN column is numeric; Excel would round it")
	}
}

func TestReviewRowsCountAWorkAcrossItsCopies(t *testing.T) {
	a := statsApp(t)
	restoreSettings(t)
	setAnonymousID(1)
	exec(t, a.db, `DELETE FROM loan`)
	exec(t, a.db, `DELETE FROM copy WHERE book_id = 1`)
	exec(t, a.db, `INSERT INTO copy (id, book_id, code, status, acquired_on) VALUES
	  (902, 1, 'VOL900027', 'damaged',   date('now','-100 days')),
	  (901, 1, 'VOL900016', 'available', date('now','-100 days'))`)
	exec(t, a.db, `INSERT INTO loan (copy_id, borrower_id, loaned_on, due_on, returned_on) VALUES
	  -- Anonymised: a loan, and days out, but no one to count as a reader.
	  (901, 1,   date('now','-95 days'), date('now','-81 days'), date('now','-85 days')),
	  (901, 101, date('now','-80 days'), date('now','-66 days'), date('now','-60 days')),
	  (902, 102, date('now','-60 days'), date('now','-46 days'), date('now','-30 days')),
	  (902, 103, date('now','-5 days'),  date('now','+9 days'),  NULL)`)

	rows, err := a.reviewRows()
	if err != nil {
		t.Fatal(err)
	}
	var marks []string
	for _, r := range rows {
		marks = append(marks, r.Mark)
	}
	if got := strings.Join(marks, " "); got != "ALB PEN SAI" {
		t.Errorf("order %q, want the shelf walked by mark: ALB PEN SAI", got)
	}

	r := rows[2]
	checks := []struct {
		name      string
		got, want int
	}{
		{"copies", r.Copies, 2}, {"available", r.Available, 1}, {"damaged", r.Damaged, 1},
		{"out", r.Out, 1}, {"on the shelf", r.OnShelf, 1},
		{"loans", r.Loans, 4}, {"days out", r.LoanDays, 65}, {"borrowers", r.Borrowers, 3},
		{"share out", r.OutShare, 33}, // 65 of 200 copy-days
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s: got %d, want %d", c.name, c.got, c.want)
		}
	}
	if r.Codes != "VOL900016, VOL900027" {
		t.Errorf("codes %q, want both, in order", r.Codes)
	}
	if s := a.bookStats(1); s.OutShare != r.OutShare {
		t.Errorf("the review says %d %% out, the book's page %d %%: they must agree", r.OutShare, s.OutShare)
	}
	var want string
	if err := a.db.QueryRow(`SELECT date('now','-95 days')`).Scan(&want); err != nil {
		t.Fatal(err)
	}
	if r.FirstLoan != want {
		t.Errorf("first loan %q, want %q", r.FirstLoan, want)
	}

	// Entered today, never lent: no share to state, and nothing out.
	if c := rows[0].cells(); c[19] != "" || c[20] != "" || c[15] != "0" {
		t.Errorf("a work with no whole day owned: out/in share %q/%q, loans %q", c[19], c[20], c[15])
	}
}
