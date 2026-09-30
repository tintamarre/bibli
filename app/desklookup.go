package main

import (
	"log"
	"net/http"
	"regexp"
	"sync"
)

// The desk's "searching…" line names the catalogue a scan is waiting on.
// Each scan sends an id with /borrow/add; the narrator records the catalogue
// under it and /borrow/lookup reads it back. In memory, for the request's life.

var deskLookups = struct {
	sync.Mutex
	m map[string]string
}{m: map[string]string{}}

// The cap only stops a runaway page from growing the map.
const maxDeskLookups = 64

var reDeskLookupID = regexp.MustCompile(`^[0-9a-f]{16}$`)

func setDeskLookup(id, sentence string) {
	if !reDeskLookupID.MatchString(id) {
		return
	}
	deskLookups.Lock()
	defer deskLookups.Unlock()
	if _, ok := deskLookups.m[id]; !ok && len(deskLookups.m) >= maxDeskLookups {
		return
	}
	deskLookups.m[id] = sentence
}

func clearDeskLookup(id string) {
	deskLookups.Lock()
	delete(deskLookups.m, id)
	deskLookups.Unlock()
}

// deskNarrator is the chain's narrator for one desk scan: it names each
// catalogue as it is asked, in the school's language.
func deskNarrator(r *http.Request, id string) enrichLog {
	if !reDeskLookupID.MatchString(id) {
		return enrichLog{}
	}
	return enrichLog{trying: func(s enrichSource) {
		setDeskLookup(id, tr(r, "catalogue.sse_searching", tr(r, s.key)))
	}}
}

// borrowLookup answers the line's question: the sentence for a scan still
// waiting on a catalogue, or 204 when there is none — a scan that never
// reached the catalogues, or one that has already answered.
func (a *app) borrowLookup(w http.ResponseWriter, r *http.Request) {
	deskLookups.Lock()
	sentence, ok := deskLookups.m[r.URL.Query().Get("id")]
	deskLookups.Unlock()
	w.Header().Set("Cache-Control", "no-store")
	if !ok {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if _, err := w.Write([]byte(sentence)); err != nil {
		log.Printf("borrow/lookup: %v", err)
	}
}
