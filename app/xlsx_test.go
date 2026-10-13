package main

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"io"
	"slices"
	"strings"
	"testing"
)

// build writes a one-sheet workbook and hands back its parts by name.
func build(t *testing.T, s sheet) map[string]string {
	t.Helper()
	var buf bytes.Buffer
	if err := writeXLSX(&buf, s); err != nil {
		t.Fatalf("writing the workbook: %v", err)
	}
	return unzipParts(t, buf.Bytes())
}

// unzipParts hands back a workbook's parts by name.
func unzipParts(t *testing.T, b []byte) map[string]string {
	t.Helper()
	z, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatalf("the workbook is not a readable zip: %v", err)
	}
	parts := map[string]string{}
	for _, f := range z.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("%s: %v", f.Name, err)
		}
		b, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatalf("%s: %v", f.Name, err)
		}
		parts[f.Name] = string(b)
	}
	return parts
}

// Every part the package must declare is present and well-formed XML: Excel
// never says which part of a corrupt file was wrong.
func TestXLSXHasThePartsExcelRequires(t *testing.T) {
	parts := build(t, sheet{
		Name:   "Fonds",
		Header: []string{"isbn13", "titre"},
		Rows:   [][]string{{"9782070612758", "Le Petit Prince"}},
		Widths: []int{15, 40},
	})

	for _, want := range []string{
		"[Content_Types].xml",
		"_rels/.rels",
		"xl/workbook.xml",
		"xl/_rels/workbook.xml.rels",
		"xl/styles.xml",
		"xl/worksheets/sheet1.xml",
	} {
		if _, ok := parts[want]; !ok {
			t.Errorf("the workbook has no %s — Excel would call the file corrupt", want)
		}
	}
	for name, content := range parts {
		if err := xml.Unmarshal([]byte(content), new(any)); err != nil {
			t.Errorf("%s is not well-formed XML: %v", name, err)
		}
	}
	// Each part has to be claimed in [Content_Types].xml, and each target
	// reached by a relationship, or the workbook does not open.
	types := parts["[Content_Types].xml"]
	for _, want := range []string{"/xl/workbook.xml", "/xl/worksheets/sheet1.xml", "/xl/styles.xml"} {
		if !strings.Contains(types, want) {
			t.Errorf("[Content_Types].xml does not declare %s", want)
		}
	}
	if !strings.Contains(parts["xl/_rels/workbook.xml.rels"], "worksheets/sheet1.xml") {
		t.Error("the workbook does not point at its own sheet")
	}
}

// Every cell is written as text, or Excel turns an ISBN into 9,78207E+12.
func TestXLSXWritesEveryValueAsText(t *testing.T) {
	parts := build(t, sheet{
		Name:   "Fonds",
		Header: []string{"isbn13", "annee"},
		Rows:   [][]string{{"9782070612758", "1943"}},
	})
	sh := parts["xl/worksheets/sheet1.xml"]
	if strings.Contains(sh, `<v>`) {
		t.Error("a cell holds a numeric value; an ISBN would be rounded into scientific notation")
	}
	for _, want := range []string{">9782070612758<", ">1943<"} {
		if !strings.Contains(sh, want) {
			t.Errorf("the sheet does not carry %s verbatim", want)
		}
	}
	if n := strings.Count(sh, `t="inlineStr"`); n != 4 {
		t.Errorf("%d inline strings, want 4 (two headings and two values)", n)
	}
}

// Control characters, which XML 1.0 cannot carry even escaped, are dropped;
// accents and markup characters survive.
func TestXLSXSurvivesWhatACatalogueSends(t *testing.T) {
	parts := build(t, sheet{
		Name:   "Fonds",
		Header: []string{"titre"},
		Rows: [][]string{
			{"L'Été & « les <accents> »"},
			{"Titre\x00avec\x07des octets"},
		},
	})
	sh := parts["xl/worksheets/sheet1.xml"]
	if err := xml.Unmarshal([]byte(sh), new(any)); err != nil {
		t.Fatalf("the sheet is not well-formed after a dirty title: %v", err)
	}
	if !strings.Contains(sh, "L&#39;Été &amp; « les &lt;accents&gt; »") {
		t.Errorf("the accents or the escaping did not survive:\n%s", sh)
	}
	if strings.ContainsAny(sh, "\x00\x07") {
		t.Error("a control character reached the file; Excel would refuse to open it")
	}
	if !strings.Contains(sh, "Titreavecdes octets") {
		t.Error("dropping the control characters took the text with them")
	}
}

// An empty value writes no cell, not a cell holding an empty string.
func TestXLSXLeavesEmptyCellsOut(t *testing.T) {
	parts := build(t, sheet{
		Name:   "Fonds",
		Header: []string{"a", "b", "c"},
		Rows:   [][]string{{"x", "", "z"}},
	})
	sh := parts["xl/worksheets/sheet1.xml"]
	if strings.Contains(sh, `r="B2"`) {
		t.Error("an empty cell was written out")
	}
	for _, want := range []string{`r="A2"`, `r="C2"`} {
		if !strings.Contains(sh, want) {
			t.Errorf("%s is missing: the columns after a blank one would shift left", want)
		}
	}
}

func TestColumnName(t *testing.T) {
	for _, c := range []struct {
		n    int
		want string
	}{{1, "A"}, {12, "L"}, {26, "Z"}, {27, "AA"}, {52, "AZ"}, {53, "BA"}, {702, "ZZ"}, {703, "AAA"}} {
		if got := columnName(c.n); got != c.want {
			t.Errorf("columnName(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}

// Excel's own rules for a tab name, which it enforces by refusing the file.
func TestSheetName(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"Fonds", "Fonds"},
		{"Collection", "Collection"},
		{"a/b\\c?d*e[f]g:h", "a-b-c-d-e-f-g-h"},
		{strings.Repeat("é", 40), strings.Repeat("é", 31)},
		{"   ", "Export"},
	} {
		if got := sheetName(c.in); got != c.want {
			t.Errorf("sheetName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// The real export, end to end: the CSV's columns, in the same order.
func TestExportXLSXMatchesTheCSVColumns(t *testing.T) {
	loadForTest(t)
	if len(exportWidths) != len(csvColumns) {
		t.Fatalf("%d widths for %d columns: the sheet would be shaped wrong",
			len(exportWidths), len(csvColumns))
	}
	for _, lang := range langs {
		header := csvHeader(lang)
		parts := build(t, sheet{
			Name:   T(lang, "csv.sheet_name"),
			Header: header,
			Rows:   [][]string{make([]string, len(header))},
			Widths: exportWidths,
		})
		sh := parts["xl/worksheets/sheet1.xml"]
		for i, h := range header {
			if !strings.Contains(sh, ">"+h+"<") {
				t.Errorf("%s: the sheet has no heading %q (column %d)", lang, h, i+1)
			}
		}
		// The header row is frozen.
		if !strings.Contains(sh, `state="frozen"`) {
			t.Errorf("%s: the header row does not stay put when the list scrolls", lang)
		}
	}
}

// A column marked numeric carries numbers, so Excel sorts it by value; a text
// value in it, and every other column, stays text.
func TestXLSXWritesNumericColumnsAsNumbers(t *testing.T) {
	parts := build(t, sheet{
		Name:    "Fonds",
		Header:  []string{"isbn13", "loans"},
		Rows:    [][]string{{"9782070612758", "12"}, {"9782211037495", "n/a"}},
		Numeric: []bool{false, true},
	})
	sh := parts["xl/worksheets/sheet1.xml"]
	if !strings.Contains(sh, `<c r="B2"><v>12</v></c>`) {
		t.Error("the loans count is not written as a number")
	}
	if strings.Contains(sh, `<v>978`) {
		t.Error("an ISBN was written as a number and would be rounded")
	}
	if !strings.Contains(sh, `>n/a<`) {
		t.Error("a non-number in a numeric column was dropped")
	}
}

// zipParts writes the given parts as a zip, the way another program would.
func zipParts(t *testing.T, parts map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	for name, content := range parts {
		f, err := z.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		io.WriteString(f, content)
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// A workbook as Excel saves it: text in the shared table, some of it rich,
// an empty cell left out, a number, and the tab shown first stored second.
func TestReadXLSXReadsWhatExcelSaves(t *testing.T) {
	const main = `xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"`
	b := zipParts(t, map[string]string{
		"xl/workbook.xml": `<workbook ` + main + `><sheets>` +
			`<sheet name="Lecteurs" sheetId="2" r:id="rId7"/><sheet name="Notes" sheetId="1" r:id="rId1"/></sheets></workbook>`,
		"xl/_rels/workbook.xml.rels": `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
			`<Relationship Id="rId1" Target="worksheets/sheet1.xml"/><Relationship Id="rId7" Target="/xl/worksheets/sheet2.xml"/></Relationships>`,
		"xl/sharedStrings.xml": `<sst ` + main + `><si><t>Prénom</t></si><si><t>Nom</t></si><si><t>Classe</t></si>` +
			`<si><t>Léa</t></si><si><r><t>Du</t></r><r><rPr><b/></rPr><t>rant</t></r></si><si><t>Tom</t></si></sst>`,
		"xl/worksheets/sheet1.xml": `<worksheet ` + main + `><sheetData><row r="1"><c r="A1" t="inlineStr"><is><t>wrong tab</t></is></c></row></sheetData></worksheet>`,
		"xl/worksheets/sheet2.xml": `<worksheet ` + main + `><sheetData>` +
			`<row r="1"><c r="A1" t="s"><v>0</v></c><c r="B1" t="s"><v>1</v></c><c r="C1" t="s"><v>2</v></c><c r="D1"><v>99</v></c></row>` +
			`<row r="2"><c r="A2" t="s"><v>3</v></c><c r="B2" t="s"><v>4</v></c><c r="C2"><v>4</v></c></row>` +
			`<row r="4"><c r="A4" t="s"><v>5</v></c><c r="C4" t="inlineStr"><is><t>P3</t></is></c></row>` +
			`</sheetData></worksheet>`,
	})
	got, err := readXLSX(b, 3)
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"Prénom", "Nom", "Classe"}, {"Léa", "Durant", "4"}, {"Tom", "", "P3"}}
	if len(got) != len(want) {
		t.Fatalf("rows = %q, want %q", got, want)
	}
	for i := range want {
		if !slices.Equal(got[i], want[i]) {
			t.Errorf("row %d = %q, want %q", i, got[i], want[i])
		}
	}

	// Through the import: the header goes, Tom has no last name and is counted.
	rows, skipped := parseCSV(tabText(got))
	if len(rows) != 1 || rows[0] != (importRow{"Léa", "D.", "4", false}) || skipped != 1 {
		t.Errorf("imports as %+v (%d skipped)", rows, skipped)
	}
}

// What the import writes back for the confirmation survives a quote or a tab
// typed into a cell.
func TestTabTextRoundTrips(t *testing.T) {
	rows, _ := parseCSV(tabText([][]string{{"Prénom", "Nom", "Groupe"}, {`Jean "Jo"`, "Dupont", "P4\tbis"}}))
	if len(rows) != 1 || rows[0] != (importRow{`Jean "Jo"`, "D.", "P4\tbis", false}) {
		t.Errorf("imports as %+v", rows)
	}
}

func TestColumnIndex(t *testing.T) {
	for ref, want := range map[string]int{"A1": 0, "C7": 2, "Z9": 25, "AA1": 26, "AB12": 27, "1": -1} {
		if got := columnIndex(ref); got != want {
			t.Errorf("columnIndex(%q) = %d, want %d", ref, got, want)
		}
	}
}
