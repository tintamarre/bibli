package main

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"
)

//go:embed templates/*.html
var templatesFS embed.FS

//go:embed static
var staticFS embed.FS

// app carries what every handler shares. No global variables.
type app struct {
	db *sql.DB
	// language -> page name -> template, one complete set per language.
	pages         map[string]map[string]*template.Template
	adminPass     string // the single librarian password (BIBLI_ADMIN_PASSWORD)
	sessionKey    []byte // HMAC key signing the session cookies
	secureCookies bool   // set cookies as Secure (HTTPS)
	trustProxy    bool   // believe X-Forwarded-For (behind a reverse proxy)
	dbPath        string // path of the SQLite file, shown in the settings
	backupDir     string // where the daily backups are written, "" = disabled
	throttle      *throttle
	covers        *coverCache
}

func main() {
	if code, ran := runSetup(); ran {
		os.Exit(code)
	}
	var (
		dbPath        = flag.String("db", "biblio.db", "path to the SQLite file")
		addr          = flag.String("addr", ":8080", "listen address")
		backupDir     = flag.String("backup-dir", "backups", "backup directory (empty = disabled)")
		cacheDir      = flag.String("cache-dir", "cache", "cover cache directory (empty = disabled)")
		secureCookies = flag.Bool("secure-cookies", true, "session cookies as Secure (disable for local HTTP dev)")
		trustProxy    = flag.Bool("trust-proxy", false, "trust X-Forwarded-For to identify the client (only behind a reverse proxy)")
		trackingOffer = flag.Bool("tracking-links", true, "offer the secret tracking links (false for a single-computer install)")
		logPath       = flag.String("log-file", "", "append the log to this file, rotated at 5 MB (empty = stderr)")
		passwordFile  = flag.String("password-file", "", "read the librarian password from this file instead of BIBLI_ADMIN_PASSWORD")
	)
	flag.Parse()
	allowTrackingLinks(*trackingOffer)

	// A Windows service has no stderr: without a file, its log is lost.
	if *logPath != "" {
		lf, err := openLogFile(*logPath, maxLogSize)
		if err != nil {
			log.Fatalf("log file: %v", err)
		}
		log.SetOutput(lf)
	}

	// Early: the Windows service manager gives up on a service that does not
	// check in within 30 seconds.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	stopped := runAsService(stop)

	// Fail-closed: the application is public, so no password means no start.
	adminPass := os.Getenv("BIBLI_ADMIN_PASSWORD")
	if *passwordFile != "" {
		pw, err := readPasswordFile(*passwordFile)
		if err != nil {
			log.Fatalf("password file: %v", err)
		}
		adminPass = pw
	}
	if adminPass == "" {
		log.Fatal("BIBLI_ADMIN_PASSWORD (or -password-file) required: the application is public and refuses to start without a password")
	}

	// Read before anything opens, so a malformed value stops the binary here.
	resetEvery, err := demoInterval()
	if err != nil {
		log.Fatal(err)
	}

	// A weak shared password on a networked instance is the likeliest way in.
	if err := checkPasswordStrength(adminPass, *addr, resetEvery > 0); err != nil {
		log.Fatal(err)
	}

	db, err := openDB(*dbPath)
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	defer db.Close()

	// Before the templates, which are parsed per language and call T on render.
	if err := loadLocales(); err != nil {
		log.Fatalf("locales: %v", err)
	}

	pages, err := loadTemplates()
	if err != nil {
		log.Fatalf("templates: %v", err)
	}

	secret, err := loadOrCreateSecret(db)
	if err != nil {
		log.Fatalf("session secret: %v", err)
	}

	a := &app{
		db:            db,
		pages:         pages,
		adminPass:     adminPass,
		sessionKey:    deriveKey(secret, adminPass),
		secureCookies: *secureCookies,
		trustProxy:    *trustProxy,
		dbPath:        *dbPath,
		throttle:      newThrottle(),
		covers:        newCoverCache(*cacheDir),
	}

	// Over HTTP the browser drops a Secure cookie with no message: say so here.
	if *secureCookies {
		log.Print("Secure session cookies: Bibli must be served over HTTPS " +
			"(TLS reverse proxy). On a local HTTP network, run with -secure-cookies=false")
		if !*trustProxy {
			log.Print("note: behind a reverse proxy, add -trust-proxy, otherwise every " +
				"login attempt is counted against the same address")
		}
	} else {
		log.Print("session cookies NOT Secure (-secure-cookies=false): reserved for a local network or development")
	}

	loadSettingsCache(db)

	// Without a key, Google Books is rate limited and books go unfound.
	switch {
	case googleKeyFromEnv():
		log.Print("Google Books: API key set by BIBLI_GOOGLE_BOOKS_KEY")
	case googleKey() != "":
		log.Print("Google Books: API key saved in /settings")
	default:
		log.Print("Google Books: no API key, enrichment limited (anonymous quota, 429 errors); add one in /settings")
	}
	// After the cache: the name it writes follows the instance language.
	syncAnonymousName(db)

	// A demonstration keeps no backups: nothing in it outlives the next reset.
	if resetEvery > 0 {
		*backupDir = ""
		log.Printf("DEMONSTRATION MODE (BIBLI_DEMO_RESET): the whole collection is deleted "+
			"and the demonstration dataset loaded back in every %s; backups disabled", resetEvery)
		startDemo(db, resetEvery)
	}

	// After the demonstration override, so a demonstration offers no downloads.
	a.backupDir = *backupDir
	startMaintenance(db, *backupDir)

	srv := &http.Server{
		Addr:        *addr,
		Handler:     a.handler(),
		ReadTimeout: 10 * time.Second,
		// Wide enough for the screens waiting on a catalogue, which also set
		// their own deadline (extendWriteDeadline).
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Let in-flight requests finish before closing the database, otherwise a
	// restart can leave an inconsistent WAL.
	go func() {
		log.Printf("Bibli started on %s (database: %s)", *addr, *dbPath)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server: %v", err)
		}
	}()

	<-stop
	log.Println("shutting down...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("forced shutdown: %v", err)
	}
	// Before reporting the service stopped: Windows may end the process then.
	db.Close()
	log.Println("stopped")
	stopped()
}

// handler is the whole application — routes and middleware — as one
// http.Handler, so routes_test.go can drive the real thing. Logging is outermost
// to see the final status; requireLogin innermost so nothing slips past it.
func (a *app) handler() http.Handler {
	mux := http.NewServeMux()

	// Authentication — public routes
	mux.HandleFunc("GET /login", a.loginScreen)
	mux.HandleFunc("POST /login", a.loginSubmit)
	mux.HandleFunc("POST /logout", a.logout)
	mux.HandleFunc("GET /healthcheck", a.healthcheck)

	// The page is public, protected by the token alone; managing tokens is not.
	mux.HandleFunc("GET /track/{token}", a.trackingScreen)
	mux.HandleFunc("POST /borrowers/{id}/token", a.borrowerTokenCreate)
	mux.HandleFunc("POST /borrowers/{id}/token/revoke", a.borrowerTokenRevoke)

	// Consultation
	mux.HandleFunc("GET /{$}", a.home)
	mux.HandleFunc("GET /loans", a.loans)

	// Lending and returning
	mux.HandleFunc("GET /borrow", a.borrowScreen)
	mux.HandleFunc("POST /borrow/borrower", a.borrowBorrower)
	mux.HandleFunc("POST /borrow/search", a.borrowSearch)
	mux.HandleFunc("POST /borrow/borrower-id", a.borrowBorrowerID)
	mux.HandleFunc("POST /borrow/book-search", a.borrowBookSearch)
	mux.HandleFunc("POST /borrow/add", a.borrowAdd)
	mux.HandleFunc("GET /borrow/lookup", a.borrowLookup)
	mux.HandleFunc("POST /borrow/catalogue", a.borrowCatalogue)
	mux.HandleFunc("POST /borrow/copy", a.borrowAddCopy)
	mux.HandleFunc("POST /borrow/confirm", a.borrowConfirm)
	mux.HandleFunc("GET /return", a.returnScreen)
	mux.HandleFunc("POST /return", a.returnScan)
	mux.HandleFunc("POST /loan/{id}/return", a.loanReturn)
	mux.HandleFunc("POST /loan/{id}/extend", a.loanExtend)

	// Cataloguing
	mux.HandleFunc("GET /catalogue", a.catalogueScreen)
	mux.HandleFunc("GET /catalogue/stream", a.catalogueSearchStream)
	mux.HandleFunc("GET /catalogue/manual", a.catalogueManual)
	mux.HandleFunc("POST /catalogue/save", a.catalogueSave)
	mux.HandleFunc("GET /catalogue/batch", a.batchScreen)
	mux.HandleFunc("POST /catalogue/batch/add", a.batchAdd)
	mux.HandleFunc("POST /catalogue/batch/save", a.batchSave)

	// Borrowers
	mux.HandleFunc("GET /borrowers", a.borrowersScreen)
	mux.HandleFunc("GET /borrowers/{id}", a.borrowerDetail)
	mux.HandleFunc("POST /borrowers/add", a.borrowersAdd)
	mux.HandleFunc("POST /borrowers/search", a.borrowersSearch)
	mux.HandleFunc("GET /borrowers/{id}/edit", a.borrowerEdit)
	mux.HandleFunc("GET /borrowers/{id}/row", a.borrowerRow)
	mux.HandleFunc("POST /borrowers/{id}", a.borrowerUpdate)
	mux.HandleFunc("POST /borrowers/{id}/deactivate", a.borrowersDeactivate)
	mux.HandleFunc("POST /borrowers/{id}/reactivate", a.borrowerReactivate)
	mux.HandleFunc("GET /borrowers/import", a.borrowersImportScreen)
	mux.HandleFunc("POST /borrowers/import", a.borrowersImportPreview)
	mux.HandleFunc("POST /borrowers/import/confirm", a.borrowersImportConfirm)
	mux.HandleFunc("GET /borrowers/import/template.xlsx", a.borrowersImportTemplate)
	mux.HandleFunc("GET /borrowers/cards", a.borrowersCards)
	mux.HandleFunc("GET /borrowers/regroup", a.regroupScreen)
	mux.HandleFunc("POST /borrowers/regroup", a.regroupConfirm)

	// Book page
	mux.HandleFunc("GET /cover/{isbn}", a.cover)
	mux.HandleFunc("GET /book/{id}", a.bookScreen)
	mux.HandleFunc("POST /book/{id}", a.bookSave)
	mux.HandleFunc("POST /book/{id}/enrich", a.bookEnrich)
	mux.HandleFunc("POST /book/{id}/copy", a.bookAddCopy)
	mux.HandleFunc("POST /book/{id}/copy/{copyid}", a.bookCopyStatus)
	mux.HandleFunc("POST /book/{id}/delete", a.bookDelete)
	mux.HandleFunc("POST /copy/{id}/reinstate", a.copyReinstate)

	// Operations
	mux.HandleFunc("GET /inventory", a.inventoryScreen)
	mux.HandleFunc("POST /inventory/search", a.inventorySearch)
	mux.HandleFunc("POST /inventory/{id}", a.inventoryUpdate)
	mux.HandleFunc("GET /print/labels", a.printLabels)
	mux.HandleFunc("GET /print/loans", a.printLoans)
	mux.HandleFunc("GET /print/overdue", a.printLoans) // kept for saved links
	mux.HandleFunc("GET /print/inventory", a.printInventory)
	mux.HandleFunc("GET /export.csv", a.exportCSV)
	// A .csv cannot tell Excel its separator and encoding at once.
	mux.HandleFunc("GET /export.xlsx", a.exportXLSX)

	// Settings
	mux.HandleFunc("GET /stats", a.statsScreen)
	mux.HandleFunc("GET /stats/collection.xlsx", a.reviewXLSX)
	mux.HandleFunc("GET /stats/collection.csv", a.reviewCSV)
	mux.HandleFunc("GET /settings", a.settingsScreen)
	mux.HandleFunc("POST /settings", a.settingsSave)
	mux.HandleFunc("POST /settings/covers/absences/forget", a.settingsForgetAbsences)
	mux.HandleFunc("GET /settings/backup/{name}", a.backupDownload)

	// About
	mux.HandleFunc("GET /about", a.about)
	mux.HandleFunc("GET /build-info.json", a.buildInfo)
	mux.HandleFunc("GET /manifest.webmanifest", a.manifest)

	// Vendored assets, no CDN. Cached long: every URL carries a fingerprint.
	sub, _ := fs.Sub(staticFS, "static")
	files := http.FileServer(http.FS(sub))
	mux.Handle("GET /static/", http.StripPrefix("/static/", longCache(noDirList(files))))

	// Branded 404 for any unknown route; the exact root stays on GET /{$}.
	mux.HandleFunc("/", a.notFoundScreen)

	return logging(securityHeaders(a.rememberLang(crossOriginGuard(a.requireLogin(mux)))))
}

// loadTemplates builds one template set per page, per language: per page
// because every page defines "content" in a shared namespace, per language so
// {{T "key"}} needs no language argument.
func loadTemplates() (map[string]map[string]*template.Template, error) {
	sets := make(map[string]map[string]*template.Template, len(langs))
	for _, lang := range langs {
		set, err := loadTemplatesFor(lang)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", lang, err)
		}
		sets[lang] = set
	}
	return sets, nil
}

// sharedTemplates are parsed into the layout every page is cloned from; none
// defines "content" or "document", so none is a page.
var sharedTemplates = []string{"base", "icons", "filters", "tracking_link", "loan_actions", "timeline", "sheet"}

// loadTemplatesFor parses every page for a single language.
func loadTemplatesFor(lang string) (map[string]*template.Template, error) {
	shared := make([]string, len(sharedTemplates))
	for i, name := range sharedTemplates {
		shared[i] = "templates/" + name + ".html"
	}
	base, err := template.New("base.html").Funcs(templateFuncs(lang)).ParseFS(templatesFS, shared...)
	if err != nil {
		return nil, err
	}

	files, err := fs.Glob(templatesFS, "templates/*.html")
	if err != nil {
		return nil, err
	}

	pages := make(map[string]*template.Template)
	for _, path := range files {
		name := strings.TrimSuffix(filepath.Base(path), ".html")
		if slices.Contains(sharedTemplates, name) {
			continue
		}
		clone, err := base.Clone()
		if err != nil {
			return nil, err
		}
		if _, err := clone.ParseFS(templatesFS, path); err != nil {
			return nil, err
		}
		pages[name] = clone
	}
	return pages, nil
}

// ANSI colours for the development log, only with BIBLI_LOG_COLOR=1 on a
// terminal: escape codes in a journal are noise.
const (
	ansiReset  = "\033[0m"
	ansiDim    = "\033[2m"
	ansiRed    = "\033[31m"
	ansiGreen  = "\033[32m"
	ansiYellow = "\033[33m"
)

// Read once: log writes to stderr, which does not change under us.
var logColour = wantsLogColour()

func wantsLogColour() bool {
	if os.Getenv("BIBLI_LOG_COLOR") != "1" {
		return false
	}
	info, err := os.Stderr.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func colour(code, s string) string {
	if !logColour {
		return s
	}
	return code + s + ansiReset
}

// Green is ordinary, yellow is the caller's mistake, red is ours.
func statusColour(status int) string {
	switch {
	case status >= 500:
		return ansiRed
	case status >= 400:
		return ansiYellow
	case status >= 300:
		return ansiDim
	default:
		return ansiGreen
	}
}

// statusRecorder remembers the status for the log line. It must forward Flush
// and Unwrap, or the cataloguing stream is never flushed and cannot set its
// write deadline.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (w *statusRecorder) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusRecorder) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *statusRecorder) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		took := time.Since(start)

		status := rec.status
		if status == 0 {
			status = http.StatusOK // a body written without an explicit header
		}
		path := r.URL.Path
		if strings.HasPrefix(path, "/track/") {
			path = "/track/[token]"
		}

		// Asset fetches are dimmed whole: the page request already said it.
		if strings.HasPrefix(path, "/static/") {
			log.Print(colour(ansiDim, fmt.Sprintf("%3d %s %s (%s)", status, r.Method, path, took)))
			return
		}
		log.Printf("%s %s %s %s",
			colour(statusColour(status), fmt.Sprintf("%3d", status)),
			r.Method, path, colour(ansiDim, fmt.Sprintf("(%s)", took)))
	})
}

func longCache(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		next.ServeHTTP(w, r)
	})
}

// noDirList turns http.FileServer's directory index off: a path ending in "/"
// (or the bare root) would otherwise list the folder. Only files are served.
func noDirList(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "" || strings.HasSuffix(r.URL.Path, "/") {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}
