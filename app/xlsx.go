package main

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"path"
	"strconv"
	"strings"
)

// A spreadsheet Excel opens without being told anything, which no .csv does.
// Hand-written with the stdlib: the smallest set of parts Excel accepts.
//
// Every value is written as text: left to itself Excel rounds the ISBN
// 9782070612758 to 9,78207E+12.

// sheet is one tab, a header row and the rows under it. Widths are in
// characters and may be shorter than the header; the rest keep Excel's default.
// Numeric marks the columns whose integers are written as numbers, so Excel
// sorts them by value; every other cell is text.
type sheet struct {
	Name    string
	Header  []string
	Rows    [][]string
	Widths  []int
	Numeric []bool
}

// writeXLSX writes the whole workbook to w in one pass.
func writeXLSX(w io.Writer, s sheet) error {
	z := zip.NewWriter(w)
	parts := []struct {
		name    string
		content func() string
	}{
		{"[Content_Types].xml", contentTypesXML},
		{"_rels/.rels", rootRelsXML},
		{"xl/workbook.xml", func() string { return workbookXML(s.Name) }},
		{"xl/_rels/workbook.xml.rels", workbookRelsXML},
		{"xl/styles.xml", stylesXML},
		{"xl/worksheets/sheet1.xml", func() string { return sheetXML(s) }},
	}
	for _, p := range parts {
		f, err := z.Create(p.name)
		if err != nil {
			return fmt.Errorf("%s: %w", p.name, err)
		}
		if _, err := io.WriteString(f, p.content()); err != nil {
			return fmt.Errorf("%s: %w", p.name, err)
		}
	}
	return z.Close()
}

const xmlHeader = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n"

// Every part must be declared here, or Excel calls the workbook corrupt.
func contentTypesXML() string {
	return xmlHeader + `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
		`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>` +
		`<Default Extension="xml" ContentType="application/xml"/>` +
		`<Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/>` +
		`<Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>` +
		`<Override PartName="/xl/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.styles+xml"/>` +
		`</Types>`
}

func rootRelsXML() string {
	return xmlHeader + `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
		`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/>` +
		`</Relationships>`
}

// The tab's name, which is what someone sees at the bottom of the window.
func workbookXML(name string) string {
	return xmlHeader + `<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" ` +
		`xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">` +
		`<sheets><sheet name="` + escapeXML(sheetName(name)) + `" sheetId="1" r:id="rId1"/></sheets>` +
		`</workbook>`
}

func workbookRelsXML() string {
	return xmlHeader + `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
		`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/>` +
		`<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/>` +
		`</Relationships>`
}

// Two fonts, for a bold header, and the two fills Excel requires first
// ("none" and "gray125"), used or not.
func stylesXML() string {
	return xmlHeader + `<styleSheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">` +
		`<fonts count="2">` +
		`<font><sz val="11"/><name val="Calibri"/></font>` +
		`<font><b/><sz val="11"/><name val="Calibri"/></font>` +
		`</fonts>` +
		`<fills count="2"><fill><patternFill patternType="none"/></fill>` +
		`<fill><patternFill patternType="gray125"/></fill></fills>` +
		`<borders count="1"><border/></borders>` +
		`<cellStyleXfs count="1"><xf numFmtId="0" fontId="0" fillId="0" borderId="0"/></cellStyleXfs>` +
		`<cellXfs count="2">` +
		`<xf numFmtId="0" fontId="0" fillId="0" borderId="0" xfId="0"/>` +
		`<xf numFmtId="0" fontId="1" fillId="0" borderId="0" xfId="0" applyFont="1"/>` +
		`</cellXfs>` +
		`</styleSheet>`
}

// styleHeader is the second cellXfs entry above: bold.
const styleHeader = 1

// The sheet. Element order is fixed by the schema (dimension, sheetViews, cols,
// sheetData); Excel rejects any other.
func sheetXML(s sheet) string {
	var b strings.Builder
	b.WriteString(xmlHeader)
	b.WriteString(`<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">`)

	rows := len(s.Rows) + 1 // the header counts
	fmt.Fprintf(&b, `<dimension ref="A1:%s%d"/>`, columnName(len(s.Header)), rows)

	// The header row is frozen.
	b.WriteString(`<sheetViews><sheetView workbookViewId="0" tabSelected="1">` +
		`<pane ySplit="1" topLeftCell="A2" activePane="bottomLeft" state="frozen"/>` +
		`</sheetView></sheetViews>`)

	if len(s.Widths) > 0 {
		b.WriteString(`<cols>`)
		for i, wch := range s.Widths {
			fmt.Fprintf(&b, `<col min="%d" max="%d" width="%d" customWidth="1"/>`, i+1, i+1, wch)
		}
		b.WriteString(`</cols>`)
	}

	b.WriteString(`<sheetData>`)
	writeRow(&b, 1, s.Header, styleHeader, nil)
	for i, row := range s.Rows {
		writeRow(&b, i+2, row, 0, s.Numeric)
	}
	b.WriteString(`</sheetData></worksheet>`)
	return b.String()
}

// One row. An empty cell is left out rather than written empty.
func writeRow(b *strings.Builder, n int, cells []string, style int, numeric []bool) {
	fmt.Fprintf(b, `<row r="%d">`, n)
	for i, v := range cells {
		if v == "" {
			continue
		}
		ref := fmt.Sprintf("%s%d", columnName(i+1), n)
		if _, err := strconv.Atoi(v); err == nil && i < len(numeric) && numeric[i] {
			fmt.Fprintf(b, `<c r="%s"><v>%s</v></c>`, ref, v)
		} else if style != 0 {
			fmt.Fprintf(b, `<c r="%s" s="%d" t="inlineStr"><is><t xml:space="preserve">%s</t></is></c>`,
				ref, style, escapeXML(v))
		} else {
			fmt.Fprintf(b, `<c r="%s" t="inlineStr"><is><t xml:space="preserve">%s</t></is></c>`,
				ref, escapeXML(v))
		}
	}
	b.WriteString(`</row>`)
}

// 1 -> A, 26 -> Z, 27 -> AA.
func columnName(n int) string {
	var name []byte
	for n > 0 {
		n--
		name = append([]byte{byte('A' + n%26)}, name...)
		n /= 26
	}
	if len(name) == 0 {
		return "A"
	}
	return string(name)
}

// Excel's limits on a tab name: 31 characters, and none of \ / ? * [ ].
// A guard: a file Excel calls corrupt gives no clue which part was wrong.
func sheetName(s string) string {
	s = strings.Map(func(r rune) rune {
		if strings.ContainsRune(`\/?*[]:`, r) {
			return '-'
		}
		return r
	}, strings.TrimSpace(s))
	if r := []rune(s); len(r) > 31 {
		s = string(r[:31])
	}
	if s == "" {
		return "Export"
	}
	return s
}

// escapeXML escapes for XML and drops the control characters XML 1.0 cannot
// carry even escaped: a title from a catalogue may hold one, and it would make
// the workbook unopenable.
func escapeXML(s string) string {
	var clean strings.Builder
	for _, r := range s {
		switch {
		case r == '\t' || r == '\n' || r == '\r':
			clean.WriteRune(r)
		case r < 0x20, r == 0x7f, r >= 0xfffe:
			// dropped
		default:
			clean.WriteRune(r)
		}
	}
	var out strings.Builder
	if err := xml.EscapeText(&out, []byte(clean.String())); err != nil {
		// strings.Builder never fails; this cannot happen and must not be
		// silent if it ever does.
		return ""
	}
	return out.String()
}

// readXLSX hands back the first cols columns of a workbook's first tab, row by
// row, every cell as text: what Excel, LibreOffice and Google Sheets save. A
// date or a formula comes back as stored (a serial number, the last result).
func readXLSX(b []byte, cols int) ([][]string, error) {
	z, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		return nil, err
	}
	part := func(name string) ([]byte, error) {
		for _, f := range z.File {
			if strings.EqualFold(f.Name, name) {
				rc, err := f.Open()
				if err != nil {
					return nil, err
				}
				defer rc.Close()
				// A few kilobytes for a list of readers; the bound stops a zip bomb.
				return io.ReadAll(io.LimitReader(rc, 32<<20))
			}
		}
		return nil, fmt.Errorf("%s: missing", name)
	}

	// The first tab in the workbook's order, wherever its relationship says it is.
	raw, err := part("xl/workbook.xml")
	if err != nil {
		return nil, err
	}
	var wb struct {
		Sheets []struct {
			ID string `xml:"http://schemas.openxmlformats.org/officeDocument/2006/relationships id,attr"`
		} `xml:"sheets>sheet"`
	}
	if err := xml.Unmarshal(raw, &wb); err != nil {
		return nil, fmt.Errorf("workbook: %w", err)
	}
	if len(wb.Sheets) == 0 {
		return nil, errors.New("workbook: no sheet")
	}
	if raw, err = part("xl/_rels/workbook.xml.rels"); err != nil {
		return nil, err
	}
	var rels struct {
		Rel []struct {
			ID     string `xml:"Id,attr"`
			Target string `xml:"Target,attr"`
		} `xml:"Relationship"`
	}
	if err := xml.Unmarshal(raw, &rels); err != nil {
		return nil, fmt.Errorf("workbook rels: %w", err)
	}
	sheetPath := ""
	for _, r := range rels.Rel {
		if r.ID == wb.Sheets[0].ID {
			// Relative to xl/, or absolute from the package root.
			if strings.HasPrefix(r.Target, "/") {
				sheetPath = strings.TrimPrefix(r.Target, "/")
			} else {
				sheetPath = path.Join("xl", r.Target)
			}
		}
	}
	if sheetPath == "" {
		return nil, errors.New("workbook: the first sheet has no part")
	}

	// Excel keeps text in one shared table; Bibli's own sheets write it inline.
	type text struct {
		T    string `xml:"t"`
		Runs []struct {
			T string `xml:"t"`
		} `xml:"r"`
	}
	flat := func(t text) string {
		s := t.T
		for _, r := range t.Runs {
			s += r.T
		}
		return s
	}
	var shared []string
	if raw, err := part("xl/sharedStrings.xml"); err == nil {
		var sst struct {
			SI []text `xml:"si"`
		}
		if err := xml.Unmarshal(raw, &sst); err != nil {
			return nil, fmt.Errorf("shared strings: %w", err)
		}
		for _, si := range sst.SI {
			shared = append(shared, flat(si))
		}
	}

	if raw, err = part(sheetPath); err != nil {
		return nil, err
	}
	var ws struct {
		Rows []struct {
			Cells []struct {
				Ref    string `xml:"r,attr"`
				Type   string `xml:"t,attr"`
				Value  string `xml:"v"`
				Inline text   `xml:"is"`
			} `xml:"c"`
		} `xml:"sheetData>row"`
	}
	if err := xml.Unmarshal(raw, &ws); err != nil {
		return nil, fmt.Errorf("sheet: %w", err)
	}
	var out [][]string
	for _, row := range ws.Rows {
		cells := make([]string, cols)
		next := 0
		for _, c := range row.Cells {
			// An empty cell is left out of the file, so the reference places
			// the next one; without it, cells follow on.
			col := next
			if c.Ref != "" {
				col = columnIndex(c.Ref)
			}
			next = col + 1
			if col < 0 || col >= cols {
				continue
			}
			switch c.Type {
			case "s":
				if i, err := strconv.Atoi(c.Value); err == nil && i >= 0 && i < len(shared) {
					cells[col] = shared[i]
				}
			case "inlineStr":
				cells[col] = flat(c.Inline)
			default:
				cells[col] = c.Value
			}
		}
		out = append(out, cells)
	}
	return out, nil
}

// columnIndex reads the column of a cell reference: "A7" -> 0, "AB12" -> 27.
func columnIndex(ref string) int {
	n := 0
	for _, r := range ref {
		if r < 'A' || r > 'Z' || n > 1<<16 {
			break
		}
		n = n*26 + int(r-'A'+1)
	}
	return n - 1
}
