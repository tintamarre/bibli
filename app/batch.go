package main

import (
	"log"
	"net/http"
	"strings"
	"time"
)

// Batch cataloguing: the screen queues the scans and asks for them one after
// the other. A book a catalogue knows is saved at once, one copy; the others
// come back as "aside" rows and are filled in by hand once the scanning is
// done (the confirm form, in the screen's panel).

// batchRow is one line of the screen's lists. Kind is "ok" (saved) or "aside".
type batchRow struct {
	Kind   string
	Title  string
	ISBN   string
	Codes  []string
	Reason string
}

func (a *app) batchScreen(w http.ResponseWriter, r *http.Request) {
	a.render(w, r, "batch", map[string]any{"Title": tr(r, "catalogue.batch_title")})
}

func (a *app) batchAdd(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		badRequest(w, r)
		return
	}
	// The catalogues are asked one after the other: no write timeout.
	extendWriteDeadline(w, 2*time.Minute)

	raw := strings.TrimSpace(r.FormValue("isbn"))
	location := strings.TrimSpace(r.FormValue("location"))
	// The screen says the ISBN was already saved earlier in the session.
	again := r.FormValue("again") == "1"

	aside := func(reason string) {
		a.fragment(w, r, "batch", "batch_row", batchRow{Kind: "aside", ISBN: raw, Reason: reason})
	}

	i13, i10, err := ISBNForms(raw)
	if err != nil {
		aside(tr(r, "catalogue.batch_bad_isbn"))
		return
	}
	raw = i13

	n := Record{}
	if known, ok := a.existingBook(i13, i10); ok {
		if !again {
			aside(trn(r, "catalogue.batch_known", known.Current))
			return
		}
		n = known.N
	} else {
		rec, err := enrich(r.Context(), i13, i10)
		if r.Context().Err() != nil {
			return // the tab was closed
		}
		if rec == nil || rec.Title == "" {
			if err != nil {
				aside(tr(r, "catalogue.batch_silent"))
			} else {
				aside(tr(r, "catalogue.batch_notfound"))
			}
			return
		}
		n = *rec
	}

	tx, err := a.db.Begin()
	if err != nil {
		log.Printf("catalogue/batch/add (tx): %v", err)
		internalError(w, r)
		return
	}
	defer tx.Rollback()
	codes, err := a.createCopies(tx, n, 1, location)
	if err != nil {
		log.Printf("catalogue/batch/add: %v", err)
		internalError(w, r)
		return
	}
	if err := tx.Commit(); err != nil {
		log.Printf("catalogue/batch/add (commit): %v", err)
		internalError(w, r)
		return
	}
	a.fragment(w, r, "batch", "batch_row", batchRow{Kind: "ok", Title: n.Title, ISBN: i13, Codes: codes})
}

func (a *app) batchSave(w http.ResponseWriter, r *http.Request) {
	a.saveCatalogued(w, r, true)
}
