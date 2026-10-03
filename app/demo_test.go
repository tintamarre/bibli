package main

import (
	"testing"
	"time"
)

// A value that is not a duration stops the binary rather than leaving the reset
// quietly off.
func TestDemoInterval(t *testing.T) {
	for _, c := range []struct {
		raw  string
		want time.Duration
		err  bool
	}{
		{"", 0, false}, // every instance: demonstration mode off
		{"6h", 6 * time.Hour, false},
		{"90m", 90 * time.Minute, false},
		{"6", 0, true}, // no unit: Go reads no duration here
		{"six hours", 0, true},
		{"10s", 0, true}, // below the floor
		{"0h", 0, true},
	} {
		t.Setenv("BIBLI_DEMO_RESET", c.raw)
		got, err := demoInterval()
		if c.err {
			if err == nil {
				t.Errorf("BIBLI_DEMO_RESET=%q: no error, want one", c.raw)
			}
			continue
		}
		if err != nil {
			t.Errorf("BIBLI_DEMO_RESET=%q: %v", c.raw, err)
		}
		if got != c.want {
			t.Errorf("BIBLI_DEMO_RESET=%q: %s, want %s", c.raw, got, c.want)
		}
	}
}

// A reset is repeatable: the second runs against what the first built, and the
// UNIQUE codes catch a table the wipe missed.
func TestDemoResetRunsTwice(t *testing.T) {
	db := testDB(t) // the fixture, so the reset has something to clear
	t.Cleanup(func() { demoEvery = 0 })
	demoEvery = 6 * time.Hour

	if _, err := db.Exec(
		`INSERT INTO setting (key, value, label) VALUES ('session_secret', 'kept-across-resets', '')`,
	); err != nil {
		t.Fatal(err)
	}

	for pass := 1; pass <= 2; pass++ {
		if err := demoReset(db); err != nil {
			t.Fatalf("pass %d: %v", pass, err)
		}
		if n := count(t, db, `SELECT COUNT(*) FROM book`); n < 100 {
			t.Errorf("pass %d: %d works, want the demonstration collection", pass, n)
		}
		if n := count(t, db, `SELECT COUNT(*) FROM v_overdue`); n < 1 {
			t.Errorf("pass %d: no overdue loan, the demonstration shows none", pass)
		}
	}

	// Nothing of the fixture is left: VOL204572 is in both, so the codes would
	// collide if a table went unemptied.
	if n := count(t, db, `SELECT COUNT(*) FROM borrower WHERE card_code = 'LEC73048'`); n != 1 {
		t.Errorf("Léa's card appears %d times, want once", n)
	}

	// The sentinel survives.
	if n := count(t, db, `SELECT COUNT(*) FROM borrower WHERE id = 1`); n != 1 {
		t.Error("the anonymous borrower was deleted by the reset")
	}

	// The settings a visitor could change are back.
	var days, lang string
	if err := db.QueryRow(`SELECT value FROM setting WHERE key = 'loan_days'`).Scan(&days); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT value FROM setting WHERE key = 'language'`).Scan(&lang); err != nil {
		t.Fatal(err)
	}
	if days != "21" || lang != "fr" {
		t.Errorf("settings after a reset: loan_days %q, language %q; want 21 and fr", days, lang)
	}

	// The session secret is not reset, or every visitor is signed out mid-click.
	var secret string
	if err := db.QueryRow(
		`SELECT value FROM setting WHERE key = 'session_secret'`).Scan(&secret); err != nil {
		t.Fatalf("session secret after two resets: %v", err)
	}
	if secret != "kept-across-resets" {
		t.Errorf("session secret is %q after a reset, want the one from before", secret)
	}
}
