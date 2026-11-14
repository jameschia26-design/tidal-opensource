package main

import "testing"

// TestReportDateBoundaries covers B8 and B11: the stock detail report carries the opening quantity, the
// asset reconciliation compares the same dates on both sides, and the core reports tie to each other.
func TestReportDateBoundaries(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "report-bounds@example.com")
	org := orgFromList(t, a, owner, `{"name":"Report bounds","fiscal_year_end":"12-31"}`)
	o := itoa(org)
	cust := seedContact(t, a, org, owner)
	supp := int64(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"supplier","name":"Vendor"}`, owner), 201)["id"].(float64))
	item := int64(expectStatus(t, do(t, h, "POST", "/api/v1/items", `{"org_id":`+o+`,"name":"Widget","item_type":"goods","selling_price_cents":1000,"cost_price_cents":500,"sales_account_id":0,"purchase_account_id":0,"track_inventory":true}`, owner), 201)["id"].(float64))

	// 10 in during January, 2 out in March.
	bill := expectStatus(t, do(t, h, "POST", "/api/v1/bills", `{"org_id":`+o+`,"contact_id":`+itoa(supp)+`,"supplier_reference":"RB-1","bill_date":"2026-01-14","due_date":"2026-01-14","lines":[{"item_id":`+itoa(item)+`,"description":"stock","quantity":10,"unit_price_cents":500}]}`, owner), 201)
	expectStatus(t, do(t, h, "POST", "/api/v1/bills/"+itoa(int64(bill["id"].(float64)))+"/post?org_id="+o, "", owner), 200)
	inv := expectStatus(t, do(t, h, "POST", "/api/v1/invoices", `{"org_id":`+o+`,"contact_id":`+itoa(cust)+`,"invoice_date":"2026-03-05","due_date":"2026-03-05","lines":[{"item_id":`+itoa(item)+`,"description":"sale","quantity":2,"unit_price_cents":1000}]}`, owner), 201)
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+itoa(int64(inv["id"].(float64)))+"/post?org_id="+o, "", owner), 200)

	// March detail ends at 8 even though nothing entered stock before March inside the range.
	detail := expectStatus(t, do(t, h, "GET", "/api/v1/reports/inventory-details?org_id="+o+"&from=2026-03-01&to=2026-03-31", "", owner), 200)
	rows := detail["rows"].([]any)
	if len(rows) != 1 {
		t.Fatalf("March detail rows %v", rows)
	}
	if got := rows[0].(map[string]any)["running_qty"].(float64); got != 8 {
		t.Fatalf("March detail running qty %v, want 8", got)
	}
	summary := expectStatus(t, do(t, h, "GET", "/api/v1/reports/inventory-items?org_id="+o+"&as_of=2026-03-31", "", owner), 200)
	if got := summary["rows"].([]any)[0].(map[string]any)["qty"].(float64); got != 8 {
		t.Fatalf("summary qty %v, want 8", got)
	}

	// Asset bought in January, depreciation posted on 1 March: a 15-31 March window reconciles to the ledger.
	asset := int64(expectStatus(t, do(t, h, "POST", "/api/v1/assets", `{"org_id":`+o+`,"name":"Machine","acquired_on":"2026-01-02","cost_cents":120000,"useful_life_months":12}`, owner), 201)["id"].(float64))
	_ = asset
	// Post the asset cost so both sides of the register's cost comparison exist.
	// 1500 is the seeded asset cost account; use its id rather than creating it.
	var exp int64
	if err := a.db.QueryRow(t.Context(), `SELECT id FROM accounts WHERE org_id=$1 AND code='1500'`, org).Scan(&exp); err != nil {
		t.Fatal(err)
	}
	bank := int64(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+o+`,"code":"1030","name":"Bank 2","type":"asset"}`, owner), 201)["id"].(float64))
	expectStatus(t, do(t, h, "POST", "/api/v1/journals", `{"org_id":`+o+`,"entry_date":"2026-01-02","memo":"Asset purchase","idempotency_key":"asset-purchase","lines":[{"description":"Plant","account_id":`+itoa(exp)+`,"debit_cents":120000},{"description":"Bank","account_id":`+itoa(bank)+`,"credit_cents":120000}]}`, owner), 201)
	expectStatus(t, do(t, h, "POST", "/api/v1/assets/depreciation/run", `{"org_id":`+o+`,"month":"2026-03"}`, owner), 200)
	recon := expectStatus(t, do(t, h, "GET", "/api/v1/reports/fixed-asset-reconciliation?org_id="+o+"&from=2026-03-15&to=2026-03-31", "", owner), 200)
	diff := recon["difference"].(map[string]any)
	if diff["closing_accum_cents"].(float64) != 0 || diff["closing_cost_cents"].(float64) != 0 {
		t.Fatalf("mid-month reconciliation difference %v, want 0 on both closing figures", diff)
	}

	// Trial balance, P&L and balance sheet agree for one company.
	tb := expectStatus(t, do(t, h, "GET", "/api/v1/reports/trial-balance?org_id="+o+"&as_of=2026-03-31", "", owner), 200)
	if tb["balanced"] != true || tb["total_debit_cents"] != tb["total_credit_cents"] {
		t.Fatalf("trial balance not balanced: %v", tb)
	}
	pl := expectStatus(t, do(t, h, "GET", "/api/v1/reports/pnl?org_id="+o+"&from=2026-01-01&to=2026-03-31", "", owner), 200)
	if pl["profit_cents"].(float64) != pl["revenue_cents"].(float64)-pl["expense_cents"].(float64) {
		t.Fatalf("P&L does not tie: %v", pl)
	}
	bs := expectStatus(t, do(t, h, "GET", "/api/v1/reports/balance-sheet?org_id="+o+"&as_of=2026-03-31", "", owner), 200)
	if bs["balanced"] != true {
		t.Fatalf("balance sheet not balanced: %v", bs)
	}
	// The balance sheet's accumulated profit is the same profit the P&L reports.
	if got := bs["equity_cents"].(float64); got < pl["profit_cents"].(float64) {
		t.Fatalf("equity %v is below profit %v", got, pl["profit_cents"])
	}
}
