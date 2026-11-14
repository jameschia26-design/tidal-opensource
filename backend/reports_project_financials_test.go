package main

import (
	"context"
	"testing"
)

func TestProjectFinancials(t *testing.T) {
	a, pool := testApp(t)
	h := a.routes()
	ctx := context.Background()
	owner := seedSession(t, a, "projfin-owner@example.com")
	outsider := seedSession(t, a, "projfin-other@example.com")
	org := orgFromList(t, a, owner, `{"name":"Project financials","fiscal_year_end":"12-31"}`)
	o := itoa(org)
	cust := itoa(int64(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"customer","name":"Client"}`, owner), 201)["id"].(float64)))
	project := expectStatus(t, do(t, h, "POST", "/api/v1/projects", `{"org_id":`+o+`,"contact_id":`+cust+`,"name":"Build"}`, owner), 201)
	p := itoa(int64(project["id"].(float64)))
	expectStatus(t, do(t, h, "POST", "/api/v1/time-entries", `{"org_id":`+o+`,"project_id":`+p+`,"date":"2026-10-05","task":"Design","minutes":120}`, owner), 201)
	// Labour cost needs a rate for the owner, which only exists once the owner reports a rate for themselves.
	var ownerID int64
	if err := pool.QueryRow(ctx, `SELECT id FROM users WHERE email='projfin-owner@example.com'`).Scan(&ownerID); err != nil {
		t.Fatal(err)
	}
	expectStatus(t, do(t, h, "POST", "/api/v1/staff-rates", `{"org_id":`+o+`,"user_id":`+itoa(ownerID)+`,"hourly_cents":6000}`, owner), 200)
	// Seeded accounts appear on the first write, so ask for them through the same helper the handlers use.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	seeded, err := accountIDs(ctx, tx, org)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	bank := seeded[acctBank]
	office := int64(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+o+`,"code":"6120","name":"Project costs","type":"expense"}`, owner), 201)["id"].(float64))
	expectStatus(t, do(t, h, "POST", "/api/v1/expenses", `{"org_id":`+o+`,"expense_date":"2026-10-06","payee":"Vendor","description":"Materials","expense_account_id":`+itoa(office)+`,"amount_cents":4500,"paid_through_account_id":`+itoa(bank)+`,"project_id":`+p+`,"customer_id":`+cust+`,"billable":true,"idempotency_key":"pf-exp-1"}`, owner), 201)
	expectStatus(t, do(t, h, "POST", "/api/v1/expenses", `{"org_id":`+o+`,"expense_date":"2026-09-01","payee":"Vendor","description":"Old","expense_account_id":`+itoa(office)+`,"amount_cents":100,"paid_through_account_id":`+itoa(bank)+`,"project_id":`+p+`,"customer_id":`+cust+`,"idempotency_key":"pf-exp-2"}`, owner), 201)
	inv := expectStatus(t, do(t, h, "POST", "/api/v1/invoices", `{"org_id":`+o+`,"contact_id":`+cust+`,"invoice_date":"2026-10-07","due_date":"2026-10-07","lines":[{"description":"Build work","quantity":1,"unit_price_cents":10000,"project_id":`+p+`}]}`, owner), 201)
	invID := itoa(int64(inv["id"].(float64)))
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+invID+"/post?org_id="+o, "", owner), 200)

	got := expectStatus(t, do(t, h, "GET", "/api/v1/reports/project-financials?org_id="+o+"&from=2026-10-01&to=2026-10-31", "", owner), 200)
	rows := got["rows"].([]any)
	if len(rows) != 1 {
		t.Fatal(got)
	}
	first := rows[0].(map[string]any)
	if first["minutes"] != float64(120) || first["entries"] != float64(1) {
		t.Fatal(first)
	}
	// Two hours at 6000 cents an hour is 12000 cents of labour cost.
	if first["labour_cents"] != float64(12000) {
		t.Fatal(first)
	}
	// Only the October expense counts; the September one is outside the range.
	if first["expense_cents"] != float64(4500) || first["billable_expense_cents"] != float64(4500) {
		t.Fatal(first)
	}
	// The posted invoice line carries the same project, so its revenue and gross profit count.
	if first["revenue_cents"] != float64(10000) || first["gross_profit_cents"] != float64(-6500) {
		t.Fatal(first)
	}
	empty := expectStatus(t, do(t, h, "GET", "/api/v1/reports/project-financials?org_id="+o+"&from=2026-11-01&to=2026-11-30", "", owner), 200)
	if empty["rows"].([]any)[0].(map[string]any)["minutes"] != float64(0) || empty["rows"].([]any)[0].(map[string]any)["expense_cents"] != float64(0) {
		t.Fatal(empty)
	}
	expectStatus(t, do(t, h, "GET", "/api/v1/reports/project-financials?org_id="+o+"&from=2026-10-01&to=2026-10-31&status=bad", "", owner), 400)
	expectStatus(t, do(t, h, "GET", "/api/v1/reports/project-financials?org_id="+o+"&from=2026-10-01&to=2026-10-31", "", outsider), 403)
}
