package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"modernc.org/sqlite"
)

// Restoring a backup replaces the whole library, months of work perhaps. So it
// goes: upload, checks, the two databases side by side, the password, and a
// copy of the current database kept first, which one click puts back.

const (
	restoreMaxBytes   = 1 << 30
	restorePendingTTL = time.Hour
	// One slot, overwritten by each restore: the database the last one replaced.
	beforeRestoreName = "biblio-before-restore.db"
)

var sqliteMagic = []byte("SQLite format 3\x00")

// The checks a file must pass, each with what the librarian is told.
var (
	errRestoreNotSQLite = errors.New("restore.err_not_sqlite")
	errRestoreCorrupt   = errors.New("restore.err_corrupt")
	errRestoreNotBibli  = errors.New("restore.err_not_bibli")
	errRestoreNewer     = errors.New("restore.err_newer")
)

// libraryFigures is what the comparison shows of either database.
type libraryFigures struct {
	Name                              string
	Books, Copies, Readers, OpenLoans int
	LastActivity                      string // ISO date of the latest loan, return, copy or reader
}

func figures(db *sql.DB) (libraryFigures, error) {
	var f libraryFigures
	err := db.QueryRow(`
		SELECT (SELECT COUNT(*) FROM book),
		       (SELECT COUNT(*) FROM copy),
		       (SELECT COUNT(*) FROM borrower WHERE active = 1 AND id <> COALESCE(
		          (SELECT CAST(value AS INTEGER) FROM setting WHERE key = 'anonymous_borrower_id'), 0)),
		       (SELECT COUNT(*) FROM loan WHERE returned_on IS NULL),
		       COALESCE((SELECT MAX(d) FROM (
		          SELECT MAX(loaned_on) AS d FROM loan
		          UNION ALL SELECT MAX(returned_on) FROM loan
		          UNION ALL SELECT MAX(substr(created_at, 1, 10)) FROM copy
		          UNION ALL SELECT MAX(substr(created_at, 1, 10)) FROM borrower)), ''),
		       COALESCE((SELECT value FROM setting WHERE key = 'library_name'), '')`,
	).Scan(&f.Books, &f.Copies, &f.Readers, &f.OpenLoans, &f.LastActivity, &f.Name)
	return f, err
}

// inspectBackup checks that path is an intact Bibli database this version can
// take, and reads its figures. It opens the file read-only, apart from a.db.
func inspectBackup(path string) (libraryFigures, error) {
	head := make([]byte, len(sqliteMagic))
	f, err := os.Open(path)
	if err != nil {
		return libraryFigures{}, err
	}
	_, err = io.ReadFull(f, head)
	f.Close()
	if err != nil || !bytes.Equal(head, sqliteMagic) {
		return libraryFigures{}, errRestoreNotSQLite
	}

	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&immutable=1")
	if err != nil {
		return libraryFigures{}, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	var check string
	if err := db.QueryRow(`PRAGMA quick_check`).Scan(&check); err != nil || check != "ok" {
		return libraryFigures{}, errRestoreCorrupt
	}
	var tables int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table'
		AND name IN ('book', 'copy', 'borrower', 'loan', 'setting', 'schema_migrations')`).Scan(&tables); err != nil || tables != 6 {
		return libraryFigures{}, errRestoreNotBibli
	}

	// A migration this version does not know means a newer Bibli wrote it.
	known := map[string]bool{}
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return libraryFigures{}, err
	}
	for _, e := range entries {
		known[e.Name()] = true
	}
	rows, err := db.Query(`SELECT name FROM schema_migrations`)
	if err != nil {
		return libraryFigures{}, errRestoreNotBibli
	}
	var unknown bool
	for rows.Next() {
		var name string
		if rows.Scan(&name) == nil && !known[name] {
			unknown = true
		}
	}
	rows.Close()
	if unknown {
		return libraryFigures{}, errRestoreNewer
	}

	fig, err := figures(db)
	if err != nil {
		return libraryFigures{}, errRestoreNotBibli
	}
	return fig, nil
}

// vacuumInto writes a consistent copy of the live database to dest, alongside
// then renamed: VACUUM INTO refuses an existing file.
func vacuumInto(db *sql.DB, dest string) error {
	tmp := dest + ".tmp"
	_ = os.Remove(tmp) // leftover from an interrupted attempt
	// The path is controlled by the app; quotes are escaped all the same.
	if _, err := db.Exec("VACUUM INTO '" + strings.ReplaceAll(tmp, "'", "''") + "'"); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("VACUUM INTO %s: %w", tmp, err)
	}
	if err := retryFile(func() error { return os.Rename(tmp, dest) }); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replacing %s: %w", dest, err)
	}
	return nil
}

// copyDatabase overwrites the live database with src through SQLite's backup
// API, on a.db's own connection: every handler keeps its *sql.DB, and a
// failure leaves the database as it was.
func copyDatabase(db *sql.DB, src string) error {
	conn, err := db.Conn(context.Background())
	if err != nil {
		return err
	}
	defer conn.Close()
	return conn.Raw(func(dc any) error {
		r, ok := dc.(interface {
			NewRestore(string) (*sqlite.Backup, error)
		})
		if !ok {
			return errors.New("the SQLite driver cannot restore")
		}
		b, err := r.NewRestore("file:" + src + "?mode=ro")
		if err != nil {
			return err
		}
		for more := true; more; {
			if more, err = b.Step(-1); err != nil {
				b.Finish()
				return err
			}
		}
		return b.Finish()
	})
}

// restoreFrom replaces the library with src, after keeping the current one in
// the backup folder. The session secret belongs to the installation, not to
// the data: it is carried over, so nobody is signed out.
func (a *app) restoreFrom(src string) error {
	safety := filepath.Join(a.backupDir, beforeRestoreName)
	if err := vacuumInto(a.db, safety); err != nil {
		return fmt.Errorf("keeping the current database: %w", err)
	}
	secret, err := loadOrCreateSecret(a.db)
	if err != nil {
		return err
	}
	if err := copyDatabase(a.db, src); err != nil {
		return err
	}
	if err := a.afterRestore(secret); err != nil {
		// A backup that cannot be brought up to date is not left in place.
		if back := copyDatabase(a.db, safety); back != nil {
			return fmt.Errorf("%v; putting the previous database back: %v", err, back)
		}
		_ = a.afterRestore(secret)
		return err
	}
	return nil
}

// afterRestore brings a restored database up to this version and reloads what
// the process keeps of it.
func (a *app) afterRestore(secret string) error {
	var mode string
	if err := a.db.QueryRow(`PRAGMA journal_mode = WAL`).Scan(&mode); err != nil {
		return err
	}
	if err := migrate(a.db); err != nil {
		return fmt.Errorf("migrations: %w", err)
	}
	if _, err := a.db.Exec(`INSERT INTO setting (key, value, label)
		VALUES ('session_secret', ?, 'Session-signing secret (generated automatically)')
		ON CONFLICT (key) DO UPDATE SET value = excluded.value`, secret); err != nil {
		return err
	}
	loadSettingsCache(a.db)
	syncAnonymousName(a.db)
	return nil
}

// Uploaded files wait beside the database, under a random name the
// comparison page carries to the confirmation.
func (a *app) pendingPath(token string) string {
	return filepath.Join(filepath.Dir(a.dbPath), "restore-"+token+".db")
}

func validToken(token string) bool {
	if len(token) != 32 {
		return false
	}
	_, err := hex.DecodeString(token)
	return err == nil
}

func newRestoreToken() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// removePending deletes an uploaded file, which holds every reader: one left
// behind is said in the log, and the next sweep takes it.
func removePending(path string) {
	if err := retryFile(func() error { return os.Remove(path) }); err != nil && !errors.Is(err, fs.ErrNotExist) {
		log.Printf("restore: could not delete %s: %v", path, err)
	}
}

// sweepPending removes uploads nobody confirmed.
func (a *app) sweepPending() {
	matches, _ := filepath.Glob(filepath.Join(filepath.Dir(a.dbPath), "restore-*.db"))
	for _, m := range matches {
		if info, err := os.Stat(m); err == nil && time.Since(info.ModTime()) > restorePendingTTL {
			_ = os.Remove(m)
		}
	}
}

// restoreAllowed: restoring needs the backup folder, where the current
// database is kept first. A demonstration has none.
func (a *app) restoreAllowed(w http.ResponseWriter, r *http.Request) bool {
	if a.backupDir == "" {
		a.notFoundScreen(w, r)
		return false
	}
	return true
}

// restoreUpload receives the file, streamed to disk, and shows what it holds.
func (a *app) restoreUpload(w http.ResponseWriter, r *http.Request) {
	if !a.restoreAllowed(w, r) {
		return
	}
	// A large library over Wi-Fi takes longer than the server's timeouts.
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Now().Add(10 * time.Minute))
	extendWriteDeadline(w, 10*time.Minute)
	a.sweepPending()

	r.Body = http.MaxBytesReader(w, r.Body, restoreMaxBytes)
	mr, err := r.MultipartReader()
	if err != nil {
		a.restoreScreen(w, r, restorePage{Error: tr(r, "restore.err_no_file")})
		return
	}
	for {
		part, err := mr.NextPart()
		if err != nil {
			a.restoreScreen(w, r, restorePage{Error: tr(r, "restore.err_no_file")})
			return
		}
		if part.FormName() != "file" || part.FileName() == "" {
			continue
		}
		token := newRestoreToken()
		path := a.pendingPath(token)
		out, err := os.Create(path)
		if err != nil {
			log.Printf("restore (upload): %v", err)
			internalError(w, r)
			return
		}
		_, err = io.Copy(out, part)
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			removePending(path)
			var tooBig *http.MaxBytesError
			if errors.As(err, &tooBig) {
				a.restoreScreen(w, r, restorePage{Error: tr(r, "restore.err_too_big")})
				return
			}
			log.Printf("restore (upload): %v", err)
			internalError(w, r)
			return
		}
		a.restorePreview(w, r, token, filepath.Base(part.FileName()), "")
		return
	}
}

// restoreUndo offers the database the last restore replaced, through the same
// comparison and confirmation as an upload.
func (a *app) restoreUndo(w http.ResponseWriter, r *http.Request) {
	if !a.restoreAllowed(w, r) {
		return
	}
	a.sweepPending()
	token := newRestoreToken()
	if err := replaceFile(filepath.Join(a.backupDir, beforeRestoreName), a.pendingPath(token)); err != nil {
		log.Printf("restore (undo): %v", err)
		a.restoreScreen(w, r, restorePage{Error: tr(r, "restore.err_expired")})
		return
	}
	a.restorePreview(w, r, token, tr(r, "restore.before_file"), "")
}

// restorePreview checks the pending file and sets it beside the current
// database. A file that fails a check is deleted on the spot.
func (a *app) restorePreview(w http.ResponseWriter, r *http.Request, token, fileName, passwordError string) {
	path := a.pendingPath(token)
	backup, err := inspectBackup(path)
	if err != nil {
		removePending(path)
		msg := tr(r, "restore.err_corrupt")
		for _, known := range []error{errRestoreNotSQLite, errRestoreCorrupt, errRestoreNotBibli, errRestoreNewer} {
			if errors.Is(err, known) {
				msg = tr(r, known.Error())
			}
		}
		log.Printf("restore: %s refused: %v", fileName, err)
		a.restoreScreen(w, r, restorePage{Error: msg})
		return
	}
	current, err := figures(a.db)
	if err != nil {
		log.Printf("restore (current figures): %v", err)
		internalError(w, r)
		return
	}
	a.restoreScreen(w, r, restorePage{
		Token: token, FileName: fileName, Current: current, Backup: backup,
		Warnings: restoreWarnings(r, current, backup), PasswordError: passwordError,
	})
}

// restoreWarnings names what the librarian would lose, beyond the general
// warning every restore carries.
func restoreWarnings(r *http.Request, current, backup libraryFigures) []string {
	lang := requestLang(r)
	var out []string
	if backup.Books == 0 && current.Books > 0 {
		out = append(out, tr(r, "restore.warn_empty"))
	} else if n := current.Books - backup.Books; n > 0 {
		out = append(out, trn(r, "restore.warn_fewer_books", n))
	}
	if backup.LastActivity != "" && backup.LastActivity < current.LastActivity {
		out = append(out, tr(r, "restore.warn_older",
			shortDate(lang, backup.LastActivity), shortDate(lang, current.LastActivity)))
	}
	if backup.Name != "" && current.Name != "" && backup.Name != current.Name {
		out = append(out, tr(r, "restore.warn_other_library", backup.Name))
	}
	return out
}

// restoreConfirm replaces the library once the password is typed again.
func (a *app) restoreConfirm(w http.ResponseWriter, r *http.Request) {
	if !a.restoreAllowed(w, r) {
		return
	}
	if err := r.ParseForm(); err != nil {
		badRequest(w, r)
		return
	}
	token, fileName := r.FormValue("token"), r.FormValue("file_name")
	if !validToken(token) {
		badRequest(w, r)
		return
	}
	path := a.pendingPath(token)
	if info, err := os.Stat(path); err != nil || time.Since(info.ModTime()) > restorePendingTTL {
		_ = os.Remove(path)
		a.restoreScreen(w, r, restorePage{Error: tr(r, "restore.err_expired")})
		return
	}

	// The same guard as the login: this form can try the password too.
	ip, now := clientIP(r, a.trustProxy), time.Now()
	if a.throttle != nil {
		if wait := a.throttle.held(ip, now); wait > 0 {
			minutes := int((wait + time.Minute - 1) / time.Minute)
			a.restorePreview(w, r, token, fileName, trn(r, "login.too_many_attempts", minutes))
			return
		}
	}
	if subtle.ConstantTimeCompare([]byte(r.FormValue("password")), []byte(a.adminPass)) != 1 {
		if a.throttle != nil {
			a.throttle.failure(ip, now)
			a.throttle.globalFailure(now)
		}
		time.Sleep(500 * time.Millisecond)
		a.restorePreview(w, r, token, fileName, tr(r, "restore.err_password"))
		return
	}

	// Checked again: the page is not proof, the file may have changed since.
	if _, err := inspectBackup(path); err != nil {
		a.restorePreview(w, r, token, fileName, "")
		return
	}
	extendWriteDeadline(w, 10*time.Minute)
	if err := a.restoreFrom(path); err != nil {
		log.Printf("restore of %s failed: %v", fileName, err)
		a.restoreScreen(w, r, restorePage{Error: tr(r, "restore.err_failed", err.Error())})
		return
	}
	removePending(path)
	log.Printf("database restored from %s; the previous one is kept as %s", fileName, beforeRestoreName)
	http.Redirect(w, r, "/settings?restored=1", http.StatusSeeOther)
}

// restoreCancel drops the pending file.
func (a *app) restoreCancel(w http.ResponseWriter, r *http.Request) {
	if token := r.FormValue("token"); validToken(token) {
		removePending(a.pendingPath(token))
	}
	http.Redirect(w, r, "/settings?tab=installation", http.StatusSeeOther)
}

type restorePage struct {
	Error           string
	Token, FileName string
	Current, Backup libraryFigures
	Warnings        []string
	PasswordError   string
}

func (a *app) restoreScreen(w http.ResponseWriter, r *http.Request, p restorePage) {
	a.render(w, r, "restore", map[string]any{"Title": tr(r, "restore.title"), "P": p})
}
