package main

import "testing"

func TestRecurringBillsRunOnceAndEnd(t *testing.T) {
	a, pool := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "recbill@example.com")
	other := seedSession(t, a, "recbill-other@example.com")
	org := orgFromList(t, a, owner, `{"name":"Recurring bills","fiscal_year_end":"12-31"}`)
	supplier := int64(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+itoa(org)+`,"role":"supplier","name":"Landlord"}`, owner), 201)["id"].(float64))
	rent := int64(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+itoa(org)+`,"code":"6300","name":"Rent","type":"expense"}`, owner), 201)["id"].(float64))
	asset := int64(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+itoa(org)+`,"code":"1015","name":"Cash","type":"asset"}`, owner), 201)["id"].(float64))
	body := func(account int64, extra string) string {
		return `{"org_id":` + itoa(org) + `,"name":"Office rent","repeat_every":1,"repeat_unit":"month","start_date":"2026-01-31"` + extra + `,"bill":{"contact_id":` + itoa(supplier) + `,"supplier_reference":"RENT","payment_terms_days":14,"lines":[{"account_id":` + itoa(account) + `,"description":"Rent","quantity":1,"unit_price_cents":200000}]}}`
	}
	// An asset-account line is a capitalised purchase, so it is accepted (R10). Stop it so it does not run.
	assetTemplate := expectStatus(t, do(t, h, "POST", "/api/v1/recurring-bills", body(asset, ""), owner), 201)
	expectStatus(t, do(t, h, "POST", "/api/v1/recurring-bills/active", `{"org_id":`+itoa(org)+`,"id":`+itoa(int64(assetTemplate["id"].(float64)))+`,"active":false}`, owner), 200)
	expectStatus(t, do(t, h, "POST", "/api/v1/recurring-bills", body(rent, ""), other), 403)
	expectStatus(t, do(t, h, "POST", "/api/v1/recurring-bills", body(rent, `,"ends_on":"2025-01-01"`), owner), 400)
	expectStatus(t, do(t, h, "POST", "/api/v1/recurring-bills", body(rent, `,"ends_on":"2026-03-31"`), owner), 201)
	run := func(asOf string) []any {
		return expectStatus(t, do(t, h, "POST", "/api/v1/recurring-bills/run", `{"org_id":`+itoa(org)+`,"as_of":"`+asOf+`"}`, owner), 200)["created_bill_ids"].([]any)
	}
	if n := len(run("2026-02-15")); n != 1 {
		t.Fatalf("first run created %d", n)
	}
	if n := len(run("2026-02-15")); n != 0 {
		t.Fatalf("repeat run created %d", n)
	}
	if n := len(run("2026-12-31")); n != 2 {
		t.Fatalf("catch-up created %d", n)
	}
	if n := len(run("2027-12-31")); n != 0 {
		t.Fatalf("run after end created %d", n)
	}
	var count, total int64
	var posted int
	if err := pool.QueryRow(t.Context(), `SELECT count(*),sum(total_cents),count(*) FILTER (WHERE status='posted') FROM bills WHERE org_id=$1`, org).Scan(&count, &total, &posted); err != nil || count != 3 || total != 600000 || posted != 0 {
		t.Fatal(count, total, posted, err)
	}
	var due string
	if err := pool.QueryRow(t.Context(), `SELECT due_date::text FROM bills WHERE org_id=$1 AND recurring_run_date='2026-02-28'`, org).Scan(&due); err != nil || due != "2026-03-14" {
		t.Fatal(due, err)
	}
	p := expectStatus(t, do(t, h, "GET", "/api/v1/recurring-bills?org_id="+itoa(org), "", owner), 200)["profiles"].([]any)[0].(map[string]any)
	if p["active"] != false || p["generated_count"] != float64(3) {
		t.Fatal(p)
	}
}
