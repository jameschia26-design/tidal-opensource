package main

import "testing"

func TestContactTransactionsSummary(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "ct-summary-owner@example.com")
	outsider := seedSession(t, a, "ct-summary-other@example.com")
	org := orgFromList(t, a, owner, `{"name":"Contact summary","fiscal_year_end":"12-31"}`)
	o := itoa(org)
	cust := itoa(int64(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"customer","name":"Client"}`, owner), 201)["id"].(float64)))
	supp := itoa(int64(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"supplier","name":"Supplier"}`, owner), 201)["id"].(float64)))
	inv := expectStatus(t, do(t, h, "POST", "/api/v1/invoices", `{"org_id":`+o+`,"contact_id":`+cust+`,"invoice_date":"2026-10-05","due_date":"2026-10-05","lines":[{"description":"work","quantity":1,"unit_price_cents":5000}]}`, owner), 201)
	id := itoa(int64(inv["id"].(float64)))
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+id+"/post", `{"org_id":`+o+`}`, owner), 200)
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+id+"/payments", `{"org_id":`+o+`,"amount_cents":2000,"payment_date":"2026-10-20","idempotency_key":"ct-pay-1"}`, owner), 201)
	expenseAcct := itoa(int64(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+o+`,"code":"6100","name":"Parts","type":"expense"}`, owner), 201)["id"].(float64)))
	bill := expectStatus(t, do(t, h, "POST", "/api/v1/bills", `{"org_id":`+o+`,"contact_id":`+supp+`,"supplier_reference":"S-1","bill_date":"2026-10-06","due_date":"2026-10-06","lines":[{"account_id":`+expenseAcct+`,"description":"parts","quantity":1,"unit_price_cents":1200}]}`, owner), 201)
	bid := itoa(int64(bill["id"].(float64)))
	expectStatus(t, do(t, h, "POST", "/api/v1/bills/"+bid+"/post", `{"org_id":`+o+`}`, owner), 200)

	got := expectStatus(t, do(t, h, "GET", "/api/v1/reports/contact-transactions?org_id="+o+"&contact_id="+cust+"&from=2026-10-01&to=2026-10-31", "", owner), 200)
	if got["contact"] != "Client" || got["net_cents"] != float64(3000) {
		t.Fatal(got)
	}
	rows := got["rows"].([]any)
	if len(rows) != 2 {
		t.Fatal(rows)
	}
	first := rows[0].(map[string]any)
	if first["kind"] != "Invoice" || first["amount_cents"] != float64(5000) || first["count"] != float64(1) {
		t.Fatal(first)
	}
	// The supplier's bill belongs to the other contact, so it does not appear here.
	suppGot := expectStatus(t, do(t, h, "GET", "/api/v1/reports/contact-transactions?org_id="+o+"&contact_id="+supp+"&from=2026-10-01&to=2026-10-31", "", owner), 200)
	if suppGot["net_cents"] != float64(1200) || len(suppGot["rows"].([]any)) != 1 {
		t.Fatal(suppGot)
	}
	expectStatus(t, do(t, h, "GET", "/api/v1/reports/contact-transactions?org_id="+o+"&from=2026-10-01&to=2026-10-31", "", owner), 400)
	expectStatus(t, do(t, h, "GET", "/api/v1/reports/contact-transactions?org_id="+o+"&contact_id=999999&from=2026-10-01&to=2026-10-31", "", owner), 404)
	expectStatus(t, do(t, h, "GET", "/api/v1/reports/contact-transactions?org_id="+o+"&contact_id="+cust+"&from=2026-10-01&to=2026-10-31", "", outsider), 403)
}
