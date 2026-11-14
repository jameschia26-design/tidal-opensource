package main

import (
	"strings"
	"testing"
)

func TestHashRoundTrip(t *testing.T) {
	h, err := hashSecret("123456")
	if err != nil {
		t.Fatal(err)
	}
	if !verifySecret("123456", h) {
		t.Fatal("correct secret rejected")
	}
	if verifySecret("123457", h) {
		t.Fatal("wrong secret accepted")
	}
	if verifySecret("123456", "$argon2id$garbage") {
		t.Fatal("malformed hash accepted")
	}
}

func TestNewCodeIsSixDigits(t *testing.T) {
	for i := 0; i < 200; i++ {
		c, err := newCode()
		if err != nil || !codeRe.MatchString(c) {
			t.Fatalf("bad code %q err=%v", c, err)
		}
	}
}

func TestNormalizeEmail(t *testing.T) {
	got, valid := normalizeEmail(" Owner@Example.COM ")
	if !valid || got != "owner@example.com" {
		t.Fatalf("normalize got %q valid=%v", got, valid)
	}
	for _, bad := range []string{"", "a@b", "Name <a@b.com>", "a@b.com\r\nBcc: x@y.z", "a b@c.com"} {
		if _, valid := normalizeEmail(bad); valid {
			t.Fatalf("accepted %q", bad)
		}
	}
}

func TestFiscalYearEnd(t *testing.T) {
	for _, s := range []string{"12-31", "02-29", "06-30"} {
		if !validFiscalYearEnd(s) {
			t.Fatalf("rejected %s", s)
		}
	}
	for _, s := range []string{"02-30", "13-01", "00-10", "12-32", "1231", "12-31 "} {
		if validFiscalYearEnd(s) {
			t.Fatalf("accepted %s", s)
		}
	}
}

func TestLoadConfigFailsClosed(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	if _, err := loadConfig(env(map[string]string{})); err == nil {
		t.Fatal("missing database URL accepted")
	}
	base := map[string]string{"TIDAL_DATABASE_URL": "postgres://x", "TIDAL_PUBLIC_ORIGIN": "http://a.test/path"}
	if _, err := loadConfig(env(base)); err == nil {
		t.Fatal("origin with path accepted")
	}
	for _, origin := range []string{"https://a.test?x=1", "https://a.test?", "https://a.test#fragment", "https://user:password@a.test"} {
		if _, err := loadConfig(env(map[string]string{"TIDAL_DATABASE_URL": "postgres://x", "TIDAL_PUBLIC_ORIGIN": origin})); err == nil {
			t.Fatalf("non-origin URL accepted: %q", origin)
		}
	}
	prod := map[string]string{"TIDAL_DATABASE_URL": "postgres://x", "TIDAL_PUBLIC_ORIGIN": "http://a.test", "TIDAL_ENV": "production"}
	if _, err := loadConfig(env(prod)); err == nil || !strings.Contains(err.Error(), "https") {
		t.Fatal("http origin accepted in production")
	}
}

// A bad proxy list stops startup instead of silently trusting nothing or everyone.
func TestLoadConfigRejectsBadTrustedProxies(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	base := map[string]string{"TIDAL_DATABASE_URL": "postgres://x", "TIDAL_PUBLIC_ORIGIN": "https://a.test"}
	for _, bad := range []string{"proxy.internal", "10.0.0.0/33", "1.2.3.4,proxy.internal", "0.0.0.0/0", "::/0"} {
		m := map[string]string{"TIDAL_TRUSTED_PROXIES": bad}
		for k, v := range base {
			m[k] = v
		}
		if _, err := loadConfig(env(m)); err == nil {
			t.Fatalf("bad proxy list accepted: %q", bad)
		}
	}
	good := map[string]string{"TIDAL_TRUSTED_PROXIES": "10.1.2.3, 172.16.0.0/12, 2001:db8::/32"}
	for k, v := range base {
		good[k] = v
	}
	c, err := loadConfig(env(good))
	if err != nil || len(c.TrustedProxies) != 3 {
		t.Fatalf("valid proxy list refused: %v (%d)", err, len(c.TrustedProxies))
	}
}

// The proxy secret is enforced at startup in production (minimum 32 characters) while development
// accepts an empty secret. The message must not repeat the value.
func TestLoadConfigRequiresProxySecretInProduction(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	base := map[string]string{"TIDAL_DATABASE_URL": "postgres://x", "TIDAL_PUBLIC_ORIGIN": "https://a.test", "TIDAL_ENV": "production"}
	for _, m := range []map[string]string{
		{},
		{"TIDAL_PROXY_SECRET": "short"},
		{"TIDAL_PROXY_SECRET": "0123456789abcdef0123456789abcde"}, // 31 chars
	} {
		for k, v := range base {
			m[k] = v
		}
		_, err := loadConfig(env(m))
		if err == nil || !strings.Contains(err.Error(), "TIDAL_PROXY_SECRET") {
			t.Fatalf("short or missing proxy secret accepted in production: %+v", m)
		}
		if got := err.Error(); strings.Contains(got, m["TIDAL_PROXY_SECRET"]) && m["TIDAL_PROXY_SECRET"] != "" {
			t.Fatalf("startup error echoes the secret: %q", got)
		}
	}
	ok := map[string]string{
		"TIDAL_DATABASE_URL": "postgres://x", "TIDAL_PUBLIC_ORIGIN": "https://a.test", "TIDAL_ENV": "production",
		"TIDAL_PROXY_SECRET": "0123456789abcdef0123456789abcdef", // 32 chars
	}
	if _, err := loadConfig(env(ok)); err != nil {
		t.Fatalf("32-char proxy secret refused: %v", err)
	}
	dev := map[string]string{"TIDAL_DATABASE_URL": "postgres://x", "TIDAL_PUBLIC_ORIGIN": "https://a.test"}
	if c, err := loadConfig(env(dev)); err != nil || c.ProxySecret != "" {
		t.Fatalf("empty proxy secret refused in development: %v", err)
	}
}

func TestMailerRefusesWhenUnconfigured(t *testing.T) {
	if err := newMailer(config{}).send("a@b.co", "s", "b"); err != errEmailUnavailable {
		t.Fatalf("got %v", err)
	}
}
