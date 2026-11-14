package main

import (
	"context"
	"testing"
)

// TestWatchlist checks the dashboard watchlist: only flagged accounts appear, with their posted balance.
func TestWatchlist(t *testing.T) {
	a, pool := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "watch-owner@example.com")
	org := orgFromList(t, a, owner, `{"name":"Watch","fiscal_year_end":"12-31"}`)
	o := itoa(org)
	expense := accID(t, h, `"6501"`, o, owner, "expense")
	// A flagged account with no postings so far.
	watch := expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+o+`,"code":"1005","name":"Cash","type":"asset","show_on_dashboard":true}`, owner), 201)
	watchID := itoa(int64(watch["id"].(float64)))
	// Unflagged account must not show up.
	accID(t, h, `"1006"`, o, owner, "asset")
	expectStatus(t, do(t, h, "POST", "/api/v1/journals", `{"org_id":`+o+`,"entry_date":"2026-03-02","memo":"seed","idempotency_key":"w-1","lines":[{"account_id":`+expense+`,"debit_cents":800},{"account_id":`+watchID+`,"credit_cents":800}]}`, owner), 201)
	got := expectStatus(t, do(t, h, "GET", "/api/v1/reports/watchlist?org_id="+o, "", owner), 200)
	rows := got["accounts"].([]any)
	if len(rows) != 1 {
		t.Fatal(rows)
	}
	row := rows[0].(map[string]any)
	if row["code"].(string) != "1005" || row["balance_cents"].(float64) != -800 {
		t.Fatal(row)
	}
	// A future-dated posting is not part of a balance to date.
	today, _ := parseDay(reportToday())
	future := today.AddDate(0, 0, 1).Format("2006-01-02")
	expectStatus(t, do(t, h, "POST", "/api/v1/journals", `{"org_id":`+o+`,"entry_date":"`+future+`","memo":"future","idempotency_key":"w-future","lines":[{"account_id":`+expense+`,"debit_cents":500},{"account_id":`+watchID+`,"credit_cents":500}]}`, owner), 201)
	got = expectStatus(t, do(t, h, "GET", "/api/v1/reports/watchlist?org_id="+o, "", owner), 200)
	if row = got["accounts"].([]any)[0].(map[string]any); row["balance_cents"].(float64) != -800 {
		t.Fatalf("future posting counted in watchlist: %v", row)
	}
	// A viewer can read the watchlist; an outsider cannot.
	viewer := seedSession(t, a, "watch-viewer@example.com")
	if _, err := pool.Exec(context.Background(), `INSERT INTO memberships(org_id,user_id,role) SELECT $1,id,'viewer' FROM users WHERE email='watch-viewer@example.com'`, org); err != nil {
		t.Fatal(err)
	}
	expectStatus(t, do(t, h, "GET", "/api/v1/reports/watchlist?org_id="+o, "", viewer), 200)
	outsider := seedSession(t, a, "watch-out@example.com")
	expectStatus(t, do(t, h, "GET", "/api/v1/reports/watchlist?org_id="+o, "", outsider), 403)
}
