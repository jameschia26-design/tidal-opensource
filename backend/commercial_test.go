package main

import "testing"

func TestCommercialTotals(t *testing.T) {
	for _, tc := range []struct {
		sub  int64
		c    commercialFields
		want int64
		ok   bool
	}{
		{10001, commercialFields{DiscountType: "percent", DiscountBasisPoints: 1250, ShippingCents: 100, AdjustmentCents: -1}, 8850, true},
		{10000, commercialFields{DiscountCents: 1000}, 9000, true},
		{10000, commercialFields{DiscountCents: 10001}, 0, false},
		{100000000000000, commercialFields{DiscountType: "percent", DiscountBasisPoints: 9999}, 10000000000, true},
	} {
		got, ok := commercialTotal(tc.sub, tc.c)
		if ok != tc.ok || (ok && got != tc.want) {
			t.Fatalf("got %d,%v want %d,%v", got, ok, tc.want, tc.ok)
		}
	}
}

func TestInvoiceAndQuoteCommercialRoundTrip(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "commercial@example.com")
	org := orgFromList(t, a, owner, `{"name":"Commercial","fiscal_year_end":"12-31"}`)
	customer := expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+itoa(org)+`,"role":"customer","name":"Buyer"}`, owner), 201)
	common := `"org_id":` + itoa(org) + `,"contact_id":` + itoa(int64(customer["id"].(float64))) + `,"commercial":{"subject":"Consulting","salesperson":"Alex","order_number":"PO-1","discount_type":"percent","discount_basis_points":1250,"shipping_cents":100,"adjustment_cents":-1},"lines":[{"description":"Work","quantity":1,"unit_price_cents":10001}]`
	invoice := expectStatus(t, do(t, h, "POST", "/api/v1/invoices", `{`+common+`,"invoice_date":"2026-01-01","due_date":"2026-01-01"}`, owner), 201)
	if invoice["total_cents"] != float64(8850) {
		t.Fatalf("wrong invoice: %v", invoice)
	}
	path := "/api/v1/invoices/" + itoa(int64(invoice["id"].(float64)))
	detail := expectStatus(t, do(t, h, "GET", path+"?org_id="+itoa(org), "", owner), 200)["invoice"].(map[string]any)
	if detail["commercial"].(map[string]any)["subject"] != "Consulting" {
		t.Fatal(detail)
	}
	expectStatus(t, do(t, h, "POST", path+"/post?org_id="+itoa(org), "", owner), 200)
	quote := expectStatus(t, do(t, h, "POST", "/api/v1/quotes", `{`+common+`,"quote_date":"2026-01-01","expiry_date":"2026-01-02"}`, owner), 201)
	if quote["total_cents"] != float64(8850) {
		t.Fatal(quote)
	}
	qpath := "/api/v1/quotes/" + itoa(int64(quote["id"].(float64)))
	for _, stage := range []string{"sent", "accepted"} {
		expectStatus(t, do(t, h, "POST", qpath+"/stage", `{"org_id":`+itoa(org)+`,"stage":"`+stage+`"}`, owner), 200)
	}
	converted := expectStatus(t, do(t, h, "POST", qpath+"/convert", `{"org_id":`+itoa(org)+`,"invoice_date":"2026-01-02","due_date":"2026-01-02"}`, owner), 201)
	inv := expectStatus(t, do(t, h, "GET", "/api/v1/invoices/"+itoa(int64(converted["invoice_id"].(float64)))+"?org_id="+itoa(org), "", owner), 200)["invoice"].(map[string]any)
	if inv["total_cents"] != float64(8850) || inv["commercial"].(map[string]any)["order_number"] != "PO-1" {
		t.Fatal(inv)
	}
}
