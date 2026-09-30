package main

import (
	"bytes"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// templateFuncs builds the template functions for ONE language, which is why
// {{T "key"}} takes no language argument.
func templateFuncs(lang string) template.FuncMap {
	return template.FuncMap{
		"T":                func(key string, args ...any) string { return T(lang, key, args...) },
		"Tn":               func(key string, n int, args ...any) string { return Tn(lang, key, n, args...) },
		"lang":             func() string { return lang },
		"statusWording":    func(status string) string { return statusWording(lang, status) },
		"onShelf":          onShelf,
		"sourceWording":    func(source string) string { return sourceWording(lang, source) },
		"relativeDate":     func(iso string) string { return relativeDate(lang, iso) },
		"duration":         func(days int) string { return humanDuration(lang, days) },
		"shortDate":        func(iso string) string { return shortDate(lang, iso) },
		"version":          displayVersion,
		"sourceURL":        func() string { return sourceURL },
		"school":           school,
		"theme":            instanceTheme,
		"themeColor":       func() string { return themeColor[instanceTheme()] },
		"googleKeyMissing": func() bool { return googleKey() == "" },
		"familyLinks":      familyLinks,
		"demo":             func() bool { return demoEvery > 0 },
		"asset":            asset,
		"jsTexts":          func() map[string]string { return jsTexts(lang) },
		"sortCol":          sortCol,
		"sortColDesc":      sortColDesc,
		"loanGauge":        loanGaugeToday,
		"loanActions":      loanActions,
		"navActive":        navActive,
		"titleHead":        titleHead,
		"titleTail":        titleTail,
	}
}

// navActive says whether a header entry is the screen shown; a screen owns its
// sub-pages. Not a bare prefix match, which would light /borrow on /borrowers.
func navActive(path, screen string) bool {
	return path == screen || strings.HasPrefix(path, screen+"/")
}

// sortState is what a sortable heading needs from its screen; /inventory and
// /borrowers both answer it, so one "sort_head" partial serves both.
type sortState interface {
	SortedBy(column string) bool
	Descending() bool
	SortForm() string
}

// sortColumn packs a sortable heading into one value: a {{template}} is handed
// one argument and cannot reach back to the page for the filter.
type sortColumn struct {
	Form       string // id of the form the heading posts through
	Key        string
	Label      string
	Sorted     bool
	Descending bool
	First      string // "asc" or "desc": which way the FIRST click sorts
}

func sortCol(f sortState, key, label string) sortColumn {
	return sortColumn{
		Form: f.SortForm(), Key: key, Label: label,
		Sorted: f.SortedBy(key), Descending: f.Descending(), First: "asc",
	}
}

// sortColDesc is for a column of counts, whose first click shows the largest.
func sortColDesc(f sortState, key, label string) sortColumn {
	c := sortCol(f, key, label)
	c.First = "desc"
	return c
}

// loanActionRow packs the two buttons on an open loan into one value, for the
// reason sortColumn gives.
type loanActionRow struct {
	LoanID      int64
	Back        string
	Title       string
	FirstName   string
	LastInitial string
	Days        int // what the extend dialog opens on (extendDefaultDays)
}

func loanActions(loanID int64, back, title, firstName, lastInitial string, days int) loanActionRow {
	return loanActionRow{
		LoanID: loanID, Back: back,
		Title: title, FirstName: firstName, LastInitial: lastInitial,
		Days: days,
	}
}

// placeholders is "?, ?, ?" for an IN list of n values, which stay parameters.
// Never call it with n < 1: SQL has no empty IN list.
func placeholders(n int) string { return strings.Repeat(", ?", n)[2:] }

// The two generic failure responses. The detail is logged by the caller.
func internalError(w http.ResponseWriter, r *http.Request) {
	http.Error(w, tr(r, "error.internal"), http.StatusInternalServerError)
}

func badRequest(w http.ResponseWriter, r *http.Request) {
	http.Error(w, tr(r, "error.bad_request"), http.StatusBadRequest)
}

// jsKeys lists the strings the JavaScript needs: app.js is cached a year under
// one URL for every language, so it carries no text. i18n_test.go checks
// the list against the .js files.
var jsKeys = []string{
	"loan.scan_to_start",
	"js.confirm_loan",
	"js.books.one", "js.books.other",
	"js.confirm_lost",
	"js.confirm_withdrawn",
	"js.link_copied",
	"js.copy_this_link",
	"js.search_interrupted",
	"js.reconnect",
	"js.session_expired",
	"js.aim_barcode",
	"js.cancel",
	"js.nothing_read",
	"js.read_failed",
	"js.camera_unavailable",
	"js.scan_with_camera",
}

// Rendered in a <script> by base.html, which serialises and escapes it as JSON.
func jsTexts(lang string) map[string]string {
	m := make(map[string]string, len(jsKeys))
	for _, key := range jsKeys {
		m[key] = T(lang, key)
	}
	return m
}

// Assets are immutable for a year, so every URL carries the fingerprint.
func asset(path string) string {
	return path + "?v=" + assetFingerprint
}

// pathID reads the {id} path segment. A malformed id yields 0, which matches no
// row, so the handler renders "not found" rather than erroring — the one place
// that intent lives.
func pathID(r *http.Request) int64 {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id
}

// Loan is one row of v_active_loan. The class can be NULL, hence *string.
type Loan struct {
	ID          int64
	BorrowerID  int64
	BookID      int64
	FirstName   string
	LastInitial string
	Class       *string
	Code        string
	Title       string
	LoanedOn    string
	DueOn       string
	DaysOverdue int
}

// A language whose set is missing falls back to French, the reference.
func (a *app) pageTemplate(r *http.Request, page string) (*template.Template, bool) {
	if tpl, ok := a.pages[requestLang(r)][page]; ok {
		return tpl, true
	}
	tpl, ok := a.pages[defaultLang][page]
	return tpl, ok
}

// render displays a full page: layout plus page content.
func (a *app) render(w http.ResponseWriter, r *http.Request, page string, data any) {
	tpl, ok := a.pageTemplate(r, page)
	if !ok {
		log.Printf("unknown page: %s", page)
		internalError(w, r)
		return
	}
	// For navActive, without threading the path through every handler.
	if m, ok := data.(map[string]any); ok {
		m["Path"] = r.URL.Path
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tpl.ExecuteTemplate(w, "base", data); err != nil {
		log.Printf("render %s: %v", page, err)
		internalError(w, r)
	}
}

// For handlers that wait on the network longer than WriteTimeout, which would
// otherwise cut the response off silently.
func extendWriteDeadline(w http.ResponseWriter, d time.Duration) {
	if err := http.NewResponseController(w).SetWriteDeadline(time.Now().Add(d)); err != nil {
		log.Printf("write deadline: %v", err)
	}
}

func (a *app) about(w http.ResponseWriter, r *http.Request) {
	a.render(w, r, "about", map[string]any{"Title": tr(r, "nav.about"), "B": buildMeta})
}

func (a *app) buildInfo(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Write(buildInfoJSON)
}

// A branded 404, also served for an invalid family token.
func (a *app) notFoundScreen(w http.ResponseWriter, r *http.Request) {
	tpl, ok := a.pageTemplate(r, "notfound")
	if !ok {
		http.Error(w, tr(r, "notfound.title"), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	if err := tpl.ExecuteTemplate(w, "document", map[string]any{}); err != nil {
		log.Printf("render 404 page: %v", err)
	}
}

// renderDoc renders a page defined as "document", without the shared layout:
// printouts that want neither header nor menu.
func (a *app) renderDoc(w http.ResponseWriter, r *http.Request, page string, data any) {
	tpl, ok := a.pageTemplate(r, page)
	if !ok {
		log.Printf("unknown page: %s", page)
		internalError(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tpl.ExecuteTemplate(w, "document", data); err != nil {
		log.Printf("render doc %s: %v", page, err)
		internalError(w, r)
	}
}

// fragmentString renders a named block into a string, for an SSE stream.
func (a *app) fragmentString(r *http.Request, page, block string, data any) (string, error) {
	tpl, ok := a.pageTemplate(r, page)
	if !ok {
		return "", fmt.Errorf("unknown page: %s", page)
	}
	var b bytes.Buffer
	if err := tpl.ExecuteTemplate(&b, block, data); err != nil {
		return "", err
	}
	return b.String(), nil
}

// fragment renders a named block without the layout: what HTMX responses return.
func (a *app) fragment(w http.ResponseWriter, r *http.Request, page, block string, data any) {
	tpl, ok := a.pageTemplate(r, page)
	if !ok {
		log.Printf("unknown page: %s", page)
		internalError(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tpl.ExecuteTemplate(w, block, data); err != nil {
		log.Printf("render fragment %s/%s: %v", page, block, err)
		internalError(w, r)
	}
}

func (a *app) home(w http.ResponseWriter, r *http.Request) {
	// Before signing in: a presentation and a sign-in button, with no data.
	if !a.validSession(r) {
		a.renderDoc(w, r, "landing", map[string]any{})
		return
	}

	var out, available, overdue int

	err := a.db.QueryRow(`SELECT COUNT(*) FROM v_active_loan`).Scan(&out)
	if err == nil {
		err = a.db.QueryRow(`SELECT COUNT(*) FROM v_available`).Scan(&available)
	}
	if err == nil {
		err = a.db.QueryRow(`SELECT COUNT(*) FROM v_overdue`).Scan(&overdue)
	}
	if err != nil {
		log.Printf("home: %v", err)
		internalError(w, r)
		return
	}

	a.render(w, r, "home", map[string]any{
		"Title":     tr(r, "home.title"),
		"Out":       out,
		"Available": available,
		"Overdue":   overdue,
	})
}

// Four readings of the loans, kept off / because / is opened at every loan and
// is a public path.
func (a *app) statsScreen(w http.ResponseWriter, r *http.Request) {
	d := a.readings(requestLang(r), todayISO())
	a.render(w, r, "stats", map[string]any{
		"Title":     tr(r, "stats.title"),
		"Year":      d.Year,
		"Months":    d.Months,
		"Top":       d.Top,
		"Idle":      d.Idle,
		"Retention": retentionYears(a.db),
	})
}

// Two filters that combine: ?overdue=1 and ?class=P3. One page for both views.
func (a *app) loans(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	overdueOnly := q.Get("overdue") == "1"
	class := strings.TrimSpace(q.Get("class"))
	classValue, byClass := classFilter(class)

	view, title := "v_active_loan", tr(r, "loans.title")
	if overdueOnly {
		view, title = "v_overdue", tr(r, "loans.title_overdue")
	}
	query := `
		SELECT loan_id, borrower_id, book_id, first_name, last_initial, class,
		       code, title, loaned_on, due_on, days_overdue
		FROM ` + view
	var args []any
	if byClass {
		query += ` WHERE COALESCE(class, '') = ?`
		args = append(args, classValue)
	}
	query += loansOrderForGrouping

	loans, err := a.listLoans(query, args...)
	if err != nil {
		log.Printf("loans: %v", err)
		internalError(w, r)
		return
	}
	// After the list: one connection, and its *sql.Rows must be closed.
	classes, err := a.loanClasses(view)
	if err != nil {
		log.Printf("loans (classes): %v", err)
		internalError(w, r)
		return
	}
	// The class dropdown is shared with /borrowers (filters.html).
	extra := map[string]string{}
	if overdueOnly {
		extra["overdue"] = "1"
	}
	a.render(w, r, "loans", map[string]any{
		"Title":        title,
		"Groups":       groupLoans(loans),
		"Total":        len(loans),
		"Classes":      classes,
		"Overdue":      overdueOnly,
		"Class":        class,
		"ByClass":      byClass,
		"URL":          r.URL.RequestURI(),
		"FilterAction": "/loans",
		"FilterExtra":  extra,
		"ExtendDays":   extendDefaultDays,
	})
}

// loanClasses counts the classes with a book out, for the tabs. The class
// filter is not applied: the tabs are how you leave it. view is one of a fixed
// pair, never from the request.
func (a *app) loanClasses(view string) ([]ClassCount, error) {
	rows, err := a.db.Query(`
		SELECT COALESCE(class, ''), COUNT(*) FROM ` + view + `
		 GROUP BY COALESCE(class, '')
		 ORDER BY CASE WHEN COALESCE(class, '') = '' THEN 1 ELSE 0 END, class`)
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

// loansOrderForGrouping is what makes groupLoans correct: it folds consecutive
// rows. borrower_id separates namesakes; the CASE puts the classless last.
const loansOrderForGrouping = ` ORDER BY CASE WHEN COALESCE(class, '') = '' THEN 1 ELSE 0 END,
	       class, last_initial, first_name, borrower_id, due_on`

// BorrowerLoans is one borrower and the books they have out; ClassLoans gathers
// the borrowers of one class.
type BorrowerLoans struct {
	BorrowerID   int64
	FirstName    string
	LastInitial  string
	Loans        []Loan
	OverdueCount int
}

type ClassLoans struct {
	Class     string // "" = no class: the teachers
	Borrowers []BorrowerLoans
	Count     int // books out in this class
}

// groupLoans folds the flat list into class → borrower → books, in one pass
// and no map so the order is stable. It relies on loansOrderForGrouping.
func groupLoans(loans []Loan) []ClassLoans {
	var out []ClassLoans
	for _, l := range loans {
		class := ""
		if l.Class != nil {
			class = *l.Class
		}
		if len(out) == 0 || out[len(out)-1].Class != class {
			out = append(out, ClassLoans{Class: class})
		}
		g := &out[len(out)-1]
		g.Count++

		if len(g.Borrowers) == 0 || g.Borrowers[len(g.Borrowers)-1].BorrowerID != l.BorrowerID {
			g.Borrowers = append(g.Borrowers, BorrowerLoans{
				BorrowerID:  l.BorrowerID,
				FirstName:   l.FirstName,
				LastInitial: l.LastInitial,
			})
		}
		b := &g.Borrowers[len(g.Borrowers)-1]
		b.Loans = append(b.Loans, l)
		if l.DaysOverdue > 0 {
			b.OverdueCount++
		}
	}
	return out
}

func (a *app) listLoans(query string, args ...any) ([]Loan, error) {
	rows, err := a.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var loans []Loan
	for rows.Next() {
		var p Loan
		if err := rows.Scan(
			&p.ID, &p.BorrowerID, &p.BookID, &p.FirstName, &p.LastInitial, &p.Class,
			&p.Code, &p.Title, &p.LoanedOn, &p.DueOn, &p.DaysOverdue,
		); err != nil {
			return nil, err
		}
		loans = append(loans, p)
	}
	return loans, rows.Err()
}
