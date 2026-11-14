package main

import "testing"

func TestSalesReceiptPostsAtOnceAndReplays(t *testing.T) {
	a, pool := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "sr-owner@example.com")
	other := seedSession(t, a, "sr-other@example.com")
	org := orgFromList(t, a, owner, `{"name":"Receipts","fiscal_year_end":"12-31"}`)
	id := func(m map[string]any) int64 { return int64(m["id"].(float64)) }
	cust := id(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+itoa(org)+`,"role":"customer","name":"Walk-in"}`, owner), 201))
	sup := id(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+itoa(org)+`,"role":"supplier","name":"Seller"}`, owner), 201))
	rev := id(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+itoa(org)+`,"code":"4100","name":"Sales","type":"revenue"}`, owner), 201))
	cash := id(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+itoa(org)+`,"code":"1015","name":"Petty cash","type":"asset"}`, owner), 201))
	body := func(contact, deposit int64, key, extra string) string {
		return `{"org_id":` + itoa(org) + `,"contact_id":` + itoa(contact) + `,"receipt_date":"2026-10-15","payment_mode":"cash","deposit_account_id":` + itoa(deposit) + `,"idempotency_key":"` + key + `","lines":[{"description":"Item","account_id":` + itoa(rev) + `,"quantity":3,"unit_price_cents":1000}]` + extra + `}`
	}
	disc := `,"commercial":{"discount_type":"flat","discount_cents":500,"shipping_cents":200}`
	expectStatus(t, do(t, h, "POST", "/api/v1/sales-receipts", body(sup, cash, "key-00001", ""), owner), 422)
	expectStatus(t, do(t, h, "POST", "/api/v1/sales-receipts", body(cust, rev, "key-00001", ""), owner), 422)
	expectStatus(t, do(t, h, "POST", "/api/v1/sales-receipts", body(cust, cash, "key-00001", ""), other), 403)
	expectStatus(t, do(t, h, "POST", "/api/v1/sales-receipts", body(cust, cash, "short", ""), owner), 400)
	// 3000 - 500 + 200 = 2700
	made := expectStatus(t, do(t, h, "POST", "/api/v1/sales-receipts", body(cust, cash, "key-00001", disc), owner), 201)
	if made["total_cents"] != float64(2700) || made["receipt_number"] != "SR-00001" {
		t.Fatal(made)
	}
	if again := expectStatus(t, do(t, h, "POST", "/api/v1/sales-receipts", body(cust, cash, "key-00001", disc), owner), 200); again["id"] != made["id"] {
		t.Fatal("replay created another")
	}
	expectStatus(t, do(t, h, "POST", "/api/v1/sales-receipts", body(cust, cash, "key-00001", ""), owner), 409)
	var bank, income int64
	if err := pool.QueryRow(t.Context(), `SELECT COALESCE(sum(debit_cents-credit_cents),0) FROM journal_lines WHERE org_id=$1 AND account_id=$2`, org, cash).Scan(&bank); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(t.Context(), `SELECT COALESCE(sum(credit_cents-debit_cents),0) FROM journal_lines WHERE org_id=$1 AND account_id=$2`, org, rev).Scan(&income); err != nil {
		t.Fatal(err)
	}
	if bank != 2700 || income != 2700 {
		t.Fatalf("deposit %d income %d", bank, income)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE sales_receipts SET reference='x' WHERE org_id=$1`, org); err == nil {
		t.Fatal("receipt mutable")
	}
	if l := expectStatus(t, do(t, h, "GET", "/api/v1/sales-receipts?org_id="+itoa(org), "", owner), 200)["sales_receipts"].([]any); len(l) != 1 {
		t.Fatal(l)
	}
	expectStatus(t, do(t, h, "GET", "/api/v1/entities/sales-receipts/"+itoa(id(made))+"/attachments?org_id="+itoa(org), "", owner), 200)
}
