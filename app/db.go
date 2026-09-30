package main

import (
	"database/sql"
	"database/sql/driver"
	"embed"
	"fmt"
	"log"
	"sort"

	"modernc.org/sqlite"
)

// fold(text) is foldSearch inside SQL. Registered in init so it exists on every
// connection, the tests' included; NULL stays NULL.
func init() {
	sqlite.MustRegisterDeterministicScalarFunction("fold", 1,
		func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
			switch v := args[0].(type) {
			case nil:
				return nil, nil
			case string:
				return foldSearch(v), nil
			case []byte:
				return foldSearch(string(v)), nil
			default:
				return v, nil
			}
		})
	// shelf_mark(authors, title) is shelfMark inside SQL, so a list can be
	// ordered by cote and still be paged by the database.
	sqlite.MustRegisterDeterministicScalarFunction("shelf_mark", 2,
		func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
			return shelfMark(sqlText(args[0]), sqlText(args[1])), nil
		})
}

// sqlText reads a text argument of a registered function; NULL is "".
func sqlText(v driver.Value) string {
	switch v := v.(type) {
	case string:
		return v
	case []byte:
		return string(v)
	}
	return ""
}

//go:embed migrations/*.sql
var migrationsFS embed.FS

// openDB opens the SQLite file and applies the pending migrations. One
// connection, because SQLite takes one writer: never query a.db while a tx or
// *sql.Rows is open, or the handler waits on itself.
func openDB(path string) (*sql.DB, error) {
	dsn := fmt.Sprintf(
		"file:%s?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)",
		path,
	)

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}
	db.SetMaxOpenConns(1)

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("connecting to %s: %w", path, err)
	}

	if err := migrate(db); err != nil {
		return nil, fmt.Errorf("migrations: %w", err)
	}

	return db, nil
}

// migrate applies, in order, the files in migrations/ that have not run yet,
// and records which ones have.
func migrate(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS schema_migrations (
			name       TEXT PRIMARY KEY,
			applied_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`)
	if err != nil {
		return err
	}

	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return err
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names) // 001_, 002_, ...: lexical order is application order

	for _, name := range names {
		var already int
		err := db.QueryRow(
			`SELECT COUNT(*) FROM schema_migrations WHERE name = ?`, name,
		).Scan(&already)
		if err != nil {
			return err
		}
		if already > 0 {
			continue
		}

		content, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}

		// A migration applies entirely or not at all.
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(string(content)); err != nil {
			tx.Rollback()
			return fmt.Errorf("%s: %w", name, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations (name) VALUES (?)`, name); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}

		log.Printf("migration applied: %s", name)
	}

	return nil
}
