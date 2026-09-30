package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCoverCacheFiles(t *testing.T) {
	dir := t.TempDir()
	c := newCoverCache(dir)

	// Never looked for.
	if _, _, known := c.read("9782070408504"); known {
		t.Error("unknown ISBN reported as known")
	}

	// A written thumbnail is read back identically, and survives a new cache
	// object: the point is that it is on disk, not in memory.
	c.write("9782070408504", []byte("\xff\xd8\xff-jpeg"), "image/jpeg")
	c2 := newCoverCache(dir)
	img, ct, known := c2.read("9782070408504")
	if !known || string(img) != "\xff\xd8\xff-jpeg" || ct != "image/jpeg" {
		t.Fatalf("read back: known=%v ct=%q img=%q", known, ct, img)
	}

	// Absence is remembered too: known, without an image.
	c.write("9782211037495", nil, "")
	img, _, known = c.read("9782211037495")
	if !known {
		t.Error("absence not remembered: the catalogues would be queried every time")
	}
	if img != nil {
		t.Error("an absence must not return an image")
	}

	// Forgetting returns to the "never looked for" state (the Enrich button).
	c.forget("9782070408504")
	c.forget("9782211037495")
	for _, i := range []string{"9782070408504", "9782211037495"} {
		if _, _, known := c.read(i); known {
			t.Errorf("%s still known after being forgotten", i)
		}
	}

	// No temporary file left behind.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Errorf("temporary file left behind: %s", e.Name())
		}
	}
}

// An empty directory disables the cache without breaking anything.
func TestCoverCacheDisabled(t *testing.T) {
	c := newCoverCache("")
	c.write("9782070408504", []byte("x"), "image/jpeg")
	if _, _, known := c.read("9782070408504"); known {
		t.Error("cache disabled: nothing must be remembered")
	}
	if count, bytes, absent := c.status(); count != 0 || bytes != 0 || absent != 0 {
		t.Errorf("status of a disabled cache: %d / %d", count, bytes)
	}
}

func TestHumanSize(t *testing.T) {
	cases := map[int64]string{0: "0 o", 512: "512 o", 2048: "2 Kio", 3 << 20: "3.0 Mio"}
	for b, want := range cases {
		if got := humanSize(b, "fr"); got != want {
			t.Errorf("humanSize(%d, fr) = %q, want %q", b, got, want)
		}
	}
}

// The byte units follow the interface language.
func TestHumanSizeEnglish(t *testing.T) {
	cases := map[int64]string{0: "0 B", 512: "512 B", 2048: "2 KiB", 3 << 20: "3.0 MiB"}
	for b, want := range cases {
		if got := humanSize(b, "en"); got != want {
			t.Errorf("humanSize(%d, en) = %q, want %q", b, got, want)
		}
	}
}

// The cache size counts covers only, not absence markers or half-written files.
func TestCoverCacheStatusCountsOnlyImages(t *testing.T) {
	dir := t.TempDir()
	c := newCoverCache(dir)

	c.write("9782070408504", []byte("0123456789"), "image/jpeg") // 10 bytes
	c.write("9782211037495", []byte("012345"), "image/png")      // 6 bytes
	c.write("9782352046783", nil, "")                            // absence marker
	if err := os.WriteFile(filepath.Join(dir, "leftover.tmp"), []byte("xxxx"), 0o644); err != nil {
		t.Fatal(err)
	}

	count, bytes, absent := c.status()
	if count != 2 {
		t.Errorf("count = %d, want 2 (the absence marker and the .tmp do not count)", count)
	}
	if bytes != 16 {
		t.Errorf("size = %d, want 16", bytes)
	}
	if absent != 1 {
		t.Errorf("absences = %d, want 1", absent)
	}
}

// The absence markers go and the images stay.
func TestForgetAbsencesKeepsTheImages(t *testing.T) {
	dir := t.TempDir()
	c := newCoverCache(dir)

	c.write("9782070408504", []byte("0123456789"), "image/jpeg")
	c.write("9782352046783", nil, "")
	c.write("9782290333129", nil, "")

	if n := c.forgetAbsences(); n != 2 {
		t.Errorf("forgotten = %d, want 2", n)
	}
	count, _, absent := c.status()
	if count != 1 {
		t.Errorf("images kept = %d, want 1", count)
	}
	if absent != 0 {
		t.Errorf("absences left = %d, want 0", absent)
	}
	// And the book is really asked about again.
	if _, _, known := c.read("9782352046783"); known {
		t.Error("a forgotten absence is still known: the catalogues would not be asked again")
	}
	if _, _, known := c.read("9782070408504"); !known {
		t.Error("a kept image is no longer known: it would be downloaded again")
	}
}

// Nothing to forget, and a disabled cache, are both a quiet zero, not a failure.
func TestForgetAbsencesOnNothing(t *testing.T) {
	if n := newCoverCache(t.TempDir()).forgetAbsences(); n != 0 {
		t.Errorf("empty cache: forgotten = %d, want 0", n)
	}
	if n := newCoverCache("").forgetAbsences(); n != 0 {
		t.Errorf("disabled cache: forgotten = %d, want 0", n)
	}
}

// A type we do not keep is not written: the extension carries the type.
func TestCoverCacheRefusesAnUnknownType(t *testing.T) {
	dir := t.TempDir()
	c := newCoverCache(dir)
	c.write("9782070408504", []byte("%PDF-1.4"), "application/pdf")
	if _, _, known := c.read("9782070408504"); known {
		t.Error("a type the cache cannot name was kept anyway")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("%d file(s) written for a refused type", len(entries))
	}
}

// A cache directory that cannot be created disables the cache, not the application.
func TestCoverCacheFallsBackWhenTheDirectoryIsUnusable(t *testing.T) {
	blocking := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(blocking, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := newCoverCache(filepath.Join(blocking, "cache"))
	if c.dir != "" {
		t.Errorf("cache dir = %q, want disabled", c.dir)
	}
	c.write("9782070408504", []byte("x"), "image/jpeg") // must not panic
}

// --- Fetching -------------------------------------------------------------

// The server fetches the thumbnail, so no pupil's browser calls a
// third party, and only an ISBN leaves the school.

func TestFetchCoverPrefersOpenLibrary(t *testing.T) {
	calls := stubCatalogues(t, func(r *http.Request) (int, string, string) {
		if r.URL.Host == "covers.openlibrary.org" {
			return 200, "image/jpeg", "\xff\xd8\xff-openlibrary"
		}
		return 404, "", ""
	})

	img, ct, _ := fetchCover(context.Background(), petitPrince13, petitPrince10)
	if string(img) != "\xff\xd8\xff-openlibrary" || ct != "image/jpeg" {
		t.Fatalf("image %q, type %q", img, ct)
	}
	// Open Library answered: no SRU request to the BnF.
	if got := calls.count(); got != 1 {
		t.Errorf("%d requests, want 1: the BnF was queried for nothing", got)
	}
	for _, u := range calls.all() {
		if !strings.Contains(u, petitPrince13) {
			t.Errorf("request without the ISBN: %s", u)
		}
	}
}

// Both ISBN forms for thumbnails: Open Library answers on the number it was handed, so a
// pre-2007 book has its cover under the ISBN-10 only.
func TestFetchCoverAsksOpenLibraryForBothForms(t *testing.T) {
	calls := stubCatalogues(t, func(r *http.Request) (int, string, string) {
		if r.URL.Host == "covers.openlibrary.org" && strings.Contains(r.URL.Path, petitPrince10) {
			return 200, "image/jpeg", "\xff\xd8\xff-old-edition"
		}
		return 404, "", ""
	})

	img, ct, _ := fetchCover(context.Background(), petitPrince13, petitPrince10)
	if string(img) != "\xff\xd8\xff-old-edition" || ct != "image/jpeg" {
		t.Fatalf("image %q, type %q: the ISBN-10 was never asked for", img, ct)
	}
	// Found on the second form: the BnF is not queried at all.
	if got := calls.hosts(t); len(got) != 2 {
		t.Errorf("requests: %v, want two to Open Library and nothing else", got)
	}
}

// A 979 prefix has no ISBN-10, and neither has a book entered by hand
// without an ISBN: nothing is asked for an empty number.
func TestFetchCoverAsksOnlyForTheFormsThatExist(t *testing.T) {
	calls := stubCatalogues(t, func(r *http.Request) (int, string, string) {
		return 404, "", ""
	})

	_, _, _ = fetchCover(context.Background(), "9791023505108", "")
	for _, u := range calls.all() {
		if strings.Contains(u, "/b/isbn/-M.jpg") {
			t.Errorf("a cover was asked for under an empty ISBN: %s", u)
		}
	}
}

// The BnF is the second source: it announces its thumbnail in the record, so
// finding it costs an SRU request first.
func TestFetchCoverFallsBackToTheBnF(t *testing.T) {
	calls := stubCatalogues(t, func(r *http.Request) (int, string, string) {
		switch {
		case r.URL.Host == "covers.openlibrary.org":
			return 404, "", "" // no thumbnail there
		case r.URL.Path == "/api/SRU":
			return 200, "text/xml", coverRecord
		case r.URL.Path == "/cover":
			return 200, "image/jpeg", "\xff\xd8\xff-bnf"
		}
		return 404, "", ""
	})

	img, ct, _ := fetchCover(context.Background(), petitPrince13, petitPrince10)
	if string(img) != "\xff\xd8\xff-bnf" || ct != "image/jpeg" {
		t.Fatalf("image %q, type %q", img, ct)
	}
	// Open Library twice, once per form of the ISBN, then the BnF record and
	// the thumbnail it announces.
	want := []string{"covers.openlibrary.org", "covers.openlibrary.org", "catalogue.bnf.fr", "catalogue.bnf.fr"}
	if got := calls.hosts(t); len(got) != len(want) {
		t.Fatalf("requests: %v, want %v", got, want)
	}
}

// A cached record found elsewhere carries no cover URL: the BnF is still asked.
func TestARecordFromAnotherSourceDoesNotShortCircuitTheBnF(t *testing.T) {
	calls := stubCatalogues(t, func(r *http.Request) (int, string, string) {
		switch {
		case r.URL.Host == "covers.openlibrary.org":
			return 404, "", ""
		case r.URL.Path == "/api/SRU":
			return 200, "text/xml", coverRecord
		case r.URL.Path == "/cover":
			return 200, "image/jpeg", "\xff\xd8\xff-bnf"
		}
		return 404, "", ""
	})
	// What enrichment would have left behind after Google answered.
	catalogueMemo.rememberRecord(petitPrince13, &Record{Title: "Le Petit Prince", Source: "google"})

	if img, _, _ := fetchCover(context.Background(), petitPrince13, petitPrince10); img == nil {
		t.Fatal("no cover: the BnF was skipped because Google had answered")
	}
	for _, h := range calls.hosts(t) {
		if h == "catalogue.bnf.fr" {
			return
		}
	}
	t.Error("the BnF was never asked")
}

// An HTML error page, or a format we cannot name, is not cached as an image.
func TestDownloadImageRejectsWhatIsNotAnImage(t *testing.T) {
	stubCatalogues(t, func(r *http.Request) (int, string, string) {
		return 200, "text/html; charset=utf-8", "<html>service unavailable</html>"
	})
	img, ct, err := downloadImage(context.Background(), "https://covers.openlibrary.org/x.jpg")
	if img != nil {
		t.Errorf("HTML accepted as an image: %q (%s)", img, ct)
	}
	if err == nil {
		t.Error("an HTML page passed for the source saying it has no cover")
	}
}

// The charset that follows the type must not stop it being recognised.
func TestDownloadImageIgnoresTheCharsetParameter(t *testing.T) {
	stubCatalogues(t, func(r *http.Request) (int, string, string) {
		return 200, "image/jpeg; charset=binary", "\xff\xd8\xff"
	})
	img, ct, _ := downloadImage(context.Background(), "https://catalogue.bnf.fr/cover")
	if img == nil || ct != "image/jpeg" {
		t.Errorf("image %q, type %q, want a JPEG", img, ct)
	}
}

func TestDownloadImagePutsARateLimitedHostToRest(t *testing.T) {
	stubCatalogues(t, func(r *http.Request) (int, string, string) {
		return 429, "", "slow down"
	})
	if _, _, err := downloadImage(context.Background(), "https://covers.openlibrary.org/x.jpg"); err == nil {
		t.Error("a 429 passed for an answer")
	}
	if !catalogueMemo.isResting("covers.openlibrary.org") {
		t.Error("a 429 on a thumbnail did not put the host to rest")
	}
}

// A BnF record that announces its thumbnail, as fetchCover needs to find one.
const coverRecord = `<searchRetrieveResponse><records><record><recordData><record>
 <datafield tag="200"><subfield code="a">Le petit prince</subfield></datafield>
 <datafield tag="856" ind2="2">
  <subfield code="u">274622</subfield>
  <subfield code="b">Première de couverture</subfield>
 </datafield>
</record></recordData></record></records></searchRetrieveResponse>`

// When enrichment has just been through the BnF, the cover URL is taken from
// memory rather than from a second SRU request.
func TestABnFRecordInMemoryShortCircuitsTheSRURequest(t *testing.T) {
	calls := stubCatalogues(t, func(r *http.Request) (int, string, string) {
		switch {
		case r.URL.Host == "covers.openlibrary.org":
			return 404, "", ""
		case r.URL.Path == "/cover":
			return 200, "image/jpeg", "\xff\xd8\xff-bnf"
		}
		t.Errorf("unexpected request: %s", r.URL)
		return 404, "", ""
	})
	catalogueMemo.rememberRecord(petitPrince13, &Record{
		Title:    "Le petit prince",
		Source:   "bnf",
		CoverURL: "https://catalogue.bnf.fr/cover?appName=NE&idImage=274622&couverture=1",
	})

	img, _, _ := fetchCover(context.Background(), petitPrince13, petitPrince10)
	if string(img) != "\xff\xd8\xff-bnf" {
		t.Fatalf("image = %q", img)
	}
	for _, u := range calls.all() {
		if strings.Contains(u, "/api/SRU") {
			t.Errorf("an SRU request was fired for a URL already in memory: %s", u)
		}
	}
}

// A BnF record with no 856 has no thumbnail: the neutral image is served.
func TestNoCoverIsNotAFailure(t *testing.T) {
	stubCatalogues(t, func(r *http.Request) (int, string, string) {
		if r.URL.Path == "/api/SRU" {
			return 200, "text/xml", bnfResponse // a record, but no 856
		}
		return 404, "", ""
	})
	img, ct, err := fetchCover(context.Background(), petitPrince13, petitPrince10)
	if img != nil || ct != "" || err != nil {
		t.Errorf("image %q, type %q, error %v — want nothing, and no fault", img, ct, err)
	}
	// The neutral image is embedded, so the slot keeps its place on the page.
	if len(defaultCover) == 0 {
		t.Error("the default cover is missing from the assets")
	}
}

// A cover that could not be fetched is not a cover that does not exist.
// Recording it as absent would keep the placeholder for good.
func TestAFailedCoverFetchIsNotRememberedAsAbsent(t *testing.T) {
	down := true
	stubCatalogues(t, func(r *http.Request) (int, string, string) {
		if down {
			if r.URL.Host == "covers.openlibrary.org" {
				return 503, "", "" // passing outage
			}
			return 0, "", "" // the BnF unreachable
		}
		if r.URL.Host == "covers.openlibrary.org" {
			return 200, "image/jpeg", "\xff\xd8\xff-back"
		}
		return 404, "", ""
	})
	a := &app{covers: newCoverCache(t.TempDir())}
	get := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "/cover/"+petitPrince13, nil)
		r.SetPathValue("isbn", petitPrince13)
		w := httptest.NewRecorder()
		a.cover(w, r)
		return w
	}

	w := get()
	if ct := w.Header().Get("Content-Type"); ct != "image/svg+xml" {
		t.Fatalf("during the outage: type %q, want the placeholder", ct)
	}
	if cc := w.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("during the outage: Cache-Control %q, want no-store so the browser asks again", cc)
	}
	if _, _, known := a.covers.read(petitPrince13); known {
		t.Fatal("the outage was recorded: the book keeps the placeholder for good")
	}

	down = false
	if w := get(); w.Body.String() != "\xff\xd8\xff-back" {
		t.Fatalf("after the outage: body %q, want the image", w.Body.String())
	}
	if img, _, _ := a.covers.read(petitPrince13); string(img) != "\xff\xd8\xff-back" {
		t.Errorf("after the outage: cached %q, want the image", img)
	}
}

// Every source answering "none" is an absence, and is recorded as one.
func TestACoverNobodyHasIsRememberedAsAbsent(t *testing.T) {
	stubCatalogues(t, func(r *http.Request) (int, string, string) {
		if r.URL.Path == "/api/SRU" {
			return 200, "text/xml", emptySRU
		}
		return 404, "", ""
	})
	a := &app{covers: newCoverCache(t.TempDir())}
	r := httptest.NewRequest(http.MethodGet, "/cover/"+petitPrince13, nil)
	r.SetPathValue("isbn", petitPrince13)
	a.cover(httptest.NewRecorder(), r)
	if img, _, known := a.covers.read(petitPrince13); !known || img != nil {
		t.Errorf("known %v, image %q: want an absence recorded", known, img)
	}
}

// An SVG can carry script, and /cover serves it from our own origin: one from a
// catalogue, or from wherever it redirected, is not kept. It is still an answer,
// not a fault — the source has nothing we can show.
func TestAnSVGFromUpstreamIsRefused(t *testing.T) {
	stubCatalogues(t, func(r *http.Request) (int, string, string) {
		return 200, "image/svg+xml", `<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`
	})
	img, ct, err := downloadImage(context.Background(), "https://covers.openlibrary.org/x.jpg")
	if img != nil || ct != "" {
		t.Errorf("SVG accepted: %q (%s)", img, ct)
	}
	if err != nil {
		t.Errorf("an SVG was read as a fault: %v", err)
	}
	c := newCoverCache(t.TempDir())
	c.write(petitPrince13, []byte("<svg/>"), "image/svg+xml")
	if img, _, known := c.read(petitPrince13); known {
		t.Errorf("an SVG was kept in the cache: %q", img)
	}
}

// Every /cover response carries the policy, the placeholder included.
func TestCoverResponsesAreSandboxed(t *testing.T) {
	stubCatalogues(t, func(r *http.Request) (int, string, string) {
		if r.URL.Host == "covers.openlibrary.org" {
			return 200, "image/jpeg", "\xff\xd8\xff"
		}
		return 404, "", ""
	})
	a := &app{covers: newCoverCache(t.TempDir())}
	for _, isbn := range []string{petitPrince13, "not-an-isbn"} {
		r := httptest.NewRequest(http.MethodGet, "/cover/"+isbn, nil)
		r.SetPathValue("isbn", isbn)
		w := httptest.NewRecorder()
		a.cover(w, r)
		if got := w.Header().Get("Content-Security-Policy"); !strings.Contains(got, "sandbox") || !strings.Contains(got, "default-src 'none'") {
			t.Errorf("%s: Content-Security-Policy %q", isbn, got)
		}
	}
}

// A key that is not an ISBN-13 never becomes a file name: no read, no write,
// no delete outside the cache directory.
func TestCoverCacheIgnoresAKeyThatIsNotAnISBN13(t *testing.T) {
	parent := t.TempDir()
	outside := filepath.Join(parent, "victim.jpg")
	if err := os.WriteFile(outside, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := newCoverCache(filepath.Join(parent, "cache"))

	c.forget("../victim")
	if _, err := os.Stat(outside); err != nil {
		t.Errorf("forget deleted a file outside the cache: %v", err)
	}
	if _, _, known := c.read("../victim"); known {
		t.Error("read served a file outside the cache")
	}
	c.write("../written", []byte("\xff\xd8\xff"), "image/jpeg")
	if _, err := os.Stat(filepath.Join(parent, "written.jpg")); err == nil {
		t.Error("write created a file outside the cache")
	}
}
