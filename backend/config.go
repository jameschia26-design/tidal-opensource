package main

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
)

// config holds server settings. Secrets come from the environment only and are never logged.
type config struct {
	ListenAddr         string
	DatabaseURL        string
	PublicOrigin       string // exact browser origin, e.g. https://tidal.example
	Production         bool
	AdminEmail         string
	AdminPassword      string
	AdminPasswordReset bool
	GoogleClientID     string
	GoogleClientSecret string
	BrevoAPIKey        string
	MailFromEmail      string
	MailFromName       string
	// TrustedProxies lists the addresses whose X-Forwarded-For headers are believed
	// (TIDAL_TRUSTED_PROXIES, comma/space separated IPs or CIDRs). Loopback is always trusted.
	TrustedProxies []*net.IPNet
	// ProxySecret authenticates the frontend's signed client-IP headers (TIDAL_PROXY_SECRET, same
	// value in the Vercel and backend environments). Empty disables the signature check.
	ProxySecret string
}

// loadConfig reads TIDAL_* variables. get is os.Getenv in production and a map in tests.
func loadConfig(get func(string) string) (config, error) {
	c := config{
		ListenAddr:         envOr(get, "TIDAL_LISTEN_ADDR", ":8080"),
		DatabaseURL:        get("TIDAL_DATABASE_URL"),
		PublicOrigin:       strings.TrimRight(get("TIDAL_PUBLIC_ORIGIN"), "/"),
		Production:         get("TIDAL_ENV") == "production",
		AdminEmail:         strings.ToLower(strings.TrimSpace(get("TIDAL_ADMIN_EMAIL"))),
		AdminPassword:      get("TIDAL_ADMIN_PASSWORD"),
		AdminPasswordReset: get("TIDAL_ADMIN_PASSWORD_RESET") == "1",
		GoogleClientID:     get("TIDAL_GOOGLE_CLIENT_ID"),
		GoogleClientSecret: get("TIDAL_GOOGLE_CLIENT_SECRET"),
		BrevoAPIKey:        get("TIDAL_BREVO_API_KEY"),
		MailFromEmail:      get("TIDAL_MAIL_FROM_EMAIL"),
		MailFromName:       envOr(get, "TIDAL_MAIL_FROM_NAME", "Tidal"),
		TrustedProxies:     nil, // parsed below so a malformed list fails fast
		ProxySecret:        get("TIDAL_PROXY_SECRET"),
	}
	if c.DatabaseURL == "" {
		return c, errors.New("TIDAL_DATABASE_URL is required")
	}
	u, err := url.Parse(c.PublicOrigin)
	if err != nil || u.Hostname() == "" || u.Path != "" || u.User != nil || u.RawQuery != "" ||
		u.ForceQuery || u.Fragment != "" || (u.Scheme != "https" && u.Scheme != "http") {
		return c, errors.New("TIDAL_PUBLIC_ORIGIN must be an absolute origin such as https://tidal.example")
	}
	if c.Production && u.Scheme != "https" {
		return c, errors.New("TIDAL_PUBLIC_ORIGIN must use https in production")
	}
	if c.Production && len(c.ProxySecret) < 32 {
		// The value itself is never echoed: a short secret is a configuration problem, not a credential leak.
		return c, errors.New("TIDAL_PROXY_SECRET must be at least 32 characters in production")
	}
	trusted, err := parseTrustedProxies(get("TIDAL_TRUSTED_PROXIES"))
	if err != nil {
		return c, err
	}
	c.TrustedProxies = trusted
	return c, nil
}

// parseTrustedProxies accepts explicit IPs and CIDRs and fails fast on anything else: a typo must
// stop startup instead of silently trusting nothing. Loopback needs no entry. Private ranges are
// never trusted implicitly; listing them is an explicit operator decision. A catch-all prefix is
// refused because it would trust X-Forwarded-For from every peer, including the client itself.
func parseTrustedProxies(raw string) ([]*net.IPNet, error) {
	var out []*net.IPNet
	for _, f := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ' ' }) {
		var n *net.IPNet
		if strings.Contains(f, "/") {
			_, parsed, err := net.ParseCIDR(f)
			if err != nil {
				return nil, fmt.Errorf("TIDAL_TRUSTED_PROXIES entry %q is not an IP or CIDR", f)
			}
			n = parsed
		} else {
			ip := net.ParseIP(f)
			if ip == nil {
				return nil, fmt.Errorf("TIDAL_TRUSTED_PROXIES entry %q is not an IP or CIDR", f)
			}
			bits := net.CIDRMask(32, 32)
			if ip.To4() == nil {
				bits = net.CIDRMask(128, 128)
			}
			n = &net.IPNet{IP: ip, Mask: bits}
		}
		if ones, _ := n.Mask.Size(); ones == 0 {
			return nil, fmt.Errorf("TIDAL_TRUSTED_PROXIES entry %q would trust every peer", f)
		}
		out = append(out, n)
	}
	return out, nil
}

// secureCookies is true whenever the public origin is https.
func (c config) secureCookies() bool { return strings.HasPrefix(c.PublicOrigin, "https://") }

func envOr(get func(string) string, key, def string) string {
	if v := get(key); v != "" {
		return v
	}
	return def
}
