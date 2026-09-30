package main

import (
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// backupStatus is the backup state, formatted for the screen, so anyone can
// check that backups run without reading a log.
type backupStatus struct {
	Active  bool
	Never   bool // no attempt since startup
	When    string
	Path    string
	Error   string
	DBPath  string
	Started string
	Files   []backupFile // what can be downloaded, newest first
}

// backupFile is one snapshot offered for download.
type backupFile struct {
	Name string // the file on disk, under its rotation slot name
	When string // the day it was taken, in the school's wording
	Size string
}

// cacheView is the space taken by the kept thumbnails, the only folder that
// grows with use.
type cacheView struct {
	Active bool
	Count  int
	Size   string
	// Books no catalogue had a cover for when asked (coverCache.forgetAbsences).
	Absent int
}

// byteUnits are the binary size suffixes per language: formatting conventions,
// kept in Go like the date layouts.
var byteUnits = map[string][3]string{
	"fr": {"o", "Kio", "Mio"},
	"en": {"B", "KiB", "MiB"},
	"nl": {"B", "KiB", "MiB"},
}

func humanSize(o int64, lang string) string {
	u, ok := byteUnits[lang]
	if !ok {
		u = byteUnits[defaultLang]
	}
	switch {
	case o >= 1<<20:
		return fmt.Sprintf("%.1f %s", float64(o)/(1<<20), u[2])
	case o >= 1<<10:
		return fmt.Sprintf("%d %s", o/(1<<10), u[1])
	default:
		return fmt.Sprintf("%d %s", o, u[0])
	}
}

// listBackups is the snapshots offered for download, newest first, one per
// day: a weekly or monthly copy is that day's daily copy byte for byte.
func listBackups(dir, lang string) []backupFile {
	if dir == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		log.Printf("backups: reading %s: %v", dir, err)
		return nil
	}
	newest := map[string]fs.FileInfo{}
	for _, e := range entries {
		if e.IsDir() || !isBackupName(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		day := info.ModTime().Format("2006-01-02")
		if kept, ok := newest[day]; !ok || info.ModTime().After(kept.ModTime()) {
			newest[day] = info
		}
	}
	days := make([]string, 0, len(newest))
	for day := range newest {
		days = append(days, day)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(days))) // ISO dates sort as text

	out := make([]backupFile, 0, len(days))
	for _, day := range days {
		info := newest[day]
		out = append(out, backupFile{
			Name: info.Name(),
			When: shortDate(lang, day),
			Size: humanSize(info.Size(), lang),
		})
	}
	return out
}

func backupView(r *http.Request, basePath, backupDir string) backupStatus {
	e := LastBackup()
	s := backupStatus{Active: e.Active, Path: e.Path, Error: e.Error, DBPath: basePath,
		Files: listBackups(backupDir, requestLang(r))}
	if e.When.IsZero() {
		s.Never = true
		return s
	}
	// The joining word is translated; the layouts stay numeric, since a letter
	// in a `time` layout can be a symbol.
	lang := requestLang(r)
	s.When = tr(r, "format.datetime",
		e.When.Format(dateLayout(lang)), e.When.Format("15:04"))
	return s
}

// settingsData is everything the settings screen shows, on opening as well as
// after a rejected entry.
func (a *app) settingsData(r *http.Request, schoolName, lang, theme string) map[string]any {
	return map[string]any{
		"Title":         tr(r, "nav.settings"),
		"LoanDays":      a.loanDays(),
		"School":        schoolName,
		"Language":      lang,
		"Languages":     offeredLangs(lang),
		"Themes":        offeredThemes(requestLang(r), theme),
		"Retention":     retentionYears(a.db),
		"RetentionMax":  maxRetentionYears,
		"Express":       a.expressCatalogue(),
		"FamilyLinks":   familyLinks(),
		"FamilyOffered": familyLinksOffered(),
		"GoogleFromEnv": googleKeyFromEnv(),
		"Backup":        backupView(r, a.dbPath, a.backupDir),
		"Cache":         a.cacheView(requestLang(r)),
	}
}

// offeredLang is a language offered by the settings screen, named in itself.
type offeredLang struct {
	Code     string
	Name     string
	Selected bool
}

func offeredLangs(selected string) []offeredLang {
	l := make([]offeredLang, 0, len(langs))
	for _, c := range langs {
		l = append(l, offeredLang{Code: c, Name: langName[c], Selected: c == selected})
	}
	return l
}

func (a *app) settingsScreen(w http.ResponseWriter, r *http.Request) {
	d := a.settingsData(r, school(), instanceLang(), instanceTheme())
	d["Ok"] = r.URL.Query().Get("ok") == "1"
	// Absent is not zero: "0 forgotten" is said only to whoever just clicked.
	if v := r.URL.Query().Get("forgotten"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			d["Forgotten"] = n
			d["ForgottenShown"] = true
		}
	}
	a.render(w, r, "settings", d)
}

func (a *app) cacheView(lang string) cacheView {
	count, bytes, absent := a.covers.status()
	return cacheView{Active: a.covers.dir != "", Count: count, Size: humanSize(bytes, lang), Absent: absent}
}

// settingsForgetAbsences drops the recorded cover absences so the catalogues
// are asked again; the images are kept.
func (a *app) settingsForgetAbsences(w http.ResponseWriter, r *http.Request) {
	n := a.covers.forgetAbsences()
	log.Printf("cover cache: %d absences forgotten, the catalogues will be asked again", n)
	http.Redirect(w, r, "/settings?forgotten="+strconv.Itoa(n), http.StatusSeeOther)
}

// settingsSave validates and stores the settings (loan period, school,
// language, theme, loans links, Google Books key).
func (a *app) settingsSave(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		badRequest(w, r)
		return
	}

	schoolName := strings.TrimSpace(r.FormValue("school_name"))
	if len(schoolName) > 120 {
		schoolName = schoolName[:120]
	}

	lang := strings.TrimSpace(r.FormValue("language"))
	if !knownLang(lang) {
		lang = instanceLang() // unexpected value: change nothing
	}
	theme := strings.TrimSpace(r.FormValue("theme"))
	if !knownTheme(theme) {
		theme = instanceTheme()
	}

	n, err := strconv.Atoi(strings.TrimSpace(r.FormValue("loan_days")))
	retention, errR := strconv.Atoi(strings.TrimSpace(r.FormValue("retention_years")))

	// Empty leaves the saved key alone; a typed key wins over the box to remove it.
	googleKeyIn := strings.TrimSpace(r.FormValue("google_key"))
	removeGoogleKey := r.FormValue("google_key_remove") != ""

	errMsg := ""
	if err != nil || n < 1 || n > maxLoanDays {
		errMsg = tr(r, "settings.err_loan_days")
	} else if errR != nil || retention < 1 || retention > maxRetentionYears {
		errMsg = tr(r, "settings.err_retention", maxRetentionYears)
	} else if len(googleKeyIn) > 200 || strings.ContainsAny(googleKeyIn, " \t\r\n") {
		errMsg = tr(r, "settings.err_google_key")
	}
	if errMsg != "" {
		// Keep the name as typed, so what was right is not typed again.
		d := a.settingsData(r, schoolName, lang, theme)
		d["Error"] = errMsg
		a.render(w, r, "settings", d)
		return
	}

	if _, err := a.db.Exec(
		`UPDATE setting SET value = ?, updated_at = datetime('now') WHERE key = 'loan_days'`,
		strconv.Itoa(n),
	); err != nil {
		log.Printf("settings/save (loan duration): %v", err)
		internalError(w, r)
		return
	}
	if _, err := a.db.Exec(
		`UPDATE setting SET value = ?, updated_at = datetime('now') WHERE key = 'school_name'`,
		schoolName,
	); err != nil {
		log.Printf("settings/save (school): %v", err)
		internalError(w, r)
		return
	}
	if _, err := a.db.Exec(
		`UPDATE setting SET value = ?, updated_at = datetime('now')
		  WHERE key = 'retention_years'`, strconv.Itoa(retention),
	); err != nil {
		log.Printf("settings/save (retention): %v", err)
		internalError(w, r)
		return
	}
	if _, err := a.db.Exec(
		`UPDATE setting SET value = ?, updated_at = datetime('now') WHERE key = 'language'`,
		lang,
	); err != nil {
		log.Printf("settings/save (language): %v", err)
		internalError(w, r)
		return
	}
	if _, err := a.db.Exec(
		`UPDATE setting SET value = ?, updated_at = datetime('now') WHERE key = 'theme'`,
		theme,
	); err != nil {
		log.Printf("settings/save (theme): %v", err)
		internalError(w, r)
		return
	}
	// A cleared checkbox sends nothing, so absence means off.
	express := "0"
	if r.FormValue("express_catalogue") != "" {
		express = "1"
	}
	if _, err := a.db.Exec(
		`UPDATE setting SET value = ?, updated_at = datetime('now')
		  WHERE key = 'express_catalogue'`, express,
	); err != nil {
		log.Printf("settings/save (express catalogue): %v", err)
		internalError(w, r)
		return
	}
	// A disabled checkbox sends nothing either: keep the stored choice.
	family := familyLinks()
	if familyLinksOffered() {
		family = r.FormValue("family_links") != ""
		v := "0"
		if family {
			v = "1"
		}
		if _, err := a.db.Exec(
			`INSERT INTO setting (key, value, label)
			 VALUES ('family_links', ?, 'Offer the secret loans links for families (family.go)')
			 ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = datetime('now')`,
			v,
		); err != nil {
			log.Printf("settings/save (loans links): %v", err)
			internalError(w, r)
			return
		}
	}
	// BIBLI_GOOGLE_BOOKS_KEY wins, so the field is disabled and nothing is written.
	if !googleKeyFromEnv() {
		switch {
		case googleKeyIn != "":
			if _, err := a.db.Exec(
				`INSERT INTO setting (key, value, label)
				 VALUES ('google_books_key', ?, 'Google Books API key, never shown once saved')
				 ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = datetime('now')`,
				googleKeyIn,
			); err != nil {
				log.Printf("settings/save (Google Books key): %v", err)
				internalError(w, r)
				return
			}
			setGoogleKey(googleKeyIn)
			log.Print("Google Books: API key saved in /settings")
		case removeGoogleKey:
			if _, err := a.db.Exec(`DELETE FROM setting WHERE key = 'google_books_key'`); err != nil {
				log.Printf("settings/save (Google Books key): %v", err)
				internalError(w, r)
				return
			}
			setGoogleKey("")
			log.Print("Google Books: API key removed in /settings")
		}
	}
	setSchool(schoolName)
	setLang(lang)
	setTheme(theme)
	if familyLinksOffered() {
		setFamilyLinks(family)
	}
	// The sentinel's stored name follows the language.
	syncAnonymousName(a.db)

	http.Redirect(w, r, "/settings?ok=1", http.StatusSeeOther)
}

// backupDownload hands over one backup the daily task wrote with VACUUM INTO —
// never the live database — named after the day it was taken rather than
// its rotation slot.
func (a *app) backupDownload(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if a.backupDir == "" || !isBackupName(name) {
		a.notFoundScreen(w, r)
		return
	}
	f, err := os.Open(filepath.Join(a.backupDir, name))
	if err != nil {
		a.notFoundScreen(w, r)
		return
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil || info.IsDir() {
		a.notFoundScreen(w, r)
		return
	}
	// Every borrower is in this file: never cached.
	download := "biblio-" + info.ModTime().Format("2006-01-02") + ".db"
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+download+`"`)
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, download, info.ModTime(), f)
}
