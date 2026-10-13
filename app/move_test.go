package main

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func locationOf(t *testing.T, a *app, copyID int) string {
	t.Helper()
	var loc *string
	if err := a.db.QueryRow(`SELECT location FROM copy WHERE id = ?`, copyID).Scan(&loc); err != nil {
		t.Fatalf("location of copy %d: %v", copyID, err)
	}
	if loc == nil {
		return ""
	}
	return *loc
}

// Every copy of the location follows, and the screen comes back on the new
// name with the rest of its filter.
func TestRenamingALocationMovesEveryCopyInIt(t *testing.T) {
	a, h := testHandler(t)
	w := postForm(h, signedIn(t, a), "/inventory/location", url.Values{
		"from": {"bac albums"}, "to": {"Albums 3-5"}, "filter": {"location=bac+albums&sort=title&dir=asc"},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("rename = %d, want 200", w.Code)
	}
	if got := locationOf(t, a, 1) + "|" + locationOf(t, a, 2); got != "Albums 3-5|Albums 3-5" {
		t.Errorf("copies 1 and 2 at %q, want both at Albums 3-5", got)
	}
	if got := locationOf(t, a, 3); got != "classe P3" {
		t.Errorf("copy 3 moved to %q: only the renamed location's copies may move", got)
	}
	redirect := w.Header().Get("HX-Redirect")
	if !strings.Contains(redirect, "location=Albums+3-5") || !strings.Contains(redirect, "sort=title") {
		t.Errorf("HX-Redirect = %q, want the inventory on the new name, sort kept", redirect)
	}
}

// A name already in use merges two locations for good: asked, then done.
func TestRenamingOntoAnExistingLocationAsksFirst(t *testing.T) {
	a, h := testHandler(t)
	c := signedIn(t, a)
	form := url.Values{"from": {"bac albums"}, "to": {"classe P3"}}
	w := postForm(h, c, "/inventory/location", form)
	if w.Header().Get("HX-Redirect") != "" || locationOf(t, a, 1) != "bac albums" {
		t.Fatal("merged without asking")
	}
	if !strings.Contains(w.Body.String(), `"merge": "1"`) {
		t.Errorf("no merge button in %s", w.Body.String())
	}
	form.Set("merge", "1")
	if w := postForm(h, c, "/inventory/location", form); w.Header().Get("HX-Redirect") == "" {
		t.Fatalf("confirmed merge did not redirect: %d %s", w.Code, w.Body.String())
	}
	if n := count(t, a.db, `SELECT COUNT(*) FROM copy WHERE location = 'classe P3'`); n != 4 {
		t.Errorf("%d copies in classe P3 after the merge, want 4", n)
	}
}

// "-" names the copies with no location: they all receive the new one.
func TestRenamingTheUnfiledGivesThemALocation(t *testing.T) {
	a, h := testHandler(t)
	a.db.Exec(`UPDATE copy SET location = NULL WHERE id = 5`)
	w := postForm(h, signedIn(t, a), "/inventory/location", url.Values{"from": {"-"}, "to": {"réserve"}})
	if w.Header().Get("HX-Redirect") == "" {
		t.Fatalf("rename = %d %s, want a redirect", w.Code, w.Body.String())
	}
	if got := locationOf(t, a, 5); got != "réserve" {
		t.Errorf("copy 5 at %q, want réserve", got)
	}
}

func TestRenamingRefusesAnEmptyOrUnchangedName(t *testing.T) {
	a, h := testHandler(t)
	c := signedIn(t, a)
	for _, to := range []string{"", "  ", "bac albums"} {
		w := postForm(h, c, "/inventory/location", url.Values{"from": {"bac albums"}, "to": {to}})
		if w.Header().Get("HX-Redirect") != "" {
			t.Errorf("renamed to %q", to)
		}
	}
	if w := postForm(h, c, "/inventory/location", url.Values{"from": {""}, "to": {"x"}}); w.Code != http.StatusBadRequest {
		t.Errorf("rename with no location named = %d, want 400: it would rename every copy", w.Code)
	}
	if got := locationOf(t, a, 1); got != "bac albums" {
		t.Errorf("copy 1 at %q, want untouched", got)
	}
}

// A copy code moves that copy and keeps its status.
func TestScanningACodeMovesThatCopy(t *testing.T) {
	a, h := testHandler(t)
	a.db.Exec(`UPDATE copy SET status = 'damaged' WHERE id = 3`)
	w := postForm(h, signedIn(t, a), "/inventory/move", url.Values{"location": {"réserve"}, "code": {"vol350929"}})
	if w.Code != http.StatusOK {
		t.Fatalf("move = %d", w.Code)
	}
	if got := locationOf(t, a, 3); got != "réserve" {
		t.Errorf("copy 3 at %q, want réserve", got)
	}
	if got := locationOf(t, a, 4); got != "classe P3" {
		t.Errorf("copy 4 moved to %q: only the scanned copy may move", got)
	}
	var status string
	a.db.QueryRow(`SELECT status FROM copy WHERE id = 3`).Scan(&status)
	if status != "damaged" {
		t.Errorf("status = %q, want damaged kept", status)
	}
}

// An ISBN with several copies moves nothing until one is picked.
func TestScanningAnISBNWithSeveralCopiesAsksWhichOne(t *testing.T) {
	a, h := testHandler(t)
	c := signedIn(t, a)
	w := postForm(h, c, "/inventory/move", url.Values{"location": {"réserve"}, "code": {"2070408507"}})
	if !strings.Contains(w.Body.String(), "VOL204572") || !strings.Contains(w.Body.String(), "VOL811045") {
		t.Errorf("the choice does not list both copies: %s", w.Body.String())
	}
	if n := count(t, a.db, `SELECT COUNT(*) FROM copy WHERE location = 'réserve'`); n != 0 {
		t.Fatalf("%d copies moved before the librarian chose", n)
	}
	postForm(h, c, "/inventory/move", url.Values{"location": {"réserve"}, "copy_id": {"2"}})
	if got := locationOf(t, a, 2) + "|" + locationOf(t, a, 1); got != "réserve|bac albums" {
		t.Errorf("copies 2|1 at %q, want réserve|bac albums", got)
	}
}

func TestScanningNeedsADestinationAndAKnownCode(t *testing.T) {
	a, h := testHandler(t)
	c := signedIn(t, a)
	for _, v := range []url.Values{
		{"location": {""}, "code": {"VOL350929"}},
		{"location": {"réserve"}, "code": {"9780000000002"}},
		{"location": {"réserve"}, "code": {"nothing"}},
	} {
		w := postForm(h, c, "/inventory/move", v)
		if !strings.Contains(w.Body.String(), "err") {
			t.Errorf("%v: no error shown: %s", v, w.Body.String())
		}
	}
	if n := count(t, a.db, `SELECT COUNT(*) FROM copy WHERE location = 'réserve'`); n != 0 {
		t.Errorf("%d copies moved", n)
	}
}
