package main

import "testing"

// TestServiceItemsInvoiceWithoutStock covers B5: only stock-tracked items need stock on hand.
func TestServiceItemsInvoiceWithoutStock(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "service-stock@example.com")
	org := orgFromList(t, a, owner, `{"name":"Service stock","fiscal_year_end":"12-31"}`)
	o := itoa(org)
	cust := seedContact(t, a, org, owner)
	item := func(body string) int64 {
		t.Helper()
		return int64(expectStatus(t, do(t, h, "POST", "/api/v1/items", body, owner), 201)["id"].(float64))
	}
	service := item(`{"org_id":` + o + `,"name":"Consulting","item_type":"service","selling_price_cents":10000,"sales_account_id":0}`)
	untracked := item(`{"org_id":` + o + `,"name":"Manual","item_type":"goods","selling_price_cents":1000,"cost_price_cents":500,"sales_account_id":0,"purchase_account_id":0}`)
	tracked := item(`{"org_id":` + o + `,"name":"Widget","item_type":"goods","selling_price_cents":1000,"cost_price_cents":500,"sales_account_id":0,"purchase_account_id":0,"track_inventory":true}`)

	invoice := func(itemID int64) int64 {
		t.Helper()
		m := expectStatus(t, do(t, h, "POST", "/api/v1/invoices", `{"org_id":`+o+`,"contact_id":`+itoa(cust)+`,"invoice_date":"2026-08-01","due_date":"2026-08-01","lines":[{"item_id":`+itoa(itemID)+`,"description":"Line","quantity":1,"unit_price_cents":10000}]}`, owner), 201)
		return int64(m["id"].(float64))
	}
	for _, tc := range []struct {
		name string
		id   int64
	}{{"service", service}, {"untracked goods", untracked}} {
		id := invoice(tc.id)
		expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+itoa(id)+"/post?org_id="+o, "", owner), 200)
		var moves int
		if err := a.db.QueryRow(t.Context(), `SELECT count(*) FROM stock_movements WHERE org_id=$1 AND item_id=$2`, org, tc.id).Scan(&moves); err != nil {
			t.Fatal(err)
		}
		if moves != 0 {
			t.Fatalf("%s: want no stock movement, got %d", tc.name, moves)
		}
	}
	// A tracked item with nothing on hand is not refused: the sale books and the shortfall becomes
	// a backorder, the same way Xero keeps selling and tracks what is owed.
	id := invoice(tracked)
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+itoa(id)+"/post?org_id="+o, "", owner), 200)
	var openQty int
	if err := a.db.QueryRow(t.Context(), `SELECT COALESCE(sum(qty - fulfilled_qty),0) FROM backorders WHERE org_id=$1 AND item_id=$2`, org, tracked).Scan(&openQty); err != nil {
		t.Fatal(err)
	}
	if openQty != 1 {
		t.Fatalf("want one widget backordered, got %d", openQty)
	}
}
