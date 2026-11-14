package main

import "testing"

func TestReceivedPaymentSurplusBecomesCustomerCredit(t *testing.T) {
	a, pool := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "excess-owner@example.com")
	org := orgFromList(t, a, owner, `{"name":"Excess Co","fiscal_year_end":"12-31"}`)
	o := itoa(org)
	id := func(m map[string]any) int64 { return int64(m["id"].(float64)) }
	cust := id(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"customer","name":"Overpayer"}`, owner), 201))
	bank := id(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+o+`,"code":"1020","name":"DBS","type":"asset"}`, owner), 201))
	inv := id(expectStatus(t, do(t, h, "POST", "/api/v1/invoices", `{"org_id":`+o+`,"contact_id":`+itoa(cust)+`,"invoice_date":"2026-10-01","due_date":"2026-11-01","lines":[{"description":"Work","quantity":1,"unit_price_cents":10000}]}`, owner), 201))
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+itoa(inv)+"/post?org_id="+o, "", owner), 200)
	body := func(key string, amount int64, allocs string) string {
		return `{"org_id":` + o + `,"contact_id":` + itoa(cust) + `,"payment_date":"2026-10-20","payment_mode":"bank_transfer","account_id":` + itoa(bank) + `,"amount_cents":` + itoa(amount) + `,"idempotency_key":"` + key + `","allocations":` + allocs + `}`
	}
	alloc := `[{"id":` + itoa(inv) + `,"amount_cents":10000}]`
	// Receipt of 12000 against a 10000 invoice: 2000 surplus becomes a customer credit.
	made := expectStatus(t, do(t, h, "POST", "/api/v1/payments-received", body("excess-00001", 12000, alloc), owner), 201)
	var surplus int64
	if err := pool.QueryRow(t.Context(), `SELECT amount_cents FROM payment_excess WHERE org_id=$1 AND payment_id=$2`, org, id(made)).Scan(&surplus); err != nil || surplus != 2000 {
		t.Fatal(surplus, err)
	}
	// Allocations larger than the receipt are still refused.
	expectStatus(t, do(t, h, "POST", "/api/v1/payments-received", body("excess-00002", 5000, alloc), owner), 422)
}
