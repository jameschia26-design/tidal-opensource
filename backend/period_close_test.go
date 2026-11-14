package main

import (
	"context"
	"testing"
)

func TestPeriodCloseBlocksOlderJournals(t *testing.T) {
	a, pool := testApp(t)
	h := a.routes()
	ctx := context.Background()
	owner := seedSession(t, a, "close-owner@example.com")
	viewer := seedSession(t, a, "close-viewer@example.com")
	org := orgFromList(t, a, owner, `{"name":"Close co","fiscal_year_end":"12-31"}`)
	if _, err := pool.Exec(ctx, `INSERT INTO memberships(org_id,user_id,role) SELECT $1,id,'viewer' FROM users WHERE email='close-viewer@example.com'`, org); err != nil {
		t.Fatal(err)
	}
	if rec := do(t, h, "GET", "/api/v1/period-close?org_id="+itoa(org), "", owner); rec.Code != 200 {
		t.Fatal(rec.Code)
	}
	expectStatus(t, do(t, h, "POST", "/api/v1/period-close", `{"org_id":`+itoa(org)+`,"closed_through":"2026-09-30"}`, viewer), 403)
	expectStatus(t, do(t, h, "POST", "/api/v1/period-close", `{"org_id":`+itoa(org)+`,"closed_through":"2026-09-30"}`, owner), 200)
	// Cannot move the close date backward.
	expectStatus(t, do(t, h, "POST", "/api/v1/period-close", `{"org_id":`+itoa(org)+`,"closed_through":"2026-06-30"}`, owner), 422)
	// Journals on or before the close date are refused by the database even if the handler is bypassed.
	acct := expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+itoa(org)+`,"code":"6400","name":"Misc","type":"expense"}`, owner), 201)
	_ = acct
	bad := `{"org_id":` + itoa(org) + `,"entry_date":"2026-09-30","memo":"late","idempotency_key":"late-1","lines":[{"account_id":` + itoa(int64(acct["id"].(float64))) + `,"debit_cents":100,"credit_cents":0},{"account_id":` + itoa(int64(acct["id"].(float64))) + `,"debit_cents":0,"credit_cents":100}]}`
	expectStatus(t, do(t, h, "POST", "/api/v1/journals", bad, owner), 422)
	ok := `{"org_id":` + itoa(org) + `,"entry_date":"2026-10-01","memo":"open","idempotency_key":"open-1","lines":[{"account_id":` + itoa(int64(acct["id"].(float64))) + `,"debit_cents":100,"credit_cents":0},{"account_id":` + itoa(int64(acct["id"].(float64))) + `,"debit_cents":0,"credit_cents":100}]}`
	expectStatus(t, do(t, h, "POST", "/api/v1/journals", ok, owner), 201)
	got := expectStatus(t, do(t, h, "GET", "/api/v1/period-close?org_id="+itoa(org), "", viewer), 200)
	if got["closed_through"] != "2026-09-30" {
		t.Fatal(got)
	}
}
