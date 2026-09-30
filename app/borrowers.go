package main

import (
	"cmp"
	"crypto/sha256"
	"database/sql"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Borrowers: entry, CSV import with preview, barcode cards, year rollover.
// Teachers are borrowers of a particular kind, and borrow in their own name.

type BorrowerRow struct {
	ID           int64
	FirstName    string
	LastInitial  string
	Class        string
	Kind         string
	CardCode     string
	Token        string // parent link token, empty when not created
	Active       bool   // false = left the school, kept for the history
	TotalCount   int    // loans in total
	OutCount     int    // open loans
	OverdueCount int    // open loans that are overdue
}

// Columns common to borrower queries, with counters named so an ORDER BY can
// refer to them (brSortColumns).
const borrowerColumns = `br.id, br.first_name, br.last_initial, COALESCE(br.class, ''), br.kind, COALESCE(br.card_code, ''),
	COALESCE(br.family_token, ''), br.active,
	(SELECT COUNT(*) FROM loan l WHERE l.borrower_id = br.id) AS total_count,
	(SELECT COUNT(*) FROM loan l WHERE l.borrower_id = br.id AND l.returned_on IS NULL) AS out_count,
	(SELECT COUNT(*) FROM loan l WHERE l.borrower_id = br.id AND l.returned_on IS NULL AND l.due_on < date('now')) AS overdue_count`

func scanBorrower(sc rowScanner, e *BorrowerRow) error {
	var active int
	err := sc.Scan(&e.ID, &e.FirstName, &e.LastInitial, &e.Class, &e.Kind, &e.CardCode, &e.Token, &active,
		&e.TotalCount, &e.OutCount, &e.OverdueCount)
	e.Active = active == 1
	return err
}

// Anonymised: the daily purge has cleared the initial and the card, which
// no living record lacks.
func (e BorrowerRow) Anonymised() bool {
	return !e.Active && e.LastInitial == "" && e.CardCode == ""
}

// "Durant" -> "D.". The full last name is NEVER stored (GDPR): should the
// database leak, "first name + D. + class" stays barely identifying.
func lastNameInitial(lastName string) string {
	lastName = strings.TrimSpace(lastName)
	if lastName == "" {
		return ""
	}
	r := []rune(lastName)
	return strings.ToUpper(string(r[0])) + "."
}

type importRow struct {
	FirstName   string
	LastInitial string
	Class       string
	Duplicate   bool // already on file, or already met in the file
}

// First name + last name initial + class: the full last name is not stored.
func borrowerKey(firstName, lastName, class string) string {
	n := func(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
	return n(firstName) + "|" + n(lastName) + "|" + n(class)
}

// Flags rows already on file and repetitions inside the file: a second import
// would otherwise duplicate every pupil, and there is no deletion to undo it.
func markDuplicates(rows []importRow, existing map[string]bool) int {
	seen := make(map[string]bool, len(rows))
	count := 0
	for i := range rows {
		c := borrowerKey(rows[i].FirstName, rows[i].LastInitial, rows[i].Class)
		if existing[c] || seen[c] {
			rows[i].Duplicate = true
			count++
		}
		seen[c] = true
	}
	return count
}

// Read BEFORE opening a transaction (one-connection pool, see openDB).
func (a *app) existingBorrowers() (map[string]bool, error) {
	rows, err := a.db.Query(
		`SELECT first_name, last_initial, COALESCE(class, '') FROM borrower WHERE active = 1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := make(map[string]bool)
	for rows.Next() {
		var firstName, lastName, class string
		if err := rows.Scan(&firstName, &lastName, &class); err != nil {
			return nil, err
		}
		m[borrowerKey(firstName, lastName, class)] = true
	}
	return m, rows.Err()
}

// brFilter is what the borrower list is showing: which borrowers, and in what
// order. Same shape as invFilter: a query string, and form fields for HTMX.
type brFilter struct {
	Q        string
	Inactive bool
	Class    string
	Sort     string // a key of brSortColumns; "" is the standing order
	Dir      string // "asc" or "desc"
}

func readBrFilter(r *http.Request) brFilter {
	return brFilter{
		Q:        strings.TrimSpace(r.FormValue("q")),
		Inactive: r.FormValue("inactive") == "1",
		Class:    currentClass(r),
		Sort:     r.FormValue("sort"),
		Dir:      r.FormValue("dir"),
	}
}

func (f brFilter) Active() bool                { return !f.Inactive }
func (f brFilter) SortedBy(column string) bool { return f.Sort == column }
func (f brFilter) Descending() bool            { return f.Dir == "desc" }
func (f brFilter) SortForm() string            { return "br-filter" }

// brSortColumns is the whitelist of what a heading may sort by: the value comes
// from a form and is concatenated into SQL. Every order ends in the name, so
// ties do not shuffle between draws.
var brSortColumns = map[string]string{
	"name":    `br.first_name {dir}, br.last_initial {dir}`,
	"class":   `COALESCE(br.class, '') = '' {dir}, COALESCE(br.class, '') {dir}, br.first_name, br.last_initial`,
	"kind":    `br.kind {dir}, br.first_name, br.last_initial`,
	"loans":   `total_count {dir}, br.first_name, br.last_initial`,
	"out":     `out_count {dir}, br.first_name, br.last_initial`,
	"overdue": `overdue_count {dir}, br.first_name, br.last_initial`,
	"card":    `COALESCE(br.card_code, '') {dir}, br.first_name, br.last_initial`,
}

// The standing order of an unsorted list: pupils then teachers, by class, by
// name. It is no one column, so no heading shows the sorted arrow.
const brStandingOrder = `br.kind, br.class, br.last_initial, br.first_name`

func brOrderBy(f brFilter) string {
	expr, ok := brSortColumns[f.Sort]
	if !ok {
		return brStandingOrder
	}
	dir := "ASC"
	if f.Dir == "desc" {
		dir = "DESC"
	}
	return strings.ReplaceAll(expr, "{dir}", dir)
}

// brWhere is the WHERE the filter means. One builder for the list and its
// count, so the two cannot disagree.
func brWhere(f brFilter) (where string, args []any) {
	// The sentinel borrower of anonymised loans is not a person.
	where = ` WHERE br.active = ? AND br.id <> ?`
	args = []any{b2i(f.Active()), anonymousID()}
	if value, filter := classFilter(f.Class); filter {
		where += ` AND COALESCE(br.class, '') = ?`
		args = append(args, value)
	}
	// The card code is searched too, so scanning a card into the box finds its
	// borrower (as titleCodeFilter does for VOL codes).
	for _, word := range strings.Fields(f.Q) {
		like := "%" + foldSearch(word) + "%"
		where += ` AND (fold(br.first_name) LIKE ? OR fold(br.last_initial) LIKE ?
		            OR fold(COALESCE(br.class,'')) LIKE ? OR fold(COALESCE(br.card_code,'')) LIKE ?)`
		args = append(args, like, like, like, like)
	}
	return where, args
}

// listBorrowersFiltered is everyone the filter selects, in its order. Every
// word must appear; an empty q means everything.
func (a *app) listBorrowersFiltered(f brFilter) ([]BorrowerRow, error) {
	where, args := brWhere(f)
	return a.queryBorrowers(`SELECT `+borrowerColumns+` FROM borrower br`+where+` ORDER BY `+brOrderBy(f), args)
}

// pageBorrowers reads one page from offset, and the total the filter selects.
// The count closes before the rows open.
func (a *app) pageBorrowers(f brFilter, offset int) ([]BorrowerRow, page, error) {
	pg := page{Form: f.SortForm(), Post: "/borrowers/search", Target: "#borrower-list"}
	where, args := brWhere(f)
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM borrower br`+where, args...).Scan(&pg.Total); err != nil {
		return nil, pg, err
	}
	pg.Offset = clampOffset(offset, pg.Total)
	rows, err := a.queryBorrowers(
		`SELECT `+borrowerColumns+` FROM borrower br`+where+` ORDER BY `+brOrderBy(f)+` LIMIT ? OFFSET ?`,
		append(args, pageSize, pg.Offset))
	return rows, pg, err
}

func (a *app) queryBorrowers(query string, args []any) ([]BorrowerRow, error) {
	rows, err := a.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BorrowerRow
	for rows.Next() {
		var e BorrowerRow
		if err := scanBorrower(rows, &e); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// classFilterNone is what the class dropdown submits for "no class", since
// empty already means "every class".
const classFilterNone = "-"

// classFilter turns the ?class= parameter into the value to compare against
// COALESCE(class, ”), and whether to compare at all.
func classFilter(v string) (value string, filter bool) {
	switch v = strings.TrimSpace(v); v {
	case "":
		return "", false // every class
	case classFilterNone:
		return "", true // the borrowers who have none
	default:
		return v, true
	}
}

// A class and its headcount, for the filter dropdown.
type ClassCount struct {
	Class string
	Count int
}

// The classless come last, under an empty Class. The sentinel borrower of
// anonymised loans is excluded.
func (a *app) listClasses(active bool) ([]ClassCount, error) {
	rows, err := a.db.Query(
		`SELECT COALESCE(class, ''), COUNT(*) FROM borrower
		  WHERE active = ? AND id <> ?
		  GROUP BY COALESCE(class, '')
		  ORDER BY CASE WHEN COALESCE(class, '') = '' THEN 1 ELSE 0 END, class`,
		b2i(active), anonymousID())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ClassCount
	for rows.Next() {
		var c ClassCount
		if err := rows.Scan(&c.Class, &c.Count); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// HTMX actions do not carry the class filter, so it is recovered from the URL
// the browser shows, which HTMX sends as a header.
func currentClass(r *http.Request) string {
	if c := strings.TrimSpace(r.FormValue("class")); c != "" {
		return c
	}
	return urlClass(r)
}

// The class the list is filtered to, from the URL alone: the add form posts a
// "class" field of its own, which is not the filter.
func urlClass(r *http.Request) string {
	if u, err := url.Parse(r.Header.Get("HX-Current-URL")); err == nil {
		return strings.TrimSpace(u.Query().Get("class"))
	}
	return ""
}

// For SQLite INTEGER columns.
func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// borrowersSearch redraws the list (HTMX) from the posted filter form and page.
func (a *app) borrowersSearch(w http.ResponseWriter, r *http.Request) {
	f := readBrFilter(r)
	borrowers, pg, err := a.pageBorrowers(f, readOffset(r))
	if err != nil {
		log.Printf("borrowers/search: %v", err)
		internalError(w, r)
		return
	}
	a.fragment(w, r, "borrowers", "borrowers_list", map[string]any{
		"Borrowers": borrowers, "Page": pg,
		"Inactive": f.Inactive, "Class": f.Class, "Filter": f,
	})
}

func (a *app) loadBorrower(id int64) (BorrowerRow, error) {
	var e BorrowerRow
	err := scanBorrower(a.db.QueryRow(`SELECT `+borrowerColumns+` FROM borrower br WHERE br.id = ?`, id), &e)
	return e, err
}

type BorrowerLoan struct {
	LoanID      int64
	BookID      int64
	Title       string
	Code        string
	LoanedOn    string
	DueOn       string
	ReturnedOn  string
	DaysOverdue int // open loan: days overdue (> 0 = overdue)
	Days        int // loan duration, for the history
	Out         bool
}

// The books a borrower has, and their history.
func (a *app) borrowerDetail(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	e, err := a.loadBorrower(id)
	if err != nil {
		a.notFoundScreen(w, r)
		return
	}

	rows, err := a.db.Query(
		`SELECT l.id, b.id, b.title, c.code, l.loaned_on, l.due_on,
		        l.returned_on IS NULL AS is_out, COALESCE(l.returned_on, ''),
		        CAST(julianday('now') - julianday(l.due_on) AS INTEGER) AS days_overdue,
		        CAST(julianday(CASE WHEN l.returned_on IS NULL THEN date('now') ELSE l.returned_on END)
		             - julianday(l.loaned_on) AS INTEGER) AS days
		   FROM loan l
		   JOIN copy c ON c.id = l.copy_id
		   JOIN book b ON b.id = c.book_id
		  WHERE l.borrower_id = ?
		  ORDER BY (l.returned_on IS NULL) DESC, l.loaned_on DESC
		  LIMIT 100`, id)
	if err != nil {
		log.Printf("borrower/detail: %v", err)
		internalError(w, r)
		return
	}
	defer rows.Close()

	var out, past []BorrowerLoan
	for rows.Next() {
		var l BorrowerLoan
		var isOut int
		var loanedOn, due, returnedOn string
		if err := rows.Scan(&l.LoanID, &l.BookID, &l.Title, &l.Code, &loanedOn, &due, &isOut,
			&returnedOn, &l.DaysOverdue, &l.Days); err != nil {
			log.Printf("borrower/detail (scan): %v", err)
			internalError(w, r)
			return
		}
		l.Out = isOut == 1
		l.LoanedOn = loanedOn
		l.DueOn = due
		if returnedOn != "" {
			l.ReturnedOn = returnedOn
		}
		if l.Out {
			out = append(out, l)
		} else {
			past = append(past, l)
		}
	}
	if err := rows.Err(); err != nil {
		log.Printf("borrower/detail (rows): %v", err)
		internalError(w, r)
		return
	}

	// The axis for the bars beside the open loans, as on the loans list.
	dates := make([]string, 0, len(out)*2)
	for _, l := range out {
		dates = append(dates, l.LoanedOn, l.DueOn)
	}

	a.render(w, r, "borrower", map[string]any{
		"Title":      e.FirstName + " " + e.LastInitial,
		"E":          e,
		"Out":        out,
		"Past":       past,
		"Timeline":   newTimeline(todayISO(), dates...),
		"ExtendDays": extendDefaultDays, // what the extend dialog opens on, as on /loans
		"Stats":      a.borrowerStats(id),
	})
}

func (a *app) borrowerEdit(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	e, err := a.loadBorrower(id)
	if err != nil {
		a.notFoundScreen(w, r)
		return
	}
	a.fragment(w, r, "borrowers", "borrower_edit", e)
}

func (a *app) borrowerRow(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	e, err := a.loadBorrower(id)
	if err != nil {
		a.notFoundScreen(w, r)
		return
	}
	a.fragment(w, r, "borrowers", "borrower_row", e)
}

func (a *app) borrowerUpdate(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	if err := r.ParseForm(); err != nil {
		badRequest(w, r)
		return
	}
	firstName := strings.TrimSpace(r.FormValue("first_name"))
	lastName := strings.TrimSpace(r.FormValue("last_name"))
	class := strings.TrimSpace(r.FormValue("class"))
	kind := r.FormValue("kind")
	if kind != "teacher" {
		kind = "student"
	}
	// From the list, the row swaps itself back; from the borrower's own page,
	// the form posts backField and gets a redirect.
	back := r.FormValue(backField)

	if firstName == "" || lastName == "" {
		if back != "" {
			// The form requires both fields; a post without them saves nothing.
			http.Redirect(w, r, sanitizeNext(back), http.StatusSeeOther)
			return
		}
		// Stay in edit mode with the values typed.
		e, _ := a.loadBorrower(id)
		e.FirstName, e.LastInitial, e.Kind = firstName, lastName, kind
		cl := class
		e.Class = cl
		a.fragment(w, r, "borrowers", "borrower_edit", e)
		return
	}
	if _, err := a.db.Exec(
		`UPDATE borrower SET first_name = ?, last_initial = ?, class = ?, kind = ? WHERE id = ?`,
		firstName, lastNameInitial(lastName), nullable(class), kind, id,
	); err != nil {
		log.Printf("borrower/update: %v", err)
		internalError(w, r)
		return
	}
	if back != "" {
		http.Redirect(w, r, sanitizeNext(back), http.StatusSeeOther)
		return
	}
	e, err := a.loadBorrower(id)
	if err != nil {
		a.notFoundScreen(w, r)
		return
	}
	a.fragment(w, r, "borrowers", "borrower_row", e)
}

// Removes a borrower from the active list without deleting their history.
func (a *app) borrowersDeactivate(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)

	// Refuse while they still have books: they would vanish from the list, their
	// loans would stay open, and their parent page would stop answering.
	var count int
	if err := a.db.QueryRow(
		`SELECT COUNT(*) FROM loan WHERE borrower_id = ? AND returned_on IS NULL`, id,
	).Scan(&count); err != nil {
		log.Printf("borrowers/deactivate (loans): %v", err)
		internalError(w, r)
		return
	}
	if count > 0 {
		e, _ := a.loadBorrower(id)
		a.borrowersFragment(w, r, trn(r, "borrowers.err_still_has_books", count, e.FirstName, e.LastInitial), false)
		return
	}

	// Only the active: deactivating again would restart the retention clock.
	if _, err := a.db.Exec(`UPDATE borrower SET active = 0, deactivated_on = date('now') WHERE id = ? AND active = 1`, id); err != nil {
		log.Printf("borrowers/deactivate: %v", err)
		internalError(w, r)
		return
	}
	a.borrowersFragment(w, r, "", false)
}

// A rollover mistake, or a pupil who comes back. Not the sentinel, which is
// not a person, nor an anonymised borrower, who has nothing left to bring back.
func (a *app) borrowerReactivate(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	if id == anonymousBorrowerID(a.db) {
		a.borrowersFragment(w, r, tr(r, "borrowers.err_reactivate_sentinel", tr(r, "borrower.anonymised_name")), true)
		return
	}
	e, err := a.loadBorrower(id)
	if errors.Is(err, sql.ErrNoRows) {
		a.notFoundScreen(w, r)
		return
	}
	if err != nil {
		log.Printf("borrowers/reactivate (load): %v", err)
		internalError(w, r)
		return
	}
	if e.Anonymised() {
		a.borrowersFragment(w, r, tr(r, "borrowers.err_reactivate_anonymised"), true)
		return
	}
	if _, err := a.db.Exec(`UPDATE borrower SET active = 1, deactivated_on = NULL WHERE id = ? AND active = 0`, id); err != nil {
		log.Printf("borrowers/reactivate: %v", err)
		internalError(w, r)
		return
	}
	a.borrowersFragment(w, r, "", true)
}

func (a *app) borrowersScreen(w http.ResponseWriter, r *http.Request) {
	f := readBrFilter(r)
	f.Q = "" // the search box starts empty; the list is everything the filters allow
	inactive, class := f.Inactive, f.Class
	borrowers, pg, err := a.pageBorrowers(f, readOffset(r))
	if err != nil {
		log.Printf("borrowers: %v", err)
		internalError(w, r)
		return
	}
	classes, err := a.listClasses(!inactive)
	if err != nil {
		log.Printf("borrowers (classes): %v", err)
		internalError(w, r)
		return
	}
	title := tr(r, "nav.borrowers")
	if inactive {
		title = tr(r, "borrowers.title_inactive")
	}
	if class != "" {
		title += " · " + class
	}
	extra := map[string]string{}
	if inactive {
		extra["inactive"] = "1"
	}
	a.render(w, r, "borrowers", map[string]any{
		"Title": title, "Borrowers": borrowers, "Page": pg,
		"Inactive": inactive, "Filter": f,
		"Classes": classes, "Class": class,
		"FilterAction": "/borrowers", "FilterExtra": extra,
	})
}

func (a *app) borrowersAdd(w http.ResponseWriter, r *http.Request) {
	firstName := strings.TrimSpace(r.FormValue("first_name"))
	lastName := strings.TrimSpace(r.FormValue("last_name"))
	class := strings.TrimSpace(r.FormValue("class"))
	kind := r.FormValue("kind")
	if kind != "teacher" {
		kind = "student"
	}
	if firstName == "" || lastName == "" {
		a.borrowersFragment(w, r, tr(r, "borrowers.err_name_required"), false)
		return
	}

	tx, err := a.db.Begin()
	if err != nil {
		log.Printf("borrowers/add (tx): %v", err)
		internalError(w, r)
		return
	}
	defer tx.Rollback()

	code, err := freshCardCode(tx)
	if err != nil {
		log.Printf("borrowers/add (card code): %v", err)
		internalError(w, r)
		return
	}
	if _, err := tx.Exec(
		`INSERT INTO borrower (first_name, last_initial, class, kind, card_code, active)
		 VALUES (?, ?, ?, ?, ?, 1)`,
		firstName, lastNameInitial(lastName), nullable(class), kind, code,
	); err != nil {
		log.Printf("borrowers/add (insert): %v", err)
		internalError(w, r)
		return
	}
	if err := tx.Commit(); err != nil {
		log.Printf("borrowers/add (commit): %v", err)
		internalError(w, r)
		return
	}
	a.borrowersFragment(w, r, "", false)
}

// inactive is the caller's decision, not the form's. The order and the page
// come off the screen (#br-sort, #br-page), so an action keeps both.
func (a *app) borrowersFragment(w http.ResponseWriter, r *http.Request, errMsg string, inactive bool) {
	f := readBrFilter(r)
	f.Inactive = inactive
	f.Q = ""              // these come from a button, not from the search box
	f.Class = urlClass(r) // nor is the class filter theirs to carry (urlClass)
	borrowers, pg, err := a.pageBorrowers(f, readOffset(r))
	if err != nil {
		log.Printf("borrowers (list): %v", err)
		internalError(w, r)
		return
	}
	a.fragment(w, r, "borrowers", "borrowers_list", map[string]any{
		"Borrowers": borrowers, "Error": errMsg, "Page": pg,
		"Inactive": f.Inactive, "Class": f.Class, "Filter": f,
	})
}

// Reads "first name, last name, class". Detects the separator and skips a header.
// A line without both names is skipped and counted: a full name typed in one
// cell would otherwise be stored whole as the first name.
func parseCSV(raw string) (rows []importRow, skipped int) {
	raw = strings.TrimSpace(strings.TrimPrefix(raw, "\ufeff")) // Excel's "CSV UTF-8" BOM
	if raw == "" {
		return nil, 0
	}
	firstLine := raw
	if i := strings.IndexAny(raw, "\r\n"); i >= 0 {
		firstLine = raw[:i]
	}
	// Tab is what cells pasted from a spreadsheet arrive as.
	comma, best := ',', strings.Count(firstLine, ",")
	for _, c := range []rune{';', '\t'} {
		if n := strings.Count(firstLine, string(c)); n > best {
			comma, best = c, n
		}
	}

	rd := csv.NewReader(strings.NewReader(raw))
	rd.Comma = comma
	rd.FieldsPerRecord = -1
	rd.TrimLeadingSpace = true
	records, err := rd.ReadAll()
	if err != nil {
		// Tolerant fallback: split by hand.
		records = nil
		for _, l := range strings.Split(raw, "\n") {
			records = append(records, strings.Split(l, string(comma)))
		}
	}

	for i, rec := range records {
		field := func(k int) string {
			if k < len(rec) {
				return strings.TrimSpace(rec[k])
			}
			return ""
		}
		firstName, lastName, class := field(0), field(1), field(2)
		if i == 0 && isHeader(firstName) {
			continue
		}
		if firstName == "" && lastName == "" {
			continue
		}
		if firstName == "" || lastName == "" {
			skipped++
			continue
		}
		// Minimisation at import time: only the last name initial is kept.
		rows = append(rows, importRow{FirstName: firstName, LastInitial: lastNameInitial(lastName), Class: class})
		if len(rows) >= 2000 {
			break
		}
	}
	return rows, skipped
}

func isHeader(firstColumn string) bool {
	switch strings.ToLower(strings.TrimSpace(firstColumn)) {
	case "prénom", "prenom", "first name", "voornaam", "firstname":
		return true
	}
	return false
}

func (a *app) borrowersImportScreen(w http.ResponseWriter, r *http.Request) {
	a.render(w, r, "import", map[string]any{"Title": tr(r, "import.heading_short")})
}

// importHeader is the row the empty sheet starts with, in the words the
// preview uses; isHeader must recognise its first cell in every language.
func importHeader(lang string) []string {
	return []string{T(lang, "borrowers.f_first_name"), T(lang, "borrowers.f_last_name"), T(lang, "loans.class")}
}

// borrowersImportTemplate hands out an empty sheet to fill in and paste back:
// cells copied from a spreadsheet arrive tab-separated, which parseCSV reads.
func (a *app) borrowersImportTemplate(w http.ResponseWriter, r *http.Request) {
	lang := requestLang(r)
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.xlsx"`, T(lang, "import.template_file")))
	if err := writeXLSX(w, sheet{
		Name:   T(lang, "import.template_sheet"),
		Header: importHeader(lang),
		Widths: []int{20, 20, 10},
	}); err != nil {
		log.Printf("import template (write): %v", err)
	}
}

// A workbook is a zip: the import reads text, and says so rather than
// previewing its bytes.
func isZip(raw string) bool {
	return strings.HasPrefix(raw, "PK\x03\x04")
}

func (a *app) borrowersImportPreview(w http.ResponseWriter, r *http.Request) {
	raw := readIncomingCSV(r)
	if isZip(raw) {
		a.fragment(w, r, "borrowers", "import_preview", map[string]any{"Error": tr(r, "import.err_workbook")})
		return
	}
	rows, skipped := parseCSV(raw)
	if len(rows) == 0 {
		a.fragment(w, r, "borrowers", "import_preview", map[string]any{
			"Error": tr(r, "import.err_no_usable_line"), "Skipped": skipped,
		})
		return
	}
	existing, err := a.existingBorrowers()
	if err != nil {
		log.Printf("import/preview (existing): %v", err)
		internalError(w, r)
		return
	}
	duplicateCount := markDuplicates(rows, existing)

	a.fragment(w, r, "borrowers", "import_preview", map[string]any{
		"Rows":           rows,
		"Raw":            raw,
		"DuplicateCount": duplicateCount,
		"NewCount":       len(rows) - duplicateCount,
		"Skipped":        skipped,
	})
}

func (a *app) borrowersImportConfirm(w http.ResponseWriter, r *http.Request) {
	rows, _ := parseCSV(r.FormValue("csv"))
	if len(rows) == 0 {
		a.fragment(w, r, "borrowers", "import_preview", map[string]any{"Error": tr(r, "import.err_empty")})
		return
	}

	// Checked again: the preview is not proof, the hidden text may have changed.
	existing, err := a.existingBorrowers()
	if err != nil {
		log.Printf("import/confirm (existing): %v", err)
		internalError(w, r)
		return
	}
	markDuplicates(rows, existing)

	force := r.FormValue("force") != ""
	toCreate := rows[:0:0]
	for _, l := range rows {
		if l.Duplicate && !force {
			continue
		}
		toCreate = append(toCreate, l)
	}
	if len(toCreate) == 0 {
		a.fragment(w, r, "borrowers", "import_preview", map[string]any{
			"Error": tr(r, "import.err_all_known"),
		})
		return
	}
	rows = toCreate

	tx, err := a.db.Begin()
	if err != nil {
		log.Printf("import/confirm (tx): %v", err)
		internalError(w, r)
		return
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(
		`INSERT INTO borrower (first_name, last_initial, class, kind, card_code, active) VALUES (?, ?, ?, 'student', ?, 1)`)
	if err != nil {
		log.Printf("import/confirm (prepare): %v", err)
		internalError(w, r)
		return
	}
	defer stmt.Close()

	for _, l := range rows {
		if l.FirstName == "" && l.LastInitial == "" {
			continue
		}
		// The import creates pupils; a teacher is added one at a time.
		code, err := freshCardCode(tx)
		if err != nil {
			log.Printf("import/confirm (card code): %v", err)
			internalError(w, r)
			return
		}
		if _, err := stmt.Exec(l.FirstName, l.LastInitial, nullable(l.Class), code); err != nil {
			log.Printf("import/confirm (insert): %v", err)
			internalError(w, r)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		log.Printf("import/confirm (commit): %v", err)
		internalError(w, r)
		return
	}
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", "/borrowers")
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, "/borrowers", http.StatusSeeOther)
}

// From the text field OR the uploaded file.
func readIncomingCSV(r *http.Request) string {
	if f, _, err := r.FormFile("file"); err == nil {
		defer f.Close()
		if b, err := io.ReadAll(io.LimitReader(f, 4<<20)); err == nil && len(b) > 0 {
			return decodeCSVBytes(b)
		}
	}
	return r.FormValue("csv")
}

// decodeCSVBytes reads a file as UTF-8, or as Windows-1252 when it is not
// valid UTF-8: what Excel on Windows writes for a plain "CSV" in French.
func decodeCSVBytes(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	var sb strings.Builder
	sb.Grow(len(b) + len(b)/4)
	for _, c := range b {
		if c >= 0x80 && c <= 0x9f {
			sb.WriteRune(cp1252[c-0x80])
		} else {
			sb.WriteRune(rune(c)) // the rest of Windows-1252 is Latin-1
		}
	}
	return sb.String()
}

// cp1252 is the 0x80–0x9F block of Windows-1252; its five unassigned bytes
// keep their Latin-1 code point, as browsers decode them.
var cp1252 = [32]rune{
	'€', 0x81, '‚', 'ƒ', '„', '…', '†', '‡', 'ˆ', '‰', 'Š', '‹', 'Œ', 0x8d, 'Ž', 0x8f,
	0x90, '‘', '’', '“', '”', '•', '–', '—', '˜', '™', 'š', '›', 'œ', 0x9d, 'ž', 'Ÿ',
}

// Card is one printable barcode card.
type Card struct{ FirstName, LastInitial, Class, Code string }

// listCards gathers the cards to print. The class goes through classFilter, as
// everything the dropdown drives does, so "-" selects the classless.
func (a *app) listCards(class string) ([]Card, error) {
	query := `SELECT first_name, last_initial, COALESCE(class, ''), card_code
	              FROM borrower WHERE active = 1 AND card_code IS NOT NULL`
	args := []any{}
	if value, filter := classFilter(class); filter {
		query += ` AND COALESCE(class, '') = ?`
		args = append(args, value)
	}
	query += ` ORDER BY class, last_initial, first_name`

	rows, err := a.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var cards []Card
	for rows.Next() {
		var c Card
		if err := rows.Scan(&c.FirstName, &c.LastInitial, &c.Class, &c.Code); err != nil {
			return nil, err
		}
		cards = append(cards, c)
	}
	return cards, rows.Err()
}

// Printable card sheet, optionally filtered by ?class=P3A.
func (a *app) borrowersCards(w http.ResponseWriter, r *http.Request) {
	class := strings.TrimSpace(r.URL.Query().Get("class"))
	cards, err := a.listCards(class)
	if err != nil {
		log.Printf("cards: %v", err)
		internalError(w, r)
		return
	}
	a.renderDoc(w, r, "cards", map[string]any{"Cards": cards, "Class": class})
}

// Year rollover.

// noClassLabel is the on-screen label for pupils whose class is NULL, and the
// value the form posts back; localised, since it is shown.
func noClassLabel() string {
	return T(instanceLang(), "borrower.no_class")
}

type rolloverPupil struct {
	ID          int64
	FirstName   string
	LastInitial string
	Class       string // "" = no class
	OutCount    int    // books still in hand
}

// What happens to a class at rollover.
type rolloverRule struct {
	Src     string // current class ("" = no class)
	Dst     string // new class ("" = unchanged)
	Leaving bool   // the pupils of this class are leaving the school
}

// Works on a SNAPSHOT, and must: applying "P1 -> P2" then "P2 -> P3" in the
// database would carry every pupil through every later rule.
func planRollover(pupils []rolloverPupil, rules []rolloverRule) (movedUp map[int64]string, leavers []int64) {
	bySrc := make(map[string]rolloverRule, len(rules))
	for _, r := range rules {
		bySrc[r.Src] = r
	}
	movedUp = make(map[int64]string)
	for _, e := range pupils {
		r, ok := bySrc[e.Class]
		switch {
		case !ok:
		case r.Leaving:
			// "Leaving" wins over any new class.
			leavers = append(leavers, e.ID)
		case r.Dst != "" && r.Dst != e.Class:
			movedUp[e.ID] = r.Dst
		}
	}
	return movedUp, leavers
}

// rolloverMerge is a class that receives pupils while some of its own stay
// in it: once merged, nothing tells the two groups apart.
type rolloverMerge struct {
	Into    string
	From    []string // the classes moving into it
	Staying int      // its own pupils, neither moved on nor leaving
}

// findMerges works on the same snapshot as planRollover. Two classes moving
// together into one that is empty or moving on is not a merge: both groups
// were decided in the same breath.
func findMerges(pupils []rolloverPupil, rules []rolloverRule) []rolloverMerge {
	bySrc := make(map[string]rolloverRule, len(rules))
	for _, r := range rules {
		bySrc[r.Src] = r
	}
	staying := make(map[string]int)
	for _, p := range pupils {
		r := bySrc[p.Class]
		if !r.Leaving && (r.Dst == "" || r.Dst == p.Class) {
			staying[p.Class]++
		}
	}
	var out []rolloverMerge
	index := make(map[string]int)
	for _, r := range rules {
		if r.Leaving || r.Dst == "" || r.Dst == r.Src || staying[r.Dst] == 0 {
			continue
		}
		i, seen := index[r.Dst]
		if !seen {
			i = len(out)
			index[r.Dst] = i
			out = append(out, rolloverMerge{Into: r.Dst, Staying: staying[r.Dst]})
		}
		src := r.Src
		if src == "" {
			src = noClassLabel()
		}
		out[i].From = append(out[i].From, src)
	}
	return out
}

// Sources lists the classes moving in, for the sentence that names them.
func (m rolloverMerge) Sources() string { return strings.Join(m.From, ", ") }

// mergeKey names a set of merges, so "apply anyway" confirms the merges the
// librarian was shown and not whatever the form holds by then.
func mergeKey(merges []rolloverMerge) string {
	names := make([]string, len(merges))
	for i, m := range merges {
		names[i] = m.Into + "<" + strings.Join(m.From, "+")
	}
	slices.Sort(names)
	return strings.Join(names, ",")
}

// nextClasses suggests each class's next year: its one number plus one, P3 to
// P4, P5B to P6B. The top of each series, the highest number between the same
// letters, gets none: P6 leaves and M3 goes to P1, which no arithmetic knows,
// and left empty the merge check asks about them. Nor does a name with no
// number or with two ("P5-6").
func nextClasses(classes []string) map[string]string {
	type parts struct {
		prefix, suffix string
		n, width       int
	}
	split := make(map[string]parts)
	top := make(map[[2]string]int)
	for _, c := range classes {
		loc := reDigits.FindAllStringIndex(c, -1)
		if len(loc) != 1 {
			continue
		}
		n, err := strconv.Atoi(c[loc[0][0]:loc[0][1]])
		if err != nil {
			continue
		}
		p := parts{prefix: c[:loc[0][0]], suffix: c[loc[0][1]:], n: n, width: loc[0][1] - loc[0][0]}
		split[c] = p
		key := [2]string{p.prefix, p.suffix}
		top[key] = max(top[key], n)
	}
	out := make(map[string]string)
	for c, p := range split {
		if p.n < top[[2]string{p.prefix, p.suffix}] {
			out[c] = p.prefix + fmt.Sprintf("%0*d", p.width, p.n+1) + p.suffix
		}
	}
	return out
}

var reDigits = regexp.MustCompile(`[0-9]+`)

// q is a.db or the open transaction, never a.db while one is open (openDB).
func pupilsSnapshot(q interface {
	Query(string, ...any) (*sql.Rows, error)
}) ([]rolloverPupil, error) {
	rows, err := q.Query(
		`SELECT br.id, br.first_name, br.last_initial, COALESCE(br.class, ''),
		        (SELECT COUNT(*) FROM loan l WHERE l.borrower_id = br.id AND l.returned_on IS NULL)
		   FROM borrower br WHERE br.active = 1 AND br.kind = 'student'
		  ORDER BY br.class, br.last_initial, br.first_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []rolloverPupil
	for rows.Next() {
		var e rolloverPupil
		if err := rows.Scan(&e.ID, &e.FirstName, &e.LastInitial, &e.Class, &e.OutCount); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Fingerprint of who is in which class. The form carries the one it was drawn
// from, so a reloaded or resubmitted rollover cannot move the school up twice.
func rolloverState(pupils []rolloverPupil) string {
	sorted := slices.Clone(pupils)
	slices.SortFunc(sorted, func(x, y rolloverPupil) int { return cmp.Compare(x.ID, y.ID) })
	h := sha256.New()
	for _, p := range sorted {
		fmt.Fprintf(h, "%d\x00%s\n", p.ID, p.Class)
	}
	return hex.EncodeToString(h.Sum(nil)[:12])
}

func (a *app) rolloverScreen(w http.ResponseWriter, r *http.Request) {
	a.rolloverForm(w, r, "", nil, nil)
}

// Each active class gets a new class, or its pupils marked as leaving. typed
// and merges are set when the form comes back refused for a merge: what the
// librarian filled in is drawn again, beside what it would have mixed.
func (a *app) rolloverForm(w http.ResponseWriter, r *http.Request, errMsg string, typed []rolloverRule, merges []rolloverMerge) {
	pupils, err := pupilsSnapshot(a.db)
	if err != nil {
		log.Printf("rollover (snapshot): %v", err)
		internalError(w, r)
		return
	}
	// Top class first: the leavers are settled before anyone moves into
	// their class, which is the order that cannot merge two years.
	rows, err := a.db.Query(
		`SELECT COALESCE(class, ?), COUNT(*)
		   FROM borrower WHERE active = 1 AND kind = 'student'
		  GROUP BY class ORDER BY class DESC`, noClassLabel())
	if err != nil {
		log.Printf("rollover: %v", err)
		internalError(w, r)
		return
	}
	defer rows.Close()
	type classInfo struct {
		Class   string
		Count   int
		Dst     string
		Leaving bool
	}
	bySrc := make(map[string]rolloverRule, len(typed))
	for _, t := range typed {
		src := t.Src
		if src == "" {
			src = noClassLabel()
		}
		bySrc[src] = t
	}
	var classes []classInfo
	for rows.Next() {
		var c classInfo
		if err := rows.Scan(&c.Class, &c.Count); err != nil {
			log.Printf("rollover (scan): %v", err)
			internalError(w, r)
			return
		}
		c.Dst, c.Leaving = bySrc[c.Class].Dst, bySrc[c.Class].Leaving
		classes = append(classes, c)
	}
	if err := rows.Err(); err != nil {
		log.Printf("rollover (rows): %v", err)
		internalError(w, r)
		return
	}
	if typed == nil {
		names := make([]string, len(classes))
		for i, c := range classes {
			names[i] = c.Class
		}
		next := nextClasses(names)
		for i := range classes {
			classes[i].Dst = next[classes[i].Class]
		}
	}
	a.render(w, r, "rollover", map[string]any{
		"Title": tr(r, "borrowers.rollover"), "Classes": classes,
		"State": rolloverState(pupils), "Error": errMsg,
		"Merges": merges, "MergeKey": mergeKey(merges),
	})
}

func (a *app) rolloverConfirm(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		badRequest(w, r)
		return
	}
	n, _ := strconv.Atoi(r.FormValue("n"))
	if n < 0 || n > 200 {
		n = 0
	}
	rules := make([]rolloverRule, 0, n)
	for i := 0; i < n; i++ {
		src := r.FormValue(fmt.Sprintf("src_%d", i))
		if src == noClassLabel() {
			src = ""
		}
		rules = append(rules, rolloverRule{
			Src:     src,
			Dst:     strings.TrimSpace(r.FormValue(fmt.Sprintf("dst_%d", i))),
			Leaving: r.FormValue(fmt.Sprintf("out_%d", i)) != "",
		})
	}

	tx, err := a.db.Begin()
	if err != nil {
		log.Printf("rollover/confirm (tx): %v", err)
		internalError(w, r)
		return
	}
	defer tx.Rollback()

	// Read inside the transaction, so two submissions cannot both pass the check.
	pupils, err := pupilsSnapshot(tx)
	if err != nil {
		log.Printf("rollover/confirm (snapshot): %v", err)
		internalError(w, r)
		return
	}
	if r.FormValue("state") != rolloverState(pupils) {
		tx.Rollback()
		a.rolloverForm(w, r, tr(r, "rollover.err_changed"), nil, nil)
		return
	}
	// A merge goes through only once confirmed, and only the one shown.
	if merges := findMerges(pupils, rules); len(merges) > 0 && r.FormValue("merge_ok") != mergeKey(merges) {
		tx.Rollback()
		a.rolloverForm(w, r, "", rules, merges)
		return
	}
	movedUp, leavers := planRollover(pupils, rules)

	// A leaver who still has books stays active, and is named: deactivating them
	// would hide them while their loans stay open.
	byID := make(map[int64]rolloverPupil, len(pupils))
	for _, e := range pupils {
		byID[e.ID] = e
	}
	var toDeactivate []int64
	var kept []string
	for _, id := range leavers {
		if e := byID[id]; e.OutCount > 0 {
			kept = append(kept, trn(r, "rollover.kept_entry", e.OutCount, e.FirstName, e.LastInitial, e.Class))
			continue
		}
		toDeactivate = append(toDeactivate, id)
	}

	// Pupil by pupil: each is touched once, whatever the order of the rules.
	for id, class := range movedUp {
		if _, err := tx.Exec(`UPDATE borrower SET class = ? WHERE id = ?`, class, id); err != nil {
			log.Printf("rollover/confirm (move-up): %v", err)
			internalError(w, r)
			return
		}
	}
	for _, id := range toDeactivate {
		// Deactivate, never delete: anonymity and rotation statistics are handled
		// separately (GDPR).
		if _, err := tx.Exec(`UPDATE borrower SET active = 0, deactivated_on = date('now') WHERE id = ?`, id); err != nil {
			log.Printf("rollover/confirm (leaver): %v", err)
			internalError(w, r)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		log.Printf("rollover/confirm (commit): %v", err)
		internalError(w, r)
		return
	}

	a.render(w, r, "rollover", map[string]any{
		"Title":   tr(r, "borrowers.rollover"),
		"Classes": []any{},
		"Done":    true,
		"Summary": tr(r, "rollover.summary",
			trn(r, "rollover.moved", len(movedUp)),
			trn(r, "rollover.deactivated", len(toDeactivate))),
		"Kept": kept,
	})
}
