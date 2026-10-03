package main

import (
	"database/sql"
	"log"
	"os"
	"sync"
)

// Settings shown on almost every page, cached in memory so a render runs no
// query; the settings screen refreshes the cache after a change.

var (
	settingsMu          sync.RWMutex
	cachedLibraryName   string
	cachedAnonymousID   int64
	cachedLang          = defaultLang
	cachedGoogleKey     string
	cachedTrackingLinks = true
	// -tracking-links: off in the Mac and Windows apps, which no one else can reach.
	trackingLinksAllowed = true
)

func loadSettingsCache(db *sql.DB) {
	var v string
	switch err := db.QueryRow(`SELECT value FROM setting WHERE key = 'library_name'`).Scan(&v); err {
	case nil:
		setLibraryName(v)
	case sql.ErrNoRows:
	default:
		log.Printf("settings cache (library name): %v", err)
	}

	switch err := db.QueryRow(`SELECT value FROM setting WHERE key = 'language'`).Scan(&v); err {
	case nil:
		setLang(v)
	case sql.ErrNoRows:
	default:
		log.Printf("settings cache (language): %v", err)
	}

	loadTheme(db)
	setAnonymousID(anonymousBorrowerID(db))

	// No row until a key is saved in /settings.
	v = ""
	if err := db.QueryRow(`SELECT value FROM setting WHERE key = 'google_books_key'`).Scan(&v); err != nil && err != sql.ErrNoRows {
		log.Printf("settings cache (Google Books key): %v", err)
	}
	setGoogleKey(v)

	// On unless the library turns it off; no row until /settings is saved.
	v = ""
	if err := db.QueryRow(`SELECT value FROM setting WHERE key = 'tracking_links'`).Scan(&v); err != nil && err != sql.ErrNoRows {
		log.Printf("settings cache (loans links): %v", err)
	}
	setTrackingLinks(v != "0")
}

// anonymousBorrowerID reads the sentinel borrower's id, 0 when absent — the
// caller must then write nothing.
func anonymousBorrowerID(db *sql.DB) int64 {
	var id int64
	switch err := db.QueryRow(
		`SELECT CAST(value AS INTEGER) FROM setting WHERE key = 'anonymous_borrower_id'`,
	).Scan(&id); err {
	case nil, sql.ErrNoRows:
	default:
		log.Printf("anonymous borrower: %v", err)
	}
	return id
}

func setLibraryName(s string) {
	settingsMu.Lock()
	cachedLibraryName = s
	settingsMu.Unlock()
}

// setLang records the instance language, ignoring an unknown value.
func setLang(l string) {
	if !knownLang(l) {
		log.Printf("unknown language in the database: %q — keeping %s", l, defaultLang)
		return
	}
	settingsMu.Lock()
	cachedLang = l
	settingsMu.Unlock()
}

func setTrackingLinks(on bool) {
	settingsMu.Lock()
	cachedTrackingLinks = on
	settingsMu.Unlock()
}

func allowTrackingLinks(on bool) {
	settingsMu.Lock()
	trackingLinksAllowed = on
	settingsMu.Unlock()
}

// trackingLinksOffered says whether this install may offer the loans links at all.
func trackingLinksOffered() bool {
	settingsMu.RLock()
	defer settingsMu.RUnlock()
	return trackingLinksAllowed
}

// trackingLinks says whether the loans links (tracking.go) are offered and open.
func trackingLinks() bool {
	settingsMu.RLock()
	defer settingsMu.RUnlock()
	return trackingLinksAllowed && cachedTrackingLinks
}

func setAnonymousID(id int64) {
	settingsMu.Lock()
	cachedAnonymousID = id
	settingsMu.Unlock()
}

// libraryName returns the library name, empty when unset.
func libraryName() string {
	settingsMu.RLock()
	defer settingsMu.RUnlock()
	return cachedLibraryName
}

// instanceLang is the language set in /settings; requestLang may override it
// per browser.
func instanceLang() string {
	settingsMu.RLock()
	defer settingsMu.RUnlock()
	return cachedLang
}

// anonymousID is the sentinel borrower, to be excluded from every list.
func anonymousID() int64 {
	settingsMu.RLock()
	defer settingsMu.RUnlock()
	return cachedAnonymousID
}

func setGoogleKey(k string) {
	settingsMu.Lock()
	cachedGoogleKey = k
	settingsMu.Unlock()
}

// googleKeyFromEnv is true when BIBLI_GOOGLE_BOOKS_KEY is set: it then wins
// over the key saved in /settings, which cannot change it.
func googleKeyFromEnv() bool {
	return os.Getenv("BIBLI_GOOGLE_BOOKS_KEY") != ""
}

// googleKey is the Google Books API key, empty when there is none.
func googleKey() string {
	if k := os.Getenv("BIBLI_GOOGLE_BOOKS_KEY"); k != "" {
		return k
	}
	settingsMu.RLock()
	defer settingsMu.RUnlock()
	return cachedGoogleKey
}
