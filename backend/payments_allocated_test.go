package main

import (
	"strings"
	"testing"
)

func TestAllocatedPaymentsSettleSeveralDocuments(t *testing.T) {
	a, pool := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "pa-owner@example.com")
	other := seedSession(t, a, "pa-other@example.com")
	org := orgFromList(t, a, owner, `{"name":"Allocated","fiscal_year_end":"12-31"}`)
	o := itoa(org)
	id := func(m map[string]any) int64 { return int64(m["id"].(float64)) }
	cust := id(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"customer","name":"Buyer"}`, owner), 201))
	sup := id(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"supplier","name":"Seller"}`, owner), 201))
	bank := id(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+o+`,"code":"1020","name":"DBS","type":"asset"}`, owner), 201))
	fees := id(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+o+`,"code":"6900","name":"Bank fees","type":"expense"}`, owner), 201))
	inv := func(amount int) int64 {
		i := id(expectStatus(t, do(t, h, "POST", "/api/v1/invoices", `{"org_id":`+o+`,"contact_id":`+itoa(cust)+`,"invoice_date":"2026-10-01","due_date":"2026-11-01","lines":[{"description":"Work","quantity":1,"unit_price_cents":`+itoa(int64(amount))+`}]}`, owner), 201))
		expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+itoa(i)+"/post?org_id="+o, "", owner), 200)
		return i
	}
	i1, i2 := inv(10000), inv(5000)
	draft := id(expectStatus(t, do(t, h, "POST", "/api/v1/invoices", `{"org_id":`+o+`,"contact_id":`+itoa(cust)+`,"invoice_date":"2026-10-01","due_date":"2026-11-01","lines":[{"description":"Work","quantity":1,"unit_price_cents":100}]}`, owner), 201))
	body := func(key string, amount int64, charge, chargeAcct int64, allocs string) string {
		return `{"org_id":` + o + `,"contact_id":` + itoa(cust) + `,"payment_date":"2026-10-20","payment_mode":"bank_transfer","account_id":` + itoa(bank) + `,"amount_cents":` + itoa(amount) + `,"bank_charge_cents":` + itoa(charge) + `,"bank_charge_account_id":` + itoa(chargeAcct) + `,"idempotency_key":"` + key + `","allocations":` + allocs + `}`
	}
	credited := inv(1000)
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+itoa(credited)+"/credit-notes", `{"org_id":`+o+`,"credit_date":"2026-10-10","reason":"Correction","amount_cents":400}`, owner), 201)
	expectStatus(t, do(t, h, "POST", "/api/v1/payments-received", body("credit-overpay", 601, 0, 0, `[{"id":`+itoa(credited)+`,"amount_cents":601}]`), owner), 422)
	two := `[{"id":` + itoa(i1) + `,"amount_cents":10000},{"id":` + itoa(i2) + `,"amount_cents":2000}]`
	expectStatus(t, do(t, h, "POST", "/api/v1/payments-received", body("pay-00001", 12000, 0, 0, two), other), 403)
	expectStatus(t, do(t, h, "POST", "/api/v1/payments-received", body("pay-00001", 11000, 0, 0, two), owner), 422)                                         // mismatch
	expectStatus(t, do(t, h, "POST", "/api/v1/payments-received", body("pay-00001", 12000, 100, 0, two), owner), 400)                                       // charge without account
	expectStatus(t, do(t, h, "POST", "/api/v1/payments-received", body("pay-00001", 12000, 100, bank, two), owner), 422)                                    // charge account not expense
	expectStatus(t, do(t, h, "POST", "/api/v1/payments-received", body("pay-00001", 100, 0, 0, `[{"id":`+itoa(draft)+`,"amount_cents":100}]`), owner), 422) // draft
	expectStatus(t, do(t, h, "POST", "/api/v1/payments-received", body("pay-00001", 6000, 0, 0, `[{"id":`+itoa(i2)+`,"amount_cents":6000}]`), owner), 422)  // overpay
	expectStatus(t, do(t, h, "POST", "/api/v1/payments-received", body("pay-00001", 12000, 0, 0, `[{"id":`+itoa(i1)+`,"amount_cents":6000},{"id":`+itoa(i1)+`,"amount_cents":6000}]`), owner), 400)
	// A failed payment keeps the draft; a successful payment consumes it atomically.
	paymentDraft := id(expectStatus(t, do(t, h, "POST", "/api/v1/payment-drafts", `{"org_id":`+o+`,"side":"received","data":{}}`, owner), 201))
	withDraft := func(s string) string { return strings.TrimSuffix(s, "}") + `,"draft_id":` + itoa(paymentDraft) + `}` }
	expectStatus(t, do(t, h, "POST", "/api/v1/payments-received", withDraft(body("pay-00001", 12000, 100, bank, two)), owner), 422)
	var draftCount int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM payment_drafts WHERE org_id=$1 AND id=$2`, org, paymentDraft).Scan(&draftCount); err != nil || draftCount != 1 {
		t.Fatal(draftCount, err)
	}
	// 12000 received, 150 withheld as bank charge: bank +11850, fees +150, AR -12000.
	made := expectStatus(t, do(t, h, "POST", "/api/v1/payments-received", withDraft(body("pay-00001", 12000, 150, fees, two)), owner), 201)
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM payment_drafts WHERE org_id=$1 AND id=$2`, org, paymentDraft).Scan(&draftCount); err != nil || draftCount != 0 {
		t.Fatal(draftCount, err)
	}
	if again := expectStatus(t, do(t, h, "POST", "/api/v1/payments-received", withDraft(body("pay-00001", 12000, 150, fees, two)), owner), 200); again["id"] != made["id"] {
		t.Fatal("draft retry created another payment")
	}
	if made["payment_number"] != "PR-00001" {
		t.Fatal(made)
	}
	if again := expectStatus(t, do(t, h, "POST", "/api/v1/payments-received", withDraft(body("pay-00001", 12000, 150, fees, two)), owner), 200); again["id"] != made["id"] {
		t.Fatal("replay created another")
	}
	expectStatus(t, do(t, h, "POST", "/api/v1/payments-received", body("pay-00001", 12000, 0, 0, two), owner), 409)
	bal := func(acct int64) int64 {
		var n int64
		if err := pool.QueryRow(t.Context(), `SELECT COALESCE(sum(debit_cents-credit_cents),0) FROM journal_lines WHERE org_id=$1 AND account_id=$2`, org, acct).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if bal(bank) != 11850 || bal(fees) != 150 {
		t.Fatalf("bank %d fees %d", bal(bank), bal(fees))
	}
	var left int64
	if err := pool.QueryRow(t.Context(), `SELECT sum(total_cents) - (SELECT sum(amount_cents) FROM payments WHERE org_id=$1) FROM invoices WHERE org_id=$1 AND status='posted' AND id <> $2`, org, credited).Scan(&left); err != nil || left != 3000 {
		t.Fatal(left, err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE payments_received SET notes='x' WHERE org_id=$1`, org); err == nil {
		t.Fatal("payment mutable")
	}
	if l := expectStatus(t, do(t, h, "GET", "/api/v1/payments-received?org_id="+o, "", owner), 200)["payments"].([]any); len(l) != 1 {
		t.Fatal(l)
	}
	expectStatus(t, do(t, h, "GET", "/api/v1/entities/payments-received/"+itoa(id(made))+"/attachments?org_id="+o, "", owner), 200)

	// Vendor side.
	bill := func(amount int64) int64 {
		b := id(expectStatus(t, do(t, h, "POST", "/api/v1/bills", `{"org_id":`+o+`,"contact_id":`+itoa(sup)+`,"supplier_reference":"B-`+itoa(amount)+`","bill_date":"2026-10-01","due_date":"2026-11-01","lines":[{"account_id":`+itoa(fees)+`,"description":"Service","quantity":1,"unit_price_cents":`+itoa(amount)+`}]}`, owner), 201))
		expectStatus(t, do(t, h, "POST", "/api/v1/bills/"+itoa(b)+"/post?org_id="+o, "", owner), 200)
		return b
	}
	b1, b2 := bill(8000), bill(2000)
	pay := func(key, contact string, amount int64, allocs string) string {
		return `{"org_id":` + o + `,"contact_id":` + contact + `,"payment_date":"2026-10-25","payment_mode":"cheque","account_id":` + itoa(bank) + `,"amount_cents":` + itoa(amount) + `,"idempotency_key":"` + key + `","allocations":` + allocs + `}`
	}
	bills := `[{"id":` + itoa(b1) + `,"amount_cents":8000},{"id":` + itoa(b2) + `,"amount_cents":1000}]`
	vendorDraft := id(expectStatus(t, do(t, h, "POST", "/api/v1/payment-drafts", `{"org_id":`+o+`,"side":"made","data":{}}`, owner), 201))
	vendorBody := func(contact string, draftID int64) string {
		return strings.TrimSuffix(pay("mad-00001", contact, 9000, bills), "}") + `,"draft_id":` + itoa(draftID) + `}`
	}
	expectStatus(t, do(t, h, "POST", "/api/v1/payments-made", vendorBody(itoa(sup), paymentDraft), owner), 404)
	expectStatus(t, do(t, h, "POST", "/api/v1/payments-made", vendorBody(itoa(cust), vendorDraft), owner), 422)
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM payment_drafts WHERE org_id=$1 AND id=$2`, org, vendorDraft).Scan(&draftCount); err != nil || draftCount != 1 {
		t.Fatal(draftCount, err)
	}
	m := expectStatus(t, do(t, h, "POST", "/api/v1/payments-made", vendorBody(itoa(sup), vendorDraft), owner), 201)
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM payment_drafts WHERE org_id=$1 AND id=$2`, org, vendorDraft).Scan(&draftCount); err != nil || draftCount != 0 {
		t.Fatal(draftCount, err)
	}
	expectStatus(t, do(t, h, "POST", "/api/v1/payments-made", vendorBody(itoa(sup), vendorDraft), owner), 200)
	if m["payment_number"] != "PM-00001" {
		t.Fatal(m)
	}
	expectStatus(t, do(t, h, "POST", "/api/v1/payments-made", pay("mad-00002", itoa(sup), 1500, `[{"id":`+itoa(b2)+`,"amount_cents":1500}]`), owner), 422) // only 1000 left
	if bal(bank) != 11850-9000 {
		t.Fatalf("bank after vendor payment %d", bal(bank))
	}
	if l := expectStatus(t, do(t, h, "GET", "/api/v1/payments-made?org_id="+o, "", owner), 200)["payments"].([]any); len(l) != 1 {
		t.Fatal(l)
	}
}
