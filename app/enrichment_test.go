package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// catalogueClient is a package variable so the four sources are answered from
// here: the chain, retries, rest after a 429 and memory, with no real request.

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// catalogueCalls records what left the school: only an ISBN may.
type catalogueCalls struct {
	mu   sync.Mutex
	urls []string
}

func (c *catalogueCalls) record(u string) {
	c.mu.Lock()
	c.urls = append(c.urls, u)
	c.mu.Unlock()
}

func (c *catalogueCalls) all() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.urls...)
}

func (c *catalogueCalls) count() int { return len(c.all()) }

// hosts returns the catalogues queried, in order.
func (c *catalogueCalls) hosts(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, u := range c.all() {
		r, err := http.NewRequest(http.MethodGet, u, nil)
		if err != nil {
			t.Fatalf("recorded URL %q: %v", u, err)
		}
		out = append(out, r.URL.Host)
	}
	return out
}

// A status of 0 stands for a network failure, which the retry treats as transient.
type stubReply func(*http.Request) (status int, contentType, body string)

// stubCatalogues answers every outgoing request from reply, and starts from an
// empty memory, which is a package variable.
func stubCatalogues(t *testing.T, reply stubReply) *catalogueCalls {
	t.Helper()

	savedMemo := catalogueMemo
	catalogueMemo = &memo{
		records:      make(map[string]recordMemo),
		restingUntil: make(map[string]time.Time),
	}
	savedClient := catalogueClient
	calls := &catalogueCalls{}
	catalogueClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls.record(r.URL.String())
		status, ct, body := reply(r)
		if status == 0 {
			return nil, fmt.Errorf("simulated network failure")
		}
		h := make(http.Header)
		if ct != "" {
			h.Set("Content-Type", ct)
		}
		return &http.Response{
			StatusCode: status,
			Header:     h,
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    r,
		}, nil
	})}
	t.Cleanup(func() {
		catalogueMemo = savedMemo
		catalogueClient = savedClient
	})
	return calls
}

// A Google Books answer, trimmed to the fields read. The second volume checks
// that only the first is kept.
const googleResponse = `{"items":[
 {"volumeInfo":{"title":"Le Petit Prince","subtitle":"avec des aquarelles de l'auteur",
   "authors":["Antoine de Saint-Exupéry","Joann Sfar"],
   "publisher":"Gallimard","publishedDate":"1999-04-15","language":"fr"}},
 {"volumeInfo":{"title":"Une autre édition"}}]}`

// What openlibrary.org/isbn/9782070408504.json redirects to: the edition, its
// authors by key only.
const olEditionJSON = `{
 "key":"/books/OL8839231M","title":"Le Petit Prince","subtitle":"avec des aquarelles",
 "publishers":["Gallimard","Folio"],"publish_date":"February 28, 1999",
 "isbn_13":["9782070408504"],"isbn_10":["2070408507"],
 "authors":[{"key":"/authors/OL31901A"}],
 "languages":[{"key":"/languages/fre"}]}`

const olAuthorJSON = `{"key":"/authors/OL31901A","name":"Antoine de Saint-Exupéry"}`

const emptySRU = `<searchRetrieveResponse><records/></searchRetrieveResponse>`

// nothingFound answers every catalogue with an empty but well-formed response.
func nothingFound(r *http.Request) (int, string, string) {
	switch r.URL.Host {
	case "www.googleapis.com":
		return 200, "application/json", `{"totalItems":0}`
	case "openlibrary.org":
		return 404, "", ""
	default:
		return 200, "text/xml", emptySRU
	}
}

const petitPrince13 = "9782070408504"
const petitPrince10 = "2070408507"

func TestEnrichStopsAtTheFirstCatalogueThatAnswers(t *testing.T) {
	calls := stubCatalogues(t, func(r *http.Request) (int, string, string) {
		return 200, "text/xml", bnfResponse
	})

	n, err := enrich(context.Background(), petitPrince13, petitPrince10)
	if err != nil {
		t.Fatalf("the BnF answered and enrich still reported a fault: %v", err)
	}
	if n == nil {
		t.Fatal("nil record while the BnF answered")
	}
	if n.Source != "bnf" || n.Title != "Le petit prince" {
		t.Errorf("record: source %q, title %q", n.Source, n.Title)
	}
	// First answer wins: the other catalogues are not asked.
	if got := calls.hosts(t); len(got) != 1 || got[0] != "catalogue.bnf.fr" {
		t.Errorf("catalogues queried: %v, want the BnF alone", got)
	}
}

func TestEnrichFallsThroughToTheNextCatalogue(t *testing.T) {
	calls := stubCatalogues(t, func(r *http.Request) (int, string, string) {
		switch r.URL.Host {
		case "www.unicat.be":
			return 200, "text/xml", unicatResponse
		default:
			return nothingFound(r)
		}
	})

	n, err := enrich(context.Background(), "9782211037495", "2211037496")
	if err != nil {
		t.Fatalf("UniCat answered and enrich still reported a fault: %v", err)
	}
	if n == nil {
		t.Fatal("nil record while UniCat answered")
	}
	if n.Source != "unicat" {
		t.Errorf("source: %q, want unicat", n.Source)
	}
	// The BnF came up empty; Google and Open Library are spared.
	want := []string{"catalogue.bnf.fr", "www.unicat.be"}
	got := calls.hosts(t)
	if len(got) != len(want) {
		t.Fatalf("catalogues queried: %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("catalogue %d: %s, want %s", i, got[i], want[i])
		}
	}
}

// An absence is remembered too. A miss costs five requests: one per
// catalogue, and a second for Open Library, asked under each ISBN form.
const requestsForAMiss = 5

func TestEnrichRemembersAnAbsence(t *testing.T) {
	calls := stubCatalogues(t, nothingFound)

	n, err := enrich(context.Background(), petitPrince13, petitPrince10)
	if n != nil || err != nil {
		t.Fatalf("four catalogues answered \"no\": record %+v, err %v — want nothing and no fault", n, err)
	}
	if got := calls.count(); got != requestsForAMiss {
		t.Errorf("first pass: %d requests, want %d", got, requestsForAMiss)
	}

	if n, _ := enrich(context.Background(), petitPrince13, petitPrince10); n != nil {
		t.Fatalf("record found where nothing was: %+v", n)
	}
	if got := calls.count(); got != requestsForAMiss {
		t.Errorf("second pass queried again: %d requests in total, want %d", got, requestsForAMiss)
	}

	// "Enrich from the ISBN" is an explicit request to go back and look.
	catalogueMemo.forgetRecord(petitPrince13)
	enrich(context.Background(), petitPrince13, petitPrince10) //nolint:errcheck // counting requests
	if got := calls.count(); got != 2*requestsForAMiss {
		t.Errorf("after forgetting: %d requests in total, want %d", got, 2*requestsForAMiss)
	}
}

// A chain cut off halfway is not an answer, so it is not remembered.
func TestAnExhaustedBudgetRemembersNothing(t *testing.T) {
	calls := stubCatalogues(t, nothingFound)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	n, err := enrich(ctx, petitPrince13, petitPrince10)
	if n != nil {
		t.Fatalf("record returned on an exhausted budget: %+v", n)
	}
	if err == nil {
		t.Error("an exhausted budget passed for an answer: the screen would say the book is unknown")
	}
	if got := calls.count(); got != 0 {
		t.Errorf("%d requests fired on an exhausted budget", got)
	}
	if _, known := catalogueMemo.record(petitPrince13); known {
		t.Error("a partial answer was remembered: the next enrichment would serve it")
	}
}

// Only an ISBN leaves the school, and both forms go out — a pre-2007 work
// may be indexed under its ISBN-10 alone.
func TestOnlyTheISBNLeavesTheSchool(t *testing.T) {
	calls := stubCatalogues(t, nothingFound)
	enrich(context.Background(), petitPrince13, petitPrince10)

	urls := calls.all()
	if len(urls) != requestsForAMiss {
		t.Fatalf("%d requests, want %d", len(urls), requestsForAMiss)
	}
	for _, u := range urls {
		if !strings.Contains(u, petitPrince13) && !strings.Contains(u, petitPrince10) {
			t.Errorf("request carrying neither form of the ISBN: %s", u)
		}
	}
	// The two SRU catalogues are asked for both forms.
	for _, u := range urls[:2] {
		if !strings.Contains(u, petitPrince10) {
			t.Errorf("SRU request without the ISBN-10 — a pre-2007 work would be missed: %s", u)
		}
	}
}

// A 979 ISBN has no ISBN-10: the query must not carry an empty one.
func TestA979ISBNIsQueriedOnItsOwn(t *testing.T) {
	calls := stubCatalogues(t, nothingFound)
	i13, i10, err := ISBNForms("9791036300271")
	if err != nil {
		t.Fatalf("ISBNForms: %v", err)
	}
	if i10 != "" {
		t.Fatalf("979 prefix: ISBN-10 %q, want empty", i10)
	}
	enrich(context.Background(), i13, i10)

	for _, u := range calls.all()[:2] {
		if strings.Contains(u, `isbn=""`) || strings.Contains(u, "isbn%3D+or") {
			t.Errorf("empty ISBN-10 sent in the query: %s", u)
		}
	}
}

func TestRequestRetriesOnceOnATransientFailure(t *testing.T) {
	var n int
	calls := stubCatalogues(t, func(r *http.Request) (int, string, string) {
		n++
		if n == 1 {
			return 503, "", "gateway hiccup"
		}
		return 200, "text/xml", bnfResponse
	})

	rec, _ := searchBnF(context.Background(), petitPrince13, petitPrince10)
	if rec == nil {
		t.Fatal("nil record: the retry did not happen")
	}
	if got := calls.count(); got != 2 {
		t.Errorf("%d requests, want 2 (one failure, one retry)", got)
	}
}

func TestRequestDoesNotRetryOnAPermanentFailure(t *testing.T) {
	calls := stubCatalogues(t, func(r *http.Request) (int, string, string) {
		return 404, "", "no such record"
	})

	rec, err := searchBnF(context.Background(), petitPrince13, petitPrince10)
	if rec != nil {
		t.Errorf("record built from a 404: %+v", rec)
	}
	// The source hands the 404 back as errNoRecord; enrich reads it as an absence.
	if err == nil {
		t.Error("a 404 passed for an answer")
	}
	if got := calls.count(); got != 1 {
		t.Errorf("%d requests, want 1: a 404 is not worth retrying", got)
	}
}

// After a 429 the catalogue is left alone: the school shares one IP address.
func TestARateLimitedSourceIsPutToRest(t *testing.T) {
	calls := stubCatalogues(t, func(r *http.Request) (int, string, string) {
		return 429, "", "slow down"
	})

	searchBnF(context.Background(), petitPrince13, petitPrince10)
	if !catalogueMemo.isResting("catalogue.bnf.fr") {
		t.Fatal("a 429 did not put the source to rest")
	}
	after := calls.count()

	searchBnF(context.Background(), petitPrince13, petitPrince10)
	if got := calls.count(); got != after {
		t.Errorf("a resting source was queried again: %d requests, want %d", got, after)
	}
	// Resting one catalogue leaves the others alone.
	if catalogueMemo.isResting("www.unicat.be") {
		t.Error("UniCat put to rest by a BnF 429")
	}
}

func TestMemoDistinguishesUnknownFromLookedForInVain(t *testing.T) {
	stubCatalogues(t, nothingFound)

	if _, known := catalogueMemo.record(petitPrince13); known {
		t.Error("an ISBN never looked for is reported as known")
	}
	catalogueMemo.rememberRecord(petitPrince13, nil)
	rec, known := catalogueMemo.record(petitPrince13)
	if !known {
		t.Error("a fruitless search was not remembered")
	}
	if rec != nil {
		t.Errorf("a fruitless search returned a record: %+v", rec)
	}
}

func TestMemoExpires(t *testing.T) {
	stubCatalogues(t, nothingFound)
	catalogueMemo.rememberRecord(petitPrince13, &Record{Title: "Le Petit Prince"})

	// Age the entry past its time to live rather than wait a day for it.
	catalogueMemo.mu.Lock()
	e := catalogueMemo.records[petitPrince13]
	e.expires = time.Now().Add(-time.Second)
	catalogueMemo.records[petitPrince13] = e
	catalogueMemo.mu.Unlock()

	if _, known := catalogueMemo.record(petitPrince13); known {
		t.Error("an expired record is still served")
	}
}

// A long cataloguing session must not grow the memory without bound.
func TestMemoCapsWhatItKeeps(t *testing.T) {
	stubCatalogues(t, nothingFound)
	for i := 0; i < maxNotices+10; i++ {
		catalogueMemo.rememberRecord(fmt.Sprintf("isbn-%d", i), nil)
	}
	catalogueMemo.mu.Lock()
	n := len(catalogueMemo.records)
	catalogueMemo.mu.Unlock()
	if n > maxNotices {
		t.Errorf("memory holds %d records, cap is %d", n, maxNotices)
	}
}

func TestSearchGoogleKeepsTheFirstVolume(t *testing.T) {
	stubCatalogues(t, func(r *http.Request) (int, string, string) {
		return 200, "application/json", googleResponse
	})

	n, _ := searchGoogle(context.Background(), petitPrince13)
	if n == nil {
		t.Fatal("nil record for a valid Google answer")
	}
	cases := []struct{ field, got, want string }{
		{"title", n.Title, "Le Petit Prince"},
		{"subtitle", n.Subtitle, "avec des aquarelles de l'auteur"},
		{"authors", n.Authors, "Antoine de Saint-Exupéry ; Joann Sfar"},
		{"publisher", n.Publisher, "Gallimard"},
		{"language", n.Language, "fr"},
		{"source", n.Source, "google"},
		{"isbn10", n.ISBN10, petitPrince10},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s: %q, want %q", c.field, c.got, c.want)
		}
	}
	if n.Year != 1999 {
		t.Errorf("year: %d, want 1999 (from \"1999-04-15\")", n.Year)
	}
	if n.Payload == "" {
		t.Error("empty payload: the raw answer is kept so enrichment can be replayed")
	}
}

// country=FR is required: without it Google answers 403 "unregistered callers".
func TestSearchGoogleAsksForACountry(t *testing.T) {
	calls := stubCatalogues(t, func(r *http.Request) (int, string, string) {
		return 200, "application/json", `{"totalItems":0}`
	})
	searchGoogle(context.Background(), petitPrince13)
	if u := calls.all()[0]; !strings.Contains(u, "country=FR") {
		t.Errorf("query without country=FR: %s", u)
	}
}

// The key travels in a header: a network error quotes the URL, and that error
// is logged.
func TestSearchGooglePassesTheKeyInAHeader(t *testing.T) {
	t.Setenv("BIBLI_GOOGLE_BOOKS_KEY", "a-key")
	var sent string
	calls := stubCatalogues(t, func(r *http.Request) (int, string, string) {
		sent = r.Header.Get("X-Goog-Api-Key")
		return 200, "application/json", `{"totalItems":0}`
	})
	searchGoogle(context.Background(), petitPrince13)
	if sent != "a-key" {
		t.Errorf("configured key not sent: header %q", sent)
	}
	if u := calls.all()[0]; strings.Contains(u, "a-key") {
		t.Errorf("key in the URL: %s", u)
	}
}

func openLibraryStub(r *http.Request) (int, string, string) {
	switch r.URL.Path {
	case "/isbn/9782070408504.json":
		return 200, "application/json", olEditionJSON
	case "/authors/OL31901A.json":
		return 200, "application/json", olAuthorJSON
	}
	return 404, "", ""
}

func TestSearchOpenLibrary(t *testing.T) {
	stubCatalogues(t, openLibraryStub)

	n, err := searchOpenLibrary(context.Background(), petitPrince13, petitPrince10)
	if err != nil || n == nil {
		t.Fatalf("searchOpenLibrary = %v, %v; want a record", n, err)
	}
	cases := []struct{ field, got, want string }{
		{"title", n.Title, "Le Petit Prince"},
		{"subtitle", n.Subtitle, "avec des aquarelles"},
		{"authors", n.Authors, "Antoine de Saint-Exupéry"},
		{"publisher", n.Publisher, "Gallimard ; Folio"},
		{"language", n.Language, "fr"},
		{"source", n.Source, "openlibrary"},
		{"url", n.URL, "https://openlibrary.org/books/OL8839231M"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s: %q, want %q", c.field, c.got, c.want)
		}
	}
	if n.Year != 1999 {
		t.Errorf("year: %d, want 1999 (from \"February 28, 1999\")", n.Year)
	}
}

// Open Library files a book under the number it was handed: an older book may
// be there under its ISBN-10 alone.
func TestSearchOpenLibraryFallsBackToTheISBN10(t *testing.T) {
	calls := stubCatalogues(t, func(r *http.Request) (int, string, string) {
		switch r.URL.Path {
		case "/isbn/2070408507.json":
			return 200, "application/json", olEditionJSON
		case "/authors/OL31901A.json":
			return 200, "application/json", olAuthorJSON
		}
		return 404, "", ""
	})
	n, err := searchOpenLibrary(context.Background(), petitPrince13, petitPrince10)
	if err != nil || n == nil || n.Title != "Le Petit Prince" {
		t.Fatalf("searchOpenLibrary = %+v, %v; want the record found under the ISBN-10", n, err)
	}
	if got := calls.all(); len(got) < 2 || !strings.Contains(got[0], petitPrince13) || !strings.Contains(got[1], petitPrince10) {
		t.Errorf("calls = %v, want the ISBN-13 first and then the ISBN-10", got)
	}
}

// Both forms answered 404: an answer ("no such book"), not a fault.
func TestSearchOpenLibraryAbsence(t *testing.T) {
	stubCatalogues(t, func(r *http.Request) (int, string, string) { return 404, "", "" })
	n, err := searchOpenLibrary(context.Background(), petitPrince13, petitPrince10)
	if n != nil || !errors.Is(err, errNoRecord) {
		t.Errorf("searchOpenLibrary = %v, %v; want no record and errNoRecord", n, err)
	}
}

// The edition must be the one asked for, and a record needs a title.
func TestSearchOpenLibraryIgnoresAnUnrelatedAnswer(t *testing.T) {
	for _, body := range []string{
		`{"title":"Autre chose","isbn_13":["9999999999999"]}`,
		`{"isbn_13":["9782070408504"]}`,
	} {
		stubCatalogues(t, func(r *http.Request) (int, string, string) {
			return 200, "application/json", body
		})
		if n, _ := searchOpenLibrary(context.Background(), petitPrince13, petitPrince10); n != nil {
			t.Errorf("record built from %s: %+v", body, n)
		}
	}
}

// An author that cannot be read leaves the field empty, not the record lost.
func TestSearchOpenLibraryKeepsTheRecordWithoutItsAuthor(t *testing.T) {
	stubCatalogues(t, func(r *http.Request) (int, string, string) {
		if r.URL.Path == "/isbn/9782070408504.json" {
			return 200, "application/json", olEditionJSON
		}
		return 500, "", ""
	})
	n, err := searchOpenLibrary(context.Background(), petitPrince13, petitPrince10)
	if err != nil || n == nil || n.Title != "Le Petit Prince" || n.Authors != "" {
		t.Errorf("searchOpenLibrary = %+v, %v; want the record with no author", n, err)
	}
}

// With the budget nearly spent, the authors are not asked.
func TestSearchOpenLibrarySkipsAuthorsWhenTheBudgetIsSpent(t *testing.T) {
	calls := stubCatalogues(t, openLibraryStub)
	ctx, cancel := context.WithTimeout(context.Background(), openLibraryAuthorRoom/2)
	defer cancel()
	n, err := searchOpenLibrary(ctx, petitPrince13, petitPrince10)
	if err != nil || n == nil {
		t.Fatalf("searchOpenLibrary = %v, %v; want the record", n, err)
	}
	for _, u := range calls.all() {
		if strings.Contains(u, "/authors/") {
			t.Errorf("asked %s with too little budget left", u)
		}
	}
}

// An author key is spliced into a URL, so only Open Library's own shape of key
// is followed.
func TestSearchOpenLibraryFollowsOnlyAuthorKeys(t *testing.T) {
	calls := stubCatalogues(t, func(r *http.Request) (int, string, string) {
		if r.URL.Path == "/isbn/9782070408504.json" {
			return 200, "application/json", `{"title":"Le Petit Prince","authors":[{"key":"/../admin"},{"key":"https://evil.example/x"}]}`
		}
		return 404, "", ""
	})
	searchOpenLibrary(context.Background(), petitPrince13, petitPrince10)
	for _, u := range calls.all() {
		if !strings.Contains(u, "/isbn/") {
			t.Errorf("followed a key that is not an author's: %s", u)
		}
	}
}

// A record with no title is not usable.
func TestARecordWithoutATitleIsNotAnAnswer(t *testing.T) {
	calls := stubCatalogues(t, func(r *http.Request) (int, string, string) {
		switch r.URL.Host {
		case "catalogue.bnf.fr":
			// A record with an author and no 200$a: nothing to catalogue under.
			return 200, "text/xml", `<r><mxc:record xmlns:mxc="info:lc/xmlns/marcxchange-v2">
				<mxc:datafield tag="700"><mxc:subfield code="a">Anonyme</mxc:subfield></mxc:datafield>
				</mxc:record></r>`
		default:
			return nothingFound(r)
		}
	})

	if n, _ := enrich(context.Background(), petitPrince13, petitPrince10); n != nil {
		t.Errorf("titleless record accepted: %+v", n)
	}
	if got := calls.count(); got != requestsForAMiss {
		t.Errorf("%d requests: the chain stopped at a titleless record", got)
	}
}

// Every source answers a nil record when the network is down; manual entry takes over.
func TestANetworkFailureIsNotFatal(t *testing.T) {
	stubCatalogues(t, func(r *http.Request) (int, string, string) {
		return 0, "", "" // connection refused
	})
	n, err := enrich(context.Background(), petitPrince13, petitPrince10)
	if n != nil {
		t.Errorf("record returned with the network down: %+v", n)
	}
	if err == nil {
		t.Error("the network was down and enrich reported no fault")
	}
}

// An absence is only remembered when every catalogue reported one: a nil record
// beside an error means "could not find out".
func TestAnAbsenceIsRememberedOnlyWhenEveryCatalogueSaidSo(t *testing.T) {
	// UniCat is down; the other three genuinely have nothing.
	calls := stubCatalogues(t, func(r *http.Request) (int, string, string) {
		if r.URL.Host == "www.unicat.be" {
			return 0, "", "" // connection refused
		}
		return nothingFound(r)
	})

	n, err := enrich(context.Background(), petitPrince13, petitPrince10)
	if n != nil {
		t.Fatalf("record found where nothing was: %+v", n)
	}
	if err == nil {
		t.Fatal("a catalogue that never answered passed for one with nothing to say")
	}
	first := calls.count()
	if first == 0 {
		t.Fatal("no catalogue was queried: the rest of this proves nothing")
	}
	if _, known := catalogueMemo.record(petitPrince13); known {
		t.Error("the absence was remembered: rescanning cannot get past it for 24 h")
	}

	// So the next scan goes back out.
	if n, _ := enrich(context.Background(), petitPrince13, petitPrince10); n != nil {
		t.Fatalf("record found where nothing was: %+v", n)
	}
	if got := calls.count(); got <= first {
		t.Errorf("the second scan queried nothing: %d requests in total, want more than %d", got, first)
	}
}

// A maintenance page served with a 200 is not the BnF saying it lacks the book.
func TestAMaintenancePageIsNotRememberedAsAnAbsence(t *testing.T) {
	stubCatalogues(t, func(r *http.Request) (int, string, string) {
		if r.URL.Host == "catalogue.bnf.fr" {
			return 200, "text/html", `<!DOCTYPE html><html><head><meta charset="utf-8"></head><body>Maintenance en cours</body></html>`
		}
		return nothingFound(r)
	})

	if _, err := enrich(context.Background(), petitPrince13, petitPrince10); err == nil {
		t.Fatal("an HTML page passed for an answer")
	}
	if _, known := catalogueMemo.record(petitPrince13); known {
		t.Error("the book was filed as unknown for a day on the strength of a maintenance page")
	}
}

// A source resting after a 429 could not be asked: that is not an absence either.
func TestASourceAtRestDoesNotMakeABookUnknown(t *testing.T) {
	stubCatalogues(t, func(r *http.Request) (int, string, string) {
		if r.URL.Host == "www.googleapis.com" {
			return 429, "", "slow down"
		}
		return nothingFound(r)
	})

	if _, err := enrich(context.Background(), petitPrince13, petitPrince10); err == nil {
		t.Fatal("a 429 passed for an answer")
	}
	if !catalogueMemo.isResting("www.googleapis.com") {
		t.Fatal("the 429 did not put Google to rest: this test is not testing resting")
	}
	if _, known := catalogueMemo.record(petitPrince13); known {
		t.Error("a book looked up while a catalogue rested was filed as unknown")
	}

	// Nor is the next book, looked up while Google is still resting.
	if _, err := enrich(context.Background(), "9782211037495", "2211037496"); err == nil {
		t.Error("the resting source was silently treated as having nothing")
	}
	if _, known := catalogueMemo.record("9782211037495"); known {
		t.Error("the second book was filed as unknown while a catalogue rested")
	}
}

// A 403 is retried once: UniCat answers 403 then 200 to the same query.
func TestAForbiddenAnswerIsTriedAgain(t *testing.T) {
	var n int
	calls := stubCatalogues(t, func(r *http.Request) (int, string, string) {
		if r.URL.Host != "www.unicat.be" {
			return nothingFound(r)
		}
		if n++; n == 1 {
			return 403, "", "go away"
		}
		return 200, "text/xml", unicatResponse
	})

	rec, err := enrich(context.Background(), "9782211037495", "2211037496")
	if err != nil {
		t.Fatalf("the retry answered and enrich still reported a fault: %v", err)
	}
	if rec == nil {
		t.Fatal("nil record: a 403 was taken as UniCat's final word")
	}
	if rec.Source != "unicat" {
		t.Errorf("source %q, want unicat", rec.Source)
	}
	if got := calls.hosts(t); len(got) != 3 || got[1] != "www.unicat.be" || got[2] != "www.unicat.be" {
		t.Errorf("catalogues queried: %v, want the BnF then UniCat twice", got)
	}
}

// 400, 401 and 422 are final: retrying them only spends the quota.
func TestAnUnauthorisedAnswerIsNotTriedAgain(t *testing.T) {
	for _, status := range []int{400, 401, 422} {
		calls := stubCatalogues(t, func(r *http.Request) (int, string, string) {
			return status, "", "no"
		})
		if _, err := enrich(context.Background(), petitPrince13, petitPrince10); err == nil {
			t.Errorf("status %d passed for an answer", status)
		}
		if got := calls.count(); got != 4 {
			t.Errorf("status %d: %d requests, want 4 — one per catalogue, none retried", status, got)
		}
	}
}

// A 404 is the catalogue saying it holds no such record, so an absence made of
// 404s is remembered.
func TestANotFoundIsAnAnswerAndIsRemembered(t *testing.T) {
	calls := stubCatalogues(t, func(r *http.Request) (int, string, string) {
		if r.URL.Host == "openlibrary.org" {
			return 404, "application/json", ""
		}
		return nothingFound(r)
	})

	n, err := enrich(context.Background(), petitPrince13, petitPrince10)
	if n != nil {
		t.Fatalf("record found where nothing was: %+v", n)
	}
	if err != nil {
		t.Fatalf("a 404 was read as a catalogue falling silent: %v", err)
	}
	if got := calls.count(); got != requestsForAMiss {
		t.Fatalf("%d requests, want %d: a 404 is an answer and is not retried", got, requestsForAMiss)
	}
	if _, known := catalogueMemo.record(petitPrince13); !known {
		t.Error("the absence was not remembered: every book page would query four catalogues again")
	}
}

// The narrated chain is the desk's chain and feeds the same memory.
func TestTheNarratedChainIsTheSameChain(t *testing.T) {
	calls := stubCatalogues(t, func(r *http.Request) (int, string, string) {
		if r.URL.Host == "www.unicat.be" {
			return 200, "text/xml", unicatResponse
		}
		return nothingFound(r)
	})

	var tried, answered []string
	n, err := enrichReported(context.Background(), "9782211037495", "2211037496", enrichLog{
		trying:   func(s enrichSource) { tried = append(tried, s.name) },
		answered: func(s enrichSource, _ *Record, _ error) { answered = append(answered, s.name) },
	})
	if err != nil || n == nil {
		t.Fatalf("record %+v, err %v — want UniCat's record and no fault", n, err)
	}
	want := []string{"bnf", "unicat"}
	if strings.Join(tried, ",") != strings.Join(want, ",") {
		t.Errorf("narrated %v, want %v — the log must follow the chain, not a copy of it", tried, want)
	}
	if strings.Join(answered, ",") != strings.Join(want, ",") {
		t.Errorf("answered for %v, want %v", answered, want)
	}
	// And it fed the one memory, so the desk now finds what this screen found.
	if got := calls.count(); got != 2 {
		t.Fatalf("%d requests, want 2", got)
	}
	if _, known := catalogueMemo.record("9782211037495"); !known {
		t.Error("the narrated chain remembered nothing: the desk would go back out for it")
	}
}

// A catalogue must not bounce us onto the machine itself or the local network:
// the redirect guard refuses a private or loopback target, an unresolvable one,
// and lets a public one through.
func TestRefusePrivateHost(t *testing.T) {
	blocked := []string{"127.0.0.1", "169.254.169.254", "10.0.0.5", "192.168.1.1", "::1", "0.0.0.0"}
	for _, h := range blocked {
		if refusePrivateHost(h) == nil {
			t.Errorf("refusePrivateHost(%q) allowed a private/loopback target", h)
		}
	}
	if refusePrivateHost("this-name-does-not-resolve.invalid") == nil {
		t.Error("an unresolvable host should be refused")
	}
	if err := refusePrivateHost("8.8.8.8"); err != nil {
		t.Errorf("a public IP was refused: %v", err)
	}
}
