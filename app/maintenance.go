package main

import (
	"database/sql"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Daily tasks: GDPR purge and backup. They are checked every hour rather than
// slept on for 24 h: a sleeping laptop stops Go's timers, so a fixed day would
// drift or be skipped. The files' own dates say what is due.
// Backups rotate in fixed slots — 7 daily, 4 weekly, 4 monthly — so there are
// always 15 files and nothing to purge by age.

const maintenanceCheckEvery = time.Hour

// BackupState is shown in the settings, so a failing backup is seen.
type BackupState struct {
	Active bool // false = backups disabled (-backup-dir "")
	When   time.Time
	Path   string
	Error  string
}

var (
	backupMu    sync.Mutex
	backupState BackupState
)

func recordBackup(e BackupState) {
	backupMu.Lock()
	backupState = e
	backupMu.Unlock()
}

func LastBackup() BackupState {
	backupMu.Lock()
	defer backupMu.Unlock()
	return backupState
}

func startMaintenance(db *sql.DB, backupDir string) {
	state := BackupState{Active: backupDir != ""}
	if backupDir != "" {
		// Today's copy may predate this start: show it rather than "none yet".
		p := filepath.Join(backupDir, dailyBackupName(time.Now()))
		if info, err := os.Stat(p); err == nil {
			state.When, state.Path = info.ModTime(), p
		}
	}
	recordBackup(state)
	go func() {
		time.Sleep(1 * time.Minute) // let the server start up quietly
		var purged string
		for {
			purged = maintain(db, backupDir, time.Now(), purged)
			time.Sleep(maintenanceCheckEvery)
		}
	}()
}

// maintain runs whatever is due at now. purgedOn is the day the purge last ran
// ("" at start); the returned day is passed back on the next call.
func maintain(db *sql.DB, backupDir string, now time.Time, purgedOn string) string {
	today := now.Format("2006-01-02")
	if purgedOn != today {
		gdprPurge(db)
	}
	if backupDir != "" && backupDue(backupDir, now) {
		path, err := backup(db, backupDir, now)
		state := BackupState{Active: true, When: now, Path: path}
		if err != nil {
			state.Error = err.Error()
			log.Printf("backup: %v", err)
		}
		recordBackup(state)
	}
	return today
}

// backupDue is true when a daily, weekly or monthly copy is missing or stale.
func backupDue(dir string, now time.Time) bool {
	info, err := os.Stat(filepath.Join(dir, dailyBackupName(now)))
	if err != nil || !sameDay(info.ModTime(), now) {
		return true
	}
	return weeklyDue(dir, now) || monthlyDue(dir, now)
}

func weeklyDue(dir string, now time.Time) bool {
	newest, ok := newestSlot(dir, weeklyBackupName)
	return !ok || now.Sub(newest) >= 7*24*time.Hour-time.Hour // an hour's slack for the check interval
}

func monthlyDue(dir string, now time.Time) bool {
	newest, ok := newestSlot(dir, monthlyBackupName)
	return !ok || newest.Year() != now.Year() || newest.Month() != now.Month()
}

func sameDay(a, b time.Time) bool {
	return a.Format("2006-01-02") == b.Format("2006-01-02")
}

// retentionYears falls back to three when the setting is absent or unreadable,
// so the purge never stops.
func retentionYears(db *sql.DB) int {
	const fallback = 3
	var v string
	if err := db.QueryRow(
		`SELECT value FROM setting WHERE key = 'retention_years'`,
	).Scan(&v); err != nil {
		return fallback
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n < 1 || n > maxRetentionYears {
		return fallback
	}
	return n
}

// Beyond ten years a minor's reading history under their name is indefensible.
const maxRetentionYears = 10

// anonymisedFirstName is the placeholder name of an anonymised borrower,
// from the locale catalogue since it is shown on screen.
func anonymisedFirstName() string {
	return T(instanceLang(), "borrower.anonymised_name")
}

// syncAnonymousName gives the sentinel borrower the name in the instance
// language, at startup and whenever the language changes; the migration seeds
// it empty so no wording is frozen in SQL.
func syncAnonymousName(db *sql.DB) {
	anon := anonymousBorrowerID(db)
	if anon == 0 {
		return // no sentinel: gdprPurge says so, once, rather than twice
	}
	if _, err := db.Exec(
		`UPDATE borrower SET first_name = ? WHERE id = ? AND first_name <> ?`,
		anonymisedFirstName(), anon, anonymisedFirstName(),
	); err != nil {
		log.Printf("anonymous borrower (name): %v", err)
	}
}

// gdprPurge anonymises, never deletes, whatever exceeds the retention period,
// so each work keeps its loan count.
func gdprPurge(db *sql.DB) {
	anon := anonymousBorrowerID(db)
	if anon == 0 {
		log.Print("GDPR purge skipped: anonymous borrower not found (001_initial.sql)")
		return
	}
	years := retentionYears(db)

	// Returned loans change owner: the child-to-reading link goes, the count stays.
	res, err := db.Exec(
		`UPDATE loan SET borrower_id = ?
		  WHERE returned_on IS NOT NULL
		    AND returned_on < date('now', '-' || ? || ' years')
		    AND borrower_id <> ?`, anon, years, anon)
	if err != nil {
		log.Printf("GDPR purge (loans): %v", err)
		return
	}
	if n, _ := res.RowsAffected(); n > 0 {
		log.Printf("GDPR purge: %d loan(s) returned more than %d year(s) ago anonymised", n, years)
	}

	// A pupil long gone, named on no remaining loan, need not stay on file.
	res, err = db.Exec(
		`UPDATE borrower
		    SET first_name = ?, last_initial = '', class = NULL, card_code = NULL, family_token = NULL
		  WHERE active = 0
		    AND id <> ?
		    AND first_name <> ?
		    AND deactivated_on IS NOT NULL
		    AND deactivated_on < date('now', '-' || ? || ' years')
		    AND NOT EXISTS (SELECT 1 FROM loan l WHERE l.borrower_id = borrower.id)`,
		anonymisedFirstName(), anon, anonymisedFirstName(), years)
	if err != nil {
		log.Printf("GDPR purge (borrowers): %v", err)
		return
	}
	if n, _ := res.RowsAffected(); n > 0 {
		log.Printf("GDPR purge: %d borrower(s) gone more than %d year(s) ago anonymised", n, years)
	}
}

// backup copies the database with VACUUM INTO, never cp, while the service
// runs. VACUUM INTO refuses an existing file: write alongside, then rename.
func backup(db *sql.DB, dir string, now time.Time) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	tmp := filepath.Join(dir, "biblio.db.tmp")
	_ = os.Remove(tmp) // leftover from an interrupted attempt

	// The path is controlled by the app; quotes are escaped all the same.
	q := "VACUUM INTO '" + strings.ReplaceAll(tmp, "'", "''") + "'"
	if _, err := db.Exec(q); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("VACUUM INTO %s: %w", tmp, err)
	}

	dest := filepath.Join(dir, dailyBackupName(now))
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("replacing %s: %w", dest, err)
	}
	log.Printf("daily backup written: %s", dest)

	if weeklyDue(dir, now) {
		weekly := filepath.Join(dir, weeklyBackupName(oldestSlot(dir, weeklyBackupName)))
		if err := replaceFile(dest, weekly); err != nil {
			log.Printf("weekly backup: %v", err)
		} else {
			log.Printf("weekly backup written: %s", weekly)
		}
	}
	if monthlyDue(dir, now) {
		monthly := filepath.Join(dir, monthlyBackupName(oldestSlot(dir, monthlyBackupName)))
		if err := replaceFile(dest, monthly); err != nil {
			log.Printf("monthly backup: %v", err)
		} else {
			log.Printf("monthly backup written: %s", monthly)
		}
	}

	purgeLegacyBackups(dir)
	return dest, nil
}

func dailyBackupName(t time.Time) string {
	return "biblio-daily-" + strings.ToLower(t.Weekday().String()) + ".db"
}

func weeklyBackupName(slot int) string {
	return fmt.Sprintf("biblio-weekly-%d.db", slot)
}

func monthlyBackupName(slot int) string {
	return fmt.Sprintf("biblio-monthly-%d.db", slot)
}

// isBackupName whitelists the names the three builders above produce: the
// download route takes the name from a URL and serves nothing else.
func isBackupName(name string) bool {
	if name == "" || name != filepath.Base(name) {
		return false
	}
	slot, ok := strings.CutPrefix(name, "biblio-")
	if !ok {
		return false
	}
	slot, ok = strings.CutSuffix(slot, ".db")
	if !ok {
		return false
	}
	if day, ok := strings.CutPrefix(slot, "daily-"); ok {
		for d := time.Sunday; d <= time.Saturday; d++ {
			if strings.ToLower(d.String()) == day {
				return true
			}
		}
		return false
	}
	for _, prefix := range []string{"weekly-", "monthly-"} {
		if n, ok := strings.CutPrefix(slot, prefix); ok {
			i, err := strconv.Atoi(n)
			return err == nil && i >= 1 && i <= backupSlots
		}
	}
	return false
}

// backupSlots is how many weekly and monthly copies the rotation keeps.
const backupSlots = 4

// oldestSlot is the slot to overwrite next: the first missing one, else the
// one written longest ago.
func oldestSlot(dir string, name func(int) string) int {
	best, bestTime := 1, time.Time{}
	for slot := 1; slot <= backupSlots; slot++ {
		info, err := os.Stat(filepath.Join(dir, name(slot)))
		if err != nil {
			return slot
		}
		if bestTime.IsZero() || info.ModTime().Before(bestTime) {
			best, bestTime = slot, info.ModTime()
		}
	}
	return best
}

// newestSlot is when the most recent copy of a level was written.
func newestSlot(dir string, name func(int) string) (time.Time, bool) {
	var newest time.Time
	for slot := 1; slot <= backupSlots; slot++ {
		if info, err := os.Stat(filepath.Join(dir, name(slot))); err == nil && info.ModTime().After(newest) {
			newest = info.ModTime()
		}
	}
	return newest, !newest.IsZero()
}

// replaceFile writes alongside then renames, so a crash mid-copy never leaves a
// half-written backup in place.
func replaceFile(src, dest string) error {
	tmp := dest + ".tmp"
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dest)
}

// purgeLegacyBackups removes dated files (biblio-YYYY-MM-DD.db), which the
// rotation never writes.
func purgeLegacyBackups(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		rest, ok := strings.CutPrefix(e.Name(), "biblio-")
		if !ok {
			continue
		}
		rest, ok = strings.CutSuffix(rest, ".db")
		if !ok {
			continue
		}
		if _, err := time.Parse("2006-01-02", rest); err != nil {
			continue
		}
		path := filepath.Join(dir, e.Name())
		if err := os.Remove(path); err == nil {
			log.Printf("old backup removed: %s", path)
		}
	}
}
