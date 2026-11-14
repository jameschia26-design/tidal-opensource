package main

import "testing"

func TestDocumentReport(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "documents-report@example.com")
	outsider := seedSession(t, a, "documents-report-other@example.com")
	o := itoa(orgFromList(t, a, owner, `{"name":"Reports","fiscal_year_end":"12-31"}`))
	id := func(m map[string]any) string { return itoa(int64(m["id"].(float64))) }
	customer := id(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"customer","name":"Buyer"}`, owner), 201))
	invoice := id(expectStatus(t, do(t, h, "POST", "/api/v1/invoices", `{"org_id":`+o+`,"contact_id":`+customer+`,"invoice_date":"2026-10-01","due_date":"2026-10-31","lines":[{"description":"Work","quantity":1,"unit_price_cents":10000}]}`, owner), 201))
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+invoice+"/post?org_id="+o, "", owner), 200)
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+invoice+"/payments", `{"org_id":`+o+`,"payment_date":"2026-11-01","amount_cents":3000,"idempotency_key":"document-pay"}`, owner), 201)
	supplier := id(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"supplier","name":"Seller"}`, owner), 201))
	expense := id(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+o+`,"code":"6100","name":"Expense","type":"expense"}`, owner), 201))
	bill := id(expectStatus(t, do(t, h, "POST", "/api/v1/bills", `{"org_id":`+o+`,"contact_id":`+supplier+`,"supplier_reference":"B1","bill_date":"2026-10-01","due_date":"2026-10-31","lines":[{"description":"Cost","account_id":`+expense+`,"quantity":1,"unit_price_cents":4000}]}`, owner), 201))
	expectStatus(t, do(t, h, "POST", "/api/v1/bills/"+bill+"/post?org_id="+o, "", owner), 200)
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+invoice+"/credit-notes", `{"org_id":`+o+`,"credit_date":"2026-10-15","reason":"Correction","amount_cents":1000}`, owner), 201)
	expectStatus(t, do(t, h, "POST", "/api/v1/bills/"+bill+"/payments", `{"org_id":`+o+`,"payment_date":"2026-10-15","amount_cents":1000,"idempotency_key":"document-bill-pay"}`, owner), 201)
	q := "?org_id=" + o + "&from=2026-10-01&to=2026-10-31&kind="
	for _, tc := range []struct {
		kind   string
		amount float64
	}{{"receivable", 9000}, {"payable", 3000}} {
		rows := expectStatus(t, do(t, h, "GET", "/api/v1/reports/documents"+q+tc.kind, "", owner), 200)["rows"].([]any)
		if len(rows) != 1 || rows[0].(map[string]any)["balance_cents"] != tc.amount {
			t.Fatal(tc.kind, rows)
		}
	}
	for _, tc := range []struct {
		kind, description string
		amount            float64
	}{{"receivable", "Work", 10000}, {"payable", "Cost", 4000}} {
		lines := expectStatus(t, do(t, h, "GET", "/api/v1/reports/document-lines"+q+tc.kind, "", owner), 200)["rows"].([]any)
		if len(lines) != 1 || lines[0].(map[string]any)["description"] != tc.description || lines[0].(map[string]any)["amount_cents"] != tc.amount {
			t.Fatal(tc.kind, lines)
		}
	}
	if rows := expectStatus(t, do(t, h, "GET", "/api/v1/reports/document-lines"+q+"receivable&contact_id="+supplier, "", owner), 200)["rows"].([]any); len(rows) != 0 {
		t.Fatal(rows)
	}
	expectStatus(t, do(t, h, "GET", "/api/v1/reports/document-lines"+q+"receivable", "", outsider), 403)
	expectStatus(t, do(t, h, "GET", "/api/v1/reports/document-lines"+q+"bad", "", owner), 400)
	expectStatus(t, do(t, h, "GET", "/api/v1/reports/documents"+q+"receivable", "", outsider), 403)
	expectStatus(t, do(t, h, "GET", "/api/v1/reports/documents"+q+"bad", "", owner), 400)
	if rows := expectStatus(t, do(t, h, "GET", "/api/v1/reports/documents"+q+"receivable&contact_id="+supplier, "", owner), 200)["rows"].([]any); len(rows) != 0 {
		t.Fatal(rows)
	}
	if rows := expectStatus(t, do(t, h, "GET", "/api/v1/reports/documents"+q+"receivable&contact_id="+customer, "", owner), 200)["rows"].([]any); len(rows) != 1 {
		t.Fatal(rows)
	}
	expectStatus(t, do(t, h, "GET", "/api/v1/reports/documents"+q+"payable&contact_id=-1", "", owner), 400)
}
