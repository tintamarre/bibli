package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The checks a unit test cannot make: that the routes and the authentication
// gate are wired as intended. Status codes and Location only, never the
// body.

func testHandler(t *testing.T) (*app, http.Handler) {
	t.Helper()
	pages, err := loadTemplates()
	if err != nil {
		t.Fatalf("templates: %v", err)
	}
	a := &app{
		db:         testDB(t),
		pages:      pages,
		adminPass:  "password",
		sessionKey: deriveKey("secret", "password"),
		// main builds one whatever -cache-dir says; the settings screen reads it.
		covers: newCoverCache(""),
	}
	return a, a.handler()
}

// signedIn is the cookie a librarian's browser carries.
func signedIn(t *testing.T, a *app) *http.Cookie {
	t.Helper()
	w := httptest.NewRecorder()
	a.setSessionCookie(w, time.Now())
	return readCookie(t, w, sessionCookieName)
}

func get(h http.Handler, path string, c *http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", path, nil)
	if c != nil {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// Everything but the allow list needs a session.
func TestAScreenWithoutASessionGoesToTheLogin(t *testing.T) {
	_, h := testHandler(t)
	for _, path := range []string{"/settings", "/stats", "/borrowers", "/inventory", "/loans", "/borrow", "/borrow/lookup?id=0123456789abcdef", "/borrowers/import/template.xlsx", "/stats/collection.xlsx", "/stats/collection.csv"} {
		w := get(h, path, nil)
		if w.Code != http.StatusSeeOther {
			t.Errorf("GET %s without a session = %d, want 303 to the login", path, w.Code)
		}
		if loc := w.Header().Get("Location"); loc == "" || loc[:7] != "/login?" {
			t.Errorf("GET %s redirected to %q, want the login screen", path, loc)
		}
	}
}

// And the allow list does not: a browser asks for these before anyone signs in.
func TestThePublicSurfacesNeedNoSession(t *testing.T) {
	_, h := testHandler(t)
	for _, path := range []string{"/", "/login", "/healthcheck", "/static/app.css"} {
		if w := get(h, path, nil); w.Code != http.StatusOK {
			t.Errorf("GET %s without a session = %d, want 200 — it is a public surface", path, w.Code)
		}
	}
}

// A static file is served, but the folder holding it is not: http.FileServer
// would otherwise list every asset's name.
func TestStaticServesNoDirectoryListing(t *testing.T) {
	_, h := testHandler(t)
	for _, path := range []string{"/static/", "/static/vendor/"} {
		if w := get(h, path, nil); w.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404 — a directory must not be listed", path, w.Code)
		}
	}
	if w := get(h, "/static/app.css", nil); w.Code != http.StatusOK {
		t.Errorf("GET /static/app.css = %d, want 200 — a file must still be served", w.Code)
	}
}

func TestASessionOpensTheScreens(t *testing.T) {
	a, h := testHandler(t)
	c := signedIn(t, a)
	for _, path := range []string{"/settings", "/stats", "/borrowers", "/inventory", "/borrowers/import/template.xlsx", "/stats/collection.xlsx", "/stats/collection.csv"} {
		if w := get(h, path, c); w.Code != http.StatusOK {
			t.Errorf("GET %s with a session = %d, want 200", path, w.Code)
		}
	}
}

// The backup route must still ask isBackupName. The files are planted, so a 404
// is the refusal and not their absence.
func TestBackupDownloadServesNothingOutsideTheRotation(t *testing.T) {
	a, h := testHandler(t)
	a.backupDir = t.TempDir()
	c := signedIn(t, a)

	plant := func(name string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(a.backupDir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	plant("biblio-daily-monday.db") // ours
	plant("biblio.db")              // the live database, never served
	plant("readme.txt")             // whatever else is in the folder

	if w := get(h, "/settings/backup/biblio-daily-monday.db", c); w.Code != http.StatusOK {
		t.Errorf("a backup of the rotation = %d, want 200", w.Code)
	}
	for _, name := range []string{
		"biblio.db",
		"readme.txt",
		"..%2f..%2fbiblio.db",
		"%2e%2e%2f%2e%2e%2fetc%2fpasswd",
		"biblio-weekly-9.db", // outside the four slots
	} {
		if w := get(h, "/settings/backup/"+name, c); w.Code != http.StatusNotFound {
			t.Errorf("GET /settings/backup/%s = %d, want 404", name, w.Code)
		}
	}

	// And nothing at all when backups are switched off.
	a.backupDir = ""
	if w := get(h, "/settings/backup/biblio-daily-monday.db", c); w.Code != http.StatusNotFound {
		t.Errorf("backups disabled = %d, want 404", w.Code)
	}
}

// The tracking page: a revocable token is the only authentication.
func TestTheLoansLinkOpensOnlyForALiveReader(t *testing.T) {
	a, h := testHandler(t)
	const token = "0123456789abcdef0123456789abcdef"
	// Léa, a live reader of the fixture; checked, so a no-op UPDATE cannot pass.
	res, err := a.db.Exec(`UPDATE borrower SET tracking_token = ? WHERE id = 101`, token)
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		t.Fatalf("fixture: %d borrowers given a token, want 1", n)
	}
	if w := get(h, "/track/"+token, nil); w.Code != http.StatusOK {
		t.Fatalf("a live reader's link = %d, want 200", w.Code)
	}
	if w := get(h, "/track/"+token+"x", nil); w.Code != http.StatusNotFound {
		t.Errorf("an unknown token = %d, want 404", w.Code)
	}

	// Deactivated: the link stops working, without being revoked.
	if _, err := a.db.Exec(`UPDATE borrower SET active = 0 WHERE id = 101`); err != nil {
		t.Fatal(err)
	}
	if w := get(h, "/track/"+token, nil); w.Code != http.StatusNotFound {
		t.Errorf("a deactivated reader's link = %d, want 404", w.Code)
	}

	// Revoked: the token is NULL, and NULL matches no token handed in a URL.
	if _, err := a.db.Exec(`UPDATE borrower SET active = 1, tracking_token = NULL WHERE id = 101`); err != nil {
		t.Fatal(err)
	}
	if w := get(h, "/track/"+token, nil); w.Code != http.StatusNotFound {
		t.Errorf("a revoked link = %d, want 404", w.Code)
	}
	if w := get(h, "/track/", nil); w.Code == http.StatusOK {
		t.Error("an empty token opened a page: NULL must match nothing")
	}
}

// On unless the library turns it off: then no column, no way to create a link,
// and a link already given is closed.
func TestALibraryCanTurnTheLoansLinksOff(t *testing.T) {
	a, h := testHandler(t)
	c := signedIn(t, a)
	const token = "0123456789abcdef0123456789abcdef"
	if _, err := a.db.Exec(`UPDATE borrower SET tracking_token = ? WHERE id = 101`, token); err != nil {
		t.Fatal(err)
	}
	loadSettingsCache(a.db) // a fresh database has no tracking_links row
	t.Cleanup(func() { setTrackingLinks(true) })
	if !trackingLinks() {
		t.Fatal("the loans links are off in a fresh database")
	}
	if w := get(h, "/borrowers", c); !strings.Contains(w.Body.String(), "/borrowers/101/token") {
		t.Error("the borrower list hides loans links by default")
	}

	if _, err := a.db.Exec(`INSERT INTO setting (key, value, label) VALUES ('tracking_links', '0', '')`); err != nil {
		t.Fatal(err)
	}
	loadSettingsCache(a.db)
	if w := get(h, "/track/"+token, nil); w.Code != http.StatusNotFound {
		t.Errorf("a given link while off = %d, want 404", w.Code)
	}
	if w := get(h, "/borrowers", c); strings.Contains(w.Body.String(), "/borrowers/101/token") {
		t.Error("the borrower list offers loans links while off")
	}
	r := httptest.NewRequest(http.MethodPost, "/borrowers/102/token", nil)
	r.AddCookie(c)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Errorf("creating a link while off = %d, want 404", w.Code)
	}
	if n := count(t, a.db, `SELECT COUNT(*) FROM borrower WHERE id = 102 AND tracking_token IS NOT NULL`); n != 0 {
		t.Error("a link was created while off")
	}
}

// The Mac and Windows apps start with -tracking-links=false: no one else reaches
// them, so the setting is greyed out and saving the screen leaves it alone.
func TestASingleComputerInstallOffersNoLoansLinks(t *testing.T) {
	a, h := testHandler(t)
	c := signedIn(t, a)
	const token = "0123456789abcdef0123456789abcdef"
	if _, err := a.db.Exec(`UPDATE borrower SET tracking_token = ? WHERE id = 101`, token); err != nil {
		t.Fatal(err)
	}
	loadSettingsCache(a.db)
	allowTrackingLinks(false)
	t.Cleanup(func() { allowTrackingLinks(true); setTrackingLinks(true) })

	if w := get(h, "/track/"+token, nil); w.Code != http.StatusNotFound {
		t.Errorf("a given link on a single computer = %d, want 404", w.Code)
	}
	if w := get(h, "/borrowers", c); strings.Contains(w.Body.String(), "/borrowers/101/token") {
		t.Error("the borrower list offers loans links on a single computer")
	}
	page := get(h, "/settings", c).Body.String()
	if !strings.Contains(page, `name="tracking_links" value="1" disabled`) {
		t.Error("the loans links checkbox can be ticked on a single computer")
	}

	form := url.Values{"library_name": {""}, "language": {"fr"}, "theme": {defaultTheme},
		"loan_days": {"14"}, "retention_years": {"3"}, "tracking_links": {"1"}}
	r := httptest.NewRequest(http.MethodPost, "/settings", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(c)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("saving the settings = %d, want 303", w.Code)
	}
	if n := count(t, a.db, `SELECT COUNT(*) FROM setting WHERE key = 'tracking_links'`); n != 0 {
		t.Error("saving the settings on a single computer wrote the loans links choice")
	}
	if trackingLinks() {
		t.Error("a forged form turned the loans links on for a single computer")
	}
}

// Deleting a book is the only irreversible route, and a POST.
func TestDeletingABookNeedsASession(t *testing.T) {
	a, h := testHandler(t)

	r := httptest.NewRequest("POST", "/book/3/delete", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusSeeOther {
		t.Errorf("POST /book/3/delete without a session = %d, want 303 to the login", w.Code)
	}
	if n := count(t, a.db, `SELECT COUNT(*) FROM book WHERE id = 3`); n != 1 {
		t.Error("a request with no session deleted the book")
	}
}

// The desk's live search answers 204 to an empty box, which HTMX does not swap,
// so a queued search cannot erase what a scan just drew. The 200 is asserted too.
func TestAnEmptyLiveSearchLeavesTheScreenAlone(t *testing.T) {
	a, h := testHandler(t)
	c := signedIn(t, a)

	post := func(code string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest("POST", "/borrow/book-search",
			strings.NewReader(url.Values{"code": {code}}.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.AddCookie(c)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}

	for _, empty := range []string{"", "   "} {
		if w := post(empty); w.Code != http.StatusNoContent {
			t.Errorf("a search for %q = %d, want 204 so the page is left alone", empty, w.Code)
		}
	}
	// "Le Petit Prince" is in the fixture, so a real word still draws its list.
	if w := post("prince"); w.Code != http.StatusOK || w.Body.Len() == 0 {
		t.Errorf("a search for a word on the shelf = %d with %d bytes, want 200 and a list",
			w.Code, w.Body.Len())
	}
}

// The early return in a.home is what keeps / public but empty: with no database
// the landing page renders while the signed-in screen cannot.
func TestTheLandingPageReadsNothing(t *testing.T) {
	a, h := testHandler(t)
	cookie := signedIn(t, a)
	a.db.Close()

	if w := get(h, "/", nil); w.Code != http.StatusOK {
		t.Errorf("signed out: want 200 from a page that needs no data, got %d", w.Code)
	}
	if w := get(h, "/", cookie); w.Code != http.StatusInternalServerError {
		t.Errorf("signed in: want 500 with no database, got %d — "+
			"the screen is not reading the collection at all", w.Code)
	}
}

// A sibling subdomain is "same-site", which SameSite=Lax trusts: the guard must
// refuse it as it refuses any other origin, with the librarian's cookie attached.
func TestACrossOriginPostIsRefused(t *testing.T) {
	a, h := testHandler(t)
	c := signedIn(t, a)

	post := func(path string, headers map[string]string) int {
		t.Helper()
		r := httptest.NewRequest("POST", "https://bibli.example.org"+path,
			strings.NewReader(url.Values{"code": {"prince"}}.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		r.AddCookie(c)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}

	for name, headers := range map[string]map[string]string{
		"cross-site":            {"Sec-Fetch-Site": "cross-site", "Origin": "https://evil.example"},
		"sibling subdomain":     {"Sec-Fetch-Site": "same-site", "Origin": "https://blog.example.org"},
		"old browser, no fetch": {"Origin": "https://blog.example.org"},
	} {
		if code := post("/book/3/delete", headers); code != http.StatusForbidden {
			t.Errorf("%s: POST /book/3/delete = %d, want 403", name, code)
		}
	}
	if n := count(t, a.db, `SELECT COUNT(*) FROM book WHERE id = 3`); n != 1 {
		t.Error("a cross-origin request deleted the book")
	}

	// The same request from Bibli's own pages goes through.
	for name, headers := range map[string]map[string]string{
		"same-origin":       {"Sec-Fetch-Site": "same-origin", "Origin": "https://bibli.example.org"},
		"old browser, own":  {"Origin": "https://bibli.example.org"},
		"no browser at all": {},
		"HTMX, same-origin": {"Sec-Fetch-Site": "same-origin", "HX-Request": "true"},
	} {
		if code := post("/borrow/book-search", headers); code != http.StatusOK {
			t.Errorf("%s: POST /borrow/book-search = %d, want 200", name, code)
		}
	}
}

// Every response carries the policy, the public ones and the refusals included.
func TestEveryResponseCarriesTheContentSecurityPolicy(t *testing.T) {
	a, h := testHandler(t)
	c := signedIn(t, a)
	for _, tc := range []struct {
		path   string
		cookie *http.Cookie
	}{{"/", nil}, {"/login", nil}, {"/settings", nil}, {"/static/app.js", nil}, {"/nope", c}, {"/borrow", c}} {
		w := get(h, tc.path, tc.cookie)
		if got := w.Header().Get("Content-Security-Policy"); got != contentSecurityPolicy {
			t.Errorf("GET %s: Content-Security-Policy = %q", tc.path, got)
		}
	}
	for _, directive := range []string{"default-src 'self'", "object-src 'none'", "base-uri 'self'", "frame-ancestors 'none'", "form-action 'self'"} {
		if !strings.Contains(contentSecurityPolicy, directive) {
			t.Errorf("the policy lacks %s", directive)
		}
	}
	if strings.Contains(contentSecurityPolicy, "http") || strings.Contains(contentSecurityPolicy, "*") {
		t.Errorf("the policy names a host other than our own: %s", contentSecurityPolicy)
	}
}

// The gate renews a cookie past an hour of use without moving its sign-in, and
// turns away one past its week however recently it was signed.
func TestTheGateRenewsWithinTheWeekAndNotBeyond(t *testing.T) {
	a, h := testHandler(t)
	now := time.Now()
	signIn := now.Add(-3 * 24 * time.Hour)

	c := &http.Cookie{Name: sessionCookieName, Value: signSession(a.sessionKey, signIn, now.Add(-2*time.Hour))}
	w := get(h, "/settings", c)
	if w.Code != http.StatusOK {
		t.Fatalf("a three-day-old session signed two hours ago = %d, want 200", w.Code)
	}
	renewed := readCookie(t, w, sessionCookieName)
	s, ok := readSession(a.sessionKey, renewed.Value, now)
	if !ok || s.issued.Unix() != signIn.Unix() {
		t.Errorf("renewed cookie: valid %v, sign-in %s, want the original %s", ok, s.issued, signIn)
	}

	expired := &http.Cookie{Name: sessionCookieName,
		Value: signSession(a.sessionKey, now.Add(-sessionLifetime-time.Minute), now.Add(-time.Minute))}
	if w := get(h, "/settings", expired); w.Code != http.StatusSeeOther {
		t.Errorf("a session past its week = %d, want 303 to the login", w.Code)
	}
}

// Signing in is a POST too, and must still work from the login screen.
func TestTheLoginPostPassesTheGuard(t *testing.T) {
	a, h := testHandler(t)
	a.throttle = newThrottle()
	r := httptest.NewRequest("POST", "https://bibli.example.org/login",
		strings.NewReader(url.Values{"password": {"password"}}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	r.Header.Set("Origin", "https://bibli.example.org")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusSeeOther {
		t.Errorf("POST /login from the login screen = %d, want 303", w.Code)
	}
}

// Every screen, every book and every borrower, over the demonstration data:
// its NULLs (no ISBN, no group, no year, no location) and its lost, withdrawn
// and anonymised rows are what a Scan into the wrong type trips on.
func TestEveryScreenOpensOverTheDemonstrationData(t *testing.T) {
	a, h := testHandler(t)
	if err := demoReset(a.db); err != nil {
		t.Fatalf("demo reset: %v", err)
	}
	c := signedIn(t, a)

	paths := []string{
		"/", "/loans", "/loans?overdue=1", "/borrow", "/return", "/catalogue", "/catalogue/manual",
		"/borrowers", "/borrowers?inactive=1", "/borrowers/import", "/borrowers/cards", "/borrowers/rollover",
		"/inventory?q=loup", "/inventory?status=lost", "/inventory?loan=out", "/inventory?loan=shelf", "/inventory?added=7d",
		"/print/labels", "/print/loans", "/print/overdue", "/print/inventory", "/export.csv", "/export.xlsx",
		"/stats", "/stats/collection.xlsx", "/stats/collection.csv", "/settings", "/about",
	}
	for key := range invSortColumns {
		paths = append(paths, "/inventory?sort="+key, "/inventory?sort="+key+"&dir=asc")
	}
	for _, q := range []struct{ prefix, query string }{
		{"/book/", `SELECT id FROM book`},
		{"/borrowers/", `SELECT id FROM borrower`},
	} {
		// Read to the end before any request: with one SQLite connection, querying
		// while these rows are open would deadlock the handler.
		rows, err := a.db.Query(q.query)
		if err != nil {
			t.Fatal(err)
		}
		var ids []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				t.Fatal(err)
			}
			ids = append(ids, id)
		}
		rows.Close()
		if len(ids) == 0 {
			t.Fatalf("%s: the demonstration data has none", q.query)
		}
		for _, id := range ids {
			paths = append(paths, q.prefix+id)
		}
	}

	for _, path := range paths {
		if w := get(h, path, c); w.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", path, w.Code)
		}
	}
}
