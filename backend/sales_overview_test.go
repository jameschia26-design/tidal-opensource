package main

import "testing"

func TestSalesOverview(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "overview-owner@example.com")
	other := seedSession(t, a, "overview-other@example.com")
	org := itoa(orgFromList(t, a, owner, `{"name":"Overview","fiscal_year_end":"12-31"}`))
	customer := itoa(int64(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+org+`,"role":"customer","name":"Buyer"}`, owner), 201)["id"].(float64)))
	invoice := itoa(int64(expectStatus(t, do(t, h, "POST", "/api/v1/invoices", `{"org_id":`+org+`,"contact_id":`+customer+`,"invoice_date":"2026-10-01","due_date":"2026-10-10","lines":[{"description":"Work","quantity":1,"unit_price_cents":10000}]}`, owner), 201)["id"].(float64)))
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+invoice+"/post?org_id="+org, "", owner), 200)
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+invoice+"/credit-notes", `{"org_id":`+org+`,"credit_date":"2026-10-15","reason":"Adjustment","amount_cents":2000}`, owner), 201)
	path := "/api/v1/sales-overview?org_id=" + org + "&as_of=2026-10-20"
	expectStatus(t, do(t, h, "GET", path, "", other), 403)
	result := expectStatus(t, do(t, h, "GET", path, "", owner), 200)
	if result["awaiting_payment"] != float64(1) || result["overdue"] != float64(1) || result["outstanding_cents"] != float64(8000) {
		t.Fatal(result)
	}
}
