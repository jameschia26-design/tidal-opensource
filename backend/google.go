package main

import (
	"context"
	"crypto/subtle"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/jackc/pgx/v5"
	"golang.org/x/oauth2"
)

const (
	googleIssuer        = "https://accounts.google.com"
	googleStateTTL      = 10 * time.Minute
	googleBindingCookie = "tidal_google_bind"
	googleInviteCookie  = "tidal_google_invite"
)

var (
	errGoogleNotConfigured = errors.New("google sign-in is not configured")
	errGoogleFailed        = errors.New("google sign-in failed")
	errGoogleAccountExists = errors.New("email already belongs to another account")
	errDisabled            = errors.New("account disabled")
)

// googleClaims is the verified part of the Google ID token this app uses.
type googleClaims struct {
	Sub           string
	Email         string
	EmailVerified bool
	Nonce         string
}

// googleState is the OIDC client. exchange and verify are fields so tests can replace the network steps.
type googleState struct {
	oauth    oauth2.Config
	exchange func(ctx context.Context, code, pkceVerifier string) (rawIDToken string, err error)
	verify   func(ctx context.Context, rawIDToken string) (googleClaims, error)
}

// newGoogle discovers Google's OIDC configuration. Missing credentials return errGoogleNotConfigured,
// and the routes then answer 503 google_unavailable.
func newGoogle(ctx context.Context, cfg config) (*googleState, error) {
	if cfg.GoogleClientID == "" || cfg.GoogleClientSecret == "" {
		return nil, errGoogleNotConfigured
	}
	provider, err := oidc.NewProvider(ctx, googleIssuer)
	if err != nil {
		return nil, err
	}
	oc := oauth2.Config{
		ClientID:     cfg.GoogleClientID,
		ClientSecret: cfg.GoogleClientSecret,
		RedirectURL:  cfg.PublicOrigin + "/api/v1/auth/google/callback",
		Endpoint:     provider.Endpoint(),
		Scopes:       []string{oidc.ScopeOpenID, "email", "profile"},
	}
	idv := provider.Verifier(&oidc.Config{ClientID: cfg.GoogleClientID}) // checks issuer, audience, expiry, signature
	return &googleState{
		oauth: oc,
		exchange: func(ctx context.Context, code, pkce string) (string, error) {
			tok, err := oc.Exchange(ctx, code, oauth2.VerifierOption(pkce))
			if err != nil {
				return "", errGoogleFailed
			}
			raw, _ := tok.Extra("id_token").(string)
			if raw == "" {
				return "", errGoogleFailed
			}
			return raw, nil
		},
		verify: func(ctx context.Context, raw string) (googleClaims, error) {
			idt, err := idv.Verify(ctx, raw)
			if err != nil {
				return googleClaims{}, err
			}
			var c struct {
				Email         string `json:"email"`
				EmailVerified bool   `json:"email_verified"`
				Nonce         string `json:"nonce"`
			}
			if err := idt.Claims(&c); err != nil {
				return googleClaims{}, err
			}
			return googleClaims{Sub: idt.Subject, Email: strings.ToLower(c.Email), EmailVerified: c.EmailVerified, Nonce: c.Nonce}, nil
		},
	}, nil
}

// googleStart stores state, nonce and the PKCE verifier server-side, then redirects to Google.
func (a *app) googleStart(w http.ResponseWriter, r *http.Request) {
	if a.google == nil {
		writeError(w, http.StatusServiceUnavailable, "google_unavailable")
		return
	}
	ctx := r.Context()
	state, err := randomString(32)
	if err != nil {
		serverError(w, err)
		return
	}
	nonce, err := randomString(32)
	if err != nil {
		serverError(w, err)
		return
	}
	pkce := oauth2.GenerateVerifier()
	if _, err := a.db.Exec(ctx, `DELETE FROM oauth_states WHERE expires_at <= now()`); err != nil {
		serverError(w, err)
		return
	}
	binding, err := randomString(32)
	if err != nil {
		serverError(w, err)
		return
	}
	if _, err := a.db.Exec(ctx,
		`INSERT INTO oauth_states (state, nonce, code_verifier, binding_hash, expires_at)
		 VALUES ($1, $2, $3, $4, now() + make_interval(secs => $5))`,
		state, nonce, pkce, tokenHash(binding), googleStateTTL.Seconds()); err != nil {
		serverError(w, err)
		return
	}
	// Only the fixed invitations destination may cross the OAuth round trip.
	invite := ""
	if r.URL.Query().Get("next") == "/invitations" {
		invite = "1"
	}
	http.SetCookie(w, &http.Cookie{Name: googleInviteCookie, Value: invite, Path: "/api/v1/auth/google", HttpOnly: true, Secure: a.cfg.secureCookies(), SameSite: http.SameSiteLaxMode, MaxAge: int(googleStateTTL.Seconds())})
	// Browser binding: the callback is accepted only from the browser that started the flow.
	http.SetCookie(w, &http.Cookie{
		Name: googleBindingCookie, Value: binding, Path: "/api/v1/auth/google",
		HttpOnly: true, Secure: a.cfg.secureCookies(), SameSite: http.SameSiteLaxMode,
		MaxAge: int(googleStateTTL.Seconds()),
	})
	authURL := a.google.oauth.AuthCodeURL(state,
		oidc.Nonce(nonce),
		oauth2.S256ChallengeOption(pkce),
		oauth2.SetAuthURLParam("prompt", "select_account"))
	http.Redirect(w, r, authURL, http.StatusFound)
}

// googleCallback validates state (single use), exchanges the code with the PKCE verifier, verifies the
// ID token and nonce, then signs in or refuses. Every failure redirects to the login page with a reason.
func (a *app) googleCallback(w http.ResponseWriter, r *http.Request) {
	if a.google == nil {
		writeError(w, http.StatusServiceUnavailable, "google_unavailable")
		return
	}
	ctx := r.Context()
	inviteCookie, _ := r.Cookie(googleInviteCookie)
	invite := inviteCookie != nil && inviteCookie.Value == "1"
	http.SetCookie(w, &http.Cookie{Name: googleInviteCookie, Value: "", Path: "/api/v1/auth/google", HttpOnly: true, Secure: a.cfg.secureCookies(), SameSite: http.SameSiteLaxMode, MaxAge: -1})
	fail := func(reason string) {
		destination := a.cfg.PublicOrigin + "/login?error=" + url.QueryEscape(reason)
		if invite {
			destination += "&next=%2Finvitations"
		}
		http.Redirect(w, r, destination, http.StatusFound)
	}
	q := r.URL.Query()
	// The binding cookie is single use for this flow: clear it on every outcome.
	http.SetCookie(w, &http.Cookie{
		Name: googleBindingCookie, Value: "", Path: "/api/v1/auth/google",
		HttpOnly: true, Secure: a.cfg.secureCookies(), SameSite: http.SameSiteLaxMode, MaxAge: -1,
	})
	bc, cerr := r.Cookie(googleBindingCookie)
	if cerr != nil || bc.Value == "" || q.Get("error") != "" || q.Get("state") == "" || q.Get("code") == "" {
		fail("google_failed")
		return
	}
	// Fetch the row by state, compare the cookie hash in constant time, and only then delete it.
	var nonce, pkce string
	var storedBinding []byte
	err := a.db.QueryRow(ctx,
		`SELECT nonce, code_verifier, binding_hash FROM oauth_states WHERE state=$1 AND expires_at > now()`,
		q.Get("state")).Scan(&nonce, &pkce, &storedBinding)
	if errors.Is(err, pgx.ErrNoRows) {
		fail("google_failed")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if subtle.ConstantTimeCompare(storedBinding, tokenHash(bc.Value)) != 1 {
		fail("google_failed")
		return
	}
	// Single use: the same statement consumes the row; a concurrent second callback finds nothing.
	tag, err := a.db.Exec(ctx, `DELETE FROM oauth_states WHERE state=$1`, q.Get("state"))
	if err != nil {
		serverError(w, err)
		return
	}
	if tag.RowsAffected() != 1 {
		fail("google_failed")
		return
	}
	raw, err := a.google.exchange(ctx, q.Get("code"), pkce)
	if err != nil {
		log.Printf("google token exchange failed")
		fail("google_failed")
		return
	}
	claims, err := a.google.verify(ctx, raw)
	if err != nil {
		log.Printf("google id token rejected")
		fail("google_failed")
		return
	}
	if subtle.ConstantTimeCompare([]byte(claims.Nonce), []byte(nonce)) != 1 || claims.Sub == "" {
		fail("google_failed")
		return
	}
	if !claims.EmailVerified || claims.Email == "" {
		fail("google_unverified")
		return
	}
	uid, err := a.resolveGoogleUser(ctx, claims)
	switch {
	case errors.Is(err, errGoogleAccountExists):
		fail("google_account_exists")
		return
	case errors.Is(err, errDisabled):
		fail("deactivated")
		return
	case err != nil:
		serverError(w, err)
		return
	}
	token, err := a.issueSession(ctx, uid)
	if err != nil {
		serverError(w, err)
		return
	}
	a.recordLogin(r, uid)
	a.setSessionCookie(w, token, int(sessionTTL.Seconds()))
	destination := "/overview"
	if invite {
		destination = "/invitations"
	}
	http.Redirect(w, r, a.cfg.PublicOrigin+destination, http.StatusFound)
}

// resolveGoogleUser maps a verified Google identity to a user, under row locks.
//   - Known Google subject: sign in (refused if disabled).
//   - Email already used by any account: refuse. A password account is never merged by email alone.
//   - Configured platform-admin email with no account: create the admin, bound to this Google subject.
//   - Otherwise: public signup as essential; a pending invitation for this email raises the tier.
func (a *app) resolveGoogleUser(ctx context.Context, c googleClaims) (int64, error) {
	tx, err := a.db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	var id int64
	var disabled bool
	err = tx.QueryRow(ctx, `SELECT id, disabled FROM users WHERE google_sub=$1 FOR UPDATE`, c.Sub).Scan(&id, &disabled)
	if err == nil {
		if disabled {
			return 0, errDisabled
		}
		return id, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return 0, err
	}

	var other int64
	err = tx.QueryRow(ctx, `SELECT id FROM users WHERE email=$1 FOR UPDATE`, c.Email).Scan(&other)
	if err == nil {
		return 0, errGoogleAccountExists
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return 0, err
	}

	// Platform admin: only the configured address, only via verified Google, bound to this subject.
	isAdmin := a.cfg.AdminEmail != "" && c.Email == a.cfg.AdminEmail
	// Public Google signup: anyone with a verified Google email may create an essential account.
	// A pending invitation for the same email raises the tier and is consumed.
	tier := "essential"
	var invID int64
	var invOrg int64
	var invRole, invFirst, invLast string
	if !isAdmin {
		// A company invitation also carries the membership to grant; a platform invitation only raises the tier.
		err = tx.QueryRow(ctx,
			`SELECT id, tier, COALESCE(org_id,0), COALESCE(role,''), first_name, last_name
			 FROM invitations WHERE email=$1 AND org_id IS NULL AND accepted_at IS NULL AND expires_at > now()
			 ORDER BY created_at DESC LIMIT 1 FOR UPDATE`, c.Email).Scan(&invID, &tier, &invOrg, &invRole, &invFirst, &invLast)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return 0, err
		}
		if errors.Is(err, pgx.ErrNoRows) {
			invID = 0
			tier = "essential"
		}
	}
	name := strings.TrimSpace(invFirst + " " + invLast)
	if err := tx.QueryRow(ctx,
		`INSERT INTO users (email, name, google_sub, tier, is_platform_admin, email_verified_at)
		 VALUES ($1, $2, $3, $4, $5, now()) RETURNING id`,
		c.Email, name, c.Sub, tier, isAdmin).Scan(&id); err != nil {
		return 0, err
	}
	if !isAdmin && invID != 0 {
		if _, err := tx.Exec(ctx, `UPDATE invitations SET accepted_at=now() WHERE id=$1`, invID); err != nil {
			return 0, err
		}
		if invOrg != 0 && invRole != "" {
			if _, err := tx.Exec(ctx, `INSERT INTO memberships (org_id,user_id,role) VALUES ($1,$2,$3) ON CONFLICT DO NOTHING`, invOrg, id, invRole); err != nil {
				return 0, err
			}
		}
	}
	action := "user.google_signup"
	if isAdmin {
		action = "user.platform_admin_bootstrapped"
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events (user_id, action) VALUES ($1, $2)`, id, action); err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return id, nil
}
