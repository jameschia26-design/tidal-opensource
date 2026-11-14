package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"golang.org/x/oauth2"
)

// fakeGoogle replaces the network steps. The ID token it "verifies" is the claims themselves.
func fakeGoogle(c googleClaims, verifyErr error) *googleState {
	return &googleState{
		oauth: oauth2.Config{ClientID: "test"},
		exchange: func(ctx context.Context, code, pkce string) (string, error) {
			if code != "good-code" {
				return "", errGoogleFailed
			}
			return "raw", nil
		},
		verify: func(ctx context.Context, raw string) (googleClaims, error) {
			return c, verifyErr
		},
	}
}

// startGoogle runs /start and returns the state value the server stored.
func startGoogle(t *testing.T, a *app) string {
	t.Helper()
	if a.google == nil {
		a.google = fakeGoogle(googleClaims{}, nil)
	}
	rec := do(t, a.routes(), http.MethodGet, "/api/v1/auth/google/start", "", "")
	if rec.Code != http.StatusFound {
		t.Fatalf("start: %d", rec.Code)
	}
	browserCookie = ""
	for _, c := range rec.Result().Cookies() {
		if c.Name == googleBindingCookie {
			browserCookie = c.Value
			if !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Path != "/api/v1/auth/google" {
				t.Fatalf("binding cookie attributes wrong: %+v", c)
			}
		}
	}
	if browserCookie == "" {
		t.Fatal("start did not set the binding cookie")
	}
	loc, _ := url.Parse(rec.Header().Get("Location"))
	if loc.Query().Get("code_challenge_method") != "S256" || loc.Query().Get("nonce") == "" {
		t.Fatalf("start URL missing PKCE or nonce: %s", loc)
	}
	return loc.Query().Get("state")
}

// browserCookie is the binding cookie set by /start for this browser (see startGoogle).
var browserCookie string

func callback(t *testing.T, a *app, state, code string) *httptest.ResponseRecorder {
	t.Helper()
	return callbackWith(t, a, state, code, browserCookie)
}

// callbackWith sends the callback with the given binding cookie value ("" = no cookie).
func callbackWith(t *testing.T, a *app, state, code, cookie string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/auth/google/callback?state="+url.QueryEscape(state)+"&code="+url.QueryEscape(code), nil)
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: googleBindingCookie, Value: cookie})
	}
	rec := httptest.NewRecorder()
	a.routes().ServeHTTP(rec, req)
	return rec
}

func TestGoogleNotConfiguredFailsClosed(t *testing.T) {
	a, _ := testApp(t)
	a.google = nil
	if rec := do(t, a.routes(), http.MethodGet, "/api/v1/auth/google/start", "", ""); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("start without config: %d", rec.Code)
	}
}

func TestGoogleStateIsSingleUseAndExpires(t *testing.T) {
	a, pool := testApp(t)
	a.google = fakeGoogle(googleClaims{Sub: "s1", Email: "new@example.test", EmailVerified: true}, nil)
	state := startGoogle(t, a)
	// Unknown state: rejected before any token exchange.
	if loc := callback(t, a, "forged", "good-code").Header().Get("Location"); !strings.Contains(loc, "error=google_failed") {
		t.Fatalf("forged state accepted: %s", loc)
	}
	// Expired state: rejected.
	if _, err := pool.Exec(context.Background(), `UPDATE oauth_states SET expires_at = now() - interval '1 second' WHERE state=$1`, state); err != nil {
		t.Fatal(err)
	}
	if loc := callback(t, a, state, "good-code").Header().Get("Location"); !strings.Contains(loc, "error=google_failed") {
		t.Fatalf("expired state accepted: %s", loc)
	}
}

func TestGoogleReplayedCallbackRejected(t *testing.T) {
	a, pool := testApp(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `INSERT INTO users (email, password_hash) VALUES ('x@example.test','h')`); err != nil {
		t.Fatal(err)
	}
	state := startGoogle(t, a)
	var nonce string
	pool.QueryRow(ctx, `SELECT nonce FROM oauth_states WHERE state=$1`, state).Scan(&nonce)
	a.google = fakeGoogle(googleClaims{Sub: "sub-x", Email: "x@example.test", EmailVerified: true, Nonce: nonce}, nil)
	// Existing password account with the same email: refused, no merge. This also consumes the state.
	first := callback(t, a, state, "good-code")
	if !strings.Contains(first.Header().Get("Location"), "error=google_account_exists") {
		t.Fatalf("first callback: got %s", first.Header().Get("Location"))
	}
	// Replay with the same state must fail even though the first attempt used it.
	if loc := callback(t, a, state, "good-code").Header().Get("Location"); !strings.Contains(loc, "error=google_failed") {
		t.Fatalf("replayed state accepted: %s", loc)
	}
}

func TestGoogleRejectsBadTokenNonceUnverifiedAndExistingPasswordAccount(t *testing.T) {
	cases := []struct {
		name      string
		claims    googleClaims
		verifyErr error
		seedUser  string // existing password account with this email
		wantErr   string
	}{
		{"bad signature/audience/expiry", googleClaims{}, errors.New("token expired"), "", "google_failed"},
		{"nonce mismatch", googleClaims{Sub: "s", Email: "a@example.test", EmailVerified: true, Nonce: "other"}, nil, "", "google_failed"},
		{"unverified email", googleClaims{Sub: "s", Email: "a@example.test", EmailVerified: false}, nil, "", "google_unverified"},
		{"password account, same email, no auto-merge", googleClaims{Sub: "s", Email: "pw@example.test", EmailVerified: true}, nil, "pw@example.test", "google_account_exists"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, pool := testApp(t)
			ctx := context.Background()
			if tc.seedUser != "" {
				if _, err := pool.Exec(ctx, `INSERT INTO users (email, password_hash) VALUES ($1,'h')`, tc.seedUser); err != nil {
					t.Fatal(err)
				}
			}
			c := tc.claims
			if c.Nonce == "" && tc.name != "bad signature/audience/expiry" {
				// Real flow: the nonce returned by Google must equal the stored nonce. Read it back.
				var stored string
				state := startGoogle(t, a)
				if err := pool.QueryRow(ctx, `SELECT nonce FROM oauth_states WHERE state=$1`, state).Scan(&stored); err != nil {
					t.Fatal(err)
				}
				c.Nonce = stored
				a.google = fakeGoogle(c, tc.verifyErr)
				loc := callback(t, a, state, "good-code").Header().Get("Location")
				if !strings.Contains(loc, "error="+tc.wantErr) {
					t.Fatalf("want %s, got %s", tc.wantErr, loc)
				}
				return
			}
			a.google = fakeGoogle(c, tc.verifyErr)
			state := startGoogle(t, a)
			loc := callback(t, a, state, "good-code").Header().Get("Location")
			if !strings.Contains(loc, "error="+tc.wantErr) {
				t.Fatalf("want %s, got %s", tc.wantErr, loc)
			}
		})
	}
}

func TestPlatformAdminBootstrapBoundToGoogleAndNoPasswordTakeover(t *testing.T) {
	a, pool := testApp(t)
	ctx := context.Background()
	a.cfg.AdminEmail = "james.chia26@gmail.com"
	// Pre-registration block: someone signs up with the admin address by password.
	if rec := do(t, a.routes(), http.MethodPost, "/api/v1/auth/signup",
		`{"email":"james.chia26@gmail.com","password":"correct horse battery"}`, ""); rec.Code != http.StatusAccepted {
		t.Fatalf("signup: %d", rec.Code)
	}
	var n int
	pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE email='james.chia26@gmail.com'`).Scan(&n)
	if n != 0 {
		t.Fatal("admin address was pre-registered by password")
	}
	// Verified Google sign-in of the admin address creates the admin, bound to the Google subject.
	a.google = fakeGoogle(googleClaims{Sub: "google-sub-james", Email: "james.chia26@gmail.com", EmailVerified: true}, nil)
	state := startGoogle(t, a)
	var stored string
	pool.QueryRow(ctx, `SELECT nonce FROM oauth_states WHERE state=$1`, state).Scan(&stored)
	a.google = fakeGoogle(googleClaims{Sub: "google-sub-james", Email: "james.chia26@gmail.com", EmailVerified: true, Nonce: stored}, nil)
	if loc := callback(t, a, state, "good-code").Header().Get("Location"); !strings.Contains(loc, "/overview") {
		t.Fatalf("admin sign-in failed: %s", loc)
	}
	var isAdmin bool
	var sub string
	pool.QueryRow(ctx, `SELECT is_platform_admin, google_sub FROM users WHERE email='james.chia26@gmail.com'`).Scan(&isAdmin, &sub)
	if !isAdmin || sub != "google-sub-james" {
		t.Fatalf("admin not bound: admin=%v sub=%q", isAdmin, sub)
	}
}

func TestInvitationAdminOnlyAndTierValidated(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	token := seedSession(t, a, "notadmin@example.test")
	if rec := do(t, h, http.MethodPost, "/api/v1/admin/invitations", `{"email":"x@example.test","tier":"growth"}`, token); rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin invite: %d", rec.Code)
	}
	if rec := do(t, h, http.MethodPost, "/api/v1/admin/invitations", `{"email":"x@example.test","tier":"root"}`, token); rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin invalid tier should still be forbidden first: %d", rec.Code)
	}
}

// Login CSRF: a callback with a valid state and code but no binding cookie (attacker-supplied
// callback opened in the victim's browser) must be rejected and must not create or sign in anyone.
func TestGoogleCallbackRequiresBindingCookie(t *testing.T) {
	a, pool := testApp(t)
	ctx := context.Background()
	state := startGoogle(t, a)
	var nonce string
	pool.QueryRow(ctx, `SELECT nonce FROM oauth_states WHERE state=$1`, state).Scan(&nonce)
	a.google = fakeGoogle(googleClaims{Sub: "victim-sub", Email: "victim@example.test", EmailVerified: true, Nonce: nonce}, nil)
	rec := callbackWith(t, a, state, "good-code", "")
	if !strings.Contains(rec.Header().Get("Location"), "error=google_failed") {
		t.Fatalf("callback without cookie accepted: %s", rec.Header().Get("Location"))
	}
	var n int
	pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE email='victim@example.test'`).Scan(&n)
	if n != 0 {
		t.Fatal("login CSRF created an account")
	}
}

// Another browser's cookie must not complete someone else's flow, and the state must survive it
// so the real browser can still finish. A rejected attempt must not consume the state.
func TestGoogleCallbackRejectsWrongBrowserCookie(t *testing.T) {
	a, pool := testApp(t)
	ctx := context.Background()
	state := startGoogle(t, a)
	var nonce string
	pool.QueryRow(ctx, `SELECT nonce FROM oauth_states WHERE state=$1`, state).Scan(&nonce)
	a.google = fakeGoogle(googleClaims{Sub: "sub-r", Email: "real@example.test", EmailVerified: true, Nonce: nonce}, nil)

	if loc := callbackWith(t, a, state, "good-code", "attacker-guess").Header().Get("Location"); !strings.Contains(loc, "error=google_failed") {
		t.Fatalf("wrong cookie accepted: %s", loc)
	}
	var left int
	pool.QueryRow(ctx, `SELECT count(*) FROM oauth_states WHERE state=$1`, state).Scan(&left)
	if left != 1 {
		t.Fatalf("rejected cookie consumed the state (left=%d)", left)
	}
	// The correct browser can still complete the flow.
	if loc := callback(t, a, state, "good-code").Header().Get("Location"); !strings.Contains(loc, "/overview") {
		t.Fatalf("correct browser blocked after a rejected attempt: %s", loc)
	}
}

// Public Google signup: a new verified Google email with no invitation becomes an essential account.
func TestGooglePublicSignupDefaultsToEssential(t *testing.T) {
	a, pool := testApp(t)
	ctx := context.Background()
	state := startGoogle(t, a)
	var nonce string
	pool.QueryRow(ctx, `SELECT nonce FROM oauth_states WHERE state=$1`, state).Scan(&nonce)
	a.google = fakeGoogle(googleClaims{Sub: "sub-new", Email: "newcomer@example.test", EmailVerified: true, Nonce: nonce}, nil)
	if loc := callback(t, a, state, "good-code").Header().Get("Location"); !strings.Contains(loc, "/overview") {
		t.Fatalf("public signup refused: %s", loc)
	}
	var tier string
	pool.QueryRow(ctx, `SELECT tier FROM users WHERE email='newcomer@example.test'`).Scan(&tier)
	if tier != "essential" {
		t.Fatalf("tier=%q, want essential", tier)
	}
}

// A pending invitation for a new Google email raises the tier and is consumed.
func TestGoogleInvitationUpgradesTier(t *testing.T) {
	a, pool := testApp(t)
	ctx := context.Background()
	admin := seedSession(t, a, "boss@example.test")
	pool.Exec(ctx, `UPDATE users SET is_platform_admin=true, google_sub='admin-sub' WHERE email='boss@example.test'`)
	// Mail is not configured in tests, so create the invitation row directly (same columns the handler writes).
	if _, err := pool.Exec(ctx, `INSERT INTO invitations (email, tier, created_by, expires_at) SELECT 'growth@example.test','growth',id, now()+interval '1 day' FROM users WHERE email='boss@example.test'`); err != nil {
		t.Fatal(err)
	}
	if rec := do(t, a.routes(), http.MethodGet, "/api/v1/admin/users", "", admin); rec.Code != http.StatusOK {
		t.Fatalf("admin list: %d", rec.Code)
	}
	state := startGoogle(t, a)
	var nonce string
	pool.QueryRow(ctx, `SELECT nonce FROM oauth_states WHERE state=$1`, state).Scan(&nonce)
	a.google = fakeGoogle(googleClaims{Sub: "sub-g", Email: "growth@example.test", EmailVerified: true, Nonce: nonce}, nil)
	callback(t, a, state, "good-code")
	var tier string
	var accepted bool
	pool.QueryRow(ctx, `SELECT tier FROM users WHERE email='growth@example.test'`).Scan(&tier)
	pool.QueryRow(ctx, `SELECT accepted_at IS NOT NULL FROM invitations WHERE email='growth@example.test' ORDER BY id DESC LIMIT 1`).Scan(&accepted)
	if tier != "growth" || !accepted {
		t.Fatalf("tier=%q accepted=%v, want growth and consumed", tier, accepted)
	}
}

// Admin users list: admin only, never exposes hashes or Google subjects.
func TestAdminUsersListAdminOnlyAndNoSecrets(t *testing.T) {
	a, pool := testApp(t)
	ctx := context.Background()
	user := seedSession(t, a, "plain@example.test")
	admin := seedSession(t, a, "root@example.test")
	pool.Exec(ctx, `UPDATE users SET is_platform_admin=true, google_sub='root-sub' WHERE email='root@example.test'`)
	if rec := do(t, a.routes(), http.MethodGet, "/api/v1/admin/users", "", user); rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin list: %d", rec.Code)
	}
	rec := do(t, a.routes(), http.MethodGet, "/api/v1/admin/users", "", admin)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "plain@example.test") {
		t.Fatalf("admin list: %d %s", rec.Code, rec.Body)
	}
	for _, leak := range []string{"password_hash", "google_sub", "root-sub", "$argon2"} {
		if strings.Contains(rec.Body.String(), leak) {
			t.Fatalf("list leaks %q", leak)
		}
	}
}

func TestGoogleInvitationDestination(t *testing.T) {
	for _, tc := range []struct{ next, want string }{
		{"/invitations", "/invitations"},
		{"https://evil.test", "/overview"},
		{"//evil.test", "/overview"},
		{"/invitations?x=1", "/overview"},
		{"", "/overview"},
	} {
		t.Run(tc.next, func(t *testing.T) {
			a, pool := testApp(t)
			a.google = fakeGoogle(googleClaims{}, nil)
			start := do(t, a.routes(), http.MethodGet, "/api/v1/auth/google/start?next="+url.QueryEscape(tc.next), "", "")
			if start.Code != http.StatusFound {
				t.Fatalf("start: %d", start.Code)
			}
			loc, err := url.Parse(start.Header().Get("Location"))
			if err != nil {
				t.Fatal(err)
			}
			state := loc.Query().Get("state")
			var nonce string
			if err := pool.QueryRow(context.Background(), `SELECT nonce FROM oauth_states WHERE state=$1`, state).Scan(&nonce); err != nil {
				t.Fatal(err)
			}
			a.google = fakeGoogle(googleClaims{Sub: "invited-sub", Email: "invite@example.test", EmailVerified: true, Nonce: nonce}, nil)
			req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/google/callback?state="+url.QueryEscape(state)+"&code=good-code", nil)
			for _, c := range start.Result().Cookies() {
				req.AddCookie(c)
			}
			rec := httptest.NewRecorder()
			a.routes().ServeHTTP(rec, req)
			if got := rec.Header().Get("Location"); got != a.cfg.PublicOrigin+tc.want {
				t.Fatalf("got %q", got)
			}
		})
	}
}
