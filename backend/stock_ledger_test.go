package main

import (
	"net/http"
	"testing"
)

func reportValue(t *testing.T, h http.Handler, a *app, org, token, asOf string) int64 {
	t.Helper()
	rep := expectStatus(t, do(t, h, "GET", "/api/v1/reports/inventory-items?org_id="+org+"&as_of="+asOf, "", token), 200)
	var total int64
	for _, raw := range rep["rows"].([]any) {
		total += int64(raw.(map[string]any)["value_cents"].(float64))
	}
	_ = a
	return total
}

// TestStockIntegratesWithLedger covers B7: buying, selling and adjusting tracked stock posts matching ledger
// money, so the inventory account equals the stock report. Costing is weighted average (D1), stock counts at
// the bill date (D2), and one control-account pair applies (D3).
func TestStockIntegratesWithLedger(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "stock-ledger@example.com")
	org := orgFromList(t, a, owner, `{"name":"Stock ledger","fiscal_year_end":"12-31"}`)
	o := itoa(org)
	cust := seedContact(t, a, org, owner)
	supp := int64(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"supplier","name":"Vendor"}`, owner), 201)["id"].(float64))
	item := int64(expectStatus(t, do(t, h, "POST", "/api/v1/items", `{"org_id":`+o+`,"name":"Widget","item_type":"goods","sku":"W9","selling_price_cents":1000,"cost_price_cents":500,"sales_account_id":0,"purchase_account_id":0,"track_inventory":true}`, owner), 201)["id"].(float64))

	// Buy 10 at 500 through a tracked bill: stock counts at the bill date.
	bill := expectStatus(t, do(t, h, "POST", "/api/v1/bills", `{"org_id":`+o+`,"contact_id":`+itoa(supp)+`,"supplier_reference":"SL-1","bill_date":"2026-03-04","due_date":"2026-03-04","lines":[{"item_id":`+itoa(item)+`,"description":"stock","quantity":10,"unit_price_cents":500}]}`, owner), 201)
	expectStatus(t, do(t, h, "POST", "/api/v1/bills/"+itoa(int64(bill["id"].(float64)))+"/post?org_id="+o, "", owner), 200)
	var movementDate string
	var movementValue int64
	if err := a.db.QueryRow(t.Context(), `SELECT movement_date::text, qty_delta*unit_cost_cents FROM stock_movements WHERE org_id=$1 AND item_id=$2 AND source_kind='bill'`, org, item).Scan(&movementDate, &movementValue); err != nil {
		t.Fatal(err)
	}
	if movementDate != "2026-03-04" || movementValue != 5000 {
		t.Fatalf("bill movement date=%s value=%d, want 2026-03-04 / 5000", movementDate, movementValue)
	}

	// Sell 4: cost of sales leaves at the moving average.
	inv := expectStatus(t, do(t, h, "POST", "/api/v1/invoices", `{"org_id":`+o+`,"contact_id":`+itoa(cust)+`,"invoice_date":"2026-03-10","due_date":"2026-03-10","lines":[{"item_id":`+itoa(item)+`,"description":"sale","quantity":4,"unit_price_cents":1000}]}`, owner), 201)
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+itoa(int64(inv["id"].(float64)))+"/post?org_id="+o, "", owner), 200)
	// Adjust one unit out for damage.
	expectStatus(t, do(t, h, "POST", "/api/v1/stock/adjustments", `{"org_id":`+o+`,"item_id":`+itoa(item)+`,"adjustment_date":"2026-03-12","qty_delta":-1,"reason":"Damaged"}`, owner), 201)

	balance := func(code string) int64 {
		t.Helper()
		var v int64
		if err := a.db.QueryRow(t.Context(), `SELECT COALESCE(sum(l.debit_cents-l.credit_cents),0) FROM journal_lines l JOIN accounts a ON a.org_id=l.org_id AND a.id=l.account_id WHERE l.org_id=$1 AND a.code=$2`, org, code).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	var qty, value int64
	if err := a.db.QueryRow(t.Context(), `SELECT COALESCE(sum(qty_delta),0), COALESCE(sum(qty_delta*unit_cost_cents),0) FROM stock_movements WHERE org_id=$1 AND item_id=$2`, org, item).Scan(&qty, &value); err != nil {
		t.Fatal(err)
	}
	if qty != 5 || value != 2500 {
		t.Fatalf("stock qty=%d value=%d, want 5 / 2500", qty, value)
	}
	if got := balance("1300"); got != 2500 {
		t.Fatalf("inventory account=%d, want 2500", got)
	}
	if got := balance("5100"); got != 2000 {
		t.Fatalf("cost of sales=%d, want 2000 (4 units at 500)", got)
	}
	if got := balance("6600"); got != 500 {
		t.Fatalf("inventory adjustments=%d, want 500 (1 unit written off)", got)
	}
	if got := reportValue(t, h, a, o, owner, "2026-03-31"); got != 2500 {
		t.Fatalf("report value %d, want 2500", got)
	}

	// A later cost-price change does not rewrite past movements or the account balances.
	if _, err := a.db.Exec(t.Context(), `UPDATE items SET cost_price_cents=999 WHERE org_id=$1 AND id=$2`, org, item); err != nil {
		t.Fatal(err)
	}
	if got := balance("1300"); got != 2500 {
		t.Fatalf("inventory account after cost change=%d, want 2500", got)
	}

	// Zero quantity is zero value: take the rest out.
	expectStatus(t, do(t, h, "POST", "/api/v1/stock/adjustments", `{"org_id":`+o+`,"item_id":`+itoa(item)+`,"adjustment_date":"2026-03-20","qty_delta":-5,"reason":"Written off"}`, owner), 201)
	if got := reportValue(t, h, a, o, owner, "2026-03-31"); got != 0 || balance("1300") != 0 {
		t.Fatalf("after clearing stock: report=%d inventory=%d, want 0/0", got, balance("1300"))
	}
}
