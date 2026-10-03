package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestGenerateToken(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 500; i++ {
		tok, err := generateToken()
		if err != nil {
			t.Fatalf("generateToken: %v", err)
		}
		if len(tok) < 22 {
			t.Errorf("token too short: %q", tok)
		}
		for _, c := range tok {
			ok := (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' || c == '_'
			if !ok {
				t.Errorf("non URL-safe character %q in %q", c, tok)
			}
		}
		if seen[tok] {
			t.Fatalf("token collision: %q", tok)
		}
		seen[tok] = true
	}
}

func TestSessionSignVerify(t *testing.T) {
	key := []byte("test-key-0123456789abcdef01234567")
	now := time.Unix(1_700_000_000, 0)

	v := signSession(key, now, now)
	if !verifySession(key, v, now) {
		t.Fatal("a fresh session should be valid")
	}
	// Wrong key.
	if verifySession([]byte("wrong-key"), v, now) {
		t.Error("wrong key accepted")
	}
	// Idle past the duty period.
	if verifySession(key, v, now.Add(sessionDuration+time.Second)) {
		t.Error("expired session accepted")
	}
	// Cookie from the future.
	if verifySession(key, v, now.Add(-time.Hour)) {
		t.Error("session from the future accepted")
	}
	// Forged signature.
	forged := v[:len(v)-1]
	if v[len(v)-1] == '0' {
		forged += "1"
	} else {
		forged += "0"
	}
	if verifySession(key, forged, now) {
		t.Error("forged signature accepted")
	}
	// Malformed.
	if verifySession(key, "nodot", now) {
		t.Error("malformed value accepted")
	}
}

func verifySession(key []byte, value string, now time.Time) bool {
	_, ok := readSession(key, value, now)
	return ok
}

// The idle expiry slides; the cap does not, however often the cookie is used.
func TestASessionEndsAWeekAfterSignInDespiteRenewal(t *testing.T) {
	key := []byte("test-key-0123456789abcdef01234567")
	signIn := time.Unix(1_700_000_000, 0)

	// Renewed every hour, as a request past sessionRenewal would.
	cookie := signSession(key, signIn, signIn)
	at := signIn
	for at.Before(signIn.Add(sessionLifetime - time.Hour)) {
		at = at.Add(sessionRenewal + time.Minute)
		s, ok := readSession(key, cookie, at)
		if !ok {
			t.Fatalf("a session renewed hourly died after %s, before the cap", at.Sub(signIn))
		}
		if !s.issued.Equal(signIn) {
			t.Fatalf("renewal moved the sign-in time to %s", s.issued)
		}
		cookie = signSession(key, s.issued, at)
	}
	// Signed a moment ago, but the week is up.
	past := signIn.Add(sessionLifetime + time.Second)
	fresh := signSession(key, signIn, past.Add(-time.Minute))
	if verifySession(key, fresh, past) {
		t.Error("a session renewed a minute ago outlived its week")
	}
}

// Renewal carries the original sign-in forward, through the real cookie code.
func TestRenewalKeepsTheOriginalSignInTime(t *testing.T) {
	a := &app{sessionKey: deriveKey("secret", "password")}
	signIn := time.Now().Add(-3 * 24 * time.Hour)
	w := httptest.NewRecorder()
	a.setSessionCookie(w, signIn)
	c := readCookie(t, w, sessionCookieName)

	s, ok := readSession(a.sessionKey, c.Value, time.Now())
	if !ok {
		t.Fatal("a renewed three-day-old session is not valid")
	}
	if s.issued.Unix() != signIn.Unix() {
		t.Errorf("issued = %s, want the original sign-in %s", s.issued, signIn)
	}
	if time.Since(s.signed) > time.Minute {
		t.Errorf("signed = %s, want now", s.signed)
	}

	// Near the cap, the browser is told to drop it at the cap, not twelve hours on.
	w = httptest.NewRecorder()
	a.setSessionCookie(w, time.Now().Add(-sessionLifetime+time.Hour))
	if got := readCookie(t, w, sessionCookieName).MaxAge; got > 3600 || got < 3500 {
		t.Errorf("MaxAge an hour before the cap = %d, want about 3600", got)
	}
}

// Rewriting either clock, or presenting a cookie of the old format, fails.
func TestATamperedSessionIsRefused(t *testing.T) {
	key := []byte("test-key-0123456789abcdef01234567")
	signIn := time.Unix(1_700_000_000, 0)
	now := signIn.Add(sessionLifetime - time.Hour)
	v := signSession(key, signIn, now)
	sig := strings.SplitN(v, ".", 2)[1]

	stamp := func(issued, signed time.Time) string {
		return base64.RawURLEncoding.EncodeToString([]byte(
			strconv.FormatInt(issued.Unix(), 10) + "-" + strconv.FormatInt(signed.Unix(), 10)))
	}
	later := now.Add(2 * time.Hour)
	for name, value := range map[string]string{
		"sign-in moved later":   stamp(signIn.Add(time.Hour), now) + "." + sig,
		"signature time bumped": stamp(signIn, now.Add(time.Minute)) + "." + sig,
	} {
		if verifySession(key, value, now) {
			t.Errorf("%s: accepted with the old signature", name)
		}
	}
	// Past the cap the real cookie is dead, and a forged later sign-in stays dead.
	if verifySession(key, stamp(signIn.Add(3*time.Hour), later)+"."+sig, later) {
		t.Error("a forged sign-in time extended the session")
	}

	// The format before the cap: one timestamp, correctly signed. Refused.
	ts := strconv.FormatInt(now.Unix(), 10)
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(ts))
	old := base64.RawURLEncoding.EncodeToString([]byte(ts)) + "." + hex.EncodeToString(mac.Sum(nil))
	if verifySession(key, old, now) {
		t.Error("an old-format cookie opened a session")
	}

	// A validly signed payload whose clocks run backwards.
	bad := strconv.FormatInt(now.Unix(), 10) + "-" + strconv.FormatInt(signIn.Unix(), 10)
	mac = hmac.New(sha256.New, key)
	mac.Write([]byte(bad))
	if verifySession(key, base64.RawURLEncoding.EncodeToString([]byte(bad))+"."+hex.EncodeToString(mac.Sum(nil)), now) {
		t.Error("a session signed before it was issued was accepted")
	}
}

func TestSanitizeNext(t *testing.T) {
	cases := map[string]string{
		"/borrowers":     "/borrowers",
		"/catalogue?x=1": "/catalogue?x=1",
		"":               "/",
		"//evil.com":     "/",
		"https://evil":   "/",
		"javascript:x":   "/",
		"/a b":           "/",
		"/a\nb":          "/",
	}
	for in, want := range cases {
		if got := sanitizeNext(in); got != want {
			t.Errorf("sanitizeNext(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLastNameInitial(t *testing.T) {
	cases := map[string]string{
		"Durant":     "D.",
		"du pont":    "D.",
		"  Martin  ": "M.",
		"éric":       "É.",
		"":           "",
		"D.":         "D.",
	}
	for in, want := range cases {
		if got := lastNameInitial(in); got != want {
			t.Errorf("lastNameInitial(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestThrottle(t *testing.T) {
	t0 := time.Unix(1_700_000_000, 0)
	th := newThrottle()

	// Four failures: still allowed. The fifth holds the address for a minute.
	for i := 0; i < maxLoginAttempts-1; i++ {
		th.failure("10.0.0.1", t0)
	}
	if th.held("10.0.0.1", t0) != 0 {
		t.Fatalf("held after only %d failures", maxLoginAttempts-1)
	}
	th.failure("10.0.0.1", t0)
	if got := th.held("10.0.0.1", t0); got != throttleHold {
		t.Fatalf("held for %v after %d failures, want %v", got, maxLoginAttempts, throttleHold)
	}

	// Another address is unaffected.
	if th.held("10.0.0.2", t0) != 0 {
		t.Error("a third-party IP must not be held")
	}

	// The hold expires, and the next five failures hold twice as long.
	t1 := t0.Add(throttleHold + time.Second)
	if th.held("10.0.0.1", t1) != 0 {
		t.Fatal("the hold must expire")
	}
	for i := 0; i < maxLoginAttempts; i++ {
		th.failure("10.0.0.1", t1)
	}
	if got := th.held("10.0.0.1", t1); got != 2*throttleHold {
		t.Errorf("second hold %v, want %v", got, 2*throttleHold)
	}

	// A successful sign-in clears the count.
	th.success("10.0.0.1")
	if th.held("10.0.0.1", t1) != 0 {
		t.Error("a success must reset the counter")
	}

	// So does half an hour without a failure, back to a one-minute hold.
	th2 := newThrottle()
	for i := 0; i < maxLoginAttempts; i++ {
		th2.failure("10.0.0.3", t0)
	}
	t2 := t0.Add(throttleMemory + time.Minute)
	for i := 0; i < maxLoginAttempts; i++ {
		th2.failure("10.0.0.3", t2)
	}
	if got := th2.held("10.0.0.3", t2); got != throttleHold {
		t.Errorf("after the count was forgotten: %v, want %v", got, throttleHold)
	}
}

func TestHoldFor(t *testing.T) {
	cases := map[int]time.Duration{
		1: throttleHold,
		2: 2 * throttleHold,
		3: 4 * throttleHold,
		9: maxThrottleHold,
	}
	for holds, want := range cases {
		if got := holdFor(holds); got != want {
			t.Errorf("holdFor(%d) = %v, want %v", holds, got, want)
		}
	}
}

// The map must not grow forever, and a held address must survive the purge.
func TestThrottlePurge(t *testing.T) {
	t0 := time.Unix(1_700_000_000, 0)
	th := newThrottle()
	for i := 0; i < maxLoginAttempts; i++ {
		th.failure("held", t0)
	}
	if th.held("held", t0) == 0 {
		t.Fatal("the address under test is not held")
	}
	for i := 0; i <= maxThrottleEntries; i++ {
		th.failure(strconv.Itoa(i), t0)
	}
	th.failure("trigger", t0)
	// What is left is the held address and the entries added since the purge.
	if len(th.m) > 10 {
		t.Errorf("counters not purged: %d entries left", len(th.m))
	}
	if th.held("held", t0) == 0 {
		t.Error("a held address was dropped by the purge")
	}
}

func TestClientIP(t *testing.T) {
	r := httptest.NewRequest("POST", "/login", nil)
	r.RemoteAddr = "127.0.0.1:54321"
	r.Header.Set("X-Forwarded-For", "203.0.113.7, 10.0.0.1")

	// Without -trust-proxy the header is ignored.
	if got := clientIP(r, false); got != "127.0.0.1" {
		t.Errorf("without trust-proxy: %q, want 127.0.0.1", got)
	}
	// With it, the entry our proxy appended: the last one.
	if got := clientIP(r, true); got != "10.0.0.1" {
		t.Errorf("with trust-proxy: %q, want 10.0.0.1", got)
	}
	// A client that sends a new address on every attempt, in front of a proxy
	// that appends the real one, is still counted under the real one.
	for _, forged := range []string{"1.1.1.1", "2.2.2.2", "3.3.3.3, 4.4.4.4"} {
		r.Header.Set("X-Forwarded-For", forged+", 198.51.100.4")
		if got := clientIP(r, true); got != "198.51.100.4" {
			t.Errorf("forged %q: counted as %q, want 198.51.100.4", forged, got)
		}
	}
	// Several header lines: the proxy's is the last.
	r.Header.Set("X-Forwarded-For", "1.1.1.1")
	r.Header.Add("X-Forwarded-For", "198.51.100.4")
	if got := clientIP(r, true); got != "198.51.100.4" {
		t.Errorf("two header lines: %q, want 198.51.100.4", got)
	}
	// X-Real-IP is not believed: a proxy that sets only it passes a forged
	// X-Forwarded-For through untouched, so neither can be trusted there.
	r.Header.Del("X-Forwarded-For")
	r.Header.Set("X-Real-IP", "198.51.100.9")
	if got := clientIP(r, true); got != "127.0.0.1" {
		t.Errorf("RemoteAddr fallback: %q, want 127.0.0.1", got)
	}
}

func TestIsHTTPS(t *testing.T) {
	r := httptest.NewRequest("POST", "/login", nil)
	if isHTTPS(r) {
		t.Error("bare HTTP request taken for HTTPS")
	}
	r.Header.Set("X-Forwarded-Proto", "https")
	if !isHTTPS(r) {
		t.Error("X-Forwarded-Proto: https not recognised")
	}
	r.Header.Set("X-Forwarded-Proto", "https, http")
	if !isHTTPS(r) {
		t.Error("first hop https not recognised")
	}
	r.Header.Set("X-Forwarded-Proto", "http")
	if isHTTPS(r) {
		t.Error("X-Forwarded-Proto: http taken for HTTPS")
	}
}

// Everything but the public allow list requires a session; a path slipping into it
// opens the library's data to the Internet.
func TestIsPublicPath(t *testing.T) {
	public := []string{
		"/", "/login", "/logout", "/healthcheck",
		"/static/app.js", "/static/vendor/htmx.min.js",
		"/track/AbCdEf0123456789012345",
	}
	for _, p := range public {
		if !isPublicPath(p) {
			t.Errorf("%s should be public", p)
		}
	}
	private := []string{
		"/borrowers", "/borrow", "/return", "/catalogue", "/inventory",
		"/settings", "/book/1", "/export.csv", "/export.xlsx", "/print/overdue", "/print/loans",
		"/loans", "/about",
		// Neighbours of a public prefix, which must not inherit it.
		"/staticfiles", "/track", "/logout/all", "/login2",
	}
	for _, p := range private {
		if isPublicPath(p) {
			t.Errorf("%s must require a session", p)
		}
	}
}

// Changing BIBLI_ADMIN_PASSWORD signs everyone out.
func TestChangingThePasswordInvalidatesTheSessions(t *testing.T) {
	const secret = "0123456789abcdef0123456789abcdef"
	now := time.Unix(1_700_000_000, 0)

	before := deriveKey(secret, "old-password")
	cookie := signSession(before, now, now)
	if !verifySession(before, cookie, now) {
		t.Fatal("a session signed with the current key does not verify")
	}

	after := deriveKey(secret, "new-password")
	if verifySession(after, cookie, now) {
		t.Error("a session survived the password change")
	}
	// The secret alone is not the key: instances sharing a password do not share
	// sessions.
	if verifySession(deriveKey("another-secret", "old-password"), cookie, now) {
		t.Error("a session survived the secret changing")
	}
}

// The secret is stored, so a restart does not sign the desk out.
func TestSecretSurvivesARestart(t *testing.T) {
	db := testDB(t)

	first, err := loadOrCreateSecret(db)
	if err != nil {
		t.Fatalf("loadOrCreateSecret: %v", err)
	}
	if len(first) < 32 {
		t.Errorf("secret too short: %q", first)
	}
	second, err := loadOrCreateSecret(db)
	if err != nil {
		t.Fatalf("loadOrCreateSecret (second call): %v", err)
	}
	if second != first {
		t.Errorf("a second start drew a new secret: %q then %q", first, second)
	}
}

// Past an hour of use, an authenticated request gets a fresh cookie.
func TestSessionAgeReportsHowOldTheCookieIs(t *testing.T) {
	key := []byte("test-key-0123456789abcdef01234567")
	now := time.Unix(1_700_000_000, 0)
	cookie := signSession(key, now, now)

	later := now.Add(90 * time.Minute)
	s, ok := readSession(key, cookie, later)
	if !ok {
		t.Fatal("a 90-minute-old session is not valid")
	}
	if age := later.Sub(s.signed); age < sessionRenewal {
		t.Errorf("age = %s, want at least %s so the cookie is renewed", age, sessionRenewal)
	}
	if verifySession(key, cookie, now.Add(13*time.Hour)) {
		t.Error("a session past its 12 hours is still valid")
	}
	// A garbled value is refused rather than read as a zero age.
	for _, bad := range []string{"", ".", "notbase64.00", "AAAA.nothex"} {
		if verifySession(key, bad, now) {
			t.Errorf("malformed cookie accepted: %q", bad)
		}
	}
}

// HttpOnly, Secure and SameSite=Lax.
func TestSessionCookieCarriesItsProtections(t *testing.T) {
	a := &app{sessionKey: deriveKey("secret", "password"), secureCookies: true}

	w := httptest.NewRecorder()
	a.setSessionCookie(w, time.Now())
	c := readCookie(t, w, sessionCookieName)
	if !c.HttpOnly {
		t.Error("cookie not HttpOnly: a script could read the session")
	}
	if !c.Secure {
		t.Error("cookie not Secure while -secure-cookies is on")
	}
	if c.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite = %v, want Lax", c.SameSite)
	}
	if c.MaxAge != int(sessionDuration.Seconds()) {
		t.Errorf("MaxAge = %d, want %d", c.MaxAge, int(sessionDuration.Seconds()))
	}

	// The request that carries it back is recognised as signed in.
	r := httptest.NewRequest("GET", "/borrowers", nil)
	r.AddCookie(c)
	if !a.validSession(r) {
		t.Error("the cookie just set does not open a session")
	}

	// Signing out expires the cookie rather than blanking it.
	w = httptest.NewRecorder()
	a.clearSessionCookie(w)
	if c := readCookie(t, w, sessionCookieName); c.MaxAge >= 0 || c.Value != "" {
		t.Errorf("logout cookie: value %q, MaxAge %d — want empty and negative", c.Value, c.MaxAge)
	}
}

// -secure-cookies=false drops the Secure flag for a local HTTP network.
func TestSecureCookiesCanBeTurnedOff(t *testing.T) {
	a := &app{sessionKey: deriveKey("secret", "password"), secureCookies: false}
	w := httptest.NewRecorder()
	a.setSessionCookie(w, time.Now())
	if readCookie(t, w, sessionCookieName).Secure {
		t.Error("cookie still Secure with -secure-cookies=false")
	}
}

// A request with no cookie, or one signed by someone else, is not a session.
func TestNoCookieIsNoSession(t *testing.T) {
	a := &app{sessionKey: deriveKey("secret", "password"), secureCookies: true}

	if a.validSession(httptest.NewRequest("GET", "/borrowers", nil)) {
		t.Error("a request with no cookie opened a session")
	}
	r := httptest.NewRequest("GET", "/borrowers", nil)
	r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: signSession([]byte("someone else"), time.Now(), time.Now())})
	if a.validSession(r) {
		t.Error("a cookie signed elsewhere opened a session")
	}
}

func readCookie(t *testing.T, w *httptest.ResponseRecorder, name string) *http.Cookie {
	t.Helper()
	for _, c := range w.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("cookie %s not set", name)
	return nil
}

// The global gate slows every sign-in once failures pile up across many
// addresses, which is what defeats a guess that rotates IPs. It decays.
func TestGlobalFailureBudget(t *testing.T) {
	t0 := time.Unix(1_700_000_000, 0)
	th := newThrottle()

	// Under the budget: no global delay, whatever the address.
	for i := 0; i < globalBudget; i++ {
		th.globalFailure(t0)
	}
	if d := th.globalDelay(t0); d != 0 {
		t.Fatalf("global delay %v at the budget, want 0", d)
	}

	// One past the budget: every attempt now waits, even from a fresh address.
	th.globalFailure(t0)
	if d := th.globalDelay(t0); d != globalPenalty {
		t.Fatalf("global delay %v past the budget, want %v", d, globalPenalty)
	}

	// It fades: after several windows the rate has decayed well below budget.
	later := t0.Add(6 * globalWindow)
	if d := th.globalDelay(later); d != 0 {
		t.Errorf("global delay %v long after the storm, want 0", d)
	}
}

// A weak password stops a networked start, but not a localhost-only one or a
// demonstration, where the password is public on purpose.
func TestCheckPasswordStrength(t *testing.T) {
	long := "correct-horse-battery"
	cases := []struct {
		name     string
		pass     string
		addr     string
		demo     bool
		wantFail bool
	}{
		{"short, public port", "bibli", ":8080", false, true},
		{"short, all interfaces", "bibli", "0.0.0.0:8080", false, true},
		{"long, public port", long, ":8080", false, false},
		{"short, localhost only", "bibli", "127.0.0.1:8765", false, false},
		{"short, ::1 only", "bibli", "[::1]:8765", false, false},
		{"short, demonstration", "demo", ":8080", true, false},
		{"exactly the minimum", "123456789012", ":8080", false, false},
		{"one short of it", "12345678901", ":8080", false, true},
	}
	for _, c := range cases {
		err := checkPasswordStrength(c.pass, c.addr, c.demo)
		if (err != nil) != c.wantFail {
			t.Errorf("%s: err = %v, wantFail = %v", c.name, err, c.wantFail)
		}
	}
}
