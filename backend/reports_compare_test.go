package main

import (
	"context"
	"testing"
)

// TestReportCompareColumns covers the previous-period column on the profit and loss, balance sheet
// and trial balance. Each report must show the earlier figures next to the current ones.
func TestReportCompareColumns(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "compare-owner@example.com")
	org := orgFromList(t, a, owner, `{"name":"Compare","fiscal_year_end":"12-31"}`)
	o := itoa(org)
	ctx := context.Background()
	tx, err := a.db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ids, err := accountIDs(ctx, tx, org)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	bank := itoa(ids["1000"])
	sales := itoa(ids["4000"])
	expense := accID(t, h, `"6502"`, o, owner, "expense")
	// Prior year: sales 2000, expense 500.
	expectStatus(t, do(t, h, "POST", "/api/v1/journals", `{"org_id":`+o+`,"entry_date":"2025-02-10","memo":"Prior sales","idempotency_key":"cmp-1","lines":[{"account_id":`+bank+`,"debit_cents":2000},{"account_id":`+sales+`,"credit_cents":2000}]}`, owner), 201)
	expectStatus(t, do(t, h, "POST", "/api/v1/journals", `{"org_id":`+o+`,"entry_date":"2025-02-11","memo":"Prior cost","idempotency_key":"cmp-2","lines":[{"account_id":`+expense+`,"debit_cents":500},{"account_id":`+bank+`,"credit_cents":500}]}`, owner), 201)
	// Current year: sales 3000, expense 800.
	expectStatus(t, do(t, h, "POST", "/api/v1/journals", `{"org_id":`+o+`,"entry_date":"2026-02-10","memo":"Sales","idempotency_key":"cmp-3","lines":[{"account_id":`+bank+`,"debit_cents":3000},{"account_id":`+sales+`,"credit_cents":3000}]}`, owner), 201)
	expectStatus(t, do(t, h, "POST", "/api/v1/journals", `{"org_id":`+o+`,"entry_date":"2026-02-11","memo":"Cost","idempotency_key":"cmp-4","lines":[{"account_id":`+expense+`,"debit_cents":800},{"account_id":`+bank+`,"credit_cents":800}]}`, owner), 201)

	// Profit and loss: the compared window is the same dates one year earlier.
	pl := expectStatus(t, do(t, h, "GET", "/api/v1/reports/pnl?org_id="+o+"&from=2026-02-01&to=2026-02-28&compare=previous", "", owner), 200)
	if pl["revenue_cents"].(float64) != 3000 || pl["expense_cents"].(float64) != 800 {
		t.Fatalf("current pnl: %v", pl)
	}
	cmp, ok := pl["compare"].(map[string]any)
	if !ok {
		t.Fatalf("no compare block: %v", pl)
	}
	if cmp["revenue_cents"].(float64) != 2000 || cmp["expense_cents"].(float64) != 500 || cmp["profit_cents"].(float64) != 1500 {
		t.Fatalf("compare pnl: %v", cmp)
	}
	if cmp["from"].(string) != "2025-02-01" || cmp["to"].(string) != "2025-02-28" {
		t.Fatalf("compare window: %v", cmp)
	}

	// Balance sheet and trial balance compare with the same date one year earlier.
	bs := expectStatus(t, do(t, h, "GET", "/api/v1/reports/balance-sheet?org_id="+o+"&as_of=2026-12-31&compare=previous", "", owner), 200)
	bsCmp, ok := bs["compare"].(map[string]any)
	if !ok || bsCmp["as_of"].(string) != "2025-12-31" {
		t.Fatalf("balance sheet compare: %v", bs)
	}
	if bs["assets_cents"].(float64) != 3700 || bsCmp["assets_cents"].(float64) != 1500 {
		t.Fatalf("balance sheet figures: %v / %v", bs, bsCmp)
	}
	tb := expectStatus(t, do(t, h, "GET", "/api/v1/reports/trial-balance?org_id="+o+"&as_of=2026-12-31&compare=previous", "", owner), 200)
	tbCmp, ok := tb["compare"].(map[string]any)
	if !ok || tbCmp["as_of"].(string) != "2025-12-31" {
		t.Fatalf("trial balance compare: %v", tb)
	}
	if tbCmp["total_debit_cents"].(float64) != tbCmp["total_credit_cents"].(float64) {
		t.Fatalf("compared trial balance is not balanced: %v", tbCmp)
	}
	// Without the parameter the response stays the same shape as before.
	plain := expectStatus(t, do(t, h, "GET", "/api/v1/reports/pnl?org_id="+o+"&from=2026-02-01&to=2026-02-28", "", owner), 200)
	if _, ok := plain["compare"]; ok {
		t.Fatalf("compare present without asking: %v", plain)
	}
}
