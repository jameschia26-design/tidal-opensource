package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// brevoEndpoint is Brevo's transactional email API. HTTPS only, fixed host.
const brevoEndpoint = "https://api.brevo.com/v3/smtp/email"

var errEmailUnavailable = errors.New("email delivery is not configured")

// mailer sends transactional email through the Brevo HTTP API. Login codes fail closed (503)
// until the API key and sender address are configured.
type mailer struct {
	apiKey    string
	fromEmail string
	fromName  string
	client    *http.Client
	endpoint  string
	// sink captures messages in tests instead of calling the network.
	sink func(to, subject, body string) error
}

func newMailer(c config) *mailer {
	return &mailer{
		apiKey:    c.BrevoAPIKey,
		fromEmail: c.MailFromEmail,
		fromName:  c.MailFromName,
		client:    &http.Client{Timeout: 10 * time.Second},
		endpoint:  brevoEndpoint,
	}
}

// configured reports whether sending is possible.
func (m *mailer) configured() bool {
	return m != nil && (m.sink != nil || (m.apiKey != "" && m.fromEmail != ""))
}

// send posts one plain-text message and requires Brevo to answer 201 with a messageId, like the
// flashcards service. The response body is never logged: it can echo recipient or account data.
func (m *mailer) send(to, subject, body string) error {
	if !m.configured() {
		return errEmailUnavailable
	}
	if m.sink != nil {
		return m.sink(to, subject, body)
	}
	if strings.ContainsAny(to+subject, "\r\n") {
		return errors.New("refusing header injection")
	}
	payload, err := json.Marshal(map[string]any{
		"sender":      map[string]string{"email": m.fromEmail, "name": m.fromName},
		"to":          []map[string]string{{"email": to}},
		"subject":     subject,
		"textContent": body,
	})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("api-key", m.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := m.client.Do(req)
	if err != nil {
		// Do not wrap err: url.Error carries the request URL, and network text is not useful to users.
		return errors.New("brevo request failed (network or timeout)")
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("brevo rejected message with status %d", resp.StatusCode)
	}
	var out struct {
		MessageID string `json:"messageId"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || out.MessageID == "" {
		return errors.New("brevo response missing messageId")
	}
	return nil
}
