package main

import (
	"database/sql"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Cataloguing: scan the ISBN, fetch the metadata (enrichment.go), confirm on an
// editable screen, create N copies with their internal code (VOL204517).
//
// Manual entry is always one click away: 10-15% of a small collection comes
// back unresolved, and that is not an exception path.

// catalogueForm is a book being catalogued, rendered by both the cataloguing
// screen's form and the desk's express one. Exists, Current and Copies are the
// cataloguing screen's alone.
type catalogueForm struct {
	N       Record
	Found   bool // a catalogue had the book
	Silent  bool // a catalogue could not be asked: nothing was learnt
	Exists  bool // the work (isbn13) is already in the database
	Current int  // copies already present for this title
	Copies  int  // number of copies to create (default 1)
	Error   string

	// Batch is the form shown in the batch screen's panel: it saves through
	// /catalogue/batch/save and Location carries that screen's shelf.
	Batch    bool
	Location string
}

func (a *app) catalogueScreen(w http.ResponseWriter, r *http.Request) {
	a.render(w, r, "catalogue", map[string]any{"Title": tr(r, "nav.catalogue")})
}

// A blank form, for a book without an ISBN. With ?isbn= it starts from that
// ISBN (or from the work, when it is already catalogued): the batch screen
// fills in its set-aside books this way.

func (a *app) catalogueManual(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := catalogueForm{N: Record{Source: sourceManual, Language: "fr"}, Copies: 1}
	if isbn := strings.TrimSpace(q.Get("isbn")); isbn != "" {
		if i13, i10, err := ISBNForms(isbn); err != nil {
			f.Error = tr(r, "catalogue.err_bad_isbn")
		} else if known, ok := a.existingBook(i13, i10); ok {
			f = known
		} else {
			f.N.ISBN13, f.N.ISBN10 = i13, i10
		}
	}
	if n, _ := strconv.Atoi(q.Get("copies")); n > 1 && n <= 100 {
		f.Copies = n
	}
	f.Batch = q.Get("batch") == "1"
	f.Location = q.Get("location")
	a.fragment(w, r, "catalogue", "catalogue_form", f)
}

// Prefills the confirmation screen from an already catalogued work, to which
// copies are then added.

func (a *app) existingBook(i13, i10 string) (catalogueForm, bool) {
	f := catalogueForm{Copies: 1}
	var id int64
	var subtitle, authors, publisher, language *string
	var year *int
	if err := a.db.QueryRow(
		`SELECT id, title, subtitle, authors, publisher, year, language FROM book WHERE isbn13 = ?`, i13,
	).Scan(&id, &f.N.Title, &subtitle, &authors, &publisher, &year, &language); err != nil {
		return catalogueForm{Copies: 1}, false
	}
	f.Exists = true
	f.N.ISBN13, f.N.ISBN10 = i13, i10
	f.N.Subtitle = deref(subtitle)
	f.N.Authors = deref(authors)
	f.N.Publisher = deref(publisher)
	f.N.Language = deref(language)
	if year != nil {
		f.N.Year = *year
	}
	f.N.Source = sourceLocal
	a.db.QueryRow(`SELECT COUNT(*) FROM copy WHERE book_id = ?`, id).Scan(&f.Current)
	return f, true
}

// Streaming search: one "step" event per catalogue queried, then a "result"
// carrying the confirmation form as HTML.

func (a *app) catalogueSearchStream(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // disables nginx buffering
	flusher, _ := w.(http.Flusher)
	// No write timeout and no overall budget: the stream spans several
	// catalogues and shows its progress meanwhile.
	if err := http.NewResponseController(w).SetWriteDeadline(time.Time{}); err != nil {
		log.Printf("stream (write deadline): %v", err)
	}

	sse := func(event, payload string) {
		var b strings.Builder
		b.WriteString("event: ")
		b.WriteString(event)
		b.WriteByte('\n')
		for _, line := range strings.Split(payload, "\n") {
			b.WriteString("data: ")
			b.WriteString(line)
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
		io.WriteString(w, b.String())
		if flusher != nil {
			flusher.Flush()
		}
	}
	sendResult := func(f catalogueForm) {
		html, err := a.fragmentString(r, "catalogue", "catalogue_form", f)
		if err != nil {
			log.Printf("stream (render): %v", err)
			sse("result", `<p class="err">`+tr(r, "catalogue.sse_internal_error")+`</p>`)
			return
		}
		sse("result", html)
	}

	i13, i10, err := ISBNForms(r.URL.Query().Get("isbn"))
	if err != nil {
		sse("step", tr(r, "catalogue.sse_bad_isbn"))
		sendResult(catalogueForm{N: Record{Source: sourceManual, Language: "fr"}, Copies: 1,
			Error: tr(r, "catalogue.err_bad_isbn")})
		return
	}
	if f, ok := a.existingBook(i13, i10); ok {
		sse("step", tr(r, "catalogue.sse_already_known"))
		sendResult(f)
		return
	}

	// The desk's chain and memory. Typing an ISBN is an explicit request to go
	// and look, so the memory is dropped first, as "Enrich from the ISBN" does.
	catalogueMemo.forgetRecord(i13)
	n, err := enrichReported(r.Context(), i13, i10, enrichLog{
		trying: func(s enrichSource) {
			sse("step", tr(r, "catalogue.sse_searching", tr(r, s.key)))
		},
		// A catalogue that did not answer is said, not folded into "no record
		// found": it is the likeliest reason the desk could not find a book.
		answered: func(s enrichSource, n *Record, err error) {
			switch {
			case err != nil:
				sse("step", tr(r, "catalogue.sse_no_answer", tr(r, s.key)))
			case n != nil && n.Title != "":
				sse("step", tr(r, "catalogue.sse_found", tr(r, s.key))+" ✓")
			}
		},
	})
	if r.Context().Err() != nil {
		return // the tab was closed
	}
	if n != nil {
		sendResult(catalogueForm{N: *n, Found: true, Copies: 1})
		return
	}
	form := catalogueForm{N: Record{ISBN13: i13, ISBN10: i10, Source: sourceManual, Language: "fr"}, Copies: 1}
	if err != nil {
		// Not "no record found": that the book does not exist is what we do not know.
		form.Silent = true
	} else {
		sse("step", tr(r, "catalogue.sse_not_found"))
	}
	sendResult(form)
}

// createCopies reuses the work when the ISBN already exists. Call inside a
// transaction. Shared with express cataloguing from the lending screen.

func (a *app) createCopies(tx *sql.Tx, n Record, count int, location string) ([]string, error) {
	if count < 1 {
		count = 1
	}
	var workID int64
	reused := false
	if strings.TrimSpace(n.ISBN13) != "" {
		if err := tx.QueryRow(`SELECT id FROM book WHERE isbn13 = ?`, n.ISBN13).Scan(&workID); err == nil {
			reused = true
		}
	}
	if !reused {
		payload := n.Payload
		if len(payload) > 100000 {
			payload = payload[:100000]
		}
		res, err := tx.Exec(
			`INSERT INTO book (isbn13, isbn10, title, subtitle, authors, publisher, year, language,
			                      source_metadata, source_url, source_payload, source_date)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))`,
			nullable(n.ISBN13), nullable(n.ISBN10), n.Title, nullable(n.Subtitle), nullable(n.Authors),
			nullable(n.Publisher), nullableInt(n.Year), nullable(n.Language), nullable(n.Source), nullable(n.URL), nullable(payload),
		)
		if err != nil {
			return nil, err
		}
		workID, _ = res.LastInsertId()
	}

	loc := nullable(location)
	var codes []string
	for k := 1; k <= count; k++ {
		code, err := freshCopyCode(tx)
		if err != nil {
			return nil, err
		}
		if _, err := tx.Exec(
			`INSERT INTO copy (book_id, code, location, status, acquired_on)
			 VALUES (?, ?, ?, 'available', date('now'))`, workID, code, loc); err != nil {
			return nil, err
		}
		codes = append(codes, code)
	}
	return codes, nil
}

// Used when every copy is out and one more has to be lent.

func (a *app) addCopy(workID int64) (code string, exID int64, err error) {
	tx, err := a.db.Begin()
	if err != nil {
		return "", 0, err
	}
	defer tx.Rollback()

	code, err = freshCopyCode(tx)
	if err != nil {
		return "", 0, err
	}
	res, err := tx.Exec(
		`INSERT INTO copy (book_id, code, status, acquired_on)
		 VALUES (?, ?, 'available', date('now'))`, workID, code)
	if err != nil {
		return "", 0, err
	}
	exID, _ = res.LastInsertId()
	return code, exID, tx.Commit()
}

// recordFromForm builds a Record from the cataloguing form's hidden and typed
// fields. The ISBN pair, title and year are parsed by the caller; the rest are
// read here, in one place, so the copy shown again on an error and the copy that
// gets saved can never drift (they once did: the error redraw dropped the URL).
func recordFromForm(r *http.Request, i13, i10, title string, year int) Record {
	return Record{
		ISBN13:    i13,
		ISBN10:    i10,
		Title:     title,
		Subtitle:  r.FormValue("subtitle"),
		Authors:   r.FormValue("authors"),
		Publisher: r.FormValue("publisher"),
		Year:      year,
		Language:  r.FormValue("language"),
		Source:    r.FormValue("source_metadata"),
		URL:       r.FormValue("source_url"),
		Payload:   r.FormValue("source_payload"),
	}
}

func (a *app) catalogueSave(w http.ResponseWriter, r *http.Request) {
	a.saveCatalogued(w, r, false)
}

// saveCatalogued is the confirm form's save; batch answers with a row for the
// batch screen's list instead of the success screen.
func (a *app) saveCatalogued(w http.ResponseWriter, r *http.Request, batch bool) {
	if err := r.ParseForm(); err != nil {
		badRequest(w, r)
		return
	}

	title := strings.TrimSpace(r.FormValue("title"))
	i13, i10, isbnErr := FormISBNs(r.FormValue("isbn13"), r.FormValue("isbn10"))
	year, _ := strconv.Atoi(strings.TrimSpace(r.FormValue("year")))
	count, _ := strconv.Atoi(strings.TrimSpace(r.FormValue("copies")))
	if count < 1 {
		count = 1
	}
	if count > 100 {
		count = 100
	}

	n := recordFromForm(r, i13, i10, title, year)
	redisplay := func(msg string) {
		a.fragment(w, r, "catalogue", "catalogue_form", catalogueForm{
			N: n, Copies: count, Error: msg, Batch: batch, Location: r.FormValue("location"),
		})
	}
	if isbnErr != nil {
		redisplay(tr(r, "catalogue.err_bad_isbn"))
		return
	}
	if title == "" {
		redisplay(tr(r, "common.err_title_required"))
		return
	}

	tx, err := a.db.Begin()
	if err != nil {
		log.Printf("catalogue/save (tx): %v", err)
		internalError(w, r)
		return
	}
	defer tx.Rollback()

	codes, err := a.createCopies(tx, n, count, r.FormValue("location"))
	if err != nil {
		log.Printf("catalogue/save: %v", err)
		internalError(w, r)
		return
	}
	if err := tx.Commit(); err != nil {
		log.Printf("catalogue/save (commit): %v", err)
		internalError(w, r)
		return
	}

	if batch {
		a.fragment(w, r, "batch", "batch_saved", batchRow{Kind: "ok", Title: title, ISBN: i13, Codes: codes})
		return
	}
	a.fragment(w, r, "catalogue", "catalogue_success", map[string]any{
		"Title":   title,
		"Codes":   codes,
		"HasISBN": i13 != "",
	})
}

// Source values, as written to book.source_metadata: stored data, English and
// stable; sourceWording translates them for display.
const (
	sourceManual = "manual" // typed in by hand, no catalogue involved
	sourceLocal  = "local"  // the work was already in our own catalogue
)

// sourceWording turns a stored provenance into what the screen shows, the way
// statusWording does for a copy's status.
func sourceWording(lang, source string) string {
	switch source {
	case "bnf", "unicat", "google", "openlibrary", sourceManual, sourceLocal:
		return T(lang, "source."+source)
	default:
		return source // unexpected value: show it as is rather than lie
	}
}

// nullable maps an empty string to NULL.

func nullable(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}

func nullableInt(n int) any {
	if n <= 0 {
		return nil
	}
	return n
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
