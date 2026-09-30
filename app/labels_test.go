package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The mark is what a child reads to put a book back: leading articles,
// apostrophes and accents are the cases that matter.
func TestTitleMark(t *testing.T) {
	cases := []struct{ title, want string }{
		{"Le Petit Prince", "PET"},
		{"La Belle et la Bête", "BEL"},
		{"Les Misérables", "MIS"},
		{"L'Odyssée", "ODY"},
		// An elided article is dropped; anything else elided belongs to the
		// word it leans on, or the mark is one letter and no shelf.
		{"C'est moi le plus beau", "CES"},
		{"C'est pas juste", "CES"},
		{"J'ai un problème", "JAI"},
		{"N'oublie pas", "NOU"},
		{"Aujourd'hui je suis", "AUJ"},
		{"Qu'as-tu vu ?", "QUA"},
		{"D'un pays lointain", "PAY"},
		{"L’Odyssée", "ODY"}, // typographic apostrophe
		{"C’est moi", "CES"},
		{"Émile et les détectives", "EMI"},
		{"À la recherche du temps perdu", "REC"}, // two articles in a row
		{"Du côté de chez Swann", "COT"},
		{"The Hobbit", "HOB"},
		{"A Wrinkle in Time", "WRI"},
		{"Cœur de pierre", "COE"}, // a ligature counts for its two letters
		{"1000 bornes", "100"},
		{"« Bonjour ! »", "BON"}, // punctuation is not a letter
		{"Ah ! ça ira", "AH"},    // shorter than a mark: what there is
		{"Des", "DES"},           // an article alone is still the title
		{"", ""},
		{"— ? !", ""},
	}
	for _, c := range cases {
		if got := titleMark(c.title); got != c.want {
			t.Errorf("titleMark(%q) = %q, want %q", c.title, got, c.want)
		}
	}
}

// Filed by the first author's surname, whichever way round the catalogue wrote
// it; by the title when nobody signed the book.
func TestShelfMark(t *testing.T) {
	cases := []struct{ authors, title, want string }{
		{"Saint-Exupéry, Antoine de ; Sfar, Joann", "Le Petit Prince", "SAI"}, // BnF
		{"Antoine de Saint-Exupéry ; Joann Sfar", "Le Petit Prince", "SAI"},   // Google Books
		{"Pennart, Geoffroy de", "Le Loup sentimental", "PEN"},
		{"Geoffroy de Pennart", "Le Loup sentimental", "PEN"},
		{"La Fontaine, Jean de", "Fables", "LAF"},
		{"Jean de La Fontaine", "Fables", "LAF"},
		{"Michel Van Zeveren", "C'est à moi, ça !", "VAN"},
		{"Ursula K. Le Guin", "Terremer", "LEG"},
		{"J. K. Rowling", "Harry Potter à l'école des sorciers", "ROW"},
		{"Marie-Aude Murail", "Oh, boy !", "MUR"},
		{"Jean Claude Mourlevat", "L'Enfant Océan", "MOU"},
		{"Marie-Catherine d'Aulnoy", "L'Oiseau bleu", "AUL"},
		{"Eoin O'Brien", "", "OBR"},
		{"Émile Zola", "Germinal", "ZOL"},
		{"Nadja", "Chien bleu", "NAD"}, // a single name is the surname
		{"Pennart", "Le Loup sentimental", "PEN"},
		{"Anonyme", "Le Roman de Renart", "ROM"},
		{"Collectif", "Contes d'Afrique", "CON"},
		{"", "Le Petit Prince", "PET"},
		{" ; ", "Le Petit Prince", "PET"},
		{"", "", ""},
	}
	for _, c := range cases {
		if got := shelfMark(c.authors, c.title); got != c.want {
			t.Errorf("shelfMark(%q, %q) = %q, want %q", c.authors, c.title, got, c.want)
		}
	}
}

func TestTitleHeadAndTail(t *testing.T) {
	cases := []struct{ title, head, tail string }{
		// The tail keeps two volumes of a series apart once the head clips.
		{"Le Journal d'un dégonflé - Tome 1", "Le Journal d'un dégonflé -", " Tome 1"},
		{"Le Journal d'un dégonflé - Tome 2", "Le Journal d'un dégonflé -", " Tome 2"},
		// Counted in runes, not bytes: the accents must not shift the cut.
		{"Émile et les détectives", "Émile et les dét", "ectives"},
		// Shorter than the tail, or exactly its length: the whole title is the
		// tail and the head is empty, so nothing renders twice.
		{"Tome 1", "", "Tome 1"},
		{"Tome 12", "", "Tome 12"},
		{"Zip", "", "Zip"},
		{"", "", ""},
	}
	for _, c := range cases {
		if got := titleHead(c.title); got != c.head {
			t.Errorf("titleHead(%q) = %q, want %q", c.title, got, c.head)
		}
		if got := titleTail(c.title); got != c.tail {
			t.Errorf("titleTail(%q) = %q, want %q", c.title, got, c.tail)
		}
		// The split drops nothing: the two halves rejoin into the title.
		if got := titleHead(c.title) + titleTail(c.title); got != c.title {
			t.Errorf("head+tail(%q) = %q, want the title back", c.title, got)
		}
	}
}

func TestFoldAccents(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Émile", "Emile"},
		{"ÉTÉ", "ETE"},
		{"Cœur", "Coeur"},
		{"ŒUVRE", "OEUVRE"},
		{"garçon", "garcon"},
		{"Hobbit", "Hobbit"},
	}
	for _, c := range cases {
		if got := foldAccents(c.in); got != c.want {
			t.Errorf("foldAccents(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFoldSearch(t *testing.T) {
	cases := []struct{ in, want string }{
		{"École", "ecole"},
		{"ÉCOLE", "ecole"},
		{"école", "ecole"},
		{"Émile", "emile"},
		{"Garçon", "garcon"},
		{"ŒUVRE", "oeuvre"},
		{"Cœur", "coeur"},
		{"VOL204572", "vol204572"},
		{"100%_x", "100%_x"},
	}
	for _, c := range cases {
		if got := foldSearch(c.in); got != c.want {
			t.Errorf("foldSearch(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// A date that is not a date is dropped, not queried with: SQLite compares text.
func TestReadInvFilterDropsANonDate(t *testing.T) {
	r := httptest.NewRequest("GET", "/inventory?q=+prince+&from=2026-09-01&to=not-a-date", nil)
	f := readInvFilter(r)
	if f.Q != "prince" {
		t.Errorf("Q = %q, want %q", f.Q, "prince")
	}
	if f.From != "2026-09-01" {
		t.Errorf("From = %q", f.From)
	}
	if f.To != "" {
		t.Errorf("To = %q, want it dropped", f.To)
	}
}

// The date filter compares the date part of the day a copy was recorded (a
// datetime), or a copy recorded this afternoon falls outside "up to today".
func TestFilteringOnTheDayACopyWasRecorded(t *testing.T) {
	a := testApp(t)

	// The demo copies are recorded now; this one belongs to last month.
	if _, err := a.db.Exec(
		`UPDATE copy SET created_at = datetime('now', '-30 days') WHERE code = 'VOL146302'`,
	); err != nil {
		t.Fatalf("backdating a copy: %v", err)
	}

	today := time.Now().UTC().Format("2006-01-02")

	// By title, for a stable first row: the demo copies share their day of entry.
	all, err := a.listInventory(invFilter{Sort: "title", Dir: "asc"})
	if err != nil {
		t.Fatalf("listInventory: %v", err)
	}
	if len(all) != 5 {
		t.Fatalf("no filter: want the 5 demo copies, got %d", len(all))
	}
	// The shelf mark comes from the title, and is derived for the sheet only.
	if labelsForCopies(all)[0].Mark != "ALB" {
		t.Errorf("first row: mark %q, want ALB (Album maternelle)", labelsForCopies(all)[0].Mark)
	}

	// A copy recorded today is inside a range that ends today.
	recent, err := a.listInventory(invFilter{From: today, To: today, Sort: invSortDefault, Dir: "asc"})
	if err != nil {
		t.Fatalf("listInventory (range): %v", err)
	}
	if len(recent) != 4 {
		t.Errorf("recorded today: want 4, got %d", len(recent))
	}
	for _, l := range recent {
		if l.Code == "VOL146302" {
			t.Error("the copy recorded last month came back in today's range")
		}
	}

	// Sorting by the day of entry is what makes the range readable.
	byAdded, err := a.listInventory(invFilter{Sort: "added", Dir: "asc"})
	if err != nil {
		t.Fatalf("listInventory (sort): %v", err)
	}
	if byAdded[0].Code != "VOL146302" {
		t.Errorf("oldest first = %s, want the copy recorded last month", byAdded[0].Code)
	}
}

// ?codes= keeps the order it was handed, and an unknown code still gets its barcode.
func TestLabelsForCodesKeepsTheOrderAndTheUnknown(t *testing.T) {
	a := testApp(t)

	rows := a.labelsForCodes([]string{"VOL350929", " ", "VOL999999", "VOL204572"})
	if len(rows) != 3 {
		t.Fatalf("want 3 labels, got %d", len(rows))
	}
	if rows[0].Code != "VOL350929" || rows[0].Mark != "PEN" {
		t.Errorf("first label: %+v, want VOL350929 marked PEN", rows[0])
	}
	if rows[1].Code != "VOL999999" || rows[1].Title != "" {
		t.Errorf("unknown code: %+v, want the code alone", rows[1])
	}
	if rows[2].Code != "VOL204572" || rows[2].Mark != "SAI" {
		t.Errorf("last label: %+v, want VOL204572 marked SAI", rows[2])
	}
}

// Sorted by entry, the sheet runs in the order the copies were recorded,
// oldest first, even while the screen shows the newest first; and within one
// day it is the time that orders, not the title.
func TestLabelSheetFollowsTheOrderOfCataloguing(t *testing.T) {
	a, h := testHandler(t)
	c := signedIn(t, a)
	order := []string{"VOL627437", "VOL146302", "VOL204572", "VOL811045", "VOL350929"}
	for i, code := range order {
		if _, err := a.db.Exec(
			`UPDATE copy SET created_at = datetime('now', '-1 hour', ?) WHERE code = ?`,
			fmt.Sprintf("+%d minutes", i), code); err != nil {
			t.Fatal(err)
		}
	}

	r := httptest.NewRequest("GET", "/print/labels?sort=added&dir=desc", nil)
	r.AddCookie(c)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("GET = %d", w.Code)
	}
	body := w.Body.String()
	last := -1
	for _, code := range order {
		i := strings.Index(body, `data-code="`+code+`"`)
		if i < 0 {
			t.Fatalf("%s missing from the sheet", code)
		}
		if i < last {
			t.Errorf("%s printed before a copy recorded earlier: want %v", code, order)
		}
		last = i
	}

	// The screen itself keeps its newest-first order.
	rows, err := a.listInventory(invFilter{Sort: "added", Dir: "desc"})
	if err != nil {
		t.Fatal(err)
	}
	if rows[0].Code != order[len(order)-1] {
		t.Errorf("screen, newest first: %s on top, want %s", rows[0].Code, order[len(order)-1])
	}
}
