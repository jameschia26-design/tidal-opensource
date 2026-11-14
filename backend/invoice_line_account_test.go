package main

import (
	"testing"
)

// A line can name its own income account; posting credits each account with its own net, and a non-revenue
// account is refused before anything is saved.
func TestInvoiceLineRevenueAccount(t *testing.T) {
	a, pool := testApp(t)
	h := a.routes()
	ctx := t.Context()
	owner := seedSession(t, a, "line-acct@example.com")
	org := orgFromList(t, a, owner, `{"name":"Line accounts","fiscal_year_end":"12-31"}`)
	o := itoa(org)
	cust := seedContact(t, a, org, owner)
	services := accID(t, h, `"4100"`, o, owner, "revenue")
	expense := accID(t, h, `"6500"`, o, owner, "expense")

	base := func(extra string) string {
		return `{"org_id":` + o + `,"contact_id":` + itoa(cust) + `,"invoice_date":"2026-03-01","due_date":"2026-03-01","lines":[
			{"description":"Goods","quantity":2,"unit_price_cents":100},
			{"description":"Service","quantity":1,"unit_price_cents":500` + extra + `}]}`
	}
	// Expense account on a sales line is refused.
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices", base(`,"revenue_account_id":`+expense), owner), 422)
	// Account from another company is refused too.
	stranger := seedSession(t, a, "line-acct-other@example.com")
	otherOrg := orgFromList(t, a, stranger, `{"name":"Other","fiscal_year_end":"12-31"}`)
	foreign := accID(t, h, `"4200"`, itoa(otherOrg), stranger, "revenue")
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices", base(`,"revenue_account_id":`+foreign), owner), 422)

	created := expectStatus(t, do(t, h, "POST", "/api/v1/invoices", base(`,"revenue_account_id":`+services), owner), 201)
	id := itoa(int64(created["id"].(float64)))
	detail := expectStatus(t, do(t, h, "GET", "/api/v1/invoices/"+id+"?org_id="+o, "", owner), 200)
	lines := detail["lines"].([]any)
	if lines[0].(map[string]any)["revenue_account_id"].(float64) != 0 || itoa(int64(lines[1].(map[string]any)["revenue_account_id"].(float64))) != services {
		t.Fatalf("account did not roundtrip: %v", lines)
	}
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+id+"/post?org_id="+o, "", owner), 200)

	rows, err := pool.Query(ctx, `SELECT a.code, jl.credit_cents FROM journal_lines jl JOIN accounts a ON a.org_id=jl.org_id AND a.id=jl.account_id
		JOIN invoices i ON i.org_id=jl.org_id AND i.journal_id=jl.journal_id WHERE i.id=$1 ORDER BY a.code`, id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	credits := map[string]int64{}
	for rows.Next() {
		var code string
		var cents int64
		if err := rows.Scan(&code, &cents); err != nil {
			t.Fatal(err)
		}
		credits[code] = cents
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if credits["4000"] != 200 || credits["4100"] != 500 {
		t.Fatalf("credits = %v, want 4000:200 and 4100:500", credits)
	}
}
