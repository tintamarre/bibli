package main

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Each language is named in itself.
func TestOfferedLangs(t *testing.T) {
	offered := offeredLangs("en")
	if len(offered) != len(langs) {
		t.Fatalf("%d languages offered, want %d", len(offered), len(langs))
	}
	selected := 0
	for _, l := range offered {
		if l.Name == "" || l.Name == l.Code {
			t.Errorf("%s is offered under %q, not its own name", l.Code, l.Name)
		}
		if l.Selected {
			selected++
			if l.Code != "en" {
				t.Errorf("%s marked as selected instead of en", l.Code)
			}
		}
	}
	if selected != 1 {
		t.Errorf("%d languages marked as selected, want exactly 1", selected)
	}
	// An unknown selection selects nothing rather than guessing.
	for _, l := range offeredLangs("kl") {
		if l.Selected {
			t.Errorf("%s selected for an unknown setting", l.Code)
		}
	}
}

// Backups can be checked from the screen, without reading a log.
func TestBackupView(t *testing.T) {
	loadForTest(t)
	restoreSettings(t)
	setLang("fr")
	r := httptest.NewRequest("GET", "/settings", nil)

	saved := LastBackup()
	t.Cleanup(func() { recordBackup(saved) })

	// Just started: on, none run yet, said as "never" rather than a zero date.
	recordBackup(BackupState{Active: true})
	v := backupView(r, "/var/lib/bibli/biblio.db", "")
	if !v.Active || !v.Never {
		t.Errorf("fresh start: %+v, want active and never", v)
	}
	if v.DBPath != "/var/lib/bibli/biblio.db" {
		t.Errorf("DBPath = %q", v.DBPath)
	}

	// One done: the date is shown in the interface language.
	when := time.Date(2026, 9, 11, 14, 3, 0, 0, time.UTC)
	recordBackup(BackupState{Active: true, When: when, Path: "/backups/biblio-daily-friday.db"})
	v = backupView(r, "biblio.db", "")
	if v.Never {
		t.Error("a backup was recorded and the screen still says never")
	}
	if v.When == "" {
		t.Fatal("empty date for a recorded backup")
	}
	for _, want := range []string{"11-09-2026", "14:03"} {
		if !strings.Contains(v.When, want) {
			t.Errorf("date shown %q, want it to carry %q", v.When, want)
		}
	}

	// A failure is shown.
	recordBackup(BackupState{Active: true, When: when, Error: "disk full"})
	if v = backupView(r, "biblio.db", ""); v.Error != "disk full" {
		t.Errorf("Error = %q, want it passed through", v.Error)
	}

	// Backups turned off (-backup-dir ""): said plainly rather than as "never".
	recordBackup(BackupState{Active: false})
	if v = backupView(r, "biblio.db", ""); v.Active {
		t.Error("backups are disabled and the screen says they are on")
	}
}

// The cover cache size is shown, and says when it is turned off.
func TestCacheView(t *testing.T) {
	a := &app{covers: newCoverCache(t.TempDir())}
	a.covers.write("9782070408504", []byte("0123456789"), "image/jpeg")

	v := a.cacheView("fr")
	if !v.Active || v.Count != 1 || v.Size != "10 o" {
		t.Errorf("cacheView = %+v, want one file of 10 o", v)
	}
	if v := (&app{covers: newCoverCache("")}).cacheView("fr"); v.Active {
		t.Error("a disabled cache is shown as active")
	}
}

// settingsData echoes back what was just typed after a rejected entry.
func TestSettingsData(t *testing.T) {
	loadForTest(t)
	restoreSettings(t)
	a := &app{db: testDB(t), covers: newCoverCache(t.TempDir()), dbPath: "/var/lib/bibli/biblio.db"}
	setLang("fr")

	d := a.settingsData(httptest.NewRequest("GET", "/settings", nil), "École communale", "en", defaultTheme)

	if d["LoanDays"] != 14 {
		t.Errorf("LoanDays = %v, want 14", d["LoanDays"])
	}
	if d["Retention"] != 3 {
		t.Errorf("Retention = %v, want the default 3 years", d["Retention"])
	}
	if d["RetentionMax"] != maxRetentionYears {
		t.Errorf("RetentionMax = %v, want %d", d["RetentionMax"], maxRetentionYears)
	}
	// The values passed in are echoed back, not re-read from the database.
	if d["School"] != "École communale" || d["Language"] != "en" {
		t.Errorf("School = %v, Language = %v", d["School"], d["Language"])
	}
	langs, ok := d["Languages"].([]offeredLang)
	if !ok {
		t.Fatalf("Languages = %T", d["Languages"])
	}
	for _, l := range langs {
		if l.Selected && l.Code != "en" {
			t.Errorf("%s marked as selected, want en (the value being edited)", l.Code)
		}
	}
	if b, ok := d["Backup"].(backupStatus); !ok || b.DBPath != a.dbPath {
		t.Errorf("Backup = %+v, want the database path shown", d["Backup"])
	}
	if _, ok := d["Cache"].(cacheView); !ok {
		t.Errorf("Cache = %T", d["Cache"])
	}
}

// An unreadable or out-of-range retention falls back to three years.
func TestRetentionYears(t *testing.T) {
	db := testDB(t)

	cases := map[string]int{
		"1":     1,
		"5":     5,
		"10":    maxRetentionYears,
		" 4 ":   4,
		"11":    3, // beyond ten years, keeping a minor's history is not defensible
		"0":     3,
		"-1":    3,
		"":      3,
		"trois": 3,
	}
	for value, want := range cases {
		if _, err := db.Exec(
			`UPDATE setting SET value = ? WHERE key = 'retention_years'`, value); err != nil {
			t.Fatal(err)
		}
		if got := retentionYears(db); got != want {
			t.Errorf("retention_years = %q: retentionYears() = %d, want %d", value, got, want)
		}
	}

	if _, err := db.Exec(`DELETE FROM setting WHERE key = 'retention_years'`); err != nil {
		t.Fatal(err)
	}
	if got := retentionYears(db); got != 3 {
		t.Errorf("retentionYears = %d with the setting deleted, want 3", got)
	}
}

// One row per day, newest first: weekly and monthly copies are that day's daily.
func TestListBackupsGroupsByDay(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, when time.Time, size int) {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, make([]byte, size), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, when, when); err != nil {
			t.Fatal(err)
		}
	}
	mon := time.Date(2026, 9, 14, 3, 0, 0, 0, time.UTC)
	// Monday's backup, and the weekly copy taken from it the same morning.
	write("biblio-daily-monday.db", mon, 2048)
	write("biblio-weekly-3.db", mon.Add(time.Minute), 2048)
	// A second day, and a file that is not ours at all.
	write("biblio-daily-tuesday.db", mon.AddDate(0, 0, 1), 4096)
	write("notes.txt", mon.AddDate(0, 0, 2), 10)

	got := listBackups(dir, "en")
	if len(got) != 2 {
		t.Fatalf("%d rows, want one per day: %+v", len(got), got)
	}
	// Newest first: Tuesday before Monday.
	if got[0].Name != "biblio-daily-tuesday.db" {
		t.Errorf("first row = %q, want Tuesday's", got[0].Name)
	}
	// Of Monday's two files the later one is offered.
	if got[1].Name != "biblio-weekly-3.db" {
		t.Errorf("second row = %q, want the later of Monday's two files", got[1].Name)
	}
	for _, f := range got {
		if f.When == "" || f.Size == "" {
			t.Errorf("row without a date or a size: %+v", f)
		}
	}

	// Backups switched off: nothing to offer, and no error about it.
	if n := listBackups("", "en"); n != nil {
		t.Errorf("backups disabled and %d rows offered", len(n))
	}
}

// The access block turns the request's own address into what a teacher types,
// and recognises a localhost-only opening (which no other device can reach).
func TestAccessAddress(t *testing.T) {
	loadForTest(t)
	a := &app{db: testDB(t), covers: newCoverCache(t.TempDir())}

	lan := httptest.NewRequest("GET", "/settings", nil)
	lan.Host = "192.168.1.42:8080"
	d := a.settingsData(lan, "", "fr", defaultTheme)
	if d["AccessURL"] != "http://192.168.1.42:8080" {
		t.Errorf("AccessURL = %v, want the request's own http address", d["AccessURL"])
	}
	if d["AccessLocal"] != false {
		t.Error("a LAN address must not be flagged as loopback")
	}

	for _, host := range []string{"localhost:8080", "127.0.0.1:8765", "[::1]:8080", "bibli.localhost"} {
		if !accessIsLoopback(host) {
			t.Errorf("accessIsLoopback(%q) = false, want true", host)
		}
	}
	for _, host := range []string{"192.168.1.42:8080", "bibli.school.be", "10.0.0.5:8080"} {
		if accessIsLoopback(host) {
			t.Errorf("accessIsLoopback(%q) = true, want false", host)
		}
	}
}
