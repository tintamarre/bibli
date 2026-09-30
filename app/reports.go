package main

import (
	"encoding/csv"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
)

// Operations: paper output (overdue lists, inventory) and CSV export.
// Paper stays the last resort.

// printLoans is the loans screen on paper, grouped by class, with the screen's
// class and tab filters. /print/overdue is always the late ones only.
func (a *app) printLoans(w http.ResponseWriter, r *http.Request) {
	overdueOnly := r.URL.Path == "/print/overdue" || r.URL.Query().Get("overdue") == "1"
	classValue, byClass := classFilter(r.URL.Query().Get("class"))

	query := `SELECT first_name, last_initial, COALESCE(class, ?), title, code,
	                 due_on, days_overdue
	            FROM v_active_loan`
	args := []any{noClassLabel()}
	var where []string
	if overdueOnly {
		where = append(where, `days_overdue > 0`)
	}
	if byClass {
		where = append(where, `COALESCE(class, '') = ?`)
		args = append(args, classValue)
	}
	if len(where) > 0 {
		query += ` WHERE ` + strings.Join(where, ` AND `)
	}
	// The grouping below folds consecutive rows, so the order has to be the
	// grouping's: out of order, one class becomes two headings.
	query += ` ORDER BY class, last_initial, first_name`

	rows, err := a.db.Query(query, args...)
	if err != nil {
		log.Printf("loans print: %v", err)
		internalError(w, r)
		return
	}
	defer rows.Close()

	type row struct {
		FirstName, LastInitial, Title, Code, DueOn string
		Days                                       int
	}
	type group struct {
		Class string
		Rows  []row
	}
	var groups []group
	for rows.Next() {
		var class string
		var l row
		var dueOn string
		if err := rows.Scan(&l.FirstName, &l.LastInitial, &class, &l.Title, &l.Code, &dueOn, &l.Days); err != nil {
			log.Printf("loans print (scan): %v", err)
			internalError(w, r)
			return
		}
		l.DueOn = shortDate(requestLang(r), dueOn)
		if n := len(groups); n > 0 && groups[n-1].Class == class {
			groups[n-1].Rows = append(groups[n-1].Rows, l)
		} else {
			groups = append(groups, group{Class: class, Rows: []row{l}})
		}
	}
	// The class the sheet is restricted to, named in the heading.
	printedClass := ""
	if byClass {
		printedClass = classValue
		if printedClass == "" {
			printedClass = noClassLabel()
		}
	}
	a.renderDoc(w, r, "loans_print", map[string]any{
		"Groups":      groups,
		"OverdueOnly": overdueOnly,
		"Class":       printedClass,
		"Date":        shortDate(requestLang(r), time.Now().Format("2006-01-02")),
	})
}

// printInventory is the whole collection, one copy per line, ready to print.
func (a *app) printInventory(w http.ResponseWriter, r *http.Request) {
	// The inventory screen on paper, filter and order included.
	f := readInvFilter(r)
	rows, err := a.db.Query(
		`SELECT b.title, COALESCE(b.authors, ''), COALESCE(b.isbn13, ''), c.code, c.status,
		        COALESCE(c.location, ''),
		        CASE WHEN l.id IS NOT NULL THEN 'out' ELSE '' END
		   FROM copy c
		   JOIN book b ON b.id = c.book_id
		   LEFT JOIN loan l ON l.copy_id = c.id AND l.returned_on IS NULL`+
			invWhere(f)+` ORDER BY `+invOrderBy(f), invArgs(f)...)
	if err != nil {
		log.Printf("inventory print: %v", err)
		internalError(w, r)
		return
	}
	defer rows.Close()

	type row struct{ Title, Authors, ISBN, Code, Status, Location, Out string }
	var list []row
	for rows.Next() {
		var l row
		if err := rows.Scan(&l.Title, &l.Authors, &l.ISBN, &l.Code, &l.Status, &l.Location, &l.Out); err != nil {
			log.Printf("inventory print (scan): %v", err)
			internalError(w, r)
			return
		}
		list = append(list, l)
	}
	a.renderDoc(w, r, "inventory_print", map[string]any{
		"Rows":  list,
		"Total": len(list),
		"Date":  shortDate(requestLang(r), time.Now().Format("2006-01-02")),
	})
}

// exportRows is one row per copy, as the database holds it, narrowed by the
// inventory filter; untranslated. Read into memory so a database error is
// caught before a byte of the response is sent.
func (a *app) exportRows(f invFilter) ([][]string, error) {
	q, err := a.db.Query(
		`SELECT COALESCE(b.isbn13, ''), b.title, COALESCE(b.authors, ''),
		        COALESCE(b.publisher, ''), COALESCE(b.year, ''),
		        c.code, c.status, COALESCE(c.location, ''),
		        COALESCE(br.first_name || ' ' || br.last_initial, ''), COALESCE(br.class, ''),
		        COALESCE(l.loaned_on, ''), COALESCE(l.due_on, '')
		   FROM copy c
		   JOIN book b ON b.id = c.book_id
		   LEFT JOIN loan l  ON l.copy_id = c.id AND l.returned_on IS NULL
		   LEFT JOIN borrower br ON br.id = l.borrower_id`+
			invWhere(f)+` ORDER BY `+invOrderBy(f), invArgs(f)...)
	if err != nil {
		return nil, err
	}
	defer q.Close()

	var rows [][]string
	for q.Next() {
		fields := make([]string, len(csvColumns))
		ptrs := make([]any, len(fields))
		for i := range fields {
			ptrs[i] = &fields[i]
		}
		if err := q.Scan(ptrs...); err != nil {
			return nil, err
		}
		rows = append(rows, fields)
	}
	return rows, q.Err()
}

// exportCSV is RFC 4180 and nothing else: no BOM, no "sep=" line, no CRLF, no
// semicolons — Excel has /export.xlsx. Column names and copy status stay
// English: the file is a schema.
func (a *app) exportCSV(w http.ResponseWriter, r *http.Request) {
	rows, err := a.exportRows(readInvFilter(r))
	if err != nil {
		log.Printf("CSV export: %v", err)
		internalError(w, r)
		return
	}

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf(`attachment; filename="%s.csv"`, exportBasename()))

	cw := csv.NewWriter(w)
	defer cw.Flush()

	cw.Write(csvColumnNames()) // fixed English keys: never formula-shaped
	for _, row := range rows {
		cw.Write(csvSafeRow(row))
	}
}

// csvSafe defends a spreadsheet that opens the CSV. A cell beginning with = + -
// @ (or a control character some parsers treat the same way) is read as a
// formula, so a catalogue-sourced title like =HYPERLINK("http://evil","x") would
// run when a volunteer opens the file. A leading apostrophe pins the cell to
// plain text; an RFC 4180 reader keeps it verbatim.
func csvSafe(cell string) string {
	if cell == "" {
		return cell
	}
	switch cell[0] {
	case '=', '+', '-', '@', '\t', '\r':
		return "'" + cell
	}
	return cell
}

func csvSafeRow(row []string) []string {
	out := make([]string, len(row))
	for i, cell := range row {
		out[i] = csvSafe(cell)
	}
	return out
}

// exportXLSX is the same export as a spreadsheet, translated, for a person to
// open. An ISBN written as text is not rounded to 9,78207E+12.
func (a *app) exportXLSX(w http.ResponseWriter, r *http.Request) {
	rows, err := a.exportRows(readInvFilter(r))
	if err != nil {
		log.Printf("XLSX export: %v", err)
		internalError(w, r)
		return
	}
	lang := requestLang(r)
	for _, row := range rows {
		row[statusColumn] = statusWording(lang, row[statusColumn])
	}

	w.Header().Set("Content-Type",
		"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf(`attachment; filename="%s.xlsx"`, exportBasename()))

	if err := writeXLSX(w, sheet{
		Name:   T(lang, "csv.sheet_name"),
		Header: csvHeader(lang),
		Rows:   rows,
		Widths: exportWidths,
	}); err != nil {
		// The response is already on its way, so there is nowhere to put this
		// but the log: the download arrives truncated and Excel says so.
		log.Printf("XLSX export (write): %v", err)
	}
}

// Both files carry the same name and the same date; only the extension differs.
func exportBasename() string {
	return "bibli-export-" + time.Now().Format("2006-01-02")
}

// statusColumn is the copy status, the one column translated rather than copied
// out of the database.
const statusColumn = 6

// Column widths for the spreadsheet, in characters, in file order: a title needs
// room and a year does not.
var exportWidths = []int{15, 40, 28, 20, 7, 14, 12, 18, 18, 10, 12, 16}

// csvColumnNames are the locale keys with their prefix taken off: the code already
// keeps keys English and stable, as a column name must be.
func csvColumnNames() []string {
	names := make([]string, len(csvColumns))
	for i, key := range csvColumns {
		names[i] = strings.TrimPrefix(key, "csv.")
	}
	return names
}

// csvColumns are the keys of the export columns, in file order.
var csvColumns = []string{
	"csv.isbn13", "csv.title", "csv.authors", "csv.publisher", "csv.year",
	"csv.code", "csv.status", "csv.location",
	"csv.borrower", "csv.borrower_class", "csv.loaned_on", "csv.due_on",
}

func csvHeader(lang string) []string {
	row := make([]string, len(csvColumns))
	for i, key := range csvColumns {
		row[i] = T(lang, key)
	}
	return row
}
