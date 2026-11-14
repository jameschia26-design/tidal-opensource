package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// These tests need a disposable PostgreSQL. Set TEST_DATABASE_URL (used by parent CI) to a database
// that may be wiped; they skip otherwise. Never point this at a real company database.
func testApp(t *testing.T) (*app, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public`); err != nil {
		t.Fatal(err)
	}
	if err := migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	cfg := config{PublicOrigin: "http://app.test"}
	return &app{cfg: cfg, db: pool, mail: newMailer(cfg)}, pool
}

// seedSession creates a user with a live session and returns the cookie value.
func seedSession(t *testing.T, a *app, email string) string {
	t.Helper()
	ctx := context.Background()
	var id int64
	if err := a.db.QueryRow(ctx,
		`INSERT INTO users (email, password_hash) VALUES ($1, 'x') RETURNING id`, email).Scan(&id); err != nil {
		t.Fatal(err)
	}
	token, _ := randomString(32)
	if _, err := a.db.Exec(ctx,
		`INSERT INTO sessions (token_hash, user_id, expires_at) VALUES ($1, $2, now() + interval '1 hour')`,
		tokenHash(token), id); err != nil {
		t.Fatal(err)
	}
	return token
}

func do(t *testing.T, h http.Handler, method, path, body, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://app.test")
	if token != "" {
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// Concurrent creates by one standard user must yield exactly one company.
func TestOneCompanyLimitUnderConcurrency(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	token := seedSession(t, a, "solo@example.com")
	body := `{"name":"Acme Pte Ltd","uen":"","fiscal_year_end":"12-31","gst_registered":false}`

	var wg sync.WaitGroup
	codes := make(chan int, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes <- do(t, h, http.MethodPost, "/api/v1/organizations", body, token).Code
		}()
	}
	wg.Wait()
	close(codes)
	created, limited := 0, 0
	for c := range codes {
		switch c {
		case http.StatusCreated:
			created++
		case http.StatusConflict:
			limited++
		default:
			t.Fatalf("unexpected status %d", c)
		}
	}
	if created != 1 || limited != 11 {
		t.Fatalf("created=%d limited=%d, want 1 and 11", created, limited)
	}
	var n int
	a.db.QueryRow(context.Background(), `SELECT count(*) FROM organizations`).Scan(&n)
	if n != 1 {
		t.Fatalf("organizations rows=%d", n)
	}
}

// User B must not read or write company A's contacts, even by passing A's org_id.
func TestOrgIsolation(t *testing.T) {
	a, pool := testApp(t)
	h := a.routes()
	ctx := context.Background()
	tokenA := seedSession(t, a, "a@example.com")
	tokenB := seedSession(t, a, "b@example.com")

	rec := do(t, h, http.MethodPost, "/api/v1/organizations",
		`{"name":"Org A","uen":"","fiscal_year_end":"12-31","gst_registered":false}`, tokenA)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create org A: %d %s", rec.Code, rec.Body)
	}
	var orgA int64
	pool.QueryRow(ctx, `SELECT id FROM organizations WHERE name='Org A'`).Scan(&orgA)
	do(t, h, http.MethodPost, "/api/v1/organizations",
		`{"name":"Org B","uen":"","fiscal_year_end":"12-31","gst_registered":false}`, tokenB)

	path := "/api/v1/contacts"
	body := `{"org_id":` + itoa(orgA) + `,"role":"customer","name":"Secret Client","email":"c@example.com"}`
	if rec := do(t, h, http.MethodPost, path, body, tokenB); rec.Code != http.StatusForbidden {
		t.Fatalf("B wrote into A: %d", rec.Code)
	}
	if rec := do(t, h, http.MethodGet, path+"?role=customer&org_id="+itoa(orgA), "", tokenB); rec.Code != http.StatusForbidden {
		t.Fatalf("B read A: %d", rec.Code)
	}
	if rec := do(t, h, http.MethodPost, path, body, tokenA); rec.Code != http.StatusCreated {
		t.Fatalf("A write own: %d %s", rec.Code, rec.Body)
	}
	// B has exactly one company (Org B), so omitting org_id resolves to B's own company, never A's.
	rec = do(t, h, http.MethodGet, path+"?role=customer", "", tokenB)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "Secret Client") {
		t.Fatalf("B default org leaked or failed: %d %s", rec.Code, rec.Body)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
