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
	settingsMu        sync.RWMutex
	cachedSchool      string
	cachedAnonymousID int64
	cachedLang        = defaultLang
	cachedGoogleKey   string
	cachedFamilyLinks = true
	// -family-links: off in the Mac and Windows apps, which no family can reach.
	familyLinksAllowed = true
)

func loadSettingsCache(db *sql.DB) {
	var v string
	switch err := db.QueryRow(`SELECT value FROM setting WHERE key = 'school_name'`).Scan(&v); err {
	case nil:
		setSchool(v)
	case sql.ErrNoRows:
	default:
		log.Printf("settings cache (school): %v", err)
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

	// On unless a school turns it off; no row until /settings is saved.
	v = ""
	if err := db.QueryRow(`SELECT value FROM setting WHERE key = 'family_links'`).Scan(&v); err != nil && err != sql.ErrNoRows {
		log.Printf("settings cache (loans links): %v", err)
	}
	setFamilyLinks(v != "0")
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

func setSchool(s string) {
	settingsMu.Lock()
	cachedSchool = s
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

func setFamilyLinks(on bool) {
	settingsMu.Lock()
	cachedFamilyLinks = on
	settingsMu.Unlock()
}

func allowFamilyLinks(on bool) {
	settingsMu.Lock()
	familyLinksAllowed = on
	settingsMu.Unlock()
}

// familyLinksOffered says whether this install may offer the loans links at all.
func familyLinksOffered() bool {
	settingsMu.RLock()
	defer settingsMu.RUnlock()
	return familyLinksAllowed
}

// familyLinks says whether the loans links (family.go) are offered and open.
func familyLinks() bool {
	settingsMu.RLock()
	defer settingsMu.RUnlock()
	return familyLinksAllowed && cachedFamilyLinks
}

func setAnonymousID(id int64) {
	settingsMu.Lock()
	cachedAnonymousID = id
	settingsMu.Unlock()
}

// school returns the school name, empty when unset.
func school() string {
	settingsMu.RLock()
	defer settingsMu.RUnlock()
	return cachedSchool
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
