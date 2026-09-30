package main

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"mime/multipart"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
)

// The nominal year rollover: the whole school moves up one, P6 leaves. Only a
// snapshot gets this right.
func TestPlanRolloverFullChain(t *testing.T) {
	classes := []string{"M1", "M2", "M3", "P1", "P2", "P3", "P4", "P5", "P6"}
	var pupils []rolloverPupil
	for i, c := range classes {
		pupils = append(pupils, rolloverPupil{ID: int64(i + 1), FirstName: "e", LastInitial: "E.", Class: c})
	}
	rules := []rolloverRule{
		{Src: "M1", Dst: "M2"}, {Src: "M2", Dst: "M3"}, {Src: "M3", Dst: "P1"},
		{Src: "P1", Dst: "P2"}, {Src: "P2", Dst: "P3"}, {Src: "P3", Dst: "P4"},
		{Src: "P4", Dst: "P5"}, {Src: "P5", Dst: "P6"}, {Src: "P6", Leaving: true},
	}

	moved, leavers := planRollover(pupils, rules)

	// Each moves up exactly one.
	want := map[int64]string{1: "M2", 2: "M3", 3: "P1", 4: "P2", 5: "P3", 6: "P4", 7: "P5", 8: "P6"}
	if len(moved) != len(want) {
		t.Fatalf("moves: want %d, got %d (%v)", len(want), len(moved), moved)
	}
	for id, c := range want {
		if moved[id] != c {
			t.Errorf("pupil %d: want class %q, got %q", id, c, moved[id])
		}
	}
	// Only P6 leaves.
	if len(leavers) != 1 || leavers[0] != 9 {
		t.Errorf("leavers: want [9], got %v", leavers)
	}
}

func TestPlanRolloverEdgeCases(t *testing.T) {
	pupils := []rolloverPupil{
		{ID: 1, Class: "P1"},
		{ID: 2, Class: "P2"},
		{ID: 3, Class: ""},   // no class
		{ID: 4, Class: "P5"}, // no rule
	}
	rules := []rolloverRule{
		{Src: "P1", Dst: ""},                  // empty = unchanged
		{Src: "P2", Dst: "P3", Leaving: true}, // leaving wins
		{Src: "", Dst: "P1"},                  // the class-less enter P1
	}

	moved, leavers := planRollover(pupils, rules)

	if _, ok := moved[1]; ok {
		t.Error("empty destination: the class must stay unchanged")
	}
	if _, ok := moved[4]; ok {
		t.Error("no rule: the class must stay unchanged")
	}
	if moved[3] != "P1" {
		t.Errorf("no class -> want P1, got %q", moved[3])
	}
	if len(leavers) != 1 || leavers[0] != 2 {
		t.Errorf("leavers: want [2], got %v", leavers)
	}
	if _, ok := moved[2]; ok {
		t.Error("a leaver must not be moved up as well")
	}
}

// A destination equal to the current class produces no update.
func TestPlanRolloverNoChange(t *testing.T) {
	pupils := []rolloverPupil{{ID: 1, Class: "P3"}, {ID: 2, Class: "P3"}}
	moved, leavers := planRollover(pupils, []rolloverRule{{Src: "P3", Dst: "P3"}})
	if len(moved) != 0 || len(leavers) != 0 {
		t.Errorf("want no change, got %v / %v", moved, leavers)
	}
}

// The order of the rules does not change the result.
func TestPlanRolloverOrderIndependent(t *testing.T) {
	pupils := []rolloverPupil{{ID: 1, Class: "P1"}, {ID: 2, Class: "P2"}, {ID: 3, Class: "P3"}}
	forward := []rolloverRule{{Src: "P1", Dst: "P2"}, {Src: "P2", Dst: "P3"}, {Src: "P3", Leaving: true}}
	backward := []rolloverRule{{Src: "P3", Leaving: true}, {Src: "P2", Dst: "P3"}, {Src: "P1", Dst: "P2"}}

	ra, sa := planRollover(pupils, forward)
	rb, sb := planRollover(pupils, backward)

	sort.Slice(sa, func(i, j int) bool { return sa[i] < sa[j] })
	sort.Slice(sb, func(i, j int) bool { return sb[i] < sb[j] })
	if len(sa) != len(sb) || len(ra) != len(rb) {
		t.Fatalf("different plans: %v/%v against %v/%v", ra, sa, rb, sb)
	}
	for id, c := range ra {
		if rb[id] != c {
			t.Errorf("pupil %d: %q against %q depending on the rule order", id, c, rb[id])
		}
	}
}

// A class that receives pupils while some of its own stay is a merge; one
// emptied or moving on in the same submission is not.
func TestFindMerges(t *testing.T) {
	pupils := []rolloverPupil{
		{ID: 1, Class: "P5"}, {ID: 2, Class: "P5"},
		{ID: 3, Class: "P6"}, {ID: 4, Class: "P6"}, {ID: 5, Class: "P6"},
		{ID: 6, Class: "P5B"},
	}
	cases := []struct {
		name  string
		rules []rolloverRule
		want  string // mergeKey, "" for none
		stay  int
	}{
		{"P6 left untouched", []rolloverRule{{Src: "P5", Dst: "P6"}}, "P6<P5", 3},
		{"P6 kept by name", []rolloverRule{{Src: "P5", Dst: "P6"}, {Src: "P6", Dst: "P6"}}, "P6<P5", 3},
		{"two classes into it", []rolloverRule{{Src: "P5", Dst: "P6"}, {Src: "P5B", Dst: "P6"}}, "P6<P5+P5B", 3},
		{"P6 leaving", []rolloverRule{{Src: "P5", Dst: "P6"}, {Src: "P6", Leaving: true}}, "", 0},
		{"P6 moving on", []rolloverRule{{Src: "P5", Dst: "P6"}, {Src: "P6", Dst: "S1"}}, "", 0},
		{"two classes into an empty one", []rolloverRule{{Src: "P5", Dst: "P7"}, {Src: "P5B", Dst: "P7"}}, "", 0},
		{"a leaving class going nowhere", []rolloverRule{{Src: "P5", Dst: "P6", Leaving: true}}, "", 0},
		{"nothing moves", []rolloverRule{{Src: "P5"}, {Src: "P6"}}, "", 0},
	}
	for _, c := range cases {
		merges := findMerges(pupils, c.rules)
		if got := mergeKey(merges); got != c.want {
			t.Errorf("%s: merges %q, want %q", c.name, got, c.want)
			continue
		}
		if c.want != "" && merges[0].Staying != c.stay {
			t.Errorf("%s: %d staying, want %d", c.name, merges[0].Staying, c.stay)
		}
	}
}

// A rollover done in two steps, the wrong way round, must not mix two years:
// the merge is refused until it is confirmed, and confirmed as it was shown.
func TestRolloverRefusesAnUnconfirmedMerge(t *testing.T) {
	loadForTest(t)
	a := testApp(t)
	sets, err := loadTemplates()
	if err != nil {
		t.Fatalf("loading the templates: %v", err)
	}
	a.pages = sets
	classOf := func(name string) string {
		t.Helper()
		var c string
		if err := a.db.QueryRow(`SELECT COALESCE(class, '') FROM borrower WHERE first_name = ?`, name).Scan(&c); err != nil {
			t.Fatalf("class of %s: %v", name, err)
		}
		return c
	}
	if classOf("Tom") != "P3" || classOf("Léa") != "P4" {
		t.Fatalf("fixture: Tom in %q, Léa in %q, want P3 and P4", classOf("Tom"), classOf("Léa"))
	}
	post := func(mergeOK string) {
		t.Helper()
		pupils, err := pupilsSnapshot(a.db)
		if err != nil {
			t.Fatalf("pupilsSnapshot: %v", err)
		}
		// P3 into P4, and the P4 row left empty: P4's own pupils stay.
		form := url.Values{"n": {"2"}, "src_0": {"P4"}, "dst_0": {""},
			"src_1": {"P3"}, "dst_1": {"P4"}, "state": {rolloverState(pupils)}}
		if mergeOK != "" {
			form.Set("merge_ok", mergeOK)
		}
		r := httptest.NewRequest("POST", "/borrowers/rollover", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		a.rolloverConfirm(w, r)
		if w.Code != 200 {
			t.Fatalf("status %d: %s", w.Code, w.Body)
		}
	}

	post("")
	if got := classOf("Tom"); got != "P3" {
		t.Fatalf("an unconfirmed merge moved Tom to %q", got)
	}
	post("P4<P5")
	if got := classOf("Tom"); got != "P3" {
		t.Fatalf("a confirmation of another merge moved Tom to %q", got)
	}
	post("P4<P3")
	if got := classOf("Tom"); got != "P4" {
		t.Errorf("the confirmed merge left Tom in %q, want P4", got)
	}
}

// One year up, except at the top of each series, where arithmetic would move
// the leavers into a class that does not exist.
func TestNextClasses(t *testing.T) {
	got := nextClasses([]string{"M1", "M2", "M3", "P1", "P5", "P6", "P5A", "P5B", "P6B",
		"3A", "4A", "L09", "L10", "Mme Dupont", "P5-6", "Aucune classe", "K%1", "K%2"})
	want := map[string]string{
		"M1": "M2", "M2": "M3", "P1": "P2", "P5": "P6",
		"P5B": "P6B", "3A": "4A", "L09": "L10", "K%1": "K%2",
	}
	for c, w := range want {
		if got[c] != w {
			t.Errorf("%s: next %q, want %q", c, got[c], w)
		}
	}
	for c, g := range got {
		if _, ok := want[c]; !ok {
			t.Errorf("%s: suggested %q, want nothing", c, g)
		}
	}
}

func TestMarkDuplicates(t *testing.T) {
	existing := map[string]bool{borrowerKey("Léa", "D.", "P4"): true}
	rows := []importRow{
		{FirstName: "Léa", LastInitial: "D.", Class: "P4"}, // already on file
		{FirstName: "léa", LastInitial: "d.", Class: "p4"}, // same, different case
		{FirstName: "Tom", LastInitial: "B.", Class: "P3"}, // new
		{FirstName: "Tom", LastInitial: "B.", Class: "P3"}, // repeated in the file
		{FirstName: "Léa", LastInitial: "D.", Class: "P5"}, // same name, other class
	}
	count := markDuplicates(rows, existing)
	if count != 3 {
		t.Errorf("want 3 duplicates, got %d", count)
	}
	want := []bool{true, true, false, true, false}
	for i, a := range want {
		if rows[i].Duplicate != a {
			t.Errorf("row %d (%s %s %s): duplicate=%v, want %v",
				i, rows[i].FirstName, rows[i].LastInitial, rows[i].Class, rows[i].Duplicate, a)
		}
	}
}

func TestParseCSV(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []importRow
	}{
		{"comma, with a header", "prénom,nom,classe\nLéa,Durant,P4\nTom,Bernard,P3",
			[]importRow{{"Léa", "D.", "P4", false}, {"Tom", "B.", "P3", false}}},
		{"semicolon", "prenom;nom;classe\nLéa;Durant;P4",
			[]importRow{{"Léa", "D.", "P4", false}}},
		{"no header", "Léa,Durant,P4", []importRow{{"Léa", "D.", "P4", false}}},
		{"CRLF", "Léa,Durant,P4\r\nTom,Bernard,P3\r\n",
			[]importRow{{"Léa", "D.", "P4", false}, {"Tom", "B.", "P3", false}}},
		{"quoted field containing a comma", "\"Durant, Léa\",Durant,P4",
			[]importRow{{"Durant, Léa", "D.", "P4", false}}},
		{"blank lines ignored", "Léa,Durant,P4\n\n\nTom,Bernard,P3",
			[]importRow{{"Léa", "D.", "P4", false}, {"Tom", "B.", "P3", false}}},
		{"class missing", "Léa,Durant", []importRow{{"Léa", "D.", "", false}}},
		{"stray spaces", " Léa , Durant , P4 ",
			[]importRow{{"Léa", "D.", "P4", false}}},
		{"empty", "", nil},
		{"header only", "prénom,nom,classe", nil},
		{"Excel's BOM before the header", "\ufeffprénom;nom;classe\nLéa;Durant;P4",
			[]importRow{{"Léa", "D.", "P4", false}}},
		{"cells pasted from a spreadsheet", "Léa\tDurant\tP4\nTom\tBernard\tP3",
			[]importRow{{"Léa", "D.", "P4", false}, {"Tom", "B.", "P3", false}}},
		{"a line without a last name is skipped", "Léa Durant\nTom,Bernard,P3",
			[]importRow{{"Tom", "B.", "P3", false}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, _ := parseCSV(c.in)
			if len(got) != len(c.want) {
				t.Fatalf("%d row(s), want %d: %+v", len(got), len(c.want), got)
			}
			for i := range c.want {
				if got[i] != c.want[i] {
					t.Errorf("row %d: %+v, want %+v", i, got[i], c.want[i])
				}
			}
		})
	}
}

// A full name typed into one cell must not be stored whole as a first name.
func TestParseCSVCountsLinesWithoutBothNames(t *testing.T) {
	rows, skipped := parseCSV("Léa Durant,,P4\n,Bernard,P3\nTom,Bernard,P3")
	if len(rows) != 1 || skipped != 2 {
		t.Fatalf("rows=%+v skipped=%d, want 1 row and 2 skipped", rows, skipped)
	}
	rows, skipped = parseCSV("prénom,nom,classe\n\nLéa,Durant,P4")
	if len(rows) != 1 || skipped != 0 {
		t.Errorf("header and blank line: rows=%d skipped=%d, want 1 and 0", len(rows), skipped)
	}
}

func TestIsHeader(t *testing.T) {
	for _, v := range []string{"prénom", "Prenom", " PRÉNOM ", "first name", "voornaam"} {
		if !isHeader(v) {
			t.Errorf("isHeader(%q) = false, want true", v)
		}
	}
	for _, v := range []string{"Léa", "", "Durant"} {
		if isHeader(v) {
			t.Errorf("isHeader(%q) = true, want false", v)
		}
	}
}

// lastNameInitial is the minimisation guarantee: the full last name must
// never leave this function.
func TestLastNameInitialKeepsOnlyTheInitial(t *testing.T) {
	for _, lastName := range []string{"Durant", "de la Fontaine", "Ó Briain", "École"} {
		got := lastNameInitial(lastName)
		if len([]rune(got)) != 2 || got[len(got)-1] != '.' {
			t.Errorf("lastNameInitial(%q) = %q: want an initial followed by a full stop", lastName, got)
		}
	}
}

// --- The borrower lists ---------------------------------------------------
//
// The fixture is app/testdata/fixture.sql (see testDB): Léa D. (P4), Tom B. (P3),
// Zoé P. (P4), Noah M. (P3) and Claire L., a teacher with no class. Tom has one
// overdue loan, Léa one on time.

func TestListBorrowersFiltered(t *testing.T) {
	loadForTest(t)
	restoreSettings(t)
	a := testApp(t)
	setAnonymousID(anonymousBorrowerID(a.db))

	all, err := a.listBorrowersFiltered(brFilter{})
	if err != nil {
		t.Fatalf("listBorrowersFiltered: %v", err)
	}
	if len(all) != 5 {
		t.Fatalf("%d borrowers, want 5", len(all))
	}
	// The sentinel borrower of anonymised loans is not a person and must never
	// show up in a list, a search or a card sheet.
	for _, e := range all {
		if e.ID == anonymousID() {
			t.Error("the anonymisation sentinel is listed as a borrower")
		}
	}

	cases := []struct {
		q     string
		class string
		want  int
		why   string
	}{
		{"", "P4", 2, "class filter"},
		{"", "P3", 2, "class filter"},
		{"", "", 5, "no filter"},
		{"léa", "", 1, "first name"},
		{"LÉA", "", 1, "case does not matter"},
		{"P3", "", 2, "class as a search word"},
		{"m.", "", 1, "last-name initial"},
		{"léa", "P3", 0, "the class filter and the words both apply"},
		// A card scanned into the search box finds its borrower.
		{"LEC73048", "", 1, "a pupil's card code, as a scanner types it"},
		{"lec73048", "", 1, "case does not matter for a code either"},
		{"LEC40275", "", 1, "a teacher's card code"},
		{"LEC73048", "P3", 0, "the class filter still applies to a scan"},
		{"zzz", "", 0, "no match"},
	}
	for _, c := range cases {
		got, err := a.listBorrowersFiltered(brFilter{Q: c.q, Class: c.class})
		if err != nil {
			t.Fatalf("listBorrowersFiltered(%q, %q): %v", c.q, c.class, err)
		}
		if len(got) != c.want {
			t.Errorf("listBorrowersFiltered(%q, class=%q) = %d, want %d (%s)",
				c.q, c.class, len(got), c.want, c.why)
		}
	}
}

// A deactivated pupil leaves the list without leaving the database: GDPR rules forbid
// deleting, and a mistaken deactivation has to be undoable.
func TestListBorrowersSeparatesTheInactive(t *testing.T) {
	loadForTest(t)
	restoreSettings(t)
	a := testApp(t)
	setAnonymousID(anonymousBorrowerID(a.db))

	if _, err := a.db.Exec(
		`UPDATE borrower SET active = 0, deactivated_on = date('now') WHERE id = 103`); err != nil {
		t.Fatal(err)
	}

	active, err := a.listBorrowersFiltered(brFilter{})
	if err != nil {
		t.Fatalf("listBorrowersFiltered: %v", err)
	}
	if len(active) != 4 {
		t.Errorf("%d active borrowers, want 4", len(active))
	}
	inactive, err := a.listBorrowersFiltered(brFilter{Inactive: true})
	if err != nil {
		t.Fatalf("listBorrowersFiltered: %v", err)
	}
	if len(inactive) != 1 || inactive[0].FirstName != "Zoé" {
		t.Errorf("inactive list: %+v", inactive)
	}
	if inactive[0].Active {
		t.Error("a deactivated borrower is reported as active")
	}
}

// The order a column heading asks for reaches us from a form and is
// concatenated into SQL, so it may only ever be a key of the whitelist.
func TestBrOrderBy(t *testing.T) {
	if got := brOrderBy(brFilter{}); got != brStandingOrder {
		t.Errorf("an unsorted list = %q, want the standing order", got)
	}
	for _, bad := range []string{"nope", "br.first_name", "1; DROP TABLE borrower", ""} {
		if got := brOrderBy(brFilter{Sort: bad}); got != brStandingOrder {
			t.Errorf("sort=%q reached the SQL: %q", bad, got)
		}
	}
	if got := brOrderBy(brFilter{Sort: "overdue", Dir: "desc"}); !strings.Contains(got, "overdue_count DESC") {
		t.Errorf("sort by the overdue count, descending = %q", got)
	}
	// An unknown direction is ascending, not an error.
	if got := brOrderBy(brFilter{Sort: "name", Dir: "sideways"}); strings.Contains(got, "DESC") {
		t.Errorf("dir=sideways produced %q", got)
	}
}

// Sorting by the counts works only because the subqueries are named.
func TestListBorrowersSorted(t *testing.T) {
	loadForTest(t)
	restoreSettings(t)
	a := testApp(t)
	setAnonymousID(anonymousBorrowerID(a.db))

	first := func(t *testing.T, f brFilter) BorrowerRow {
		t.Helper()
		got, err := a.listBorrowersFiltered(f)
		if err != nil {
			t.Fatalf("listBorrowersFiltered: %v", err)
		}
		if len(got) == 0 {
			t.Fatal("no borrower listed")
		}
		return got[0]
	}

	// Tom is the one with a book overdue in the fixture.
	if e := first(t, brFilter{Sort: "overdue", Dir: "desc"}); e.OverdueCount == 0 {
		t.Errorf("sorted by who is most overdue, the list opens on %s %s with none",
			e.FirstName, e.LastInitial)
	}
	// And the same heading the other way up opens on someone who owes nothing.
	if e := first(t, brFilter{Sort: "overdue", Dir: "asc"}); e.OverdueCount != 0 {
		t.Errorf("ascending, the list opens on %s %s with %d overdue",
			e.FirstName, e.LastInitial, e.OverdueCount)
	}

	byName, err := a.listBorrowersFiltered(brFilter{Sort: "name"})
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, len(byName))
	for i, e := range byName {
		names[i] = e.FirstName
	}
	if !sort.StringsAreSorted(names) {
		t.Errorf("sorted by name, the list came back as %v", names)
	}
	// Reversing the heading reverses the list, rather than sorting it again.
	byNameDesc, err := a.listBorrowersFiltered(brFilter{Sort: "name", Dir: "desc"})
	if err != nil {
		t.Fatal(err)
	}
	for i, e := range byNameDesc {
		if want := byName[len(byName)-1-i].FirstName; e.FirstName != want {
			t.Errorf("descending, row %d is %s, want %s", i, e.FirstName, want)
			break
		}
	}
}

// A list longer than a page comes in pages of pageSize, the heading counts
// the whole of it, and an offset past the end lands on the last page rather
// than on an empty one.
func TestPageBorrowers(t *testing.T) {
	loadForTest(t)
	restoreSettings(t)
	a := testApp(t)
	setAnonymousID(anonymousBorrowerID(a.db))

	tx, err := a.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2*pageSize+5; i++ {
		if _, err := tx.Exec(
			`INSERT INTO borrower (first_name, last_initial, class, kind, card_code, active)
			 VALUES (?, 'X.', 'P6', 'student', ?, 1)`,
			fmt.Sprintf("Élève%d", i), fmt.Sprintf("LEC-9%03d", i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	all, err := a.listBorrowersFiltered(brFilter{})
	if err != nil {
		t.Fatalf("listBorrowersFiltered: %v", err)
	}
	if len(all) <= 2*pageSize {
		t.Fatalf("%d borrowers in all, want more than two pages", len(all))
	}

	first, pg, err := a.pageBorrowers(brFilter{}, 0)
	if err != nil {
		t.Fatalf("pageBorrowers: %v", err)
	}
	if len(first) != pageSize || pg.Total != len(all) || pg.Offset != 0 || !pg.HasNext() || pg.HasPrev() {
		t.Errorf("first page: %d rows, total %d, offset %d, prev %v, next %v",
			len(first), pg.Total, pg.Offset, pg.HasPrev(), pg.HasNext())
	}
	for i, e := range first {
		if e.ID != all[i].ID {
			t.Fatalf("row %d of the first page is %q, the list has %q", i, e.FirstName, all[i].FirstName)
		}
	}

	second, pg, err := a.pageBorrowers(brFilter{}, pageSize)
	if err != nil {
		t.Fatalf("pageBorrowers (second): %v", err)
	}
	if len(second) != pageSize || second[0].ID != all[pageSize].ID || !pg.HasPrev() {
		t.Errorf("second page: %d rows starting at %q, want %d starting at %q",
			len(second), second[0].FirstName, pageSize, all[pageSize].FirstName)
	}

	// Past the end: the last page, full or not, never an empty one.
	last, pg, err := a.pageBorrowers(brFilter{}, 50*pageSize)
	if err != nil {
		t.Fatalf("pageBorrowers (past the end): %v", err)
	}
	if want := len(all) - 2*pageSize; len(last) != want || pg.Offset != 2*pageSize || pg.HasNext() {
		t.Errorf("past the end: %d rows at offset %d, next %v; want %d at %d",
			len(last), pg.Offset, pg.HasNext(), want, 2*pageSize)
	}

	// A filter that selects one page shows no turner.
	few, pg, err := a.pageBorrowers(brFilter{Q: "Élève1"}, 0)
	if err != nil {
		t.Fatalf("pageBorrowers (search): %v", err)
	}
	if pg.Total != len(few) || pg.Paged() && len(few) <= pageSize {
		t.Errorf("search: %d rows, total %d, paged %v", len(few), pg.Total, pg.Paged())
	}
}

// The counters drive the whole borrower screen: what is out, what is late.
func TestBorrowerCounters(t *testing.T) {
	a := testApp(t)

	tom, err := a.loadBorrower(102)
	if err != nil {
		t.Fatalf("loadBorrower: %v", err)
	}
	if tom.FirstName != "Tom" || tom.LastInitial != "B." || tom.Class != "P3" {
		t.Errorf("borrower: %+v", tom)
	}
	if tom.TotalCount != 1 || tom.OutCount != 1 || tom.OverdueCount != 1 {
		t.Errorf("Tom: total %d, out %d, overdue %d — want 1, 1, 1",
			tom.TotalCount, tom.OutCount, tom.OverdueCount)
	}

	// Léa's loan is on time: out, not overdue. Red is for overdue alone.
	lea, err := a.loadBorrower(101)
	if err != nil {
		t.Fatalf("loadBorrower: %v", err)
	}
	if lea.OutCount != 1 || lea.OverdueCount != 0 {
		t.Errorf("Léa: out %d, overdue %d — want 1 and 0", lea.OutCount, lea.OverdueCount)
	}

	// A returned book still counts towards the total, and no longer as out.
	if _, err := a.db.Exec(
		`UPDATE loan SET returned_on = date('now') WHERE borrower_id = 101 AND returned_on IS NULL`); err != nil {
		t.Fatal(err)
	}
	lea, _ = a.loadBorrower(101)
	if lea.TotalCount != 1 || lea.OutCount != 0 {
		t.Errorf("after the return: total %d, out %d — want 1 and 0", lea.TotalCount, lea.OutCount)
	}
}

func TestLoadBorrowerUnknownID(t *testing.T) {
	a := testApp(t)
	if _, err := a.loadBorrower(9999); err == nil {
		t.Error("an unknown borrower loaded without error")
	}
}

// Teachers have no class and must not turn up as one in the filter tabs.
func TestListClasses(t *testing.T) {
	loadForTest(t)
	restoreSettings(t)
	a := testApp(t)
	setAnonymousID(anonymousBorrowerID(a.db))

	classes, err := a.listClasses(true)
	if err != nil {
		t.Fatalf("listClasses: %v", err)
	}
	// The classless are one of the entries, so teachers can be reached.
	want := map[string]int{"P3": 2, "P4": 2, "": 1}
	if len(classes) != len(want) {
		t.Fatalf("classes = %+v, want %v", classes, want)
	}
	for _, c := range classes {
		if want[c.Class] != c.Count {
			t.Errorf("class %q: %d pupils, want %d", c.Class, c.Count, want[c.Class])
		}
	}
	// Sorted, with the classless last.
	if classes[0].Class != "P3" {
		t.Errorf("classes not sorted: %+v", classes)
	}
	if classes[len(classes)-1].Class != "" {
		t.Errorf("the classless are not last: %+v", classes)
	}
	// The anonymisation sentinel has no class and is not a person: it must not
	// be counted among them.
	for _, c := range classes {
		if c.Class == "" && c.Count != 1 {
			t.Errorf("the classless count is %d, want 1 (the teacher alone)", c.Count)
		}
	}
}

// A second import of the same list must not duplicate pupils.
func TestExistingBorrowersFeedsTheDuplicateCheck(t *testing.T) {
	a := testApp(t)

	existing, err := a.existingBorrowers()
	if err != nil {
		t.Fatalf("existingBorrowers: %v", err)
	}
	if !existing[borrowerKey("Léa", "D.", "P4")] {
		t.Error("a pupil on file was not recognised")
	}
	// The key is built on the initial, which is all that is stored, and
	// ignores case and stray spaces — a September list has both.
	if !existing[borrowerKey("  léa  ", "d.", "P4")] {
		t.Error("the key is sensitive to case or spacing")
	}
	// The same first name in another class is another child.
	if existing[borrowerKey("Léa", "D.", "P5")] {
		t.Error("the class is not part of the key")
	}

	rows := []importRow{
		{FirstName: "Léa", LastInitial: "D.", Class: "P4"},  // already on file
		{FirstName: "Nina", LastInitial: "V.", Class: "P4"}, // new
		{FirstName: "Nina", LastInitial: "V.", Class: "P4"}, // repeated in the file
	}
	if n := markDuplicates(rows, existing); n != 2 {
		t.Errorf("%d duplicates found, want 2", n)
	}
	if !rows[0].Duplicate || rows[1].Duplicate || !rows[2].Duplicate {
		t.Errorf("flags: %+v", rows)
	}
}

// A deactivated pupil is not on file for the import: re-importing them is how
// a child who comes back is re-entered.
func TestExistingBorrowersIgnoresTheInactive(t *testing.T) {
	a := testApp(t)
	if _, err := a.db.Exec(`UPDATE borrower SET active = 0 WHERE id = 101`); err != nil {
		t.Fatal(err)
	}
	existing, err := a.existingBorrowers()
	if err != nil {
		t.Fatalf("existingBorrowers: %v", err)
	}
	if existing[borrowerKey("Léa", "D.", "P4")] {
		t.Error("a deactivated pupil blocks their own re-entry")
	}
}

// Pupils and teachers carry the same kind of card (codes.go): the kind is a
// column, and a card cannot be reprinted every time it changes.
func TestCardCodesAreTheSameForEveryone(t *testing.T) {
	a := testApp(t)
	tx, err := a.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()

	code, err := freshCardCode(tx)
	if err != nil {
		t.Fatalf("freshCardCode: %v", err)
	}
	if !reCardCode.MatchString(code) || misreadCode(code) {
		t.Errorf("card = %q, want LEC, four digits and a check digit", code)
	}
}

// The draw must not hand out a code the database already holds.
func TestCardCodesAvoidWhatIsAlreadyTaken(t *testing.T) {
	a := testApp(t)
	tx, err := a.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()

	seen := make(map[string]bool)
	// The demo fixture's five cards are already on file.
	rows, err := tx.Query(`SELECT card_code FROM borrower WHERE card_code IS NOT NULL`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			t.Fatal(err)
		}
		seen[c] = true
	}
	rows.Close()

	for i := 0; i < 100; i++ {
		code, err := freshCardCode(tx)
		if err != nil {
			t.Fatalf("draw %d: %v", i, err)
		}
		if seen[code] {
			t.Fatalf("drew a code already taken: %q", code)
		}
		seen[code] = true
		if _, err := tx.Exec(
			`INSERT INTO borrower (first_name, last_initial, kind, card_code, active)
			 VALUES ('Nina', 'V.', 'student', ?, 1)`, code); err != nil {
			t.Fatalf("insert %s: %v", code, err)
		}
	}
}

// The rollover reasons on a snapshot, and the snapshot must carry what stops a
// leaver being deactivated: the books still in their hands.
func TestPupilsSnapshot(t *testing.T) {
	a := testApp(t)

	pupils, err := pupilsSnapshot(a.db)
	if err != nil {
		t.Fatalf("pupilsSnapshot: %v", err)
	}
	// Claire is a teacher: the rollover is about classes moving up.
	if len(pupils) != 4 {
		t.Fatalf("%d pupils, want 4 (the teacher is not one)", len(pupils))
	}
	byName := make(map[string]rolloverPupil, len(pupils))
	for _, p := range pupils {
		byName[p.FirstName] = p
	}
	if byName["Tom"].OutCount != 1 {
		t.Errorf("Tom still has a book out, OutCount = %d", byName["Tom"].OutCount)
	}
	if byName["Zoé"].OutCount != 0 {
		t.Errorf("Zoé has nothing out, OutCount = %d", byName["Zoé"].OutCount)
	}
	if byName["Tom"].Class != "P3" {
		t.Errorf("Tom's class: %q, want P3", byName["Tom"].Class)
	}
}

// HTMX actions do not carry the class filter, so it is recovered from the URL
// the browser shows — which HTMX sends as a header.
func TestCurrentClass(t *testing.T) {
	// The form field wins when there is one.
	r := httptest.NewRequest("POST", "/borrowers/search", strings.NewReader("class=P4"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("HX-Current-URL", "https://bibli.example.org/borrowers?class=P3")
	if got := currentClass(r); got != "P4" {
		t.Errorf("currentClass = %q, want P4 (the posted field)", got)
	}

	// Otherwise the class comes off the URL the browser is showing.
	r = httptest.NewRequest("POST", "/borrowers/search", nil)
	r.Header.Set("HX-Current-URL", "https://bibli.example.org/borrowers?class=P3")
	if got := currentClass(r); got != "P3" {
		t.Errorf("currentClass = %q, want P3 (from HX-Current-URL)", got)
	}

	// No filter anywhere: everything.
	r = httptest.NewRequest("POST", "/borrowers/search", nil)
	if got := currentClass(r); got != "" {
		t.Errorf("currentClass = %q, want empty", got)
	}
	// A header that is not a URL must not take the screen down.
	r = httptest.NewRequest("POST", "/borrowers/search", nil)
	r.Header.Set("HX-Current-URL", "://not a url")
	if got := currentClass(r); got != "" {
		t.Errorf("currentClass = %q on a malformed header, want empty", got)
	}
}

// The add form posts a "class" of its own — the new pupil's — and the list it
// swaps in must stay on the class the screen was filtered to.
func TestUrlClassIgnoresThePostedField(t *testing.T) {
	r := httptest.NewRequest("POST", "/borrowers/add", strings.NewReader("first_name=Léa&last_name=Durant&class=P4"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("HX-Current-URL", "https://bibli.example.org/borrowers?class=P3")
	if got := urlClass(r); got != "P3" {
		t.Errorf("urlClass = %q, want P3 (the filter, not the new borrower's class)", got)
	}

	// An unfiltered list stays unfiltered.
	r = httptest.NewRequest("POST", "/borrowers/add", strings.NewReader("class=P4"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("HX-Current-URL", "https://bibli.example.org/borrowers")
	if got := urlClass(r); got != "" {
		t.Errorf("urlClass = %q, want empty", got)
	}
}

// The list arrives pasted or as a file; both paths end in the same parser.
func TestReadIncomingCSV(t *testing.T) {
	const list = "Prénom;Nom;Classe\nLéa;Durant;P4\nTom;Bastin;P3\n"

	// Pasted into the text box.
	r := httptest.NewRequest("POST", "/borrowers/import/preview",
		strings.NewReader("csv="+url.QueryEscape(list)))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if got := readIncomingCSV(r); got != list {
		t.Errorf("pasted CSV: %q", got)
	}

	// Uploaded as a file, which wins: choosing a file and leaving an old paste
	// in the box must send the file.
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	if err := mw.WriteField("csv", "stale paste"); err != nil {
		t.Fatal(err)
	}
	part, err := mw.CreateFormFile("file", "eleves.csv")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte(list)); err != nil {
		t.Fatal(err)
	}
	mw.Close()

	r = httptest.NewRequest("POST", "/borrowers/import/preview", &body)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	if got := readIncomingCSV(r); got != list {
		t.Errorf("uploaded file: %q", got)
	}

	// An empty file falls back to the text box rather than importing nothing.
	body.Reset()
	mw = multipart.NewWriter(&body)
	mw.WriteField("csv", list)
	part, _ = mw.CreateFormFile("file", "vide.csv")
	part.Write(nil)
	mw.Close()
	r = httptest.NewRequest("POST", "/borrowers/import/preview", &body)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	if got := readIncomingCSV(r); got != list {
		t.Errorf("empty file: the pasted list was dropped (%q)", got)
	}

	// And what comes out of either path only ever carries the initial.
	rows, _ := parseCSV(readIncomingCSV(r))
	for _, row := range rows {
		if len(row.LastInitial) > 2 {
			t.Errorf("a full last name survived the import: %q", row.LastInitial)
		}
	}
}

// Pupils with no class are grouped under a label, which is shown on screen and
// posted back by the rollover form — so it follows the instance language.
func TestNoClassLabel(t *testing.T) {
	loadForTest(t)
	restoreSettings(t)

	setLang("fr")
	fr := noClassLabel()
	setLang("en")
	en := noClassLabel()

	if fr == "" || en == "" {
		t.Fatalf("empty label: fr %q, en %q", fr, en)
	}
	if fr == en {
		t.Errorf("the label does not follow the language: %q in both", fr)
	}
	if fr == "borrower.no_class" {
		t.Error("the key is showing through instead of the string")
	}
}

// "Every class" is the empty value, so the classless need one of their own.
func TestClassFilter(t *testing.T) {
	cases := []struct {
		in     string
		value  string
		filter bool
		why    string
	}{
		{"", "", false, "the dropdown's own \"every class\""},
		{"   ", "", false, "whitespace is still every class"},
		{classFilterNone, "", true, "the borrowers with no class"},
		{"P3", "P3", true, "one class"},
		{" P3 ", "P3", true, "trimmed"},
	}
	for _, c := range cases {
		value, filter := classFilter(c.in)
		if value != c.value || filter != c.filter {
			t.Errorf("classFilter(%q) = (%q, %v), want (%q, %v) — %s",
				c.in, value, filter, c.value, c.filter, c.why)
		}
	}
	// The two must not collapse into each other: every class and no class are
	// different screens.
	_, allFilters := classFilter("")
	_, noneFilters := classFilter(classFilterNone)
	if allFilters == noneFilters {
		t.Error("every class and the classless produce the same filter")
	}
}

// The card sheet reads the dropdown's "-" as the borrowers who have no class.
func TestListCardsFollowsTheClassFilter(t *testing.T) {
	a := testApp(t)

	all, err := a.listCards("")
	if err != nil {
		t.Fatalf("listCards: %v", err)
	}
	if len(all) != 5 {
		t.Fatalf("%d cards without a filter, want 5", len(all))
	}

	p3, err := a.listCards("P3")
	if err != nil {
		t.Fatalf("listCards(P3): %v", err)
	}
	if len(p3) != 2 {
		t.Errorf("%d cards for P3, want 2", len(p3))
	}

	none, err := a.listCards(classFilterNone)
	if err != nil {
		t.Fatalf("listCards(%q): %v", classFilterNone, err)
	}
	if len(none) != 1 || none[0].FirstName != "Claire" {
		t.Errorf("cards for the classless = %+v, want the teacher alone", none)
	}
}

// The edit form on a borrower's own page posts backField, or the handler
// answers with a bare <tr> instead of a redirect.
func TestBorrowerPageEditPostsTheFieldTheHandlerReads(t *testing.T) {
	content, err := os.ReadFile("templates/borrower.html")
	if err != nil {
		t.Fatal(err)
	}
	form := regexp.MustCompile(`(?s)<form[^>]*action="/borrowers/\{\{[^"]*\}\}"[^>]*>(.*?)</form>`)
	hidden := regexp.MustCompile(`<input[^>]*type="hidden"[^>]*name="([^"]+)"`)

	found := form.FindAllStringSubmatch(string(content), -1)
	if len(found) != 1 {
		t.Fatalf("%d forms posting to a borrower on their own page, want the one that edits them", len(found))
	}
	var names []string
	for _, h := range hidden.FindAllStringSubmatch(found[0][1], -1) {
		names = append(names, h[1])
	}
	if !slices.Contains(names, backField) {
		t.Errorf("the edit form posts %v, and the handler reads %q to know where to go back to",
			names, backField)
	}
}

// Posting the same rollover form twice (a reload, a second tab) must not move
// the school up twice: the second post finds the classes changed and refuses.
func TestRolloverRefusesAStaleForm(t *testing.T) {
	loadForTest(t)
	a := testApp(t)
	sets, err := loadTemplates()
	if err != nil {
		t.Fatalf("loading the templates: %v", err)
	}
	a.pages = sets

	pupils, err := pupilsSnapshot(a.db)
	if err != nil {
		t.Fatalf("pupilsSnapshot: %v", err)
	}
	classOf := func(name string) string {
		t.Helper()
		var c string
		if err := a.db.QueryRow(`SELECT COALESCE(class, '') FROM borrower WHERE first_name = ?`, name).Scan(&c); err != nil {
			t.Fatalf("class of %s: %v", name, err)
		}
		return c
	}
	if classOf("Tom") != "P3" {
		t.Fatalf("fixture: Tom is in %q, want P3", classOf("Tom"))
	}
	// The same form posted twice, as a reload resends it.
	form := url.Values{"n": {"2"}, "src_0": {"P3"}, "dst_0": {"P4"},
		"src_1": {"P4"}, "dst_1": {"P5"}, "state": {rolloverState(pupils)}}
	post := func() {
		r := httptest.NewRequest("POST", "/borrowers/rollover", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		a.rolloverConfirm(w, r)
		if w.Code != 200 {
			t.Fatalf("status %d: %s", w.Code, w.Body)
		}
	}

	post()
	if got := classOf("Tom"); got != "P4" {
		t.Fatalf("first post: Tom in %q, want P4", got)
	}
	post()
	if got := classOf("Tom"); got != "P4" {
		t.Errorf("a stale form moved Tom again, to %q", got)
	}
}

// Reactivation brings back a pupil who left, never the sentinel nor a record
// the purge has emptied; deactivating twice keeps the first date, which
// the retention period counts from.
func TestReactivationGuards(t *testing.T) {
	loadForTest(t)
	restoreSettings(t)
	a := testApp(t)
	sets, err := loadTemplates()
	if err != nil {
		t.Fatalf("loading the templates: %v", err)
	}
	a.pages = sets
	anon := anonymousBorrowerID(a.db)
	setAnonymousID(anon)

	post := func(id int64, action string) {
		t.Helper()
		path := fmt.Sprintf("/borrowers/%d/%s", id, action)
		r := httptest.NewRequest("POST", path, nil)
		r.SetPathValue("id", fmt.Sprint(id))
		w := httptest.NewRecorder()
		if action == "reactivate" {
			a.borrowerReactivate(w, r)
		} else {
			a.borrowersDeactivate(w, r)
		}
		if w.Code != 200 {
			t.Fatalf("POST %s: status %d", path, w.Code)
		}
	}
	state := func(id int64) (active bool, since string) {
		t.Helper()
		var on sql.NullString
		if err := a.db.QueryRow(`SELECT active, deactivated_on FROM borrower WHERE id = ?`, id).Scan(&active, &on); err != nil {
			t.Fatal(err)
		}
		return active, on.String
	}

	// Zoé (103) left two years ago; a second click must not restart her clock.
	if _, err := a.db.Exec(`UPDATE borrower SET active = 0, deactivated_on = date('now', '-2 years') WHERE id = 103`); err != nil {
		t.Fatal(err)
	}
	_, before := state(103)
	post(103, "deactivate")
	if _, after := state(103); after != before {
		t.Errorf("deactivating again moved the date from %s to %s", before, after)
	}

	// And she can still come back.
	post(103, "reactivate")
	if active, _ := state(103); !active {
		t.Error("a deactivated pupil could not be reactivated")
	}

	// Once the purge has emptied her record, there is no one to bring back.
	if _, err := a.db.Exec(`UPDATE borrower SET active = 0, deactivated_on = date('now', '-4 years') WHERE id = 103`); err != nil {
		t.Fatal(err)
	}
	gdprPurge(a.db)
	if e, _ := a.loadBorrower(103); !e.Anonymised() {
		t.Fatalf("fixture: the purge did not anonymise Zoé: %+v", e)
	}
	post(103, "reactivate")
	if active, _ := state(103); active {
		t.Error("an anonymised borrower was reactivated")
	}

	post(anon, "reactivate")
	if active, _ := state(anon); active {
		t.Error("the anonymisation sentinel was reactivated")
	}
}

// Excel on Windows saves a French "CSV" in Windows-1252: its bytes are not
// UTF-8, and read as such every accent becomes U+FFFD.
func TestDecodeCSVBytesReadsWindows1252(t *testing.T) {
	cases := []struct {
		in   []byte
		want string
	}{
		{[]byte("\xc9mile;Fran\xe7ois;P4"), "Émile;François;P4"},
		{[]byte("L\xe9a;H\xe8gre;P3"), "Léa;Hègre;P3"},
		{[]byte("Z\x9c;\x80uro;\x8c"), "Zœ;€uro;Œ"},
		{[]byte("Léa;Durant;P4"), "Léa;Durant;P4"}, // UTF-8 stays as it is
	}
	for _, c := range cases {
		if got := decodeCSVBytes(c.in); got != c.want {
			t.Errorf("decodeCSVBytes(%q) = %q, want %q", c.in, got, c.want)
		}
	}

	// Through the upload, down to the rows the preview shows.
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("file", "eleves.csv")
	if err != nil {
		t.Fatal(err)
	}
	part.Write([]byte("Pr\xe9nom;Nom;Classe\r\n\xc9mile;\xc9tienne;P4\r\n"))
	mw.Close()
	r := httptest.NewRequest("POST", "/borrowers/import", &body)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	rows, _ := parseCSV(readIncomingCSV(r))
	if len(rows) != 1 || rows[0] != (importRow{"Émile", "É.", "P4", false}) {
		t.Errorf("a Windows-1252 file imports as %+v", rows)
	}
}

// The empty sheet is a workbook Excel opens, headed in the instance language
// with words the import skips, so pasting it whole imports the pupils only.
func TestImportTemplateRoundTrips(t *testing.T) {
	loadForTest(t)
	a := testApp(t)
	for _, lang := range langs {
		r := httptest.NewRequest("GET", "/borrowers/import/template.xlsx", nil)
		r = r.WithContext(context.WithValue(r.Context(), langKey{}, lang))
		w := httptest.NewRecorder()
		a.borrowersImportTemplate(w, r)

		name := T(lang, "import.template_file")
		if !regexp.MustCompile(`^[a-z0-9-]+$`).MatchString(name) {
			t.Errorf("%s: file name %q must stay plain ASCII to sit in Content-Disposition", lang, name)
		}
		if got := w.Header().Get("Content-Disposition"); !strings.Contains(got, `filename="`+name+`.xlsx"`) {
			t.Errorf("%s: Content-Disposition = %q", lang, got)
		}
		parts := unzipParts(t, w.Body.Bytes())
		for _, want := range []string{"[Content_Types].xml", "xl/workbook.xml", "xl/styles.xml", "xl/worksheets/sheet1.xml"} {
			if _, ok := parts[want]; !ok {
				t.Errorf("%s: the workbook has no %s", lang, want)
			}
		}
		header := importHeader(lang)
		sh := parts["xl/worksheets/sheet1.xml"]
		for _, h := range header {
			if !strings.Contains(sh, ">"+h+"<") {
				t.Errorf("%s: the sheet has no heading %q", lang, h)
			}
		}
		if strings.Contains(sh, `<row r="2"`) {
			t.Errorf("%s: the empty sheet carries a data row", lang)
		}
		if !isHeader(header[0]) {
			t.Errorf("%s: isHeader(%q) = false, so a pasted header row would import as a pupil", lang, header[0])
		}

		// The sheet filled in and pasted back: cells arrive tab-separated.
		pasted := strings.Join(header, "\t") + "\nLéa\tDurant\tP4\nTom\tBernard\tP3\n"
		rows, skipped := parseCSV(pasted)
		want := []importRow{{"Léa", "D.", "P4", false}, {"Tom", "B.", "P3", false}}
		if !slices.Equal(rows, want) || skipped != 0 {
			t.Errorf("%s: the pasted sheet imports as %+v (%d skipped), want %+v", lang, rows, skipped, want)
		}

		// Uploaded as it is, it is refused in words rather than previewed.
		if !isZip(w.Body.String()) {
			t.Errorf("%s: the workbook is not recognised as one", lang)
		}
	}
}

// Deactivating is refused while a borrower still has a book out — and the check
// is part of the UPDATE, so it holds even against a loan recorded at the very
// same moment (the old code counted, then updated, with a gap between).
func TestDeactivationRefusedWhileBooksAreOut(t *testing.T) {
	loadForTest(t)
	restoreSettings(t)
	a := testApp(t)
	sets, err := loadTemplates()
	if err != nil {
		t.Fatalf("loading the templates: %v", err)
	}
	a.pages = sets
	setAnonymousID(anonymousBorrowerID(a.db))

	deactivate := func(id int64) {
		t.Helper()
		r := httptest.NewRequest("POST", fmt.Sprintf("/borrowers/%d/deactivate", id), nil)
		r.SetPathValue("id", fmt.Sprint(id))
		w := httptest.NewRecorder()
		a.borrowersDeactivate(w, r)
		if w.Code != 200 {
			t.Fatalf("deactivate %d: status %d", id, w.Code)
		}
	}
	active := func(id int64) bool {
		t.Helper()
		var on bool
		if err := a.db.QueryRow(`SELECT active FROM borrower WHERE id = ?`, id).Scan(&on); err != nil {
			t.Fatal(err)
		}
		return on
	}

	// Léa (101) has copy 3 out: deactivation must not go through.
	deactivate(101)
	if !active(101) {
		t.Error("a borrower with a book out was deactivated")
	}
	// Zoé (103) has nothing out: deactivation succeeds.
	deactivate(103)
	if active(103) {
		t.Error("a borrower with no books out was not deactivated")
	}
}
