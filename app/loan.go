package main

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
)

// Sentinels for resolving a scan that is an ISBN rather than an internal code.
var (
	errNoCopyAvailable = errors.New("no copy available")
	errNotOnLoan       = errors.New("no copy of this work is out on loan")
)

// copyForISBN resolves a scanned ISBN to a copy available to lend. err says
// which of three cases: nil, an available copy; errNoCopyAvailable, the work
// exists but every copy is out or unusable, so offer to add one; sql.ErrNoRows,
// the ISBN is unknown and express cataloguing takes over.
// bookByISBN finds a work by either form of a scanned ISBN. The "isbn10 IS NOT
// NULL" guard stops a book without an ISBN-10 (a NULL column) from matching a
// caller's empty i10. sql.ErrNoRows means the ISBN is not in the catalogue.
func (a *app) bookByISBN(i13, i10 string) (workID int64, title string, err error) {
	err = a.db.QueryRow(
		`SELECT id, title FROM book WHERE isbn13 = ? OR (isbn10 IS NOT NULL AND isbn10 = ?)`,
		i13, i10).Scan(&workID, &title)
	return
}

func (a *app) copyForISBN(scan string) (exID, workID int64, code, status, title string, err error) {
	i13, i10, e := ISBNForms(scan)
	if e != nil {
		return 0, 0, "", "", "", sql.ErrNoRows // neither an internal code nor a valid ISBN
	}
	if workID, title, err = a.bookByISBN(i13, i10); err != nil {
		return 0, 0, "", "", "", err // sql.ErrNoRows = unknown ISBN
	}
	err = a.db.QueryRow(
		`SELECT c.id, c.code, c.status
		   FROM copy c
		  WHERE c.book_id = ? AND c.status = 'available'
		    AND NOT EXISTS (SELECT 1 FROM loan l WHERE l.copy_id = c.id AND l.returned_on IS NULL)
		  ORDER BY c.code LIMIT 1`, workID,
	).Scan(&exID, &code, &status)
	if err == sql.ErrNoRows {
		return 0, workID, "", "", title, errNoCopyAvailable
	}
	return exID, workID, code, status, title, err
}

type OpenLoan struct {
	LoanID      int64
	CopyID      int64
	Code        string
	Status      string
	Title       string
	FirstName   string
	LastInitial string
	Class       string
	LoanedOn    string
}

// Who renders the borrower in plain words, "Amy S. (P4)".
func (p OpenLoan) Who() string {
	s := strings.TrimSpace(p.FirstName + " " + p.LastInitial)
	if p.Class != "" {
		s += " (" + p.Class + ")"
	}
	return s
}

const openLoanColumns = `l.id, c.id, c.code, c.status, b.title,
	br.first_name, br.last_initial, COALESCE(br.class, ''), l.loaned_on`

func scanOpenLoan(sc rowScanner, p *OpenLoan) error {
	return sc.Scan(&p.LoanID, &p.CopyID, &p.Code, &p.Status, &p.Title,
		&p.FirstName, &p.LastInitial, &p.Class, &p.LoanedOn)
}

// borrowerLiteCols and scanBorrowerLite are the borrower fields the lending
// screen shows; the class can be NULL, hence *string in Borrower.
const borrowerLiteCols = "id, first_name, last_initial, class"

func scanBorrowerLite(sc rowScanner, b *Borrower) error {
	return sc.Scan(&b.ID, &b.FirstName, &b.LastInitial, &b.Class)
}

// activeBorrowerByID loads an active borrower for the lending screen; a missing
// or deactivated id gives sql.ErrNoRows.
func (a *app) activeBorrowerByID(id int64) (Borrower, error) {
	var b Borrower
	err := scanBorrowerLite(a.db.QueryRow(
		`SELECT `+borrowerLiteCols+` FROM borrower WHERE id = ? AND active = 1`, id), &b)
	return b, err
}

// borrowSection redraws the lending screen's borrower panel with the current
// loan counts and an optional error line.
func (a *app) borrowSection(w http.ResponseWriter, r *http.Request, b Borrower, errMsg string) {
	a.fragment(w, r, "borrow", "borrow_section", map[string]any{"Borrower": a.withLoanCounts(b), "Error": errMsg})
}

const openLoanJoins = `
	   FROM loan l
	   JOIN copy c  ON c.id = l.copy_id
	   JOIN book b     ON b.id = c.book_id
	   JOIN borrower br ON br.id = l.borrower_id`

// A title with several copies can be out several times at once: picking the
// oldest loan would close another child's, with no undo, so the screen asks.
func (a *app) openLoansByISBN(scan string) ([]OpenLoan, string, error) {
	i13, i10, e := ISBNForms(scan)
	if e != nil {
		return nil, "", sql.ErrNoRows // neither an internal code nor a valid ISBN
	}
	workID, title, err := a.bookByISBN(i13, i10)
	if err != nil {
		return nil, "", err // sql.ErrNoRows = ISBN unknown to the catalogue
	}

	rows, err := a.db.Query(
		`SELECT `+openLoanColumns+openLoanJoins+`
		  WHERE c.book_id = ? AND l.returned_on IS NULL
		  ORDER BY l.loaned_on, c.code`, workID)
	if err != nil {
		return nil, title, err
	}
	defer rows.Close()
	var loans []OpenLoan
	for rows.Next() {
		var p OpenLoan
		if err := scanOpenLoan(rows, &p); err != nil {
			return nil, title, err
		}
		loans = append(loans, p)
	}
	if err := rows.Err(); err != nil {
		return nil, title, err
	}
	if len(loans) == 0 {
		return nil, title, errNotOnLoan
	}
	return loans, title, nil
}

// Resolves an internal code or an ISBN to the candidate open loans.
func (a *app) openLoansForScan(code string) ([]OpenLoan, string, error) {
	var p OpenLoan
	var loanID sql.NullInt64
	var firstName, lastName, class, loanedOn sql.NullString
	err := a.db.QueryRow(
		`SELECT l.id, c.id, c.code, c.status, b.title,
		        br.first_name, br.last_initial, br.class, l.loaned_on
		   FROM copy c
		   JOIN book b ON b.id = c.book_id
		   LEFT JOIN loan l ON l.copy_id = c.id AND l.returned_on IS NULL
		   LEFT JOIN borrower br ON br.id = l.borrower_id
		  WHERE c.code = ?`, code,
	).Scan(&loanID, &p.CopyID, &p.Code, &p.Status, &p.Title,
		&firstName, &lastName, &class, &loanedOn)
	switch {
	case err == sql.ErrNoRows:
		// Internal code unknown: it may be the ISBN on the back of the book.
		return a.openLoansByISBN(code)
	case err != nil:
		return nil, "", err
	case !loanID.Valid:
		return nil, p.Title, errNotOnLoan
	}
	p.LoanID = loanID.Int64
	p.FirstName, p.LastInitial, p.Class, p.LoanedOn = firstName.String, lastName.String, class.String, loanedOn.String
	return []OpenLoan{p}, p.Title, nil
}

// Lending and returning, built around the USB scanner (a keyboard that submits
// with Enter): one field always focused, no mouse needed.

// Pupil or teacher. The class can be NULL.
type Borrower struct {
	ID          int64
	FirstName   string
	LastInitial string
	Class       *string
	// What this borrower already has out, shown the moment the card is scanned.
	OutCount     int
	OverdueCount int
}

// withLoanCounts fills in OutCount and OverdueCount. Never call it with a
// transaction or a *sql.Rows open. Counts that cannot be read stay at
// zero: a database hiccup must not stop a loan.
func (a *app) withLoanCounts(b Borrower) Borrower {
	if err := a.db.QueryRow(
		`SELECT COUNT(*), COALESCE(SUM(due_on < date('now')), 0)
		   FROM loan WHERE borrower_id = ? AND returned_on IS NULL`, b.ID,
	).Scan(&b.OutCount, &b.OverdueCount); err != nil {
		log.Printf("loan/borrower (counts): %v", err)
	}
	return b
}

// A copy added to the basket being prepared.
type AddedRow struct {
	CopyID int64
	Title  string
	Code   string
}

// Strips what a scanner adds around the code and uppercases what has the shape
// of an internal code; anything else is a title or name and keeps its case.
func cleanCode(s string) string {
	s = strings.TrimSpace(s)
	if looksLikeCode(s) {
		return strings.ToUpper(s)
	}
	return s
}

// looksLikeCode is letters followed by digits and nothing else. Not tied to
// today's prefixes, so an older collection's codes are read the same way.
func looksLikeCode(s string) bool {
	i := 0
	for i < len(s) && (s[i] >= 'A' && s[i] <= 'Z' || s[i] >= 'a' && s[i] <= 'z') {
		i++
	}
	return i > 0 && allDigits(s[i:])
}

// Falls back to 14 days when the setting is absent or unreadable, so a loan is
// never blocked by a setting.
func (a *app) loanDays() int {
	const fallback = 14
	var v string
	if err := a.db.QueryRow(
		`SELECT value FROM setting WHERE key = 'loan_days'`,
	).Scan(&v); err != nil {
		return fallback
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}

// expressCatalogue says whether an unknown ISBN scanned at the desk may become a
// book there and then. On when the setting is missing or unreadable.
func (a *app) expressCatalogue() bool {
	var v string
	if err := a.db.QueryRow(
		`SELECT value FROM setting WHERE key = 'express_catalogue'`,
	).Scan(&v); err != nil {
		return true
	}
	return strings.TrimSpace(v) != "0"
}

// statusWording turns a copy's stored status into the wording shown inside a
// sentence — prose, unlike the status.* labels of the inventory dropdown.
func statusWording(lang, status string) string {
	switch status {
	case "available", "damaged", "lost", "withdrawn":
		return T(lang, "copy_status."+status)
	default:
		return status // unexpected value: show it as is rather than lie
	}
}

// A work offered after a title search, with a specific copy to lend.
type FoundBook struct {
	Code      string
	Title     string
	Authors   string
	Available int
}

// Fuzzy search over title and authors, limited to works with a copy on the
// shelf. It returns a specific copy, so the rest of the path is an ordinary scan.
// inBasket copies are struck out in the query, not filtered from the result, so
// the next free copy of the same title is offered instead.
func (a *app) searchAvailableBooks(q string, inBasket []int64) ([]FoundBook, bool) {
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, false
	}
	free := `c2.status = 'available'
	    AND NOT EXISTS (SELECT 1 FROM loan l WHERE l.copy_id = c2.id AND l.returned_on IS NULL)`
	var freeArgs []any
	if len(inBasket) > 0 {
		free += ` AND c2.id NOT IN (` + placeholders(len(inBasket)) + `)`
		for _, id := range inBasket {
			freeArgs = append(freeArgs, id)
		}
	}

	var conds []string
	var wordArgs []any
	for _, mot := range strings.Fields(q) {
		like := "%" + foldSearch(mot) + "%"
		conds = append(conds, "(fold(b.title) LIKE ? OR fold(COALESCE(b.authors,'')) LIKE ?)")
		wordArgs = append(wordArgs, like, like)
	}

	query := `
		SELECT c.code, b.title, COALESCE(b.authors, ''),
		       (SELECT COUNT(*) FROM copy c2
		         WHERE c2.book_id = b.id AND ` + free + `)
		  FROM copy c
		  JOIN book b ON b.id = c.book_id
		 WHERE ` + strings.Join(conds, " AND ") + `
		   AND c.code = (SELECT MIN(c2.code) FROM copy c2
		                          WHERE c2.book_id = b.id AND ` + free + `)
		 ORDER BY b.title LIMIT 6`

	// The order the placeholders appear in the statement: the count in the
	// SELECT, then the words of the WHERE, then the copy the row is about.
	args := make([]any, 0, len(freeArgs)*2+len(wordArgs))
	args = append(args, freeArgs...)
	args = append(args, wordArgs...)
	args = append(args, freeArgs...)

	rows, err := a.db.Query(query, args...)
	if err != nil {
		log.Printf("book search: %v", err)
		return nil, false
	}
	defer rows.Close()
	var out []FoundBook
	for rows.Next() {
		var l FoundBook
		if err := rows.Scan(&l.Code, &l.Title, &l.Authors, &l.Available); err != nil {
			log.Printf("book search (scan): %v", err)
			return nil, false
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		log.Printf("book search (rows): %v", err)
		return nil, false
	}
	if len(out) > 5 {
		return out[:5], true
	}
	return out, false
}

// Step 1: scan the card.
func (a *app) borrowScreen(w http.ResponseWriter, r *http.Request) {
	a.render(w, r, "borrow", map[string]any{"Title": tr(r, "nav.borrow"), "Error": ""})
}

// Identifies the borrower, then shows step 2. On failure step 1 comes back with
// a message: nothing is chosen yet, nothing is lost.
func (a *app) borrowBorrower(w http.ResponseWriter, r *http.Request) {
	code := cleanCode(r.FormValue("code"))
	if code == "" {
		a.fragment(w, r, "borrow", "borrow_step1", map[string]any{"Error": tr(r, "loan.err_no_code_card")})
		return
	}
	if misreadCode(code) {
		a.fragment(w, r, "borrow", "borrow_step1", map[string]any{"Error": tr(r, "loan.err_misread", code)})
		return
	}

	var borrower Borrower
	err := scanBorrowerLite(a.db.QueryRow(
		`SELECT `+borrowerLiteCols+` FROM borrower WHERE card_code = ? AND active = 1`, code), &borrower)
	switch {
	case err == sql.ErrNoRows:
		// Not a known card: fuzzy search on first name, last name and class.
		res, tooMany := a.searchBorrowers(code)
		if len(res) == 1 && !tooMany {
			a.borrowSection(w, r, res[0], "")
			return
		}
		a.fragment(w, r, "borrow", "borrow_step1", map[string]any{
			"Results": res, "TooMany": tooMany, "Search": code,
		})
		return
	case err != nil:
		log.Printf("loan/borrower: %v", err)
		internalError(w, r)
		return
	}

	a.borrowSection(w, r, borrower, "")
}

// Every word must appear somewhere. Up to 5 results; the boolean says there were
// more, so the search should be narrowed.
func (a *app) searchBorrowers(q string) ([]Borrower, bool) {
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, false
	}
	var conds []string
	var args []any
	for _, mot := range strings.Fields(q) {
		like := "%" + foldSearch(mot) + "%"
		conds = append(conds, "(fold(first_name) LIKE ? OR fold(last_initial) LIKE ? OR fold(COALESCE(class,'')) LIKE ?)")
		args = append(args, like, like, like)
	}
	query := `SELECT ` + borrowerLiteCols + ` FROM borrower
	             WHERE active = 1 AND ` + strings.Join(conds, " AND ") + `
	             ORDER BY class, last_initial, first_name LIMIT 6`
	rows, err := a.db.Query(query, args...)
	if err != nil {
		log.Printf("borrower search: %v", err)
		return nil, false
	}
	defer rows.Close()
	var out []Borrower
	for rows.Next() {
		var e Borrower
		if err := scanBorrowerLite(rows, &e); err != nil {
			log.Printf("borrower search (scan): %v", err)
			return nil, false
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		log.Printf("borrower search (rows): %v", err)
		return nil, false
	}
	if len(out) > 5 {
		return out[:5], true
	}
	return out, false
}

func (a *app) borrowSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.FormValue("code"))
	res, tooMany := a.searchBorrowers(q)
	a.fragment(w, r, "borrow", "borrower_results", map[string]any{
		"Results": res, "TooMany": tooMany, "Search": q,
	})
}

// basketCopies reads the basket the browser sent, which lives only in the page
// until the loan is confirmed. Capped, so a request cannot build a huge statement.
func basketCopies(r *http.Request) []int64 {
	if err := r.ParseForm(); err != nil {
		return nil
	}
	const maxBasket = 100
	var out []int64
	for _, v := range r.Form["copy_id"] {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			continue
		}
		if out = append(out, id); len(out) == maxBasket {
			break
		}
	}
	return out
}

// borrowBookSearch is step 2's live search. It only reads, which is what lets a
// keystroke drive it; /borrow/add commits and stays behind Enter.
//
// An empty box answers 204, which HTMX does not swap: the debounced search fires
// after a scan has reset the field, and a 200 would wipe the scan's answer.
func (a *app) borrowBookSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.FormValue("code"))
	if q == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	books, tooMany := a.searchAvailableBooks(q, basketCopies(r))
	if len(books) == 0 && !tooMany {
		return
	}
	a.fragment(w, r, "borrow", "borrow_book_results", map[string]any{
		"Results": books, "TooMany": tooMany, "Search": q,
	})
}

func (a *app) borrowBorrowerID(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.FormValue("borrower_id"), 10, 64)
	borrower, err := a.activeBorrowerByID(id)
	if err != nil {
		a.fragment(w, r, "borrow", "borrow_step1", map[string]any{"Error": tr(r, "loan.err_borrower_unknown")})
		return
	}
	a.borrowSection(w, r, borrower, "")
}

// The message goes to #scan-feedback; on success an <li> row is swapped out of
// band into the basket.
func (a *app) borrowAdd(w http.ResponseWriter, r *http.Request) {
	code := cleanCode(r.FormValue("code"))
	if code == "" {
		a.fragment(w, r, "borrow", "borrow_add_err", tr(r, "loan.err_no_code_book"))
		return
	}
	if misreadCode(code) {
		a.fragment(w, r, "borrow", "borrow_add_err", tr(r, "loan.err_misread", code))
		return
	}

	var exID int64
	var foundCode, status, title string
	err := a.db.QueryRow(
		`SELECT c.id, c.code, c.status, b.title
		   FROM copy c JOIN book b ON b.id = c.book_id
		  WHERE c.code = ?`, code,
	).Scan(&exID, &foundCode, &status, &title)
	if err == sql.ErrNoRows {
		// Internal code (VOL...) unknown: try the ISBN on the back of the book.
		var workID int64
		exID, workID, foundCode, status, title, err = a.copyForISBN(code)
		switch err {
		case nil:
		case errNoCopyAvailable:
			// Work known but every copy is out: offer to add one and lend it.

			a.fragment(w, r, "borrow", "borrow_add_copy", map[string]any{"BookID": workID, "Title": title})
			return
		case sql.ErrNoRows:
			// Valid ISBN absent from the catalogue: look it up and offer prefilled
			// express cataloguing before the loan — unless the school has asked
			// that its catalogue be added to somewhere other than the desk.
			if i13, i10, e := ISBNForms(code); e == nil {
				if !a.expressCatalogue() {
					a.fragment(w, r, "borrow", "borrow_add_err",
						tr(r, "loan.err_not_catalogued", i13))
					return
				}
				// Short budget: there is a queue. Past it the empty form is shown, and
				// enrichment happens later from the book page.
				ctx, done := enrichWithin(w, r, enrichBudgetDesk)
				defer done()
				// A catalogue that could not be asked is not the book being unknown;
				// the form says which (Silent), since one is worth retrying.
				lookup := r.FormValue("lookup")
				defer clearDeskLookup(lookup)
				n, err := enrichReported(ctx, i13, i10, deskNarrator(r, lookup))
				form := catalogueForm{
					N:      Record{ISBN13: i13, ISBN10: i10},
					Silent: err != nil,
					Copies: 1,
				}
				if n != nil {
					// The whole record, not the title and authors: everything the
					// catalogue gave is kept, and the provenance with it.
					form.N = *n
					form.Found = true
				}
				a.fragment(w, r, "borrow", "borrow_new_book", form)
				return
			}
			// Probably a title: offer the matching books, to be chosen explicitly.
			// A random book in a child's bag is worse than one more click.
			if books, tooMany := a.searchAvailableBooks(code, basketCopies(r)); len(books) > 0 || tooMany {
				a.fragment(w, r, "borrow", "borrow_book_results", map[string]any{
					"Results": books, "TooMany": tooMany, "Search": code,
				})
				return
			}
			a.fragment(w, r, "borrow", "borrow_add_err",
				tr(r, "loan.err_nothing_found", code))
			return
		default:
			log.Printf("loan/add (ISBN): %v", err)
			internalError(w, r)
			return
		}
	} else if err != nil {
		log.Printf("loan/add: %v", err)
		internalError(w, r)
		return
	}

	if status != "available" {
		a.fragment(w, r, "borrow", "borrow_add_err",
			tr(r, "loan.err_marked", title, statusWording(requestLang(r), status)))
		return
	}

	var pren string
	var cls *string
	err = a.db.QueryRow(
		`SELECT br.first_name, br.class
		   FROM loan l JOIN borrower br ON br.id = l.borrower_id
		  WHERE l.copy_id = ? AND l.returned_on IS NULL`, exID,
	).Scan(&pren, &cls)
	switch {
	case err == nil:
		// Two whole sentences rather than a conditional concatenation: the class
		// goes in brackets at the end in French, elsewhere it may not.
		msg := tr(r, "loan.err_already_out", title, pren)
		if cls != nil {
			msg = tr(r, "loan.err_already_out_class", title, pren, *cls)
		}
		a.fragment(w, r, "borrow", "borrow_add_err", msg)
		return
	case err != sql.ErrNoRows:
		log.Printf("loan/add (already out?): %v", err)
		internalError(w, r)
		return
	}

	// The copy's own code, not what was scanned: an ISBN names the work, the basket lists copies.
	a.fragment(w, r, "borrow", "borrow_add_ok", AddedRow{CopyID: exID, Title: title, Code: foundCode})
}

// Creates an unknown book on the fly and adds it to the basket, so scanning an
// ISBN outside the collection does not send anyone to the cataloguing screen.
func (a *app) borrowCatalogue(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		badRequest(w, r)
		return
	}
	// The screen does not offer this when the setting is off, but the route is
	// still there: a form kept open across the change would otherwise still
	// create books.
	if !a.expressCatalogue() {
		a.fragment(w, r, "borrow", "borrow_add_err",
			tr(r, "loan.err_not_catalogued", strings.TrimSpace(r.FormValue("isbn13"))))
		return
	}
	title := strings.TrimSpace(r.FormValue("title"))
	i13, i10, isbnErr := FormISBNs(r.FormValue("isbn13"), r.FormValue("isbn10"))
	year, _ := strconv.Atoi(strings.TrimSpace(r.FormValue("year")))
	n := recordFromForm(r, i13, i10, title, year)
	if strings.TrimSpace(n.Source) == "" {
		n.Source = sourceManual // an express-catalogued book is a manual entry
	}
	// Handed back with everything already typed: the record is built first so
	// this cannot drift from the one that gets saved.
	if isbnErr != nil {
		a.fragment(w, r, "borrow", "borrow_new_book", catalogueForm{
			N: n, Copies: 1, Error: tr(r, "catalogue.err_bad_isbn"),
		})
		return
	}
	if title == "" {
		a.fragment(w, r, "borrow", "borrow_new_book", catalogueForm{
			N: n, Copies: 1, Error: tr(r, "common.err_title_required"),
		})
		return
	}
	tx, err := a.db.Begin()
	if err != nil {
		log.Printf("loan/catalogue (tx): %v", err)
		internalError(w, r)
		return
	}
	defer tx.Rollback()

	codes, err := a.createCopies(tx, n, 1, "")
	if err != nil {
		log.Printf("loan/catalogue: %v", err)
		internalError(w, r)
		return
	}
	if err := tx.Commit(); err != nil {
		log.Printf("loan/catalogue (commit): %v", err)
		internalError(w, r)
		return
	}

	var exID int64
	if err := a.db.QueryRow(`SELECT id FROM copy WHERE code = ?`, codes[0]).Scan(&exID); err != nil {
		log.Printf("loan/catalogue (copy id): %v", err)
		internalError(w, r)
		return
	}
	a.fragment(w, r, "borrow", "borrow_add_ok", AddedRow{CopyID: exID, Title: title, Code: codes[0]})
}

// Adds a copy to a work whose copies are all out, then adds it to the basket.
func (a *app) borrowAddCopy(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		badRequest(w, r)
		return
	}
	workID, _ := strconv.ParseInt(r.FormValue("book_id"), 10, 64)
	var title string
	if err := a.db.QueryRow(`SELECT title FROM book WHERE id = ?`, workID).Scan(&title); err != nil {
		a.fragment(w, r, "borrow", "borrow_add_err", tr(r, "loan.err_book_unknown"))
		return
	}
	code, exID, err := a.addCopy(workID)
	if err != nil {
		log.Printf("loan/add-copy: %v", err)
		internalError(w, r)
		return
	}
	a.fragment(w, r, "borrow", "borrow_add_ok", AddedRow{CopyID: exID, Title: title, Code: code})
}

// One transaction, one confirmation for several books.
func (a *app) borrowConfirm(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		badRequest(w, r)
		return
	}

	borrowerID, _ := strconv.ParseInt(r.FormValue("borrower_id"), 10, 64)
	if borrowerID == 0 {
		http.Error(w, tr(r, "error.missing_borrower"), http.StatusBadRequest)
		return
	}

	// Reload the pupil: needed for the display, and confirms they are still active.
	borrower, err := a.activeBorrowerByID(borrowerID)
	if err == sql.ErrNoRows {
		a.fragment(w, r, "borrow", "borrow_step1", map[string]any{"Error": tr(r, "loan.err_borrower_gone")})
		return
	}
	if err != nil {
		log.Printf("loan/confirm (borrower): %v", err)
		internalError(w, r)
		return
	}

	ids := uniqueIDs(r.Form["copy_id"])
	if len(ids) == 0 {
		a.borrowSection(w, r, borrower, tr(r, "loan.err_empty_basket"))
		return
	}

	// BEFORE the transaction: the pool holds one connection (openDB), and the
	// transaction takes it. A query through a.db meanwhile would block.
	days := a.loanDays()

	tx, err := a.db.Begin()
	if err != nil {
		log.Printf("loan/confirm (tx): %v", err)
		internalError(w, r)
		return
	}
	defer tx.Rollback() // no effect after Commit; a safety net otherwise

	var dueOn string
	if err := tx.QueryRow(`SELECT date('now', '+' || ? || ' days')`, days).Scan(&dueOn); err != nil {
		log.Printf("loan/confirm (due date): %v", err)
		internalError(w, r)
		return
	}

	var titles, conflicts []string
	for _, id := range ids {
		var title, status string
		var isOut int
		err := tx.QueryRow(
			`SELECT b.title, c.status,
			        EXISTS(SELECT 1 FROM loan l WHERE l.copy_id = c.id AND l.returned_on IS NULL)
			   FROM copy c JOIN book b ON b.id = c.book_id
			  WHERE c.id = ?`, id,
		).Scan(&title, &status, &isOut)
		switch {
		case err == sql.ErrNoRows:
			conflicts = append(conflicts, tr(r, "loan.conflict_missing", id))
			continue
		case err != nil:
			log.Printf("loan/confirm (check): %v", err)
			internalError(w, r)
			return
		}
		if status != "available" {
			conflicts = append(conflicts, tr(r, "loan.conflict_status", title, statusWording(requestLang(r), status)))
			continue
		}
		if isOut == 1 {
			conflicts = append(conflicts, tr(r, "loan.conflict_just_taken", title))
			continue
		}
		if _, err := tx.Exec(
			`INSERT INTO loan (copy_id, borrower_id, loaned_on, due_on)
			 VALUES (?, ?, date('now'), ?)`, id, borrowerID, dueOn,
		); err != nil {
			conflicts = append(conflicts, tr(r, "loan.conflict_write_failed", title))
			continue
		}
		titles = append(titles, title)
	}

	if len(titles) == 0 {
		_ = tx.Rollback()
		a.fragment(w, r, "borrow", "borrow_conflict", map[string]any{
			"Conflicts": conflicts,
			"Prompt":    tr(r, "loan.next_borrower"),
		})
		return
	}

	if err := tx.Commit(); err != nil {
		log.Printf("loan/confirm (commit): %v", err)
		internalError(w, r)
		return
	}

	a.fragment(w, r, "borrow", "borrow_success", map[string]any{
		"Prompt":    tr(r, "loan.next_borrower"),
		"Borrower":  borrower,
		"Titles":    titles,
		"DueOn":     dueOn,
		"Conflicts": conflicts, // may be non-empty: a partial loan
	})
}

// Deduplicates the basket while preserving scan order.
func uniqueIDs(raw []string) []int64 {
	seen := make(map[int64]bool, len(raw))
	var ids []int64
	for _, s := range raw {
		id, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
		if err != nil || id == 0 || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	return ids
}

// One field, scan the book. No need to look up the pupil.
func (a *app) returnScreen(w http.ResponseWriter, r *http.Request) {
	a.render(w, r, "return", map[string]any{"Title": tr(r, "return.title")})
}

// Records the return, or asks which copy when several of a title are out.
func (a *app) returnScan(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		badRequest(w, r)
		return
	}

	// Answer to the question "who is returning theirs?".
	if v := strings.TrimSpace(r.FormValue("loan_id")); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			a.fragment(w, r, "return", "return_err", tr(r, "return.err_bad_choice"))
			return
		}
		a.recordReturn(w, r, id)
		return
	}

	code := cleanCode(r.FormValue("code"))
	if code == "" {
		a.fragment(w, r, "return", "return_err", tr(r, "loan.err_no_code_book"))
		return
	}
	if misreadCode(code) {
		a.fragment(w, r, "return", "return_err", tr(r, "loan.err_misread", code))
		return
	}

	loans, title, err := a.openLoansForScan(code)
	switch {
	case err == errNotOnLoan:
		a.fragment(w, r, "return", "return_err", tr(r, "return.err_not_out", title))
		return
	case err == sql.ErrNoRows:
		a.fragment(w, r, "return", "return_err", tr(r, "return.err_unknown_code", code))
		return
	case err != nil:
		log.Printf("return (resolve): %v", err)
		internalError(w, r)
		return
	}

	// Picking one at random would close another child's loan, with no way to undo.
	if len(loans) > 1 {
		a.fragment(w, r, "return", "return_choice", map[string]any{"Title": title, "Loans": loans})
		return
	}
	a.recordReturn(w, r, loans[0].LoanID)
}

// backField is the hidden field naming the page to come back to. A mismatch
// with the templates fails silently, so loan_test.go checks them against it.
const backField = "back"

// Closes a loan from a list, without scanning, then redirects back to the page
// it was called from so its filters and counters stay consistent.
func (a *app) loanReturn(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		badRequest(w, r)
		return
	}
	id := pathID(r)
	if _, err := a.db.Exec(
		`UPDATE loan SET returned_on = date('now') WHERE id = ? AND returned_on IS NULL`, id,
	); err != nil {
		log.Printf("loan/return: %v", err)
		internalError(w, r)
		return
	}
	// An already closed loan is not an error. sanitizeNext keeps the redirect to
	// a local path, since the value comes from a form.
	http.Redirect(w, r, sanitizeNext(r.FormValue(backField)), http.StatusSeeOther)
}

// Pushes the due date of an open loan forward, then redirects back to the list.
// An absent or out-of-range count falls back to a week rather than refusing.
func (a *app) loanExtend(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		badRequest(w, r)
		return
	}
	id := pathID(r)
	if err := a.extendLoan(id, extendDays(r.FormValue("days"), extendDefaultDays)); err != nil {
		log.Printf("loan/extend: %v", err)
		internalError(w, r)
		return
	}
	// A loan already closed is not an error, as on the return path.
	http.Redirect(w, r, sanitizeNext(r.FormValue(backField)), http.StatusSeeOther)
}

// maxLoanDays bounds both the loan period in /settings and the count the
// extend dialog opens on: a year is already longer than a school one.
const maxLoanDays = 365

// extendDefaultDays is what the extend dialog opens on: a week, not the loan
// period, since an extension is a little longer rather than a second loan.
const extendDefaultDays = 7

// What the dialog asked for, or the week when the answer is not a number of days
// this application writes.
func extendDays(value string, fallback int) int {
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || n < 1 || n > maxLoanDays {
		return fallback
	}
	return n
}

// extendLoan adds the days to the loan's current due date, not to today, so a
// late book stays late until enough is added. Never shortens a loan.
func (a *app) extendLoan(loanID int64, days int) error {
	_, err := a.db.Exec(
		`UPDATE loan SET due_on = date(due_on, ?)
		  WHERE id = ? AND returned_on IS NULL`,
		fmt.Sprintf("+%d days", days), loanID)
	if err != nil {
		return fmt.Errorf("extending loan %d: %w", loanID, err)
	}
	return nil
}

// Re-reads the loan before writing: between the question and the click, someone
// may have recorded the return at the other desk.
func (a *app) recordReturn(w http.ResponseWriter, r *http.Request, loanID int64) {
	var p OpenLoan
	if err := scanOpenLoan(a.db.QueryRow(
		`SELECT `+openLoanColumns+openLoanJoins+`
		  WHERE l.id = ? AND l.returned_on IS NULL`, loanID), &p); err != nil {
		if err == sql.ErrNoRows {
			a.fragment(w, r, "return", "return_err", tr(r, "return.err_already_done"))
			return
		}
		log.Printf("return (reread): %v", err)
		internalError(w, r)
		return
	}

	res, err := a.db.Exec(
		`UPDATE loan SET returned_on = date('now') WHERE id = ? AND returned_on IS NULL`, loanID)
	if err != nil {
		log.Printf("return (save): %v", err)
		internalError(w, r)
		return
	}
	if n, _ := res.RowsAffected(); n != 1 {
		a.fragment(w, r, "return", "return_err", tr(r, "return.err_already_done"))
		return
	}

	a.fragment(w, r, "return", "return_ok", map[string]any{
		"Title":         p.Title,
		"Code":          p.Code,
		"Borrower":      p.Who(),
		"Status":        p.Status,
		"StatusWording": statusWording(requestLang(r), p.Status),
		"CopyID":        p.CopyID,
	})
}
