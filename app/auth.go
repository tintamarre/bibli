package main

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"log"
	"math"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Librarian authentication: one shared password, an HMAC-signed cookie, no
// server-side state. Everything but isPublicPath sits behind requireLogin.

const (
	sessionCookieName = "bibli_session"
	sessionDuration   = 12 * time.Hour // one duty period
	// Past this age, an authenticated request sets a fresh cookie.
	sessionRenewal = time.Hour
	// Counted from the sign-in, renewal or not: a copied cookie dies within a
	// week, and a desk never meets it, since each night's idle hours sign it out.
	sessionLifetime = 7 * 24 * time.Hour
)

// Stored rather than random per start, so sessions survive a restart.
func loadOrCreateSecret(db *sql.DB) (string, error) {
	var s string
	err := db.QueryRow(`SELECT value FROM setting WHERE key = 'session_secret'`).Scan(&s)
	if err == nil && s != "" {
		return s, nil
	}
	if err != nil && err != sql.ErrNoRows {
		return "", err
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	s = hex.EncodeToString(b)
	if _, err := db.Exec(
		`INSERT INTO setting (key, value, label) VALUES
		 ('session_secret', ?, 'Session-signing secret (generated automatically)')`, s,
	); err != nil {
		return "", err
	}
	return s, nil
}

// deriveKey mixes the stored secret with the password: changing
// BIBLI_ADMIN_PASSWORD invalidates every session (everyone is signed out).
func deriveKey(secretHex, password string) []byte {
	h := sha256.Sum256([]byte(secretHex + password))
	return h[:]
}

// session is what the cookie proves: when the password was typed, and when the
// cookie was last signed. Renewal moves the second, never the first.
type session struct {
	issued, signed time.Time
}

// Format: base64url(issued "-" signed, Unix seconds) "." hex(HMAC(key, payload)).
func signSession(key []byte, issued, now time.Time) string {
	payload := strconv.FormatInt(issued.Unix(), 10) + "-" + strconv.FormatInt(now.Unix(), 10)
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + hex.EncodeToString(mac.Sum(nil))
}

// readSession validates the signature in constant time, then both clocks:
// idle since the last signature, and the absolute cap since the sign-in.
func readSession(key []byte, value string, now time.Time) (session, bool) {
	parts := strings.SplitN(value, ".", 2)
	if len(parts) != 2 {
		return session{}, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return session{}, false
	}
	sig, err := hex.DecodeString(parts[1])
	if err != nil {
		return session{}, false
	}
	mac := hmac.New(sha256.New, key)
	mac.Write(payload)
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return session{}, false
	}
	issuedS, signedS, ok := strings.Cut(string(payload), "-")
	if !ok {
		return session{}, false
	}
	issued, err1 := strconv.ParseInt(issuedS, 10, 64)
	signed, err2 := strconv.ParseInt(signedS, 10, 64)
	if err1 != nil || err2 != nil || signed < issued {
		return session{}, false
	}
	s := session{issued: time.Unix(issued, 0), signed: time.Unix(signed, 0)}
	idle, age := now.Sub(s.signed), now.Sub(s.issued)
	return s, idle >= 0 && idle <= sessionDuration && age <= sessionLifetime
}

func (a *app) requestSession(r *http.Request) (session, bool) {
	c, err := r.Cookie(sessionCookieName)
	if err != nil {
		return session{}, false
	}
	return readSession(a.sessionKey, c.Value, time.Now())
}

func (a *app) validSession(r *http.Request) bool {
	_, ok := a.requestSession(r)
	return ok
}

// setSessionCookie signs a session that began at issued. The browser is told
// to drop it at whichever limit comes first.
func (a *app) setSessionCookie(w http.ResponseWriter, issued time.Time) {
	now := time.Now()
	maxAge := min(sessionDuration, issued.Add(sessionLifetime).Sub(now))
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    signSession(a.sessionKey, issued, now),
		Path:     "/",
		HttpOnly: true,
		Secure:   a.secureCookies,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   max(int(maxAge.Seconds()), 1),
	})
}

func (a *app) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   a.secureCookies,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

func isPublicPath(path string) bool {
	switch {
	case path == "/", path == "/login", path == "/healthcheck", path == "/manifest.webmanifest":
		return true
	case path == "/logout":
		// Signing out of an expired session must not detour through /login.
		return true
	case strings.HasPrefix(path, "/family/"):
		return true
	case strings.HasPrefix(path, "/static/"):
		return true
	}
	return false
}

func (a *app) requireLogin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isPublicPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		if s, ok := a.requestSession(r); ok {
			// Sliding session, so a duty period is not cut off mid-basket.
			if time.Since(s.signed) > sessionRenewal {
				a.setSessionCookie(w, s.issued)
			}
			next.ServeHTTP(w, r)
			return
		}
		if strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
			// EventSource cannot usefully follow a redirect to an HTML page.
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Header.Get("HX-Request") == "true" {
			w.Header().Set("HX-Redirect", "/login")
			w.WriteHeader(http.StatusOK)
			return
		}
		http.Redirect(w, r, "/login?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusSeeOther)
	})
}

// crossOriginGuard refuses every non-safe request another origin sends, which
// SameSite=Lax lets through from any sibling subdomain. A browser without
// Sec-Fetch-Site is judged on Origin against Host, so a proxy must pass Host on.
func crossOriginGuard(next http.Handler) http.Handler {
	p := http.NewCrossOriginProtection()
	p.SetDenyHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, tr(r, "error.cross_origin"), http.StatusForbidden)
	}))
	return p.Handler(next)
}

// contentSecurityPolicy names no host but our own: everything is vendored.
// Scripts keep 'unsafe-inline' for the onclick handlers and the inline blocks
// that carry per-page data, and 'unsafe-eval' for HTMX's hx-on attributes;
// styles keep it for style attributes and HTMX's indicator rule.
const contentSecurityPolicy = "default-src 'self'; " +
	"script-src 'self' 'unsafe-inline' 'unsafe-eval'; " +
	"style-src 'self' 'unsafe-inline'; " +
	"object-src 'none'; base-uri 'self'; frame-ancestors 'none'; form-action 'self'"

// securityHeaders also forbids indexing and caching of the secret pages.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", contentSecurityPolicy)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		if !strings.HasPrefix(r.URL.Path, "/static/") {
			// Pupil data: "Back" after signing out must not show it from cache.
			h.Set("Cache-Control", "no-store")
		}
		if r.URL.Path == "/login" || strings.HasPrefix(r.URL.Path, "/family/") {
			h.Set("X-Robots-Tag", "noindex, nofollow")
		}
		next.ServeHTTP(w, r)
	})
}

// healthcheck discloses nothing, and queries the database: a live process whose
// SQLite file is missing, locked or on a full disk is not healthy.
func (a *app) healthcheck(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	var n int
	if err := a.db.QueryRowContext(ctx, `SELECT 1`).Scan(&n); err != nil {
		log.Printf("healthcheck: %v", err)
		http.Error(w, tr(r, "error.unavailable"), http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write([]byte("ok"))
}

func (a *app) loginScreen(w http.ResponseWriter, r *http.Request) {
	if a.validSession(r) {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	a.renderDoc(w, r, "login", map[string]any{
		"Next":  sanitizeNext(r.URL.Query().Get("next")),
		"Error": "",
	})
}

func (a *app) loginSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		badRequest(w, r)
		return
	}
	next := sanitizeNext(r.FormValue("next"))
	ip := clientIP(r, a.trustProxy)
	now := time.Now()

	if wait := a.throttle.held(ip, now); wait > 0 {
		time.Sleep(time.Second)
		minutes := int((wait + time.Minute - 1) / time.Minute) // rounded up: never "0 minutes"
		a.renderDoc(w, r, "login", map[string]any{
			"Next":  next,
			"Error": trn(r, "login.too_many_attempts", minutes),
		})
		return
	}

	// A distributed guess slips past the per-IP hold; the global gate slows it
	// for everyone, at the cost of a couple of seconds for a real sign-in.
	if d := a.throttle.globalDelay(now); d > 0 {
		time.Sleep(d)
	}

	pass := r.FormValue("password")
	if subtle.ConstantTimeCompare([]byte(pass), []byte(a.adminPass)) == 1 {
		// A Secure cookie over HTTP is dropped silently and the login screen
		// loops: say so rather than redirect.
		if a.secureCookies && !isHTTPS(r) {
			a.renderDoc(w, r, "login", map[string]any{
				"Next":  next,
				"Error": tr(r, "login.http_warning"),
			})
			return
		}
		a.throttle.success(ip)
		a.setSessionCookie(w, time.Now())
		http.Redirect(w, r, next, http.StatusSeeOther)
		return
	}

	a.throttle.failure(ip, now)
	a.throttle.globalFailure(now)
	time.Sleep(500 * time.Millisecond) // slows brute force and smooths the timing
	a.renderDoc(w, r, "login", map[string]any{
		"Next":  next,
		"Error": tr(r, "login.wrong_password"),
	})
}

func (a *app) logout(w http.ResponseWriter, r *http.Request) {
	a.clearSessionCookie(w)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// sanitizeNext allows only a local path, which guards against open redirects:
// it must start with a single "/" and carry no control characters.
func sanitizeNext(s string) string {
	if s == "" || !strings.HasPrefix(s, "/") || strings.HasPrefix(s, "//") {
		return "/"
	}
	if strings.ContainsAny(s, "\\\r\n\t ") {
		return "/"
	}
	return s
}

// clientIP identifies the client for counting sign-in attempts. Behind a proxy
// RemoteAddr is the same for everyone, so under -trust-proxy the LAST
// X-Forwarded-For entry is taken: the one our proxy appended. Every entry
// before it was written by the client and can be anything.
func clientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if v := r.Header.Values("X-Forwarded-For"); len(v) > 0 {
			last := v[len(v)-1]
			if i := strings.LastIndexByte(last, ','); i >= 0 {
				last = last[i+1:]
			}
			if ip := strings.TrimSpace(last); ip != "" {
				return ip
			}
		}
	}
	if h, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return h
	}
	return r.RemoteAddr
}

// X-Forwarded-Proto is believed without -trust-proxy: it only decides a help
// message, never an authorisation.
func isHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	proto := r.Header.Get("X-Forwarded-Proto")
	if i := strings.IndexByte(proto, ','); i >= 0 {
		proto = proto[:i] // the first link in the chain is the client's
	}
	return strings.EqualFold(strings.TrimSpace(proto), "https")
}

// minPasswordLength is the shortest admin password Bibli accepts when it can be
// reached over a network. One shared password protects the whole application,
// so a short one falls to an online guess; a passphrase of a few words clears
// this easily. Not enforced on a localhost-only or demonstration instance.
const minPasswordLength = 12

// listensNetworkWide is false when addr binds the loopback interface alone
// (127.0.0.1, ::1, localhost), where nothing off the machine can reach Bibli.
func listensNetworkWide(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr // no port, e.g. ":8080" already returned "" as host above
	}
	switch host {
	case "", "0.0.0.0", "::":
		return true // every interface
	case "127.0.0.1", "::1", "localhost":
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		return !ip.IsLoopback()
	}
	return true // a hostname we cannot resolve here: assume reachable
}

// checkPasswordStrength refuses a weak password before the server starts, but
// only where it matters: a networked, non-demonstration instance.
func checkPasswordStrength(password, addr string, demo bool) error {
	if demo || !listensNetworkWide(addr) {
		return nil
	}
	if len(password) < minPasswordLength {
		return fmt.Errorf(
			"BIBLI_ADMIN_PASSWORD is too short (%d characters): a network instance needs at least %d. "+
				"A passphrase of three or four words is easy to remember, e.g. la-chouette-lit-la-nuit",
			len(password), minPasswordLength)
	}
	return nil
}

// Anti-brute-force throttle: best effort, in memory, per IP.
const (
	maxLoginAttempts = 5
	// Each further group of failures doubles the hold, up to the cap.
	throttleHold    = time.Minute
	maxThrottleHold = 16 * time.Minute
	// A count is forgotten after this long without a failure.
	throttleMemory = 30 * time.Minute
	// Past this many entries, expired counters are purged.
	maxThrottleEntries = 1000

	// Global budget: the per-IP hold is defeated by rotating addresses (an
	// IPv6 /64 is free), so a second, address-blind gate slows every sign-in
	// once failures pile up across the whole instance. globalRate decays with a
	// half-life of about globalWindow; past globalBudget, each attempt waits.
	globalWindow  = 10 * time.Minute
	globalBudget  = 50
	globalPenalty = 2 * time.Second
)

type throttle struct {
	mu sync.Mutex
	m  map[string]*attempts

	// Decaying count of failures across all addresses, and when it last moved.
	globalRate   float64
	globalStamp  time.Time
	globalWarned bool
}

type attempts struct {
	n     int       // failures since this address last signed in
	last  time.Time // most recent failure
	until time.Time // held until this instant
}

func newThrottle() *throttle { return &throttle{m: make(map[string]*attempts)} }

// holdFor doubles with every group of maxLoginAttempts failures, capped.
func holdFor(holds int) time.Duration {
	d := throttleHold
	for i := 1; i < holds && d < maxThrottleHold; i++ {
		d *= 2
	}
	if d > maxThrottleHold {
		return maxThrottleHold
	}
	return d
}

// held is how long the address must still wait, zero if it may try now.
func (t *throttle) held(ip string, now time.Time) time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	c := t.m[ip]
	if c == nil || !now.Before(c.until) {
		return 0
	}
	return c.until.Sub(now)
}

// Call under lock. The instant is a parameter so this stays testable.
func (t *throttle) current(ip string, now time.Time) *attempts {
	c := t.m[ip]
	if c == nil || now.Sub(c.last) > throttleMemory {
		c = &attempts{}
		t.m[ip] = c
	}
	return c
}

// Call under lock. A held address always survives the purge, or rotating
// addresses would free it.
func (t *throttle) purge(now time.Time) {
	if len(t.m) <= maxThrottleEntries {
		return
	}
	for ip, c := range t.m {
		if !now.Before(c.until) {
			delete(t.m, ip)
		}
	}
}

func (t *throttle) failure(ip string, now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.purge(now)
	c := t.current(ip, now)
	c.n++
	c.last = now
	if c.n%maxLoginAttempts == 0 {
		c.until = now.Add(holdFor(c.n / maxLoginAttempts))
	}
}

func (t *throttle) success(ip string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.m, ip)
}

// decayGlobal ages globalRate toward zero: exponential decay with a time
// constant of globalWindow, so a burst fades over the following minutes.
func (t *throttle) decayGlobal(now time.Time) {
	if t.globalStamp.IsZero() {
		t.globalStamp = now
		return
	}
	if elapsed := now.Sub(t.globalStamp); elapsed > 0 {
		t.globalRate *= math.Exp(-elapsed.Seconds() / globalWindow.Seconds())
		t.globalStamp = now
	}
	if t.globalRate < float64(globalBudget) {
		t.globalWarned = false
	}
}

// globalFailure adds one to the address-blind counter.
func (t *throttle) globalFailure(now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.decayGlobal(now)
	t.globalRate++
	if t.globalRate > float64(globalBudget) && !t.globalWarned {
		log.Printf("login: %.0f recent failures across many addresses — every sign-in is now slowed", t.globalRate)
		t.globalWarned = true
	}
}

// globalDelay is how long the next sign-in must wait because the whole instance
// is under a guessing storm, zero when it is not.
func (t *throttle) globalDelay(now time.Time) time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.decayGlobal(now)
	if t.globalRate <= float64(globalBudget) {
		return 0
	}
	return globalPenalty
}
