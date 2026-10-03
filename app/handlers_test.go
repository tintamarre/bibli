package main

import (
	stdhtml "html"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Every template must parse and define either "content" or "document".
func TestLoadTemplates(t *testing.T) {
	loadForTest(t)
	sets, err := loadTemplates()
	if err != nil {
		t.Fatalf("loading the templates: %v", err)
	}
	// One complete set per language.
	for _, lang := range langs {
		pages, ok := sets[lang]
		if !ok || len(pages) == 0 {
			t.Fatalf("%s: no page loaded", lang)
		}
		for name, tpl := range pages {
			if tpl.Lookup("content") == nil && tpl.Lookup("document") == nil {
				t.Errorf("%s/%s: neither \"content\" nor \"document\" defined", lang, name)
			}
		}
		// These screens redefine their width (the "width" block of base.html).
		for _, name := range []string{"borrow", "return", "settings"} {
			if pages[name].Lookup("width") == nil {
				t.Errorf("%s/%s: \"width\" block expected", lang, name)
			}
		}
	}
}

// A tag bearing a positional hx-swap-oob ("beforeend:#basket") is a wrapper:
// HTMX swaps in its children and drops the tag, so it must carry no id or group.
func TestPositionalOOBSwapsAreOnWrappers(t *testing.T) {
	files, err := filepath.Glob("templates/*.html")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no template found")
	}
	// The whole opening tag, so the other attributes on it can be inspected.
	tag := regexp.MustCompile(`(?s)<[a-zA-Z][^>]*\bhx-swap-oob\s*=\s*"([^"]*)"[^>]*>`)
	positional := regexp.MustCompile(`^(beforebegin|afterbegin|beforeend|afterend)\s*:`)
	carries := regexp.MustCompile(`\b(id|group)\s*=`)

	for _, path := range files {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range tag.FindAllStringSubmatch(string(content), -1) {
			if !positional.MatchString(m[1]) {
				continue
			}
			if carries.MatchString(m[0]) {
				t.Errorf("%s: %q swaps in this tag's children, so its own id/group are dropped — "+
					"put the swap on a bare wrapper around the element to insert:\n\t%s",
					filepath.Base(path), m[1], strings.TrimSpace(m[0]))
			}
		}
	}
}

// Each language's set must be bound to that language.
func TestTemplateFuncsAreBoundToTheirLanguage(t *testing.T) {
	loadForTest(t)

	fr := templateFuncs("fr")
	en := templateFuncs("en")

	for _, name := range []string{"T", "Tn", "lang", "statusWording", "relativeDate",
		"duration", "shortDate", "version", "libraryName", "googleKeyMissing", "asset", "jsTexts"} {
		if _, ok := fr[name]; !ok {
			t.Errorf("template function %q missing", name)
		}
	}

	if got := fr["lang"].(func() string)(); got != "fr" {
		t.Errorf("lang() = %q in the French set", got)
	}
	if got := en["lang"].(func() string)(); got != "en" {
		t.Errorf("lang() = %q in the English set", got)
	}
	if a, b := fr["T"].(func(string, ...any) string)("nav.borrowers"),
		en["T"].(func(string, ...any) string)("nav.borrowers"); a == b {
		t.Errorf("T is bound to the same catalogue in both sets: %q", a)
	}
	if a, b := fr["shortDate"].(func(string) string)("2026-09-11"),
		en["shortDate"].(func(string) string)("2026-09-11"); a != "11-09-2026" || b != "11 Sep 2026" {
		t.Errorf("shortDate: fr %q, en %q", a, b)
	}
	if a, b := fr["statusWording"].(func(string) string)("lost"),
		en["statusWording"].(func(string) string)("lost"); a == b {
		t.Errorf("statusWording is bound to the same catalogue in both sets: %q", a)
	}
}

// app.js is cached a year under one URL for every language, so it carries no
// text.
func TestJSTextsAreTranslated(t *testing.T) {
	loadForTest(t)
	fr := jsTexts("fr")
	en := jsTexts("en")

	if len(fr) != len(jsKeys) {
		t.Fatalf("%d strings handed to the JS, want %d", len(fr), len(jsKeys))
	}
	for _, key := range jsKeys {
		if fr[key] == "" || fr[key] == key {
			t.Errorf("%s: %q — the key is showing through", key, fr[key])
		}
		if en[key] == "" || en[key] == key {
			t.Errorf("%s (en): %q — the key is showing through", key, en[key])
		}
	}
}

// Every asset URL carries the version fingerprint.
func TestAssetCarriesAFingerprint(t *testing.T) {
	if assetFingerprint == "" {
		t.Fatal("empty asset fingerprint: /static/ is cached immutable for a year")
	}
	got := asset("/static/style.css")
	if !strings.HasPrefix(got, "/static/style.css?v=") {
		t.Errorf("asset() = %q", got)
	}
	if !strings.HasSuffix(got, assetFingerprint) {
		t.Errorf("asset() = %q, want it to end in the fingerprint %q", got, assetFingerprint)
	}
}

// The views are the only place the overdue count is computed.
func TestListLoans(t *testing.T) {
	a := testApp(t)
	const columns = `SELECT loan_id, borrower_id, book_id, first_name, last_initial, group_name,
		code, title, loaned_on, due_on, days_overdue FROM `

	open, err := a.listLoans(columns + `v_active_loan ORDER BY group_name, last_initial, first_name`)
	if err != nil {
		t.Fatalf("listLoans: %v", err)
	}
	if len(open) != 2 {
		t.Fatalf("%d open loans, want 2", len(open))
	}

	overdue, err := a.listLoans(columns + `v_overdue`)
	if err != nil {
		t.Fatalf("listLoans (overdue): %v", err)
	}
	if len(overdue) != 1 {
		t.Fatalf("%d overdue loans, want 1", len(overdue))
	}
	// Tom is five days late.
	p := overdue[0]
	if p.FirstName != "Tom" || p.Title != "Le Petit Prince" || p.Code != "VOL204572" {
		t.Errorf("overdue row: %+v", p)
	}
	if p.DaysOverdue != 5 {
		t.Errorf("days overdue: %d, want 5", p.DaysOverdue)
	}
	for _, l := range open {
		if l.FirstName == "Léa" && l.DaysOverdue > 0 {
			t.Errorf("Léa's loan is on time and counts %d days overdue", l.DaysOverdue)
		}
	}

	// The group filter.
	p3, err := a.listLoans(columns+`v_active_loan WHERE COALESCE(group_name, '') = ?`, "P3")
	if err != nil {
		t.Fatalf("listLoans (group): %v", err)
	}
	if len(p3) != 1 || p3[0].FirstName != "Tom" {
		t.Errorf("P3 loans: %+v", p3)
	}
	// A staff member may have no group: a NULL that must not be scanned into a string.
	if p3[0].Group == nil || *p3[0].Group != "P3" {
		t.Errorf("group: %v", p3[0].Group)
	}

	// A query that does not match the columns is an error, not a panic.
	if _, err := a.listLoans(`SELECT 1`); err == nil {
		t.Error("a mismatched query returned no error")
	}
}

func loanRow(id, borrowerID int64, first, group, title string, overdue int) Loan {
	l := Loan{ID: id, BorrowerID: borrowerID, FirstName: first, LastInitial: "X.",
		Title: title, DaysOverdue: overdue}
	if group != "" {
		c := group
		l.Group = &c
	}
	return l
}

// Folding into headings is correct only because of the query's ORDER BY.
func TestGroupLoans(t *testing.T) {
	groups := groupLoans([]Loan{
		loanRow(1, 102, "Tom", "P3", "Le Petit Prince", 5),
		loanRow(2, 102, "Tom", "P3", "Le loup est revenu", 0),
		loanRow(3, 104, "Noah", "P3", "Chien bleu", 0),
		loanRow(4, 101, "Léa", "P4", "Album", 0),
	})

	if len(groups) != 2 {
		t.Fatalf("%d groups, want 2", len(groups))
	}
	if groups[0].Group != "P3" || groups[1].Group != "P4" {
		t.Errorf("groups: %q then %q", groups[0].Group, groups[1].Group)
	}
	// The group counts books, not borrowers.
	if groups[0].Count != 3 {
		t.Errorf("P3 holds %d books, want 3", groups[0].Count)
	}
	if len(groups[0].Borrowers) != 2 {
		t.Fatalf("P3 has %d borrowers, want 2", len(groups[0].Borrowers))
	}
	tom := groups[0].Borrowers[0]
	if tom.FirstName != "Tom" || len(tom.Loans) != 2 {
		t.Errorf("Tom: %q with %d books", tom.FirstName, len(tom.Loans))
	}
	// One of Tom's two is late; the other is not.
	if tom.OverdueCount != 1 {
		t.Errorf("Tom's overdue count: %d, want 1", tom.OverdueCount)
	}
	if groups[0].Borrowers[1].OverdueCount != 0 {
		t.Errorf("Noah has nothing late, got %d", groups[0].Borrowers[1].OverdueCount)
	}
}

// Staff may have no group and group together.
func TestGroupLoansKeepsTheGrouplessTogether(t *testing.T) {
	groups := groupLoans([]Loan{
		loanRow(1, 105, "Claire", "", "Chien bleu", 0),
		loanRow(2, 105, "Claire", "", "Album", 0),
		loanRow(3, 106, "Marc", "", "Le loup", 0),
	})
	if len(groups) != 1 || groups[0].Group != "" {
		t.Fatalf("groups: %+v, want one with no group", groups)
	}
	if len(groups[0].Borrowers) != 2 {
		t.Errorf("%d borrowers, want 2", len(groups[0].Borrowers))
	}
}

// Namesakes in one group are separated by borrower_id alone.
func TestGroupLoansSeparatesNamesakes(t *testing.T) {
	groups := groupLoans([]Loan{
		loanRow(1, 201, "Léa", "P4", "Album", 0),
		loanRow(2, 202, "Léa", "P4", "Chien bleu", 0),
	})
	if len(groups) != 1 {
		t.Fatalf("%d groups, want 1", len(groups))
	}
	if len(groups[0].Borrowers) != 2 {
		t.Fatalf("%d borrowers, want 2 — two namesakes folded into one", len(groups[0].Borrowers))
	}
	for _, b := range groups[0].Borrowers {
		if len(b.Loans) != 1 {
			t.Errorf("borrower %d holds %d books, want 1", b.BorrowerID, len(b.Loans))
		}
	}
}

func TestGroupLoansOnNothing(t *testing.T) {
	if got := groupLoans(nil); len(got) != 0 {
		t.Errorf("groupLoans(nil) = %+v, want empty", got)
	}
}

// Pins loansOrderForGrouping against the real database.
func TestLoansQueryOrdersForGrouping(t *testing.T) {
	a := testApp(t)
	lend(t, a, 2, 102, 1) // a second book for Tom, who already has copy 1

	rows, err := a.listLoans(`SELECT loan_id, borrower_id, book_id, first_name, last_initial, group_name,
	       code, title, loaned_on, due_on, days_overdue
	  FROM v_active_loan` + loansOrderForGrouping)
	if err != nil {
		t.Fatalf("listLoans: %v", err)
	}
	groups := groupLoans(rows)

	// Every borrower appears in exactly one group, and once within it.
	seen := make(map[int64]int)
	for _, g := range groups {
		for _, b := range g.Borrowers {
			seen[b.BorrowerID]++
		}
	}
	for id, n := range seen {
		if n != 1 {
			t.Errorf("borrower %d appears in %d blocks — the rows are not grouped", id, n)
		}
	}
	// The ungrouped come last: the groups are the list.
	for i, g := range groups {
		if g.Group == "" && i != len(groups)-1 {
			t.Errorf("the groupless group is at %d of %d, want last", i, len(groups))
		}
	}

	// And a borrower's books are ordered by due date, soonest first.
	for _, g := range groups {
		for _, b := range g.Borrowers {
			for i := 1; i < len(b.Loans); i++ {
				if b.Loans[i-1].DueOn > b.Loans[i].DueOn {
					t.Errorf("%s: %s before %s", b.FirstName, b.Loans[i-1].DueOn, b.Loans[i].DueOn)
				}
			}
		}
	}
}

// The tabs are counted from the same view the list is built from.
func TestLoanGroups(t *testing.T) {
	a := testApp(t)
	// The fixture: Tom (P3) overdue, Léa (P4) on time.
	groups, err := a.loanGroups("v_active_loan")
	if err != nil {
		t.Fatalf("loanGroups: %v", err)
	}
	got := map[string]int{}
	for _, c := range groups {
		got[c.Group] = c.Count
	}
	if got["P3"] != 1 || got["P4"] != 1 {
		t.Errorf("active groups = %+v, want one book each in P3 and P4", got)
	}

	// Only what is late: Léa's on-time loan drops out, and so does her tab.
	groups, err = a.loanGroups("v_overdue")
	if err != nil {
		t.Fatalf("loanGroups (overdue): %v", err)
	}
	if len(groups) != 1 || groups[0].Group != "P3" || groups[0].Count != 1 {
		t.Errorf("overdue groups = %+v, want P3 alone with 1", groups)
	}
}

// The ungrouped get a tab of their own, last, under an empty group string: the
// handler must tell "?class=" from no parameter.
func TestLoanGroupsKeepsTheGrouplessLast(t *testing.T) {
	a := testApp(t)
	lend(t, a, 5, 105, 3) // Claire, who has no group

	groups, err := a.loanGroups("v_active_loan")
	if err != nil {
		t.Fatalf("loanGroups: %v", err)
	}
	if len(groups) == 0 {
		t.Fatal("no groups")
	}
	last := groups[len(groups)-1]
	if last.Group != "" {
		t.Errorf("last tab is %q, want the groupless", last.Group)
	}
	for _, c := range groups[:len(groups)-1] {
		if c.Group == "" {
			t.Error("the groupless appear twice")
		}
	}
}

// A group with nothing out gets no tab.
func TestLoanGroupsSkipsGroupsWithNothingOut(t *testing.T) {
	a := testApp(t)
	if _, err := a.db.Exec(
		`INSERT INTO borrower (first_name, last_initial, group_name, card_code, active)
		 VALUES ('Enzo', 'R.', 'P6', 'LEC99992', 1)`); err != nil {
		t.Fatal(err)
	}
	groups, err := a.loanGroups("v_active_loan")
	if err != nil {
		t.Fatalf("loanGroups: %v", err)
	}
	for _, c := range groups {
		if c.Group == "P6" {
			t.Error("P6 has nothing out and still got a tab")
		}
	}
}

// html/template escapes a value dropped straight after "?" or "&" as ONE
// parameter value, so the filter reads as empty and the sheet prints everything.
// Hand the whole query over as a template.URL (invFilter.URL).
func TestNoTemplateBuildsAQueryStringByDroppingAValueAfterAQuestionMark(t *testing.T) {
	files, err := filepath.Glob("templates/*.html")
	if err != nil {
		t.Fatal(err)
	}
	// An action opening straight after "?" or "&" rather than after "=".
	attr := regexp.MustCompile(`(?:href|src|action|formaction|hx-get|hx-post|hx-put|hx-patch|hx-delete)="([^"]*)"`)
	afterSeparator := regexp.MustCompile(`[?&](?:amp;)?\{\{`)

	checked := 0
	for _, path := range files {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range attr.FindAllStringSubmatch(string(content), -1) {
			checked++
			if afterSeparator.MatchString(m[1]) {
				t.Errorf("%s: %q hands a whole query string to html/template, which escapes it "+
					"as one parameter value — the filter arrives empty and the page selects "+
					"everything. Pass the address as a template.URL instead (invFilter.URL).",
					filepath.Base(path), m[1])
			}
		}
	}
	if checked == 0 {
		t.Fatal("no URL attribute found: the scan is looking at the wrong thing")
	}
}

// HTMX resolves "from:find X" with querySelector, the FIRST match: a bare tag
// name can bind the trigger to a hidden input.
func TestFindTriggersNameTheFieldTheyMean(t *testing.T) {
	files, err := filepath.Glob("templates/*.html")
	if err != nil {
		t.Fatal(err)
	}
	from := regexp.MustCompile(`from:find ([^",]+)`)
	bareTag := regexp.MustCompile(`^[a-z]+$`)

	found := 0
	for _, path := range files {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range from.FindAllStringSubmatch(string(content), -1) {
			found++
			if selector := strings.TrimSpace(m[1]); bareTag.MatchString(selector) {
				t.Errorf("%s: from:find %s picks whichever %s comes first in the form — "+
					"a hidden field added above it would silently take the event",
					filepath.Base(path), selector, selector)
			}
		}
	}
	if found < 2 {
		t.Fatalf("found %d from:find triggers, expected the two live searches", found)
	}
}

// A screen owns its sub-pages, and a prefix of another screen's name is not it.
func TestNavActive(t *testing.T) {
	cases := []struct {
		path, screen string
		want         bool
	}{
		{"/borrow", "/borrow", true},
		{"/borrowers", "/borrow", false},
		{"/borrowers", "/borrowers", true},
		{"/borrowers/12", "/borrowers", true},
		{"/borrowers/import", "/borrowers", true},
		{"/book/3", "/inventory", false},
		{"/inventory", "/inventory", true},
		{"/", "/borrow", false},
	}
	for _, c := range cases {
		if got := navActive(c.path, c.screen); got != c.want {
			t.Errorf("navActive(%q, %q) = %v, want %v", c.path, c.screen, got, c.want)
		}
	}
}

// Templates resolve fields at RENDER time: every field of the cataloguing
// form gets a unique value, and each is looked for in the output.
func TestBothCataloguingFormsRenderTheWholeRecord(t *testing.T) {
	loadForTest(t)
	sets, err := loadTemplates()
	if err != nil {
		t.Fatalf("loading the templates: %v", err)
	}
	a := &app{pages: sets}

	form := catalogueForm{
		N: Record{
			ISBN13: "9780000000001", ISBN10: "0000000001",
			Title: "TitreSentinelle", Subtitle: "SousTitreSentinelle",
			Authors: "AuteurSentinelle", Publisher: "EditeurSentinelle",
			Year: 1977, Language: "br",
			Source: "bnf", URL: "https://example.invalid/ark",
			Payload: "ChargeSentinelle",
		},
		Found:  true,
		Copies: 1,
	}
	want := []string{
		"9780000000001", "0000000001", "TitreSentinelle", "SousTitreSentinelle",
		"AuteurSentinelle", "EditeurSentinelle", "1977", "br",
		"https://example.invalid/ark", "ChargeSentinelle",
	}

	r := httptest.NewRequest("GET", "/", nil)
	for _, c := range []struct{ page, block string }{
		{"borrow", "borrow_new_book"},
		{"catalogue", "catalogue_form"},
	} {
		html, err := a.fragmentString(r, c.page, c.block, form)
		if err != nil {
			t.Fatalf("%s/%s: %v", c.page, c.block, err)
		}
		for _, w := range want {
			if !strings.Contains(html, w) {
				t.Errorf("%s/%s: %q is nowhere in the form — a field path no longer resolves",
					c.page, c.block, w)
			}
		}
		// What a wrong path actually looks like, rather than an empty string.
		if strings.Contains(html, "<no value>") {
			t.Errorf("%s/%s: rendered \"<no value>\"", c.page, c.block)
		}
	}

	// Found, not found and not asked are three states, not two.
	silent := catalogueForm{N: Record{ISBN13: "9780000000001"}, Silent: true, Copies: 1}
	for _, c := range []struct{ page, block, sentence string }{
		{"borrow", "borrow_new_book", T("fr", "loan.catalogues_silent")},
		{"catalogue", "catalogue_form", T("fr", "catalogue.err_catalogues_silent")},
	} {
		html, err := a.fragmentString(r, c.page, c.block, silent)
		if err != nil {
			t.Fatalf("%s/%s: %v", c.page, c.block, err)
		}
		// Escaped as the template writes it ("n&#39;ont").
		if !strings.Contains(html, stdhtml.EscapeString(c.sentence)) {
			t.Errorf("%s/%s: a silent catalogue did not say so", c.page, c.block)
		}
	}
}

// A book catalogued without an ISBN has no barcode of its own: the success
// screen must ask for a label rather than say none is needed.
func TestCatalogueSuccessAsksForALabelOnlyWithoutAnISBN(t *testing.T) {
	loadForTest(t)
	sets, err := loadTemplates()
	if err != nil {
		t.Fatalf("loading the templates: %v", err)
	}
	a := &app{pages: sets}
	r := httptest.NewRequest("GET", "/", nil)
	for _, c := range []struct {
		hasISBN     bool
		want, avoid string
	}{
		{true, "catalogue.no_label_needed", "catalogue.label_needed"},
		{false, "catalogue.label_needed", "catalogue.no_label_needed"},
	} {
		html, err := a.fragmentString(r, "catalogue", "catalogue_success", map[string]any{
			"Title": "Album", "Codes": []string{"VOL204572"}, "HasISBN": c.hasISBN,
		})
		if err != nil {
			t.Fatalf("HasISBN=%v: %v", c.hasISBN, err)
		}
		if !strings.Contains(html, stdhtml.EscapeString(T("fr", c.want))) {
			t.Errorf("HasISBN=%v: %s is missing", c.hasISBN, c.want)
		}
		if strings.Contains(html, stdhtml.EscapeString(T("fr", c.avoid))) {
			t.Errorf("HasISBN=%v: %s is shown", c.hasISBN, c.avoid)
		}
	}
}
