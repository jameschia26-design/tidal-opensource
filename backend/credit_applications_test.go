package main

import "testing"

func TestApplyCustomerCreditToInvoice(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "credit-apply-owner@example.com")
	other := seedSession(t, a, "credit-apply-other@example.com")
	org := orgFromList(t, a, owner, `{"name":"Apply Co","fiscal_year_end":"12-31"}`)
	o := itoa(org)
	id := func(m map[string]any) string { return itoa(int64(m["id"].(float64))) }
	cust := id(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"customer","name":"Buyer"}`, owner), 201))
	other2 := id(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"customer","name":"Stranger"}`, owner), 201))
	rev := id(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+o+`,"code":"4100","name":"Sales","type":"revenue"}`, owner), 201))
	inv := id(expectStatus(t, do(t, h, "POST", "/api/v1/invoices", `{"org_id":`+o+`,"contact_id":`+cust+`,"invoice_date":"2026-10-01","due_date":"2026-10-31","lines":[{"description":"Work","quantity":1,"unit_price_cents":10000}]}`, owner), 201))
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+inv+"/post?org_id="+o, "", owner), 200)
	credit := id(expectStatus(t, do(t, h, "POST", "/api/v1/credit-notes", `{"org_id":`+o+`,"contact_id":`+cust+`,"credit_date":"2026-10-05","lines":[{"description":"Refund","account_id":`+rev+`,"quantity":1,"unit_price_cents":6000}]}`, owner), 201))
	expectStatus(t, do(t, h, "POST", "/api/v1/credit-notes/"+credit+"/stage", `{"org_id":`+o+`,"stage":"open"}`, owner), 200)
	apply := "/api/v1/customer-credit-notes/" + credit + "/apply"
	expectStatus(t, do(t, h, "POST", apply, `{"org_id":`+o+`,"invoice_id":`+inv+`,"amount_cents":2500,"applied_on":"2026-10-06"}`, other), 403)
	// Partial application: the rest stays available on the credit.
	part := expectStatus(t, do(t, h, "POST", apply, `{"org_id":`+o+`,"invoice_id":`+inv+`,"amount_cents":2500,"applied_on":"2026-10-06"}`, owner), 201)
	if part["invoice_balance_cents"] != float64(7500) {
		t.Fatal(part)
	}
	before := expectStatus(t, do(t, h, "GET", "/api/v1/reports/receivables-aging?org_id="+o+"&as_of=2026-10-05", "", owner), 200)
	after := expectStatus(t, do(t, h, "GET", "/api/v1/reports/receivables-aging?org_id="+o+"&as_of=2026-10-06", "", owner), 200)
	// The unapplied credit is its own negative row, so bucket totals net it. What must change is the
	// invoice's own balance, and the net total must stay the same because applying a credit only moves
	// money between two rows.
	net := func(rep map[string]any) int64 {
		var n int64
		for _, v := range rep["totals_by_bucket"].(map[string]any) {
			n += int64(v.(float64))
		}
		return n
	}
	if net(before) != 4000 || net(after) != 4000 {
		t.Fatalf("net ageing must be unchanged by an application: before=%d after=%d", net(before), net(after))
	}
	invoiceBalance := func(rep map[string]any) int64 {
		for _, r := range rep["rows"].([]any) {
			row := r.(map[string]any)
			if row["document"] == "Invoice 1" {
				return int64(row["balance_cents"].(float64))
			}
		}
		return -1
	}
	if invoiceBalance(before) != 10000 || invoiceBalance(after) != 7500 {
		t.Fatalf("aging must subtract applications on and after application date: before=%v after=%v", before, after)
	}
	invoice := expectStatus(t, do(t, h, "GET", "/api/v1/invoices/"+inv+"?org_id="+o, "", owner), 200)
	if invoice["invoice"].(map[string]any)["balance_cents"] != float64(7500) {
		t.Fatalf("invoice must reflect applied credit: %v", invoice)
	}
	// Payment and direct credit cannot exceed the remaining invoice balance.
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+inv+"/payments", `{"org_id":`+o+`,"amount_cents":7501,"payment_date":"2026-10-07","idempotency_key":"over-credit"}`, owner), 422)
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+inv+"/credit-notes", `{"org_id":`+o+`,"amount_cents":7501,"credit_date":"2026-10-07","reason":"Exceeds balance"}`, owner), 422)
	// Refused: more than the invoice still owes, or more than the credit has left.
	// Credit total is 6000: 2500 + 3500 uses it up exactly; invoice then owes 4000.
	expectStatus(t, do(t, h, "POST", apply, `{"org_id":`+o+`,"invoice_id":`+inv+`,"amount_cents":3500,"applied_on":"2026-10-06"}`, owner), 201)
	expectStatus(t, do(t, h, "POST", apply, `{"org_id":`+o+`,"invoice_id":`+inv+`,"amount_cents":2000,"applied_on":"2026-10-06"}`, owner), 422)
	// Refused: a different customer's invoice.
	otherInv := id(expectStatus(t, do(t, h, "POST", "/api/v1/invoices", `{"org_id":`+o+`,"contact_id":`+other2+`,"invoice_date":"2026-10-01","due_date":"2026-10-31","lines":[{"description":"Work","quantity":1,"unit_price_cents":5000}]}`, owner), 201))
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+otherInv+"/post?org_id="+o, "", owner), 200)
	expectStatus(t, do(t, h, "POST", apply, `{"org_id":`+o+`,"invoice_id":`+otherInv+`,"amount_cents":100,"applied_on":"2026-10-06"}`, owner), 422)
}
