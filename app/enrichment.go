package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Enrichment: turning an ISBN into a record by asking public catalogues in
// turn. Only the ISBN leaves the server, never reader data.

type Record struct {
	ISBN13    string
	ISBN10    string
	Title     string
	Subtitle  string
	Authors   string
	Publisher string
	Year      int
	Language  string
	Source    string // 'bnf' | 'unicat' | 'google' | 'openlibrary' | 'manual' | 'local' (catalogue.go)
	URL       string // permanent record URL (BnF ARK, Open Library page...)
	// Never stored in the database — see cover.go.
	CoverURL string
	Payload  string // raw response, to replay enrichment later
}

// userAgent identifies Bibli to the catalogues, with a contact URL a catalogue
// operator can reach the project at. The version comes from the build, so it is
// never a stale literal; sourceURL is the one place the project's home is named.
func userAgent() string {
	return "Bibli/" + displayVersion() + " (small library; +" + sourceURL + ")"
}

type enrichSource struct {
	// A locale key, since the displayed name is translated; Record.Source is
	// the stored, stable spelling.
	key string
	// The short name, for the log. Record.Source uses the same spellings.
	name string
	// nil record + nil error: the catalogue does not have the book. An error:
	// it did not answer, which must not be read as an absence (see enrich).
	fn func(context.Context, string, string) (*Record, error)
}

// The order in which catalogues are queried.

func enrichmentSources() []enrichSource {
	return []enrichSource{
		{"catalogue.source_bnf", "bnf", searchBnF},
		{"catalogue.source_unicat", "unicat", searchUniCat},
		{"catalogue.source_google", "google",
			func(ctx context.Context, i13, _ string) (*Record, error) { return searchGoogle(ctx, i13) }},
		{"catalogue.source_openlibrary", "openlibrary", searchOpenLibrary},
	}
}

const (
	catalogueRequestTimeout = 7 * time.Second
	// Per-screen budget for the whole chain; past it, hand back to manual entry.
	enrichBudgetDesk     = 10 * time.Second // at the desk: do not keep the queue waiting
	enrichBudgetBookPage = 25 * time.Second // book page: an explicit request, worth the wait
)

// enrichWithin gives the chain the screen's budget and pushes the write
// deadline past it together: a chain outliving the socket's deadline gives a
// response cut off mid-way after the status has gone.
func enrichWithin(w http.ResponseWriter, r *http.Request, budget time.Duration) (context.Context, context.CancelFunc) {
	extendWriteDeadline(w, budget+5*time.Second)
	return context.WithTimeout(r.Context(), budget)
}

// Catalogues rate-limit per IP and a library has one: a day's memory of
// answers, and a forced rest after a 429, keep it from being blocked.

const (
	recordCacheTTL = 24 * time.Hour
	maxNotices     = 500
	restAfter429   = 15 * time.Minute
)

var errSourceResting = errors.New("source resting (rate limited)")

// A 404 from these by-identifier endpoints is an answer ("no such record"),
// not a fault: read as a fault, no absence would ever be remembered.
var errNoRecord = errors.New("no such record")

type memo struct {
	mu           sync.Mutex
	records      map[string]recordMemo
	restingUntil map[string]time.Time // host -> resting until
}

type recordMemo struct {
	n       *Record // nil = looked for without success
	expires time.Time
}

var catalogueMemo = &memo{
	records:      make(map[string]recordMemo),
	restingUntil: make(map[string]time.Time),
}

// The second return tells "nothing in memory" from "looked for, no result".

func (m *memo) record(i13 string) (*Record, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.records[i13]
	if !ok || time.Now().After(e.expires) {
		return nil, false
	}
	return e.n, true
}

func (m *memo) rememberRecord(i13 string, n *Record) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.records) >= maxNotices {
		m.records = make(map[string]recordMemo, maxNotices)
	}
	m.records[i13] = recordMemo{n: n, expires: time.Now().Add(recordCacheTTL)}
}

// "Enrich from the ISBN" is an explicit request to go back to the catalogues:
// serving a day-old answer from memory would be the opposite.

func (m *memo) forgetRecord(i13 string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.records, i13)
}

func (m *memo) isResting(host string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return time.Now().Before(m.restingUntil[host])
}

func (m *memo) putToRest(host string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.restingUntil[host] = time.Now().Add(restAfter429)
	log.Printf("catalogue: %s rate-limited us (429), resting for %s", host, restAfter429)
}

// No Timeout: the deadline comes from the context, the calling screen's budget.
// CheckRedirect keeps a catalogue from sending us somewhere it should not: every
// hop we start is https to a public catalogue, so a redirect to plain http or to
// a private address is a downgrade or an SSRF probe, not a real move.
var catalogueClient = &http.Client{
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		if req.URL.Scheme != "https" {
			return fmt.Errorf("refusing a redirect to non-https (%s)", req.URL.Scheme)
		}
		if err := refusePrivateHost(req.URL.Hostname()); err != nil {
			return err
		}
		return nil
	},
}

// refusePrivateHost blocks a redirect aimed at the machine itself or the local
// network — a cloud metadata endpoint (169.254.169.254), localhost, a 10.x box.
// The host is resolved because the target may be a name that points inward.
func refusePrivateHost(host string) error {
	var ips []net.IP
	if ip := net.ParseIP(host); ip != nil {
		ips = []net.IP{ip}
	} else {
		resolved, err := net.LookupIP(host)
		if err != nil {
			return fmt.Errorf("refusing a redirect to an unresolvable host (%s)", host)
		}
		ips = resolved
	}
	for _, ip := range ips {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
			ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
			return fmt.Errorf("refusing a redirect to a private address (%s)", host)
		}
	}
	return nil
}

// Returns the first usable record, or nil when no catalogue has the book. A
// nil record beside a non-nil error means "we could not find out" (a source
// resting, a fault, the budget spent), not "it does not exist". Only an
// absence every catalogue reported is remembered.
func enrich(ctx context.Context, i13, i10 string) (*Record, error) {
	return enrichReported(ctx, i13, i10, enrichLog{})
}

// enrichLog narrates the chain for a screen that shows which catalogue it is
// on. Either field may be nil. Every screen runs this one chain and memory;
// only the budget differs.
type enrichLog struct {
	trying   func(enrichSource)
	answered func(enrichSource, *Record, error)
}

func enrichReported(ctx context.Context, i13, i10 string, lg enrichLog) (*Record, error) {
	if n, known := catalogueMemo.record(i13); known {
		return n, nil
	}
	var faults []error
	for _, s := range enrichmentSources() {
		if err := ctx.Err(); err != nil {
			// Budget exhausted: the sources after this one were never asked.
			return nil, errors.Join(append(faults, err)...)
		}
		if lg.trying != nil {
			lg.trying(s)
		}
		n, err := s.fn(ctx, i13, i10)
		if errors.Is(err, errNoRecord) {
			err = nil // the catalogue answered: it holds no such record
		}
		if lg.answered != nil {
			lg.answered(s, n, err)
		}
		if err != nil {
			log.Printf("catalogue/%s: %v", s.name, err)
			faults = append(faults, fmt.Errorf("%s: %w", s.name, err))
			continue
		}
		if n != nil && n.Title != "" {
			catalogueMemo.rememberRecord(i13, n)
			return n, nil
		}
	}
	if len(faults) > 0 {
		return nil, errors.Join(faults...)
	}
	catalogueMemo.rememberRecord(i13, nil) // every catalogue said no
	return nil, nil
}

func request(ctx context.Context, url string) ([]byte, error) {
	return requestWithHeader(ctx, url, nil)
}

// requestWithHeader carries what must stay out of the URL: Go's network
// errors quote the URL, and those errors are logged.
func requestWithHeader(ctx context.Context, url string, header http.Header) ([]byte, error) {
	// One retry on a transient error, so a passing 503 does not hide a book.
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err // budget exhausted: do not retry for nothing
		}
		body, transient, err := requestOnce(ctx, url, header)
		if err == nil {
			return body, nil
		}
		lastErr = err
		if !transient {
			return nil, err
		}
	}
	return nil, lastErr
}

// 5xx and 403 are retried once: UniCat answers 403 then 200 to the same query.
// 400, 401 and 422 are final, since a wrong key or a malformed query answers
// the same way however often it is asked.
func retryWorthwhile(status int) bool {
	switch status {
	case http.StatusForbidden, http.StatusRequestTimeout, http.StatusTooEarly:
		return true
	}
	return status >= 500
}

func requestOnce(ctx context.Context, url string, header http.Header) (body []byte, transient bool, err error) {
	ctx, cancel := context.WithTimeout(ctx, catalogueRequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, false, err
	}
	for k, v := range header {
		req.Header[k] = v
	}
	req.Header.Set("User-Agent", userAgent())
	if catalogueMemo.isResting(req.URL.Host) {
		return nil, false, errSourceResting
	}
	resp, err := catalogueClient.Do(req)
	if err != nil {
		return nil, true, err // network error: transient
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests {
		catalogueMemo.putToRest(req.URL.Host)
		return nil, false, fmt.Errorf("status 429: rate limited")
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil, false, errNoRecord
	}
	if resp.StatusCode != http.StatusOK {
		return nil, retryWorthwhile(resp.StatusCode), fmt.Errorf("status %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20)) // 2 MiB is plenty
	return b, false, err
}

// BnF — SRU, unimarcxchange. Best coverage of the French-language collection.

func searchBnF(ctx context.Context, i13, i10 string) (*Record, error) {
	cql := fmt.Sprintf(`bib.ean all "%s" or bib.isbn all "%s"`, i13, i13)
	if i10 != "" {
		cql += fmt.Sprintf(` or bib.isbn all "%s"`, i10)
	}
	u := "https://catalogue.bnf.fr/api/SRU?" + url.Values{
		"version":        {"1.2"},
		"operation":      {"searchRetrieve"},
		"query":          {cql},
		"recordSchema":   {"unimarcxchange"},
		"maximumRecords": {"1"},
	}.Encode()

	body, err := request(ctx, u)
	if err != nil {
		return nil, err
	}
	return recordFromUnimarc(body, i13, i10)
}

// Separate from the network call so it can be tested on real responses.

func searchUniCat(ctx context.Context, i13, i10 string) (*Record, error) {
	q := "isbn=" + i13
	if i10 != "" {
		q += " or isbn=" + i10
	}
	u := "https://www.unicat.be/sru?" + url.Values{
		"version":        {"1.1"},
		"operation":      {"searchRetrieve"},
		"query":          {q},
		"recordSchema":   {"marcxml"},
		"maximumRecords": {"1"},
	}.Encode()

	body, err := request(ctx, u)
	if err != nil {
		return nil, err
	}
	return recordFromMarc21(body, i13, i10)
}

// Google Books — good recall on non-French titles.

func searchGoogle(ctx context.Context, i13 string) (*Record, error) {
	// country is required, otherwise 403 "unregistered callers". The optional
	// key raises the quota and avoids 429s; it goes in a header, never the URL.
	u := "https://www.googleapis.com/books/v1/volumes?country=FR&q=isbn:" + url.QueryEscape(i13)
	var header http.Header
	if k := googleKey(); k != "" {
		header = http.Header{"X-Goog-Api-Key": {k}}
	}
	body, err := requestWithHeader(ctx, u, header)
	if err != nil {
		return nil, err
	}
	var rep struct {
		Items []struct {
			VolumeInfo struct {
				Title         string   `json:"title"`
				Subtitle      string   `json:"subtitle"`
				Authors       []string `json:"authors"`
				Publisher     string   `json:"publisher"`
				PublishedDate string   `json:"publishedDate"`
				Language      string   `json:"language"`
			} `json:"volumeInfo"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &rep); err != nil {
		return nil, fmt.Errorf("unreadable answer: %w", err)
	}
	if len(rep.Items) == 0 {
		return nil, nil
	}
	v := rep.Items[0].VolumeInfo
	return &Record{
		ISBN13:    i13,
		ISBN10:    To10(i13),
		Title:     v.Title,
		Subtitle:  v.Subtitle,
		Authors:   strings.Join(v.Authors, " ; "),
		Publisher: v.Publisher,
		Year:      year4(v.PublishedDate),
		Language:  lang2(v.Language),
		Source:    "google",
		Payload:   string(body),
	}, nil
}

// Open Library — last resort. /isbn/{isbn}.json redirects to the edition's
// record; not /search.json, which answers for the work and would fill the form
// with another edition. Both ISBN forms are asked: it files a book under the
// number it was handed. Author keys are resolved one request each, at most
// maxOpenLibraryAuthors and only while the budget allows.

const (
	maxOpenLibraryAuthors = 3
	openLibraryAuthorRoom = 1500 * time.Millisecond
)

var reOpenLibraryAuthor = regexp.MustCompile(`^/authors/OL[0-9]+A$`)

type openLibraryEdition struct {
	Key         string   `json:"key"`
	Title       string   `json:"title"`
	Subtitle    string   `json:"subtitle"`
	Publishers  []string `json:"publishers"`
	PublishDate string   `json:"publish_date"`
	ISBN13      []string `json:"isbn_13"`
	ISBN10      []string `json:"isbn_10"`
	Authors     []struct {
		Key string `json:"key"`
	} `json:"authors"`
	Languages []struct {
		Key string `json:"key"`
	} `json:"languages"`
}

func searchOpenLibrary(ctx context.Context, i13, i10 string) (*Record, error) {
	var body []byte
	var err error
	for _, isbn := range []string{i13, i10} {
		if isbn == "" {
			continue
		}
		body, err = request(ctx, "https://openlibrary.org/isbn/"+url.PathEscape(isbn)+".json")
		if !errors.Is(err, errNoRecord) {
			break
		}
	}
	if err != nil {
		return nil, err
	}
	var ed openLibraryEdition
	if err := json.Unmarshal(body, &ed); err != nil {
		return nil, fmt.Errorf("unreadable answer: %w", err)
	}
	if strings.TrimSpace(ed.Title) == "" || !ed.holds(i13, i10) {
		return nil, nil
	}
	n := &Record{
		ISBN13:    i13,
		ISBN10:    i10,
		Title:     ed.Title,
		Subtitle:  ed.Subtitle,
		Authors:   strings.Join(openLibraryAuthors(ctx, ed), " ; "),
		Publisher: strings.Join(ed.Publishers, " ; "),
		Year:      year4(ed.PublishDate),
		Source:    "openlibrary",
		Payload:   string(body),
	}
	if len(ed.Languages) > 0 {
		n.Language = lang2(strings.TrimPrefix(ed.Languages[0].Key, "/languages/"))
	}
	if strings.HasPrefix(ed.Key, "/books/") {
		n.URL = "https://openlibrary.org" + ed.Key
	}
	return n, nil
}

// holds says whether the edition is the one asked for. An edition that lists no
// ISBN at all is taken at the redirect's word.
func (ed openLibraryEdition) holds(i13, i10 string) bool {
	if len(ed.ISBN13) == 0 && len(ed.ISBN10) == 0 {
		return true
	}
	for _, v := range append(ed.ISBN13, ed.ISBN10...) {
		if v = NormaliseISBN(v); v == i13 || (i10 != "" && v == i10) {
			return true
		}
	}
	return false
}

// openLibraryAuthors resolves the edition's author keys to names, in order,
// skipping any that cannot be read: an author missing from the form is a
// field left for the librarian, not a reason to lose the record.
func openLibraryAuthors(ctx context.Context, ed openLibraryEdition) []string {
	var names []string
	for i, a := range ed.Authors {
		if i == maxOpenLibraryAuthors {
			break
		}
		if dl, ok := ctx.Deadline(); ok && time.Until(dl) < openLibraryAuthorRoom {
			break
		}
		if !reOpenLibraryAuthor.MatchString(a.Key) {
			continue
		}
		body, err := request(ctx, "https://openlibrary.org"+a.Key+".json")
		if err != nil {
			log.Printf("catalogue/openlibrary: author %s: %v", a.Key, err)
			continue
		}
		var au struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(body, &au) == nil && strings.TrimSpace(au.Name) != "" {
			names = append(names, strings.TrimSpace(au.Name))
		}
	}
	return names
}
