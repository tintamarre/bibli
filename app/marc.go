package main

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
)

// Reading what the catalogues answer: UNIMARC from the BnF, MARC21 from
// UniCat. Apart from the network calls so it can be tested on real responses.
//
// Useful UNIMARC fields: 200$a title, 200$e subtitle, 700/701/702 $a$b authors,
// 214$c/210$c publisher, 214$d/210$d year, 101$a language, 856 the cover.
// MARC21: 245$a$b title, 100/700$a authors, 260/264$b publisher, 260/264$c
// year, 041$a language.

func recordFromUnimarc(body []byte, i13, i10 string) (*Record, error) {
	fields, err := parseUnimarc(body)
	if err != nil || len(fields) == 0 {
		return nil, err
	}

	n := &Record{ISBN13: i13, ISBN10: i10, Source: "bnf", Payload: string(body)}
	if f := firstField(fields, "200"); f != nil {
		n.Title = subfield(f, "a")
		n.Subtitle = subfield(f, "e")
	}
	// 214 (new standard) then 210 (old). The field is repeatable: a record often
	// carries a publication 214 and a manufacturing 214 with neither publisher
	// nor date, so the first one met is not necessarily the right one.
	for _, tag := range []string{"214", "210"} {
		if n.Publisher == "" {
			n.Publisher = subfieldAmong(fields, tag, "c")
		}
		if n.Year == 0 {
			n.Year = year4(subfieldAmong(fields, tag, "d"))
		}
	}
	if f := firstField(fields, "101"); f != nil {
		n.Language = lang2(subfield(f, "a"))
	}
	n.Authors = authorsUnimarc(fields)
	n.CoverURL = unimarcCover(fields)
	if ark := reArkBnF.FindString(string(body)); ark != "" {
		n.URL = "https://catalogue.bnf.fr/" + ark
	}
	return n, nil
}

var reArkBnF = regexp.MustCompile(`ark:/12148/cb[0-9a-z]+`)

var reImageID = regexp.MustCompile(`^[0-9]+$`)

// unimarcCover reads the thumbnail the BnF announces: field 856, $u an image
// identifier (not a URL), $b its label; the catalogue's cover service serves it.

func unimarcCover(fields []Datafield) string {
	for _, f := range fields {
		if f.Tag != "856" {
			continue
		}
		id := subfield(&f, "u")
		if !reImageID.MatchString(id) {
			continue
		}
		if !strings.Contains(strings.ToLower(subfield(&f, "b")), "couverture") {
			continue
		}
		return "https://catalogue.bnf.fr/cover?appName=NE&idImage=" + id + "&couverture=1"
	}
	return ""
}

// Datafield is a UNIMARC tag and its subfields, in order.

type Datafield struct {
	Tag  string
	Subs [][2]string // {code, value}
}

// parseUnimarc extracts the datafields of the first record, ignoring XML
// namespaces (only the local name is matched). A reply that is not a whole SRU
// or MARC document is an error, never an empty result: an HTML maintenance
// page or a body cut short would otherwise be remembered as "not held".

func parseUnimarc(data []byte) ([]Datafield, error) {
	dec := xml.NewDecoder(strings.NewReader(string(data)))
	var fields []Datafield
	var cur *Datafield
	var code string
	var val strings.Builder
	inSub := false
	root := ""
	diagnostic := false
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("unreadable reply: %w", err)
		}
		switch e := tok.(type) {
		case xml.StartElement:
			if root == "" {
				root = e.Name.Local
			}
			switch e.Name.Local {
			case "diagnostics":
				diagnostic = true
			case "datafield":
				cur = &Datafield{Tag: attribute(e, "tag")}
			case "subfield":
				code = attribute(e, "code")
				val.Reset()
				inSub = true
			}
		case xml.CharData:
			if inSub {
				val.Write([]byte(e))
			}
		case xml.EndElement:
			switch e.Name.Local {
			case "subfield":
				if cur != nil {
					cur.Subs = append(cur.Subs, [2]string{code, strings.TrimSpace(val.String())})
				}
				inSub = false
			case "datafield":
				if cur != nil {
					fields = append(fields, *cur)
					cur = nil
				}
			}
		}
	}
	switch root {
	case "searchRetrieveResponse", "record", "collection":
	case "":
		return nil, errors.New("unreadable reply: no XML document")
	default:
		return nil, fmt.Errorf("unreadable reply: <%s> is not an SRU response", root)
	}
	if diagnostic && len(fields) == 0 {
		return nil, errors.New("the catalogue answered with an SRU diagnostic")
	}
	return fields, nil
}

func attribute(e xml.StartElement, attrName string) string {
	for _, a := range e.Attr {
		if a.Name.Local == attrName {
			return a.Value
		}
	}
	return ""
}

func firstField(fields []Datafield, tag string) *Datafield {
	for i := range fields {
		if fields[i].Tag == tag {
			return &fields[i]
		}
	}
	return nil
}

// subfieldAmong returns the first non-empty subfield among ALL the fields
// carrying this tag. Needed for repeatable fields (see 214 above).

func subfieldAmong(fields []Datafield, tag, code string) string {
	for i := range fields {
		if fields[i].Tag != tag {
			continue
		}
		if v := subfield(&fields[i], code); v != "" {
			return v
		}
	}
	return ""
}

func subfield(f *Datafield, code string) string {
	for _, s := range f.Subs {
		if s[0] == code {
			return s[1]
		}
	}
	return ""
}

// authorsUnimarc assembles the authors from fields 700/701/702: $a (last name)
// plus $b (first name), separated by " ; ".

func authorsUnimarc(fields []Datafield) string {
	var lastNames []string
	for _, f := range fields {
		if f.Tag != "700" && f.Tag != "701" && f.Tag != "702" {
			continue
		}
		lastName := subfield(&f, "a")
		firstName := subfield(&f, "b")
		switch {
		case lastName != "" && firstName != "":
			lastNames = append(lastNames, lastName+", "+firstName)
		case lastName != "":
			lastNames = append(lastNames, lastName)
		}
	}
	return strings.Join(lastNames, " ; ")
}

// ---------------------------------------------------------------------------
// UniCat — the Belgian union catalogue (SRU, MARC21, no key).
// Covers Belgian editions (Racine and others) often absent from the BnF.
// ---------------------------------------------------------------------------

func recordFromMarc21(body []byte, i13, i10 string) (*Record, error) {
	// Same datafield/subfield structure as the BnF (MARC21 here, not UNIMARC).
	fields, err := parseUnimarc(body)
	if err != nil || len(fields) == 0 {
		return nil, err
	}
	n := &Record{ISBN13: i13, ISBN10: i10, Source: "unicat", Payload: string(body)}
	if f := firstField(fields, "245"); f != nil {
		n.Title = cleanMarc(subfield(f, "a"))
		n.Subtitle = cleanMarc(subfield(f, "b"))
	}
	n.Authors = authorsMarc21(fields)
	// Publisher and year: 264 (RDA) then 260 (old).
	for _, tag := range []string{"264", "260"} {
		if f := firstField(fields, tag); f != nil {
			if n.Publisher == "" {
				pub := cleanMarc(subfield(f, "b"))
				// MARC 260$b often holds "Place: Publisher": keep the publisher.
				if i := strings.LastIndex(pub, ": "); i >= 0 {
					pub = strings.TrimSpace(pub[i+2:])
				}
				n.Publisher = pub
			}
			if n.Year == 0 {
				n.Year = year4(subfield(f, "c"))
			}
		}
	}
	if f := firstField(fields, "041"); f != nil {
		n.Language = lang2(subfield(f, "a"))
	}
	return n, nil
}

// cleanMarc removes the trailing ISBD punctuation of a MARC subfield (" : / ; ,").

func cleanMarc(s string) string {
	return strings.TrimRight(strings.TrimSpace(s), " /:;,.")
}

// authorsMarc21 assembles MARC21 authors: 100$a (main) plus 700$a (added).

func authorsMarc21(fields []Datafield) string {
	var lastNames []string
	for _, f := range fields {
		if f.Tag != "100" && f.Tag != "700" {
			continue
		}
		if lastName := cleanMarc(subfield(&f, "a")); lastName != "" {
			lastNames = append(lastNames, lastName)
		}
	}
	return strings.Join(lastNames, " ; ")
}

// Google Books — good recall on non-French titles. Limited quota.

var reYear = regexp.MustCompile(`\d{4}`)

func year4(s string) int {
	n, _ := strconv.Atoi(reYear.FindString(s))
	return n
}

func lang2(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "fre", "fra", "fr":
		return "fr"
	case "dut", "nld", "nl":
		return "nl"
	case "eng", "en":
		return "en"
	case "ger", "deu", "de":
		return "de"
	}
	if len(s) >= 2 {
		return s[:2]
	}
	return s
}

// Data of the editable confirmation screen.
