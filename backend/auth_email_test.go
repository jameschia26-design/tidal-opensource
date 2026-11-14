package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// codeSink stands in for Brevo: it records the last message so tests can read the emailed code.
type codeSink struct {
	mu      sync.Mutex
	to      string
	subject string
	body    string
	sent    int
}

func (s *codeSink) send(to, subject, body string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.to, s.subject, s.body = to, subject, body
	s.sent++
	return nil
}

func (s *codeSink) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.to, s.subject, s.body, s.sent = "", "", "", 0
}

// lastSubject is the subject of the most recent captured message.
func (s *codeSink) lastSubject(t *testing.T) string {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.subject
}

var codeInBody = regexp.MustCompile(`\b([0-9]{6})\b`)

// code returns the 6-digit code from the last captured message.
func (s *codeSink) code(t *testing.T) string {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	m := codeInBody.FindStringSubmatch(s.body)
	if len(m) != 2 {
		t.Fatalf("no code in captured mail: %q", s.body)
	}
	return m[1]
}

func mailSink(a *app) *codeSink {
	s := &codeSink{}
	a.mail = newMailer(config{})
	a.mail.sink = s.send
	return s
}

func sessionCookieValue(rec interface{ Result() *http.Response }) string {
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookie && c.Value != "" {
			return c.Value
		}
	}
	return ""
}

// clearSendLog ages out the cooldown window so a test can request a second code immediately.
func clearSendLog(t *testing.T, a *app) {
	t.Helper()
	if _, err := a.db.Exec(context.Background(),
		`UPDATE login_send_log SET created_at = now() - interval '2 hours'`); err != nil {
		t.Fatal(err)
	}
}

func mustVerify(t *testing.T, a *app, email, password string) {
	t.Helper()
	hash, err := hashSecret(password)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec(context.Background(),
		`INSERT INTO users (email, password_hash, email_verified_at) VALUES ($1, $2, now())`, email, hash); err != nil {
		t.Fatal(err)
	}
}

// signup then verify with the emailed code: the account becomes verified and gets a live session.
func TestSignupThenVerify(t *testing.T) {
	a, pool := testApp(t)
	sink := mailSink(a)
	h := a.routes()
	const email, password = "new@example.com", "correct horse battery"
	if rec := do(t, h, http.MethodPost, "/api/v1/auth/signup",
		fmt.Sprintf(`{"email":%q,"password":%q}`, email, password), ""); rec.Code != http.StatusAccepted {
		t.Fatalf("signup: %d %s", rec.Code, rec.Body)
	}
	var verified *time.Time
	if err := pool.QueryRow(context.Background(),
		`SELECT email_verified_at FROM users WHERE email=$1`, email).Scan(&verified); err != nil {
		t.Fatal(err)
	}
	if verified != nil {
		t.Fatal("signup marked the address verified before the code")
	}
	rec := do(t, h, http.MethodPost, "/api/v1/auth/signup/verify",
		fmt.Sprintf(`{"email":%q,"code":%q}`, email, sink.code(t)), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("verify: %d %s", rec.Code, rec.Body)
	}
	token := sessionCookieValue(rec)
	if token == "" {
		t.Fatal("verify did not set a session cookie")
	}
	if rec := do(t, h, http.MethodGet, "/api/v1/me", "", token); rec.Code != http.StatusOK {
		t.Fatalf("session from verification is not live: %d", rec.Code)
	}
	if err := pool.QueryRow(context.Background(),
		`SELECT email_verified_at FROM users WHERE email=$1`, email).Scan(&verified); err != nil {
		t.Fatal(err)
	}
	if verified == nil {
		t.Fatal("address still unverified after a correct code")
	}
}

// An unverified account cannot sign in: login re-sends the verification code and opens no session.
func TestUnverifiedLoginBlocked(t *testing.T) {
	a, _ := testApp(t)
	sink := mailSink(a)
	h := a.routes()
	const email, password = "pending@example.com", "correct horse battery"
	do(t, h, http.MethodPost, "/api/v1/auth/signup",
		fmt.Sprintf(`{"email":%q,"password":%q}`, email, password), "")
	sink.reset()
	clearSendLog(t, a)

	rec := do(t, h, http.MethodPost, "/api/v1/auth/login",
		fmt.Sprintf(`{"email":%q,"password":%q}`, email, password), "")
	if rec.Code != http.StatusAccepted || !strings.Contains(rec.Body.String(), "code_sent") {
		t.Fatalf("unverified login: %d %s", rec.Code, rec.Body)
	}
	if sessionCookieValue(rec) != "" {
		t.Fatal("unverified login issued a session")
	}
	if sink.sent != 1 {
		t.Fatalf("unverified login sent %d mails, want the verification code again", sink.sent)
	}
	rec = do(t, h, http.MethodPost, "/api/v1/auth/signup/verify",
		fmt.Sprintf(`{"email":%q,"code":%q}`, email, sink.code(t)), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("verify after login resend: %d %s", rec.Code, rec.Body)
	}
}

// Reset: code works once, sets the new password, revokes live sessions.
func TestPasswordResetHappyPathAndSingleUse(t *testing.T) {
	a, _ := testApp(t)
	sink := mailSink(a)
	h := a.routes()
	const email, oldPassword, newPassword = "reset@example.com", "correct horse battery", "another long password"
	mustVerify(t, a, email, oldPassword)
	// A signed-in session must die when the password is reset.
	login := do(t, h, http.MethodPost, "/api/v1/auth/login",
		fmt.Sprintf(`{"email":%q,"password":%q}`, email, oldPassword), "")
	if login.Code != http.StatusAccepted {
		t.Fatalf("login: %d %s", login.Code, login.Body)
	}
	// The login mailed a code; verify it to obtain a session.
	rec := do(t, h, http.MethodPost, "/api/v1/auth/login/verify",
		fmt.Sprintf(`{"email":%q,"code":%q}`, email, sink.code(t)), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("login verify: %d %s", rec.Code, rec.Body)
	}
	oldSession := sessionCookieValue(rec)

	sink.reset()
	clearSendLog(t, a)
	if rec := do(t, h, http.MethodPost, "/api/v1/auth/password/forgot",
		fmt.Sprintf(`{"email":%q}`, email), ""); rec.Code != http.StatusAccepted {
		t.Fatalf("forgot: %d %s", rec.Code, rec.Body)
	}
	code := sink.code(t)
	rec = do(t, h, http.MethodPost, "/api/v1/auth/password/reset",
		fmt.Sprintf(`{"email":%q,"code":%q,"new_password":%q}`, email, code, newPassword), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("reset: %d %s", rec.Code, rec.Body)
	}
	if rec := do(t, h, http.MethodGet, "/api/v1/me", "", oldSession); rec.Code != http.StatusUnauthorized {
		t.Fatalf("old session still live after reset: %d", rec.Code)
	}
	var hash string
	if err := a.db.QueryRow(context.Background(),
		`SELECT password_hash FROM users WHERE email=$1`, email).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if !verifySecret(newPassword, hash) || verifySecret(oldPassword, hash) {
		t.Fatal("password hash was not replaced")
	}
	if rec := do(t, h, http.MethodPost, "/api/v1/auth/password/reset",
		fmt.Sprintf(`{"email":%q,"code":%q,"new_password":%q}`, email, code, newPassword), ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("reused reset code accepted: %d %s", rec.Code, rec.Body)
	}
}

// An expired reset code is refused and burned.
func TestPasswordResetCodeExpiry(t *testing.T) {
	a, pool := testApp(t)
	sink := mailSink(a)
	h := a.routes()
	const email = "expired@example.com"
	mustVerify(t, a, email, "correct horse battery")
	if rec := do(t, h, http.MethodPost, "/api/v1/auth/password/forgot",
		fmt.Sprintf(`{"email":%q}`, email), ""); rec.Code != http.StatusAccepted {
		t.Fatalf("forgot: %d", rec.Code)
	}
	code := sink.code(t)
	if _, err := pool.Exec(context.Background(),
		`UPDATE login_challenges SET expires_at=now() - interval '1 minute' WHERE purpose='reset'`); err != nil {
		t.Fatal(err)
	}
	if rec := do(t, h, http.MethodPost, "/api/v1/auth/password/reset",
		fmt.Sprintf(`{"email":%q,"code":%q,"new_password":"another long password"}`, email, code), ""); rec.Code != http.StatusGone {
		t.Fatalf("expired code: %d %s", rec.Code, rec.Body)
	}
}

// Signup and forgot-password replies are byte-identical for known and unknown addresses.
func TestAuthRepliesDoNotEnumerate(t *testing.T) {
	a, _ := testApp(t)
	sink := mailSink(a)
	h := a.routes()
	mustVerify(t, a, "known@example.com", "correct horse battery")
	const password = "correct horse battery"

	newRec := do(t, h, http.MethodPost, "/api/v1/auth/signup",
		fmt.Sprintf(`{"email":%q,"password":%q}`, "fresh@example.com", password), "")
	existingRec := do(t, h, http.MethodPost, "/api/v1/auth/signup",
		fmt.Sprintf(`{"email":%q,"password":%q}`, "known@example.com", password), "")
	if newRec.Code != existingRec.Code || newRec.Body.String() != existingRec.Body.String() {
		t.Fatalf("signup leaks account existence: %d/%s vs %d/%s",
			newRec.Code, newRec.Body, existingRec.Code, existingRec.Body)
	}

	sink.reset()
	knownForgot := do(t, h, http.MethodPost, "/api/v1/auth/password/forgot",
		`{"email":"known@example.com"}`, "")
	unknownForgot := do(t, h, http.MethodPost, "/api/v1/auth/password/forgot",
		`{"email":"nobody@example.com"}`, "")
	if knownForgot.Code != unknownForgot.Code || knownForgot.Body.String() != unknownForgot.Body.String() {
		t.Fatalf("forgot leaks account existence: %d/%s vs %d/%s",
			knownForgot.Code, knownForgot.Body, unknownForgot.Code, unknownForgot.Body)
	}
	if sink.sent != 1 {
		t.Fatalf("forgot sent %d mails, want exactly one for the known address", sink.sent)
	}
}

// The environment admin is created once, keeps a changed password, and only resets on opt-in.
func TestEnsureAdminIdempotent(t *testing.T) {
	_, pool := testApp(t)
	ctx := context.Background()
	cfg := config{AdminEmail: "boss@example.com", AdminPassword: "initial admin password"}
	if err := ensureAdmin(ctx, pool, cfg); err != nil {
		t.Fatal(err)
	}
	if err := ensureAdmin(ctx, pool, cfg); err != nil {
		t.Fatal(err)
	}
	var n int
	var admin, verified bool
	var tier string
	var hash string
	if err := pool.QueryRow(ctx,
		`SELECT count(*), bool_and(is_platform_admin), bool_and(email_verified_at IS NOT NULL), min(tier) FROM users WHERE email=$1`,
		cfg.AdminEmail).Scan(&n, &admin, &verified, &tier); err != nil {
		t.Fatal(err)
	}
	if n != 1 || !admin || !verified || tier != "premier" {
		t.Fatalf("admin seed: n=%d admin=%t verified=%t tier=%s", n, admin, verified, tier)
	}
	if err := pool.QueryRow(ctx, `SELECT password_hash FROM users WHERE email=$1`, cfg.AdminEmail).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if !verifySecret("initial admin password", hash) {
		t.Fatal("seeded password does not verify")
	}

	// A changed environment value must not overwrite the stored password by accident.
	cfg.AdminPassword = "a different admin password"
	if err := ensureAdmin(ctx, pool, cfg); err != nil {
		t.Fatal(err)
	}
	var unchanged string
	if err := pool.QueryRow(ctx, `SELECT password_hash FROM users WHERE email=$1`, cfg.AdminEmail).Scan(&unchanged); err != nil {
		t.Fatal(err)
	}
	if unchanged != hash {
		t.Fatal("admin password overwritten without TIDAL_ADMIN_PASSWORD_RESET=1")
	}

	cfg.AdminPasswordReset = true
	if err := ensureAdmin(ctx, pool, cfg); err != nil {
		t.Fatal(err)
	}
	var reset string
	if err := pool.QueryRow(ctx, `SELECT password_hash FROM users WHERE email=$1`, cfg.AdminEmail).Scan(&reset); err != nil {
		t.Fatal(err)
	}
	if !verifySecret("a different admin password", reset) {
		t.Fatal("TIDAL_ADMIN_PASSWORD_RESET=1 did not replace the password")
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO users (email, password_hash) VALUES ('unverified-admin@example.com', 'x')`); err != nil {
		t.Fatal(err)
	}
	cfg.AdminEmail = "unverified-admin@example.com"
	if err := ensureAdmin(ctx, pool, cfg); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT is_platform_admin AND email_verified_at IS NOT NULL FROM users WHERE email=$1`, cfg.AdminEmail).Scan(&verified); err != nil {
		t.Fatal(err)
	}
	if !verified {
		t.Fatal("existing admin account was not marked verified and admin")
	}
}

// argonWork reports how many Argon2id operations run while fn executes.
func argonWork(a *app, fn func()) int64 {
	before := argonOps.Load()
	fn()
	return argonOps.Load() - before
}

// Every reply path of signup, login and forgot-password must spend the same Argon2id work, or the
// response time tells an attacker whether the account exists. Counted, not timed, so it cannot flake.
func TestAuthArgonWorkPerReplyPath(t *testing.T) {
	a, _ := testApp(t)
	mailSink(a)
	h := a.routes()
	const password = "correct horse battery"
	a.cfg.AdminEmail = "boss@example.com"
	mustVerify(t, a, "known@example.com", password)

	post := func(path, body string) int {
		return do(t, h, http.MethodPost, path, body, "").Code
	}
	for _, c := range []struct {
		name string
		want int64
		run  func() int
		code int
	}{
		{"signup new account", 2, func() int {
			return post("/api/v1/auth/signup", fmt.Sprintf(`{"email":"fresh@example.com","password":%q}`, password))
		}, http.StatusAccepted},
		{"signup existing verified", 2, func() int {
			return post("/api/v1/auth/signup", fmt.Sprintf(`{"email":"known@example.com","password":%q}`, password))
		}, http.StatusAccepted},
		{"signup admin address", 2, func() int {
			return post("/api/v1/auth/signup", fmt.Sprintf(`{"email":"boss@example.com","password":%q}`, password))
		}, http.StatusAccepted},
		{"signup address over cap", 2, func() int {
			// Fill the allowance for this test's own connecting address; httptest uses 192.0.2.1.
			if _, err := a.db.Exec(context.Background(),
				`INSERT INTO signup_requests (ip) SELECT '192.0.2.1' FROM generate_series(1, $1)`, signupsPerIPHour); err != nil {
				t.Fatal(err)
			}
			return post("/api/v1/auth/signup", fmt.Sprintf(`{"email":"capped@example.com","password":%q}`, password))
		}, http.StatusTooManyRequests},
		{"login unknown account", 1, func() int {
			return post("/api/v1/auth/login", fmt.Sprintf(`{"email":"nobody@example.com","password":%q}`, password))
		}, http.StatusUnauthorized},
		{"login wrong password", 1, func() int {
			return post("/api/v1/auth/login", fmt.Sprintf(`{"email":"known@example.com","password":"wrong password here"}`))
		}, http.StatusUnauthorized},
		{"login correct password", 2, func() int {
			return post("/api/v1/auth/login", fmt.Sprintf(`{"email":"known@example.com","password":%q}`, password))
		}, http.StatusAccepted},
		{"forgot unknown account", 1, func() int {
			return post("/api/v1/auth/password/forgot", `{"email":"nobody@example.com"}`)
		}, http.StatusAccepted},
		{"forgot known account", 1, func() int {
			return post("/api/v1/auth/password/forgot", `{"email":"known@example.com"}`)
		}, http.StatusAccepted},
	} {
		code := 0
		got := argonWork(a, func() { code = c.run() })
		if code != c.code {
			t.Fatalf("%s: status %d, want %d", c.name, code, c.code)
		}
		if got != c.want {
			t.Fatalf("%s: %d Argon2id calls, want %d", c.name, got, c.want)
		}
	}
}

// Forwarding headers are believed only from a loopback or configured proxy: the chain is read
// from the right, skipping trusted proxies, and the first untrusted entry is the client our own
// proxy saw. Client-supplied entries on the left are never trusted.
func TestClientIPTrustsForwardedOnlyFromProxy(t *testing.T) {
	a, _ := testApp(t)
	req := func(remote, forwarded string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
		r.RemoteAddr = remote
		if forwarded != "" {
			r.Header.Set("X-Forwarded-For", forwarded)
		}
		return r
	}
	for _, c := range []struct{ name, remote, forwarded, want string }{
		{"untrusted peer header ignored", "198.51.100.7:443", "203.0.113.9", "198.51.100.7"},
		{"untrusted leftmost ignored", "127.0.0.1:443", "203.0.113.9, 198.51.100.7", "198.51.100.7"},
		{"spoofed left entries ignored", "127.0.0.1:443", "1.2.3.4, 203.0.113.9, 198.51.100.7", "198.51.100.7"},
		{"garbage header falls back", "127.0.0.1:443", "not-an-ip", "127.0.0.1"},
		{"empty header falls back", "127.0.0.1:443", "", "127.0.0.1"},
	} {
		if got := a.clientIP(req(c.remote, c.forwarded)); got != c.want {
			t.Fatalf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
	a.cfg.TrustedProxies, _ = parseTrustedProxies("10.9.8.7/32")
	if got := a.clientIP(req("10.9.8.7:443", "203.0.113.9")); got != "203.0.113.9" {
		t.Fatalf("configured proxy not trusted: %q", got)
	}
	if got := a.clientIP(req("10.9.8.8:443", "203.0.113.9")); got != "10.9.8.8" {
		t.Fatalf("unconfigured peer trusted: %q", got)
	}

	// A configured proxy hop is skipped when reading from the right; earlier client entries are noise.
	a.cfg.TrustedProxies, _ = parseTrustedProxies("10.9.8.7/32, 198.51.100.7/32")
	if got := a.clientIP(req("127.0.0.1:443", "9.9.9.9, 203.0.113.9, 198.51.100.7")); got != "203.0.113.9" {
		t.Fatalf("configured proxy hop not skipped: %q", got)
	}

	// A client cannot smuggle an address past the last trusted hop by naming a trusted proxy to its
	// left: the walk still stops at the first untrusted entry to the right.
	a.cfg.TrustedProxies, _ = parseTrustedProxies("10.9.8.7/32, 10.9.8.9/32")
	if got := a.clientIP(req("127.0.0.1:443", "10.9.8.7, 203.0.113.9, 10.9.8.9")); got != "203.0.113.9" {
		t.Fatalf("trusted-looking client entry shifted the client: %q", got)
	}
}

// The signed client-IP trio from the Vercel frontend is believed only when the HMAC verifies and the
// timestamp is fresh. Every rejection falls back to the unchanged trusted-proxy walk.
func TestClientIPSignedHeader(t *testing.T) {
	a, _ := testApp(t)
	const secret = "0123456789abcdef0123456789abcdef"
	sign := func(key, ip, ts string) string {
		mac := hmac.New(sha256.New, []byte(key))
		mac.Write([]byte(ip + "|" + ts))
		return hex.EncodeToString(mac.Sum(nil))
	}
	now := strconv.FormatInt(time.Now().Unix(), 10)
	signed := func(remote, ip, key, ts, sig string, withSig bool) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
		r.RemoteAddr = remote
		if ip != "" {
			r.Header.Set("X-Tidal-Client-Ip", ip)
		}
		if ts != "" {
			r.Header.Set("X-Tidal-Client-Ts", ts)
		}
		if withSig {
			r.Header.Set("X-Tidal-Client-Sig", sig)
		}
		return r
	}
	a.cfg.ProxySecret = secret
	for _, c := range []struct {
		name   string
		remote string
		ip     string
		sig    string
		want   string
	}{
		{"valid signature wins from untrusted peer", "198.51.100.7:443", "203.0.113.9", sign(secret, "203.0.113.9", now), "203.0.113.9"},
		{"valid IPv6 signature", "198.51.100.7:443", "2001:db8::1", sign(secret, "2001:db8::1", now), "2001:db8::1"},
		{"tampered ip ignored", "198.51.100.7:443", "203.0.113.10", sign(secret, "203.0.113.9", now), "198.51.100.7"},
		{"stale timestamp ignored", "198.51.100.7:443", "203.0.113.9", sign(secret, "203.0.113.9", strconv.FormatInt(time.Now().Add(-2*time.Minute).Unix(), 10)), "198.51.100.7"},
		{"future timestamp ignored", "198.51.100.7:443", "203.0.113.9", sign(secret, "203.0.113.9", strconv.FormatInt(time.Now().Add(2*time.Minute).Unix(), 10)), "198.51.100.7"},
		{"wrong secret ignored", "198.51.100.7:443", "203.0.113.9", sign("ffffffffffffffffffffffffffffffff", "203.0.113.9", now), "198.51.100.7"},
		{"invalid ip ignored", "198.51.100.7:443", "not-an-ip", sign(secret, "not-an-ip", now), "198.51.100.7"},
	} {
		if got := a.clientIP(signed(c.remote, c.ip, secret, now, c.sig, true)); got != c.want {
			t.Fatalf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
	// Headers without a signature are ignored from an untrusted peer.
	if got := a.clientIP(signed("198.51.100.7:443", "203.0.113.9", secret, now, "", false)); got != "198.51.100.7" {
		t.Fatalf("unsigned header trusted: %q", got)
	}
	// No configured secret disables the check entirely, even with a correct signature for some key.
	a.cfg.ProxySecret = ""
	if got := a.clientIP(signed("198.51.100.7:443", "203.0.113.9", secret, now, sign(secret, "203.0.113.9", now), true)); got != "198.51.100.7" {
		t.Fatalf("signed header used without a secret: %q", got)
	}
}

// The global per-IP failure cap throttles one client without touching another behind the same proxy,
// and an untrusted peer cannot dodge it by setting the header itself.
func TestLoginPerIPCapAndForwardedIsolation(t *testing.T) {
	a, _ := testApp(t)
	mailSink(a)
	h := a.routes()
	const password = "correct horse battery"
	mustVerify(t, a, "victim@example.com", password)
	if _, err := a.db.Exec(context.Background(),
		`INSERT INTO login_failures (email, ip) SELECT 'spray-'||g||'@example.com', '203.0.113.50' FROM generate_series(1, $1) g`,
		failLimitIPHour); err != nil {
		t.Fatal(err)
	}
	login := func(remote, forwarded string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login",
			strings.NewReader(fmt.Sprintf(`{"email":"victim@example.com","password":%q}`, password)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", "http://app.test")
		req.RemoteAddr = remote
		if forwarded != "" {
			req.Header.Set("X-Forwarded-For", forwarded)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	if rec := login("127.0.0.1:5555", "203.0.113.50"); rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("capped client not throttled: %d %v", rec.Code, rec.Header())
	}
	if rec := login("127.0.0.1:5555", "203.0.113.51"); rec.Code != http.StatusAccepted {
		t.Fatalf("uncapped client throttled: %d %s", rec.Code, rec.Body)
	}

	// From an untrusted peer the header is ignored, so the connection address is what gets recorded.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/signup",
		strings.NewReader(`{"email":"spoof@example.com","password":"correct horse battery"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://app.test")
	req.RemoteAddr = "198.51.100.7:4444"
	req.Header.Set("X-Forwarded-For", "203.0.113.99")
	h.ServeHTTP(httptest.NewRecorder(), req)
	var spoofed, actual int
	if err := a.db.QueryRow(context.Background(),
		`SELECT count(*) FILTER (WHERE ip='203.0.113.99'), count(*) FILTER (WHERE ip='198.51.100.7') FROM signup_requests`).
		Scan(&spoofed, &actual); err != nil {
		t.Fatal(err)
	}
	if spoofed != 0 || actual != 1 {
		t.Fatalf("forwarded header from an untrusted peer was stored: spoofed=%d actual=%d", spoofed, actual)
	}
}

// A correct code proves control of the mailbox, so every other pending code for that user dies.
func TestCodeSuccessBurnsOtherPurposes(t *testing.T) {
	a, _ := testApp(t)
	sink := mailSink(a)
	h := a.routes()
	const email, password = "multi@example.com", "correct horse battery"
	mustVerify(t, a, email, password)
	if rec := do(t, h, http.MethodPost, "/api/v1/auth/login", fmt.Sprintf(`{"email":%q,"password":%q}`, email, password), ""); rec.Code != http.StatusAccepted {
		t.Fatalf("login: %d %s", rec.Code, rec.Body)
	}
	loginCode := sink.code(t)
	clearSendLog(t, a)
	if rec := do(t, h, http.MethodPost, "/api/v1/auth/password/forgot", fmt.Sprintf(`{"email":%q}`, email), ""); rec.Code != http.StatusAccepted {
		t.Fatalf("forgot: %d %s", rec.Code, rec.Body)
	}
	resetCode := sink.code(t)
	if loginCode == resetCode {
		t.Fatal("both purposes produced the same code, so the test proves nothing")
	}
	if rec := do(t, h, http.MethodPost, "/api/v1/auth/login/verify", fmt.Sprintf(`{"email":%q,"code":%q}`, email, loginCode), ""); rec.Code != http.StatusOK {
		t.Fatalf("login verify: %d %s", rec.Code, rec.Body)
	}
	if rec := do(t, h, http.MethodPost, "/api/v1/auth/password/reset",
		fmt.Sprintf(`{"email":%q,"code":%q,"new_password":"another long password"}`, email, resetCode), ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("reset code survived another purpose's success: %d %s", rec.Code, rec.Body)
	}
}
