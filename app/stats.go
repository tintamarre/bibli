package main

import (
	"log"
	"math"
	"time"
)

// Readings of what the loan table already records: one line on a borrower's
// page, one on a book's, and four on /stats. Not a reporting screen:
// nothing here is filtered, dated, sorted or exported. The yearly review
// downloaded from /stats is review.go.
//
// Every reading runs once the page's own rows are closed: one connection.
// A reading that fails is logged and comes back zero.

// The window a borrower's reading is measured over: a year, well inside the
// retention period, so anonymisation never empties it.
const statsWindow = "-12 months"

// What a borrower has read over the window.
type BorrowerStats struct {
	Loans int // loans started inside the window

	// Book-days: one book kept one day is one. Clipped to the window at both
	// ends, so the figure never runs past the window it names.
	BookDays int
}

func (a *app) borrowerStats(id int64) BorrowerStats {
	var s BorrowerStats
	// MIN and MAX with two arguments are SQLite's scalar functions, not the
	// aggregates: they clip each loan to the window before it is summed. Dates
	// are ISO-8601 text, which compares chronologically.
	err := a.db.QueryRow(
		`SELECT COALESCE(SUM(CASE WHEN l.loaned_on >= date('now', ?) THEN 1 ELSE 0 END), 0),
		        CAST(COALESCE(SUM(MAX(
		              julianday(MIN(COALESCE(l.returned_on, date('now')), date('now')))
		            - julianday(MAX(l.loaned_on, date('now', ?))), 0)), 0) AS INTEGER)
		   FROM loan l
		  WHERE l.borrower_id = ?`,
		statsWindow, statsWindow, id).Scan(&s.Loans, &s.BookDays)
	if err != nil {
		log.Printf("borrower stats: %v", err)
	}
	return s
}

// What a book has done since it arrived: its whole life, not a window.
type BookStats struct {
	// Distinct borrowers, ever; the anonymisation sentinel counts as one.
	Readers int

	// The share of its copies' time the book has spent out, as a percentage,
	// counted in copy-days on both sides.
	OutShare int

	// False while the book owns no whole day yet: 0 % would read as unwanted.
	HasShare bool
}

func (a *app) bookStats(id int64) BookStats {
	var s BookStats
	var outDays, ownedDays int

	if err := a.db.QueryRow(
		`SELECT COUNT(DISTINCT l.borrower_id),
		        CAST(COALESCE(SUM(MAX(
		              julianday(COALESCE(l.returned_on, date('now')))
		            - julianday(l.loaned_on), 0)), 0) AS INTEGER)
		   FROM loan l
		   JOIN copy c ON c.id = l.copy_id
		  WHERE c.book_id = ?`, id).Scan(&s.Readers, &outDays); err != nil {
		log.Printf("book stats (loans): %v", err)
		return s
	}

	// A copy is owned from the day it was acquired, or failing that from the
	// day it was entered: acquired_on is optional, created_at never is.
	if err := a.db.QueryRow(
		`SELECT CAST(COALESCE(SUM(MAX(
		              julianday(date('now'))
		            - julianday(COALESCE(date(c.acquired_on), date(c.created_at))), 0)), 0) AS INTEGER)
		   FROM copy c
		  WHERE c.book_id = ?`, id).Scan(&ownedDays); err != nil {
		log.Printf("book stats (copies): %v", err)
		return s
	}

	s.OutShare, s.HasShare = outShare(outDays, ownedDays)
	return s
}

// outShare is the percentage of owned copy-days spent out, false while the
// copies own no whole day yet: 0 % would read as unwanted. Capped at 100: a
// copy acquired after it was first lent would otherwise report more than all
// of the time.
func outShare(outDays, ownedDays int) (int, bool) {
	if ownedDays <= 0 {
		return 0, false
	}
	return min((outDays*100+ownedDays/2)/ownedDays, 100), true
}

// How many months the bar row under the grid holds.
const monthsShown = 12

// How many titles each of the two lists names.
const titlesShown = 5

// Readings is the whole of what /stats draws.
type Readings struct {
	Year   heatmap
	Months []MonthBar
	Top    []TopTitle
	Idle   IdleStock
}

// MonthBar is one column of the twelve. Height is a percentage of the tallest
// month, so the row is read against itself.
type MonthBar struct {
	Label  string
	Count  int
	Height float64
	Title  string // "mars — 33 prêts"
}

type TopTitle struct {
	ID    int64
	Title string
	Count int
}

// IdleStock is the part of the collection that has never gone out. Lost and
// withdrawn copies count on neither side: they are not stock anyone can weed.
type IdleStock struct {
	Never  int
	Total  int
	Share  int
	Titles []IdleTitle
}

// IdleTitle is a work that has never been borrowed, with the day the library
// got it.
type IdleTitle struct {
	ID    int64
	Title string
	Since string // ISO
}

// readings runs the four in order, each one drained before the next: one
// connection.
func (a *app) readings(lang, todayISO string) Readings {
	start, _ := activityYearBounds(todayISO)
	return Readings{
		Year:   newHeatmap(lang, todayISO, a.loansByDay(todayISO)),
		Months: a.monthBars(lang, todayISO),
		Top:    a.topTitles(start),
		Idle:   a.idleStock(),
	}
}

// loansByDay counts the loans started on each day of the activity year.
func (a *app) loansByDay(todayISO string) map[string]int {
	start, end := activityYearBounds(todayISO)
	counts := map[string]int{}
	rows, err := a.db.Query(
		`SELECT loaned_on, COUNT(*) FROM loan
		  WHERE loaned_on >= ? AND loaned_on <= ?
		  GROUP BY loaned_on`, start, end)
	if err != nil {
		log.Printf("readings (days): %v", err)
		return counts
	}
	defer rows.Close()
	for rows.Next() {
		var day string
		var n int
		if err := rows.Scan(&day, &n); err != nil {
			log.Printf("readings (days): %v", err)
			return counts
		}
		counts[day[:10]] = n
	}
	if err := rows.Err(); err != nil {
		log.Printf("readings (days): %v", err)
	}
	return counts
}

// monthBars reads the twelve months up to and including this one: a rolling
// year on purpose, where the grid reads the activity year. The months come
// from Go, since a month with no loan returns no row.
func (a *app) monthBars(lang, todayISO string) []MonthBar {
	t, ok := parseDate(todayISO)
	if !ok {
		t = today()
	}
	current := time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
	start := current.AddDate(0, -(monthsShown - 1), 0)

	counts := map[string]int{}
	rows, err := a.db.Query(
		`SELECT strftime('%Y-%m', loaned_on) AS m, COUNT(*) FROM loan
		  WHERE loaned_on >= ? GROUP BY m`, start.Format("2006-01-02"))
	if err != nil {
		log.Printf("readings (months): %v", err)
	} else {
		defer rows.Close()
		for rows.Next() {
			var m string
			var n int
			if err := rows.Scan(&m, &n); err != nil {
				log.Printf("readings (months): %v", err)
				break
			}
			counts[m] = n
		}
		if err := rows.Err(); err != nil {
			log.Printf("readings (months): %v", err)
		}
	}

	bars := make([]MonthBar, monthsShown)
	tallest := 0
	for i := range bars {
		m := start.AddDate(0, i, 0)
		n := counts[m.Format("2006-01")]
		if n > tallest {
			tallest = n
		}
		label := monthLabel(lang, m.Month())
		bars[i] = MonthBar{Label: label, Count: n, Title: Tn(lang, "stats.month_label", n, label)}
	}
	for i := range bars {
		if tallest == 0 || bars[i].Count == 0 {
			continue
		}
		// A stub rather than nothing: one loan must not round to an empty column.
		bars[i].Height = math.Max(math.Round(float64(bars[i].Count)/float64(tallest)*10000)/100, 4)
	}
	return bars
}

// topTitles names what moved this activity year, copies of one work counted
// together. Loans reassigned to the anonymisation sentinel still count.
func (a *app) topTitles(start string) []TopTitle {
	rows, err := a.db.Query(
		`SELECT b.id, b.title, COUNT(*) AS n
		   FROM loan l
		   JOIN copy c ON c.id = l.copy_id
		   JOIN book b ON b.id = c.book_id
		  WHERE l.loaned_on >= ?
		  GROUP BY b.id, b.title
		  ORDER BY n DESC, b.title
		  LIMIT ?`, start, titlesShown)
	if err != nil {
		log.Printf("readings (top): %v", err)
		return nil
	}
	defer rows.Close()
	var out []TopTitle
	for rows.Next() {
		var t TopTitle
		if err := rows.Scan(&t.ID, &t.Title, &t.Count); err != nil {
			log.Printf("readings (top): %v", err)
			return out
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		log.Printf("readings (top): %v", err)
	}
	return out
}

// shelvable is works holding at least one copy that could be picked up, on
// both sides of the share.
const shelvable = `EXISTS (SELECT 1 FROM copy c
                            WHERE c.book_id = b.id AND c.status IN ('available','damaged'))`

// borrowedEver lists the works a loan has ever touched, as a set rather than a
// correlated EXISTS (the loan(copy_id) index is partial). NOT IN is safe only
// because copy.book_id is NOT NULL: one NULL and it silently matches nothing.
const borrowedEver = `b.id NOT IN (SELECT c.book_id FROM loan l JOIN copy c ON c.id = l.copy_id)`

func (a *app) idleStock() IdleStock {
	var s IdleStock
	if err := a.db.QueryRow(
		`SELECT COUNT(*),
		        COALESCE(SUM(CASE WHEN `+borrowedEver+` THEN 1 ELSE 0 END), 0)
		   FROM book b WHERE `+shelvable).Scan(&s.Total, &s.Never); err != nil {
		log.Printf("readings (idle): %v", err)
		return s
	}
	if s.Total > 0 {
		s.Share = (s.Never*100 + s.Total/2) / s.Total
	}
	if s.Never == 0 {
		return s
	}

	rows, err := a.db.Query(
		`SELECT b.id, b.title,
		        MIN(COALESCE(date(c.acquired_on), date(c.created_at))) AS since
		   FROM book b JOIN copy c ON c.book_id = b.id
		  WHERE `+shelvable+` AND `+borrowedEver+`
		  GROUP BY b.id, b.title
		  ORDER BY since, b.title
		  LIMIT ?`, titlesShown)
	if err != nil {
		log.Printf("readings (idle titles): %v", err)
		return s
	}
	defer rows.Close()
	for rows.Next() {
		var t IdleTitle
		if err := rows.Scan(&t.ID, &t.Title, &t.Since); err != nil {
			log.Printf("readings (idle titles): %v", err)
			return s
		}
		s.Titles = append(s.Titles, t)
	}
	if err := rows.Err(); err != nil {
		log.Printf("readings (idle titles): %v", err)
	}
	return s
}
