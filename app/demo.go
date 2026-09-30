package main

import (
	"database/sql"
	"embed"
	"fmt"
	"log"
	"os"
	"time"
)

// Demonstration mode (BIBLI_DEMO_RESET=6h): the public instance reloads its
// dataset on a timer. An environment variable, not a flag a school might copy.

//go:embed demo.sql
var demoFS embed.FS

// demoEvery is the reset interval, zero outside demonstration mode.
var demoEvery time.Duration

// demoInterval reads BIBLI_DEMO_RESET. A malformed value stops the binary
// rather than leaving the reset quietly off.
func demoInterval() (time.Duration, error) {
	raw := os.Getenv("BIBLI_DEMO_RESET")
	if raw == "" {
		return 0, nil
	}
	every, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("BIBLI_DEMO_RESET=%q: %w (expected a duration such as 6h)", raw, err)
	}
	if every < time.Minute {
		return 0, fmt.Errorf("BIBLI_DEMO_RESET=%q: too short, a minute at least", raw)
	}
	return every, nil
}

// startDemo reloads the dataset now, so a restart starts clean, and every
// interval after.
func startDemo(db *sql.DB, every time.Duration) {
	demoEvery = every
	go func() {
		for {
			if err := demoReset(db); err != nil {
				log.Printf("demonstration reset: %v", err)
			}
			time.Sleep(every)
		}
	}()
}

// demoReset empties the collection and loads demo.sql back in, in one
// transaction. It spares the sentinel borrower, the session secret (which
// would sign everyone out) and schema_migrations. Deletion follows the FKs.
func demoReset(db *sql.DB) error {
	data, err := demoFS.ReadFile("demo.sql")
	if err != nil {
		return fmt.Errorf("reading demo.sql: %w", err)
	}

	// Before the transaction. Zero would delete every borrower.
	anon := anonymousBorrowerID(db)
	if anon == 0 {
		return fmt.Errorf("anonymous borrower not found (001_initial.sql): refusing to empty the borrowers")
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, stmt := range []string{`DELETE FROM loan`, `DELETE FROM copy`, `DELETE FROM book`} {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("%s: %w", stmt, err)
		}
	}
	if _, err := tx.Exec(`DELETE FROM borrower WHERE id <> ?`, anon); err != nil {
		return fmt.Errorf("emptying borrower: %w", err)
	}
	if _, err := tx.Exec(string(data)); err != nil {
		return fmt.Errorf("loading demo.sql: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return err
	}

	// demo.sql reset the settings; the cache and the sentinel's name follow.
	loadSettingsCache(db)
	syncAnonymousName(db)

	log.Printf("demonstration data reloaded, next reset in %s", demoEvery)
	return nil
}
