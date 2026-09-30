package main

import (
	"fmt"
	"net/http/httptest"
	"testing"
)

func lookupStatus(t *testing.T, a *app, id string) (int, string) {
	t.Helper()
	w := httptest.NewRecorder()
	a.borrowLookup(w, httptest.NewRequest("GET", "/borrow/lookup?id="+id, nil))
	return w.Code, w.Body.String()
}

// While a scan waits the line names the catalogue; once answered, nothing, so a
// slow poll cannot put an old sentence back.
func TestDeskLookupNamesTheCatalogueWhilePending(t *testing.T) {
	loadForTest(t)
	a := &app{}
	const id = "0123456789abcdef"
	r := httptest.NewRequest("POST", "/borrow/add", nil)

	if code, _ := lookupStatus(t, a, id); code != 204 {
		t.Errorf("before the scan: %d, want 204", code)
	}
	deskNarrator(r, id).trying(enrichmentSources()[0])
	code, body := lookupStatus(t, a, id)
	if want := tr(r, "catalogue.sse_searching", tr(r, "catalogue.source_bnf")); code != 200 || body != want {
		t.Errorf("while asking the BnF: %d %q, want 200 %q", code, body, want)
	}
	clearDeskLookup(id)
	if code, _ := lookupStatus(t, a, id); code != 204 {
		t.Errorf("after the scan: %d, want 204", code)
	}
}

// The id arrives from the page: anything but the page's own shape is not
// recorded, and the map cannot be grown past its cap.
func TestDeskLookupRefusesStrangeIDsAndStaysSmall(t *testing.T) {
	r := httptest.NewRequest("POST", "/borrow/add", nil)
	for _, id := range []string{"", "short", "0123456789ABCDEF", "0123456789abcdef0", "../../etc/passwd"} {
		if n := deskNarrator(r, id); n.trying != nil {
			t.Errorf("narrator built for id %q", id)
		}
		setDeskLookup(id, "x")
		deskLookups.Lock()
		_, ok := deskLookups.m[id]
		deskLookups.Unlock()
		if ok {
			t.Errorf("id %q was recorded", id)
		}
	}

	var ids []string
	for i := 0; i < maxDeskLookups+10; i++ {
		id := fmt.Sprintf("%016x", i)
		ids = append(ids, id)
		setDeskLookup(id, "x")
	}
	defer func() {
		for _, id := range ids {
			clearDeskLookup(id)
		}
	}()
	deskLookups.Lock()
	n := len(deskLookups.m)
	deskLookups.Unlock()
	if n > maxDeskLookups {
		t.Errorf("%d lookups held, cap is %d", n, maxDeskLookups)
	}
}
