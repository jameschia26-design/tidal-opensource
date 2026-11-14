package main

import (
	"context"
	"net/http"
	"sync"
	"testing"
)

// Concurrent login requests for one account must yield at most one emailed code per cooldown
// window, and at most one active challenge afterwards. Needs TEST_DATABASE_URL.
func TestConcurrentLoginIssuesOneChallenge(t *testing.T) {
	a, pool := testApp(t)
	ctx := context.Background()
	hash, err := hashSecret("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO users (email, password_hash) VALUES ('race@example.com', $1)`, hash); err != nil {
		t.Fatal(err)
	}
	// Real mail is not sent in tests: configure a sink that accepts the message.
	a.mail = &mailer{sink: func(to, subject, body string) error { return nil }}
	h := a.routes()

	var wg sync.WaitGroup
	codes := make(chan int, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes <- do(t, h, http.MethodPost, "/api/v1/auth/login",
				`{"email":"race@example.com","password":"correct horse battery"}`, "").Code
		}()
	}
	wg.Wait()
	close(codes)
	sent := 0
	for c := range codes {
		if c == http.StatusAccepted {
			sent++
		}
	}
	if sent != 1 {
		t.Fatalf("accepted sends=%d, want exactly 1 inside the cooldown window", sent)
	}
	var active int
	pool.QueryRow(ctx, `SELECT count(*) FROM login_challenges WHERE used_at IS NULL`).Scan(&active)
	if active != 1 {
		t.Fatalf("active challenges=%d, want 1", active)
	}
}

func TestSignupIPThrottle(t *testing.T) {
	a, pool := testApp(t)
	h := a.routes()
	for i := 0; i < signupsPerIPHour; i++ {
		body := `{"email":"u` + itoa(int64(i)) + `@example.com","password":"correct horse battery"}`
		if rec := do(t, h, http.MethodPost, "/api/v1/auth/signup", body, ""); rec.Code != http.StatusAccepted {
			t.Fatalf("signup %d: %d", i, rec.Code)
		}
	}
	rec := do(t, h, http.MethodPost, "/api/v1/auth/signup",
		`{"email":"late@example.com","password":"correct horse battery"}`, "")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("signup over cap: %d", rec.Code)
	}
	var n int
	pool.QueryRow(context.Background(), `SELECT count(*) FROM users WHERE email='late@example.com'`).Scan(&n)
	if n != 0 {
		t.Fatal("throttled signup still created an account")
	}
}
