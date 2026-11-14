package main

import "testing"

func TestSalesByItemReport(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "sales-by-item-owner@example.com")
	outsider := seedSession(t, a, "sales-by-item-other@example.com")
	org := orgFromList(t, a, owner, `{"name":"Sales by item","fiscal_year_end":"12-31"}`)
	o := itoa(org)
	acct := itoa(int64(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+o+`,"code":"4100","name":"Sales","type":"revenue"}`, owner), 201)["id"].(float64)))
	item := itoa(int64(expectStatus(t, do(t, h, "POST", "/api/v1/items", `{"org_id":`+o+`,"name":"Widget","item_type":"goods","sku":"W1","selling_price_cents":1000,"cost_price_cents":500,"sales_account_id":`+acct+`,"purchase_account_id":null}`, owner), 201)["id"].(float64)))
	cust := itoa(int64(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"customer","name":"Buyer"}`, owner), 201)["id"].(float64)))
	inv := expectStatus(t, do(t, h, "POST", "/api/v1/invoices", `{"org_id":`+o+`,"contact_id":`+cust+`,"invoice_date":"2026-10-01","due_date":"2026-10-31","lines":[{"item_id":`+item+`,"description":"Widget","quantity":2,"unit_price_cents":1000}]}`, owner), 201)
	id := itoa(int64(inv["id"].(float64)))
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+id+"/post?org_id="+o, "", owner), 200)
	path := "/api/v1/reports/sales-by-item?org_id=" + o + "&from=2026-10-01&to=2026-10-31"
	got := expectStatus(t, do(t, h, "GET", path, "", owner), 200)
	rows := got["rows"].([]any)
	if len(rows) != 1 || rows[0].(map[string]any)["item"] != "Widget" || rows[0].(map[string]any)["quantity"] != float64(2) || rows[0].(map[string]any)["sales_cents"] != float64(2000) {
		t.Fatal(got)
	}
	expectStatus(t, do(t, h, "GET", path, "", outsider), 403)
	expectStatus(t, do(t, h, "GET", "/api/v1/reports/sales-by-item?org_id="+o+"&from=bad&to=2026-10-31", "", owner), 400)
}
