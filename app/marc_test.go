package main

import "testing"

// A real extract of a BnF SRU response (unimarcxchange) for Le Petit Prince,
// 9782070408504. The mxc: namespaces are kept: that is precisely what the
// parser has to ignore.
const bnfResponse = `<?xml version="1.0" encoding="UTF-8"?>
<srw:searchRetrieveResponse xmlns:srw="http://www.loc.gov/zing/srw/">
 <srw:records><srw:record>
  <srw:recordData>
   <mxc:record xmlns:mxc="info:lc/xmlns/marcxchange-v2">
    <mxc:controlfield tag="001">ark:/12148/cb37007958t</mxc:controlfield>
    <mxc:datafield tag="101" ind1="0" ind2=" ">
     <mxc:subfield code="a">fre</mxc:subfield>
    </mxc:datafield>
    <mxc:datafield tag="200" ind1="1" ind2=" ">
     <mxc:subfield code="a">Le petit prince</mxc:subfield>
     <mxc:subfield code="e">avec des aquarelles de l'auteur</mxc:subfield>
     <mxc:subfield code="f">Antoine de Saint-Exupéry</mxc:subfield>
    </mxc:datafield>
    <mxc:datafield tag="210" ind1=" " ind2=" ">
     <mxc:subfield code="a">[Paris]</mxc:subfield>
     <mxc:subfield code="c">Gallimard</mxc:subfield>
     <mxc:subfield code="d">1999</mxc:subfield>
    </mxc:datafield>
    <mxc:datafield tag="700" ind1=" " ind2="|">
     <mxc:subfield code="3">11923342</mxc:subfield>
     <mxc:subfield code="a">Saint-Exupéry</mxc:subfield>
     <mxc:subfield code="b">Antoine de</mxc:subfield>
     <mxc:subfield code="f">1900-1944</mxc:subfield>
    </mxc:datafield>
    <mxc:datafield tag="701" ind1=" " ind2="|">
     <mxc:subfield code="a">Dupont</mxc:subfield>
    </mxc:datafield>
   </mxc:record>
  </srw:recordData>
 </srw:record></srw:records>
</srw:searchRetrieveResponse>`

func TestRecordFromUnimarc(t *testing.T) {
	n := parsed(t, recordFromUnimarc, bnfResponse, "9782070408504", "2070408507")
	if n == nil {
		t.Fatal("nil record for a valid BnF response")
	}
	cases := []struct{ field, got, want string }{
		{"title", n.Title, "Le petit prince"},
		{"subtitle", n.Subtitle, "avec des aquarelles de l'auteur"},
		{"publisher", n.Publisher, "Gallimard"},
		{"language", n.Language, "fr"},
		{"source", n.Source, "bnf"},
		// 700 $a + $b, then 701 $a alone, separated by " ; "
		{"authors", n.Authors, "Saint-Exupéry, Antoine de ; Dupont"},
		{"record URL", n.URL, "https://catalogue.bnf.fr/ark:/12148/cb37007958t"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s: %q, want %q", c.field, c.got, c.want)
		}
	}
	if n.Year != 1999 {
		t.Errorf("year: %d, want 1999", n.Year)
	}
	if n.Payload == "" {
		t.Error("empty payload: the raw response must be kept")
	}
}

// Publisher and year come from 214 (the recent standard) before 210.
func TestRecordFromUnimarcPrefers214(t *testing.T) {
	const x = `<record>
	 <datafield tag="200"><subfield code="a">Title</subfield></datafield>
	 <datafield tag="214"><subfield code="c">Éditeur récent</subfield><subfield code="d">DL 2021</subfield></datafield>
	 <datafield tag="210"><subfield code="c">Éditeur ancien</subfield><subfield code="d">1990</subfield></datafield>
	</record>`
	n := parsed(t, recordFromUnimarc, x, "9782070408504", "")
	if n == nil {
		t.Fatal("nil record")
	}
	if n.Publisher != "Éditeur récent" {
		t.Errorf("publisher: %q, want the one from field 214", n.Publisher)
	}
	if n.Year != 2021 {
		t.Errorf("year: %d, want 2021 (extracted from \"DL 2021\")", n.Year)
	}
}

func TestRecordFromUnimarcEmpty(t *testing.T) {
	n, err := recordFromUnimarc([]byte(`<searchRetrieveResponse><numberOfRecords>0</numberOfRecords></searchRetrieveResponse>`),
		"9782070408504", "")
	if n != nil || err != nil {
		t.Errorf("response with no record: want nil, nil, got %+v, %v", n, err)
	}
}

// Only an answer is an absence. What cannot be read — a maintenance page
// served with a 200, a body cut at the read limit — is a fault.
func TestAnUnreadableReplyIsAnError(t *testing.T) {
	for name, body := range map[string]string{
		"plain text":     "not xml at all",
		"html":           `<!DOCTYPE html><html><head><meta charset="utf-8"><title>Maintenance</title></head><body><p>Le catalogue est en maintenance</p></body></html>`,
		"xhtml":          `<html><body><p>Service indisponible</p></body></html>`,
		"cut short":      bnfResponse[:len(bnfResponse)/2],
		"sru diagnostic": `<searchRetrieveResponse><diagnostics><diagnostic><message>Query syntax error</message></diagnostic></diagnostics></searchRetrieveResponse>`,
	} {
		if n, err := recordFromUnimarc([]byte(body), "9782070408504", ""); err == nil {
			t.Errorf("%s (UNIMARC): read as %v with no error", name, n != nil)
		}
		if n, err := recordFromMarc21([]byte(body), "9782070408504", ""); err == nil {
			t.Errorf("%s (MARC21): read as %v with no error", name, n != nil)
		}
	}
}

func parsed(t *testing.T, parse func([]byte, string, string) (*Record, error), body, i13, i10 string) *Record {
	t.Helper()
	n, err := parse([]byte(body), i13, i10)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return n
}

// UniCat answers MARC21: different fields and ISBD punctuation.
const unicatResponse = `<searchRetrieveResponse>
 <records><record><recordData>
  <record xmlns="http://www.loc.gov/MARC21/slim">
   <datafield tag="041" ind1="0" ind2=" "><subfield code="a">dut</subfield></datafield>
   <datafield tag="100" ind1="1" ind2=" "><subfield code="a">Pennart, Geoffroy de,</subfield></datafield>
   <datafield tag="245" ind1="1" ind2="3">
    <subfield code="a">Le loup est revenu ! /</subfield>
    <subfield code="b">Geoffroy de Pennart :</subfield>
   </datafield>
   <datafield tag="260" ind1=" " ind2=" ">
    <subfield code="a">Bruxelles :</subfield>
    <subfield code="b">Bruxelles: Racine,</subfield>
    <subfield code="c">c1994.</subfield>
   </datafield>
   <datafield tag="700" ind1="1" ind2=" "><subfield code="a">Dubois, Claude,</subfield></datafield>
  </record>
 </recordData></record></records>
</searchRetrieveResponse>`

func TestRecordFromMarc21(t *testing.T) {
	n := parsed(t, recordFromMarc21, unicatResponse, "9782211037495", "2211037496")
	if n == nil {
		t.Fatal("nil record for a valid UniCat response")
	}
	cases := []struct{ field, got, want string }{
		// The trailing ISBD punctuation of a subfield is removed.
		{"title", n.Title, "Le loup est revenu !"},
		{"subtitle", n.Subtitle, "Geoffroy de Pennart"},
		// 260$b often holds "Place: Publisher": the publisher is kept.
		{"publisher", n.Publisher, "Racine"},
		{"language", n.Language, "nl"},
		{"source", n.Source, "unicat"},
		{"authors", n.Authors, "Pennart, Geoffroy de ; Dubois, Claude"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s: %q, want %q", c.field, c.got, c.want)
		}
	}
	if n.Year != 1994 {
		t.Errorf("year: %d, want 1994 (extracted from \"c1994.\")", n.Year)
	}
}

func TestCleanMarc(t *testing.T) {
	cases := map[string]string{
		"Le loup est revenu ! /": "Le loup est revenu !",
		"Pennart, Geoffroy de,":  "Pennart, Geoffroy de",
		"  Racine :  ":           "Racine",
		"c1994.":                 "c1994",
		"":                       "",
	}
	for in, want := range cases {
		if got := cleanMarc(in); got != want {
			t.Errorf("cleanMarc(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestYear4(t *testing.T) {
	cases := map[string]int{
		"1999":       1999,
		"DL 2021":    2021,
		"c1994.":     1994,
		"2003-08-15": 2003,
		"s.d.":       0,
		"":           0,
	}
	for in, want := range cases {
		if got := year4(in); got != want {
			t.Errorf("year4(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestLang2(t *testing.T) {
	cases := map[string]string{
		"fre": "fr", "fra": "fr", "fr": "fr",
		"dut": "nl", "nld": "nl",
		"eng": "en", "ger": "de",
		"ita": "it", // unlisted: the first two letters are kept
		"":    "",
	}
	for in, want := range cases {
		if got := lang2(in); got != want {
			t.Errorf("lang2(%q) = %q, want %q", in, got, want)
		}
	}
}

// Field 214 is repeatable: a publication 214 and a manufacturing 214 without
// publisher or date. The first field met is not necessarily the right one.
func TestRecordFromUnimarcRepeated214(t *testing.T) {
	const x = `<record>
	 <datafield tag="200"><subfield code="a">Histoires du soir</subfield></datafield>
	 <datafield tag="214" ind2="3"><subfield code="a">impr. en Espagne</subfield></datafield>
	 <datafield tag="214" ind2="0"><subfield code="a">Paris</subfield><subfield code="c">les Arènes</subfield><subfield code="d">DL 2017</subfield></datafield>
	</record>`
	n := parsed(t, recordFromUnimarc, x, "9782352046783", "")
	if n == nil {
		t.Fatal("nil record")
	}
	if n.Publisher != "les Arènes" {
		t.Errorf("publisher: %q, want \"les Arènes\" (the second 214)", n.Publisher)
	}
	if n.Year != 2017 {
		t.Errorf("year: %d, want 2017", n.Year)
	}
}

// The BnF thumbnail is announced in 856: $u carries an image identifier, not a
// URL, and $b the label.
func TestCoverFromUnimarc(t *testing.T) {
	const withCover = `<record>
	 <datafield tag="200"><subfield code="a">T</subfield></datafield>
	 <datafield tag="856" ind2="2"><subfield code="u">274622</subfield><subfield code="b">Première de couverture</subfield></datafield>
	</record>`
	n := parsed(t, recordFromUnimarc, withCover, "9782352046783", "")
	want := "https://catalogue.bnf.fr/cover?appName=NE&idImage=274622&couverture=1"
	if n.CoverURL != want {
		t.Errorf("CoverURL = %q, want %q", n.CoverURL, want)
	}

	// An 856 that is not a cover (another linked resource) is ignored.
	const other = `<record>
	 <datafield tag="200"><subfield code="a">T</subfield></datafield>
	 <datafield tag="856" ind2="2"><subfield code="u">999</subfield><subfield code="b">Table des matières</subfield></datafield>
	</record>`
	if n := parsed(t, recordFromUnimarc, other, "9782352046783", ""); n.CoverURL != "" {
		t.Errorf("a non-cover 856 was wrongly kept: %q", n.CoverURL)
	}
}

// UniCat reports a book it does not hold as diagnostic 61, not as an empty
// result: that is an absence, not a fault.
func TestUniCatNoMatchDiagnosticIsAbsence(t *testing.T) {
	body := `<?xml version='1.0'?><searchRetrieveResponse xmlns="http://www.loc.gov/zing/srw/"><version>1.1</version><numberOfRecords>0</numberOfRecords><diagnostics><uri>info:srw/diagnostic/1/61</uri><message>First record position out of range</message></diagnostics></searchRetrieveResponse>`
	n, err := recordFromMarc21([]byte(body), "9791090757189", "")
	if err != nil || n != nil {
		t.Errorf("got record %v, error %v; want nil, nil", n, err)
	}
}
