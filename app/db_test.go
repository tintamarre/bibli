package main

import (
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"
)

// testDB opens a fresh database with the migrations applied and the small
// fixture loaded; tests count its rows by hand, so it stays small (demo.sql is
// checked by TestDemoDatasetFitsTheSchema).
func testDB(t *testing.T) *sql.DB {
	t.Helper()
	return loadInto(t, "testdata/fixture.sql")
}

// emptyDB is the schema alone, for a case that loads its own SQL.
func emptyDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := openDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("openDB: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func loadInto(t *testing.T, path string) *sql.DB {
	t.Helper()
	db := emptyDB(t)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	if _, err := db.Exec(string(data)); err != nil {
		t.Fatalf("loading %s: %v", path, err)
	}
	return db
}

func count(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

func TestMigrationsAndViews(t *testing.T) {
	db := testDB(t)

	if n := count(t, db, `SELECT COUNT(*) FROM v_active_loan`); n != 2 {
		t.Errorf("v_active_loan: want 2, got %d", n)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM v_overdue`); n != 1 {
		t.Errorf("v_overdue: want 1, got %d", n)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM v_available`); n != 3 {
		t.Errorf("v_available: want 3, got %d", n)
	}
	// The sentinel borrower exists and does not count as a person.
	if id := anonymousBorrowerID(db); id == 0 {
		t.Error("anonymous borrower missing after the schema migration")
	}
}

// The purge anonymises, never deletes: a work's loan count survives.
func TestGDPRPurgeAnonymisesRatherThanDeletes(t *testing.T) {
	db := testDB(t)
	anon := anonymousBorrowerID(db)

	// One loan returned beyond the retention period, one returned recently.
	if _, err := db.Exec(
		`INSERT INTO loan (copy_id, borrower_id, loaned_on, due_on, returned_on) VALUES
		 (2, 101, date('now','-4 years'), date('now','-4 years','+21 days'), date('now','-4 years','+10 days')),
		 (4, 102, date('now','-30 days'), date('now','-9 days'),             date('now','-20 days'))`); err != nil {
		t.Fatalf("insert: %v", err)
	}
	before := count(t, db, `SELECT COUNT(*) FROM loan`)

	gdprPurge(db)

	if after := count(t, db, `SELECT COUNT(*) FROM loan`); after != before {
		t.Errorf("the purge deleted loans: %d -> %d (rotation statistics must survive)", before, after)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM loan WHERE borrower_id = ?`, anon); n != 1 {
		t.Errorf("anonymised loans: want 1, got %d", n)
	}
	// The recent loan keeps its owner.
	if n := count(t, db, `SELECT COUNT(*) FROM loan WHERE borrower_id = 102 AND returned_on IS NOT NULL`); n != 1 {
		t.Errorf("a recently closed loan must not be anonymised (%d)", n)
	}
	// Replaying the purge has no further effect.
	gdprPurge(db)
	if n := count(t, db, `SELECT COUNT(*) FROM loan WHERE borrower_id = ?`, anon); n != 1 {
		t.Errorf("purge not idempotent: %d anonymised loans", n)
	}
}

func TestGDPRPurgeAnonymisesLeavers(t *testing.T) {
	db := testDB(t)

	// Zoé (103) has no loan: she left beyond the delay, so she is anonymised.
	// Noah (104) left yesterday: he keeps his name.
	if _, err := db.Exec(
		`UPDATE borrower SET active = 0, deactivated_on = date('now','-4 years') WHERE id = 103;
		 UPDATE borrower SET active = 0, deactivated_on = date('now','-1 days')  WHERE id = 104;`); err != nil {
		t.Fatalf("deactivation: %v", err)
	}

	gdprPurge(db)

	var firstName, card sql.NullString
	if err := db.QueryRow(`SELECT first_name, card_code FROM borrower WHERE id = 103`).Scan(&firstName, &card); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if firstName.String != anonymisedFirstName() {
		t.Errorf("reader gone beyond the delay: first name %q, want %q", firstName.String, anonymisedFirstName())
	}
	if card.Valid {
		t.Error("the card of an anonymised reader must be cleared")
	}

	if err := db.QueryRow(`SELECT first_name FROM borrower WHERE id = 104`).Scan(&firstName); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if firstName.String != "Noah" {
		t.Errorf("reader gone yesterday: first name %q, it must be kept for the retention period", firstName.String)
	}

	// Léa (101) has an open loan: never touched.
	if err := db.QueryRow(`SELECT first_name FROM borrower WHERE id = 101`).Scan(&firstName); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if firstName.String != "Léa" {
		t.Errorf("active borrower wrongly anonymised: %q", firstName.String)
	}
}

// VACUUM INTO refuses an existing file: the second backup of a day must still
// succeed.
func TestBackupTwiceInADay(t *testing.T) {
	db := testDB(t)
	dir := t.TempDir()

	first, err := backup(db, dir, time.Now())
	if err != nil {
		t.Fatalf("first backup: %v", err)
	}
	second, err := backup(db, dir, time.Now())
	if err != nil {
		t.Fatalf("second backup on the same day: %v", err)
	}
	if first != second {
		t.Errorf("the day's backup must be replaced: %q then %q", first, second)
	}

	// The file produced is a readable database, and no .tmp is left behind.
	clone, err := sql.Open("sqlite", second)
	if err != nil {
		t.Fatalf("opening the copy: %v", err)
	}
	defer clone.Close()
	if n := count(t, clone, `SELECT COUNT(*) FROM borrower`); n < 5 {
		t.Errorf("incomplete copy: %d borrowers", n)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Errorf("temporary file left behind: %s", e.Name())
		}
	}
}

// The daily name rotates over the 7 weekdays: 7 distinct names, and the same
// name comes back after a week.
func TestDailyBackupNameRotatesOverSevenDays(t *testing.T) {
	start := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC) // a Sunday
	names := map[string]bool{}
	for i := 0; i < 7; i++ {
		names[dailyBackupName(start.AddDate(0, 0, i))] = true
	}
	if len(names) != 7 {
		t.Errorf("want 7 distinct names, got %d: %v", len(names), names)
	}
	if got := dailyBackupName(start.AddDate(0, 0, 7)); got != dailyBackupName(start) {
		t.Errorf("the name must repeat after 7 days: %q then %q", dailyBackupName(start), got)
	}
}

// Every name the rotation writes is one the download route serves, and nothing
// else: a year of days is generated rather than the patterns restated.
func TestEveryBackupWrittenCanBeDownloaded(t *testing.T) {
	day := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	for i := 0; i < 7; i++ {
		if name := dailyBackupName(day.AddDate(0, 0, i)); !isBackupName(name) {
			t.Fatalf("the rotation writes %q, which the download route refuses", name)
		}
	}
	for slot := 1; slot <= backupSlots; slot++ {
		for _, name := range []string{weeklyBackupName(slot), monthlyBackupName(slot)} {
			if !isBackupName(name) {
				t.Fatalf("the rotation writes %q, which the download route refuses", name)
			}
		}
	}
}

// Nothing outside the rotation is served, and nothing climbs out of the directory.
func TestIsBackupNameRefusesEverythingElse(t *testing.T) {
	for _, name := range []string{
		"",
		"biblio.db",                     // the live database, never served
		"../biblio.db",                  // the live database, from outside the folder
		"../../etc/passwd",              // and anything else on the machine
		"biblio-daily-wednesday.db.tmp", // a backup half written
		"biblio-daily-someday.db",       // not a weekday
		"biblio-weekly-0.db",            // the slots are 1..4
		"biblio-weekly-5.db",
		"biblio-monthly-99.db",
		"biblio-weekly-.db",
		"biblio-2026-09-17.db", // the old dated scheme, purged rather than kept
		"sub/biblio-daily-monday.db",
		"biblio-daily-monday.db/",
	} {
		if isBackupName(name) {
			t.Errorf("%q is served by the download route and should not be", name)
		}
	}
}

// A backup always lands under today's fixed daily name, and matches what
// backup() reports as its path.
func TestBackupWritesTodaysName(t *testing.T) {
	db := testDB(t)
	dir := t.TempDir()

	path, err := backup(db, dir, time.Now())
	if err != nil {
		t.Fatalf("backup: %v", err)
	}
	want := filepath.Join(dir, dailyBackupName(time.Now()))
	if path != want {
		t.Errorf("want path %q, got %q", want, path)
	}
	if _, err := os.Stat(want); err != nil {
		t.Errorf("daily backup missing: %v", err)
	}
}

// Dated legacy backups (biblio-YYYY-MM-DD.db) are removed; fixed names stay.
func TestPurgeLegacyBackupsRemovesTheOldNaming(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "biblio-2026-01-01.db")
	current := filepath.Join(dir, "biblio-daily-monday.db")
	if err := os.WriteFile(old, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(current, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	purgeLegacyBackups(dir)

	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Error("the old dated file should have been removed")
	}
	if _, err := os.Stat(current); err != nil {
		t.Error("the file in the current naming must not be touched")
	}
}

// Without the sentinel the purge stops rather than anonymise against borrower 0.
func TestGDPRPurgeStopsWithoutTheSentinel(t *testing.T) {
	db := testDB(t)

	// A loan well past the retention period: the purge would take this one.
	if _, err := db.Exec(
		`INSERT INTO loan (copy_id, borrower_id, loaned_on, due_on, returned_on) VALUES
		 (2, 101, date('now','-4 years'), date('now','-4 years','+21 days'), date('now','-4 years','+10 days'))`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM setting WHERE key = 'anonymous_borrower_id'`); err != nil {
		t.Fatal(err)
	}
	// Counted rather than assumed: the fixture already lends Léa a book.
	before := count(t, db, `SELECT COUNT(*) FROM loan WHERE borrower_id = 101`)

	gdprPurge(db)

	if n := count(t, db, `SELECT COUNT(*) FROM loan WHERE borrower_id = 101`); n != before {
		t.Errorf("loans on 101: %d -> %d, with no sentinel to reassign them to", before, n)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM loan WHERE borrower_id = 0`); n != 0 {
		t.Errorf("%d loan(s) anonymised against borrower 0", n)
	}
}

func TestGDPRPurgeFollowsTheSetting(t *testing.T) {
	db := testDB(t)
	anon := anonymousBorrowerID(db)

	if got := retentionYears(db); got != 3 {
		t.Fatalf("want a default of 3 years, got %d", got)
	}

	// A loan returned two years ago: kept under the default setting.
	if _, err := db.Exec(
		`INSERT INTO loan (copy_id, borrower_id, loaned_on, due_on, returned_on)
		 VALUES (2, 101, date('now','-2 years'), date('now','-2 years','+21 days'), date('now','-2 years','+10 days'))`); err != nil {
		t.Fatalf("insert: %v", err)
	}
	gdprPurge(db)
	if n := count(t, db, `SELECT COUNT(*) FROM loan WHERE borrower_id = ?`, anon); n != 0 {
		t.Errorf("at 3 years, a 2-year-old loan must not be anonymised (%d are)", n)
	}

	// Brought down to one year, the same loan falls under the purge.
	if _, err := db.Exec(
		`UPDATE setting SET value = '1' WHERE key = 'retention_years'`); err != nil {
		t.Fatalf("setting: %v", err)
	}
	if got := retentionYears(db); got != 1 {
		t.Fatalf("setting read back: %d, want 1", got)
	}
	gdprPurge(db)
	if n := count(t, db, `SELECT COUNT(*) FROM loan WHERE borrower_id = ?`, anon); n != 1 {
		t.Errorf("at 1 year, the 2-year-old loan must be anonymised (%d are)", n)
	}

	// An absurd value must not disable the purge: it falls back to 3 years.
	for _, v := range []string{"0", "-5", "99", "trois", ""} {
		if _, err := db.Exec(`UPDATE setting SET value = ? WHERE key = 'retention_years'`, v); err != nil {
			t.Fatalf("setting: %v", err)
		}
		if got := retentionYears(db); got != 3 {
			t.Errorf("value %q: %d years, want the fallback to 3", v, got)
		}
	}
}

// The DBML beside the migration must match it; nothing else reads it. Parsed
// with a regex to avoid a second dependency.
func TestDBMLMatchesTheSchema(t *testing.T) {
	raw, err := os.ReadFile("migrations/schema.dbml")
	if err != nil {
		t.Fatal(err)
	}
	// Strip ''' note blocks, then // comments, so neither reads as a column.
	text := regexp.MustCompile(`(?s)'''.*?'''`).ReplaceAllString(string(raw), "")
	text = regexp.MustCompile(`(?m)^\s*//.*$`).ReplaceAllString(text, "")

	// "Table name {" ... "}" at column 0, so the indented indexes block does not
	// end the table early.
	blocks := regexp.MustCompile(`(?ms)^Table\s+(\w+)\s*\{(.*?)^\}`).FindAllStringSubmatch(text, -1)
	if len(blocks) == 0 {
		t.Fatal("no Table block found in schema.dbml")
	}

	documented := map[string][]string{}
	for _, b := range blocks {
		var cols []string
		// A column is the first word of a line, once the indexes block is gone.
		body := regexp.MustCompile(`(?ms)^\s{2}indexes\s*\{.*?^\s{2}\}`).ReplaceAllString(b[2], "")
		for _, line := range strings.Split(body, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "//") {
				continue
			}
			if m := regexp.MustCompile(`^(\w+)\s+\S`).FindStringSubmatch(line); m != nil {
				cols = append(cols, m[1])
			}
		}
		documented[b[1]] = cols
	}

	db := testDB(t)
	rows, err := db.Query(
		`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, n)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close() // before the next query: one connection

	for _, table := range tables {
		cols, ok := documented[table]
		if !ok {
			t.Errorf("table %q exists but is absent from schema.dbml", table)
			continue
		}
		var real []string
		r, err := db.Query(`SELECT name FROM pragma_table_info(?)`, table)
		if err != nil {
			t.Fatal(err)
		}
		for r.Next() {
			var n string
			if err := r.Scan(&n); err != nil {
				t.Fatal(err)
			}
			real = append(real, n)
		}
		r.Close()
		if !reflect.DeepEqual(real, cols) {
			t.Errorf("table %q: schema.dbml is out of date\n  database: %v\n  dbml:     %v", table, real, cols)
		}
		delete(documented, table)
	}
	for table := range documented {
		t.Errorf("schema.dbml documents table %q, which does not exist", table)
	}
}

// A due backup records what happened, for the settings screen.
func TestDailyMaintenanceRecordsWhatItDid(t *testing.T) {
	loadForTest(t)
	db := testDB(t)
	dir := t.TempDir()

	saved := LastBackup()
	t.Cleanup(func() { recordBackup(saved) })

	maintain(db, dir, time.Now(), "")

	state := LastBackup()
	if !state.Active {
		t.Error("backups are on and the state says otherwise")
	}
	if state.Error != "" {
		t.Errorf("backup failed: %s", state.Error)
	}
	if state.When.IsZero() {
		t.Error("no date recorded: the settings screen would still say never")
	}
	if state.Path != filepath.Join(dir, dailyBackupName(time.Now())) {
		t.Errorf("path recorded: %q", state.Path)
	}
	if _, err := os.Stat(state.Path); err != nil {
		t.Errorf("the recorded backup is not on disk: %v", err)
	}
}

// A failure reaches the screen too.
func TestDailyMaintenanceRecordsAFailure(t *testing.T) {
	loadForTest(t)
	db := testDB(t)

	saved := LastBackup()
	t.Cleanup(func() { recordBackup(saved) })

	// A file where the backup directory should be: MkdirAll cannot get past it.
	blocking := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(blocking, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	maintain(db, filepath.Join(blocking, "backups"), time.Now(), "")

	if state := LastBackup(); state.Error == "" {
		t.Error("a failed backup was recorded as a success")
	}
}

// With -backup-dir "" the purge still runs.
func TestDailyMaintenancePurgesWithoutBackups(t *testing.T) {
	loadForTest(t)
	restoreSettings(t)
	db := testDB(t)
	setAnonymousID(anonymousBorrowerID(db))
	anon := anonymousBorrowerID(db)

	if _, err := db.Exec(
		`INSERT INTO loan (copy_id, borrower_id, loaned_on, due_on, returned_on) VALUES
		 (2, 101, date('now','-4 years'), date('now','-4 years','+21 days'), date('now','-4 years','+10 days'))`); err != nil {
		t.Fatalf("insert: %v", err)
	}

	maintain(db, "", time.Now(), "")

	if n := count(t, db, `SELECT COUNT(*) FROM loan WHERE borrower_id = ?`, anon); n != 1 {
		t.Errorf("%d anonymised loans, want 1: the purge did not run", n)
	}
}

// Migrations are applied once: a restart must not replay 001_initial.sql.
func TestMigrationsAreAppliedOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")

	db, err := openDB(path)
	if err != nil {
		t.Fatalf("first start: %v", err)
	}
	applied := count(t, db, `SELECT COUNT(*) FROM schema_migrations`)
	if applied == 0 {
		t.Fatal("no migration recorded")
	}
	if _, err := db.Exec(
		`INSERT INTO book (isbn13, title) VALUES ('9782070408504', 'Le Petit Prince')`); err != nil {
		t.Fatalf("insert: %v", err)
	}
	db.Close()

	// Restart, as a small-office PC does every morning.
	db, err = openDB(path)
	if err != nil {
		t.Fatalf("restart: %v", err)
	}
	defer db.Close()
	if again := count(t, db, `SELECT COUNT(*) FROM schema_migrations`); again != applied {
		t.Errorf("%d migrations recorded after the restart, want %d", again, applied)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM book`); n != 1 {
		t.Errorf("%d works after the restart: the data did not survive", n)
	}
}

// The schema comes up complete: STRICT tables, the views and the settings.
func TestAFreshDatabaseIsUsable(t *testing.T) {
	db, err := openDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("openDB: %v", err)
	}
	defer db.Close()

	for _, name := range []string{"book", "copy", "borrower", "loan", "setting"} {
		if n := count(t, db,
			`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, name); n != 1 {
			t.Errorf("table %s missing", name)
		}
	}
	for _, name := range []string{"v_available", "v_active_loan", "v_overdue"} {
		if n := count(t, db,
			`SELECT COUNT(*) FROM sqlite_master WHERE type = 'view' AND name = ?`, name); n != 1 {
			t.Errorf("view %s missing", name)
		}
	}
	for _, key := range []string{"loan_days", "retention_years", "library_name", "language", "anonymous_borrower_id"} {
		if n := count(t, db, `SELECT COUNT(*) FROM setting WHERE key = ?`, key); n != 1 {
			t.Errorf("setting %s missing from a fresh database", key)
		}
	}
	// An empty database gives empty lists, not an error.
	for _, view := range []string{"v_available", "v_active_loan", "v_overdue"} {
		if n := count(t, db, `SELECT COUNT(*) FROM `+view); n != 0 {
			t.Errorf("%s: %d rows in an empty database", view, n)
		}
	}
}

// The sentinel's name follows the instance language: seeded empty, set by
// syncAnonymousName.
func TestTheSentinelIsNamedByTheLocaleCatalogue(t *testing.T) {
	loadForTest(t)
	restoreSettings(t)
	db := testDB(t)
	setAnonymousID(anonymousBorrowerID(db))

	name := func() string {
		var s string
		if err := db.QueryRow(
			`SELECT first_name FROM borrower WHERE id = ?`, anonymousID(),
		).Scan(&s); err != nil {
			t.Fatalf("reading the sentinel: %v", err)
		}
		return s
	}

	// Seeded empty.
	if got := name(); got != "" {
		t.Errorf("the migration seeds the sentinel as %q, want it left to Go", got)
	}

	setLang("fr")
	syncAnonymousName(db)
	if got, want := name(), T("fr", "borrower.anonymised_name"); got != want {
		t.Errorf("sentinel named %q on a French instance, want %q", got, want)
	}

	// Changing the language renames it.
	setLang("en")
	syncAnonymousName(db)
	if got, want := name(), T("en", "borrower.anonymised_name"); got != want {
		t.Errorf("sentinel named %q after switching to English, want %q", got, want)
	}
}

// demo.sql is loaded into the real schema and checked for the rules its header
// claims, since demonstration mode reloads it on a timer.
func TestDemoDatasetFitsTheSchema(t *testing.T) {
	db := loadInto(t, "demo.sql")

	// Enough of everything that the screens look like a library in use.
	for _, c := range []struct {
		what  string
		query string
		least int
	}{
		{"works", `SELECT COUNT(*) FROM book`, 100},
		{"copies", `SELECT COUNT(*) FROM copy`, 100},
		{"readers", `SELECT COUNT(*) FROM borrower WHERE group_name <> 'Enseignants' AND id <> 1`, 30},
		{"staff", `SELECT COUNT(*) FROM borrower WHERE group_name = 'Enseignants'`, 3},
		{"loans", `SELECT COUNT(*) FROM loan`, 300},
		{"loans still out", `SELECT COUNT(*) FROM v_active_loan`, 10},
		{"overdue loans", `SELECT COUNT(*) FROM v_overdue`, 1},
		// The NULLs the screens must read, so the screens test meets one of each.
		{"copies not filed", `SELECT COUNT(*) FROM copy WHERE location IS NULL`, 1},
		{"works without an ISBN", `SELECT COUNT(*) FROM book WHERE isbn13 IS NULL`, 1},
		{"works without a year", `SELECT COUNT(*) FROM book WHERE year IS NULL`, 1},
		{"works without a publisher", `SELECT COUNT(*) FROM book WHERE publisher IS NULL`, 1},
		{"borrowers without a group", `SELECT COUNT(*) FROM borrower WHERE group_name IS NULL`, 1},
	} {
		if n := count(t, db, c.query); n < c.least {
			t.Errorf("%s: %d, want at least %d", c.what, n, c.least)
		}
	}

	// A reader holds two books at most; staff borrow batches.
	if n := count(t, db,
		`SELECT COALESCE(MAX(n), 0) FROM (
		   SELECT COUNT(*) AS n FROM v_active_loan a
		     JOIN borrower b ON b.id = a.borrower_id
		    WHERE b.group_name <> 'Enseignants'
		    GROUP BY a.borrower_id)`); n > 2 {
		t.Errorf("a reader holds %d books at once, want 2 at most", n)
	}

	// The library opens once or twice a week, the same days: an offset's remainder
	// modulo seven is its weekday, so nearly every loan shares two of them.
	if n := count(t, db,
		`SELECT COUNT(*) FROM (
		   SELECT CAST(julianday('now') - julianday(loaned_on) AS INTEGER) % 7 AS d
		     FROM loan GROUP BY d)`); n > 4 {
		t.Errorf("loans fall on %d days of the week, want at most 4 "+
			"(two openings, plus the two loans the README quotes)", n)
	}

	// Every group is shown borrowing.
	if n := count(t, db,
		`SELECT COUNT(*) FROM (
		   SELECT b.group_name FROM loan l JOIN borrower b ON b.id = l.borrower_id
		    WHERE b.group_name <> 'Enseignants' GROUP BY b.group_name)`); n < 4 {
		t.Errorf("only %d groups ever borrow, want 4", n)
	}

	// A lost or withdrawn copy has no open loan (inventory.go).
	if n := count(t, db,
		`SELECT COUNT(*) FROM loan l JOIN copy c ON c.id = l.copy_id
		  WHERE l.returned_on IS NULL AND c.status <> 'available'`); n != 0 {
		t.Errorf("%d open loans on copies that are not on the shelf", n)
	}

	// Dates are relative to load day: none in the future, history reaching back months.
	if n := count(t, db, `SELECT COUNT(*) FROM loan WHERE loaned_on > date('now')`); n != 0 {
		t.Errorf("%d loans start in the future", n)
	}
	if n := count(t, db,
		`SELECT CAST(julianday('now') - julianday(MIN(loaned_on)) AS INTEGER) FROM loan`); n < 150 {
		t.Errorf("the oldest loan is %d days old, want months of history", n)
	}
}

// The schedule is anchored on a weekday, so it is loaded once per weekday: on
// the wrong one it could put a copy in two hands and be refused by the partial
// unique index. Substituting the literal for 'now' chooses the day.
func TestDemoDatasetLoadsOnEveryDayOfTheWeek(t *testing.T) {
	demo, err := os.ReadFile("demo.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, day := range []string{
		"2026-09-21", "2026-09-22", "2026-09-23", "2026-09-24",
		"2026-09-25", "2026-09-26", "2026-09-27",
	} {
		t.Run(day, func(t *testing.T) {
			db := emptyDB(t)
			if _, err := db.Exec(strings.ReplaceAll(string(demo), "'now'", "'"+day+"'")); err != nil {
				t.Fatalf("loading the demonstration as if it were %s: %v", day, err)
			}
			for _, c := range []struct {
				what  string
				query string
			}{
				{"copies out more than once at a time",
					`SELECT COUNT(*) FROM (SELECT copy_id FROM loan
					   WHERE returned_on IS NULL GROUP BY copy_id HAVING COUNT(*) > 1)`},
				{"readers holding more than two books",
					`SELECT COUNT(*) FROM (SELECT l.borrower_id FROM loan l
					   JOIN borrower b ON b.id = l.borrower_id
					  WHERE l.returned_on IS NULL AND b.group_name <> 'Enseignants'
					  GROUP BY l.borrower_id HAVING COUNT(*) > 2)`},
				{"loans given back before they were taken",
					`SELECT COUNT(*) FROM loan WHERE returned_on < loaned_on`},
				{"loans taken in the future",
					`SELECT COUNT(*) FROM loan WHERE loaned_on > '` + day + `'`},
			} {
				if n := count(t, db, c.query); n != 0 {
					t.Errorf("%s: %d", c.what, n)
				}
			}
		})
	}
}

// Every search folds accents and case on both sides, for letters SQLite's own
// LOWER() and LIKE leave alone.
func TestSearchesFoldAccentsAndCase(t *testing.T) {
	loadForTest(t)
	restoreSettings(t)
	a := testApp(t)
	setAnonymousID(anonymousBorrowerID(a.db))
	if _, err := a.db.Exec(
		`INSERT INTO book (id, title, authors, language, source_metadata)
		      VALUES (300, 'École des sorciers', 'Kästner, Erich', 'fr', 'test');
		 INSERT INTO copy (id, book_id, code, status) VALUES (300, 300, 'VOL-E0001', 'available');
		 INSERT INTO borrower (id, first_name, last_initial, group_name, active)
		      VALUES (300, 'Émile', 'Œ.', 'P5', 1)`); err != nil {
		t.Fatal(err)
	}

	for _, q := range []string{"école", "ecole", "ÉCOLE", "ECOLE", "kastner", "KÄSTNER"} {
		if found, _ := a.searchAvailableBooks(q, nil); len(found) != 1 || found[0].Code != "VOL-E0001" {
			t.Errorf("desk book search %q: %+v, want VOL-E0001", q, found)
		}
		rows, err := a.listInventory(invFilter{Q: q, Sort: invSortDefault, Dir: "asc"})
		if err != nil {
			t.Fatalf("listInventory(%q): %v", q, err)
		}
		if len(rows) != 1 || rows[0].Code != "VOL-E0001" {
			t.Errorf("inventory search %q: %d row(s), want VOL-E0001", q, len(rows))
		}
	}
	for _, q := range []string{"émile", "emile", "ÉMILE", "oe.", "Œ."} {
		if found, _ := a.searchBorrowers(q); len(found) != 1 || found[0].ID != 300 {
			t.Errorf("desk borrower search %q: %+v, want Émile", q, found)
		}
		list, err := a.listBorrowersFiltered(brFilter{Q: q})
		if err != nil {
			t.Fatalf("listBorrowersFiltered(%q): %v", q, err)
		}
		if len(list) != 1 || list[0].ID != 300 {
			t.Errorf("borrower list search %q: %d row(s), want Émile", q, len(list))
		}
	}
}

// setMTime dates a backup file as if written at t.
func setMTime(t *testing.T, path string, when time.Time) {
	t.Helper()
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatal(err)
	}
}

// What is due comes from the files' dates, not from the day of the week: a
// laptop asleep on Sunday still gets its weekly copy on Monday.
func TestBackupDue(t *testing.T) {
	now := time.Date(2026, 9, 30, 10, 0, 0, 0, time.Local) // a Wednesday
	fresh := func(t *testing.T) string {
		t.Helper()
		dir := t.TempDir()
		setMTime(t, filepath.Join(dir, dailyBackupName(now)), now.Add(-time.Hour))
		setMTime(t, filepath.Join(dir, weeklyBackupName(1)), now.AddDate(0, 0, -2))
		setMTime(t, filepath.Join(dir, monthlyBackupName(1)), now.AddDate(0, 0, -2))
		return dir
	}

	if backupDue(fresh(t), now) {
		t.Error("everything is fresh and a backup is due")
	}
	if !backupDue(t.TempDir(), now) {
		t.Error("an empty folder and no backup due")
	}

	dir := fresh(t)
	setMTime(t, filepath.Join(dir, dailyBackupName(now)), now.AddDate(0, 0, -7)) // last week's Wednesday
	if !backupDue(dir, now) {
		t.Error("today's slot holds last week's copy and no backup is due")
	}

	dir = fresh(t)
	setMTime(t, filepath.Join(dir, weeklyBackupName(1)), now.AddDate(0, 0, -9))
	if !backupDue(dir, now) {
		t.Error("the newest weekly copy is 9 days old and no backup is due")
	}

	dir = fresh(t)
	setMTime(t, filepath.Join(dir, monthlyBackupName(1)), time.Date(2026, 8, 31, 18, 0, 0, 0, time.Local))
	if !backupDue(dir, now) {
		t.Error("no monthly copy this month and no backup is due")
	}
}

// Weekly and monthly copies overwrite the missing slot first, then the oldest.
func TestBackupFillsTheOldestSlot(t *testing.T) {
	db := testDB(t)
	dir := t.TempDir()
	now := time.Now()
	for slot, age := range map[int]int{1: 10, 2: 40, 3: 20, 4: 30} {
		setMTime(t, filepath.Join(dir, weeklyBackupName(slot)), now.AddDate(0, 0, -age))
		setMTime(t, filepath.Join(dir, monthlyBackupName(slot)), now.AddDate(0, -age/10, 0))
	}

	if _, err := backup(db, dir, now); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{weeklyBackupName(2), monthlyBackupName(2)} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil || !sameDay(info.ModTime(), now) {
			t.Errorf("%s not rewritten: the oldest slot is the one to replace", name)
		}
	}
	if info, _ := os.Stat(filepath.Join(dir, weeklyBackupName(1))); sameDay(info.ModTime(), now) {
		t.Error("a newer weekly slot was overwritten")
	}
}

// The purge runs once a day, not on every hourly check.
func TestMaintainPurgesOncePerDay(t *testing.T) {
	loadForTest(t)
	restoreSettings(t)
	db := testDB(t)
	setAnonymousID(anonymousBorrowerID(db))
	anon := anonymousBorrowerID(db)
	if _, err := db.Exec(
		`INSERT INTO loan (copy_id, borrower_id, loaned_on, due_on, returned_on) VALUES
		 (2, 101, date('now','-4 years'), date('now','-4 years','+21 days'), date('now','-4 years','+10 days'))`); err != nil {
		t.Fatalf("insert: %v", err)
	}
	now := time.Now()
	today := now.Format("2006-01-02")

	if got := maintain(db, "", now, today); got != today {
		t.Errorf("maintain returned %q, want %q", got, today)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM loan WHERE borrower_id = ?`, anon); n != 0 {
		t.Error("the purge ran again on a day it had already run")
	}
	maintain(db, "", now, "2026-01-01")
	if n := count(t, db, `SELECT COUNT(*) FROM loan WHERE borrower_id = ?`, anon); n != 1 {
		t.Error("a new day and the purge did not run")
	}
}
