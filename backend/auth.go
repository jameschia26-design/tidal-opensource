package main

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/mail"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/argon2"
)

const (
	sessionCookie     = "tidal_session"
	sessionTTL        = 12 * time.Hour
	codeTTL           = 10 * time.Minute
	codeMaxAttempts   = 5
	failWindow        = 15 * time.Minute
	failLimitEmail    = 10 // password failures per email per window
	failLimitIPHour   = 60 // password failures per connecting address per hour
	codesPerHour      = 5  // emailed codes per account per hour
	codeCooldown      = 60 * time.Second
	signupsPerIPHour  = 20               // signup requests per connecting address per hour
	resetsPerIPHour   = 10               // password-reset requests per connecting address per hour
	argonConcurrency  = 4                // concurrent Argon2id operations; each uses argonMemory (64 MiB)
	proxySignatureTTL = 60 * time.Second // signed client-IP headers are valid for +/- this window

	argonTime    = 2
	argonMemory  = 64 * 1024
	argonThreads = 2
	argonKeyLen  = 32
)

// argonSlots bounds concurrent Argon2id work so a burst of requests cannot exhaust server memory.
var argonSlots = make(chan struct{}, argonConcurrency)

var (
	codeRe       = regexp.MustCompile(`^[0-9]{6}$`)
	dummyHash    = mustHash("timing-equalizer-not-a-real-password")
	errNoSession = errors.New("no session cookie")

	// argonOps counts Argon2id calls. Tests use it to prove every reply path of a handler spends
	// the same hashing work, without relying on wall-clock timing.
	argonOps atomic.Int64
)

type currentUser struct {
	ID      int64
	Email   string
	Name    string
	IsAdmin bool
}

// userHandler is a handler that runs only after requireUser has resolved a live session.
type userHandler func(http.ResponseWriter, *http.Request, currentUser)

type credentials struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type verifyInput struct {
	Email string `json:"email"`
	Code  string `json:"code"`
}

func mustHash(s string) string {
	h, err := hashSecret(s)
	if err != nil {
		panic(err)
	}
	return h
}

// hashSecret returns an argon2id encoding: $argon2id$v=19$m=..,t=..,p=..$salt$key.
func hashSecret(secret string) (string, error) {
	argonSlots <- struct{}{}
	defer func() { <-argonSlots }()
	argonOps.Add(1)
	salt, err := randomBytes(16)
	if err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(secret), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

func verifySecret(secret, encoded string) bool {
	argonSlots <- struct{}{}
	defer func() { <-argonSlots }()
	argonOps.Add(1)
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var mem, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &mem, &t, &p); err != nil {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(want) == 0 {
		return false
	}
	got := argon2.IDKey([]byte(secret), salt, t, mem, p, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

func randomBytes(n int) ([]byte, error) {
	b := make([]byte, n)
	_, err := rand.Read(b)
	return b, err
}

func randomString(n int) (string, error) {
	b, err := randomBytes(n)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// newCode returns a uniformly random 6-digit code.
func newCode() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

// tokenHash stores session tokens hashed, so a database read does not yield live sessions.
func tokenHash(token string) []byte {
	s := sha256.Sum256([]byte(token))
	return s[:]
}

// normalizeEmail lowercases and validates a bare address (no display names, no header characters).
func normalizeEmail(raw string) (string, bool) {
	s := strings.ToLower(strings.TrimSpace(raw))
	if s == "" || len(s) > 254 {
		return "", false
	}
	a, err := mail.ParseAddress(s)
	if err != nil || a.Address != s {
		return "", false
	}
	if at := strings.LastIndex(s, "@"); at < 0 || !strings.Contains(s[at+1:], ".") {
		return "", false
	}
	return s, true
}

// spendDummyArgon performs n throwaway Argon2id verifications. A reply path that skips real hashing
// (no mail to send, unknown account, admin address) calls this so its cost matches the path that does.
func spendDummyArgon(n int) {
	for i := 0; i < n; i++ {
		verifySecret("timing-equalizer", dummyHash)
	}
}

// writeRateLimited answers 429 with Retry-After so clients and proxies back off predictably.
func writeRateLimited(w http.ResponseWriter) {
	w.Header().Set("Retry-After", strconv.Itoa(int(failWindow.Seconds())))
	writeError(w, http.StatusTooManyRequests, "too_many_attempts")
}

// writeAuthError keeps the retry hint on every throttled auth reply.
func writeAuthError(w http.ResponseWriter, status int, code string) {
	if status == http.StatusTooManyRequests {
		writeRateLimited(w)
		return
	}
	writeError(w, status, code)
}

// clientIP returns the real connecting address. The frontend runs on Vercel, so its signed headers win
// first: a valid HMAC proves the address came from our own proxy, whatever peer connected. Without a
// valid signature, X-Forwarded-For is honoured only when the connection itself comes from a loopback or
// configured proxy, because anyone else can set the header. The chain is read from the right, skipping
// trusted proxies: the first untrusted entry is the address our own proxy saw. Entries to the left were
// supplied by the client and are never believed. A malformed entry stops the walk and falls back to the
// connection address.
func (a *app) clientIP(r *http.Request) string {
	if ip, ok := a.signedClientIP(r); ok {
		return ip
	}
	host := r.RemoteAddr
	if h, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		host = h
	}
	ip := net.ParseIP(host)
	if ip == nil || !a.trustedProxy(ip) {
		return host
	}
	entries := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
	for i := len(entries) - 1; i >= 0; i-- {
		p := net.ParseIP(strings.TrimSpace(strings.Trim(entries[i], `"`)))
		if p == nil {
			return host
		}
		if a.trustedProxy(p) {
			continue
		}
		return p.String()
	}
	return host
}

// signedClientIP verifies the frontend's HMAC-signed client-IP headers (x-tidal-client-ip,
// x-tidal-client-ts, x-tidal-client-sig over ip + "|" + unix-seconds). Disabled when no secret is
// configured. Rejects a blank/invalid IP, a timestamp outside +proxySignatureTTL, and a signature that
// differs in constant time. Trusts the signature itself, not the peer, so it runs before the
// trusted-proxy walk regardless of who connected.
func (a *app) signedClientIP(r *http.Request) (string, bool) {
	if a.cfg.ProxySecret == "" {
		return "", false
	}
	ip := r.Header.Get("X-Tidal-Client-Ip")
	ts := r.Header.Get("X-Tidal-Client-Ts")
	sig := r.Header.Get("X-Tidal-Client-Sig")
	if ip == "" || ts == "" || sig == "" {
		return "", false
	}
	if net.ParseIP(strings.TrimSpace(ip)) == nil {
		return "", false
	}
	secs, err := strconv.ParseInt(strings.TrimSpace(ts), 10, 64)
	if err != nil {
		return "", false
	}
	if dt := time.Now().Unix() - secs; dt > int64(proxySignatureTTL.Seconds()) || dt < -int64(proxySignatureTTL.Seconds()) {
		return "", false
	}
	mac := hmac.New(sha256.New, []byte(a.cfg.ProxySecret))
	mac.Write([]byte(strings.TrimSpace(ip) + "|" + strings.TrimSpace(ts)))
	want, err := hex.DecodeString(strings.TrimSpace(sig))
	if err != nil || !hmac.Equal(mac.Sum(nil), want) {
		return "", false
	}
	return strings.TrimSpace(ip), true
}

// trustedProxy reports whether forwarding headers from this peer may be believed: loopback always,
// plus the addresses listed in TIDAL_TRUSTED_PROXIES.
func (a *app) trustedProxy(ip net.IP) bool {
	if ip.IsLoopback() {
		return true
	}
	for _, n := range a.cfg.TrustedProxies {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

func (a *app) setSessionCookie(w http.ResponseWriter, token string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   a.cfg.secureCookies(),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   maxAge,
	})
}

// signup creates an unverified password account and emails a verification code. Every valid request gets
// the same 202 so the reply never reveals whether the address is already registered.
func (a *app) signup(w http.ResponseWriter, r *http.Request) {
	var in credentials
	if !decodeJSON(w, r, &in) {
		return
	}
	email, ok := normalizeEmail(in.Email)
	if !ok || len(in.Password) < 12 || len(in.Password) > 128 {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	ctx := r.Context()
	ip := a.clientIP(r)
	var recentSignups int
	if err := a.db.QueryRow(ctx,
		`SELECT count(*) FROM signup_requests WHERE ip=$1 AND created_at > now() - interval '1 hour'`, ip).Scan(&recentSignups); err != nil {
		serverError(w, err)
		return
	}
	// The per-address limit applies to every signup address, including the platform admin (R18).
	if recentSignups >= signupsPerIPHour {
		spendDummyArgon(2)
		writeRateLimited(w)
		return
	}
	if _, err := a.db.Exec(ctx, `INSERT INTO signup_requests (ip) VALUES ($1)`, ip); err != nil {
		serverError(w, err)
		return
	}
	if email == a.cfg.AdminEmail {
		// The platform admin is created from the environment or by verified Google sign-in. Accept silently
		// so the reply does not reveal the address, and never create the account here.
		spendDummyArgon(2) // password hash + code hash the real path would have done
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "accepted"})
		return
	}
	hash, err := hashSecret(in.Password)
	if err != nil {
		serverError(w, err)
		return
	}
	// Password hashing happens before the lookup so new and existing addresses cost the same time.
	var userID int64
	err = a.db.QueryRow(ctx,
		`INSERT INTO users (email, password_hash) VALUES ($1, $2) ON CONFLICT (email) DO NOTHING RETURNING id`,
		email, hash).Scan(&userID)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// Already registered. A still-unverified password account gets a fresh verification code; anything
		// else is left untouched (in particular, a stored password is never replaced by a signup).
		var unverified bool
		err := a.db.QueryRow(ctx,
			`SELECT id, email_verified_at IS NULL FROM users WHERE email=$1 AND NOT disabled`, email).
			Scan(&userID, &unverified)
		if errors.Is(err, pgx.ErrNoRows) {
			writeJSON(w, http.StatusAccepted, map[string]string{"status": "accepted"})
			return
		}
		if err != nil {
			serverError(w, err)
			return
		}
		if !unverified {
			spendDummyArgon(1) // no code is mailed, so spend its hash
			writeJSON(w, http.StatusAccepted, map[string]string{"status": "accepted"})
			return
		}
	case err != nil:
		serverError(w, err)
		return
	}
	// Delivery and per-account limits are not reported here: any difference would reveal the account.
	if _, _, err := a.sendCode(ctx, userID, email, "signup", "Verify your Tidal email", "Your Tidal verification code is"); err != nil {
		log.Printf("signup code not sent: %v", err)
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "accepted"})
}

// sendCode issues an email code for one purpose and sends it, applying the cooldown and hourly cap.
// The challenge is burned if delivery fails. It returns the HTTP status and error code for a rejected
// send (0, "" on success). Neither the code nor the recipient is ever logged.
func (a *app) sendCode(ctx context.Context, userID int64, email, purpose, subject, heading string) (int, string, error) {
	if !a.mail.configured() {
		spendDummyArgon(1) // nothing is sent, but the caller still pays the code-hash cost
		return http.StatusServiceUnavailable, "email_unavailable", nil
	}
	code, err := newCode()
	if err != nil {
		return 0, "", err
	}
	codeHash, err := hashSecret(code)
	if err != nil {
		return 0, "", err
	}
	challengeID, err := randomString(16)
	if err != nil {
		return 0, "", err
	}
	status, errCode, err := a.issueChallenge(ctx, userID, challengeID, codeHash, purpose)
	if err != nil || errCode != "" {
		return status, errCode, err
	}
	body := fmt.Sprintf("%s %s.\n\nIt expires in 10 minutes and works once. "+
		"If you did not request this, ignore this email.", heading, code)
	if err := a.mail.send(email, subject, body); err != nil {
		// Burn the challenge we just issued: a code that was never delivered must not stay usable.
		_, _ = a.db.Exec(ctx, `UPDATE login_challenges SET used_at=now() WHERE id=$1`, challengeID)
		log.Printf("%s email not sent: %v", purpose, err)
		return http.StatusServiceUnavailable, "email_unavailable", nil
	}
	return 0, "", nil
}

// login checks the password, then emails a fresh one-time code. Every password login needs the code.
// Rules enforced in the database under the user row lock, before any email is sent:
// a cooldown between codes, an hourly cap, and invalidation of every earlier active challenge.
func (a *app) login(w http.ResponseWriter, r *http.Request) {
	var in credentials
	if !decodeJSON(w, r, &in) {
		return
	}
	email, ok := normalizeEmail(in.Email)
	if !ok || in.Password == "" || len(in.Password) > 128 {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	ctx := r.Context()
	ip := a.clientIP(r)
	limited, err := a.loginLimited(ctx, email, ip)
	if err != nil {
		serverError(w, err)
		return
	}
	if limited {
		spendDummyArgon(1)
		writeRateLimited(w)
		return
	}

	var userID int64
	var hash *string
	var verified bool
	err = a.db.QueryRow(ctx,
		`SELECT id, password_hash, email_verified_at IS NOT NULL FROM users WHERE email=$1 AND NOT disabled`, email).
		Scan(&userID, &hash, &verified)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		verifySecret(in.Password, dummyHash) // spend similar time as a real account
		hash = nil
	case err != nil:
		serverError(w, err)
		return
	}
	if hash == nil || !verifySecret(in.Password, *hash) {
		if err := a.recordFailure(ctx, email, ip); err != nil {
			serverError(w, err)
			return
		}
		writeError(w, http.StatusUnauthorized, "invalid_credentials")
		return
	}
	// An unverified signup cannot sign in: re-send the verification code instead. The reply is the same as a
	// sign-in code so this step does not advertise account state.
	purpose, subject, heading := "login", "Your Tidal sign-in code", "Your Tidal sign-in code is"
	if !verified {
		purpose, subject, heading = "signup", "Verify your Tidal email", "Your Tidal verification code is"
	}
	status, errCode, err := a.sendCode(ctx, userID, email, purpose, subject, heading)
	if err != nil {
		serverError(w, err)
		return
	}
	if errCode != "" {
		writeAuthError(w, status, errCode)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "code_sent"})
}

// issueChallenge locks the user row, applies the cooldown and hourly cap, invalidates earlier active
// challenges of the same purpose, and records the send. Commits before the email is sent.
// Returns 429 codes as errCode. The cooldown and hourly cap are shared across purposes.
func (a *app) issueChallenge(ctx context.Context, userID int64, challengeID, codeHash, purpose string) (int, string, error) {
	tx, err := a.db.Begin(ctx)
	if err != nil {
		return 0, "", err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT 1 FROM users WHERE id=$1 FOR UPDATE`, userID); err != nil {
		return 0, "", err
	}
	var recent int
	var lastSeconds *float64
	if err := tx.QueryRow(ctx, `
		SELECT count(*), extract(epoch FROM now() - max(created_at))
		FROM login_send_log WHERE user_id=$1 AND created_at > now() - interval '1 hour'`, userID).
		Scan(&recent, &lastSeconds); err != nil {
		return 0, "", err
	}
	if recent >= codesPerHour {
		return http.StatusTooManyRequests, "too_many_attempts", nil
	}
	if lastSeconds != nil && *lastSeconds < codeCooldown.Seconds() {
		return http.StatusTooManyRequests, "too_many_attempts", nil
	}
	if _, err := tx.Exec(ctx,
		`UPDATE login_challenges SET used_at=now() WHERE user_id=$1 AND purpose=$2 AND used_at IS NULL`,
		userID, purpose); err != nil {
		return 0, "", err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO login_challenges (id, user_id, code_hash, purpose, expires_at) VALUES ($1, $2, $3, $4, now() + make_interval(secs => $5))`,
		challengeID, userID, codeHash, purpose, codeTTL.Seconds()); err != nil {
		return 0, "", err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO login_send_log (user_id) VALUES ($1)`, userID); err != nil {
		return 0, "", err
	}
	return 0, "", tx.Commit(ctx)
}

// loginLimited reports whether this address or connecting IP has too many recent password failures.
// The IP limit is what stops one client spraying many accounts behind a shared proxy address.
func (a *app) loginLimited(ctx context.Context, email, ip string) (bool, error) {
	var limited bool
	err := a.db.QueryRow(ctx, `
		SELECT (SELECT count(*) >= $2 FROM login_failures WHERE email=$1 AND created_at > now() - make_interval(secs => $3))
		    OR (SELECT count(*) >= $4 FROM login_failures WHERE ip=$5 AND created_at > now() - interval '1 hour')`,
		email, failLimitEmail, failWindow.Seconds(), failLimitIPHour, ip).Scan(&limited)
	return limited, err
}

func (a *app) recordFailure(ctx context.Context, email, ip string) error {
	_, err := a.db.Exec(ctx, `INSERT INTO login_failures (email, ip) VALUES ($1, $2)`, email, ip)
	return err
}

// loginVerify checks the emailed code and, on success, issues a revocable server session.
func (a *app) loginVerify(w http.ResponseWriter, r *http.Request) {
	var in verifyInput
	if !decodeJSON(w, r, &in) {
		return
	}
	email, ok := normalizeEmail(in.Email)
	if !ok || !codeRe.MatchString(in.Code) {
		writeError(w, http.StatusUnauthorized, "invalid_code")
		return
	}
	uid, token, status, code, err := a.checkCode(r.Context(), email, in.Code, "login")
	if err != nil {
		serverError(w, err)
		return
	}
	if code != "" {
		writeAuthError(w, status, code)
		return
	}
	a.recordLogin(r, uid)
	a.setSessionCookie(w, token, int(sessionTTL.Seconds()))
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// signupVerify consumes a verification code, marks the address verified, and signs the user in.
func (a *app) signupVerify(w http.ResponseWriter, r *http.Request) {
	var in verifyInput
	if !decodeJSON(w, r, &in) {
		return
	}
	email, ok := normalizeEmail(in.Email)
	if !ok || !codeRe.MatchString(in.Code) {
		writeError(w, http.StatusUnauthorized, "invalid_code")
		return
	}
	uid, token, status, code, err := a.checkCode(r.Context(), email, in.Code, "signup")
	if err != nil {
		serverError(w, err)
		return
	}
	if code != "" {
		writeAuthError(w, status, code)
		return
	}
	if _, err := a.db.Exec(r.Context(),
		`UPDATE users SET email_verified_at=COALESCE(email_verified_at, now()) WHERE id=$1 AND NOT disabled`, uid); err != nil {
		serverError(w, err)
		return
	}
	a.recordLogin(r, uid)
	a.setSessionCookie(w, token, int(sessionTTL.Seconds()))
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// checkCode consumes one attempt on the newest active challenge of a purpose, under a row lock.
// On success it returns the user ID and a session token, otherwise an HTTP status and error code.
// Failure outcomes are committed so the attempt counter persists.
func (a *app) checkCode(ctx context.Context, email, code, purpose string) (int64, string, int, string, error) {
	tx, err := a.db.Begin(ctx)
	if err != nil {
		return 0, "", 0, "", err
	}
	defer tx.Rollback(ctx)
	var chID, chHash string
	fail := func(burn bool, attempts, status int, errCode string) (int64, string, int, string, error) {
		if _, err := tx.Exec(ctx,
			`UPDATE login_challenges SET attempts=$2, used_at=CASE WHEN $3::boolean THEN now() ELSE used_at END WHERE id=$1`,
			chID, attempts, burn); err != nil {
			return 0, "", 0, "", err
		}
		if err := tx.Commit(ctx); err != nil {
			return 0, "", 0, "", err
		}
		return 0, "", status, errCode, nil
	}

	var userID int64
	err = tx.QueryRow(ctx, `SELECT id FROM users WHERE email=$1 AND NOT disabled`, email).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, "", http.StatusUnauthorized, "invalid_code", nil
	}
	if err != nil {
		return 0, "", 0, "", err
	}

	var attempts int
	var expired bool
	err = tx.QueryRow(ctx,
		`SELECT id, code_hash, attempts, expires_at <= now() FROM login_challenges
		 WHERE user_id=$1 AND purpose=$2 AND used_at IS NULL ORDER BY created_at DESC LIMIT 1 FOR UPDATE`, userID, purpose).
		Scan(&chID, &chHash, &attempts, &expired)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, "", http.StatusUnauthorized, "invalid_code", nil
	}
	if err != nil {
		return 0, "", 0, "", err
	}
	if expired {
		return fail(true, attempts, http.StatusGone, "code_expired")
	}
	if attempts >= codeMaxAttempts {
		return fail(true, attempts, http.StatusTooManyRequests, "too_many_attempts")
	}
	if !verifySecret(code, chHash) {
		attempts++
		if attempts >= codeMaxAttempts {
			return fail(true, attempts, http.StatusTooManyRequests, "too_many_attempts")
		}
		return fail(false, attempts, http.StatusUnauthorized, "invalid_code")
	}

	// A correct code proves control of the mailbox, so every other pending code dies with it: a code
	// from a different purpose must not stay usable once one of them has been spent.
	if _, err := tx.Exec(ctx, `UPDATE login_challenges SET used_at=now() WHERE user_id=$1 AND used_at IS NULL`, userID); err != nil {
		return 0, "", 0, "", err
	}
	token, err := randomString(32)
	if err != nil {
		return 0, "", 0, "", err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO sessions (token_hash, user_id, expires_at) VALUES ($1, $2, now() + make_interval(secs => $3))`,
		tokenHash(token), userID, sessionTTL.Seconds()); err != nil {
		return 0, "", 0, "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, "", 0, "", err
	}
	return userID, token, http.StatusOK, "", nil
}

// passwordForgot always answers 202. A reset code is sent only when the address belongs to a verified
// password account, so the reply never reveals whether an account exists or can be reset.
func (a *app) passwordForgot(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email string `json:"email"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	email, ok := normalizeEmail(in.Email)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	// Throttle before the lookup and before any hashing (R18): the reply must not depend on whether the
	// address exists, only on how many requests this address has already made.
	ip := a.clientIP(r)
	if _, err := a.db.Exec(r.Context(), `INSERT INTO password_reset_requests (ip) VALUES ($1)`, ip); err != nil {
		serverError(w, err)
		return
	}
	var recent int
	if err := a.db.QueryRow(r.Context(),
		`SELECT count(*) FROM password_reset_requests WHERE ip=$1 AND created_at > now() - interval '1 hour'`, ip).Scan(&recent); err != nil {
		serverError(w, err)
		return
	}
	if recent > resetsPerIPHour {
		writeRateLimited(w)
		return
	}
	// Password hashing happens on the reset path for the same reason; here the lookup cost is the same reply.
	var userID int64
	err := a.db.QueryRow(r.Context(),
		`SELECT id FROM users
		 WHERE email=$1 AND NOT disabled AND password_hash IS NOT NULL AND email_verified_at IS NOT NULL`, email).
		Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		spendDummyArgon(1) // same cost as the code hash the known-address path performs
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "accepted"})
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	// Delivery and per-account limits are not reported: any difference would reveal the account.
	if _, _, err := a.sendCode(r.Context(), userID, email, "reset", "Reset your Tidal password", "Your Tidal password-reset code is"); err != nil {
		log.Printf("reset code not sent: %v", err)
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "accepted"})
}

type resetInput struct {
	Email       string `json:"email"`
	Code        string `json:"code"`
	NewPassword string `json:"new_password"`
}

// passwordReset consumes a single-use reset code, sets the new password, and revokes all sessions.
// The code is spent before the new hash is stored: a rare failure past that point costs one more
// code request, which is cheaper than keeping a spent code usable.
func (a *app) passwordReset(w http.ResponseWriter, r *http.Request) {
	var in resetInput
	if !decodeJSON(w, r, &in) {
		return
	}
	email, ok := normalizeEmail(in.Email)
	if !ok || !codeRe.MatchString(in.Code) || len(in.NewPassword) < 12 || len(in.NewPassword) > 128 {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	ctx := r.Context()
	uid, _, status, code, err := a.checkCode(ctx, email, in.Code, "reset")
	if err != nil {
		serverError(w, err)
		return
	}
	if code != "" {
		writeAuthError(w, status, code)
		return
	}
	hash, err := hashSecret(in.NewPassword)
	if err != nil {
		serverError(w, err)
		return
	}
	tx, err := a.db.Begin(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(ctx)
	// A reset proves control of the mailbox, so the address counts as verified afterwards.
	if _, err := tx.Exec(ctx,
		`UPDATE users SET password_hash=$2, email_verified_at=COALESCE(email_verified_at, now()) WHERE id=$1`,
		uid, hash); err != nil {
		serverError(w, err)
		return
	}
	if _, err := tx.Exec(ctx,
		`UPDATE sessions SET revoked_at=now() WHERE user_id=$1 AND revoked_at IS NULL`, uid); err != nil {
		serverError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (a *app) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		if _, err := a.db.Exec(r.Context(),
			`UPDATE sessions SET revoked_at=now() WHERE token_hash=$1 AND revoked_at IS NULL`, tokenHash(c.Value)); err != nil {
			serverError(w, err)
			return
		}
	}
	a.setSessionCookie(w, "", -1)
	http.Redirect(w, r, a.cfg.PublicOrigin+"/", http.StatusSeeOther)
}

func (a *app) me(w http.ResponseWriter, r *http.Request, u currentUser) {
	writeJSON(w, http.StatusOK, map[string]any{"email": u.Email, "name": u.Name, "is_platform_admin": u.IsAdmin})
}

func (a *app) updateProfile(w http.ResponseWriter, r *http.Request, u currentUser) {
	var input struct {
		Name *string `json:"name"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.Name == nil {
		writeError(w, http.StatusBadRequest, "invalid_name")
		return
	}
	name := strings.TrimSpace(*input.Name)
	if name == "" || utf8.RuneCountInString(name) > 100 || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		writeError(w, http.StatusBadRequest, "invalid_name")
		return
	}
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	var changed bool
	err = tx.QueryRow(r.Context(), `WITH changed AS (UPDATE users SET name=$1 WHERE id=$2 AND name IS DISTINCT FROM $1 RETURNING id) SELECT EXISTS(SELECT 1 FROM changed)`, name, u.ID).Scan(&changed)
	if err == nil && changed {
		_, err = tx.Exec(r.Context(), `INSERT INTO audit_events (user_id, action, detail) VALUES ($1, 'user.profile_updated', '{}'::jsonb)`, u.ID)
	}
	if err == nil {
		err = tx.Commit(r.Context())
	}
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"name": name})
}

// requireUser resolves the session cookie to a live, unrevoked, unexpired session of an enabled user.
func (a *app) requireUser(next userHandler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, err := a.sessionUser(r)
		if errors.Is(err, errNoSession) || errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		if err != nil {
			serverError(w, err)
			return
		}
		next(w, r, u)
	})
}

func (a *app) sessionUser(r *http.Request) (currentUser, error) {
	var u currentUser
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return u, errNoSession
	}
	err = a.db.QueryRow(r.Context(),
		`SELECT u.id, u.email, u.name, u.is_platform_admin
		 FROM sessions s JOIN users u ON u.id = s.user_id
		 WHERE s.token_hash=$1 AND s.revoked_at IS NULL AND s.expires_at > now() AND NOT u.disabled`,
		tokenHash(c.Value)).Scan(&u.ID, &u.Email, &u.Name, &u.IsAdmin)
	return u, err
}

// issueSession creates a revocable server session for a user and returns the raw token.
func (a *app) issueSession(ctx context.Context, userID int64) (string, error) {
	token, err := randomString(32)
	if err != nil {
		return "", err
	}
	_, err = a.db.Exec(ctx,
		`INSERT INTO sessions (token_hash, user_id, expires_at) VALUES ($1, $2, now() + make_interval(secs => $3))`,
		tokenHash(token), userID, sessionTTL.Seconds())
	return token, err
}

// Platform admin: invitations and tier assignment. Both require the verified platform admin session.

var validTiers = map[string]bool{"essential": true, "growth": true, "premier": true}

const invitationTTL = 7 * 24 * time.Hour

func (a *app) adminCreateInvitation(w http.ResponseWriter, r *http.Request, u currentUser) {
	if !u.IsAdmin {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	var in struct {
		Email string `json:"email"`
		Tier  string `json:"tier"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	email, ok := normalizeEmail(in.Email)
	if !ok || !validTiers[in.Tier] {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	ctx := r.Context()
	tx, err := a.db.Begin(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(ctx)
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM users WHERE email=$1)`, email).Scan(&exists); err != nil {
		serverError(w, err)
		return
	}
	if exists {
		writeError(w, http.StatusConflict, "account_exists")
		return
	}
	var id int64
	if err := tx.QueryRow(ctx,
		`INSERT INTO invitations (email, tier, created_by, expires_at) VALUES ($1, $2, $3, now() + make_interval(secs => $4)) RETURNING id`,
		email, in.Tier, u.ID, invitationTTL.Seconds()).Scan(&id); err != nil {
		serverError(w, err)
		return
	}
	// Send before commit: if delivery fails the invitation rolls back and the admin can retry.
	body := "You have been invited to Tidal. Sign in with the Google account for this email address at " +
		a.cfg.PublicOrigin + "/login. The invitation expires in 7 days."
	if err := a.mail.send(email, "You are invited to Tidal", body); err != nil {
		log.Printf("invitation email not sent: %v", err)
		writeError(w, http.StatusServiceUnavailable, "email_unavailable")
		return
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events (user_id, action, detail) VALUES ($1, 'invitation.created', jsonb_build_object('invitation', $2::bigint, 'tier', $3::text))`,
		u.ID, id, in.Tier); err != nil {
		serverError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id})
}

func (a *app) adminUpdateUser(w http.ResponseWriter, r *http.Request, u currentUser) {
	if !u.IsAdmin {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	var in struct {
		Tier string `json:"tier"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if !validTiers[in.Tier] {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	ctx := r.Context()
	tx, err := a.db.Begin(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE users SET tier=$2 WHERE id=$1`, id, in.Tier)
	if err != nil {
		serverError(w, err)
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events (user_id, action, detail) VALUES ($1, 'user.tier_changed', jsonb_build_object('target', $2::bigint, 'tier', $3::text))`,
		u.ID, id, in.Tier); err != nil {
		serverError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "tier": in.Tier})
}

// adminListUsers returns every account so the admin UI can pick a user for a tier change by selection.
// Only the platform admin may read it. Password hashes and Google subjects are never returned.
func (a *app) adminListUsers(w http.ResponseWriter, r *http.Request, u currentUser) {
	if !u.IsAdmin {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	rows, err := a.db.Query(r.Context(),
		`SELECT id, email, name, tier, is_platform_admin, disabled, created_at FROM users ORDER BY id LIMIT 500`)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	type userRow struct {
		ID        int64     `json:"id"`
		Email     string    `json:"email"`
		Name      string    `json:"name"`
		Tier      string    `json:"tier"`
		IsAdmin   bool      `json:"is_platform_admin"`
		Disabled  bool      `json:"disabled"`
		CreatedAt time.Time `json:"created_at"`
	}
	out := []userRow{}
	for rows.Next() {
		var x userRow
		if err := rows.Scan(&x.ID, &x.Email, &x.Name, &x.Tier, &x.IsAdmin, &x.Disabled, &x.CreatedAt); err != nil {
			serverError(w, err)
			return
		}
		out = append(out, x)
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": out})
}
