package main

import (
	"encoding/json"
	"testing"
)

// Quote lines carry the same per-line percentage discount as invoices: the stored amount is the
// discounted line total, rounded half up, and the quote total is the sum of those amounts.
func TestQuoteLineDiscounts(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "quotedisc-owner@example.com")
	org := orgFromList(t, a, owner, `{"name":"QD","fiscal_year_end":"12-31"}`)
	o := itoa(org)
	cust := itoa(int64(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"customer","name":"Buyer"}`, owner), 201)["id"].(float64)))
	q := expectStatus(t, do(t, h, "POST", "/api/v1/quotes", `{"org_id":`+o+`,"contact_id":`+cust+`,"quote_date":"2026-04-01","expiry_date":"2026-05-01","lines":[{"description":"work","quantity":2,"unit_price_cents":1000,"discount_basis_points":2500},{"description":"other","quantity":1,"unit_price_cents":500}]}`, owner), 201)
	if q["total_cents"] != 2000.0 {
		t.Fatalf("total %v", q["total_cents"])
	}
	got := expectStatus(t, do(t, h, "GET", "/api/v1/quotes?org_id="+o, "", owner), 200)
	var shape struct {
		Quotes []struct {
			Lines json.RawMessage `json:"lines"`
		} `json:"quotes"`
	}
	raw, _ := json.Marshal(got)
	if err := json.Unmarshal(raw, &shape); err != nil {
		t.Fatal(err)
	}
	if len(shape.Quotes) != 1 {
		t.Fatalf("quotes %+v", shape.Quotes)
	}
	var lines []struct {
		Description      string `json:"description"`
		Quantity         int64  `json:"quantity"`
		UnitPriceCents   int64  `json:"unit_price_cents"`
		AmountCents      int64  `json:"amount_cents"`
		DiscountBasisPts int64  `json:"discount_basis_points"`
	}
	if err := json.Unmarshal(shape.Quotes[0].Lines, &lines); err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 || lines[0].DiscountBasisPts != 2500 || lines[0].AmountCents != 1500 {
		t.Fatalf("lines %+v", lines)
	}
	// Out-of-range discounts are refused at the boundary, not trimmed.
	expectStatus(t, do(t, h, "POST", "/api/v1/quotes", `{"org_id":`+o+`,"contact_id":`+cust+`,"quote_date":"2026-04-01","expiry_date":"2026-05-01","lines":[{"description":"work","quantity":1,"unit_price_cents":100,"discount_basis_points":10001}]}`, owner), 400)

	other := seedSession(t, a, "quotedisc-other@example.com")
	expectStatus(t, do(t, h, "GET", "/api/v1/quotes?org_id="+o, "", other), 403)
}
