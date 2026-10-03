package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Cover thumbnails, fetched by the server so no reader's browser calls a third
// party. Cached in -cache-dir, one file per ISBN, indefinitely; nothing
// goes in the database, so the folder can be emptied by hand.

const (
	openLibraryCoverURL = "https://covers.openlibrary.org/b/isbn/%s-M.jpg?default=false"
	coverFetchTimeout   = 8 * time.Second
	// For the whole of fetchCover, which asks up to four times in a row.
	coverBudget = 20 * time.Second
	// An empty file marking "no catalogue has one", so an absence — the common
	// case — is not asked for on every view.
	absenceMarker = ".absent"
)

// The extension carries the type, which avoids a companion file to remember it.
// No SVG: it is a document that can carry script, and it would be served from
// our origin on the word of whatever host a redirect led to.
var imageExtension = map[string]string{
	"image/jpeg": ".jpg",
	"image/png":  ".png",
	"image/gif":  ".gif",
	"image/webp": ".webp",
}

// A thumbnail opened on its own is still a third party's bytes on our origin:
// nothing in it may run or load. The inline style is the placeholder's.
const coverCSP = "default-src 'none'; style-src 'unsafe-inline'; sandbox"

// One directory, one file per ISBN-13. An empty dir disables the cache; any
// other key is ignored, so no file name is ever built from something else.
type coverCache struct {
	dir string
}

func newCoverCache(dir string) *coverCache {
	if dir == "" {
		log.Print("cover cache disabled (-cache-dir \"\"): every display will re-query the catalogues")
		return &coverCache{}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Printf("cover cache: %s unusable (%v) — thumbnails will not be kept", dir, err)
		return &coverCache{}
	}
	return &coverCache{dir: dir}
}

// The second return tells "never looked for" from "looked for, no result" (nil image).
func (c *coverCache) read(isbn string) (image []byte, typeMT string, known bool) {
	if c.dir == "" || !ISBN13Valid(isbn) {
		return nil, "", false
	}
	if _, err := os.Stat(filepath.Join(c.dir, isbn+absenceMarker)); err == nil {
		return nil, "", true
	}
	for ct, ext := range imageExtension {
		b, err := os.ReadFile(filepath.Join(c.dir, isbn+ext))
		if err == nil && len(b) > 0 {
			return b, ct, true
		}
	}
	return nil, "", false
}

// Writes through a temporary file: a power cut at the wrong moment must not
// leave a truncated image that would then be served forever.
func (c *coverCache) write(isbn string, image []byte, typeMT string) {
	if c.dir == "" || !ISBN13Valid(isbn) {
		return
	}
	name := isbn + absenceMarker
	if image != nil {
		ext, ok := imageExtension[typeMT]
		if !ok {
			return // unexpected type: do not keep it
		}
		name = isbn + ext
	}
	dest := filepath.Join(c.dir, name)
	tmp := dest + ".tmp"
	if err := os.WriteFile(tmp, image, 0o644); err != nil {
		log.Printf("cover cache: %v", err)
		return
	}
	if err := os.Rename(tmp, dest); err != nil {
		log.Printf("cover cache: %v", err)
		_ = os.Remove(tmp)
	}
}

// forget makes the next request really go looking again.
func (c *coverCache) forget(isbn string) {
	if c.dir == "" || !ISBN13Valid(isbn) {
		return
	}
	for _, ext := range imageExtension {
		_ = os.Remove(filepath.Join(c.dir, isbn+ext))
	}
	_ = os.Remove(filepath.Join(c.dir, isbn+absenceMarker))
}

// forgetAbsences drops every "no catalogue has one" marker and keeps every
// image: an absence is one day's answer, an image does not change.
// Returns how many were dropped.
func (c *coverCache) forgetAbsences() int {
	if c.dir == "" {
		return 0
	}
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		log.Printf("cover cache: %v", err)
		return 0
	}
	n := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), absenceMarker) {
			continue
		}
		if err := os.Remove(filepath.Join(c.dir, e.Name())); err != nil {
			log.Printf("cover cache: %v", err)
			continue
		}
		n++
	}
	return n
}

// For the settings screen. Absences are counted apart: they take no space,
// and are the books the screen offers to ask about again.
func (c *coverCache) status() (count int, bytes int64, absent int) {
	if c.dir == "" {
		return 0, 0, 0
	}
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		return 0, 0, 0
	}
	for _, e := range entries {
		if e.IsDir() || strings.HasSuffix(e.Name(), ".tmp") {
			continue
		}
		if strings.HasSuffix(e.Name(), absenceMarker) {
			absent++
			continue
		}
		if info, err := e.Info(); err == nil {
			count++
			bytes += info.Size()
		}
	}
	return count, bytes, absent
}

// fetchCover tries Open Library, then the BnF, which holds French-language
// children's books. Open Library is asked under both ISBN forms: a pre-2007
// book has its cover under the ISBN-10 only. i10 is empty for a 979
// prefix. A nil image with a nil error means every source answered that it
// has none; with an error, at least one could not be asked, which is not an
// absence.
func fetchCover(ctx context.Context, i13, i10 string) (image []byte, typeMT string, err error) {
	var faults []error
	for _, isbn := range [...]string{i13, i10} {
		if isbn == "" {
			continue
		}
		img, ct, err := downloadImage(ctx, fmt.Sprintf(openLibraryCoverURL, isbn))
		if img != nil {
			return img, ct, nil
		}
		if err != nil {
			faults = append(faults, fmt.Errorf("openlibrary: %w", err))
		}
	}
	u, err := bnfCoverURL(ctx, i13, i10)
	if err != nil {
		faults = append(faults, fmt.Errorf("bnf: %w", err))
	}
	if u != "" {
		img, ct, err := downloadImage(ctx, u)
		if img != nil {
			return img, ct, nil
		}
		if err != nil {
			faults = append(faults, fmt.Errorf("bnf: %w", err))
		}
	}
	return nil, "", errors.Join(faults...)
}

// The cached record is a shortcut only when it came from the BnF: one found
// elsewhere carries no CoverURL. Never writes to the record cache, which must
// stay the result of the whole chain.
func bnfCoverURL(ctx context.Context, i13, i10 string) (string, error) {
	if n, known := catalogueMemo.record(i13); known && n != nil && n.Source == "bnf" {
		return n.CoverURL, nil
	}
	n, err := searchBnF(ctx, i13, i10)
	if errors.Is(err, errNoRecord) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if n != nil {
		return n.CoverURL, nil
	}
	return "", nil
}

// A nil image with a nil error is the source answering that it has none (a
// 404, or an image type we do not keep); an error is a source that did not
// answer, and says nothing about the book.
func downloadImage(ctx context.Context, u string) (image []byte, typeMT string, err error) {
	ctx, cancel := context.WithTimeout(ctx, coverFetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", userAgent())
	if catalogueMemo.isResting(req.URL.Host) {
		return nil, "", errSourceResting
	}
	resp, err := catalogueClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound, http.StatusGone:
		return nil, "", nil
	case http.StatusTooManyRequests:
		catalogueMemo.putToRest(req.URL.Host)
		return nil, "", errors.New("status 429: rate limited")
	default:
		return nil, "", fmt.Errorf("status %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, "", err
	}
	if len(b) == 0 {
		return nil, "", errors.New("empty image")
	}
	ct := resp.Header.Get("Content-Type")
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = strings.TrimSpace(ct[:i])
	}
	if _, ok := imageExtension[ct]; !ok {
		if strings.HasPrefix(ct, "image/") {
			return nil, "", nil // an image, in a format we do not keep
		}
		return nil, "", fmt.Errorf("%q instead of an image", ct) // a disguised error page
	}
	return b, ct, nil
}

// The neutral image, served when no catalogue has a thumbnail. Read from the
// embedded assets, which are initialised before init().
var defaultCover = func() []byte {
	b, err := staticFS.ReadFile("static/cover-missing.svg")
	if err != nil {
		// Can only happen on a build error: the asset is embedded.
		panic("default cover missing from assets: " + err.Error())
	}
	return b
}()

// With no thumbnail a neutral image is served rather than an error, so nothing looks broken.
func (a *app) cover(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Security-Policy", coverCSP)
	i13, i10, err := ISBNForms(r.PathValue("isbn"))
	if err != nil {
		missingCover(w, "private, max-age=3600")
		return
	}

	img, ct, known := a.covers.read(i13)
	if !known {
		ctx, done := enrichWithin(w, r, coverBudget)
		img, ct, err = fetchCover(ctx, i13, i10)
		done()
		if err != nil {
			// Nothing recorded, and nothing kept by the browser: the next view asks again.
			log.Printf("cover %s: %v", i13, err)
			missingCover(w, "no-store")
			return
		}
		a.covers.write(i13, img, ct)
	}
	if img == nil {
		missingCover(w, "private, max-age=3600")
		return
	}

	w.Header().Set("Content-Type", ct)
	// The URL carries a freshness token (BookPage.Fingerprint), so the browser
	// can cache long without missing an update.
	w.Header().Set("Cache-Control", "private, max-age=604800")
	w.Write(img)
}

// A short browser cache for an answer, since the freshness token does not
// change on a mere view; none when the catalogues could not be asked.
func missingCover(w http.ResponseWriter, cacheControl string) {
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", cacheControl)
	w.Write(defaultCover)
}
