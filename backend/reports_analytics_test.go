package main

import (
	"context"
	"testing"
)

// TestAnalyticsReport covers the analytics endpoint: monthly revenue and spending series headline
// totals that agree with the cash and account balances open receivables and payables the due-document
// forecast and the ratios built on them. A viewer may read but not write.
func TestAnalyticsReport(t *testing.T) {
	a, _ := testApp(t)
	mailSink(a)
	h := a.routes()
	owner := seedSession(t, a, "analytics-owner@example.com")
	org := orgFromList(t, a, owner, `{"name":"Analytics","fiscal_year_end":"12-31"}`)
	o := itoa(org)
	cust := itoa(int64(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"customer","name":"Buyer"}`, owner), 201)["id"].(float64)))
	supp := itoa(int64(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"supplier","name":"Vendor"}`, owner), 201)["id"].(float64)))
	expense := accID(t, h, `"6532"`, o, owner, "expense")

	// February: posted invoice 3000 due March and posted bill 800 due April.
	inv := expectStatus(t, do(t, h, "POST", "/api/v1/invoices", `{"org_id":`+o+`,"contact_id":`+cust+`,"invoice_date":"2026-02-10","due_date":"2026-04-10","lines":[{"description":"work","quantity":1,"unit_price_cents":3000}]}`, owner), 201)
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+itoa(int64(inv["id"].(float64)))+"/post?org_id="+o, "", owner), 200)
	bill := expectStatus(t, do(t, h, "POST", "/api/v1/bills", `{"org_id":`+o+`,"contact_id":`+supp+`,"supplier_reference":"AF-1","bill_date":"2026-02-11","due_date":"2026-04-11","lines":[{"account_id":`+expense+`,"description":"cost","quantity":1,"unit_price_cents":800}]}`, owner), 201)
	expectStatus(t, do(t, h, "POST", "/api/v1/bills/"+itoa(int64(bill["id"].(float64)))+"/post?org_id="+o, "", owner), 200)

	got := expectStatus(t, do(t, h, "GET", "/api/v1/reports/analytics?org_id="+o+"&from=2026-02-01&to=2026-03-31", "", owner), 200)
	if got["from"] != "2026-02-01" || got["to"] != "2026-03-31" {
		t.Fatalf("window %v", got)
	}
	series := got["series"].([]any)
	if len(series) != 1 {
		t.Fatalf("want 1 month with movement, got %d", len(series))
	}
	first := series[0].(map[string]any)
	if first["month"] != "2026-02" || first["revenue_cents"].(float64) != 3000 || first["expense_cents"].(float64) != 800 {
		t.Fatalf("february %v", first)
	}
	totals := got["totals"].(map[string]any)
	if totals["revenue_cents"].(float64) != 3000 || totals["expense_cents"].(float64) != 800 || totals["net_cents"].(float64) != 2200 {
		t.Fatalf("totals %v", totals)
	}
	// Receivables: the unpaid 3000 invoice. Payables: the unpaid 800 bill.
	if totals["receivable_cents"].(float64) != 3000 || totals["payable_cents"].(float64) != 800 {
		t.Fatalf("open documents %v", totals)
	}
	// Forecast: 3000 in from the invoice due in March, 800 out from the bill due in April.
	forecast := got["forecast"].([]any)
	if len(forecast) != 1 {
		t.Fatalf("want 1 forecast month, got %d", len(forecast))
	}
	if forecast[0].(map[string]any)["in_cents"].(float64) != 3000 || forecast[0].(map[string]any)["out_cents"].(float64) != 800 {
		t.Fatalf("forecast %v", forecast)
	}
	ratios := got["ratios"].(map[string]any)
	if ratios["net_margin_bp"].(float64) != 7333 {
		t.Fatalf("margin %v", ratios)
	}

	// Invalid ranges are refused, as on the other report endpoints.
	expectStatus(t, do(t, h, "GET", "/api/v1/reports/analytics?org_id="+o+"&from=2026-03-01&to=2026-02-01", "", owner), 400)

	viewer := seedSession(t, a, "analytics-viewer@example.com")
	if _, err := a.db.Exec(context.Background(), `UPDATE users SET email_verified_at=now() WHERE email=$1`, "analytics-viewer@example.com"); err != nil {
		t.Fatal(err)
	}
	invitation := expectStatus(t, do(t, h, "POST", "/api/v1/members/invite", `{"org_id":`+o+`,"email":"analytics-viewer@example.com","role":"viewer"}`, owner), 201)
	expectStatus(t, do(t, h, "GET", "/api/v1/reports/analytics?org_id="+o+"&from=2026-02-01&to=2026-03-31", "", viewer), 403)
	expectStatus(t, do(t, h, "POST", "/api/v1/invitations/"+itoa(int64(invitation["invitation_id"].(float64)))+"/accept", "", viewer), 200)
	expectStatus(t, do(t, h, "GET", "/api/v1/reports/analytics?org_id="+o+"&from=2026-02-01&to=2026-03-31", "", viewer), 200)
	other := seedSession(t, a, "analytics-other@example.com")
	expectStatus(t, do(t, h, "GET", "/api/v1/reports/analytics?org_id="+o+"&from=2026-02-01&to=2026-03-31", "", other), 403)
}
