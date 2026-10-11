package main

import (
	"bytes"
	"database/sql"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// backupOf writes db to a file the way the backup download hands it over.
func backupOf(t *testing.T, db *sql.DB) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "biblio-2026-10-01.db")
	if err := vacuumInto(db, path); err != nil {
		t.Fatal(err)
	}
	return path
}

func countBooks(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM book`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestInspectBackupRefusesWhatIsNotABibliBackup(t *testing.T) {
	dir := t.TempDir()
	text := filepath.Join(dir, "notes.db")
	os.WriteFile(text, []byte("not a database at all, just some text"), 0o644)

	other := filepath.Join(dir, "other.db")
	odb, err := sql.Open("sqlite", other)
	if err != nil {
		t.Fatal(err)
	}
	odb.Exec(`CREATE TABLE things (id INTEGER)`)
	odb.Close()

	good := backupOf(t, testDB(t))
	raw, _ := os.ReadFile(good)
	cut := filepath.Join(dir, "cut.db")
	os.WriteFile(cut, raw[:len(raw)/2], 0o644)

	newer := testDB(t)
	newer.Exec(`INSERT INTO schema_migrations (name) VALUES ('999_future.sql')`)
	future := backupOf(t, newer)

	for _, c := range []struct {
		path string
		want error
	}{
		{text, errRestoreNotSQLite},
		{other, errRestoreNotBibli},
		{cut, errRestoreCorrupt},
		{future, errRestoreNewer},
	} {
		if _, err := inspectBackup(c.path); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", filepath.Base(c.path), err, c.want)
		}
	}
	if f, err := inspectBackup(good); err != nil || f.Books == 0 {
		t.Errorf("a real backup: %+v, %v", f, err)
	}
}

// restoreApp is the handler over a library of its own, with a backup folder.
func restoreApp(t *testing.T) (*app, http.Handler, *http.Cookie) {
	t.Helper()
	a, h := testHandler(t)
	a.db = emptyDB(t)
	a.dbPath = filepath.Join(t.TempDir(), "biblio.db")
	a.backupDir = t.TempDir()
	a.throttle = newThrottle()
	if _, err := a.db.Exec(`INSERT INTO book (title) VALUES ('Mine')`); err != nil {
		t.Fatal(err)
	}
	return a, h, signedIn(t, a)
}

var tokenField = regexp.MustCompile(`name="token" value="([0-9a-f]{32})"`)

func uploadBackup(t *testing.T, h http.Handler, c *http.Cookie, path string) string {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", filepath.Base(path))
	raw, _ := os.ReadFile(path)
	fw.Write(raw)
	mw.Close()
	r := httptest.NewRequest("POST", "/settings/restore", &body)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	r.AddCookie(c)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	m := tokenField.FindStringSubmatch(w.Body.String())
	if w.Code != http.StatusOK || m == nil {
		t.Fatalf("upload = %d, no comparison page", w.Code)
	}
	return m[1]
}

func postForm(h http.Handler, c *http.Cookie, path string, v url.Values) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", path, strings.NewReader(v.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(c)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestARestoreReplacesTheLibraryKeepsTheOneBeforeAndCanGoBack(t *testing.T) {
	a, h, c := restoreApp(t)
	source := testDB(t)
	want := countBooks(t, source)
	token := uploadBackup(t, h, c, backupOf(t, source))

	w := postForm(h, c, "/settings/restore/confirm", url.Values{"token": {token}, "password": {"wrong"}})
	if w.Code != http.StatusOK || countBooks(t, a.db) != 1 {
		t.Fatalf("a wrong password = %d with %d books, want the page again and the library untouched", w.Code, countBooks(t, a.db))
	}

	w = postForm(h, c, "/settings/restore/confirm", url.Values{"token": {token}, "password": {"password"}})
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/settings?restored=1" {
		t.Fatalf("confirm = %d to %q", w.Code, w.Header().Get("Location"))
	}
	if got := countBooks(t, a.db); got != want {
		t.Errorf("after the restore: %d books, want the backup's %d", got, want)
	}
	if _, err := os.Stat(a.pendingPath(token)); !os.IsNotExist(err) {
		t.Errorf("the uploaded file is left behind: %v", err)
	}
	if w := get(h, "/settings", c); w.Code != http.StatusOK {
		t.Errorf("the session after the restore = %d, want it still open", w.Code)
	}
	if f, err := inspectBackup(filepath.Join(a.backupDir, beforeRestoreName)); err != nil || f.Books != 1 {
		t.Fatalf("the database kept before the restore: %+v, %v", f, err)
	}

	w = postForm(h, c, "/settings/restore/undo", nil)
	m := tokenField.FindStringSubmatch(w.Body.String())
	if m == nil {
		t.Fatalf("undo = %d, no comparison page", w.Code)
	}
	postForm(h, c, "/settings/restore/confirm", url.Values{"token": {m[1]}, "password": {"password"}})
	if got := countBooks(t, a.db); got != 1 {
		t.Errorf("after going back: %d books, want the original 1", got)
	}
}

func TestARefusedFileReplacesNothing(t *testing.T) {
	a, h, c := restoreApp(t)
	path := filepath.Join(t.TempDir(), "photo.db")
	os.WriteFile(path, []byte("\xff\xd8\xff not a database"), 0o644)

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", "photo.db")
	raw, _ := os.ReadFile(path)
	fw.Write(raw)
	mw.Close()
	r := httptest.NewRequest("POST", "/settings/restore", &body)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	r.AddCookie(c)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if tokenField.MatchString(w.Body.String()) {
		t.Error("a refused file still offers the confirmation")
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(a.dbPath), "restore-*.db")); len(left) > 0 {
		t.Errorf("a refused file is kept: %v", left)
	}
	if countBooks(t, a.db) != 1 {
		t.Error("the library changed")
	}
}

// A demonstration keeps no backups, so it has nowhere to keep the database a
// restore would replace.
func TestNoRestoreWithoutABackupFolder(t *testing.T) {
	a, h, c := restoreApp(t)
	a.backupDir = ""
	if w := postForm(h, c, "/settings/restore/undo", nil); w.Code != http.StatusNotFound {
		t.Errorf("restore without backups = %d, want 404", w.Code)
	}
}
