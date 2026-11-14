package main

import (
	"context"
	"testing"
)

// TestStockAdjustmentsAndStocktakes covers the manual side of inventory tracking: an adjustment moves
// stock with a reason and is refused when it would go below zero, a stocktake posts only the
// differences from the quantity on hand at its date, and both show up in the inventory reports and the
// list endpoint. Services and untracked items are refused, and a viewer cannot write.
func TestStockAdjustmentsAndStocktakes(t *testing.T) {
	a, _ := testApp(t)
	mailSink(a)
	h := a.routes()
	owner := seedSession(t, a, "stock-owner@example.com")
	org := orgFromList(t, a, owner, `{"name":"Manual stock","fiscal_year_end":"12-31"}`)
	o := itoa(org)
	item := itoa(int64(expectStatus(t, do(t, h, "POST", "/api/v1/items", `{"org_id":`+o+`,"name":"Widget","item_type":"goods","sku":"W2","selling_price_cents":1000,"cost_price_cents":400,"sales_account_id":0,"purchase_account_id":0,"track_inventory":true}`, owner), 201)["id"].(float64)))
	service := itoa(int64(expectStatus(t, do(t, h, "POST", "/api/v1/items", `{"org_id":`+o+`,"name":"Labour","item_type":"service","selling_price_cents":100,"sales_account_id":0}`, owner), 201)["id"].(float64)))

	// Bill brings 10 in at $4.00 so the cost is known (R07: tracked bills are supported).
	supp := itoa(int64(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"supplier","name":"Vendor"}`, owner), 201)["id"].(float64)))
	bill := expectStatus(t, do(t, h, "POST", "/api/v1/bills", `{"org_id":`+o+`,"contact_id":`+supp+`,"supplier_reference":"BR-2","bill_date":"2026-02-01","due_date":"2026-02-01","lines":[{"item_id":`+item+`,"description":"stock","quantity":10,"unit_price_cents":400}]}`, owner), 201)
	expectStatus(t, do(t, h, "POST", "/api/v1/bills/"+itoa(int64(bill["id"].(float64)))+"/post?org_id="+o, "", owner), 200)

	// Adjustment down 3 with a reason.
	adj := expectStatus(t, do(t, h, "POST", "/api/v1/stock/adjustments", `{"org_id":`+o+`,"item_id":`+item+`,"adjustment_date":"2026-02-05","qty_delta":-3,"reason":"Damaged in transit","note":"Two boxes crushed"}`, owner), 201)
	if adj["adjustment_id"] == nil || adj["on_hand"].(float64) != 7 {
		t.Fatalf("adjustment response %v", adj)
	}
	// Refuse going below zero, and refuse services.
	expectStatus(t, do(t, h, "POST", "/api/v1/stock/adjustments", `{"org_id":`+o+`,"item_id":`+item+`,"adjustment_date":"2026-02-05","qty_delta":-99,"reason":"Too much"}`, owner), 409)
	expectStatus(t, do(t, h, "POST", "/api/v1/stock/adjustments", `{"org_id":`+o+`,"item_id":`+service+`,"adjustment_date":"2026-02-05","qty_delta":1,"reason":"No"}`, owner), 422)
	// Invalid input: zero delta, missing reason, bad date.
	expectStatus(t, do(t, h, "POST", "/api/v1/stock/adjustments", `{"org_id":`+o+`,"item_id":`+item+`,"adjustment_date":"2026-02-05","qty_delta":0,"reason":"Zero"}`, owner), 400)
	expectStatus(t, do(t, h, "POST", "/api/v1/stock/adjustments", `{"org_id":`+o+`,"item_id":`+item+`,"adjustment_date":"2026-02-05","qty_delta":1}`, owner), 400)
	expectStatus(t, do(t, h, "POST", "/api/v1/stock/adjustments", `{"org_id":`+o+`,"item_id":`+item+`,"adjustment_date":"05/02/2026","qty_delta":1,"reason":"Bad date"}`, owner), 400)

	// Stocktake counts 5 on hand, so it posts -2 from the 7 the adjustment left.
	take := expectStatus(t, do(t, h, "POST", "/api/v1/stock/stocktakes", `{"org_id":`+o+`,"taken_on":"2026-02-10","note":"Counted shelves","lines":[{"item_id":`+item+`,"counted_qty":5}]}`, owner), 201)
	if take["items_counted"].(float64) != 1 || take["adjusted"].(float64) != 1 {
		t.Fatalf("stocktake response %v", take)
	}
	// A stocktake that matches what is on hand posts nothing.
	same := expectStatus(t, do(t, h, "POST", "/api/v1/stock/stocktakes", `{"org_id":`+o+`,"taken_on":"2026-02-11","lines":[{"item_id":`+item+`,"counted_qty":5}]}`, owner), 201)
	if same["adjusted"].(float64) != 0 {
		t.Fatalf("matching stocktake adjusted %v", same)
	}
	// Duplicate item lines, services and empty counts are refused.
	expectStatus(t, do(t, h, "POST", "/api/v1/stock/stocktakes", `{"org_id":`+o+`,"taken_on":"2026-02-11","lines":[{"item_id":`+item+`,"counted_qty":1},{"item_id":`+item+`,"counted_qty":2}]}`, owner), 400)
	expectStatus(t, do(t, h, "POST", "/api/v1/stock/stocktakes", `{"org_id":`+o+`,"taken_on":"2026-02-11","lines":[{"item_id":`+service+`,"counted_qty":1}]}`, owner), 422)
	expectStatus(t, do(t, h, "POST", "/api/v1/stock/stocktakes", `{"org_id":`+o+`,"taken_on":"2026-02-11","lines":[]}`, owner), 400)

	// Reports agree: 10 in, 3 out on the adjustment, 2 out on the stocktake, 5 on hand.
	got := expectStatus(t, do(t, h, "GET", "/api/v1/reports/inventory-items?org_id="+o+"&as_of=2026-02-28", "", owner), 200)
	row := got["rows"].([]any)[0].(map[string]any)
	if row["qty"].(float64) != 5 || row["value_cents"].(float64) != 2000 {
		t.Fatalf("stock after manual changes %v", row)
	}
	// Invoice can now sell exactly 5 and no more.
	cust := itoa(int64(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"customer","name":"Buyer"}`, owner), 201)["id"].(float64)))
	inv := expectStatus(t, do(t, h, "POST", "/api/v1/invoices", `{"org_id":`+o+`,"contact_id":`+cust+`,"invoice_date":"2026-02-20","due_date":"2026-02-20","lines":[{"item_id":`+item+`,"description":"sale","quantity":5,"unit_price_cents":1000}]}`, owner), 201)
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+itoa(int64(inv["id"].(float64)))+"/post?org_id="+o, "", owner), 200)

	// The list endpoint returns both manual changes, newest first, with item names and reasons.
	list := expectStatus(t, do(t, h, "GET", "/api/v1/stock/adjustments?org_id="+o, "", owner), 200)
	rows := list["adjustments"].([]any)
	if len(rows) != 2 {
		t.Fatalf("want 2 adjustments, got %d", len(rows))
	}
	first := rows[0].(map[string]any)
	if first["reason"].(string) != "Stocktake" || first["qty_delta"].(float64) != -2 || first["item_name"].(string) != "Widget" {
		t.Fatalf("newest adjustment %v", first)
	}
	denied := expectStatus(t, do(t, h, "GET", "/api/v1/stock/adjustments?org_id="+o, "", owner), 200)
	if denied["adjustments"] == nil {
		t.Fatal(denied)
	}

	// A viewer may read but not write; an outsider is refused outright.
	viewer := seedSession(t, a, "stock-viewer@example.com")
	if _, err := a.db.Exec(context.Background(), `UPDATE users SET email_verified_at=now() WHERE email=$1`, "stock-viewer@example.com"); err != nil {
		t.Fatal(err)
	}
	invitation := expectStatus(t, do(t, h, "POST", "/api/v1/members/invite", `{"org_id":`+o+`,"email":"stock-viewer@example.com","role":"viewer"}`, owner), 201)
	expectStatus(t, do(t, h, "GET", "/api/v1/stock/adjustments?org_id="+o, "", viewer), 403)
	expectStatus(t, do(t, h, "POST", "/api/v1/invitations/"+itoa(int64(invitation["invitation_id"].(float64)))+"/accept", "", viewer), 200)
	expectStatus(t, do(t, h, "GET", "/api/v1/stock/adjustments?org_id="+o, "", viewer), 200)
	expectStatus(t, do(t, h, "POST", "/api/v1/stock/adjustments", `{"org_id":`+o+`,"item_id":`+item+`,"adjustment_date":"2026-02-12","qty_delta":1,"reason":"No"}`, viewer), 403)
	other := seedSession(t, a, "stock-other@example.com")
	expectStatus(t, do(t, h, "GET", "/api/v1/stock/adjustments?org_id="+o, "", other), 403)
	expectStatus(t, do(t, h, "POST", "/api/v1/stock/adjustments", `{"org_id":`+o+`,"item_id":`+item+`,"adjustment_date":"2026-02-12","qty_delta":1,"reason":"No"}`, other), 403)
}
