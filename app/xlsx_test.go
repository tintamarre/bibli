package main

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"io"
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
