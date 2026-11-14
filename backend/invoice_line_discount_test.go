package main

import "testing"

// A line discount shrinks that line's amount, and the stored total still equals the sum of line amounts.
// Out-of-range percentages are refused before anything is written.
func TestInvoiceLineDiscount(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "line-disc@example.com")
	org := orgFromList(t, a, owner, `{"name":"Line discounts","fiscal_year_end":"12-31"}`)
	o := itoa(org)
	cust := seedContact(t, a, org, owner)
	body := func(lines string) string {
		return `{"org_id":` + o + `,"contact_id":` + itoa(cust) + `,"invoice_date":"2026-03-01","due_date":"2026-03-01","lines":[` + lines + `]}`
	}
	for _, bad := range []string{
		`{"description":"Over","quantity":1,"unit_price_cents":100,"discount_basis_points":10001}`,
		`{"description":"Negative","quantity":1,"unit_price_cents":100,"discount_basis_points":-1}`,
	} {
		expectStatus(t, do(t, h, "POST", "/api/v1/invoices", body(bad), owner), 400)
	}
	created := expectStatus(t, do(t, h, "POST", "/api/v1/invoices", body(
		`{"description":"Ten off","quantity":1,"unit_price_cents":10000,"discount_basis_points":1000},
		 {"description":"Free","quantity":1,"unit_price_cents":500,"discount_basis_points":10000},
		 {"description":"Half","quantity":3,"unit_price_cents":333,"discount_basis_points":5000}`), owner), 201)
	if created["total_cents"].(float64) != 9000+0+500 {
		t.Fatalf("total = %v, want 9500", created["total_cents"])
	}
	id := itoa(int64(created["id"].(float64)))
	detail := expectStatus(t, do(t, h, "GET", "/api/v1/invoices/"+id+"?org_id="+o, "", owner), 200)
	lines := detail["lines"].([]any)
	want := []struct {
		amount float64
		bp     float64
	}{{9000, 1000}, {0, 10000}, {500, 5000}}
	for i, w := range want {
		got := lines[i].(map[string]any)
		if got["amount_cents"].(float64) != w.amount || got["discount_basis_points"].(float64) != w.bp {
			t.Fatalf("line %d = %v, want amount %v bp %v", i+1, got, w.amount, w.bp)
		}
	}
	// Posting books the discounted net, so the ledger agrees with the total.
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+id+"/post?org_id="+o, "", owner), 200)
	posted := expectStatus(t, do(t, h, "GET", "/api/v1/invoices/"+id+"?org_id="+o, "", owner), 200)
	if posted["invoice"].(map[string]any)["total_cents"].(float64) != 9500 {
		t.Fatal(posted["invoice"])
	}
}
