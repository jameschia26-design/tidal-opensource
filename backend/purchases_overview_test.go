package main

import "testing"

func TestPurchasesOverview(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "purchase-overview@example.com")
	other := seedSession(t, a, "purchase-overview-other@example.com")
	org := itoa(orgFromList(t, a, owner, `{"name":"Purchases","fiscal_year_end":"12-31"}`))
	supplier := itoa(int64(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+org+`,"role":"supplier","name":"Vendor"}`, owner), 201)["id"].(float64)))
	account := itoa(int64(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+org+`,"code":"6100","name":"Services","type":"expense"}`, owner), 201)["id"].(float64)))
	bill := itoa(int64(expectStatus(t, do(t, h, "POST", "/api/v1/bills", `{"org_id":`+org+`,"contact_id":`+supplier+`,"supplier_reference":"B1","bill_date":"2026-10-01","due_date":"2026-10-10","lines":[{"account_id":`+account+`,"description":"Work","quantity":1,"unit_price_cents":10000}]}`, owner), 201)["id"].(float64)))
	expectStatus(t, do(t, h, "POST", "/api/v1/bills/"+bill+"/post?org_id="+org, "", owner), 200)
	path := "/api/v1/purchases-overview?org_id=" + org + "&as_of=2026-10-20"
	expectStatus(t, do(t, h, "GET", path, "", other), 403)
	result := expectStatus(t, do(t, h, "GET", path, "", owner), 200)
	if result["awaiting_payment"] != float64(1) || result["overdue"] != float64(1) || result["outstanding_cents"] != float64(10000) {
		t.Fatal(result)
	}
}
