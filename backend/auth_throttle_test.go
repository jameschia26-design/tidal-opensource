package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestPasswordResetThrottledPerIP covers R18: requests beyond the per-address limit get 429 before any
// lookup or Argon2 work, and known and unknown addresses get identical replies.
func TestPasswordResetThrottledPerIP(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	mailSink(a)
	mustVerify(t, a, "known@example.com", "correct horse battery")
	a.cfg.AdminEmail = ""

	forgot := func(ip, email string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/password/forgot",
			strings.NewReader(`{"email":"`+email+`"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", "http://app.test")
		req.RemoteAddr = ip + ":4444"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	// Under the limit: known and unknown addresses answer the same 202 and the same body.
	known := forgot("198.51.100.40", "known@example.com")
	unknown := forgot("198.51.100.41", "nobody@example.com")
	if known.Code != http.StatusAccepted || unknown.Code != http.StatusAccepted || known.Body.String() != unknown.Body.String() {
		t.Fatalf("known/unknown replies differ: %d %q vs %d %q", known.Code, known.Body, unknown.Code, unknown.Body)
	}

	// Over the limit: 429 and no Argon2 work at all, for a known and an unknown address alike.
	ip := "198.51.100.42"
	for i := 0; i < resetsPerIPHour; i++ {
		if rec := forgot(ip, "known@example.com"); rec.Code != http.StatusAccepted {
			t.Fatalf("request %d under limit: %d %s", i+1, rec.Code, rec.Body)
		}
	}
	work := argonWork(a, func() {
		for _, email := range []string{"known@example.com", "nobody@example.com"} {
			rec := forgot(ip, email)
			if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
				t.Fatalf("over limit reply: %d %q", rec.Code, rec.Body)
			}
		}
	})
	if work != 0 {
		t.Fatalf("over-limit requests spent %d Argon2 operations, want 0", work)
	}
}

// TestSignupThrottleCoversAdminAddress covers R18: the platform admin address no longer skips the per-IP signup limit.
func TestSignupThrottleCoversAdminAddress(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	a.cfg.AdminEmail = "admin@example.com"
	signup := func(ip, email string) int {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/signup",
			strings.NewReader(`{"email":"`+email+`","password":"correct horse battery"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", "http://app.test")
		req.RemoteAddr = ip + ":4444"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	ip := "198.51.100.50"
	for i := 0; i < signupsPerIPHour; i++ {
		if code := signup(ip, "person"+itoa(int64(i))+"@example.com"); code != http.StatusAccepted {
			t.Fatalf("signup %d under limit: %d", i+1, code)
		}
	}
	if code := signup(ip, "admin@example.com"); code != http.StatusTooManyRequests {
		t.Fatalf("admin signup over limit: %d, want 429", code)
	}
}
