package main

import (
	"html/template"
	"log"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

// Editable inventory. Unlike the printable one (/print/inventory), this screen
// corrects a copy's status and location in place (HTMX).

// InvRow is one row of the editable inventory.
type InvRow struct {
	ID          int64
	BookID      int64
	Title       string
	Authors     string
	ISBN        string
	URL         string // source record (ARK BnF, Open Library page…), empty otherwise
	Code        string
	Mark        string // the cote, shelfMark of the book
	Location    string
	Status      string
	AddedOn     string // ISO date the copy was recorded
	Out         bool
	DueOn       string // open loan: when it is due back, "" on the shelf
	DaysOverdue int    // open loan: days past DueOn (> 0 = late)
	Flash       bool   // true right after a save, for the highlight
	LoanClosed  bool   // the open loan has just been closed (book lost or withdrawn)
}

var validStatuses = map[string]bool{
	"available": true, "damaged": true, "lost": true, "withdrawn": true,
}

// statusesClosingLoan are the statuses meaning the book will not come back.
var statusesClosingLoan = map[string]bool{"lost": true, "withdrawn": true}

// statusesOnShelf are the statuses of a copy physically in the library:
// damaged counts, lost and withdrawn do not (and are not out either). Not
// v_available, which answers "can this be lent". One list for onShelf and the filter.
var statusesOnShelf = []string{"available", "damaged"}

// onShelf answers, for a copy that is not out, whether it is somewhere to be
// found. Exposed to the templates, so the three that draw the loan column agree.
func onShelf(status string) bool { return slices.Contains(statusesOnShelf, status) }

// updateCopy saves a copy's status and, when given one, its location. Lost or
// withdrawn closes the open loan; damaged does not. A nil location leaves it alone.
func (a *app) updateCopy(exID int64, status string, location *string) (loanClosed bool, err error) {
	tx, err := a.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	if location != nil {
		_, err = tx.Exec(`UPDATE copy SET status = ?, location = ? WHERE id = ?`,
			status, nullable(*location), exID)
	} else {
		_, err = tx.Exec(`UPDATE copy SET status = ? WHERE id = ?`, status, exID)
	}
	if err != nil {
		return false, err
	}

	if statusesClosingLoan[status] {
		res, err := tx.Exec(
			`UPDATE loan SET returned_on = date('now')
			  WHERE copy_id = ? AND returned_on IS NULL`, exID)
		if err != nil {
			return false, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			loanClosed = true
		}
	}
	return loanClosed, tx.Commit()
}

// A location and how many copies sit there, for the filter dropdown. Locations
// are free text, so the list is whatever has been used.
type LocationCount struct {
	Location string // "" = the copies with none
	Count    int
}

// locationFilterNone is what the dropdown submits for "copies with no location",
// since an empty value means every location. Same sentinel as the class filter.
const locationFilterNone = classFilterNone

// locationFilter reads the dropdown's value: "" is every location, the
// sentinel is the copies that have none, anything else is itself.
func locationFilter(v string) (value string, filter bool) {
	switch v = strings.TrimSpace(v); v {
	case "":
		return "", false
	case locationFilterNone:
		return "", true
	default:
		return v, true
	}
}

// copyLocations lists the locations in use, with how many copies are in each.
// The copies with no location come last: a state, not a place.
func (a *app) copyLocations() ([]LocationCount, error) {
	rows, err := a.db.Query(
		`SELECT COALESCE(location, ''), COUNT(*) FROM copy
		  GROUP BY COALESCE(location, '')
		  ORDER BY CASE WHEN COALESCE(location, '') = '' THEN 1 ELSE 0 END, location`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LocationCount
	for rows.Next() {
		var c LocationCount
		if err := rows.Scan(&c.Location, &c.Count); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// locationCondition is the SQL the dropdown means, for a query that aliases copy as c.
func locationCondition(v string) (cond string, args []any, filtering bool) {
	value, filtering := locationFilter(v)
	if !filtering {
		return "", nil, false
	}
	return `COALESCE(c.location, '') = ?`, []any{value}, true
}

// titleCodeFilter turns a search box into SQL conditions: every word must match
// the title, subtitle, an author, the internal code or the ISBN; an empty q adds
// nothing. A whole ISBN is looked up under both forms. The query must alias
// book as b and copy as c.
func titleCodeFilter(q string) (conds []string, args []any) {
	if i13, i10, ok := isbnQuery(q); ok {
		return []string{`(b.isbn13 = ? OR b.isbn10 = ?)`}, []any{i13, i10}
	}
	for _, word := range strings.Fields(q) {
		like := "%" + foldSearch(word) + "%"
		conds = append(conds, `(fold(b.title) LIKE ?
		     OR fold(COALESCE(b.subtitle, '')) LIKE ?
		     OR fold(COALESCE(b.authors, '')) LIKE ?
		     OR fold(c.code) LIKE ?
		     OR fold(COALESCE(b.isbn13, '')) LIKE ?)`)
		args = append(args, like, like, like, like, like)
	}
	return conds, args
}

// isbnQuery reads a search box that holds one ISBN and nothing else: digits,
// hyphens and spaces, an X, perhaps the word "ISBN" copied with it. A valid
// check digit is required, so a title with a number in it stays a title.
func isbnQuery(q string) (isbn13, isbn10 string, ok bool) {
	rest := strings.ToUpper(strings.TrimSpace(q))
	rest = strings.TrimSpace(strings.TrimPrefix(rest, "ISBN"))
	rest = strings.TrimSpace(strings.TrimPrefix(rest, ":"))
	if rest == "" || strings.Trim(rest, "0123456789X- ") != "" {
		return "", "", false
	}
	i13, i10, err := ISBNForms(rest)
	return i13, i10, err == nil
}

// invFilter is what the screen is showing: which copies, and in what order.
// It travels as a query string (bookmarkable) and as form fields (live search).
type invFilter struct {
	Q        string // words from the title, subtitle, author, code or ISBN
	Location string // "" = everywhere, locationFilterNone = the unfiled
	Status   string // "" = every condition
	// Apart from Status: a condition is what the copy is, a loan where it is today.
	Loan string // "" = both, invLoanOut, invLoanShelf
	// When the copy was recorded (not acquired_on, mostly empty). Added names a
	// period (addedPeriods); From and To are an exact ISO range. One or the other.
	Added string
	From  string
	To    string
	Sort  string // a key of invSortColumns
	Dir   string // "asc" or "desc"
}

// addedPeriods is what the dropdown offers besides an exact range. The key
// travels, not the dates, so a bookmarked "?added=7d" stays relative to today.
var addedPeriods = map[string]int{"today": 0, "7d": 7, "30d": 30}

// What the loan filter may say. Whitelisted like every value arriving in a query
// string: anything else means both.
const (
	invLoanOut   = "out"
	invLoanShelf = "shelf"
)

var invLoanStates = map[string]bool{invLoanOut: true, invLoanShelf: true}

// addedCustom is the entry that reveals the two date fields.
const addedCustom = "custom"

// Bounds is the range the filter selects, as ISO dates — the period resolved
// against today, or the two dates typed. An empty string is no bound.
func (f invFilter) Bounds() (from, to string) { return f.boundsFrom(today()) }

func (f invFilter) boundsFrom(now time.Time) (from, to string) {
	if days, ok := addedPeriods[f.Added]; ok {
		return now.AddDate(0, 0, -days).Format("2006-01-02"), ""
	}
	return f.From, f.To
}

// RangeOpen says whether the two date fields are on screen: the entry that
// reveals them, or an address that arrived carrying dates.
func (f invFilter) RangeOpen() bool {
	return f.Added == addedCustom || f.From != "" || f.To != ""
}

// invSortColumns is the whitelist of what a heading may sort by: an ORDER BY
// cannot be a parameter, so the request's value is only ever a map key. Every
// order ends in title and code so ties do not shuffle between loads.
var invSortColumns = map[string]string{
	// The order a shelf is walked in, as the collection review has it: cote,
	// then author, then title.
	"mark":     `shelf_mark(b.authors, b.title) {dir}, fold(COALESCE(b.authors, '')) {dir}, fold(b.title) {dir}, c.code`,
	"title":    `b.title {dir}, c.code`,
	"isbn":     `COALESCE(b.isbn13, '') {dir}, b.title, c.code`,
	"code":     `c.code {dir}`,
	"location": `COALESCE(c.location, '') = '' {dir}, COALESCE(c.location, '') {dir}, b.title, c.code`,
	"status":   `c.status {dir}, b.title, c.code`,
	"loan":     `outFlag {dir}, b.title, c.code`,
	// The order the copies were recorded in, to the second, then as created.
	"added": `c.created_at {dir}, c.id {dir}`,
}

// An unsorted screen opens on the copies most recently recorded, newest first.
const (
	invSortDefault    = "added"
	invSortDefaultDir = "desc"
)

// readInvFilter reads the filter from r.Form (query string or posted fields).
// Anything unrecognised falls back to the default rather than erroring.
func readInvFilter(r *http.Request) invFilter {
	f := invFilter{
		Q:        strings.TrimSpace(r.FormValue("q")),
		Location: strings.TrimSpace(r.FormValue("location")),
		Status:   strings.TrimSpace(r.FormValue("status")),
		Loan:     strings.TrimSpace(r.FormValue("loan")),
		Added:    strings.TrimSpace(r.FormValue("added")),
		Sort:     r.FormValue("sort"),
		Dir:      r.FormValue("dir"),
	}
	// A period that is not one of ours means every date, like an empty value.
	if _, ok := addedPeriods[f.Added]; !ok && f.Added != addedCustom {
		f.Added = ""
	}
	// A date that is not a date is dropped rather than queried with: an empty
	// bound shows too much, a nonsense one would silently show nothing.
	if d := strings.TrimSpace(r.FormValue("from")); isISODate(d) {
		f.From = d
	}
	if d := strings.TrimSpace(r.FormValue("to")); isISODate(d) {
		f.To = d
	}
	// Hidden date fields still post what was last typed; the period wins.
	if _, ok := addedPeriods[f.Added]; ok {
		f.From, f.To = "", ""
	}
	// No column named, or one not in the whitelist: the default order, direction
	// included. The direction is one of two literals, never the request's word.
	if _, ok := invSortColumns[f.Sort]; !ok {
		f.Sort, f.Dir = invSortDefault, invSortDefaultDir
	}
	if f.Dir != "desc" {
		f.Dir = "asc"
	}
	if !validStatuses[f.Status] {
		f.Status = ""
	}
	if !invLoanStates[f.Loan] {
		f.Loan = ""
	}
	return f
}

// URL is a link out of the screen carrying the filter. The whole address as a
// template.URL: after a "?" html/template escapes a value as ONE parameter, so
// "a=1&b=2" arrives as "a%3d1%26b%3d2" and the filter reads as empty.
func (f invFilter) URL(path string) template.URL {
	if q := f.Query(); q != "" {
		return template.URL(path + "?" + q)
	}
	return template.URL(path)
}

// Query is the filter as a query string, built by url.Values: a location is
// free text, and a half-escaped one silently selects nothing.
func (f invFilter) Query() string {
	v := url.Values{}
	for name, value := range map[string]string{
		"q": f.Q, "location": f.Location, "status": f.Status, "loan": f.Loan,
		"added": f.Added, "from": f.From, "to": f.To,
		"sort": f.Sort, "dir": f.Dir,
	} {
		if value != "" {
			v.Set(name, value)
		}
	}
	return v.Encode()
}

// Filtering says whether the screen is narrowed at all, so a printout can name
// what it was narrowed to.
func (f invFilter) Filtering() bool {
	from, to := f.Bounds()
	return f.Q != "" || f.Location != "" || f.Status != "" || f.Loan != "" || from != "" || to != ""
}

// ByDate says whether the list is narrowed to a period of entry, which is when
// the screen shows the day each copy was recorded (inventory.html, .col-added).
func (f invFilter) ByDate() bool {
	from, to := f.Bounds()
	return from != "" || to != ""
}

// SortedBy and Descending let a column heading draw its own arrow.
func (f invFilter) SortedBy(column string) bool { return f.Sort == column }
func (f invFilter) Descending() bool            { return f.Dir == "desc" }

// The form the headings post through, so a sort keeps the filter. The id is in inventory.html.
func (f invFilter) SortForm() string { return "inv-filter" }

// invSelect is where a row of the inventory is read from, shared by the list
// and the row redrawn after an inline edit, so the swap loses no column.
const invSelect = `SELECT c.id, b.id, b.title, COALESCE(b.authors, ''), COALESCE(b.isbn13, ''), COALESCE(b.source_url, ''), c.code,
	        COALESCE(c.location, ''), c.status, date(c.created_at),
	        CASE WHEN l.id IS NOT NULL THEN 1 ELSE 0 END AS outFlag,
	        COALESCE(l.due_on, ''),
	        COALESCE(CAST(julianday('now') - julianday(l.due_on) AS INTEGER), 0),
	        shelf_mark(b.authors, b.title)
	   FROM copy c
	   JOIN book b ON b.id = c.book_id
	   LEFT JOIN loan l ON l.copy_id = c.id AND l.returned_on IS NULL`

// rowScanner is what both *sql.Row and *sql.Rows answer, so one scan reads a
// row whether it came from the list or from a single lookup.
type rowScanner interface{ Scan(dest ...any) error }

func scanInvRow(s rowScanner) (InvRow, error) {
	var l InvRow
	var outFlag int
	err := s.Scan(&l.ID, &l.BookID, &l.Title, &l.Authors, &l.ISBN, &l.URL, &l.Code,
		&l.Location, &l.Status, &l.AddedOn, &outFlag, &l.DueOn, &l.DaysOverdue, &l.Mark)
	l.Out = outFlag == 1
	return l, err
}

// invRow reads the one copy, as the list would have read it.
func (a *app) invRow(id int64) (InvRow, error) {
	return scanInvRow(a.db.QueryRow(invSelect+` WHERE c.id = ?`, id))
}

// listInventory lists every copy the filter selects, in its order, for the
// printouts and exports; the screen reads one page through pageInventory.
func (a *app) listInventory(f invFilter) ([]InvRow, error) {
	return a.queryInvRows(invSelect+invWhere(f)+` ORDER BY `+invOrderBy(f), invArgs(f))
}

// pageInventory reads the page starting at offset and the total count. Two
// queries in sequence: the count closes before the rows open.
func (a *app) pageInventory(f invFilter, offset int) ([]InvRow, page, error) {
	pg := page{Form: f.SortForm(), Post: "/inventory/search", Target: "#inventory-list"}
	if err := a.db.QueryRow(
		`SELECT COUNT(*) FROM copy c JOIN book b ON b.id = c.book_id`+invWhere(f),
		invArgs(f)...).Scan(&pg.Total); err != nil {
		return nil, pg, err
	}
	pg.Offset = clampOffset(offset, pg.Total)
	rows, err := a.queryInvRows(
		invSelect+invWhere(f)+` ORDER BY `+invOrderBy(f)+` LIMIT ? OFFSET ?`,
		append(invArgs(f), pageSize, pg.Offset))
	return rows, pg, err
}

func (a *app) queryInvRows(query string, args []any) ([]InvRow, error) {
	rows, err := a.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []InvRow
	for rows.Next() {
		l, err := scanInvRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// invConditions is the WHERE the filter means, for any query that aliases book
// as b and copy as c: one builder for the list, the printouts and the exports.
func invConditions(f invFilter) (conds []string, args []any) {
	conds, args = titleCodeFilter(f.Q)
	if cond, a2, ok := locationCondition(f.Location); ok {
		conds = append(conds, cond)
		args = append(args, a2...)
	}
	if validStatuses[f.Status] {
		conds = append(conds, `c.status = ?`)
		args = append(args, f.Status)
	}
	// An EXISTS rather than a test on invSelect's LEFT JOIN: this builder also
	// serves the count query, which joins copy to book and nothing else.
	switch f.Loan {
	case invLoanOut:
		conds = append(conds, `EXISTS (`+openLoanForCopy+`)`)
	case invLoanShelf:
		// Not out AND actually there: a lost copy has no open loan either.
		conds = append(conds, `NOT EXISTS (`+openLoanForCopy+`) AND c.status IN (`+
			placeholders(len(statusesOnShelf))+`)`)
		for _, s := range statusesOnShelf {
			args = append(args, s)
		}
	}
	from, to := f.Bounds()
	if from != "" {
		conds = append(conds, `date(c.created_at) >= ?`)
		args = append(args, from)
	}
	if to != "" {
		conds = append(conds, `date(c.created_at) <= ?`)
		args = append(args, to)
	}
	return conds, args
}

// openLoanForCopy is "this copy is out": a loan on it that has not come back.
// The partial unique index guarantees there is at most one.
const openLoanForCopy = `SELECT 1 FROM loan l2
	 WHERE l2.copy_id = c.id AND l2.returned_on IS NULL`

// invWhere and invArgs are invConditions in the two halves a query needs.
func invWhere(f invFilter) string {
	conds, _ := invConditions(f)
	if len(conds) == 0 {
		return ""
	}
	return ` WHERE ` + strings.Join(conds, " AND ")
}

func invArgs(f invFilter) []any {
	_, args := invConditions(f)
	return args
}

// invOrderBy builds the ORDER BY from the whitelist: the column is a map key and
// the direction one of two literals, so nothing from the request reaches the SQL.
func invOrderBy(f invFilter) string {
	expr, ok := invSortColumns[f.Sort]
	if !ok {
		expr = invSortColumns[invSortDefault]
	}
	dir := "ASC"
	if f.Dir == "desc" {
		dir = "DESC"
	}
	return strings.ReplaceAll(expr, "{dir}", dir)
}

// inventoryData is the screen and its fragment, which show the same thing:
// one page of what the filter selects.
func (a *app) inventoryData(f invFilter, offset int) (map[string]any, error) {
	rows, pg, err := a.pageInventory(f, offset)
	if err != nil {
		return nil, err
	}
	// After the list: one connection in the pool, and the *sql.Rows above must
	// be closed before this asks for it.
	locations, err := a.copyLocations()
	if err != nil {
		return nil, err
	}
	return map[string]any{"Rows": rows, "Page": pg, "Filter": f, "Locations": locations}, nil
}

func (a *app) inventoryScreen(w http.ResponseWriter, r *http.Request) {
	data, err := a.inventoryData(readInvFilter(r), readOffset(r))
	if err != nil {
		log.Printf("inventory: %v", err)
		internalError(w, r)
		return
	}
	data["Title"] = tr(r, "nav.inventory")
	a.render(w, r, "inventory", data)
}

// inventorySearch redraws the list (HTMX) from the one filter form, which the
// headings and the page turner post too.
func (a *app) inventorySearch(w http.ResponseWriter, r *http.Request) {
	data, err := a.inventoryData(readInvFilter(r), readOffset(r))
	if err != nil {
		log.Printf("inventory/search: %v", err)
		internalError(w, r)
		return
	}
	// The output links sit outside the list, so they are swapped out of band.
	data["OOB"] = true
	a.fragment(w, r, "inventory", "inventory_list", data)
}

// inventoryUpdate saves a copy's status and location, then returns the updated
// row with a confirmation highlight.
func (a *app) inventoryUpdate(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	if err := r.ParseForm(); err != nil {
		badRequest(w, r)
		return
	}
	status := strings.TrimSpace(r.FormValue("status"))
	if !validStatuses[status] {
		http.Error(w, tr(r, "error.bad_status"), http.StatusBadRequest)
		return
	}
	location := strings.TrimSpace(r.FormValue("location"))

	loanClosed, err := a.updateCopy(id, status, &location)
	if err != nil {
		log.Printf("inventory/update: %v", err)
		internalError(w, r)
		return
	}

	// Re-read through the query the list itself uses, so the swap puts back the
	// row the list would have drawn.
	l, err := a.invRow(id)
	if err != nil {
		log.Printf("inventory/update (reread): %v", err)
		internalError(w, r)
		return
	}
	l.Flash = true
	l.LoanClosed = loanClosed
	a.fragment(w, r, "inventory", "inv_row", l)
}
