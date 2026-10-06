package main

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Batch cataloguing: the screen queues the scans and asks for them one after
// the other, but writes nothing. A book a catalogue knows is staged on the
// right with its copies, to be checked (copies changed, a book removed) and
// added all at once, under the location chosen beside the add button. A book no
// catalogue can place is refused: it is catalogued book by book, on the screen
// that has room to type a notice.

// batchItem is a staged book as the screen carries it between lookup and save
// (the data-item of its row). The location is not part of it: it belongs to the
// batch, read once at save. The raw catalogue answer stays out of it too: at
// save it is read back from the lookup memory.
type batchItem struct {
	ISBN13    string `json:"isbn13"`
	ISBN10    string `json:"isbn10"`
	Title     string `json:"title"`
	Subtitle  string `json:"subtitle"`
	Authors   string `json:"authors"`
	Publisher string `json:"publisher"`
	Year      int    `json:"year"`
	Language  string `json:"language"`
	Source    string `json:"source"`
	URL       string `json:"url"`
	Copies    int    `json:"copies"`
}

func (it batchItem) record() Record {
	return Record{
		ISBN13: it.ISBN13, ISBN10: it.ISBN10, Title: it.Title, Subtitle: it.Subtitle,
		Authors: it.Authors, Publisher: it.Publisher, Year: it.Year, Language: it.Language,
		Source: it.Source, URL: it.URL,
	}
}

// batchRow is one line of the screen's lists. Kind is "found" (staged) or
// "refused"; Codes carries the result block's recap line.
type batchRow struct {
	Kind   string
	Title  string
	ISBN   string
	Meta   string // authors · publisher · year
	Badge  string // what adding it changes: a new title, or more copies of a known one
	Known  bool
	Copies int
	Item   string // batchItem as JSON
	Reason string
	Manual bool // a valid ISBN, so the refusal can link to cataloguing it by hand
	Codes  []string
}

func (a *app) batchScreen(w http.ResponseWriter, r *http.Request) {
	locations, err := a.copyLocations()
	if err != nil {
		log.Printf("catalogue/batch (locations): %v", err)
		internalError(w, r)
		return
	}
	a.render(w, r, "batch", map[string]any{
		"Title": tr(r, "catalogue.batch_title"), "Locations": locations,
	})
}

// stagedRow describes a book about to be added, for a person to check: what it
// is, and whether it is a new title or more copies of one already catalogued.
func (a *app) stagedRow(r *http.Request, n Record, copies int) batchRow {
	it := batchItem{
		ISBN13: n.ISBN13, ISBN10: n.ISBN10, Title: n.Title, Subtitle: n.Subtitle,
		Authors: n.Authors, Publisher: n.Publisher, Year: n.Year, Language: n.Language,
		Source: n.Source, URL: n.URL, Copies: copies,
	}
	js, _ := json.Marshal(it)

	var meta []string
	for _, s := range []string{n.Authors, n.Publisher} {
		if s != "" {
			meta = append(meta, s)
		}
	}
	if n.Year > 0 {
		meta = append(meta, strconv.Itoa(n.Year))
	}

	row := batchRow{Kind: "found", Title: n.Title, ISBN: n.ISBN13, Meta: strings.Join(meta, " · "),
		Copies: copies, Item: string(js), Badge: tr(r, "catalogue.batch_new")}
	if known, ok := a.existingBook(n.ISBN13, n.ISBN10); ok {
		row.Known = true
		row.Badge = trn(r, "catalogue.batch_known", known.Current)
	}
	return row
}

// batchAdd looks a scan up and answers with the row to stage, or the refusal to
// list. Nothing is written.
func (a *app) batchAdd(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		badRequest(w, r)
		return
	}
	// The catalogues are asked one after the other: no write timeout.
	extendWriteDeadline(w, 2*time.Minute)

	raw := strings.TrimSpace(r.FormValue("isbn"))

	refuse := func(reason string, manual bool) {
		a.fragment(w, r, "batch", "batch_row", batchRow{Kind: "refused", ISBN: raw, Reason: reason, Manual: manual})
	}

	i13, i10, err := ISBNForms(raw)
	if err != nil {
		// A wrong check digit is a misread far more often than a wrong book:
		// scanning it again is the fix, not typing a notice.
		refuse(tr(r, "catalogue.batch_bad_isbn"), false)
		return
	}
	raw = i13

	var n Record
	if known, ok := a.existingBook(i13, i10); ok {
		n = known.N
	} else {
		rec, err := enrich(r.Context(), i13, i10)
		if r.Context().Err() != nil {
			return // the tab was closed
		}
		if rec == nil || rec.Title == "" {
			if err != nil {
				refuse(tr(r, "catalogue.batch_silent"), true)
			} else {
				refuse(tr(r, "catalogue.batch_notfound"), true)
			}
			return
		}
		n = *rec
	}
	a.fragment(w, r, "batch", "batch_row", a.stagedRow(r, n, 1))
}

// batchSave writes every staged book in one transaction, from the screen's
// "item" fields, into the location the screen carries, and answers with what
// was created.
func (a *app) batchSave(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		badRequest(w, r)
		return
	}
	location := strings.TrimSpace(r.FormValue("location"))
	raw := r.Form["item"]
	if len(raw) == 0 || len(raw) > 5000 {
		badRequest(w, r)
		return
	}
	items := make([]batchItem, 0, len(raw))
	for _, s := range raw {
		var it batchItem
		if err := json.Unmarshal([]byte(s), &it); err != nil {
			badRequest(w, r)
			return
		}
		it.Title = strings.TrimSpace(it.Title)
		i13, i10, err := FormISBNs(it.ISBN13, it.ISBN10)
		if err != nil || it.Title == "" {
			badRequest(w, r)
			return
		}
		it.ISBN13, it.ISBN10 = i13, i10
		it.Copies = min(max(it.Copies, 1), 100)
		items = append(items, it)
	}

	tx, err := a.db.Begin()
	if err != nil {
		log.Printf("catalogue/batch/save (tx): %v", err)
		internalError(w, r)
		return
	}
	defer tx.Rollback()

	var rows []batchRow
	total := 0
	for _, it := range items {
		n := it.record()
		// The catalogue's raw answer is kept for later enrichment, when the
		// lookup memory still holds it.
		if it.ISBN13 != "" && it.Source != sourceManual {
			if m, ok := catalogueMemo.record(it.ISBN13); ok && m != nil {
				n.Payload = m.Payload
			}
		}
		codes, err := a.createCopies(tx, n, it.Copies, location)
		if err != nil {
			log.Printf("catalogue/batch/save: %v", err)
			internalError(w, r)
			return
		}
		total += len(codes)
		rows = append(rows, batchRow{Title: it.Title, ISBN: it.ISBN13, Codes: codes})
	}
	if err := tx.Commit(); err != nil {
		log.Printf("catalogue/batch/save (commit): %v", err)
		internalError(w, r)
		return
	}

	var codes []string
	for _, row := range rows {
		codes = append(codes, row.Codes...)
	}
	a.fragment(w, r, "batch", "batch_result", map[string]any{
		"Total": total, "Rows": rows, "Codes": strings.Join(codes, ","),
	})
}
