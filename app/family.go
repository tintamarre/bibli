package main

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"log"
	"net/http"
)

// Parent space: one secret link per pupil shows their open loans, with the
// first name and school name only. No data at all for an unknown token.

// generateToken produces a URL-safe 128-bit token (22 characters).
func generateToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// familyScreen serves the public GET /family/{token} page.
func (a *app) familyScreen(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	if !familyLinks() {
		a.notFoundScreen(w, r) // turned off in /settings: every link is closed
		return
	}

	var id int64
	var firstName string
	err := a.db.QueryRow(
		`SELECT id, first_name FROM borrower WHERE family_token = ? AND active = 1`, token,
	).Scan(&id, &firstName)
	if err == sql.ErrNoRows {
		a.notFoundScreen(w, r) // generic 404: does not distinguish unknown / revoked / inactive
		return
	}
	if err != nil {
		log.Printf("family: %v", err)
		internalError(w, r)
		return
	}

	rows, err := a.db.Query(
		`SELECT b.title, l.due_on,
		        CAST(julianday('now') - julianday(l.due_on) AS INTEGER) AS days_overdue
		   FROM loan l
		   JOIN copy c ON c.id = l.copy_id
		   JOIN book    b ON b.id = c.book_id
		  WHERE l.borrower_id = ? AND l.returned_on IS NULL
		  ORDER BY days_overdue DESC, b.title`, id)
	if err != nil {
		log.Printf("family (loans): %v", err)
		internalError(w, r)
		return
	}
	defer rows.Close()

	type book struct {
		Title string
		DueOn string
		Days  int
	}
	var overdue, current []book
	for rows.Next() {
		var title, dueOn string
		var days int
		if err := rows.Scan(&title, &dueOn, &days); err != nil {
			log.Printf("family (scan): %v", err)
			internalError(w, r)
			return
		}
		l := book{Title: title, DueOn: dueOn, Days: days}
		if days > 0 {
			overdue = append(overdue, l)
		} else {
			current = append(current, l)
		}
	}

	a.renderDoc(w, r, "family", map[string]any{
		"FirstName": firstName,
		"Overdue":   overdue,
		"Current":   current,
	})
}

// borrowerTokenCreate generates (or regenerates) a pupil's token.
func (a *app) borrowerTokenCreate(w http.ResponseWriter, r *http.Request) {
	if !familyLinks() {
		a.notFoundScreen(w, r)
		return
	}
	id := pathID(r)
	token, err := generateToken()
	if err != nil {
		log.Printf("token (generation): %v", err)
		internalError(w, r)
		return
	}
	res, err := a.db.Exec(`UPDATE borrower SET family_token = ? WHERE id = ? AND active = 1`, token, id)
	if err != nil {
		log.Printf("token (save): %v", err)
		internalError(w, r)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		token = "" // pupil unknown or inactive: show the "no link" state again
	}
	a.fragment(w, r, "borrowers", tokenBlock(r), map[string]any{"ID": id, "Token": token})
}

// borrowerTokenRevoke deletes the parent token, so the old link stops working.
func (a *app) borrowerTokenRevoke(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	if _, err := a.db.Exec(`UPDATE borrower SET family_token = NULL WHERE id = ?`, id); err != nil {
		log.Printf("token (revoke): %v", err)
		internalError(w, r)
		return
	}
	a.fragment(w, r, "borrowers", tokenBlock(r), map[string]any{"ID": id, "Token": ""})
}

// tokenBlock answers in the shape the buttons were drawn in: a list cell, or
// the menu on a borrower's page (tracking_link.html).
func tokenBlock(r *http.Request) string {
	if r.FormValue("view") == "menu" {
		return "token_menu"
	}
	return "token_cell"
}
