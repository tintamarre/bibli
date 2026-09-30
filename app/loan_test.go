package main

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// Two scans of the same book must produce one loan only, and the scan order is
// preserved (the summary follows the order the books were put down).
func TestUniqueIDs(t *testing.T) {
	got := uniqueIDs([]string{"7", " 3 ", "7", "abc", "", "0", "12", "3"})
	want := []int64{7, 3, 12}
	if len(got) != len(want) {
		t.Fatalf("uniqueIDs: %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("position %d: %d, want %d", i, got[i], want[i])
		}
	}
	if got := uniqueIDs(nil); got != nil {
		t.Errorf("empty basket: want nil, got %v", got)
	}
}

// The scanner adds a carriage return and sometimes spaces; a code typed by hand
// may come in lowercase. Only what has the shape of a code is uppercased.
func TestCleanCode(t *testing.T) {
	cases := map[string]string{
		"VOL204572\r\n":     "VOL204572",
		"  9782070408504  ": "9782070408504",
		"\tLEC73048\t":      "LEC73048",
		"VOL204572":         "VOL204572",
		"vol204572":         "VOL204572",
		" Lec73048 ":        "LEC73048",
		"bib204517":         "BIB204517", // an older collection's labels
		"volcan":            "volcan",    // a word, for the title search
		"le petit prince":   "le petit prince",
		"207040850x":        "207040850x",
		"":                  "",
	}
	for in, want := range cases {
		if got := cleanCode(in); got != want {
			t.Errorf("cleanCode(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestStatusWording(t *testing.T) {
	loadForTest(t)
	cases := map[string][2]string{
		// input -> {French, English}
		"damaged":   {"abîmé", "damaged"},
		"lost":      {"perdu", "lost"},
		"withdrawn": {"retiré du fonds", "withdrawn from the collection"},
		"available": {"disponible", "available"},
		// Unexpected value: shown as is rather than mistranslated or blanked.
		"zzz": {"zzz", "zzz"},
	}
	for in, want := range cases {
		if got := statusWording("fr", in); got != want[0] {
			t.Errorf("statusWording(fr, %q) = %q, want %q", in, got, want[0])
		}
		if got := statusWording("en", in); got != want[1] {
			t.Errorf("statusWording(en, %q) = %q, want %q", in, got, want[1])
		}
	}
}

// The wording shown at the desk when several copies are out.
func TestOpenLoanWho(t *testing.T) {
	cases := []struct {
		p    OpenLoan
		want string
	}{
		{OpenLoan{FirstName: "Léa", LastInitial: "D.", Class: "P4"}, "Léa D. (P4)"},
		{OpenLoan{FirstName: "Claire", LastInitial: "L."}, "Claire L."}, // a teacher, no class
		{OpenLoan{FirstName: "Tom"}, "Tom"},
	}
	for _, c := range cases {
		if got := c.p.Who(); got != c.want {
			t.Errorf("Who() = %q, want %q", got, c.want)
		}
	}
}

// Lending and returning, against a real database, through the methods the
// handlers call.
//
// The fixture is app/testdata/fixture.sql (see testDB): book 1 "Le Petit Prince" with
// copies VOL204572 and VOL811045, book 2 "Le loup est revenu" with VOL350929
// and VOL627437, book 3 with VOL146302 and no ISBN. Copy 1 is out to Tom and
// overdue, copy 3 is out to Léa and on time.

func testApp(t *testing.T) *app {
	t.Helper()
	return &app{db: testDB(t)}
}

// lend puts a copy out, as borrowConfirm would.
func lend(t *testing.T, a *app, copyID, borrowerID int64, daysAgo int) {
	t.Helper()
	if _, err := a.db.Exec(
		`INSERT INTO loan (copy_id, borrower_id, loaned_on, due_on)
		 VALUES (?, ?, date('now', ?), date('now', ?))`,
		copyID, borrowerID,
		fmt.Sprintf("-%d days", daysAgo), fmt.Sprintf("%d days", 21-daysAgo),
	); err != nil {
		t.Fatalf("lending copy %d: %v", copyID, err)
	}
}

// --- Returning ------------------------------------------------------------

// The ordinary return: one scan of the label, one open loan, and the screen can
// name who is giving it back.
func TestOpenLoansForScanByCopyCode(t *testing.T) {
	a := testApp(t)

	loans, title, err := a.openLoansForScan("VOL204572")
	if err != nil {
		t.Fatalf("openLoansForScan: %v", err)
	}
	if len(loans) != 1 {
		t.Fatalf("want 1 open loan, got %d", len(loans))
	}
	if title != "Le Petit Prince" {
		t.Errorf("title = %q, want %q", title, "Le Petit Prince")
	}
	if got := loans[0].Who(); got != "Tom B. (P3)" {
		t.Errorf("Who() = %q, want %q", got, "Tom B. (P3)")
	}
	if loans[0].CopyID != 1 || loans[0].Code != "VOL204572" {
		t.Errorf("copy = %d/%q, want 1/VOL204572", loans[0].CopyID, loans[0].Code)
	}
}

// Scanning the ISBN of a title out several times must hand back EVERY
// candidate, so the screen can ask.
func TestOpenLoansForScanISBNWithSeveralCopiesOut(t *testing.T) {
	a := testApp(t)
	lend(t, a, 2, 103, 1) // the second Petit Prince, to Zoé, yesterday

	loans, title, err := a.openLoansForScan("9782070408504")
	if err != nil {
		t.Fatalf("openLoansForScan: %v", err)
	}
	if len(loans) != 2 {
		t.Fatalf("want both open loans so the screen can ask, got %d", len(loans))
	}
	if title != "Le Petit Prince" {
		t.Errorf("title = %q", title)
	}
	// Oldest loan first (ORDER BY l.loaned_on, c.code): Tom borrowed 26 days
	// ago, Zoé yesterday.
	if loans[0].Who() != "Tom B. (P3)" || loans[1].Who() != "Zoé P. (P4)" {
		t.Errorf("order = %q then %q, want the oldest loan first",
			loans[0].Who(), loans[1].Who())
	}
	// Distinct loans, or the screen would offer the same one twice.
	if loans[0].LoanID == loans[1].LoanID || loans[0].CopyID == loans[1].CopyID {
		t.Errorf("the two candidates are the same loan: %+v / %+v", loans[0], loans[1])
	}
}

// The screen that asks, rendered with a real OpenLoan rather than a map:
// html/template resolves fields at render time, and a map answers to any name.
func TestReturnChoiceRenders(t *testing.T) {
	loadForTest(t)
	sets, err := loadTemplates()
	if err != nil {
		t.Fatalf("loading the templates: %v", err)
	}
	loans := []OpenLoan{
		{LoanID: 7, CopyID: 1, Code: "VOL204572", Title: "Le Petit Prince",
			FirstName: "Tom", LastInitial: "B.", Class: "P3", LoanedOn: "2026-08-20"},
		{LoanID: 9, CopyID: 2, Code: "VOL204580", Title: "Le Petit Prince",
			FirstName: "Zoé", LastInitial: "P.", Class: "P4", LoanedOn: "2026-09-01"},
	}
	for _, lang := range langs {
		var out strings.Builder
		if err := sets[lang]["return"].ExecuteTemplate(&out, "return_choice", map[string]any{
			"Title": "Le Petit Prince",
			"Loans": loans,
		}); err != nil {
			t.Fatalf("%s: rendering the choice of copy: %v", lang, err)
		}
		// Naming each borrower is the whole point of asking.
		for _, want := range []string{"Tom B. (P3)", "Zoé P. (P4)", "VOL204572", "VOL204580"} {
			if !strings.Contains(out.String(), want) {
				t.Errorf("%s: the choice screen does not show %q", lang, want)
			}
		}
		// Each button has to carry its own loan.
		for _, want := range []string{`"loan_id": "7"`, `"loan_id": "9"`} {
			if !strings.Contains(out.String(), want) {
				t.Errorf("%s: the choice screen does not post %s", lang, want)
			}
		}
	}
}

// The scan may be spelled in either form, with or without hyphens, and
// every spelling must reach the same work.
func TestOpenLoansForScanAcceptsBothISBNForms(t *testing.T) {
	a := testApp(t)

	for _, scan := range []string{"9782070408504", "2070408507", "978-2-07-040850-4"} {
		loans, _, err := a.openLoansForScan(scan)
		if err != nil {
			t.Errorf("%s: %v", scan, err)
			continue
		}
		if len(loans) != 1 || loans[0].CopyID != 1 {
			t.Errorf("%s: want the one open loan on copy 1, got %d loan(s)", scan, len(loans))
		}
	}
}

// A copy scanned back in while it is already on the shelf. The title still comes
// back, so the screen says which book rather than "unknown code".
func TestOpenLoansForScanCopyNotOut(t *testing.T) {
	a := testApp(t)

	loans, title, err := a.openLoansForScan("VOL146302")
	if !errors.Is(err, errNotOnLoan) {
		t.Fatalf("err = %v, want errNotOnLoan", err)
	}
	if len(loans) != 0 {
		t.Errorf("want no loan, got %d", len(loans))
	}
	if title != "Album maternelle (sans ISBN)" {
		t.Errorf("title = %q, want the book to be named anyway", title)
	}
}

// Every copy of a known work is in: still errNotOnLoan, still named.
func TestOpenLoansForScanKnownISBNNothingOut(t *testing.T) {
	a := testApp(t)
	// Book 2's copy 3 is out in the fixture; bring it back.
	if _, err := a.db.Exec(`UPDATE loan SET returned_on = date('now') WHERE copy_id = 3`); err != nil {
		t.Fatal(err)
	}

	_, title, err := a.openLoansForScan("9782211037495")
	if !errors.Is(err, errNotOnLoan) {
		t.Fatalf("err = %v, want errNotOnLoan", err)
	}
	if title != "Le loup est revenu" {
		t.Errorf("title = %q", title)
	}
}

// Neither an internal code nor a valid ISBN: sql.ErrNoRows, which the screen
// turns into "nothing read — scan again" rather than an error code.
func TestOpenLoansForScanUnknown(t *testing.T) {
	a := testApp(t)

	for _, scan := range []string{"ZZZ-9999", "9782070408505" /* bad check digit */, "54999" /* EAN-5 price */, ""} {
		if _, _, err := a.openLoansForScan(scan); !errors.Is(err, sql.ErrNoRows) {
			t.Errorf("%q: err = %v, want sql.ErrNoRows", scan, err)
		}
	}
}

// A work on file under its ISBN-10 alone must still be found by scanning
// the EAN-13 on its back.
func TestScanFindsAWorkFiledUnderItsISBN10Only(t *testing.T) {
	a := testApp(t)
	// "Le loup est revenu": on file under 2211037496, with no 13-form.
	if _, err := a.db.Exec(`UPDATE book SET isbn13 = NULL WHERE id = 2`); err != nil {
		t.Fatal(err)
	}

	// Lending: scanning the barcode on the back must still find the work.
	copyID, workID, _, _, title, err := a.copyForISBN("9782211037495")
	if err != nil {
		t.Fatalf("scanning the EAN-13 of a work filed under its ISBN-10: %v", err)
	}
	if workID != 2 || title != "Le loup est revenu" || copyID != 4 {
		t.Errorf("work = %d %q, copy = %d; want book 2 and copy 4 (copy 3 is out)",
			workID, title, copyID)
	}

	// Returning: the same, through the other entry point.
	loans, title, err := a.openLoansForScan("9782211037495")
	if err != nil {
		t.Fatalf("returning by EAN-13: %v", err)
	}
	if len(loans) != 1 || title != "Le loup est revenu" {
		t.Errorf("got %d loan(s) for %q, want the one open loan", len(loans), title)
	}
}

// --- Lending --------------------------------------------------------------

// Scanning a book at the desk picks a copy that is actually free.
func TestCopyForISBNPicksAFreeCopy(t *testing.T) {
	a := testApp(t)

	copyID, workID, code, status, title, err := a.copyForISBN("9782070408504")
	if err != nil {
		t.Fatalf("copyForISBN: %v", err)
	}
	if copyID != 2 || code != "VOL811045" {
		t.Errorf("copy = %d/%q, want 2/VOL811045 — copy 1 is out", copyID, code)
	}
	if workID != 1 || title != "Le Petit Prince" || status != "available" {
		t.Errorf("work = %d %q, status %q", workID, title, status)
	}
}

// The same, on the lending side.
func TestCopyForISBNAcceptsBothForms(t *testing.T) {
	a := testApp(t)

	for _, scan := range []string{"9782070408504", "2070408507"} {
		copyID, _, _, _, _, err := a.copyForISBN(scan)
		if err != nil {
			t.Errorf("%s: %v", scan, err)
			continue
		}
		if copyID != 2 {
			t.Errorf("%s: copy = %d, want 2", scan, copyID)
		}
	}
}

// Every copy out: workID and title come back so the screen can offer to add a
// copy rather than starting express cataloguing.
func TestCopyForISBNAllCopiesOut(t *testing.T) {
	a := testApp(t)
	lend(t, a, 2, 103, 1)

	_, workID, _, _, title, err := a.copyForISBN("9782070408504")
	if !errors.Is(err, errNoCopyAvailable) {
		t.Fatalf("err = %v, want errNoCopyAvailable", err)
	}
	if workID != 1 || title != "Le Petit Prince" {
		t.Errorf("work = %d %q, want the work named so a copy can be offered", workID, title)
	}
}

// A damaged, lost or withdrawn copy is not lendable, even though no loan holds
// it. Left out, a book declared lost would go back out on the next scan.
func TestCopyForISBNSkipsUnusableCopies(t *testing.T) {
	for _, status := range []string{"damaged", "lost", "withdrawn"} {
		t.Run(status, func(t *testing.T) {
			a := testApp(t)
			if _, err := a.db.Exec(`UPDATE copy SET status = ? WHERE id = 2`, status); err != nil {
				t.Fatal(err)
			}
			// Copy 1 is out, copy 2 is now unusable: nothing left to lend.
			if _, _, _, _, _, err := a.copyForISBN("9782070408504"); !errors.Is(err, errNoCopyAvailable) {
				t.Errorf("status %q: err = %v, want errNoCopyAvailable", status, err)
			}
		})
	}
}

// An ISBN absent from the catalogue: sql.ErrNoRows, and express cataloguing
// takes over without leaving the lending screen.
func TestCopyForISBNUnknownISBN(t *testing.T) {
	a := testApp(t)

	if _, _, _, _, _, err := a.copyForISBN("9782020493727"); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("unknown ISBN: err = %v, want sql.ErrNoRows", err)
	}
}

// The check digit is verified before anything else, so a partial read from a
// badly calibrated scanner never reaches the database or a catalogue.
func TestCopyForISBNRejectsAnInvalidScan(t *testing.T) {
	a := testApp(t)

	for _, scan := range []string{"9782070408505", "207040850", "54999", "abc", ""} {
		if _, _, _, _, _, err := a.copyForISBN(scan); !errors.Is(err, sql.ErrNoRows) {
			t.Errorf("%q: err = %v, want sql.ErrNoRows", scan, err)
		}
	}
}

// --- The invariant the desk depends on ------------------------------------

// A copy is out at most once, guaranteed by the partial unique index rather than
// by the handler alone.
func TestACopyIsOutAtMostOnce(t *testing.T) {
	a := testApp(t)

	// Copy 1 is already out to Tom.
	_, err := a.db.Exec(
		`INSERT INTO loan (copy_id, borrower_id, loaned_on, due_on)
		 VALUES (1, 103, date('now'), date('now', '+21 days'))`)
	if err == nil {
		t.Fatal("a second open loan on the same copy was accepted")
	}

	// Once returned, the same copy goes out again: the index is partial.
	if _, err := a.db.Exec(`UPDATE loan SET returned_on = date('now') WHERE copy_id = 1`); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec(
		`INSERT INTO loan (copy_id, borrower_id, loaned_on, due_on)
		 VALUES (1, 103, date('now'), date('now', '+21 days'))`); err != nil {
		t.Fatalf("lending a returned copy again: %v", err)
	}
	if n := count(t, a.db, `SELECT COUNT(*) FROM loan WHERE copy_id = 1`); n != 2 {
		t.Errorf("loan history = %d rows, want 2 (the past loan is kept)", n)
	}
}

// --- Searching at the desk ------------------------------------------------

// The desk offers at most five matches and says there are more.
func TestSearchAvailableBooksCapsAtFive(t *testing.T) {
	a := testApp(t)
	for i := 0; i < 7; i++ {
		if _, err := a.db.Exec(
			`INSERT INTO book (id, title, language, source_metadata) VALUES (?, ?, 'fr', 'test');
			 INSERT INTO copy (book_id, code, status) VALUES (?, ?, 'available')`,
			100+i, fmt.Sprintf("Chasse au trésor %d", i), 100+i, fmt.Sprintf("VOL-T%04d", i)); err != nil {
			t.Fatal(err)
		}
	}

	found, tooMany := a.searchAvailableBooks("trésor", nil)
	if len(found) != 5 || !tooMany {
		t.Errorf("got %d result(s), tooMany=%v; want 5 and true", len(found), tooMany)
	}

	found, tooMany = a.searchAvailableBooks("petit prince", nil)
	if len(found) != 1 || tooMany {
		t.Errorf("got %d result(s), tooMany=%v; want 1 and false", len(found), tooMany)
	}
	// Only free copies are offered, and the count says how many are left.
	if found[0].Code != "VOL811045" || found[0].Available != 1 {
		t.Errorf("offered %q with %d available, want VOL811045 and 1 (copy 1 is out)",
			found[0].Code, found[0].Available)
	}
}

// A title whose copies are all out must not be offered at all: the desk would
// propose a book that cannot leave.
func TestSearchAvailableBooksSkipsTitlesWithNothingFree(t *testing.T) {
	a := testApp(t)
	lend(t, a, 2, 103, 1) // both Petit Prince copies now out

	if found, _ := a.searchAvailableBooks("petit prince", nil); len(found) != 0 {
		t.Errorf("got %d result(s), want none — every copy is out", len(found))
	}
}

// A copy in the basket is no longer offered; for a title with several copies,
// the next free one is.
func TestSearchAvailableBooksSkipsWhatIsAlreadyInTheBasket(t *testing.T) {
	a := testApp(t)
	if _, err := a.db.Exec(
		`INSERT INTO book (id, title, language, source_metadata)
		      VALUES (200, 'La Nuit du visiteur', 'fr', 'test');
		 INSERT INTO copy (id, book_id, code, status) VALUES (200, 200, 'VOL-V0001', 'available');
		 INSERT INTO copy (id, book_id, code, status) VALUES (201, 200, 'VOL-V0002', 'available')`); err != nil {
		t.Fatal(err)
	}

	// Empty basket: the lowest-numbered copy is offered, and both are counted.
	found, _ := a.searchAvailableBooks("visiteur", nil)
	if len(found) != 1 || found[0].Code != "VOL-V0001" || found[0].Available != 2 {
		t.Fatalf("empty basket: %+v, want VOL-V0001 alone with 2 free", found)
	}

	// That copy in the basket: the other one is offered, and one is counted.
	found, _ = a.searchAvailableBooks("visiteur", []int64{200})
	if len(found) != 1 || found[0].Code != "VOL-V0002" || found[0].Available != 1 {
		t.Fatalf("one in the basket: %+v, want VOL-V0002 alone with 1 free", found)
	}

	// Both in the basket: nothing left to offer, so the title drops out.
	if found, _ = a.searchAvailableBooks("visiteur", []int64{200, 201}); len(found) != 0 {
		t.Errorf("both in the basket: %+v, want nothing", found)
	}
}

// A stray basket value is stepped over; a flood is cut off.
func TestBasketCopiesIsReadDefensively(t *testing.T) {
	form := url.Values{}
	form.Set("code", "petit prince")
	form.Add("copy_id", "12")
	form.Add("copy_id", "")
	form.Add("copy_id", "not-a-number")
	form.Add("copy_id", "34")
	r := httptest.NewRequest("POST", "/borrow/book-search", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if got := basketCopies(r); len(got) != 2 || got[0] != 12 || got[1] != 34 {
		t.Errorf("basketCopies = %v, want [12 34] — the unreadable ones stepped over", got)
	}

	flood := url.Values{}
	for i := 0; i < 500; i++ {
		flood.Add("copy_id", "7")
	}
	r = httptest.NewRequest("POST", "/borrow/book-search", strings.NewReader(flood.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if got := basketCopies(r); len(got) != 100 {
		t.Errorf("basketCopies kept %d of 500, want the cap of 100", len(got))
	}
}

func TestSearchBorrowersCapsAtFive(t *testing.T) {
	a := testApp(t)
	for i := 0; i < 7; i++ {
		if _, err := a.db.Exec(
			`INSERT INTO borrower (first_name, last_initial, class, kind, active)
			 VALUES (?, 'Z.', 'P5', 'student', 1)`, fmt.Sprintf("Camille%d", i)); err != nil {
			t.Fatal(err)
		}
	}

	found, tooMany := a.searchBorrowers("camille")
	if len(found) != 5 || !tooMany {
		t.Errorf("got %d result(s), tooMany=%v; want 5 and true", len(found), tooMany)
	}
	// Several words narrow rather than widen: "léa p4" is one pupil.
	if found, tooMany := a.searchBorrowers("léa p4"); len(found) != 1 || tooMany {
		t.Errorf("got %d result(s), tooMany=%v; want the one pupil", len(found), tooMany)
	}
}

// The sentinel borrower carrying anonymised loans is not a person. It must
// never show at the desk, where a book could be lent to it by mistake.
func TestSearchBorrowersExcludesTheAnonymousSentinel(t *testing.T) {
	a := testApp(t)
	anon := anonymousBorrowerID(a.db)
	if anon == 0 {
		t.Fatal("no sentinel borrower: 001_initial.sql did not run")
	}
	var name string
	if err := a.db.QueryRow(`SELECT first_name FROM borrower WHERE id = ?`, anon).Scan(&name); err != nil {
		t.Fatal(err)
	}

	for _, found := range [][]Borrower{
		first(a.searchBorrowers(name)),
		first(a.searchBorrowers("anonym")),
	} {
		for _, b := range found {
			if b.ID == anon {
				t.Errorf("the sentinel borrower %q is offered at the desk", name)
			}
		}
	}
}

func first(b []Borrower, _ bool) []Borrower { return b }

// --- Overdue --------------------------------------------------------------

// A loan due today is not late.
func TestOverdueStartsTheDayAfterTheDueDate(t *testing.T) {
	a := testApp(t)
	if _, err := a.db.Exec(`DELETE FROM loan`); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name    string
		dueOn   string
		overdue bool
	}{
		{"due tomorrow", "date('now', '+1 days')", false},
		{"due today", "date('now')", false},
		{"due yesterday", "date('now', '-1 days')", true},
		{"a week late", "date('now', '-7 days')", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := a.db.Exec(`DELETE FROM loan`); err != nil {
				t.Fatal(err)
			}
			if _, err := a.db.Exec(
				`INSERT INTO loan (copy_id, borrower_id, loaned_on, due_on)
				 VALUES (2, 101, date('now', '-21 days'), ` + c.dueOn + `)`); err != nil {
				t.Fatal(err)
			}
			n := count(t, a.db, `SELECT COUNT(*) FROM v_overdue`)
			if (n > 0) != c.overdue {
				t.Errorf("v_overdue has %d row(s), want overdue=%v", n, c.overdue)
			}
			if m := count(t, a.db, `SELECT COUNT(*) FROM v_active_loan`); m != 1 {
				t.Errorf("v_active_loan = %d, want 1 whatever the due date", m)
			}
		})
	}
}

// A loan is never blocked by a setting: an absent or unreadable loan period
// falls back to 14 days rather than refusing to lend.
func TestLoanDays(t *testing.T) {
	a := testApp(t)

	if got := a.loanDays(); got != 14 {
		t.Errorf("loanDays = %d, want the 14 days of the fresh schema", got)
	}

	cases := map[string]int{
		"7":     7,
		"  21 ": 21, // the settings form does not always trim
		"365":   365,
		"zero":  14, // unreadable
		"0":     14, // a loan period of nothing is not one
		"-3":    14,
		"":      14,
	}
	for value, want := range cases {
		if _, err := a.db.Exec(
			`UPDATE setting SET value = ? WHERE key = 'loan_days'`, value); err != nil {
			t.Fatal(err)
		}
		if got := a.loanDays(); got != want {
			t.Errorf("loan_days = %q: loanDays() = %d, want %d", value, got, want)
		}
	}

	// The setting gone altogether — a hand-edited database — is the same case.
	if _, err := a.db.Exec(`DELETE FROM setting WHERE key = 'loan_days'`); err != nil {
		t.Fatal(err)
	}
	if got := a.loanDays(); got != 14 {
		t.Errorf("loanDays = %d with the setting deleted, want 14", got)
	}
}

// What the desk shows the moment a card is scanned: books out, and how many late.
func TestWithLoanCounts(t *testing.T) {
	a := testApp(t)

	// The fixture: Tom (102) has copy 1 out and overdue, Léa (101) has copy 3
	// out and on time, Noah (104) has nothing.
	cases := []struct {
		id           int64
		who          string
		out, overdue int
	}{
		{102, "Tom", 1, 1},
		{101, "Léa", 1, 0},
		{104, "Noah", 0, 0},
	}
	for _, c := range cases {
		got := a.withLoanCounts(Borrower{ID: c.id})
		if got.OutCount != c.out || got.OverdueCount != c.overdue {
			t.Errorf("%s: out %d, overdue %d — want %d and %d",
				c.who, got.OutCount, got.OverdueCount, c.out, c.overdue)
		}
	}

	// A second book, on time, counts as out and not as late: the red is for
	// the overdue one alone.
	lend(t, a, 4, 102, 2)
	if got := a.withLoanCounts(Borrower{ID: 102}); got.OutCount != 2 || got.OverdueCount != 1 {
		t.Errorf("after a second loan: out %d, overdue %d — want 2 and 1", got.OutCount, got.OverdueCount)
	}

	// Returning the late one clears the warning without clearing the count.
	if _, err := a.db.Exec(
		`UPDATE loan SET returned_on = date('now') WHERE copy_id = 1 AND borrower_id = 102`); err != nil {
		t.Fatal(err)
	}
	if got := a.withLoanCounts(Borrower{ID: 102}); got.OutCount != 1 || got.OverdueCount != 0 {
		t.Errorf("after the return: out %d, overdue %d — want 1 and 0", got.OutCount, got.OverdueCount)
	}
}

// A due date is not overdue on the day itself, here as on every other screen
// (v_overdue): a book due today may still be brought back today.
func TestLoanCountsUseTheSameOverdueRuleAsTheViews(t *testing.T) {
	a := testApp(t)
	if _, err := a.db.Exec(
		`UPDATE loan SET due_on = date('now') WHERE copy_id = 1 AND borrower_id = 102`); err != nil {
		t.Fatal(err)
	}
	if got := a.withLoanCounts(Borrower{ID: 102}); got.OverdueCount != 0 {
		t.Errorf("due today counted as overdue: %d", got.OverdueCount)
	}
	if n := count(t, a.db, `SELECT COUNT(*) FROM v_overdue WHERE first_name = 'Tom'`); n != 0 {
		t.Errorf("v_overdue disagrees: %d rows", n)
	}

	// Yesterday is late, on both.
	if _, err := a.db.Exec(
		`UPDATE loan SET due_on = date('now','-1 day') WHERE copy_id = 1 AND borrower_id = 102`); err != nil {
		t.Fatal(err)
	}
	if got := a.withLoanCounts(Borrower{ID: 102}); got.OverdueCount != 1 {
		t.Errorf("due yesterday not counted as overdue: %d", got.OverdueCount)
	}
	if n := count(t, a.db, `SELECT COUNT(*) FROM v_overdue WHERE first_name = 'Tom'`); n != 1 {
		t.Errorf("v_overdue disagrees: %d rows", n)
	}
}

// Extending adds the days to the loan's current due date, not to today; a
// closed loan is left alone.
func TestExtendLoan(t *testing.T) {
	a := testApp(t)

	// Copy 1 is out to Tom and overdue in the fixture.
	var id int64
	if err := a.db.QueryRow(
		`SELECT id FROM loan WHERE copy_id = 1 AND returned_on IS NULL`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	due := func() string {
		var got string
		if err := a.db.QueryRow(`SELECT due_on FROM loan WHERE id = ?`, id).Scan(&got); err != nil {
			t.Fatal(err)
		}
		return got
	}
	set := func(expr string) {
		if _, err := a.db.Exec(`UPDATE loan SET due_on = date('now', ?) WHERE id = ?`, expr, id); err != nil {
			t.Fatal(err)
		}
	}
	want := func(expr, why string) {
		t.Helper()
		if n := count(t, a.db,
			`SELECT COUNT(*) FROM loan WHERE id = ? AND due_on = date('now', ?)`, id, expr); n != 1 {
			t.Errorf("due_on = %s, want %s: %s", due(), expr, why)
		}
	}

	// Still running: the week is added to the date it had, not to today.
	set("+3 days")
	if err := a.extendLoan(id, extendDefaultDays); err != nil {
		t.Fatal(err)
	}
	want("+10 days", "a week added to a loan due in three days")

	// A month late and given a week: three weeks late, and still in v_overdue.
	set("-30 days")
	if err := a.extendLoan(id, extendDefaultDays); err != nil {
		t.Fatal(err)
	}
	want("-23 days", "a week added to a loan a month late")
	if n := count(t, a.db, `SELECT COUNT(*) FROM v_overdue WHERE first_name = 'Tom'`); n != 1 {
		t.Errorf("a loan still three weeks late is not overdue: %d rows", n)
	}

	// A count typed over in the dialog: thirty days, not the week.
	set("-1 day")
	if err := a.extendLoan(id, 30); err != nil {
		t.Fatal(err)
	}
	want("+29 days", "extending ignored the number of days it was given")
	if n := count(t, a.db, `SELECT COUNT(*) FROM v_overdue WHERE first_name = 'Tom'`); n != 0 {
		t.Errorf("still overdue after being given a month: %d rows", n)
	}

	// A loan already closed keeps its date: a stale click changes nothing.
	if _, err := a.db.Exec(
		`UPDATE loan SET returned_on = date('now'), due_on = date('now','-1 day') WHERE id = ?`,
		id); err != nil {
		t.Fatal(err)
	}
	if err := a.extendLoan(id, extendDefaultDays); err != nil {
		t.Fatal(err)
	}
	want("-1 day", "extending touched a loan that is already closed")
}

// Any count off the form that is not a valid number of days falls back to the
// week rather than refusing to extend.
func TestExtendDays(t *testing.T) {
	cases := map[string]int{
		"7":     7,
		" 14  ": 14, // the dialog does not trim
		"1":     1,
		"365":   365,
		"":      extendDefaultDays, // JavaScript off, or the field cleared
		"0":     extendDefaultDays,
		"-3":    extendDefaultDays,
		"366":   extendDefaultDays,
		"three": extendDefaultDays,
		"7.5":   extendDefaultDays,
	}
	for value, want := range cases {
		if got := extendDays(value, extendDefaultDays); got != want {
			t.Errorf("extendDays(%q) = %d, want %d", value, got, want)
		}
	}
}

// The loan forms post the field the handlers read (backField): a mismatch only
// sends the redirect elsewhere, silently.
func TestLoanRowFormsPostTheFieldTheHandlerReads(t *testing.T) {
	files, err := filepath.Glob("templates/*.html")
	if err != nil {
		t.Fatal(err)
	}
	// The two forms a row of an open loan carries: close it, or push its due date.
	form := regexp.MustCompile(`(?s)<form[^>]*action="/loan/[^"]*/(return|extend)"[^>]*>(.*?)</form>`)
	hidden := regexp.MustCompile(`<input[^>]*type="hidden"[^>]*name="([^"]+)"`)

	seen := make(map[string]bool)
	for _, path := range files {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range form.FindAllStringSubmatch(string(content), -1) {
			seen[m[1]] = true
			var names []string
			for _, h := range hidden.FindAllStringSubmatch(m[2], -1) {
				names = append(names, h[1])
			}
			if !slices.Contains(names, backField) {
				t.Errorf("%s: the %s form posts %v, and the handler reads %q — "+
					"the loan would be updated and the redirect would go home",
					filepath.Base(path), m[1], names, backField)
			}
		}
	}
	// If the forms are ever renamed out from under this test, it must fail
	// rather than quietly check nothing.
	for _, action := range []string{"return", "extend"} {
		if !seen[action] {
			t.Fatalf("no form posting to /loan/{id}/%s was found in the templates", action)
		}
	}
}

// The value comes off a form, so it is not to be trusted as a redirect target.
func TestReturnDestinationStaysLocal(t *testing.T) {
	for _, bad := range []string{"//evil.example", "https://evil.example/x", "javascript:alert(1)"} {
		if got := sanitizeNext(bad); got != "/" {
			t.Errorf("sanitizeNext(%q) = %q, want / — an open redirect off the return button", bad, got)
		}
	}
	for _, ok := range []string{"/loans", "/loans?overdue=1", "/borrowers/101"} {
		if got := sanitizeNext(ok); got != ok {
			t.Errorf("sanitizeNext(%q) = %q, want it kept", ok, got)
		}
	}
}

// Express cataloguing is on unless a school turns it off, including when the
// setting is missing or unreadable.
func TestExpressCatalogue(t *testing.T) {
	a := testApp(t)

	if !a.expressCatalogue() {
		t.Error("a fresh database has it off; it should be on")
	}

	cases := map[string]bool{
		"1":    true,
		"0":    false,
		" 0 ":  false, // the checkbox writes a bare value, but be forgiving
		" 1 ":  true,
		"":     true, // anything that is not a plain 0 leaves the desk working
		"oui":  true,
		"true": true,
	}
	for value, want := range cases {
		if _, err := a.db.Exec(
			`UPDATE setting SET value = ? WHERE key = 'express_catalogue'`, value); err != nil {
			t.Fatal(err)
		}
		if got := a.expressCatalogue(); got != want {
			t.Errorf("express_catalogue = %q: %v, want %v", value, got, want)
		}
	}

	// Deleted altogether — a hand-edited database — is still on.
	if _, err := a.db.Exec(`DELETE FROM setting WHERE key = 'express_catalogue'`); err != nil {
		t.Fatal(err)
	}
	if !a.expressCatalogue() {
		t.Error("the setting is gone and the desk stopped cataloguing; it should not")
	}
}

// A book scanned by its ISBN enters the basket under the code of the copy
// chosen, like one scanned by its label: the basket lists copies, not works.
func TestBasketShowsTheCopyCodeForAnISBN(t *testing.T) {
	loadForTest(t)
	a := testApp(t)
	sets, err := loadTemplates()
	if err != nil {
		t.Fatalf("loading the templates: %v", err)
	}
	a.pages = sets

	r := httptest.NewRequest("POST", "/borrow/add", strings.NewReader("code=978-2-07-040850-4"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	a.borrowAdd(w, r)
	body := w.Body.String()
	if !strings.Contains(body, `<span class="code">VOL811045</span>`) {
		t.Errorf("the basket row does not show the copy's code VOL811045:\n%s", body)
	}
	if strings.Contains(body, "9782070408504") {
		t.Errorf("the basket row shows the ISBN instead of the copy's code:\n%s", body)
	}
}
