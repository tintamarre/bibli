package main

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Interface strings live in locales/, one flat JSON file per language.
// Placeholders are positional ({0}, {1}) so a sentence can be reordered.
// fr.json is the reference; a key missing elsewhere falls back to it.

//go:embed locales/*.json
var localesFS embed.FS

const defaultLang = "fr"

const langCookie = "bibli_lang"

// langs is also the order the settings screen offers. A new language also
// needs langName, a date layout (dates.go) and byte units (settings.go).
var langs = []string{"fr", "en", "nl"}

// Each language is named in itself.
var langName = map[string]string{"fr": "Français", "en": "English", "nl": "Nederlands"}

// language -> key -> string. Written once at startup, so it needs no lock.
var catalogues map[string]map[string]string

// loadLocales runs at startup, so a malformed file stops the binary rather
// than blanking a screen.
func loadLocales() error {
	catalogues = make(map[string]map[string]string, len(langs))
	for _, l := range langs {
		raw, err := localesFS.ReadFile("locales/" + l + ".json")
		if err != nil {
			return fmt.Errorf("locales/%s.json: %w", l, err)
		}
		var c map[string]string
		if err := json.Unmarshal(raw, &c); err != nil {
			return fmt.Errorf("locales/%s.json: %w", l, err)
		}
		catalogues[l] = c
	}
	return nil
}

// T renders the string filed under key, with {0}, {1}... replaced by args.
func T(lang, key string, args ...any) string {
	return substitute(lookup(lang, key), args)
}

// Tn looks up "<key>.one" or "<key>.other" and passes n as {0}.
func Tn(lang, key string, n int, args ...any) string {
	form := lookup(lang, key+"."+pluralForm(lang, n))
	return substitute(form, append([]any{n}, args...))
}

// Falls back to French, then to the key itself, so the omission gets noticed.
func lookup(lang, key string) string {
	if s, ok := catalogues[lang][key]; ok {
		return s
	}
	reportMissingKey(lang, key)
	if s, ok := catalogues[defaultLang][key]; ok {
		return s
	}
	return key
}

// A missing key is logged once.
var reportedKeys sync.Map

func reportMissingKey(lang, key string) {
	if _, already := reportedKeys.LoadOrStore(lang+"/"+key, true); !already {
		log.Printf("i18n: key missing from %s: %s", lang, key)
	}
}

// French keeps the singular at zero ("0 jour"); English and Dutch do not.
func pluralForm(lang string, n int) string {
	if n < 0 {
		n = -n
	}
	switch lang {
	case "fr":
		if n <= 1 {
			return "one"
		}
	default:
		if n == 1 {
			return "one"
		}
	}
	return "other"
}

// One pass, so an argument holding "{1}" is not substituted in turn.
func substitute(s string, args []any) string {
	if len(args) == 0 {
		return s
	}
	pairs := make([]string, 0, 2*len(args))
	for i, a := range args {
		pairs = append(pairs, "{"+strconv.Itoa(i)+"}", fmt.Sprint(a))
	}
	return strings.NewReplacer(pairs...).Replace(s)
}

// tr is T for a handler, in the request's language.
func tr(r *http.Request, key string, args ...any) string {
	return T(requestLang(r), key, args...)
}

func trn(r *http.Request, key string, n int, args ...any) string {
	return Tn(requestLang(r), key, n, args...)
}

func knownLang(l string) bool {
	for _, c := range langs {
		if c == l {
			return true
		}
	}
	return false
}

// Where rememberLang files the language it resolved for the request.
type langKey struct{}

// requestLang is the instance setting, unless ?lang=xx overrode it for this
// browser; ?lang=auto clears the override.
func requestLang(r *http.Request) string {
	if l, ok := r.Context().Value(langKey{}).(string); ok {
		return l
	}
	return resolveLang(r)
}

func resolveLang(r *http.Request) string {
	if l := r.URL.Query().Get("lang"); knownLang(l) {
		return l
	}
	if r.URL.Query().Get("lang") == "auto" {
		return instanceLang()
	}
	if c, err := r.Cookie(langCookie); err == nil && knownLang(c.Value) {
		return c.Value
	}
	return instanceLang()
}

// rememberLang resolves the language once per request, and turns a ?lang=
// override into a cookie so the next click keeps it.
func (a *app) rememberLang(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch l := r.URL.Query().Get("lang"); {
		case knownLang(l):
			a.setLangCookie(w, l, 12*time.Hour)
		case l == "auto":
			a.setLangCookie(w, "", -time.Second)
		}
		ctx := context.WithValue(r.Context(), langKey{}, resolveLang(r))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (a *app) setLangCookie(w http.ResponseWriter, value string, maxAge time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name:     langCookie,
		Value:    value,
		Path:     "/",
		MaxAge:   int(maxAge / time.Second),
		HttpOnly: true,
		Secure:   a.secureCookies,
		SameSite: http.SameSiteLaxMode,
	})
}
