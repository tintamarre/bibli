package main

import (
	"encoding/csv"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

// The yearly collection review, downloaded from /stats: one row per work,
// in shelf-mark order, for deciding what moves, is replaced or is withdrawn.
// The one export /stats has, unfiltered, and it names no borrower.

type reviewRow struct {
	ID                                          int64
	Mark, Title, Authors, ISBN, Year, Locations string
	Codes                                       string
	CataloguedOn, FirstLoan, LastLoan           string
	Copies, Available, Damaged, Lost, Withdrawn int
	Out, OnShelf, Loans, LoanDays, Borrowers    int
	OutShare                                    int
	HasShare                                    bool
}

// reviewRows reads every work with its copies and loans in one query, then
// sorts by shelf mark, which SQL cannot compute (labels.go). The sentinel is
// left out of the distinct borrowers: every anonymised loan is its.
func (a *app) reviewRows() ([]reviewRow, error) {
	onShelf := placeholders(len(statusesOnShelf))
	args := []any{}
	for _, s := range statusesOnShelf {
		args = append(args, s)
	}
	args = append(args, anonymousID())

	q, err := a.db.Query(
		`WITH cp AS (
		   SELECT c.book_id,
		          COUNT(*) AS copies,
		          SUM(c.status = 'available') AS available,
		          SUM(c.status = 'damaged')   AS damaged,
		          SUM(c.status = 'lost')      AS lost,
		          SUM(c.status = 'withdrawn') AS withdrawn,
		          SUM(c.status IN (`+onShelf+`) AND NOT EXISTS (
		                SELECT 1 FROM loan o WHERE o.copy_id = c.id AND o.returned_on IS NULL)) AS on_shelf,
		          MIN(date(c.created_at)) AS catalogued_on,
		          COALESCE(GROUP_CONCAT(DISTINCT NULLIF(c.location, '')), '') AS locations,
		          GROUP_CONCAT(c.code, ', ' ORDER BY c.code) AS codes,
		          CAST(COALESCE(SUM(MAX(
		                julianday(date('now'))
		              - julianday(COALESCE(date(c.acquired_on), date(c.created_at))), 0)), 0) AS INTEGER) AS owned_days
		     FROM copy c
		    GROUP BY c.book_id
		 ), ln AS (
		   SELECT c.book_id,
		          COUNT(*) AS loans,
		          SUM(l.returned_on IS NULL) AS out_now,
		          CAST(COALESCE(SUM(MAX(
		                julianday(COALESCE(l.returned_on, date('now')))
		              - julianday(l.loaned_on), 0)), 0) AS INTEGER) AS loan_days,
		          MIN(l.loaned_on) AS first_loan,
		          MAX(l.loaned_on) AS last_loan,
		          COUNT(DISTINCT NULLIF(l.borrower_id, ?)) AS borrowers
		     FROM loan l
		     JOIN copy c ON c.id = l.copy_id
		    GROUP BY c.book_id
		 )
		 SELECT b.id, b.title, COALESCE(b.authors, ''), COALESCE(b.isbn13, ''),
		        COALESCE(CAST(b.year AS TEXT), ''),
		        COALESCE(cp.locations, ''), COALESCE(cp.catalogued_on, ''), COALESCE(cp.codes, ''),
		        COALESCE(cp.copies, 0), COALESCE(cp.available, 0), COALESCE(cp.damaged, 0),
		        COALESCE(cp.lost, 0), COALESCE(cp.withdrawn, 0), COALESCE(cp.on_shelf, 0),
		        COALESCE(ln.out_now, 0), COALESCE(ln.loans, 0), COALESCE(ln.loan_days, 0),
		        COALESCE(ln.first_loan, ''), COALESCE(ln.last_loan, ''), COALESCE(ln.borrowers, 0),
		        COALESCE(cp.owned_days, 0)
		   FROM book b
		   LEFT JOIN cp ON cp.book_id = b.id
		   LEFT JOIN ln ON ln.book_id = b.id`, args...)
	if err != nil {
		return nil, fmt.Errorf("collection review: %w", err)
	}
	defer q.Close()

	var rows []reviewRow
	for q.Next() {
		var r reviewRow
		var owned int
		if err := q.Scan(&r.ID, &r.Title, &r.Authors, &r.ISBN, &r.Year,
			&r.Locations, &r.CataloguedOn, &r.Codes,
			&r.Copies, &r.Available, &r.Damaged, &r.Lost, &r.Withdrawn, &r.OnShelf,
			&r.Out, &r.Loans, &r.LoanDays, &r.FirstLoan, &r.LastLoan, &r.Borrowers,
			&owned); err != nil {
			return nil, fmt.Errorf("collection review (scan): %w", err)
		}
		r.Mark = shelfMark(r.Authors, r.Title)
		r.OutShare, r.HasShare = outShare(r.LoanDays, owned)
		rows = append(rows, r)
	}
	if err := q.Err(); err != nil {
		return nil, fmt.Errorf("collection review: %w", err)
	}

	// The order a shelf is walked in: mark, then author, then title.
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.Mark != b.Mark {
			return a.Mark < b.Mark
		}
		if fa, fb := foldSearch(a.Authors), foldSearch(b.Authors); fa != fb {
			return fa < fb
		}
		return foldSearch(a.Title) < foldSearch(b.Title)
	})
	return rows, nil
}

// reviewColumns are the keys of the review's columns, in file order; the CSV
// names them with the prefix taken off, as csvColumnNames does.
var reviewColumns = []string{
	"review.shelf_mark", "review.title", "review.authors", "review.isbn13", "review.year",
	"review.locations", "review.catalogued_on",
	"review.copies", "review.codes", "review.available", "review.damaged", "review.lost", "review.withdrawn",
	"review.out", "review.on_shelf",
	"review.loans", "review.loan_days", "review.first_loan", "review.last_loan",
	"review.out_share", "review.in_share", "review.borrowers",
}

// reviewNumeric marks the columns written as numbers in the spreadsheet, so
// that sorting on them sorts by value. The ISBN stays text.
var reviewNumeric = []bool{
	false, false, false, false, true,
	false, false,
	true, false, true, true, true, true,
	true, true,
	true, true, false, false,
	true, true, true,
}

var reviewWidths = []int{8, 40, 28, 15, 7, 16, 12, 6, 24, 6, 6, 6, 6, 6, 6, 6, 8, 12, 12, 8, 8, 8}

func (r reviewRow) cells() []string {
	out, in := "", ""
	if r.HasShare {
		out, in = strconv.Itoa(r.OutShare), strconv.Itoa(100-r.OutShare)
	}
	n := strconv.Itoa
	return []string{
		r.Mark, r.Title, r.Authors, r.ISBN, r.Year, r.Locations, r.CataloguedOn,
		n(r.Copies), r.Codes, n(r.Available), n(r.Damaged), n(r.Lost), n(r.Withdrawn),
		n(r.Out), n(r.OnShelf),
		n(r.Loans), n(r.LoanDays), r.FirstLoan, r.LastLoan,
		out, in, n(r.Borrowers),
	}
}

func reviewBasename() string {
	return "bibli-collection-" + today().Format("2006-01-02")
}

// reviewCSV is RFC 4180 with English column names, like /export.csv.
func (a *app) reviewCSV(w http.ResponseWriter, r *http.Request) {
	rows, err := a.reviewRows()
	if err != nil {
		log.Print(err)
		internalError(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf(`attachment; filename="%s.csv"`, reviewBasename()))

	cw := csv.NewWriter(w)
	defer cw.Flush()
	names := make([]string, len(reviewColumns))
	for i, key := range reviewColumns {
		names[i] = strings.TrimPrefix(key, "review.")
	}
	cw.Write(names)
	for _, row := range rows {
		cw.Write(csvSafeRow(row.cells()))
	}
}

// reviewXLSX is the same file for a person to open, in the library's language.
func (a *app) reviewXLSX(w http.ResponseWriter, r *http.Request) {
	rows, err := a.reviewRows()
	if err != nil {
		log.Print(err)
		internalError(w, r)
		return
	}
	lang := requestLang(r)
	header := make([]string, len(reviewColumns))
	for i, key := range reviewColumns {
		header[i] = T(lang, key)
	}
	cells := make([][]string, len(rows))
	for i, row := range rows {
		cells[i] = row.cells()
	}

	w.Header().Set("Content-Type",
		"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf(`attachment; filename="%s.xlsx"`, reviewBasename()))
	if err := writeXLSX(w, sheet{
		Name:    T(lang, "review.sheet_name"),
		Header:  header,
		Rows:    cells,
		Widths:  reviewWidths,
		Numeric: reviewNumeric,
	}); err != nil {
		log.Printf("collection review (write): %v", err)
	}
}
