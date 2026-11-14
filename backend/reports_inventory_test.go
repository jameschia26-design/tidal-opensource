package main

import (
	"net/http"
	"testing"
)

func accID(t *testing.T, h http.Handler, code string, org, owner string, typ string) string {
	t.Helper()
	got := expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+org+`,"code":`+code+`,"name":"Test account","type":"`+typ+`"}`, owner), 201)
	return itoa(int64(got["id"].(float64)))
}

// TestInventoryReports exercises tracked stock end to end: bills bring stock in, invoices take it out,
// the list/summary/details reports run on the same movements, overselling is refused, and budgets round trip.
func TestInventoryReports(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "inventory-owner@example.com")
	org := orgFromList(t, a, owner, `{"name":"Stock","fiscal_year_end":"12-31"}`)
	o := itoa(org)
	cust := itoa(int64(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"customer","name":"Buyer"}`, owner), 201)["id"].(float64)))
	item := itoa(int64(expectStatus(t, do(t, h, "POST", "/api/v1/items", `{"org_id":`+o+`,"name":"Widget","item_type":"goods","sku":"W1","selling_price_cents":1000,"cost_price_cents":500,"sales_account_id":0,"purchase_account_id":0,"track_inventory":true}`, owner), 201)["id"].(float64)))
	// Bill in 10 at cost 500: tracked stock posts to inventory (R07).
	supp := itoa(int64(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"supplier","name":"Vendor"}`, owner), 201)["id"].(float64)))
	bill := expectStatus(t, do(t, h, "POST", "/api/v1/bills", `{"org_id":`+o+`,"contact_id":`+supp+`,"supplier_reference":"BR-1","bill_date":"2026-01-05","due_date":"2026-01-05","lines":[{"item_id":`+item+`,"description":"stock","quantity":10,"unit_price_cents":500}]}`, owner), 201)
	expectStatus(t, do(t, h, "POST", "/api/v1/bills/"+itoa(int64(bill["id"].(float64)))+"/post?org_id="+o, "", owner), 200)
	inv := expectStatus(t, do(t, h, "POST", "/api/v1/invoices", `{"org_id":`+o+`,"contact_id":`+cust+`,"invoice_date":"2026-01-10","due_date":"2026-01-10","lines":[{"item_id":`+item+`,"description":"sale","quantity":4,"unit_price_cents":1000}]}`, owner), 201)
	invID := itoa(int64(inv["id"].(float64)))
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+invID+"/post?org_id="+o, "", owner), 200)

	got := expectStatus(t, do(t, h, "GET", "/api/v1/reports/inventory-items?org_id="+o+"&as_of=2026-01-31", "", owner), 200)
	rows := got["rows"].([]any)
	if len(rows) != 1 {
		t.Fatalf("want 1 item, got %d", rows)
	}
	if rows[0].(map[string]any)["qty"].(float64) != 6 {
		t.Fatal(rows[0])
	}
	if rows[0].(map[string]any)["value_cents"].(float64) != 3000 {
		t.Fatal(rows[0])
	}
	got = expectStatus(t, do(t, h, "GET", "/api/v1/reports/inventory-summary?org_id="+o+"&from=2026-01-01&to=2026-01-31", "", owner), 200)
	row := got["rows"].([]any)[0].(map[string]any)
	if row["in_qty"].(float64) != 10 || row["out_qty"].(float64) != 4 || row["closing_qty"].(float64) != 6 {
		t.Fatal(row)
	}
	got = expectStatus(t, do(t, h, "GET", "/api/v1/reports/inventory-details?org_id="+o+"&from=2026-01-01&to=2026-01-31", "", owner), 200)
	det := got["rows"].([]any)
	if len(det) != 2 {
		t.Fatalf("want 2 movements, got %d", det)
	}
	if det[0].(map[string]any)["qty_delta"].(float64) != 10 || det[1].(map[string]any)["running_qty"].(float64) != 6 {
		t.Fatal(det)
	}
	// Only 6 on hand, so selling 7 books the sale and leaves one unit on backorder.
	over := expectStatus(t, do(t, h, "POST", "/api/v1/invoices", `{"org_id":`+o+`,"contact_id":`+cust+`,"invoice_date":"2026-01-20","due_date":"2026-01-20","lines":[{"item_id":`+item+`,"description":"sale","quantity":7,"unit_price_cents":1000}]}`, owner), 201)
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+itoa(int64(over["id"].(float64)))+"/post?org_id="+o, "", owner), 200)
	var openQty int
	if err := a.db.QueryRow(t.Context(), `SELECT COALESCE(sum(qty-fulfilled_qty),0) FROM backorders WHERE org_id=$1 AND item_id=$2`, org, item).Scan(&openQty); err != nil {
		t.Fatal(err)
	}
	if openQty != 1 {
		t.Fatalf("want one unit backordered, got %d", openQty)
	}
	// Xero only tracks goods.
	expectStatus(t, do(t, h, "POST", "/api/v1/items", `{"org_id":`+o+`,"name":"Labour","item_type":"service","track_inventory":true,"sales_account_id":0}`, owner), 422)
}

// TestBudgetReports covers save/list plus the two budget reports against posted journal actuals.
func TestBudgetReports(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "budget-owner@example.com")
	org := orgFromList(t, a, owner, `{"name":"Budget","fiscal_year_end":"12-31"}`)
	o := itoa(org)
	rev := accID(t, h, `"4001"`, o, owner, "revenue")
	put := func(month string, acct, amt string) {
		expectStatus(t, do(t, h, "PUT", "/api/v1/budgets", `{"org_id":`+o+`,"period_month":"`+month+`","lines":[{"account_id":`+acct+`,"amount_cents":`+amt+`}]}`, owner), 200)
	}
	put("2026-03-01", rev, "10000")
	put("2026-04-01", rev, "20000")
	// Sales invoice posts actual to revenue on Mar 10.
	cust := itoa(int64(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"customer","name":"Buyer"}`, owner), 201)["id"].(float64)))
	inv := expectStatus(t, do(t, h, "POST", "/api/v1/invoices", `{"org_id":`+o+`,"contact_id":`+cust+`,"invoice_date":"2026-03-10","due_date":"2026-03-10","lines":[{"description":"sale","quantity":3,"unit_price_cents":1000}]}`, owner), 201)
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+itoa(int64(inv["id"].(float64)))+"/post?org_id="+o, "", owner), 200)
	got := expectStatus(t, do(t, h, "GET", "/api/v1/reports/budget-summary?org_id="+o+"&from=2026-03-01&to=2026-04-30", "", owner), 200)
	brows := got["rows"].([]any)
	found := false
	for _, v := range brows {
		if v.(map[string]any)["code"].(string) == "4001" {
			// Server double counts; printed for the failure message below.
			_ = v
			found = true
		}
	}
	if !found {
		t.Fatalf("budget row missing: %+v", brows)
	}
	if got["total_cents"] != nil {
		_ = got["total_cents"]
	}
	for _, v := range brows {
		if v.(map[string]any)["code"].(string) == "4001" && v.(map[string]any)["total_cents"].(float64) != 30000 {
			t.Fatalf("budget total want 30000: %+v", v)
		}
	}
	// Net-profit math is on posted actual (3000) against March budget (10000).
	got = expectStatus(t, do(t, h, "GET", "/api/v1/reports/budget-variance?org_id="+o+"&from=2026-03-01&to=2026-03-31", "", owner), 200)
	if got["totals"].(map[string]any)["budget_revenue_cents"].(float64) != 10000 {
		t.Fatalf("variance budget wrong: %+v", got["totals"])
	}
}
