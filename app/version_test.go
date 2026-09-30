package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The AGPL asks every running instance to offer its source: the About page and
// the footer of every screen link to it, whether or not CI filled build-info.
func TestEveryScreenOffersTheSourceCode(t *testing.T) {
	a, h := testHandler(t)
	c := signedIn(t, a)
	for _, path := range []string{"/about", "/inventory"} {
		r := httptest.NewRequest("GET", path, nil)
		r.AddCookie(c)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("GET %s = %d", path, w.Code)
		}
		if !strings.Contains(w.Body.String(), `href="`+sourceURL+`"`) {
			t.Errorf("%s does not link to the source code", path)
		}
	}
}
