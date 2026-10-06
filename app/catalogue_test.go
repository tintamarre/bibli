package main

import (
	"html/template"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestNullable(t *testing.T) {
	for _, s := range []string{"", "   ", "\t\n"} {
		if v := nullable(s); v != nil {
			t.Errorf("nullable(%q) = %v, want nil", s, v)
		}
	}
	// The value is stored as typed: trimming happens in the handlers, and a
	// location like "bac albums" must not lose its spaces here.
	if v := nullable(" bac albums "); v != " bac albums " {
		t.Errorf("nullable = %v, want the string unchanged", v)
	}
	// A year is not known as 0, and an empty column reads better than a 0 that
	// looks like a date.
	for _, n := range []int{0, -1} {
		if v := nullableInt(n); v != nil {
			t.Errorf("nullableInt(%d) = %v, want nil", n, v)
		}
	}
	if v := nullableInt(1999); v != 1999 {
		t.Errorf("nullableInt(1999) = %v", v)
	}
}

func TestDeref(t *testing.T) {
	if got := deref(nil); got != "" {
		t.Errorf("deref(nil) = %q, want empty", got)
	}
	s := "Gallimard"
	if got := deref(&s); got != s {
		t.Errorf("deref = %q, want %q", got, s)
	}
}

// Codes carry no order; every code must be well formed, unique, and handed
// back so the labels can be printed.
func TestCreateCopiesDrawsUnorderedCodes(t *testing.T) {
	a := testApp(t)
	tx, err := a.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()

	codes, err := a.createCopies(tx, Record{
		ISBN13: "9782211201896", Title: "Chien bleu", Authors: "Nadja",
		Publisher: "École des loisirs", Year: 1989, Language: "fr", Source: "bnf",
	}, 3, "coin lecture")
	if err != nil {
		t.Fatalf("createCopies: %v", err)
	}
	if len(codes) != 3 {
		t.Fatalf("codes = %v, want 3", codes)
	}
	seen := make(map[string]bool, len(codes))
	for _, c := range codes {
		if !reCopyCode.MatchString(c) {
			t.Errorf("code %q is not VOL, five digits and a check digit", c)
		}
		if seen[c] {
			t.Errorf("the same code was handed out twice: %q", c)
		}
		seen[c] = true
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	if n := count(t, a.db, `SELECT COUNT(*) FROM book WHERE isbn13 = '9782211201896'`); n != 1 {
		t.Errorf("%d works, want 1", n)
	}
	for _, c := range codes {
		if n := count(t, a.db, `SELECT COUNT(*) FROM copy WHERE code = ?`, c); n != 1 {
			t.Errorf("copy %s: %d rows on file, want 1", c, n)
		}
	}
}

// A draw must avoid the codes already taken (the fixture's five copies).
func TestCopyCodesStayUniqueOverManyDraws(t *testing.T) {
	a := testApp(t)
	tx, err := a.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()

	codes, err := a.createCopies(tx, Record{Title: "Un fonds entier"}, 100, "")
	if err != nil {
		t.Fatalf("createCopies: %v", err)
	}
	seen := make(map[string]bool, len(codes))
	for _, c := range codes {
		if seen[c] {
			t.Fatalf("duplicate code %q inside one batch", c)
		}
		seen[c] = true
	}
	if len(seen) != 100 {
		t.Errorf("%d distinct codes for 100 copies", len(seen))
	}
}

// Cataloguing a book the library already has adds copies to the work rather
// than a second record: the ISBN is the unique key.
func TestCreateCopiesReusesTheWorkBehindAnISBN(t *testing.T) {
	a := testApp(t)
	tx, err := a.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()

	// 9782070408504 is Le Petit Prince, already catalogued with two copies.
	if _, err := a.createCopies(tx, Record{ISBN13: "9782070408504", Title: "Le Petit Prince (autre tirage)"}, 2, ""); err != nil {
		t.Fatalf("createCopies: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	if n := count(t, a.db, `SELECT COUNT(*) FROM book WHERE isbn13 = '9782070408504'`); n != 1 {
		t.Errorf("%d works under the same ISBN, want 1", n)
	}
	if n := count(t, a.db, `SELECT COUNT(*) FROM copy WHERE book_id = 1`); n != 4 {
		t.Errorf("%d copies of Le Petit Prince, want 4", n)
	}
	// The title on file is left alone: the copies join the work as it stands.
	var title string
	a.db.QueryRow(`SELECT title FROM book WHERE id = 1`).Scan(&title)
	if title != "Le Petit Prince" {
		t.Errorf("title overwritten: %q", title)
	}
}

// A book with no ISBN — a picture book, a home-made album — gets its own work
// every time: there is no key to match it on.
func TestCreateCopiesWithoutAnISBN(t *testing.T) {
	a := testApp(t)
	for i := 0; i < 2; i++ {
		tx, err := a.db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := a.createCopies(tx, Record{Title: "Album sans ISBN", Source: sourceManual}, 1, ""); err != nil {
			t.Fatalf("createCopies: %v", err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	if n := count(t, a.db, `SELECT COUNT(*) FROM book WHERE title = 'Album sans ISBN'`); n != 2 {
		t.Errorf("%d works, want 2: with no ISBN nothing says it is the same book", n)
	}
}

// A count of zero or less still makes one copy.
func TestCreateCopiesAlwaysMakesAtLeastOne(t *testing.T) {
	a := testApp(t)
	tx, err := a.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	codes, err := a.createCopies(tx, Record{Title: "Sans exemplaire"}, 0, "")
	if err != nil {
		t.Fatalf("createCopies: %v", err)
	}
	if len(codes) != 1 {
		t.Errorf("%d copies for count 0, want 1", len(codes))
	}
}

// The raw catalogue response is kept so enrichment can be replayed, but a
// runaway payload must not take the database with it.
func TestCreateCopiesTruncatesAnOversizedPayload(t *testing.T) {
	a := testApp(t)
	tx, err := a.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := a.createCopies(tx, Record{
		ISBN13: "9782211201896", Title: "Chien bleu",
		Payload: strings.Repeat("x", 250000),
	}, 1, ""); err != nil {
		t.Fatalf("createCopies: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var size int
	a.db.QueryRow(`SELECT LENGTH(source_payload) FROM book WHERE isbn13 = '9782211201896'`).Scan(&size)
	if size != 100000 {
		t.Errorf("payload kept: %d bytes, want 100000", size)
	}
}

// Used when every copy is out and one more has to be lent.
func TestAddCopy(t *testing.T) {
	a := testApp(t)
	code, id, err := a.addCopy(1)
	if err != nil {
		t.Fatalf("addCopy: %v", err)
	}
	if !reCopyCode.MatchString(code) {
		t.Errorf("code = %q, want VOL, five digits and a check digit", code)
	}
	if n := count(t, a.db, `SELECT COUNT(*) FROM copy WHERE code = ?`, code); n != 1 {
		t.Errorf("the new copy is not on file under %q", code)
	}
	var bookID int64
	var status string
	if err := a.db.QueryRow(`SELECT book_id, status FROM copy WHERE id = ?`, id).Scan(&bookID, &status); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if bookID != 1 || status != "available" {
		t.Errorf("copy: book %d, status %q — want book 1, available", bookID, status)
	}
}

// Scanning a book already on file prefills the confirmation screen, so copies
// are added rather than a second record created.
func TestExistingBookPrefillsTheForm(t *testing.T) {
	a := testApp(t)

	f, ok := a.existingBook("9782070408504", "2070408507")
	if !ok {
		t.Fatal("a catalogued work was not recognised")
	}
	if !f.Exists {
		t.Error("Exists is false for a work on file")
	}
	if f.N.Title != "Le Petit Prince" || f.N.Publisher != "Gallimard" || f.N.Year != 1999 {
		t.Errorf("prefill: %+v", f.N)
	}
	if f.N.Source != sourceLocal {
		t.Errorf("source = %q, want %q — the form says where the data came from", f.N.Source, sourceLocal)
	}
	if f.Current != 2 {
		t.Errorf("copies already present: %d, want 2", f.Current)
	}
	if f.Copies != 1 {
		t.Errorf("copies to create: %d, want 1", f.Copies)
	}

	// An unknown ISBN comes back as a blank form ready for enrichment.
	f, ok = a.existingBook("9782211201896", "2211201897")
	if ok || f.Exists {
		t.Errorf("unknown ISBN reported as known: %+v", f)
	}
	if f.Copies != 1 {
		t.Errorf("blank form: Copies = %d, want 1", f.Copies)
	}
}

// A work filed with no ISBN13 must not be matched by an empty scan.
func TestExistingBookIgnoresAWorkWithoutAnISBN(t *testing.T) {
	a := testApp(t)
	if _, ok := a.existingBook("", ""); ok {
		t.Error("an empty ISBN matched the work catalogued without one")
	}
}

// Saving rebuilds the ISBN pair from the posted fields, and refuses one that is
// not an ISBN rather than storing it.
func TestCatalogueSaveChecksTheISBN(t *testing.T) {
	a, h := testHandler(t)
	c := signedIn(t, a)
	post := func(i13, i10 string) *httptest.ResponseRecorder {
		t.Helper()
		form := url.Values{"title": {"Un livre"}, "isbn13": {i13}, "isbn10": {i10}, "copies": {"1"}}
		r := httptest.NewRequest("POST", "/catalogue/save", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Sec-Fetch-Site", "same-origin")
		r.AddCookie(c)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("POST = %d, want 200", w.Code)
		}
		return w
	}
	books := func() int {
		t.Helper()
		var n int
		if err := a.db.QueryRow(`SELECT COUNT(*) FROM book`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	before := books()
	w := post("../../static/app", "")
	if books() != before {
		t.Error("a book was saved under something that is not an ISBN")
	}
	if msg := tr(httptest.NewRequest("GET", "/", nil), "catalogue.err_bad_isbn"); !strings.Contains(w.Body.String(), template.HTMLEscapeString(msg)) {
		t.Errorf("no error shown: %s", w.Body.String())
	}

	post("", "2266000004")
	var i13, i10 string
	if err := a.db.QueryRow(`SELECT isbn13, isbn10 FROM book WHERE title = 'Un livre'`).Scan(&i13, &i10); err != nil {
		t.Fatal(err)
	}
	if i13 != "9782266000000" || i10 != "2266000004" {
		t.Errorf("stored %q / %q, want the pair rebuilt from the ISBN-10", i13, i10)
	}
}

// The form redrawn after a rejected save keeps the fields the save would store,
// the source URL included — it once dropped it, so a corrected re-save lost the
// catalogue link.
func TestCatalogueSaveRedisplayKeepsTheSourceURL(t *testing.T) {
	a, h := testHandler(t)
	c := signedIn(t, a)
	form := url.Values{
		"title":      {""}, // empty: forces the redisplay
		"isbn13":     {"9782070408504"},
		"source_url": {"https://catalogue.bnf.fr/ark:/12148/cb123"},
		"copies":     {"1"},
	}
	r := httptest.NewRequest("POST", "/catalogue/save", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	r.AddCookie(c)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("POST = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "https://catalogue.bnf.fr/ark:/12148/cb123") {
		t.Error("the redisplayed form dropped the source URL")
	}
}

// The shelf boxes suggest the shelves already in use, on every screen that
// renders the cataloguing form and on the batch screen. Each fills Locations
// on its own, so a lost one would only show as a box that stopped suggesting.
func TestShelvesInUseAreSuggested(t *testing.T) {
	a, h := testHandler(t)
	c := signedIn(t, a)

	get := func(path string, htmx bool) string {
		t.Helper()
		r := httptest.NewRequest("GET", path, nil)
		if htmx {
			r.Header.Set("HX-Request", "true")
		}
		r.AddCookie(c)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200", path, w.Code)
		}
		return w.Body.String()
	}

	for _, path := range []string{"/catalogue/batch", "/catalogue/manual"} {
		body := get(path, true)
		if !strings.Contains(body, `<datalist id="locations">`) {
			t.Errorf("%s: no shelf suggestions at all: %s", path, body)
		}
		for _, shelf := range []string{"bac albums", "classe P3", "coin lecture"} {
			if !strings.Contains(body, `<option value="`+shelf+`">`) {
				t.Errorf("%s: %q is in use but not suggested", path, shelf)
			}
		}
	}

	// The stream ends on the same form; an already catalogued ISBN answers
	// without asking a catalogue.
	if body := get("/catalogue/stream?isbn=9782070408504", false); !strings.Contains(body, `<option value="bac albums">`) {
		t.Errorf("the form the stream sends back suggests nothing: %s", body)
	}

	// Unfiled copies have no name, which is no suggestion.
	if _, err := a.db.Exec(`UPDATE copy SET location = NULL WHERE location = 'coin lecture'`); err != nil {
		t.Fatal(err)
	}
	body := get("/catalogue/manual", true)
	if strings.Contains(body, `<option value="">`) {
		t.Errorf("the unfiled copies were offered as a shelf: %s", body)
	}
	if strings.Contains(body, "coin lecture") {
		t.Errorf("an emptied shelf is still suggested: %s", body)
	}
}

// A refused save redraws the form with the shelf that was typed, and still
// suggests the others.
func TestCatalogueSaveRedisplayKeepsTheShelf(t *testing.T) {
	a, h := testHandler(t)
	form := url.Values{
		"title":    {""}, // empty: forces the redisplay
		"isbn13":   {"9782070408504"},
		"location": {"réserve du grenier"},
		"copies":   {"3"},
	}
	r := httptest.NewRequest("POST", "/catalogue/save", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	r.AddCookie(signedIn(t, a))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("POST = %d, want 200", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `value="réserve du grenier"`) {
		t.Errorf("the redisplayed form dropped the shelf that was typed: %s", body)
	}
	if !strings.Contains(body, `<option value="bac albums">`) {
		t.Errorf("the redisplayed form stopped suggesting the shelves in use: %s", body)
	}
}
