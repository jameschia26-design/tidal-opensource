package main

import "testing"

func TestBillDiscountAccountPosting(t *testing.T) {
	a, pool := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "bill-discount-owner@example.com")
	org := orgFromList(t, a, owner, `{"name":"Discount co","fiscal_year_end":"12-31"}`)
	supplier := expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+itoa(org)+`,"role":"supplier","name":"Vendor"}`, owner), 201)
	expense := expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+itoa(org)+`,"code":"6100","name":"Purchases","type":"expense"}`, owner), 201)
	discount := expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+itoa(org)+`,"code":"6101","name":"Purchase discounts","type":"expense"}`, owner), 201)
	asset := expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+itoa(org)+`,"code":"1015","name":"Cash","type":"asset"}`, owner), 201)
	accountID := int64(discount["id"].(float64))
	body := func(discountID int64) string {
		return `{"org_id":` + itoa(org) + `,"contact_id":` + itoa(int64(supplier["id"].(float64))) + `,"supplier_reference":"DISC-1","bill_date":"2026-10-01","due_date":"2026-10-31","discount_account_id":` + itoa(discountID) + `,"lines":[{"account_id":` + itoa(int64(expense["id"].(float64))) + `,"description":"Goods","quantity":2,"unit_price_cents":1000,"discount_cents":500}]}`
	}
	expectStatus(t, do(t, h, "POST", "/api/v1/bills", body(int64(asset["id"].(float64))), owner), 400)
	created := expectStatus(t, do(t, h, "POST", "/api/v1/bills", body(accountID), owner), 201)
	id := int64(created["id"].(float64))
	path := "/api/v1/bills/" + itoa(id) + "?org_id=" + itoa(org)
	detail := expectStatus(t, do(t, h, "GET", path, "", owner), 200)
	if detail["bill"].(map[string]any)["discount_account_id"] != float64(accountID) {
		t.Fatal(detail)
	}
	posted := expectStatus(t, do(t, h, "POST", "/api/v1/bills/"+itoa(id)+"/post?org_id="+itoa(org), "", owner), 200)
	journal := int64(posted["journal_id"].(float64))
	var debit, credit int64
	if err := pool.QueryRow(t.Context(), `SELECT sum(debit_cents),sum(credit_cents) FROM journal_lines WHERE journal_id=$1`, journal).Scan(&debit, &credit); err != nil || debit != 2000 || credit != 2000 {
		t.Fatal(debit, credit, err)
	}
	var discountCredit int64
	if err := pool.QueryRow(t.Context(), `SELECT credit_cents FROM journal_lines WHERE journal_id=$1 AND account_id=$2`, journal, accountID).Scan(&discountCredit); err != nil || discountCredit != 500 {
		t.Fatal(discountCredit, err)
	}

	// A line's own discount account wins over the bill's, and credits group per account.
	own := expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+itoa(org)+`,"code":"6102","name":"Line discounts","type":"expense"}`, owner), 201)
	ownID := int64(own["id"].(float64))
	line := func(ref string, lineDiscountAccount string) string {
		return `{"org_id":` + itoa(org) + `,"contact_id":` + itoa(int64(supplier["id"].(float64))) + `,"supplier_reference":"` + ref + `","bill_date":"2026-10-01","due_date":"2026-10-31","discount_account_id":` + itoa(accountID) + `,"lines":[{"account_id":` + itoa(int64(expense["id"].(float64))) + `,"description":"A","quantity":1,"unit_price_cents":1000,"discount_cents":100` + lineDiscountAccount + `},{"account_id":` + itoa(int64(expense["id"].(float64))) + `,"description":"B","quantity":1,"unit_price_cents":1000,"discount_cents":300}]}`
	}
	expectStatus(t, do(t, h, "POST", "/api/v1/bills", line("DISC-BAD", `,"discount_account_id":`+itoa(int64(asset["id"].(float64)))), owner), 400)
	mixed := expectStatus(t, do(t, h, "POST", "/api/v1/bills", line("DISC-2", `,"discount_account_id":`+itoa(ownID)), owner), 201)
	mixedID := int64(mixed["id"].(float64))
	got := expectStatus(t, do(t, h, "GET", "/api/v1/bills/"+itoa(mixedID)+"?org_id="+itoa(org), "", owner), 200)["lines"].([]any)[0].(map[string]any)
	if got["discount_account_id"] != float64(ownID) {
		t.Fatal(got)
	}
	journal2 := int64(expectStatus(t, do(t, h, "POST", "/api/v1/bills/"+itoa(mixedID)+"/post?org_id="+itoa(org), "", owner), 200)["journal_id"].(float64))
	if err := pool.QueryRow(t.Context(), `SELECT sum(debit_cents),sum(credit_cents) FROM journal_lines WHERE journal_id=$1`, journal2).Scan(&debit, &credit); err != nil || debit != 2000 || credit != 2000 {
		t.Fatal(debit, credit, err)
	}
	var own100, bill300 int64
	if err := pool.QueryRow(t.Context(), `SELECT COALESCE(sum(credit_cents) FILTER (WHERE account_id=$2),0),COALESCE(sum(credit_cents) FILTER (WHERE account_id=$3),0) FROM journal_lines WHERE journal_id=$1`, journal2, ownID, accountID).Scan(&own100, &bill300); err != nil || own100 != 100 || bill300 != 300 {
		t.Fatal(own100, bill300, err)
	}
}
