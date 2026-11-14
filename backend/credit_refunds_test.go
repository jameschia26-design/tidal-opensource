package main

import "testing"

func TestRefundOpenCreditPostsBankAndCapsAmount(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "credit-refund-owner@example.com")
	other := seedSession(t, a, "credit-refund-other@example.com")
	org := orgFromList(t, a, owner, `{"name":"Refund Co","fiscal_year_end":"12-31"}`)
	o := itoa(org)
	id := func(m map[string]any) string { return itoa(int64(m["id"].(float64))) }
	cust := id(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"customer","name":"Refundee"}`, owner), 201))
	rev := id(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+o+`,"code":"4100","name":"Sales","type":"revenue"}`, owner), 201))
	credit := id(expectStatus(t, do(t, h, "POST", "/api/v1/credit-notes", `{"org_id":`+o+`,"contact_id":`+cust+`,"credit_date":"2026-10-05","lines":[{"description":"Return","account_id":`+rev+`,"quantity":1,"unit_price_cents":5000}]}`, owner), 201))
	expectStatus(t, do(t, h, "POST", "/api/v1/credit-notes/"+credit+"/stage", `{"org_id":`+o+`,"stage":"open"}`, owner), 200)
	path := "/api/v1/customer-credit-notes/" + credit + "/refund"
	body := func(amt, key string) string {
		return `{"org_id":` + o + `,"amount_cents":` + amt + `,"refund_date":"2026-10-07","idempotency_key":"` + key + `"}`
	}
	expectStatus(t, do(t, h, "POST", path, body("1000", "refund-x"), other), 403)
	expectStatus(t, do(t, h, "POST", path, `{"org_id":`+o+`,"amount_cents":1000,"refund_date":"2026-10-04","idempotency_key":"early-refund"}`, owner), 422)
	first := expectStatus(t, do(t, h, "POST", path, body("3000", "refund-01"), owner), 201)
	if first["credit_balance_cents"] != float64(2000) {
		t.Fatal(first)
	}
	notes := expectStatus(t, do(t, h, "GET", "/api/v1/credit-notes?org_id="+o, "", owner), 200)["credit_notes"].([]any)
	if notes[0].(map[string]any)["balance_cents"] != float64(2000) {
		t.Fatal(notes)
	}
	inv := id(expectStatus(t, do(t, h, "POST", "/api/v1/invoices", `{"org_id":`+o+`,"contact_id":`+cust+`,"invoice_date":"2026-10-01","due_date":"2026-10-31","lines":[{"description":"Work","quantity":1,"unit_price_cents":10000}]}`, owner), 201))
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+inv+"/post?org_id="+o, "", owner), 200)
	expectStatus(t, do(t, h, "POST", "/api/v1/customer-credit-notes/"+credit+"/apply", `{"org_id":`+o+`,"invoice_id":`+inv+`,"amount_cents":2001,"applied_on":"2026-10-08"}`, owner), 422)
	// Same key, same request: returns the original result, no second journal.
	expectStatus(t, do(t, h, "POST", path, body("3000", "refund-01"), owner), 200)
	// Same key, different amount: conflict.
	expectStatus(t, do(t, h, "POST", path, body("1000", "refund-01"), owner), 409)
	// More than remains on the credit.
	expectStatus(t, do(t, h, "POST", path, body("2500", "refund-02"), owner), 422)
	expectStatus(t, do(t, h, "POST", path, body("2000", "refund-03"), owner), 201)
}
