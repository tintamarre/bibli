package main

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// With the ARK on the page, the BnF search link (rate-limited per IP) is not offered.
func TestComputeLinksDropsTheBnFSearchWhenTheARKIsThere(t *testing.T) {
	f := &BookPage{
		ISBN13:    "9782070408504",
		Title:     "Le Petit Prince",
		SourceURL: "https://catalogue.bnf.fr/ark:/12148/cb37007958t",
	}
	f.computeLinks()

	if f.RecordLink != f.SourceURL {
		t.Errorf("RecordLink = %q, want the permanent record", f.RecordLink)
	}
	if f.BnFLink != "" {
		t.Errorf("BnF search link offered alongside the ARK: %q", f.BnFLink)
	}
	// The others still are: they are not the BnF.
	for _, l := range []struct{ name, url string }{
		{"GoogleBooksLink", f.GoogleBooksLink},
		{"UniCatLink", f.UniCatLink},
		{"GoogleLink", f.GoogleLink},
	} {
		if !strings.Contains(l.url, "9782070408504") {
			t.Errorf("%s = %q, want a link carrying the ISBN", l.name, l.url)
		}
	}
}

// A work found elsewhere — or entered by hand with an ISBN — gets the BnF search
// link, which is the only way in without an ARK.
func TestComputeLinksOffersTheBnFSearchWithoutAnARK(t *testing.T) {
	f := &BookPage{ISBN13: "9782211037495", SourceURL: "https://openlibrary.org/books/OL1M"}
	f.computeLinks()
	if !strings.Contains(f.BnFLink, "9782211037495") {
		t.Errorf("BnFLink = %q, want a search on the ISBN", f.BnFLink)
	}
	if f.RecordLink != "https://openlibrary.org/books/OL1M" {
		t.Errorf("RecordLink = %q", f.RecordLink)
	}
}

// A book with no ISBN — a picture book, a home-made album — still gets a way
// to look it up, on its title and authors.
func TestComputeLinksFallsBackToTheTitle(t *testing.T) {
	f := &BookPage{Title: "Album maternelle", Authors: "Anonyme"}
	f.computeLinks()

	if f.BnFLink != "" || f.UniCatLink != "" {
		t.Error("an ISBN search was offered for a book with no ISBN")
	}
	for _, l := range []string{f.GoogleLink, f.GoogleBooksLink} {
		if !strings.Contains(l, "Album+maternelle") || !strings.Contains(l, "Anonyme") {
			t.Errorf("link = %q, want the title and authors escaped into it", l)
		}
	}
}

// A book with neither ISBN nor title has nothing to search on, and must not
// offer an empty query.
func TestComputeLinksOnAnEmptyBook(t *testing.T) {
	f := &BookPage{}
	f.computeLinks()
	if f.RecordLink != "" || f.BnFLink != "" || f.GoogleLink != "" || f.GoogleBooksLink != "" || f.UniCatLink != "" {
		t.Errorf("links offered on an empty book: %+v", f)
	}
}

// --- The page itself ------------------------------------------------------

func TestLoadBookPage(t *testing.T) {
	a := testApp(t)
	f, err := a.loadBookPage(1) // Le Petit Prince, two copies, one out to Tom
	if err != nil {
		t.Fatalf("loadBookPage: %v", err)
	}

	if f.Title != "Le Petit Prince" || f.ISBN13 != "9782070408504" || f.ISBN10 != "2070408507" {
		t.Errorf("metadata: %+v", f)
	}
	if f.TotalCount != 2 || f.OutCount != 1 || f.AvailableCount != 1 {
		t.Errorf("counts: total %d, out %d, available %d — want 2, 1, 1",
			f.TotalCount, f.OutCount, f.AvailableCount)
	}
	if f.LoansTotal != 1 {
		t.Errorf("loans in total: %d, want 1", f.LoansTotal)
	}
	if len(f.History) != 1 || f.History[0].FirstName != "Tom" || !f.History[0].Out {
		t.Errorf("history: %+v", f.History)
	}
	// The copy that is out names who has it, so the page answers "where is it?".
	var out *CopyRow
	for i := range f.Copies {
		if f.Copies[i].Out {
			out = &f.Copies[i]
		}
	}
	if out == nil {
		t.Fatal("no copy marked as out")
	}
	if out.BorrowerFirstName != "Tom" || out.BorrowerGroup != "P3" {
		t.Errorf("the copy that is out does not name its borrower: %+v", out)
	}
}

// A withdrawn copy is not counted as available.
func TestLoadBookPageIgnoresWithdrawnCopies(t *testing.T) {
	a := testApp(t)
	if _, err := a.updateCopy(2, "withdrawn", nil); err != nil {
		t.Fatalf("withdrawing: %v", err)
	}

	f, err := a.loadBookPage(1)
	if err != nil {
		t.Fatalf("loadBookPage: %v", err)
	}
	if f.TotalCount != 1 || f.AvailableCount != 0 {
		t.Errorf("total %d, available %d — want 1 and 0", f.TotalCount, f.AvailableCount)
	}
	// It is still listed, so it can be reinstated.
	if len(f.Copies) != 2 {
		t.Errorf("%d copies listed, want 2: a withdrawn copy stays visible", len(f.Copies))
	}
}

// A damaged copy is still on the shelf but not lendable.
func TestLoadBookPageCountsADamagedCopyAsUnavailable(t *testing.T) {
	a := testApp(t)
	if _, err := a.updateCopy(2, "damaged", nil); err != nil {
		t.Fatalf("marking damaged: %v", err)
	}
	f, err := a.loadBookPage(1)
	if err != nil {
		t.Fatalf("loadBookPage: %v", err)
	}
	if f.TotalCount != 2 {
		t.Errorf("total = %d, want 2: a damaged copy is still in the collection", f.TotalCount)
	}
	if f.AvailableCount != 0 {
		t.Errorf("available = %d, want 0: a damaged copy is not lendable", f.AvailableCount)
	}
}

// The copies entered today, which are the ones waiting for a sticker.
func TestLoadBookPageMarksTodaysCopies(t *testing.T) {
	a := testApp(t)

	// The fixture writes no created_at, so its copies default to this morning.
	// A shelf catalogued last term is what the question is asked against.
	if _, err := a.db.Exec(`UPDATE copy SET created_at = datetime('now','-60 days')`); err != nil {
		t.Fatal(err)
	}

	code, _, err := a.addCopy(1)
	if err != nil {
		t.Fatalf("adding a copy: %v", err)
	}
	f, err := a.loadBookPage(1)
	if err != nil {
		t.Fatalf("loadBookPage: %v", err)
	}
	if len(f.NewCodes) != 1 || f.NewCodes[0] != code {
		t.Errorf("NewCodes = %v, want just the copy added a moment ago (%s)", f.NewCodes, code)
	}
	for _, c := range f.Copies {
		if want := c.Code == code; c.New != want {
			t.Errorf("copy %s marked new = %v, want %v", c.Code, c.New, want)
		}
	}

	// Yesterday's is not today's, whatever time of day the page is opened.
	if _, err := a.db.Exec(
		`UPDATE copy SET created_at = datetime('now','-1 day') WHERE code = ?`, code); err != nil {
		t.Fatal(err)
	}
	f, err = a.loadBookPage(1)
	if err != nil {
		t.Fatalf("loadBookPage: %v", err)
	}
	if len(f.NewCodes) != 0 {
		t.Errorf("NewCodes = %v, want none: the sticker went on it yesterday", f.NewCodes)
	}
}

// The thumbnail URL carries a freshness token, so the browser can cache it for
// a week without missing a cover refreshed in the meantime.
func TestBookPageCarriesAFingerprint(t *testing.T) {
	a := testApp(t)
	f, err := a.loadBookPage(1)
	if err != nil {
		t.Fatalf("loadBookPage: %v", err)
	}
	before := f.Fingerprint
	if before == "" {
		t.Fatal("empty fingerprint: a refreshed cover would stay invisible")
	}
	for _, r := range before {
		if r < '0' || r > '9' {
			t.Fatalf("fingerprint %q holds %q, which does not belong in a URL", before, r)
		}
	}

	if _, err := a.db.Exec(
		`UPDATE book SET updated_at = datetime('now', '+1 hour') WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	f, _ = a.loadBookPage(1)
	if f.Fingerprint == before {
		t.Error("the fingerprint did not move when the work was edited")
	}
}

func TestLoadBookPageUnknownID(t *testing.T) {
	a := testApp(t)
	if _, err := a.loadBookPage(9999); err == nil {
		t.Error("an unknown work loaded without error")
	}
}

// --- Deleting a book ------------------------------------------------------

// A work catalogued by mistake is deleted with its copies.
func TestDeleteBookRemovesTheWorkAndItsCopies(t *testing.T) {
	a := testApp(t)
	// A work nobody has touched, with two copies, as cataloguing creates them.
	if _, err := a.db.Exec(
		`INSERT INTO book (id, title, language, source_metadata)
		      VALUES (300, 'Livre test', 'fr', 'manual');
		 INSERT INTO copy (id, book_id, code, status) VALUES (300, 300, 'VOL-X0001', 'available');
		 INSERT INTO copy (id, book_id, code, status) VALUES (301, 300, 'VOL-X0002', 'available')`); err != nil {
		t.Fatal(err)
	}

	page, err := a.loadBookPage(300)
	if err != nil {
		t.Fatal(err)
	}
	if !page.Deletable {
		t.Fatal("a work nobody has borrowed is not offered for deletion")
	}

	if err := a.deleteBook(300); err != nil {
		t.Fatalf("deleteBook: %v", err)
	}
	if n := count(t, a.db, `SELECT COUNT(*) FROM book WHERE id = 300`); n != 0 {
		t.Error("the work is still on file")
	}
	if n := count(t, a.db, `SELECT COUNT(*) FROM copy WHERE book_id = 300`); n != 0 {
		t.Errorf("%d copies left behind — copy.book_id is ON DELETE RESTRICT, so this cannot half-happen", n)
	}
}

// A work with any loan, open or returned, is never deleted: "no OPEN loan"
// is the obvious condition and the wrong one.
func TestDeleteBookRefusesAnythingEverBorrowed(t *testing.T) {
	a := testApp(t)

	// Book 1: copy 1 is out to Tom, an open loan.
	if err := a.deleteBook(1); !errors.Is(err, errBookHasLoans) {
		t.Errorf("a work with a copy out: %v, want errBookHasLoans", err)
	}

	// Book 3 holds copy 5, which nobody has ever borrowed.
	page, err := a.loadBookPage(3)
	if err != nil {
		t.Fatal(err)
	}
	if !page.Deletable {
		t.Fatal("fixture: book 3 has a history already — the case below proves nothing")
	}

	// Lend it and take it back: no open loan anywhere on the work, and it must
	// still refuse.
	lend(t, a, 5, 102, 30)
	if _, err := a.db.Exec(
		`UPDATE loan SET returned_on = date('now', '-20 days') WHERE copy_id = 5`); err != nil {
		t.Fatal(err)
	}
	if n := count(t, a.db, `SELECT COUNT(*) FROM loan WHERE copy_id = 5 AND returned_on IS NULL`); n != 0 {
		t.Fatalf("%d open loans on copy 5, want none — the case is not what it claims", n)
	}
	if err := a.deleteBook(3); !errors.Is(err, errBookHasLoans) {
		t.Errorf("a work borrowed and returned: %v, want errBookHasLoans", err)
	}

	// Refused means nothing moved.
	if n := count(t, a.db, `SELECT COUNT(*) FROM book WHERE id = 3`); n != 1 {
		t.Error("a refused deletion removed the work anyway")
	}
	if n := count(t, a.db, `SELECT COUNT(*) FROM copy WHERE book_id = 3`); n != 1 {
		t.Error("a refused deletion removed a copy anyway")
	}

	// And the page agrees with the write, or the button is offered for a
	// deletion that then fails.
	if page, err = a.loadBookPage(3); err != nil {
		t.Fatal(err)
	} else if page.Deletable {
		t.Error("the page offers to delete a work that deleteBook refuses")
	}
}

// The page names each bin a copy still in the collection sits in, once, and the
// unfiled as "".
func TestBookShelvesListsEachBinOnce(t *testing.T) {
	f := BookPage{Copies: []CopyRow{
		{Location: "Albums 3/5", Status: "available"},
		{Location: "Albums 3/5", Status: "damaged"},
		{Location: "réserve", Status: "lost"},
		{Location: "classe P3", Status: "withdrawn"},
		{Location: "", Status: "available"},
	}}
	var got []string
	for _, s := range f.Shelves() {
		got = append(got, fmt.Sprintf("%s=%d", s.Place, s.Copies))
	}
	if strings.Join(got, "|") != "Albums 3/5=2|=1" {
		t.Errorf("shelves %v, want the bin once with its two copies, then the unfiled; not the lost or withdrawn", got)
	}
	if n := f.ShelvedCopies(); n != 3 {
		t.Errorf("%d shelved copies, want 3", n)
	}
}

// The book page's copy row saves its location with its status; a post without
// the field leaves the location as it was.
func TestBookCopyRowSavesTheLocation(t *testing.T) {
	a, h := testHandler(t)
	c := signedIn(t, a)
	post := func(form url.Values) {
		t.Helper()
		r := httptest.NewRequest("POST", "/book/1/copy/1", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Sec-Fetch-Site", "same-origin")
		r.AddCookie(c)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusSeeOther {
			t.Fatalf("POST = %d, want 303", w.Code)
		}
	}
	location := func() string {
		t.Helper()
		var l sql.NullString
		if err := a.db.QueryRow(`SELECT location FROM copy WHERE id = 1`).Scan(&l); err != nil {
			t.Fatal(err)
		}
		return l.String
	}

	post(url.Values{"status": {"available"}, "location": {" Albums 8/10 "}})
	if got := location(); got != "Albums 8/10" {
		t.Errorf("location %q, want Albums 8/10", got)
	}
	post(url.Values{"status": {"available"}})
	if got := location(); got != "Albums 8/10" {
		t.Errorf("a post without the field changed the location to %q", got)
	}
}
