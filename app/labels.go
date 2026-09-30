package main

import (
	"database/sql"
	"log"
	"net/http"
	"strings"
	"unicode"
)

// Printable copy labels: barcode plus shelf mark. One sheet, reached by the
// codes just drawn (?codes=VOL204517,VOL811064) or by the inventory's filter.

// shelfMarkLength is how many letters a mark carries. Three is what school and
// public libraries print, and what fits on the spine of a picture book.
const shelfMarkLength = 3

// leadingArticles are dropped from the front of a title before the mark is
// taken, or a third of a French collection files under LE, LA and LES.
var leadingArticles = map[string]bool{
	"le": true, "la": true, "les": true, "l": true,
	"un": true, "une": true, "des": true,
	"du": true, "de": true, "d": true, "a": true, "au": true, "aux": true,
	"the": true, "an": true,
}

// shelfMark is where a book is filed: the first letters of its first author's
// surname, as school and public libraries file fiction, so an author's books
// stand together. A book with no author is filed by its title (titleMark).
func shelfMark(authors, title string) string {
	if m := markLetters(surname(authors)); m != "" {
		return m
	}
	return titleMark(title)
}

// titleMark is the first letters of a title, leading article dropped:
// "Le Petit Prince" is filed at PET, "L'Odyssée" at ODY. Empty for a title
// with no letter or digit; the label shows the code alone.
func titleMark(title string) string {
	words := titleWords(title)
	for i, w := range words {
		// An article is skipped only while something else follows: a book
		// actually called "Des" is filed at DES rather than nowhere.
		if leadingArticles[strings.ToLower(w)] && i < len(words)-1 {
			continue
		}
		return strings.ToUpper(firstRunes(w, shelfMarkLength))
	}
	return ""
}

// noAuthor are what catalogues write in the author field of a book nobody
// signed; such a book is filed by its title, as an anonymous work is.
var noAuthor = map[string]bool{
	"anonyme": true, "anonymous": true, "anoniem": true,
	"collectif": true, "collective": true,
}

// nameParticles belong to the surname when they precede it: "Van Zeveren"
// files at VAN, "La Fontaine" at LAF.
var nameParticles = map[string]bool{
	"de": true, "d": true, "du": true, "des": true, "la": true, "le": true,
	"van": true, "von": true, "der": true, "den": true, "ten": true, "ter": true,
	"da": true, "di": true, "del": true, "dos": true,
}

// surname is the first author's surname. Authors arrive both ways round: the
// BnF writes "Saint-Exupéry, Antoine de", Google Books and Open Library
// "Antoine de Saint-Exupéry". A lowercase "de" is dropped, as the French
// filing rule has it; a capitalised particle is part of the name.
func surname(authors string) string {
	first, _, _ := strings.Cut(authors, ";")
	first = strings.TrimSpace(first)
	if noAuthor[strings.ToLower(first)] {
		return ""
	}
	var words []string
	if last, _, ok := strings.Cut(first, ","); ok {
		words = nameWords(last)
	} else {
		words = nameWords(first)
		i := len(words) - 1
		for i > 0 && nameParticles[strings.ToLower(words[i-1])] {
			i--
		}
		words = words[max(i, 0):]
	}
	for len(words) > 1 && (words[0] == "de" || strings.EqualFold(words[0], "d")) {
		words = words[1:]
	}
	return strings.Join(words, " ")
}

// nameWords splits a name on spaces, with an elided "d'" as a word of its own.
// Hyphens stay: "Saint-Exupéry" is one surname, not a first name and a second.
func nameWords(name string) []string {
	var out []string
	for _, w := range strings.Fields(name) {
		if head, tail, ok := cutApostrophe(w); ok && strings.EqualFold(head, "d") && tail != "" {
			out = append(out, head, tail)
			continue
		}
		out = append(out, w)
	}
	return out
}

// markLetters is the first letters of a name in capitals, accents folded and
// everything that is not a letter or a digit dropped: "O'Brien" gives OBR.
func markLetters(s string) string {
	var b strings.Builder
	for _, r := range foldAccents(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(unicode.ToUpper(r))
		}
	}
	return firstRunes(b.String(), shelfMarkLength)
}

// titleWords splits a title into accent-free words. An elided article
// ("L'Odyssée") is a word of its own, so it is dropped; any other apostrophe
// just disappears: "C'est" gives CES, "aujourd'hui" AUJ.
func titleWords(title string) []string {
	var out []string
	for _, chunk := range strings.FieldsFunc(foldAccents(title), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && !isApostrophe(r)
	}) {
		head, tail, elided := cutApostrophe(chunk)
		switch {
		case !elided:
			out = append(out, chunk)
		case len([]rune(head)) == 1 && leadingArticles[strings.ToLower(head)]:
			// "L'Odyssée": the article stands alone and is dropped by the caller.
			out = append(out, head)
			out = append(out, strings.Join(strings.FieldsFunc(tail, isApostrophe), ""))
		default:
			// "C'est", "aujourd'hui": one word, the apostrophe removed.
			out = append(out, strings.Join(strings.FieldsFunc(chunk, isApostrophe), ""))
		}
	}
	return out
}

// Both the typewriter apostrophe and the typographic one: a title typed by hand
// carries the first, one copied from a catalogue record the second.
func isApostrophe(r rune) bool { return r == '\'' || r == '\u2019' }

func cutApostrophe(s string) (head, tail string, found bool) {
	for i, r := range s {
		if isApostrophe(r) {
			return s[:i], s[i+len(string(r)):], true
		}
	}
	return s, "", false
}

func firstRunes(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		r = r[:n]
	}
	return string(r)
}

// accentFolding maps diacritics onto their plain letter. A table rather than
// golang.org/x/text: one production dependency is the rule.
var accentFolding = map[rune]string{
	'à': "a", 'á': "a", 'â': "a", 'ä': "a", 'ã': "a", 'å': "a", 'æ': "ae",
	'ç': "c",
	'è': "e", 'é': "e", 'ê': "e", 'ë': "e",
	'ì': "i", 'í': "i", 'î': "i", 'ï': "i",
	'ñ': "n",
	'ò': "o", 'ó': "o", 'ô': "o", 'ö': "o", 'õ': "o", 'ø': "o", 'œ': "oe",
	'ù': "u", 'ú': "u", 'û': "u", 'ü': "u",
	'ý': "y", 'ÿ': "y",
	'ß': "ss",
}

func foldAccents(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		plain, ok := accentFolding[unicode.ToLower(r)]
		if !ok {
			b.WriteRune(r)
			continue
		}
		if unicode.IsUpper(r) {
			plain = strings.ToUpper(plain)
		}
		b.WriteString(plain)
	}
	return b.String()
}

// foldSearch is the form every search compares in, on both sides of a LIKE:
// SQLite's LOWER() and LIKE fold ASCII only, and "ecole" must find "École".
func foldSearch(s string) string {
	return foldAccents(strings.ToLower(s))
}

// labelRow is one label: what is printed on it, plus what the screen listing
// them shows.
type labelRow struct {
	BookID   int64
	Code     string
	Title    string
	Mark     string
	ISBN     string // empty for a book catalogued without one
	AddedOn  string // ISO date the copy was recorded
	Location string // where the copy sits, empty when it has not been filed
}

// titleTailLen is how many trailing characters a clipped title keeps visible:
// enough for "Tome 10", the only thing telling two volumes of a series apart,
// so a clipped title loses its middle, not its end.
const titleTailLen = 7

// titleHead and titleTail split a title where a fixed-width slot clips it: the
// head carries the ellipsis, the tail stays visible. Together they are the whole
// title. Template functions, since the screen lists rows that are not labelRows.
func titleHead(title string) string {
	r := []rune(title)
	if len(r) <= titleTailLen {
		return ""
	}
	return string(r[:len(r)-titleTailLen])
}

func titleTail(title string) string {
	r := []rune(title)
	if len(r) <= titleTailLen {
		return title
	}
	return string(r[len(r)-titleTailLen:])
}

// isISODate guards the two date bounds of the filter: a value that is not a
// date is dropped rather than queried with.
func isISODate(s string) bool {
	if len(s) != 10 {
		return false
	}
	_, ok := parseDate(s)
	return ok
}

// labelsForCopies turns the copies a filter selected into labels. The shelf
// mark is derived here, never stored: it is a function of the book.
func labelsForCopies(rows []InvRow) []labelRow {
	out := make([]labelRow, 0, len(rows))
	for _, c := range rows {
		out = append(out, labelRow{
			BookID:   c.BookID,
			Code:     c.Code,
			Title:    c.Title,
			Mark:     shelfMark(c.Authors, c.Title),
			ISBN:     c.ISBN,
			AddedOn:  c.AddedOn,
			Location: c.Location,
		})
	}
	return out
}

// labelsForCodes labels the codes the catalogue screen just created, in order.
// An unknown code still gets a label, title empty.
func (a *app) labelsForCodes(codes []string) []labelRow {
	var out []labelRow
	for _, code := range codes {
		code = strings.TrimSpace(code)
		if code == "" {
			continue
		}
		l := labelRow{Code: code}
		var authors string
		err := a.db.QueryRow(
			`SELECT b.id, b.title, COALESCE(b.authors, ''), COALESCE(b.isbn13, '')
			   FROM copy c JOIN book b ON b.id = c.book_id
			  WHERE c.code = ?`, code,
		).Scan(&l.BookID, &l.Title, &authors, &l.ISBN)
		switch err {
		case nil:
			l.Mark = shelfMark(authors, l.Title)
		case sql.ErrNoRows:
		default:
			log.Printf("labels (%s): %v", code, err)
		}
		out = append(out, l)
	}
	return out
}

// printLabels is the sheet itself: ?codes= for what has just been catalogued,
// the inventory's filter otherwise. Copies are chosen on /inventory alone.
func (a *app) printLabels(w http.ResponseWriter, r *http.Request) {
	if codes := r.URL.Query().Get("codes"); codes != "" {
		a.renderDoc(w, r, "labels_print", map[string]any{"Rows": a.labelsForCodes(strings.Split(codes, ","))})
		return
	}
	f := readInvFilter(r)
	// Sorted by entry, a sheet runs oldest first whichever way the screen
	// does: label 1 is the first book catalogued, as the pile has them.
	order := f
	chronological := f.Sort == "added"
	if chronological {
		order.Dir = "asc"
	}
	rows, err := a.listInventory(order)
	if err != nil {
		log.Printf("labels print: %v", err)
		internalError(w, r)
		return
	}
	a.renderDoc(w, r, "labels_print", map[string]any{
		"Rows": labelsForCopies(rows),
		// So the sheet can say what it was narrowed to, and link back to the screen.
		"Filter":        f,
		"Chronological": chronological,
	})
}
