package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func batchPost(t *testing.T, a *app, h http.Handler, path string, form url.Values) string {
	t.Helper()
	r := httptest.NewRequest("POST", path, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	r.AddCookie(signedIn(t, a))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("POST %s = %d, want 200", path, w.Code)
	}
	return w.Body.String()
}

func copiesOf(t *testing.T, a *app, isbn string) int {
	t.Helper()
	var n int
	if err := a.db.QueryRow(
		`SELECT COUNT(*) FROM copy c JOIN book b ON b.id = c.book_id WHERE b.isbn13 = ?`, isbn).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// A book a catalogue knows is saved at once; the one nobody knows is set aside,
// and nothing is written for it.
func TestBatchAddSavesWhatIsFoundAndSetsTheRestAside(t *testing.T) {
	a, h := testHandler(t)
	const found, missing = "9782211201896", "9782070612758"
	catalogueMemo.rememberRecord(found, &Record{ISBN13: found, Title: "Chien bleu", Source: "bnf"})
	catalogueMemo.rememberRecord(missing, nil)
	t.Cleanup(func() { catalogueMemo.forgetRecord(found); catalogueMemo.forgetRecord(missing) })

	body := batchPost(t, a, h, "/catalogue/batch/add", url.Values{"isbn": {found}, "location": {"bac 3"}})
	if !strings.Contains(body, `data-kind="ok"`) || !strings.Contains(body, "Chien bleu") {
		t.Errorf("a found book should come back as a saved row: %s", body)
	}
	if n := copiesOf(t, a, found); n != 1 {
		t.Errorf("%d copies saved, want 1", n)
	}
	var loc string
	a.db.QueryRow(`SELECT c.location FROM copy c JOIN book b ON b.id = c.book_id WHERE b.isbn13 = ?`, found).Scan(&loc)
	if loc != "bac 3" {
		t.Errorf("location = %q, want the screen's shelf", loc)
	}

	body = batchPost(t, a, h, "/catalogue/batch/add", url.Values{"isbn": {missing}})
	if !strings.Contains(body, `data-kind="aside"`) || !strings.Contains(body, missing) {
		t.Errorf("an unknown book should be set aside: %s", body)
	}
	if n := copiesOf(t, a, missing); n != 0 {
		t.Errorf("%d copies saved for a book set aside", n)
	}

	body = batchPost(t, a, h, "/catalogue/batch/add", url.Values{"isbn": {"9782211201897"}})
	if !strings.Contains(body, `data-kind="aside"`) {
		t.Errorf("a bad check digit should be set aside: %s", body)
	}
}

// A book already in the catalogue is left alone, unless the screen says it was
// scanned earlier in the session: then it is one more copy.
func TestBatchAddAgainAddsACopy(t *testing.T) {
	a, h := testHandler(t)
	const isbn = "9782211201896"
	catalogueMemo.rememberRecord(isbn, &Record{ISBN13: isbn, Title: "Chien bleu"})
	t.Cleanup(func() { catalogueMemo.forgetRecord(isbn) })

	batchPost(t, a, h, "/catalogue/batch/add", url.Values{"isbn": {isbn}})
	body := batchPost(t, a, h, "/catalogue/batch/add", url.Values{"isbn": {isbn}})
	if !strings.Contains(body, `data-kind="aside"`) || copiesOf(t, a, isbn) != 1 {
		t.Errorf("a known book scanned without the session flag should be set aside: %s", body)
	}
	body = batchPost(t, a, h, "/catalogue/batch/add", url.Values{"isbn": {isbn}, "again": {"1"}})
	if !strings.Contains(body, `data-kind="ok"`) || copiesOf(t, a, isbn) != 2 {
		t.Errorf("a book scanned again should gain a copy: %s", body)
	}
}

// The set-aside book is filled in through the panel's form, which answers with
// a row rather than the success screen.
func TestBatchSaveAnswersWithARow(t *testing.T) {
	a, h := testHandler(t)
	body := batchPost(t, a, h, "/catalogue/batch/save", url.Values{
		"title": {"Un livre"}, "isbn13": {"9782070612758"}, "copies": {"2"}, "location": {"bac 3"},
	})
	if !strings.Contains(body, "data-batch-saved") || strings.Count(body, `class="code"`) != 2 {
		t.Errorf("want a row with the two codes: %s", body)
	}
	body = batchPost(t, a, h, "/catalogue/batch/save", url.Values{"isbn13": {"9782070612758"}, "copies": {"1"}})
	if strings.Contains(body, "data-batch-saved") || !strings.Contains(body, "/catalogue/batch/save") {
		t.Errorf("a refused save should redraw the batch form: %s", body)
	}
}
