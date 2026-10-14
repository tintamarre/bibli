package main

import (
	"database/sql"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Changing the location of many copies at once: renaming a location (every
// copy in it follows), or scanning copies one by one into a chosen location.

// renameLocation moves every copy filed under from (the empty string for the
// copies with none) to to, and says how many moved.
func (a *app) renameLocation(from, to string) (int64, error) {
	res, err := a.db.Exec(`UPDATE copy SET location = ? WHERE COALESCE(location, '') = ?`, to, from)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// locationRename renames the location the inventory is filtered on. A name
// already in use merges the two, which cannot be undone: that is asked first.
// Done, the screen reloads on the new name with the rest of its filter.
func (a *app) locationRename(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		badRequest(w, r)
		return
	}
	from, filtering := locationFilter(r.FormValue("from"))
	to := strings.TrimSpace(r.FormValue("to"))
	switch {
	case !filtering:
		badRequest(w, r)
		return
	case to == "":
		a.fragment(w, r, "inventory", "rename_err", tr(r, "rename.err_empty"))
		return
	case to == from:
		a.fragment(w, r, "inventory", "rename_err", tr(r, "rename.err_same"))
		return
	}

	if r.FormValue("merge") != "1" {
		var already int
		if err := a.db.QueryRow(`SELECT COUNT(*) FROM copy WHERE location = ?`, to).Scan(&already); err != nil {
			log.Printf("inventory/location (count): %v", err)
			internalError(w, r)
			return
		}
		if already > 0 {
			a.fragment(w, r, "inventory", "rename_merge", map[string]any{"To": to, "Count": already})
			return
		}
	}

	if _, err := a.renameLocation(from, to); err != nil {
		log.Printf("inventory/location: %v", err)
		internalError(w, r)
		return
	}
	vals, _ := url.ParseQuery(r.FormValue("filter"))
	f := readInvFilter(&http.Request{Form: vals})
	f.Location = to
	w.Header().Set("HX-Redirect", string(f.URL("/inventory")))
}

// ShelfCopy is a copy as a "which one?" list shows it.
type ShelfCopy struct {
	ID       int64
	Code     string
	Title    string
	Location string
}

// copiesForScan finds what a scan names: one copy by its code, or every copy
// of the book whose ISBN it is. sql.ErrNoRows when neither.
func (a *app) copiesForScan(scan string) ([]ShelfCopy, error) {
	const columns = `SELECT c.id, c.code, b.title, COALESCE(c.location, '')
		   FROM copy c JOIN book b ON b.id = c.book_id`
	var c ShelfCopy
	err := a.db.QueryRow(columns+` WHERE c.code = ?`, scan).Scan(&c.ID, &c.Code, &c.Title, &c.Location)
	if err == nil {
		return []ShelfCopy{c}, nil
	}
	if err != sql.ErrNoRows {
		return nil, err
	}
	i13, i10, e := ISBNForms(scan)
	if e != nil {
		return nil, sql.ErrNoRows
	}
	bookID, _, err := a.bookByISBN(i13, i10)
	if err != nil {
		return nil, err
	}
	rows, err := a.db.Query(columns+` WHERE c.book_id = ? ORDER BY c.code`, bookID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ShelfCopy
	for rows.Next() {
		if err := rows.Scan(&c.ID, &c.Code, &c.Title, &c.Location); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, sql.ErrNoRows
	}
	return out, nil
}

func (a *app) moveScreen(w http.ResponseWriter, r *http.Request) {
	a.render(w, r, "move", map[string]any{
		"Title":     tr(r, "move.title"),
		"Location":  strings.TrimSpace(r.URL.Query().Get("location")),
		"Locations": a.locationSuggestions(),
	})
}

// moveScan files the scanned copy under the location the screen carries, or
// asks which one when an ISBN names several copies.
func (a *app) moveScan(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		badRequest(w, r)
		return
	}
	location := strings.TrimSpace(r.FormValue("location"))
	if location == "" {
		a.fragment(w, r, "move", "move_err", tr(r, "move.err_no_location"))
		return
	}

	// Answer to "which copy?".
	if v := strings.TrimSpace(r.FormValue("copy_id")); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			badRequest(w, r)
			return
		}
		var c ShelfCopy
		err = a.db.QueryRow(`SELECT c.id, c.code, b.title, COALESCE(c.location, '')
			   FROM copy c JOIN book b ON b.id = c.book_id WHERE c.id = ?`, id).
			Scan(&c.ID, &c.Code, &c.Title, &c.Location)
		if err != nil {
			log.Printf("inventory/move (copy %d): %v", id, err)
			badRequest(w, r)
			return
		}
		a.moveCopy(w, r, c, location)
		return
	}

	code := cleanCode(r.FormValue("code"))
	if code == "" {
		a.fragment(w, r, "move", "move_err", tr(r, "loan.err_no_code_book"))
		return
	}
	if misreadCode(code) {
		a.fragment(w, r, "move", "move_err", tr(r, "loan.err_misread", code))
		return
	}
	copies, err := a.copiesForScan(code)
	switch {
	case err == sql.ErrNoRows:
		a.fragment(w, r, "move", "move_err", tr(r, "return.err_unknown_code", code))
		return
	case err != nil:
		log.Printf("inventory/move (resolve): %v", err)
		internalError(w, r)
		return
	}
	// Each copy of a title may sit somewhere else: the librarian picks.
	if len(copies) > 1 {
		a.fragment(w, r, "move", "move_choice", map[string]any{"Copies": copies, "Location": location})
		return
	}
	a.moveCopy(w, r, copies[0], location)
}

func (a *app) moveCopy(w http.ResponseWriter, r *http.Request, c ShelfCopy, location string) {
	data := map[string]any{"Copy": c, "Location": location, "Already": c.Location == location}
	if c.Location != location {
		if _, err := a.db.Exec(`UPDATE copy SET location = ? WHERE id = ?`, location, c.ID); err != nil {
			log.Printf("inventory/move: %v", err)
			internalError(w, r)
			return
		}
	}
	a.fragment(w, r, "move", "move_ok", data)
}
