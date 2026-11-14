package main

import "testing"

func TestDocumentActivity(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "activity-owner@example.com")
	outsider := seedSession(t, a, "activity-other@example.com")
	org := orgFromList(t, a, owner, `{"name":"Activity","fiscal_year_end":"12-31"}`)
	o := itoa(org)
	cust := itoa(int64(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"customer","name":"Client"}`, owner), 201)["id"].(float64)))
	inv := expectStatus(t, do(t, h, "POST", "/api/v1/invoices", `{"org_id":`+o+`,"contact_id":`+cust+`,"invoice_date":"2026-10-05","due_date":"2026-10-05","lines":[{"description":"work","quantity":1,"unit_price_cents":5000}]}`, owner), 201)
	id := itoa(int64(inv["id"].(float64)))
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+id+"/post", `{"org_id":`+o+`}`, owner), 200)
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+id+"/payments", `{"org_id":`+o+`,"amount_cents":2000,"payment_date":"2026-10-20","idempotency_key":"act-pay-1"}`, owner), 201)
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+id+"/credit-notes", `{"org_id":`+o+`,"amount_cents":500,"credit_date":"2026-10-21","reason":"damaged"}`, owner), 201)

	got := expectStatus(t, do(t, h, "GET", "/api/v1/reports/document-activity?org_id="+o+"&kind=receivable&from=2026-10-01&to=2026-10-31&contact_id="+cust, "", owner), 200)
	rows := got["rows"].([]any)
	if len(rows) != 3 || got["net_cents"] != float64(2500) {
		t.Fatal(rows, got)
	}
	kinds := []string{}
	for _, v := range rows {
		kinds = append(kinds, v.(map[string]any)["kind"].(string))
	}
	if kinds[0] != "Invoice" || kinds[1] != "Payment" || kinds[2] != "Credit note" {
		t.Fatal(kinds)
	}
	if pay := rows[1].(map[string]any); pay["amount_cents"] != float64(-2000) {
		t.Fatal(pay)
	}
	// Out-of-range dates return nothing; a wrong kind is refused.
	if r := expectStatus(t, do(t, h, "GET", "/api/v1/reports/document-activity?org_id="+o+"&kind=receivable&from=2026-11-01&to=2026-11-30", "", owner), 200); len(r["rows"].([]any)) != 0 {
		t.Fatal(r)
	}
	expectStatus(t, do(t, h, "GET", "/api/v1/reports/document-activity?org_id="+o+"&kind=other&from=2026-10-01&to=2026-10-31", "", owner), 400)
	expectStatus(t, do(t, h, "GET", "/api/v1/reports/document-activity?org_id="+o+"&kind=receivable&from=2026-10-31&to=2026-10-01", "", owner), 400)
	expectStatus(t, do(t, h, "GET", "/api/v1/reports/document-activity?org_id="+o+"&kind=receivable&from=2026-10-01&to=2026-10-31", "", outsider), 403)
}
