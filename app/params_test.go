package main

import "testing"

// restoreSettings puts the settings cache back after a test changes it.
func restoreSettings(t *testing.T) {
	t.Helper()
	settingsMu.RLock()
	name, lang, anon, key := cachedSchool, cachedLang, cachedAnonymousID, cachedGoogleKey
	settingsMu.RUnlock()
	t.Cleanup(func() {
		settingsMu.Lock()
		cachedSchool, cachedLang, cachedAnonymousID, cachedGoogleKey = name, lang, anon, key
		settingsMu.Unlock()
	})
}

func TestLoadSettingsCache(t *testing.T) {
	loadForTest(t)
	restoreSettings(t)
	db := testDB(t)

	if _, err := db.Exec(
		`UPDATE setting SET value = 'École communale' WHERE key = 'school_name';
		 UPDATE setting SET value = 'en'             WHERE key = 'language';`); err != nil {
		t.Fatalf("settings: %v", err)
	}
	loadSettingsCache(db)

	if got := school(); got != "École communale" {
		t.Errorf("school() = %q, want %q", got, "École communale")
	}
	if got := instanceLang(); got != "en" {
		t.Errorf("instanceLang() = %q, want en", got)
	}
	// The sentinel borrower, excluded from every list.
	if anonymousID() == 0 {
		t.Error("anonymousID() = 0: the sentinel borrower was not cached")
	}
	if anonymousID() != anonymousBorrowerID(db) {
		t.Errorf("cache (%d) and database (%d) disagree", anonymousID(), anonymousBorrowerID(db))
	}
}

// An unknown language is logged and ignored.
func TestAnUnknownLanguageInTheDatabaseIsIgnored(t *testing.T) {
	loadForTest(t)
	restoreSettings(t)
	db := testDB(t)

	setLang("en")
	if _, err := db.Exec(`UPDATE setting SET value = 'kl' WHERE key = 'language'`); err != nil {
		t.Fatalf("settings: %v", err)
	}
	loadSettingsCache(db)

	if got := instanceLang(); got != "en" {
		t.Errorf("instanceLang() = %q, want en — an unreadable value overwrote the language", got)
	}
}

// Without the sentinel the id reads as zero rather than a guess.
func TestAnonymousBorrowerIDIsZeroWhenAbsent(t *testing.T) {
	db := testDB(t)
	if _, err := db.Exec(`DELETE FROM setting WHERE key = 'anonymous_borrower_id'`); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if id := anonymousBorrowerID(db); id != 0 {
		t.Errorf("anonymousBorrowerID = %d, want 0", id)
	}
}

// The key saved in /settings is cached; BIBLI_GOOGLE_BOOKS_KEY wins over it.
func TestGoogleKeyFromSettingsAndEnvironment(t *testing.T) {
	loadForTest(t)
	restoreSettings(t)
	db := testDB(t)
	t.Setenv("BIBLI_GOOGLE_BOOKS_KEY", "")

	loadSettingsCache(db)
	if got := googleKey(); got != "" {
		t.Fatalf("googleKey() = %q on a fresh database, want none", got)
	}

	if _, err := db.Exec(
		`INSERT INTO setting (key, value, label) VALUES ('google_books_key', 'saved', '')`); err != nil {
		t.Fatalf("settings: %v", err)
	}
	loadSettingsCache(db)
	if got := googleKey(); got != "saved" {
		t.Errorf("googleKey() = %q, want the saved key", got)
	}

	t.Setenv("BIBLI_GOOGLE_BOOKS_KEY", "env")
	if got := googleKey(); got != "env" || !googleKeyFromEnv() {
		t.Errorf("googleKey() = %q, googleKeyFromEnv() = %v: the environment must win", got, googleKeyFromEnv())
	}
}
