package main

import "testing"

func TestContactActivity(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "contact-activity@example.com")
	other := seedSession(t, a, "contact-activity-other@example.com")
	org := itoa(orgFromList(t, a, owner, `{"name":"Activity","fiscal_year_end":"12-31"}`))
	contact := itoa(int64(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+org+`,"role":"customer","name":"Buyer"}`, owner), 201)["id"].(float64)))
	inv := itoa(int64(expectStatus(t, do(t, h, "POST", "/api/v1/invoices", `{"org_id":`+org+`,"contact_id":`+contact+`,"invoice_date":"2026-10-01","due_date":"2026-10-31","lines":[{"description":"Work","quantity":1,"unit_price_cents":10000}]}`, owner), 201)["id"].(float64)))
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+inv+"/post?org_id="+org, "", owner), 200)
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+inv+"/credit-notes", `{"org_id":`+org+`,"credit_date":"2026-10-15","reason":"Adjustment","amount_cents":2000}`, owner), 201)
	supplier := itoa(int64(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+org+`,"role":"supplier","name":"Seller"}`, owner), 201)["id"].(float64)))
	expense := itoa(int64(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+org+`,"code":"6100","name":"Cost","type":"expense"}`, owner), 201)["id"].(float64)))
	bill := itoa(int64(expectStatus(t, do(t, h, "POST", "/api/v1/bills", `{"org_id":`+org+`,"contact_id":`+supplier+`,"supplier_reference":"B1","bill_date":"2026-10-01","due_date":"2026-10-31","lines":[{"description":"Cost","account_id":`+expense+`,"quantity":1,"unit_price_cents":4000}]}`, owner), 201)["id"].(float64)))
	expectStatus(t, do(t, h, "POST", "/api/v1/bills/"+bill+"/post?org_id="+org, "", owner), 200)
	bank := itoa(int64(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+org+`,"code":"1020","name":"Bank","type":"asset"}`, owner), 201)["id"].(float64)))
	sales := itoa(int64(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+org+`,"code":"4100","name":"Sales","type":"revenue"}`, owner), 201)["id"].(float64)))
	expectStatus(t, do(t, h, "POST", "/api/v1/sales-receipts", `{"org_id":`+org+`,"contact_id":`+contact+`,"receipt_date":"2026-10-01","payment_mode":"cash","deposit_account_id":`+bank+`,"idempotency_key":"activity-receipt","lines":[{"description":"Walk-in","account_id":`+sales+`,"quantity":1,"unit_price_cents":5000}]}`, owner), 201)
	path := "/api/v1/reports/contact-activity?org_id=" + org + "&from=2026-10-01&to=2026-10-31"
	expectStatus(t, do(t, h, "GET", path, "", other), 403)
	rows := expectStatus(t, do(t, h, "GET", path, "", owner), 200)["rows"].([]any)
	if len(rows) != 2 || rows[0].(map[string]any)["income_cents"] != float64(13000) || rows[1].(map[string]any)["expense_cents"] != float64(4000) {
		t.Fatal(rows)
	}
	expectStatus(t, do(t, h, "GET", "/api/v1/reports/contact-activity?org_id="+org+"&from=bad&to=2026-10-31", "", owner), 400)
}
