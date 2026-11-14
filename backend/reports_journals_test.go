package main

import (
	"context"
	"testing"
)

// TestJournalAndCashValidationReports covers the journal report over a posted manual journal and a sale,
// and the cash validation check that settlements line up with invoices.
func TestJournalAndCashValidationReports(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "journals-owner@example.com")
	org := orgFromList(t, a, owner, `{"name":"Journals","fiscal_year_end":"12-31"}`)
	o := itoa(org)
	// Seed the standard accounts (they are created on first write) before asking for the Bank id.
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
	expense := accID(t, h, `"6501"`, o, owner, "expense")
	// Manual journal: Dr expense, Cr bank.
	expectStatus(t, do(t, h, "POST", "/api/v1/journals", `{"org_id":`+o+`,"entry_date":"2026-02-03","memo":"Paper","idempotency_key":"man-1","lines":[{"account_id":`+expense+`,"debit_cents":1500},{"account_id":`+bank+`,"credit_cents":1500}]}`, owner), 201)
	got := expectStatus(t, do(t, h, "GET", "/api/v1/reports/journals?org_id="+o+"&from=2026-02-01&to=2026-02-28", "", owner), 200)
	if len(got["rows"].([]any)) != 2 || got["debit_cents"].(float64) != 1500 || got["credit_cents"].(float64) != 1500 {
		t.Fatal(got)
	}
	if got["rows"].([]any)[0].(map[string]any)["source"].(string) != "manual:man-1" {
		t.Fatal(got["rows"])
	}
	// Customer invoice plus a part payment: balance equals the unpaid part.
	cust := itoa(int64(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"customer","name":"Payer"}`, owner), 201)["id"].(float64)))
	inv := expectStatus(t, do(t, h, "POST", "/api/v1/invoices", `{"org_id":`+o+`,"contact_id":`+cust+`,"invoice_date":"2026-02-05","due_date":"2026-02-05","lines":[{"description":"work","quantity":2,"unit_price_cents":1000}]}`, owner), 201)
	invID := itoa(int64(inv["id"].(float64)))
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+invID+"/post?org_id="+o, "", owner), 200)
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+invID+"/payments", `{"org_id":`+o+`,"amount_cents":500,"payment_date":"2026-02-06","idempotency_key":"cv-pay-1"}`, owner), 201)
	got = expectStatus(t, do(t, h, "GET", "/api/v1/reports/cash-validation?org_id="+o+"&as_of=2026-02-28", "", owner), 200)
	row := got["rows"].([]any)[0].(map[string]any)
	if row["invoiced_cents"].(float64) != 2000 || row["paid_cents"].(float64) != 500 || row["balance_cents"].(float64) != 1500 {
		t.Fatal(row)
	}
}
