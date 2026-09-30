package main

import (
	"bytes"
	"encoding/json"
	"html/template"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
	"text/template/parse"
)

func loadForTest(t *testing.T) {
	t.Helper()
	if err := loadLocales(); err != nil {
		t.Fatalf("loading the locales: %v", err)
	}
}

// Guard rail 1 — the catalogues hold exactly the same keys, compared both ways.
func TestCataloguesShareTheSameKeys(t *testing.T) {
	loadForTest(t)

	ref := catalogues[defaultLang]
	if len(ref) == 0 {
		t.Fatalf("locales/%s.json is empty", defaultLang)
	}

	for _, lang := range langs {
		if lang == defaultLang {
			continue
		}
		for key := range ref {
			if _, ok := catalogues[lang][key]; !ok {
				t.Errorf("%s.json: missing key %q", lang, key)
			}
		}
		for key := range catalogues[lang] {
			if _, ok := ref[key]; !ok {
				t.Errorf("%s.json: key %q is absent from %s.json (leftover to clean up?)", lang, key, defaultLang)
			}
		}
	}
}

// Plural forms come in pairs, or the string empties when the count flips.
func TestPluralsComeInPairs(t *testing.T) {
	loadForTest(t)

	for lang, cat := range catalogues {
		for key := range cat {
			var twin string
			switch {
			case strings.HasSuffix(key, ".one"):
				twin = strings.TrimSuffix(key, ".one") + ".other"
			case strings.HasSuffix(key, ".other"):
				twin = strings.TrimSuffix(key, ".other") + ".one"
			default:
				continue
			}
			if _, ok := cat[twin]; !ok {
				t.Errorf("%s.json: %q without %q", lang, key, twin)
			}
		}
	}
}

// {0} in one language and not in another signals a sentence translated wrongly.
func TestSameSubstitutions(t *testing.T) {
	loadForTest(t)

	marker := regexp.MustCompile(`\{\d+\}`)
	fingerprint := func(s string) string {
		m := marker.FindAllString(s, -1)
		sort.Strings(m)
		return strings.Join(m, " ")
	}

	for key, ref := range catalogues[defaultLang] {
		for _, lang := range langs {
			if lang == defaultLang {
				continue
			}
			translation, ok := catalogues[lang][key]
			if !ok {
				continue // already reported by TestCataloguesShareTheSameKeys
			}
			if a, b := fingerprint(ref), fingerprint(translation); a != b {
				t.Errorf("%s: substitutions %q in %s, %q in %s", key, a, defaultLang, b, lang)
			}
		}
	}
}

// Guard rail — the emphasis marks balance, the same number in every catalogue:
// a stray "*" bolds the rest of the question in one language and fails nowhere.
func TestEmphasisMarksBalance(t *testing.T) {
	loadForTest(t)

	for lang, cat := range catalogues {
		for key, s := range cat {
			if n := strings.Count(s, "*"); n%2 != 0 {
				t.Errorf("%s (%s): %d emphasis mark(s) — they come in pairs: %q",
					key, lang, n, s)
			}
		}
	}

	for key, ref := range catalogues[defaultLang] {
		for _, lang := range langs {
			if lang == defaultLang {
				continue
			}
			translation, ok := catalogues[lang][key]
			if !ok {
				continue // already reported by TestCataloguesShareTheSameKeys
			}
			if a, b := strings.Count(ref, "*"), strings.Count(translation, "*"); a != b {
				t.Errorf("%s: %d emphasis mark(s) in %s, %d in %s — a question that "+
					"points at the title in one language must point at it in the other",
					key, a, defaultLang, b, lang)
			}
		}
	}
}

// Guard rail 2 — no hard-coded French in the templates, and no exceptions.
var (
	// A {{...}} action can legitimately hold French: a comment, a key.
	reAction = regexp.MustCompile(`(?s)\{\{.*?\}\}`)
	// Script and style bodies are not displayed text.
	reScript = regexp.MustCompile(`(?is)<(script|style)\b[^>]*>.*?</(script|style)>`)
	// The attributes a user reads.
	reAttrTrad = regexp.MustCompile(`(?i)\b(placeholder|title|aria-label|alt|hx-confirm|data-confirm)\s*=\s*"([^"]*)"`)
	// The rest of the markup.
	reBalise = regexp.MustCompile(`(?s)<[^>]*>`)

	reAccent = regexp.MustCompile(`[àâäçéèêëîïôöùûüÀÂÄÇÉÈÊËÎÏÔÖÙÛÜœŒ]`)
	// French without accents ("Mot de passe"). ASCII only, so \b behaves.
	reMotFR = regexp.MustCompile(`(?i)\b(le|la|les|un|une|des|du|de|au|aux|et|ou|par|pour|avec|sans|sur|dans|vers|est|sont|ont|qui|que|pas|plus|ce|cet|cette|ces|son|sa|ses|leur|votre|vos|notre|nos|tout|tous|toute|toutes|aucun|aucune|puis|mot|passe|livre|livres|eleve|eleves|classe|prenom|nom|date|jour|jours|an|ans)\b`)
)

// What a user actually reads: text outside markup, plus translatable attributes.
func visibleText(tpl string) string {
	s := reAction.ReplaceAllString(tpl, " ")
	s = reScript.ReplaceAllString(s, " ")

	var b strings.Builder
	for _, m := range reAttrTrad.FindAllStringSubmatch(s, -1) {
		b.WriteString(m[2])
		b.WriteByte('\n')
	}
	b.WriteString(reBalise.ReplaceAllString(s, " "))
	return b.String()
}

func TestTemplatesHaveNoHardCodedText(t *testing.T) {
	files, err := filepath.Glob("templates/*.html")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no template found")
	}

	for _, path := range files {
		name := filepath.Base(path)
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		text := visibleText(string(content))
		if reAccent.MatchString(text) || reMotFR.MatchString(text) {
			t.Errorf("%s: hard-coded French text — go through {{T}}", name)
		}
	}
}

// Detection catches French without accents, and ignores French class names.
func TestVisibleText(t *testing.T) {
	cases := []struct {
		name   string
		tpl    string
		french bool
	}{
		{"unaccented text", `<span>Mot de passe</span>`, true},
		{"accented text", `<p>Déjà enregistré</p>`, true},
		{"translatable attribute", `<input placeholder="Nom de l'eleve">`, true},
		{"translation key alone", `<span>{{T "login.password"}}</span>`, false},
		{"French class names", `<div class="menu-admin-panneau"><a href="/borrowers"></a></div>`, false},
		{"script", `<script>const livre = 1;</script>`, false},
		{"English", `<span>Password</span><button>Sign in</button>`, false},
	}
	for _, c := range cases {
		text := visibleText(c.tpl)
		got := reAccent.MatchString(text) || reMotFR.MatchString(text)
		if got != c.french {
			t.Errorf("%s: detected=%v, want %v (visible text: %q)", c.name, got, c.french, text)
		}
	}
}

func TestPositionalSubstitution(t *testing.T) {
	cases := []struct {
		pattern string
		args    []any
		want    string
	}{
		{"dans {0}", []any{"3 semaines"}, "dans 3 semaines"},
		{"{1} ({0})", []any{"P4", "Léa"}, "Léa (P4)"},
		{"nothing to replace", nil, "nothing to replace"},
		// An argument that itself holds a marker must not be substituted in turn.
		{"{0} puis {1}", []any{"{1}", "fin"}, "{1} puis fin"},
	}
	for _, c := range cases {
		if got := substitute(c.pattern, c.args); got != c.want {
			t.Errorf("substitute(%q, %v) = %q, want %q", c.pattern, c.args, got, c.want)
		}
	}
}

func TestPluralForm(t *testing.T) {
	// French keeps the singular at zero, English does not.
	cases := []struct {
		lang string
		n    int
		want string
	}{
		{"fr", 0, "one"}, {"fr", 1, "one"}, {"fr", -1, "one"}, {"fr", 2, "other"},
		{"en", 0, "other"}, {"en", 1, "one"}, {"en", -1, "one"}, {"en", 2, "other"},
	}
	for _, c := range cases {
		if got := pluralForm(c.lang, c.n); got != c.want {
			t.Errorf("pluralForm(%q, %d) = %q, want %q", c.lang, c.n, got, c.want)
		}
	}
}

func TestUnknownKey(t *testing.T) {
	loadForTest(t)
	if got := T("fr", "aucune.key.de.ce.name"); got != "aucune.key.de.ce.name" {
		t.Errorf("unknown key = %q, want the key itself", got)
	}
}

// Guard rail 3 — template function calls match their signatures. html/template
// resolves calls at render time, so the parse tree is read and every call
// compared to the real signature by reflection.
func TestTemplateArity(t *testing.T) {
	loadForTest(t)
	sets, err := loadTemplates()
	if err != nil {
		t.Fatalf("loading the templates: %v", err)
	}

	fns := templateFuncs(defaultLang)
	seen := make(map[string]bool) // one fault per function is enough to report it

	for page, tpl := range sets[defaultLang] {
		for _, block := range tpl.Templates() {
			if block.Tree == nil || block.Tree.Root == nil {
				continue
			}
			walkNodes(block.Tree.Root, func(pipe *parse.PipeNode) {
				for i, cmd := range pipe.Cmds {
					checkCall(t, fns, seen, page, block.Name(), cmd, i > 0)
				}
			})
		}
	}
}

// A command whose first word is an identifier is a call. In a pipeline, the
// incoming value counts as one more argument.
func checkCall(t *testing.T, fns template.FuncMap, seen map[string]bool,
	page, block string, cmd *parse.CommandNode, pipe bool) {
	t.Helper()
	if len(cmd.Args) == 0 {
		return
	}
	id, ok := cmd.Args[0].(*parse.IdentifierNode)
	if !ok {
		return
	}
	fn, ok := fns[id.Ident]
	if !ok {
		return // a function built into the engine (index, len, printf...)
	}
	typ := reflect.TypeOf(fn)
	if typ.Kind() != reflect.Func {
		return
	}

	given := len(cmd.Args) - 1
	if pipe {
		given++
	}
	wanted := typ.NumIn()

	tooMany := given > wanted && !typ.IsVariadic()
	tooFew := given < wanted
	if typ.IsVariadic() {
		tooFew = given < wanted-1
	}
	if !tooMany && !tooFew {
		return
	}
	key := page + "/" + id.Ident
	if seen[key] {
		return
	}
	seen[key] = true
	t.Errorf("%s (%s): %s called with %d argument(s), the function takes %d",
		page, block, id.Ident, given, wanted)
}

// text/template/parse has no generic walk: a node type left out here is a blind
// spot.
func walkNodes(n parse.Node, f func(*parse.PipeNode)) {
	switch n := n.(type) {
	case nil:
		return
	case *parse.ListNode:
		if n == nil {
			return
		}
		for _, child := range n.Nodes {
			walkNodes(child, f)
		}
	case *parse.ActionNode:
		walkNodes(n.Pipe, f)
	case *parse.PipeNode:
		if n == nil {
			return
		}
		f(n)
		for _, cmd := range n.Cmds {
			for _, arg := range cmd.Args {
				if sub, ok := arg.(*parse.PipeNode); ok {
					walkNodes(sub, f)
				}
			}
		}
	case *parse.IfNode:
		walkBranch(&n.BranchNode, f)
	case *parse.RangeNode:
		walkBranch(&n.BranchNode, f)
	case *parse.WithNode:
		walkBranch(&n.BranchNode, f)
	case *parse.TemplateNode:
		walkNodes(n.Pipe, f)
	}
}

func walkBranch(b *parse.BranchNode, f func(*parse.PipeNode)) {
	walkNodes(b.Pipe, f)
	walkNodes(b.List, f)
	walkNodes(b.ElseList, f)
}

// Guard rail 4 — jsKeys matches exactly what static/*.js asks for.
func TestJSKeys(t *testing.T) {
	loadForTest(t)

	// Tn looks up "<key>.one" and "<key>.other", so both are counted.
	callRe := regexp.MustCompile(`\b(Tn?)\("([a-z0-9_.]+)"`)

	asked := map[string]bool{}
	files, err := filepath.Glob("static/*.js")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no .js file found")
	}
	for _, path := range files {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range callRe.FindAllStringSubmatch(string(content), -1) {
			if m[1] == "Tn" {
				asked[m[2]+".one"] = true
				asked[m[2]+".other"] = true
			} else {
				asked[m[2]] = true
			}
		}
	}

	listed := map[string]bool{}
	for _, key := range jsKeys {
		listed[key] = true
		if _, ok := catalogues[defaultLang][key]; !ok {
			t.Errorf("jsKeys lists %q, which is absent from %s.json", key, defaultLang)
		}
	}

	for key := range asked {
		if !listed[key] {
			t.Errorf("the JavaScript asks for %q, absent from jsKeys — it will show as is", key)
		}
	}
	for key := range listed {
		if !asked[key] {
			t.Errorf("jsKeys lists %q, which no .js asks for — dead weight on every page", key)
		}
	}
}

// Guard rail 5 — routes stay English.

// Checked in main.go, the templates and the JavaScript.
var frenchSegments = []string{
	"emprunteur", "emprunteurs", "emprunter", "prets", "pret", "retour",
	"rendre", "cataloguer", "inventaire", "reglages", "livre", "exemplaire",
	"couverture", "famille", "imprimer", "connexion", "deconnexion",
	"a-propos", "retards", "jeton", "revoquer", "editer", "desactiver",
	"reactiver", "importer", "valider", "cartes", "rentree", "ajouter",
	"rechercher", "enregistrer", "manuel", "etiquettes", "enrichir",
	"remettre",
}

func TestRoutesAreEnglish(t *testing.T) {
	// Tolerates the method before the path in mux.HandleFunc("GET /settings", ...).
	pathRe := regexp.MustCompile(`["'](?:(?:GET|POST|PUT|PATCH|DELETE|HEAD) )?(/[a-zA-Z0-9{}$._/-]*)`)

	files := []string{"main.go"}
	for _, pattern := range []string{"templates/*.html", "static/*.js"} {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, matches...)
	}

	forbidden := make(map[string]bool, len(frenchSegments))
	for _, s := range frenchSegments {
		forbidden[s] = true
	}

	for _, f := range files {
		content, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range pathRe.FindAllStringSubmatch(string(content), -1) {
			for _, seg := range strings.Split(strings.TrimPrefix(m[1], "/"), "/") {
				if forbidden[seg] {
					t.Errorf("%s: the path %q keeps the French segment %q", f, m[1], seg)
				}
			}
		}
	}
}

// One language per instance, ?lang= overriding it for a single browser.
func TestResolveLang(t *testing.T) {
	loadForTest(t)
	restoreSettings(t)
	setLang("fr")

	// No hint at all: the instance setting decides.
	if got := resolveLang(httptest.NewRequest("GET", "/loans", nil)); got != "fr" {
		t.Errorf("bare request: %q, want fr", got)
	}
	// ?lang= wins.
	if got := resolveLang(httptest.NewRequest("GET", "/loans?lang=en", nil)); got != "en" {
		t.Errorf("?lang=en: %q, want en", got)
	}
	// An unknown code changes nothing rather than blanking the screen.
	if got := resolveLang(httptest.NewRequest("GET", "/loans?lang=kl", nil)); got != "fr" {
		t.Errorf("?lang=kl: %q, want fr", got)
	}
	// The cookie carries the override to the next click.
	r := httptest.NewRequest("GET", "/loans", nil)
	r.AddCookie(&http.Cookie{Name: langCookie, Value: "en"})
	if got := resolveLang(r); got != "en" {
		t.Errorf("cookie: %q, want en", got)
	}
	// ?lang=auto goes back to the instance setting, cookie or no cookie.
	r = httptest.NewRequest("GET", "/loans?lang=auto", nil)
	r.AddCookie(&http.Cookie{Name: langCookie, Value: "en"})
	if got := resolveLang(r); got != "fr" {
		t.Errorf("?lang=auto: %q, want fr (the instance setting)", got)
	}
	// A garbled cookie is no better than none.
	r = httptest.NewRequest("GET", "/loans", nil)
	r.AddCookie(&http.Cookie{Name: langCookie, Value: "zz"})
	if got := resolveLang(r); got != "fr" {
		t.Errorf("unknown cookie: %q, want fr", got)
	}
}

// rememberLang resolves the language once and files it on the context.
func TestRequestLangUsesWhatWasResolvedOnce(t *testing.T) {
	loadForTest(t)
	restoreSettings(t)
	setLang("fr")

	a := &app{secureCookies: true}
	var seen string
	var cookie *http.Cookie
	h := a.rememberLang(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = requestLang(r)
	}))

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/loans?lang=en", nil))
	if seen != "en" {
		t.Errorf("resolved language: %q, want en", seen)
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == langCookie {
			cookie = c
		}
	}
	if cookie == nil || cookie.Value != "en" {
		t.Fatalf("the ?lang= override was not turned into a cookie: %+v", cookie)
	}

	// ?lang=auto expires it, back to the instance setting.
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/loans?lang=auto", nil))
	if seen != "fr" {
		t.Errorf("?lang=auto resolved to %q, want fr", seen)
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == langCookie && c.MaxAge >= 0 {
			t.Errorf("?lang=auto left the cookie in place: MaxAge %d", c.MaxAge)
		}
	}
}

func TestKnownLang(t *testing.T) {
	for _, l := range langs {
		if !knownLang(l) {
			t.Errorf("%s is offered but not recognised", l)
		}
	}
	for _, l := range []string{"", "kl", "FR", "fr-BE", "auto"} {
		if knownLang(l) {
			t.Errorf("%q taken for a language we serve", l)
		}
	}
}

// tr and trn read the language off the request.
func TestTrFollowsTheRequest(t *testing.T) {
	loadForTest(t)
	restoreSettings(t)
	setLang("fr")

	fr := httptest.NewRequest("GET", "/loans", nil)
	en := httptest.NewRequest("GET", "/loans?lang=en", nil)
	if tr(fr, "nav.borrowers") == tr(en, "nav.borrowers") {
		t.Error("tr returns the same string in both languages")
	}
	// French keeps the singular at zero, English does not.
	if trn(fr, "duration.days", 0) == trn(en, "duration.days", 0) {
		t.Error("trn returns the same string in both languages")
	}
}

// A key written twice in one catalogue: the JSON parser silently keeps the last.
func TestNoDuplicateKeys(t *testing.T) {
	for _, lang := range langs {
		raw, err := os.ReadFile("locales/" + lang + ".json")
		if err != nil {
			t.Fatal(err)
		}
		dec := json.NewDecoder(bytes.NewReader(raw))
		if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
			t.Fatalf("%s.json: not an object", lang)
		}
		seen := map[string]bool{}
		for dec.More() {
			tok, err := dec.Token()
			if err != nil {
				t.Fatalf("%s.json: %v", lang, err)
			}
			key := tok.(string)
			if seen[key] {
				t.Errorf("%s.json: key %q is written twice", lang, key)
			}
			seen[key] = true
			var v json.RawMessage
			if err := dec.Decode(&v); err != nil {
				t.Fatalf("%s.json: %v", lang, err)
			}
		}
	}
}

// Guard rail 9 — every key the Go code looks up exists; a missing one reaches
// the screen as the raw key.
func TestEveryKeyNamedInGoExists(t *testing.T) {
	loadForTest(t)
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	// The key is the argument after the language or request, and must be a literal.
	call := regexp.MustCompile(`\b(?:T|Tn)\((?:lang|defaultLang|"[a-z]{2}"), "([^"]+)"|\b(?:tr|trn)\(r, "([^"]+)"`)
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range call.FindAllStringSubmatch(string(src), -1) {
			key := m[1] + m[2]
			// A literal ending in a dot is a prefix ("source." + source), checked where
			// its values are declared.
			if strings.HasSuffix(key, ".") {
				continue
			}
			// Tn asks for a plural form, so either half proves the key.
			if _, ok := catalogues[defaultLang][key]; ok {
				continue
			}
			if _, ok := catalogues[defaultLang][key+".one"]; ok {
				continue
			}
			t.Errorf("%s names %q, which is in no catalogue", f, key)
		}
	}
}
