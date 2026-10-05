package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func batchPostCode(t *testing.T, a *app, h http.Handler, path string, form url.Values) (int, string) {
	t.Helper()
	r := httptest.NewRequest("POST", path, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	r.AddCookie(signedIn(t, a))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w.Code, w.Body.String()
}

func batchPost(t *testing.T, a *app, h http.Handler, path string, form url.Values) string {
	t.Helper()
	code, body := batchPostCode(t, a, h, path, form)
	if code != http.StatusOK {
		t.Fatalf("POST %s = %d, want 200", path, code)
	}
	return body
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

func itemJSON(t *testing.T, it batchItem) string {
	t.Helper()
	b, err := json.Marshal(it)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// A scan writes nothing: a book a catalogue knows comes back staged, to be
// checked, and the one nobody knows is set aside.
func TestBatchAddStagesWhatIsFoundAndSetsTheRestAside(t *testing.T) {
	a, h := testHandler(t)
	const found, missing = "9782211201896", "9782070612758"
	catalogueMemo.rememberRecord(found, &Record{ISBN13: found, Title: "Chien bleu", Authors: "Nadja", Source: "bnf"})
	catalogueMemo.rememberRecord(missing, nil)
	t.Cleanup(func() { catalogueMemo.forgetRecord(found); catalogueMemo.forgetRecord(missing) })

	body := batchPost(t, a, h, "/catalogue/batch/add", url.Values{"isbn": {found}, "location": {"bac 3"}})
	if !strings.Contains(body, `data-kind="found"`) || !strings.Contains(body, "Chien bleu") || !strings.Contains(body, "Nadja") {
		t.Errorf("a found book should come back staged: %s", body)
	}
	if n := copiesOf(t, a, found); n != 0 {
		t.Errorf("%d copies written by a scan, want none before the save", n)
	}
	if !strings.Contains(body, "bac 3") {
		t.Errorf("the shelf should show on the staged line: %s", body)
	}

	body = batchPost(t, a, h, "/catalogue/batch/add", url.Values{"isbn": {missing}})
	if !strings.Contains(body, `data-kind="aside"`) || !strings.Contains(body, missing) {
		t.Errorf("an unknown book should be set aside: %s", body)
	}

	body = batchPost(t, a, h, "/catalogue/batch/add", url.Values{"isbn": {"9782211201897"}})
	if !strings.Contains(body, `data-kind="aside"`) {
		t.Errorf("a bad check digit should be set aside: %s", body)
	}
}

// A book already catalogued is staged too, said to be one: the person sees that
// it adds copies rather than a title.
func TestBatchAddShowsWhatIsAlreadyCatalogued(t *testing.T) {
	a, h := testHandler(t)
	const isbn = "9782211201896"
	_, body := batchPostCode(t, a, h, "/catalogue/batch/save", url.Values{
		"item": {itemJSON(t, batchItem{ISBN13: isbn, Title: "Chien bleu", Copies: 2})}})
	if copiesOf(t, a, isbn) != 2 {
		t.Fatalf("setup: %s", body)
	}
	body = batchPost(t, a, h, "/catalogue/batch/add", url.Values{"isbn": {isbn}})
	if !strings.Contains(body, `data-kind="found"`) || !strings.Contains(body, "batch-badge-known") {
		t.Errorf("a known book should be staged with its known badge: %s", body)
	}
	if copiesOf(t, a, isbn) != 2 {
		t.Error("a scan changed the copies")
	}
}

// The panel's form stages the set-aside book it is filled in for; a refused one
// redraws the form.
func TestBatchStageAnswersWithARowAndWritesNothing(t *testing.T) {
	a, h := testHandler(t)
	body := batchPost(t, a, h, "/catalogue/batch/stage", url.Values{
		"title": {"Un livre"}, "isbn13": {"9782070612758"}, "copies": {"2"}, "location": {"bac 3"},
	})
	if !strings.Contains(body, `data-kind="found"`) || !strings.Contains(body, `class="batch-n">2<`) {
		t.Errorf("want a staged row with 2 copies: %s", body)
	}
	if copiesOf(t, a, "9782070612758") != 0 {
		t.Error("staging wrote a copy")
	}
	body = batchPost(t, a, h, "/catalogue/batch/stage", url.Values{"isbn13": {"9782070612758"}, "copies": {"1"}})
	if strings.Contains(body, `data-kind="found"`) || !strings.Contains(body, "/catalogue/batch/stage") {
		t.Errorf("a refused stage should redraw the batch form: %s", body)
	}
}

// The save writes every staged book at once, with the copies and shelf the
// person left on it.
func TestBatchSaveWritesTheStagedBooks(t *testing.T) {
	a, h := testHandler(t)
	body := batchPost(t, a, h, "/catalogue/batch/save", url.Values{"item": {
		itemJSON(t, batchItem{ISBN13: "9782211201896", Title: "Chien bleu", Copies: 3, Location: "bac 3"}),
		itemJSON(t, batchItem{ISBN13: "9782070612758", Title: "Un livre", Copies: 1}),
		itemJSON(t, batchItem{Title: "Sans ISBN", Copies: 500}),
	}})
	if copiesOf(t, a, "9782211201896") != 3 || copiesOf(t, a, "9782070612758") != 1 {
		t.Errorf("copies not written as staged: %s", body)
	}
	var n int
	a.db.QueryRow(`SELECT COUNT(*) FROM copy c JOIN book b ON b.id = c.book_id WHERE b.title = 'Sans ISBN'`).Scan(&n)
	if n != 100 {
		t.Errorf("%d copies for 500 asked, want the cap of 100", n)
	}
	var loc string
	a.db.QueryRow(`SELECT c.location FROM copy c JOIN book b ON b.id = c.book_id WHERE b.isbn13 = '9782211201896' LIMIT 1`).Scan(&loc)
	if loc != "bac 3" {
		t.Errorf("location = %q", loc)
	}
	if !strings.Contains(body, "batch-result") || !strings.Contains(body, "/print/labels?codes=") {
		t.Errorf("want the result block with the labels link: %s", body)
	}
}

// One bad line and nothing is written.
func TestBatchSaveIsAllOrNothing(t *testing.T) {
	a, h := testHandler(t)
	code, _ := batchPostCode(t, a, h, "/catalogue/batch/save", url.Values{"item": {
		itemJSON(t, batchItem{ISBN13: "9782211201896", Title: "Chien bleu", Copies: 1}),
		itemJSON(t, batchItem{ISBN13: "9782211201897", Title: "Mauvaise clé", Copies: 1}),
	}})
	if code != http.StatusBadRequest {
		t.Errorf("code = %d, want 400", code)
	}
	if copiesOf(t, a, "9782211201896") != 0 {
		t.Error("a book was written although another line was refused")
	}
	if code, _ := batchPostCode(t, a, h, "/catalogue/batch/save", url.Values{}); code != http.StatusBadRequest {
		t.Errorf("an empty save = %d, want 400", code)
	}
}
