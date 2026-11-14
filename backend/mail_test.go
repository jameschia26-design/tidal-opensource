package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// brevoFake answers like Brevo for one request and records what the client sent.
func brevoFake(t *testing.T, status int, reply string) (*mailer, *http.Request, *string) {
	t.Helper()
	var got http.Request
	var sentBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = *r
		b, _ := io.ReadAll(r.Body)
		sentBody = string(b)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(reply))
	}))
	t.Cleanup(srv.Close)
	m := &mailer{apiKey: "test-key", fromEmail: "admin@example.test", fromName: "Tidal",
		client: &http.Client{Timeout: time.Second}, endpoint: srv.URL}
	return m, &got, &sentBody
}

func TestBrevoAcceptsOnlyCreatedWithMessageID(t *testing.T) {
	m, req, body := brevoFake(t, http.StatusCreated, `{"messageId":"<abc@smtp>"}`)
	if err := m.send("user@example.test", "Code", "123456"); err != nil {
		t.Fatalf("201 with messageId rejected: %v", err)
	}
	if req.Header.Get("api-key") != "test-key" || req.Method != http.MethodPost {
		t.Fatalf("wrong request: %s %v", req.Method, req.Header)
	}
	if !contains(*body, `"textContent":"123456"`) || !contains(*body, `"email":"admin@example.test"`) {
		t.Fatalf("payload missing fields: %s", *body)
	}

	for name, tc := range map[string]struct {
		status int
		reply  string
	}{
		"200 not 201":      {http.StatusOK, `{"messageId":"x"}`},
		"201 without id":   {http.StatusCreated, `{}`},
		"201 empty id":     {http.StatusCreated, `{"messageId":""}`},
		"201 invalid json": {http.StatusCreated, `not json`},
		"401 bad key":      {http.StatusUnauthorized, `{"message":"Key not found"}`},
	} {
		m, _, _ := brevoFake(t, tc.status, tc.reply)
		if err := m.send("user@example.test", "Code", "1"); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
}

func TestBrevoTimeoutHidesNetworkDetail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
	}))
	defer srv.Close()
	m := &mailer{apiKey: "k", fromEmail: "a@b.test", client: &http.Client{Timeout: 20 * time.Millisecond}, endpoint: srv.URL}
	err := m.send("u@b.test", "s", "b")
	if err == nil || contains(err.Error(), srv.URL) {
		t.Fatalf("timeout error leaked URL or passed: %v", err)
	}
}

func TestBrevoRefusesWithoutKey(t *testing.T) {
	if err := newMailer(config{MailFromEmail: "a@b.test"}).send("u@b.test", "s", "b"); err != errEmailUnavailable {
		t.Fatalf("got %v", err)
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
