package main

import (
	"database/sql"
	"errors"
	"log"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

// redirectBook sends the browser back to a book's page, with an optional query
// (e.g. "?ok=1"); the See Other keeps a POST from being replayed on refresh.
func redirectBook(w http.ResponseWriter, r *http.Request, id int64, query string) {
	http.Redirect(w, r, "/book/"+strconv.FormatInt(id, 10)+query, http.StatusSeeOther)
}

// A work's page: editable metadata, enrichment, copies, external links and loan
// statistics. The history names borrowers — minimised data, visible only to the
// signed-in librarian, purged past the retention period.

type CopyRow struct {
	ID                  int64
	Code                string
	Status              string
	Location            string
	Out                 bool
	BorrowerID          int64
	BorrowerFirstName   string
	BorrowerLastInitial string
	BorrowerClass       string
	DueOn               string // open loan: when it is due back, "" on the shelf
	DaysOverdue         int    // open loan: days past DueOn (> 0 = late)
	New                 bool   // entered today, and still to be stickered
}

type HistoryRow struct {
	BorrowerID                    int64
	FirstName, LastInitial, Class string
	LoanedOn                      string
	ReturnedOn                    string
	Days                          int
	Out                           bool
}

type BookPage struct {
	ID                           int64
	ISBN13, ISBN10               string
	Title, Subtitle              string
	Authors, Publisher, Language string
	Year                         int
	Source, SourceURL            string

	Copies         []CopyRow
	TotalCount     int
	AvailableCount int
	OutCount       int

	LoansTotal int
	History    []HistoryRow
	Stats      BookStats

	RecordLink, BnFLink, GoogleLink, GoogleBooksLink, UniCatLink string

	// Freshness token in the thumbnail URL, which the browser caches for 24 h:
	// without it a refreshed cover would stay invisible.
	Fingerprint string

	// Deletable is LoansTotal == 0: a work nobody has ever borrowed. The
	// foreign keys refuse the rest whatever the screen offers.
	Deletable  bool
	Ok         bool
	Enriched   bool // enrichment succeeded
	NotFound   bool // every catalogue answered, none has the book
	Silent     bool // a catalogue could not be asked: nothing was learnt
	LoanClosed bool // an open loan has just been closed (copy lost or withdrawn)
	Error      string

	// The copies entered today, marked and offered as one label sheet: random
	// codes say nothing about which are new. The day, not the visit.
	NewCodes []string
}

func (a *app) loadBookPage(id int64) (*BookPage, error) {
	f := &BookPage{ID: id}
	var isbn13, isbn10, subtitle, authors, publisher, language, source, sourceURL sql.NullString
	var year sql.NullInt64
	var updatedAt sql.NullString
	err := a.db.QueryRow(
		`SELECT isbn13, isbn10, title, subtitle, authors, publisher, year, language,
		        source_metadata, source_url, updated_at
		   FROM book WHERE id = ?`, id,
	).Scan(&isbn13, &isbn10, &f.Title, &subtitle, &authors, &publisher, &year, &language,
		&source, &sourceURL, &updatedAt)
	if err != nil {
		return nil, err
	}
	f.ISBN13, f.ISBN10 = isbn13.String, isbn10.String
	f.Subtitle, f.Authors, f.Publisher, f.Language = subtitle.String, authors.String, publisher.String, language.String
	f.Source, f.SourceURL = source.String, sourceURL.String
	if year.Valid {
		f.Year = int(year.Int64)
	}
	f.Fingerprint = strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, updatedAt.String)

	rows, err := a.db.Query(
		`SELECT c.id, c.code, c.status, COALESCE(c.location, ''),
		        CASE WHEN l.id IS NOT NULL THEN 1 ELSE 0 END,
		        COALESCE(br.id, 0), COALESCE(br.first_name, ''), COALESCE(br.last_initial, ''), COALESCE(br.class, ''),
		        COALESCE(l.due_on, ''),
		        COALESCE(CAST(julianday('now') - julianday(l.due_on) AS INTEGER), 0),
		        CASE WHEN date(c.created_at) = date('now') THEN 1 ELSE 0 END
		   FROM copy c
		   LEFT JOIN loan l ON l.copy_id = c.id AND l.returned_on IS NULL
		   LEFT JOIN borrower br ON br.id = l.borrower_id
		  WHERE c.book_id = ? ORDER BY c.code`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var cp CopyRow
		var outFlag, newFlag int
		if err := rows.Scan(&cp.ID, &cp.Code, &cp.Status, &cp.Location, &outFlag,
			&cp.BorrowerID, &cp.BorrowerFirstName, &cp.BorrowerLastInitial, &cp.BorrowerClass,
			&cp.DueOn, &cp.DaysOverdue, &newFlag); err != nil {
			return nil, err
		}
		cp.Out = outFlag == 1
		if cp.New = newFlag == 1; cp.New {
			f.NewCodes = append(f.NewCodes, cp.Code)
		}
		if cp.Status != "withdrawn" {
			f.TotalCount++
			if cp.Out {
				f.OutCount++
			} else if cp.Status == "available" {
				f.AvailableCount++
			}
		}
		f.Copies = append(f.Copies, cp)
	}

	a.db.QueryRow(
		`SELECT COUNT(*) FROM loan l JOIN copy c ON c.id = l.copy_id WHERE c.book_id = ?`, id,
	).Scan(&f.LoansTotal)
	f.Deletable = f.LoansTotal == 0

	hrows, err := a.db.Query(
		`SELECT br.id, br.first_name, br.last_initial, COALESCE(br.class, ''), l.loaned_on,
		        l.returned_on IS NULL AS en_cours, COALESCE(l.returned_on, ''),
		        CAST(julianday(CASE WHEN l.returned_on IS NULL THEN date('now') ELSE l.returned_on END)
		             - julianday(l.loaned_on) AS INTEGER)
		   FROM loan l
		   JOIN copy c ON c.id = l.copy_id
		   JOIN borrower br ON br.id = l.borrower_id
		  WHERE c.book_id = ? ORDER BY l.loaned_on DESC LIMIT 50`, id)
	if err != nil {
		return nil, err
	}
	defer hrows.Close()
	for hrows.Next() {
		var h HistoryRow
		var outFlag int
		var loanedOn, returnedOn string
		if err := hrows.Scan(&h.BorrowerID, &h.FirstName, &h.LastInitial, &h.Class, &loanedOn, &outFlag, &returnedOn, &h.Days); err != nil {
			return nil, err
		}
		h.Out = outFlag == 1
		h.LoanedOn = loanedOn
		if returnedOn != "" {
			h.ReturnedOn = returnedOn
		}
		f.History = append(f.History, h)
	}

	if err := hrows.Err(); err != nil {
		return nil, err
	}

	// Read once every set of rows above is drained: one connection.
	f.Stats = a.bookStats(id)

	f.computeLinks()
	return f, nil
}

// No network call.
func (f *BookPage) computeLinks() {
	if f.SourceURL != "" {
		f.RecordLink = f.SourceURL // permanent record (BnF ARK, Open Library...)
	}
	if f.ISBN13 != "" {
		// Not the BnF search link, which rate-limits per IP; the ARK permalink is enough.
		if !strings.Contains(f.SourceURL, "catalogue.bnf.fr") {
			f.BnFLink = "https://catalogue.bnf.fr/rechercher.do?motRecherche=" + f.ISBN13 + "&critereRecherche=0&depart=0"
		}
		f.GoogleBooksLink = "https://books.google.com/books?vid=ISBN" + f.ISBN13
		f.UniCatLink = "https://www.unicat.be/uniCat?func=search&query=sysall:" + f.ISBN13
		f.GoogleLink = "https://www.google.com/search?q=" + f.ISBN13
	} else if f.Title != "" {
		q := url.QueryEscape(f.Title + " " + f.Authors)
		f.GoogleLink = "https://www.google.com/search?q=" + q
		f.GoogleBooksLink = "https://www.google.com/search?tbm=bks&q=" + q
	}
}

// Mark is the cote the book's labels print, from the record as it stands.
func (f BookPage) Mark() string { return shelfMark(f.Authors, f.Title) }

// Shelf is one emplacement a book's copies sit in, and how many sit there.
type Shelf struct {
	Place  string // "" for copies not filed yet
	Copies int
}

// Shelves are the emplacements the copies still in the collection sit in, each
// once, in copy order.
func (f BookPage) Shelves() []Shelf {
	var out []Shelf
	for _, c := range f.Copies {
		if c.Status == "withdrawn" || c.Status == "lost" {
			continue
		}
		i := slices.IndexFunc(out, func(s Shelf) bool { return s.Place == c.Location })
		if i < 0 {
			out = append(out, Shelf{Place: c.Location})
			i = len(out) - 1
		}
		out[i].Copies++
	}
	return out
}

// ShelvedCopies is how many copies Shelves counts, which says whether a count
// per emplacement tells anything.
func (f BookPage) ShelvedCopies() int {
	n := 0
	for _, s := range f.Shelves() {
		n += s.Copies
	}
	return n
}

func (a *app) bookScreen(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	f, err := a.loadBookPage(id)
	if err == sql.ErrNoRows {
		a.notFoundScreen(w, r)
		return
	}
	if err != nil {
		log.Printf("book: %v", err)
		internalError(w, r)
		return
	}
	f.Ok = r.URL.Query().Get("ok") == "1"
	f.LoanClosed = r.URL.Query().Get("closed") == "1"
	switch r.URL.Query().Get("enriched") {
	case "1":
		f.Enriched = true
	case "0":
		f.NotFound = true
	case "silent":
		f.Silent = true
	}
	switch r.URL.Query().Get("err") {
	case "title":
		f.Error = tr(r, "common.err_title_required")
	case "loans":
		f.Error = tr(r, "book.delete_blocked")
	}
	a.render(w, r, "book", map[string]any{"Title": f.Title, "F": f})
}

func (a *app) bookSave(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	if err := r.ParseForm(); err != nil {
		badRequest(w, r)
		return
	}
	title := strings.TrimSpace(r.FormValue("title"))
	if title == "" {
		redirectBook(w, r, id, "?err=title")
		return
	}
	year, _ := strconv.Atoi(strings.TrimSpace(r.FormValue("year")))
	if _, err := a.db.Exec(
		`UPDATE book SET title = ?, subtitle = ?, authors = ?, publisher = ?, year = ?, language = ?,
		                    updated_at = datetime('now')
		  WHERE id = ?`,
		title, nullable(r.FormValue("subtitle")), nullable(r.FormValue("authors")), nullable(r.FormValue("publisher")),
		nullableInt(year), nullable(r.FormValue("language")), id,
	); err != nil {
		log.Printf("book/save: %v", err)
		internalError(w, r)
		return
	}
	redirectBook(w, r, id, "?ok=1")
}

// errBookHasLoans is a work somebody has borrowed, which is never deleted.
var errBookHasLoans = errors.New("book has loans")

// deleteBook removes a work catalogued by mistake, with its copies — the only
// deletion in the application, and only for a work no loan has ever touched.
// ON DELETE RESTRICT enforces it; the count is there to answer with a
// sentence. Counted inside the transaction, so nothing lends a copy in between.
func (a *app) deleteBook(id int64) error {
	tx, err := a.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var title string
	if err := tx.QueryRow(`SELECT title FROM book WHERE id = ?`, id).Scan(&title); err != nil {
		return err
	}
	var loans int
	if err := tx.QueryRow(
		`SELECT COUNT(*) FROM loan l JOIN copy c ON c.id = l.copy_id WHERE c.book_id = ?`, id,
	).Scan(&loans); err != nil {
		return err
	}
	if loans > 0 {
		return errBookHasLoans
	}
	if _, err := tx.Exec(`DELETE FROM copy WHERE book_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM book WHERE id = ?`, id); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	// Nothing here can be undone and nothing else in the application removes a
	// row, so it leaves a trace.
	log.Printf("book deleted: %d %q", id, title)
	return nil
}

func (a *app) bookDelete(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	switch err := a.deleteBook(id); {
	case err == nil:
		// The page it was called from no longer exists.
		http.Redirect(w, r, "/inventory", http.StatusSeeOther)
	case errors.Is(err, errBookHasLoans):
		redirectBook(w, r, id, "?err=loans")
	case errors.Is(err, sql.ErrNoRows):
		a.notFoundScreen(w, r)
	default:
		log.Printf("book/delete: %v", err)
		internalError(w, r)
	}
}

// bookEnrich fills the empty fields from the catalogues. Non-destructive.
func (a *app) bookEnrich(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	f, err := a.loadBookPage(id)
	if err != nil {
		a.notFoundScreen(w, r)
		return
	}
	if f.ISBN13 == "" {
		redirectBook(w, r, id, "")
		return
	}
	// An explicit request: forget record and thumbnail so the catalogues are asked again.
	catalogueMemo.forgetRecord(f.ISBN13)
	a.covers.forget(f.ISBN13)

	ctx, done := enrichWithin(w, r, enrichBudgetBookPage)
	defer done()

	// The page says whether no catalogue has the book or one could not be asked.
	n, err := enrich(ctx, f.ISBN13, f.ISBN10)
	if n == nil {
		outcome := "0"
		if err != nil {
			outcome = "silent"
		}
		redirectBook(w, r, id, "?enriched="+outcome)
		return
	}

	// Only what is missing, preserving manual corrections.
	fillIfEmpty := func(current, incoming string) any {
		if strings.TrimSpace(current) == "" && strings.TrimSpace(incoming) != "" {
			return incoming
		}
		return nullable(current)
	}
	finalYear := f.Year
	if finalYear == 0 {
		finalYear = n.Year
	}
	finalURL := f.SourceURL
	if finalURL == "" {
		finalURL = n.URL
	}
	// Provenance follows the data; the raw response is stored so enrichment can
	// be replayed.
	payload := n.Payload
	if len(payload) > 100000 {
		payload = payload[:100000]
	}
	if _, err := a.db.Exec(
		`UPDATE book SET subtitle = ?, authors = ?, publisher = ?, year = ?, language = ?,
		                    source_metadata = ?, source_url = ?, source_payload = ?,
		                    source_date = datetime('now'), updated_at = datetime('now')
		  WHERE id = ?`,
		fillIfEmpty(f.Subtitle, n.Subtitle), fillIfEmpty(f.Authors, n.Authors), fillIfEmpty(f.Publisher, n.Publisher),
		nullableInt(finalYear), fillIfEmpty(f.Language, n.Language),
		nullable(n.Source), nullable(finalURL), nullable(payload), id,
	); err != nil {
		log.Printf("book/enrich: %v", err)
		internalError(w, r)
		return
	}
	redirectBook(w, r, id, "?enriched=1")
}

func (a *app) bookAddCopy(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	if _, _, err := a.addCopy(id); err != nil {
		log.Printf("book/add-copy: %v", err)
		internalError(w, r)
		return
	}
	// The page finds today's copies for itself.
	redirectBook(w, r, id, "")
}

func (a *app) bookCopyStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	exid, _ := strconv.ParseInt(r.PathValue("copyid"), 10, 64)
	status := strings.TrimSpace(r.FormValue("status"))
	if !validStatuses[status] {
		http.Error(w, tr(r, "error.bad_status"), http.StatusBadRequest)
		return
	}
	// The row's form carries the location too; a form without it leaves it alone.
	var location *string
	if _, ok := r.Form["location"]; ok {
		l := strings.TrimSpace(r.FormValue("location"))
		location = &l
	}
	loanClosed, err := a.updateCopy(exid, status, location)
	if err != nil {
		log.Printf("book/copy-status: %v", err)
		internalError(w, r)
		return
	}
	dest := "/book/" + id
	if loanClosed {
		dest += "?closed=1"
	}
	http.Redirect(w, r, dest, http.StatusSeeOther)
}

// A book declared lost or damaged has come back after all. Answers with a
// fragment for the return screen, where the action starts.
func (a *app) copyReinstate(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	if _, err := a.updateCopy(id, "available", nil); err != nil {
		log.Printf("copy/reinstate: %v", err)
		internalError(w, r)
		return
	}
	a.fragment(w, r, "return", "return_reinstated", nil)
}
