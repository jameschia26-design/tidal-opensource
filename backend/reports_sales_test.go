package main

import "testing"

func TestSalesByCustomer(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "sales-report-owner@example.com")
	outsider := seedSession(t, a, "sales-report-other@example.com")
	org := orgFromList(t, a, owner, `{"name":"Sales report","fiscal_year_end":"12-31"}`)
	o := itoa(org)
	id := func(v map[string]any) string { return itoa(int64(v["id"].(float64))) }
	customer := id(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"customer","name":"Buyer"}`, owner), 201))
	revenue := id(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+o+`,"code":"4100","name":"Sales","type":"revenue"}`, owner), 201))
	bank := id(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+o+`,"code":"1020","name":"Bank","type":"asset"}`, owner), 201))
	invoice := id(expectStatus(t, do(t, h, "POST", "/api/v1/invoices", `{"org_id":`+o+`,"contact_id":`+customer+`,"invoice_date":"2026-10-01","due_date":"2026-10-31","lines":[{"description":"Work","quantity":1,"unit_price_cents":10000}]}`, owner), 201))
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+invoice+"/post?org_id="+o, "", owner), 200)
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+invoice+"/credit-notes", `{"org_id":`+o+`,"credit_date":"2026-10-02","reason":"Correction","amount_cents":1000}`, owner), 201)
	expectStatus(t, do(t, h, "POST", "/api/v1/sales-receipts", `{"org_id":`+o+`,"contact_id":`+customer+`,"receipt_date":"2026-10-03","payment_mode":"cash","deposit_account_id":`+bank+`,"idempotency_key":"report-key-1","lines":[{"description":"Sale","account_id":`+revenue+`,"quantity":1,"unit_price_cents":2000}]}`, owner), 201)
	creditBody := `{"org_id":` + o + `,"contact_id":` + customer + `,"credit_date":"2026-10-04","lines":[{"description":"Credit","account_id":` + revenue + `,"quantity":1,"unit_price_cents":500}]}`
	credit := id(expectStatus(t, do(t, h, "POST", "/api/v1/credit-notes", creditBody, owner), 201))
	expectStatus(t, do(t, h, "POST", "/api/v1/credit-notes/"+credit+"/stage", `{"org_id":`+o+`,"stage":"open"}`, owner), 200)
	expectStatus(t, do(t, h, "POST", "/api/v1/credit-notes", creditBody, owner), 201) // draft excluded
	path := "/api/v1/reports/sales-by-customer?org_id=" + o + "&from=2026-10-01&to=2026-10-31"
	report := expectStatus(t, do(t, h, "GET", path, "", owner), 200)
	rows := report["rows"].([]any)
	if len(rows) != 1 {
		t.Fatal(rows)
	}
	r := rows[0].(map[string]any)
	if r["invoices_cents"] != float64(10000) || r["receipts_cents"] != float64(2000) || r["credits_cents"] != float64(1500) || r["net_cents"] != float64(10500) {
		t.Fatal(r)
	}
	expectStatus(t, do(t, h, "GET", path, "", outsider), 403)
	expectStatus(t, do(t, h, "GET", "/api/v1/reports/sales-by-customer?org_id="+o+"&from=2026-11-01&to=2026-10-01", "", owner), 400)
	empty := expectStatus(t, do(t, h, "GET", "/api/v1/reports/sales-by-customer?org_id="+o+"&from=2026-11-01&to=2026-11-30", "", owner), 200)
	if len(empty["rows"].([]any)) != 0 {
		t.Fatal(empty)
	}
}
